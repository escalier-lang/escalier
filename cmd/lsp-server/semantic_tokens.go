package main

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// semanticTokenTypes lists the token types the server reports, in legend order. A
// token's type is its index in this list.
var semanticTokenTypes = []string{
	string(protocol.SemanticTokenTypeVariable),
	string(protocol.SemanticTokenTypeParameter),
	string(protocol.SemanticTokenTypeMethod),
}

const (
	tokenTypeVariable = iota
	tokenTypeParameter
	tokenTypeMethod
)

// semanticTokenModifiers lists the token modifiers the server reports, in legend order.
// Modifier i sets bit 1<<i of a token's modifier bits. `mutable` is not one of the
// modifiers LSP predefines. rust-analyzer reports the same name for the same purpose, and
// an editor styles it through its own configuration.
var semanticTokenModifiers = []string{"mutable"}

const tokenModifierMutable = 1 << 0

// semanticTokensOptions returns the semantic tokens capability the server advertises.
func semanticTokensOptions() *protocol.SemanticTokensOptions {
	return &protocol.SemanticTokensOptions{
		Legend: protocol.SemanticTokensLegend{
			TokenTypes:     semanticTokenTypes,
			TokenModifiers: semanticTokenModifiers,
		},
		Full: true,
	}
}

// semanticToken is one token of a file before encoding. span covers the token's text
// on a single line.
type semanticToken struct {
	span      ast.Span
	tokenType int
	modifiers int
}

func (s *Server) textDocumentSemanticTokensFull(context *glsp.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	uri := params.TextDocument.URI

	// validateBinScript replaces a script's entries in the check output under the write
	// lock, so the file's AST, source, and Info are read together under the read lock.
	s.mu.RLock()
	s.waitForFreshCheck(uri)
	sourceID := s.sourceIDForURI(uri)
	var script *ast.Script
	var module *ast.Module
	var src *ast.Source
	var info *solver.Info
	if co := s.checkOutput; co != nil {
		script = co.Scripts[sourceID]
		module = co.Module
		src = co.SourceByID(sourceID)
		info = co.InfoFor(sourceID)
	}
	s.mu.RUnlock()

	if src == nil || info == nil {
		return &protocol.SemanticTokens{Data: []protocol.UInteger{}}, nil
	}

	s.solverMu.RLock()
	defer s.solverMu.RUnlock()

	visitor := &mutableTokenVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		info:           info,
		src:            src,
		params:         set.NewSet[ast.Node](),
	}
	if script != nil {
		script.Accept(visitor)
	} else if module != nil {
		walkModuleFile(visitor, module, sourceID)
	}
	return &protocol.SemanticTokens{Data: encodeSemanticTokens(src.LineMap(), visitor.tokens)}, nil
}

// encodeSemanticTokens returns tokens in LSP's wire form, ordered by position. It sorts
// tokens in place. lineMap maps the file the tokens' spans index into. Each token becomes
// five integers:
//
//   - its line, relative to the previous token's line
//   - its start column, relative to the previous token's start column when both are on
//     one line, and counted from the start of the line otherwise
//   - its length
//   - its type
//   - its modifier bits
//
// Columns and lengths count UTF-16 code units.
func encodeSemanticTokens(lineMap *ast.LineMap, tokens []semanticToken) []protocol.UInteger {
	slices.SortFunc(tokens, func(a, b semanticToken) int {
		return a.span.Start.Offset - b.span.Start.Offset
	})
	data := make([]protocol.UInteger, 0, 5*len(tokens))
	prevLine, prevColumn := 1, 1
	for _, token := range tokens {
		line, column := lineMap.Position(token.span.Start.Offset, ast.UTF16Columns)
		_, endColumn := lineMap.Position(token.span.End.Offset, ast.UTF16Columns)
		deltaColumn := column - 1
		if line == prevLine {
			deltaColumn = column - prevColumn
		}
		data = append(data,
			protocol.UInteger(line-prevLine),
			protocol.UInteger(deltaColumn),
			protocol.UInteger(endColumn-column),
			protocol.UInteger(token.tokenType),
			protocol.UInteger(token.modifiers),
		)
		prevLine, prevColumn = line, column
	}
	return data
}

// mutableTokenVisitor collects a token for each name in one file that binds or reads
// mutable data, and for each name of a method that takes `&mut self`. A name binds
// mutable data when its binding is mutable, as in `val mut p`, or when its value is a
// mutable borrow, as in `val r: &mut Point`. A method name is marked where a class or an
// object type declares it and where a call reads it, as `push` in `xs.push(1)`.
type mutableTokenVisitor struct {
	ast.DefaultVisitor
	info *solver.Info
	src  *ast.Source
	// params holds each leaf of a parameter pattern the walk has entered, an
	// *ast.IdentPat or an *ast.ObjShorthandPat.
	params set.Set[ast.Node]
	tokens []semanticToken
}

func (v *mutableTokenVisitor) EnterDecl(d ast.Decl) bool {
	if fn, ok := d.(*ast.FuncDecl); ok {
		v.addParams(fn.Params)
	}
	return true
}

func (v *mutableTokenVisitor) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		v.addParams(e.Params)
	case *ast.IdentExpr:
		v.addRead(e)
	case *ast.MemberExpr:
		if v.readsMutMethod(e) {
			v.add(e.Prop.Span(), tokenTypeMethod)
		}
	}
	return true
}

func (v *mutableTokenVisitor) EnterClassElem(e ast.ClassElem) bool {
	// A constructor's receiver is a parameter pattern of its function, which EnterPat
	// marks, so only these members' receivers are marked here.
	switch e := e.(type) {
	case *ast.MethodElem:
		v.addMethodName(e.Name, e.Receiver)
		v.addReceiver(e.Receiver)
	case *ast.GetterElem:
		v.addReceiver(e.Receiver)
	case *ast.SetterElem:
		v.addReceiver(e.Receiver)
	}
	return true
}

func (v *mutableTokenVisitor) EnterObjTypeAnnElem(e ast.ObjTypeAnnElem) bool {
	if m, ok := e.(*ast.MethodTypeAnn); ok {
		v.addMethodName(m.Name, m.Receiver)
	}
	return true
}

func (v *mutableTokenVisitor) EnterObjExprElem(e ast.ObjExprElem) bool {
	// The walk skips the key of a shorthand property such as `{p}`, since a key is
	// usually a field name. A shorthand key also reads the binding p.
	if prop, ok := e.(*ast.PropertyExpr); ok && prop.Value == nil {
		if ident, ok := prop.ValueExpr().(*ast.IdentExpr); ok {
			v.addRead(ident)
		}
	}
	return true
}

func (v *mutableTokenVisitor) EnterPat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.IdentPat:
		if v.info.BindsMut(p) || v.info.LendsMut(p) {
			v.add(identPatNameSpan(p, v.src), v.leafTokenType(p))
		}
	case *ast.ObjectPat:
		// The walk enters no node for a shorthand leaf such as `{mut x}`, so its parent
		// object pattern reports it.
		for _, elem := range p.Elems {
			leaf, ok := elem.(*ast.ObjShorthandPat)
			if ok && leaf.Key != nil && (leaf.Mutable || v.info.LendsMut(leaf)) {
				v.add(leaf.Key.Span(), v.leafTokenType(leaf))
			}
		}
	}
	return true
}

// addRead records a token for e, a read of a binding, when it reads mutable data.
func (v *mutableTokenVisitor) addRead(e *ast.IdentExpr) {
	_, mut, _ := bindingOf(v.info, e)
	if !mut && !v.info.LendsMut(e) {
		return
	}
	tokenType := tokenTypeVariable
	decl, found := v.info.DeclOf(e)
	switch {
	case found && v.params.Contains(decl):
		tokenType = tokenTypeParameter
	case !found && e.Name == "self":
		// The checker records no declaration for a read of a method's receiver, and
		// `self` with no declaration names that receiver. A local written
		// `val mut self` has a declaration and keeps the variable type.
		tokenType = tokenTypeParameter
	}
	v.add(e.Span(), tokenType)
}

// addMethodName records a token for name, the name a method is declared under, when
// receiver is `&mut self`. It records nothing for a name written as a string or a
// computed key.
func (v *mutableTokenVisitor) addMethodName(name ast.ObjKey, receiver *ast.MethodReceiver) {
	if receiver == nil || receiver.Mode != ast.BorrowReceiver || !receiver.Mut {
		return
	}
	if ident, ok := name.(*ast.IdentExpr); ok {
		v.add(ident.Span(), tokenTypeMethod)
	}
}

// addReceiver records a parameter token for the `self` of receiver when receiver is
// `&mut self` or `mut self`. Inside the member's body, `self` then reads mutable data.
func (v *mutableTokenVisitor) addReceiver(receiver *ast.MethodReceiver) {
	if receiver == nil || !receiver.Mut {
		return
	}
	// A receiver ends with `self`.
	end := receiver.Span_.End.Offset
	v.add(ast.Span{
		Start:    ast.Location{Offset: end - len("self")},
		End:      ast.Location{Offset: end},
		SourceID: receiver.Span_.SourceID,
	}, tokenTypeParameter)
}

// readsMutMethod reports whether e reads a method that takes `&mut self` off its object.
// An overloaded method counts when every arm takes `&mut self`.
func (v *mutableTokenVisitor) readsMutMethod(e *ast.MemberExpr) bool {
	if e.Prop == nil || !v.info.IsMethodRead(e) {
		return false
	}
	obj, ok := v.info.MemberObject(v.info.DisplayTypeOf(e.Object))
	if !ok {
		return false
	}
	for _, elem := range obj.Elems {
		if method, ok := elem.(*soltype.MethodElem); ok && soltype.ObjElemName(method) == e.Prop.Name {
			return solver.NeedsMutReceiver(method)
		}
	}
	return false
}

// addParams records the binding leaves of params. Call it on entering the function that
// declares params, before the walk reaches the function's body.
func (v *mutableTokenVisitor) addParams(params []*ast.Param) {
	for _, param := range params {
		ast.ForEachLeafBinding(param.Pattern, func(_ string, leaf ast.Node) {
			v.params.Add(leaf)
		})
	}
}

// leafTokenType returns the token type of a binding leaf, an *ast.IdentPat or an
// *ast.ObjShorthandPat.
func (v *mutableTokenVisitor) leafTokenType(leaf ast.Node) int {
	if v.params.Contains(leaf) {
		return tokenTypeParameter
	}
	return tokenTypeVariable
}

// add records a mutable token of tokenType over span.
func (v *mutableTokenVisitor) add(span ast.Span, tokenType int) {
	v.tokens = append(v.tokens, semanticToken{span: span, tokenType: tokenType, modifiers: tokenModifierMutable})
}

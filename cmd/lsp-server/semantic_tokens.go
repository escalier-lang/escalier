package main

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
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
	string(protocol.SemanticTokenTypeProperty),
}

const (
	tokenTypeVariable = iota
	tokenTypeParameter
	tokenTypeMethod
	tokenTypeProperty
)

// semanticTokenModifiers lists the token modifiers the server reports, in legend order.
// Modifier i sets bit 1<<i of a token's modifier bits. Neither is one of the modifiers
// LSP predefines, so an editor styles each through its own configuration.
//
//   - `mutable` marks a name whose value can be mutated. rust-analyzer reports the same
//     name for the same purpose.
//   - `reassignable` marks a name a `var` declaration binds, which can be reassigned.
var semanticTokenModifiers = []string{"mutable", "reassignable"}

const (
	tokenModifierMutable = 1 << iota
	tokenModifierReassignable
)

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
		reassignables:  set.NewSet[ast.Node](),
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
// mutable data or a reassignable binding, and for each method, getter, setter, or
// constructor that takes `&mut self`. A name binds mutable data when its binding is
// mutable, as in `val mut p`, or when its value is a mutable borrow, as in
// `val r: &mut Point`. A name binds a reassignable binding when a `var` declaration binds
// it, as in `var n = 0`. A member name is marked where a class or an object type declares
// it, and not where a member expression reads it, which is how rust-analyzer marks a
// method that takes `&mut self`.
type mutableTokenVisitor struct {
	ast.DefaultVisitor
	info *solver.Info
	src  *ast.Source
	// params holds each leaf of a parameter pattern the walk has entered, an
	// *ast.IdentPat or an *ast.ObjShorthandPat.
	params set.Set[ast.Node]
	// reassignables holds each leaf of a `var` declaration's pattern the walk has
	// entered, an *ast.IdentPat or an *ast.ObjShorthandPat.
	reassignables set.Set[ast.Node]
	tokens        []semanticToken
}

func (v *mutableTokenVisitor) EnterDecl(d ast.Decl) bool {
	switch d := d.(type) {
	case *ast.FuncDecl:
		v.addParams(d.Params)
	case *ast.VarDecl:
		// The walk enters a declaration before its pattern and before every read of
		// the names it binds.
		if d.Kind == ast.VarKind {
			ast.ForEachLeafBinding(d.Pattern, func(_ string, leaf ast.Node) {
				v.reassignables.Add(leaf)
			})
		}
	}
	return true
}

func (v *mutableTokenVisitor) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		v.addParams(e.Params)
	case *ast.IdentExpr:
		v.addRead(e)
	}
	return true
}

func (v *mutableTokenVisitor) EnterClassElem(e ast.ClassElem) bool {
	// A constructor's receiver is a parameter pattern of its function, which EnterPat
	// marks, so only these members' receivers are marked here.
	switch e := e.(type) {
	case *ast.ConstructorElem:
		// A constructor takes `&mut self` like a mutating method, and its keyword
		// signals that the body mutates the instance it builds.
		if e.Receiver != nil && e.Receiver.Mode == ast.BorrowReceiver && e.Receiver.Mut {
			if span, ok := constructorKeywordSpan(e, v.src); ok {
				v.add(span, tokenTypeMethod, tokenModifierMutable)
			}
		}
	case *ast.MethodElem:
		v.addMemberName(e.Name, e.Receiver, tokenTypeMethod)
		v.addReceiver(e.Receiver)
	case *ast.GetterElem:
		v.addMemberName(e.Name, e.Receiver, tokenTypeProperty)
		v.addReceiver(e.Receiver)
	case *ast.SetterElem:
		v.addMemberName(e.Name, e.Receiver, tokenTypeProperty)
		v.addReceiver(e.Receiver)
	}
	return true
}

func (v *mutableTokenVisitor) EnterObjTypeAnnElem(e ast.ObjTypeAnnElem) bool {
	switch e := e.(type) {
	case *ast.MethodTypeAnn:
		v.addMemberName(e.Name, e.Receiver, tokenTypeMethod)
	case *ast.GetterTypeAnn:
		v.addMemberName(e.Name, e.Receiver, tokenTypeProperty)
	case *ast.SetterTypeAnn:
		v.addMemberName(e.Name, e.Receiver, tokenTypeProperty)
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
		// Finding the name searches the source text, so it runs only for a marked name.
		if modifiers := v.leafModifiers(p, v.info.BindsMut(p)); modifiers != 0 {
			v.add(identPatNameSpan(p, v.src), v.leafTokenType(p), modifiers)
		}
	case *ast.ObjectPat:
		// The walk enters no node for a shorthand leaf such as `{mut x}`, so its parent
		// object pattern reports it.
		for _, elem := range p.Elems {
			if leaf, ok := elem.(*ast.ObjShorthandPat); ok && leaf.Key != nil {
				v.add(leaf.Key.Span(), v.leafTokenType(leaf), v.leafModifiers(leaf, leaf.Mutable))
			}
		}
	}
	return true
}

// addRead records a token for e, a read of a binding, when it reads mutable data or a
// reassignable binding.
func (v *mutableTokenVisitor) addRead(e *ast.IdentExpr) {
	_, mut, _ := bindingOf(v.info, e)
	decl, found := v.info.DeclOf(e)
	modifiers := 0
	if mut || v.info.LendsMut(e) {
		modifiers |= tokenModifierMutable
	}
	if found && v.isReassignable(decl) {
		modifiers |= tokenModifierReassignable
	}
	tokenType := tokenTypeVariable
	switch {
	case found && v.params.Contains(decl):
		tokenType = tokenTypeParameter
	case !found && e.Name == "self":
		// The checker records no declaration for a read of a method's receiver, and
		// `self` with no declaration names that receiver. A local written
		// `val mut self` has a declaration and keeps the variable type.
		tokenType = tokenTypeParameter
	}
	v.add(e.Span(), tokenType, modifiers)
}

// isReassignable reports whether decl, the node a read resolved to, is bound by a `var`
// declaration. decl is the whole *ast.VarDecl for a module-level binding and the leaf of
// its pattern for a local one.
func (v *mutableTokenVisitor) isReassignable(decl ast.Node) bool {
	if varDecl, ok := decl.(*ast.VarDecl); ok {
		return varDecl.Kind == ast.VarKind
	}
	return v.reassignables.Contains(decl)
}

// leafModifiers returns the modifier bits of leaf, a binding leaf that is an
// *ast.IdentPat or an *ast.ObjShorthandPat. bindsMut says whether its binding is `mut`.
func (v *mutableTokenVisitor) leafModifiers(leaf ast.Node, bindsMut bool) int {
	modifiers := 0
	if bindsMut || v.info.LendsMut(leaf) {
		modifiers |= tokenModifierMutable
	}
	if v.reassignables.Contains(leaf) {
		modifiers |= tokenModifierReassignable
	}
	return modifiers
}

// addMemberName records a token of tokenType for name, the name a method, getter, or
// setter is declared under, when receiver is `&mut self`. It records nothing for a name written as
// a string or a computed key.
func (v *mutableTokenVisitor) addMemberName(name ast.ObjKey, receiver *ast.MethodReceiver, tokenType int) {
	if receiver == nil || receiver.Mode != ast.BorrowReceiver || !receiver.Mut {
		return
	}
	if ident, ok := name.(*ast.IdentExpr); ok {
		v.add(ident.Span(), tokenType, tokenModifierMutable)
	}
}

// constructorKeywordSpan returns the span of the `constructor` keyword that names c in
// src's text, and false when the text holds none. The element's span can start before
// the keyword, at `private`, so the keyword is the first `constructor` followed by `(`.
func constructorKeywordSpan(c *ast.ConstructorElem, src *ast.Source) (ast.Span, bool) {
	const keyword = "constructor"
	span := c.Span()
	start, end := span.Start.Offset, span.End.Offset
	if src == nil || start < 0 || end > len(src.Contents) || start > end {
		return ast.Span{}, false
	}
	text := src.Contents[start:end]
	for i := 0; i+len(keyword) <= len(text); {
		j := strings.Index(text[i:], keyword)
		if j < 0 {
			break
		}
		at := i + j
		if rest := strings.TrimLeft(text[at+len(keyword):], " \t\r\n"); strings.HasPrefix(rest, "(") {
			return ast.Span{
				Start:    ast.Location{Offset: start + at},
				End:      ast.Location{Offset: start + at + len(keyword)},
				SourceID: span.SourceID,
			}, true
		}
		i = at + len(keyword)
	}
	return ast.Span{}, false
}

// addReceiver records a parameter token for the `self` of receiver when receiver is
// `&mut self` or `mut self`.
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
	}, tokenTypeParameter, tokenModifierMutable)
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

// add records a token of tokenType with modifier bits modifiers over span. It records
// nothing when modifiers is zero, since the server reports only marked names.
func (v *mutableTokenVisitor) add(span ast.Span, tokenType int, modifiers int) {
	if modifiers == 0 {
		return
	}
	v.tokens = append(v.tokens, semanticToken{span: span, tokenType: tokenType, modifiers: modifiers})
}

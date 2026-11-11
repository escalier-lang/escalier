package main

import (
	"slices"

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
}

const (
	tokenTypeVariable = iota
	tokenTypeParameter
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

	s.mu.RLock()
	s.waitForFreshCheck(uri)
	co := s.checkOutput
	sourceID := s.sourceIDForURI(uri)
	s.mu.RUnlock()

	empty := &protocol.SemanticTokens{Data: []protocol.UInteger{}}
	if co == nil {
		return empty, nil
	}
	src := co.SourceByID(sourceID)
	info := co.InfoFor(sourceID)
	if src == nil || info == nil {
		return empty, nil
	}

	s.solverMu.RLock()
	defer s.solverMu.RUnlock()

	visitor := &mutableTokenVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		info:           info,
		src:            src,
		params:         set.NewSet[ast.Node](),
	}
	if script, ok := co.Scripts[sourceID]; ok {
		script.Accept(visitor)
	} else if co.Module != nil {
		walkModuleFile(visitor, co.Module, sourceID)
	}
	return &protocol.SemanticTokens{Data: encodeSemanticTokens(src.LineMap(), visitor.tokens)}, nil
}

// encodeSemanticTokens returns tokens in LSP's wire form. Each token becomes five
// integers: its line relative to the previous token's line, its start column relative
// to the previous token's start column when both sit on one line and absolute
// otherwise, its length, its type, and its modifier bits. Columns and lengths count
// UTF-16 code units. lineMap maps the file the tokens' spans index into. The result
// lists the tokens in order of position whatever order tokens holds them in.
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
// mutable data. A name binds mutable data when its binding is mutable, as in
// `val mut p`, or when its value is a mutable borrow, as in `val r: &mut Point`.
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
		_, mut, _ := bindingOf(v.info, e)
		if !mut && !v.info.LendsMut(e) {
			break
		}
		tokenType := tokenTypeVariable
		if decl, found := v.info.DeclOf(e); found && v.params.Contains(decl) {
			tokenType = tokenTypeParameter
		}
		v.add(e.Span(), tokenType)
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
			if leaf, ok := elem.(*ast.ObjShorthandPat); ok && leaf.Key != nil && leaf.Mutable {
				v.add(leaf.Key.Span(), v.leafTokenType(leaf))
			}
		}
	}
	return true
}

// addParams records the binding leaves of params. The walk enters a function's
// parameters before its body, so every read of a parameter comes after this call.
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

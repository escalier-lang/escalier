package parser

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/tidwall/btree"
)

// deriveNamespaceFromPath derives a namespace name from a file path
// Examples:
//   - "main.esc" -> ""
//   - "foo/math.esc" -> "foo"
//   - "bar/string.esc" -> "bar"
//   - "core/utils/helpers.esc" -> "core.utils"
func deriveNamespaceFromPath(path string) string {
	// remove "lib/" prefix if it exists
	path = strings.TrimPrefix(path, "lib/")

	// Get the directory part of the path
	dir := filepath.Dir(path)

	// If it's the current directory ".", return empty namespace
	if dir == "." || dir == "" {
		return ""
	}

	// Replace path separators with dots
	namespace := strings.ReplaceAll(dir, "/", ".")
	namespace = strings.ReplaceAll(namespace, "\\", ".") // Handle Windows paths

	return namespace
}

type Parser struct {
	ctx      context.Context
	lexer    *Lexer
	errors   []*Error
	exprMode Stack[ExprMode]
}

type ExprMode int

const (
	MultiLineExpr = iota
	SingleLineExpr
)

func NewParser(ctx context.Context, source *ast.Source) *Parser {
	return &Parser{
		ctx:      ctx,
		lexer:    NewLexer(source),
		errors:   []*Error{},
		exprMode: Stack[ExprMode]{SingleLineExpr},
	}
}

func (p *Parser) saveState() *Parser {
	return &Parser{
		ctx:      p.ctx,
		lexer:    p.lexer.saveState(),
		errors:   append([]*Error{}, p.errors...),
		exprMode: p.exprMode,
	}
}

func (p *Parser) restoreState(saved *Parser) {
	p.ctx = saved.ctx
	p.lexer.restoreState(saved.lexer)
	p.errors = saved.errors
	p.exprMode = saved.exprMode
}

// script = stmt* <eof>
func (p *Parser) ParseScript() (*ast.Script, []*Error) {
	stmts, _ := p.stmts(EndOfFile)
	return &ast.Script{Stmts: *stmts}, p.errors
}

func (p *Parser) decls() []ast.Decl {
	decls := []ast.Decl{}

	token := p.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case EndOfFile:
			p.lexer.consume()
			return decls
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			decl := p.decl()
			if decl != nil {
				decls = append(decls, decl)
			} else {
				nextToken := p.lexer.peek()
				// If no tokens have been consumed then we've encountered
				// something we don't know how to parse.  We consume the token
				// and then try to parse the another statement.
				if token.Span.End.Line == nextToken.Span.End.Line &&
					token.Span.End.Column == nextToken.Span.End.Column {
					p.reportError(token.Span, "Unexpected token")
					p.lexer.consume()
				}
			}
			token = p.lexer.peek()
		}
	}
}

func ParseLibFiles(ctx context.Context, sources []*ast.Source) (*ast.Module, []*Error) {
	var namespaces btree.Map[string, *ast.Namespace]
	mod := &ast.Module{
		Namespaces: namespaces,
	}

	allErrors := []*Error{}

	for _, source := range sources {
		if source == nil {
			continue
		}

		// Determine the namespace based on the source path
		nsName := deriveNamespaceFromPath(source.Path)

		if _, exists := mod.Namespaces.Get(nsName); !exists {
			mod.Namespaces.Set(nsName, &ast.Namespace{
				Decls: []ast.Decl{},
			})
		}

		parser := NewParser(ctx, source)
		decls := parser.decls()

		ns, _ := mod.Namespaces.Get(nsName)
		ns.Decls = append(ns.Decls, decls...)
		// allDecls = append(allDecls, decls...)
		allErrors = append(allErrors, parser.errors...)
	}

	return mod, allErrors
}

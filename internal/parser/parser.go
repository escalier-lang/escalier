package parser

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
)

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

// module = decl* <eof>
func (p *Parser) ParseModule() (*ast.Module, []*Error) {
	decls := p.decls()
	return &ast.Module{Decls: decls}, p.errors
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
	allDecls := []ast.Decl{}
	allErrors := []*Error{}

	for _, source := range sources {
		if source == nil {
			continue
		}
		if source.ID == 0 {
			panic("Source ID must be set")
		}
		parser := NewParser(ctx, source)
		decls := parser.decls()
		allDecls = append(allDecls, decls...)
		allErrors = append(allErrors, parser.errors...)
	}

	return &ast.Module{Decls: allDecls}, allErrors
}

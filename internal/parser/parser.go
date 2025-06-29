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

func NewParser(ctx context.Context, source *Source) *Parser {
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

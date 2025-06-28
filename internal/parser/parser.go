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

func NewParser(ctx context.Context, source Source) *Parser {
	return &Parser{
		ctx:      ctx,
		lexer:    NewLexer(source),
		errors:   []*Error{},
		exprMode: Stack[ExprMode]{SingleLineExpr},
	}
}

// script = stmt* <eof>
func (p *Parser) ParseScript() (*ast.Script, []*Error) {
	stmts, _ := p.stmts(EndOfFile)
	return &ast.Script{Stmts: *stmts}, p.errors
}

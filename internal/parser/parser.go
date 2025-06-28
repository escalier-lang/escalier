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

func (p *Parser) ParseScript() (*ast.Script, []*Error) {
	stmts := []ast.Stmt{}

	token := p.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case EndOfFile:
			return &ast.Script{Stmts: stmts}, p.errors
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt := p.stmt()
			if stmt != nil {
				stmts = append(stmts, stmt)
			}
			token = p.lexer.peek()
		}
	}
}

package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// block = '{' stmt* '}'
func (p *Parser) block() ast.Block {
	stmts := []ast.Stmt{}
	var start ast.Location

	token := p.lexer.next()
	if token.Type != OpenBrace {
		p.errors = append(p.errors, NewError(token.Span, "Expected an opening brace"))
		return ast.Block{Stmts: stmts, Span: token.Span}
	} else {
		start = token.Span.Start
	}

	token = p.lexer.peek()
	for {
		// nolint: exhaustive
		switch token.Type {
		case CloseBrace:
			p.lexer.consume()
			return ast.Block{Stmts: stmts, Span: ast.Span{Start: start, End: token.Span.End}}
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

// stmt = decl | ('return' expr?) | expr
func (p *Parser) stmt() ast.Stmt {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Type, Declare, Export:
		decl := p.decl()
		if decl == nil {
			return nil
		}
		stmt := ast.NewDeclStmt(decl, decl.Span())
		return stmt
	case Return:
		p.lexer.consume()
		expr := p.nonDelimitedExpr()
		if expr == nil {
			return ast.NewReturnStmt(nil, token.Span)
		}
		return ast.NewReturnStmt(
			expr, ast.MergeSpans(token.Span, expr.Span()),
		)
	default:
		expr := p.nonDelimitedExpr()
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := p.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			p.lexer.consume()
		}
		if expr == nil {
			return nil
		}
		return ast.NewExprStmt(expr, expr.Span())
	}
}

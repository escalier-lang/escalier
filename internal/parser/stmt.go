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
		p.reportError(token.Span, "Expected an opening brace")
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
	p.exprMode.Push(SingleLineExpr)

	var stmt ast.Stmt

	// nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Type, Declare, Export:
		decl := p.decl()
		if decl == nil {
			return nil
		}
		stmt = ast.NewDeclStmt(decl, decl.Span())
	case Return:
		p.lexer.consume()
		expr := p.exprInternal()
		if expr == nil {
			stmt = ast.NewReturnStmt(nil, token.Span)
		} else {
			stmt = ast.NewReturnStmt(
				expr, ast.MergeSpans(token.Span, expr.Span()),
			)
		}
	default:
		expr := p.exprInternal()
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := p.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			p.lexer.consume()
		}
		if expr == nil {
			stmt = nil
		} else {
			stmt = ast.NewExprStmt(expr, expr.Span())
		}
	}

	p.exprMode.Pop()
	return stmt
}

package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parseBlock() ast.Block {
	stmts := []ast.Stmt{}
	var start ast.Location

	token := p.lexer.next()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected an opening brace")
		// TODO: include Span
		return ast.Block{Stmts: stmts}
	} else {
		start = token.Span.Start
	}

	token = p.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case CloseBrace:
			p.lexer.consume()
			return ast.Block{Stmts: stmts, Span: ast.Span{Start: start, End: token.Span.End}}
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt := p.parseStmt()
			stmts = append(stmts, stmt)
			token = p.lexer.peek()
		}
	}
}

func (p *Parser) parseStmt() ast.Stmt {
	token := p.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Declare, Export:
		decl := p.parseDecl()
		if decl == nil {
			return nil
		}
		return ast.NewDeclStmt(decl, decl.Span())
	case Return:
		p.lexer.consume()
		expr := p.parseNonDelimitedExpr()
		if expr == nil {
			return ast.NewReturnStmt(nil, token.Span)
		}
		return ast.NewReturnStmt(optional.Some(expr), ast.Span{Start: token.Span.Start, End: expr.Span().End})
	default:
		expr := p.parseNonDelimitedExpr()
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := p.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			p.lexer.consume()
		}
		return ast.NewExprStmt(expr, expr.Span())
	}
}

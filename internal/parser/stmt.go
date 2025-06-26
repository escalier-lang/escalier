package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// block = '{' stmt* '}'
// TODO: return (optional.Option[ast.Block], []*Error)
func (p *Parser) block() (ast.Block, []*Error) {
	stmts := []ast.Stmt{}
	errors := []*Error{}
	var start ast.Location

	token := p.lexer.next()
	if token.Type != OpenBrace {
		return ast.Block{Stmts: stmts, Span: token.Span}, []*Error{NewError(token.Span, "Expected an opening brace")}
	} else {
		start = token.Span.Start
	}

	token = p.lexer.peek()
	for {
		// nolint: exhaustive
		switch token.Type {
		case CloseBrace:
			p.lexer.consume()
			return ast.Block{Stmts: stmts, Span: ast.Span{Start: start, End: token.Span.End}}, errors
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt, stmtErrors := p.stmt()
			errors = append(errors, stmtErrors...)
			if stmt != nil {
				stmts = append(stmts, stmt)
			}
			token = p.lexer.peek()
		}
	}
}

// stmt = decl | ('return' expr?) | expr
func (p *Parser) stmt() (ast.Stmt, []*Error) {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Type, Declare, Export:
		decl, declErrors := p.decl()
		if decl == nil {
			return nil, declErrors
		}
		stmt := ast.NewDeclStmt(decl, decl.Span())
		return stmt, declErrors
	case Return:
		p.lexer.consume()
		expr, exprErrors := p.nonDelimitedExpr()
		if expr == nil {
			return ast.NewReturnStmt(nil, token.Span), exprErrors
		}
		return ast.NewReturnStmt(
			expr, ast.MergeSpans(token.Span, expr.Span()),
		), exprErrors
	default:
		expr, exprErrors := p.nonDelimitedExpr()
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := p.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			p.lexer.consume()
		}
		if expr == nil {
			return nil, exprErrors
		}
		return ast.NewExprStmt(expr, expr.Span()), exprErrors
	}
}

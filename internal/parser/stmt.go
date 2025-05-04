package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
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
			stmt.IfSome(func(stmt ast.Stmt) {
				stmts = append(stmts, stmt)
			})
			token = p.lexer.peek()
		}
	}
}

// stmt = decl | ('return' expr?) | expr
func (p *Parser) stmt() (optional.Option[ast.Stmt], []*Error) {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Type, Declare, Export:
		decl, declErrors := p.decl()
		stmt := optional.Map(decl, func(d ast.Decl) ast.Stmt {
			return ast.NewDeclStmt(d, d.Span())
		})
		return stmt, declErrors
	case Return:
		p.lexer.consume()
		expr, exprErrors := p.nonDelimitedExpr()
		stmt := optional.Map(expr, func(expr ast.Expr) ast.Stmt {
			return ast.NewReturnStmt(
				optional.Some(expr), ast.MergeSpans(token.Span, expr.Span()),
			)
		}).Or(optional.Some[ast.Stmt](ast.NewReturnStmt(nil, token.Span)))
		return stmt, exprErrors
	default:
		expr, exprErrors := p.nonDelimitedExpr()
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := p.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			p.lexer.consume()
		}
		stmt := optional.Map(expr, func(expr ast.Expr) ast.Stmt {
			return ast.NewExprStmt(expr, expr.Span())
		})
		return stmt, exprErrors
	}
}

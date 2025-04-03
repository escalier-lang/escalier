package parser

import "github.com/escalier-lang/escalier/internal/ast"

func (parser *Parser) parseBlock() ast.Block {
	stmts := []ast.Stmt{}
	var start ast.Location

	token := parser.lexer.next()
	if token.Type != OpenBrace {
		parser.reportError(token.Span, "Expected an opening brace")
		// TODO: include Span
		return ast.Block{Stmts: stmts}
	} else {
		start = token.Span.Start
	}

	token = parser.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case CloseBrace:
			parser.lexer.consume()
			return ast.Block{Stmts: stmts, Span: ast.Span{Start: start, End: token.Span.End}}
		default:
			stmt := parser.parseStmt()
			stmts = append(stmts, stmt)
			token = parser.lexer.peek()
		}
	}
}

func (parser *Parser) parseStmt() ast.Stmt {
	token := parser.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case Fn, Var, Val, Declare, Export:
		decl := parser.parseDecl()
		if decl == nil {
			return nil
		}
		return ast.NewDeclStmt(decl, decl.Span())
	case Return:
		parser.lexer.consume()
		expr := parser.ParseExprWithMarker(MarkerExpr)
		return ast.NewReturnStmt(expr, ast.Span{Start: token.Span.Start, End: expr.Span().End})
	default:
		expr := parser.ParseExprWithMarker(MarkerExpr)
		// If no tokens have been consumed then we've encountered something we
		// don't know how to parse.
		nextToken := parser.lexer.peek()
		if token.Span.End.Line == nextToken.Span.End.Line &&
			token.Span.End.Column == nextToken.Span.End.Column {
			parser.lexer.consume()
		}
		return ast.NewExprStmt(expr, expr.Span())
	}
}

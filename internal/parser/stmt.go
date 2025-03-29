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
		parser.markers.Push(MarkerExpr)
		expr := parser.ParseExpr()
		parser.markers.Pop()
		return ast.NewReturnStmt(expr, ast.Span{Start: token.Span.Start, End: expr.Span().End})
	default:
		parser.markers.Push(MarkerExpr)
		expr := parser.ParseExpr()
		parser.markers.Pop()
		return ast.NewExprStmt(expr, expr.Span())
	}
}

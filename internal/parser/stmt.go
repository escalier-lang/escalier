package parser

import "github.com/escalier-lang/escalier/internal/ast"

func (parser *Parser) parseBlock() ast.Block {
	stmts := []ast.Stmt{}
	var start ast.Location

	token := parser.lexer.next()
	if t, ok := token.(*TOpenBrace); !ok {
		parser.reportError(token.Span(), "Expected an opening brace")
		// TODO: include Span
		return ast.Block{Stmts: stmts}
	} else {
		start = t.Span().Start
	}

	token = parser.lexer.peek()
	for {
		switch t := token.(type) {
		case *TCloseBrace:
			parser.lexer.consume()
			return ast.Block{Stmts: stmts, Span: ast.Span{Start: start, End: t.Span().End}}
		default:
			stmt := parser.parseStmt()
			stmts = append(stmts, stmt)
			token = parser.lexer.peek()
		}
	}
}

func (parser *Parser) parseStmt() ast.Stmt {
	token := parser.lexer.peek()

	switch token.(type) {
	case *TFn, *TVar, *TVal, *TDeclare, *TExport:
		decl := parser.parseDecl()
		if decl == nil {
			return nil
		}
		return ast.NewDeclStmt(decl, decl.Span())
	case *TReturn:
		parser.lexer.consume()
		expr := parser.ParseExpr()
		return ast.NewReturnStmt(expr, ast.Span{Start: token.Span().Start, End: expr.Span().End})
	default:
		expr := parser.ParseExpr()
		return ast.NewExprStmt(expr, expr.Span())
	}
}

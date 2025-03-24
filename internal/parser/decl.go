package parser

import "github.com/escalier-lang/escalier/internal/ast"

func (parser *Parser) parseDecl() ast.Decl {
	export := false
	declare := false

	token := parser.lexer.next()
	start := token.Span().Start
	if _, ok := token.(*TExport); ok {
		export = true
		token = parser.lexer.next()
	}

	if _, ok := token.(*TDeclare); ok {
		declare = true
		token = parser.lexer.next()
	}

	switch token.(type) {
	case *TVal, *TVar:
		kind := ast.ValKind
		if _, ok := token.(*TVar); ok {
			kind = ast.VarKind
		}

		token := parser.lexer.peek()
		_ident, ok := token.(*TIdentifier)
		var ident *ast.Ident
		if ok {
			parser.lexer.consume()
			ident = ast.NewIdentifier(_ident.Value, token.Span())
		} else {
			parser.reportError(token.Span(), "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span().Start, End: token.Span().Start},
			)
		}
		end := token.Span().End

		token = parser.lexer.peek()
		var init ast.Expr
		if !declare {
			_, ok = token.(*TEquals)
			if !ok {
				parser.reportError(token.Span(), "Expected equals sign")
				return nil
			}
			parser.lexer.consume()
			init = parser.ParseExpr()
			end = init.Span().End
		}

		return ast.NewVarDecl(kind, ident, init, declare, export, ast.Span{Start: start, End: end})
	case *TFn:
		token := parser.lexer.peek()
		_ident, ok := token.(*TIdentifier)
		var ident *ast.Ident
		if ok {
			parser.lexer.consume()
			ident = ast.NewIdentifier(_ident.Value, token.Span())
		} else {
			parser.reportError(token.Span(), "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span().Start, End: token.Span().Start},
			)
		}

		token = parser.lexer.peek()
		if _, ok := token.(*TOpenParen); !ok {
			parser.reportError(token.Span(), "Expected an opening paren")
		} else {
			parser.lexer.consume()
		}

		params := parser.parseParamSeq()

		token = parser.lexer.peek()
		if _, ok := token.(*TCloseParen); !ok {
			parser.reportError(token.Span(), "Expected a closing paren")
		} else {
			parser.lexer.consume()
		}

		end := token.Span().End

		var body ast.Block
		if !declare {
			body = parser.parseBlock()
			end = body.Span.End
		}

		return ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end})
	default:
		parser.reportError(token.Span(), "Unexpected token")
		return nil
	}
}

package parser

import "github.com/escalier-lang/escalier/internal/ast"

func (parser *Parser) parseDecl() ast.Decl {
	export := false
	declare := false

	token := parser.lexer.next()
	start := token.Span.Start
	if token.Type == Export {
		export = true
		token = parser.lexer.next()
	}

	if token.Type == Declare {
		declare = true
		token = parser.lexer.next()
	}

	//nolint: exhaustive
	switch token.Type {
	case Val, Var:
		kind := ast.ValKind
		if token.Type == Var {
			kind = ast.VarKind
		}

		token := parser.lexer.peek()
		var pat ast.Pat
		if token.Type == Identifier {
			parser.lexer.consume()
			pat = ast.NewIdentPat(token.Value, token.Span)
		} else {
			parser.reportError(token.Span, "Expected identifier")
			pat = ast.NewIdentPat(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}
		end := token.Span.End

		token = parser.lexer.peek()
		var init ast.Expr
		if !declare {
			if token.Type != Equal {
				parser.reportError(token.Span, "Expected equals sign")
				return nil
			}
			parser.lexer.consume()
			parser.markers.Push(MarkerExpr)
			init = parser.ParseExpr()
			parser.markers.Pop()
			end = init.Span().End
		}

		return ast.NewVarDecl(kind, pat, init, declare, export, ast.Span{Start: start, End: end})
	case Fn:
		token := parser.lexer.peek()
		var ident *ast.Ident
		if token.Type == Identifier {
			parser.lexer.consume()
			ident = ast.NewIdentifier(token.Value, token.Span)
		} else {
			parser.reportError(token.Span, "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}

		token = parser.lexer.peek()
		if token.Type != OpenParen {
			parser.reportError(token.Span, "Expected an opening paren")
		} else {
			parser.lexer.consume()
		}

		params := parser.parseParamSeq()

		token = parser.lexer.peek()
		if token.Type != CloseParen {
			parser.reportError(token.Span, "Expected a closing paren")
		} else {
			parser.lexer.consume()
		}

		end := token.Span.End

		var body ast.Block
		if !declare {
			body = parser.parseBlock()
			end = body.Span.End
		}

		return ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end})
	default:
		parser.reportError(token.Span, "Unexpected token")
		return nil
	}
}

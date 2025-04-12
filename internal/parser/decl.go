package parser

import "github.com/escalier-lang/escalier/internal/ast"

func (p *Parser) parseDecl() ast.Decl {
	export := false
	declare := false

	token := p.lexer.next()
	start := token.Span.Start
	if token.Type == Export {
		export = true
		token = p.lexer.next()
	}

	if token.Type == Declare {
		declare = true
		token = p.lexer.next()
	}

	//nolint: exhaustive
	switch token.Type {
	case Val, Var:
		kind := ast.ValKind
		if token.Type == Var {
			kind = ast.VarKind
		}

		pat := p.parsePattern(false)
		if pat == nil {
			p.reportError(token.Span, "Expected pattern")
			pat = ast.NewIdentPat(
				"",
				nil,
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}
		end := pat.Span().End

		token = p.lexer.peek()
		var init ast.Expr
		if !declare {
			if token.Type != Equal {
				p.reportError(token.Span, "Expected equals sign")
				return nil
			}
			p.lexer.consume()
			init = p.parseNonDelimitedExpr()
			if init == nil {
				token := p.lexer.peek()
				p.reportError(token.Span, "Expected an expression")
				init = ast.NewEmpty(token.Span)
			}
			end = init.Span().End
		}

		return ast.NewVarDecl(kind, pat, init, declare, export, ast.Span{Start: start, End: end})
	case Fn:
		token := p.lexer.peek()
		var ident *ast.Ident
		if token.Type == Identifier {
			p.lexer.consume()
			ident = ast.NewIdentifier(token.Value, token.Span)
		} else {
			p.reportError(token.Span, "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}

		token = p.lexer.peek()
		if token.Type != OpenParen {
			p.reportError(token.Span, "Expected an opening paren")
		} else {
			p.lexer.consume()
		}

		params := parseDelimSeq(p, CloseParen, Comma, p.parseParam)

		token = p.lexer.peek()
		if token.Type != CloseParen {
			p.reportError(token.Span, "Expected a closing paren")
		} else {
			p.lexer.consume()
		}

		end := token.Span.End

		var body ast.Block
		if !declare {
			body = p.parseBlock()
			end = body.Span.End
		}

		return ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end})
	default:
		p.reportError(token.Span, "Unexpected token")
		return nil
	}
}

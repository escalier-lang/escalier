package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parseDecl() optional.Option[ast.Decl] {
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

		pat := p.parsePattern(false).TakeOrElse(func() ast.Pat {
			p.reportError(token.Span, "Expected pattern")
			return ast.NewIdentPat(
				"",
				nil,
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		})
		end := pat.Span().End

		token = p.lexer.peek()
		init := optional.None[ast.Expr]()
		if !declare {
			if token.Type != Equal {
				p.reportError(token.Span, "Expected equals sign")
				return optional.None[ast.Decl]()
			}
			p.lexer.consume()
			init = p.parseNonDelimitedExpr().OrElse(func() optional.Option[ast.Expr] {
				token := p.lexer.peek()
				p.reportError(token.Span, "Expected an expression")
				return optional.Some[ast.Expr](ast.NewEmpty(token.Span))
			})
			init.IfSome(func(e ast.Expr) {
				end = e.Span().End
			})
		}

		return optional.Some[ast.Decl](
			ast.NewVarDecl(kind, pat, init, declare, export, ast.Span{Start: start, End: end}),
		)
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

		return optional.Some[ast.Decl](
			ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end}),
		)
	default:
		p.reportError(token.Span, "Unexpected token")
		return optional.None[ast.Decl]()
	}
}

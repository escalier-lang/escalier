package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parseDecl() (optional.Option[ast.Decl], [](*Error)) {
	errors := [](*Error){}

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

		patOption, patErrors := p.parsePattern(false)
		errors = append(errors, patErrors...)
		pat := patOption.TakeOrElse(func() ast.Pat {
			errors = append(errors, NewError(token.Span, "Expected pattern"))
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
				errors = append(errors, NewError(token.Span, "Expected equals sign"))
				return optional.None[ast.Decl](), errors
			}
			p.lexer.consume()
			initOption, initErrors := p.parseNonDelimitedExpr()
			errors = append(errors, initErrors...)
			init = initOption.OrElse(func() optional.Option[ast.Expr] {
				token := p.lexer.peek()
				errors = append(errors, NewError(token.Span, "Expected an expression"))
				return optional.Some[ast.Expr](ast.NewEmpty(token.Span))
			})
			init.IfSome(func(e ast.Expr) {
				end = e.Span().End
			})
		}

		return optional.Some[ast.Decl](
			ast.NewVarDecl(kind, pat, init, declare, export, ast.Span{Start: start, End: end}),
		), errors
	case Fn:
		token := p.lexer.peek()
		var ident *ast.Ident
		if token.Type == Identifier {
			p.lexer.consume()
			ident = ast.NewIdentifier(token.Value, token.Span)
		} else {
			errors = append(errors, NewError(token.Span, "Expected identifier"))
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}

		token = p.lexer.peek()
		if token.Type != OpenParen {
			errors = append(errors, NewError(token.Span, "Expected an opening paren"))
		} else {
			p.lexer.consume()
		}

		params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.parseParam)
		errors = append(errors, seqErrors...)

		token = p.lexer.peek()
		if token.Type != CloseParen {
			errors = append(errors, NewError(token.Span, "Expected a closing paren"))
		} else {
			p.lexer.consume()
		}

		end := token.Span.End

		if !declare {
			body, bodyErrors := p.parseBlock()
			errors = append(errors, bodyErrors...)
			end = body.Span.End

			return optional.Some[ast.Decl](
				ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end}),
			), errors
		}

		var body ast.Block // TODO: make this optional
		return optional.Some[ast.Decl](
			ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: end}),
		), errors
	default:
		errors = append(errors, NewError(token.Span, "Unexpected token"))
		return optional.None[ast.Decl](), errors
	}
}

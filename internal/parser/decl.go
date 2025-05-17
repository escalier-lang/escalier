package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

// decl = 'export'? 'declare'? (varDecl | fnDecl)
func (p *Parser) decl() (optional.Option[ast.Decl], [](*Error)) {
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

	// nolint: exhaustive
	switch token.Type {
	case Val, Var:
		return p.varDecl(start, token, export, declare)
	case Fn:
		return p.fnDecl(start, export, declare)
	case Type:
		return p.typeDecl(start, export, declare)
	default:
		errors = append(errors, NewError(token.Span, "Unexpected token"))
		return optional.None[ast.Decl](), errors
	}
}

// valDecl = 'val' pat '=' expr
// NOTE: '=' `expr` is optional for valDecl when `declare` is true.
func (p *Parser) varDecl(start ast.Location, token *Token, export bool, declare bool) (optional.Option[ast.Decl], []*Error) {
	errors := []*Error{}
	kind := ast.ValKind
	if token.Type == Var {
		kind = ast.VarKind
	}

	patOption, patErrors := p.pattern(false)
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

	typeAnn := optional.None[ast.TypeAnn]()
	if token.Type == Colon {
		p.lexer.consume() // consume ':'
		typeAnnOption, typeAnnErrors := p.typeAnn()
		errors = append(errors, typeAnnErrors...)
		typeAnn = typeAnnOption
		token = p.lexer.peek()
	}

	init := optional.None[ast.Expr]()
	if !declare {
		if token.Type != Equal {
			errors = append(errors, NewError(token.Span, "Expected equals sign"))
			return optional.None[ast.Decl](), errors
		}
		p.lexer.consume()
		initOption, initErrors := p.nonDelimitedExpr()
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

	span := ast.Span{Start: start, End: end}
	return optional.Some[ast.Decl](
		ast.NewVarDecl(kind, pat, typeAnn, init, export, declare, span),
	), errors
}

// fnDecl = 'fn' ident '(' param* ')' block
// NOTE: `block` is optional for fnDecl when `declare` is true.
// TODO: dedupe with `fnExpr`
func (p *Parser) fnDecl(start ast.Location, export bool, declare bool) (optional.Option[ast.Decl], []*Error) {
	errors := []*Error{}
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

	params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
	errors = append(errors, seqErrors...)

	token = p.lexer.peek()
	if token.Type != CloseParen {
		errors = append(errors, NewError(token.Span, "Expected a closing paren"))
	} else {
		p.lexer.consume()
	}

	end := token.Span.End

	bodyOption := optional.None[ast.Block]()
	if !declare {
		body, bodyErrors := p.block()
		errors = append(errors, bodyErrors...)
		end = body.Span.End
		bodyOption = optional.Some(body)
	}

	return optional.Some[ast.Decl](
		ast.NewFuncDecl(
			ident, params, bodyOption, export, declare,
			ast.NewSpan(start, end),
		),
	), errors
}

func (p *Parser) typeDecl(start ast.Location, export bool, declare bool) (optional.Option[ast.Decl], []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	if token.Type != Identifier {
		errors = append(errors, NewError(token.Span, "Expected identifier"))
		return optional.None[ast.Decl](), errors
	}
	p.lexer.consume()
	ident := ast.NewIdentifier(token.Value, token.Span)

	end := token.Span.End

	_, tokenErrors := p.expect(Equal, AlwaysConsume)
	errors = append(errors, tokenErrors...)

	typeParams := []*ast.TypeParam{}

	typeAnnOption, typeAnnErrors := p.typeAnn()
	errors = append(errors, typeAnnErrors...)
	decl := optional.Map(typeAnnOption, func(typeAnn ast.TypeAnn) ast.Decl {
		return ast.NewTypeDecl(ident, typeParams, typeAnn, export, declare, ast.NewSpan(start, end))
	})
	return decl, errors
}

package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// decl = 'export'? 'declare'? (varDecl | fnDecl)
func (p *Parser) decl() ast.Decl {
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
		p.reportError(token.Span, "Unexpected token")
		return nil
	}
}

// valDecl = 'val' pat '=' expr
// NOTE: '=' `expr` is optional for valDecl when `declare` is true.
func (p *Parser) varDecl(
	start ast.Location,
	token *Token,
	export bool,
	declare bool,
) ast.Decl {
	kind := ast.ValKind
	if token.Type == Var {
		kind = ast.VarKind
	}

	pat := p.pattern(false)
	if pat == nil {
		p.reportError(token.Span, "Expected pattern")
		pat = ast.NewIdentPat(
			"",
			nil,
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	}
	end := pat.Span().End

	token = p.lexer.peek()

	var typeAnn ast.TypeAnn
	if token.Type == Colon {
		p.lexer.consume() // consume ':'
		typeAnn = p.typeAnn()
		token = p.lexer.peek()
	}

	var init ast.Expr
	if !declare {
		if token.Type != Equal {
			p.reportError(token.Span, "Expected equals sign")
			return nil
		}
		p.lexer.consume()
		init = p.expr()
		if init == nil {
			token := p.lexer.peek()
			p.reportError(token.Span, "Expected an expression")
			init = ast.NewEmpty(token.Span)
		}
		end = init.Span().End
	}

	span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
	return ast.NewVarDecl(kind, pat, typeAnn, init, export, declare, span)
}

// fnDecl = 'fn' ident '(' param* ')' block
// NOTE: `block` is optional for fnDecl when `declare` is true.
// TODO: dedupe with `fnExpr`
func (p *Parser) fnDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	var ident *ast.Ident
	if token.Type == Identifier {
		p.lexer.consume()
		ident = ast.NewIdentifier(token.Value, token.Span)
	} else {
		p.reportError(token.Span, "Expected identifier")
		ident = ast.NewIdentifier(
			"",
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	}

	token = p.lexer.peek()
	if token.Type != OpenParen {
		p.reportError(token.Span, "Expected an opening paren")
	} else {
		p.lexer.consume()
	}

	params := parseDelimSeq(p, CloseParen, Comma, p.param)

	token = p.lexer.peek()
	if token.Type != CloseParen {
		p.reportError(token.Span, "Expected a closing paren")
	} else {
		p.lexer.consume()
	}

	end := token.Span.End

	var returnType ast.TypeAnn
	token = p.lexer.peek()
	if token.Type == Arrow {
		p.lexer.consume()
		typeAnn := p.typeAnn()
		if typeAnn == nil {
			p.reportError(token.Span, "Expected type annotation after arrow")
			return nil
		}
		end = typeAnn.Span().End
		returnType = typeAnn
	}

	var body ast.Block
	if !declare {
		body = p.block()
		end = body.Span.End
	}

	return ast.NewFuncDecl(
		ident, params, returnType, &body, export, declare,
		ast.NewSpan(start, end),
	)
}

func (p *Parser) typeDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier")
		return nil
	}
	p.lexer.consume()
	ident := ast.NewIdentifier(token.Value, token.Span)

	end := token.Span.End

	p.expect(Equal, AlwaysConsume)

	typeParams := []*ast.TypeParam{}

	typeAnn := p.typeAnn()

	if typeAnn == nil {
		return nil
	}
	decl := ast.NewTypeDecl(ident, typeParams, typeAnn, export, declare, ast.NewSpan(start, end))
	return decl
}

package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// Decl = 'export'? 'declare'? 'async'? (varDecl | fnDecl)
func (p *Parser) Decl() ast.Decl {
	export := false
	declare := false
	async := false

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

	if token.Type == Async {
		async = true
		token = p.lexer.next()
	}

	// nolint: exhaustive
	switch token.Type {
	case Val, Var:
		if async {
			p.reportError(token.Span, "async can only be used with functions")
			return nil
		}
		return p.varDecl(start, token, export, declare)
	case Fn:
		return p.fnDecl(start, export, declare, async)
	case Type:
		if async {
			p.reportError(token.Span, "async can only be used with functions")
			return nil
		}
		return p.typeDecl(start, export, declare)
	case Class:
		if async {
			p.reportError(token.Span, "async can only be used with functions")
			return nil
		}
		return p.classDecl(start, export, declare)
	default:
		// Accept 'class' as a valid top-level declaration
		if token.Type == Class {
			return p.classDecl(start, export, declare)
		}
		p.reportError(token.Span, "Unexpected token")
		return nil
	}
}

// classDecl = 'class' ident '(' param* ')' '{' classElem* '}'
func (p *Parser) classDecl(start ast.Location, export, declare bool) ast.Decl {
	token := p.lexer.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier after 'class'")
		return nil
	}
	p.lexer.consume()
	name := ast.NewIdentifier(token.Value, token.Span)

	// Parse optional type parameters for the class
	var typeParams []*ast.TypeParam
	token = p.lexer.peek()
	if token.Type == LessThan {
		p.lexer.consume() // consume '<'
		typeParams = parseDelimSeq(p, GreaterThan, Comma, p.typeParam)
		p.expect(GreaterThan, AlwaysConsume)
		token = p.lexer.peek()
	}

	// Parse optional constructor params
	params := []*ast.Param{}
	if token.Type == OpenParen {
		p.lexer.consume()
		params = parseDelimSeq(p, CloseParen, Comma, p.param)
		p.expect(CloseParen, AlwaysConsume)
		token = p.lexer.peek()
	}

	// Parse class body
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected '{' to start class body")
		return nil
	}
	p.lexer.consume()

	body := parseDelimSeq(p, CloseBrace, Comma, p.parseClassElem)
	p.expect(CloseBrace, AlwaysConsume)

	end := p.lexer.currentLocation
	span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
	return ast.NewClassDecl(name, typeParams, params, body, export, declare, span)
}

// parseClassElem parses a single class element (field, method, static, etc.)
func (p *Parser) parseClassElem() ast.ClassElem {
	token := p.lexer.peek()

	isStatic := false
	isAsync := false
	isPrivate := false
	isGet := false
	isSet := false
	start := token.Span.Start

	// Parse modifiers: static, async, private (order-insensitive)
	for {
		// nolint: exhaustive
		switch token.Type {
		case Static:
			isStatic = true
			p.lexer.consume()
		case Async:
			isAsync = true
			p.lexer.consume()
		case Private:
			isPrivate = true
			p.lexer.consume()
		case Get:
			isGet = true
			p.lexer.consume()
		case Set:
			isSet = true
			p.lexer.consume()
		default:
			goto modifiers_done
		}
		token = p.lexer.peek()
	}
modifiers_done:
	name := p.objExprKey()
	if name == nil {
		return nil
	}

	next := p.lexer.peek()

	// Parse optional type parameters for the method
	var typeParams []*ast.TypeParam
	if next.Type == LessThan {
		p.lexer.consume() // consume '<'
		typeParams = parseDelimSeq(p, GreaterThan, Comma, p.typeParam)
		p.expect(GreaterThan, AlwaysConsume)
		next = p.lexer.peek()
	}

	// Handle getter
	if isGet {
		// Accept and parse params for instance getters (e.g., self)
		var returnType ast.TypeAnn
		var throwsType ast.TypeAnn
		var body *ast.Block

		if next.Type == OpenParen {
			p.lexer.consume() // consume '('

			p.mutSelf() // TODO: check the value of mutSelf

			// TODO: report an error if `self` is not the only param
			// _ = parseDelimSeq(p, CloseParen, Comma, p.param)
			p.expect(CloseParen, AlwaysConsume)
			next = p.lexer.peek()
		}

		// Optionally parse return type (for getter)
		if next.Type == Arrow {
			p.lexer.consume()
			returnType = p.typeAnn()
			next = p.lexer.peek()
		}

		if next.Type == Throws {
			p.lexer.consume()
			throwsType = p.typeAnn()
			next = p.lexer.peek()
		}

		// Optionally parse block
		if next.Type == OpenBrace {
			block := p.block()
			body = &block
		}

		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.GetterElem{
			Name:    name,
			Fn:      ast.NewFuncExpr(typeParams, []*ast.Param{}, returnType, throwsType, false, body, span),
			Static:  isStatic,
			Private: isPrivate,
			Span_:   span,
		}
	}

	// Handle setter
	if isSet {
		var params []*ast.Param
		var body *ast.Block

		if next.Type == OpenParen {
			p.lexer.consume() // consume '('

			p.mutSelf() // TODO: check the value of mutSelf

			token = p.lexer.peek()
			if token.Type == Comma {
				p.lexer.consume() // consume ','
				params = parseDelimSeq(p, CloseParen, Comma, p.param)
			}

			// TODO: report an error if `mut self` is not the first param
			p.expect(CloseParen, AlwaysConsume)
			next = p.lexer.peek()
		}

		// Optionally parse block
		if next.Type == OpenBrace {
			block := p.block()
			body = &block
		}

		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.SetterElem{
			Name:    name,
			Fn:      ast.NewFuncExpr(typeParams, params, nil, nil, false, body, span),
			Static:  isStatic,
			Private: isPrivate,
			Span_:   span,
		}
	}

	if next.Type == OpenParen {
		// Method
		p.lexer.consume() // consume '('

		// TODO: skip mutSelf if `isStatic` is true
		mutSelf := p.mutSelf()

		params := []*ast.Param{}
		if isStatic {
			params = parseDelimSeq(p, CloseParen, Comma, p.param)
		} else {
			token = p.lexer.peek()
			if token.Type == Comma {
				p.lexer.consume() // consume ','
				params = parseDelimSeq(p, CloseParen, Comma, p.param)
			}
		}
		p.expect(CloseParen, AlwaysConsume)

		// Optionally parse return type
		var returnType ast.TypeAnn
		next = p.lexer.peek()
		if next.Type == Arrow {
			p.lexer.consume()
			returnType = p.typeAnn()
		}

		var throwsType ast.TypeAnn
		next = p.lexer.peek()
		if next.Type == Throws {
			p.lexer.consume()
			throwsType = p.typeAnn()
		}

		// Optionally parse block
		var body *ast.Block
		next = p.lexer.peek()
		if next.Type == OpenBrace {
			block := p.block()
			body = &block
		}

		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.MethodElem{
			Name:    name,
			Fn:      ast.NewFuncExpr(typeParams, params, returnType, throwsType, isAsync, body, span),
			MutSelf: mutSelf,
			Static:  isStatic,
			Private: isPrivate,
			Span_:   span,
		}
	} else {
		// Field
		var value ast.Expr
		var typeAnn ast.TypeAnn
		var default_ ast.Expr

		next = p.lexer.peek()

		// nolint: exhaustive
		switch next.Type {
		case Colon:
			p.lexer.consume()
			value = p.expr()
			next = p.lexer.peek()
			switch next.Type {
			case Colon:
				p.lexer.consume()
				typeAnn = p.typeAnn()
				next = p.lexer.peek()
				if next.Type == Equal {
					p.lexer.consume()
					default_ = p.expr()
				}
			case Equal:
				p.lexer.consume()
				default_ = p.expr()
			}
		case DoubleColon:
			p.lexer.consume()
			typeAnn = p.typeAnn()
			next = p.lexer.peek()
			if next.Type == Equal {
				p.lexer.consume()
				default_ = p.expr()
			}
		case Equal:
			p.lexer.consume()
			default_ = p.expr()
		}

		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.FieldElem{
			Name:    name,
			Value:   value,
			Type:    typeAnn,
			Default: default_,
			Private: isPrivate,
			Span_:   span,
		}
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

	pat := p.pattern(false, false)
	if pat == nil {
		p.reportError(token.Span, "Expected pattern")
		pat = ast.NewIdentPat(
			"",
			nil,
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
func (p *Parser) fnDecl(start ast.Location, export bool, declare bool, async bool) ast.Decl {
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
	var throwsType ast.TypeAnn
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

		// Check for throws clause after return type
		token = p.lexer.peek()
		if token.Type == Throws {
			p.lexer.consume()
			throwsTypeAnn := p.typeAnn()
			if throwsTypeAnn == nil {
				p.reportError(token.Span, "Expected type annotation after 'throws'")
			} else {
				throwsType = throwsTypeAnn
				end = throwsType.Span().End
			}
		}
	}

	var body ast.Block
	if !declare {
		body = p.block()
		end = body.Span.End
	}

	return ast.NewFuncDecl(
		ident, params, returnType, throwsType, &body, export, declare, async,
		ast.NewSpan(start, end, p.lexer.source.ID),
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

	// Parse optional type parameters
	var typeParams []*ast.TypeParam
	if p.lexer.peek().Type == LessThan {
		p.lexer.consume() // consume '<'
		typeParams = parseDelimSeq(p, GreaterThan, Comma, p.typeParam)
		p.expect(GreaterThan, AlwaysConsume)
	}

	p.expect(Equal, AlwaysConsume)

	typeAnn := p.typeAnn()

	if typeAnn == nil {
		return nil
	}

	// End position is the end of the type annotation
	end := typeAnn.Span().End

	span := ast.NewSpan(start, end, p.lexer.source.ID)
	decl := ast.NewTypeDecl(ident, typeParams, typeAnn, export, declare, span)
	return decl
}

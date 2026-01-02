package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// maybeTypeParams parses optional type parameters if present.
// Returns the parsed type parameters and updates the current token position.
func (p *Parser) maybeTypeParams() []*ast.TypeParam {
	var typeParams []*ast.TypeParam
	token := p.lexer.peek()
	if token.Type == LessThan {
		p.lexer.consume() // consume '<'
		typeParams = parseDelimSeq(p, GreaterThan, Comma, p.typeParam)
		p.expect(GreaterThan, AlwaysConsume)
	}
	return typeParams
}

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
	case Interface:
		if async {
			p.reportError(token.Span, "async can only be used with functions")
			return nil
		}
		return p.interfaceDecl(start, export, declare)
	case Enum:
		if async {
			p.reportError(token.Span, "async can only be used with functions")
			return nil
		}
		return p.enumDecl(start, export, declare)
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

// classDecl = 'class' ident typeParams? '(' param* ')' ('extends' typeAnn ('(' expr* ')')?)? '{' classElem* '}'
func (p *Parser) classDecl(start ast.Location, export, declare bool) ast.Decl {
	token := p.lexer.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier after 'class'")
		return nil
	}
	p.lexer.consume()
	name := ast.NewIdentifier(token.Value, token.Span)

	// Parse optional type parameters for the class
	typeParams := p.maybeTypeParams()
	token = p.lexer.peek()

	// Parse optional constructor params
	params := []*ast.Param{}
	if token.Type == OpenParen {
		p.lexer.consume()
		params = parseDelimSeq(p, CloseParen, Comma, p.param)
		p.expect(CloseParen, AlwaysConsume)
		token = p.lexer.peek()
	}

	// Parse optional extends clause
	var extends *ast.TypeRefTypeAnn
	if token.Type == Extends {
		p.lexer.consume()
		extendsTypeAnn := p.typeAnn()
		if extendsTypeAnn == nil {
			p.reportError(token.Span, "Expected type annotation after 'extends'")
			return nil
		}
		// Ensure the extends clause is a type reference
		var ok bool
		extends, ok = extendsTypeAnn.(*ast.TypeRefTypeAnn)
		if !ok {
			p.reportError(token.Span, "extends clause must be a type reference")
			return nil
		}
		token = p.lexer.peek()

		// Parse optional super constructor args after extends
		if token.Type == OpenParen {
			p.lexer.consume()
			// For now, we parse and discard the super constructor args
			// TODO(#262): store these args in the AST if needed for validation or codegen
			_ = parseDelimSeq(p, CloseParen, Comma, p.expr)
			p.expect(CloseParen, AlwaysConsume)
			token = p.lexer.peek()
		}
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
	return ast.NewClassDecl(name, typeParams, extends, params, body, export, declare, span)
}

// parseClassElem parses a single class element (field, method, static, etc.)
func (p *Parser) parseClassElem() ast.ClassElem {
	token := p.lexer.peek()

	isStatic := false
	isAsync := false
	isPrivate := false
	isReadonly := false
	isGet := false
	isSet := false
	start := token.Span.Start

	// Parse modifiers: static, async, private, readonly (order-insensitive)
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
		case Readonly:
			isReadonly = true
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

	// Parse optional type parameters for the method
	typeParams := p.maybeTypeParams()
	next := p.lexer.peek()

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

			if !isStatic {
				p.mutSelf() // TODO: check the value of mutSelf

				token = p.lexer.peek()
				if token.Type == Comma {
					p.lexer.consume() // consume ','
					params = parseDelimSeq(p, CloseParen, Comma, p.param)
				}
			} else {
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

		// TODO: report an error if `isAsync` is true
		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.FieldElem{
			Name:     name,
			Value:    value,
			Type:     typeAnn,
			Default:  default_,
			Static:   isStatic,
			Private:  isPrivate,
			Readonly: isReadonly,
			Span_:    span,
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

// fnDecl = 'fn' ident '<' typeParam* '>' '(' param* ')' block
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

	// Parse optional type parameters for the function
	typeParams := p.maybeTypeParams()
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
		ident, typeParams, params, returnType, throwsType, &body, export, declare, async,
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
	typeParams := p.maybeTypeParams()

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

func (p *Parser) interfaceDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier")
		return nil
	}
	p.lexer.consume()
	ident := ast.NewIdentifier(token.Value, token.Span)

	// Parse optional type parameters
	typeParams := p.maybeTypeParams()

	// Parse optional extends clause
	var extends []*ast.TypeRefTypeAnn
	token = p.lexer.peek()
	if token.Type == Extends {
		p.lexer.consume() // consume 'extends'
		typeAnns := parseDelimSeq(p, OpenBrace, Comma, p.typeAnn)
		for _, typeAnn := range typeAnns {
			typeRefType, ok := typeAnn.(*ast.TypeRefTypeAnn)
			if !ok {
				p.reportError(typeAnn.Span(), "extends type for interface isn't a type ref")
			}
			extends = append(extends, typeRefType)
		}
	}

	// Parse the object type body (interface body)
	token = p.lexer.peek()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected '{' to start interface body")
		return nil
	}
	p.lexer.consume() // consume '{'
	elems := parseDelimSeq(p, CloseBrace, Comma, p.objTypeAnnElem)
	end := p.expect(CloseBrace, AlwaysConsume)
	objType := ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))

	if objType == nil {
		return nil
	}

	span := ast.NewSpan(start, end, p.lexer.source.ID)
	decl := ast.NewInterfaceDecl(ident, typeParams, extends, objType, export, declare, span)
	return decl
}

// enumElem parses a single enum element (variant or extension)
func (p *Parser) enumElem() ast.EnumElem {
	token := p.lexer.peek()

	// Check for spread notation (extension)
	if token.Type == DotDotDot {
		spreadStart := token.Span.Start
		p.lexer.consume()
		token = p.lexer.peek()
		if token.Type != Identifier {
			p.reportError(token.Span, "Expected identifier after '...'")
			return nil
		}
		p.lexer.consume()
		arg := ast.NewIdentifier(token.Value, token.Span)
		spreadEnd := p.lexer.currentLocation
		spreadSpan := ast.NewSpan(spreadStart, spreadEnd, p.lexer.source.ID)
		return ast.NewEnumSpread(arg, spreadSpan)
	}

	// Parse variant
	if token.Type == Identifier {
		variantStart := token.Span.Start
		p.lexer.consume()
		variantName := ast.NewIdentifier(token.Value, token.Span)

		token = p.lexer.peek()

		// Check for params
		var params []*ast.Param
		if token.Type == OpenParen {
			p.lexer.consume()
			params = parseDelimSeq(p, CloseParen, Comma, p.param)
			p.expect(CloseParen, AlwaysConsume)
		}

		variantEnd := p.lexer.currentLocation
		variantSpan := ast.NewSpan(variantStart, variantEnd, p.lexer.source.ID)
		return ast.NewEnumVariant(variantName, params, variantSpan)
	}

	p.reportError(token.Span, "Expected variant name or '...' for extension")
	return nil
}

// enumDecl = 'enum' ident typeParams? '{' enumVariant* '}'
// enumVariant = '...' ident | ident '(' typeAnn* ')'?
func (p *Parser) enumDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier after 'enum'")
		return nil
	}
	p.lexer.consume()
	name := ast.NewIdentifier(token.Value, token.Span)

	// Parse optional type parameters
	typeParams := p.maybeTypeParams()

	// Expect opening brace
	token = p.lexer.peek()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected '{' to start enum body")
		return nil
	}
	p.lexer.consume()

	// Parse enum elements (variants and spreads)
	elems := parseDelimSeq(p, CloseBrace, Comma, p.enumElem)

	// Expect closing brace
	p.expect(CloseBrace, AlwaysConsume)

	end := p.lexer.currentLocation
	span := ast.NewSpan(start, end, p.lexer.source.ID)
	decl := ast.NewEnumDecl(name, typeParams, elems, export, declare, span)
	return decl
}

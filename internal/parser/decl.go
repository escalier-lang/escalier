package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// maybeTypeParams parses optional type parameters if present.
// Returns the parsed type parameters and updates the current token position.
//
// Lifetime parameters (e.g. 'a) are syntactically accepted in the same
// angle-bracket list but are not yet supported on the declaration kinds
// that call this helper (class, interface, type alias, enum, methods),
// so we surface a parse error instead of silently dropping them. Callers
// that do support lifetimes (function decl/expr, `fn` type annotations)
// use maybeLifetimeAndTypeParams directly.
func (p *Parser) maybeTypeParams() []*ast.TypeParam {
	lifetimeParams, typeParams := p.maybeLifetimeAndTypeParams()
	for _, lp := range lifetimeParams {
		p.reportError(lp.Span(),
			"lifetime parameters are not supported in this context")
	}
	return typeParams
}

// lifetimeAnn parses a single lifetime annotation token (e.g. 'a) and
// returns the corresponding AST node. Returns nil when the next token is
// not a Lifetime — suitable for use as a parseDelimSeq combinator.
func (p *Parser) lifetimeAnn() *ast.LifetimeAnn {
	tok := p.lexer.peek()
	if tok.Type != Lifetime {
		return nil
	}
	p.lexer.consume()
	return ast.NewLifetimeAnn(tok.Value, tok.Span)
}

// maybeLifetimeAndTypeParams parses optional generic parameters that may
// include both lifetime parameters ('a) and type parameters (T). Lifetime
// parameters must precede type parameters by convention, but this parser
// accepts them in any order and sorts them out by token kind.
func (p *Parser) maybeLifetimeAndTypeParams() ([]*ast.LifetimeAnn, []*ast.TypeParam) {
	var lifetimeParams []*ast.LifetimeAnn
	var typeParams []*ast.TypeParam
	token := p.lexer.peek()
	if token.Type != LessThan {
		return nil, nil
	}
	p.lexer.consume() // consume '<'
	for {
		select {
		case <-p.ctx.Done():
			return lifetimeParams, typeParams
		default:
		}
		token = p.lexer.peek()
		if token.Type == GreaterThan {
			break
		}
		if token.Type == Lifetime {
			p.lexer.consume()
			lifetimeParams = append(lifetimeParams, ast.NewLifetimeAnn(token.Value, token.Span))
		} else {
			tp := p.typeParam()
			if tp == nil {
				break
			}
			typeParams = append(typeParams, tp)
		}
		token = p.lexer.peek()
		if token.Type == Comma {
			p.lexer.consume()
			continue
		}
		break
	}
	p.expect(GreaterThan, AlwaysConsume)
	return lifetimeParams, typeParams
}

// Decl = 'export'? 'declare'? 'async'? (varDecl | fnDecl)
func (p *Parser) Decl() ast.Decl {
	export := false
	declare := false
	async := false
	data := false

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

	// `data` is a contextual modifier — only treated as a keyword when it
	// immediately precedes `class`. Anywhere else it is a regular identifier.
	if token.Type == Identifier && token.Value == "data" && p.lexer.peek().Type == Class {
		data = true
		token = p.lexer.next()
	}

	if async && token.Type != Fn {
		p.reportError(token.Span, "async can only be used with functions")
	}

	// nolint: exhaustive
	switch token.Type {
	case Val, Var:
		return p.varDecl(start, token, export, declare)
	case Fn:
		return p.fnDecl(start, export, declare, async)
	case Type:
		return p.typeDecl(start, export, declare)
	case Interface:
		return p.interfaceDecl(start, export, declare)
	case Enum:
		return p.enumDecl(start, export, declare)
	case Class:
		return p.classDecl(start, export, declare, data)
	default:
		p.reportError(token.Span, "Unexpected token")
		return nil
	}
}

// classDecl = 'data'? 'class' ident typeParams? '(' param* ')' ('extends' typeAnn ('(' expr* ')')?)? '{' classElem* '}'
func (p *Parser) classDecl(start ast.Location, export, declare, data bool) ast.Decl {
	token := p.lexer.peek()
	var name *ast.Ident
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier after 'class'")
		name = ast.NewIdentifier(
			"",
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	} else {
		p.lexer.consume()
		name = ast.NewIdentifier(token.Value, token.Span)
	}

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
		nextToken := p.lexer.peek()
		if nextToken.Type == OpenBrace {
			// The '{' is the class body, not a type annotation.
			p.reportError(token.Span, "Expected type annotation after 'extends'")
		} else {
			extendsTypeAnn := p.typeAnn()
			if extendsTypeAnn == nil {
				p.reportError(token.Span, "Expected type annotation after 'extends'")
			} else {
				// Ensure the extends clause is a type reference
				var ok bool
				extends, ok = extendsTypeAnn.(*ast.TypeRefTypeAnn)
				if !ok {
					p.reportError(token.Span, "extends clause must be a type reference")
				}
			}
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
		end := p.lexer.currentLocation
		span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
		decl := ast.NewClassDecl(name, typeParams, extends, params, nil, data, export, declare, span)
		return decl
	}
	p.lexer.consume()

	body := parseDelimSeq(p, CloseBrace, Comma, p.parseClassElem)
	p.expect(CloseBrace, AlwaysConsume)

	end := p.lexer.currentLocation
	span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
	decl := ast.NewClassDecl(name, typeParams, extends, params, body, data, export, declare, span)
	return decl
}

// parseConstructorElem parses an explicit `constructor(...) { ... }` block.
// The `constructor` token has not yet been consumed.
//
// Per the requirements, the constructor's first parameter must be `mut self`
// (no type annotation). The remaining params follow after a comma. The
// `mut self` is preserved as `ConstructorElem.MutSelf` and as `Fn.Params[0]`
// for the body checker.
func (p *Parser) parseConstructorElem(
	start ast.Location,
	token *Token,
	isStatic, isAsync, isPrivate, isReadonly, isGet, isSet bool,
) ast.ClassElem {
	if isStatic {
		p.reportError(token.Span, "constructors cannot be static")
	}
	if isAsync {
		p.reportError(token.Span, "constructors cannot be async")
	}
	if isReadonly {
		p.reportError(token.Span, "constructors cannot be readonly")
	}
	if isGet || isSet {
		p.reportError(token.Span, "constructors cannot be getters or setters")
	}

	p.lexer.consume() // consume `constructor`

	typeParams := p.maybeTypeParams()

	var mutSelf *bool
	var params []*ast.Param

	next := p.lexer.peek()
	if next.Type != OpenParen {
		p.reportError(next.Span, "Expected '(' after 'constructor'")
	} else {
		p.lexer.consume() // consume '('

		// Parse leading `mut self` / `self`.
		selfStart := p.lexer.currentLocation
		mutSelf = p.mutSelf()
		if mutSelf == nil {
			// No `self` at all — error and continue parsing the rest of the
			// param list as if `self` had been there.
			p.reportError(
				ast.Span{Start: selfStart, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID},
				"constructors must declare `mut self` as their first parameter",
			)
		} else if !*mutSelf {
			// `self` without `mut`.
			p.reportError(
				ast.Span{Start: selfStart, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID},
				"the `self` parameter of a constructor must be declared `mut self`",
			)
		}

		// `mut self : Self` — type annotation on self is not allowed.
		if mutSelf != nil && p.lexer.peek().Type == Colon {
			colonTok := p.lexer.peek()
			p.lexer.consume() // consume ':'
			_ = p.typeAnn()   // discard the annotation
			p.reportError(colonTok.Span, "the `mut self` parameter cannot have a type annotation")
		}

		// Materialize the `mut self` parameter as the first entry in params
		// so downstream phases can read `Fn.Params[0]` uniformly. We skip
		// this when no `self` token was found (mutSelf == nil); the error
		// has already been reported, and inserting a phantom param here
		// would mask the absence in the AST.
		if mutSelf != nil {
			selfSpan := ast.Span{Start: selfStart, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
			selfPat := ast.NewIdentPat("self", *mutSelf, nil, nil, selfSpan)
			params = append(params, &ast.Param{
				Pattern:  selfPat,
				TypeAnn:  nil,
				Optional: false,
			})
		}

		next = p.lexer.peek()
		if next.Type == Comma {
			p.lexer.consume() // consume ','
			rest := parseDelimSeq(p, CloseParen, Comma, p.param)
			params = append(params, rest...)
		} else if mutSelf == nil {
			// Recovery: no leading self was found; parse the params as a
			// regular list so we still produce a usable AST.
			params = parseDelimSeq(p, CloseParen, Comma, p.param)
		}
		p.expect(CloseParen, AlwaysConsume)

		// Detect `self` appearing as a non-leading parameter (skip the
		// leading self we just inserted).
		startIdx := 0
		if mutSelf != nil {
			startIdx = 1
		}
		for _, param := range params[startIdx:] {
			if ip, ok := param.Pattern.(*ast.IdentPat); ok && ip.Name == "self" {
				p.reportError(param.Pattern.Span(),
					"the `self` parameter must be the first parameter of a constructor")
			}
		}
	}

	// `throws` may appear in either order relative to `->` (the arrow form
	// is itself an error). Accept whichever comes first; if both appear we
	// keep the first one and discard the second.
	next = p.lexer.peek()
	var throwsType ast.TypeAnn
	if next.Type == Throws {
		p.lexer.consume()
		throwsType = p.typeAnn()
		next = p.lexer.peek()
	}
	if next.Type == Arrow {
		p.reportError(next.Span, "constructors cannot declare a return type")
		p.lexer.consume()
		_ = p.typeAnn()
		next = p.lexer.peek()
		// A `throws` after `->` is also tolerated, but we already errored.
		if next.Type == Throws && throwsType == nil {
			p.lexer.consume()
			throwsType = p.typeAnn()
			next = p.lexer.peek()
		}
	}

	var body *ast.Block
	if next.Type == OpenBrace {
		block := p.block()
		body = &block
	}

	span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
	return &ast.ConstructorElem{
		Fn:      ast.NewFuncExpr(nil, typeParams, params, nil, throwsType, false, body, span),
		MutSelf: mutSelf,
		Private: isPrivate,
		Span_:   span,
	}
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
		// Check if context has been cancelled (timeout or cancellation)
		select {
		case <-p.ctx.Done():
			// Return what we have so far when context is done
			return nil
		default:
			// continue
		}

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
	// `constructor` is a contextual keyword at the start of a class element.
	// Anywhere else it is a regular identifier.
	if token.Type == Identifier && token.Value == "constructor" {
		return p.parseConstructorElem(start, token, isStatic, isAsync, isPrivate, isReadonly, isGet, isSet)
	}

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

			// TODO(#506): report an error if `self` is not the only param
			// (instance) or if any params are present (static).
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
			Fn:      ast.NewFuncExpr(nil, typeParams, []*ast.Param{}, returnType, throwsType, false, body, span),
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

			// TODO(#506): report an error if `mut self` is not the first
			// param, if there isn't exactly one value param after it
			// (instance), or if there isn't exactly one param (static).
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
			Fn:      ast.NewFuncExpr(nil, typeParams, params, nil, nil, false, body, span),
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
			Fn:      ast.NewFuncExpr(nil, typeParams, params, returnType, throwsType, isAsync, body, span),
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
			"", false, nil, nil,
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID})
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
			onNewLine := token.Span.Start.Line != end.Line
			if p.isStatementInitiator(token.Type) || onNewLine {
				zeroSpan := ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID}
				init = ast.NewError(zeroSpan)
			} else {
				init = p.expr()
			}
		} else {
			p.lexer.consume()
			init = p.expr()
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

	// Parse optional lifetime + type parameters for the function
	lifetimeParams, typeParams := p.maybeLifetimeAndTypeParams()
	token = p.lexer.peek()

	if token.Type != OpenParen {
		p.reportError(token.Span, "Expected an opening paren")
		if ident.Name == "" ||
			p.isStatementInitiator(token.Type) ||
			token.Span.Start.Line != ident.Span().Start.Line {
			// The declaration is incomplete (e.g. "export fn" or
			// "export fn foo" while the user is still typing). Return a
			// partial node to avoid consuming tokens from subsequent
			// statements.
			end := ident.Span().End
			fd := ast.NewFuncDecl(
				ident, lifetimeParams, typeParams, nil, nil, nil, nil, export, declare, async,
				ast.NewSpan(start, end, p.lexer.source.ID),
			)
			return fd
		}
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
			end = token.Span.End
			p.reportError(token.Span, "Expected type annotation after arrow")
		} else {
			end = typeAnn.Span().End
			returnType = typeAnn
		}

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

	fd := ast.NewFuncDecl(
		ident, lifetimeParams, typeParams, params, returnType, throwsType, &body, export, declare, async,
		ast.NewSpan(start, end, p.lexer.source.ID),
	)
	return fd
}

func (p *Parser) typeDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	var ident *ast.Ident
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier")
		ident = ast.NewIdentifier(
			"",
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	} else {
		p.lexer.consume()
		ident = ast.NewIdentifier(token.Value, token.Span)
	}

	// Parse optional type parameters
	typeParams := p.maybeTypeParams()

	p.expect(Equal, AlwaysConsume)

	typeAnn := p.typeAnn()

	if typeAnn == nil {
		end := p.lexer.currentLocation
		span := ast.NewSpan(start, end, p.lexer.source.ID)
		return ast.NewTypeDecl(ident, typeParams, nil, export, declare, span)
	}

	// End position is the end of the type annotation
	end := typeAnn.Span().End

	span := ast.NewSpan(start, end, p.lexer.source.ID)
	decl := ast.NewTypeDecl(ident, typeParams, typeAnn, export, declare, span)
	return decl
}

func (p *Parser) interfaceDecl(start ast.Location, export bool, declare bool) ast.Decl {
	token := p.lexer.peek()
	var ident *ast.Ident
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier")
		ident = ast.NewIdentifier(
			"",
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	} else {
		p.lexer.consume()
		ident = ast.NewIdentifier(token.Value, token.Span)
	}

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
		end := p.lexer.currentLocation
		span := ast.NewSpan(start, end, p.lexer.source.ID)
		objType := ast.NewObjectTypeAnn(nil, span)
		return ast.NewInterfaceDecl(ident, typeParams, extends, objType, export, declare, span)
	}
	p.lexer.consume() // consume '{'
	elems := parseDelimSeq(p, CloseBrace, Comma, p.objTypeAnnElem)
	end := p.expect(CloseBrace, AlwaysConsume)
	objType := ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))

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
	var name *ast.Ident
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier after 'enum'")
		name = ast.NewIdentifier(
			"",
			ast.Span{Start: token.Span.Start, End: token.Span.Start, SourceID: p.lexer.source.ID},
		)
	} else {
		p.lexer.consume()
		name = ast.NewIdentifier(token.Value, token.Span)
	}

	// Parse optional type parameters
	typeParams := p.maybeTypeParams()

	// Expect opening brace
	token = p.lexer.peek()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected '{' to start enum body")
		end := p.lexer.currentLocation
		span := ast.NewSpan(start, end, p.lexer.source.ID)
		return ast.NewEnumDecl(name, typeParams, nil, export, declare, span)
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

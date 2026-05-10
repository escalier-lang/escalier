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

// Decl = 'export'? 'override'? 'declare'? 'async'? (varDecl | fnDecl | ...)
//
//	| 'override'? 'declare' 'module' StrLit '{' decl* '}'
//	| 'override'? 'declare' 'global' '{' decl* '}'
func (p *Parser) Decl() ast.Decl {
	export := false
	override := false
	declare := false
	async := false

	token := p.lexer.next()
	start := token.Span.Start
	var exportSpan ast.Span
	if token.Type == Export {
		export = true
		exportSpan = token.Span
		token = p.lexer.next()
	}

	var overrideSpan ast.Span
	if token.Type == Override {
		override = true
		overrideSpan = token.Span
		token = p.lexer.next()
	}

	if token.Type == Declare {
		declare = true
		token = p.lexer.next()

		// `module "name" { ... }` and `global { ... }` are contextual:
		// `module` and `global` are plain identifiers everywhere else, so
		// only treat them as block headers immediately after `declare`.
		if token.Type == Identifier && token.Value == "module" {
			if export {
				p.reportError(exportSpan, "'export' is not allowed before 'declare module'")
			}
			return p.declareModuleDecl(start, override)
		}
		if token.Type == Identifier && token.Value == "global" {
			if export {
				p.reportError(exportSpan, "'export' is not allowed before 'declare global'")
			}
			return p.declareGlobalDecl(start, override)
		}
	}

	if override && !declare {
		p.reportError(overrideSpan, "'override' requires 'declare'")
	}

	if token.Type == Async {
		async = true
		token = p.lexer.next()
	}

	if async && token.Type != Fn {
		p.reportError(token.Span, "async can only be used with functions")
	}

	var decl ast.Decl
	// nolint: exhaustive
	switch token.Type {
	case Val, Var:
		decl = p.varDecl(start, token, export, declare)
	case Fn:
		decl = p.fnDecl(start, export, declare, async)
	case Type:
		decl = p.typeDecl(start, export, declare)
	case Interface:
		decl = p.interfaceDecl(start, export, declare)
	case Enum:
		decl = p.enumDecl(start, export, declare)
	case Class:
		decl = p.classDecl(start, export, declare)
	case Identifier:
		// `namespace` is contextual — only meaningful inside declare blocks,
		// but we parse it wherever a decl is valid and let the checker enforce
		// placement rules.
		if token.Value == "namespace" {
			decl = p.namespaceDecl(start, export, override)
		} else {
			p.reportError(token.Span, "Unexpected token")
			return nil
		}
	default:
		p.reportError(token.Span, "Unexpected token")
		return nil
	}
	if decl != nil && override {
		decl.SetOverride(true)
	}
	return decl
}

// namespaceDecl parses `namespace Name { <decl>* }` after the `namespace`
// identifier has already been consumed by the caller.
func (p *Parser) namespaceDecl(start ast.Location, export, override bool) *ast.NamespaceDecl {
	nameTok := p.lexer.next()
	var name *ast.Ident
	if nameTok.Type != Identifier {
		p.reportError(nameTok.Span, "Expected identifier after 'namespace'")
		name = ast.NewIdentifier("", nameTok.Span)
	} else {
		name = ast.NewIdentifier(nameTok.Value, nameTok.Span)
	}

	decls := p.declareBlockBody(override)
	end := p.expect(CloseBrace, AlwaysConsume)
	span := ast.NewSpan(start, end, p.lexer.source.ID)
	return ast.NewNamespaceDecl(name, decls, export, override, span)
}

// declareModuleDecl parses `module "<name>" { <decl>* }` after both the
// `declare` and `module` keywords have already been consumed by the caller.
// `override` indicates whether the enclosing `Decl()` saw an `override`
// keyword; it's stamped on the resulting decl and propagated to every inner
// declaration in the block body.
func (p *Parser) declareModuleDecl(start ast.Location, override bool) *ast.DeclareModuleDecl {
	nameTok := p.lexer.next()
	var name *ast.StrLit
	if nameTok.Type != StrLit {
		p.reportError(nameTok.Span, "Expected string literal after 'module'")
		name = ast.NewString("", nameTok.Span)
	} else {
		name = ast.NewString(nameTok.Value, nameTok.Span)
	}

	decls := p.declareBlockBody(override)
	end := p.expect(CloseBrace, AlwaysConsume)
	span := ast.NewSpan(start, end, p.lexer.source.ID)
	return ast.NewDeclareModuleDecl(name, decls, override, span)
}

// declareGlobalDecl parses `global { <decl>* }` after both the `declare`
// keyword and the `global` identifier have already been consumed by the caller.
// See declareModuleDecl for the meaning of `override`.
func (p *Parser) declareGlobalDecl(start ast.Location, override bool) *ast.DeclareGlobalDecl {
	// `global` was already consumed by the caller via lexer.next().
	decls := p.declareBlockBody(override)
	end := p.expect(CloseBrace, AlwaysConsume)
	span := ast.NewSpan(start, end, p.lexer.source.ID)
	return ast.NewDeclareGlobalDecl(decls, override, span)
}

// setOverrideRecursive stamps override=true on d and, if d is a
// NamespaceDecl, recurses into its children so that every decl in the
// namespace tree also carries the flag.
func setOverrideRecursive(d ast.Decl) {
	d.SetOverride(true)
	if ns, ok := d.(*ast.NamespaceDecl); ok {
		for _, child := range ns.Decls {
			setOverrideRecursive(child)
		}
	}
}

// declareBlockBody parses the `{ <decl>* }` body shared by
// `declare module` and `declare global`. When `override` is true, each
// inner decl (and all decls nested inside NamespaceDecls) inherits the
// override flag — this matches the design where `override declare module /
// global` implies `override` on every member.
// Returns once the next token is `}` or EOF; the caller is responsible
// for consuming the closing brace.
func (p *Parser) declareBlockBody(override bool) []ast.Decl {
	p.expect(OpenBrace, AlwaysConsume)
	decls := []ast.Decl{}
	for {
		select {
		case <-p.ctx.Done():
			return decls
		default:
		}
		token := p.lexer.peek()
		// nolint: exhaustive
		switch token.Type {
		case CloseBrace, EndOfFile:
			return decls
		case LineComment, BlockComment:
			p.lexer.consume()
			continue
		}
		inner := p.Decl()
		if inner == nil {
			// Decl() always consumes at least one token before returning
			// nil, so the loop is guaranteed to make progress.
			continue
		}
		if override {
			setOverrideRecursive(inner)
		}
		decls = append(decls, inner)
	}
}

// classDecl = 'class' ident typeParams?
//
//	('extends' typeAnn ('(' expr* ')')?)?
//	('implements' typeAnn (',' typeAnn)*)?
//	'{' classElem* '}'
func (p *Parser) classDecl(start ast.Location, export, declare bool) ast.Decl {
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

	// Parse optional lifetime + type parameters for the class
	lifetimeParams, typeParams := p.maybeLifetimeAndTypeParams()
	token = p.lexer.peek()

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

	// Parse optional implements clause
	var implements []*ast.TypeRefTypeAnn
	if token.Type == Implements {
		implementsTok := token
		p.lexer.consume()
		for {
			// Implements entries must be type references (qualified
			// identifiers with optional type args), not arbitrary type
			// annotations — that's both what the grammar requires and
			// what avoids the `implements { ... }` ambiguity where the
			// class body would otherwise be parsed as an object type.
			next := p.lexer.peek()
			if next.Type != Identifier {
				p.reportError(implementsTok.Span, "Expected type reference after 'implements'")
				break
			}
			p.lexer.consume()
			implements = append(implements, p.parseTypeRef(next))
			if p.lexer.peek().Type != Comma {
				break
			}
			p.lexer.consume()
		}
		token = p.lexer.peek()
	}

	// Parse class body
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected '{' to start class body")
		end := p.lexer.currentLocation
		span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
		decl := ast.NewClassDecl(name, lifetimeParams, typeParams, extends, implements, nil, export, declare, span)
		return decl
	}
	p.lexer.consume()

	body := parseDelimSeq(p, CloseBrace, Comma, p.parseClassElem)
	p.expect(CloseBrace, AlwaysConsume)

	end := p.lexer.currentLocation
	span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
	decl := ast.NewClassDecl(name, lifetimeParams, typeParams, extends, implements, body, export, declare, span)
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

	var receiver *ast.MethodReceiver
	var params []*ast.Param

	next := p.lexer.peek()
	if next.Type != OpenParen {
		p.reportError(next.Span, "Expected '(' after 'constructor'")
	} else {
		p.lexer.consume() // consume '('

		// Parse leading `mut self` / `self`. A lifetime on the receiver
		// (`'a self`) is captured into receiver.Lifetime and reported by
		// the checker (MutSelfHasLifetime); the parser stays silent so
		// the user sees one diagnostic, not two.
		selfStart := p.lexer.currentLocation
		receiver = p.selfReceiver()
		if receiver == nil {
			// No `self` at all — error and continue parsing the rest of the
			// param list as if `self` had been there.
			p.reportError(
				ast.Span{Start: selfStart, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID},
				"constructors must declare `mut self` as their first parameter",
			)
		} else if !receiver.Mut {
			// `self` without `mut`.
			p.reportError(
				receiver.Span_,
				"the `self` parameter of a constructor must be declared `mut self`",
			)
		}

		// `mut self : Self` — type annotation on self is not allowed.
		if receiver != nil && p.lexer.peek().Type == Colon {
			colonTok := p.lexer.peek()
			p.lexer.consume() // consume ':'
			_ = p.typeAnn()   // discard the annotation
			p.reportError(colonTok.Span, "the `mut self` parameter cannot have a type annotation")
		}

		// Materialize the `mut self` parameter as the first entry in params
		// so downstream phases can read `Fn.Params[0]` uniformly. We skip
		// this when no `self` token was found (receiver == nil); the error
		// has already been reported, and inserting a phantom param here
		// would mask the absence in the AST.
		if receiver != nil {
			selfPat := ast.NewIdentPat("self", receiver.Mut, nil, nil, receiver.Span_)
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
		} else if receiver == nil {
			// Recovery: no leading self was found; parse the params as a
			// regular list so we still produce a usable AST.
			params = parseDelimSeq(p, CloseParen, Comma, p.param)
		}
		p.expect(CloseParen, AlwaysConsume)

		// Detect `self` appearing as a non-leading parameter (skip the
		// leading self we just inserted).
		startIdx := 0
		if receiver != nil {
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
		Fn:       ast.NewFuncExpr(nil, typeParams, params, nil, throwsType, false, body, span),
		Receiver: receiver,
		Private:  isPrivate,
		Span_:    span,
	}
}

// consumeContextualGetSet handles the `get`/`set` contextual-keyword
// disambiguation at the start of a class element.
//
// `get` and `set` are only modifiers when followed by a property name. If the
// next token after `get`/`set` is `(` or `<`, then the word itself is the
// method name (e.g. `get()` is a method named `get`, `get<T>()` is a generic
// method named `get`). The token list `OpenParen | LessThan` covers both
// cases — those are the only tokens that can start the formal-parameter or
// type-parameter list of a method definition; an actual getter/setter is
// always followed by an identifier (the property name).
//
// On call, the current token must be `get` or `set`. Returns true and leaves
// the lexer positioned after the consumed keyword if the keyword acts as a
// modifier; returns false and restores the lexer state if it doesn't.
func (p *Parser) consumeContextualGetSet() bool {
	saved := p.saveState()
	p.lexer.consume()
	after := p.lexer.peek()
	if after.Type == OpenParen || after.Type == LessThan {
		p.restoreState(saved)
		return false
	}
	return true
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
			if !p.consumeContextualGetSet() {
				goto modifiers_done
			}
			isGet = true
		case Set:
			if !p.consumeContextualGetSet() {
				goto modifiers_done
			}
			isSet = true
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

	// Parse optional lifetime + type parameters for the method.
	lifetimeParams, typeParams := p.maybeLifetimeAndTypeParams()
	next := p.lexer.peek()

	// Handle getter
	if isGet {
		// Accept and parse params for instance getters (e.g., self)
		var returnType ast.TypeAnn
		var throwsType ast.TypeAnn
		var body *ast.Block
		var receiver *ast.MethodReceiver

		if next.Type == OpenParen {
			p.lexer.consume() // consume '('

			if !isStatic {
				receiver = p.selfReceiver()
			}

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
			Name:     name,
			Fn:       ast.NewFuncExpr(lifetimeParams, typeParams, []*ast.Param{}, returnType, throwsType, false, body, span),
			Receiver: receiver,
			Static:   isStatic,
			Private:  isPrivate,
			Span_:    span,
		}
	}

	// Handle setter
	if isSet {
		var params []*ast.Param
		var body *ast.Block
		var receiver *ast.MethodReceiver

		if next.Type == OpenParen {
			p.lexer.consume() // consume '('

			if !isStatic {
				receiver = p.selfReceiver()

				token = p.lexer.peek()
				if receiver != nil {
					if token.Type == Comma {
						p.lexer.consume() // consume ','
						params = parseDelimSeq(p, CloseParen, Comma, p.param)
					}
				} else if token.Type != CloseParen {
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
			Name:     name,
			Fn:       ast.NewFuncExpr(lifetimeParams, typeParams, params, nil, nil, false, body, span),
			Receiver: receiver,
			Static:   isStatic,
			Private:  isPrivate,
			Span_:    span,
		}
	}

	if next.Type == OpenParen {
		// Method
		p.lexer.consume() // consume '('

		receiver := p.selfReceiver()

		params := []*ast.Param{}
		if isStatic {
			// Static methods have no receiver. If the user wrote one
			// anyway (`static foo(self)`, `static foo(mut self)`,
			// `static foo<'a>('a self)`), report it — silently dropping
			// would leave the user thinking `self` was meaningful.
			if receiver != nil {
				p.reportError(receiver.Span_, "static methods cannot have a `self` receiver")
			}
			receiver = nil
			params = parseDelimSeq(p, CloseParen, Comma, p.param)
		} else {
			token = p.lexer.peek()
			if token.Type == Comma {
				p.lexer.consume() // consume ','
				params = parseDelimSeq(p, CloseParen, Comma, p.param)
			} else if receiver == nil && token.Type != CloseParen {
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
			Name:     name,
			Fn:       ast.NewFuncExpr(lifetimeParams, typeParams, params, returnType, throwsType, isAsync, body, span),
			Receiver: receiver,
			Static:   isStatic,
			Private:  isPrivate,
			Span_:    span,
		}
	} else {
		// Field
		//
		// Grammar:
		//   name : T               — type annotation (required)
		//   name : T = expr        — static fields only; the initializer
		//                            is rejected by the checker on
		//                            instance fields. Instance fields are
		//                            initialized in the constructor body.
		for _, lp := range lifetimeParams {
			p.reportError(lp.Span(),
				"lifetime parameters are not supported in this context")
		}
		var typeAnn ast.TypeAnn
		var value ast.Expr
		isOptional := false

		next = p.lexer.peek()

		// `name?: T` declares an optional field. The `?` binds to the
		// name and must precede the type annotation.
		if next.Type == Question {
			p.lexer.consume()
			// We still set isOptional even when isStatic — downstream
			// consumers (requiredFieldNames, synthesizeConstructorElem)
			// short-circuit on Static first, so leaving the bit set keeps
			// error recovery consistent rather than depending on the
			// order of these guards.
			isOptional = true
			if isStatic {
				p.reportError(next.Span, "Static fields cannot be optional")
			}
			next = p.lexer.peek()
		}

		if next.Type == Colon {
			p.lexer.consume()
			typeAnn = p.typeAnn()
		} else if !isStatic {
			// Static fields may omit the annotation when an initializer
			// is present; the checker infers the type from the
			// initializer expression.
			p.reportError(next.Span, "Class fields require a type annotation (e.g. `name: T`)")
		}

		// Optional initializer (`= expr`). Whether it's allowed depends on
		// the field being static, which the checker enforces — keeping
		// the grammar permissive lets the parser recover after an
		// erroneous instance-field initializer.
		if p.lexer.peek().Type == Equal {
			p.lexer.consume()
			value = p.expr()
		}

		// TODO: report an error if `isAsync` is true
		span := ast.Span{Start: start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return &ast.FieldElem{
			Name:     name,
			Type:     typeAnn,
			Value:    value,
			Static:   isStatic,
			Private:  isPrivate,
			Readonly: isReadonly,
			Optional: isOptional,
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

	// Parse optional lifetime + type parameters
	lifetimeParams, typeParams := p.maybeLifetimeAndTypeParams()

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
		return ast.NewInterfaceDecl(ident, lifetimeParams, typeParams, extends, objType, export, declare, span)
	}
	p.lexer.consume() // consume '{'
	elems := parseDelimSeq(p, CloseBrace, Comma, p.objTypeAnnElem)
	end := p.expect(CloseBrace, AlwaysConsume)
	objType := ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))

	span := ast.NewSpan(start, end, p.lexer.source.ID)
	decl := ast.NewInterfaceDecl(ident, lifetimeParams, typeParams, extends, objType, export, declare, span)
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

package dts_parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// Phase 4: Object & Interface Types
// ============================================================================

// parseObjectType parses an object type literal: { prop: Type; method(): void }
func (p *DtsParser) parseObjectType() TypeAnn {
	start := p.expect(OpenBrace)
	if start == nil {
		return nil
	}

	members := []InterfaceMember{}

	// Parse members
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		// Skip comments before member
		for p.peek().Type == LineComment || p.peek().Type == BlockComment {
			p.consume()
		}

		// Check again after skipping comments
		if p.peek().Type == CloseBrace || p.peek().Type == EndOfFile {
			break
		}

		member := p.parseInterfaceMember()
		if member != nil {
			members = append(members, member)
		} else {
			// Skip to next member or closing brace on error
			p.skipToNextMember()
		}

		// Consume optional separator (comma or semicolon)
		if p.peek().Type == Comma || p.peek().Type == Semicolon {
			p.consume()
		}
	}

	end := p.expect(CloseBrace)
	if end == nil {
		// Return what we have even if closing brace is missing
		span := ast.Span{
			Start:    start.Span.Start,
			End:      start.Span.End,
			SourceID: start.Span.SourceID,
		}
		return &ObjectType{Members: members, span: span}
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      end.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &ObjectType{Members: members, span: span}
}

// parseInterfaceMember parses a single member of an interface or object type
func (p *DtsParser) parseInterfaceMember() InterfaceMember {
	token := p.peek()

	// Handle readonly modifier for property signatures
	readonly := false
	if token.Type == Readonly {
		readonly = true
		p.consume()
		token = p.peek()
	}

	// Check for special signatures
	switch token.Type {
	case LessThan:
		// Call signature with type parameters: <T>(params): Type
		return p.parseCallSignature()

	case OpenParen:
		// Call signature: (params): Type
		return p.parseCallSignature()

	case New:
		// Constructor signature: new (params): Type
		return p.parseConstructSignature()

	case OpenBracket:
		// Could be index signature [key: string]: Type or computed property [expr]: Type
		// Try index signature first (it's more restrictive)
		savedState := p.saveState()
		indexSig := p.tryParseIndexSignature(readonly)
		if indexSig != nil {
			return indexSig
		}
		// Not an index signature, restore and parse as computed property key
		p.restoreState(savedState)
		return p.parsePropertyOrMethodSignature(readonly)

	case Get:
		// Try to parse as getter signature first (get prop(): Type)
		// If that fails, treat 'get' as a regular property/method name
		savedState := p.saveState()
		getterSig := p.tryParseGetterSignature()
		if getterSig != nil {
			return getterSig
		}
		// Not a getter, restore and parse as property/method named 'get'
		p.restoreState(savedState)
		return p.parsePropertyOrMethodSignature(readonly)

	case Set:
		// Try to parse as setter signature first (set prop(value: Type))
		// If that fails, treat 'set' as a regular property/method name
		savedState := p.saveState()
		setterSig := p.tryParseSetterSignature()
		if setterSig != nil {
			return setterSig
		}
		// Not a setter, restore and parse as property/method named 'set'
		p.restoreState(savedState)
		return p.parsePropertyOrMethodSignature(readonly)

	default:
		// Property or method signature
		return p.parsePropertyOrMethodSignature(readonly)
	}
}

// parseCallSignature parses a call signature: <T>(params): Type or (params): Type
func (p *DtsParser) parseCallSignature() InterfaceMember {
	startSpan := p.peek().Span

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
		if len(typeParams) > 0 {
			startSpan = typeParams[0].Span()
		}
	}

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		returnType = p.parseReturnType()
		if returnType == nil {
			p.reportError(p.peek().Span, "Expected return type after ':'")
		}
	}

	endSpan := p.peek().Span
	if returnType != nil {
		endSpan = returnType.Span()
	} else if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &CallSignature{
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		span:       span,
	}
}

// parseConstructSignature parses a constructor signature: new (params): Type
func (p *DtsParser) parseConstructSignature() InterfaceMember {
	start := p.expect(New)
	if start == nil {
		return nil
	}

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		returnType = p.parseTypeAnn()
		if returnType == nil {
			p.reportError(p.peek().Span, "Expected return type after ':'")
		}
	}

	endSpan := p.peek().Span
	if returnType != nil {
		endSpan = returnType.Span()
	} else if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &ConstructSignature{
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		span:       span,
	}
}

// tryParseIndexSignature attempts to parse an index signature without reporting errors
// Returns nil if it doesn't match the pattern: [identifier: Type]: ValueType
func (p *DtsParser) tryParseIndexSignature(readonly bool) InterfaceMember {
	// Must start with '['
	if p.peek().Type != OpenBracket {
		return nil
	}
	start := p.consume()

	// Must be followed by an identifier (not an expression)
	if p.lexer.peekIdent() == nil {
		return nil
	}
	keyName := p.parseIdent()
	if keyName == nil {
		return nil
	}

	// Must have ':' after identifier (this distinguishes from computed keys)
	if p.peek().Type != Colon {
		return nil
	}
	p.consume()

	// Parse key type
	keyType := p.parseTypeAnn()
	if keyType == nil {
		return nil
	}

	// Must have ']'
	if p.peek().Type != CloseBracket {
		return nil
	}
	p.consume()

	// Must have ':' after ']'
	if p.peek().Type != Colon {
		return nil
	}
	p.consume()

	// Parse value type
	valueType := p.parseTypeAnn()
	if valueType == nil {
		return nil
	}

	endSpan := valueType.Span()
	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &IndexSignature{
		KeyName:   keyName,
		KeyType:   keyType,
		ValueType: valueType,
		Readonly:  readonly,
		span:      span,
	}
}

// tryParseGetterSignature attempts to parse a getter signature without reporting errors
// Returns nil if it doesn't match the getter pattern: get prop(): Type
func (p *DtsParser) tryParseGetterSignature() InterfaceMember {
	// Must start with 'get'
	if p.peek().Type != Get {
		return nil
	}
	start := p.consume()

	// If followed by ?, (, or <, then 'get' is a property name, not an accessor
	token := p.peek()
	if token.Type == Question || token.Type == OpenParen || token.Type == LessThan {
		return nil
	}

	// Must be followed by a property key (not '(' or '<')
	identToken := p.lexer.peekIdent()
	if identToken == nil && token.Type != StrLit && token.Type != NumLit && token.Type != OpenBracket {
		return nil
	}

	name := p.parsePropertyKey()
	if name == nil {
		return nil
	}

	// Must have '()'
	if p.peek().Type != OpenParen {
		return nil
	}
	p.consume()

	if p.peek().Type != CloseParen {
		return nil
	}
	p.consume()

	// Parse optional return type
	var returnType TypeAnn
	if p.peek().Type == Colon {
		p.consume()
		returnType = p.parseTypeAnn()
	}

	endSpan := name.Span()
	if returnType != nil {
		endSpan = returnType.Span()
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &GetterSignature{
		Name:       name,
		ReturnType: returnType,
		span:       span,
	}
}

// tryParseSetterSignature attempts to parse a setter signature without reporting errors
// Returns nil if it doesn't match the setter pattern: set prop(value: Type)
func (p *DtsParser) tryParseSetterSignature() InterfaceMember {
	// Must start with 'set'
	if p.peek().Type != Set {
		return nil
	}
	start := p.consume()

	// If followed by ?, (, or <, then 'set' is a property name, not an accessor
	token := p.peek()
	if token.Type == Question || token.Type == OpenParen || token.Type == LessThan {
		return nil
	}

	// Must be followed by a property key (not '(' or '<')
	identToken := p.lexer.peekIdent()
	if identToken == nil && token.Type != StrLit && token.Type != NumLit && token.Type != OpenBracket {
		return nil
	}

	name := p.parsePropertyKey()
	if name == nil {
		return nil
	}

	// Must have '(param)'
	if p.peek().Type != OpenParen {
		return nil
	}
	p.consume()

	param := p.parseParam()
	if param == nil {
		return nil
	}

	if p.peek().Type != CloseParen {
		return nil
	}
	closeParen := p.consume()

	span := ast.Span{
		Start:    start.Span.Start,
		End:      closeParen.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &SetterSignature{
		Name:  name,
		Param: param,
		span:  span,
	}
}

// parsePropertyOrMethodSignature parses a property or method signature
// Property: prop?: Type
// Method: method<T>(params): Type
func (p *DtsParser) parsePropertyOrMethodSignature(readonly bool) InterfaceMember {
	startSpan := p.peek().Span

	// Parse property name
	name := p.parsePropertyKey()
	if name == nil {
		return nil
	}

	// Check for optional marker
	optional := false
	if p.peek().Type == Question {
		optional = true
		p.consume() // consume '?'
	}

	// Check if this is a method signature (has type parameters or parameter list)
	if p.peek().Type == LessThan || p.peek().Type == OpenParen {
		return p.parseMethodSignatureAfterName(name, optional, startSpan)
	}

	// Otherwise, it's a property signature
	// Parse type annotation
	var typeAnn TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		typeAnn = p.parseTypeAnn()
		if typeAnn == nil {
			p.reportError(p.peek().Span, "Expected type annotation")
		}
	}

	endSpan := name.Span()
	if typeAnn != nil {
		endSpan = typeAnn.Span()
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &PropertySignature{
		Name:     name,
		TypeAnn:  typeAnn,
		Optional: optional,
		Readonly: readonly,
		span:     span,
	}
}

// parseMethodSignatureAfterName parses a method signature after the name and optional marker
func (p *DtsParser) parseMethodSignatureAfterName(name PropertyKey, optional bool, startSpan ast.Span) InterfaceMember {
	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		returnType = p.parseReturnType()
		if returnType == nil {
			p.reportError(p.peek().Span, "Expected return type after ':'")
		}
	}

	endSpan := name.Span()
	if returnType != nil {
		endSpan = returnType.Span()
	} else if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &MethodSignature{
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		Optional:   optional,
		span:       span,
	}
}

// parsePropertyKey parses a property key (identifier, string, number, or computed)
func (p *DtsParser) parsePropertyKey() PropertyKey {
	token := p.peek()

	switch token.Type {
	case Identifier, String, Number, Boolean, Bigint:
		return p.parseIdent()

	// Allow 'get' and 'set' as property names (contextual keywords)
	case Get:
		p.consume()
		return NewIdent("get", token.Span)

	case Set:
		p.consume()
		return NewIdent("set", token.Span)

	// Allow other keywords to be used as property names
	// This is common in TypeScript where methods can be named after reserved words
	// e.g., Promise.catch(), Array.from(), etc.
	case Catch, Try, Throw, Throws, Return, If, Else, Do, For,
		New, Function, Var, Let, Const, Class, Extends,
		Implements, Interface, Private, Protected, Public, Static, Yield, Await, Async,
		Enum, Export, Import, As, From, Null, True, False,
		Typeof, In, Namespace, ModuleKeyword, Declare, Type, Readonly,
		Never, Unknown, Any, Undefined, Symbol, Unique, Abstract, Is, Asserts, Infer, Keyof,
		Fn, Gen, Mut, Val, Void, Object:
		p.consume()
		return NewIdent(token.Value, token.Span)

	case StrLit:
		p.consume()
		return &StringLiteral{Value: token.Value, span: token.Span}

	case NumLit:
		p.consume()
		// Parse the numeric value
		value := 0.0
		if val, err := strconv.ParseFloat(token.Value, 64); err == nil {
			value = val
		} else {
			p.reportError(token.Span, "Invalid number literal")
		}
		return &NumberLiteral{Value: value, span: token.Span}

	case OpenBracket:
		// Computed property key: [expr]
		return p.parseComputedPropertyKey()

	default:
		p.reportError(token.Span, "Expected property key")
		return nil
	}
}

// parseComputedPropertyKey parses a computed property key: [expr]
func (p *DtsParser) parseComputedPropertyKey() PropertyKey {
	start := p.expect(OpenBracket)
	if start == nil {
		return nil
	}

	// In .d.ts files, computed keys use type expressions
	expr := p.parseTypeAnn()
	if expr == nil {
		p.reportError(p.peek().Span, "Expected type expression in computed key")
		return nil
	}

	end := p.expect(CloseBracket)
	if end == nil {
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      end.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &ComputedKey{Expr: expr, span: span}
}

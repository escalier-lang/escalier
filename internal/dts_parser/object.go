package dts_parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

// ============================================================================
// Phase 4: Object & Interface Types
// ============================================================================

// parseObjectType parses an object type literal: { prop: Type; method(): void }
func (p *DtsParser) parseObjectType() TypeAnn {
	start := p.expect(parser.OpenBrace)
	if start == nil {
		return nil
	}

	members := []InterfaceMember{}

	// Parse members
	for p.peek().Type != parser.CloseBrace && p.peek().Type != parser.EndOfFile {
		member := p.parseInterfaceMember()
		if member != nil {
			members = append(members, member)
		} else {
			// Skip to next member or closing brace on error
			p.skipToNextMember()
		}

		// Consume optional separator (comma)
		if p.peek().Type == parser.Comma {
			p.consume()
		}
	}

	end := p.expect(parser.CloseBrace)
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
	if token.Type == parser.Readonly {
		readonly = true
		p.consume()
		token = p.peek()
	}

	// Check for special signatures
	switch token.Type {
	case parser.OpenParen:
		// Call signature: (params): Type
		return p.parseCallSignature()

	case parser.New:
		// Constructor signature: new (params): Type
		return p.parseConstructSignature()

	case parser.OpenBracket:
		// Index signature: [key: string]: Type
		return p.parseIndexSignature(readonly)

	case parser.Get:
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

	case parser.Set:
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
	if p.peek().Type == parser.LessThan {
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
	if p.peek().Type == parser.Colon {
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
	start := p.expect(parser.New)
	if start == nil {
		return nil
	}

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == parser.LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == parser.Colon {
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

// parseIndexSignature parses an index signature: [key: string]: Type
func (p *DtsParser) parseIndexSignature(readonly bool) InterfaceMember {
	start := p.expect(parser.OpenBracket)
	if start == nil {
		return nil
	}

	// Parse key name (identifier)
	keyName := p.parseIdent()
	if keyName == nil {
		p.reportError(p.peek().Span, "Expected key name in index signature")
		return nil
	}

	// Expect ':'
	if p.expect(parser.Colon) == nil {
		return nil
	}

	// Parse key type (must be string, number, or symbol)
	keyType := p.parseTypeAnn()
	if keyType == nil {
		p.reportError(p.peek().Span, "Expected key type in index signature")
		return nil
	}

	// Expect ']'
	if p.expect(parser.CloseBracket) == nil {
		return nil
	}

	// Expect ':'
	if p.expect(parser.Colon) == nil {
		return nil
	}

	// Parse value type
	valueType := p.parseTypeAnn()
	if valueType == nil {
		p.reportError(p.peek().Span, "Expected value type in index signature")
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

// parseGetterSignature parses a getter signature: get prop(): Type
func (p *DtsParser) parseGetterSignature() InterfaceMember {
	start := p.expect(parser.Get)
	if start == nil {
		return nil
	}

	// Parse property name
	name := p.parsePropertyKey()
	if name == nil {
		p.reportError(p.peek().Span, "Expected property name after 'get'")
		return nil
	}

	// Expect '('
	if p.expect(parser.OpenParen) == nil {
		return nil
	}

	// Expect ')' (getters have no parameters)
	if p.expect(parser.CloseParen) == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == parser.Colon {
		p.consume() // consume ':'
		returnType = p.parseTypeAnn()
		if returnType == nil {
			p.reportError(p.peek().Span, "Expected return type after ':'")
		}
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

// parseSetterSignature parses a setter signature: set prop(value: Type)
func (p *DtsParser) parseSetterSignature() InterfaceMember {
	start := p.expect(parser.Set)
	if start == nil {
		return nil
	}

	// Parse property name
	name := p.parsePropertyKey()
	if name == nil {
		p.reportError(p.peek().Span, "Expected property name after 'set'")
		return nil
	}

	// Expect '('
	if p.expect(parser.OpenParen) == nil {
		return nil
	}

	// Parse parameter (setters have exactly one parameter)
	param := p.parseParam()
	if param == nil {
		p.reportError(p.peek().Span, "Expected parameter in setter")
	}

	// Expect ')'
	closeParen := p.expect(parser.CloseParen)
	if closeParen == nil {
		return nil
	}

	endSpan := closeParen.Span
	if param != nil {
		endSpan = param.Span()
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &SetterSignature{
		Name:  name,
		Param: param,
		span:  span,
	}
}

// tryParseGetterSignature attempts to parse a getter signature without reporting errors
// Returns nil if it doesn't match the getter pattern: get prop(): Type
func (p *DtsParser) tryParseGetterSignature() InterfaceMember {
	// Must start with 'get'
	if p.peek().Type != parser.Get {
		return nil
	}
	start := p.consume()

	// Must be followed by a property key (not '(' or '<')
	token := p.peek()
	if token.Type != parser.Identifier && token.Type != parser.StrLit && token.Type != parser.NumLit && token.Type != parser.OpenBracket {
		return nil
	}

	name := p.parsePropertyKey()
	if name == nil {
		return nil
	}

	// Must have '()'
	if p.peek().Type != parser.OpenParen {
		return nil
	}
	p.consume()

	if p.peek().Type != parser.CloseParen {
		return nil
	}
	p.consume()

	// Parse optional return type
	var returnType TypeAnn
	if p.peek().Type == parser.Colon {
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
	if p.peek().Type != parser.Set {
		return nil
	}
	start := p.consume()

	// Must be followed by a property key (not '(' or '<')
	token := p.peek()
	if token.Type != parser.Identifier && token.Type != parser.StrLit && token.Type != parser.NumLit && token.Type != parser.OpenBracket {
		return nil
	}

	name := p.parsePropertyKey()
	if name == nil {
		return nil
	}

	// Must have '(param)'
	if p.peek().Type != parser.OpenParen {
		return nil
	}
	p.consume()

	param := p.parseParam()
	if param == nil {
		return nil
	}

	if p.peek().Type != parser.CloseParen {
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
	if p.peek().Type == parser.Question {
		optional = true
		p.consume() // consume '?'
	}

	// Check if this is a method signature (has type parameters or parameter list)
	if p.peek().Type == parser.LessThan || p.peek().Type == parser.OpenParen {
		return p.parseMethodSignatureAfterName(name, optional, startSpan)
	}

	// Otherwise, it's a property signature
	// Parse type annotation
	var typeAnn TypeAnn
	if p.peek().Type == parser.Colon {
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
	if p.peek().Type == parser.LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == parser.Colon {
		p.consume() // consume ':'
		returnType = p.parseTypeAnn()
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
	case parser.Identifier:
		return p.parseIdent()

	// Allow 'get' and 'set' as property names (contextual keywords)
	case parser.Get:
		p.consume()
		return NewIdent("get", token.Span)

	case parser.Set:
		p.consume()
		return NewIdent("set", token.Span)

	case parser.StrLit:
		p.consume()
		return &StringLiteral{Value: token.Value, span: token.Span}

	case parser.NumLit:
		p.consume()
		// Parse the numeric value
		value := 0.0
		if val, err := strconv.ParseFloat(token.Value, 64); err == nil {
			value = val
		} else {
			p.reportError(token.Span, "Invalid number literal")
		}
		return &NumberLiteral{Value: value, span: token.Span}

	case parser.OpenBracket:
		// Computed property key: [expr]
		return p.parseComputedPropertyKey()

	default:
		p.reportError(token.Span, "Expected property key")
		return nil
	}
}

// parseComputedPropertyKey parses a computed property key: [expr]
func (p *DtsParser) parseComputedPropertyKey() PropertyKey {
	start := p.expect(parser.OpenBracket)
	if start == nil {
		return nil
	}

	// In .d.ts files, computed keys use type expressions
	expr := p.parseTypeAnn()
	if expr == nil {
		p.reportError(p.peek().Span, "Expected type expression in computed key")
		return nil
	}

	end := p.expect(parser.CloseBracket)
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

// skipToNextMember skips tokens until we reach a potential member start or closing brace
func (p *DtsParser) skipToNextMember() {
	for {
		token := p.peek()

		// Stop at closing brace or EOF
		if token.Type == parser.CloseBrace || token.Type == parser.EndOfFile {
			break
		}

		// Stop at comma (member separator)
		if token.Type == parser.Comma {
			p.consume()
			break
		}

		// Stop at potential member starts
		if token.Type == parser.Identifier ||
			token.Type == parser.Readonly ||
			token.Type == parser.OpenParen ||
			token.Type == parser.New ||
			token.Type == parser.OpenBracket ||
			token.Type == parser.Get ||
			token.Type == parser.Set {
			break
		}

		p.consume()
	}
}

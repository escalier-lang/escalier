package dts_parser

import "github.com/escalier-lang/escalier/internal/ast"

// ============================================================================
// Class Member Parsing
// ============================================================================

// parseClassMember parses a single class member
func (p *DtsParser) parseClassMember() ClassMember {
	// Parse modifiers
	modifiers := p.parseClassModifiers()

	token := p.peek()

	// Constructor
	if token.Type == Identifier && token.Value == "constructor" {
		return p.parseConstructorDeclaration(modifiers)
	}

	// Index signature
	if token.Type == OpenBracket {
		return p.parseClassIndexSignature(modifiers)
	}

	// Getter
	if token.Type == Get {
		return p.parseGetterDeclaration(modifiers)
	}

	// Setter
	if token.Type == Set {
		return p.parseSetterDeclaration(modifiers)
	}

	// Property or method
	return p.parsePropertyOrMethodDeclaration(modifiers)
}

// parseClassModifiers parses access and other modifiers for class members
func (p *DtsParser) parseClassModifiers() Modifiers {
	mods := Modifiers{}

	for {
		token := p.peek()
		switch token.Type {
		case Public:
			mods.Public = true
			p.consume()
		case Private:
			mods.Private = true
			p.consume()
		case Protected:
			mods.Protected = true
			p.consume()
		case Static:
			mods.Static = true
			p.consume()
		case Readonly:
			mods.Readonly = true
			p.consume()
		case Abstract:
			mods.Abstract = true
			p.consume()
		case Async:
			mods.Async = true
			p.consume()
		default:
			return mods
		}
	}
}

// parseConstructorDeclaration parses: constructor(params)
func (p *DtsParser) parseConstructorDeclaration(mods Modifiers) ClassMember {
	startToken := p.peek()
	p.consume() // consume 'constructor'

	// Parse parameter list
	params := p.parseParams()
	if params == nil {
		return nil
	}

	endSpan := startToken.Span
	if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endSpan.End,
		SourceID: startToken.Span.SourceID,
	}

	return &ConstructorDecl{
		Params:    params,
		Modifiers: mods,
		span:      span,
	}
}

// parsePropertyOrMethodDeclaration parses a property or method declaration
func (p *DtsParser) parsePropertyOrMethodDeclaration(mods Modifiers) ClassMember {
	// Parse property/method name
	name := p.parsePropertyName()
	if name == nil {
		return nil
	}

	// Check if this is a method by looking for type parameters or parameters
	token := p.peek()

	// Method with type parameters: name<T>(...)
	if token.Type == LessThan {
		return p.parseMethodDeclarationWithName(name, mods)
	}

	// Method with parameters: name(...)
	if token.Type == OpenParen {
		return p.parseMethodDeclarationWithName(name, mods)
	}

	// Property: name: Type or name?: Type
	return p.parsePropertyDeclarationWithName(name, mods)
}

// parsePropertyName parses a property name (identifier, string, number, or computed)
func (p *DtsParser) parsePropertyName() PropertyKey {
	// Check for identifier first (including keywords used as identifiers)
	// peekIdent() treats all keywords as valid identifiers in this context
	if p.lexer.peekIdent() != nil {
		return p.parseIdent()
	}

	token := p.peek()

	if token.Type == StrLit {
		p.consume()
		return &StringLiteral{
			Value: token.Value,
			span:  token.Span,
		}
	}

	if token.Type == NumLit {
		p.consume()
		return &NumberLiteral{
			Value: parseNumber(token.Value),
			span:  token.Span,
		}
	}

	if token.Type == OpenBracket {
		// Computed property name [expr]
		startToken := p.consume() // consume '['

		// Parse the type expression inside brackets
		expr := p.parseTypeAnn()
		if expr == nil {
			p.reportError(p.peek().Span, "Expected type expression in computed property name")
			return nil
		}

		endToken := p.expect(CloseBracket)
		if endToken == nil {
			return nil
		}

		return &ComputedKey{
			Expr: expr,
			span: ast.Span{
				Start:    startToken.Span.Start,
				End:      endToken.Span.End,
				SourceID: startToken.Span.SourceID,
			},
		}
	}

	p.reportError(token.Span, "Expected property name")
	return nil
}

// parsePropertyDeclarationWithName parses a property declaration with already-parsed name
func (p *DtsParser) parsePropertyDeclarationWithName(name PropertyKey, mods Modifiers) ClassMember {
	startSpan := name.Span()

	// Check for optional modifier
	optional := false
	if p.peek().Type == Question {
		optional = true
		p.consume()
	}

	// Parse type annotation
	var typeAnn TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		typeAnn = p.parseTypeAnn()
	}

	endSpan := startSpan
	if typeAnn != nil {
		endSpan = typeAnn.Span()
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &PropertyDecl{
		Name:      name,
		TypeAnn:   typeAnn,
		Optional:  optional,
		Modifiers: mods,
		span:      span,
	}
}

// parseMethodDeclarationWithName parses a method declaration with already-parsed name
func (p *DtsParser) parseMethodDeclarationWithName(name PropertyKey, mods Modifiers) ClassMember {
	startSpan := name.Span()

	// Check for optional modifier
	optional := false
	if p.peek().Type == Question {
		optional = true
		p.consume()
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
		returnType = p.parseReturnType()
	}

	endSpan := startSpan
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

	return &MethodDecl{
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		Optional:   optional,
		Modifiers:  mods,
		span:       span,
	}
}

// parseGetterDeclaration parses: get propertyName(): Type
func (p *DtsParser) parseGetterDeclaration(mods Modifiers) ClassMember {
	startToken := p.expect(Get)
	if startToken == nil {
		return nil
	}

	// Parse property name
	name := p.parsePropertyName()
	if name == nil {
		return nil
	}

	// Expect empty parameter list
	if p.expect(OpenParen) == nil {
		return nil
	}
	if p.expect(CloseParen) == nil {
		return nil
	}

	// Parse return type
	var returnType TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		returnType = p.parseTypeAnn()
	}

	endSpan := name.Span()
	if returnType != nil {
		endSpan = returnType.Span()
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endSpan.End,
		SourceID: startToken.Span.SourceID,
	}

	return &GetterDecl{
		Name:       name,
		ReturnType: returnType,
		Modifiers:  mods,
		span:       span,
	}
}

// parseSetterDeclaration parses: set propertyName(value: Type)
func (p *DtsParser) parseSetterDeclaration(mods Modifiers) ClassMember {
	startToken := p.expect(Set)
	if startToken == nil {
		return nil
	}

	// Parse property name
	name := p.parsePropertyName()
	if name == nil {
		return nil
	}

	// Parse parameter list (should have exactly one parameter)
	params := p.parseParams()
	if params == nil {
		return nil
	}

	endSpan := name.Span()
	if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endSpan.End,
		SourceID: startToken.Span.SourceID,
	}

	var param *Param
	if len(params) > 0 {
		param = params[0]
	}

	return &SetterDecl{
		Name:      name,
		Param:     param,
		Modifiers: mods,
		span:      span,
	}
}

// parseClassIndexSignature parses: [key: string]: Type
func (p *DtsParser) parseClassIndexSignature(mods Modifiers) ClassMember {
	startToken := p.expect(OpenBracket)
	if startToken == nil {
		return nil
	}

	// Parse key name
	keyName := p.parseIdent()
	if keyName == nil {
		return nil
	}

	// Expect ':'
	if p.expect(Colon) == nil {
		return nil
	}

	// Parse key type
	keyType := p.parseTypeAnn()
	if keyType == nil {
		return nil
	}

	// Expect ']'
	if p.expect(CloseBracket) == nil {
		return nil
	}

	// Expect ':'
	if p.expect(Colon) == nil {
		return nil
	}

	// Parse value type
	valueType := p.parseTypeAnn()
	if valueType == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      valueType.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	return &IndexSignature{
		KeyName:   keyName,
		KeyType:   keyType,
		ValueType: valueType,
		Readonly:  mods.Readonly,
		span:      span,
	}
}

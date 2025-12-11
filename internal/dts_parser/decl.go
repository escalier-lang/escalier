package dts_parser

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// Phase 6: Declarations
// ============================================================================

// parseStatement parses a top-level statement/declaration
func (p *DtsParser) parseStatement() Statement {
	token := p.peek()

	// Handle 'declare' keyword
	if token.Type == Declare {
		p.consume() // consume 'declare'
		return p.parseAmbientDeclaration()
	}

	// Handle 'export' keyword
	if token.Type == Export {
		return p.parseExportDeclaration()
	}

	// Handle 'import' keyword
	if token.Type == Import {
		return p.parseImportDeclaration()
	}

	// Parse top-level declarations without 'declare'
	return p.parseTopLevelDeclaration()
}

// parseAmbientDeclaration parses declarations after 'declare' keyword
func (p *DtsParser) parseAmbientDeclaration() Statement {
	token := p.peek()

	switch token.Type {
	case Var, Let:
		return p.parseVariableDeclaration()
	case Const:
		// Check if this is 'const enum'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'const'
		if p.peek().Type == Enum {
			p.lexer.RestoreState(nextToken)
			return p.parseEnumDeclaration()
		}
		p.lexer.RestoreState(nextToken)
		return p.parseVariableDeclaration()
	case Function:
		return p.parseFunctionDeclaration()
	case Class:
		return p.parseClassDeclaration()
	case Abstract:
		// Check if this is 'abstract class'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'abstract'
		if p.peek().Type == Class {
			p.lexer.RestoreState(nextToken)
			return p.parseClassDeclaration()
		}
		p.lexer.RestoreState(nextToken)
		p.reportError(token.Span, "Expected 'class' after 'abstract' in ambient declaration")
		return nil
	case Interface:
		return p.parseInterfaceDeclaration()
	case Type:
		return p.parseTypeAliasDeclaration()
	case Enum:
		return p.parseEnumDeclaration()
	case Namespace, ModuleKeyword:
		return p.parseNamespaceDeclaration()
	default:
		p.reportError(token.Span, "Expected variable, function, class, interface, type, enum, or namespace declaration after 'declare'")
		return nil
	}
}

// parseTopLevelDeclaration parses top-level declarations without 'declare'
func (p *DtsParser) parseTopLevelDeclaration() Statement {
	token := p.peek()

	switch token.Type {
	case Interface:
		return p.parseInterfaceDeclaration()
	case Type:
		return p.parseTypeAliasDeclaration()
	case Enum:
		return p.parseEnumDeclaration()
	case Const:
		// Check if this is 'const enum'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'const'
		if p.peek().Type == Enum {
			p.lexer.RestoreState(nextToken)
			return p.parseEnumDeclaration()
		}
		p.lexer.RestoreState(nextToken)
		p.reportError(token.Span, "Expected 'enum' after 'const' at top level")
		return nil
	case Abstract:
		// Check if this is 'abstract class'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'abstract'
		if p.peek().Type == Class {
			p.lexer.RestoreState(nextToken)
			return p.parseClassDeclaration()
		}
		p.lexer.RestoreState(nextToken)
		p.reportError(token.Span, "Expected 'class' after 'abstract' at top level")
		return nil
	case Namespace, ModuleKeyword:
		return p.parseNamespaceDeclaration()
	default:
		p.reportError(token.Span, "Expected interface, type, enum, or namespace declaration")
		return nil
	}
}

// ============================================================================
// Variable Declarations
// ============================================================================

// parseVariableDeclaration parses: var/let/const name: Type
func (p *DtsParser) parseVariableDeclaration() Statement {
	startToken := p.peek()
	readonly := false

	if startToken.Type == Const {
		readonly = true
	}

	// Consume var/let/const
	if startToken.Type != Var && startToken.Type != Let && startToken.Type != Const {
		p.reportError(startToken.Span, "Expected 'var', 'let', or 'const'")
		return nil
	}
	p.consume()

	// Parse identifier
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse optional type annotation
	var typeAnn TypeAnn
	if p.peek().Type == Colon {
		p.consume() // consume ':'
		typeAnn = p.parseTypeAnn()
		if typeAnn == nil {
			p.reportError(p.peek().Span, "Expected type annotation after ':'")
		}
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      name.Span().End,
		SourceID: startToken.Span.SourceID,
	}
	if typeAnn != nil {
		span.End = typeAnn.Span().End
	}

	return &DeclareVariable{
		Name:     name,
		TypeAnn:  typeAnn,
		Readonly: readonly,
		span:     span,
	}
}

// ============================================================================
// Function Declarations
// ============================================================================

// parseFunctionDeclaration parses: function name<T>(params): ReturnType
func (p *DtsParser) parseFunctionDeclaration() Statement {
	startToken := p.expect(Function)
	if startToken == nil {
		return nil
	}

	// Parse function name
	name := p.parseIdent()
	if name == nil {
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

	endSpan := name.Span()
	if returnType != nil {
		endSpan = returnType.Span()
	} else if len(params) > 0 {
		endSpan = params[len(params)-1].Span()
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endSpan.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareFunction{
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		span:       span,
	}
}

// ============================================================================
// Type Alias Declarations
// ============================================================================

// parseTypeAliasDeclaration parses: type Name<T> = Type
func (p *DtsParser) parseTypeAliasDeclaration() Statement {
	startToken := p.expect(Type)
	if startToken == nil {
		return nil
	}

	// Parse type alias name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
	}

	// Expect '='
	if p.expect(Equal) == nil {
		return nil
	}

	// Parse type annotation
	typeAnn := p.parseTypeAnn()
	if typeAnn == nil {
		p.reportError(p.peek().Span, "Expected type after '='")
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      typeAnn.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareTypeAlias{
		Name:       name,
		TypeParams: typeParams,
		TypeAnn:    typeAnn,
		span:       span,
	}
}

// ============================================================================
// Interface Declarations
// ============================================================================

// parseInterfaceDeclaration parses: interface Name<T> extends Base { ... }
func (p *DtsParser) parseInterfaceDeclaration() Statement {
	startToken := p.expect(Interface)
	if startToken == nil {
		return nil
	}

	// Parse interface name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse optional 'extends' clause
	var extends []TypeAnn
	if p.peek().Type == Extends {
		p.consume() // consume 'extends'

		// Parse first extended interface
		ext := p.parseTypeAnn()
		if ext != nil {
			extends = append(extends, ext)
		}

		// Parse additional extended interfaces
		for p.peek().Type == Comma {
			p.consume() // consume ','
			ext := p.parseTypeAnn()
			if ext != nil {
				extends = append(extends, ext)
			}
		}
	}

	// Parse interface body
	if p.expect(OpenBrace) == nil {
		return nil
	}

	members := []InterfaceMember{}
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		member := p.parseInterfaceMember()
		if member != nil {
			members = append(members, member)
		} else {
			// Skip to next member or closing brace on error
			p.skipToNextMember()
		}

		// Consume optional separator (semicolon or comma)
		if p.peek().Type == Comma {
			p.consume()
		}
	}

	endToken := p.expect(CloseBrace)
	if endToken == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareInterface{
		Name:       name,
		TypeParams: typeParams,
		Extends:    extends,
		Members:    members,
		span:       span,
	}
}

// skipToNextMember skips tokens until we find a likely start of next member or closing brace
func (p *DtsParser) skipToNextMember() {
	for {
		token := p.peek()
		if token.Type == CloseBrace || token.Type == EndOfFile {
			return
		}
		// Look for likely starts of members
		if token.Type == Identifier || token.Type == OpenBracket ||
			token.Type == Readonly || token.Type == Get || token.Type == Set ||
			token.Type == New || token.Type == OpenParen {
			return
		}
		p.consume()
	}
}

// ============================================================================
// Enum Declarations
// ============================================================================

// parseEnumDeclaration parses: enum Name { ... } or const enum Name { ... }
func (p *DtsParser) parseEnumDeclaration() Statement {
	// Check for 'const' modifier
	isConst := false
	startToken := p.peek()

	if startToken.Type == Const {
		isConst = true
		p.consume()
		startToken = p.peek()
	}

	// Expect 'enum'
	if p.expect(Enum) == nil {
		return nil
	}

	// Parse enum name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse enum body
	if p.expect(OpenBrace) == nil {
		return nil
	}

	members := []*EnumMember{}
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		member := p.parseEnumMember()
		if member != nil {
			members = append(members, member)
		}

		// Consume optional separator (comma)
		if p.peek().Type == Comma {
			p.consume()
		} else if p.peek().Type != CloseBrace {
			// If not a comma and not closing brace, it's likely an error
			break
		}
	}

	endToken := p.expect(CloseBrace)
	if endToken == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareEnum{
		Name:    name,
		Members: members,
		Const:   isConst,
		span:    span,
	}
}

// parseEnumMember parses a single enum member: Name or Name = Value
func (p *DtsParser) parseEnumMember() *EnumMember {
	// Parse member name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse optional initializer
	var value Literal
	if p.peek().Type == Equal {
		p.consume() // consume '='

		token := p.peek()
		switch token.Type {
		case NumLit:
			p.consume()
			value = &NumberLiteral{Value: parseNumber(token.Value), span: token.Span}
		case StrLit:
			p.consume()
			value = &StringLiteral{Value: token.Value, span: token.Span}
		default:
			p.reportError(token.Span, "Expected number or string literal for enum member value")
		}
	}

	span := name.Span()
	if value != nil {
		span.End = value.Span().End
	}

	return &EnumMember{
		Name:  name,
		Value: value,
		span:  span,
	}
}

// parseNumber parses a number string into a float64
func parseNumber(s string) float64 {
	// Simple number parsing - could be enhanced
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	if err != nil {
		return 0
	}
	return result
}

// ============================================================================
// Class Declarations
// ============================================================================

// parseClassDeclaration parses: class Name<T> extends Base implements I1, I2 { ... }
func (p *DtsParser) parseClassDeclaration() Statement {
	// Check for 'abstract' modifier
	abstract := false
	startToken := p.peek()

	if startToken.Type == Abstract {
		abstract = true
		p.consume()
		startToken = p.peek()
	}

	// Expect 'class'
	if p.expect(Class) == nil {
		return nil
	}

	// Parse class name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse optional 'extends' clause
	var extends TypeAnn
	if p.peek().Type == Extends {
		p.consume() // consume 'extends'
		extends = p.parseTypeAnn()
		if extends == nil {
			p.reportError(p.peek().Span, "Expected base class type after 'extends'")
		}
	}

	// Parse optional 'implements' clause
	var implements []TypeAnn
	if p.peek().Type == Implements {
		p.consume() // consume 'implements'

		// Parse first implemented interface
		impl := p.parseTypeAnn()
		if impl != nil {
			implements = append(implements, impl)
		}

		// Parse additional implemented interfaces
		for p.peek().Type == Comma {
			p.consume() // consume ','
			impl := p.parseTypeAnn()
			if impl != nil {
				implements = append(implements, impl)
			}
		}
	}

	// Parse class body
	if p.expect(OpenBrace) == nil {
		return nil
	}

	members := []ClassMember{}
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		member := p.parseClassMember()
		if member != nil {
			members = append(members, member)
		} else {
			// Skip to next member or closing brace on error
			p.skipToNextMember()
		}

		// Consume optional separator (semicolon or comma)
		if p.peek().Type == Comma {
			p.consume()
		}
	}

	endToken := p.expect(CloseBrace)
	if endToken == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareClass{
		Name:       name,
		TypeParams: typeParams,
		Extends:    extends,
		Implements: implements,
		Members:    members,
		Abstract:   abstract,
		span:       span,
	}
}

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

// parsePropertyName parses a property name (identifier or computed)
func (p *DtsParser) parsePropertyName() PropertyKey {
	token := p.peek()

	if token.Type == Identifier {
		return p.parseIdent()
	}

	if token.Type == OpenBracket {
		// Computed property name [expr]
		// For now, just parse simple identifier inside brackets
		p.consume() // consume '['
		ident := p.parseIdent()
		p.expect(CloseBracket)
		return ident
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
		returnType = p.parseTypeAnn()
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

	return &SetterDecl{
		Name:      name,
		Param:     params[0], // Should have exactly one param
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

// ============================================================================
// Namespace Declarations
// ============================================================================

// parseNamespaceDeclaration parses: namespace Name { ... } or module Name { ... }
func (p *DtsParser) parseNamespaceDeclaration() Statement {
	startToken := p.peek()

	// Consume 'namespace' or 'module'
	if startToken.Type != Namespace && startToken.Type != ModuleKeyword {
		p.reportError(startToken.Span, "Expected 'namespace' or 'module'")
		return nil
	}
	p.consume()

	// Check if this is an ambient module declaration (module with string literal)
	if p.peek().Type == StrLit {
		return p.parseAmbientModuleDeclaration(startToken)
	}

	// Parse namespace name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	// Parse namespace body
	if p.expect(OpenBrace) == nil {
		return nil
	}

	statements := []Statement{}
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		stmt := p.parseStatement()
		if stmt != nil {
			statements = append(statements, stmt)
		} else {
			// Skip token on error to avoid infinite loop
			p.consume()
		}
	}

	endToken := p.expect(CloseBrace)
	if endToken == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareNamespace{
		Name:       name,
		Statements: statements,
		span:       span,
	}
}

// parseAmbientModuleDeclaration parses: declare module "name" { ... }
func (p *DtsParser) parseAmbientModuleDeclaration(startToken *Token) Statement {
	// Parse module name (string literal)
	nameToken := p.expect(StrLit)
	if nameToken == nil {
		return nil
	}

	// Parse module body
	if p.expect(OpenBrace) == nil {
		return nil
	}

	statements := []Statement{}
	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		stmt := p.parseStatement()
		if stmt != nil {
			statements = append(statements, stmt)
		} else {
			// Skip token on error to avoid infinite loop
			p.consume()
		}
	}

	endToken := p.expect(CloseBrace)
	if endToken == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      endToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &DeclareModule{
		Name:       nameToken.Value,
		Statements: statements,
		span:       span,
	}
}

// ============================================================================
// Import/Export Declarations (Stub)
// ============================================================================

// parseImportDeclaration parses import statements (to be implemented in Phase 7)
func (p *DtsParser) parseImportDeclaration() Statement {
	// TODO: Implement in Phase 7
	p.reportError(p.peek().Span, "Import declarations not yet implemented")
	return nil
}

// parseExportDeclaration parses export statements (to be implemented in Phase 7)
func (p *DtsParser) parseExportDeclaration() Statement {
	// TODO: Implement in Phase 7
	p.reportError(p.peek().Span, "Export declarations not yet implemented")
	return nil
}

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

		// Allow const variable declarations inside ambient namespaces
		if p.inAmbientContext {
			return p.parseVariableDeclaration()
		}

		p.reportError(token.Span, "Expected 'enum' after 'const' at top level")
		return nil
	case Var, Let:
		// Allow var/let variable declarations inside ambient namespaces
		if p.inAmbientContext {
			return p.parseVariableDeclaration()
		}
		p.reportError(token.Span, "Variable declarations require 'declare' keyword at top level")
		return nil
	case Function:
		// Allow function declarations inside ambient namespaces
		if p.inAmbientContext {
			return p.parseFunctionDeclaration()
		}
		p.reportError(token.Span, "Function declarations require 'declare' keyword at top level")
		return nil
	case Class:
		// Allow class declarations inside ambient namespaces
		if p.inAmbientContext {
			return p.parseClassDeclaration()
		}
		p.reportError(token.Span, "Class declarations require 'declare' keyword at top level")
		return nil
	case Abstract:
		// 'abstract class' is allowed at top level without 'declare'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'abstract'
		if p.peek().Type == Class {
			p.lexer.RestoreState(nextToken)
			return p.parseClassDeclaration()
		}
		p.lexer.RestoreState(nextToken)
		p.reportError(token.Span, "Expected 'class' after 'abstract'")
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

	// Consume optional semicolon
	if p.peek().Type == Semicolon {
		semiToken := p.consume()
		span.End = semiToken.Span.End
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
		Start:    startToken.Span.Start,
		End:      endSpan.End,
		SourceID: startToken.Span.SourceID,
	}

	// Consume optional semicolon
	if p.peek().Type == Semicolon {
		semiToken := p.consume()
		span.End = semiToken.Span.End
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

	// Consume optional semicolon
	if p.peek().Type == Semicolon {
		semiToken := p.consume()
		span.End = semiToken.Span.End
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

		// Consume optional separator (semicolon or comma)
		if p.peek().Type == Comma || p.peek().Type == Semicolon {
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
			token.Type == New || token.Type == OpenParen ||
			token.Type == Public || token.Type == Private || token.Type == Protected ||
			token.Type == Static || token.Type == Abstract || token.Type == Async {
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
			// If parsing failed, consume at least one token to avoid infinite loop
			p.consume()
			// Skip to next member or closing brace on error
			p.skipToNextMember()
		}

		// Consume optional separator (comma)
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

// Note: Class member parsing functions (parseClassMember, parseClassModifiers,
// parseConstructorDeclaration, parsePropertyOrMethodDeclaration, parsePropertyName,
// parsePropertyDeclarationWithName, parseMethodDeclarationWithName, parseGetterDeclaration,
// parseSetterDeclaration, parseClassIndexSignature) have been moved to class.go

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

	// Set ambient context for parsing namespace body
	savedAmbientContext := p.inAmbientContext
	p.inAmbientContext = true

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

	// Restore ambient context
	p.inAmbientContext = savedAmbientContext

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

	// Set ambient context for parsing module body
	savedAmbientContext := p.inAmbientContext
	p.inAmbientContext = true

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

	// Restore ambient context
	p.inAmbientContext = savedAmbientContext

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

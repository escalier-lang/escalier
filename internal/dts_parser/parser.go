package dts_parser

import (
	"fmt"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

// DtsParser parses TypeScript .d.ts declaration files
type DtsParser struct {
	lexer  *parser.Lexer
	errors []*parser.Error
}

// NewDtsParser creates a new parser for TypeScript declaration files
func NewDtsParser(source *ast.Source) *DtsParser {
	return &DtsParser{
		lexer:  parser.NewLexer(source),
		errors: []*parser.Error{},
	}
}

// ParseModule parses a complete .d.ts file and returns a Module
func (p *DtsParser) ParseModule() (*Module, []*parser.Error) {
	statements := []Statement{}

	for {
		token := p.peek()
		if token.Type == parser.EndOfFile {
			break
		}

		// Skip comments
		if token.Type == parser.LineComment || token.Type == parser.BlockComment {
			p.consume()
			continue
		}

		stmt := p.parseStatement()
		if stmt != nil {
			statements = append(statements, stmt)
		} else {
			// If we can't parse a statement, skip the token to avoid infinite loop
			p.reportError(token.Span, "Unexpected token")
			p.consume()
		}
	}

	return &Module{Statements: statements}, p.errors
}

// ============================================================================
// Helper Functions
// ============================================================================

// peek returns the next token without consuming it
func (p *DtsParser) peek() *parser.Token {
	return p.lexer.Peek()
}

// consume advances the lexer to the next token
func (p *DtsParser) consume() *parser.Token {
	token := p.lexer.Peek()
	p.lexer.Consume()
	return token
}

// expect checks if the next token matches the expected type and consumes it
func (p *DtsParser) expect(expected parser.TokenType) *parser.Token {
	token := p.peek()
	if token.Type != expected {
		p.reportError(token.Span, fmt.Sprintf("Expected %v but got %v", expected, token.Type))
		return nil
	}
	return p.consume()
}

// reportError adds an error to the error list
func (p *DtsParser) reportError(span ast.Span, message string) {
	p.errors = append(p.errors, parser.NewError(span, message))
}

// saveState saves the current parser state for backtracking
func (p *DtsParser) saveState() *DtsParser {
	// Create a deep copy of the errors slice to avoid sharing the underlying array
	errorsCopy := make([]*parser.Error, len(p.errors))
	copy(errorsCopy, p.errors)

	return &DtsParser{
		lexer:  p.lexer.SaveState(),
		errors: errorsCopy,
	}
}

// restoreState restores a previously saved parser state
func (p *DtsParser) restoreState(saved *DtsParser) {
	p.lexer.RestoreState(saved.lexer)
	p.errors = saved.errors
}

// ============================================================================
// Statement Parsing (Stub for Phase 1)
// ============================================================================

// parseStatement parses a top-level statement (to be implemented in later phases)
func (p *DtsParser) parseStatement() Statement {
	// For Phase 1, we only need basic structure
	// Full implementation will come in Phase 6-8
	return nil
}

// ============================================================================
// Type Annotation Parsing - Phase 1: Foundation & Basic Types
// ============================================================================

// ParseTypeAnn is the main entry point for parsing type annotations
func (p *DtsParser) ParseTypeAnn() TypeAnn {
	return p.parseTypeAnn()
}

// parseTypeAnn parses a complete type annotation with unions and intersections
func (p *DtsParser) parseTypeAnn() TypeAnn {
	// Start with intersection types (higher precedence than union)
	left := p.parseIntersectionType()
	if left == nil {
		return nil
	}

	// Check for union operator
	if p.peek().Type == parser.Pipe {
		types := []TypeAnn{left}

		for p.peek().Type == parser.Pipe {
			p.consume() // consume '|'
			right := p.parseIntersectionType()
			if right == nil {
				p.reportError(p.peek().Span, "Expected type after '|'")
				return nil
			}
			types = append(types, right)
		}

		span := ast.MergeSpans(types[0].Span(), types[len(types)-1].Span())
		return &UnionType{Types: types, span: span}
	}

	return left
}

// parseIntersectionType parses intersection types (T & U & ...)
func (p *DtsParser) parseIntersectionType() TypeAnn {
	left := p.parsePostfixType()
	if left == nil {
		return nil
	}

	// Check for intersection operator
	if p.peek().Type == parser.Ampersand {
		types := []TypeAnn{left}

		for p.peek().Type == parser.Ampersand {
			p.consume() // consume '&'
			right := p.parsePostfixType()
			if right == nil {
				p.reportError(p.peek().Span, "Expected type after '&'")
				return nil
			}
			types = append(types, right)
		}

		span := ast.MergeSpans(types[0].Span(), types[len(types)-1].Span())
		return &IntersectionType{Types: types, span: span}
	}

	return left
}

// parsePostfixType parses postfix type operators like array types (T[])
func (p *DtsParser) parsePostfixType() TypeAnn {
	left := p.parsePrimaryType()
	if left == nil {
		return nil
	}

	// Handle postfix array syntax: T[]
	for p.peek().Type == parser.OpenBracket {
		start := left.Span()
		p.consume() // consume '['

		closeBracket := p.expect(parser.CloseBracket)
		if closeBracket == nil {
			return left // Return what we have even if closing bracket is missing
		}

		span := ast.Span{
			Start:    start.Start,
			End:      closeBracket.Span.End,
			SourceID: start.SourceID,
		}

		left = &ArrayType{ElementType: left, span: span}
	}

	return left
}

// parsePrimaryType parses a primary type (primitives, literals, type references, etc.)
func (p *DtsParser) parsePrimaryType() TypeAnn {
	token := p.peek()

	switch token.Type {
	// Primitive types
	case parser.Any:
		p.consume()
		return &PrimitiveType{Kind: PrimAny, span: token.Span}

	case parser.Unknown:
		p.consume()
		return &PrimitiveType{Kind: PrimUnknown, span: token.Span}

	case parser.String:
		p.consume()
		return &PrimitiveType{Kind: PrimString, span: token.Span}

	case parser.Number:
		p.consume()
		return &PrimitiveType{Kind: PrimNumber, span: token.Span}

	case parser.Boolean:
		p.consume()
		return &PrimitiveType{Kind: PrimBoolean, span: token.Span}

	case parser.Symbol:
		p.consume()
		return &PrimitiveType{Kind: PrimSymbol, span: token.Span}

	case parser.Null:
		p.consume()
		return &PrimitiveType{Kind: PrimNull, span: token.Span}

	case parser.Undefined:
		p.consume()
		return &PrimitiveType{Kind: PrimUndefined, span: token.Span}

	case parser.Never:
		p.consume()
		return &PrimitiveType{Kind: PrimNever, span: token.Span}

	// Literal types
	case parser.StrLit:
		p.consume()
		literal := &StringLiteral{Value: token.Value, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case parser.NumLit:
		p.consume()
		// Parse the numeric value from the string
		// For now, we'll store it as a string and convert later if needed
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			p.reportError(token.Span, fmt.Sprintf("Invalid number literal: %s", token.Value))
			value = 0
		}
		literal := &NumberLiteral{Value: value, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case parser.True:
		p.consume()
		literal := &BooleanLiteral{Value: true, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case parser.False:
		p.consume()
		literal := &BooleanLiteral{Value: false, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	// Type reference (identifier or qualified name)
	case parser.Identifier:
		return p.parseTypeReference()

	// Parenthesized type or function type
	case parser.OpenParen:
		// Need to disambiguate between parenthesized type and function type
		// We look ahead to see if this is a parameter list
		return p.parseParenthesizedOrFunctionType()

	// Function type with type parameters: <T>(params) => ReturnType
	case parser.LessThan:
		// This could be a type argument or a function type with type parameters
		// Try to parse as function type first
		savedState := p.saveState()
		funcType := p.parseFunctionType()
		if funcType != nil {
			return funcType
		}
		// If that fails, restore and return nil
		p.restoreState(savedState)
		return nil

	// Constructor type: new (params) => ReturnType
	case parser.New:
		return p.parseConstructorType()

	// Tuple type
	case parser.OpenBracket:
		return p.parseTupleType()

	default:
		return nil
	}
}

// parseTypeReference parses a type reference with optional type arguments
func (p *DtsParser) parseTypeReference() TypeAnn {
	// Parse qualified identifier (e.g., Foo.Bar.Baz)
	name := p.parseQualifiedIdent()
	if name == nil {
		return nil
	}

	start := name.Span()
	span := start

	// Check for type arguments
	var typeArgs []TypeAnn
	var closingBracket *parser.Token
	if p.peek().Type == parser.LessThan {
		typeArgs, closingBracket = p.parseTypeArguments()
		if closingBracket != nil {
			// Include the closing '>' in the span
			span = ast.MergeSpans(start, closingBracket.Span)
		} else if len(typeArgs) > 0 {
			span = ast.MergeSpans(start, typeArgs[len(typeArgs)-1].Span())
		}
	}

	return &TypeReference{Name: name, TypeArgs: typeArgs, span: span}
}

// parseQualifiedIdent parses a qualified identifier (e.g., Foo.Bar.Baz)
func (p *DtsParser) parseQualifiedIdent() QualIdent {
	token := p.peek()
	if token.Type != parser.Identifier {
		return nil
	}
	p.consume()

	var result QualIdent = NewIdent(token.Value, token.Span)

	// Check for member access
	for p.peek().Type == parser.Dot {
		p.consume() // consume '.'

		token = p.peek()
		if token.Type != parser.Identifier {
			p.reportError(token.Span, "Expected identifier after '.'")
			return nil
		}
		p.consume()

		right := NewIdent(token.Value, token.Span)
		result = &Member{Left: result, Right: right}
	}

	return result
}

// parseTypeArguments parses type arguments: <T, U, V>
// Returns the type arguments and the closing '>' token (if found)
func (p *DtsParser) parseTypeArguments() ([]TypeAnn, *parser.Token) {
	if p.peek().Type != parser.LessThan {
		return nil, nil
	}
	p.consume() // consume '<'

	typeArgs := []TypeAnn{}

	// Parse first type argument
	typeArg := p.parseTypeAnn()
	if typeArg == nil {
		p.reportError(p.peek().Span, "Expected type argument")
		return typeArgs, nil
	}
	typeArgs = append(typeArgs, typeArg)

	// Parse remaining type arguments
	for p.peek().Type == parser.Comma {
		p.consume() // consume ','

		typeArg := p.parseTypeAnn()
		if typeArg == nil {
			p.reportError(p.peek().Span, "Expected type argument")
			break
		}
		typeArgs = append(typeArgs, typeArg)
	}

	closingBracket := p.expect(parser.GreaterThan)

	return typeArgs, closingBracket
}

// parseParenthesizedOrFunctionType disambiguates between (T) and (params) => ReturnType
func (p *DtsParser) parseParenthesizedOrFunctionType() TypeAnn {
	// We need to look ahead to determine if this is a function type or parenthesized type
	// Strategy: Try parsing as function type first, fall back to parenthesized type

	savedState := p.saveState()

	// Try to parse as function type
	funcType := p.parseFunctionType()
	if funcType != nil {
		return funcType
	}

	// Restore state and parse as parenthesized type
	p.restoreState(savedState)
	return p.parseParenthesizedType()
}

// parseParenthesizedType parses a parenthesized type: (T)
func (p *DtsParser) parseParenthesizedType() TypeAnn {
	start := p.expect(parser.OpenParen)
	if start == nil {
		return nil
	}

	typeAnn := p.parseTypeAnn()
	if typeAnn == nil {
		p.reportError(p.peek().Span, "Expected type inside parentheses")
		return nil
	}

	end := p.expect(parser.CloseParen)
	if end == nil {
		return typeAnn // Return what we have even if closing paren is missing
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      end.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &ParenthesizedType{Type: typeAnn, span: span}
}

// parseTupleType parses a tuple type: [T1, T2, ...]
func (p *DtsParser) parseTupleType() TypeAnn {
	start := p.expect(parser.OpenBracket)
	if start == nil {
		return nil
	}

	elements := []TupleElement{}

	// Handle empty tuple
	if p.peek().Type == parser.CloseBracket {
		end := p.consume()
		span := ast.Span{
			Start:    start.Span.Start,
			End:      end.Span.End,
			SourceID: start.Span.SourceID,
		}
		return &TupleType{Elements: elements, span: span}
	}

	// Parse first element
	element := p.parseTupleElement()
	if element != nil {
		elements = append(elements, *element)
	} else {
		p.reportError(p.peek().Span, "Expected tuple element")
		return nil
	}

	// Parse remaining elements
	for p.peek().Type == parser.Comma {
		p.consume() // consume ','

		// Allow trailing comma
		if p.peek().Type == parser.CloseBracket {
			break
		}

		element := p.parseTupleElement()
		if element != nil {
			elements = append(elements, *element)
		} else {
			p.reportError(p.peek().Span, "Expected tuple element")
			break
		}
	}

	end := p.expect(parser.CloseBracket)
	if end == nil {
		// Return what we have even if closing bracket is missing
		if len(elements) > 0 {
			span := ast.MergeSpans(start.Span, elements[len(elements)-1].Span())
			return &TupleType{Elements: elements, span: span}
		}
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      end.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &TupleType{Elements: elements, span: span}
}

// parseTupleElement parses a single tuple element with optional label, rest, and optional modifiers
func (p *DtsParser) parseTupleElement() *TupleElement {
	startSpan := p.peek().Span
	var name *Ident
	var typeAnn TypeAnn
	rest := false
	optional := false

	// Check for rest element: ...T
	if p.peek().Type == parser.DotDotDot {
		rest = true
		p.consume() // consume '...'
	}

	// Try to parse label: name: type or name?: type
	// We need to look ahead to distinguish between a label and a plain type
	if p.peek().Type == parser.Identifier {
		// Look ahead for ':' or '?:'
		savedState := p.saveState()
		ident := p.parseIdent()

		if p.peek().Type == parser.Question {
			// This is a labeled optional element: name?: type
			optional = true
			p.consume() // consume '?'

			if p.peek().Type == parser.Colon {
				p.consume() // consume ':'
				name = ident
				typeAnn = p.parseTypeAnn()
			} else {
				// No colon after '?', this was not a label
				p.restoreState(savedState)
				typeAnn = p.parseTypeAnn()
			}
		} else if p.peek().Type == parser.Colon {
			// This is a labeled element: name: type
			p.consume() // consume ':'
			name = ident
			typeAnn = p.parseTypeAnn()
		} else {
			// No colon, this was just a type reference
			p.restoreState(savedState)
			typeAnn = p.parseTypeAnn()
		}
	} else {
		typeAnn = p.parseTypeAnn()
	}

	if typeAnn == nil {
		return nil
	}

	// Check for optional marker after type (for non-labeled elements)
	if name == nil && p.peek().Type == parser.Question {
		optional = true
		p.consume() // consume '?'
	}

	endSpan := typeAnn.Span()
	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &TupleElement{
		Name:     name,
		Type:     typeAnn,
		Optional: optional,
		Rest:     rest,
		span:     span,
	}
}

// ============================================================================
// Identifier Parsing
// ============================================================================

// parseIdent parses a simple identifier
func (p *DtsParser) parseIdent() *Ident {
	token := p.peek()
	if token.Type != parser.Identifier {
		p.reportError(token.Span, "Expected identifier")
		return nil
	}
	p.consume()
	return NewIdent(token.Value, token.Span)
}

// ============================================================================
// Phase 3: Function & Constructor Types
// ============================================================================

// parseFunctionType parses a function type: <T>(params) => ReturnType
func (p *DtsParser) parseFunctionType() TypeAnn {
	startSpan := p.peek().Span

	// Parse optional type parameters
	var typeParams []*TypeParam
	if p.peek().Type == parser.LessThan {
		typeParams = p.parseTypeParams()
	}

	// Parse parameter list
	if p.peek().Type != parser.OpenParen {
		return nil
	}

	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Expect '=>'
	arrow := p.expect(parser.FatArrow)
	if arrow == nil {
		return nil
	}

	// Parse return type (could be a type predicate or regular type)
	returnType := p.parseReturnType()
	if returnType == nil {
		p.reportError(p.peek().Span, "Expected return type after '=>'")
		return nil
	}

	endSpan := returnType.Span()
	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &FunctionType{
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		span:       span,
	}
}

// parseConstructorType parses a constructor type: new <T>(params) => ReturnType
func (p *DtsParser) parseConstructorType() TypeAnn {
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
	if p.peek().Type != parser.OpenParen {
		p.reportError(p.peek().Span, "Expected '(' after 'new'")
		return nil
	}

	params := p.parseParams()
	if params == nil {
		return nil
	}

	// Expect '=>'
	arrow := p.expect(parser.FatArrow)
	if arrow == nil {
		return nil
	}

	// Parse return type
	returnType := p.parseTypeAnn()
	if returnType == nil {
		p.reportError(p.peek().Span, "Expected return type after '=>'")
		return nil
	}

	endSpan := returnType.Span()
	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &ConstructorType{
		TypeParams: typeParams,
		Params:     params,
		ReturnType: returnType,
		span:       span,
	}
}

// parseTypeParams parses type parameters: <T, U extends V = Default>
func (p *DtsParser) parseTypeParams() []*TypeParam {
	if p.peek().Type != parser.LessThan {
		return nil
	}
	p.consume() // consume '<'

	typeParams := []*TypeParam{}

	// Parse first type parameter
	typeParam := p.parseTypeParam()
	if typeParam == nil {
		p.reportError(p.peek().Span, "Expected type parameter")
		return typeParams
	}
	typeParams = append(typeParams, typeParam)

	// Parse remaining type parameters
	for p.peek().Type == parser.Comma {
		p.consume() // consume ','

		typeParam := p.parseTypeParam()
		if typeParam == nil {
			p.reportError(p.peek().Span, "Expected type parameter")
			break
		}
		typeParams = append(typeParams, typeParam)
	}

	p.expect(parser.GreaterThan)

	return typeParams
}

// parseTypeParam parses a single type parameter: T extends U = Default
func (p *DtsParser) parseTypeParam() *TypeParam {
	startSpan := p.peek().Span

	// Parse name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	endSpan := name.Span()

	// Parse optional constraint
	var constraint TypeAnn
	if p.peek().Type == parser.Extends {
		p.consume() // consume 'extends'
		constraint = p.parseTypeAnn()
		if constraint == nil {
			p.reportError(p.peek().Span, "Expected type after 'extends'")
		} else {
			endSpan = constraint.Span()
		}
	}

	// Parse optional default
	var defaultType TypeAnn
	if p.peek().Type == parser.Equal {
		p.consume() // consume '='
		defaultType = p.parseTypeAnn()
		if defaultType == nil {
			p.reportError(p.peek().Span, "Expected type after '='")
		} else {
			endSpan = defaultType.Span()
		}
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &TypeParam{
		Name:       name,
		Constraint: constraint,
		Default:    defaultType,
		span:       span,
	}
}

// parseParams parses a parameter list: (arg1: Type1, arg2?: Type2, ...rest: Type3)
func (p *DtsParser) parseParams() []*Param {
	if p.peek().Type != parser.OpenParen {
		return nil
	}
	p.consume() // consume '('

	params := []*Param{}

	// Handle empty parameter list
	if p.peek().Type == parser.CloseParen {
		p.consume()
		return params
	}

	// Parse first parameter
	param := p.parseParam()
	if param != nil {
		params = append(params, param)
	} else {
		p.reportError(p.peek().Span, "Expected parameter")
	}

	// Parse remaining parameters
	for p.peek().Type == parser.Comma {
		p.consume() // consume ','

		// Allow trailing comma
		if p.peek().Type == parser.CloseParen {
			break
		}

		param := p.parseParam()
		if param != nil {
			params = append(params, param)
		} else {
			p.reportError(p.peek().Span, "Expected parameter")
			break
		}
	}

	p.expect(parser.CloseParen)

	return params
}

// parseParam parses a single parameter: name?: Type or ...rest: Type
func (p *DtsParser) parseParam() *Param {
	startSpan := p.peek().Span
	rest := false
	optional := false

	// Check for rest parameter
	if p.peek().Type == parser.DotDotDot {
		rest = true
		p.consume() // consume '...'
	}

	// Parse parameter name
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	endSpan := name.Span()

	// Check for optional marker
	if p.peek().Type == parser.Question {
		optional = true
		p.consume() // consume '?'
	}

	// Parse type annotation
	var typeAnn TypeAnn
	if p.peek().Type == parser.Colon {
		p.consume() // consume ':'
		typeAnn = p.parseTypeAnn()
		if typeAnn == nil {
			p.reportError(p.peek().Span, "Expected type annotation")
		} else {
			endSpan = typeAnn.Span()
		}
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &Param{
		Name:     name,
		Type:     typeAnn,
		Optional: optional,
		Rest:     rest,
		span:     span,
	}
}

// parseReturnType parses a return type, which can be either a type predicate or a regular type
func (p *DtsParser) parseReturnType() TypeAnn {
	// Try to parse as type predicate first
	// Type predicates look like: "arg is Type" or "asserts arg" or "asserts arg is Type"
	if p.peek().Type == parser.Identifier || p.peek().Type == parser.Asserts {
		savedState := p.saveState()
		predicate := p.tryParseTypePredicate()
		if predicate != nil {
			return predicate
		}
		// If not a type predicate, restore and parse as regular type
		p.restoreState(savedState)
	}

	return p.parseTypeAnn()
}

// tryParseTypePredicate attempts to parse a type predicate: arg is Type or asserts arg is Type
func (p *DtsParser) tryParseTypePredicate() TypeAnn {
	startSpan := p.peek().Span
	asserts := false

	// Check for 'asserts' keyword
	if p.peek().Type == parser.Asserts {
		asserts = true
		p.consume() // consume 'asserts'
	}

	// Parse parameter name
	if p.peek().Type != parser.Identifier {
		return nil
	}
	paramName := p.parseIdent()
	if paramName == nil {
		return nil
	}

	endSpan := paramName.Span()

	// For asserts predicates, 'is Type' is optional
	// For regular predicates, 'is Type' is required
	var typeAnn TypeAnn
	if p.peek().Type == parser.Is {
		p.consume() // consume 'is'
		typeAnn = p.parseTypeAnn()
		if typeAnn == nil {
			return nil // Invalid type predicate
		}
		endSpan = typeAnn.Span()
	} else if !asserts {
		// Regular type predicate requires 'is'
		return nil
	}

	span := ast.Span{
		Start:    startSpan.Start,
		End:      endSpan.End,
		SourceID: startSpan.SourceID,
	}

	return &TypePredicate{
		ParamName: paramName,
		Asserts:   asserts,
		Type:      typeAnn,
		span:      span,
	}
}

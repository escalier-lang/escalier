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
	return &DtsParser{
		lexer:  p.lexer.SaveState(),
		errors: p.errors,
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

	// Parenthesized type
	case parser.OpenParen:
		return p.parseParenthesizedType()

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

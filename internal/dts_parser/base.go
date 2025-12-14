package dts_parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// DtsParser parses TypeScript .d.ts declaration files
type DtsParser struct {
	lexer            *Lexer
	errors           []*Error
	inAmbientContext bool // true when inside a declare namespace or declare module
}

// NewDtsParser creates a new parser for TypeScript declaration files
func NewDtsParser(source *ast.Source) *DtsParser {
	return &DtsParser{
		lexer:            NewLexer(source),
		errors:           []*Error{},
		inAmbientContext: false,
	}
}

// ParseModule parses a complete .d.ts file and returns a Module
func (p *DtsParser) ParseModule() (*Module, []*Error) {
	statements := []Statement{}

	for {
		token := p.peek()
		if token.Type == EndOfFile {
			break
		}

		// Skip comments
		if token.Type == LineComment || token.Type == BlockComment {
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
func (p *DtsParser) peek() *Token {
	return p.lexer.Peek()
}

// consume advances the lexer to the next token
func (p *DtsParser) consume() *Token {
	token := p.lexer.Peek()
	p.lexer.Consume()
	return token
}

// expect checks if the next token matches the expected type and consumes it
func (p *DtsParser) expect(expected TokenType) *Token {
	token := p.peek()
	if token.Type != expected {
		p.reportError(token.Span, fmt.Sprintf("Expected %v but got %v", expected, token.Type))
		return nil
	}
	return p.consume()
}

// reportError adds an error to the error list
func (p *DtsParser) reportError(span ast.Span, message string) {
	p.errors = append(p.errors, NewError(span, message))
}

// skipComments skips over any comment tokens (line comments and block comments)
func (p *DtsParser) skipComments() {
	for p.peek().Type == LineComment || p.peek().Type == BlockComment {
		p.consume()
	}
}

// saveState saves the current parser state for backtracking
func (p *DtsParser) saveState() *DtsParser {
	// Create a deep copy of the errors slice to avoid sharing the underlying array
	errorsCopy := make([]*Error, len(p.errors))
	copy(errorsCopy, p.errors)

	return &DtsParser{
		lexer:            p.lexer.SaveState(),
		errors:           errorsCopy,
		inAmbientContext: p.inAmbientContext,
	}
}

// restoreState restores a previously saved parser state
func (p *DtsParser) restoreState(saved *DtsParser) {
	p.lexer.RestoreState(saved.lexer)
	p.errors = saved.errors
	p.inAmbientContext = saved.inAmbientContext
}

// ============================================================================
// Identifier Parsing
// ============================================================================

// parseIdent parses a simple identifier
func (p *DtsParser) parseIdent() *Ident {
	token := p.lexer.peekIdent()
	if token == nil {
		p.reportError(p.peek().Span, "Expected identifier")
		return nil
	}
	p.consume() // Consume the token
	return NewIdent(token.Value, token.Span)
}

// ============================================================================
// Type Annotation Parsing - Phase 1: Foundation & Basic Types
// ============================================================================

// ParseTypeAnn is the main entry point for parsing type annotations
func (p *DtsParser) ParseTypeAnn() TypeAnn {
	return p.parseTypeAnn()
}

// parsePrimaryType parses a primary type (primitives, literals, type references, etc.)
func (p *DtsParser) parsePrimaryType() TypeAnn {
	token := p.peek()

	switch token.Type {
	// Primitive types
	case Any:
		p.consume()
		return &PrimitiveType{Kind: PrimAny, span: token.Span}

	case Unknown:
		p.consume()
		return &PrimitiveType{Kind: PrimUnknown, span: token.Span}

	case String:
		p.consume()
		return &PrimitiveType{Kind: PrimString, span: token.Span}

	case Number:
		p.consume()
		return &PrimitiveType{Kind: PrimNumber, span: token.Span}

	case Boolean:
		p.consume()
		return &PrimitiveType{Kind: PrimBoolean, span: token.Span}

	case Bigint:
		p.consume()
		return &PrimitiveType{Kind: PrimBigInt, span: token.Span}

	case Symbol:
		p.consume()
		return &PrimitiveType{Kind: PrimSymbol, span: token.Span}

	case Null:
		p.consume()
		return &PrimitiveType{Kind: PrimNull, span: token.Span}

	case Undefined:
		p.consume()
		return &PrimitiveType{Kind: PrimUndefined, span: token.Span}

	case Never:
		p.consume()
		return &PrimitiveType{Kind: PrimNever, span: token.Span}

	// Literal types
	case StrLit:
		p.consume()
		literal := &StringLiteral{Value: token.Value, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case NumLit:
		p.consume()
		// Parse the numeric value from the string (handles both decimal and hex)
		value, err := parseNumberValue(token.Value)
		if err != nil {
			p.reportError(token.Span, fmt.Sprintf("Invalid number literal: %s", token.Value))
			value = 0
		}
		literal := &NumberLiteral{Value: value, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case True:
		p.consume()
		literal := &BooleanLiteral{Value: true, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	case False:
		p.consume()
		literal := &BooleanLiteral{Value: false, span: token.Span}
		return &LiteralType{Literal: literal, span: token.Span}

	// Negative number literal: -123
	case Minus:
		minusToken := p.consume() // consume '-'
		numToken := p.peek()
		if numToken.Type != NumLit {
			p.reportError(numToken.Span, "Expected number after '-'")
			return nil
		}
		p.consume() // consume number

		// Parse the numeric value and negate it (handles both decimal and hex)
		value, err := parseNumberValue(numToken.Value)
		if err != nil {
			p.reportError(numToken.Span, fmt.Sprintf("Invalid number literal: %s", numToken.Value))
			value = 0
		}
		value = -value

		span := ast.Span{
			Start:    minusToken.Span.Start,
			End:      numToken.Span.End,
			SourceID: minusToken.Span.SourceID,
		}
		literal := &NumberLiteral{Value: value, span: span}
		return &LiteralType{Literal: literal, span: span}

	// Type reference (identifier or qualified name), or 'this' type
	case Identifier:
		if token.Value == "this" {
			return p.parseThisType()
		}
		return p.parseTypeReference()

	// Parenthesized type or function type
	case OpenParen:
		// Need to disambiguate between parenthesized type and function type
		// We look ahead to see if this is a parameter list
		return p.parseParenthesizedOrFunctionType()

	// Function type with type parameters: <T>(params) => ReturnType
	case LessThan:
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
	// or Abstract constructor type: abstract new (params) => ReturnType
	case New:
		return p.parseConstructorType(false, token.Span)

	case Abstract:
		// Check if this is 'abstract new'
		savedState := p.saveState()
		abstractToken := p.consume() // consume 'abstract'
		if p.peek().Type == New {
			return p.parseConstructorType(true, abstractToken.Span)
		}
		// Not an abstract constructor type, restore state
		p.restoreState(savedState)
		return nil

	// Tuple type
	case OpenBracket:
		return p.parseTupleType()

	// Object type literal
	case OpenBrace:
		// Could be object type or mapped type
		// Check if it's a mapped type by looking for [K in T] pattern
		savedState := p.saveState()
		mappedType := p.parseMappedType()
		if mappedType != nil {
			return mappedType
		}
		// If not a mapped type, restore and parse as object type
		p.restoreState(savedState)
		return p.parseObjectType()

	// Template literal type
	case BackTick:
		return p.parseTemplateLiteralType()

	// keyof operator
	case Keyof:
		return p.parseKeyOfType()

	// typeof operator
	case Typeof:
		return p.parseTypeOfType()

	// import type
	case Import:
		return p.parseImportType()

	// infer type (used in conditional types)
	case Infer:
		return p.parseInferType()

	// Rest type ...T
	case DotDotDot:
		return p.parseRestType()

	// Readonly array type: readonly T[]
	case Readonly:
		return p.parseReadonlyArrayType()

	default:
		return nil
	}
}

// parseReadonlyArrayType parses readonly array types: readonly T[]
// The readonly modifier creates a readonly array with element type T
// Examples:
// - readonly string[] -> ArrayType{ElementType: PrimitiveType(string), Readonly: true}
// - readonly string[][] -> outer non-readonly ArrayType containing inner readonly ArrayType{ElementType: PrimitiveType(string), Readonly: true}
func (p *DtsParser) parseReadonlyArrayType() TypeAnn {
	start := p.expect(Readonly)
	if start == nil {
		return nil
	}

	// Parse just the primary type (no postfix operators yet)
	elementType := p.parsePrimaryType()
	if elementType == nil {
		p.reportError(p.peek().Span, "Expected type after 'readonly'")
		return nil
	}

	// Expect at least one array bracket []
	if p.peek().Type != OpenBracket {
		p.reportError(p.peek().Span, "Expected '[]' after 'readonly T' to form readonly array type")
		return nil
	}

	p.consume() // consume '['
	if p.peek().Type != CloseBracket {
		p.reportError(p.peek().Span, "Expected ']' for readonly array type")
		return nil
	}
	closeBracket := p.consume() // consume ']'

	span := ast.Span{
		Start:    start.Span.Start,
		End:      closeBracket.Span.End,
		SourceID: start.Span.SourceID,
	}

	return &ArrayType{
		ElementType: elementType,
		Readonly:    true,
		span:        span,
	}
}

// parseNumberValue parses a number literal string, handling both decimal and hexadecimal formats
func parseNumberValue(s string) (float64, error) {
	// Check if it's a hex literal
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		// Parse as hex integer
		val, err := strconv.ParseInt(s[2:], 16, 64)
		if err != nil {
			return 0, err
		}
		return float64(val), nil
	}
	// Parse as decimal float
	return strconv.ParseFloat(s, 64)
}

package dts_parser

import (
	"fmt"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// DtsParser parses TypeScript .d.ts declaration files
type DtsParser struct {
	lexer  *Lexer
	errors []*Error
}

// NewDtsParser creates a new parser for TypeScript declaration files
func NewDtsParser(source *ast.Source) *DtsParser {
	return &DtsParser{
		lexer:  NewLexer(source),
		errors: []*Error{},
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

// saveState saves the current parser state for backtracking
func (p *DtsParser) saveState() *DtsParser {
	// Create a deep copy of the errors slice to avoid sharing the underlying array
	errorsCopy := make([]*Error, len(p.errors))
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
// Identifier Parsing
// ============================================================================

// parseIdent parses a simple identifier
func (p *DtsParser) parseIdent() *Ident {
	token := p.peek()
	if token.Type != Identifier {
		p.reportError(token.Span, "Expected identifier")
		return nil
	}
	p.consume()
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
		// Parse the numeric value from the string
		// For now, we'll store it as a string and convert later if needed
		value, err := strconv.ParseFloat(token.Value, 64)
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

	// Type reference (identifier or qualified name)
	case Identifier:
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
	case New:
		return p.parseConstructorType()

	// Tuple type
	case OpenBracket:
		return p.parseTupleType()

	// Object type literal
	case OpenBrace:
		return p.parseObjectType()

	default:
		return nil
	}
}

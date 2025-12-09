package dts_parser

import (
	"fmt"

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
	left := p.parsePrimaryType()
	if left == nil {
		return nil
	}

	// Check for intersection operator
	if p.peek().Type == parser.Ampersand {
		types := []TypeAnn{left}

		for p.peek().Type == parser.Ampersand {
			p.consume() // consume '&'
			right := p.parsePrimaryType()
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
		literal := &NumberLiteral{Value: 0, span: token.Span} // TODO: parse actual value
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

	// Check for type arguments
	var typeArgs []TypeAnn
	if p.peek().Type == parser.LessThan {
		typeArgs = p.parseTypeArguments()
	}

	var span ast.Span
	if len(typeArgs) > 0 {
		span = ast.MergeSpans(start, typeArgs[len(typeArgs)-1].Span())
	} else {
		span = start
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
func (p *DtsParser) parseTypeArguments() []TypeAnn {
	if p.peek().Type != parser.LessThan {
		return nil
	}
	p.consume() // consume '<'

	typeArgs := []TypeAnn{}

	// Parse first type argument
	typeArg := p.parseTypeAnn()
	if typeArg == nil {
		p.reportError(p.peek().Span, "Expected type argument")
		return typeArgs
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

	p.expect(parser.GreaterThan)

	return typeArgs
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

package dts_parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// Phase 2: Simple Compound Types
// ============================================================================

// parseTypeAnn parses a complete type annotation with unions, intersections, and conditionals
// Precedence (lowest to highest): conditional > union > intersection > postfix
func (p *DtsParser) parseTypeAnn() TypeAnn {
	// Parse union/intersection types first (higher precedence than conditional)
	left := p.parseUnionType()
	if left == nil {
		return nil
	}

	// Check for conditional type (lowest precedence)
	if p.peek().Type == Extends {
		return p.parseConditionalType(left)
	}

	return left
}

// parseUnionType parses union types (T | U | ...)
func (p *DtsParser) parseUnionType() TypeAnn {
	// Start with intersection types (higher precedence than union)
	left := p.parseIntersectionType()
	if left == nil {
		return nil
	}

	// Check for union operator
	if p.peek().Type == Pipe {
		types := []TypeAnn{left}

		for p.peek().Type == Pipe {
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
	if p.peek().Type == Ampersand {
		types := []TypeAnn{left}

		for p.peek().Type == Ampersand {
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

// parsePostfixType parses postfix type operators like array types (T[]) and indexed access (T[K])
func (p *DtsParser) parsePostfixType() TypeAnn {
	left := p.parsePrimaryType()
	if left == nil {
		return nil
	}

	// Handle postfix operators: array syntax T[] or indexed access T[K]
	for {
		token := p.peek()

		if token.Type == OpenBracket {
			// Peek ahead to see if this is an array type T[] or indexed access T[K]
			savedState := p.saveState()
			p.consume() // consume '['

			// Check if immediately followed by ]
			if p.peek().Type == CloseBracket {
				// This is array type T[]
				closeBracket := p.consume()
				span := ast.Span{
					Start:    left.Span().Start,
					End:      closeBracket.Span.End,
					SourceID: left.Span().SourceID,
				}
				left = &ArrayType{ElementType: left, span: span}
			} else {
				// This is indexed access T[K]
				p.restoreState(savedState)
				left = p.parseIndexedAccessType(left)
			}
		} else {
			break
		}
	}

	return left
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
	var closingBracket *Token
	if p.peek().Type == LessThan {
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
	if token.Type != Identifier && !isTypeKeywordIdentifier(token.Type) {
		return nil
	}
	p.consume()

	var result QualIdent = NewIdent(token.Value, token.Span)

	// Check for member access
	for p.peek().Type == Dot {
		p.consume() // consume '.'

		token = p.peek()
		if token.Type != Identifier && !isTypeKeywordIdentifier(token.Type) {
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
func (p *DtsParser) parseTypeArguments() ([]TypeAnn, *Token) {
	if p.peek().Type != LessThan {
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
	for p.peek().Type == Comma {
		p.consume() // consume ','

		typeArg := p.parseTypeAnn()
		if typeArg == nil {
			p.reportError(p.peek().Span, "Expected type argument")
			break
		}
		typeArgs = append(typeArgs, typeArg)
	}

	closingBracket := p.expect(GreaterThan)

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
	start := p.expect(OpenParen)
	if start == nil {
		return nil
	}

	typeAnn := p.parseTypeAnn()
	if typeAnn == nil {
		p.reportError(p.peek().Span, "Expected type inside parentheses")
		return nil
	}

	end := p.expect(CloseParen)
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
	start := p.expect(OpenBracket)
	if start == nil {
		return nil
	}

	elements := []TupleElement{}

	// Handle empty tuple
	if p.peek().Type == CloseBracket {
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
	for p.peek().Type == Comma {
		p.consume() // consume ','

		// Allow trailing comma
		if p.peek().Type == CloseBracket {
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

	end := p.expect(CloseBracket)
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
	if p.peek().Type == DotDotDot {
		rest = true
		p.consume() // consume '...'
	}

	// Try to parse label: name: type or name?: type
	// We need to look ahead to distinguish between a label and a plain type
	if p.peek().Type == Identifier {
		// Look ahead for ':' or '?:'
		savedState := p.saveState()
		ident := p.parseIdent()

		if p.peek().Type == Question {
			// This is a labeled optional element: name?: type
			optional = true
			p.consume() // consume '?'

			if p.peek().Type == Colon {
				p.consume() // consume ':'
				name = ident
				typeAnn = p.parseTypeAnn()
			} else {
				// No colon after '?', this was not a label
				p.restoreState(savedState)
				typeAnn = p.parseTypeAnn()
			}
		} else if p.peek().Type == Colon {
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
	if name == nil && p.peek().Type == Question {
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

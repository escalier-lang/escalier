package dts_parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// Phase 5: Advanced Type Operators
// ============================================================================

// parseIndexedAccessType parses T[K] indexed access types
// This is called when we've already parsed T and see a [
func (p *DtsParser) parseIndexedAccessType(objectType TypeAnn) TypeAnn {
	start := objectType.Span()
	p.consume() // consume '['

	indexType := p.parseTypeAnn()
	if indexType == nil {
		p.reportError(p.peek().Span, "Expected index type")
		return objectType
	}

	end := p.expect(CloseBracket)
	if end == nil {
		return objectType
	}

	span := ast.Span{
		Start:    start.Start,
		End:      end.Span.End,
		SourceID: start.SourceID,
	}

	return &IndexedAccessType{
		ObjectType: objectType,
		IndexType:  indexType,
		span:       span,
	}
}

// parseConditionalType parses T extends U ? X : Y
// This is called when we've already parsed T and see 'extends'
// Note: checkType comes from parseUnionType (already parsed with union/intersection)
// extendsType should also allow unions/intersections but NOT conditionals
func (p *DtsParser) parseConditionalType(checkType TypeAnn) TypeAnn {
	start := checkType.Span()
	p.consume() // consume 'extends'

	// Parse the extends type - use parseUnionType to allow unions/intersections
	// but prevent recursive conditionals
	extendsType := p.parseUnionType()
	if extendsType == nil {
		p.reportError(p.peek().Span, "Expected type after 'extends'")
		return checkType
	}

	question := p.expect(Question)
	if question == nil {
		return checkType
	}

	// Skip any comments after '?'
	p.skipComments()

	// True and false branches can contain full conditional types
	trueType := p.parseTypeAnn()
	if trueType == nil {
		p.reportError(p.peek().Span, "Expected type after '?'")
		return checkType
	}

	colon := p.expect(Colon)
	if colon == nil {
		return checkType
	}

	// Skip any comments after ':'
	p.skipComments()

	falseType := p.parseTypeAnn()
	if falseType == nil {
		p.reportError(p.peek().Span, "Expected type after ':'")
		return checkType
	}

	span := ast.Span{
		Start:    start.Start,
		End:      falseType.Span().End,
		SourceID: start.SourceID,
	}

	return &ConditionalType{
		CheckType:   checkType,
		ExtendsType: extendsType,
		TrueType:    trueType,
		FalseType:   falseType,
		span:        span,
	}
}

// parseInferType parses infer T or infer T extends U
func (p *DtsParser) parseInferType() TypeAnn {
	start := p.expect(Infer)
	if start == nil {
		return nil
	}

	name := p.parseIdent()
	if name == nil {
		p.reportError(p.peek().Span, "Expected type parameter name after 'infer'")
		return nil
	}

	// Check for optional 'extends' constraint
	var constraint TypeAnn
	endSpan := name.Span()
	if p.peek().Type == Extends {
		p.consume() // consume 'extends'
		constraint = p.parseTypeAnn()
		if constraint == nil {
			p.reportError(p.peek().Span, "Expected type after 'extends'")
		} else {
			endSpan = constraint.Span()
		}
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	typeParam := &TypeParam{
		Name:       name,
		Constraint: constraint,
		span:       span,
	}

	return &InferType{
		TypeParam: typeParam,
		span:      span,
	}
}

// parseMappedType parses { [K in T]: U } with optional modifiers
func (p *DtsParser) parseMappedType() TypeAnn {
	start := p.expect(OpenBrace)
	if start == nil {
		return nil
	}

	// Skip comments after opening brace
	p.skipComments()

	// Parse optional readonly modifier
	readonlyMod := ReadonlyNone
	if p.peek().Type == Readonly {
		p.consume()
		readonlyMod = ReadonlyAdd
	} else if p.peek().Type == Minus {
		savedState := p.saveState()
		p.consume() // try consuming '-'
		if p.peek().Type == Readonly {
			p.consume() // consume 'readonly'
			readonlyMod = ReadonlyRemove
		} else {
			// Not a -readonly modifier, restore
			p.restoreState(savedState)
		}
	} else if p.peek().Type == Plus {
		savedState := p.saveState()
		p.consume() // try consuming '+'
		if p.peek().Type == Readonly {
			p.consume() // consume 'readonly'
			readonlyMod = ReadonlyAdd
		} else {
			// Not a +readonly modifier, restore
			p.restoreState(savedState)
		}
	}

	openBracket := p.expect(OpenBracket)
	if openBracket == nil {
		return nil
	}

	// Parse type parameter: K in T
	paramName := p.parseIdent()
	if paramName == nil {
		p.reportError(p.peek().Span, "Expected type parameter name")
		return nil
	}

	inToken := p.expect(In)
	if inToken == nil {
		return nil
	}

	constraint := p.parseTypeAnn()
	if constraint == nil {
		p.reportError(p.peek().Span, "Expected constraint type")
		return nil
	}

	// Check for 'as' clause for key remapping
	var asClause TypeAnn
	if p.peek().Type == As {
		p.consume() // consume 'as'
		asClause = p.parseTypeAnn()
		if asClause == nil {
			p.reportError(p.peek().Span, "Expected type after 'as'")
		}
	}

	closeBracket := p.expect(CloseBracket)
	if closeBracket == nil {
		return nil
	}

	// Parse optional '?' or '-?' or '+?' modifier
	optionalMod := OptionalNone
	if p.peek().Type == Question {
		p.consume()
		optionalMod = OptionalAdd
	} else if p.peek().Type == Minus {
		savedState := p.saveState()
		p.consume() // try consuming '-'
		if p.peek().Type == Question {
			p.consume() // consume '?'
			optionalMod = OptionalRemove
		} else {
			// Not a -? modifier, restore
			p.restoreState(savedState)
		}
	} else if p.peek().Type == Plus {
		savedState := p.saveState()
		p.consume() // try consuming '+'
		if p.peek().Type == Question {
			p.consume() // consume '?'
			optionalMod = OptionalAdd
		} else {
			// Not a +? modifier, restore
			p.restoreState(savedState)
		}
	}

	colon := p.expect(Colon)
	if colon == nil {
		return nil
	}

	valueType := p.parseTypeAnn()
	if valueType == nil {
		p.reportError(p.peek().Span, "Expected value type")
		return nil
	}

	// Consume optional semicolon or comma separator
	if p.peek().Type == Semicolon || p.peek().Type == Comma {
		p.consume()
	}

	end := p.expect(CloseBrace)
	if end == nil {
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      end.Span.End,
		SourceID: start.Span.SourceID,
	}

	typeParamSpan := ast.Span{
		Start:    paramName.Span().Start,
		End:      constraint.Span().End,
		SourceID: paramName.Span().SourceID,
	}

	typeParam := &TypeParam{
		Name:       paramName,
		Constraint: constraint,
		span:       typeParamSpan,
	}

	return &MappedType{
		TypeParam: typeParam,
		ValueType: valueType,
		Optional:  optionalMod,
		Readonly:  readonlyMod,
		AsClause:  asClause,
		span:      span,
	}
}

// parseTemplateLiteralType parses `${T}...` template literal types
// This function manually scans the content between backticks since the lexer
// doesn't have full template literal support
func (p *DtsParser) parseTemplateLiteralType() TypeAnn {
	startToken := p.expect(BackTick)
	if startToken == nil {
		return nil
	}

	// Get access to the source content to manually parse template contents
	source := p.lexer.source
	startOffset := p.lexer.currentOffset
	contents := source.Contents

	parts := []TemplatePart{}
	currentOffset := startOffset
	stringStart := currentOffset
	stringStartLocation := p.lexer.currentLocation // Track location at string start
	currentLocation := p.lexer.currentLocation

	// Scan until we find the closing backtick
	for currentOffset < len(contents) {
		ch := contents[currentOffset]

		if ch == '`' {
			// Add any pending string part
			if stringStart < currentOffset {
				stringValue := contents[stringStart:currentOffset]
				strSpan := ast.Span{
					Start:    stringStartLocation,
					End:      currentLocation,
					SourceID: source.ID,
				}
				parts = append(parts, &TemplateString{
					Value: stringValue,
					span:  strSpan,
				})
			}

			// Advance past the closing backtick
			currentOffset++
			currentLocation.Column++

			// Update lexer position
			p.lexer.currentOffset = currentOffset
			p.lexer.currentLocation = currentLocation

			span := ast.Span{
				Start:    startToken.Span.Start,
				End:      currentLocation,
				SourceID: source.ID,
			}
			return &TemplateLiteralType{Parts: parts, span: span}
		}

		if ch == '$' && currentOffset+1 < len(contents) && contents[currentOffset+1] == '{' {
			// Add any pending string part before the placeholder
			if stringStart < currentOffset {
				stringValue := contents[stringStart:currentOffset]
				strSpan := ast.Span{
					Start:    stringStartLocation,
					End:      currentLocation,
					SourceID: source.ID,
				}
				parts = append(parts, &TemplateString{
					Value: stringValue,
					span:  strSpan,
				})
			}

			// Mark the start of ${
			placeholderStart := currentLocation

			// Skip ${
			currentOffset += 2
			currentLocation.Column += 2
			p.lexer.currentOffset = currentOffset
			p.lexer.currentLocation = currentLocation

			// Parse the type inside ${}
			typeAnn := p.parseTypeAnn()
			if typeAnn == nil {
				p.reportError(p.peek().Span, "Expected type in template placeholder")
				return nil
			}

			// Expect closing }
			closeBrace := p.expect(CloseBrace)
			if closeBrace == nil {
				return nil
			}

			// Update our tracking variables
			currentOffset = p.lexer.currentOffset
			currentLocation = p.lexer.currentLocation

			templateTypeSpan := ast.Span{
				Start:    placeholderStart,
				End:      currentLocation,
				SourceID: source.ID,
			}

			parts = append(parts, &TemplateType{
				Type: typeAnn,
				span: templateTypeSpan,
			})

			// Start a new string part after the placeholder
			stringStart = currentOffset
			stringStartLocation = currentLocation // Update string start location
		} else {
			// Regular character
			currentOffset++
			if ch == '\n' {
				currentLocation.Line++
				currentLocation.Column = 1
			} else {
				currentLocation.Column++
			}
		}
	}

	// If we reach here, we didn't find a closing backtick
	p.reportError(startToken.Span, "Unterminated template literal")
	return nil
}

// parseKeyOfType parses keyof T
// Uses parsePostfixType to properly handle array types and indexed access
// e.g., "keyof T[]" should parse as keyof(T[]), not (keyof T)[]
func (p *DtsParser) parseKeyOfType() TypeAnn {
	start := p.expect(Keyof)
	if start == nil {
		return nil
	}

	typeAnn := p.parsePostfixType()
	if typeAnn == nil {
		p.reportError(p.peek().Span, "Expected type after 'keyof'")
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      typeAnn.Span().End,
		SourceID: start.Span.SourceID,
	}

	return &KeyOfType{
		Type: typeAnn,
		span: span,
	}
}

// parseTypeOfType parses typeof expr
func (p *DtsParser) parseTypeOfType() TypeAnn {
	start := p.expect(Typeof)
	if start == nil {
		return nil
	}

	expr := p.parseQualifiedIdent()
	if expr == nil {
		p.reportError(p.peek().Span, "Expected identifier after 'typeof'")
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      expr.Span().End,
		SourceID: start.Span.SourceID,
	}

	return &TypeOfType{
		Expr: expr,
		span: span,
	}
}

// parseImportType parses import("module").Type or import("module")
func (p *DtsParser) parseImportType() TypeAnn {
	start := p.expect(Import)
	if start == nil {
		return nil
	}

	openParen := p.expect(OpenParen)
	if openParen == nil {
		return nil
	}

	moduleToken := p.expect(StrLit)
	if moduleToken == nil {
		p.reportError(p.peek().Span, "Expected module string")
		return nil
	}
	module := moduleToken.Value

	closeParen := p.expect(CloseParen)
	if closeParen == nil {
		return nil
	}

	endSpan := closeParen.Span

	// Check for .Type access
	var name QualIdent
	if p.peek().Type == Dot {
		p.consume() // consume '.'
		name = p.parseQualifiedIdent()
		if name == nil {
			p.reportError(p.peek().Span, "Expected identifier after '.'")
		} else {
			endSpan = name.Span()
		}
	}

	// Check for type arguments
	var typeArgs []TypeAnn
	if p.peek().Type == LessThan {
		var endToken *Token
		typeArgs, endToken = p.parseTypeArguments()
		if endToken != nil {
			endSpan = endToken.Span
		}
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      endSpan.End,
		SourceID: start.Span.SourceID,
	}

	return &ImportType{
		Module:   module,
		Name:     name,
		TypeArgs: typeArgs,
		span:     span,
	}
}

// parseThisType parses 'this' as a type
func (p *DtsParser) parseThisType() TypeAnn {
	token := p.peek()
	if token.Type != Identifier || token.Value != "this" {
		return nil
	}
	p.consume()

	return &ThisType{
		span: token.Span,
	}
}

// parseRestType parses ...T (used in function parameters)
func (p *DtsParser) parseRestType() TypeAnn {
	start := p.expect(DotDotDot)
	if start == nil {
		return nil
	}

	typeAnn := p.parseTypeAnn()
	if typeAnn == nil {
		p.reportError(p.peek().Span, "Expected type after '...'")
		return nil
	}

	span := ast.Span{
		Start:    start.Span.Start,
		End:      typeAnn.Span().End,
		SourceID: start.Span.SourceID,
	}

	return &RestType{
		Type: typeAnn,
		span: span,
	}
}

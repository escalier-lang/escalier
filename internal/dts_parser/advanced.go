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
func (p *DtsParser) parseConditionalType(checkType TypeAnn) TypeAnn {
	start := checkType.Span()
	p.consume() // consume 'extends'

	// Parse the extends type - this should be a union/intersection type, not a full conditional
	// We need to parse up to but not including the '?' operator
	extendsType := p.parseIntersectionType()
	if extendsType == nil {
		p.reportError(p.peek().Span, "Expected type after 'extends'")
		return checkType
	}

	// Check for union after intersection
	if p.peek().Type == Pipe {
		types := []TypeAnn{extendsType}
		for p.peek().Type == Pipe {
			p.consume() // consume '|'
			right := p.parseIntersectionType()
			if right == nil {
				p.reportError(p.peek().Span, "Expected type after '|'")
				break
			}
			types = append(types, right)
		}
		span := ast.MergeSpans(types[0].Span(), types[len(types)-1].Span())
		extendsType = &UnionType{Types: types, span: span}
	}

	question := p.expect(Question)
	if question == nil {
		return checkType
	}

	trueType := p.parseTypeAnn()
	if trueType == nil {
		p.reportError(p.peek().Span, "Expected type after '?'")
		return checkType
	}

	colon := p.expect(Colon)
	if colon == nil {
		return checkType
	}

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

// parseInferType parses infer T
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

	span := ast.Span{
		Start:    start.Span.Start,
		End:      name.Span().End,
		SourceID: start.Span.SourceID,
	}

	typeParam := &TypeParam{
		Name: name,
		span: span,
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
	if p.peek().Type == Identifier && p.peek().Value == "as" {
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

	// No need to check for semicolon - TypeScript doesn't require it in mapped types

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
func (p *DtsParser) parseTemplateLiteralType() TypeAnn {
	start := p.expect(BackTick)
	if start == nil {
		return nil
	}

	parts := []TemplatePart{}

	for {
		token := p.peek()

		if token.Type == BackTick {
			// End of template
			end := p.consume()
			span := ast.Span{
				Start:    start.Span.Start,
				End:      end.Span.End,
				SourceID: start.Span.SourceID,
			}
			return &TemplateLiteralType{Parts: parts, span: span}
		}

		if token.Type == StrLit {
			// Template string part
			p.consume()
			parts = append(parts, &TemplateString{
				Value: token.Value,
				span:  token.Span,
			})
		} else if token.Type == Identifier && token.Value == "$" {
			// This might be the start of ${...}
			p.consume()
			if p.peek().Type == OpenBrace {
				p.consume() // consume '{'

				typeAnn := p.parseTypeAnn()
				if typeAnn == nil {
					p.reportError(p.peek().Span, "Expected type in template placeholder")
					break
				}

				closeBrace := p.expect(CloseBrace)
				if closeBrace == nil {
					break
				}

				templateTypeSpan := ast.Span{
					Start:    token.Span.Start,
					End:      closeBrace.Span.End,
					SourceID: token.Span.SourceID,
				}

				parts = append(parts, &TemplateType{
					Type: typeAnn,
					span: templateTypeSpan,
				})
			} else {
				// Just a $ character in the string
				parts = append(parts, &TemplateString{
					Value: "$",
					span:  token.Span,
				})
			}
		} else {
			// Unexpected token
			p.reportError(token.Span, "Unexpected token in template literal")
			break
		}
	}

	// If we get here without proper closing, return what we have
	if len(parts) > 0 {
		span := ast.Span{
			Start:    start.Span.Start,
			End:      parts[len(parts)-1].Span().End,
			SourceID: start.Span.SourceID,
		}
		return &TemplateLiteralType{Parts: parts, span: span}
	}

	return nil
}

// parseKeyOfType parses keyof T
func (p *DtsParser) parseKeyOfType() TypeAnn {
	start := p.expect(Keyof)
	if start == nil {
		return nil
	}

	typeAnn := p.parsePrimaryType()
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

// parseOptionalType parses T? (used in tuples and mapped types)
func (p *DtsParser) parseOptionalType(baseType TypeAnn) TypeAnn {
	question := p.expect(Question)
	if question == nil {
		return baseType
	}

	span := ast.Span{
		Start:    baseType.Span().Start,
		End:      question.Span.End,
		SourceID: baseType.Span().SourceID,
	}

	return &OptionalType{
		Type: baseType,
		span: span,
	}
}

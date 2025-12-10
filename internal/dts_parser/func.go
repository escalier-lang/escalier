package dts_parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

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

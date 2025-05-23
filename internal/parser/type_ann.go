package parser

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) typeAnn() (optional.Option[ast.TypeAnn], []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	// nolint: exhaustive
	switch token.Type {
	case Number:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewNumberTypeAnn(token.Span),
		), errors
	case String:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewStringTypeAnn(token.Span),
		), errors
	case Boolean:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewBooleanTypeAnn(token.Span),
		), errors
	case Null:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewLitTypeAnn(ast.NewNull(token.Span), token.Span),
		), errors
	case Undefined:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewLitTypeAnn(ast.NewUndefined(token.Span), token.Span),
		), errors
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			errors = append(errors, NewError(token.Span, "Expected a number"))
			return optional.None[ast.TypeAnn](), errors
		}
		return optional.Some[ast.TypeAnn](
			ast.NewLitTypeAnn(
				ast.NewNumber(value, token.Span),
				token.Span,
			),
		), errors
	case StrLit:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewLitTypeAnn(
				ast.NewString(token.Value, token.Span),
				token.Span,
			),
		), errors
	case Fn:
		p.lexer.consume()
		maybeTypeParams := optional.None[[]ast.TypeParam]()

		if p.lexer.peek().Type == LessThan {
			p.lexer.consume() // consume '<'
			typeParams, typeParamErrors := parseDelimSeq(p, GreaterThan, Comma, func() (optional.Option[ast.TypeParam], []*Error) {
				typeParam, typeParamErrors := p.typeParam()
				return typeParam, typeParamErrors
			})
			errors = append(errors, typeParamErrors...)
			maybeTypeParams = optional.Some(typeParams)

			_, tokenErrors := p.expect(GreaterThan, AlwaysConsume)
			errors = append(errors, tokenErrors...)
		}

		_, tokenErrors := p.expect(OpenParen, AlwaysConsume)
		errors = append(errors, tokenErrors...)

		funcParams, funcParamsErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
		errors = append(errors, funcParamsErrors...)

		_, tokenErrors = p.expect(CloseParen, AlwaysConsume)
		errors = append(errors, tokenErrors...)

		_, tokenErrors = p.expect(Arrow, AlwaysConsume)
		errors = append(errors, tokenErrors...)

		retTypeOption, retTypeErrors := p.typeAnn()
		errors = append(errors, retTypeErrors...)
		if retTypeOption.IsNone() {
			errors = append(errors, &Error{
				Span:    token.Span,
				Message: "expected return type annotation",
			})
			return optional.None[ast.TypeAnn](), errors
		}
		retType, _ := retTypeOption.Take()

		throws := optional.None[ast.TypeAnn]()

		return optional.Some[ast.TypeAnn](
			ast.NewFuncTypeAnn(
				maybeTypeParams,
				funcParams,
				retType,
				throws,
				ast.NewSpan(token.Span.Start, retType.Span().End),
			),
		), errors
	case OpenBracket: // tuple type
		p.lexer.consume()
		elemTypes, elemErrors := parseDelimSeq(p, CloseBracket, Comma, func() (optional.Option[ast.TypeAnn], []*Error) {
			typeArg, typeArgErrors := p.typeAnn()
			return typeArg, typeArgErrors
		})
		errors = append(errors, elemErrors...)
		end, endErrors := p.expect(CloseBracket, AlwaysConsume)
		errors = append(errors, endErrors...)
		return optional.Some[ast.TypeAnn](
			ast.NewTupleTypeAnn(elemTypes, ast.NewSpan(token.Span.Start, end)),
		), errors
	case OpenBrace: // object type
		p.lexer.consume() // consume '{'
		elems, propErrors := parseDelimSeq(p, CloseBrace, Comma, func() (optional.Option[ast.ObjTypeAnnElem], []*Error) {
			elem, elemErrors := p.objTypeAnnElem()
			if elem == nil {
				return optional.None[ast.ObjTypeAnnElem](), elemErrors
			}
			return optional.Some(elem), elemErrors
		})
		errors = append(errors, propErrors...)
		end, endErrors := p.expect(CloseBrace, AlwaysConsume)
		errors = append(errors, endErrors...)
		return optional.Some[ast.TypeAnn](
			ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end)),
		), errors
	case Identifier:
		p.lexer.consume()

		if p.lexer.peek().Type == LessThan {
			p.lexer.consume() // consume '<'
			typeArgs, typeArgErrors := parseDelimSeq(p, GreaterThan, Comma, func() (optional.Option[ast.TypeAnn], []*Error) {
				typeArg, typeArgErrors := p.typeAnn()
				return typeArg, typeArgErrors
			})
			end, endErrors := p.expect(GreaterThan, AlwaysConsume)
			return optional.Some[ast.TypeAnn](
				ast.NewRefTypeAnn(token.Value, typeArgs, ast.NewSpan(token.Span.Start, end)),
			), slices.Concat(typeArgErrors, endErrors)
		}

		return optional.Some[ast.TypeAnn](
			ast.NewRefTypeAnn(token.Value, []ast.TypeAnn{}, token.Span),
		), errors
	default:
		errors = append(errors, &Error{
			Span:    token.Span,
			Message: "expected type annotation",
		})
		p.lexer.consume()
		return optional.None[ast.TypeAnn](), errors
	}
}

func (p *Parser) objTypeAnnElem() (ast.ObjTypeAnnElem, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	mod := ""
	if token.Type == Get {
		p.lexer.consume() // consume 'get'
		mod = "get"
	} else if token.Type == Set {
		p.lexer.consume() // consume 'set'
		mod = "set"
	}

	objKeyOption, objKeyErrors := p.objExprKey()
	errors = append(errors, objKeyErrors...)
	if objKeyOption.IsNone() {
		return nil, errors
	}
	objKey := objKeyOption.Unwrap() // safe because we checked for None
	token = p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case CloseBrace:
		errors = append(errors, &Error{
			Span:    token.Span,
			Message: "expected type annotation",
		})

		var property ast.ObjTypeAnnElem = &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: false,
			Readonly: false, // TODO: handle readonly
			Value:    nil,
		}
		return property, errors
	case Comma:
		errors = append(errors, &Error{
			Span:    token.Span,
			Message: "expected type annotation",
		})

		var property ast.ObjTypeAnnElem = &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: false,
			Readonly: false, // TODO: handle readonly
			Value:    nil,
		}
		return property, errors
	case Colon:
		p.lexer.consume() // consume ':'

		property := &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: false,
			Readonly: false, // TODO: handle readonly
			Value:    nil,
		}

		token = p.lexer.peek()
		if token.Type == Comma {
			errors = append(errors, &Error{
				Span:    token.Span,
				Message: "expected type annotation",
			})
			return property, errors
		}

		value, valueErrors := p.typeAnn()
		errors = append(errors, valueErrors...)

		if value.IsNone() {
			token := p.lexer.peek()
			if token.Type == Comma {
				return property, errors
			}
		}

		value.IfSome(func(value ast.TypeAnn) {
			property.Value = value
		})

		return property, errors
	case Question:
		p.lexer.consume() // consume '?'
		_, expectErrors := p.expect(Colon, ConsumeOnMatch)
		errors = append(errors, expectErrors...)
		value, valueErrors := p.typeAnn()
		errors = append(errors, valueErrors...)
		return &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: true,
			Readonly: false, // TODO: handle readonly
			Value:    value.TakeOr(nil),
		}, errors
	case OpenParen:
		p.lexer.consume() // consume '('
		params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
		errors = append(errors, seqErrors...)
		_, expectErrors := p.expect(CloseParen, ConsumeOnMatch)
		errors = append(errors, expectErrors...)

		_, expectErrors = p.expect(Arrow, ConsumeOnMatch)
		errors = append(errors, expectErrors...)

		retTypeOption, retTypeErrors := p.typeAnn()
		errors = append(errors, retTypeErrors...)
		if retTypeOption.IsNone() {
			errors = append(errors, &Error{
				Span:    token.Span,
				Message: "expected return type annotation",
			})
			return nil, errors
		}
		retType, _ := retTypeOption.Take()

		fnTypeAnn := ast.NewFuncTypeAnn(
			optional.None[[]ast.TypeParam](),
			params,
			retType,
			optional.None[ast.TypeAnn](),
			ast.MergeSpans(token.Span, retType.Span()),
		)

		if mod == "get" {
			return &ast.GetterTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}, errors
		} else if mod == "set" {
			return &ast.SetterTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}, errors
		} else {
			return &ast.MethodTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}, errors
		}
	default:
		// skip over the token and return optional.None
		panic("objTypeAnnElem - not a valid property")
	}
}

func (p *Parser) typeParam() (optional.Option[ast.TypeParam], []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	if token.Type != Identifier {
		errors = append(errors, &Error{
			Span:    token.Span,
			Message: "expected type parameter",
		})
		p.lexer.consume()
		return optional.None[ast.TypeParam](), errors
	}

	p.lexer.consume() // consume identifier
	name := token.Value

	constraint := optional.None[ast.TypeAnn]()
	default_ := optional.None[ast.TypeAnn]()

	var parseErrors []*Error

	if p.lexer.peek().Type == Colon {
		p.lexer.consume() // consume ':'
		constraint, parseErrors = p.typeAnn()
		errors = append(errors, parseErrors...)
	}

	if p.lexer.peek().Type == Equal {
		p.lexer.consume() // consume '='
		default_, parseErrors = p.typeAnn()
		errors = append(errors, parseErrors...)
	}

	return optional.Some(
		ast.NewTypeParam(name, constraint, default_),
	), errors
}

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
			ast.NewNullTypeAnn(token.Span),
		), errors
	case Undefined:
		p.lexer.consume()
		return optional.Some[ast.TypeAnn](
			ast.NewUndefinedTypeAnn(token.Span),
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

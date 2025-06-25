package parser

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

type TypeAnnOpKind string

const (
	Union        TypeAnnOpKind = "union"
	Intersection TypeAnnOpKind = "intersection"
)

var typeAnnPrecedence = map[TypeAnnOpKind]int{
	Intersection: 4,
	Union:        3,
}

type TypeAnnOp struct {
	Kind  TypeAnnOpKind
	Arity int
}

func (p *Parser) typeAnn() (ast.TypeAnn, []*Error) {
	select {
	case <-p.ctx.Done():
		fmt.Println("Taking too long to parse")
	default:
		// continue
	}

	typeAnns := NewStack[ast.TypeAnn]()
	errors := []*Error{}
	ops := NewStack[*TypeAnnOp]()

	token := p.lexer.peek()
	//nolint: exhaustive
	switch token.Type {
	case Pipe:
		p.lexer.consume() // skip leading '|'
	case Ampersand:
		p.lexer.consume() // skip leading '&'
	default:
		// Nothing to skip, continue parsing
	}

	primary, primaryErrors := p.primaryTypeAnn()
	if primary == nil {
		return nil, primaryErrors
	}

	errors = append(errors, primaryErrors...)
	typeAnns.Push(primary)

loop:
	for {
		token := p.lexer.peek()
		var nextOp *TypeAnnOp

		// nolint: exhaustive
		switch token.Type {
		case Pipe:
			nextOp = &TypeAnnOp{
				Kind:  Union,
				Arity: 2,
			}
		case Ampersand:
			nextOp = &TypeAnnOp{
				Kind:  Intersection,
				Arity: 2,
			}
		case LineComment, BlockComment:
			p.lexer.consume()
			continue
		default:
			break loop
		}

		if token.Span.Start.Line != p.lexer.currentLocation.Line {
			if len(p.markers) == 0 || p.markers.Peek() != MarkerDelim {
				return typeAnns.Pop(), errors
			}
		}

		p.lexer.consume()
		skipOp := false

		if !ops.IsEmpty() {
			if ops.Peek().Kind == nextOp.Kind {
				ops.Peek().Arity++ // update the arity of the operator
				skipOp = true
			} else if typeAnnPrecedence[ops.Peek().Kind] >= typeAnnPrecedence[nextOp.Kind] {
				op := ops.Pop()
				arity := op.Arity
				args := make([]ast.TypeAnn, arity)
				for i := range arity {
					args[i] = typeAnns[len(typeAnns)-arity+i]
				}
				typeAnns = typeAnns[:len(typeAnns)-arity]
				span := ast.MergeSpans(args[0].Span(), args[arity-1].Span())

				switch op.Kind {
				case Union:
					typeAnns.Push(ast.NewUnionTypeAnn(args, span))
				case Intersection:
					typeAnns.Push(ast.NewIntersectionTypeAnn(args, span))
				default:
					// This should never happen, but just in case
					panic(fmt.Sprintf("Unknown type annotation operator: %s", op.Kind))
				}
			}
		}

		if !skipOp {
			ops.Push(nextOp)
		}

		typeAnn, primaryErrors := p.primaryTypeAnn()
		errors = append(errors, primaryErrors...)
		if typeAnn == nil {
			token := p.lexer.peek()
			errors = append(errors, NewError(token.Span, "Expected an type annotation"))

			// TODO: add an EmptyTypeAnn to the AST
			// For now, we panic to indicate that something went wrong
			panic("parseExpr - expected a TypeAnn, but got none")
			// return ast.NewEmpty(token.Span)
		}
		typeAnns.Push(typeAnn)
	}

	for !ops.IsEmpty() {
		op := ops.Pop()
		arity := op.Arity
		args := make([]ast.TypeAnn, arity)
		for i := range arity {
			args[i] = typeAnns[len(typeAnns)-arity+i]
		}
		typeAnns = typeAnns[:len(typeAnns)-arity]
		span := ast.MergeSpans(args[0].Span(), args[arity-1].Span())

		switch op.Kind {
		case Union:
			typeAnns.Push(ast.NewUnionTypeAnn(args, span))
		case Intersection:
			typeAnns.Push(ast.NewIntersectionTypeAnn(args, span))
		default:
			// This should never happen, but just in case
			panic(fmt.Sprintf("Unknown type annotation operator: %s", op.Kind))
		}
	}

	if len(typeAnns) != 1 {
		panic("parseExpr - expected one TypeAnn on the stack")
	}
	return typeAnns.Pop(), errors
}

func (p *Parser) primaryTypeAnn() (ast.TypeAnn, []*Error) {
	// TODO: parse prefixes, e.g. `mut`
	token := p.lexer.peek()
	errors := []*Error{}
	isMut := false

	if token.Type == Mut {
		p.lexer.consume() // consume 'mut'
		token = p.lexer.peek()
		isMut = true
	}

	var typeAnn ast.TypeAnn

	for typeAnn == nil {
		// nolint: exhaustive
		switch token.Type {
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		case Number:
			p.lexer.consume()
			typeAnn = ast.NewNumberTypeAnn(token.Span)
		case String:
			p.lexer.consume()
			typeAnn = ast.NewStringTypeAnn(token.Span)
		case Boolean:
			p.lexer.consume()
			typeAnn = ast.NewBooleanTypeAnn(token.Span)
		case Null:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(ast.NewNull(token.Span), token.Span)
		case Undefined:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(ast.NewUndefined(token.Span), token.Span)
		case NumLit:
			p.lexer.consume()
			value, err := strconv.ParseFloat(token.Value, 64)
			if err != nil {
				errors = append(errors, NewError(token.Span, "Expected a number"))
				return nil, errors
			}
			typeAnn = ast.NewLitTypeAnn(ast.NewNumber(value, token.Span), token.Span)
		case StrLit:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(
				ast.NewString(token.Value, token.Span),
				token.Span,
			)
		case Fn:
			p.lexer.consume()
			maybeTypeParams := optional.None[[]*ast.TypeParam]()

			if p.lexer.peek().Type == LessThan {
				p.lexer.consume() // consume '<'
				typeParams, typeParamErrors := parseDelimSeqNonOptional(p, GreaterThan, Comma, p.typeParam)
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

			retType, retTypeErrors := p.typeAnn()
			errors = append(errors, retTypeErrors...)
			if retType == nil {
				errors = append(errors, &Error{
					Span:    token.Span,
					Message: "expected return type annotation",
				})
				return nil, errors
			}

			throws := optional.None[ast.TypeAnn]()

			typeAnn = ast.NewFuncTypeAnn(
				maybeTypeParams,
				funcParams,
				retType,
				throws,
				ast.NewSpan(token.Span.Start, retType.Span().End),
			)
		case OpenBracket: // tuple type
			p.lexer.consume()
			elemTypes, elemErrors := parseDelimSeqNonOptional(p, CloseBracket, Comma, p.typeAnn)
			errors = append(errors, elemErrors...)
			end, endErrors := p.expect(CloseBracket, AlwaysConsume)
			errors = append(errors, endErrors...)
			typeAnn = ast.NewTupleTypeAnn(elemTypes, ast.NewSpan(token.Span.Start, end))
		case OpenBrace: // object type
			p.lexer.consume() // consume '{'
			elems, propErrors := parseDelimSeqNonOptional(p, CloseBrace, Comma, p.objTypeAnnElem)
			errors = append(errors, propErrors...)
			end, endErrors := p.expect(CloseBrace, AlwaysConsume)
			errors = append(errors, endErrors...)
			typeAnn = ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end))
		case Identifier:
			p.lexer.consume()

			// Try to parse a set of type parameters
			if p.lexer.peek().Type == LessThan {
				p.lexer.consume() // consume '<'
				typeArgs, typeArgErrors := parseDelimSeqNonOptional(p, GreaterThan, Comma, p.typeAnn)
				end, endErrors := p.expect(GreaterThan, AlwaysConsume)
				return ast.NewRefTypeAnn(token.Value, typeArgs, ast.NewSpan(token.Span.Start, end)),
					slices.Concat(typeArgErrors, endErrors)
			}

			typeAnn = ast.NewRefTypeAnn(token.Value, []ast.TypeAnn{}, token.Span)
		default:
			errors = append(errors, &Error{
				Span:    token.Span,
				Message: "expected type annotation",
			})
			p.lexer.consume()
			return nil, errors
		}
	}

	typeAnn, suffixErrors := p.typeAnnSuffix(typeAnn)
	errors = append(errors, suffixErrors...)

	if isMut {
		typeAnn = ast.NewMutableTypeAnn(typeAnn, token.Span)
	}

	return typeAnn, errors
}

func (p *Parser) typeAnnSuffix(typeAnn ast.TypeAnn) (ast.TypeAnn, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

loop:
	for {
		// nolint: exhaustive
		switch token.Type {
		case OpenBracket:
			p.lexer.consume()
			// TODO: handle the case when parseExpr() return None correctly
			index, indexErrors := p.typeAnn()
			errors = append(errors, indexErrors...)
			if index == nil {
				errors = append(errors, NewError(token.Span, "Expected an expression after '['"))
				break loop
			}
			terminator := p.lexer.next()
			if terminator.Type != CloseBracket {
				errors = append(errors, NewError(token.Span, "Expected a closing bracket"))
			}
			obj := typeAnn
			typeAnn = ast.NewIndexTypeAnn(
				obj, index,
				ast.Span{Start: obj.Span().Start, End: terminator.Span.End},
			)
		case Dot:
			p.lexer.consume()
			prop := p.lexer.next()
			// nolint: exhaustive
			switch prop.Type {
			case Identifier, Underscore:
				obj := typeAnn
				// This interprets T.K as T["K"]
				prop := ast.NewLitTypeAnn(ast.NewString(prop.Value, token.Span), token.Span)
				typeAnn = ast.NewIndexTypeAnn(
					obj, prop,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End},
				)
			default:
				obj := typeAnn
				// This interprets T. as T[""]
				prop := ast.NewLitTypeAnn(ast.NewString("", token.Span), token.Span)
				typeAnn = ast.NewIndexTypeAnn(
					obj, prop,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End},
				)
				if token.Type == Dot {
					errors = append(errors, NewError(token.Span, "expected an identifier after ."))
				} else {
					errors = append(errors, NewError(token.Span, "expected an identifier after ?."))
				}
			}
		default:
			break loop
		}
		token = p.lexer.peek()
	}

	return typeAnn, errors
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

		if value == nil {
			token := p.lexer.peek()
			if token.Type == Comma {
				return property, errors
			}
		}

		property.Value = value

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
			Value:    value,
		}, errors
	case OpenParen:
		p.lexer.consume() // consume '('
		params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
		errors = append(errors, seqErrors...)
		_, expectErrors := p.expect(CloseParen, ConsumeOnMatch)
		errors = append(errors, expectErrors...)

		_, expectErrors = p.expect(Arrow, ConsumeOnMatch)
		errors = append(errors, expectErrors...)

		retType, retTypeErrors := p.typeAnn()
		errors = append(errors, retTypeErrors...)
		if retType == nil {
			errors = append(errors, &Error{
				Span:    token.Span,
				Message: "expected return type annotation",
			})
			return nil, errors
		}

		fnTypeAnn := ast.NewFuncTypeAnn(
			optional.None[[]*ast.TypeParam](),
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

func (p *Parser) typeParam() (*ast.TypeParam, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	if token.Type != Identifier {
		errors = append(errors, &Error{
			Span:    token.Span,
			Message: "expected type parameter",
		})
		p.lexer.consume()
		return nil, errors
	}

	p.lexer.consume() // consume identifier
	name := token.Value

	var constraint ast.TypeAnn
	var default_ ast.TypeAnn

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

	typeParam := ast.NewTypeParam(name, constraint, default_)
	return &typeParam, errors
}

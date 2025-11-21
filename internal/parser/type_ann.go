package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
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

func (p *Parser) typeAnn() ast.TypeAnn {
	select {
	case <-p.ctx.Done():
		fmt.Println("Taking too long to parse")
	default:
		// continue
	}

	typeAnns := NewStack[ast.TypeAnn]()
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

	primary := p.primaryTypeAnn()
	if primary == nil {
		return nil
	}

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

		typeAnn := p.primaryTypeAnn()
		if typeAnn == nil {
			token := p.lexer.peek()
			p.reportError(token.Span, "Expected an type annotation")

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
	return typeAnns.Pop()
}

func (p *Parser) primaryTypeAnn() ast.TypeAnn {
	token := p.lexer.peek()
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
		case Symbol:
			p.lexer.consume()
			typeAnn = ast.NewSymbolTypeAnn(token.Span)
		case Unique:
			p.lexer.consume()
			if p.lexer.peek().Type == Symbol {
				symbolToken := p.lexer.next()
				typeAnn = ast.NewUniqueSymbolTypeAnn(ast.MergeSpans(token.Span, symbolToken.Span))
			} else {
				p.reportError(token.Span, "expected 'symbol' after 'unique'")
				return nil
			}
		case Any:
			p.lexer.consume()
			typeAnn = ast.NewAnyTypeAnn(token.Span)
		case Unknown:
			p.lexer.consume()
			typeAnn = ast.NewUnknownTypeAnn(token.Span)
		case Never:
			p.lexer.consume()
			typeAnn = ast.NewNeverTypeAnn(token.Span)
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
				p.reportError(token.Span, "Expected a number")
				return nil
			}
			typeAnn = ast.NewLitTypeAnn(ast.NewNumber(value, token.Span), token.Span)
		case StrLit:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(
				ast.NewString(token.Value, token.Span),
				token.Span,
			)
		case True:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(
				ast.NewBoolean(true, token.Span),
				token.Span,
			)
		case False:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(
				ast.NewBoolean(false, token.Span),
				token.Span,
			)
		case RegexLit:
			p.lexer.consume()
			typeAnn = ast.NewLitTypeAnn(
				ast.NewRegex(token.Value, token.Span),
				token.Span,
			)
		case Fn:
			p.lexer.consume()
			var typeParams []*ast.TypeParam

			if p.lexer.peek().Type == LessThan {
				p.lexer.consume() // consume '<'
				typeParams = parseDelimSeq(p, GreaterThan, Comma, p.typeParam)

				p.expect(GreaterThan, AlwaysConsume)
			}

			p.expect(OpenParen, AlwaysConsume)

			funcParams := parseDelimSeq(p, CloseParen, Comma, p.param)

			p.expect(CloseParen, AlwaysConsume)

			p.expect(Arrow, AlwaysConsume)

			retType := p.typeAnn()
			if retType == nil {
				p.reportError(token.Span, "expected return type annotation")
				return nil
			}

			typeAnn = ast.NewFuncTypeAnn(
				typeParams,
				funcParams,
				retType,
				nil, // TODO: support throws clause
				ast.NewSpan(token.Span.Start, retType.Span().End, p.lexer.source.ID),
			)
		case If: // conditional type
			p.lexer.consume() // consume 'if'
			checkType := p.typeAnn()
			if checkType == nil {
				p.reportError(token.Span, "expected check type for conditional type")
				return nil
			}
			p.expect(Colon, AlwaysConsume)
			extendsType := p.typeAnn()
			if extendsType == nil {
				p.reportError(token.Span, "expected extends type for conditional type")
				return nil
			}
			p.expect(OpenBrace, AlwaysConsume)
			thenType := p.typeAnn()
			if thenType == nil {
				p.reportError(token.Span, "expected then type for conditional type")
				return nil
			}
			p.expect(CloseBrace, AlwaysConsume)
			p.expect(Else, AlwaysConsume)

			// Check if this is an 'else if' or a final 'else'
			var elseType ast.TypeAnn
			nextToken := p.lexer.peek()
			if nextToken.Type == If {
				// Parse 'else if' - recursively parse another conditional type
				elseType = p.primaryTypeAnn() // This will parse the nested conditional
			} else {
				// Parse final 'else' clause
				p.expect(OpenBrace, AlwaysConsume)
				elseType = p.typeAnn()
				if elseType == nil {
					p.reportError(nextToken.Span, "expected else type for conditional type")
					return nil
				}
				p.expect(CloseBrace, AlwaysConsume)
			}

			typeAnn = ast.NewCondTypeAnn(
				checkType,
				extendsType,
				thenType,
				elseType,
				ast.NewSpan(token.Span.Start, elseType.Span().End, p.lexer.source.ID),
			)
		case Infer: // infer type
			p.lexer.consume() // consume 'infer'
			nameToken := p.lexer.peek()
			if nameToken.Type != Identifier {
				p.reportError(nameToken.Span, "expected identifier after 'infer'")
				return nil
			}
			p.lexer.consume() // consume identifier
			typeAnn = ast.NewInferTypeAnn(
				nameToken.Value,
				ast.NewSpan(token.Span.Start, nameToken.Span.End, p.lexer.source.ID),
			)
		case Keyof: // keyof type
			p.lexer.consume() // consume 'keyof'
			typ := p.primaryTypeAnn()
			if typ == nil {
				p.reportError(token.Span, "expected type annotation after 'keyof'")
				return nil
			}
			typeAnn = ast.NewKeyOfTypeAnn(
				typ,
				ast.NewSpan(token.Span.Start, typ.Span().End, p.lexer.source.ID),
			)
		case Typeof: // typeof value
			p.lexer.consume() // consume 'typeof'
			// Parse the identifier that refers to a value
			identToken := p.lexer.peek()
			if identToken.Type != Identifier {
				p.reportError(token.Span, "expected identifier after 'typeof'")
				return nil
			}
			p.lexer.consume() // consume identifier
			qualIdent := p.parseQualifiedIdent(identToken)
			typeAnn = ast.NewTypeOfTypeAnn(qualIdent, qualIdent.Span())
		case OpenBracket: // tuple type
			p.lexer.consume()
			elemTypes := parseDelimSeq(p, CloseBracket, Comma, p.typeAnn)
			end := p.expect(CloseBracket, AlwaysConsume)
			typeAnn = ast.NewTupleTypeAnn(elemTypes, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))
		case OpenBrace: // object type
			p.lexer.consume() // consume '{'
			elems := parseDelimSeq(p, CloseBrace, Comma, p.objTypeAnnElem)
			end := p.expect(CloseBrace, AlwaysConsume)
			typeAnn = ast.NewObjectTypeAnn(elems, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))
		case Identifier:
			p.lexer.consume()

			// Parse qualified identifier (e.g., Foo.Bar.Baz)
			qualIdent := p.parseQualifiedIdent(token)

			// Try to parse a set of type parameters
			if p.lexer.peek().Type == LessThan {
				p.lexer.consume() // consume '<'
				typeArgs := parseDelimSeq(p, GreaterThan, Comma, p.typeAnn)
				end := p.expect(GreaterThan, AlwaysConsume)
				typeAnn = ast.NewRefTypeAnn(qualIdent, typeArgs, ast.NewSpan(token.Span.Start, end, p.lexer.source.ID))
			} else {
				typeAnn = ast.NewRefTypeAnn(qualIdent, []ast.TypeAnn{}, getQualIdentSpan(qualIdent))
			}
		case BackTick: // template literal type
			typeAnn = p.templateLitTypeAnn(token)
		default:
			p.reportError(token.Span, "expected type annotation")
			p.lexer.consume()
			return nil
		}
	}

	typeAnn = p.typeAnnSuffix(typeAnn)

	if isMut {
		typeAnn = ast.NewMutableTypeAnn(typeAnn, token.Span)
	}

	return typeAnn
}

func (p *Parser) typeAnnSuffix(typeAnn ast.TypeAnn) ast.TypeAnn {
	token := p.lexer.peek()

loop:
	for {
		// nolint: exhaustive
		switch token.Type {
		case OpenBracket:
			p.lexer.consume()
			// TODO: handle the case when parseExpr() return None correctly
			index := p.typeAnn()
			if index == nil {
				p.reportError(token.Span, "Expected an expression after '['")
				break loop
			}
			terminator := p.lexer.next()
			if terminator.Type != CloseBracket {
				p.reportError(token.Span, "Expected a closing bracket")
			}
			obj := typeAnn
			typeAnn = ast.NewIndexTypeAnn(
				obj, index,
				ast.Span{Start: obj.Span().Start, End: terminator.Span.End, SourceID: p.lexer.source.ID},
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
					ast.Span{Start: obj.Span().Start, End: prop.Span().End, SourceID: p.lexer.source.ID},
				)
			default:
				obj := typeAnn
				// This interprets T. as T[""]
				prop := ast.NewLitTypeAnn(ast.NewString("", token.Span), token.Span)
				typeAnn = ast.NewIndexTypeAnn(
					obj, prop,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End, SourceID: p.lexer.source.ID},
				)
				if token.Type == Dot {
					p.reportError(token.Span, "expected an identifier after .")
				} else {
					p.reportError(token.Span, "expected an identifier after ?.")
				}
			}
		default:
			break loop
		}
		token = p.lexer.peek()
	}

	return typeAnn
}

func (p *Parser) tryParseMappedType() *ast.MappedTypeAnn {
	// Syntax:
	// {[K]: T[K] for K in T}

	token := p.lexer.peek()
	if token.Type == OpenBracket {
		savedState := p.saveState()

		p.lexer.consume() // consume '['
		name := p.typeAnn()
		if name == nil {
			p.reportError(token.Span, "expected name for mapped type")
			p.restoreState(savedState)
			return nil
		}

		p.expect(CloseBracket, AlwaysConsume)
		p.expect(Colon, AlwaysConsume)

		value := p.typeAnn()

		p.expect(For, AlwaysConsume)
		token = p.lexer.peek()
		var key string
		if token.Type == Identifier {
			p.lexer.consume() // consume identifier
			key = token.Value
		} else {
			p.reportError(token.Span, "expected identifier for mapped type key")
			p.restoreState(savedState)
			return nil
		}
		p.expect(In, AlwaysConsume)
		constraint := p.typeAnn()

		// TODO: try to parse a mapped type
		return &ast.MappedTypeAnn{
			TypeParam: &ast.IndexParamTypeAnn{
				Name:       key,
				Constraint: constraint,
			},
			Name:     nil, // TODO: handle renaming
			Value:    value,
			Optional: nil, // TODO: handle optional
			ReadOnly: nil, // TODO: handle readonly
		}
	}

	return nil
}

func (p *Parser) objTypeAnnElem() ast.ObjTypeAnnElem {
	token := p.lexer.peek()

	// Handle rest spread syntax: ...T
	if token.Type == DotDotDot {
		p.lexer.consume() // consume '...'
		value := p.typeAnn()
		if value == nil {
			p.reportError(token.Span, "expected type annotation after '...'")
			return nil
		}
		span := ast.MergeSpans(token.Span, value.Span())
		return ast.NewRestSpreadTypeAnn(value, span)
	}

	mod := ""
	if token.Type == Get {
		p.lexer.consume() // consume 'get'
		mod = "get"
	} else if token.Type == Set {
		p.lexer.consume() // consume 'set'
		mod = "set"
	}

	mappedElem := p.tryParseMappedType()
	if mappedElem != nil {
		return mappedElem
	}

	objKey := p.objExprKey()
	if objKey == nil {
		return nil
	}
	token = p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case CloseBrace:
		p.reportError(token.Span, "expected type annotation")

		var property ast.ObjTypeAnnElem = &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: false,
			Readonly: false, // TODO: handle readonly
			Value:    nil,
		}
		return property
	case Comma:
		p.reportError(token.Span, "expected type annotation")

		var property ast.ObjTypeAnnElem = &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: false,
			Readonly: false, // TODO: handle readonly
			Value:    nil,
		}
		return property
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
			p.reportError(token.Span, "expected type annotation")
			return property
		}

		value := p.typeAnn()

		if value == nil {
			token := p.lexer.peek()
			if token.Type == Comma {
				return property
			}
		}

		property.Value = value

		return property
	case Question:
		p.lexer.consume() // consume '?'
		p.expect(Colon, ConsumeOnMatch)
		value := p.typeAnn()
		return &ast.PropertyTypeAnn{
			Name:     objKey,
			Optional: true,
			Readonly: false, // TODO: handle readonly
			Value:    value,
		}
	case OpenParen:
		p.lexer.consume() // consume '('
		params := parseDelimSeq(p, CloseParen, Comma, p.param)
		p.expect(CloseParen, ConsumeOnMatch)

		p.expect(Arrow, ConsumeOnMatch)

		retType := p.typeAnn()
		if retType == nil {
			p.reportError(token.Span, "expected return type annotation")
			return nil
		}

		fnTypeAnn := ast.NewFuncTypeAnn(
			nil, // TODO: support type parameters on methods
			params,
			retType,
			nil, // TODO: support throws clause
			ast.MergeSpans(token.Span, retType.Span()),
		)

		if mod == "get" {
			return &ast.GetterTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}
		} else if mod == "set" {
			return &ast.SetterTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}
		} else {
			return &ast.MethodTypeAnn{
				Name: objKey,
				Fn:   fnTypeAnn,
			}
		}
	default:
		// skip over the token and return optional.None
		panic("objTypeAnnElem - not a valid property")
	}
}

func (p *Parser) typeParam() *ast.TypeParam {
	token := p.lexer.peek()

	if token.Type != Identifier {
		p.reportError(token.Span, "expected type parameter")
		p.lexer.consume()
		return nil
	}

	p.lexer.consume() // consume identifier
	name := token.Value

	var constraint ast.TypeAnn
	var default_ ast.TypeAnn

	if p.lexer.peek().Type == Colon {
		p.lexer.consume() // consume ':'
		constraint = p.typeAnn()
	}

	if p.lexer.peek().Type == Equal {
		p.lexer.consume() // consume '='
		default_ = p.typeAnn()
	}

	typeParam := ast.NewTypeParam(name, constraint, default_)
	return &typeParam
}

// parseQualifiedIdent parses a qualified identifier like Foo.Bar.Baz
func (p *Parser) parseQualifiedIdent(firstToken *Token) ast.QualIdent {
	// Start with the first identifier
	var qualIdent ast.QualIdent = ast.NewIdentifier(firstToken.Value, firstToken.Span)

	// Check if there are more parts separated by dots
	for p.lexer.peek().Type == Dot {
		// Save state to peek ahead
		savedState := p.lexer.saveState()
		p.lexer.consume() // consume the dot
		nextToken := p.lexer.peek()

		// If the next token is not an identifier, restore state and break
		if nextToken.Type != Identifier {
			p.lexer.restoreState(savedState)
			break
		}

		// It is an identifier, so consume it
		nextToken = p.lexer.next()
		nextIdent := ast.NewIdentifier(nextToken.Value, nextToken.Span)

		// Create a Member with the current qualified identifier as left and new identifier as right
		qualIdent = &ast.Member{
			Left:  qualIdent,
			Right: nextIdent,
		}
	}

	return qualIdent
}

// getQualIdentSpan returns the span of a qualified identifier
func getQualIdentSpan(qi ast.QualIdent) ast.Span {
	switch q := qi.(type) {
	case *ast.Ident:
		return q.Span()
	case *ast.Member:
		leftSpan := getQualIdentSpan(q.Left)
		rightSpan := q.Right.Span()
		return ast.MergeSpans(leftSpan, rightSpan)
	default:
		panic("getQualIdentSpan - unknown QualIdent type")
	}
}

// templateLitTypeAnn parses a template literal type annotation like `${T}-${U}`
func (p *Parser) templateLitTypeAnn(token *Token) ast.TypeAnn {
	p.lexer.consume() // consume backtick
	quasis := []*ast.Quasi{}
	typeAnns := []ast.TypeAnn{}
	for {
		quasi := p.lexer.lexQuasi()

		var raw string
		if strings.HasSuffix(quasi.Value, "`") {
			raw = quasi.Value[:len(quasi.Value)-1]
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			break
		} else if strings.HasSuffix(quasi.Value, "${") {
			raw = quasi.Value[:len(quasi.Value)-2]
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			typeAnn := p.typeAnn()
			if typeAnn != nil {
				typeAnns = append(typeAnns, typeAnn)
			}
			p.lexer.consume() // consumes the closing brace
		} else {
			// This case happens when the template literal is not closed which
			// means we've reached the end of the file.
			raw = quasi.Value
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			span := ast.Span{Start: token.Span.Start, End: quasi.Span.End, SourceID: p.lexer.source.ID}
			p.reportError(span, "Expected a closing backtick")
			break
		}
	}
	span := ast.NewSpan(token.Span.Start, p.lexer.currentLocation, p.lexer.source.ID)
	return ast.NewTemplateLitTypeAnn(quasis, typeAnns, span)
}

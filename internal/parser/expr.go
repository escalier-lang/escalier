package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

var precedence = map[ast.BinaryOp]int{
	ast.Times:             12,
	ast.Divide:            12,
	ast.Modulo:            12,
	ast.Plus:              11,
	ast.Minus:             11,
	ast.Assign:            10,
	ast.LessThan:          9,
	ast.LessThanEqual:     9,
	ast.GreaterThan:       9,
	ast.GreaterThanEqual:  9,
	ast.Equal:             8,
	ast.NotEqual:          8,
	ast.LogicalAnd:        4,
	ast.LogicalOr:         3,
	ast.NullishCoalescing: 3,
}

func (p *Parser) exprWithMarker(marker Marker) (optional.Option[ast.Expr], []*Error) {
	p.markers.Push(marker)
	defer p.markers.Pop()
	return p.exprInternal()
}

func (p *Parser) nonDelimitedExpr() (ast.Expr, []*Error) {
	exprOption, exprError := p.exprWithMarker(MarkerExpr)
	return exprOption.Unwrap(), exprError
}

func (p *Parser) expr() (optional.Option[ast.Expr], []*Error) {
	expr, errors := p.exprWithMarker(MarkerDelim)
	expr = expr.OrElse(func() optional.Option[ast.Expr] {
		token := p.lexer.peek()
		errors = append(errors, NewError(token.Span, "Expected an expression"))
		return optional.Some[ast.Expr](ast.NewEmpty(token.Span))
	})
	return expr, errors
}

func (p *Parser) exprInternal() (optional.Option[ast.Expr], []*Error) {
	select {
	case <-p.ctx.Done():
		fmt.Println("Taking too long to parse")
	default:
		// continue
	}

	values := NewStack[ast.Expr]()
	ops := NewStack[ast.BinaryOp]()

	primary, primaryErrors := p.primaryExpr()
	if primary == nil {
		return nil, primaryErrors
	}

	errors := []*Error{}
	errors = append(errors, primaryErrors...)
	values = append(values, primary)

loop:
	for {
		token := p.lexer.peek()
		var nextOp ast.BinaryOp

		// nolint: exhaustive
		switch token.Type {
		case Plus:
			nextOp = ast.Plus
		case Minus:
			nextOp = ast.Minus
		case Asterisk:
			nextOp = ast.Times
		case Slash:
			nextOp = ast.Divide
		case Equal:
			nextOp = ast.Assign
		case EqualEqual:
			nextOp = ast.Equal
		case NotEqual:
			nextOp = ast.NotEqual
		case LessThan:
			nextOp = ast.LessThan
		case LessThanEqual:
			nextOp = ast.LessThanEqual
		case GreaterThan:
			nextOp = ast.GreaterThan
		case GreaterThanEqual:
			nextOp = ast.GreaterThanEqual
		case LineComment, BlockComment:
			p.lexer.consume()
			continue
		default:
			break loop
		}

		if token.Span.Start.Line != p.lexer.currentLocation.Line {
			if len(p.markers) == 0 || p.markers.Peek() != MarkerDelim {
				return optional.Some(values.Pop()), errors
			}
		}

		p.lexer.consume()

		if !ops.IsEmpty() {
			if precedence[ops.Peek()] >= precedence[nextOp] {
				// get the last operator and remove it from the list
				op := ops.Pop()
				right := values.Pop()
				left := values.Pop()

				values.Push(ast.NewBinary(left, right, op, ast.Span{Start: left.Span().Start, End: right.Span().End}))
			}
		}

		ops.Push(nextOp)
		expr, primaryErrors := p.primaryExpr()
		errors = append(errors, primaryErrors...)
		if expr == nil {
			token := p.lexer.peek()
			errors = append(errors, NewError(token.Span, "Expected an expression"))
			expr = ast.NewEmpty(token.Span)
		}
		values.Push(expr)
	}

	for !ops.IsEmpty() {
		op := ops.Pop()
		right := values.Pop()
		left := values.Pop()

		values.Push(ast.NewBinary(left, right, op, ast.Span{Start: left.Span().Start, End: right.Span().End}))
	}

	if len(values) != 1 {
		panic("parseExpr - expected one value on the stack")
	}
	return optional.Some(values.Pop()), errors
}

type TokenAndOp struct {
	Token *Token
	Op    ast.UnaryOp
}

func (p *Parser) parsePrefix() Stack[TokenAndOp] {
	token := p.lexer.peek()
	result := NewStack[TokenAndOp]()

loop:
	for {
		// nolint: exhaustive
		switch token.Type {
		case Plus:
			result.Push(TokenAndOp{Token: token, Op: ast.UnaryPlus})
		case Minus:
			result.Push(TokenAndOp{Token: token, Op: ast.UnaryMinus})
		default:
			break loop
		}
		p.lexer.consume()
		token = p.lexer.peek()
	}

	return result
}

func (p *Parser) exprSuffix(expr ast.Expr) (ast.Expr, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

loop:
	for {
		// nolint: exhaustive
		switch token.Type {
		case OpenParen, QuestionOpenParen:
			p.lexer.consume()
			args, argsErrors := parseDelimSeq(p, CloseParen, Comma, p.expr)
			errors = append(errors, argsErrors...)
			terminator := p.lexer.next()
			if terminator.Type != CloseParen {
				errors = append(errors, NewError(token.Span, "Expected a closing paren"))
			}
			callee := expr
			optChain := false
			if token.Type == QuestionOpenParen {
				optChain = true
			}
			expr = ast.NewCall(
				callee, args, optChain,
				ast.Span{Start: callee.Span().Start, End: terminator.Span.End},
			)
		case OpenBracket, QuestionOpenBracket:
			p.lexer.consume()
			// TODO: handle the case when parseExpr() return None correctly
			indexOption, indexErrors := p.expr()
			errors = append(errors, indexErrors...)
			if indexOption.IsNone() {
				errors = append(errors, NewError(token.Span, "Expected an expression after '['"))
				break loop
			}
			terminator := p.lexer.next()
			if terminator.Type != CloseBracket {
				errors = append(errors, NewError(token.Span, "Expected a closing bracket"))
			}
			obj := expr
			optChain := false
			if token.Type == QuestionOpenBracket {
				optChain = true
			}
			expr = ast.NewIndex(
				obj, indexOption.Unwrap(), optChain,
				ast.Span{Start: obj.Span().Start, End: terminator.Span.End},
			)
		case Dot, QuestionDot:
			p.lexer.consume()
			prop := p.lexer.next()
			optChain := false
			if token.Type == QuestionDot {
				optChain = true
			}
			// nolint: exhaustive
			switch prop.Type {
			case Identifier, Underscore:
				obj := expr
				prop := ast.NewIdentifier(prop.Value, prop.Span)
				expr = ast.NewMember(
					obj, prop, optChain,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End},
				)
			default:
				obj := expr
				prop := ast.NewIdentifier(
					"",
					ast.Span{Start: token.Span.End, End: token.Span.End},
				)
				expr = ast.NewMember(
					obj, prop, optChain,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End},
				)
				if token.Type == Dot {
					errors = append(errors, NewError(token.Span, "expected an identifier after ."))
				} else {
					errors = append(errors, NewError(token.Span, "expected an identifier after ?."))
				}
			}
		case BackTick:
			temp, tempErrors := p.templateLitExpr(token, optional.Some(expr))
			errors = append(errors, tempErrors...)
			expr = temp
		default:
			break loop
		}
		token = p.lexer.peek()
	}

	return expr, errors
}

func (p *Parser) objExprKey() (optional.Option[ast.ObjKey], []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	// nolint: exhaustive
	switch token.Type {
	case Identifier, Underscore:
		p.lexer.consume()
		return optional.Some[ast.ObjKey](
			ast.NewIdent(token.Value, token.Span),
		), []*Error{}
	case StrLit:
		p.lexer.consume()
		return optional.Some[ast.ObjKey](
			ast.NewString(token.Value, token.Span),
		), []*Error{}
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			errors = append(errors, NewError(token.Span, "Expected a number"))
		}
		return optional.Some[ast.ObjKey](
			ast.NewNumber(value, token.Span),
		), errors
	case OpenBracket:
		p.lexer.consume()
		expr, exprErrors := p.expr()
		errors = append(errors, exprErrors...)
		_, expectErrors := p.expect(CloseBracket, AlwaysConsume)
		errors = append(errors, expectErrors...)
		return optional.Map(expr, func(expr ast.Expr) ast.ObjKey {
			return &ast.ComputedKey{Expr: expr}
		}), errors
	default:
		return optional.None[ast.ObjKey](), []*Error{NewError(token.Span, "Expected a property name")}
	}
}

func (p *Parser) primaryExpr() (ast.Expr, []*Error) {
	ops := p.parsePrefix()
	token := p.lexer.peek()
	errors := []*Error{}

	var expr ast.Expr

	// Loop so that we can skip over unexpected tokens
	for expr == nil {
		// nolint: exhaustive
		switch token.Type {
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		case NumLit:
			p.lexer.consume()
			value, err := strconv.ParseFloat(token.Value, 64)
			if err != nil {
				errors = append(errors, NewError(token.Span, "Expected a number"))
				// TODO: return an EmptyExpr instead of nil
				return nil, errors
			}
			expr = ast.NewLitExpr(ast.NewNumber(value, token.Span))
		case StrLit:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewString(token.Value, token.Span))
		case True:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewBoolean(true, token.Span))
		case False:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewBoolean(false, token.Span))
		case Null:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewNull(token.Span))
		case Undefined:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewUndefined(token.Span))
		case Identifier, Underscore:
			p.lexer.consume()
			expr = ast.NewIdent(token.Value, token.Span)
		case OpenParen:
			p.lexer.consume()
			// TODO: handle the case when parseExpr() return None
			exprOption, exprErrors := p.expr()
			errors = append(errors, exprErrors...)
			if exprOption.IsNone() {
				errors = append(errors, NewError(token.Span, "Expected an expression after '('"))
				return nil, errors
			}
			expr = exprOption.Unwrap() // safe because we checked for None
			_, expectErrors := p.expect(CloseParen, AlwaysConsume)
			errors = append(errors, expectErrors...)
		case OpenBracket:
			p.lexer.consume()
			elems, seqErrors := parseDelimSeq(p, CloseBracket, Comma, p.expr)
			errors = append(errors, seqErrors...)
			end, endErrors := p.expect(CloseBracket, AlwaysConsume)
			errors = append(errors, endErrors...)
			expr = ast.NewArray(elems, ast.Span{Start: token.Span.Start, End: end})
		case OpenBrace:
			p.lexer.consume()
			elems, seqErrors := parseDelimSeq(p, CloseBrace, Comma, p.objExprElem)
			errors = append(errors, seqErrors...)
			end, endErrors := p.expect(CloseBrace, AlwaysConsume)
			errors = append(errors, endErrors...)
			expr = ast.NewObject(elems, ast.Span{Start: token.Span.Start, End: end})
		case BackTick:
			temp, tempErrors := p.templateLitExpr(token, nil)
			errors = append(errors, tempErrors...)
			expr = temp
		case Fn:
			fnExpr, fnError := p.fnExpr(token.Span.Start)
			errors = append(errors, fnError...)
			return fnExpr, errors
		case If:
			return p.ifElse()
		case LessThan:
			// TODO: figure out how to cast this more directly.
			jsx, jsxErrors := p.jsxElement()
			errors = append(errors, jsxErrors...)
			return jsx, errors
		case
			Val, Var, Return,
			CloseBrace, CloseParen, CloseBracket,
			EndOfFile:
			// Every call to `parseExpr()` should check if `nil` is returned so
			// that we can raise an error if we were expecting an expression.
			// We could also have a function like `maybeParseExpr()` that is okay
			// with return `nil` whereas `parseExpr()` would return an error if
			// `nil` is returned.
			return nil, errors
		default:
			p.lexer.consume()
			errors = append(
				errors,
				NewError(token.Span, fmt.Sprintf("Unexpected token, '%s'", token.Value)),
			)
			token = p.lexer.peek()
		}
	}

	expr, suffixErrors := p.exprSuffix(expr)
	errors = append(errors, suffixErrors...)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = ast.NewUnary(tokenAndOp.Op, expr, ast.Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span().End})
	}

	return expr, errors
}

// fnExpr = 'fn' '(' param (',' param)* ')' block
// TODO: dedupe with `fnDecl`
func (p *Parser) fnExpr(start ast.Location) (ast.Expr, []*Error) {
	errors := []*Error{}
	// TODO: allow an optional identifier
	// token := parser.lexer.peek()
	// _ident, ok := token.(*TIdentifier)
	// var ident *ast.Ident
	// if ok {
	// 	parser.lexer.consume()
	// 	ident = ast.NewIdentifier(_ident.Value, token.Span)
	// } else {
	// 	parser.reportError(token.Span, "Expected identifier")
	// 	ident = ast.NewIdentifier(
	// 		"",
	// 		ast.Span{Start: token.Span.Start, End: token.Span.Start},
	// 	)
	// }
	p.lexer.consume() // consume the fn keyword

	_, expectErrors := p.expect(OpenParen, ConsumeOnMatch)
	errors = append(errors, expectErrors...)
	params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
	errors = append(errors, seqErrors...)
	_, expectErrors = p.expect(CloseParen, ConsumeOnMatch)
	errors = append(errors, expectErrors...)

	body, bodyErrors := p.block()
	errors = append(errors, bodyErrors...)
	end := body.Span.End

	// TODO: parse return and throws types
	return ast.NewFuncExpr(
		[]*ast.TypeParam{}, // TODO
		params,
		optional.None[ast.TypeAnn](),
		optional.None[ast.TypeAnn](),
		body,
		ast.NewSpan(start, end),
	), errors
}

func (p *Parser) objExprElem() (optional.Option[ast.ObjExprElem], []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	if token.Type == DotDotDot {
		p.lexer.consume() // consume '...'
		arg, argErrors := p.expr()
		errors = append(errors, argErrors...)
		arg.IfNone(func() {
			errors = append(errors, NewError(token.Span, "Expected an expression after '...'"))
		})
		if arg.IsSome() {
			arg := optional.Map(arg, func(arg ast.Expr) ast.ObjExprElem {
				return ast.NewRestSpread(arg, ast.MergeSpans(token.Span, arg.Span()))
			})
			return arg, errors
		}
	}

	// TODO: raise an error if 'get' or 'set' is used with a property definition
	// instead of a method.
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
		return optional.None[ast.ObjExprElem](), errors
	}
	objKey := objKeyOption.Unwrap() // safe because we checked for None
	token = p.lexer.peek()

	// TODO: loop until we find a ':', '?', '(', ',' or '}' so
	// that we can skip over unexpected tokens

	// nolint: exhaustive
	switch token.Type {
	case Colon:
		p.lexer.consume() // consume ':'
		value, valueErrors := p.expr()
		errors = append(errors, valueErrors...)
		return optional.Map(value, func(value ast.Expr) ast.ObjExprElem {
			property := ast.NewProperty(
				objKey,
				false,
				false, // TODO: handle readonly
				optional.Some(value),
				ast.MergeSpans(objKey.Span(), value.Span()),
			)
			return property
		}), errors
	case Question:
		p.lexer.consume() // consume '?'
		_, expectErrors := p.expect(Colon, ConsumeOnMatch)
		errors = append(errors, expectErrors...)
		value, valueErrors := p.expr()
		errors = append(errors, valueErrors...)
		return optional.Map(value, func(value ast.Expr) ast.ObjExprElem {
			property := ast.NewProperty(
				objKey,
				true,
				false, // TODO: handle readonly
				optional.Some(value),
				ast.MergeSpans(objKey.Span(), value.Span()),
			)
			return property
		}), errors
	case OpenParen:
		p.lexer.consume() // consume '('
		params, seqErrors := parseDelimSeq(p, CloseParen, Comma, p.param)
		errors = append(errors, seqErrors...)
		_, expectErrors := p.expect(CloseParen, ConsumeOnMatch)
		errors = append(errors, expectErrors...)

		body, bodyErrors := p.block()
		errors = append(errors, bodyErrors...)
		end := body.Span.End

		span := ast.Span{Start: objKey.Span().Start, End: end}

		// TODO: parse return and throws types
		fn := ast.NewFuncExpr(
			[]*ast.TypeParam{}, // TODO
			params,
			optional.None[ast.TypeAnn](),
			optional.None[ast.TypeAnn](),
			body,
			span,
		)

		if mod == "get" {
			return optional.Some[ast.ObjExprElem](ast.NewGetter(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)), errors
		} else if mod == "set" {
			return optional.Some[ast.ObjExprElem](ast.NewSetter(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)), errors
		} else {
			return optional.Some[ast.ObjExprElem](ast.NewMethod(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)), errors
		}
	default:
		switch objKey.(type) {
		case *ast.IdentExpr:
			switch token.Type {
			case Comma, CloseBrace:
				property := ast.NewProperty(
					objKey,
					false,
					false,
					optional.None[ast.Expr](), // shorthand property
					objKey.Span(),
				)
				return optional.Some[ast.ObjExprElem](property), errors
			default:
				value, valueErrors := p.expr()
				errors = append(errors, valueErrors...)
				if value.IsNone() {
					errors = append(errors, NewError(token.Span, "Expected a comma, closing brace, or expression"))
					return optional.None[ast.ObjExprElem](), errors
				} else {
					errors = append(errors, NewError(token.Span, "Expected a comma or closing brace"))
				}
				return optional.Map(value, func(value ast.Expr) ast.ObjExprElem {
					property := ast.NewProperty(
						objKey,
						false,
						false,
						optional.Some(value),
						objKey.Span(),
					)
					return property
				}), errors
			}
		default:
			errors = append(errors, NewError(token.Span, "Expected a comma or closing brace"))
		}
	}
	return optional.None[ast.ObjExprElem](), errors
}

// <pattern>: <type annotation>
// <pattern>?: <type annotation>
// <pattern>?
// <pattern>
func (p *Parser) param() (optional.Option[*ast.Param], []*Error) {
	pat, errors := p.pattern(true)
	return optional.Map(pat, func(pat ast.Pat) *ast.Param {
		token := p.lexer.peek()

		opt := false
		if token.Type == Question {
			p.lexer.consume() // consume '?'
			opt = true
		}

		if token.Type == Colon {
			p.lexer.consume() // consume ':'
			typeAnn, typeAnnErrors := p.typeAnn()
			errors = append(errors, typeAnnErrors...)
			return &ast.Param{
				Pattern:  pat,
				TypeAnn:  optional.Some(typeAnn),
				Optional: opt,
			}
		}

		return &ast.Param{
			Pattern:  pat,
			TypeAnn:  optional.None[ast.TypeAnn](),
			Optional: opt,
		}
	}), errors
}

func (p *Parser) templateLitExpr(token *Token, tag optional.Option[ast.Expr]) (ast.Expr, []*Error) {
	p.lexer.consume()
	quasis := []*ast.Quasi{}
	exprs := []ast.Expr{}
	errors := []*Error{}
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
			expr, exprErrors := p.expr()
			errors = append(errors, exprErrors...)
			expr.IfSome(func(expr ast.Expr) {
				exprs = append(exprs, expr)
			})
			p.lexer.consume() // consumes the closing brace
		} else {
			// This case happens when the template literal is not closed which
			// means we've reached the end of the file.
			raw = quasi.Value
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			span := ast.Span{Start: token.Span.Start, End: quasi.Span.End}
			errors = append(errors, NewError(span, "Expected a closing backtick"))
			break
		}
	}
	return optional.Map(tag, func(tag ast.Expr) ast.Expr {
		span := ast.Span{Start: tag.Span().Start, End: p.lexer.currentLocation}
		return ast.NewTaggedTemplateLit(tag, quasis, exprs, span)
	}).OrElse(func() optional.Option[ast.Expr] {
		span := ast.NewSpan(token.Span.Start, p.lexer.currentLocation)
		return optional.Some[ast.Expr](ast.NewTemplateLit(quasis, exprs, span))
	}).Unwrap(), errors
}

func (p *Parser) ifElse() (ast.Expr, []*Error) {
	start := p.lexer.currentLocation

	p.lexer.consume() // consume 'if'

	token := p.lexer.peek()
	var cond ast.Expr
	errors := []*Error{}
	if token.Type == OpenBrace {
		errors = append(errors, NewError(token.Span, "Expected a condition"))
	} else {
		condOption, condErrors := p.expr()
		errors = append(errors, condErrors...)
		if condOption.IsNone() {
			errors = append(errors, NewError(token.Span, "Expected a valid condition expression"))
			return nil, errors
		}
		cond = condOption.Unwrap() // safe because we checked for None
	}

	body, bodyErrors := p.block()
	errors = append(errors, bodyErrors...)
	token = p.lexer.peek()
	if token.Type == Else {
		p.lexer.consume()
		token = p.lexer.peek()
		// nolint: exhaustive
		switch token.Type {
		case If:
			ifElseResult, ifElseErrors := p.ifElse()
			errors = append(errors, ifElseErrors...)
			if ifElseResult == nil {
				errors = append(errors, NewError(token.Span, "Expected a valid expression after 'if'"))
				return ast.NewIfElse(
					cond, body, optional.None[ast.BlockOrExpr](),
					ast.Span{Start: start, End: token.Span.Start},
				), errors
			}
			expr := ifElseResult
			alt := ast.BlockOrExpr{
				Expr:  expr,
				Block: nil,
			}
			return ast.NewIfElse(
				cond, body, optional.Some(alt), ast.Span{Start: start, End: expr.Span().End},
			), errors
		case OpenBrace:
			block, blockErrors := p.block()
			errors = append(errors, blockErrors...)
			alt := ast.BlockOrExpr{
				Expr:  nil,
				Block: &block,
			}
			return ast.NewIfElse(
				cond, body, optional.Some(alt), ast.Span{Start: start, End: block.Span.End},
			), errors
		default:
			errors = append(errors, NewError(token.Span, "Expected an if or an opening brace"))
			return ast.NewIfElse(
				cond, body, optional.None[ast.BlockOrExpr](),
				ast.Span{Start: start, End: token.Span.Start},
			), errors
		}
	}
	return ast.NewIfElse(
		cond, body, optional.None[ast.BlockOrExpr](),
		ast.Span{Start: start, End: token.Span.Start},
	), errors
}

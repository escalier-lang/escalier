package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
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

func (p *Parser) expr() ast.Expr {
	select {
	case <-p.ctx.Done():
		fmt.Println("Taking too long to parse")
	default:
		// continue
	}

	expr := p.exprWithoutErrorCheck()
	if expr == nil {
		token := p.lexer.peek()
		p.reportError(token.Span, "Expected an expression")
		return ast.NewEmpty(token.Span)
	}

	return expr
}

func (p *Parser) exprWithoutErrorCheck() ast.Expr {
	values := NewStack[ast.Expr]()
	ops := NewStack[ast.BinaryOp]()

	primary := p.primaryExpr()

	// In some situations, expressions are optional which means that we don't
	// want to raise an error if the primary expression is nil.
	if primary == nil {
		return nil
	}

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
			if len(p.exprMode) == 0 || p.exprMode.Peek() != MultiLineExpr {
				return values.Pop()
			}
		}

		p.lexer.consume()

		if !ops.IsEmpty() {
			if precedence[ops.Peek()] >= precedence[nextOp] {
				// get the last operator and remove it from the list
				op := ops.Pop()
				right := values.Pop()
				left := values.Pop()
				span := ast.Span{Start: left.Span().Start, End: right.Span().End, SourceID: p.lexer.source.ID}

				values.Push(ast.NewBinary(left, right, op, span))
			}
		}

		ops.Push(nextOp)
		expr := p.primaryExpr()
		if expr == nil {
			token := p.lexer.peek()
			p.reportError(token.Span, "Expected an expression")
			expr = ast.NewEmpty(token.Span)
		}
		values.Push(expr)
	}

	for !ops.IsEmpty() {
		op := ops.Pop()
		right := values.Pop()
		left := values.Pop()
		span := ast.Span{Start: left.Span().Start, End: right.Span().End, SourceID: p.lexer.source.ID}

		values.Push(ast.NewBinary(left, right, op, span))
	}

	if len(values) != 1 {
		panic("parseExpr - expected one value on the stack")
	}
	return values.Pop()
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

func (p *Parser) exprSuffix(expr ast.Expr) ast.Expr {
	token := p.lexer.peek()

loop:
	for {
		// nolint: exhaustive
		switch token.Type {
		case OpenParen, QuestionOpenParen:
			p.lexer.consume()
			p.exprMode.Push(MultiLineExpr)
			args := parseDelimSeq(p, CloseParen, Comma, p.expr)
			p.exprMode.Pop()
			terminator := p.lexer.next()
			if terminator.Type != CloseParen {
				p.reportError(token.Span, "Expected a closing paren")
			}
			callee := expr
			optChain := false
			if token.Type == QuestionOpenParen {
				optChain = true
			}
			span := ast.Span{Start: callee.Span().Start, End: terminator.Span.End, SourceID: p.lexer.source.ID}
			expr = ast.NewCall(callee, args, optChain, span)
		case OpenBracket, QuestionOpenBracket:
			p.lexer.consume()
			p.exprMode.Push(MultiLineExpr)
			// TODO: handle the case when parseExpr() return None correctly
			index := p.expr()
			p.exprMode.Pop()
			if index == nil {
				p.reportError(token.Span, "Expected an expression after '['")
				break loop
			}
			terminator := p.lexer.next()
			if terminator.Type != CloseBracket {
				p.reportError(token.Span, "Expected a closing bracket")
			}
			obj := expr
			optChain := false
			if token.Type == QuestionOpenBracket {
				optChain = true
			}
			span := ast.Span{Start: obj.Span().Start, End: terminator.Span.End, SourceID: p.lexer.source.ID}
			expr = ast.NewIndex(obj, index, optChain, span)
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
					ast.Span{Start: obj.Span().Start, End: prop.Span().End, SourceID: p.lexer.source.ID},
				)
			default:
				obj := expr
				prop := ast.NewIdentifier(
					"",
					ast.Span{Start: token.Span.End, End: token.Span.End, SourceID: p.lexer.source.ID},
				)
				expr = ast.NewMember(
					obj, prop, optChain,
					ast.Span{Start: obj.Span().Start, End: prop.Span().End, SourceID: p.lexer.source.ID},
				)
				if token.Type == Dot {
					p.reportError(token.Span, "expected an identifier after .")
				} else {
					p.reportError(token.Span, "expected an identifier after ?.")
				}
			}
		case BackTick:
			temp := p.templateLitExpr(token, expr)
			expr = temp
		default:
			break loop
		}
		token = p.lexer.peek()
	}

	return expr
}

func (p *Parser) objExprKey() ast.ObjKey {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Identifier, Underscore:
		p.lexer.consume()
		return ast.NewIdent(token.Value, token.Span)
	case StrLit:
		p.lexer.consume()
		return ast.NewString(token.Value, token.Span)
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			p.reportError(token.Span, "Expected a number")
		}
		return ast.NewNumber(value, token.Span)
	case OpenBracket:
		p.lexer.consume()
		expr := p.expr()
		p.expect(CloseBracket, AlwaysConsume)
		if expr != nil {
			return &ast.ComputedKey{Expr: expr}
		}
		return nil
	default:
		p.reportError(token.Span, "Expected a property name")
		return nil
	}
}

func (p *Parser) primaryExpr() ast.Expr {
	ops := p.parsePrefix()
	token := p.lexer.peek()

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
				p.reportError(token.Span, "Expected a number")
				// TODO: return an EmptyExpr instead of nil
				return nil
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
			p.exprMode.Push(MultiLineExpr)
			expr = p.expr()
			p.exprMode.Pop()
			if expr == nil {
				return ast.NewEmpty(token.Span)
			}
			p.expect(CloseParen, AlwaysConsume)
		case OpenBracket:
			p.lexer.consume()
			elems := parseDelimSeq(p, CloseBracket, Comma, p.expr)
			end := p.expect(CloseBracket, AlwaysConsume)
			expr = ast.NewArray(elems, ast.Span{Start: token.Span.Start, End: end, SourceID: p.lexer.source.ID})
		case OpenBrace:
			p.lexer.consume()
			elems := parseDelimSeq(p, CloseBrace, Comma, p.objExprElem)
			end := p.expect(CloseBrace, AlwaysConsume)
			expr = ast.NewObject(elems, ast.Span{Start: token.Span.Start, End: end, SourceID: p.lexer.source.ID})
		case BackTick:
			temp := p.templateLitExpr(token, nil)
			expr = temp
		case Fn:
			fnExpr := p.fnExpr(token.Span.Start)
			return fnExpr
		case If:
			return p.ifElse()
		case LessThan:
			// TODO: figure out how to cast this more directly.
			jsx := p.jsxElement()
			return jsx
		case
			Val, Var, Return,
			CloseBrace, CloseParen, CloseBracket,
			EndOfFile:
			// Every call to `parseExpr()` should check if `nil` is returned so
			// that we can raise an error if we were expecting an expression.
			// We could also have a function like `maybeParseExpr()` that is okay
			// with return `nil` whereas `parseExpr()` would return an error if
			// `nil` is returned.
			return nil
		default:
			p.lexer.consume()
			p.errors = append(
				p.errors,
				NewError(token.Span, fmt.Sprintf("Unexpected token, '%s'", token.Value)),
			)
			token = p.lexer.peek()
		}
	}

	expr = p.exprSuffix(expr)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = ast.NewUnary(tokenAndOp.Op, expr, ast.Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span().End, SourceID: p.lexer.source.ID})
	}

	return expr
}

// fnExpr = 'fn' '(' param (',' param)* ')' block
// TODO: dedupe with `fnDecl`
func (p *Parser) fnExpr(start ast.Location) ast.Expr {
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

	p.expect(OpenParen, ConsumeOnMatch)
	params := parseDelimSeq(p, CloseParen, Comma, p.param)
	p.expect(CloseParen, ConsumeOnMatch)

	var returnType ast.TypeAnn
	token := p.lexer.peek()
	if token.Type == Arrow {
		p.lexer.consume()
		typeAnn := p.typeAnn()
		if typeAnn == nil {
			p.reportError(token.Span, "Expected type annotation after arrow")
			return nil
		}
		returnType = typeAnn
	}

	body := p.block()
	end := body.Span.End

	return ast.NewFuncExpr(
		[]*ast.TypeParam{}, // TODO: parse type params
		params,
		returnType,
		nil, // TODO: parse throws type
		body,
		ast.NewSpan(start, end),
	)
}

func (p *Parser) objExprElem() ast.ObjExprElem {
	token := p.lexer.peek()

	if token.Type == DotDotDot {
		p.lexer.consume() // consume '...'
		arg := p.expr()
		if arg == nil {
			p.reportError(token.Span, "Expected an expression after '...'")
		}
		if arg != nil {
			return ast.NewRestSpread(arg, ast.MergeSpans(token.Span, arg.Span()))
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

	objKey := p.objExprKey()
	if objKey == nil {
		return nil
	}
	token = p.lexer.peek()

	// TODO: loop until we find a ':', '?', '(', ',' or '}' so
	// that we can skip over unexpected tokens

	// nolint: exhaustive
	switch token.Type {
	case Colon:
		p.lexer.consume() // consume ':'
		value := p.expr()
		if value != nil {
			property := ast.NewProperty(
				objKey,
				false,
				false, // TODO: handle readonly
				value,
				ast.MergeSpans(objKey.Span(), value.Span()),
			)
			return property
		}
		return nil
	case Question:
		p.lexer.consume() // consume '?'
		p.expect(Colon, ConsumeOnMatch)
		value := p.expr()
		if value != nil {
			property := ast.NewProperty(
				objKey,
				true,
				false, // TODO: handle readonly
				value,
				ast.MergeSpans(objKey.Span(), value.Span()),
			)
			return property
		}
		return nil
	case OpenParen:
		p.lexer.consume() // consume '('
		params := parseDelimSeq(p, CloseParen, Comma, p.param)
		p.expect(CloseParen, ConsumeOnMatch)

		body := p.block()
		end := body.Span.End

		span := ast.Span{Start: objKey.Span().Start, End: end, SourceID: p.lexer.source.ID}

		fn := ast.NewFuncExpr(
			[]*ast.TypeParam{}, // TODO: parse type params
			params,
			nil, // TODO: parse return type
			nil, // TODO: parse throws type
			body,
			span,
		)

		if mod == "get" {
			return ast.NewGetter(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)
		} else if mod == "set" {
			return ast.NewSetter(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)
		} else {
			return ast.NewMethod(
				objKey,
				fn,
				ast.MergeSpans(token.Span, span),
			)
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
					nil, // shorthand property
					objKey.Span(),
				)
				return property
			default:
				value := p.expr()
				if value == nil {
					p.reportError(token.Span, "Expected a comma, closing brace, or expression")
					return nil
				} else {
					p.reportError(token.Span, "Expected a comma or closing brace")
				}

				property := ast.NewProperty(
					objKey,
					false,
					false,
					value,
					objKey.Span(),
				)
				return property
			}
		default:
			p.reportError(token.Span, "Expected a comma or closing brace")
		}
	}
	return nil
}

// <pattern>: <type annotation>
// <pattern>?: <type annotation>
// <pattern>?
// <pattern>
func (p *Parser) param() *ast.Param {
	pat := p.pattern(true)
	if pat == nil {
		return nil
	}
	token := p.lexer.peek()

	opt := false
	if token.Type == Question {
		p.lexer.consume() // consume '?'
		opt = true
	}

	var param ast.Param

	if token.Type == Colon {
		p.lexer.consume() // consume ':'
		typeAnn := p.typeAnn()
		param = ast.Param{
			Pattern:  pat,
			TypeAnn:  typeAnn,
			Optional: opt,
		}
	} else {
		param = ast.Param{
			Pattern:  pat,
			TypeAnn:  nil,
			Optional: opt,
		}
	}

	return &param
}

func (p *Parser) templateLitExpr(token *Token, tag ast.Expr) ast.Expr {
	p.lexer.consume()
	quasis := []*ast.Quasi{}
	exprs := []ast.Expr{}
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
			expr := p.expr()
			if expr != nil {
				exprs = append(exprs, expr)
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
	if tag != nil {
		span := ast.Span{Start: tag.Span().Start, End: p.lexer.currentLocation, SourceID: p.lexer.source.ID}
		return ast.NewTaggedTemplateLit(tag, quasis, exprs, span)
	} else {
		span := ast.NewSpan(token.Span.Start, p.lexer.currentLocation)
		return ast.NewTemplateLit(quasis, exprs, span)
	}
}

func (p *Parser) ifElse() ast.Expr {
	start := p.lexer.currentLocation

	p.lexer.consume() // consume 'if'

	token := p.lexer.peek()
	var cond ast.Expr
	if token.Type == OpenBrace {
		p.reportError(token.Span, "Expected a condition")
	} else {
		cond = p.expr()
		if cond == nil {
			p.reportError(token.Span, "Expected a valid condition expression")
			return nil
		}
	}

	body := p.block()
	token = p.lexer.peek()
	if token.Type == Else {
		p.lexer.consume()
		token = p.lexer.peek()
		// nolint: exhaustive
		switch token.Type {
		case If:
			ifElseResult := p.ifElse()
			if ifElseResult == nil {
				p.reportError(token.Span, "Expected a valid expression after 'if'")
				return ast.NewIfElse(
					cond, body, nil,
					ast.Span{Start: start, End: token.Span.Start, SourceID: p.lexer.source.ID},
				)
			}
			expr := ifElseResult
			alt := &ast.BlockOrExpr{
				Expr:  expr,
				Block: nil,
			}
			return ast.NewIfElse(
				cond, body, alt, ast.Span{Start: start, End: expr.Span().End, SourceID: p.lexer.source.ID},
			)
		case OpenBrace:
			block := p.block()
			alt := &ast.BlockOrExpr{
				Expr:  nil,
				Block: &block,
			}
			return ast.NewIfElse(
				cond, body, alt, ast.Span{Start: start, End: block.Span.End, SourceID: p.lexer.source.ID},
			)
		default:
			p.reportError(token.Span, "Expected an if or an opening brace")
			return ast.NewIfElse(
				cond, body, nil,
				ast.Span{Start: start, End: token.Span.Start, SourceID: p.lexer.source.ID},
			)
		}
	}
	return ast.NewIfElse(
		cond, body, nil,
		ast.Span{Start: start, End: token.Span.Start, SourceID: p.lexer.source.ID},
	)
}

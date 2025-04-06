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

func (p *Parser) ParseExprWithMarker(marker Marker) ast.Expr {
	p.markers.Push(marker)
	defer p.markers.Pop()
	return p.ParseExpr()
}

func (p *Parser) ParseExpr() ast.Expr {
	select {
	case <-p.ctx.Done():
		fmt.Println("Taking too long to parse")
	default:
		// continue
	}

	values := NewStack[ast.Expr]()
	ops := NewStack[ast.BinaryOp]()

	values = append(values, p.parsePrimary())

loop:
	for {
		token := p.lexer.peek()
		var nextOp ast.BinaryOp

		//nolint: exhaustive
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
		case BangEqual:
			nextOp = ast.NotEqual
		case LessThan:
			nextOp = ast.LessThan
		case LessThanEqual:
			nextOp = ast.LessThanEqual
		case GreaterThan:
			nextOp = ast.GreaterThan
		case GreaterThanEqual:
			nextOp = ast.GreaterThanEqual
		case CloseParen, CloseBracket, CloseBrace, Comma, EndOfFile, Var, Val, Fn, Return:
			break loop
		default:
			return values.Pop()
			// parser.reportError(token.Span, "Unexpected token")
			// continue
		}

		if token.Span.Start.Line != p.lexer.currentLocation.Line {
			if len(p.markers) == 0 || p.markers.Peek() != MarkerDelim {
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

				values.Push(ast.NewBinary(left, right, op, ast.Span{Start: left.Span().Start, End: right.Span().End}))
			}
		}

		ops.Push(nextOp)
		expr := p.parsePrimary()
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
		//nolint: exhaustive
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

func (p *Parser) parseSuffix(expr ast.Expr) ast.Expr {
	token := p.lexer.peek()

loop:
	for {
		//nolint: exhaustive
		switch token.Type {
		case OpenParen, QuestionOpenParen:
			p.lexer.consume()
			args := p.parseExprSeq()
			terminator := p.lexer.next()
			if terminator.Type != CloseParen {
				p.reportError(token.Span, "Expected a closing paren")
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
			index := p.ParseExprWithMarker(MarkerDelim)
			terminator := p.lexer.next()
			if terminator.Type != CloseBracket {
				p.reportError(token.Span, "Expected a closing bracket")
			}
			obj := expr
			optChain := false
			if token.Type == QuestionOpenBracket {
				optChain = true
			}
			expr = ast.NewIndex(
				obj, index, optChain,
				ast.Span{Start: obj.Span().Start, End: terminator.Span.End},
			)
		case Dot, QuestionDot:
			p.lexer.consume()
			prop := p.lexer.next()
			optChain := false
			if token.Type == QuestionDot {
				optChain = true
			}
			//nolint: exhaustive
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
					p.reportError(token.Span, "expected an identifier after .")
				} else {
					p.reportError(token.Span, "expected an identifier after ?.")
				}
			}
		case BackTick:
			expr = p.parseTemplateLitExpr(token, expr)
		default:
			break loop
		}
		token = p.lexer.peek()
	}

	return expr
}

func (p *Parser) parseObjKey() ast.ObjExprKey {
	token := p.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case Identifier, Underscore:
		p.lexer.consume()
		return ast.NewIdent(token.Value, token.Span)
	case String:
		p.lexer.consume()
		return ast.NewString(token.Value, token.Span)
	case Number:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			p.reportError(token.Span, "Expected a number")
		}
		return ast.NewNumber(value, token.Span)
	case OpenBracket:
		p.lexer.consume()
		expr := p.ParseExprWithMarker(MarkerDelim)
		p.expect(CloseBracket, AlwaysConsume)
		return &ast.ComputedKey{Expr: expr}
	default:
		p.reportError(token.Span, "Expected a property name")
		return nil
	}
}

func (p *Parser) parsePrimary() ast.Expr {
	ops := p.parsePrefix()
	token := p.lexer.peek()

	var expr ast.Expr

	// Loop so that we can skip over unexpected tokens
	for expr == nil {
		start := token.Span.Start

		//nolint: exhaustive
		switch token.Type {
		case Number:
			p.lexer.consume()
			value, err := strconv.ParseFloat(token.Value, 64)
			if err != nil {
				// TODO: handle parsing errors
			}
			expr = ast.NewLitExpr(ast.NewNumber(value, token.Span))
		case String:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewString(token.Value, token.Span))
		case True:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewBoolean(true, token.Span))
		case False:
			p.lexer.consume()
			expr = ast.NewLitExpr(ast.NewBoolean(false, token.Span))
		case Identifier, Underscore:
			p.lexer.consume()
			expr = ast.NewIdent(token.Value, token.Span)
		case OpenParen:
			p.lexer.consume()
			expr = p.ParseExprWithMarker(MarkerDelim)
			p.expect(CloseParen, AlwaysConsume)
		case OpenBracket:
			p.lexer.consume()
			elems := p.parseExprSeq()
			end := p.expect(CloseBracket, AlwaysConsume)
			expr = ast.NewArray(elems, ast.Span{Start: token.Span.Start, End: end})
		case OpenBrace:
			var elems []ast.ObjExprElem
			var first bool = true
			p.lexer.consume()
		loop:
			for {
				token = p.lexer.peek()
				if token.Type == CloseBrace {
					break
				}

				if !first {
					p.expect(Comma, ConsumeOnMatch)
				}

				token = p.lexer.peek()

				mod := ""
				if token.Type == Get {
					p.lexer.consume() // consume 'get'
					mod = "get"
				} else if token.Type == Set {
					p.lexer.consume() // consume 'set'
					mod = "set"
				}

				objKey := p.parseObjKey()
				if objKey == nil {
					break
				}
				token = p.lexer.peek()

				// TODO: loop until we find a ':', '?', '(', ',' or '}' so
				// that we can skip over unexpected tokens

				switch token.Type {
				case Colon:
					p.lexer.consume() // consume ':'
					value := p.ParseExprWithMarker(MarkerDelim)
					if value == nil {
						break
					}
					property := &ast.Property[ast.Expr, ast.ObjExprKey]{
						Name:     objKey,
						Value:    value,
						Readonly: false, // TODO
						Optional: false,
					}
					elems = append(elems, property)
				case Question:
					p.lexer.consume() // consume '?'
					p.expect(Colon, ConsumeOnMatch)
					value := p.ParseExprWithMarker(MarkerDelim)
					if value == nil {
						break
					}
					property := &ast.Property[ast.Expr, ast.ObjExprKey]{
						Name:     objKey,
						Value:    value,
						Readonly: true,
						Optional: false,
					}
					elems = append(elems, property)
				case OpenParen:
					p.lexer.consume() // consume '('
					params := p.parseParamSeq()
					p.expect(CloseParen, ConsumeOnMatch)

					body := p.parseBlock()
					end := body.Span.End

					// TODO: parse return and throws types
					fn := ast.NewFuncExpr(
						params,
						nil,
						nil,
						body,
						ast.Span{Start: start, End: end},
					)

					if mod == "get" {
						method := &ast.Getter[ast.Expr, ast.ObjExprKey]{
							Name: objKey,
							Fn:   fn,
						}
						elems = append(elems, method)
					} else if mod == "set" {
						method := &ast.Setter[ast.Expr, ast.ObjExprKey]{
							Name: objKey,
							Fn:   fn,
						}
						elems = append(elems, method)
					} else {
						method := &ast.Method[ast.Expr, ast.ObjExprKey]{
							Name: objKey,
							Fn:   fn,
						}
						elems = append(elems, method)
					}
				default:
					switch objKey.(type) {
					case *ast.IdentExpr:
						switch token.Type {
						case Comma, CloseBrace:
							property := &ast.Property[ast.Expr, ast.ObjExprKey]{
								Name:     objKey,
								Value:    nil, // shorthand property
								Readonly: false,
								Optional: false,
							}
							elems = append(elems, property)
							if token.Type == CloseBrace {
								break loop
							}
						default:
							value := p.ParseExprWithMarker(MarkerDelim)
							if value == nil {
								p.reportError(token.Span, "Expected a comma, closing brace, or expression")
							} else {
								p.reportError(token.Span, "Expected a comma or closing brace")
							}
							property := &ast.Property[ast.Expr, ast.ObjExprKey]{
								Name:     objKey,
								Value:    value,
								Readonly: false,
								Optional: false,
							}
							elems = append(elems, property)
						}
					default:
						p.reportError(token.Span, "Expected a comma or closing brace")
					}
				}

				first = false
			}
			end := p.expect(CloseBrace, AlwaysConsume)
			expr = ast.NewObject(elems, ast.Span{Start: token.Span.Start, End: end})
		case BackTick:
			expr = p.parseTemplateLitExpr(token, nil)
		case Fn:
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

			start := token.Span.Start

			p.expect(OpenParen, ConsumeOnMatch)
			params := p.parseParamSeq()
			p.expect(CloseParen, ConsumeOnMatch)

			body := p.parseBlock()
			end := body.Span.End

			// TODO: parse return and throws types
			return ast.NewFuncExpr(params, nil, nil, body, ast.Span{Start: start, End: end})
		case If:
			return p.parseIfElse()
		case LessThan:
			return p.parseJSXElement()
		case
			Val, Var, Return,
			CloseBrace, CloseParen, CloseBracket,
			EndOfFile:
			expr = ast.NewEmpty(token.Span)
			p.reportError(token.Span, "Expected an expression")
			return expr
		default:
			p.lexer.consume()
			p.reportError(token.Span, fmt.Sprintf("Unexpected token, '%s'", token.Value))
			token = p.lexer.peek()
		}
	}

	expr = p.parseSuffix(expr)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = ast.NewUnary(tokenAndOp.Op, expr, ast.Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span().End})
	}

	return expr
}

func (p *Parser) parseExprSeq() []ast.Expr {
	exprs := []ast.Expr{}

	// handles empty sequences
	token := p.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case CloseBracket, CloseParen, CloseBrace:
		return exprs
	default:
	}

	expr := p.ParseExprWithMarker(MarkerDelim)
	exprs = append(exprs, expr)

	token = p.lexer.peek()

	for {
		//nolint: exhaustive
		switch token.Type {
		case Comma:
			// TODO: handle trailing comma
			p.lexer.consume()
			expr = p.ParseExprWithMarker(MarkerDelim)
			exprs = append(exprs, expr)
			token = p.lexer.peek()
		default:
			return exprs
		}
	}
}

// TODO: parse type annotations
func (p *Parser) parseParamSeq() []*ast.Param {
	params := []*ast.Param{}

	token := p.lexer.peek()
	if token.Type == CloseParen {
		return params
	}

	pat := p.parsePattern()
	if pat == nil {
		return params
	}
	params = append(params, &ast.Param{Pattern: pat})

	for {
		token := p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case Comma:
			p.lexer.consume() // consume ','
			pat := p.parsePattern()
			if pat == nil {
				return params
			}
			params = append(params, &ast.Param{Pattern: pat})
		default:
			return params
		}
	}
}

func (p *Parser) parseTemplateLitExpr(token *Token, tag ast.Expr) ast.Expr {
	p.lexer.consume()
	var quasis []*ast.Quasi
	var exprs []ast.Expr
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
			expr := p.ParseExprWithMarker(MarkerDelim)
			exprs = append(exprs, expr)
			p.lexer.consume() // consumes the closing brace
		} else {
			// This case happens when the template literal is not closed which
			// means we've reached the end of the file.
			raw = quasi.Value
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			span := ast.Span{Start: token.Span.Start, End: quasi.Span.End}
			p.reportError(span, "Expected a closing backtick")
			break
		}
	}
	if tag != nil {
		span := ast.Span{Start: tag.Span().Start, End: p.lexer.currentLocation}
		return ast.NewTaggedTemplateLit(tag, quasis, exprs, span)
	}
	span := ast.Span{Start: token.Span.Start, End: p.lexer.currentLocation}
	return ast.NewTemplateLit(quasis, exprs, span)
}

func (p *Parser) parseIfElse() ast.Expr {
	start := p.lexer.currentLocation

	p.lexer.consume() // consume 'if'

	token := p.lexer.peek()
	var cond ast.Expr
	if token.Type == OpenBrace {
		p.reportError(token.Span, "Expected a condition")
	} else {
		cond = p.ParseExprWithMarker(MarkerDelim)
	}

	body := p.parseBlock()
	token = p.lexer.peek()
	if token.Type == Else {
		p.lexer.consume()
		token = p.lexer.peek()
		//nolint: exhaustive
		switch token.Type {
		case If:
			expr := p.parseIfElse()
			alt := ast.BlockOrExpr{
				Expr:  expr,
				Block: nil,
			}
			return ast.NewIfElse(cond, body, alt, ast.Span{Start: start, End: expr.Span().End})
		case OpenBrace:
			block := p.parseBlock()
			alt := ast.BlockOrExpr{
				Expr:  nil,
				Block: &block,
			}
			return ast.NewIfElse(cond, body, alt, ast.Span{Start: start, End: block.Span.End})
		default:
			p.reportError(token.Span, "Expected an if or an opening brace")
			alt := ast.BlockOrExpr{
				Expr:  nil,
				Block: nil,
			}
			return ast.NewIfElse(cond, body, alt, ast.Span{Start: start, End: token.Span.Start})
		}
	}
	alt := ast.BlockOrExpr{
		Expr:  nil,
		Block: nil,
	}
	return ast.NewIfElse(cond, body, alt, ast.Span{Start: start, End: token.Span.Start})
}

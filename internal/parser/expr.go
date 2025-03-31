package parser

import (
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

func (parser *Parser) ParseExprWithMarker(marker Marker) ast.Expr {
	parser.markers.Push(marker)
	defer parser.markers.Pop()
	return parser.ParseExpr()
}

func (parser *Parser) ParseExpr() ast.Expr {
	values := NewStack[ast.Expr]()
	ops := NewStack[ast.BinaryOp]()

	values = append(values, parser.parsePrimary())

loop:
	for {
		token := parser.lexer.peek()
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
		case CloseParen, CloseBracket, CloseBrace, Comma, EndOfFile, Var, Val, Fn, Return:
			break loop
		default:
			return values.Pop()
			// parser.reportError(token.Span, "Unexpected token")
			// continue
		}

		if token.Span.Start.Line != parser.lexer.currentLocation.Line {
			if len(parser.markers) == 0 || parser.markers.Peek() != MarkerDelim {
				return values.Pop()
			}
		}

		parser.lexer.consume()

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
		expr := parser.parsePrimary()
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

func (parser *Parser) parsePrefix() Stack[TokenAndOp] {
	token := parser.lexer.peek()
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
		parser.lexer.consume()
		token = parser.lexer.peek()
	}

	return result
}

func (parser *Parser) parseSuffix(expr ast.Expr) ast.Expr {
	token := parser.lexer.peek()

loop:
	for {
		//nolint: exhaustive
		switch token.Type {
		case OpenParen, QuestionOpenParen:
			parser.lexer.consume()
			args := parser.parseExprSeq()
			terminator := parser.lexer.next()
			if terminator.Type != CloseParen {
				parser.reportError(token.Span, "Expected a closing paren")
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
			parser.lexer.consume()
			index := parser.ParseExprWithMarker(MarkerDelim)
			terminator := parser.lexer.next()
			if terminator.Type != CloseBracket {
				parser.reportError(token.Span, "Expected a closing bracket")
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
			parser.lexer.consume()
			prop := parser.lexer.next()
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
					parser.reportError(token.Span, "expected an identifier after .")
				} else {
					parser.reportError(token.Span, "expected an identifier after ?.")
				}
			}
		case BackTick:
			expr = parser.parseTemplateLitExpr(token, expr)
		default:
			break loop
		}
		token = parser.lexer.peek()
	}

	return expr
}

func (parser *Parser) parsePrimary() ast.Expr {
	ops := parser.parsePrefix()
	token := parser.lexer.peek()

	var expr ast.Expr

	// Loop until we parse a primary expression.
	for expr == nil {
		//nolint: exhaustive
		switch token.Type {
		case Number:
			parser.lexer.consume()
			value, err := strconv.ParseFloat(token.Value, 64)
			if err != nil {
				// TODO: handle parsing errors
			}
			expr = ast.NewNumber(value, token.Span)
		case String:
			parser.lexer.consume()
			expr = ast.NewString(token.Value, token.Span)
		case True:
			parser.lexer.consume()
			expr = ast.NewBoolean(true, token.Span)
		case False:
			parser.lexer.consume()
			expr = ast.NewBoolean(false, token.Span)
		case Identifier, Underscore:
			parser.lexer.consume()
			expr = ast.NewIdent(token.Value, token.Span)
		case OpenParen:
			parser.lexer.consume()
			expr = parser.ParseExprWithMarker(MarkerDelim)
			final := parser.lexer.next() // consume the closing paren
			if final.Type != CloseParen {
				parser.reportError(token.Span, "Expected a closing paren")
			}
		case OpenBracket:
			parser.lexer.consume()
			elems := parser.parseExprSeq()
			final := parser.lexer.next() // consume the closing bracket
			if final.Type != CloseBracket {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
			expr = ast.NewArray(elems, ast.Span{Start: token.Span.Start, End: final.Span.End})
		case BackTick:
			expr = parser.parseTemplateLitExpr(token, nil)
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
			parser.lexer.consume() // consume the fn keyword

			start := token.Span.Start

			token = parser.lexer.peek()
			if token.Type != OpenParen {
				parser.reportError(token.Span, "Expected an opening paren")
			} else {
				parser.lexer.consume()
			}

			params := parser.parseParamSeq()

			token = parser.lexer.peek()
			if token.Type != CloseParen {
				parser.reportError(token.Span, "Expected a closing paren")
			} else {
				parser.lexer.consume()
			}

			body := parser.parseBlock()
			end := body.Span.End

			// TODO: parse return and throws types
			return ast.NewFuncExpr(params, nil, nil, body, ast.Span{Start: start, End: end})
		case If:
			return parser.parseIfElse()
		case LessThan:
			return parser.parseJSXElement()
		case
			Val, Var, Return,
			CloseBrace, CloseParen, CloseBracket,
			EndOfFile:
			expr = ast.NewEmpty(token.Span)
			parser.reportError(token.Span, "Expected an expression")
			return expr
		default:
			parser.lexer.consume()
			parser.reportError(token.Span, "Unexpected token")
			token = parser.lexer.peek()
		}
	}

	expr = parser.parseSuffix(expr)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = ast.NewUnary(tokenAndOp.Op, expr, ast.Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span().End})
	}

	return expr
}

func (parser *Parser) parseExprSeq() []ast.Expr {
	exprs := []ast.Expr{}

	// handles empty sequences
	token := parser.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case CloseBracket, CloseParen, CloseBrace:
		return exprs
	default:
	}

	expr := parser.ParseExprWithMarker(MarkerDelim)
	exprs = append(exprs, expr)

	token = parser.lexer.peek()

	for {
		//nolint: exhaustive
		switch token.Type {
		case Comma:
			// TODO: handle trailing comma
			parser.lexer.consume()
			expr = parser.ParseExprWithMarker(MarkerDelim)
			exprs = append(exprs, expr)
			token = parser.lexer.peek()
		default:
			return exprs
		}
	}
}

// TODO: parse type annotations
func (parser *Parser) parseParamSeq() []*ast.Param {
	params := []*ast.Param{}

	token := parser.lexer.peek()
	if token.Type == CloseParen {
		return params
	}

	pat := parser.parsePattern()
	if pat == nil {
		return params
	}
	params = append(params, &ast.Param{Pattern: pat})

	for {
		token := parser.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case Comma:
			parser.lexer.consume() // consume ','
			pat := parser.parsePattern()
			if pat == nil {
				return params
			}
			params = append(params, &ast.Param{Pattern: pat})
		default:
			return params
		}
	}
}

func (parser *Parser) parseTemplateLitExpr(token *Token, tag ast.Expr) ast.Expr {
	parser.lexer.consume()
	var quasis []*ast.Quasi
	var exprs []ast.Expr
	for {
		quasi := parser.lexer.lexQuasi()

		var raw string
		if strings.HasSuffix(quasi.Value, "`") {
			raw = quasi.Value[:len(quasi.Value)-1]
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			break
		} else if strings.HasSuffix(quasi.Value, "${") {
			raw = quasi.Value[:len(quasi.Value)-2]
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			expr := parser.ParseExprWithMarker(MarkerDelim)
			exprs = append(exprs, expr)
			parser.lexer.consume() // consumes the closing brace
		} else {
			// This case happens when the template literal is not closed which
			// means we've reached the end of the file.
			raw = quasi.Value
			quasis = append(quasis, &ast.Quasi{Value: raw, Span: quasi.Span})
			span := ast.Span{Start: token.Span.Start, End: quasi.Span.End}
			parser.reportError(span, "Expected a closing backtick")
			break
		}
	}
	if tag != nil {
		span := ast.Span{Start: tag.Span().Start, End: parser.lexer.currentLocation}
		return ast.NewTaggedTemplateLit(tag, quasis, exprs, span)
	}
	span := ast.Span{Start: token.Span.Start, End: parser.lexer.currentLocation}
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

	token = p.lexer.peek()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected an opening brace")
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

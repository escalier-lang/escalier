package parser

import (
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

func (parser *Parser) ParseExpr() ast.Expr {
	values := NewStack[ast.Expr]()
	ops := NewStack[ast.BinaryOp]()

	values = append(values, parser.parsePrimary())

loop:
	for {
		token := parser.lexer.peek()
		var nextOp ast.BinaryOp

		switch token.(type) {
		case *TPlus:
			nextOp = ast.Plus
		case *TMinus:
			nextOp = ast.Minus
		case *TAsterisk:
			nextOp = ast.Times
		case *TSlash:
			nextOp = ast.Divide
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TComma, *TEndOfFile, *TVar, *TVal, *TFn, *TReturn:
			break loop
		default:
			return values.Pop()
			// parser.reportError(token.Span(), "Unexpected token")
			// continue
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
	Token Token
	Op    ast.UnaryOp
}

func (parser *Parser) parsePrefix() Stack[TokenAndOp] {
	token := parser.lexer.peek()
	result := NewStack[TokenAndOp]()

loop:
	for {
		switch token.(type) {
		case *TPlus:
			result.Push(TokenAndOp{Token: token, Op: ast.UnaryPlus})
		case *TMinus:
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
		switch token.(type) {
		case *TOpenParen, *TQuestionOpenParen:
			parser.lexer.consume()
			args := parser.parseExprSeq()
			terminator := parser.lexer.next()
			if _, ok := terminator.(*TCloseParen); !ok {
				parser.reportError(token.Span(), "Expected a closing paren")
			}
			callee := expr
			optChain := false
			if _, ok := token.(*TQuestionOpenParen); ok {
				optChain = true
			}
			expr = ast.NewCall(callee, args, optChain, ast.Span{Start: callee.Span().Start, End: terminator.Span().End})
		case *TOpenBracket, *TQuestionOpenBracket:
			parser.lexer.consume()
			index := parser.ParseExpr()
			terminator := parser.lexer.next()
			if _, ok := terminator.(*TCloseBracket); !ok {
				parser.reportError(token.Span(), "Expected a closing bracket")
			}
			obj := expr
			optChain := false
			if _, ok := token.(*TQuestionOpenBracket); ok {
				optChain = true
			}
			expr = ast.NewIndex(obj, index, optChain, ast.Span{Start: obj.Span().Start, End: terminator.Span().End})
		case *TDot, *TQuestionDot:
			parser.lexer.consume()
			prop := parser.lexer.next()
			optChain := false
			if _, ok := token.(*TQuestionDot); ok {
				optChain = true
			}
			switch t := prop.(type) {
			case *TIdentifier:
				obj := expr
				prop := ast.NewIdentifier(t.Value, prop.Span())
				expr = ast.NewMember(obj, prop, optChain, ast.Span{Start: obj.Span().Start, End: prop.Span().End})
			default:
				obj := expr
				prop := ast.NewIdentifier(
					"",
					ast.Span{Start: token.Span().End, End: token.Span().End},
				)
				expr = ast.NewMember(obj, prop, optChain, ast.Span{Start: obj.Span().Start, End: prop.Span().End})
				if _, ok := token.(*TDot); ok {
					parser.reportError(token.Span(), "expected an identifier after .")
				} else {
					parser.reportError(token.Span(), "expected an identifier after ?.")
				}
			}
		case *TBackTick:
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
		switch t := token.(type) {
		case *TNumber:
			parser.lexer.consume()
			expr = ast.NewNumber(t.Value, token.Span())
		case *TString:
			parser.lexer.consume()
			expr = ast.NewString(t.Value, token.Span())
		case *TIdentifier:
			parser.lexer.consume()
			expr = ast.NewIdent(t.Value, token.Span())
		case *TOpenParen:
			parser.lexer.consume()
			expr = parser.ParseExpr()
			final := parser.lexer.next() // consume the closing paren
			if _, ok := final.(*TCloseParen); !ok {
				parser.reportError(token.Span(), "Expected a closing paren")
			}
		case *TOpenBracket:
			parser.lexer.consume()
			elems := parser.parseExprSeq()
			final := parser.lexer.next() // consume the closing bracket
			if _, ok := final.(*TCloseBracket); !ok {
				parser.reportError(token.Span(), "Expected a closing bracket")
			}
			expr = ast.NewArray(elems, ast.Span{Start: token.Span().Start, End: final.Span().End})
		case *TBackTick:
			expr = parser.parseTemplateLitExpr(token, nil)
		case *TFn:
			// TODO: allow an optional identifier
			// token := parser.lexer.peek()
			// _ident, ok := token.(*TIdentifier)
			// var ident *ast.Ident
			// if ok {
			// 	parser.lexer.consume()
			// 	ident = ast.NewIdentifier(_ident.Value, token.Span())
			// } else {
			// 	parser.reportError(token.Span(), "Expected identifier")
			// 	ident = ast.NewIdentifier(
			// 		"",
			// 		ast.Span{Start: token.Span().Start, End: token.Span().Start},
			// 	)
			// }
			parser.lexer.consume() // consume the fn keyword

			start := token.Span().Start

			token = parser.lexer.next()
			if _, ok := token.(*TOpenParen); !ok {
				parser.reportError(token.Span(), "Expected an opening paren")
				return nil
			}
			params := parser.parseParamSeq()
			token = parser.lexer.next()
			if _, ok := token.(*TCloseParen); !ok {
				parser.reportError(token.Span(), "Expected a closing paren")
				return nil
			}

			body := parser.parseBlock()
			end := body.Span.End

			// TODO: parse return and throws types
			return ast.NewFuncExpr(params, nil, nil, body, ast.Span{Start: start, End: end})
		case
			*TVal, *TVar, *TReturn,
			*TCloseBrace, *TCloseParen, *TCloseBracket,
			*TEndOfFile:
			expr = ast.NewEmpty(token.Span())
			parser.reportError(token.Span(), "Expected an expression")
			return expr
		default:
			parser.lexer.consume()
			parser.reportError(token.Span(), "Unexpected token")
			token = parser.lexer.peek()
		}
	}

	expr = parser.parseSuffix(expr)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = ast.NewUnary(tokenAndOp.Op, expr, ast.Span{Start: tokenAndOp.Token.Span().Start, End: expr.Span().End})
	}

	return expr
}

func (parser *Parser) parseExprSeq() []ast.Expr {
	exprs := []ast.Expr{}

	// handles empty sequences
	token := parser.lexer.peek()
	switch token.(type) {
	case *TCloseBracket, *TCloseParen, *TCloseBrace:
		return exprs
	default:
	}

	expr := parser.ParseExpr()
	exprs = append(exprs, expr)

	token = parser.lexer.peek()

	for {
		switch token.(type) {
		case *TComma:
			// TODO: handle trailing comma
			parser.lexer.consume()
			expr = parser.ParseExpr()
			exprs = append(exprs, expr)
			token = parser.lexer.peek()
		default:
			return exprs
		}
	}
}

func (parser *Parser) parseParamSeq() []*ast.Param {
	params := []*ast.Param{}

	token := parser.lexer.peek()
	_ident, ok := token.(*TIdentifier)
	if !ok {
		return params
	}
	param := &ast.Param{Name: ast.NewIdentifier(_ident.Value, token.Span())}
	params = append(params, param)
	parser.lexer.consume()

	token = parser.lexer.peek()

	for {
		switch token.(type) {
		case *TComma:
			parser.lexer.consume()
			token = parser.lexer.peek()
			_ident, ok := token.(*TIdentifier)
			if !ok {
				return params
			}
			param := &ast.Param{Name: ast.NewIdentifier(_ident.Value, token.Span())}
			params = append(params, param)
			parser.lexer.consume()
		default:
			return params
		}
	}
}

func (parser *Parser) parseTemplateLitExpr(token Token, tag ast.Expr) ast.Expr {
	parser.lexer.consume()
	var quasis []*ast.Quasi
	var exprs []ast.Expr
	for {
		quasi := parser.lexer.lexQuasi()
		quasis = append(quasis, &ast.Quasi{Value: quasi.Value, Span: quasi.Span()})

		if quasi.Last {
			if quasi.Incomplete {
				span := ast.Span{Start: token.Span().Start, End: quasi.Span().End}
				parser.reportError(span, "Expected a closing backtick")
			}
			break
		} else {
			expr := parser.ParseExpr()
			exprs = append(exprs, expr)
			parser.lexer.consume() // consumes the closing brace
		}
	}
	if tag != nil {
		span := ast.Span{Start: tag.Span().Start, End: parser.lexer.currentLocation}
		return ast.NewTaggedTemplateLit(tag, quasis, exprs, span)
	}
	span := ast.Span{Start: token.Span().Start, End: parser.lexer.currentLocation}
	return ast.NewTemplateLit(quasis, exprs, span)
}

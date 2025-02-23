package parser

type Parser struct {
	lexer  *Lexer
	errors []*Error
}

func NewParser(source Source) *Parser {
	return &Parser{
		lexer:  NewLexer(source),
		errors: []*Error{},
	}
}

var precedence = map[BinaryOp]int{
	Times:             12,
	Divide:            12,
	Modulo:            12,
	Plus:              11,
	Minus:             11,
	LessThan:          9,
	LessThanEqual:     9,
	GreaterThan:       9,
	GreaterThanEqual:  9,
	Equal:             8,
	NotEqual:          8,
	LogicalAnd:        4,
	LogicalOr:         3,
	NullishCoalescing: 3,
}

func (parser *Parser) parseExpr() (*Expr, *Token) {
	values := NewStack[*Expr]()
	ops := NewStack[BinaryOp]()

	values = append(values, parser.parsePrimary())
	var lastToken *Token

loop:
	for {
		token := parser.lexer.next()
		lastToken = &token
		var nextOp BinaryOp

		switch token.Data.(type) {
		case *TPlus:
			nextOp = Plus
		case *TMinus:
			nextOp = Minus
		case *TAsterisk:
			nextOp = Times
		case *TSlash:
			nextOp = Divide
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TComma, *TEOF:
			break loop
		default:
			parser.errors = append(parser.errors, &Error{
				Span:    token.Span,
				Message: "Unexpected token",
			})
			continue
		}

		if nextOp == -1 {
			continue
		}

		if !ops.IsEmpty() {
			if precedence[ops.Peek()] >= precedence[nextOp] {
				// get the last operator and remove it from the list
				op := ops.Pop()
				right := values.Pop()
				left := values.Pop()

				values.Push(&Expr{
					Kind: &EBinary{Left: left, Op: op, Right: right},
					Span: Span{Start: left.Span.Start, End: right.Span.End},
				})
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

		values.Push(&Expr{
			Kind: &EBinary{Left: left, Op: op, Right: right},
			Span: Span{Start: left.Span.Start, End: right.Span.End},
		})
	}

	return values.Pop(), lastToken
}

type TokenAndOp struct {
	Token *Token
	Op    UnaryOp
}

func (parser *Parser) parsePrefix() Stack[TokenAndOp] {
	token := parser.lexer.peek()
	result := NewStack[TokenAndOp]()

loop:
	for {
		switch token.Data.(type) {
		case *TPlus:
			result.Push(TokenAndOp{Token: &token, Op: UnaryPlus})
		case *TMinus:
			result.Push(TokenAndOp{Token: &token, Op: UnaryMinus})
		default:
			break loop
		}
		parser.lexer.consume()
		token = parser.lexer.peek()
	}

	return result
}

func (parser *Parser) parseSuffix(expr *Expr) *Expr {
	token := parser.lexer.peek()

loop3:
	for {
		switch token.Data.(type) {
		case *TOpenParen:
			parser.lexer.consume()
			args, terminator := parser.parseSeq()
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: false},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenParen:
			parser.lexer.consume()
			args, terminator := parser.parseSeq()
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: true},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TOpenBracket:
			parser.lexer.consume()
			index, terminator := parser.parseExpr()
			obj := expr
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: false},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenBracket:
			parser.lexer.consume()
			index, terminator := parser.parseExpr()
			obj := expr
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: true},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		// TODO: dedupe with *TQuestionDot case
		case *TDot:
			parser.lexer.consume()
			prop := parser.lexer.next()
			switch t := prop.Data.(type) {
			case *TIdentifier:
				obj := expr
				prop := &Identifier{Name: t.Value, Span: prop.Span}
				expr =
					&Expr{
						Kind: &EMember{Object: obj, Prop: prop, OptChain: false},
						Span: Span{Start: obj.Span.Start, End: prop.Span.End},
					}
			default:
				obj := expr
				prop := &Identifier{
					Name: "",
					Span: Span{Start: token.Span.End, End: token.Span.End},
				}
				expr =
					&Expr{
						Kind: &EMember{Object: obj, Prop: prop, OptChain: false},
						Span: Span{Start: obj.Span.Start, End: prop.Span.End},
					}
				parser.errors = append(parser.errors, &Error{
					Span:    Span{Start: token.Span.Start, End: token.Span.End},
					Message: "expected an identifier after .",
				})
			}
		// TODO: dedupe with *TDot case
		case *TQuestionDot:
			parser.lexer.consume()
			prop := parser.lexer.next()
			switch t := prop.Data.(type) {
			case *TIdentifier:
				obj := expr
				prop := &Identifier{Name: t.Value, Span: token.Span}
				expr =
					&Expr{
						Kind: &EMember{Object: obj, Prop: prop, OptChain: true},
						Span: Span{Start: obj.Span.Start, End: prop.Span.End},
					}
			default:
				obj := expr
				prop := &Identifier{
					Name: "",
					Span: Span{Start: token.Span.End, End: token.Span.End},
				}
				expr =
					&Expr{
						Kind: &EMember{Object: obj, Prop: prop, OptChain: true},
						Span: Span{Start: obj.Span.Start, End: prop.Span.End},
					}
				parser.errors = append(parser.errors, &Error{
					Span:    Span{Start: token.Span.Start, End: token.Span.End},
					Message: "expected an identifier after ?.",
				})
			}
		default:
			break loop3
		}
		token = parser.lexer.peek()
	}

	return expr
}

func (parser *Parser) parsePrimary() *Expr {
	ops := parser.parsePrefix()
	token := parser.lexer.next()

	var expr *Expr

	// Loop until we parse a primary expression.
	for expr == nil {
		switch t := token.Data.(type) {
		case *TNumber:
			expr = &Expr{
				Kind: &ENumber{Value: t.Value},
				Span: token.Span,
			}
		case *TString:
			expr = &Expr{
				Kind: &EString{Value: t.Value},
				Span: token.Span,
			}
		case *TIdentifier:
			expr = &Expr{
				Kind: &EIdentifier{Name: t.Value},
				Span: token.Span,
			}
		case *TOpenParen:
			// parseExpr handles the closing paren for us
			expr, _ = parser.parseExpr()
		case *TOpenBracket:
			// parseExpr handles the closing bracket for us
			elems, final := parser.parseSeq()
			expr = &Expr{
				Kind: &EArray{Elems: elems},
				Span: Span{Start: token.Span.Start, End: final.Span.End},
			}
		case *TCloseBrace, *TComma, *TCloseParen, *TEOF:
			expr = &Expr{
				Kind: &EIgnore{Token: &token},
				Span: token.Span,
			}
			parser.errors = append(parser.errors, &Error{
				Span:    token.Span,
				Message: "Unexpected token",
			})
		default:
			parser.errors = append(parser.errors, &Error{
				Span:    token.Span,
				Message: "Unexpected token",
			})
			token = parser.lexer.next()
		}
	}

	expr = parser.parseSuffix(expr)

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = &Expr{
			Kind: &EUnary{Op: tokenAndOp.Op, Arg: expr},
			Span: Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span.End},
		}
	}

	return expr
}

// TODO: detect and recover from mismatched parens, e.g. foo(bar]
func (parser *Parser) parseSeq() ([]*Expr, *Token) {
	exprs := []*Expr{}

	expr, lastToken := parser.parseExpr()
	exprs = append(exprs, expr)

	for {
		switch lastToken.Data.(type) {
		case *TComma:
			expr, lastToken = parser.parseExpr()
			exprs = append(exprs, expr)
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TEOF:
			return exprs, lastToken
		default:
			panic("parseSeq - unexpected token")
		}
	}
}

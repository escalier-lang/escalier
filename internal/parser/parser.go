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
		token := parser.lexer.nextToken()
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

func (parser *Parser) parsePrefix() (Token, Stack[TokenAndOp]) {
	token := parser.lexer.nextToken()
	result := NewStack[TokenAndOp]()

loop1:
	for {
		switch token.Data.(type) {
		case *TPlus:
			result.Push(TokenAndOp{Token: &token, Op: UnaryPlus})
		case *TMinus:
			result.Push(TokenAndOp{Token: &token, Op: UnaryMinus})
		default:
			break loop1
		}
		token = parser.lexer.nextToken()
	}

	return token, result
}

func (parser *Parser) parsePrimary() *Expr {
	token, ops := parser.parsePrefix()

	var expr *Expr

loop2:
	for {
		switch t := token.Data.(type) {
		case *TNumber:
			expr = &Expr{
				Kind: &ENumber{Value: t.Value},
				Span: token.Span,
			}
			break loop2
		case *TString:
			expr = &Expr{
				Kind: &EString{Value: t.Value},
				Span: token.Span,
			}
			break loop2
		case *TIdentifier:
			expr = &Expr{
				Kind: &EIdentifier{Name: t.Value},
				Span: token.Span,
			}
			break loop2
		case *TOpenParen:
			// parseExpr handles the closing paren for us
			expr, _ = parser.parseExpr()
			break loop2
		case *TOpenBracket:
			// parseExpr handles the closing bracket for us
			elems, final := parser.parseSeq()
			expr = &Expr{
				Kind: &EArray{Elems: elems},
				Span: Span{Start: token.Span.Start, End: final.Span.End},
			}
			break loop2
		case *TCloseBrace, *TComma, *TCloseParen, *TEOF:
			expr = &Expr{
				Kind: &EIgnore{Token: &token},
				Span: token.Span,
			}
			parser.errors = append(parser.errors, &Error{
				Span:    token.Span,
				Message: "Unexpected token",
			})
			break loop2
		default:
			parser.errors = append(parser.errors, &Error{
				Span:    token.Span,
				Message: "Unexpected token",
			})
			// Loop until we parse a primary expression
			token = parser.lexer.nextToken()
		}
	}

	token = parser.lexer.peekToken()

loop3:
	for {
		switch token.Data.(type) {
		case *TOpenParen:
			parser.lexer.nextToken() // consumes the next token
			args, terminator := parser.parseSeq()
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: false},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenParen:
			parser.lexer.nextToken() // consumes the next token
			args, terminator := parser.parseSeq()
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: true},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TOpenBracket:
			parser.lexer.nextToken() // consumes the next token
			index, terminator := parser.parseExpr()
			obj := expr
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: false},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenBracket:
			parser.lexer.nextToken() // consumes the next token
			index, terminator := parser.parseExpr()
			obj := expr
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: true},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		// TODO: dedupe with *TQuestionDot case
		case *TDot:
			parser.lexer.nextToken() // consumes the next token
			prop := parser.lexer.nextToken()
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
			parser.lexer.nextToken() // consumes the next token
			prop := parser.lexer.nextToken()
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
		token = parser.lexer.peekToken()
	}

	for !ops.IsEmpty() {
		tokenAndOp := ops.Pop()
		expr = &Expr{
			Kind: &EUnary{Op: tokenAndOp.Op, Arg: expr},
			Span: Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span.End},
		}
	}

	return expr
}

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

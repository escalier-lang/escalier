package parser

type Parser struct {
	lexer *Lexer
}

func NewParser(source Source) *Parser {
	return &Parser{
		lexer: NewLexer(source),
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
	var terminator *Token

loop:
	for {
		token := parser.lexer.nextToken()
		var nextOp BinaryOp = -1

		switch token.Data.(type) {
		case *TPlus:
			nextOp = Plus
		case *TMinus:
			nextOp = Minus
		case *TAsterisk:
			nextOp = Times
		case *TSlash:
			nextOp = Divide
		case *TOpenParen:
			args, final := parser.parseSeq()
			value := values.Pop()
			values.Push(
				&Expr{
					Kind: &ECall{Callee: value, Args: args, OptChain: false},
					Span: Span{Start: value.Span.Start, End: final.Span.End},
				},
			)
		case *TQuestionOpenParen:
			args, final := parser.parseSeq()
			value := values.Pop()
			values.Push(
				&Expr{
					Kind: &ECall{Callee: value, Args: args, OptChain: true},
					Span: Span{Start: value.Span.Start, End: final.Span.End},
				},
			)
		case *TOpenBracket:
			index, final := parser.parseExpr()
			value := values.Pop()
			values.Push(
				&Expr{
					Kind: &EIndex{Object: value, Index: index, OptChain: false},
					Span: Span{Start: value.Span.Start, End: final.Span.End},
				},
			)
		case *TQuestionOpenBracket:
			index, final := parser.parseExpr()
			value := values.Pop()
			values.Push(
				&Expr{
					Kind: &EIndex{Object: value, Index: index, OptChain: true},
					Span: Span{Start: value.Span.Start, End: final.Span.End},
				},
			)
		case *TDot:
			prop := parser.lexer.nextToken()
			switch t := prop.Data.(type) {
			case *TIdentifier:
				value := values.Pop()
				prop := &Identifier{Name: t.Value, Span: prop.Span}
				values.Push(
					&Expr{
						Kind: &EMember{Object: value, Prop: prop, OptChain: false},
						Span: Span{Start: value.Span.Start, End: prop.Span.End},
					},
				)
			default:
				panic("parseExpr - expected an identifier")
			}
		case *TQuestionDot:
			prop := parser.lexer.nextToken()
			switch t := prop.Data.(type) {
			case *TIdentifier:
				value := values.Pop()
				prop := &Identifier{Name: t.Value, Span: token.Span}
				values.Push(
					&Expr{
						Kind: &EMember{Object: value, Prop: prop, OptChain: true},
						Span: Span{Start: value.Span.Start, End: prop.Span.End},
					},
				)
			default:
				panic("parseExpr - expected an identifier")
			}
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TComma:
			// TODO: report if there were mismatched parentheses
			// we can ignore extra closing parens so that we can continue
			// parsing the rest of the expression
			terminator = &token
			break loop
		case *TEOF:
			break loop
		default:
			// If there was a newline then we can treat this as being the end
			// of the expression.
			// If there wasn't a newline
			// TODO: report and error and recover
			panic("parseExpr - unexpected token")
		}

		if nextOp == -1 {
			continue
		}

		terminator = nil

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
		values.Push(parser.parsePrimary())
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

	return values.Pop(), terminator
}

func (parser *Parser) parsePrimary() *Expr {
	token := parser.lexer.nextToken()

	ops := NewStack[UnaryOp]()

loop:
	for {
		switch token.Data.(type) {
		case *TPlus:
			ops.Push(UnaryPlus)
		case *TMinus:
			ops.Push(UnaryMinus)
		default:
			break loop
		}
		token = parser.lexer.nextToken()
	}

	var expr *Expr

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
	default:
		// TODO: in this case we probably want to return an error since the
		// parent function will probably be able to handle it better
		panic("parsePrimary - unexpected token")
	}

	for !ops.IsEmpty() {
		op := ops.Pop()
		expr = &Expr{
			Kind: &EUnary{Op: op, Arg: expr},
			Span: Span{Start: token.Span.Start, End: expr.Span.End},
		}
	}

	return expr
}

func (parser *Parser) parseSeq() ([]*Expr, *Token) {
	exprs := []*Expr{}

	expr, terminator := parser.parseExpr()
	exprs = append(exprs, expr)

	// TODO: inside the loop do the following:
	// - if the terminator is a comma, parse another expression
	// - if the terminator is a closing paren, bracket, or brace, return the
	//   list of expressions

	for {
		switch terminator.Data.(type) {
		case *TComma:
			expr, terminator = parser.parseExpr()
			exprs = append(exprs, expr)
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TEOF:
			return exprs, terminator
		default:
			panic("parseSeq - unexpected token")
		}
	}
}

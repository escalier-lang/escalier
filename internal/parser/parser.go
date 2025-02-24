package parser

import (
	"fmt"
	"os"
	"runtime"
)

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

func (parser *Parser) parseExpr() *Expr {
	values := NewStack[*Expr]()
	ops := NewStack[BinaryOp]()

	values = append(values, parser.parsePrimary())

loop:
	for {
		token := parser.lexer.peek()
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
			parser.reportError(token.Span, "Unexpected token")
			continue
		}

		parser.lexer.consume()

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

	if len(values) != 1 {
		panic("parseExpr - expected one value on the stack")
	}
	return values.Pop()
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

loop:
	for {
		switch token.Data.(type) {
		case *TOpenParen:
			parser.lexer.consume()
			args := parser.parseSeq()
			terminator := parser.lexer.next()
			if _, ok := terminator.Data.(*TCloseParen); !ok {
				parser.reportError(token.Span, "Expected a closing paren")
			}
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: false},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenParen:
			parser.lexer.consume()
			args := parser.parseSeq()
			terminator := parser.lexer.next()
			if _, ok := terminator.Data.(*TCloseParen); !ok {
				parser.reportError(token.Span, "Expected a closing paren")
			}
			callee := expr
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: true},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TOpenBracket:
			parser.lexer.consume()
			index := parser.parseExpr()
			terminator := parser.lexer.next()
			if _, ok := terminator.Data.(*TCloseBracket); !ok {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
			obj := expr
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: false},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		case *TQuestionOpenBracket:
			parser.lexer.consume()
			index := parser.parseExpr()
			terminator := parser.lexer.next()
			if _, ok := terminator.Data.(*TCloseBracket); !ok {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
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
				parser.reportError(token.Span, "expected an identifier after .")
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
				parser.reportError(token.Span, "expected an identifier after ?.")
			}
		default:
			break loop
		}
		token = parser.lexer.peek()
	}

	return expr
}

func (parser *Parser) parsePrimary() *Expr {
	ops := parser.parsePrefix()
	// TODO: switch to peek() here
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
			expr = parser.parseExpr()
		case *TOpenBracket:
			// parseExpr handles the closing bracket for us
			elems := parser.parseSeq()
			final := parser.lexer.next() // consume the closing bracket
			expr = &Expr{
				Kind: &EArray{Elems: elems},
				Span: Span{Start: token.Span.Start, End: final.Span.End},
			}
		case *TCloseBrace, *TComma, *TCloseParen, *TEOF:
			expr = &Expr{
				Kind: &EIgnore{Token: &token},
				Span: token.Span,
			}
			parser.reportError(token.Span, "Unexpected token")
		default:
			parser.reportError(token.Span, "Unexpected token")
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
func (parser *Parser) parseSeq() []*Expr {
	exprs := []*Expr{}

	expr := parser.parseExpr()
	exprs = append(exprs, expr)

	lastToken := parser.lexer.peek()

	for {
		switch lastToken.Data.(type) {
		case *TComma:
			parser.lexer.consume()
			expr = parser.parseExpr()
			exprs = append(exprs, expr)
			lastToken = parser.lexer.peek()
		default:
			return exprs
		}
	}
}

func (parser *Parser) parseDecl() *Decl {
	// can start with `var`, `val`, or `fn` (and in the future `class`, `type`, `enum`, etc.)
	// we also need to be able to account `declare` and `export` modifiers in the future

	export := false
	declare := false

	token := parser.lexer.next()
	start := token.Span.Start
	if _, ok := token.Data.(*TExport); ok {
		export = true
		token = parser.lexer.next()
	}

	if _, ok := token.Data.(*TDeclare); ok {
		declare = true
		token = parser.lexer.next()
	}

	switch token.Data.(type) {
	case *TVal, *TVar:
		kind := ValKind
		if _, ok := token.Data.(*TVar); ok {
			kind = VarKind
		}

		token := parser.lexer.next()
		_ident, ok := token.Data.(*TIdentifier)
		if !ok {
			parser.reportError(token.Span, "Expected identifier")
			return nil
		}
		ident := &Identifier{Name: _ident.Value, Span: token.Span}
		end := token.Span.End

		token = parser.lexer.peek()
		var init *Expr
		if !declare {
			_, ok = token.Data.(*TEquals)
			if !ok {
				// TODO: lex tokens until we find an equals sign
				parser.reportError(token.Span, "Expected equals sign")
				return nil
			}
			parser.lexer.consume()
			init = parser.parseExpr()
			end = init.Span.End
		}

		return &Decl{
			// TODO: differentiate between 'var' and 'val'
			Kind: &DVariable{
				Name: ident,
				Kind: kind,
				Init: init,
			},
			Declare: declare,
			Export:  export,
			Span:    Span{Start: start, End: end},
		}
	// case *TFn:
	// 	token := parser.lexer.next()
	// 	_ident, ok := token.Data.(*TIdentifier)
	// 	if !ok {
	//      parser.reportError(token.Span, "Expected identifier")
	// 		return nil
	// 	}
	// 	ident := &Identifier{Name: _ident.Value, Span: token.Span}

	// 	return &Decl{
	// 		Kind: &DFunction{
	// 			Name:   ident,
	// 			Params: []*Param{},
	// 			Body:   []*Stmt{},
	// 		},
	// 		Declare: declare,
	// 		Export:  export,
	// 		Span:    Span{Start: start, End: ident.Span.End},
	// 	}
	default:
		parser.reportError(token.Span, "Unexpected token")
		return nil
	}
}

func (parser *Parser) parseStmt() *Stmt {
	token := parser.lexer.peek()

	switch token.Data.(type) {
	case *TFn, *TVar, *TVal, *TDeclare, *TExport:
		decl := parser.parseDecl()
		if decl == nil {
			return nil
		}
		return &Stmt{
			Kind: &SDecl{Decl: decl},
			Span: decl.Span,
		}
	// TODO: case *TReturn:
	// 	return nil
	default:
		parser.reportError(token.Span, "Unexpected token")
		return nil
	}
}

func (parser *Parser) reportError(span Span, message string) {
	_, _, line, _ := runtime.Caller(1)
	if os.Getenv("DEBUG") == "true" {
		message = fmt.Sprintf("%s:%d", message, line)
	}
	parser.errors = append(parser.errors, &Error{
		Span:    span,
		Message: message,
	})
}

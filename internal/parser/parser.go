package parser

import (
	"fmt"
	"os"
	"runtime"
)

type Parser struct {
	lexer  *Lexer
	Errors []*Error
}

func NewParser(source Source) *Parser {
	return &Parser{
		lexer:  NewLexer(source),
		Errors: []*Error{},
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

		switch token.Kind.(type) {
		case *TPlus:
			nextOp = Plus
		case *TMinus:
			nextOp = Minus
		case *TAsterisk:
			nextOp = Times
		case *TSlash:
			nextOp = Divide
		case *TCloseParen, *TCloseBracket, *TCloseBrace, *TComma, *TEndOfFile, *TVar, *TVal, *TFn, *TReturn:
			break loop
		default:
			return values.Pop()
			// parser.reportError(token.Span, "Unexpected token")
			// continue
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
		switch token.Kind.(type) {
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
		switch token.Kind.(type) {
		case *TOpenParen, *TQuestionOpenParen:
			parser.lexer.consume()
			args := parser.parseExprSeq()
			terminator := parser.lexer.next()
			if _, ok := terminator.Kind.(*TCloseParen); !ok {
				parser.reportError(token.Span, "Expected a closing paren")
			}
			callee := expr
			optChain := false
			if _, ok := token.Kind.(*TQuestionOpenParen); ok {
				optChain = true
			}
			expr =
				&Expr{
					Kind: &ECall{Callee: callee, Args: args, OptChain: optChain},
					Span: Span{Start: callee.Span.Start, End: terminator.Span.End},
				}
		case *TOpenBracket, *TQuestionOpenBracket:
			parser.lexer.consume()
			index := parser.parseExpr()
			terminator := parser.lexer.next()
			if _, ok := terminator.Kind.(*TCloseBracket); !ok {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
			obj := expr
			optChain := false
			if _, ok := token.Kind.(*TQuestionOpenBracket); ok {
				optChain = true
			}
			expr =
				&Expr{
					Kind: &EIndex{Object: obj, Index: index, OptChain: optChain},
					Span: Span{Start: obj.Span.Start, End: terminator.Span.End},
				}
		case *TDot, *TQuestionDot:
			parser.lexer.consume()
			prop := parser.lexer.next()
			optChain := false
			if _, ok := token.Kind.(*TQuestionDot); ok {
				optChain = true
			}
			switch t := prop.Kind.(type) {
			case *TIdentifier:
				obj := expr
				prop := &Identifier{Name: t.Value, Span: prop.Span}
				expr =
					&Expr{
						Kind: &EMember{Object: obj, Prop: prop, OptChain: optChain},
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
						Kind: &EMember{Object: obj, Prop: prop, OptChain: optChain},
						Span: Span{Start: obj.Span.Start, End: prop.Span.End},
					}
				if _, ok := token.Kind.(*TDot); ok {
					parser.reportError(token.Span, "expected an identifier after .")
				} else {
					parser.reportError(token.Span, "expected an identifier after ?.")
				}
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
	token := parser.lexer.peek()

	var expr *Expr

	// Loop until we parse a primary expression.
	for expr == nil {
		switch t := token.Kind.(type) {
		case *TNumber:
			parser.lexer.consume()
			expr = &Expr{
				Kind: &ENumber{Value: t.Value},
				Span: token.Span,
			}
		case *TString:
			parser.lexer.consume()
			expr = &Expr{
				Kind: &EString{Value: t.Value},
				Span: token.Span,
			}
		case *TIdentifier:
			parser.lexer.consume()
			expr = &Expr{
				Kind: &EIdentifier{Name: t.Value},
				Span: token.Span,
			}
		case *TOpenParen:
			parser.lexer.consume()
			expr = parser.parseExpr()
			final := parser.lexer.next() // consume the closing paren
			if _, ok := final.Kind.(*TCloseParen); !ok {
				parser.reportError(token.Span, "Expected a closing paren")
			}
		case *TOpenBracket:
			parser.lexer.consume()
			elems := parser.parseExprSeq()
			final := parser.lexer.next() // consume the closing bracket
			if _, ok := final.Kind.(*TCloseBracket); !ok {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
			expr = &Expr{
				Kind: &EArray{Elems: elems},
				Span: Span{Start: token.Span.Start, End: final.Span.End},
			}
		case
			*TVal, *TVar, *TFn, *TReturn,
			*TCloseBrace, *TCloseParen, *TCloseBracket,
			*TEndOfFile:
			expr = &Expr{
				Kind: &EEmpty{},
				Span: token.Span,
			}
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
		expr = &Expr{
			Kind: &EUnary{Op: tokenAndOp.Op, Arg: expr},
			Span: Span{Start: tokenAndOp.Token.Span.Start, End: expr.Span.End},
		}
	}

	return expr
}

func (parser *Parser) parseExprSeq() []*Expr {
	exprs := []*Expr{}

	// handles empty sequences
	token := parser.lexer.peek()
	switch token.Kind.(type) {
	case *TCloseBracket, *TCloseParen, *TCloseBrace:
		return exprs
	default:
	}

	expr := parser.parseExpr()
	exprs = append(exprs, expr)

	token = parser.lexer.peek()

	for {
		switch token.Kind.(type) {
		case *TComma:
			// TODO: handle trailing comma
			parser.lexer.consume()
			expr = parser.parseExpr()
			exprs = append(exprs, expr)
			token = parser.lexer.peek()
		default:
			return exprs
		}
	}
}

func (parser *Parser) parseParamSeq() []*Param {
	params := []*Param{}

	token := parser.lexer.peek()
	_ident, ok := token.Kind.(*TIdentifier)
	if !ok {
		return params
	}
	param := &Param{Name: &Identifier{Name: _ident.Value, Span: token.Span}}
	params = append(params, param)
	parser.lexer.consume()

	token = parser.lexer.peek()

	for {
		switch token.Kind.(type) {
		case *TComma:
			parser.lexer.consume()
			token = parser.lexer.peek()
			_ident, ok := token.Kind.(*TIdentifier)
			if !ok {
				return params
			}
			param := &Param{Name: &Identifier{Name: _ident.Value, Span: token.Span}}
			params = append(params, param)
			parser.lexer.consume()
		default:
			return params
		}
	}
}

func (parser *Parser) parseBlock() []*Stmt {
	stmts := []*Stmt{}

	token := parser.lexer.next()
	if _, ok := token.Kind.(*TOpenBrace); !ok {
		parser.reportError(token.Span, "Expected an opening brace")
		return stmts
	}

	token = parser.lexer.peek()
	for {
		switch token.Kind.(type) {
		case *TCloseBrace:
			parser.lexer.consume()
			return stmts
		default:
			stmt := parser.parseStmt()
			stmts = append(stmts, stmt)
			token = parser.lexer.peek()
		}
	}
}

func (parser *Parser) parseDecl() *Decl {
	export := false
	declare := false

	token := parser.lexer.next()
	start := token.Span.Start
	if _, ok := token.Kind.(*TExport); ok {
		export = true
		token = parser.lexer.next()
	}

	if _, ok := token.Kind.(*TDeclare); ok {
		declare = true
		token = parser.lexer.next()
	}

	switch token.Kind.(type) {
	case *TVal, *TVar:
		kind := ValKind
		if _, ok := token.Kind.(*TVar); ok {
			kind = VarKind
		}

		token := parser.lexer.peek()
		_ident, ok := token.Kind.(*TIdentifier)
		var ident *Identifier
		if ok {
			parser.lexer.consume()
			ident = &Identifier{Name: _ident.Value, Span: token.Span}
		} else {
			parser.reportError(token.Span, "Expected identifier")
			ident = &Identifier{
				Name: "",
				Span: Span{Start: token.Span.Start, End: token.Span.Start},
			}
		}
		end := token.Span.End

		token = parser.lexer.peek()
		var init *Expr
		if !declare {
			_, ok = token.Kind.(*TEquals)
			if !ok {
				parser.reportError(token.Span, "Expected equals sign")
				return nil
			}
			parser.lexer.consume()
			init = parser.parseExpr()
			end = init.Span.End
		}

		return &Decl{
			Kind: &DVariable{
				Name: ident,
				Kind: kind,
				Init: init,
			},
			Declare: declare,
			Export:  export,
			Span:    Span{Start: start, End: end},
		}
	case *TFn:
		token := parser.lexer.peek()
		_ident, ok := token.Kind.(*TIdentifier)
		var ident *Identifier
		if ok {
			parser.lexer.consume()
			ident = &Identifier{Name: _ident.Value, Span: token.Span}
		} else {
			parser.reportError(token.Span, "Expected identifier")
			ident = &Identifier{
				Name: "",
				Span: Span{Start: token.Span.Start, End: token.Span.Start},
			}
		}

		token = parser.lexer.next()
		if _, ok := token.Kind.(*TOpenParen); !ok {
			parser.reportError(token.Span, "Expected an opening paren")
			return nil
		}
		params := parser.parseParamSeq()
		token = parser.lexer.next()
		if _, ok := token.Kind.(*TCloseParen); !ok {
			parser.reportError(token.Span, "Expected a closing paren")
			return nil
		}

		body := []*Stmt{}
		if !declare {
			body = parser.parseBlock()
		}

		return &Decl{
			Kind: &DFunction{
				Name:   ident,
				Params: params,
				Body:   body,
			},
			Declare: declare,
			Export:  export,
			Span:    Span{Start: start, End: ident.Span.End},
		}
	default:
		parser.reportError(token.Span, "Unexpected token")
		return nil
	}
}

func (parser *Parser) parseStmt() *Stmt {
	token := parser.lexer.peek()

	switch token.Kind.(type) {
	case *TFn, *TVar, *TVal, *TDeclare, *TExport:
		decl := parser.parseDecl()
		if decl == nil {
			return nil
		}
		return &Stmt{
			Kind: &SDecl{Decl: decl},
			Span: decl.Span,
		}
	case *TReturn:
		parser.lexer.consume()
		expr := parser.parseExpr()
		return &Stmt{
			Kind: &SReturn{Expr: expr},
			Span: Span{Start: token.Span.Start, End: expr.Span.End},
		}
	default:
		expr := parser.parseExpr()
		return &Stmt{
			Kind: &SExpr{Expr: expr},
			Span: expr.Span,
		}
	}
}

func (parser *Parser) ParseModule() *Module {
	stmts := []*Stmt{}

	token := parser.lexer.peek()
	for {
		switch token.Kind.(type) {
		case *TEndOfFile:
			return &Module{Stmts: stmts}
		default:
			stmt := parser.parseStmt()
			stmts = append(stmts, stmt)
			token = parser.lexer.peek()
		}
	}
}

func (parser *Parser) reportError(span Span, message string) {
	_, _, line, _ := runtime.Caller(1)
	if os.Getenv("DEBUG") == "true" {
		message = fmt.Sprintf("%s:%d", message, line)
	}
	parser.Errors = append(parser.Errors, &Error{
		Span:    span,
		Message: message,
	})
}

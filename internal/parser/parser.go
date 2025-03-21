package parser

import (
	"fmt"
	"os"
	"runtime"

	"github.com/escalier-lang/escalier/internal/ast"
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

		switch token.Kind.(type) {
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
		switch token.Kind.(type) {
		case *TPlus:
			result.Push(TokenAndOp{Token: &token, Op: ast.UnaryPlus})
		case *TMinus:
			result.Push(TokenAndOp{Token: &token, Op: ast.UnaryMinus})
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
			expr = ast.NewCall(callee, args, optChain, ast.Span{Start: callee.Span().Start, End: terminator.Span.End})
		case *TOpenBracket, *TQuestionOpenBracket:
			parser.lexer.consume()
			index := parser.ParseExpr()
			terminator := parser.lexer.next()
			if _, ok := terminator.Kind.(*TCloseBracket); !ok {
				parser.reportError(token.Span, "Expected a closing bracket")
			}
			obj := expr
			optChain := false
			if _, ok := token.Kind.(*TQuestionOpenBracket); ok {
				optChain = true
			}
			expr = ast.NewIndex(obj, index, optChain, ast.Span{Start: obj.Span().Start, End: terminator.Span.End})
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
				prop := ast.NewIdentifier(t.Value, prop.Span)
				expr = ast.NewMember(obj, prop, optChain, ast.Span{Start: obj.Span().Start, End: prop.Span().End})
			default:
				obj := expr
				prop := ast.NewIdentifier(
					"",
					ast.Span{Start: token.Span.End, End: token.Span.End},
				)
				expr = ast.NewMember(obj, prop, optChain, ast.Span{Start: obj.Span().Start, End: prop.Span().End})
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

func (parser *Parser) parsePrimary() ast.Expr {
	ops := parser.parsePrefix()
	token := parser.lexer.peek()

	var expr ast.Expr

	// Loop until we parse a primary expression.
	for expr == nil {
		switch t := token.Kind.(type) {
		case *TNumber:
			parser.lexer.consume()
			expr = ast.NewNumber(t.Value, token.Span)
		case *TString:
			parser.lexer.consume()
			expr = ast.NewString(t.Value, token.Span)
		case *TIdentifier:
			parser.lexer.consume()
			expr = ast.NewIdent(t.Value, token.Span)
		case *TOpenParen:
			parser.lexer.consume()
			expr = parser.ParseExpr()
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
			expr = ast.NewArray(elems, ast.Span{Start: token.Span.Start, End: final.Span.End})
		case
			*TVal, *TVar, *TFn, *TReturn,
			*TCloseBrace, *TCloseParen, *TCloseBracket,
			*TEndOfFile:
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
	switch token.Kind.(type) {
	case *TCloseBracket, *TCloseParen, *TCloseBrace:
		return exprs
	default:
	}

	expr := parser.ParseExpr()
	exprs = append(exprs, expr)

	token = parser.lexer.peek()

	for {
		switch token.Kind.(type) {
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
	_ident, ok := token.Kind.(*TIdentifier)
	if !ok {
		return params
	}
	param := &ast.Param{Name: ast.NewIdentifier(_ident.Value, token.Span)}
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
			param := &ast.Param{Name: ast.NewIdentifier(_ident.Value, token.Span)}
			params = append(params, param)
			parser.lexer.consume()
		default:
			return params
		}
	}
}

func (parser *Parser) parseBlock() []ast.Stmt {
	stmts := []ast.Stmt{}

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

func (parser *Parser) parseDecl() ast.Decl {
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
		kind := ast.ValKind
		if _, ok := token.Kind.(*TVar); ok {
			kind = ast.VarKind
		}

		token := parser.lexer.peek()
		_ident, ok := token.Kind.(*TIdentifier)
		var ident *ast.Ident
		if ok {
			parser.lexer.consume()
			ident = ast.NewIdentifier(_ident.Value, token.Span)
		} else {
			parser.reportError(token.Span, "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
		}
		end := token.Span.End

		token = parser.lexer.peek()
		var init ast.Expr
		if !declare {
			_, ok = token.Kind.(*TEquals)
			if !ok {
				parser.reportError(token.Span, "Expected equals sign")
				return nil
			}
			parser.lexer.consume()
			init = parser.ParseExpr()
			end = init.Span().End
		}

		return ast.NewVarDecl(kind, ident, init, declare, export, ast.Span{Start: start, End: end})
	case *TFn:
		token := parser.lexer.peek()
		_ident, ok := token.Kind.(*TIdentifier)
		var ident *ast.Ident
		if ok {
			parser.lexer.consume()
			ident = ast.NewIdentifier(_ident.Value, token.Span)
		} else {
			parser.reportError(token.Span, "Expected identifier")
			ident = ast.NewIdentifier(
				"",
				ast.Span{Start: token.Span.Start, End: token.Span.Start},
			)
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

		body := []ast.Stmt{}
		if !declare {
			body = parser.parseBlock()
		}

		return ast.NewFuncDecl(ident, params, body, declare, export, ast.Span{Start: start, End: ident.Span().End})
	default:
		parser.reportError(token.Span, "Unexpected token")
		return nil
	}
}

func (parser *Parser) parseStmt() ast.Stmt {
	token := parser.lexer.peek()

	switch token.Kind.(type) {
	case *TFn, *TVar, *TVal, *TDeclare, *TExport:
		decl := parser.parseDecl()
		if decl == nil {
			return nil
		}
		return ast.NewDeclStmt(decl, decl.Span())
	case *TReturn:
		parser.lexer.consume()
		expr := parser.ParseExpr()
		return ast.NewReturnStmt(expr, ast.Span{Start: token.Span.Start, End: expr.Span().End})
	default:
		expr := parser.ParseExpr()
		return ast.NewExprStmt(expr, expr.Span())
	}
}

func (parser *Parser) ParseModule() *ast.Module {
	stmts := []ast.Stmt{}

	token := parser.lexer.peek()
	for {
		switch token.Kind.(type) {
		case *TEndOfFile:
			return &ast.Module{Stmts: stmts}
		default:
			stmt := parser.parseStmt()
			stmts = append(stmts, stmt)
			token = parser.lexer.peek()
		}
	}
}

func (parser *Parser) reportError(span ast.Span, message string) {
	_, _, line, _ := runtime.Caller(1)
	if os.Getenv("DEBUG") == "true" {
		message = fmt.Sprintf("%s:%d", message, line)
	}
	parser.Errors = append(parser.Errors, &Error{
		Span:    span,
		Message: message,
	})
}

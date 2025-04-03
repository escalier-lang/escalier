package parser

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/escalier-lang/escalier/internal/ast"
)

type Parser struct {
	lexer   *Lexer
	markers Stack[Marker]
	ctx     context.Context
	Errors  []*Error
}

type Marker int

const (
	MarkerExpr Marker = iota
	MarkerDelim
)

func NewParser(ctx context.Context, source Source) *Parser {
	return &Parser{
		ctx:     ctx,
		lexer:   NewLexer(source),
		markers: Stack[Marker]{},
		Errors:  []*Error{},
	}
}

func (parser *Parser) ParseModule() *ast.Module {
	stmts := []ast.Stmt{}

	token := parser.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case EndOfFile:
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

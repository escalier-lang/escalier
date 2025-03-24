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

func (parser *Parser) ParseModule() *ast.Module {
	stmts := []ast.Stmt{}

	token := parser.lexer.peek()
	for {
		switch token.(type) {
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

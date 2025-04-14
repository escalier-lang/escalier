package parser

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
)

type Parser struct {
	lexer   *Lexer
	markers Stack[Marker]
	ctx     context.Context
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
	}
}

func (p *Parser) Parse() (*ast.Module, []*Error) {
	stmts := []ast.Stmt{}
	errors := []*Error{}

	token := p.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case EndOfFile:
			return &ast.Module{Stmts: stmts}, errors
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt, stmtErrors := p.stmt()
			errors = append(errors, stmtErrors...)
			stmt.IfSome(func(stmt ast.Stmt) {
				stmts = append(stmts, stmt)
			})
			token = p.lexer.peek()
		}
	}
}

// func (parser *Parser) reportError(span ast.Span, message string) {
// 	_, _, line, _ := runtime.Caller(1)
// 	if os.Getenv("DEBUG") == "true" {
// 		message = fmt.Sprintf("%s:%d", message, line)
// 	}
// 	parser.Errors = append(parser.Errors, &Error{
// 		Span:    span,
// 		Message: message,
// 	})
// }

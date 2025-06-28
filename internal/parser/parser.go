package parser

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
)

type Parser struct {
	ctx     context.Context
	lexer   *Lexer
	markers Stack[Marker]
	errors  []*Error
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
		errors:  []*Error{},
	}
}

func (p *Parser) ParseScript() (*ast.Script, []*Error) {
	stmts := []ast.Stmt{}

	token := p.lexer.peek()
	for {
		//nolint: exhaustive
		switch token.Type {
		case EndOfFile:
			return &ast.Script{Stmts: stmts}, p.errors
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt := p.stmt()
			if stmt != nil {
				stmts = append(stmts, stmt)
			}
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

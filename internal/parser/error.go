package parser

import "github.com/escalier-lang/escalier/internal/ast"

type Error struct {
	Span    ast.Span `json:"span"`
	Message string   `json:"message"`
}

func NewError(span ast.Span, message string) *Error {
	return &Error{
		Span:    span,
		Message: message,
	}
}

func (p *Parser) reportError(span ast.Span, message string) {
	// _, _, line, _ := p.ctx.Value("caller").(ast.Location).LineInfo()
	// if p.ctx.Value("debug") == true {
	// 	message = message + " at line " + string(line)
	// }
	p.errors = append(p.errors, NewError(span, message))
}

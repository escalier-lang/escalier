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

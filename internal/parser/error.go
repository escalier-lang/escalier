package parser

import (
	"fmt"
	"runtime/debug"

	"github.com/escalier-lang/escalier/internal/ast"
)

type Error struct {
	Span       ast.Span `json:"span"`
	Message    string   `json:"message"`
	stackTrace []byte
}

func NewError(span ast.Span, message string) *Error {
	return &Error{
		Span:       span,
		Message:    message,
		stackTrace: debug.Stack(),
	}
}

// StackTrace returns the Go stack trace captured when this error was created.
func (e *Error) StackTrace() []byte {
	return e.stackTrace
}

// GoString implements fmt.GoStringer to produce a stable representation that
// excludes the non-deterministic stackTrace field. This keeps snapshot tests
// reproducible while still allowing the stack trace to appear in String() output.
func (e *Error) GoString() string {
	return fmt.Sprintf("&parser.Error{Span: %#v, Message: %q}", e.Span, e.Message)
}

func (e *Error) String() string {
	base := e.Span.String() + ": " + e.Message
	if len(e.stackTrace) > 0 {
		return base + "\nStack trace:\n" + string(e.stackTrace)
	}
	return base
}

func (p *Parser) reportError(span ast.Span, message string) {
	p.errors = append(p.errors, NewError(span, message))
}

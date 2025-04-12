package parser

import "github.com/escalier-lang/escalier/internal/ast"

type Error struct {
	Span    ast.Span `json:"span"`
	Message string   `json:"message"`
}

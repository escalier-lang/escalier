package parser

import "github.com/escalier-lang/escalier/internal/ast"

type Error struct {
	Span    ast.Span
	Message string
}

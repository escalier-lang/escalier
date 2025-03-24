package parser

import (
	"fmt"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseStmtNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDecl": {
			input: "var x = 5",
		},
		"ValDecl": {
			input: "val x = 5",
		},
		"ExportValDecl": {
			input: "export val x = 5",
		},
		"DeclareValDecl": {
			input: "declare val x",
		},
		"ExportDeclareValDecl": {
			input: "export declare val x",
		},
		"FunctionDecl": {
			input: "fn foo(a, b) { a + b }",
		},
		"FunctionDeclWithReturn": {
			input: "fn foo(a, b) { return a + b }",
		},
		"FunctionDeclWithMultipleStmts": {
			input: `fn foo() {
				val a = 5
				val b = 10
				return a + b
			}`,
		},
		"ExportFunctionDecl": {
			input: "export fn foo(a, b) { a + b }",
		},
		"DeclareFunctionDecl": {
			input: "declare fn foo(a, b)",
		},
		"ExportDeclareFunctionDecl": {
			input: "export declare fn foo(a, b)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			stmt := parser.parseStmt()

			snaps.MatchSnapshot(t, stmt)
			if len(parser.Errors) > 0 {
				fmt.Printf("Error[0]: %#v", parser.Errors[0])
			}
			assert.Len(t, parser.Errors, 0)
		})
	}
}

func TestParseStmtErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDeclMissingIdent": {
			input: "var = 5",
		},
		"VarDeclMissingEquals": {
			input: "var x 5",
		},
		"FunctionDeclMissingIdent": {
			input: `fn () {return 5}`,
		},
		"FunctionDeclMissingBoyd": {
			input: "fn foo(a, b)",
		},
		"FunctionDeclWithIncompleteStmts": {
			input: `fn foo() {
				val a = 
				val b = 5
				return a +
			}`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			stmt := parser.parseStmt()

			snaps.MatchSnapshot(t, stmt)
			assert.Greater(t, len(parser.Errors), 0)
			snaps.MatchSnapshot(t, parser.Errors)
		})
	}
}

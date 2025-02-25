package parser

import (
	"fmt"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseExprNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringLiteral": {
			input: "\"hello\"",
		},
		"NumberLiteral": {
			input: "5",
		},
		"Addition": {
			input: "a + b",
		},
		"AddSub": {
			input: "a - b + c",
		},
		"MulAdd": {
			input: "a * b + c * d",
		},
		"MulDiv": {
			input: "a / b * c",
		},
		"UnaryOps": {
			input: "+a - -b",
		},
		"Parens": {
			input: "a * (b + c)",
		},
		"Call": {
			input: "foo(a, b, c)",
		},
		"CallPrecedence": {
			input: "a + foo(b)",
		},
		"CurriedCall": {
			input: "foo(a)(b)(c)",
		},
		"OptChainCall": {
			input: "foo?(bar)",
		},
		"ArrayLiteral": {
			input: "[1, 2, 3]",
		},
		"Member": {
			input: "a.b?.c",
		},
		"MemberPrecedence": {
			input: "a + b.c",
		},
		"Index": {
			input: "a[base + offset]",
		},
		"IndexPrecedence": {
			input: "a + b[c]",
		},
		"MultipleIndexes": {
			input: "a[i][j]",
		},
		"OptChainIndex": {
			input: "a?[base + offset]",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.parseExpr()

			snaps.MatchSnapshot(t, expr)
			assert.Len(t, parser.errors, 0)
		})
	}
}

func TestParseExprErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"IncompleteBinaryExpr": {
			input: "a - b +",
		},
		"ExtraOperatorsInBinaryExpr": {
			input: "a + * b",
		},
		"IncompleteCall": {
			input: "foo(a,",
		},
		"IncompleteMember": {
			input: "a + b.",
		},
		"IncompleteMemberOptChain": {
			input: "a + b?.",
		},
		"MismatchedParens": {
			input: "a * (b + c]",
		},
		"MismatchedBracketsArrayLiteral": {
			input: "[1, 2, 3)",
		},
		"MismatchedBracketsIndex": {
			input: "foo[bar)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.parseExpr()

			snaps.MatchSnapshot(t, expr)
			assert.Greater(t, len(parser.errors), 0)
			snaps.MatchSnapshot(t, parser.errors)
		})
	}
}

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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			stmt := parser.parseStmt()

			snaps.MatchSnapshot(t, stmt)
			if len(parser.errors) > 0 {
				fmt.Printf("Error[0]: %#v", parser.errors[0])
			}
			assert.Len(t, parser.errors, 0)
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
				path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			stmt := parser.parseStmt()

			snaps.MatchSnapshot(t, stmt)
			assert.Greater(t, len(parser.errors), 0)
			snaps.MatchSnapshot(t, parser.errors)
		})
	}
}

package parser

import (
	"fmt"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDecls": {
			input: `
				val a = 5
				val b = 10
				val sum = a + b
			`,
		},
		"FuncDecls": {
			input: `
				fn add(a, b) {
					return a + b
				}
				fn sub(a, b) {
					return a - b
				}
			`,
		},
		"ExprStmts": {
			input: `
				foo()
				bar()
			`,
		},
		"SplitExprOnNewline": {
			input: `
				var a = x
				-y
			`,
		},
		"MultilineExprInParens": {
			input: `
				var a = (x
				-y)
			`,
		},
		"MultilineExprInBrackets": {
			input: `
				a[base
				+offset]
			`,
		},
		"SplitExprInNewScope": {
			input: `
				val funcs = [
					fn() {
						var a = x
						-y
					}		
				]
			`,
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
			module := parser.ParseModule()

			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
			if len(parser.Errors) > 0 {
				for i, err := range parser.Errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, parser.Errors, 0)
		})
	}
}

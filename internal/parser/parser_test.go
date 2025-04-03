package parser

import (
	"context"
	"fmt"
	"testing"
	"time"

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
		"IfElse": {
			input: `
				val x = if cond {
					var a = 5
					-10
				} else {
				 	var b = 10
					-5
				}
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

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
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

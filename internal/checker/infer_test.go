package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/parser"
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := parser.Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := parser.NewParser(ctx, source)
			script, errors := parser.ParseScript()

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Filename:   "input.esc",
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			bindings, inferErrors := c.InferScript(inferCtx, script)
			assert.Len(t, inferErrors, 0)
			assert.Len(t, bindings, 3)
			for name, binding := range bindings {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

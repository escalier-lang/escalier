package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestMutabilityTransitions(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		// Rule 1: mut → immutable — OK when source is dead
		"MutToImmutable_SourceDead_OK": {
			input: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					items.x = 2
					val snapshot: {x: number} = items
					snapshot
				}
			`,
			expectErrors: false,
		},
		// Rule 1: mut → immutable — ERROR when source is live
		"MutToImmutable_SourceLive_Error": {
			input: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					val snapshot: {x: number} = items
					items.x = 2
					snapshot
				}
			`,
			expectErrors: true,
		},
		// Rule 2: immutable → mut — OK when source is dead
		"ImmutableToMut_SourceDead_OK": {
			input: `
				fn test() {
					val config: {host: string} = {host: "localhost"}
					config
					val mutableConfig: mut {host: string} = config
					mutableConfig.host = "0.0.0.0"
				}
			`,
			expectErrors: false,
		},
		// Rule 2: immutable → mut — ERROR when source is live
		"ImmutableToMut_SourceLive_Error": {
			input: `
				fn test() {
					val config: {host: string} = {host: "localhost"}
					val mutableConfig: mut {host: string} = config
					mutableConfig.host = "0.0.0.0"
					config
				}
			`,
			expectErrors: true,
		},
		// Rule 3: multiple mutable aliases — always OK
		"MultipleMutableAliases_OK": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					b.x = 2
					a.x
				}
			`,
			expectErrors: false,
		},
		// Alias tracking: transitive alias conflict
		"TransitiveAlias_MutToImmutable_Error": {
			input: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val r: mut {x: number} = p
					val q: {x: number} = p
					r.x = 5
				}
			`,
			expectErrors: true,
		},
		// Fresh value — no alias, no transition check
		"FreshValue_NoTransition": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: {x: number} = {x: 2}
					a.x = 3
					b
				}
			`,
			expectErrors: false,
		},
		// Chain aliasing: val b = a; val c = b — a is still live and mutable
		"ChainAlias_MutToImmutable_Error": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					val c: {x: number} = b
					a.x = 2
					c
				}
			`,
			expectErrors: true,
		},
		// Chain aliasing: val b = a; val c = b — a is dead after transition
		"ChainAlias_MutToImmutable_OK_WhenDead": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					a.x = 2
					val c: {x: number} = b
					c
				}
			`,
			expectErrors: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if test.expectErrors {
				assert.NotEmpty(t, inferErrors, "Expected inference errors for %s", name)
				// Print the errors to help debugging
				for i, err := range inferErrors {
					t.Logf("Error[%d]: %s", i, err.Message())
				}
			} else {
				if len(inferErrors) > 0 {
					for i, err := range inferErrors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				assert.Empty(t, inferErrors, "Expected no inference errors for %s", name)
			}
		})
	}
}

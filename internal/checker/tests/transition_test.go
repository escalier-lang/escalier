package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type expectedTransitionError struct {
	SourceVar       string
	TargetVar       string
	ConflictingVars []string
	MutToImmutable  bool
}

func TestMutabilityTransitions(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []expectedTransitionError
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
			expectedErrors: []expectedTransitionError{{
				SourceVar:       "items",
				TargetVar:       "snapshot",
				ConflictingVars: []string{"items"},
				MutToImmutable:  true,
			}},
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
			expectedErrors: []expectedTransitionError{{
				SourceVar:       "config",
				TargetVar:       "mutableConfig",
				ConflictingVars: []string{"config"},
				MutToImmutable:  false,
			}},
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
		},
		// Alias tracking: transitive alias — OK when immutable target is dead
		"TransitiveAlias_MutToImmutable_TargetDead_OK": {
			input: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val r: mut {x: number} = p
					val q: {x: number} = p
					r.x = 5
				}
			`,
		},
		// Alias tracking: transitive alias — ERROR when immutable target is live
		"TransitiveAlias_MutToImmutable_TargetLive_Error": {
			input: `
				fn test() {
					val p: mut {x: number} = {x: 0}
					val r: mut {x: number} = p
					val q: {x: number} = p
					r.x = 5
					q
				}
			`,
			expectedErrors: []expectedTransitionError{{
				SourceVar:       "p",
				TargetVar:       "q",
				ConflictingVars: []string{"r"},
				MutToImmutable:  true,
			}},
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
		},
		// Chain aliasing: val b = a; val c = b — OK when immutable target c is dead
		"ChainAlias_MutToImmutable_TargetDead_OK": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					val c: {x: number} = b
					a.x = 2
				}
			`,
		},
		// Chain aliasing: val b = a; val c = b — ERROR when immutable target c is live
		"ChainAlias_MutToImmutable_TargetLive_Error": {
			input: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					val c: {x: number} = b
					a.x = 2
					c
				}
			`,
			expectedErrors: []expectedTransitionError{{
				SourceVar:       "b",
				TargetVar:       "c",
				ConflictingVars: []string{"a"},
				MutToImmutable:  true,
			}},
		},
		// Parameter alias: mut param → immutable — ERROR when param is still live
		"ParamAlias_MutToImmutable_SourceLive_Error": {
			input: `
				fn test(items: mut {x: number}) {
					val snapshot: {x: number} = items
					items.x = 2
					snapshot
				}
			`,
			expectedErrors: []expectedTransitionError{{
				SourceVar:       "items",
				TargetVar:       "snapshot",
				ConflictingVars: []string{"items"},
				MutToImmutable:  true,
			}},
		},
		// Parameter alias: mut param → immutable — OK when param is dead
		"ParamAlias_MutToImmutable_SourceDead_OK": {
			input: `
				fn test(items: mut {x: number}) {
					items.x = 2
					val snapshot: {x: number} = items
					snapshot
				}
			`,
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
		},
	}

	// Top-level script code (no wrapping function) — same rules apply.
	tests["TopLevel_MutToImmutable_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			val items: mut {x: number} = {x: 1}
			val snapshot: {x: number} = items
			items.x = 2
			snapshot
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "items",
			TargetVar:       "snapshot",
			ConflictingVars: []string{"items"},
			MutToImmutable:  true,
		}},
	}
	tests["TopLevel_MutToImmutable_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			val items: mut {x: number} = {x: 1}
			items.x = 2
			val snapshot: {x: number} = items
			snapshot
		`,
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

			require.Empty(t, parseErrors, "Expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			var mutErrors []*MutabilityTransitionError
			for _, err := range inferErrors {
				if mutErr, ok := err.(*MutabilityTransitionError); ok {
					mutErrors = append(mutErrors, mutErr)
				}
			}

			if len(test.expectedErrors) == 0 {
				assert.Empty(t, mutErrors, "Expected no MutabilityTransitionError")
			} else {
				require.Len(t, mutErrors, len(test.expectedErrors), "Wrong number of MutabilityTransitionErrors")
				for i, expected := range test.expectedErrors {
					actual := mutErrors[i]
					assert.Equal(t, expected.SourceVar, actual.SourceVar, "SourceVar mismatch")
					assert.Equal(t, expected.TargetVar, actual.TargetVar, "TargetVar mismatch")
					assert.Equal(t, expected.ConflictingVars, actual.ConflictingVars, "ConflictingVars mismatch")
					assert.Equal(t, expected.MutToImmutable, actual.MutToImmutable, "MutToImmutable mismatch")
				}
			}
		})
	}
}

package tests

import (
	"cmp"
	"context"
	"slices"
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

	// Phase 7.1: Object property aliasing — obj.prop = value merges alias sets
	tests["PropertyAssignment_MergesAliasSets_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val obj: mut {prop: mut {x: number}} = {prop: {x: 0}}
				val p: mut {x: number} = {x: 1}
				obj.prop = p
				val q: {x: number} = p
				obj.prop.x = 5
				q
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "p",
			TargetVar:       "q",
			ConflictingVars: []string{"obj"},
			MutToImmutable:  true,
		}},
	}
	tests["PropertyAssignment_FreshValue_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val obj: mut {prop: mut {x: number}} = {prop: {x: 0}}
				obj.prop = {x: 1}
				val q: {x: number} = obj
				q
			}
		`,
	}

	// Phase 7.3: Destructuring aliasing
	tests["Destructuring_ObjectPat_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val obj: mut {a: mut {x: number}} = {a: {x: 0}}
				val {a}: {a: mut {x: number}} = obj
				val frozen: {a: mut {x: number}} = obj
				a.x = 5
				frozen
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "obj",
			TargetVar:       "frozen",
			ConflictingVars: []string{"a"},
			MutToImmutable:  true,
		}},
	}
	tests["Destructuring_FreshSource_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val {a}: {a: mut {x: number}} = {a: {x: 0}}
				a.x = 5
			}
		`,
	}

	// Phase 7.4: Conditional aliasing
	tests["Conditional_IfElse_AliasesBothBranches_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				val c: {x: number} = if cond { a } else { b }
				a.x = 5
				c
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "a",
			TargetVar:       "c",
			ConflictingVars: []string{"a"},
			MutToImmutable:  true,
		}},
	}
	tests["Conditional_IfElse_AliasesBothBranches_OtherBranch_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				val c: {x: number} = if cond { a } else { b }
				b.x = 5
				c
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "b",
			TargetVar:       "c",
			ConflictingVars: []string{"b"},
			MutToImmutable:  true,
		}},
	}

	// Conditional aliasing: both branches violate the transition, so both
	// errors should be reported (not just the first).
	tests["Conditional_IfElse_BothBranchesViolate_MultipleErrors"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				val c: {x: number} = if cond { a } else { b }
				a.x = 5
				b.x = 5
				c
			}
		`,
		expectedErrors: []expectedTransitionError{
			{
				SourceVar:       "a",
				TargetVar:       "c",
				ConflictingVars: []string{"a"},
				MutToImmutable:  true,
			},
			{
				SourceVar:       "b",
				TargetVar:       "c",
				ConflictingVars: []string{"b"},
				MutToImmutable:  true,
			},
		},
	}

	// Phase 7.2: Closure capture aliasing
	//
	// When a closure creates an immutable capture of a mutable variable
	// that has live mutable aliases, the mut→immut transition should be
	// checked at the closure definition point — otherwise a mutable alias
	// can mutate the value while the closure's immutable view is live.
	tests["ClosureCapture_ReadOnly_MutSourceWithLiveMutAlias_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: mut {x: number} = {x: 0}
				val mutRef: mut {x: number} = items
				val f = fn() -> {x: number} { items }
				mutRef.x = 5
				f
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "items",
			TargetVar:       "f",
			ConflictingVars: []string{"mutRef"},
			MutToImmutable:  true,
		}},
	}

	tests["ClosureCapture_ReadOnly_BlocksImmutableToMut"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: {x: number} = {x: 0}
				val f = fn() -> {x: number} { items }
				val mutItems: mut {x: number} = items
				mutItems.x = 5
				f
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "items",
			TargetVar:       "mutItems",
			ConflictingVars: []string{"f"},
			MutToImmutable:  false,
		}},
	}
	tests["ClosureCapture_MutableCapture_BlocksMutToImmutable"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: mut {x: number} = {x: 0}
				val f = fn() { items.x = 1 }
				val snapshot: {x: number} = items
				f
				snapshot
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "items",
			TargetVar:       "snapshot",
			ConflictingVars: []string{"f"},
			MutToImmutable:  true,
		}},
	}
	tests["ClosureCapture_ReadOnly_DoesNotBlockMutToImmutable"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: mut {x: number} = {x: 0}
				val f = fn() -> {x: number} { items }
				items.x = 1
				val snapshot: {x: number} = items
				snapshot
			}
		`,
		// Read-only capture f is an immutable alias of items.
		// Rule 1 (mut→immutable) only blocks on live MUTABLE aliases.
		// f is immutable, so it does NOT block the transition.
	}
	tests["ClosureCapture_DeadClosure_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: {x: number} = {x: 0}
				val f = fn() -> {x: number} { items }
				f
				val mutItems: mut {x: number} = items
				mutItems.x = 5
			}
		`,
		// f is dead after its last use, so the transition is safe.
	}

	// Closure capture with shadowed variable names: the closure should
	// capture the outer variable (the one in scope at the closure definition),
	// not a same-named variable from a nested scope.
	tests["ClosureCapture_ShadowedByForIn_CapturesOuterVar_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: mut {x: number} = {x: 0}
				for item in [1, 2, 3] {
					val items: {x: number} = {x: item}
					items
				}
				val mutRef: mut {x: number} = items
				val f = fn() -> {x: number} { items }
				mutRef.x = 5
				f
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "items",
			TargetVar:       "f",
			ConflictingVars: []string{"mutRef"},
			MutToImmutable:  true,
		}},
	}
	tests["ClosureCapture_ShadowedByForIn_CapturesOuterVar_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val items: {x: number} = {x: 0}
				for item in [1, 2, 3] {
					val items: mut {x: number} = {x: item}
					items.x = 5
				}
				val f = fn() -> {x: number} { items }
				val snapshot: {x: number} = items
				f
				snapshot
			}
		`,
		// The loop-scoped mutable `items` is a different variable from the
		// outer immutable `items`. The closure captures the outer one, so
		// no mut→immut conflict exists.
	}

	// Reassignment with conditional RHS should not merge unrelated alias sets.
	// When `d = if cond { a } else { b }`, d aliases both a and b, but a and
	// b should NOT become aliases of each other.
	tests["Conditional_Reassignment_DoesNotMergeUnrelatedSets_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				var d: mut {x: number} = {x: 2}
				d = if cond { a } else { b }
				d
				val frozen: {x: number} = a
				b.x = 5
				frozen
			}
		`,
		// d is dead after its last use. b is not an alias of a, so the
		// mut→immutable transition on a should succeed. If alias sets
		// were incorrectly merged, b would appear as a conflicting alias.
	}

	// Phase 7.5: Reassignment leaves alias set (already tested in Phase 5,
	// included here for completeness)
	tests["Reassignment_LeavesAliasSet_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val a: mut {x: number} = {x: 0}
				var b: mut {x: number} = a
				b = {x: 1}
				val q: {x: number} = a
				q
			}
		`,
		// b was reassigned to a fresh value, so it left a's alias set.
	}

	// Reassignment: `var x: immut = a; x = b` where b is mut, checks the
	// transition at the reassignment point.
	tests["Reassignment_ImmutableToMut_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val a: mut {x: number} = {x: 0}
				var b: {x: number} = {x: 1}
				b = a
				a.x = 5
				b
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "a",
			TargetVar:       "b",
			ConflictingVars: []string{"a"},
			MutToImmutable:  true,
		}},
	}
	tests["Reassignment_MutToMut_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val a: mut {x: number} = {x: 0}
				var b: mut {x: number} = {x: 1}
				b = a
				a.x = 5
				b
			}
		`,
	}

	// Conditional reassignment: `var c = if cond { a } else { b }` where
	// the transition is violated for one branch.
	tests["Conditional_Reassignment_Error"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				var c: {x: number} = {x: 2}
				c = if cond { a } else { b }
				a.x = 5
				c
			}
		`,
		expectedErrors: []expectedTransitionError{{
			SourceVar:       "a",
			TargetVar:       "c",
			ConflictingVars: []string{"a"},
			MutToImmutable:  true,
		}},
	}

	// Reassignment with a fresh value after aliasing should clear the alias
	// and allow transitions that were previously blocked.
	tests["Reassignment_FreshAfterAlias_ClearsConflict_OK"] = struct {
		input          string
		expectedErrors []expectedTransitionError
	}{
		input: `
			fn test() {
				val a: mut {x: number} = {x: 0}
				var b: {x: number} = a
				b = {x: 1}
				a.x = 5
				b
			}
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

				// Sort both slices by SourceVar so the comparison is
				// order-independent.
				sortedActual := make([]*MutabilityTransitionError, len(mutErrors))
				copy(sortedActual, mutErrors)
				slices.SortStableFunc(sortedActual, func(a, b *MutabilityTransitionError) int {
					if c := cmp.Compare(a.SourceVar, b.SourceVar); c != 0 {
						return c
					}
					return cmp.Compare(a.TargetVar, b.TargetVar)
				})
				sortedExpected := make([]expectedTransitionError, len(test.expectedErrors))
				copy(sortedExpected, test.expectedErrors)
				slices.SortStableFunc(sortedExpected, func(a, b expectedTransitionError) int {
					if c := cmp.Compare(a.SourceVar, b.SourceVar); c != 0 {
						return c
					}
					return cmp.Compare(a.TargetVar, b.TargetVar)
				})

				for i, expected := range sortedExpected {
					actual := sortedActual[i]
					assert.Equal(t, expected.SourceVar, actual.SourceVar, "SourceVar mismatch")
					assert.Equal(t, expected.TargetVar, actual.TargetVar, "TargetVar mismatch")
					assert.Equal(t, expected.ConflictingVars, actual.ConflictingVars, "ConflictingVars mismatch")
					assert.Equal(t, expected.MutToImmutable, actual.MutToImmutable, "MutToImmutable mismatch")
				}
			}
		})
	}
}

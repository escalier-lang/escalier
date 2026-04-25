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

// formatTransitionError converts a MutabilityTransitionError into a
// human-readable string for test assertions. The format matches
// MutabilityTransitionError.Message().
func formatTransitionError(e *MutabilityTransitionError) string {
	return e.Message()
}

func TestMutabilityTransitions(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
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
		// Rule ParamAlias_MutToImmutable_SourceLive_Error1: mut → immutable — ERROR when source is live
		"MutToImmutable_SourceLive_Error": {
			input: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					val snapshot: {x: number} = items
					items.x = 2
					snapshot
				}
			`,
			expectedErrors: []string{
				"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
			},
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
			expectedErrors: []string{
				"cannot assign 'config' to mutable 'mutableConfig': 'config' is still used immutably after this point",
			},
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
			expectedErrors: []string{
				"cannot assign 'p' to immutable 'q': 'r' still has mutable access to 'p' after this point",
			},
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
			expectedErrors: []string{
				"cannot assign 'b' to immutable 'c': 'a' still has mutable access to 'b' after this point",
			},
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
		expectedErrors []string
	}{
		input: `
			val items: mut {x: number} = {x: 1}
			val snapshot: {x: number} = items
			items.x = 2
			snapshot
		`,
		expectedErrors: []string{
			"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
		},
	}
	// Phase 7.1: Object property aliasing — obj.prop = value merges alias sets
	tests["PropertyAssignment_MergesAliasSets_Error"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'p' to immutable 'q': 'obj' still has mutable access to 'p' after this point",
		},
	}
	tests["PropertyAssignment_FreshValue_OK"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'obj' to immutable 'frozen': 'a' still has mutable access to 'obj' after this point",
		},
	}
	tests["Destructuring_FreshSource_OK"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'a' to immutable 'c': 'a' is still used mutably after this point",
		},
	}
	// Conditional aliasing: both branches violate the transition, so both
	// errors should be reported (not just the first).
	tests["Conditional_IfElse_BothBranchesViolate_MultipleErrors"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'a' to immutable 'c': 'a' is still used mutably after this point",
			"cannot assign 'b' to immutable 'c': 'b' is still used mutably after this point",
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
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'items' to immutable 'f': 'mutRef' still has mutable access to 'items' after this point",
		},
	}

	tests["ClosureCapture_ReadOnly_BlocksImmutableToMut"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'items' to mutable 'mutItems': 'f' still has immutable access to 'items' after this point",
		},
	}
	tests["ClosureCapture_MutableCapture_BlocksMutToImmutable"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'items' to immutable 'snapshot': 'f' still has mutable access to 'items' after this point",
		},
	}
	tests["ClosureCapture_ReadOnly_DoesNotBlockMutToImmutable"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors []string
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

	// Primitive variables have value semantics — reassigning a captured
	// primitive inside a closure can't affect other variables that copied
	// the value, so no transition error should be reported.
	tests["ClosureCapture_PrimitiveReassign_NoTransitionError"] = struct {
		input          string
		expectedErrors []string
	}{
		input: `
			fn test() {
				var count = 0
				val count2 = count
				val f = fn() { count = count + 1 }
				f()
				count2
			}
		`,
	}

	// Literal types also have value semantics — same as primitives.
	tests["ClosureCapture_LiteralTypeReassign_NoTransitionError"] = struct {
		input          string
		expectedErrors []string
	}{
		input: `
			fn test() {
				var label: "on" | "off" = "on"
				val label2 = label
				val f = fn() { label = "off" }
				f()
				label2
			}
		`,
	}

	// Closure capture with shadowed variable names: the closure should
	// capture the outer variable (the one in scope at the closure definition),
	// not a same-named variable from a nested scope.
	tests["ClosureCapture_ShadowedByForIn_CapturesOuterVar_Error"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'items' to immutable 'f': 'mutRef' still has mutable access to 'items' after this point",
		},
	}
	// Reassignment with conditional RHS should not merge unrelated alias sets.
	// When `d = if cond { a } else { b }`, d aliases both a and b, but a and
	// b should NOT become aliases of each other.
	tests["Conditional_Reassignment_DoesNotMergeUnrelatedSets_OK"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors []string
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
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'a' to immutable 'b': 'a' is still used mutably after this point",
		},
	}
	// Conditional reassignment: `var c = if cond { a } else { b }` where
	// the transition is violated for one branch.
	tests["Conditional_Reassignment_Error"] = struct {
		input          string
		expectedErrors []string
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
		expectedErrors: []string{
			"cannot assign 'a' to immutable 'c': 'a' is still used mutably after this point",
		},
	}

	// Reassignment with a fresh value after aliasing should clear the alias
	// and allow transitions that were previously blocked.
	tests["Reassignment_FreshAfterAlias_ClearsConflict_OK"] = struct {
		input          string
		expectedErrors []string
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

	// Match expression with enum destructuring: each case destructures the
	// variant's params and uses the extracted value, but returns a tracked
	// variable. The result conditionally aliases the returned variables.
	// (Match pattern bindings are not yet alias-tracked, so the alias chain
	// goes through the returned variable, not the destructured binding.)
	tests["Match_EnumDestructuring_ConditionalAlias_Error"] = struct {
		input          string
		expectedErrors []string
	}{
		input: `
			enum Maybe {
				Left(value: number),
				Right(value: number),
			}
			fn test() {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				declare val maybe: Maybe
				val c: {x: number} = match maybe {
					Maybe.Left(v) => {
						a.x = v
						a
					},
					Maybe.Right(v) => {
						b.x = v
						b
					},
				}
				a.x = 5
				c
			}
		`,
		expectedErrors: []string{
			"cannot assign 'a' to immutable 'c': 'a' is still used mutably after this point",
		},
	}
	tests["Match_EnumDestructuring_ConditionalAlias_OK"] = struct {
		input          string
		expectedErrors []string
	}{
		input: `
			enum Maybe {
				Left(value: number),
				Right(value: number),
			}
			fn test() {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				declare val maybe: Maybe
				a.x = 5
				val c: {x: number} = match maybe {
					Maybe.Left(v) => {
						a.x = v
						a
					},
					Maybe.Right(v) => {
						b.x = v
						b
					},
				}
				c
			}
		`,
	}

	// Conditional aliasing where source (c) is in two alias sets.
	// c is mut and live after the transition, so it appears in conflicting —
	// but it must appear only once, not once per alias set.
	tests["Conditional_SourceInMultipleSets_NoDuplicateConflicting"] = struct {
		input          string
		expectedErrors []string
	}{
		input: `
			fn test(cond: boolean) {
				val a: mut {x: number} = {x: 0}
				val b: mut {x: number} = {x: 1}
				val c: mut {x: number} = if cond { a } else { b }
				val frozen: {x: number} = c
				c.x = 5
				frozen
			}
		`,
		expectedErrors: []string{
			"cannot assign 'c' to immutable 'frozen': 'c' is still used mutably after this point",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runTransitionTest(t, test.input, test.expectedErrors)
		})
	}
}

func runTransitionTest(t *testing.T, input string, expectedErrors []string) {
	t.Helper()
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
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

	if len(expectedErrors) == 0 {
		assert.Empty(t, mutErrors, "Expected no MutabilityTransitionError")
	} else {
		require.Len(t, mutErrors, len(expectedErrors), "Wrong number of MutabilityTransitionErrors")

		// Sort both slices for order-independent comparison.
		actual := make([]string, len(mutErrors))
		for i, e := range mutErrors {
			actual[i] = formatTransitionError(e)
		}
		slices.SortFunc(actual, cmp.Compare)

		expected := make([]string, len(expectedErrors))
		copy(expected, expectedErrors)
		slices.SortFunc(expected, cmp.Compare)

		for i := range expected {
			assert.Equal(t, expected[i], actual[i])
		}
	}
}

func TestExtractorPatAliasTracking(t *testing.T) {
	t.Run("ExtractorPat_MutToImmutable_Error", func(t *testing.T) {
		t.Parallel()
		runTransitionTest(t, `
			enum Wrapper<T> {
				Wrap(value: T),
			}
			fn test() {
				val w: mut Wrapper<mut {x: number}> = Wrapper.Wrap({x: 0})
				val Wrapper.Wrap(item) = w
				val frozen: Wrapper<{x: number}> = w
				item.x = 5
				frozen
			}
		`, []string{
			"cannot assign 'w' to immutable 'frozen': 'item' still has mutable access to 'w' after this point",
		})
	})
}

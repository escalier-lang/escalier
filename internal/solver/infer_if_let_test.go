package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6 PR7: if-let / let-else refutable narrowing ---

// A type-annotated `if let` narrows a union to one member: the consequent binds the
// name at the annotated member type, so the body that reads it back is typed at that
// member. `if let x: number = u` over `u: number | string` binds x at number.
func TestInferIfLetNarrowsUnionMember(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			return if let x: number = u { x } else { 0 }
		}
	`)
	require.Empty(t, errs)
	// The consequent binds x at number and the alternate yields 0, joining to
	// `number | 0`; subsumption at finalization drops the literal 0 into number.
	require.Equal(t, "fn (u: number | string) -> number", values["f"])
}

// The alternate sees the scrutinee at its full type — narrowing lives on the
// consequent's fresh binding, not on the scrutinee — so reading `u` in the else
// yields the whole union.
func TestInferIfLetAlternateSeesScrutineeUnchanged(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			return if let x: number = u { 0 } else { u }
		}
	`)
	require.Empty(t, errs)
	// The alternate reads the whole union `number | string` and the consequent
	// yields 0; subsumption drops the literal 0 into number, leaving the union.
	require.Equal(t, "fn (u: number | string) -> number | string", values["f"])
}

// A bare identifier pattern carries no narrowing annotation, so it binds the whole
// scrutinee. The consequent reads the full union back.
func TestInferIfLetBareIdentBindsWholeScrutinee(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			return if let x = u { x } else { 0 }
		}
	`)
	require.Empty(t, errs)
	// The consequent reads the whole union and the alternate yields 0, which
	// subsumption drops into number, leaving `number | string`.
	require.Equal(t, "fn (u: number | string) -> number | string", values["f"])
}

// A narrowing annotation that is no member of the union is rejected — the
// union-super exists rule finds no matching branch.
func TestInferIfLetNarrowRejectsNonMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(u: number | string) {
			return if let x: boolean = u { x } else { 0 }
		}
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "3:21-3:28: cannot constrain boolean <: number | string", msgWithSpan(errs[0]))
}

// An `if let` without an else contributes Void on the non-matching path, so the
// result joins the consequent with Void.
func TestInferIfLetNoElse(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			return if let x: number = u { x }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (u: number | string) -> number | void", values["f"])
}

// A `val pat = init else { … }` binding narrows the union and binds the name for the
// rest of the block. The else diverges with a `return`, so the body past it reads x
// at the narrowed member type.
func TestInferLetElseNarrowsAndBinds(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			val x: number = u else { return "no" }
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (u: number | string) -> number | "no"`, values["f"])
}

// The else of a let-else must diverge. A block that falls through to a value leaves
// the pattern's bindings unmatched on the continuing path, so it is rejected.
func TestInferLetElseNonDivergingElseRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(u: number | string) {
			val x: number = u else { 0 }
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &LetElseMustDivergeError{}, errs[0])
	require.Equal(t, "3:4-3:32: the `else` of a `let`-`else` binding must diverge; end it with `return` or `throw`", msgWithSpan(errs[0]))
}

// The else block runs in the enclosing scope, so it can read outer bindings. Here it
// returns the `fallback` param, which joins with the matched path's `x` into the
// function's return type.
func TestInferLetElseElseReadsOuterBinding(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string, fallback: number) {
			val x: number = u else { return fallback }
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (u: number | string, fallback: number) -> number", values["f"])
}

// A structural let-else pattern binds its leaves for the rest of the block. The
// scrutinee is an exact object, so the leaves bind at the field types.
func TestInferLetElseStructuralPattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: {x: number, y: string}) {
			val {x, y} = u else { return [0, ""] }
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	// The matched path yields [number, string] and the else yields [0, ""];
	// subsumption drops the all-literal tuple into the wider [number, string].
	require.Equal(t, `fn (u: {x: number, y: string}) -> [number, string]`, values["f"])
}

// An `if let` narrows a PR6 read-until-narrowed borrow union to one mutable branch,
// so a write through the fresh binding is allowed while the original union stays
// read-only. The narrowing annotation `mut {x: number}` picks the `{x: number}`
// branch via the union-super exists rule, and `r2.x = 5` checks against it. The
// function returns `r`, showing the scrutinee keeps its full union type.
func TestInferIfLetNarrowsBorrowUnionForWrite(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: &mut {x: number}, q: &mut {x: string}) {
			val r = if true { p } else { q }
			if let r2: mut {x: number} = r {
				r2.x = 5
			}
			return r
		}
	`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: string}) -> &'a mut {x: number} | &'b mut {x: string}",
		values["f"])
}

// A conflicting write through the narrowed binding is still type-checked: `r2` binds
// at `mut {x: number}`, so assigning a string to `r2.x` is rejected.
func TestInferIfLetNarrowedWriteChecked(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: &mut {x: number}, q: &mut {x: string}) {
			val r = if true { p } else { q }
			if let r2: mut {x: number} = r {
				r2.x = "hi"
			}
		}
	`)
	require.Equal(t, []string{
		"5:5-5:16: cannot constrain number <: string",
		"5:5-5:16: cannot constrain string <: number",
	}, messagesWithSpan(errs))
}

// A union narrowing annotation picks the matching sub-union: `if let x: number |
// string = u` over `u: number | string | boolean` binds x at `number | string`.
func TestInferIfLetNarrowsToSubUnion(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string | boolean) {
			return if let x: number | string = u { x } else { 0 }
		}
	`)
	require.Empty(t, errs)
	// The consequent binds x at the sub-union `number | string` and the alternate
	// yields 0, which subsumption drops into number.
	require.Equal(t, "fn (u: number | string | boolean) -> number | string", values["f"])
}

// A `let`-`else` binding is a body-level form. At module top level there is no block
// continuation for its bindings and no diverging path, so it is rejected rather than
// silently dropping the else.
func TestInferModuleLetElseRejected(t *testing.T) {
	_, _, errs := inferSource(t, `val x: number = u else { 0 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:1-1:29: Unsupported: `let`-`else` binding at module top level", msgWithSpan(errs[0]))
}

// The scrutinee keeps its union type after an `if let`: narrowing introduced a fresh
// binding and never re-typed the scrutinee.
func TestInferIfLetLeavesScrutineeType(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(u: number | string) {
			val r = if let x: number = u { x } else { 0 }
			return u
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (u: number | string) -> number | string", values["f"])
}

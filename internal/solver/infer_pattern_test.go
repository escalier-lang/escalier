package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 E1: structural destructuring patterns ---

// An object pattern in a `val` binds each named field at its field type. The
// function below reads the bound names back, so the inferred return shows the
// binding worked.
func TestInferValObjectPatternBinds(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			val {x, y} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> [number, string]", values["f"])
}

// An object pattern may bind a SUBSET of the scrutinee's fields: the per-field
// requirement is inexact ("has at least this field"), so the unmentioned `y` is
// tolerated.
func TestInferValObjectPatternPartial(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			val {x} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> number", values["f"])
}

// Destructuring a field the scrutinee lacks is a MissingPropertyError, blamed at
// the pattern field.
func TestInferValObjectPatternMissingField(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {z} = p
			return z
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: z", errs[0].Message())
}

// A tuple pattern binds per slot at the slot's element type. Reordering the bound
// names in the result confirms each slot bound the right element.
func TestInferValTuplePatternBinds(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn h(t: [number, string]) {
			val [a, b] = t
			return [b, a]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (t: [number, string]) -> [string, number]", values["h"])
}

// A tuple pattern is exact in arity: binding more (or fewer) slots than the
// scrutinee has is a TupleLengthMismatchError.
func TestInferValTuplePatternWrongArity(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn h(t: [number, string]) {
			val [a, b, c] = t
			return a
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain tuple of length 2 <: tuple of length 3", errs[0].Message())
}

// A destructuring parameter types like a `val` destructuring of the argument: the
// leaves bind, and the parameter renders its pattern.
func TestInferObjectPatternParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g({x, y}: {x: number, y: string}) {
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn ({x, y}: {x: number, y: string}) -> [number, string]", values["g"])
}

// An UN-annotated destructuring parameter infers its shape from the leaves' uses
// (usage-based inference), closing the coalesced object to exact (Policy A).
func TestInferObjectPatternParamInferred(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g({a, b}) {
			return [a, b]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0, T1>({a, b}: {a: T0, b: T1}) -> [T0, T1]", values["g"])
}

// Patterns nest: an object pattern whose field is itself an object pattern binds
// the inner leaves at the nested field types.
func TestInferNestedObjectPattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {pt: {x: number, y: string}}) {
			val {pt: {x, y}} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {pt: {x: number, y: string}}) -> [number, string]", values["f"])
}

// A wildcard slot in a tuple pattern matches without binding a name.
func TestInferTuplePatternWildcard(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(t: [number, string]) {
			val [a, _] = t
			return a
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (t: [number, string]) -> number", values["f"])
}

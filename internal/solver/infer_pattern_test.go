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

// A leaf type annotation is enforced: annotating a field whose scrutinee type
// conflicts is a constraint error, not a silently dropped annotation.
func TestInferObjectPatternLeafTypeAnnConflict(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: string} = p
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain number <: string", errs[0].Message())
}

// A matching leaf type annotation checks and is adopted as the leaf's type.
func TestInferObjectPatternLeafTypeAnnAdopted(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: number} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// A field default makes the field optional, so destructuring an absent field
// that carries a default binds the default's type instead of reporting a missing
// property.
func TestInferObjectPatternLeafDefault(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {z = 0} = p
			return z
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> 0", values["f"])
}

// A trailing rest element relaxes the tuple requirement to an inexact prefix, so
// the fixed slots bind without a spurious arity error. The rest element itself is
// reported unsupported, since typed rest tuples are M9.
func TestInferTuplePatternRestPrefix(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(t: [number, string, boolean]) {
			val [a, ...rest] = t
			return a
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported: RestPat", errs[0].Message())
	require.Equal(t, "fn (t: [number, string, boolean]) -> number", values["f"])
}

// A closure capturing a destructured leaf resolves the leaf's binding. This
// exercises the liveness wiring: the leaf's rename-assigned VarID is copied onto
// its binding, so trackCapturedAliases finds it instead of skipping a VarID-0
// binding.
func TestInferDestructuredLeafClosureCapture(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x} = p
			val g = fn () { return x }
			return g()
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// A `var` tuple destructuring widens each leaf to its primitive, the B3 widening
// applied through the initializer.
func TestInferVarTupleDestructureWidens(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() {
			var [a, b] = [1, 2]
			return [a, b]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> [number, number]", values["f"])
}

// A `var` object destructuring widens its leaf the same way.
func TestInferVarObjectDestructureWidens(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() {
			var {x} = {x: 5}
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> number", values["f"])
}

// Destructuring a `mut` borrow scrutinee peels the borrow via CarrierOf and binds
// the borrowed field values, just as a member read through the borrow would.
func TestInferDestructureBorrowScrutinee(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: mut {x: number, y: string}) {
			val {x, y} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number, y: string}) -> [number, string]", values["f"])
}

// A default is checked against an explicit leaf annotation: a default that the
// annotation rejects is a constraint error.
func TestInferObjectPatternLeafDefaultViolatesAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: number = "hi"} = p
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// Patterns nest across kinds: an object pattern whose field is a tuple pattern
// binds the inner slots at the nested element types.
func TestInferObjectContainingTuplePattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(o: {pt: [number, string]}) {
			val {pt: [a, b]} = o
			return [b, a]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (o: {pt: [number, string]}) -> [string, number]", values["f"])
}

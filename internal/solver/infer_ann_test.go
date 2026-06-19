package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M4 A3: object / tuple / mut type annotations + the construction-site
// excess-member check (exact-types §§2.2.4, 3.2.4) ---

// An object annotation resolves to an ObjectType and an annotated binding adopts
// it, so the rendered binding type is the annotation, trailing `...` and all.
func TestInferObjectAnnotationAdopted(t *testing.T) {
	values, _, errs := inferSource(t, `val r: {x: number, y: number} = {x: 1, y: 2}`)
	require.Empty(t, errs)
	require.Equal(t, "{x: number, y: number}", values["r"])
}

// An inexact object annotation renders its `...` tail and accepts a literal whose
// fields are all declared — the tail simply goes unused.
func TestInferInexactObjectAnnotationDeclaredFields(t *testing.T) {
	values, _, errs := inferSource(t, `val r: {x: number, y: number, ...} = {x: 1, y: 2}`)
	require.Empty(t, errs)
	require.Equal(t, "{x: number, y: number, ...}", values["r"])
}

// A literal carrying a field the inexact target does not declare is rejected — the
// construction-site excess check fires even though the target is inexact (parallel
// to the direct-call extra-arg rejection, exact-types §2.2.4).
func TestInferInexactObjectAnnotationRejectsExcessLiteralField(t *testing.T) {
	_, _, errs := inferSource(t, `val r: {x: number, ...} = {x: 1, y: 2}`)
	require.Len(t, errs, 1)
	require.IsType(t, &ExtraPropertyError{}, errs[0])
	require.Equal(t, "object has extra property: y", errs[0].Message())
}

// The variable escape hatch: a NON-literal source against an inexact target takes
// ordinary width subtyping, so the extra field is dropped instead of rejected.
func TestInferInexactObjectAnnotationVariableWidthSubtyping(t *testing.T) {
	_, _, errs := inferSource(t, `
		val v = {x: 1, y: 2}
		val r: {x: number, ...} = v
	`)
	require.Empty(t, errs)
}

// An EXACT object annotation rejects an extra field through the ordinary object
// constrain arm (one ExtraPropertyError, not doubled by the excess check, which
// only fires for an inexact target).
func TestInferExactObjectAnnotationRejectsExtraField(t *testing.T) {
	_, _, errs := inferSource(t, `val r: {x: number} = {x: 1, y: 2}`)
	require.Len(t, errs, 1)
	require.IsType(t, &ExtraPropertyError{}, errs[0])
	require.Equal(t, "object has extra property: y", errs[0].Message())
}

// A tuple annotation resolves to a TupleType and an annotated binding adopts it.
func TestInferTupleAnnotationAdopted(t *testing.T) {
	values, _, errs := inferSource(t, `val t: [number, string] = [1, "a"]`)
	require.Empty(t, errs)
	require.Equal(t, "[number, string]", values["t"])
}

// An inexact tuple annotation renders its `...` tail and rejects excess elements on
// a literal source — one ExtraElementError per excess element.
func TestInferInexactTupleAnnotationRejectsExcessLiteralElements(t *testing.T) {
	values, _, errs := inferSource(t, `val t: [number, ...] = [1, 2, 3]`)
	require.Len(t, errs, 2)
	require.IsType(t, &ExtraElementError{}, errs[0])
	require.Equal(t, "tuple has extra element at index 1", errs[0].Message())
	require.Equal(t, "tuple has extra element at index 2", errs[1].Message())
	// The binding still adopts the inexact annotation (error recovery).
	require.Equal(t, "[number, ...]", values["t"])
}

// The tuple variable escape hatch: a non-literal source against an inexact tuple
// target takes width subtyping (longer <: shorter through the inexact tail).
func TestInferInexactTupleAnnotationVariableWidthSubtyping(t *testing.T) {
	_, _, errs := inferSource(t, `
		val v = [1, 2, 3]
		val t: [number, ...] = v
	`)
	require.Empty(t, errs)
}

// The construction-site excess check looks THROUGH a `mut` borrow: an inexact
// object/tuple wrapped in `mut` still rejects undeclared literal members, so the rule
// is consistent whether or not the annotation is a borrow. The freshly constructed
// literal is given the owned-mutable type without a mutability mismatch, since a fresh
// value is uniquely owned, so the excess-member diagnostic is the only error.
func TestInferMutInexactAnnotationStillChecksExcess(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		_, _, errs := inferSource(t, `val r: mut {x: number, ...} = {x: 1, y: 2}`)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message()
		}
		require.Equal(t, []string{"object has extra property: y"}, msgs)
	})
	t.Run("tuple", func(t *testing.T) {
		_, _, errs := inferSource(t, `val t: mut [number, ...] = [1, 2, 3]`)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message()
		}
		require.ElementsMatch(t, []string{
			"tuple has extra element at index 1",
			"tuple has extra element at index 2",
		}, msgs)
	})
}

// A freshly constructed literal is uniquely owned, so it may be given an owned-mutable
// annotation: `val items: mut {x} = {x: 1}` type-checks and the binding is mutable.
// The upgrade recurses through nested literals and tuples.
func TestInferOwnedMutFromFreshLiteral(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val items: mut {x: number} = {x: 1}`)
		require.Empty(t, errs)
		require.Equal(t, "mut {x: number}", values["items"])
	})
	t.Run("nested object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val w: mut {p: {x: number}} = {p: {x: 0}}`)
		require.Empty(t, errs)
		require.Equal(t, "mut {p: {x: number}}", values["w"])
	})
	t.Run("tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, `val t: mut [number, number] = [1, 2]`)
		require.Empty(t, errs)
		require.Equal(t, "mut [number, number]", values["t"])
	})
}

// The upgrade applies only to a freshly constructed value. A non-fresh source — here a
// variable — still rejects an immutable→mutable assignment, because the variable could
// alias a value held immutably elsewhere. That case waits on the lifetime/region work.
func TestInferOwnedMutFromVariableRejected(t *testing.T) {
	src := "fn f() {\n\tval cfg: {x: number} = {x: 1}\n\tval m: mut {x: number} = cfg\n\tm.x = 2\n}"
	_, _, errs := inferSource(t, src)
	require.Equal(t, "cannot constrain immutable object <: mutable object", errs[0].Message())
}

// A `mut T` annotation lowers to a borrow (RefType{Mut: true}); a function
// parameter typed `mut {x: number}` originates a fresh borrow lifetime (D2), and a
// member read through it peels the borrow to resolve the inner property. The lifetime
// is unused in the result, since the body returns a number, not the borrow, so D4's
// display-time elision drops it and the param renders as plain owned-mutable `mut {…}`.
func TestInferMutObjectAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) -> number { return p.x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> number", values["f"])
}

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

// A numeric key resolves in an object annotation just as it does in a literal,
// since both go through objKeyName. {0: number} names the field "0", which a {0: 5}
// literal satisfies.
func TestInferObjectAnnotationNumericKey(t *testing.T) {
	values, _, errs := inferSource(t, `val o: {0: number} = {0: 5}`)
	require.Empty(t, errs)
	require.Equal(t, `{"0": number}`, values["o"])
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
	require.Equal(t, "1:37-1:38: object has extra property: y", msgWithSpan(errs[0]))
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
	require.Equal(t, "1:32-1:33: object has extra property: y", msgWithSpan(errs[0]))
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
	require.Equal(t, "1:28-1:29: tuple has extra element at index 1", msgWithSpan(errs[0]))
	require.Equal(t, "1:31-1:32: tuple has extra element at index 2", msgWithSpan(errs[1]))
	// The binding still adopts the inexact annotation (error recovery).
	require.Equal(t, "[number, ...]", values["t"])
}

// The excess check counts and blames by the INFERRED tuple's index, so a literal
// with a spread reports each spliced excess element with precise per-element blame.
// Before the fix the loop indexed the AST tuple and the inferred tuple by the same
// counter, so a spread mis-blamed and under-reported the excess.
func TestInferInexactTupleAnnotationExcessThroughSpread(t *testing.T) {
	src := `val t: [number, ...] = [...[5, 6], 7]`
	values, _, errs := inferSource(t, src)
	// Inferred [5, 6, 7] against [number]: indices 1 and 2 are excess, so two errors.
	require.Len(t, errs, 2)
	require.IsType(t, &ExtraElementError{}, errs[0])
	require.Equal(t, "1:32-1:33: tuple has extra element at index 1", msgWithSpan(errs[0]))
	require.Equal(t, "1:36-1:37: tuple has extra element at index 2", msgWithSpan(errs[1]))
	// Per-element blame resolves through prov to each spliced element's own node:
	// index 1 is the spread's `6`, index 2 is the trailing `7`.
	require.Equal(t, "6", spanText(src, errs[0].Span()))
	require.Equal(t, "7", spanText(src, errs[1].Span()))
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
			msgs[i] = msgWithSpan(e)
		}
		require.Equal(t, []string{"1:41-1:42: object has extra property: y"}, msgs)
	})
	t.Run("tuple", func(t *testing.T) {
		_, _, errs := inferSource(t, `val t: mut [number, ...] = [1, 2, 3]`)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = msgWithSpan(e)
		}
		require.ElementsMatch(t, []string{
			"1:32-1:33: tuple has extra element at index 1",
			"1:35-1:36: tuple has extra element at index 2",
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
		// Deep `mut` makes the upgrade reach every nested literal.
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
	require.Equal(t, "3:13-3:14: cannot constrain immutable object <: mutable object", msgWithSpan(errs[0]))
}

// The freshness check recurses, so a literal that WRAPS a non-fresh element is itself not
// freshly constructed and gets no upgrade. This is the recursive false path of
// isFreshlyConstructed, distinct from TestInferOwnedMutFromVariableRejected's top-level
// variable: the outer literal is fresh, but a field or element reads a variable.
func TestInferOwnedMutFreshnessRecurses(t *testing.T) {
	t.Run("object field is a variable", func(t *testing.T) {
		src := "fn f() {\n\tval cfg = {x: 1}\n\tval m: mut {p: {x: number}} = {p: cfg}\n\tm\n}"
		_, _, errs := inferSource(t, src)
		require.Len(t, errs, 1)
		require.Equal(t, "3:13-3:14: cannot constrain immutable object <: mutable object", msgWithSpan(errs[0]))
	})
	t.Run("tuple element is a variable", func(t *testing.T) {
		src := "fn f() {\n\tval cfg = {x: 1}\n\tval t: mut [number, {x: number}] = [1, cfg]\n\tt\n}"
		_, _, errs := inferSource(t, src)
		require.Len(t, errs, 1)
		require.Equal(t, "3:13-3:14: cannot constrain immutable tuple <: mutable tuple", msgWithSpan(errs[0]))
	})
}

// A fully fresh literal is uniquely owned at every level, so it upgrades to a
// nested `mut` target the same way it does to a top-level one.
func TestInferOwnedMutNestedMutFieldUpgraded(t *testing.T) {
	values, _, errs := inferSource(t, `val w: mut {p: {x: number}} = {p: {x: 0}}`)
	require.Empty(t, errs)
	require.Equal(t, "mut {p: {x: number}}", values["w"])
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

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

// A non-fresh source that is a consuming MOVE of a uniquely-owned place upgrades into an
// owned-mutable annotation. `val m: mut {x} = cfg` with cfg dead afterward grants m the
// mutable type, so the write `m.x = 2` succeeds. The move consumes cfg, so the same
// source with cfg still read afterward is a use-after-move rather than an upgrade error.
func TestInferOwnedMutFromMovedVariable(t *testing.T) {
	t.Run("dead source upgrades", func(t *testing.T) {
		src := `fn f() {
	val cfg: {x: number} = {x: 1}
	val m: mut {x: number} = cfg
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	val cfg: {x: number} = {x: 1}
	val m: mut {x: number} = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
	})
}

// The upgrade recurses through a literal that wraps a moved place: the outer literal is
// fresh and the wrapped `cfg` is a consuming move of an owned variable, so the whole
// value is uniquely owned and upgrades. A live read of `cfg` afterward is a
// use-after-move, since building the literal consumed it.
func TestInferOwnedMutFromMovedVariableNested(t *testing.T) {
	t.Run("object field is a moved variable", func(t *testing.T) {
		// The write through m.p proves the upgrade reaches the nested field: m.p is
		// mutable, so storing a new number into m.p.x type-checks.
		src := `fn f() {
	val cfg = {x: 1}
	val m: mut {p: {x: number}} = {p: cfg}
	m.p.x = 9
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("tuple element is a moved variable", func(t *testing.T) {
		src := `fn f() {
	val cfg = {x: 1}
	val t: mut [number, {x: number}] = [1, cfg]
	t
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live nested source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	val cfg = {x: 1}
	val m: mut {p: {x: number}} = {p: cfg}
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
	})
}

// The immutable→mutable upgrade is consulted at every value-flow site into an
// owned-mutable target through the shared tryUpgradeIntoMutSlot helper, not only the
// declaration initializer. A reassignment into a `mut` var, an argument against a `mut`
// parameter, and a `mut` return annotation each accept a fresh literal and a moved
// owned variable, and each move consumes the source.

// A reassignment into a `mut` var grants the upgrade: both a fresh literal and a moved
// owned variable satisfy the var's mutable type. The moved source is consumed, so a read
// of it after the reassignment is a use-after-move.
func TestInferOwnedMutReassign(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn f() {
	var m: mut {x: number} = {x: 0}
	m = {x: 1}
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f() {
	var m: mut {x: number} = {x: 0}
	val cfg: {x: number} = {x: 1}
	m = cfg
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	var m: mut {x: number} = {x: 0}
	val cfg: {x: number} = {x: 1}
	m = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
	})
}

// An argument against a `mut` parameter grants the upgrade, so `f({x: 1})` and `f(cfg)`
// type-check for an owned-mutable parameter. Passing the moved variable consumes it, so a
// later read is a use-after-move.
func TestInferOwnedMutCallArgument(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn sink(q: mut {x: number}) {}
fn f() { sink({x: 1}) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn sink(q: mut {x: number}) {}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink(cfg)
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live argument is a use-after-move", func(t *testing.T) {
		src := `fn sink(q: mut {x: number}) {}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink(cfg)
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
	})
	t.Run("already-mutable argument keeps strict invariance", func(t *testing.T) {
		// An owned-mutable argument is not upgraded; it flows through the strict mut<:mut
		// constraint, which pins the nested field invariant, so a narrower field is
		// rejected rather than silently accepted through the upgrade's covariant view.
		src := `fn sink(q: mut {a: {x: number | string}}) {}
fn f(p: mut {a: {x: number}}) { sink(p) }`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"2:33-2:40: cannot constrain string <: number"}, messagesWithSpan(errs))
	})
}

// A `mut` return annotation grants the upgrade for a uniquely-owned body value, so a
// function returning a fresh literal or a moved owned variable is typed as returning the
// owned-mutable value.
func TestInferOwnedMutReturn(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() -> mut {x: number} { return {x: 1} }`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> mut {x: number}", values["f"])
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f() -> mut {x: number} {
	val cfg: {x: number} = {x: 1}
	return cfg
}`
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> mut {x: number}", values["f"])
	})
}

// A field write into a mutable container's field is the fifth value-flow site. A fresh
// literal and a moved owned variable both store into the field, the move consumes its
// source, and an immutable receiver is rejected. The container's field is bare under the
// lazy deep-mut form, so the shared helper is consulted and declines, leaving the ordinary
// path to accept the owned value. The helper's owned-mutable-field upgrade branch itself
// needs a field whose type is owned-mutable, which #779 makes unconstructible from source
// today, so no case here drives that branch.
func TestInferOwnedMutFieldWrite(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn f(obj: mut {a: {x: number}}) {
	obj.a = {x: 5}
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f(obj: mut {a: {x: number}}) {
	val cfg: {x: number} = {x: 5}
	obj.a = cfg
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f(obj: mut {a: {x: number}}) {
	val cfg: {x: number} = {x: 5}
	obj.a = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
	})
	t.Run("immutable receiver rejected", func(t *testing.T) {
		src := `fn f(obj: {a: {x: number}}) {
	obj.a = {x: 5}
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"2:2-2:16: cannot constrain immutable object <: mutable object"}, messagesWithSpan(errs))
	})
}

// A source carrying an already-owned-mutable cell is not upgraded, even when the cell is
// nested inside a fresh literal. The covariant read view the upgrade constrains against
// would widen that cell's element type, so the source falls through to the strict mut<:mut
// path, which rejects the immutable wrapper against the mutable target. This guards against
// covariantly widening `{p: inner}` with `inner: mut {x: number}` into a wider field.
func TestInferOwnedMutNestedOwnedMutRejected(t *testing.T) {
	t.Run("declaration", func(t *testing.T) {
		src := `fn f() {
	val inner: mut {x: number} = {x: 0}
	val m: mut {p: {x: number | string}} = {p: inner}
	m
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"3:13-3:14: cannot constrain immutable object <: mutable object"}, messagesWithSpan(errs))
	})
	t.Run("return", func(t *testing.T) {
		src := `fn f() -> mut {p: {x: number | string}} {
	val inner: mut {x: number} = {x: 0}
	return {p: inner}
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"1:15-1:16: cannot constrain immutable object <: mutable object"}, messagesWithSpan(errs))
	})
}

// A module-level `mut` global takes the upgrade on reassignment just like a local `mut`
// var, so a fresh literal and a moved owned variable both store into it. The store
// consumes a moved source, so a later read of it is a use-after-move.
func TestInferOwnedMutModuleGlobalWrite(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `var sink: mut {x: number} = {x: 0}
fn f() { sink = {x: 1} }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `var sink: mut {x: number} = {x: 0}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink = cfg
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `var sink: mut {x: number} = {x: 0}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(errs))
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

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
	require.Equal(t, "1:37-1:38: object has extra property: y", msgWithSpan(t, errs[0]))
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
	require.Equal(t, "1:32-1:33: object has extra property: y", msgWithSpan(t, errs[0]))
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
	require.Equal(t, "1:28-1:29: tuple has extra element at index 1", msgWithSpan(t, errs[0]))
	require.Equal(t, "1:31-1:32: tuple has extra element at index 2", msgWithSpan(t, errs[1]))
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
	require.Equal(t, "1:32-1:33: tuple has extra element at index 1", msgWithSpan(t, errs[0]))
	require.Equal(t, "1:36-1:37: tuple has extra element at index 2", msgWithSpan(t, errs[1]))
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
		_, _, errs := inferSource(t, `val mut r: {x: number, ...} = {x: 1, y: 2}`)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = msgWithSpan(t, e)
		}
		require.Equal(t, []string{"1:41-1:42: object has extra property: y"}, msgs)
	})
	t.Run("tuple", func(t *testing.T) {
		_, _, errs := inferSource(t, `val mut t: [number, ...] = [1, 2, 3]`)
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = msgWithSpan(t, e)
		}
		require.ElementsMatch(t, []string{
			"1:32-1:33: tuple has extra element at index 1",
			"1:35-1:36: tuple has extra element at index 2",
		}, msgs)
	})
}

// A freshly constructed literal moves into an annotated `val mut` binding:
// `val mut items: {x} = {x: 1}` type-checks and the binding is mutable. The binding holds the
// mutability, so its type is the annotation as written, through nested literals and tuples.
func TestInferOwnedMutFromFreshLiteral(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val mut items: {x: number} = {x: 1}`)
		require.Empty(t, errs)
		require.Equal(t, "{x: number}", values["items"])
	})
	t.Run("nested object", func(t *testing.T) {
		values, _, errs := inferSource(t, `val mut w: {p: {x: number}} = {p: {x: 0}}`)
		require.Empty(t, errs)
		require.Equal(t, "{p: {x: number}}", values["w"])
	})
	t.Run("tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, `val mut t: [number, number] = [1, 2]`)
		require.Empty(t, errs)
		require.Equal(t, "[number, number]", values["t"])
	})
}

// An owned place moves into an annotated `val mut` binding. `val mut m: {x} = cfg` consumes
// cfg and leaves m its only owner, so the write `m.x = 2` succeeds, and a read of cfg
// afterward is a use-after-move.
func TestInferOwnedMutFromMovedVariable(t *testing.T) {
	t.Run("dead source upgrades", func(t *testing.T) {
		src := `fn f() {
	val cfg: {x: number} = {x: 1}
	val mut m: {x: number} = cfg
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	val cfg: {x: number} = {x: 1}
	val mut m: {x: number} = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
}

// A literal that wraps an owned place moves the place into the literal, so the whole value
// moves into the `val mut` binding. A read of `cfg` afterward is a use-after-move, since
// building the literal consumed it.
func TestInferOwnedMutFromMovedVariableNested(t *testing.T) {
	t.Run("object field is a moved variable", func(t *testing.T) {
		// The write through m.p shows the binding's mutability reaches the nested field.
		src := `fn f() {
	val cfg = {x: 1}
	val mut m: {p: {x: number}} = {p: cfg}
	m.p.x = 9
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("tuple element is a moved variable", func(t *testing.T) {
		src := `fn f() {
	val cfg = {x: 1}
	val mut t: [number, {x: number}] = [1, cfg]
	t
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live nested source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	val cfg = {x: 1}
	val mut m: {p: {x: number}} = {p: cfg}
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
}

// Every value-flow site into a `mut` binding is an ordinary move. A reassignment into a
// `var mut`, an argument for a `mut` parameter, and a returned value bound with `val mut` each
// accept a fresh literal and a moved owned variable, and each move consumes the source.

// A reassignment into a `var mut` moves its source: both a fresh literal and a moved owned
// variable fill the var. The moved source is consumed, so a read of it after the
// reassignment is a use-after-move.
func TestInferOwnedMutReassign(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn f() {
	var mut m: {x: number} = {x: 0}
	m = {x: 1}
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f() {
	var mut m: {x: number} = {x: 0}
	val cfg: {x: number} = {x: 1}
	m = cfg
	m.x = 2
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f() {
	var mut m: {x: number} = {x: 0}
	val cfg: {x: number} = {x: 1}
	m = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
}

// An argument for a `mut` parameter moves in, so `f({x: 1})` and `f(cfg)` type-check. Passing
// the variable consumes it, so a later read is a use-after-move.
func TestInferOwnedMutCallArgument(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn sink(mut q: {x: number}) {}
fn f() { sink({x: 1}) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn sink(mut q: {x: number}) {}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink(cfg)
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live argument is a use-after-move", func(t *testing.T) {
		src := `fn sink(mut q: {x: number}) {}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink(cfg)
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
	t.Run("a narrower field moves into a wider parameter", func(t *testing.T) {
		// The caller gives the value up, so no view of it at the narrower field type
		// survives the call for the callee's wider write to break.
		src := `fn sink(mut q: {a: {x: number | string}}) { q.a.x = "s" }
fn f(mut p: {a: {x: number}}) { sink(p) }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// A returned value moves to the caller, so a fresh literal or a moved owned variable returned
// as `-> {x: number}` binds mutably at a `val mut` call site.
func TestInferOwnedMutReturn(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() -> {x: number} { return {x: 1} }
fn g() { val mut r = f()
	r.x = 2 }`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> {x: number}", values["f"])
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f() -> {x: number} {
	val cfg: {x: number} = {x: 1}
	return cfg
}
fn g() { val mut r = f()
	r.x = 2 }`
		values, _, errs := inferSource(t, src)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> {x: number}", values["f"])
	})
}

// A field write into a mutable container's field is another value-flow site. A fresh literal
// and a moved owned variable both store into the field, the move consumes its source, and an
// immutable receiver is rejected.
func TestInferOwnedMutFieldWrite(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `fn f(mut obj: {a: {x: number}}) {
	obj.a = {x: 5}
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `fn f(mut obj: {a: {x: number}}) {
	val cfg: {x: number} = {x: 5}
	obj.a = cfg
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `fn f(mut obj: {a: {x: number}}) {
	val cfg: {x: number} = {x: 5}
	obj.a = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
	t.Run("immutable receiver rejected", func(t *testing.T) {
		src := `fn f(obj: {a: {x: number}}) {
	obj.a = {x: 5}
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"2:2-2:16: cannot mutate 'obj' because its binding is immutable; declare it as `mut obj`"}, messagesWithSpan(t, errs))
	})
}

// A move into a `mut` binding leaves the binding the value's only owner because the move
// consumes its source, so a later use of the source is a use-after-move. Module top level
// consumes nothing, so a place moved into a `mut` binding there would stay readable while the
// binding writes it, and the move is rejected. A fresh literal carries no place, so it moves
// in at top level too.
func TestInferValMutMoveNeedsAConsume(t *testing.T) {
	t.Run("a fresh literal moves in at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, `val mut m: {x: number} = {x: 1}`)
		require.Empty(t, errs)
	})
	t.Run("a place does not move in at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, "val cfg = {x: 1}\nval mut m: {x: number} = cfg")
		require.Equal(t,
			[]string{"2:26-2:29: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			messagesWithSpan(t, errs))
	})
	t.Run("a place does not move into a destructured `mut` leaf at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, "val cfg = {a: {x: 1}}\nval {mut a} = cfg")
		require.Equal(t,
			[]string{"2:15-2:18: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			messagesWithSpan(t, errs))
	})
	t.Run("a field of a place does not move in at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, "val cfg = {a: {x: 1}}\nval mut m = cfg.a")
		require.Equal(t,
			[]string{"2:13-2:18: cannot move module-level value 'cfg.a' into a `mut` binding, since the module keeps it. Borrow it with `&cfg.a`"},
			messagesWithSpan(t, errs))
	})
	t.Run("an element of a place does not move in at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, "val cfg = [{x: 1}]\nval mut m = cfg[0]")
		require.Equal(t,
			[]string{"2:13-2:19: cannot move module-level value 'cfg[0]' into a `mut` binding, since the module keeps it. Borrow it with `&cfg[0]`"},
			messagesWithSpan(t, errs))
	})
	t.Run("a place does not move in through a literal at top level", func(t *testing.T) {
		tests := map[string]struct {
			src  string
			want []string
		}{
			"PropertyValue": {
				src:  "val cfg = {x: 1}\nval mut m = {p: cfg}",
				want: []string{"2:17-2:20: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			},
			"ShorthandProperty": {
				src:  "val cfg = {x: 1}\nval mut m = {cfg}",
				want: []string{"2:14-2:17: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			},
			"TupleElement": {
				src:  "val cfg = {x: 1}\nval mut m = [cfg]",
				want: []string{"2:14-2:17: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			},
			"NestedLiterals": {
				src:  "val cfg = {x: 1}\nval mut m = {a: [{b: cfg}]}",
				want: []string{"2:22-2:25: cannot move module-level value 'cfg' into a `mut` binding, since the module keeps it. Borrow it with `&cfg`"},
			},
			"PrimitivePropertyCopies": {
				src:  "val n = 5\nval mut m = {p: n}",
				want: nil,
			},
			"ImmutableBindingShares": {
				src:  "val cfg = {x: 1}\nval m = {p: cfg}",
				want: nil,
			},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				_, _, errs := inferSource(t, tc.src)
				require.Equal(t, tc.want, messagesWithSpan(t, errs))
			})
		}
	})
	t.Run("a primitive place copies in at top level", func(t *testing.T) {
		_, _, errs := inferSource(t, "val cfg = 5\nval mut m = cfg")
		require.Empty(t, errs)
	})
	t.Run("a class name is not a place", func(t *testing.T) {
		_, _, errs := inferSource(t, "class C { x: number }\nval mut m = C")
		require.Empty(t, errs)
	})
	t.Run("a closure in the initializer writes through the binding being declared", func(t *testing.T) {
		_, _, errs := inferSource(t, "val mut counter = {n: 0, bump: fn () { counter.n = 1 }}")
		require.Empty(t, errs)
	})
	t.Run("the same place moves in inside a function, and is consumed", func(t *testing.T) {
		const src = `fn f() {
	val cfg = {x: 1}
	val mut m: {x: number} = cfg
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)

		_, _, errs = inferSource(t, `fn f() {
	val cfg = {x: 1}
	val mut m: {x: number} = cfg
	cfg.x
}`)
		require.Equal(t, []string{"4:2-4:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
}

// A `mut` binding moved into a wider owned destination gives the value up, so nothing keeps
// a view of it at the narrower field type. Each destination accepts the move, and a later
// read of the source is a use-after-move.
func TestInferValMutMovesIntoAWiderDestination(t *testing.T) {
	t.Run("declaration", func(t *testing.T) {
		src := `fn f() {
	val mut inner: {x: number} = {x: 0}
	val mut m: {p: {x: number | string}} = {p: inner}
	m.p.x = "s"
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, messagesWithSpan(t, errs))
	})
	t.Run("return", func(t *testing.T) {
		src := `fn f() -> {p: {x: number | string}} {
	val mut inner: {x: number} = {x: 0}
	return {p: inner}
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, messagesWithSpan(t, errs))
	})
	t.Run("call argument", func(t *testing.T) {
		src := `declare fn take(a: {p: {x: number | string}}) -> number
fn f() -> number {
	val mut inner: {x: number} = {x: 0}
	val n = take({p: inner})
	return inner.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:9-5:16: use of moved value 'inner'"}, messagesWithSpan(t, errs))
	})
}

// A wider `mut` parameter takes a fresh literal, a moved variable, and a moved `mut` binding
// alike, since each argument moves in. The callee may write a string into the field, which
// is sound because the caller keeps no view of the value at the narrower type.
func TestInferOwnedMutArgumentIntoAWiderParam(t *testing.T) {
	const take = "fn take(mut a: {x: number | string}) -> number { a.x = \"s\"  return 0 }\n"
	t.Run("a fresh literal moves into the wider parameter", func(t *testing.T) {
		_, _, errs := inferSource(t, take+"val r = take({x: 1})")
		require.Empty(t, messagesWithSpan(t, errs))
	})
	t.Run("a moved variable does too, and is consumed", func(t *testing.T) {
		src := take + `fn g() -> number {
	val cfg = {x: 1}
	val n = take(cfg)
	return cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:9-5:14: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
	t.Run("a `val mut` binding moves in the same way", func(t *testing.T) {
		src := take + `fn g() -> number {
	val mut cfg: {x: number} = {x: 1}
	return take(cfg)
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// A reassignment into a module-level `var mut` moves its source just like a local one, so a
// fresh literal and a moved owned variable both store into it. The store
// consumes a moved source, so a later read of it is a use-after-move.
func TestInferOwnedMutModuleGlobalWrite(t *testing.T) {
	t.Run("fresh literal", func(t *testing.T) {
		src := `var mut sink: {x: number} = {x: 0}
fn f() { sink = {x: 1} }`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("moved variable", func(t *testing.T) {
		src := `var mut sink: {x: number} = {x: 0}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink = cfg
}`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})
	t.Run("live source is a use-after-move", func(t *testing.T) {
		src := `var mut sink: {x: number} = {x: 0}
fn f() {
	val cfg: {x: number} = {x: 1}
	sink = cfg
	cfg.x
}`
		_, _, errs := inferSource(t, src)
		require.Equal(t, []string{"5:2-5:7: use of moved value 'cfg'"}, messagesWithSpan(t, errs))
	})
}

// A fresh literal moves into a nested annotation the same way it does into a flat one.
func TestInferOwnedMutNestedMutFieldUpgraded(t *testing.T) {
	values, _, errs := inferSource(t, `val mut w: {p: {x: number}} = {p: {x: 0}}`)
	require.Empty(t, errs)
	require.Equal(t, "{p: {x: number}}", values["w"])
}

// A `mut` parameter binds `{x: number}` mutably in the body, and a member read through it
// resolves the inner property. The signature carries the plain type, so the
// parameter renders as `p: {x: number}`.
func TestInferMutObjectParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(mut p: {x: number}) -> number { return p.x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// `unknown` in annotation position resolves to the top of the lattice. Every type is a subtype of
// it through constrain's `_ <: unknown` rule, so an annotated binding accepts any initializer and
// renders as `unknown`. It is the counterpart of the `never` annotation at the other end of the
// lattice, and the bound that admits every type at a position whose value is never read.
func TestInferUnknownAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AcceptsPrimitive",
			src:  `val u: unknown = 5`,
			want: "unknown",
		},
		{
			name: "AcceptsObject",
			src:  `val u: unknown = {a: 1}`,
			want: "unknown",
		},
		{
			name: "AcceptsFunction",
			src:  `val u: unknown = fn () { return 1 }`,
			want: "unknown",
		},
		{
			// A nested position resolves the same way, so `unknown` composes rather than being
			// special-cased at the top of an annotation.
			name: "NestedInObject",
			src:  `val u: {a: unknown} = {a: "s"}`,
			want: "{a: unknown}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["u"])
		})
	}
}

// Two `unknown` annotations in one module each resolve without tripping the provenance table.
// soltype.UnknownType has no fields, so Go gives every instance one address, and Prov is keyed by
// pointer identity — recording against it would file both under a single entry and panic the
// debugProv guard on the second. The arm skips recording for that reason, the same as `never`.
func TestInferUnknownAnnotationTwiceInOneModule(t *testing.T) {
	values, _, errs := inferSource(t, "val u: unknown = 1\nval v: unknown = \"s\"")
	require.Empty(t, errs)
	require.Equal(t, "unknown", values["u"])
	require.Equal(t, "unknown", values["v"])
}

// `any` in annotation position resolves to `unknown`, the top of the lattice. A declaration
// writes `any` where it places no constraint on a type, and `unknown` carries that meaning
// without TypeScript's unsound assignment from `any` down to any other type.
func TestInferAnyAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AcceptsPrimitive",
			src:  `val u: any = 5`,
			want: "unknown",
		},
		{
			name: "AcceptsObject",
			src:  `val u: any = {a: 1}`,
			want: "unknown",
		},
		{
			// A nested position resolves the same way, so `any` composes rather than being
			// special-cased at the top of an annotation.
			name: "NestedInObject",
			src:  `val u: {a: any} = {a: "s"}`,
			want: "{a: unknown}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["u"])
		})
	}
}

// Two `any` annotations in one module each resolve without tripping the provenance table, for
// the reason TestInferUnknownAnnotationTwiceInOneModule gives: both resolve to the shared
// zero-size UnknownType singleton, so the arm records no provenance against it.
func TestInferAnyAnnotationTwiceInOneModule(t *testing.T) {
	values, _, errs := inferSource(t, "val u: any = 1\nval v: any = \"s\"")
	require.Empty(t, errs)
	require.Equal(t, "unknown", values["u"])
	require.Equal(t, "unknown", values["v"])
}

// A type parameter defaulted to `any` keeps its default, so a reference omitting the argument
// resolves rather than reporting an arity mismatch. Resolving the default is what makes the
// parameter optional: resolveTypeParams drops a default it cannot resolve, and requiredArgCount
// then counts the parameter as required.
func TestInferAnyTypeParamDefault(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Interface",
			src: `declare interface It<T, R = any> { a: T, b: R }
				fn f(p: It<number>) -> number { return p.a }`,
			want: "fn (p: It<number, unknown>) -> number",
		},
		{
			name: "Alias",
			src: `type It<T, R = any> = {a: T, b: R}
				fn f(p: It<number>) -> number { return p.a }`,
			want: "fn (p: It<number, unknown>) -> number",
		},
		{
			// The control the `any` cases are compared against. An ordinary default fills
			// the omitted argument the same way, so a difference between this case and the
			// others would mean `any` is still being treated as a special case.
			name: "OrdinaryDefault",
			src: `declare interface It<T, R = string> { a: T, b: R }
				fn f(p: It<number>) -> number { return p.a }`,
			want: "fn (p: It<number, string>) -> number",
		},
		{
			// The shape `std/iterator.esc` writes for `Iterable`, where two trailing
			// parameters both default to `any` and every `Iterable<T>` elsewhere in the tree
			// omits them.
			name: "TwoTrailingDefaults",
			src: `declare interface It<T, TReturn = any, TNext = any> { a: T, b: TReturn, c: TNext }
				fn f(p: It<number>) -> number { return p.a }`,
			want: "fn (p: It<number, unknown, unknown>) -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

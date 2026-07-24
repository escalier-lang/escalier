package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- TupleExpr ---

func TestInferTuple(t *testing.T) {
	c := newChecker()
	// [1, "hi"]
	e := tupleExpr(numExpr(1), strExpr("hi"))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, `[1, "hi"]`, render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

func TestInferTupleEmpty(t *testing.T) {
	c := newChecker()
	got := c.inferExpr(NewScope(), 0, tupleExpr())
	require.Empty(t, c.errs)
	require.Equal(t, "[]", render(got))
}

// A spread element ([...pair]) splices the operand tuple's element types into the
// literal: [...pair, 3] over pair: [number, string] builds [number, string, 3].
func TestInferTupleSpread(t *testing.T) {
	c := newChecker()
	// [...[1, "hi"], 3]
	pair := tupleExpr(numExpr(1), strExpr("hi"))
	e := tupleExpr(ast.NewArraySpread(pair, testSpan()), numExpr(3))
	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, `[1, "hi", 3]`, render(got))
}

// A spread in the middle of a literal keeps element order: the elements before
// the spread, then the splice, then the elements after. [1, ...[2, 3], 4] builds
// [1, 2, 3, 4].
func TestInferTupleSpreadMiddle(t *testing.T) {
	c := newChecker()
	// [1, ...[2, 3], 4]
	mid := tupleExpr(numExpr(2), numExpr(3))
	e := tupleExpr(numExpr(1), ast.NewArraySpread(mid, testSpan()), numExpr(4))
	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "[1, 2, 3, 4]", render(got))
}

// Spreading multiple exact tuples concatenates them into one exact tuple whose
// length is the sum of the operand lengths. The result stays exact, since every
// operand has a known length, so the assertion checks the Inexact flag and the
// element count directly rather than relying on the rendered form.
func TestInferTupleSpreadMultipleExact(t *testing.T) {
	c := newChecker()
	// [...[1, 2], ...[3, 4]]
	a := tupleExpr(numExpr(1), numExpr(2))
	b := tupleExpr(numExpr(3), numExpr(4))
	e := tupleExpr(ast.NewArraySpread(a, testSpan()), ast.NewArraySpread(b, testSpan()))
	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "[1, 2, 3, 4]", render(got))

	tup, ok := got.(*soltype.TupleType)
	require.True(t, ok)
	require.False(t, tup.Inexact) // every operand had a known length
	require.Len(t, tup.Elems, 4)  // 2 + 2 spliced elements
}

// Spreading a non-tuple value is a typed error: M4 splices concrete tuple
// literals only, so the operand must infer to a tuple.
func TestInferTupleSpreadNonTuple(t *testing.T) {
	c := newChecker()
	// [...5]
	e := tupleExpr(ast.NewArraySpread(numExpr(5), testSpan()))
	got := c.inferExpr(NewScope(), 0, e)
	require.Equal(t, "[]", render(got)) // the bad spread contributes no elements
	require.Len(t, c.errs, 1)
	require.Equal(t, "cannot spread 5 into a tuple", c.errs[0].Message())
}

// Spreading an inexact tuple before another element is rejected: the inexact
// operand ([number, ...]) has unknown length, so the trailing 1 would land at an
// unknown position.
func TestInferTupleSpreadInexactNotLast(t *testing.T) {
	// fn f(z: [number, ...]) { return [...z, 1] }
	_, _, errs := inferSource(t, `
		fn f(z: [number, ...]) { return [...z, 1] }
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "2:36-2:40: cannot spread an inexact tuple except as the last element", msgWithSpan(errs[0]))
}

// A trailing inexact spread is sound: the operand's known prefix extends the
// literal and its unknown tail becomes the literal's tail, so the result is itself
// an inexact tuple. [...z] over z: [number, ...] yields [number, ...], and a leading
// element keeps its place: [a, ...z] yields [typeof a, number, ...].
func TestInferTupleSpreadInexactLast(t *testing.T) {
	t.Run("spread alone", func(t *testing.T) {
		vals, _, errs := inferSource(t, `fn f(z: [number, ...]) { return [...z] }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (z: [number, ...]) -> [number, ...]", vals["f"])
	})
	t.Run("element before a trailing spread", func(t *testing.T) {
		vals, _, errs := inferSource(t, `fn f(z: [number, ...]) { return ["hi", ...z] }`)
		require.Empty(t, errs)
		require.Equal(t, `fn (z: [number, ...]) -> ["hi", number, ...]`, vals["f"])
	})
}

// A spread whose operand already errored is absorbed: walking the unbound `a`
// reports a single unknown-identifier error, and the spread does not layer a
// SpreadNotTupleError on the recovery sentinel.
func TestInferTupleSpreadErrorOperandAbsorbs(t *testing.T) {
	c := newChecker()
	// [...a]
	e := tupleExpr(ast.NewArraySpread(identExpr("a"), testSpan()))
	got := c.inferExpr(NewScope(), 0, e)
	require.Equal(t, "[]", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unknown identifier: a", c.errs[0].Message())
}

// --- ObjectExpr ---

func TestInferObject(t *testing.T) {
	c := newChecker()
	// {a: 5, b: "hi"}
	e := objExpr(prop("a", numExpr(5)), prop("b", strExpr("hi")))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, `{a: 5, b: "hi"}`, render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

func TestInferObjectEmpty(t *testing.T) {
	c := newChecker()
	got := c.inferExpr(NewScope(), 0, objExpr())
	require.Empty(t, c.errs)
	require.Equal(t, "{}", render(got))
}

// A string-literal key maps to a field name just like an identifier label.
func TestInferObjectStringKey(t *testing.T) {
	c := newChecker()
	// {"a": 5}
	strKey := ast.NewProperty(ast.NewString("a", testSpan()), false, false, numExpr(5), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(strKey))
	require.Empty(t, c.errs)
	require.Equal(t, "{a: 5}", render(got))
}

// A numeric key resolves to a static field name: JavaScript coerces it to its
// string form, so {0: 5} names the field "0". The field name is not a valid
// identifier, so it renders as a quoted key.
func TestInferObjectNumericKey(t *testing.T) {
	c := newChecker()
	// {0: 5}
	numKey := ast.NewProperty(ast.NewNumber(0, testSpan()), false, false, numExpr(5), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(numKey))
	require.Empty(t, c.errs)
	require.Equal(t, `{"0": 5}`, render(got))
}

// A numeric key and a string key that coerce to the same name are the same field
// under JavaScript semantics: {0: 1, "0": 2} is a single field "0". Last-wins
// dedup then collapses them to {"0": 2}, so the resolved name, not the syntactic
// key kind, decides identity.
func TestInferObjectNumericStringKeyCollision(t *testing.T) {
	c := newChecker()
	// {0: 1, "0": 2}
	numKey := ast.NewProperty(ast.NewNumber(0, testSpan()), false, false, numExpr(1), testSpan())
	strKey := ast.NewProperty(ast.NewString("0", testSpan()), false, false, numExpr(2), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(numKey, strKey))
	require.Empty(t, c.errs)
	require.Equal(t, `{"0": 2}`, render(got))

	obj, ok := got.(*soltype.ObjectType)
	require.True(t, ok)
	require.Len(t, obj.Elems, 1) // the two keys collapsed to one field
}

// Duplicate keys follow JS last-wins semantics: the later value replaces the
// earlier one while the field keeps its first position. This keeps field names
// unique, so the record is well-formed (and equalType stays reflexive on it).
func TestInferObjectDuplicateKeyLastWins(t *testing.T) {
	c := newChecker()
	// {a: 1, b: 2, a: "x"}  ⇒  {a: "x", b: 2}
	e := objExpr(prop("a", numExpr(1)), prop("b", numExpr(2)), prop("a", strExpr("x")))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, `{a: "x", b: 2}`, render(got))

	obj, ok := got.(*soltype.ObjectType)
	require.True(t, ok)
	require.Len(t, obj.Elems, 2) // the duplicate `a` was collapsed, not appended
	require.True(t, equalType(obj, obj), "equalType must be reflexive for a deduped object")
}

// Shorthand ({x}) is a property with no value — deferred to M4. It reports a
// clean UnsupportedNodeError and is skipped (the rest of the object still types).
func TestInferObjectShorthandUnsupported(t *testing.T) {
	c := newChecker()
	shorthand := ast.NewProperty(ast.NewIdent("x", testSpan()), false, false, nil, testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(shorthand))
	require.Equal(t, "{}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: PropertyExpr", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// A spread element ({...o}) splices the operand object's fields into the literal (M9 PR5). A
// concrete inline operand merges its fields; a later property on the same key overrides. An
// operand with no ground object shape, such as the primitive below, reports a SpreadNotObjectError.
func TestInferObjectSpreadMerges(t *testing.T) {
	c := newChecker()
	// {...{a: 1}, b: 2}
	spread := ast.NewRestSpread(objExpr(prop("a", numExpr(1))), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(spread, prop("b", numExpr(2))))
	require.Empty(t, c.errs)
	require.Equal(t, "{a: 1, b: 2}", render(got))
}

// Spreading a value with no ground object shape — a primitive here — cannot merge its fields, so
// it reports a SpreadNotObjectError and keeps the rest of the object.
func TestInferObjectSpreadNonObject(t *testing.T) {
	c := newChecker()
	spread := ast.NewRestSpread(numExpr(5), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(spread, prop("b", numExpr(2))))
	require.Equal(t, "{b: 2}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "cannot spread 5 into an object", c.errs[0].Message())
}

// A computed key ({[k]: v}) carries no static field name — M4.
func TestInferObjectComputedKeyUnsupported(t *testing.T) {
	c := newChecker()
	computed := ast.NewProperty(ast.NewComputedKey(identExpr("k")), false, false, numExpr(1), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(computed))
	require.Equal(t, "{}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: ComputedKey", c.errs[0].Message())
}

// --- MemberExpr ---

func TestInferMember(t *testing.T) {
	c := newChecker()
	// ({a: 5, b: "hi"}).a
	recv := objExpr(prop("a", numExpr(5)), prop("b", strExpr("hi")))
	e := memberExpr(recv, "a")

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "5", render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

// Reading a field the receiver lacks fails with a MissingPropertyError carrying
// the member node's span.
func TestInferMemberMissingProperty(t *testing.T) {
	c := newChecker()
	// ({a: 5}).b
	e := memberExpr(objExpr(prop("a", numExpr(5))), "b")

	got := c.inferExpr(NewScope(), 0, e)
	require.Equal(t, "never", render(got)) // the fresh result var picks up no lower bound
	require.Len(t, c.errs, 1)
	require.Equal(t, "object is missing property: b", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// Optional chaining (recv?.prop) needs union/undefined handling — M6.
func TestInferMemberOptionalChainUnsupported(t *testing.T) {
	c := newChecker()
	e := ast.NewMember(objExpr(prop("a", numExpr(5))), ast.NewIdentifier("a", testSpan()), true, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got) // PR8: report's recovery placeholder
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: OptionalChain", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// Optional chaining is reported up front WITHOUT descending into the receiver:
// an unbound receiver does not add a cascading "unknown identifier" error — the
// single OptionalChain diagnostic stands for the whole unsupported construct.
func TestInferMemberOptionalChainDoesNotDescendIntoReceiver(t *testing.T) {
	c := newChecker()
	// nope?.a — receiver `nope` is unbound and would error if typed.
	e := ast.NewMember(identExpr("nope"), ast.NewIdentifier("a", testSpan()), true, testSpan())

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: OptionalChain", c.errs[0].Message())
}

// A malformed `recv.` (no valid property name) leaves Prop.Name empty; the
// parser has already reported the missing identifier, so the walk must NOT layer
// a spurious "object is missing property: " on top. It yields the ErrorType
// recovery sentinel (PR8) — not a raw never — so that if the read flows into a
// sink (`if recv. {}`, `await recv.`, `var x = recv.`) the sentinel absorbs in
// constrain rather than cascading `never <: …`. It reports nothing itself.
func TestInferMemberEmptyPropertyNameIsSilent(t *testing.T) {
	c := newChecker()
	// recv = {a: 5}; access with an empty property name (as the parser builds for `recv.`)
	e := ast.NewMember(objExpr(prop("a", numExpr(5))), ast.NewIdentifier("", testSpan()), false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Empty(t, c.errs) // no spurious MissingPropertyError
	require.Same(t, got, c.info.TypeOf(e))
}

// A function reading a field of its param synthesizes an inexact "has at least this
// field" requirement during body inference (A1's selection-vs-concrete split), then
// SEALS it to exact at generalization (B1/B2's operative close): `p` is `non-open`
// and never escapes — `p.a`'s result is returned, but `p` itself is not — so its
// requirement closes to exact `{a: number}` and a wider argument is rejected. An
// `open` param keeps the row-polymorphic form and accepts the wider object. This
// exercises the close end-to-end through generalization and a call.
func TestInferModuleMemberReadAcceptsWiderArg(t *testing.T) {
	t.Run("wider object is rejected for a closed param", func(t *testing.T) {
		src := `
			fn f(p) { return p.a }
			val r = f({a: 1, b: 2})
		`
		_, _, errs := inferSource(t, src)
		require.Len(t, errs, 1)
		require.Equal(t, "3:24-3:25: object has extra property: b", msgWithSpan(errs[0]))
	})

	t.Run("wider object is accepted for an open param", func(t *testing.T) {
		src := `
			fn f(open p) { return p.a }
			val r = f({a: 1, b: 2})
		`
		_, _, errs := inferSource(t, src)
		require.Empty(t, errs)
	})

	t.Run("missing field is rejected at the call", func(t *testing.T) {
		src := `
			fn f(p) { return p.a }
			val r = f({b: 2})
		`
		_, _, errs := inferSource(t, src)
		// {b: 2} fails the sealed exact `{a: T}` requirement on two counts: the
		// missing `a` and the extra `b`. The object arm reports missing properties
		// before extra ones.
		require.Len(t, errs, 2)
		require.Equal(t, "3:14-3:20: object is missing property: a", msgWithSpan(errs[0]))
		require.Equal(t, "3:18-3:19: object has extra property: b", msgWithSpan(errs[1]))
		// Blame the offending argument {b: 2} — the object that lacks the field — not
		// the whole call. The requirement's field var is freshened on instantiation
		// and carries no prov, so MissingPropertyError's blame degrades to the Sub
		// (the argument object literal), which inferObject did record.
		require.Equal(t, "{b: 2}", spanText(src, errs[0].Span()))
	})
}

// --- IndexExpr (value receiver, constant string key) ---

// obj["foo-bar"] is the bracket form of property access: a constant string key
// reads the same property a dot access would, and lets the source name a key that
// is not a valid identifier. The receiver here is a value, not a namespace.
func TestInferIndexValueConstStringKey(t *testing.T) {
	c := newChecker()
	// {"foo-bar": 5, b: "hi"}["foo-bar"]
	fooBar := ast.NewProperty(ast.NewString("foo-bar", testSpan()), false, false, numExpr(5), testSpan())
	recv := objExpr(fooBar, prop("b", strExpr("hi")))
	e := ast.NewIndex(recv, strExpr("foo-bar"), false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "5", render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

// Reading a constant string key the receiver lacks fails with a
// MissingPropertyError, the same as the dot form — the index path shares
// valueProp's blame.
func TestInferIndexValueMissingProperty(t *testing.T) {
	c := newChecker()
	// {a: 5}["foo-bar"]
	e := ast.NewIndex(objExpr(prop("a", numExpr(5))), strExpr("foo-bar"), false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Equal(t, "never", render(got)) // the fresh result var picks up no lower bound
	require.Len(t, c.errs, 1)
	require.Equal(t, "object is missing property: foo-bar", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// End-to-end through the parser: `obj["foo-bar"]` parses to an IndexExpr and
// reads the property whose key is not a valid identifier.
func TestInferIndexValueConstStringKeySource(t *testing.T) {
	src := `
		val obj = {"foo-bar": 5}
		val x = obj["foo-bar"]
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
}

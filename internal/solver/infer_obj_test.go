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

// A spread element ([...a]) is an ArraySpreadExpr, which the M2 walk does not
// cover; inferExpr reports it as unsupported and drops the error-recovery
// placeholder (PR8) into the tuple slot, so the tuple still builds (no panic) and
// the spread's value `a` is never walked — so no cascading unknown-identifier
// error. Tuple/array spread is M4.
func TestInferTupleSpreadUnsupported(t *testing.T) {
	c := newChecker()
	// [...a]
	e := tupleExpr(ast.NewArraySpread(identExpr("a"), testSpan()))
	got := c.inferExpr(NewScope(), 0, e)
	require.Equal(t, "[error]", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: ArraySpreadExpr", c.errs[0].Message())
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

	rec, ok := got.(*soltype.RecordType)
	require.True(t, ok)
	require.Len(t, rec.Fields, 2) // the duplicate `a` was collapsed, not appended
	require.True(t, equalType(rec, rec), "equalType must be reflexive for a deduped record")
}

// Shorthand ({x}) is a property with no value — deferred to M4. It reports a
// clean UnsupportedNodeError and is skipped (the rest of the object still types).
func TestInferObjectShorthandUnsupported(t *testing.T) {
	c := newChecker()
	shorthand := ast.NewProperty(ast.NewIdent("x", testSpan()), false, false, nil, testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(shorthand))
	require.Equal(t, "{}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: PropertyExpr", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// A spread element ({...o}) is M4; it reports unsupported without walking its
// value, so the unknown `o` inside it never surfaces a second error.
func TestInferObjectSpreadUnsupported(t *testing.T) {
	c := newChecker()
	spread := ast.NewRestSpread(identExpr("o"), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(spread))
	require.Equal(t, "{}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: ObjSpreadExpr", c.errs[0].Message())
}

// A computed key ({[k]: v}) carries no static field name — M4.
func TestInferObjectComputedKeyUnsupported(t *testing.T) {
	c := newChecker()
	computed := ast.NewProperty(ast.NewComputedKey(identExpr("k")), false, false, numExpr(1), testSpan())
	got := c.inferExpr(NewScope(), 0, objExpr(computed))
	require.Equal(t, "{}", render(got))
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: ComputedKey", c.errs[0].Message())
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
	require.Equal(t, "Unsupported in M2: OptionalChain", c.errs[0].Message())
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
	require.Equal(t, "Unsupported in M2: OptionalChain", c.errs[0].Message())
}

// A malformed `recv.` (no valid property name) leaves Prop.Name empty; the
// parser has already reported the missing identifier, so the walk must NOT layer
// a spurious "object is missing property: " on top. It yields a never
// placeholder and reports nothing.
func TestInferMemberEmptyPropertyNameIsSilent(t *testing.T) {
	c := newChecker()
	// recv = {a: 5}; access with an empty property name (as the parser builds for `recv.`)
	e := ast.NewMember(objExpr(prop("a", numExpr(5))), ast.NewIdentifier("", testSpan()), false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.NeverType{}, got)
	require.Empty(t, c.errs) // no spurious MissingPropertyError
	require.Same(t, got, c.info.TypeOf(e))
}

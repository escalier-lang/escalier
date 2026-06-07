package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- PR8: reassignment typing (`a = expr`) ---
//
// `a = expr` parses as an ast.BinaryExpr with Op == ast.Assign — the only binary
// operator the M3 walk handles. The target must be a mutable (`var`) place; the
// RHS must be a subtype of the binding's type; the assignment expression itself is
// void. Reassignment lives in expression position, so these tests exercise it
// inside a function body (or a top-level `val` initializer).

// An annotated `var` reassignment type-checks when the RHS is a subtype of the
// declared type, and reports a single CannotConstrainError otherwise. The `var` is
// annotated because un-annotated `var` literal widening is M4 (see the literal case
// below).
func TestInferAssignAnnotatedVar(t *testing.T) {
	t.Run("matching type checks", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			var a: number = 5
			fn f() { a = 6 }
		`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["a"])
		require.Equal(t, "fn () -> void", values["f"]) // the assignment expr is void
	})
	t.Run("mismatched type reports one subtype error", func(t *testing.T) {
		src := "var a: number = 5\nfn f() { a = \"x\" }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `cannot constrain "x" <: number`, `"x"`, "number")
	})
}

// An un-annotated `var a = 5` infers the literal type `5`, so reassigning a
// DIFFERENT literal (`a = 6` ⇒ `6 <: 5`) does NOT check in M3 — `var` literal
// widening is deferred to M4. This pins the interim behavior so the M4 change is
// visible: when widening lands, this assignment will type-check.
func TestInferAssignUnannotatedVarLiteralNotWidened(t *testing.T) {
	src := "var a = 5\nfn f() { a = 6 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "cannot constrain 6 <: 5", "6", "5")
}

// Reassigning a `val` is rejected: only a `var` is reassignable. The error blames
// the assignment and relates the `val` declaration ("declared immutable here").
func TestInferAssignToValRejected(t *testing.T) {
	src := "val a = 5\nfn f() { a = 6 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"Cannot assign to immutable binding: a", "a = 6", "val a = 5")
}

// A function name and a parameter are likewise immutable. A parameter carries no
// introducing declaration source node, so its error has no related span.
func TestInferAssignToImmutableNonVal(t *testing.T) {
	t.Run("function name", func(t *testing.T) {
		src := "fn h() -> number { 5 }\nfn f() { h = 6 }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs,
			"Cannot assign to immutable binding: h", "h = 6", "fn h() -> number { 5 }")
	})
	t.Run("parameter", func(t *testing.T) {
		src := "fn f(p: number) { p = 6 }"
		_, _, errs := inferSource(t, src)
		// A parameter has no source decl node, so Related() is empty.
		requireBlame(t, src, errs, "Cannot assign to immutable binding: p", "p = 6")
	})
}

// A non-place LHS — a literal or a call — is an InvalidAssignmentTargetError that
// blames the LHS. (Member targets `obj.x = …` need record types and land in M4.)
func TestInferAssignInvalidTarget(t *testing.T) {
	t.Run("literal target", func(t *testing.T) {
		src := "val a = 5\nfn g() { 5 = a }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "Invalid assignment target: LiteralExpr", "5")
	})
	t.Run("call target", func(t *testing.T) {
		src := "fn f() -> number { 5 }\nvar a: number = 0\nfn g() { f() = a }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "Invalid assignment target: CallExpr", "f()")
	})
}

// The assignment expression's own value type is void: `val b = (a = 6)` ⇒ b: void.
func TestInferAssignValuePositionIsVoid(t *testing.T) {
	values, _, errs := inferSource(t, `
		var a: number = 5
		val b = (a = 6)
	`)
	require.Empty(t, errs)
	require.Equal(t, "void", values["b"])
}

// Headline synergy with Part 1 (the ErrorType sentinel): reassigning THROUGH a
// broken binding reports exactly ONE error — the UnknownIdentifierError on the
// declaration. `a` recovers to ErrorType, so `5 <: error` and `"hello" <: error`
// both absorb; without the sentinel each reassignment would cascade a spurious
// `cannot constrain … <: never`.
func TestInferAssignThroughBrokenBindingNoCascade(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f() {
			var a = missing
			a = 5
			a = "hello"
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: missing", errs[0].Message())
}

// Assigning to an undeclared name surfaces an UnknownIdentifierError on the target
// (an assignment must not silently accept an unbound place).
func TestInferAssignUnknownTarget(t *testing.T) {
	src := "fn f() { nope = 5 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "Unknown identifier: nope", "nope")
}

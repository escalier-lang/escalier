package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// --- PR8: reassignment typing (`a = expr`) ---
//
// `a = expr` parses as an ast.BinaryExpr with Op == ast.Assign — the only binary
// operator the M3 walk handles. The target must be a mutable (`var`) place; the
// source must be a subtype of the binding's type; the assignment expression evaluates
// to the value just stored, so its type is the target's slot type. Reassignment
// lives in expression position, so these tests exercise it inside a function body
// (or a top-level `val` initializer).

// An annotated `var` reassignment type-checks when the source is a subtype of the
// declared type, and reports a single CannotConstrainError otherwise.
func TestInferAssignAnnotatedVar(t *testing.T) {
	t.Run("matching type checks", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			var a: number = 5
			fn f() { a = 6 }
		`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["a"])
		require.Equal(t, "fn () -> void", values["f"]) // no return, so the body produces no value
	})
	t.Run("mismatched type reports one subtype error", func(t *testing.T) {
		src := "var a: number = 5\nfn f() { a = \"x\" }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `cannot constrain "x" <: number`, `"x"`, "number")
	})
}

// An un-annotated `var a = 5` widens its binding to `number` (M4 B3), so
// reassigning a DIFFERENT literal of the same primitive (`a = 6` ⇒ `6 <: number`)
// checks. A `val` keeps the literal singleton (see TestInferAssignToValRejected).
func TestInferAssignUnannotatedVarLiteralWidened(t *testing.T) {
	values, _, errs := inferSource(t, "var a = 5\nfn f() { a = 6 }")
	require.Empty(t, errs)
	require.Equal(t, "number", values["a"])
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
		src := "fn h() -> number { return 5 }\nfn f() { h = 6 }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs,
			"Cannot assign to immutable binding: h", "h = 6", "fn h() -> number { return 5 }")
	})
	t.Run("parameter", func(t *testing.T) {
		src := "fn f(p: number) { p = 6 }"
		_, _, errs := inferSource(t, src)
		// A parameter's binding records its pattern as the source, so Related() points
		// at the param ("declared immutable here").
		requireBlame(t, src, errs, "Cannot assign to immutable binding: p", "p = 6", "p")
	})
}

// A non-place target — a literal or a call — is an InvalidAssignmentTargetError that
// blames the target. A member target takes a separate path (a field write, C3) and an
// index target another (unsupported pending Array types, M7).
func TestInferAssignInvalidTarget(t *testing.T) {
	t.Run("literal target", func(t *testing.T) {
		src := "val a = 5\nfn g() { 5 = a }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "Invalid assignment target: LiteralExpr", "5")
	})
	t.Run("call target", func(t *testing.T) {
		src := "fn f() -> number { return 5 }\nvar a: number = 0\nfn g() { f() = a }"
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "Invalid assignment target: CallExpr", "f()")
	})
}

// The assignment expression evaluates to the value just stored, so its type is the
// target's slot type: `val b = (a = 6)` for `var a: number` ⇒ b: number.
func TestInferAssignValuePositionIsTargetType(t *testing.T) {
	values, _, errs := inferSource(t, `
		var a: number = 5
		val b = (a = 6)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["b"])
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

// Reassigning a POLYMORPHIC var must not corrupt the binding. inferAssign freshens
// the binding's coalesced slot type before constraining, so the source flows into
// throwaway copies rather than the binding's retained type-parameter vars; `id`
// keeps its generic type and a later `id("hello")` still type-checks.
func TestInferAssignPolyVarNoCorruption(t *testing.T) {
	values, _, errs := inferSource(t, `
		var id = fn (x) { return x }
		fn f() { id = fn (n: number) -> number { return n } }
		val r = id("hello")
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(x: T0) -> T0", values["id"]) // not the corrupted fn <T0>(x: T0 & number) -> T0 | number
	require.Equal(t, `"hello"`, values["r"])
}

// A broken TOP-LEVEL binding recovers AS the error sentinel (Fix A), so reassigning
// it yields exactly one diagnostic — the same single-error guarantee the body-level
// path gives. Before the fix the top-level group var coalesced to `never` and the
// reassignment cascaded `cannot constrain 5 <: never`.
func TestInferAssignTopLevelBrokenBindingNoCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		var a = missing
		fn f() { a = 5 }
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: missing", errs[0].Message())
	require.Equal(t, "error", values["a"]) // recovered as the sentinel, not never
}

// Reassigning a union-typed var applies the union-target rule: a member assigns, a
// non-member is rejected once. (Union subtyping in general is M6; inferAssign trials
// the members under a probe so a legal member assignment isn't wrongly rejected.)
func TestInferAssignUnionTarget(t *testing.T) {
	t.Run("member assigns", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(c: boolean) {
				var a = if c { 1 } else { 2 }
				a = 1
			}
		`)
		require.Empty(t, errs)
	})
	t.Run("non-member rejected once", func(t *testing.T) {
		src := "fn f(c: boolean) {\n  var a = if c { 1 } else { 2 }\n  a = 3\n}"
		_, _, errs := inferSource(t, src)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain 3 <: 1 | 2", errs[0].Message())
	})
}

// KNOWN GAP (M6): assigning an inference-variable source into a union target
// over-narrows the variable. `a = x` for an un-annotated param `x` and `a: 1 | 2`
// commits the first matching member (`x <: 1`), so `x` infers as `1` rather than
// the sound `1 | 2`. This is INCOMPLETE, not unsound (the committed bound is always
// stronger than required, so no invalid program is accepted), and it is not fixable
// in constrainAssign — falling through to constrain(source, union) injects the
// coalesced union node into source's bound list and panics the coalescer. The correct
// fix is M6's first-class union subtyping with inference variables. This pins the
// interim behavior so the M6 change is visible: when it lands, `x` becomes `1 | 2`
// and this assertion must be updated. See constrainAssign's KNOWN GAP note.
func TestInferAssignUnionTargetVarRHSOverNarrows(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(c: boolean, x) {
			var a = if c { 1 } else { 2 }
			a = x
		}
	`)
	require.Empty(t, errs)
	// M6 target: "fn (c: boolean, x: 1 | 2) -> void".
	require.Equal(t, "fn (c: boolean, x: 1) -> void", values["f"])
}

// A namespace name as an assignment target reports NamespaceUsedAsValue, mirroring
// inferIdent's value-position behavior (not UnknownIdentifier). Hand-built because
// namespace declarations are themselves unsupported in M3, so a namespace never
// enters scope via real source — same construction as TestInferIdentNamespaceUsedAsValue.
func TestInferAssignNamespaceTarget(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{Name: "Foo"})
	e := ast.NewBinary(identExpr("Foo"), numExpr(5), ast.Assign, testSpan())
	c.inferExpr(scope, 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace used as a value: Foo", c.errs[0].Message())
}

// A member target (obj.x = …) is a field write (M4 C3). Writing to a field of an
// IMMUTABLE object — here `o` is `val`-bound, so its `{x: 5}` is immutable — is
// rejected: the write requires `o <: mut {x: number, ...}`, and an immutable object
// cannot fill the mutable slot (the C2 gate's mutability rule).
func TestInferAssignMemberTargetImmutable(t *testing.T) {
	src := "val o = {x: 5}\nfn f() { o.x = 6 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "cannot constrain immutable object <: mutable object", "o.x = 6")
}

// An INDEX target (xs[i] = …) still needs Array and index types (M7), so it stays
// an unsupported feature — distinct from a member target, which C3 now types.
func TestInferAssignIndexTargetUnsupported(t *testing.T) {
	src := "val xs = [1, 2]\nfn f() { xs[0] = 6 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "Unsupported: assignment to a member or index", "xs[0]")
}

// An immutable target with an independently-broken source reports BOTH errors — they
// are two distinct problems, not a cascade (the immutable target never reaches
// constrain). Pinned so the behavior is explicit.
func TestInferAssignImmutableWithBadRHSReportsBoth(t *testing.T) {
	_, _, errs := inferSource(t, `
		val a = 5
		fn f() { a = missing }
	`)
	require.Equal(t, []string{
		"Unknown identifier: missing",
		"Cannot assign to immutable binding: a",
	}, Messages(errs))
}

// A malformed assignment node with a nil operand (hand-built; the real parser
// substitutes ast.NewError) must not panic — it blames the whole expression.
func TestInferAssignNilOperandDoesNotPanic(t *testing.T) {
	c := newChecker()
	e := ast.NewBinary(nil, numExpr(5), ast.Assign, testSpan())
	require.NotPanics(t, func() {
		c.inferExpr(NewScope(), 0, e)
		for _, er := range c.errs { // force lazy Span()/Message()
			_ = er.Span()
			_ = er.Message()
		}
	})
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: BinaryExpr", c.errs[0].Message())
}

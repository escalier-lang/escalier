package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// PR3 — async / await / block return-point join, against real source through
// inferSource. The PR builds on M2's monomorphic walk: the body of an `async fn`
// types exactly like a plain function, then its EXTERNAL return wraps in
// Promise<T>; `await e` constrains `e <: Promise<U>` and yields `U`; non-tail
// ReturnStmts join with the block tail before constraining against the return
// annotation. No auto-flatten of nested Promise<Promise<T>> — that is M9's
// Awaited<T>.

// --- async fn external wrap ---

// `async fn () -> number { return 5 }` externally renders as
// `fn () -> Promise<number>` — the headline async-wrap.
func TestInferAsyncFnWrapsReturnInPromise(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f() -> number {
			return 5
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> Promise<number>", values["f"])
}

// An async fn with no explicit return — the body's tail is its return — still
// wraps externally. Here the tail value is the literal "hi", so externally
// `Promise<"hi">`.
func TestInferAsyncFnTailReturnWrapped(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn greet() {
			"hi"
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn () -> Promise<"hi">`, values["greet"])
}

// --- await ---

// `await p` where `p: Promise<string>` yields `string`. The fresh `U` from the
// constraint `p <: Promise<U>` flows to `string` through bound propagation.
func TestInferAwaitUnwrapsPromise(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<string>) -> string {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<string>) -> Promise<string>", values["f"])
}

// No auto-flatten: `await p` where `p: Promise<Promise<number>>` yields
// `Promise<number>`. The constraint is `p <: Promise<U>`, so U = Promise<number>
// — one layer peeled, the rest preserved. Awaited<T>'s recursive flatten is M9.
func TestInferAwaitNoAutoFlatten(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<Promise<number>>) -> Promise<number> {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<Promise<number>>) -> Promise<Promise<number>>", values["f"])
}

// `await` outside an `async fn` is a WALK rejection — not a type rule failure.
// The AwaitOutsideAsyncError carries the await node and produces a `never`
// placeholder so a downstream consumer doesn't cascade.
func TestInferAwaitOutsideAsyncRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: Promise<string>) {
			await p
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "await can only be used inside an async function", errs[0].Message())
}

// `await` of a non-Promise concrete fails through constrain — the rule
// constrain(e <: Promise<U>) lowers `number <: Promise<U>` to a
// CannotConstrainError because the concrete shapes don't match.
func TestInferAwaitOfNonPromiseFails(t *testing.T) {
	_, _, errs := inferSource(t, `
		async fn f(n: number) {
			await n
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain number <: Promise<t1>", errs[0].Message())
}

// `await` inside an async fn nested under a non-async outer must still resolve
// against the inner's funcCtx — the push/pop discipline. The nested return type
// of `inner` wraps in Promise<…>; the outer is non-async so does NOT wrap.
func TestInferAwaitInNestedAsyncOK(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn outer(p: Promise<number>) {
			val inner = async fn () {
				return await p
			}
			inner
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<number>) -> fn () -> Promise<number>", values["outer"])
}

// Symmetric: an `await` in the OUTER function — which is non-async — fires the
// walk rejection even though an inner async fn exists. The inner async context
// is popped before the outer's `await` is walked.
func TestInferAwaitInOuterAfterInnerAsync(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn outer(p: Promise<number>) {
			val inner = async fn () { 1 }
			await p
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "await can only be used inside an async function", errs[0].Message())
}

// --- Block return-point join ---

// The headline PR3 join: `fn () { if c { return 1 } return "x" }` collects BOTH
// returns and joins them with the tail. The function externally returns
// 1 | "x".
func TestInferBlockReturnJoinAcrossIf(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn h(c: boolean) {
			if c { return 1 }
			return "x"
		}
	`)
	require.Empty(t, errs)
	// The two return points coalesce into a union; the renderer orders by
	// first-occurrence in the join var's lower-bound list (declaration order).
	require.Equal(t, `fn (c: boolean) -> 1 | "x"`, values["h"])
}

// An early return inside one branch, with the other branch producing the tail.
// Both return points contribute to the function's return type — neither path is
// dropped. With no else, the if's value is `void | <cons>`; the tail `"x"` is
// the fall-through; together with the collected `return 1`, the function
// returns `1 | "x"`.
func TestInferBlockReturnAnnotationCheckedAgainstAllReturns(t *testing.T) {
	// `return 1` inside the if, return-annotation number — should type-check.
	_, _, errs := inferSource(t, `
		fn f(c: boolean) -> number {
			if c { return 1 }
			return 2
		}
	`)
	require.Empty(t, errs)
}

// A non-tail return whose type CONFLICTS with the declared return annotation
// must be reported. Pre-PR3 (M2) the non-tail return was dropped silently; PR3
// joins it with the tail and constrains the join against the annotation.
func TestInferBlockNonTailReturnCheckedAgainstAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(c: boolean) -> number {
			if c { return "oops" }
			return 1
		}
	`)
	// The bad return surfaces through the constrain "joined <: number" path: the
	// join var has "oops" as a lower bound, propagated through constrain to the
	// return annotation. The string literal's primitive does not satisfy number.
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message(), `"oops"`)
	require.Contains(t, errs[0].Message(), "number")
}

// `async fn` joined with multiple returns wraps the join in Promise<…>. The
// external return is Promise<1 | "x">.
func TestInferAsyncFnWithJoinedReturnsWrapped(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(c: boolean) {
			if c { return 1 }
			return "x"
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> Promise<1 | "x">`, values["f"])
}

// --- IfElseExpr value ---

// An if/else used as an expression is the join of its branches. Without
// generalization on the binding (PR1's value-only generalization for `val =
// fn`), the binding here is monomorphic-frozen to the join.
func TestInferIfElseExprValueIsBranchJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick(c: boolean) {
			if c { 1 } else { "x" }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> 1 | "x"`, values["pick"])
}

// An if WITHOUT else folds in void from the missing alt — `if c { 5 }` as a
// tail expression is `5 | void`.
func TestInferIfElseExprMissingAltIsVoid(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick(c: boolean) {
			if c { 5 }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> 5 | void`, values["pick"])
}

// The if's condition must be boolean; a string condition is rejected.
func TestInferIfElseConditionMustBeBool(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn pick(c: string) {
			if c { 1 } else { 2 }
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain string <: boolean", errs[0].Message())
}

// --- Unit-level (against hand-built AST) ---

// Bare `return` (no expr) inside async wraps Void: the external return is
// Promise<void>. Exercises the funcCtx collection of a bare return.
func TestInferAsyncBareReturnIsPromiseVoid(t *testing.T) {
	c := newChecker()
	// async fn () { return }
	e := ast.NewFuncExpr(nil, nil, nil, nil, nil, true,
		block(returnStmt(nil)), testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> Promise<void>", render(got))
}

// Multiple bare returns + a void tail all join through one var, all void,
// coalescing to plain `void` (no degenerate `void | void` union).
func TestInferFnMultipleBareReturnsCollapse(t *testing.T) {
	c := newChecker()
	// fn () { return; return; }
	e := funcExpr(nil, nil, block(
		returnStmt(nil),
		returnStmt(nil),
	))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> void", render(got))
}

// A nested return is collected on the INNER funcCtx, not the outer one. After
// the inner fn ends, the outer's returns list is still empty — the body's tail
// (the inner fn's type) is the outer return, unwrapped.
func TestInferNestedFnReturnsScoped(t *testing.T) {
	c := newChecker()
	// fn outer() { fn (x: number) { return x } }
	inner := funcExpr([]*ast.Param{param("x", numAnn())}, nil,
		block(returnStmt(identExpr("x"))))
	outer := funcExpr(nil, nil, block(exprStmt(inner)))

	got := c.inferExpr(NewScope(), 0, outer)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> fn (x: number) -> number", render(got))
}

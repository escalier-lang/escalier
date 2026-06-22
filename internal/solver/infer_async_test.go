package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// PR3 — async / await / return-point join, against real source through
// inferSource. The PR builds on M2's monomorphic walk: the body of an `async fn`
// types exactly like a plain function, then its EXTERNAL return wraps in
// Promise<T>; `await e` constrains `e <: Promise<U>` and yields `U`; the
// collected ReturnStmts join before constraining against the return annotation.
// No auto-flatten of nested Promise<Promise<T>> — that is M9's Awaited<T>.

// --- async fn external wrap ---

// `async fn () -> Promise<number> { return 5 }`: the annotation names the EXTERNAL
// Promise, and the body returns the unwrapped inner (`5`), constrained `5 <:
// number`. Externally `fn () -> Promise<number>`.
func TestInferAsyncFnWrapsReturnInPromise(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f() -> Promise<number> {
			return 5
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> Promise<number>", values["f"])
}

// An async fn with no explicit return produces no value — a body's last
// expression is NOT an implicit return — so the external wrap is Promise<void>,
// not Promise<"hi">.
func TestInferAsyncFnNoReturnIsPromiseVoid(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn greet() {
			"hi"
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn () -> Promise<void>`, values["greet"])
}

// --- await ---

// `await p` where `p: Promise<string>` yields `string`. The fresh `U` from the
// constraint `p <: Promise<U>` flows to `string` through bound propagation, and
// that `string` body return satisfies the declared inner of `-> Promise<string>`.
func TestInferAwaitUnwrapsPromise(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<string>) -> Promise<string> {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<string>) -> Promise<string>", values["f"])
}

// No auto-flatten: `await p` where `p: Promise<Promise<number>>` yields
// `Promise<number>` — one layer peeled, the rest preserved (the constraint is
// `p <: Promise<U>`, so U = Promise<number>; Awaited<T>'s recursive flatten is M9).
// With NO return annotation the async fn wraps that inferred return, so the external
// type is Promise<Promise<number>> — which only holds if await peeled exactly one
// layer (a full flatten would give `number`, wrapping to `Promise<number>`).
func TestInferAwaitNoAutoFlatten(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<Promise<number>>) {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<Promise<number>>) -> Promise<Promise<number>>", values["f"])
}

// An explicit Promise return annotation names the EXTERNAL type directly — it is
// not re-wrapped, and the body returns the unwrapped inner. Here `await p` (p:
// Promise<Promise<number>>) yields `Promise<number>`, which satisfies the declared
// inner of `-> Promise<Promise<number>>`, and the external face is exactly that
// annotation.
func TestInferAsyncExplicitPromiseAnnotationGovernsReturn(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<Promise<number>>) -> Promise<Promise<number>> {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<Promise<number>>) -> Promise<Promise<number>>", values["f"])
}

// A bare (non-Promise) return annotation on an `async fn` is rejected: an async
// function's external type is always Promise<…>, so `-> number` must be written
// `-> Promise<number>`. Recovery wraps the inferred body return, so the function
// still faces callers as a Promise (`Promise<5>` here).
func TestInferAsyncBareReturnAnnotationRejected(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f() -> number {
			return 5
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "async function return type must be a Promise; write Promise<...> or Promise<_>", errs[0].Message())
	require.Equal(t, "fn () -> Promise<5>", values["f"])
}

// The bare-async-return error blames the offending annotation and relates the
// enclosing function (the signature to fix).
func TestInferAsyncBareReturnAnnotationBlame(t *testing.T) {
	src := "async fn f() -> number { return 5 }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"async function return type must be a Promise; write Promise<...> or Promise<_>",
		"number",
		"async fn f() -> number { return 5 }")
}

// `Promise<_>` lets the checker infer the inner from the body: the `_` resolves to
// a fresh var the body's return flows into. Here `await p` yields `string`, so the
// inferred inner is `string` and the external type is `Promise<string>`.
func TestInferAsyncPromiseWildcardReturnInferred(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f(p: Promise<string>) -> Promise<_> {
			return await p
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Promise<string>) -> Promise<string>", values["f"])
}

// `await` outside an `async fn` is a WALK rejection — not a type rule failure.
// The AwaitOutsideAsyncError carries the await node and produces the ErrorType
// recovery placeholder (PR8) so a downstream consumer doesn't cascade.
func TestInferAwaitOutsideAsyncRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: Promise<string>) {
			await p
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "await can only be used inside an async function", errs[0].Message())
}

// The await-outside-async error points Related() at the enclosing (non-async)
// function — the one to mark `async` — so an IDE can offer the fix. Primary span is
// the await; related is the whole enclosing fn.
func TestInferAwaitOutsideAsyncBlamesEnclosingFn(t *testing.T) {
	src := "fn f(p: Promise<string>) { await p }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"await can only be used inside an async function", "await p",
		"fn f(p: Promise<string>) { await p }")
}

// At module top-level there is no enclosing function, so Related() is empty (there
// is nothing to mark `async`). Built directly so the awaited value resolves cleanly
// and the only error is the await itself.
func TestInferAwaitOutsideAsyncTopLevelNoRelated(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("x", ValueBinding{Schemes: []TypeScheme{
		monoScheme(&soltype.PromiseType{Inner: &soltype.PrimType{Prim: soltype.StrPrim}}),
	}})
	// await x, with no enclosing function context (c.fn == nil).
	c.inferExpr(scope, 0, ast.NewAwait(identExpr("x"), testSpan()))
	require.Len(t, c.errs, 1)
	require.Equal(t, "await can only be used inside an async function", c.errs[0].Message())
	require.Empty(t, c.errs[0].Related())
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
			return inner
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
	// The two return points coalesce into a union, canonicalized by M6 PR1's
	// newUnion. NumLit sorts before StrLit within the LitType kind, so 1
	// comes before "x".
	require.Equal(t, `fn (c: boolean) -> 1 | "x"`, values["h"])
}

// An early return inside one branch plus a fall-through return: both return
// points contribute to the function's return type — neither path is dropped —
// and the join checks against the annotation.
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

// An `async fn` joined with multiple returns wraps the join in Promise. The
// external return is Promise<1 | "x">. Members are in canonical order under
// M6 PR1.
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

// An if/else used as an expression is the join of its branches, observed
// through an explicit `return` (a bare tail would be discarded). Without
// generalization on the binding (PR1's value-only generalization for `val =
// fn`), the binding here is monomorphic-frozen to the join.
func TestInferIfElseExprValueIsBranchJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick(c: boolean) {
			return if c { 1 } else { "x" }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> 1 | "x"`, values["pick"])
}

// An if without an else folds in void from the missing alt, so
// `return if c { 5 }` returns `void | 5`. Void ranks before LitType in M6
// PR1's canonical order.
func TestInferIfElseExprMissingAltIsVoid(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick(c: boolean) {
			return if c { 5 }
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> void | 5`, values["pick"])
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
// the inner fn ends, the outer's returns list holds only the outer's own
// `return` of the inner fn — the inner's `return x` never leaks out.
func TestInferNestedFnReturnsScoped(t *testing.T) {
	c := newChecker()
	// fn outer() { return fn (x: number) { return x } }
	inner := funcExpr([]*ast.Param{param("x", numAnn())}, nil,
		block(returnStmt(identExpr("x"))))
	outer := funcExpr(nil, nil, block(returnStmt(inner)))

	got := c.inferExpr(NewScope(), 0, outer)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> fn (x: number) -> number", render(got))
}

// --- Error-recovery: no cascading on a failed sub-expression ---
//
// A reported diagnostic leaves the ErrorType recovery placeholder in expression
// position (c.report, PR8). ErrorType absorbs in both directions inside constrain,
// so the if-condition and await-argument checks no longer cascade a spurious second
// `cannot constrain … <: …` on top of the real error — exactly ONE diagnostic
// surfaces. (Before PR8, inferIfElse/inferAwait hand-guarded against a `never`
// placeholder at each site; PR8 retired those guards for the absorbing sentinel.)

// A failed `if` condition (unknown identifier) yields a single UnknownIdentifierError
// — not also a `never <: boolean`. The branches still type, so the if value is their
// join.
func TestInferIfElseUnknownConditionNoCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn pick() {
			return if undeclared { 1 } else { 2 }
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: undeclared", errs[0].Message())
	require.Equal(t, "fn () -> 1 | 2", values["pick"])
}

// A failed `await` argument (unknown identifier) yields a single
// UnknownIdentifierError — not also a `never <: Promise<…>`. The await result is
// left unbound and coalesces to `never`, so the async fn externally returns
// Promise<never>.
func TestInferAwaitUnknownArgNoCascade(t *testing.T) {
	values, _, errs := inferSource(t, `
		async fn f() {
			return await undeclared
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: undeclared", errs[0].Message())
	require.Equal(t, "fn () -> Promise<never>", values["f"])
}

// An unsupported INNER of a supported `Promise<…>` keeps the Promise wrapper: the
// param stays Promise-shaped (recovered inner = a fresh var) rather than collapsing
// to a bare var, and only the inner's own unsupported error is reported. Here the
// param is unused, so its recovered inner var coalesces to `unknown` (contravariant,
// no bounds).
func TestInferPromiseUnsupportedInnerKeepsWrapper(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: Promise<bigint>) {
			return 0
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported: BigintTypeAnn", errs[0].Message())
	require.Equal(t, "fn (p: Promise<unknown>) -> 0", values["f"])
}

// When the recovered Promise inner is actually used (returned), it behaves like any
// unconstrained variable: it generalizes to a type parameter, so the wrapper carries
// through both positions as Promise<T0>. Proves the recovery is a real fresh var,
// not a poisoned `never`/`unknown` (which would have cascaded or frozen).
func TestInferPromiseUnsupportedInnerGeneralizes(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: Promise<bigint>) {
			return p
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported: BigintTypeAnn", errs[0].Message())
	require.Equal(t, "fn <T0>(p: Promise<T0>) -> Promise<T0>", values["f"])
}

// --- Rejected forms (#6, #7) ---

// A lifetime-annotated Promise is rejected, not silently coerced to a plain
// Promise<T> — the soltype PromiseType carries no lifetime, so accepting it would
// drop the annotation. Both surface forms: `Promise<'a, T>` (lifetime arg) and
// `'a Promise<T>` (leading lifetime).
func TestInferPromiseLifetimeRejected(t *testing.T) {
	tests := map[string]string{
		"lifetime arg":     "fn f(p: Promise<'a, number>) { 0 }",
		"leading lifetime": "fn f(p: 'a Promise<number>) { 0 }",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Len(t, errs, 1)
			require.Equal(t, "Unsupported: lifetime annotation on Promise", errs[0].Message())
		})
	}
}

// A `return` reached outside any function — here inside an `if` that is part of a
// top-level `val` initializer — is rejected by the walk (symmetric to
// await-outside-async), not silently dropped. The `if` still types its branches,
// and the `return 5` branch DIVERGES, so it contributes `never` to the if's value
// (not `5`): the binding recovers to the non-diverging branch alone, `6`.
func TestInferReturnOutsideFunctionRejected(t *testing.T) {
	values, _, errs := inferSource(t, `
		val x = if true { return 5 } else { 6 }
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "return can only be used inside a function", errs[0].Message())
	require.Equal(t, "6", values["x"])
}

// A diverging `if` branch (early return) contributes `never` to the if's VALUE, so
// the binding it initializes sees only the non-diverging branch. When control
// reaches `val x = …`, the `c == true` path has already returned from the
// function, so the only path that produces a value for x is the `else`.
//
// The final statement returns x wrapped in a tuple — `return [x]` — so x's
// inferred type is OBSERVABLE in the function's return distinctly from the
// early-return point. A bare `return x` would not discriminate: `return 1` makes
// `1` a function return point regardless, so both the correct `x : "y"` and the
// buggy `x : 1 | "y"` render the function as `1 | "y"` (the leaked `1` is
// absorbed by the return point). Inside the tuple the leak cannot hide — correct
// gives `["y"]`, the bug would give `[1 | "y"]`.
func TestInferIfElseDivergingBranchDropsFromValue(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(c: boolean) {
			val x = if c { return 1 } else { "y" }
			return [x]
		}
	`)
	require.Empty(t, errs)
	// x is "y", not 1 | "y" — so the returned tuple is ["y"]. The function returns
	// 1 | ["y"]: the early `return 1` IS a function return point (joined with the
	// final return), even though it is not part of x's value. A buggy leak would
	// surface here as 1 | [1 | "y"].
	require.Equal(t, `fn (c: boolean) -> 1 | ["y"]`, values["f"])
}

// The dual of the above: when the diverging-branch `if` is part of the RETURNED
// expression, the function's return type still includes the early return. The
// if's value is the non-diverging branch ("z"), wrapped in a tuple so it is
// observable distinctly from the collected `return 3`; the return-point join
// folds in that return, so the function returns 3 | ["z"] (a bug that leaked the
// diverging branch into the if's value would render 3 | [3 | "z"]).
func TestInferIfElseDivergingBranchReturnJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(c: boolean) {
			return [if c { return 3 } else { "z" }]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (c: boolean) -> 3 | ["z"]`, values["f"])
}

// When BOTH branches of an `if` diverge, the if's VALUE has no contributing branch
// and coalesces to `never` (the bottom type — no path through the `if` yields a
// value). Observed at top level so the if-value is read straight off the binding:
// each `return` outside a function is also reported (symmetric to
// TestInferReturnOutsideFunctionRejected). A bug that failed to drop a diverging
// branch would surface here as `1 | 2` instead of `never`.
func TestInferIfElseBothBranchesDivergeYieldsNever(t *testing.T) {
	values, _, errs := inferSource(t, `
		val x = if true { return 1 } else { return 2 }
	`)
	require.Len(t, errs, 2)
	require.Equal(t, "return can only be used inside a function", errs[0].Message())
	require.Equal(t, "return can only be used inside a function", errs[1].Message())
	require.Equal(t, "never", values["x"])
}

// #719/#720: dead code after a diverging statement no longer contributes to the
// function's value. Both branches return, so x's initializer diverges entirely
// and the unreachable `[x]` statement is walked but discarded — the function's
// return type is exactly the join of its return points, 1 | 2.
func TestInferDeadTailAfterDivergingValDiscarded(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(c: boolean) {
			val x = if c { return 1 } else { return 2 }
			[x]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (c: boolean) -> 1 | 2", values["f"])
}

// #719/#720: a statement after a `return` is dead code and does not contribute
// to the function's value — `fn g() { return 1; 2 }` is `1`, not `1 | 2`.
func TestInferStatementAfterReturnDiscarded(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g() {
			return 1
			2
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> 1", values["g"])
}

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// PR10c — async rejection. An `async fn` cannot raise: what its body throws is absorbed
// by the promise's rejection slot, the E in `-> Promise<V, E>`, and the function's own
// throws stays `never`. The return annotation's E is the rejection's declaration
// surface — a written E is what the body's throws are checked against, `Promise<V>`
// forbids them, and with no annotation the rejection is inferred. A `throws` clause on
// an `async fn` also names the rejection and is checked against the annotation's E.

// A clause-less async body may throw; the throw is absorbed into the promise's inferred
// or annotated rejection rather than rejected the way a sync body's would be.
func TestInferAsyncThrowsAbsorbedIntoRejection(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "ClauselessThrowInfersTheRejection",
			src:  `async fn f() { throw "x" }`,
			want: `fn () -> Promise<never, "x">`,
		},
		{
			// Two throws on different paths union into the rejection, the same join a
			// sync `throws _` builds.
			name: "ThrowsOnBothBranchesUnionIntoTheRejection",
			src:  `async fn f(c: boolean) { if c { throw "a" } else { throw 5 } }`,
			want: `fn (c: boolean) -> Promise<never, 5 | "a">`,
		},
		{
			// A written E is the declaration the body's throws are checked against, the
			// requirements' fetchWithError shape.
			name: "AnnotatedRejectionSlotAbsorbsTheBodysThrows",
			src:  `async fn f(c: boolean) -> Promise<number, string> { if c { throw "boom" } return 5 }`,
			want: `fn (c: boolean) -> Promise<number, string>`,
		},
		{
			name: "NonThrowingBodyLeavesTheRejectionNever",
			src:  `async fn f() { return 5 }`,
			want: "fn () -> Promise<5>",
		},
	})
}

// A `throws` clause on an `async fn` names the rejection explicitly: `throws _` infers
// it, `throws E` fixes it, and either way the function's external type carries no throws
// clause of its own — calling it raises nothing.
func TestInferAsyncThrowsClauseNamesTheRejection(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "WildcardClauseInfersTheRejection",
			src:  `async fn f() throws _ { throw "x" }`,
			want: `fn () -> Promise<never, "x">`,
		},
		{
			// A wildcard sink nothing reached coalesces to `never`, so the promise
			// renders its one-argument form — the same collapse an unused sync
			// `throws _` gets.
			name: "WildcardClauseNothingReachesCoalescesAway",
			src:  `async fn f() throws _ { return 5 }`,
			want: "fn () -> Promise<5>",
		},
		{
			name: "DeclaredClauseFixesTheRejection",
			src:  `async fn f(c: boolean) throws string { if c { throw "boom" } return 5 }`,
			want: `fn (c: boolean) -> Promise<5, string>`,
		},
		{
			// The clause and the annotation's E name the rejection twice, so the clause
			// is checked against the slot and the annotation stays the external face.
			name: "DeclaredClauseBesideAnAnnotatedRejectionSlot",
			src:  `async fn f(c: boolean) -> Promise<number, string> throws string { if c { throw "boom" } return 5 }`,
			want: `fn (c: boolean) -> Promise<number, string>`,
		},
		{
			name: "WildcardClauseFlowsIntoAnAnnotatedRejectionSlot",
			src:  `async fn f() -> Promise<never, string> throws _ { throw "boom" }`,
			want: `fn () -> Promise<never, string>`,
		},
	})
}

// Calling an async function raises nothing — it returns a promise — so only awaiting the
// promise is the exceptional exit. The await constrains the promise's Err into the
// enclosing body's sink the way a throwing call constrains the callee's throws.
func TestInferAsyncRejectionSurfacesAtAwait(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// The caller only holds the promise, so its clause-less sync signature is
			// fine.
			name: "HoldingThePromiseNeedsNoClause",
			src: `
				async fn f() throws string { throw "boom" }
				fn g() { val p = f() }
			`,
			binding: "g",
			want:    "fn () -> void",
		},
		{
			// An awaited rejection is absorbed into the awaiting async fn's own
			// rejection, the re-raise chain the requirements' processData example is
			// built on.
			name: "AwaitedRejectionPropagatesIntoTheCallersRejection",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() { await f() }
			`,
			binding: "g",
			want:    "fn () -> Promise<void, string>",
		},
		{
			name: "AwaitedRejectionPropagatesThroughAParameter",
			src: `
				async fn g(p: Promise<number, string>) { return await p }
			`,
			binding: "g",
			want:    "fn (p: Promise<number, string>) -> Promise<number, string>",
		},
		{
			// A `try` around the await catches the rejection, so nothing reaches the
			// caller's own rejection slot — the join with PR10b.
			name: "TryAroundAwaitCatchesTheRejection",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() { try { await f() } catch { e => 0 } }
			`,
			binding: "g",
			want:    "fn () -> Promise<void>",
		},
		{
			name: "AwaitingANonRejectingPromiseAddsNothing",
			src: `
				async fn f() { return 5 }
				async fn g() { await f() }
			`,
			binding: "g",
			want:    "fn () -> Promise<void>",
		},
	})
}

// The raised flag behind the unused-clause warning tracks awaits the way it tracks
// calls: an await that provably cannot reject leaves a declared clause unused, and one
// that may reject counts as using it. A var joining several promises rejects when ANY
// member does, so the answer must not depend on which bound the join recorded first.
func TestInferAwaitRaisedTracking(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// The awaited callee cannot reject, so the clause is unreachable — the
			// async twin of a sync clause over a non-throwing call.
			name: "AwaitingANonRejectingCalleeLeavesTheClauseUnused",
			src: `
				async fn f() { return 5 }
				async fn g() throws string { await f() }
			`,
			wantErrs: []string{
				"3:25-3:31: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
		},
	})
	runThrowsCases(t, []throwsCase{
		{
			// One branch rejects, so the join may reject and the clause is used —
			// whichever branch the join recorded first.
			name: "JoinWithARejectingBranchUsesTheClause",
			src: `
				async fn g(c: boolean, p1: Promise<number>, p2: Promise<number, string>) throws string {
					await (if c { p1 } else { p2 })
				}
			`,
			binding: "g",
			want:    "fn (c: boolean, p1: Promise<number>, p2: Promise<number, string>) -> Promise<void, string>",
		},
		{
			name: "JoinWithARejectingBranchUsesTheClauseOrderSwapped",
			src: `
				async fn g(c: boolean, p1: Promise<number, string>, p2: Promise<number>) throws string {
					await (if c { p1 } else { p2 })
				}
			`,
			binding: "g",
			want:    "fn (c: boolean, p1: Promise<number, string>, p2: Promise<number>) -> Promise<void, string>",
		},
	})
}

// `Promise<V>` reads its missing E as `never`, so an annotation without a rejection slot
// holds the body to raising nothing, the way a clause-less sync signature does.
func TestInferAsyncThrowsHeldToTheAnnotation(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "ThrowUnderAOneArgumentPromiseAnnotation",
			src:      `async fn f() -> Promise<number> { throw "x" }`,
			wantErrs: []string{`1:41-1:44: cannot constrain "x" <: never`},
		},
		{
			name: "AwaitUnderAOneArgumentPromiseAnnotation",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() -> Promise<number> {
					await f()
					return 5
				}
			`,
			wantErrs: []string{"4:6-4:15: cannot constrain string <: never"},
		},
		{
			// A clause beside a one-argument annotation declares a rejection the
			// annotation cannot deliver. The body raises nothing either, so the clause
			// is flagged unused as well.
			name: "ClauseBesideAOneArgumentPromiseAnnotation",
			src:  `async fn f() -> Promise<number> throws string { return 5 }`,
			wantErrs: []string{
				"1:40-1:46: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
				"1:40-1:46: cannot constrain string <: never",
			},
		},
	})
}

// A non-async function can also produce a `Promise<V, E>`, but it can raise of its own:
// what its body throws reaches its `throws` clause, separate from the promise's
// rejection slot.
func TestInferSyncFunctionReturningARejectingPromise(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "OwnThrowsStaysOnTheClause",
			src: `
				fn f(p: Promise<number, "a">, c: boolean) throws "boom" {
					if c { throw "boom" }
					return p
				}
			`,
			want: `fn (p: Promise<number, "a">, c: boolean) -> Promise<number, "a"> throws "boom"`,
		},
	})
}

// The fetchJSON shape from docs/06_error_handling.md: a clause-less async function that
// awaits a rejecting promise and raises its own error accumulates both into its
// rejection, and a caller that names every member in catch arms needs no rejection of
// its own.
func TestInferAsyncFetchJSONExample(t *testing.T) {
	src := `
		declare fn fetch(url: string) -> Promise<string, "fetch-error">
		async fn fetchJSON(url: string, ok: boolean) {
			val res = await fetch(url)
			if ok {
				return res
			}
			throw "syntax-error"
		}
		async fn process(url: string, ok: boolean) {
			try {
				return await fetchJSON(url, ok)
			} catch {
				"fetch-error" => "",
				"syntax-error" => "",
			}
		}
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t,
		`fn (url: string, ok: boolean) -> Promise<string, "fetch-error" | "syntax-error">`,
		values["fetchJSON"])
	require.Equal(t,
		"fn (url: string, ok: boolean) -> Promise<string>",
		values["process"])
}

// A `Promise<T, E>` annotation resolves its second argument into the rejection slot, and
// the promise arms are covariant in it: a rejecting promise fits a wider rejection slot
// but not a narrower one.
func TestInferPromiseErrAnnotationVariance(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "RejectionWidensCovariantly",
			src: `
				fn f(p: Promise<number, "a">) -> Promise<number, "a" | "b"> {
					return p
				}
			`,
			want: `fn (p: Promise<number, "a">) -> Promise<number, "a" | "b">`,
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			name: "RejectionDoesNotNarrow",
			src: `
				fn f(p: Promise<number, "a" | "b">) -> Promise<number, "a"> {
					return p
				}
			`,
			wantErrs: []string{`2:35-2:38: cannot constrain "b" <: "a"`},
		},
	})
}

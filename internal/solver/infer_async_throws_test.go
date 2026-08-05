package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// PR10c — async rejection. An `async fn` that throws rejects its promise rather than
// raising to its caller, so the body's throws becomes the promise's Err and the
// function's own throws stays `never`. The clause still governs the body's sink exactly
// as on a sync function; only the destination a caller observes moves.

// A clause on an `async fn` names the promise's rejection. `throws _` infers it from the
// body's exceptional exits, `throws E` fixes it, and either way the function's external
// type carries no throws clause of its own — calling it raises nothing.
func TestInferAsyncThrowsBecomesRejection(t *testing.T) {
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
			// Two throws on different paths union into the rejection, the same join a
			// sync `throws _` builds.
			name: "ThrowsOnBothBranchesUnionIntoTheRejection",
			src:  `async fn f(c: boolean) throws _ { if c { throw "a" } else { throw 5 } }`,
			want: `fn (c: boolean) -> Promise<never, 5 | "a">`,
		},
		{
			// The annotation names the external promise, rejection slot included, and the
			// inferred sink is constrained into it the way the body's return is
			// constrained into the payload.
			name: "AnnotatedRejectionSlotAdmitsTheInferredThrows",
			src:  `async fn f() -> Promise<never, string> throws _ { throw "boom" }`,
			want: `fn () -> Promise<never, string>`,
		},
		{
			name: "AnnotatedRejectionSlotAdmitsTheDeclaredClause",
			src:  `async fn f(c: boolean) -> Promise<number, string> throws string { if c { throw "boom" } return 5 }`,
			want: `fn (c: boolean) -> Promise<number, string>`,
		},
	})
}

// Calling an async function raises nothing — it returns a promise — so only awaiting the
// promise is the exceptional exit. The await constrains the promise's Err into the
// enclosing body's sink the way a throwing call constrains the callee's throws.
func TestInferAsyncRejectionSurfacesAtAwait(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// The caller only holds the promise, so its clause-less signature is fine.
			name: "HoldingThePromiseNeedsNoClause",
			src: `
				async fn f() throws string { throw "boom" }
				fn g() { val p = f() }
			`,
			binding: "g",
			want:    "fn () -> void",
		},
		{
			name: "AwaitUnderADeclaredClause",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() throws string { await f() }
			`,
			binding: "g",
			want:    "fn () -> Promise<void, string>",
		},
		{
			// The rejection propagates through `throws _` into the awaiting function's
			// own rejection, the re-raise chain the docs' fetchJSON example is built on.
			name: "AwaitUnderAWildcardClausePropagatesTheRejection",
			src: `
				async fn g(p: Promise<number, string>) throws _ { return await p }
			`,
			binding: "g",
			want:    "fn (p: Promise<number, string>) -> Promise<number, string>",
		},
		{
			// A `try` around the await catches the rejection, so the clause-less caller
			// is legal — the join with PR10b.
			name: "TryAroundAwaitCatchesTheRejection",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() { try { await f() } catch { e => 0 } }
			`,
			binding: "g",
			want:    "fn () -> Promise<void>",
		},
		{
			// Awaiting a promise whose Err is `never` records nothing, so the enclosing
			// clause stays untouched.
			name: "AwaitingANonRejectingPromiseNeedsNoClause",
			src: `
				async fn f() { return 5 }
				async fn g() { await f() }
			`,
			binding: "g",
			want:    "fn () -> Promise<void>",
		},
	})
}

// PR10's rules carry over unchanged: no clause means the body may not throw at all, and
// an awaited rejection needs a clause or a `try` the same way a throwing call does.
func TestInferAsyncThrowsStillRequiresAClause(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "ThrowInAClauselessAsyncFunction",
			src:      `async fn f() { throw "x" }`,
			wantErrs: []string{`1:22-1:25: cannot constrain "x" <: never`},
		},
		{
			name: "AwaitingARejectingPromiseInAClauselessAsyncFunction",
			src: `
				async fn f() throws string { throw "boom" }
				async fn g() { await f() }
			`,
			wantErrs: []string{"3:20-3:29: cannot constrain string <: never"},
		},
		{
			// A one-argument `Promise<T>` annotation reads its rejection slot as
			// `never`, so a clause beside it declares a rejection the annotation
			// cannot deliver. The body raises nothing either, so the clause is
			// flagged unused as well.
			name: "ClauseBesideAOneArgumentPromiseAnnotation",
			src:  `async fn f() -> Promise<number> throws string { return 5 }`,
			wantErrs: []string{
				"1:40-1:46: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
				"1:40-1:46: cannot constrain string <: never",
			},
		},
	})
}

// The fetchJSON shape from docs/06_error_handling.md: an async function that awaits a
// rejecting promise and raises its own error accumulates both into its rejection, and a
// caller that names every member in catch arms needs no clause of its own.
func TestInferAsyncFetchJSONExample(t *testing.T) {
	src := `
		declare fn fetch(url: string) -> Promise<string, "fetch-error">
		async fn fetchJSON(url: string, ok: boolean) throws _ {
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

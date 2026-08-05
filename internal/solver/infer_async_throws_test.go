package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// PR10c — async rejection. An `async fn` cannot raise: what its body throws is absorbed
// by the promise's rejection slot, so its signature is always `-> Promise<V, E>` and
// the E in that annotation is the rejection's only declaration surface. A written E is
// what the body's throws are checked against, `Promise<V>` forbids them, `Promise<_, _>`
// infers both arguments, and with no annotation the whole promise is inferred. The
// `throws` clause form belongs to sync functions; writing one on an `async fn` is
// rejected.

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

// The annotation's two slots each accept the `_` placeholder, so `Promise<_, _>` reads
// the payload off the body's returns and the rejection off its throws, and a written
// argument beside a `_` fixes only its own slot.
func TestInferAsyncRejectionAnnotated(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "WildcardSlotsInferBoth",
			src:  `async fn f() -> Promise<_, _> { throw "x" }`,
			want: `fn () -> Promise<never, "x">`,
		},
		{
			// A wildcard rejection slot nothing reached coalesces to `never`, so the
			// promise renders its one-argument form.
			name: "WildcardSlotsNothingThrownCoalescesAway",
			src:  `async fn f() -> Promise<_, _> { return 5 }`,
			want: "fn () -> Promise<5>",
		},
		{
			name: "WrittenRejectionBesideAWildcardPayload",
			src:  `async fn f(c: boolean) -> Promise<_, string> { if c { throw "boom" } return 5 }`,
			want: `fn (c: boolean) -> Promise<5, string>`,
		},
		{
			name: "WrittenRejectionWithANeverPayload",
			src:  `async fn f() -> Promise<never, string> { throw "boom" }`,
			want: `fn () -> Promise<never, string>`,
		},
	})
}

// A `throws` clause on an `async fn` is rejected: the function cannot raise, so there
// is no clause to declare. Recovery ignores the clause, so the rejection is still read
// from the annotation or inferred from the body and one diagnostic covers the fault.
func TestInferAsyncThrowsClauseRejected(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name: "DeclaredClause",
			src:  `async fn f() throws string { throw "boom" }`,
			wantErrs: []string{
				"1:21-1:27: async function cannot have a throws clause; declare the rejection type in the return type as Promise<..., E> or Promise<_, _>",
			},
		},
		{
			name: "WildcardClause",
			src:  `async fn f() throws _ { return 5 }`,
			wantErrs: []string{
				"1:21-1:22: async function cannot have a throws clause; declare the rejection type in the return type as Promise<..., E> or Promise<_, _>",
			},
		},
		{
			name: "ClauseBesideAPromiseAnnotation",
			src:  `async fn f() -> Promise<number> throws string { return 5 }`,
			wantErrs: []string{
				"1:40-1:46: async function cannot have a throws clause; declare the rejection type in the return type as Promise<..., E> or Promise<_, _>",
			},
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
			name: "HoldingThePromiseRaisesNothing",
			src: `
				async fn f() { throw "boom" }
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
				async fn f() { throw "boom" }
				async fn g() { await f() }
			`,
			binding: "g",
			want:    `fn () -> Promise<void, "boom">`,
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
				async fn f() { throw "boom" }
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

// A var joining several promises rejects when ANY member does, so an awaited join
// propagates the rejecting member's Err whichever bound the join recorded first.
func TestInferAwaitJoinedRejections(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "RejectingBranchSecond",
			src: `
				async fn g(c: boolean, p1: Promise<number>, p2: Promise<number, string>) {
					await (if c { p1 } else { p2 })
				}
			`,
			binding: "g",
			want:    "fn (c: boolean, p1: Promise<number>, p2: Promise<number, string>) -> Promise<void, string>",
		},
		{
			name: "RejectingBranchFirst",
			src: `
				async fn g(c: boolean, p1: Promise<number, string>, p2: Promise<number>) {
					await (if c { p1 } else { p2 })
				}
			`,
			binding: "g",
			want:    "fn (c: boolean, p1: Promise<number, string>, p2: Promise<number>) -> Promise<void, string>",
		},
		{
			// Two members rejecting with different types contribute both, so the
			// caller's rejection is their union.
			name: "BranchesRejectingWithDifferentTypesUnion",
			src: `
				async fn g(c: boolean, p1: Promise<number, "a">, p2: Promise<number, "b">) {
					await (if c { p1 } else { p2 })
				}
			`,
			binding: "g",
			want:    `fn (c: boolean, p1: Promise<number, "a">, p2: Promise<number, "b">) -> Promise<void, "a" | "b">`,
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
				async fn f() { throw "boom" }
				async fn g() -> Promise<number> {
					await f()
					return 5
				}
			`,
			wantErrs: []string{`4:6-4:15: cannot constrain "boom" <: never`},
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

// A rejecting promise renders its rejection type in diagnostics and in a canonical
// union, matching the printer's two-argument form.
func TestInferPromiseErrRendering(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// describe names the rejection type when the slot holds a concrete type,
			// so the mismatch reads as the promise the user wrote.
			name:     "DiagnosticNamesTheRejectingPromise",
			src:      `fn f(p: Promise<number, string>) { val x: number = p }`,
			wantErrs: []string{"1:52-1:53: cannot constrain Promise<number, string> <: number"},
		},
		{
			// A bad second argument reports once and recovers the slot to a fresh var,
			// keeping the Promise wrapper the way a bad payload does.
			name:     "BadRejectionArgumentRecoversTheWrapper",
			src:      `fn f(p: Promise<number, Bogus>) { }`,
			wantErrs: []string{"1:25-1:30: cannot find type `Bogus`"},
		},
	})
	runThrowsCases(t, []throwsCase{
		{
			// Two promises differing only in their rejection stay distinct union
			// members, so the canonical member order consults the rejection slot.
			name: "PromisesDifferingOnlyInRejectionStayDistinct",
			src: `
				fn f(c: boolean, p1: Promise<number, "a">, p2: Promise<number, "b">) {
					return if c { p1 } else { p2 }
				}
			`,
			want: `fn (c: boolean, p1: Promise<number, "a">, p2: Promise<number, "b">) -> Promise<number, "a"> | Promise<number, "b">`,
		},
	})
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

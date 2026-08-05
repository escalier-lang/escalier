package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// genCase is one accept case: src infers with no error, and the top-level binding named
// by `binding` renders as `want`. An empty binding names `f`, the name most cases
// declare, so only a case that inspects another binding spells one out.
type genCase struct {
	name    string
	src     string
	binding string
	want    string
}

// genErrCase is one reject case: src reports exactly the diagnostics in wantErrs, each
// rendered with its span. The full message is asserted, not a substring.
type genErrCase struct {
	name     string
	src      string
	wantErrs []string
}

func runGenCases(t *testing.T, tests []genCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := test.binding
			if binding == "" {
				binding = "f"
			}
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values[binding])
		})
	}
}

func runGenErrCases(t *testing.T, tests []genErrCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Len(t, errs, len(test.wantErrs))
			for i, want := range test.wantErrs {
				require.Equal(t, want, msgWithSpan(errs[i]))
			}
		})
	}
}

// A `gen fn` faces callers as a Generator: Y is the union of what the body yields, R is
// the body's return type, and N is what a `yield` expression evaluates to — `never`
// unless the annotation says otherwise, since a generator driven by a plain loop is
// never sent a value.
func TestInferGenExternalGeneratorFace(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "SingleYield",
			src:  `gen fn f() { yield 1 }`,
			want: `fn () -> Generator<1, void, never>`,
		},
		{
			name: "TwoYieldsUnion",
			src:  `gen fn f() { yield 1 yield "a" }`,
			want: `fn () -> Generator<1 | "a", void, never>`,
		},
		{
			name: "YieldAndReturn",
			src:  `gen fn f() { yield 1 return "done" }`,
			want: `fn () -> Generator<1, "done", never>`,
		},
		{
			// A bare `yield` yields `undefined`, matching JavaScript.
			name: "BareYield",
			src:  `gen fn f() { yield }`,
			want: `fn () -> Generator<undefined, void, never>`,
		},
		{
			name: "GenFuncExpr",
			src:  `val f = gen fn () { yield 1 }`,
			want: `fn () -> Generator<1, void, never>`,
		},
		{
			// The annotation names the external Generator; the body checks against its
			// slots and the annotation is presented as the external type.
			name: "AnnotatedGenerator",
			src:  `gen fn f() -> Generator<number, string, never> { yield 1 return "done" }`,
			want: `fn () -> Generator<number, string, never>`,
		},
		{
			// The annotation's Next slot is what a `yield` evaluates to, the value a
			// caller passes back in through `next(v)`.
			name: "AnnotatedNextTypesTheYield",
			src: `
				gen fn f() -> Generator<number, "done", string> {
					val x: string = yield 1
					return "done"
				}
			`,
			want: `fn () -> Generator<number, "done", string>`,
		},
	})
}

// An `async gen fn` faces callers as an AsyncGenerator, not a Promise: the async wrap
// never applies to a generator, and `await` is legal in its body.
func TestInferAsyncGen(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "AsyncGenIsAsyncGenerator",
			src:  `async gen fn f() { yield 1 }`,
			want: `fn () -> AsyncGenerator<1, void, never>`,
		},
		{
			name: "AwaitInsideAsyncGen",
			src:  `async gen fn f(p: Promise<number>) { yield await p }`,
			want: `fn (p: Promise<number>) -> AsyncGenerator<number, void, never>`,
		},
	})
}

// A yield outside a generator body is rejected by the walk, not the type rule: the
// operand is still walked, and the enclosing function — the one to mark `gen` — is
// related. A closure inside a generator is not itself a generator, so a yield inside
// one is rejected too.
func TestInferYieldRequiresGenContext(t *testing.T) {
	runGenErrCases(t, []genErrCase{
		{
			name:     "YieldInAPlainFunction",
			src:      `fn f() { yield 1 }`,
			wantErrs: []string{"1:10-1:17: yield can only be used inside a generator function"},
		},
		{
			name:     "YieldFromInAPlainFunction",
			src:      `fn f() { yield from [1, 2] }`,
			wantErrs: []string{"1:10-1:27: yield from can only be used inside a generator function"},
		},
		{
			// The closure opens its own function context with the marker clear, so its
			// `yield` is rejected. That same scoping means the yield does not count for
			// the enclosing `gen fn`, which therefore never yields and warns as well.
			name: "YieldInAClosureInsideAGenBody",
			src:  `gen fn f() { val g = fn () { yield 1 } }`,
			wantErrs: []string{
				"1:30-1:37: yield can only be used inside a generator function",
				"1:1-1:41: the body never yields, so this returns an empty Generator; add a `yield` or drop the `gen`",
			},
		},
		{
			// `gen` is the marker, so a plain method whose body yields is rejected the
			// way a plain function is. Writing `gen m(self)` is how a method opts in.
			name:     "YieldInAPlainMethod",
			src:      `class C { m(self) { yield 1 } }`,
			wantErrs: []string{"1:21-1:28: yield can only be used inside a generator function"},
		},
	})
}

// A `gen fn`'s return annotation names the external Generator, so a bare type and a
// generator of the wrong async-ness are both rejected, and the body still checks under
// the inferred seeding.
func TestInferGenReturnAnnotationShape(t *testing.T) {
	runGenErrCases(t, []genErrCase{
		{
			name:     "BareAnnotationRejected",
			src:      `gen fn f() -> number { yield 1 }`,
			wantErrs: []string{"1:15-1:21: generator function return type must be a Generator; write Generator<...>"},
		},
		{
			name:     "AsyncGeneratorOnSyncGenRejected",
			src:      `gen fn f() -> AsyncGenerator<number, undefined, never> { yield 1 }`,
			wantErrs: []string{"1:15-1:55: generator function return type must be a Generator; write Generator<...>"},
		},
		{
			name:     "GeneratorOnAsyncGenRejected",
			src:      `async gen fn f() -> Generator<number, undefined, never> { yield 1 }`,
			wantErrs: []string{"1:21-1:56: async generator function return type must be an AsyncGenerator; write AsyncGenerator<...>"},
		},
		{
			// The annotation's Yield slot is the sink each yield is checked against, so a
			// mismatched yield is blamed at its own site.
			name:     "YieldAgainstDeclaredYieldSlot",
			src:      `gen fn f() -> Generator<string, undefined, never> { yield 1 return undefined }`,
			wantErrs: []string{"1:59-1:60: cannot constrain 1 <: string"},
		},
		{
			name:     "ReturnAgainstDeclaredRetSlot",
			src:      `gen fn f() -> Generator<number, string, never> { yield 1 return 2 }`,
			wantErrs: []string{"1:65-1:66: cannot constrain 2 <: string"},
		},
	})
}

// The throws sink is untouched by generator-ness: a clause-less gen fn raises nothing,
// so a throw in its body is rejected at the throw, exactly as in a plain function, and a
// declared clause reaches the generator's own type.
//
// A generator carries its body's raises on its own signature, which over-approximates:
// advancing the generator is what raises, so a caller that only obtains it is asked to
// handle an exception it cannot yet observe. An `async fn` moves its raises into the
// promise's rejection slot, and a generator has no such slot to move them into. Keeping
// them on the signature errs in the safe direction — dropping them instead would let the
// raise escape a clause-less caller that iterates.
func TestInferGenThrowsStillChecked(t *testing.T) {
	runGenErrCases(t, []genErrCase{
		{
			name:     "ThrowInAClauselessGenFn",
			src:      `gen fn f() { yield 1 throw "boom" }`,
			wantErrs: []string{`1:28-1:34: cannot constrain "boom" <: never`},
		},
	})
	runGenCases(t, []genCase{
		{
			// The body always leaves along the exceptional edge, so its normal return —
			// the Generator's Ret slot — is `never`.
			name: "GenFnWithThrowsClause",
			src:  `gen fn f() throws _ { yield 1 throw "boom" }`,
			want: `fn () -> Generator<1, never, never> throws "boom"`,
		},
	})
}

// Generator subtyping: Yield and Ret are covariant; Next is contravariant, since it is
// the value a caller sends back in through `next(v)`.
func TestGeneratorSubtyping(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "CovariantYieldAndRet",
			src: `
				gen fn g() { yield 1 return "done" }
				val f: fn () -> Generator<number, string, never> = g
			`,
			want: `fn () -> Generator<number, string, never>`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			name: "YieldSlotMismatchAtUse",
			src: `
				gen fn g() { yield "a" return true }
				val h: fn () -> Generator<number, boolean, never> = g
			`,
			wantErrs: []string{`3:57-3:58: cannot constrain "a" <: number`},
		},
		{
			name: "SyncGeneratorIsNotAsync",
			src: `
				gen fn g() { yield 1 return true }
				val h: fn () -> AsyncGenerator<number, boolean, never> = g
			`,
			// The yield slot renders in describe's raw mid-constrain form, so g's
			// still-unsolved yield variable shows as `t4`.
			wantErrs: []string{"3:62-3:63: cannot constrain Generator<t4, true, never> <: AsyncGenerator<number, boolean, never>"},
		},
	})
}

// `yield from` forwards a delegate's yields rather than yielding the delegate itself,
// so it needs the iteration rules and is reported until those land. Treating it as a
// plain yield would put the iterable in the sink instead of its elements.
//
// The delegate is still walked before the rejection is reported, so a mistake inside it
// is diagnosed instead of being swallowed by the rejection.
func TestInferYieldFromIsUnsupported(t *testing.T) {
	runGenErrCases(t, []genErrCase{
		{
			name:     "DelegationReported",
			src:      `gen fn f() { yield from [1, 2] }`,
			wantErrs: []string{"1:14-1:31: Unsupported: yield from"},
		},
		{
			name: "DelegateErrorsStillSurface",
			src:  `gen fn f() { yield from missing() }`,
			wantErrs: []string{
				"1:25-1:32: Unknown identifier: missing",
				"1:14-1:34: Unsupported: yield from",
			},
		},
	})
}

// A `gen fn` whose body never yields still returns a generator, but one that finishes on
// the first `next()` without producing a value, so the marker gives the caller nothing.
// The warning says so while the function still types as the generator it returns — it is
// well-typed, just pointless, which is why it warns rather than errors.
func TestInferGenWithoutYieldWarns(t *testing.T) {
	runGenErrCases(t, []genErrCase{
		{
			name:     "SyncGenNeverYields",
			src:      `gen fn f() { return "done" }`,
			wantErrs: []string{"1:1-1:29: the body never yields, so this returns an empty Generator; add a `yield` or drop the `gen`"},
		},
		{
			// An `async gen fn` returns an AsyncGenerator, so the message names that form.
			name:     "AsyncGenNeverYields",
			src:      `async gen fn f() { return 1 }`,
			wantErrs: []string{"1:1-1:30: the body never yields, so this returns an empty AsyncGenerator; add a `yield` or drop the `gen`"},
		},
		{
			// A `gen` method is measured the same way, blamed at the member.
			name:     "GenMethodNeverYields",
			src:      `class C { gen m(self) { return 1 } }`,
			wantErrs: []string{"1:11-1:35: the body never yields, so this returns an empty Generator; add a `yield` or drop the `gen`"},
		},
	})

	// The warning does not change the type: a yield-less generator still faces callers as
	// a Generator whose Yield slot coalesces to `never`.
	values, _, errs := inferSource(t, `gen fn f() { return "done" }`)
	require.Len(t, errs, 1)
	require.True(t, isWarning(errs[0]))
	require.Equal(t, `fn () -> Generator<never, "done", never>`, values["f"])

	// A body that yields reports nothing at all.
	_, _, yielding := inferSource(t, `gen fn f() { yield 1 }`)
	require.Empty(t, yielding)

	// Delegating counts as yielding, so a body whose only yield is a `yield from` does not
	// draw this warning. It still draws the staged unsupported report, since delegation
	// itself lands later, so the assertion is that the warning is absent rather than that
	// nothing is reported.
	_, _, delegating := inferSource(t, `gen fn f() { yield from [1, 2] }`)
	require.Len(t, delegating, 1)
	require.Equal(t, "1:14-1:31: Unsupported: yield from", msgWithSpan(delegating[0]))

	// A bodyless `declare gen fn` has no body to measure, so it is never flagged.
	_, _, declared := inferSource(t, `declare gen fn f() -> Generator<number, undefined, never>`)
	require.Empty(t, declared)
}

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

// A `gen fn` faces callers as a Generator. Y is the union of what the body yields, and
// R is the body's return type. N is what a `yield` expression evaluates to, the value a
// caller sends in through `next(v)`. An annotation declares N; otherwise it is
// `unknown`, the neutral choice for that contravariant slot, so a caller may send any
// value.
func TestInferGenExternalGeneratorFace(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "SingleYield",
			src:  `gen fn f() { yield 1 }`,
			want: `fn () -> Generator<1, undefined, unknown>`,
		},
		{
			name: "TwoYieldsUnion",
			src:  `gen fn f() { yield 1 yield "a" }`,
			want: `fn () -> Generator<1 | "a", undefined, unknown>`,
		},
		{
			name: "YieldAndReturn",
			src:  `gen fn f() { yield 1 return "done" }`,
			want: `fn () -> Generator<1, "done", unknown>`,
		},
		{
			// A bare `yield` yields `undefined`, matching JavaScript.
			name: "BareYield",
			src:  `gen fn f() { yield }`,
			want: `fn () -> Generator<undefined, undefined, unknown>`,
		},
		{
			name: "GenFuncExpr",
			src:  `val f = gen fn () { yield 1 }`,
			want: `fn () -> Generator<1, undefined, unknown>`,
		},
		{
			// Without an annotation the Next slot is `unknown`, so a `yield` evaluates
			// to `unknown`. Returning that value puts `unknown` in the Ret slot too.
			name: "InferredNextTypesTheYield",
			src:  `gen fn f() { val x = yield 1 return x }`,
			want: `fn () -> Generator<1, unknown, unknown>`,
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

// An un-annotated Next slot is the concrete type `unknown`, not an inference variable,
// so what the body does with a `yield` cannot narrow it. Writing `_` in the slot asks
// for the variable instead, and then the body's uses do infer it.
//
// The default is deliberate. Seeding every generator's Next with a fresh variable would
// infer `gen fn f() { val x = yield 1 return x }` as `fn <T0>() -> Generator<1, T0, T0>`,
// a generic where a reader expects a plain generator. `_` keeps that available for an
// author who wants it without imposing it on every `gen fn`.
func TestInferGenNextSlotInference(t *testing.T) {
	runGenCases(t, []genCase{
		{
			// The `_` is a fresh variable, so annotating the binding the yield feeds
			// puts `string` in the slot.
			name: "WildcardNextInfersFromABodyUse",
			src:  `gen fn f() -> Generator<number, undefined, _> { val x: string = yield 1 return undefined }`,
			want: `fn () -> Generator<number, undefined, string>`,
		},
		{
			// A body that returns what it was sent relates the two slots, and with both
			// written `_` the relation generalizes: whatever a caller sends comes back
			// out.
			name: "WildcardNextAndRetGeneralizeTogether",
			src:  `gen fn f() -> Generator<number, _, _> { val x = yield 1 return x }`,
			want: `fn <T0>() -> Generator<number, T0, T0>`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// Without `_` the slot is `unknown`, so a body that wants a narrower type
			// out of its `yield` has to say so in the annotation.
			name:     "UnannotatedNextDoesNotInferFromABodyUse",
			src:      `gen fn f() { val x: string = yield 1 }`,
			wantErrs: []string{"1:30-1:37: cannot constrain unknown <: string"},
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
			want: `fn () -> AsyncGenerator<1, undefined, unknown>`,
		},
		{
			name: "AwaitInsideAsyncGen",
			src:  `async gen fn f(p: Promise<number>) { yield await p }`,
			want: `fn (p: Promise<number>) -> AsyncGenerator<number, undefined, unknown>`,
		},
	})
}

// `yield from g` forwards the delegate's yields into the enclosing generator and
// evaluates to the delegate's return type — what the delegating body sees once the
// delegate is exhausted. A structural iterable like a tuple forwards its element union
// and finishes with `undefined`.
func TestInferYieldFrom(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "DelegateToTuple",
			src:  `gen fn f() { yield from [1, 2] }`,
			want: `fn () -> Generator<1 | 2, undefined, unknown>`,
		},
		{
			name: "DelegateToGeneratorForwardsYieldsAndReturns",
			src: `
				gen fn g() { yield 1 return "r" }
				gen fn f() { yield "a" return yield from g() }
			`,
			want: `fn () -> Generator<1 | "a", "r", unknown>`,
		},
		{
			// A union delegate is resolved branch by branch, so the delegation keeps
			// both halves of each branch. The yields union to `number | boolean` and the
			// return types union to `string | number`, which is what `return yield from g`
			// puts in the delegating generator's Ret slot.
			name: "DelegateToAUnionOfGenerators",
			src: `
				gen fn f(g: Generator<number, string, never> | Generator<boolean, number, never>) {
					return yield from g
				}
			`,
			want: `fn (g: Generator<number, string, never> | Generator<boolean, number, never>) -> Generator<number | boolean, number | string, never>`,
		},
		{
			// A tuple branch carries no return value, so it contributes `undefined` to
			// the union the delegation evaluates to.
			name: "DelegateToAUnionOfAGeneratorAndATuple",
			src: `
				gen fn f(g: Generator<number, string, never> | [boolean]) {
					return yield from g
				}
			`,
			want: `fn (g: [boolean] | Generator<number, string, never>) -> Generator<number | boolean, string | undefined, never>`,
		},
		{
			// An async generator is a legal delegate from an async generator body, and a
			// union of them is too, one branch at a time.
			name: "AsyncGenDelegatesToAUnionOfAsyncGenerators",
			src: `
				async gen fn f(g: AsyncGenerator<number, string, never> | AsyncGenerator<boolean, number, never>) {
					return yield from g
				}
			`,
			want: `fn (g: AsyncGenerator<number, string, never> | AsyncGenerator<boolean, number, never>) -> AsyncGenerator<number | boolean, number | string, never>`,
		},
		{
			// Tree-walk delegation is the main use of `yield from`, so a generator
			// delegating to itself has to work. Its own return type is still unsolved at
			// the delegation, so the rule states a Generator requirement rather than
			// reading the operand's shape.
			name: "SelfRecursiveDelegation",
			src:  `gen fn f() { yield 1 yield from f() }`,
			want: `fn () -> Generator<1, undefined, unknown>`,
		},
		{
			// Two generators delegating to each other reach a fixed point, so both
			// yield the union across the cycle.
			name: "MutuallyRecursiveDelegation",
			src: `
				gen fn a() { yield 1 yield from b() }
				gen fn b() { yield "x" yield from a() }
			`,
			binding: "a",
			want:    `fn () -> Generator<1 | "x", undefined, unknown>`,
		},
		{
			// Delegating forwards a sent value into the delegate, so the delegator can
			// only accept what the delegate accepts. Its inferred Next is the
			// delegate's rather than the `unknown` a non-delegating body gets.
			name: "DelegationNarrowsTheInferredNext",
			src: `
				gen fn g() -> Generator<number, string, string> { yield 1 return "r" }
				gen fn f() { return yield from g() }
			`,
			want: `fn () -> Generator<number, string, string>`,
		},
		{
			// Two delegates must both accept whatever reaches them, so the Next slots
			// meet. No value is both a `string` and a `number`, so nothing can be sent
			// into this generator. TODO(#927) is what leaves the meet rendered as
			// `number & string` instead of collapsing it to `never`.
			name: "TwoDelegatesMeetTheirNextSlots",
			src: `
				gen fn a() -> Generator<number, string, string> { yield 1 return "r" }
				gen fn b() -> Generator<number, string, number> { yield 2 return "r" }
				gen fn f() { yield from a() yield from b() }
			`,
			want: `fn () -> Generator<number, undefined, number & string>`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// A declared Next is checked at the delegation instead of collected. A
			// generator promising to accept anything cannot forward into one that
			// accepts only strings.
			name: "DeclaredNextMustReachTheDelegate",
			src: `
				gen fn g() -> Generator<number, string, string> { yield 1 return "r" }
				gen fn f() -> Generator<number, string, unknown> { return yield from g() }
			`,
			wantErrs: []string{`3:63-3:77: cannot constrain unknown <: string`},
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// The delegate is walked before its shape is read, so an error inside it is
			// reported at its own site. The ErrorType that walk yields then absorbs, so
			// the delegation adds no second diagnostic on top.
			name:     "ABrokenDelegateReportsOnlyItsOwnError",
			src:      `gen fn f() { yield from missing() }`,
			wantErrs: []string{"1:25-1:32: Unknown identifier: missing"},
		},
		{
			// A union delegate is legal only when every branch is. A sync generator body
			// cannot drive an async generator, so the async branch rejects the whole
			// union and the error names the union, not the branch.
			name: "SyncGenDelegatingToAUnionWithAnAsyncBranchRejected",
			src: `
				gen fn f(g: Generator<number, string, never> | AsyncGenerator<boolean, number, never>) {
					return yield from g
				}
			`,
			wantErrs: []string{"3:24-3:25: Generator<number, string, never> | AsyncGenerator<boolean, number, never> is not iterable"},
		},
	})
}

// A sync generator is iterable, so a `for` loop binds its variable at the Yield slot;
// an async generator is iterable only under `for await`.
func TestInferGenIteration(t *testing.T) {
	runGenCases(t, []genCase{
		{
			name: "ForInOverGenerator",
			src: `
				gen fn g() { yield 1 yield 2 }
				fn f() { for x in g() { return x } }
			`,
			want: `fn () -> 1 | 2`,
		},
		{
			name: "ForAwaitOverAsyncGenerator",
			src: `
				async gen fn g() { yield 1 }
				async fn f() { for await x in g() { return x } }
			`,
			want: `fn () -> Promise<1>`,
		},
		{
			// A union of async generators is async-iterable, binding the loop variable
			// at the union of what its branches yield — the same branch-wise walk the
			// sync path applies to a union of generators.
			name: "ForAwaitOverAUnionOfAsyncGenerators",
			src: `
				async fn f(g: AsyncGenerator<number, undefined, never> | AsyncGenerator<string, undefined, never>) {
					for await x in g { return x }
				}
			`,
			want: `fn (g: AsyncGenerator<number, undefined, never> | AsyncGenerator<string, undefined, never>) -> Promise<number | string>`,
		},
		{
			name: "ForInOverAUnionOfGenerators",
			src: `
				fn f(g: Generator<number, undefined, never> | Generator<string, undefined, never>) {
					for x in g { return x }
				}
			`,
			want: `fn (g: Generator<number, undefined, never> | Generator<string, undefined, never>) -> number | string`,
		},
		{
			// A class method marked `gen` is a generator the same way a `gen fn` is, so
			// iterating its result binds at what the body yields.
			name: "GenMethodOnAClass",
			src: `
				class C { gen m(self) { yield 1 } }
				fn f(c: C) { for x in c.m() { return x } }
			`,
			want: `fn (c: C) -> 1`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// An async generator has no sync iterator, so a plain `for` rejects it.
			name: "ForInOverAsyncGeneratorRejected",
			src: `
				declare fn g() -> AsyncGenerator<number, undefined, never>
				fn f(a: AsyncGenerator<number, undefined, never>) { for x in a { } }
			`,
			wantErrs: []string{"3:66-3:67: AsyncGenerator<number, undefined, never> is not iterable"},
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
		{
			name:     "YieldFromNonIterable",
			src:      `gen fn f() { yield from 5 }`,
			wantErrs: []string{"1:25-1:26: 5 is not iterable"},
		},
	})
}

// A generator does not raise at the call: obtaining one runs no body code, so what the
// body raises lands in the generator's Throws slot and the function's own throws stays
// `never`. Advancing it is what raises, so iterating or delegating reads the slot back
// into the enclosing sink. This mirrors how an `async fn` moves its raises onto the
// promise it returns.
func TestInferGenRaises(t *testing.T) {
	runGenCases(t, []genCase{
		{
			// With no annotation the raise is inferred into the slot, the way an
			// annotation-less `async fn` infers its rejection.
			name: "RaiseInferredIntoTheSlot",
			src:  `gen fn f() { yield 1 throw "boom" }`,
			want: `fn () -> Generator<1, never, unknown, "boom">`,
		},
		{
			// A written fourth argument seeds the sink, so each `throw` is checked
			// against it and the annotation is presented as the external type.
			name: "RaiseDeclaredInTheAnnotation",
			src:  `gen fn f() -> Generator<1, never, never, "boom"> { yield 1 throw "boom" }`,
			want: `fn () -> Generator<1, never, never, "boom">`,
		},
		{
			// A generator that cannot raise leaves the slot at `never` and renders three
			// arguments, the same suppression `Promise<T>` gets.
			name: "NonRaisingGeneratorRendersThreeArgs",
			src:  `gen fn f() { yield 1 }`,
			want: `fn () -> Generator<1, undefined, unknown>`,
		},
		{
			// Obtaining a generator runs none of its body, so a clause-less caller needs
			// no clause of its own.
			name: "ObtainingAGeneratorRaisesNothing",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() { val it = g() }
			`,
			want: `fn () -> undefined`,
		},
		{
			// Iterating advances the generator, so the raise reaches the caller's clause.
			name: "IteratingCarriesTheRaiseToTheCaller",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() throws _ { for x in g() { } }
			`,
			want: `fn () -> undefined throws "boom"`,
		},
		{
			// Delegating advances the delegate, so it carries the raise the same way.
			name: "DelegatingCarriesTheRaiseToTheDelegator",
			src: `
				gen fn g() { yield 1 throw "boom" }
				gen fn f() { yield from g() }
			`,
			want: `fn () -> Generator<1, undefined, unknown, "boom">`,
		},
		{
			// Calling `next` advances the generator by hand, the third way to advance one,
			// so it carries the raise the way iterating and delegating do.
			name: "AdvancingWithNextCarriesTheRaiseToTheCaller",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() throws _ { val it = g() return it.next() }
			`,
			want: `fn () -> {value: 1, done: false} throws "boom"`,
		},
		{
			// An async generator's advance returns a promise that rejects with what the
			// body raises, so the raise reaches whoever awaits the result.
			name: "AwaitingNextCarriesTheRaiseAsARejection",
			src: `
				async gen fn g() { yield 1 throw "boom" }
				async fn f() { val it = g() return await it.next() }
			`,
			want: `fn () -> Promise<{value: 1, done: false}, "boom">`,
		},
		{
			// The promise is what carries the rejection, so calling `next` without awaiting
			// it raises nothing into the calling body.
			name: "CallingNextOnAnAsyncGeneratorRaisesNothing",
			src: `
				async gen fn g() { yield 1 throw "boom" }
				fn f() { val it = g() return it.next() }
			`,
			want: `fn () -> Promise<{value: 1, done: false}, "boom">`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// The raise type belongs in the return annotation, so a `throws` clause on a
			// generator is rejected the way one on an `async fn` is.
			name:     "ThrowsClauseRejected",
			src:      `gen fn f() throws _ { yield 1 throw "boom" }`,
			wantErrs: []string{"1:19-1:20: generator function cannot have a throws clause; declare the raise type in the return type as Generator<..., E>"},
		},
		{
			// A declared raise slot is the sink each `throw` is checked against, so a
			// mismatched raise is blamed at its own site.
			name:     "ThrowAgainstDeclaredRaiseSlot",
			src:      `gen fn f() -> Generator<1, never, never, "boom"> { yield 1 throw "other" }`,
			wantErrs: []string{`1:66-1:73: cannot constrain "other" <: "boom"`},
		},
		{
			// A clause-less caller that iterates a raising generator is asked to handle it.
			name: "IteratingWithoutAClauseRejected",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() { for x in g() { } }
			`,
			wantErrs: []string{`3:23-3:26: cannot constrain "boom" <: never`},
		},
		{
			// A clause-less caller that advances a raising generator by hand is asked to
			// handle it too, so `next` is not a hole the other two advance paths close.
			name: "AdvancingWithoutAClauseRejected",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() { val it = g() return it.next() }
			`,
			wantErrs: []string{`3:34-3:43: cannot constrain "boom" <: never`},
		},
	})
}

// A caller advances a generator by hand through `next`. It is declared as an overload set
// of two arms, one taking the sent value and one omitting it, and the argumentless arm is
// offered only when omitting the argument is sound.
func TestInferGenNext(t *testing.T) {
	runGenCases(t, []genCase{
		{
			// Advancing either produces a yielded value or reports the generator finished
			// with its return value, so the result is one arm per outcome tagged by `done`.
			// The two arms keep the yield type and the return type distinct.
			name: "NextResultTagsYieldAndReturnApart",
			src: `
				gen fn g() { yield 1 return "done" }
				fn f() { val it = g() return it.next() }
			`,
			want: `fn () -> {value: 1, done: false} | {value: "done", done: true}`,
		},
		{
			// A generator that always throws never finishes normally, so `never` in its Ret
			// slot drops the finished arm rather than carrying one no value inhabits.
			name: "NextResultDropsAnUninhabitedArm",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() throws _ { val it = g() return it.next() }
			`,
			want: `fn () -> {value: 1, done: false} throws "boom"`,
		},
		{
			// Reading `value` off the result reaches both arms, since reducing the union to
			// one arm by testing its `done` needs narrowing on a literal-tagged union.
			name: "ReadingValueOffTheResultJoinsBothArms",
			src: `
				gen fn g() { yield 1 return "done" }
				fn f() { val r = g().next() return r.value }
			`,
			want: `fn () -> 1 | "done"`,
		},
		{
			// The sent value is what a `yield` expression in the body evaluates to, so the
			// one-argument arm takes the generator's Next slot.
			name: "NextAcceptsTheSentValue",
			src: `
				gen fn g() -> Generator<number, string, string> { yield 1 return "x" }
				fn f() { val it = g() return it.next("a") }
			`,
			want: `fn () -> {value: number, done: false} | {value: string, done: true}`,
		},
		{
			// An inferred Next slot is `unknown`, which accepts `undefined`, so the
			// argumentless arm stays available for a generator that declares no send type.
			name: "ArgumentlessNextOnAnInferredSendType",
			src: `
				gen fn g() { yield 1 }
				fn f() { val it = g() return it.next() }
			`,
			want: `fn () -> {value: 1, done: false} | {value: undefined, done: true}`,
		},
		{
			// A generator obtained without an intervening binding advances the same way.
			name: "NextOnACallResult",
			src: `
				gen fn g() { yield 1 }
				fn f() { return g().next() }
			`,
			want: `fn () -> {value: 1, done: false} | {value: undefined, done: true}`,
		},
		{
			// Two calls to one generator leave the receiver holding two lower bounds that
			// differ only in their fresh slot variables, so the member read goes through
			// their join rather than declining for want of a single bound.
			name: "NextOnTwoCallsOfOneGenerator",
			src: `
				gen fn g() { yield 1 }
				fn f(b: boolean) { val it = if b { g() } else { g() } return it.next() }
			`,
			want: `fn (b: boolean) -> {value: 1, done: false} | {value: undefined, done: true}`,
		},
		{
			// A receiver holding two different generators advances through their join, so
			// the result reports what either may yield.
			name: "NextOnAJoinOfTwoGenerators",
			src: `
				gen fn g() { yield 1 }
				gen fn h() { yield "a" }
				fn f(b: boolean) { val it = if b { g() } else { h() } return it.next() }
			`,
			want: `fn (b: boolean) -> {value: 1 | "a", done: false} | {value: undefined, done: true}`,
		},
		{
			// The join's raise is the union of what either generator raises, so a caller
			// that advances it needs a clause covering both.
			name: "NextOnAJoinCarriesEitherRaise",
			src: `
				gen fn g() { yield 1 throw "boom" }
				gen fn h() { yield "a" throw 5 }
				fn f(b: boolean) throws _ { val it = if b { g() } else { h() } return it.next() }
			`,
			want: `fn (b: boolean) -> {value: 1 | "a", done: false} throws 5 | "boom"`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// Omitting the argument sends `undefined` at runtime, so a generator whose body
			// reads its sent value as a string does not offer the argumentless arm. Leaving
			// it available would let that body receive `undefined`.
			name: "ArgumentlessNextRejectedOnANarrowSendType",
			src: `
				gen fn g() -> Generator<number, string, string> { yield 1 return "x" }
				fn f() { val it = g() return it.next() }
			`,
			wantErrs: []string{`3:34-3:43: Not enough arguments: expected at least 1, but got 0`},
		},
		{
			// A declared send type has to stand for whatever a caller picks, so omitting the
			// argument is rejected there too. `undefined` is assignable to the parameter's var
			// only by narrowing it, which is not the same as the parameter already admitting
			// `undefined`.
			name: "ArgumentlessNextRejectedOnADeclaredSendTypeParam",
			src: `
				fn drive<T>(it: Generator<number, string, T>) { return it.next() }
			`,
			wantErrs: []string{`2:60-2:69: Not enough arguments: expected at least 1, but got 0`},
		},
		{
			// A generator carries `next` and nothing else, so any other member names the
			// missing property rather than failing on the receiver.
			name: "UnknownGeneratorMember",
			src: `
				gen fn g() { yield 1 }
				fn f() { val it = g() return it.done }
			`,
			wantErrs: []string{`3:37-3:41: object is missing property: done`},
		},
		{
			// A receiver that may hold something other than a generator is not advanced
			// through the generator member list, so the non-generator part is still rejected
			// rather than silently accepted.
			name: "NextOnAReceiverThatMayNotBeAGenerator",
			src: `
				gen fn g() { yield 1 }
				fn f(b: boolean) { val it = if b { g() } else { 5 } return it.next() }
			`,
			wantErrs: []string{
				`3:64-3:71: cannot constrain Generator<t8, undefined, unknown> <: object`,
				`3:64-3:71: cannot constrain 5 <: object`,
			},
		},
		{
			// A call no arm accepts is still an exceptional exit, since nothing shows it
			// cannot raise. The no-match error stands alone rather than being joined by an
			// unused-clause warning against the clause the call needs.
			name: "NoMatchingNextArmReportsOnlyTheNoMatch",
			src: `
				gen fn g() { yield 1 throw "boom" }
				fn f() throws string { val it = g() return it.next(1, 2) }
			`,
			wantErrs: []string{
				"3:48-3:61: No matching overload for this call\n" +
					`  fn () -> {value: 1, done: false} throws "boom"` + "\n" +
					`  fn (value: unknown) -> {value: 1, done: false} throws "boom"`,
			},
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
		{
			// Next is contravariant, so an inferred `unknown` in that slot satisfies
			// every annotation. A generator nobody sends values to fills a binding that
			// promises to accept a string.
			name: "ContravariantNextAcceptsANarrowerAnnotation",
			src: `
				gen fn g() { yield 1 return "done" }
				val f: fn () -> Generator<number, string, string> = g
			`,
			want: `fn () -> Generator<number, string, string>`,
		},
	})
	runGenErrCases(t, []genErrCase{
		{
			// The reverse direction is rejected. A generator whose body reads its sent
			// value as a string cannot fill a binding that lets callers send anything.
			name: "ContravariantNextRejectsAWiderAnnotation",
			src: `
				gen fn g() -> Generator<number, string, string> { yield 1 return "x" }
				val f: fn () -> Generator<number, string, unknown> = g
			`,
			wantErrs: []string{`3:58-3:59: cannot constrain unknown <: string`},
		},
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
			wantErrs: []string{"3:62-3:63: cannot constrain Generator<t4, true, unknown> <: AsyncGenerator<number, boolean, never>"},
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
	require.Equal(t, `fn () -> Generator<never, "done", unknown>`, values["f"])

	// A body that yields reports nothing at all.
	_, _, yielding := inferSource(t, `gen fn f() { yield 1 }`)
	require.Empty(t, yielding)

	// Delegating counts as yielding, so a body whose only yield is a `yield from` does not
	// draw this warning.
	_, _, delegating := inferSource(t, `gen fn f() { yield from [1, 2] }`)
	require.Empty(t, delegating)

	// A bodyless `declare gen fn` has no body to measure, so it is never flagged.
	_, _, declared := inferSource(t, `declare gen fn f() -> Generator<number, undefined, never>`)
	require.Empty(t, declared)
}

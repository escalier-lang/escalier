package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// throwsCase is one accept case: src infers with no error, and the top-level binding
// named by `binding` renders as `want`. An empty binding names `f`, the name most cases
// declare, so only a case that inspects another binding spells one out.
type throwsCase struct {
	name    string
	src     string
	binding string
	want    string
}

// throwsErrCase is one reject case: src reports exactly the diagnostics in wantErrs, each
// rendered with its span. The full message is asserted, not a substring.
type throwsErrCase struct {
	name     string
	src      string
	wantErrs []string
}

func runThrowsCases(t *testing.T, tests []throwsCase) {
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

func runThrowsErrCases(t *testing.T, tests []throwsErrCase) {
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

// Omitting the `throws` clause declares that a function raises nothing, so a body that
// does raise is rejected at the site that raises rather than at the whole function. This
// mirrors the old checker, where inferFuncSig gives a clause-less signature `never`.
// Writing `throws never` is not how a non-throwing function is spelled; omitting the
// clause is.
func TestInferThrowsRequiresAClause(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "ThrowInAClauselessFunction",
			src:      `fn f() { throw "boom" }`,
			wantErrs: []string{`1:16-1:22: cannot constrain "boom" <: never`},
		},
		{
			name: "CallToAThrowingCalleeFromAClauselessCaller",
			src: `
				fn a() throws string { throw "boom" }
				fn f() { a() }
			`,
			wantErrs: []string{"3:14-3:17: cannot constrain string <: never"},
		},
		{
			name:     "ThrowInAClauselessFunctionExpression",
			src:      `val f = fn () { throw "x" }`,
			wantErrs: []string{`1:23-1:26: cannot constrain "x" <: never`},
		},
		{
			name:     "ThrowInAClauselessMethod",
			src:      `class C { m(self) { throw "x" } }`,
			wantErrs: []string{`1:27-1:30: cannot constrain "x" <: never`},
		},
		{
			// A clause on the ANNOTATION does not reach an un-annotated function
			// expression: the lambda's own signature has no clause, so its sink is
			// `never` and its throw is rejected before the annotation is consulted. The
			// old checker behaves the same way, since inferFuncSig reads only the
			// signature. Writing `fn () throws _ { … }` is how the lambda opts in.
			name:     "AnnotationClauseDoesNotReachAnUnannotatedLambda",
			src:      `val f: fn() -> never throws string = fn () { throw "x" }`,
			wantErrs: []string{`1:52-1:55: cannot constrain "x" <: never`},
		},
	})
}

// `throws _` opts into inference: the clause mints a fresh variable that the body's
// exceptional exits flow into, so the function's throws is read off the body the way its
// return type is read off its `return` statements.
func TestInferThrowsFromBodyUnderAWildcardClause(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "SingleThrow",
			src:  `fn f() throws _ { throw "boom" }`,
			want: `fn () -> never throws "boom"`,
		},
		{
			// Two throws on different paths union, the same join the return points take.
			name: "ThrowsOnBothBranches",
			src:  `fn f(c: boolean) throws _ { if c { throw "a" } else { throw 5 } }`,
			want: `fn (c: boolean) -> never throws 5 | "a"`,
		},
		{
			// The `else` branch diverges, so it drops out of the value union and only the
			// `then` branch reaches the return.
			name: "ThrowOnOnePathOnly",
			src:  `fn f(c: boolean) throws _ { if c { return 1 } else { throw "x" } }`,
			want: `fn (c: boolean) -> 1 throws "x"`,
		},
		{
			// `throw` is `never`, so it composes where a value is expected and contributes
			// nothing to the union of the branches.
			name: "ThrowInValuePosition",
			src:  `fn f(c: boolean) throws _ { return if c { 1 } else { throw "x" } }`,
			want: `fn (c: boolean) -> 1 throws "x"`,
		},
		{
			// A body with no exceptional exit renders no clause at all, clause or no.
			name: "NoThrowRendersNoClause",
			src:  `fn f() { return 1 }`,
			want: `fn () -> 1`,
		},
		{
			// A wildcard clause nothing reached is still no clause.
			name: "UnreachedWildcardRendersNoClause",
			src:  `fn f() throws _ { return 1 }`,
			want: `fn () -> 1`,
		},
		{
			// A nested function owns its own throws sink, so `g`'s throw stays on `g` and
			// the enclosing `f` needs no clause of its own.
			name: "NestedFunctionThrowsDoNotLeak",
			src: `
				fn f() {
					val g = fn () throws _ { throw "inner" }
					return 1
				}
			`,
			want: `fn () -> 1`,
		},
	})
}

// A `throws T` clause fixes what the function raises: the declared type is what a caller
// sees, and each `throw` in the body is checked against it at its own site.
func TestInferThrowsClause(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "ClauseWidensTheThrownLiteral",
			src:  `fn f() throws string { throw "boom" }`,
			want: "fn () -> never throws string",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "ThrownValueMustSatisfyTheClause",
			src:      `fn f() throws number { throw "boom" }`,
			wantErrs: []string{`1:30-1:36: cannot constrain "boom" <: number`},
		},
	})
}

// A call raises whatever its callee declares, so the callee's throws reaches the
// caller's own clause exactly as a `throw` in the caller's body would.
func TestInferThrowsPropagateThroughCalls(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "CallerInheritsCalleeThrows",
			src: `
				fn f() throws string { throw "boom" }
				fn g() throws _ { f() }
			`,
			binding: "g",
			want:    "fn () -> void throws string",
		},
		{
			name: "CallToNonThrowingCalleeAddsNothing",
			src: `
				fn f() -> number { return 1 }
				fn g() { return f() }
			`,
			binding: "g",
			want:    "fn () -> number",
		},
		{
			name:    "CallThroughAParameter",
			src:     `fn f(cb: fn() -> number throws string) throws _ { return cb() }`,
			binding: "f",
			want:    "fn (cb: fn () -> number throws string) -> number throws string",
		},
		{
			name: "ThrowsPropagateThroughACallChain",
			src: `
				fn a() throws string { throw "boom" }
				fn b() throws _ { a() }
				fn c() throws _ { b() }
			`,
			binding: "c",
			want:    "fn () -> void throws string",
		},
		{
			// `inner` raises, but `outer` only builds and returns it, so `outer` raises
			// nothing.
			name: "ClosureKeepsItsOwnThrows",
			src: `
				fn a() throws string { throw "boom" }
				fn outer() {
					val inner = fn () throws _ { a() }
					return inner
				}
			`,
			binding: "outer",
			want:    "fn () -> fn () -> void throws string",
		},
		{
			name: "BodylessDeclareFnDeclaresThrows",
			src: `
				declare fn a() -> number throws string
				fn f() throws _ { return a() }
			`,
			want: "fn () -> number throws string",
		},
		{
			// The sink is minted at the body's own level, not at the level of whichever
			// exceptional exit reaches it first. A `val` initializer is typed one level
			// deeper, so a sink minted there would be inner to the body and the later
			// bare call would extrude it, wiring the proxy back to the original in both
			// directions. The cycle used to render as `throws μX0.(string | X0)`.
			name: "SinkMintedInAValInitializerStaysAtTheBodyLevel",
			src: `
				fn a() throws string { throw "boom" }
				fn f() throws _ {
					val x = a()
					a()
					return x
				}
			`,
			want: "fn () -> never throws string",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			name: "CalleeThrowsCheckedAgainstCallerClause",
			src: `
				fn f() throws string { throw "boom" }
				fn g() throws number { f() }
			`,
			wantErrs: []string{"3:28-3:31: cannot constrain string <: number"},
		},
	})
}

// Overload resolution picks one arm, so only that arm's throws reaches the caller. A set
// whose other arms raise contributes nothing to a call that matched a non-throwing one.
// Both callers are asserted from one source, so this stays outside the shared table.
func TestInferThrowsThroughOverloadResolution(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn a(x: number) -> never throws string { throw "boom" }
		fn a(x: string) -> string { return x }
		fn callsThrowingArm() throws _ { return a(1) }
		fn callsQuietArm() { return a("s") }
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> never throws string", values["callsThrowingArm"])
	require.Equal(t, "fn () -> string", values["callsQuietArm"])
}

// Throws is covariant, so a function that raises a narrower set stands in for one that
// raises a wider set. A function with no clause raises `never`, the bottom of the
// lattice, so it satisfies every throwing annotation and no non-throwing annotation
// accepts a throwing function.
func TestInferThrowsSubtyping(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "NonThrowingSatisfiesThrowingAnnotation",
			src:  `val f: fn(x: number) -> number throws string = fn (x) { return x }`,
			want: "fn (x: number) -> number throws string",
		},
		{
			name: "NarrowerThrowsSatisfiesWider",
			src: `
				fn narrow() throws string { throw "boom" }
				val f: fn() -> never throws string | number = narrow
			`,
			want: "fn () -> never throws number | string",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			name: "ThrowingFailsNonThrowingAnnotation",
			src: `
				fn thrower() throws string { throw "boom" }
				val f: fn() -> never = thrower
			`,
			wantErrs: []string{"3:28-3:35: cannot constrain string <: never"},
		},
		{
			name: "WiderThrowsFailsNarrowerAnnotation",
			src: `
				fn wide() throws string | number { throw "boom" }
				val f: fn() -> never throws string = wide
			`,
			wantErrs: []string{"3:42-3:46: cannot constrain number <: string"},
		},
	})
}

// A `throws E` clause naming a quantified type parameter needs no machinery of its own:
// generalization quantifies `E` from the parameter's throws position, a call binds it to
// the argument's throws, and the result flows out through the caller's own clause.
func TestInferThrowsPolymorphism(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "ParameterThrowsReachesTheReturnClause",
			src:  `fn f<E>(g: fn() -> number throws E) -> number throws E { return g() }`,
			want: "fn <E>(g: fn () -> number throws E) -> number throws E",
		},
		{
			name: "CallBindsTheThrowsParameter",
			src: `
				fn f<E>(g: fn() -> number throws E) -> number throws E { return g() }
				fn h() throws _ { return f(fn () -> never throws string { throw "x" }) }
			`,
			binding: "h",
			want:    "fn () -> number throws string",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// A `throws E` clause is an output position, so a body that pins `E` to a
			// concrete type over-promises exactly as a body pinning a declared return
			// type does.
			name: "TypeParamMustBeProducible",
			src:  `fn f<E>() throws E { throw "boom" }`,
			wantErrs: []string{
				"1:1-1:36: the body forces type parameter `E` to `\"boom\"`, so it cannot stand for an arbitrary type",
			},
		},
	})
}

// A method and a constructor declare and infer throws the same way a standalone function
// does, and a call through the receiver or through the class value raises what they
// declare. A constructor's clause rides on the callable signature the class name binds.
func TestInferThrowsOnClassMembers(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "MethodThrowsReachesItsCaller",
			src: `
				class Parser {
					text: string,
					constructor(mut self, text: string) { self.text = text },
					parse(self) -> never throws string { throw "bad input" },
				}
				fn f(p: Parser) throws _ { return p.parse() }
			`,
			want: "fn (p: Parser) -> never throws string",
		},
		{
			name: "ConstructorThrowsRidesOnTheClassValue",
			src: `
				class Counter {
					n: number,
					constructor(mut self, n: number) throws string { throw "bad count" },
				}
				fn f(n: number) throws _ { return Counter(n) }
			`,
			want: "fn (n: number) -> Counter throws string",
		},
	})
}

// An unsupported `throws` annotation recovers to a fresh variable, matching the parameter
// and return positions. Recovering to nil would read as `never`, so every value flowing
// into the annotation would be re-reported for raising something on top of the one real
// error.
func TestInferThrowsAnnotationRecovery(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// `void` stands in for any annotation resolveTypeAnn does not support.
			name:     "UnsupportedAnnotationDoesNotCascade",
			src:      `val f: fn() -> number throws void = fn () -> never throws _ { throw "x" }`,
			wantErrs: []string{"1:30-1:34: Unsupported: VoidTypeAnn"},
		},
	})
}

// A signature can declare something its body never delivers. Both directions are sound,
// since `never` sits below every type, so neither is an error — but neither is useful
// either, and each is reported as a warning naming the declaration to drop.
func TestInferThrowsOverDeclaredSignature(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// The body reaches no exceptional exit, so the clause obliges every caller to
			// handle an exception that cannot occur.
			name: "ClauseNoExceptionalExitReaches",
			src:  `fn f() -> number throws string { return 1 }`,
			wantErrs: []string{
				"1:25-1:31: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
		},
		{
			// An accessor reads its clause the way a method does, so a getter that raises
			// nothing draws the same warning a clause-less-body function draws.
			name: "GetterClauseNoExceptionalExitReaches",
			src:  `class C { v: number, get x(self) -> number throws string { return self.v } }`,
			wantErrs: []string{
				"1:51-1:57: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
		},
		{
			name: "SetterClauseNoExceptionalExitReaches",
			src:  `class C { v: number, set x(mut self, v: number) throws string { self.v = v } }`,
			wantErrs: []string{
				"1:56-1:62: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
		},
		{
			// The body reaches no normal exit, so no caller can observe a `number`.
			name: "ReturnAnnotationNoNormalExitReaches",
			src:  `fn f() -> number throws _ { throw "x" }`,
			wantErrs: []string{
				"1:11-1:17: every path through the body throws, so the declared return type `number` is unreachable; the body returns `never`",
			},
		},
		{
			// The two checks are independent. Here the clause IS used, since the body
			// throws into it, so only the unreachable return is reported.
			name: "OnlyTheReturnIsOverDeclared",
			src:  `fn f() -> number throws string { throw "x" }`,
			wantErrs: []string{
				"1:11-1:17: every path through the body throws, so the declared return type `number` is unreachable; the body returns `never`",
			},
		},
	})

	// Neither warning fires where the body delivers what the signature declares. These
	// are the shapes the warnings must stay quiet on, so a correct signature is never
	// nagged.
	runThrowsCases(t, []throwsCase{
		{
			// `-> never` says what a diverging body actually delivers.
			name: "NeverAnnotationOnADivergingBody",
			src:  `fn f() -> never throws _ { throw "x" }`,
			want: `fn () -> never throws "x"`,
		},
		{
			// One path returns and the other throws, so both declarations are reachable.
			name: "MixedReturnAndThrowPaths",
			src:  `fn f(c: boolean) -> number throws string { if c { return 1 } else { throw "x" } }`,
			want: "fn (c: boolean) -> number throws string",
		},
		{
			// A clause satisfied by a call rather than by a `throw` still counts as used.
			name: "ClauseUsedByACall",
			src: `
				fn a() throws string { throw "boom" }
				fn f() throws string { a() }
			`,
			want: "fn () -> void throws string",
		},
		{
			// A bodyless `declare fn` has no body to measure either declaration against.
			name: "BodylessDeclareFnIsNotMeasured",
			src:  `declare fn f() -> number throws string`,
			want: "fn () -> number throws string",
		},
	})
}

// raisingGetterClass declares `get x` on a class that both returns and raises, so neither
// over-declaration warning fires and a case appending to it measures only the throws flow.
const raisingGetterClass = `
	class C {
		v: number,
		bad: boolean,
		get x(self) -> number throws string { if self.bad { throw "boom" } return self.v },
	}
`

// A getter and a setter each declare what they raise, the same way a method does, and a
// getter's clause reaches whoever reads the property. Reading through a getter runs its
// body, so the read is an exceptional exit of the enclosing body just as a call is.
func TestInferThrowsFromAnAccessor(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// `throws _` on the reader infers from the read, so the getter's declared
			// `string` is what the reader raises.
			name: "ReaderInfersTheGetterClause",
			src:  raisingGetterClass + `fn f(c: C) -> number throws _ { return c.x }`,
			want: "fn (c: C) -> number throws string",
		},
		{
			// `throws _` on the GETTER infers from its body, so the reader sees the
			// literal type the body throws rather than a declared widening of it.
			name: "GetterInfersItsOwnClauseFromItsBody",
			src: `
				class C {
					v: number,
					bad: boolean,
					get x(self) -> number throws _ { if self.bad { throw "boom" } return self.v },
				}
				fn f(c: C) -> number throws _ { return c.x }
			`,
			want: `fn (c: C) -> number throws "boom"`,
		},
		{
			// A static getter is read off the class VALUE, a third resolution path.
			name: "StaticGetterRead",
			src: `
				class C {
					bad: boolean,
					static get x() -> number throws string { if true { throw "boom" } return 1 },
				}
				fn f() -> number throws string { return C.x }
			`,
			want: "fn () -> number throws string",
		},
		{
			// A class value renders both accessors' clauses, so a raising accessor is
			// visible in the type rather than reading as non-throwing.
			name:    "StaticAccessorsRenderTheirClause",
			binding: "C",
			src: `
				class C {
					static get g() -> number throws string { if true { throw "g" } return 1 },
					static set s(v: number) throws string { if true { throw "s" } },
				}
			`,
			want: "{new () -> C, get g() -> number throws string, set s(value: number) throws string}",
		},
		{
			// A getter on a generic class projects its return to the instance's argument
			// while its clause, which names no class parameter, carries through unchanged.
			name: "GenericClassGetter",
			src: `
				class Box<T> {
					v: T,
					bad: boolean,
					get item(self) -> T throws string { if self.bad { throw "boom" } return self.v },
				}
				fn f(b: Box<number>) -> number throws string { return b.item }
			`,
			want: "fn (b: Box<number>) -> number throws string",
		},
		{
			// A union receiver reads through one getter per member, so the reader raises
			// the union of what they declare.
			name: "UnionReceiverJoinsEveryMemberGetter",
			src: `
				class A { bad: boolean, get x(self) -> number throws string { if self.bad { throw "a" } return 1 } }
				class B { bad: boolean, get x(self) -> number throws number { if self.bad { throw 2 } return 1 } }
				fn f(c: A | B) -> number throws _ { return c.x }
			`,
			want: "fn (c: A | B) -> number throws number | string",
		},
		{
			// Reading a METHOD only names the function; its throws stays in the signature
			// until the method is called, so a clause-less reader is fine.
			name: "MethodValueReadIsNotAnExceptionalExit",
			src: `
				class C { m(self) throws string { throw "boom" } }
				fn f(c: C) { val g = c.m }
			`,
			want: "fn (c: C) -> void",
		},
		{
			// An accessor that handles everything internally raises nothing, so it needs
			// no clause and its readers need none either.
			name: "GetterHandlingItsCalleeInternallyRaisesNothing",
			src: `
				fn a() throws string { throw "boom" }
				class C { get x(self) -> number { return try { a() } catch { e => 0 } } }
				fn f(c: C) -> number { return c.x }
			`,
			want: "fn (c: C) -> number",
		},
	})
}

// An accessor with no `throws` clause raises nothing, so every exceptional exit in its
// body is rejected at its own site, and a read of a raising getter is rejected inside a
// reader that declares no clause of its own.
func TestInferThrowsRejectsAnUndeclaredAccessorRaise(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "ThrowInAClauselessGetter",
			src:      `class C { v: number, get x(self) { throw "boom" } }`,
			wantErrs: []string{`1:42-1:48: cannot constrain "boom" <: never`},
		},
		{
			name:     "ThrowInAClauselessSetter",
			src:      `class C { v: number, set x(mut self, v: number) { throw "boom" } }`,
			wantErrs: []string{`1:57-1:63: cannot constrain "boom" <: never`},
		},
		{
			name: "CallToAThrowingCalleeFromAClauselessGetter",
			src: `
				fn a() throws string { throw "boom" }
				class C { v: number, get x(self) -> number { return a() } }
			`,
			wantErrs: []string{"3:57-3:60: cannot constrain string <: never"},
		},
		{
			// The read is the exceptional exit, so the diagnostic blames the access
			// rather than the getter's own body, which declared its raise correctly.
			name: "ReadOfARaisingGetterFromAClauselessCaller",
			src: `
				class C {
					v: number,
					bad: boolean,
					get x(self) -> number throws string { if self.bad { throw "boom" } return self.v },
				}
				fn f(c: C) -> number { return c.x }
			`,
			wantErrs: []string{"7:35-7:38: cannot constrain string <: never"},
		},
		{
			name: "SelfReadOfARaisingGetterFromAClauselessMethod",
			src: `
				class C {
					v: number,
					bad: boolean,
					get x(self) -> number throws string { if self.bad { throw "boom" } return self.v },
					m(self) -> number { return self.x },
				}
			`,
			wantErrs: []string{"6:33-6:39: cannot constrain string <: never"},
		},
	})
}

// A read resolves an accessor pair to its getter whichever half is declared first, and a
// union receiver raises only where the read actually joins through its members.
func TestInferThrowsAccessorReadResolution(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// `set x` precedes `get x`, so a declaration-order lookup would resolve the
			// read to the setter and report it write-only, dropping the getter's raise.
			name: "SetterDeclaredBeforeGetterStillReadsTheGetter",
			src: `
				class A {
					v: number,
					bad: boolean,
					set x(mut self, n: number) { self.v = n },
					get x(self) -> number throws string { if self.bad { throw "a" } return self.v },
				}
				fn f(c: A) -> number { return c.x }
			`,
			wantErrs: []string{"8:35-8:38: cannot constrain string <: never"},
		},
		{
			// The same pair reached through a union, where the read joins per member.
			name: "SetterDeclaredBeforeGetterUnderAUnionReceiver",
			src: `
				class A {
					v: number,
					bad: boolean,
					set x(mut self, n: number) { self.v = n },
					get x(self) -> number throws string { if self.bad { throw "a" } return self.v },
				}
				class B { x: number }
				fn f(c: A | B) -> number { return c.x }
			`,
			wantErrs: []string{"9:39-9:42: cannot constrain string <: never"},
		},
		{
			// `undefined` carries no readable object, so the join abandons the read
			// entirely and falls back to strict every-member subtyping. No getter runs,
			// so the two rejections below are the whole story — the getter's `string`
			// must not be reported as an undeclared raise on top of them.
			name: "UnionMemberWithNoReadableObjectRaisesNothing",
			src: `
				class A { bad: boolean, get x(self) -> number throws string { if self.bad { throw "a" } return 1 } }
				fn f(c: A | undefined) -> number { return c.x }
			`,
			wantErrs: []string{
				"3:47-3:50: cannot constrain undefined <: object",
				"3:49-3:50: object is missing property: x",
			},
		},
	})
}

// A raise that enters a body through a getter read is an ordinary exceptional exit from
// there on. It unions with the body's other exits, rides out through the enclosing
// signature to that function's own callers, and is handled by a `try` the same way a
// `throw` or a call is. These cases exercise the code that USES a raising accessor rather
// than the accessor's own declaration.
func TestInferThrowsThroughAGetterRead(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// A catch-all covers whatever the block raises, so the read leaves the
			// enclosing clause untouched and `f` needs none.
			name: "CatchAllAroundTheReadClearsTheClause",
			src:  raisingGetterClass + `fn f(c: C) { try { return c.x } catch { e => 0 } }`,
			want: "fn (c: C) -> number",
		},
		{
			// The arm names the literal the getter's body throws, but the getter DECLARES
			// `string`, which is what the read raises. `string` outlives the arm, so it is
			// rethrown and reaches the clause.
			name: "NamedArmLeavesTheDeclaredRaiseToRethrow",
			src: raisingGetterClass + `
				fn f(c: C) throws _ { try { val n = c.x } catch { "boom" => 0 } }
			`,
			want: "fn (c: C) -> void throws string",
		},
		{
			// `g` picks the raise up from the read and `f` picks it up from the call to
			// `g`, so it travels two frames on the ordinary call rule.
			name: "RaiseTravelsOnThroughTheCallerOfTheReader",
			src: raisingGetterClass + `
				fn g(c: C) -> number throws _ { return c.x }
				fn f(c: C) -> number throws _ { return g(c) }
			`,
			want: "fn (c: C) -> number throws string",
		},
		{
			// Two reads on the same path union their raises, the same join two calls take.
			name: "TwoGetterReadsUnionTheirRaises",
			src: `
				class C {
					bad: boolean,
					get a(self) -> number throws string { if self.bad { throw "a" } return 1 },
					get b(self) -> number throws number { if self.bad { throw 2 } return 1 },
				}
				fn f(c: C) -> number throws _ {
					val p = c.a
					return c.b
				}
			`,
			want: "fn (c: C) -> number throws number | string",
		},
		{
			// A read on one path and a `throw` on the other union, so a getter raise sits
			// in the same clause as a literal the body throws itself.
			name: "GetterRaiseJoinsAThrowOnAnotherPath",
			src:  raisingGetterClass + `fn f(c: C, t: boolean) -> number throws _ { if t { throw 5 } return c.x }`,
			want: "fn (c: C, t: boolean) -> number throws string | 5",
		},
		{
			// A getter reading another getter is a consumer like any other, so the outer
			// one has to declare what the inner read raises.
			name: "GetterReadingAnotherClassGetter",
			src: raisingGetterClass + `
				class D {
					inner: C,
					get y(self) -> number throws string { return self.inner.x },
				}
				fn f(d: D) -> number throws _ { return d.y }
			`,
			want: "fn (d: D) -> number throws string",
		},
		{
			// A method reads the getter through `self` and carries the raise into its own
			// signature, so calling the method raises it at the call site.
			name: "MethodReadingAGetterCarriesTheRaiseToItsCaller",
			src: `
				class C {
					bad: boolean,
					get x(self) -> number throws string { if self.bad { throw "boom" } return 1 },
					m(self) -> number throws _ { return self.x },
				}
				fn f(c: C) -> number throws _ { return c.m() }
			`,
			want: "fn (c: C) -> number throws string",
		},
		{
			// The constant-string bracket form reads the same getter `c.x` does, so it is
			// the same exceptional exit.
			name: "ConstantStringIndexReadRaisesTheSame",
			src:  raisingGetterClass + `fn f(c: C) -> number throws _ { return c["x"] }`,
			want: "fn (c: C) -> number throws string",
		},
		{
			// The read sits inside a nested function, which owns its own throws sink, so
			// the raise stays on the lambda and the enclosing `f` needs no clause.
			name: "ReadInsideANestedLambdaDoesNotLeakOut",
			src:  raisingGetterClass + `fn f(c: C) { val g = fn () -> number throws _ { return c.x } }`,
			want: "fn (c: C) -> void",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// The arm covers the literal but not the declared `string`, so the leftover is
			// rethrown into a clause-less function.
			name: "PartialCatchAroundTheReadStillNeedsAClause",
			src: raisingGetterClass + `
				fn f(c: C) { try { val n = c.x } catch { "boom" => 0 } }
			`,
			wantErrs: []string{
				"8:18-8:57: the catch arms leave string uncovered, so it is rethrown, and the enclosing `throws never` does not admit it. Cover it with a catch arm, or widen the enclosing clause",
			},
		},
		{
			// The outer getter reads the inner one and declares nothing, so the read is
			// rejected at its own site the way it would be in a plain function.
			name: "OuterGetterReadingARaisingGetterNeedsItsOwnClause",
			src: raisingGetterClass + `
				class D {
					inner: C,
					get y(self) -> number { return self.inner.x },
				}
			`,
			wantErrs: []string{"10:37-10:49: cannot constrain string <: never"},
		},
	})
}

// raisingSetterClass declares `set x` on a class whose setter both stores and raises, the
// write-side twin of raisingGetterClass.
const raisingSetterClass = `
	class C {
		v: number,
		bad: boolean,
		set x(mut self, n: number) throws string { if self.bad { throw "boom" } self.v = n },
	}
`

// Writing through a setter runs its body, so the write is an exceptional exit of the
// enclosing body exactly as a getter read is. A write resolves to a setter as of #982, so
// the setter half of the throws position is reachable from source.
func TestInferThrowsThroughASetterWrite(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// `throws _` on the writer infers from the write.
			name: "WriterInfersTheSetterClause",
			src:  raisingSetterClass + `fn f(c: mut C) throws _ { c.x = 5 }`,
			want: "fn (c: mut C) -> void throws string",
		},
		{
			// A catch-all around the write covers it, so the writer needs no clause.
			name: "CatchAllAroundTheWriteClearsTheClause",
			src:  raisingSetterClass + `fn f(c: mut C) { try { c.x = 5 } catch { e => 0 } }`,
			want: "fn (c: mut C) -> void",
		},
		{
			// A method writes through `self` and carries the raise into its own signature,
			// so calling the method raises it at the call site.
			name: "MethodWritingThroughSelfCarriesTheRaiseToItsCaller",
			src: `
				class C {
					v: number,
					bad: boolean,
					set x(mut self, n: number) throws string { if self.bad { throw "boom" } self.v = n },
					m(mut self) throws _ { self.x = 5 },
				}
				fn f(c: mut C) throws _ { c.m() }
			`,
			want: "fn (c: mut C) -> void throws string",
		},
		{
			// A setter that raises nothing leaves the writer's clause untouched, the same
			// way a non-throwing callee adds nothing at a call site.
			name: "NonThrowingSetterWriteAddsNothing",
			src: `
				class C { v: number, set x(mut self, n: number) { self.v = n } }
				fn f(c: mut C) { c.x = 5 }
			`,
			want: "fn (c: mut C) -> void",
		},
		{
			// A plain field write is not an accessor call and raises nothing.
			name: "PlainFieldWriteIsNotAnExceptionalExit",
			src: `
				class C { v: number }
				fn f(c: mut C) { c.v = 5 }
			`,
			want: "fn (c: mut C) -> void",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// The write is the exceptional exit, so the diagnostic blames the assignment
			// rather than the setter's own body, which declared its raise correctly.
			name:     "WriteThroughARaisingSetterFromAClauselessWriter",
			src:      raisingSetterClass + `fn f(c: mut C) { c.x = 5 }`,
			wantErrs: []string{"7:18-7:25: cannot constrain string <: never"},
		},
		{
			name: "SelfWriteThroughARaisingSetterFromAClauselessMethod",
			src: `
				class C {
					v: number,
					bad: boolean,
					set x(mut self, n: number) throws string { if self.bad { throw "boom" } self.v = n },
					m(mut self) { self.x = 5 },
				}
			`,
			wantErrs: []string{"6:20-6:30: cannot constrain string <: never"},
		},
	})
}

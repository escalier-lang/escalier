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
		{
			// A body with no `return` that always leaves along the exceptional edge
			// reaches no normal exit, so its return type is `never`. Annotating it
			// `-> never` says exactly that, so nothing is over-declared.
			name: "DivergingBodyReturnsNever",
			src:  `fn f() -> never throws _ { throw "x" }`,
			want: `fn () -> never throws "x"`,
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
		{
			// The clause parses on its own, with no `-> R` in front of it.
			name: "ClauseWithoutReturnAnnotation",
			src:  `fn f(x: number) throws string { throw "boom" }`,
			want: "fn (x: number) -> never throws string",
		},
		{
			// One path returns and the other throws, so both declarations are delivered.
			name: "ClauseAfterReturnAnnotation",
			src:  `fn f(c: boolean) -> number throws string { if c { return 1 } else { throw "x" } }`,
			want: "fn (c: boolean) -> number throws string",
		},
		{
			// `throws _` mints a fresh variable the body's throws flow into, so the clause
			// asks for inference rather than fixing a type.
			name: "WildcardClauseInfers",
			src:  `fn f() throws _ { throw "boom" }`,
			want: `fn () -> never throws "boom"`,
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

// A getter and a setter each declare what they raise, the same way a method does, and a
// getter's clause reaches whoever reads the property. Reading through a getter runs its
// body, so the read is an exceptional exit of the enclosing body just as a call is.
func TestInferThrowsFromAnAccessor(t *testing.T) {
	// raisingGetter declares `get x` on a class that both returns and raises, so neither
	// over-declaration warning fires and the cases below measure only the throws flow.
	const raisingGetter = `
		class C {
			v: number,
			bad: boolean,
			get x(self) -> number throws string { if self.bad { throw "boom" } return self.v },
		}
	`
	runThrowsCases(t, []throwsCase{
		{
			// The getter's clause reaches the reader, which redeclares it as its own.
			name: "GetterClauseReachesTheReader",
			src:  raisingGetter + `fn f(c: C) -> number throws string { return c.x }`,
			want: "fn (c: C) -> number throws string",
		},
		{
			// `throws _` on the reader infers from the read, so the getter's declared
			// `string` is what the reader raises.
			name: "ReaderInfersTheGetterClause",
			src:  raisingGetter + `fn f(c: C) -> number throws _ { return c.x }`,
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
			// A sibling member reads the getter through `self`, which resolves on the
			// class body rather than on a projected instance, and raises the same.
			name: "SelfReadInsideASiblingMethod",
			src: `
				class C {
					v: number,
					bad: boolean,
					get x(self) -> number throws string { if self.bad { throw "boom" } return self.v },
					m(self) -> number throws string { return self.x },
				}
				fn f(c: C) -> number throws string { return c.m() }
			`,
			want: "fn (c: C) -> number throws string",
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
		{
			// Only one member of the union reads through a raising getter. The other
			// carries `x` as a plain field, which raises nothing, so the single raise
			// still has to be declared.
			name: "UnionReceiverReadFromAClauselessCaller",
			src: `
				class A { bad: boolean, get x(self) -> number throws string { if self.bad { throw "a" } return 1 } }
				class B { x: number }
				fn f(c: A | B) -> number { return c.x }
			`,
			wantErrs: []string{"4:39-4:42: cannot constrain string <: never"},
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

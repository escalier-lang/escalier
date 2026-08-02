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
//
// A getter or setter projects to the type its read yields or its write accepts, and
// neither element has anywhere to record a clause, so the clause is rejected rather than
// dropped. A raising accessor cannot read as non-throwing.
func TestInferThrowsAnnotationRecovery(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			// `void` stands in for any annotation resolveTypeAnn does not support.
			name:     "UnsupportedAnnotationDoesNotCascade",
			src:      `val f: fn() -> number throws void = fn () -> never throws _ { throw "x" }`,
			wantErrs: []string{"1:30-1:34: Unsupported: VoidTypeAnn"},
		},
		{
			// The clause draws two diagnostics: it is unsupported on an accessor at all,
			// and the body raises nothing so it would read as unused even where it is
			// supported. Both say to drop it.
			name: "GetterClauseIsRejected",
			src:  `class C { v: number, get x(self) -> number throws string { return self.v } }`,
			wantErrs: []string{
				"1:22-1:75: Unsupported: throws clause on a getter",
				"1:51-1:57: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
		},
		{
			name: "SetterClauseIsRejected",
			src:  `class C { v: number, set x(mut self, v: number) throws string { self.v = v } }`,
			wantErrs: []string{
				"1:22-1:77: Unsupported: throws clause on a setter",
				"1:56-1:62: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
			},
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

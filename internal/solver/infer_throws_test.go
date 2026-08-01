package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A function's `throws` type is inferred from its body when the signature declares no
// clause, the way its return type is inferred from its `return` statements. Each case
// renders the binding named `f` and expects no error.
func TestInferThrowsFromBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "SingleThrow",
			src:  `fn f() { throw "boom" }`,
			want: `fn () -> never throws "boom"`,
		},
		{
			// Two throws on different paths union, the same join the return points take.
			name: "ThrowsOnBothBranches",
			src:  `fn f(c: boolean) { if c { throw "a" } else { throw 5 } }`,
			want: `fn (c: boolean) -> never throws 5 | "a"`,
		},
		{
			// The `else` branch diverges, so it drops out of the value union and only the
			// `then` branch reaches the return.
			name: "ThrowOnOnePathOnly",
			src:  `fn f(c: boolean) { if c { return 1 } else { throw "x" } }`,
			want: `fn (c: boolean) -> 1 throws "x"`,
		},
		{
			// `throw` is `never`, so it composes where a value is expected and contributes
			// nothing to the union of the branches.
			name: "ThrowInValuePosition",
			src:  `fn f(c: boolean) { return if c { 1 } else { throw "x" } }`,
			want: `fn (c: boolean) -> 1 throws "x"`,
		},
		{
			// A body with no exceptional exit renders no clause at all.
			name: "NoThrowRendersNoClause",
			src:  `fn f() { return 1 }`,
			want: `fn () -> 1`,
		},
		{
			// A nested function owns its own throws sink, so `g`'s throw stays on `g`.
			name: "NestedFunctionThrowsDoNotLeak",
			src: `
				fn f() {
					val g = fn () { throw "inner" }
					return 1
				}
			`,
			want: `fn () -> 1`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values["f"])
		})
	}
}

// A `throws T` clause fixes what the function raises: the declared type is what a caller
// sees, and each `throw` in the body is checked against it at its own site.
func TestInferThrowsClause(t *testing.T) {
	t.Run("ClauseWidensTheThrownLiteral", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() throws string { throw "boom" }`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> never throws string", values["f"])
	})
	t.Run("ClauseWithoutReturnAnnotation", func(t *testing.T) {
		// The clause parses on its own, with no `-> R` in front of it.
		values, _, errs := inferSource(t, `fn f(x: number) throws string { throw "boom" }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (x: number) -> never throws string", values["f"])
	})
	t.Run("ClauseAfterReturnAnnotation", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f() -> number throws string { return 1 }`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number throws string", values["f"])
	})
	t.Run("ThrownValueMustSatisfyTheClause", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn f() throws number { throw "boom" }`)
		require.Len(t, errs, 1)
		require.Equal(t, `1:30-1:36: cannot constrain "boom" <: number`, msgWithSpan(errs[0]))
	})
	t.Run("WildcardClauseInfers", func(t *testing.T) {
		// `throws _` mints a fresh variable the body's throws flow into, so the clause asks
		// for inference rather than fixing a type.
		values, _, errs := inferSource(t, `fn f() throws _ { throw "boom" }`)
		require.Empty(t, errs)
		require.Equal(t, `fn () -> never throws "boom"`, values["f"])
	})
	t.Run("ClauseWithNoThrowingBodyStillShows", func(t *testing.T) {
		// The declared type is what a caller sees, whether or not the body raises anything.
		values, _, errs := inferSource(t, `fn f() -> number throws string { return 1 }`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number throws string", values["f"])
	})
}

// A body with no `return` that always leaves along the exceptional edge reaches no
// normal exit, so its return type is `never` and it satisfies any return annotation.
func TestInferThrowsDivergingBody(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() -> number { throw "x" }`)
	require.Empty(t, errs)
	require.Equal(t, `fn () -> number throws "x"`, values["f"])
}

// A call raises whatever its callee declares, so the callee's throws reaches the
// caller's own clause exactly as a `throw` in the caller's body would.
func TestInferThrowsPropagateThroughCalls(t *testing.T) {
	t.Run("CallerInheritsCalleeThrows", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f() throws string { throw "boom" }
			fn g() { f() }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> void throws string", values["g"])
	})
	t.Run("CallToNonThrowingCalleeAddsNothing", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f() -> number { return 1 }
			fn g() { return f() }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number", values["g"])
	})
	t.Run("CalleeThrowsCheckedAgainstCallerClause", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f() throws string { throw "boom" }
			fn g() throws number { f() }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:27-3:30: cannot constrain string <: number", msgWithSpan(errs[0]))
	})
	t.Run("CallThroughAParameter", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f(cb: fn() -> number throws string) { return cb() }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (cb: fn () -> number throws string) -> number throws string", values["f"])
	})
	t.Run("ThrowsPropagateThroughACallChain", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn a() throws string { throw "boom" }
			fn b() { a() }
			fn c() { b() }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> void throws string", values["c"])
	})
	t.Run("ClosureKeepsItsOwnThrows", func(t *testing.T) {
		// `inner` raises, but `outer` only builds and returns it, so `outer` raises nothing.
		values, _, errs := inferSource(t, `
			fn a() throws string { throw "boom" }
			fn outer() {
				val inner = fn () { a() }
				return inner
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> fn () -> void throws string", values["outer"])
	})
	t.Run("BodylessDeclareFnDeclaresThrows", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare fn a() -> number throws string
			fn f() { return a() }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number throws string", values["f"])
	})
}

// Overload resolution picks one arm, so only that arm's throws reaches the caller. A set
// whose other arms raise contributes nothing to a call that matched a non-throwing one.
func TestInferThrowsThroughOverloadResolution(t *testing.T) {
	src := `
		fn a(x: number) -> number throws string { throw "boom" }
		fn a(x: string) -> string { return x }
		fn callsThrowingArm() { return a(1) }
		fn callsQuietArm() { return a("s") }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> number throws string", values["callsThrowingArm"])
	require.Equal(t, "fn () -> string", values["callsQuietArm"])
}

// A `throws E` clause is an output position, so a body that pins `E` to a concrete type
// over-promises exactly as a body pinning a declared return type does.
func TestInferThrowsTypeParamMustBeProducible(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<E>() throws E { throw "boom" }`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"1:1-1:36: the body forces type parameter `E` to `\"boom\"`, so it cannot stand for an arbitrary type",
		msgWithSpan(errs[0]))
}

// Throws is covariant, so a function that raises a narrower set stands in for one that
// raises a wider set. A function with no clause raises `never`, the bottom of the
// lattice, so it satisfies every throwing annotation and no non-throwing annotation
// accepts a throwing function.
func TestInferThrowsSubtyping(t *testing.T) {
	t.Run("NonThrowingSatisfiesThrowingAnnotation", func(t *testing.T) {
		values, _, errs := inferSource(t, `val f: fn(x: number) -> number throws string = fn (x) { return x }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (x: number) -> number throws string", values["f"])
	})
	t.Run("NarrowerThrowsSatisfiesWider", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn narrow() throws string { throw "boom" }
			val f: fn() -> never throws string | number = narrow
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> never throws number | string", values["f"])
	})
	t.Run("ThrowingFailsNonThrowingAnnotation", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn thrower() throws string { throw "boom" }
			val f: fn() -> never = thrower
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:27-3:34: cannot constrain string <: never", msgWithSpan(errs[0]))
	})
	t.Run("WiderThrowsFailsNarrowerAnnotation", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn wide() throws string | number { throw "boom" }
			val f: fn() -> never throws string = wide
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:41-3:45: cannot constrain number <: string", msgWithSpan(errs[0]))
	})
}

// A `throws E` clause naming a quantified type parameter needs no machinery of its own:
// generalization quantifies `E` from the parameter's throws position, a call binds it to
// the argument's throws, and the result flows out through the caller's own clause.
func TestInferThrowsPolymorphism(t *testing.T) {
	t.Run("ParameterThrowsReachesTheReturnClause", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f<E>(g: fn() -> number throws E) -> number throws E { return g() }`)
		require.Empty(t, errs)
		require.Equal(t, "fn <E>(g: fn () -> number throws E) -> number throws E", values["f"])
	})
	t.Run("CallBindsTheThrowsParameter", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f<E>(g: fn() -> number throws E) -> number throws E { return g() }
			fn h() { return f(fn () -> number throws string { throw "x" }) }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn () -> number throws string", values["h"])
	})
}

// A method declares and infers throws the same way a standalone function does, and a
// call through the receiver raises what the method declares.
func TestInferThrowsOnMethods(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Parser {
			text: string,
			constructor(mut self, text: string) { self.text = text },
			parse(self) -> number throws string { throw "bad input" },
		}
		fn run(p: Parser) { return p.parse() }
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Parser) -> number throws string", values["run"])
}

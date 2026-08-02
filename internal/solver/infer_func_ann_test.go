package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6 PR3: monomorphic function type annotations ---

// A monomorphic function type annotation resolves to a FuncType and an annotated
// binding adopts it, so the rendered binding type is the annotation.
func TestInferFuncAnnotationAdopted(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(x: number) -> string = fn (x) { return "a" }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number) -> string", values["f"])
}

// A body whose return type does not satisfy the annotated return is rejected,
// constrained body <: declared return.
func TestInferFuncAnnotationRejectsMismatchedBody(t *testing.T) {
	_, _, errs := inferSource(t, `val f: fn(x: number) -> string = fn (x) { return 5 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:50-1:51: cannot constrain 5 <: string", msgWithSpan(errs[0]))
}

// An inexact function annotation resolves its trailing `...` onto FuncType.Inexact
// and round-trips through the printer. The value is itself inexact so its
// accept-set [1, ∞] fills the inexact slot's [1, ∞].
func TestInferInexactFuncAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(x: number, ...) -> string = fn (x, ...) { return "a" }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number, ...) -> string", values["f"])
}

// A function annotation resolves as a union member, composing with PR2's union
// annotation input.
func TestInferFuncAnnotationUnionMember(t *testing.T) {
	values, _, errs := inferSource(t, `val f: (fn() -> number) | (fn() -> string) = fn () { return 5 }`)
	require.Empty(t, errs)
	require.Equal(t, "(fn () -> number) | (fn () -> string)", values["f"])
}

// The M3 accept-set callback-slot rule is now expressible in source. Reading the
// annotation as a callback slot `fn(x: number, y: number) -> number`, an inexact
// value `fn (x, ...)` is accepted: its accept-set [1, ∞] covers the slot's [2, 2].
func TestInferFuncAnnotationAcceptSetInexactValueAccepted(t *testing.T) {
	_, _, errs := inferSource(t, `val cb: fn(x: number, y: number) -> number = fn (x, ...) { return 1 }`)
	require.Empty(t, errs)
}

// A nullary inexact value `fn (...)` is likewise accepted into the two-param slot:
// its accept-set [0, ∞] covers [2, 2].
func TestInferFuncAnnotationAcceptSetNullaryInexactValueAccepted(t *testing.T) {
	_, _, errs := inferSource(t, `val cb: fn(x: number, y: number) -> number = fn (...) { return 1 }`)
	require.Empty(t, errs)
}

// An exact value with too few params is rejected: `fn (x)` has accept-set [1, 1],
// which does not cover the slot's [2, 2] at the upper bound.
func TestInferFuncAnnotationAcceptSetTooFewParamsRejected(t *testing.T) {
	_, _, errs := inferSource(t, `val cb: fn(x: number, y: number) -> number = fn (x) { return 1 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &FuncArityMismatchError{}, errs[0])
}

// An exact value with too many params is rejected: `fn (x, y, z)` has accept-set
// [3, 3], which demands more arguments than the slot's [2, 2] supplies.
func TestInferFuncAnnotationAcceptSetTooManyParamsRejected(t *testing.T) {
	_, _, errs := inferSource(t, `val cb: fn(x: number, y: number) -> number = fn (x, y, z) { return 1 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &FuncArityMismatchError{}, errs[0])
}

// A generic function annotation resolves its `<T>` list through resolveTypeParams and
// an annotated binding adopts the quantified type, so the rendered binding keeps its
// declared `<T>` as its only quantifier. Each case renders the binding named `f` and
// reports no error.
func TestInferGenericFuncAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// The type parameter reaches both a parameter and the return, so it is retained in
		// both positions. The initializer's own inference var links to a fresh instance
		// rather than the declared `T`, so it renders `fn <T>(x: T) -> T`, not the
		// double-quantified `fn <T0, T: T0>(x: T) -> T`.
		{
			name: "parameter and return",
			src:  `val f: fn<T>(x: T) -> T = fn (x) { return x }`,
			want: "fn <T>(x: T) -> T",
		},
		// A type parameter named in neither the parameters nor the return stays quantified
		// but unused, so it renders in the prefix over a monomorphic body.
		{
			name: "unused parameter",
			src:  `val f: fn<T>(x: number) -> number = fn (x) { return x }`,
			want: "fn <T>(x: number) -> number",
		},
		// Two distinct type parameters each stay their own quantifier.
		{
			name: "two parameters",
			src:  `val f: fn<T, U>(x: T, y: U) -> T = fn (x, y) { return x }`,
			want: "fn <T, U>(x: T, y: U) -> T",
		},
		// A generic function in RETURN position is rank-1: the quantifier floats out of the
		// positive position, so it is supported. The nested `T` renders on the inner function
		// without leaking the initializer's body var as an outer quantifier.
		{
			name: "generic return is rank-1",
			src:  `val f: fn(x: number) -> (fn<T>(y: T) -> T) = fn (x) { return fn (y) { return y } }`,
			want: "fn (x: number) -> fn <T>(y: T) -> T",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A generic function in parameter position is a rank-2 callback: the annotation
// `fn(cb: fn<T>(x: T) -> T) -> number` demands a caller pass an argument that is itself
// polymorphic. The `<T>` binder is kept on the parameter, so the binding resolves and
// renders the parameter with its quantifier rather than approximating it.
func TestInferHigherRankFuncParamResolves(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(cb: fn<T>(x: T) -> T) -> number = fn (cb) { return 1 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (cb: fn <T>(x: T) -> T) -> number", values["f"])
}

// The body of a function with a rank-2 callback parameter may call that callback at more
// than one type. Each `cb(...)` instantiates the callback's `T` independently, so `cb(5)`
// and `cb("hi")` both type-check within one body rather than unifying `T` across the two
// calls.
func TestInferRank2CallbackCalledAtSeveralTypes(t *testing.T) {
	src := `val f: fn(cb: fn<T>(x: T) -> T) -> number = fn (cb) {
  val a = cb(5)
  val b = cb("hi")
  return 1
}`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn (cb: fn <T>(x: T) -> T) -> number", values["f"])
}

// A polymorphic argument satisfies a rank-2 callback parameter. Passing the generic `id`
// into `f`'s `cb: fn<T>(x: T) -> T` parameter checks `id`'s type against the parameter by
// skolemizing `T`, which `id` satisfies, so the call type-checks and yields `f`'s return.
func TestInferRank2CallbackAcceptsPolymorphicArg(t *testing.T) {
	src := `
fn id<T>(x: T) -> T { return x }
val f: fn(cb: fn<T>(x: T) -> T) -> number = fn (cb) { return 1 }
val r = f(id)`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
}

// A monomorphic argument does not satisfy a rank-2 callback parameter. Passing `inc:
// fn(x: number) -> number` where `fn<T>(x: T) -> T` is expected checks `inc` against the
// parameter with `T` held rigid as a skolem, which `inc`'s concrete `number` cannot fill in
// either the argument or the result position, so the call is rejected.
func TestInferRank2CallbackRejectsMonomorphicArg(t *testing.T) {
	src := `
fn inc(x: number) -> number { return x }
val f: fn(cb: fn<T>(x: T) -> T) -> number = fn (cb) { return 1 }
val r = f(inc)`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 2)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.IsType(t, &CannotConstrainError{}, errs[1])
	require.Equal(t, "4:9-4:15: cannot constrain T <: number", msgWithSpan(errs[0]))
	require.Equal(t, "4:9-4:15: cannot constrain number <: T", msgWithSpan(errs[1]))
}

// A function-literal body whose return type is not the declared parameter does not
// satisfy the polymorphic annotation `fn <T>(x: T) -> T`, since a caller expecting `T`
// back would receive a `number`. The initializer is checked against the annotation with
// `T` held RIGID, so the concrete return `5` cannot satisfy `T` and the definition is
// rejected. The parameter-into-return flow in `fn (x) { return x }` still passes, since
// the body relates `T` only to itself.
func TestInferGenericFuncAnnotationRejectsNonPolymorphicBody(t *testing.T) {
	_, _, errs := inferSource(t, `val f: fn<T>(x: T) -> T = fn (x) { return 5 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:43-1:44: cannot constrain 5 <: T", msgWithSpan(errs[0]))
}

// Checking-mode skolems are concrete, so a parameter's skolem propagates through the
// initializer's own inference var and is checked where the body returns it. A body that
// returns a parameter typed `T` where the annotation promises a different type is therefore
// rejected even though the offending value flows through the lambda's param var rather than
// appearing as a literal. Each case renders `f` with its declared quantifier alongside the
// error.
func TestInferGenericFuncAnnotationChecksIndirectReturn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
		want string
	}{
		{
			// `return x` yields the skolem `T`, which cannot satisfy the concrete return
			// `number`, so the definition is rejected.
			name: "ParamReturnedAsConcrete",
			src:  `val f: fn<T>(x: T) -> number = fn (x) { return x }`,
			msg:  "1:32-1:51: cannot constrain T <: number",
			want: "fn <T>(x: T) -> number",
		},
		{
			// `return x` yields the first parameter's skolem `T`, which cannot satisfy the
			// second parameter's distinct skolem `U` in the return, so the two do not unify.
			name: "ParamReturnedAsDistinctSkolem",
			src:  `val f: fn<T, U>(x: T, y: U) -> U = fn (x, y) { return x }`,
			msg:  "1:36-1:58: cannot constrain T <: U",
			want: "fn <T, U>(x: T, y: U) -> U",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, &CannotConstrainError{}, errs[0])
			require.Equal(t, tt.msg, msgWithSpan(errs[0]))
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A skolem is a subtype of a union that contains it, so a body returning a parameter `T`
// into a `T | number` return is accepted: the caller's `T` is a valid `T | number`. A
// swapped two-parameter body that returns the value of the matching return parameter is
// likewise accepted. These guard the acceptance side against an over-eager skolem rejection.
func TestInferGenericFuncAnnotationChecksAcceptsPolymorphicBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ParamIntoUnionReturn",
			src:  `val f: fn<T>(x: T) -> (T | number) = fn (x) { return x }`,
			want: "fn <T>(x: T) -> T | number",
		},
		{
			name: "SecondParamReturned",
			src:  `val f: fn<T, U>(x: T, y: U) -> U = fn (x, y) { return y }`,
			want: "fn <T, U>(x: T, y: U) -> U",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A declared constraint `<U: T>` makes the skolem for `U` a subtype of the skolem for `T`,
// so a body that returns a `U` where the annotation promises `T` is accepted. Reversing the
// direction is still rejected, since the bound gives `U <: T`, not `T <: U`.
func TestInferGenericFuncAnnotationChecksBoundedParam(t *testing.T) {
	t.Run("BoundedParamReachesReturn", func(t *testing.T) {
		values, _, errs := inferSource(t, `val f: fn<T, U: T>(x: U) -> T = fn (x) { return x }`)
		require.Empty(t, errs)
		require.Equal(t, "fn <T, U: T>(x: U) -> T", values["f"])
	})
	t.Run("BoundDirectionIsOneWay", func(t *testing.T) {
		values, _, errs := inferSource(t, `val f: fn<T, U: T>(x: T) -> U = fn (x) { return x }`)
		require.Len(t, errs, 1)
		require.IsType(t, &CannotConstrainError{}, errs[0])
		require.Equal(t, "1:33-1:52: cannot constrain T <: U", msgWithSpan(errs[0]))
		require.Equal(t, "fn <T, U: T>(x: T) -> U", values["f"])
	})
}

// A throws clause in a function type annotation resolves to the declared type and
// renders back after the return type. The annotated function raises nothing, and a
// non-throwing body satisfies a throwing annotation because throws is covariant and the
// body's `never` is the bottom of the lattice.
func TestInferThrowsFuncAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(x: number) -> number throws boolean = fn (x) { return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number) -> number throws boolean", values["f"])
}

// A lifetime parameter in a function type annotation resolves against its declared
// bounds, which lower into constrainLt so they solve like bounds a body infers. Each
// case renders the binding named by `binding` and reports no error.
func TestInferLifetimeFuncAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		binding  string
		want     string
		wantErrs []string
	}{
		// A lifetime that names no borrow is inert, so it renders elided. Declaring it and
		// then naming nothing is dead weight, so the unused-binder companion warns.
		{
			name:     "unused lifetime",
			src:      `val f: fn<'a>(x: number) -> number = fn (x) { return x }`,
			binding:  "f",
			want:     "fn (x: number) -> number",
			wantErrs: []string{"lifetime parameter 'a is declared but never used"},
		},
		// A lifetime naming a borrow that reaches the output resolves to one shared
		// lifetime across the parameter and return, so it quantifies as `'a` on both.
		{
			name:    "named borrow shares one lifetime",
			src:     `val f: fn<'a>(p: &'a {x: number}) -> &'a {x: number} = fn (p) { return p }`,
			binding: "f",
			want:    "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
		},
		// A declared `'a: 'static` bound lowers to constrainLt('a, 'static). 'static is
		// the bottom of the outlives lattice and absorbs the meet, so 'a resolves to
		// 'static and both borrows render `&'static`. Without the lowering 'a would stay
		// a plain param lifetime, so this is the direct evidence a declared bound solves.
		{
			name:    "static bound forces static",
			src:     `val f: fn<'a: 'static>(p: &'a {x: number}) -> &'a {x: number} = fn (p) { return p }`,
			binding: "f",
			want:    "fn (p: &'static {x: number}) -> &'static {x: number}",
		},
		// A declared `'b: 'a` bound relates the two lifetimes. Only p is returned, so `'b`
		// reaches no output on its own, yet the bound keeps it named. The un-bounded twin
		// below elides `'b` to a bare `&`, isolating the bound's effect. The bound renders
		// back in the prefix as `'b: 'a`, since 'b outlives 'a.
		{
			name:    "outlives bound keeps connected lifetime",
			src:     `val f: fn<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number} = fn (p, q) { return p }`,
			binding: "f",
			want:    "fn <'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}",
		},
		{
			name:    "no bound elides unconnected lifetime",
			src:     `val f: fn<'a, 'b>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number} = fn (p, q) { return p }`,
			binding: "f",
			want:    "fn <'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number}",
		},
		// A function type annotation is its own lifetime scope, so a nested annotation's
		// declared bound stays local. The inner `g` declares `'a: 'static`, which would
		// force `'a` to 'static, but `outer` also names a borrow lifetime `'a`. The two
		// must not share a variable, so `outer`'s parameter stays a plain borrow lifetime.
		{
			name: "nested annotation scope is local",
			src: `fn outer<'a>(p: &'a {x: number}) {
  val g: fn<'a: 'static>(q: &'a {y: number}) -> &'a {y: number} = fn (q) { return q }
  return p
}`,
			binding: "outer",
			want:    "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantErrs, Messages(errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A destructuring parameter pattern is preserved in the resolved function type, so
// an object or tuple pattern renders and round-trips rather than degrading to a
// positional name.
func TestInferFuncAnnotationPreservesDestructuringPattern(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn({x, y}: {x: number, y: number}, [a, b]: [number, string]) -> number = fn (p, q) { return 1 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn ({x, y}: {x: number, y: number}, [a, b]: [number, string]) -> number", values["f"])
}

// A `...xs: T` parameter in a function type annotation sets FuncParam.Rest, so it
// round-trips through the printer as written. A tuple in the slot names the arguments the
// rest param binds one per element, so the annotation fixes the arity at two and a
// two-parameter function fills it.
func TestInferRestParamFuncAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(...xs: [number, string]) -> number = fn (x, y) { return 1 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (...xs: [number, string]) -> number", values["f"])
}

// The parser accepts a rest parameter in any position, without a type, and marked `?`.
// Resolution rejects all three and recovers the parameter to a positional one, so each case
// reports exactly the one message and nothing cascades from the recovery.
func TestInferRestParamFuncAnnotationRejections(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// acceptSet reads the Rest flag off the last parameter only.
			name: "NonFinalPosition",
			src:  `val f: fn(...xs: [number], y: string) -> number = fn (x, y) { return 1 }`,
			want: "1:11-1:16: a rest parameter must be the last parameter of a function type",
		},
		{
			// Without a type the slot says nothing about how many arguments it binds, so
			// keeping Rest would let the initializer decide the declared type's arity.
			name: "NoTypeAnnotation",
			src:  `val f: fn(...xs) -> number = fn (x) { return 1 }`,
			want: "1:11-1:16: a rest parameter in a function type must have a type annotation",
		},
		{
			// The `?` marker has no meaning on a rest parameter, whose slot type already
			// settles how many arguments it binds.
			name: "MarkedOptional",
			src:  `val f: fn(...xs?: [number]) -> number = fn (x) { return 1 }`,
			want: "1:11-1:16: a rest parameter cannot be marked optional",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A tuple-typed rest parameter is a value-level type, not only a conditional's pattern. It
// names its arguments one per element, so a function whose parameters line up with those
// elements fills the slot, and a value read out of the slot is callable, assignable, and
// passable wherever the expanded signature is. Each case reports no error.
func TestInferTupleRestParamFuncAnnotationAcceptsMatchingFunction(t *testing.T) {
	const two = "fn two(x: number, y: string) -> number { return 1 }\n"
	tests := []struct {
		name string
		src  string
	}{
		{
			// A direct call through the slot binds each argument to its element.
			name: "DirectCall",
			src: two + `val g: fn(...args: [number, string]) -> number = two
val r = g(1, "a")`,
		},
		{
			// The slot's expansion is the fixed-arity signature, so the two are interchangeable.
			name: "IntoFixedAritySlot",
			src: two + `val g: fn(...args: [number, string]) -> number = two
val h: fn(x: number, y: string) -> number = g`,
		},
		{
			name: "CallbackParameter",
			src:  two + `fn take(cb: fn(...args: [number, string]) -> number) -> number { return cb(1, "a") }` + "\n" + `val r = take(two)`,
		},
		{
			name: "ObjectTypeMethod",
			src:  two + `val o: {m: fn(...args: [number, string]) -> number} = {m: two}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
		})
	}
}

// The call-site arity lints read a tuple-typed rest parameter's real ceiling and floor, so
// each reports one message naming the count the tuple's elements add up to. An argument of
// the wrong type is still rejected against the element it lines up with.
func TestInferTupleRestParamCallDiagnostics(t *testing.T) {
	const decls = `fn two(x: number, y: string) -> number { return 1 }
val g: fn(...args: [number, string]) -> number = two
`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TooFew",
			src:  decls + `val r = g(1)`,
			want: "Not enough arguments: expected at least 2, but got 1",
		},
		{
			name: "TooMany",
			src:  decls + `val r = g(1, "a", true)`,
			want: "Too many arguments: expected at most 2, but got 3",
		},
		{
			name: "WrongElementType",
			src:  decls + `val r = g(1, 2)`,
			want: "cannot constrain 2 <: string",
		},
		{
			// An INEXACT tuple rest is left unexpanded, so it can require more arguments than
			// it declares parameters: this one declares two and requires three. The lint's
			// reshaped demand is padded to the required count rather than the parameter count,
			// so the accept-set gate does not report a second, redundant arity mismatch.
			name: "TooFewThroughAnInexactTupleRest",
			src: `val h: fn(a: number, ...args: [string, boolean, ...]) -> number = fn (...) { return 1 }` + "\n" +
				`val r = h(1)`,
			want: "Not enough arguments: expected at least 3, but got 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// An `Array<E>` rest parameter binds zero or more trailing arguments and checks each against
// E, which is the arity-and-element pair a tuple-typed rest cannot express. Each case reports
// exactly the listed messages.
func TestInferArrayRestParamFuncAnnotation(t *testing.T) {
	const decl = "val g: fn(...xs: Array<number>) -> number = fn (...) { return 1 }\n"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "RoundTripsThroughThePrinter",
			src:  `val f: fn(...xs: Array<number>) -> number = fn (...) { return 1 }`,
		},
		{
			// Zero or more, so no argument count is an arity error.
			name: "CallWithNoArguments",
			src:  decl + `val r = g()`,
		},
		{
			name: "CallWithSeveralArguments",
			src:  decl + `val r = g(1, 2, 3)`,
		},
		{
			// The element type is what each trailing argument is checked against. This is the
			// per-argument checking FuncParam.Rest deferred until an element type existed.
			name: "CallRejectsAWrongElement",
			src:  decl + `val r = g(1, "a")`,
			want: []string{`cannot constrain "a" <: number`},
		},
		{
			// A fixed-arity function fills the slot, since the rest parameter absorbs its
			// parameter list and checks each position against the element.
			name: "AcceptsAFixedArityFunction",
			src:  `fn two(x: number, y: number) -> number { return 1 }` + "\n" + `val h: fn(...xs: Array<number>) -> number = two`,
		},
		{
			name: "RejectsAFixedArityFunctionOnTheElement",
			src:  `fn two(x: number, y: string) -> number { return 1 }` + "\n" + `val h: fn(...xs: Array<number>) -> number = two`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			// Two array rest parameters pair as ordinary positions and compare element to
			// element. The pairing is contravariant and the array is covariant in its element,
			// so the super's `string` is checked against the sub's `number`.
			name: "ArrayRestAgainstArrayRest",
			src:  `val a: fn(...xs: Array<number>) -> number = fn (...) { return 1 }` + "\n" + `val b: fn(...ys: Array<string>) -> number = a`,
			want: []string{"cannot constrain string <: number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errs)
				return
			}
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Message()
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// A tuple-typed rest slot fixes the arity at the tuple's length, so the relaxation the
// gather rule introduces reaches only a conditional's pattern match. At value level both
// sides are written types, so an exact function of the wrong arity is still rejected.
func TestInferRestParamFuncAnnotationRejectsWrongArityValue(t *testing.T) {
	src := `fn one(x: number) -> string { return "a" }
val slot: fn(...args: [number, string]) -> string = one`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.IsType(t, &FuncArityMismatchError{}, errs[0])
	require.Equal(t, "cannot constrain function of arity 1 <: function of arity 2", errs[0].Message())
}

// The tuple's elements are the argument types the rest param binds, one per position, so a
// mismatched element is rejected contravariantly the way a fixed parameter would be.
func TestInferRestParamFuncAnnotationChecksTupleElements(t *testing.T) {
	src := `fn two(x: number, y: number) -> string { return "a" }
val slot: fn(...args: [number, string]) -> string = two`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "cannot constrain string <: number", errs[0].Message())
}

// A rest parameter whose slot is not a tuple binds zero or more arguments, so the
// annotation's accept-set is [0, ∞) and an exact function of any fixed arity fails to
// contain it. This is the arity effect the accept-set rule has always given a rest
// parameter; the annotation surface is what is new.
func TestInferUnboundedRestParamFuncAnnotationRejectsFixedArity(t *testing.T) {
	_, _, errs := inferSource(t, `val f: fn(...xs: number) -> number = fn (x) { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &FuncArityMismatchError{}, errs[0])
	require.Equal(t, "cannot constrain function of arity 1 <: function of arity 0 or more", errs[0].Message())
}

// The Variation-B check fires end-to-end through inexact function annotations.
// `wide` declares an extra param b typed number beyond the inexact slot's single
// named param. Assigning wide into the slot demands `unknown <: number` at b's
// position. The slot's open tail may pass an argument of any type there, and a
// number param cannot accept it. This is the Variation-B rule from exact-types
// §4.2.1.2. The extra param is optional so the accept-set arity gate passes and
// the per-param check is reached.
func TestInferFuncAnnotationVariationBRejectsExtraParam(t *testing.T) {
	src := `val wide: fn(a: number, b?: number, ...) -> number = fn (a, ...) { return 1 }
val slot: fn(x: number, ...) -> number = wide`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "2:42-2:46: cannot constrain unknown <: number", msgWithSpan(errs[0]))
}

// `fn (...args: Array<_>) -> _` is the written top of the function lattice. Its rest parameter
// absorbs whatever parameter list the argument declares, and the `Array<_>` element and the `_`
// return are inference placeholders, so neither constrains the argument further. It accepts a
// function of any arity and rejects everything that is not a function.
//
// In a type-parameter bound that is all it does, since nothing reads the solved placeholders. In a
// value annotation, which these cases use, `_` keeps its ordinary meaning of "infer this here", so
// the rendered binding shows what each placeholder was solved to. Each case renders the binding
// named `f` when it type-checks and reports one error when it does not.
func TestInferFunctionTopType(t *testing.T) {
	const top = "fn(...args: Array<_>) -> _"
	tests := []struct {
		name    string
		src     string
		want    string // the rendered `f` binding; checked when wantErr is ""
		wantErr string // "" ⇒ expect no error
	}{
		{
			// Nothing constrains the element, so it coalesces to the negative-position identity.
			name: "AcceptsNullary",
			src:  `val f: ` + top + ` = fn () { return 1 }`,
			want: "fn (...args: Array<unknown>) -> 1",
		},
		{
			// The rest parameter absorbs both declared params, so the arity is no obstacle. Each
			// absorbed position bounds the element variable from above, and a parameter is
			// contravariant, so the element coalesces to their meet.
			name: "AcceptsBinary",
			src:  `val f: ` + top + ` = fn (x: number, y: string) { return 1 }`,
			want: "fn (...args: Array<number & string>) -> 1",
		},
		{
			// A written function type flows in the same way a function expression does.
			name: "AcceptsFunctionTypedBinding",
			src:  `val g: fn(x: number) -> number = fn (x) { return x }` + "\n" + `val f: ` + top + ` = g`,
			want: "fn (...args: Array<number>) -> number",
		},
		{
			name:    "RejectsNumber",
			src:     `val f: ` + top + ` = 5`,
			wantErr: "cannot constrain 5 <: function",
		},
		{
			name:    "RejectsObject",
			src:     `val f: ` + top + ` = {a: 1}`,
			wantErr: "cannot constrain object <: function",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				require.Equal(t, tt.want, values["f"])
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

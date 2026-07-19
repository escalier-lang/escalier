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

// A generic function in parameter position is rank-2: the annotation `fn(cb: fn<T>(x: T)
// -> T) -> number` would demand a caller pass an argument that is itself polymorphic. That
// is beyond the rank-1 boundary, so it reports an unsupported feature and recovers to a
// fresh var for the parameter, keeping the outer function shape.
func TestInferHigherRankFuncParamReportsUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(cb: fn<T>(x: T) -> T) -> number = fn (cb) { return 1 }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:15-1:31: Unsupported: higher-rank function parameter", msgWithSpan(errs[0]))
	require.Equal(t, "fn (cb: unknown) -> number", values["f"])
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

// A throws clause reports the documented unsupported feature and recovers
// function-shaped.
func TestInferThrowsFuncAnnotationReportsUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(x: number) -> number throws boolean = fn (x) { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:8-1:46: Unsupported: throws clause in function type annotation", msgWithSpan(errs[0]))
	require.Equal(t, "fn (x: number) -> number", values["f"])
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

// A rest parameter reports the documented unsupported feature and recovers as a
// normal positional param, so it never sets FuncParam.Rest. acceptSet / hasRest /
// requiredCount assume a rest param is last, and the parser does not enforce that
// for a non-last `...x`, so a silently-set Rest could corrupt the accept-set.
func TestInferRestParamFuncAnnotationReportsUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn(...xs: number) -> number = fn (x) { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:11-1:16: Unsupported: rest parameter in function type annotation", msgWithSpan(errs[0]))
	require.Equal(t, "fn (xs: number) -> number", values["f"])
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

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

// A generic function annotation reports the documented unsupported feature and
// recovers function-shaped, so the binding still resolves as a function rather
// than collapsing to an unconstrained var.
func TestInferGenericFuncAnnotationReportsUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn<T>(x: number) -> number = fn (x) { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:8-1:34: Unsupported: generic function type annotation", msgWithSpan(errs[0]))
	require.Equal(t, "fn (x: number) -> number", values["f"])
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
		name    string
		src     string
		binding string
		want    string
	}{
		// A lifetime that names no borrow is inert, so it renders elided.
		{
			name:    "unused lifetime",
			src:     `val f: fn<'a>(x: number) -> number = fn (x) { return x }`,
			binding: "f",
			want:    "fn (x: number) -> number",
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
			src: `fn outer(p: &'a {x: number}) {
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
			require.Empty(t, errs)
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

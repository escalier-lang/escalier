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

// A lifetime-param'd function annotation reports the documented unsupported
// feature and recovers function-shaped.
func TestInferLifetimeFuncAnnotationReportsUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val f: fn<'a>(x: number) -> number = fn (x) { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "1:8-1:35: Unsupported: lifetime parameters in function type annotation", msgWithSpan(errs[0]))
	require.Equal(t, "fn (x: number) -> number", values["f"])
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

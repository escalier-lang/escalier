package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A second arm puts an overload set on the fully-annotated pre-bind path, which
// builds each arm's signature under a probe. `Array` resolves through a lazy
// package load that a probe declines to raise, so the annotation used to read as a
// bare var and the arm checked nothing. Each row here writes an annotation that
// must survive that path.
func TestInferOverloadArmKeepsItsParameterAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "array",
			src: `
				fn g(xs: Array<number>) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: Array<number>) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "nested array",
			src: `
				fn g(xs: Array<Array<string>>) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: Array<Array<string>>) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "mut array",
			src: `
				fn g(xs: mut Array<number>) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: mut Array<number>) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "array in a tuple",
			src: `
				fn g(xs: [Array<number>, string]) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: [Array<number>, string]) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "array in an object",
			src: `
				fn g(xs: {items: Array<number>}) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: {items: Array<number>}) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "promise",
			src: `
				fn g(xs: Promise<number>) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: Promise<number>) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "tuple",
			src: `
				fn g(xs: [number, string]) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: [number, string]) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "object",
			src: `
				fn g(xs: {y: number}) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: {y: number}) -> number) & (fn (a: string, b: string) -> string)",
		},
		{
			name: "primitive",
			src: `
				fn g(xs: number) -> number { return 1 }
				fn g(a: string, b: string) -> string { return a }
			`,
			want: "(fn (xs: number) -> number) & (fn (a: string, b: string) -> string)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, values["g"])
		})
	}
}

// An arm's `Array` annotation is only useful if a call is checked against its
// element type. A wrong element has to reject, or the annotation is decoration.
func TestInferOverloadArmChecksItsArrayElement(t *testing.T) {
	t.Run("a matching element accepts", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare fn g(xs: Array<number>) -> number
			declare fn g(a: string, b: string) -> string
			fn call(ns: Array<number>) -> number { return g(ns) }
		`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, "fn (ns: Array<number>) -> number", values["call"])
	})
	t.Run("a mismatched element rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn g(xs: Array<number>) -> number
			declare fn g(a: string, b: string) -> string
			fn call(ss: Array<string>) -> number { return g(ss) }
		`)
		require.Equal(t,
			[]string{"4:50-4:55: No matching overload for this call\n" +
				"  fn (xs: Array<number>) -> number\n" +
				"  fn (a: string, b: string) -> string"},
			messagesWithSpan(t, errs))
	})
}

// The fully-annotated pre-bind path and the group-var path reach the same
// parameter type for the same written annotation. Dropping the return annotation
// is what moves a set from the first to the second, so these two differ in
// nothing else.
func TestInferOverloadPathsAgreeOnAnArrayParameter(t *testing.T) {
	annotated, _, errs := inferSource(t, `
		fn g(...xs: mut Array<number>) -> number { return 1 }
		fn g(a: string, b: string) -> string { return a }
	`)
	require.Empty(t, messagesWithSpan(t, errs))

	unannotated, _, errs := inferSource(t, `
		fn g(...xs: mut Array<number>) { return 1 }
		fn g(a: string, b: string) { return a }
	`)
	require.Empty(t, messagesWithSpan(t, errs))

	require.Equal(t,
		"(fn (...xs: mut Array<number>) -> number) & (fn (a: string, b: string) -> string)",
		annotated["g"])
	require.Equal(t,
		"(fn (...xs: mut Array<number>) -> 1) & (fn (a: string, b: string) -> string)",
		unannotated["g"])
}

// An arm's own `<T: C>` constraint records an upper bound on the parameter's var
// while the signature is built. The pre-bind's probe used to discard that bound, so
// an arm rendered as a bare `<T>` and its constraint enforced nothing.
func TestInferOverloadArmKeepsItsTypeParamBound(t *testing.T) {
	t.Run("the bound is rendered", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare fn g<T: number>(xs: T) -> number
			declare fn g(a: string, b: string) -> string
		`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t,
			"(fn <T: number>(xs: T) -> number) & (fn (a: string, b: string) -> string)",
			values["g"])
	})
	t.Run("the bound is enforced", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn g<T: number>(xs: T) -> number
			declare fn g(a: string, b: string) -> string
			fn call(s: string) -> number { return g(s) }
		`)
		require.Equal(t,
			[]string{"4:42-4:46: No matching overload for this call\n" +
				"  fn <T: number>(xs: T) -> number\n" +
				"  fn (a: string, b: string) -> string"},
			messagesWithSpan(t, errs))
	})
	t.Run("an unbounded binder still accepts anything", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn g<T>(xs: T) -> number
			declare fn g(a: string, b: string) -> string
			fn call(s: string) -> number { return g(s) }
		`)
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// Committing the pre-bind's probe keeps its bounds but must not keep its
// diagnostics: phase 2's inferFunc re-derives each signature while checking the
// body, so a signature error reported under the pre-bind as well would surface
// twice for one mistake.
func TestInferOverloadArmReportsASignatureErrorOnce(t *testing.T) {
	tests := []struct {
		name string
		arm  string
	}{
		{name: "parameter annotation", arm: "fn g(xs: NoSuchType) -> number { return 1 }"},
		{name: "return annotation", arm: "fn g(xs: number) -> NoSuchType { return 1 }"},
		{name: "type parameter bound", arm: "fn g<T: NoSuchType>(xs: T) -> number { return 1 }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t,
				tt.arm+"\nfn g(a: string, b: string) -> string { return a }")
			require.Len(t, errs, 1)
			require.Equal(t, "cannot find type `NoSuchType`", errs[0].Message())
		})
	}
}

// An overloaded method and constructor are built by buildMemberSigs rather than by
// the top-level pre-bind, so they never lost the annotation. This pins that.
func TestInferOverloadedMemberKeepsItsArrayAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare class D {
			constructor(mut self, xs: Array<number>),
			constructor(mut self, a: string, b: string),
		}
	`)
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t,
		"{new (xs: Array<number>) -> D; new (a: string, b: string) -> D}",
		values["D"])
}

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A generator annotation resolves to the soltype concrete and renders back in the form
// it was written, so `Generator<Y, R, N>` and `AsyncGenerator<Y, R, N>` round-trip. No
// inference produces one yet — a `gen fn` body is walked in a later change — so these
// reach the type through annotations alone.
func TestResolveGeneratorAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "SyncGeneratorParam",
			src:  `fn f(g: Generator<number, string, never>) { return g }`,
			want: `fn (g: Generator<number, string, never>) -> Generator<number, string, never>`,
		},
		{
			name: "AsyncGeneratorParam",
			src:  `fn f(g: AsyncGenerator<number, string, never>) { return g }`,
			want: `fn (g: AsyncGenerator<number, string, never>) -> AsyncGenerator<number, string, never>`,
		},
		{
			name: "DeclaredReturn",
			src:  `declare fn f() -> Generator<1 | 2, undefined, never>`,
			want: `fn () -> Generator<1 | 2, undefined, never>`,
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

// Yield and Ret are covariant, being what the generator hands out. Next is
// contravariant, being the value a caller sends back in through next(v), so a generator
// accepting a wider input satisfies a requirement for a narrower one.
func TestGeneratorVariance(t *testing.T) {
	accepts := []struct {
		name string
		src  string
	}{
		{
			name: "CovariantYieldAndRet",
			src: `
				declare fn g() -> Generator<1, "r", never>
				val f: fn () -> Generator<number, string, never> = g
			`,
		},
		{
			name: "ContravariantNext",
			src: `
				declare fn g() -> Generator<number, string, unknown>
				val f: fn () -> Generator<number, string, number> = g
			`,
		},
	}
	for _, test := range accepts {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
		})
	}

	rejects := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "YieldIsNotContravariant",
			src: `
				declare fn g() -> Generator<number, string, never>
				val f: fn () -> Generator<1, string, never> = g
			`,
			wantErrs: []string{"3:51-3:52: cannot constrain number <: 1"},
		},
		{
			name: "NextIsNotCovariant",
			src: `
				declare fn g() -> Generator<number, string, number>
				val f: fn () -> Generator<number, string, unknown> = g
			`,
			wantErrs: []string{"3:58-3:59: cannot constrain unknown <: number"},
		},
		{
			// A sync generator and an async one are unrelated: neither is a subtype of
			// the other whatever their slots hold.
			name: "SyncIsNotAsync",
			src: `
				declare fn g() -> Generator<number, string, never>
				val f: fn () -> AsyncGenerator<number, string, never> = g
			`,
			wantErrs: []string{"3:61-3:62: cannot constrain Generator<number, string, never> <: AsyncGenerator<number, string, never>"},
		},
	}
	for _, test := range rejects {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Len(t, errs, len(test.wantErrs))
			for i, want := range test.wantErrs {
				require.Equal(t, want, msgWithSpan(errs[i]))
			}
		})
	}
}

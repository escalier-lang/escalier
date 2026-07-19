package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A standalone `fn f<T>(…)` declaration resolves its explicit type-parameter list into
// the function's own FuncType.TypeParams, so the declared quantifier renders with the
// written name and each call instantiates it independently, each argument keeping its
// own literal type rather than widening or unifying across calls.
func TestInferGenericFuncDecl(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// The declared `<T>` renders with its written name, and the two calls
			// instantiate it independently, so `a` and `b` keep their own literal types.
			name: "IdentityRendersAndInstantiates",
			src: `
				fn id<T>(x: T) -> T { return x }
				val a = id(5)
				val b = id("hi")
			`,
			want: map[string]string{
				"id": "fn <T>(x: T) -> T",
				"a":  "5",
				"b":  `"hi"`,
			},
		},
		{
			// A parameter used only in the return position stays a valid quantifier
			// rather than inlining to never.
			name: "ReturnOnlyParam",
			src:  `fn make<T>(x: number) -> T { return x }`,
			want: map[string]string{
				"make": "fn <T>(x: number) -> T",
			},
		},
		{
			// A bodyless `declare fn` resolves its type-parameter list the same way,
			// adopting its declared return without a body to constrain.
			name: "DeclareFn",
			src:  `declare fn id<T>(x: T) -> T`,
			want: map[string]string{
				"id": "fn <T>(x: T) -> T",
			},
		},
		{
			// Two distinct type parameters bind independently, so a call passing a
			// number and a string returns a tuple of each argument's literal type.
			name: "TwoParams",
			src: `
				fn pair<T, U>(x: T, y: U) -> [T, U] { return [x, y] }
				val p = pair(1, "two")
			`,
			want: map[string]string{
				"pair": "fn <T, U>(x: T, y: U) -> [T, U]",
				"p":    `[1, "two"]`,
			},
		},
		{
			// A bounded parameter keeps its bound on the quantifier but not on the use
			// site, so `x` reads as `T` while `<T: number>` carries the constraint.
			name: "BoundedParam",
			src:  `fn first<T: number>(x: T) -> T { return x }`,
			want: map[string]string{
				"first": "fn <T: number>(x: T) -> T",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "type mismatch for %q", name)
			}
		})
	}
}

// A method's own type parameters stay gated at the rank-1 boundary: per-instance
// projection is not yet applied by the class-body freeze, so resolving them would
// collapse two calls to one shared var. The method reports the type-param feature as
// unsupported and infers monomorphically rather than panicking in the body freeze.
func TestInferGenericMethodStillGated(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Box {
			wrap<T>(self, x: T) -> T { return x }
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.Contains(t, msgs, "Unsupported: TypeParam")
}

// A parameter whose own type is polymorphic — a callback annotated `fn <V>(x: V) -> V` —
// stays rejected at the rank-1 boundary. The type-parameter list on the declaration
// resolves, but the generic function-type annotation on the parameter is the higher-rank
// surface a later PR handles, so it reports as unsupported rather than being approximated.
func TestInferGenericFuncHigherRankParamRejected(t *testing.T) {
	_, _, errs := inferSource(t, `fn apply<T>(g: fn <V>(x: V) -> V, y: T) -> T { return g(y) }`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.Contains(t, msgs, "Unsupported: generic function type annotation")
}

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferTypeParamDefaultAtCall covers a type parameter's default at a call or a
// construction. A parameter that nothing in the call constrains takes its default, and
// one that the call constrains takes what inference gives it.
func TestInferTypeParamDefaultAtCall(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name:    "FunctionWithNoCandidate",
			src:     "declare fn make<T = number>() -> Array<T>\nval xs = make()",
			binding: "xs",
			want:    "Array<number>",
		},
		{
			name:    "ConstructorWithNoCandidate",
			src:     "class Box<T = number> { v: Array<T> }\nval b = Box([])",
			binding: "b",
			want:    "Box<number>",
		},
		{
			name:    "CallableAliasWithNoCandidate",
			src:     "type Cb = fn <T = number>() -> Array<T>\ndeclare val cb: Cb\nval ws = cb()",
			binding: "ws",
			want:    "Array<number>",
		},
		{
			name:    "ArgumentCandidate",
			src:     "declare fn wrap<T = number>(x: T) -> Array<T>\nval xs = wrap(\"s\")",
			binding: "xs",
			want:    `Array<"s">`,
		},
		{
			name:    "ExpectedTypeCandidate",
			src:     "declare fn make<T = number>() -> Array<T>\nval xs: Array<string> = make()\nval ys = xs",
			binding: "ys",
			want:    "Array<string>",
		},
		{
			// `U`'s default names `T`, so it resolves to whatever `T` is inferred as.
			name:    "DefaultNamingASibling",
			src:     "declare fn pair<T, U = T>(x: T) -> [T, Array<U>]\nval p = pair(\"s\")",
			binding: "p",
			want:    `["s", Array<"s">]`,
		},
		{
			// The callback's parameter bounds `T` from above, so the default does not
			// override it.
			name:    "CallbackCandidate",
			src:     "declare fn take<T = number>(f: fn (x: T) -> undefined) -> fn (x: T) -> undefined\nval r = take(fn (s: string) {})",
			binding: "r",
			want:    "fn (x: string) -> undefined",
		},
		{
			// `g` generalizes over the call, and each use instantiates the default again.
			name:    "ThroughAGeneralizedFunction",
			src:     "declare fn make<T = number>() -> Array<T>\nfn g() { return make() }\nval r = g()",
			binding: "r",
			want:    "Array<number>",
		},
		{
			// `U` is bounded by `T` and defaults to `T`, so coalescing `U`'s default walks
			// back into the bound graph `U` is part of. The result names no μ-variable.
			name:    "DefaultInsideABoundCycle",
			src:     "declare fn f<T, U: T = T>(x: T) -> Array<U>\nval r = f(1)",
			binding: "r",
			want:    "Array<1>",
		},
		{
			name:    "NoDefault",
			src:     "declare fn make<T>() -> Array<T>\nval xs = make()",
			binding: "xs",
			want:    "Array<never>",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values[test.binding])
		})
	}
}

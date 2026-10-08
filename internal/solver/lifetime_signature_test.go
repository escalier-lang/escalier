package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignatureImpliesBodyLifetimes covers the relations a body may impose between the
// lifetimes its signature names. Each must follow from a declared bound, a chain of them, or
// a bound the signature's own types imply. An unnamed lifetime stays inferred, and a
// declared function with no body is not checked.
func TestSignatureImpliesBodyLifetimes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an undeclared relation between two named lifetimes reports",
			src:  `fn f<'a, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
			want: []string{"1:6-1:8: the body requires 'a to outlive 'b, but the signature does not declare it; add the bound 'a: 'b"},
		},
		{
			name: "a declared bound covers the relation",
			src:  `fn f<'a: 'b, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
		},
		{
			name: "a chain of declared bounds covers the relation",
			src: `fn f<'a: 'b, 'b: 'c, 'c>(x: &'a {x: number}, y: &'b {x: number}, z: &'c {x: number}) -> &'c {x: number} {
				val u: &'b {x: number} = x
				return u
			}`,
		},
		{
			name: "a parameter connected to a return-only lifetime is not assumed to outlive it",
			src: `fn f<'a, 'b, 'c>(x: &'a {x: number}, y: &'b {x: number}) -> &'c {x: number} {
				val w: &'c {x: number} = y
				val k: &'a {x: number} = w
				return y
			}`,
			want: []string{
				"1:10-1:12: the body requires 'b to outlive 'a, but the signature does not declare it; add the bound 'b: 'a",
				"1:10-1:12: the body requires 'b to outlive 'c, but the signature does not declare it; add the bound 'b: 'c",
				"1:14-1:16: the body requires 'c to outlive 'a, but the signature does not declare it; add the bound 'c: 'a",
			},
		},
		{
			name: "forcing a named lifetime to 'static reports",
			src:  `fn f<'a>(x: &'a {x: number}) -> &'static {x: number} { return x }`,
			want: []string{"1:6-1:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
		{
			name: "a declared 'static bound covers forcing the lifetime to 'static",
			src:  `fn f<'a: 'static>(x: &'a {x: number}) -> &'static {x: number} { return x }`,
		},
		{
			name: "a nested borrow implies its inner lifetime outlives its outer one",
			src:  `fn f<'a, 'b>(p: &'a &'b {x: number}) -> &'a {x: number} { return p }`,
		},
		{
			name: "an unnamed lifetime stays inferred",
			src:  `fn f(x: &{x: number}, y: &{x: number}) -> &{x: number} { return x }`,
		},
		{
			name: "a declared function with no body is not checked",
			src:  `declare fn f<'a, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

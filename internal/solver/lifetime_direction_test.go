package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBorrowOutlivesItsDestination covers the direction borrow subtyping relates two
// lifetimes in. A borrow flows only somewhere that lives no longer than it does, so
// `&'s T <: &'p T` makes 's outlive 'p. A declared bound in that direction is proven by the
// body, a bound in the other direction is not, and a call funneling two borrows into one
// lifetime parameter makes each argument outlive the result.
func TestBorrowOutlivesItsDestination(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		want  []string
		value string
		typ   string
	}{
		{
			name: "returning a borrow proves its lifetime outlives the result's",
			src:  `fn f<'a: 'b, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
		},
		{
			name: "returning a borrow does not prove the result's lifetime outlives it",
			src:  `fn f<'a, 'b: 'a>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
			want: []string{
				"1:10-1:16: declared lifetime bound 'b: 'a is not satisfied; the body does not make 'b outlive 'a",
				"1:6-1:8: the body requires 'a to outlive 'b, but the signature does not declare it; add the bound 'a: 'b",
			},
		},
		{
			name: "each argument funneled into one lifetime parameter outlives the result",
			src: `
				fn twoArgs<'r>(a: &'r {x: number}, b: &'r {x: number}) -> &'r {x: number} { return a }
				fn callTwo(a: &{x: number}, b: &{x: number}) { return twoArgs(a, b) }
			`,
			value: "callTwo",
			typ:   "fn <'a: 'c, 'b: 'c, 'c>(a: &'a {x: number}, b: &'b {x: number}) -> &'c {x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
			if tt.value != "" {
				require.Equal(t, tt.typ, values[tt.value])
			}
		})
	}
}

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWrittenLifetimeWidened covers bodies that relate the lifetimes of a written signature
// more tightly than the signature states.
func TestWrittenLifetimeWidened(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"ReturnsTheNamedInput": {
			src: `
				type Flat = {x: number}
				fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat { return a }
			`,
		},
		"ReturnsTheUnnamedInput": {
			src: `
				type Flat = {x: number}
				fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat { return b }
			`,
			want: []string{"3:43-3:51: 'b' flows into the return type, but the return type borrows only from 'x"},
		},
		// The body returns a into the member that names 'a, so it relates 'a to no other
		// lifetime of the signature.
		"ReturnsIntoTheUnionMemberThatNamesIt": {
			src: `
				type Flat = {x: number}
				class Box {
					inner: Flat,
					pick<'a>(&self, a: &'a Flat, c: boolean) -> &Flat | &'a Flat {
						if c { return &self.inner } else { return a }
					}
				}
			`,
		},
		"ThrowsTheUnnamedInput": {
			src: `
				type Flat = {x: number}
				fn pick<'x>(a: &'x Flat, b: &Flat) -> never throws &'x Flat { throw b }
			`,
			want: []string{"3:56-3:64: 'b' flows into the thrown type, but the thrown type borrows only from 'x"},
		},
		"StoresAnInputIntoAnother": {
			src: `
				type Flat = {x: number}
				fn put(out: &mut {r: &Flat}, q: &Flat) -> undefined { out.r = q }
			`,
			want: []string{"3:37-3:42: 'q' flows into 'out', but the signature does not relate their lifetimes; name a lifetime on each and add a bound"},
		},
		"StoresAnInputIntoAnotherWithABound": {
			src: `
				type Flat = {x: number}
				fn put<'b>(out: &mut {r: &'b Flat}, q: &'b Flat) -> undefined { out.r = q }
			`,
		},
		"MethodReturnsTheReceiver": {
			src: `
				class Box {
					inner: {x: number},
					peek(&self, other: &{x: number}) -> &{x: number} { return &self.inner }
				}
			`,
		},
		"MethodReturnsAnotherParam": {
			src: `
				class Box {
					inner: {x: number},
					peek(&self, other: &{x: number}) -> &{x: number} { return other }
				}
			`,
			want: []string{"4:42-4:54: 'other' flows into the return type, but the return type borrows only from the receiver"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

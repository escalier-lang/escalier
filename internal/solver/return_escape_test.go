package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReturnEscape covers PR 15's return-escape rule: a value flowing out of the frame
// may not borrow a function-local, since the local dies when the frame returns. A borrow
// of a parameter is exempt, because its lifetime is supplied by the caller and already
// outlives the return.
func TestReturnEscape(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Returning a binding that borrows a local escapes the local it borrows. The
		// binding a holds `&mut b`, so returning a would leave a's edge dangling at b,
		// which dies with the frame.
		"ReturnBindingBorrowingLocal": {
			src: `
				fn build() -> {peer: &mut {value: number}} {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					return a
				}
			`,
			want: []string{"5:13-5:14: borrowed value 'b' does not live long enough to escape the function"},
		},
		// A borrow of a local written directly into the returned literal escapes the same
		// way, with no intervening binding.
		"ReturnInlineBorrowOfLocal": {
			src: `
				fn build() -> {peer: &mut {value: number}} {
					val mut b = {value: 2}
					return {peer: &mut b}
				}
			`,
			want: []string{"4:13-4:27: borrowed value 'b' does not live long enough to escape the function"},
		},
		// Returning the borrow itself, `return &mut b`, escapes the local b.
		"ReturnDirectBorrowOfLocal": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 2}
					return &mut b
				}
			`,
			want: []string{"4:13-4:19: borrowed value 'b' does not live long enough to escape the function"},
		},
		// A binding that borrows two locals escapes both, reported in source order.
		"ReturnBindingBorrowingTwoLocals": {
			src: `
				fn build() -> {p: &mut {x: number}, q: &mut {x: number}} {
					val mut b = {x: 0}
					val mut c = {x: 1}
					val a = {p: &mut b, q: &mut c}
					return a
				}
			`,
			want: []string{
				"6:13-6:14: borrowed value 'b' does not live long enough to escape the function",
				"6:13-6:14: borrowed value 'c' does not live long enough to escape the function",
			},
		},
		// A whole-binding move carries the borrow forward: `val a2 = a` moves a's value,
		// borrow and all, into a2, so returning a2 escapes the same local a would.
		"ReturnMovedCarrierEscapes": {
			src: `
				fn build() -> {peer: &mut {value: number}} {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					val a2 = a
					return a2
				}
			`,
			want: []string{"6:13-6:15: borrowed value 'b' does not live long enough to escape the function"},
		},
		// Returning a disjoint owned field of a binding that also has a borrow field does
		// not escape: `a.data` carries none of a's borrow of b. The whole-binding edge is
		// not followed for a field return, so no spurious escape is reported.
		"ReturnDisjointFieldOk": {
			src: `
				fn build() -> {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.data
				}
			`,
			want: nil,
		},
		// Returning a parameter borrow is sound: the borrow carries the caller's lifetime,
		// which outlives the call.
		"ReturnParamBorrowOk": {
			src: `
				fn pass(p: &mut {x: number}) -> &mut {x: number} {
					return p
				}
			`,
			want: nil,
		},
		// Borrowing a parameter and returning the borrow is sound for the same reason.
		"ReturnBorrowOfParamOk": {
			src: `
				fn pass(p: mut {x: number}) {
					return &mut p
				}
			`,
			want: nil,
		},
		// A local that only borrows a parameter is returnable: the edge to the parameter
		// is never recorded, so returning the local raises no escape.
		"LocalBorrowsParamThenReturnOk": {
			src: `
				fn pass(p: &mut {x: number}) -> {peer: &mut {x: number}} {
					val a = {peer: p}
					return a
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
		})
	}
}

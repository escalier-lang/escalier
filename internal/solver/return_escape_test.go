package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReturnEscape covers the return-escape rule: a value flowing out of the frame
// may not borrow a function-local, since the local dies when the frame returns. A borrow
// of a parameter is exempt, because its lifetime is supplied by the caller and already
// outlives the return. Each case also pins the function's inferred type, since the escape
// is reported alongside ordinary inference rather than replacing it.
func TestReturnEscape(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Returning a binding that borrows a local escapes the local it borrows. The
		// binding a holds `&mut b`, so returning a would leave a's edge dangling at b,
		// which dies with the frame.
		"ReturnBindingBorrowingLocal": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					return a
				}
			`,
			want:  []string{"5:13-5:14: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// A borrow of a local written directly into the returned literal escapes the same
		// way, with no intervening binding.
		"ReturnInlineBorrowOfLocal": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return {peer: &mut b}
				}
			`,
			want:  []string{"4:13-4:27: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// Returning the borrow itself, `return &mut b`, escapes the local b.
		"ReturnDirectBorrowOfLocal": {
			src: `
				fn build() {
					val mut b = {value: 2}
					return &mut b
				}
			`,
			want:  []string{"4:13-4:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// A binding that borrows two locals escapes both, reported in source order.
		"ReturnBindingBorrowingTwoLocals": {
			src: `
				fn build() {
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
			types: map[string]string{"build": "fn () -> {p: &mut {x: number}, q: &mut {x: number}}"},
		},
		// A whole-binding move carries the borrow forward: `val a2 = a` moves a's value,
		// borrow and all, into a2, so returning a2 escapes the same local a would.
		"ReturnMovedCarrierEscapes": {
			src: `
				fn build() {
					val mut b = {value: 2}
					val a = {peer: &mut b}
					val a2 = a
					return a2
				}
			`,
			want:  []string{"6:13-6:15: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> {peer: &mut {value: number}}"},
		},
		// Returning the borrow field itself escapes the local it holds: `a.peer` is the
		// `&mut b` borrow, so returning it leaves b dangling. The field-granular edge at
		// [peer] is followed for the field return.
		"ReturnBorrowFieldEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.peer
				}
			`,
			want:  []string{"5:13-5:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Reading a field through a whole-binding borrow escapes the borrowed local: `a`
		// borrows all of b, so `a.peer` projects into b and returning it leaves b dangling.
		// The edge a → b sits at path [], above the read path [peer], so the field return
		// still follows it.
		"ReturnFieldThroughWholeBorrowEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {peer: {value: 0}}
					val a = &mut b
					return a.peer
				}
			`,
			want:  []string{"5:13-5:19: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// A borrow of a local written inside a control-flow carrier escapes the same as one
		// written directly: the scan descends the if/else branches to find the `&mut b`.
		"ReturnBorrowInIfBranchEscapes": {
			src: `
				fn build() -> &mut {value: number} {
					val mut b = {value: 0}
					return if true { &mut b } else { &mut b }
				}
			`,
			want:  []string{"4:12-4:47: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"build": "fn () -> &mut {value: number}"},
		},
		// Returning a disjoint owned field of a binding that also has a borrow field does
		// not escape: `a.data` carries none of a's borrow of b. The field return follows
		// only the edges at [data], finds none, and reports no spurious escape.
		"ReturnDisjointFieldOk": {
			src: `
				fn build() -> {value: number} {
					val mut b = {value: 2}
					val a = {peer: &mut b, data: {value: 7}}
					return a.data
				}
			`,
			want:  nil,
			types: map[string]string{"build": "fn () -> {value: number}"},
		},
		// Returning a parameter borrow is sound: the borrow carries the caller's lifetime,
		// which outlives the call.
		"ReturnParamBorrowOk": {
			src: `
				fn pass(p: &mut {x: number}) {
					return p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}"},
		},
		// Borrowing a parameter and returning the borrow is sound for the same reason.
		"ReturnBorrowOfParamOk": {
			src: `
				fn pass(p: mut {x: number}) {
					return &mut p
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn (p: mut {x: number}) -> &mut {x: number}"},
		},
		// A local that only borrows a parameter is returnable: the edge to the parameter
		// is never recorded, so returning the local raises no escape.
		"LocalBorrowsParamThenReturnOk": {
			src: `
				fn pass(p: &mut {x: number}) {
					val a = {peer: p}
					return a
				}
			`,
			want:  nil,
			types: map[string]string{"pass": "fn <'a>(p: &'a mut {x: number}) -> {peer: &'a mut {x: number}}"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
			require.Equal(t, tc.types, values)
		})
	}
}

// TestEscapeAtStoreAndArgSites covers the other two flow-out sites: a field store
// into a parameter, where the value flows into the caller's object, and a consuming
// argument, where it flows into the callee. A borrow of a local that flows out either
// way escapes, while a parameter borrow and a plain owned value do not. Each case also
// pins the inferred type of every function it declares.
func TestEscapeAtStoreAndArgSites(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Storing a borrow of a local into a parameter's field escapes: the parameter's
		// object outlives the frame, so the stored local would dangle in the caller.
		"StoreLocalBorrowIntoParamField": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}) {
					val mut b = {value: 0}
					p.peer = &mut b
				}
			`,
			want:  []string{"4:15-4:21: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}) -> void"},
		},
		// Storing a parameter borrow into a parameter's field is sound: the stored borrow
		// carries the caller's lifetime, which outlives the frame.
		"StoreParamBorrowIntoParamFieldOk": {
			src: `
				fn f(p: mut {peer: &mut {value: number}}, q: &mut {value: number}) {
					p.peer = q
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (p: mut {peer: &mut {value: number}}, q: &mut {value: number}) -> void"},
		},
		// Passing a value that borrows a local as a consuming argument escapes: the callee
		// takes ownership of the value and could retain it past the frame.
		"ConsumingArgCarriesLocalBorrow": {
			src: `
				fn store(x: {peer: &mut {value: number}}) {}
				fn f() {
					val mut b = {value: 0}
					val a = {peer: &mut b}
					store(a)
				}
			`,
			want: []string{"6:12-6:13: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"store": "fn (x: {peer: &mut {value: number}}) -> void",
				"f":     "fn () -> void",
			},
		},
		// Auto-borrowing a local into a `&mut` parameter is sound: the parameter borrows
		// for the call rather than consuming, so the local outlives the borrow.
		"BorrowArgToRefParamOk": {
			src: `
				fn read(x: &mut {value: number}) {}
				fn f() {
					val mut b = {value: 0}
					read(&mut b)
				}
			`,
			want: nil,
			types: map[string]string{
				"read": "fn (x: &mut {value: number}) -> void",
				"f":    "fn () -> void",
			},
		},
		// A consuming argument that is a plain owned value carries no borrow, so it moves
		// into the callee with no escape.
		"ConsumingArgOwnedValueOk": {
			src: `
				fn store(x: {value: number}) {}
				fn f() {
					val a = {value: 0}
					store(a)
				}
			`,
			want: nil,
			types: map[string]string{
				"store": "fn (x: {value: number}) -> void",
				"f":     "fn () -> void",
			},
		},
		// A borrow passed to an inner call is consumed by that call, not carried out by
		// the owned value the call yields. Wrapping the call in a consuming call does not
		// escape the local, so the scan must stop at the inner call boundary.
		"BorrowConsumedByInnerCallDoesNotEscape": {
			src: `
				fn read(x: &mut {value: number}) -> {value: number} {
					return {value: 1}
				}
				fn store(y: {value: number}) {}
				fn f() {
					val mut b = {value: 0}
					store(read(&mut b))
				}
			`,
			want: nil,
			types: map[string]string{
				"read":  "fn (x: &mut {value: number}) -> {value: number}",
				"store": "fn (y: {value: number}) -> void",
				"f":     "fn () -> void",
			},
		},
		// A single escape through a consuming call inside a return is reported once, at the
		// argument where the local borrow flows into the callee, not a second time at the
		// enclosing return.
		"EscapeThroughConsumingCallReportedOnce": {
			src: `
				fn id(y: {peer: &mut {value: number}}) {
					return y
				}
				fn f() {
					val mut b = {value: 0}
					return id({peer: &mut b})
				}
			`,
			want: []string{"7:16-7:30: borrowed value 'b' does not live long enough to escape the function"},
			types: map[string]string{
				"id": "fn <'a>(y: {peer: &'a mut {value: number}}) -> {peer: &'a mut {value: number}}",
				"f":  "fn () -> {peer: &mut {value: number}}",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
			require.Equal(t, tc.types, values)
		})
	}
}

// TestDestructuredBorrowLeafEscapes covers a borrow projected into a destructuring leaf:
// `val {peer} = {peer: &mut b}` binds peer to the borrow of the local b, so returning peer
// escapes b. recordDestructureBorrowEdges matches the pattern leaf to its initializer
// property and records the leaf's edge.
func TestDestructuredBorrowLeafEscapes(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f() -> &mut {value: number} {
			val mut b = {value: 0}
			val {peer} = {peer: &mut b}
			return peer
		}
	`)
	require.Equal(t, []string{
		"5:11-5:15: borrowed value 'b' does not live long enough to escape the function",
	}, messagesWithSpan(errs))
}

// TestVarReassignBorrowEscapes covers a borrow introduced by reassigning a `var`: `a =
// &mut b` records the edge a → b, so returning a escapes the local b. The edge graph
// accumulates, so the reassignment adds the edge to a's earlier ones.
func TestVarReassignBorrowEscapes(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(seed: &mut {value: number}) {
			var a = seed
			val mut b = {value: 0}
			a = &mut b
			return a
		}
	`)
	require.Equal(t, []string{
		"6:11-6:12: borrowed value 'b' does not live long enough to escape the function",
	}, messagesWithSpan(errs))
}

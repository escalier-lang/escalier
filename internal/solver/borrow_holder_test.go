package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBorrowHoldersAcrossReassignment covers how the dataflow pass tracks which loans a
// binding holds. A binding holds a loan from where the borrow flows in until a reassignment
// replaces it, and the holders of a loan on each branch reach the merge after the branches.
func TestBorrowHoldersAcrossReassignment(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// `a = &mut d` replaces the loan of c that `a` held, so storing `a` out hands d alone
		// to the caller.
		"VarReassignClearsReplacedEdge": {
			src: `
				fn f(out: &mut {slot: &mut {value: number}}) {
					val mut c = {value: 1}
					val mut d = {value: 0}
					var a = &mut c
					a = &mut d
					out.slot = a
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (out: &mut {slot: &mut {value: number}}) -> undefined"},
		},
		// `a = &mut d` runs only when `cond` holds, and `a` still holds the loan of d after
		// the merge. So storing `a` out hands d to the caller through a mutable path, and
		// storing `&d` into `view` hands it out through an immutable one, which E2 rejects.
		// `seed` is a parameter, so the fall-through path holds no loan of a local.
		"BorrowSetOnOneBranchReachesMerge": {
			src: `
				fn f<'s>(seed: &'s mut {value: number}, cond: boolean, out: &mut {slot: &'s mut {value: number}}, view: &mut {r: &{value: number}}) {
					var a = seed
					val mut d = {value: 0}
					if cond {
						a = &mut d
					}
					out.slot = a
					view.r = &d
				}
			`,
			// `out.slot` and `view.r` both reach the caller, so the `&d` stored into `view.r`
			// breaks E2 against the `&mut d` that `out.slot` holds.
			want: []string{"9:15-9:17: cannot borrow 'd' as immutable while it is borrowed as mutable"},
			types: map[string]string{
				"f": "fn <'a>(seed: &'a mut {value: number}, cond: boolean, out: &mut {slot: &'a mut {value: number}}, " +
					"view: &mut {r: &{value: number}}) -> undefined",
			},
		},
		// One branch points `a` at d and the other back at c, so after the merge `a` holds a
		// loan of each. Storing `a` out hands the caller a mutable path to c or d. Nothing in
		// the frame uses either local afterwards, so L1 accepts the store.
		"BranchesUnionReferents": {
			src: `
				fn f(cond: boolean, out: &mut {slot: &mut {value: number}}) {
					val mut c = {value: 1}
					val mut d = {value: 0}
					var a = &mut c
					if cond {
						a = &mut d
					} else {
						a = &mut c
					}
					out.slot = a
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn (cond: boolean, out: &mut {slot: &mut {value: number}}) -> undefined"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
			require.Equal(t, tc.types, values)
		})
	}
}

// TestFieldStoreBorrowHolders covers a field store of a borrow into a local receiver.
// After `b.peer = &mut d`, the place `b.peer` holds the loan of d, so a later flow out of b
// carries the borrow. The store replaces what `b.peer` held before.
func TestFieldStoreBorrowHolders(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// After `b.peer = &mut d`, returning b carries the borrow of d out. The store replaces
		// the loan of c that `b.peer` held, so only d goes with b, and stripping owns d in the
		// returned tree.
		"FieldStoreRepointThenReturn": {
			src: `
				fn f() {
					val mut c = {x: 1}
					val mut d = {x: 0}
					val mut b = {peer: &mut c}
					b.peer = &mut d
					return b
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn () -> {peer: {x: number}}"},
		},
		// Storing a local borrow into a field, then aliasing the carrier from a live binding:
		// `keep` holds `&b` and is read after b moves into `store`, so the move of b breaks E3.
		// Passing b hands the callee its borrow of d, which L1 accepts, since nothing in the
		// frame uses d afterwards.
		"FieldStoreMoveWhileExternallyAliased": {
			src: `
				fn store(x: {peer: &mut {x: number}}) {}
				fn f() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					val keep = &b
					store(b)
					val y = keep
				}
			`,
			want: []string{"7:12-7:13: cannot move 'b' while it is borrowed"},
			types: map[string]string{
				"store": "fn (x: {peer: &mut {x: number}}) -> undefined",
				"f":     "fn () -> undefined",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
			require.Equal(t, tc.types, values)
		})
	}
}

// TestMutableBorrowCarrier covers the borrow-field owned-mutable upgrade: an unannotated
// `val mut b = {peer: &mut d}` builds the owned-mutable `mut {peer: &mut {x: number}}`, so b
// is `&mut`-borrowable, its field is writable through, and its borrow field is repointable.
// This is the natural way to build a mutable object that holds a borrow.
func TestMutableBorrowCarrier(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The owned-mutable carrier is `&mut`-borrowable: passing `&mut b` to a parameter of
		// type `&mut {peer: &mut {x: number}}` checks, which requires b to be owned-mutable with
		// a `&mut` field.
		"BorrowableAsMut": {
			src: `
				fn read(x: &mut {peer: &mut {x: number}}) {}
				fn f() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					read(&mut b)
				}
			`,
			want: nil,
			types: map[string]string{
				"read": "fn (x: &mut {peer: &mut {x: number}}) -> undefined",
				"f":    "fn () -> undefined",
			},
		},
		// The referent is writable through the node's borrow field: `b.peer.x = 5` mutates d
		// through the `&mut` the field holds.
		"WriteThroughBorrowField": {
			src: `
				fn f() {
					val mut d = {x: 0}
					val mut b = {peer: &mut d}
					b.peer.x = 5
				}
			`,
			want:  nil,
			types: map[string]string{"f": "fn () -> undefined"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
			require.Equal(t, tc.types, values)
		})
	}
}

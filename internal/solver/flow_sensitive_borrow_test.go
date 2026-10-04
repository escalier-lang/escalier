package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlowSensitiveBorrowEdges covers the flow-sensitive borrow-edge graph: a binding's
// borrow edges are set where a borrow flows in, cleared at a reassignment, and joined by
// union at CFG branch merges. A reassignment away from a borrow drops the replaced referent,
// so it no longer over-reports as escaping, while a borrow set on one branch still reaches the
// merge.
func TestFlowSensitiveBorrowEdges(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Reassigning a `var`'s borrow clears the replaced edge: `a = &mut d` after
		// `a = &mut c` leaves only `a` → d, so storing `a` out hands d alone to the caller. The
		// store takes a loan of d alone, so a stale c edge carried forward would take c out with
		// no loan and report it as escaping. The strong update clears it, so nothing reports.
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
		// A borrow set on only one branch reaches the merge: `a = &mut d` runs only when `cond`
		// holds, but the union at the merge keeps `a` → d, so storing `a` out hands d to the
		// caller through a mutable path. Storing `&d` into `view` hands it out through an
		// immutable one too, and the pair is reported. Without the merged edge only the `view`
		// store would carry d, and nothing would pair with it. `seed` is a parameter, so the
		// fall-through path carries no local edge and only d is reported.
		"BorrowSetOnOneBranchReachesMerge": {
			src: `
				fn f(seed: &mut {value: number}, cond: boolean, out: &mut {slot: &mut {value: number}}, view: &mut {r: &{value: number}}) {
					var a = seed
					val mut d = {value: 0}
					if cond {
						a = &mut d
					}
					out.slot = a
					view.r = &d
				}
			`,
			want: []string{"9:15-9:17: 'd' leaves the function through a mutable path and an immutable one"},
			types: map[string]string{
				"f": "fn (seed: &mut {value: number}, cond: boolean, out: &mut {slot: &mut {value: number}}, " +
					"view: &mut {r: &{value: number}}) -> undefined",
			},
		},
		// Disagreeing branches union their referents: the then-branch repoints `a` to d and the
		// `else` branch back to c, so the merge carries both `a` → c and `a` → d. The store into
		// `out` takes its loans from the edges recorded when the walk reaches it, which follow
		// the last branch walked and reach c alone. d has no loan to keep the stored borrow the
		// only path to it, so storing `a` out reports both locals as escaping.
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
			want: []string{
				"11:17-11:18: borrowed value 'c' does not live long enough to escape the function",
				"11:17-11:18: borrowed value 'd' does not live long enough to escape the function",
			},
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

// TestFieldStoreBorrowEdges covers borrow edges recorded at a field store into a local
// receiver: `b.peer = &mut d` records b → d at [peer], so a later flow-out of b finds the
// borrow. The store is a strong update on the stored field's subtree, so a repoint replaces
// the field's referent rather than accumulating a stale one.
func TestFieldStoreBorrowEdges(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// Repointing a field to a local then returning the carrier carries the stored borrow
		// out: `b.peer = &mut d` records b → d, so returning b is a component move of d, the
		// falsified-lifetime case the field-store edge catches. The store is a strong update
		// that clears the [peer] subtree before recording, so the replaced referent c is
		// dropped and only d is co-moved, and stripping owns d in the returned tree.
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
		// Storing a local borrow into a field, then aliasing the carrier from a live binding
		// outside the moved component, blocks the move and escapes: keep holds `&b` and is read
		// after the store, so b's node is externally referenced when storing b tries to move it.
		// The same live borrow makes the move of b itself a conflict.
		"FieldStoreEscapesWhenExternallyAliased": {
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
			want: []string{
				"7:12-7:13: borrowed value 'd' does not live long enough to escape the function",
				"7:12-7:13: cannot move 'b' while it is borrowed",
			},
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

// TestMutableGraphNode covers the borrow-field owned-mutable upgrade: an unannotated `val mut
// b = {peer: &mut d}` builds the owned-mutable `mut {peer: &mut {x: number}}`, so b is
// `&mut`-borrowable, its field is writable through, and its borrow field is repointable. This
// is the natural way to construct a mutable graph node holding a borrow.
func TestMutableGraphNode(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The owned-mutable graph node is `&mut`-borrowable: passing `&mut b` to a parameter of
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

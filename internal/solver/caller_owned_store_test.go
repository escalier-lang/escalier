package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStoreIntoCallerOwnedTarget covers a borrow of a local stored into a target the caller
// owns, a borrow parameter or a borrowing receiver. The runtime is garbage collected, so the
// local stays alive after the frame ends. What matters is whether the frame still holds a second
// path to it.
//
// The caller reads the target only after the function returns, so it sees the local as the body
// leaves it. A write or move in the body before then is invisible to it. Two things can still
// go wrong, and each is reported:
//
//   - The body reads through the target after changing the local, so its own view changes.
//     The store's loan lasts while the target is read and catches that.
//   - Another path to the local outlives the function and disagrees with the stored borrow.
//     That path can be a second store, a return, or a binding the local moved into.
func TestStoreIntoCallerOwnedTarget(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// An immutable borrow stored into the caller's object with nothing after it is the
		// only path to p once f returns.
		"ImmutableStoreOk": {
			src: `
				fn f(out: &mut {r: &{x: number}}) {
					val p = {x: 1}
					out.r = &p
				}
			`,
		},
		// q takes p's data and dies with the frame, so out.r is the only path to it once f
		// returns. The write through q happens before the caller can read out.r.
		"MovingIntoALocalAfterAnImmutableStoreOk": {
			src: `
				fn f(out: &mut {r: &{x: number}}) {
					val p = {x: 1}
					out.r = &p
					val mut q = p
					q.x = 2
				}
			`,
		},
		// q moves p's data out of f, so the caller owns it and also reads it through out.r. The
		// owner could change what out.r expects to hold still.
		"ReturningALocalTheStoredLocalMovedIntoConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}) -> {x: number} {
					val p = {x: 1}
					out.r = &p
					val q = p
					return q
				}
			`,
			want: []string{"6:13-6:14: 'p' leaves the function both as an owned value and through a borrow"},
		},
		// A mutable borrow of p taken after the store and never read changes nothing.
		"BorrowingMutablyAfterAnImmutableStoreOk": {
			src: `
				fn f(out: &mut {r: &{x: number}}) {
					val mut p = {x: 1}
					out.r = &p
					val q = &mut p
				}
			`,
		},
		// f writes b and then reads it back through p.r, so its own immutable view changed.
		"WritingBeforeAReadThroughTheTargetConflicts": {
			src: `
				fn f(p: &mut {r: &{value: number}}) -> number {
					val mut b = {value: 1}
					p.r = &b
					b.value = 5
					return p.r.value
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// Two stores that hand the caller a writer and a reader of one local are reported as
		// that pair. The loan check stays quiet about the second borrow, which the pair names.
		"StoresOfMixedMutabilityConflict": {
			src: `
				fn f(p: &mut {r: &{value: number}}, q: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					p.r = &b
					q.r = &mut b
				}
			`,
			want: []string{"5:12-5:18: 'b' leaves the function through a mutable path and an immutable one"},
		},
		// Two writers are Rule 3 of planning/lifetimes/requirements.md and are accepted.
		"MutableStoresOk": {
			src: `
				fn f(p: &mut {r: &mut {value: number}}, q: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					p.r = &mut b
					q.r = &mut b
				}
			`,
		},
		// a still reaches b after the store, but a dies with the frame and f never reads p.r
		// again. The write through a happens before the caller can read p.r.
		"WritingThroughALiveAliasOk": {
			src: `
				fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					a.peer.value = 5
				}
			`,
		},
		// The same alias with a later read through p.r. a's mutable borrow of b can change what
		// the stored immutable borrow reads.
		"WritingThroughALiveAliasBeforeAReadConflicts": {
			src: `
				fn f(p: &mut {r: &{value: number}}) -> number {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					a.peer.value = 5
					return p.r.value
				}
			`,
			want: []string{"5:12-5:14: cannot store an immutable borrow of 'b' while 'a' still borrows it as mutable"},
		},
		// Returning the alias hands the caller a writer of b beside the reader p.r.
		"ReturningALiveAliasConflicts": {
			src: `
				fn f(p: &mut {r: &{value: number}}) -> {peer: &mut {value: number}} {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					return a
				}
			`,
			want: []string{"6:13-6:14: 'b' leaves the function through a mutable path and an immutable one"},
		},
		// The writes between iterations happen before the caller can read p.r, so it sees b's
		// final value.
		"StoreInsideALoopOk": {
			src: `
				fn f(p: &mut {r: &{value: number}}, xs: [number]) {
					val mut b = {value: 1}
					for x in xs {
						b.value = x
						p.r = &b
					}
				}
			`,
		},
		// A repoint on one branch leaves the earlier store in place on the other, so p.r may
		// still reach b when b is written. The read of p afterwards keeps the earlier loan live.
		"RepointingOnOneBranchKeepsTheLoan": {
			src: `
				fn f(p: &mut {r: &{value: number}}, cond: boolean) {
					val mut b = {value: 1}
					val e = {value: 2}
					p.r = &b
					if cond {
						p.r = &e
					}
					b.value = 5
					val z = p
				}
			`,
			want: []string{"9:6-9:13: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// A repoint after an `if` runs on every path out of f, so it ends the earlier loan even
		// though the two stores sit in different blocks. Writing b before p is read again is
		// fine.
		"RepointingAfterABranchEndsTheLoan": {
			src: `
				fn f(p: &mut {r: &{value: number}}, cond: boolean) {
					val mut b = {value: 1}
					val e = {value: 2}
					var n = 0
					p.r = &b
					if cond {
						n = 1
					}
					p.r = &e
					b.value = 5
					val z = p
				}
			`,
		},
		// A later store into the same field repoints it, so b is no longer reachable through
		// p.r and its loan ends there. Moving b before p is read again is fine.
		"RepointingTheFieldEndsTheLoan": {
			src: `
				fn f(p: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					val mut e = {value: 2}
					p.r = &mut b
					p.r = &mut e
					val y = b
					val z = p
				}
			`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

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
// The store takes a loan of the local that lasts to the end of the function, since the caller
// keeps reading the target after the call. A later use of the local in the frame is weighed
// against that loan. A store with no such use is the only path to the local once the function
// returns, and is accepted.
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
		// Moving p into a mutable binding would let the frame write what `out.r` expects to hold
		// still, so the move conflicts with the store's loan.
		"MovingAfterAnImmutableStoreConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}) {
					val p = {x: 1}
					out.r = &p
					val mut q = p
					q.x = 2
				}
			`,
			want: []string{"5:18-5:19: cannot move 'p' while it is borrowed"},
		},
		// A mutable borrow of p after an immutable store is a writer beside the caller's reader.
		"BorrowingMutablyAfterAnImmutableStoreConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}) {
					val mut p = {x: 1}
					out.r = &p
					val q = &mut p
				}
			`,
			want: []string{"5:14-5:20: cannot borrow 'p' as mutable while it is borrowed as immutable"},
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
		// A binding still live after the store reaches b through its own borrow edge. The loan
		// names b at the store and does not see that second path, so the store keeps reporting
		// as an escape.
		"LiveAliasAfterTheStoreEscapes": {
			src: `
				fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					a.peer.value = 5
				}
			`,
			want: []string{"5:12-5:14: borrowed value 'b' does not live long enough to escape the function"},
		},
		// A store inside a loop runs again after the write that starts the next iteration, so
		// the write changes b while the caller's `p.r` reads it. The loan check walks the body
		// once and cannot see that order, so the store keeps reporting as an escape.
		"StoreInsideALoopEscapes": {
			src: `
				fn f(p: &mut {r: &{value: number}}, xs: [number]) {
					val mut b = {value: 1}
					for x in xs {
						b.value = x
						p.r = &b
					}
				}
			`,
			want: []string{"6:13-6:15: borrowed value 'b' does not live long enough to escape the function"},
		},
		// A repoint on one branch leaves the earlier store in place on the other, so `p.r` may
		// still read b when b is written. The earlier loan keeps holding.
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
				}
			`,
			want: []string{"9:6-9:13: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// A repoint after an `if` runs on every path out of f, so it ends the earlier loan even
		// though the two stores sit in different blocks. Writing b afterwards is fine.
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
				}
			`,
		},
		// A later store into the same field repoints it, so b is no longer reachable through
		// `p.r` and its loan ends there.
		"RepointingTheFieldEndsTheLoan": {
			src: `
				fn f(p: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					val mut e = {value: 2}
					p.r = &mut b
					p.r = &mut e
					val y = b
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

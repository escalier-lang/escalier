package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSelfRootedPlaces covers places rooted at a method's receiver. The liveness pre-pass
// defines `self` as a parameter of the body, so `self.p` names a tracked place the way `s.p`
// does for a parameter s. Each analysis built on places sees it: the exclusivity check, the
// move check, and borrow-edge recording.
func TestSelfRootedPlaces(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// An immutable and a mutable borrow of one receiver field conflict, through a
		// borrowing receiver and a consuming one alike.
		"BorrowingReceiverFieldBorrowedBothWays": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				class C {
					p: {v: number},
					m(&mut self) -> undefined { readWrite(&self.p, &mut self.p) },
				}
			`,
			want: []string{"6:53-6:64: cannot borrow 'self.p' as mutable while it is borrowed as immutable"},
		},
		"ConsumingReceiverFieldBorrowedBothWays": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				class C {
					p: {v: number},
					m(mut self) -> undefined { readWrite(&self.p, &mut self.p) },
				}
			`,
			want: []string{"6:52-6:63: cannot borrow 'self.p' as mutable while it is borrowed as immutable"},
		},
		// A constructor's receiver is a mutable borrow of the instance it fills in.
		"ConstructorReceiverFieldBorrowedBothWays": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				class C {
					p: {v: number},
					constructor(&mut self, p: {v: number}) {
						self.p = p
						readWrite(&self.p, &mut self.p)
					},
				}
			`,
			want: []string{"8:26-8:37: cannot borrow 'self.p' as mutable while it is borrowed as immutable"},
		},
		// Disjoint fields of the receiver are two objects.
		"DisjointReceiverFieldsOk": {
			src: `
				declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined

				class C {
					p: {v: number},
					q: {v: number},
					m(&mut self) -> undefined { readWrite(&self.p, &mut self.q) },
				}
			`,
			want: nil,
		},
		// A consuming receiver owns the instance, so a field of it moves into an owned
		// parameter and a second read is a use after the move.
		"ConsumingReceiverFieldMovedTwice": {
			src: `
				declare fn take(x: {v: number}) -> undefined

				class C {
					p: {v: number},
					m(self) -> undefined {
						take(self.p)
						take(self.p)
					},
				}
			`,
			want: []string{"8:12-8:18: use of moved value 'self.p'"},
		},
		// A borrowing receiver points at caller-owned data. Under GC, a local borrow stored into
		// one of its fields is the only path to the local once the method returns, so the store
		// is accepted.
		"LocalStoredIntoBorrowingReceiverOk": {
			src: `
				class C {
					peer: &mut {v: number},
					m(&mut self) -> undefined {
						val mut b = {v: 1}
						self.peer = &mut b
					},
				}
			`,
			want: nil,
		},
		// `self` holds the store's loan of b while the method reads `self`, so moving b out before
		// that read conflicts with it.
		"MovingALocalStoredIntoBorrowingReceiverConflicts": {
			src: `
				class C {
					peer: &mut {v: number},
					m(&mut self) -> undefined {
						val mut b = {v: 1}
						self.peer = &mut b
						val y = b
						val z = self.peer.v
					},
				}
			`,
			want: []string{"7:15-7:16: cannot move 'b' while it is borrowed"},
		},
		// A consuming receiver's instance dies with the method, so a local borrow stored into
		// one of its fields escapes nothing.
		"LocalStoredIntoConsumingReceiverOk": {
			src: `
				class C {
					peer: &mut {v: number},
					m(mut self) -> undefined {
						val mut b = {v: 1}
						self.peer = &mut b
					},
				}
			`,
			want: nil,
		},
		// A consuming receiver hands the instance to the method, so nothing reads `self.peer` once
		// `fill` returns. The returned borrow outlives the method, and it is the only path left to
		// b. A borrow of the instance that the caller takes before the call can still read
		// `self.peer` afterwards, a gap #1845 tracks.
		"LocalStoredIntoConsumingReceiverAndReturnedOk": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					fill(mut self) -> &'a mut {value: number} {
						val mut b = {value: 2}
						self.peer = &mut b
						return self.peer
					},
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

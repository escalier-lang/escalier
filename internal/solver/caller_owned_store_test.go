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
// The caller reads the target once the function returns, so the store's loan is live from the
// store to the end of the body, unless a later write to the target replaces the borrow. While
// it is live, E2 rejects a write to the local or a mutable path to it, and E3 rejects a move
// of it.
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
		// A borrow stored through an `if`/`else` takes the same loan a plain store does.
		"ImmutableStoreThroughAnIfElseOk": {
			src: `
				fn f(cond: boolean, out: &mut {r: &{x: number}}) {
					val p = {x: 1}
					out.r = if cond { &p } else { &p }
				}
			`,
		},
		// That loan conflicts with a write before a read through the target, as a plain
		// store's does.
		"WritingBeforeAReadThroughAnIfElseStoreConflicts": {
			src: `
				fn f(cond: boolean, p: &mut {r: &{value: number}}) -> number {
					val mut b = {value: 1}
					p.r = if cond { &b } else { &b }
					b.value = 5
					return p.r.value
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// `out.r` is live for the rest of f, since the caller reads it once f returns. Moving p
		// into q breaks E3 even though q dies with the frame, since q may write what `out.r`
		// expects to hold still.
		"MovingIntoALocalAfterAnImmutableStoreConflicts": {
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
		// Returning q hands the caller an owner of the data it also reads through `out.r`.
		"ReturningALocalTheStoredLocalMovedIntoConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}) -> {x: number} {
					val p = {x: 1}
					out.r = &p
					val q = p
					return q
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		// A move lands in one field of its destination. Returning a.y hands out c alone, so it
		// does not meet the borrow of b that `p.r` holds.
		"ReturningADisjointFieldOfTheMoveDestinationOk": {
			src: `
				fn f(p: &mut {r: &{v: number}}) -> {v: number} {
					val b = {v: 1}
					val c = {v: 2}
					val a = {x: b, y: c}
					p.r = &a.x
					return a.y
				}
			`,
		},
		// A generator hands control back to the caller at each `yield`, so the caller can read
		// `p.r` before the write that follows it.
		"WritingAfterAYieldConflicts": {
			src: `
				gen fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					p.r = &b
					yield 1
					b.value = 5
				}
			`,
			want: []string{"6:6-6:13: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// Destructuring moves b.x into x while `out.r` borrows b. E3 blames the destructuring's
		// source b.
		"ReturningADestructuredLeafOfTheStoredLocalConflicts": {
			src: `
				fn f(out: &mut {r: &{x: {v: number}}}) -> {v: number} {
					val b = {x: {v: 1}}
					out.r = &b
					val {x} = b
					return x
				}
			`,
			want: []string{"5:16-5:17: cannot move 'b.x' while it is borrowed"},
		},
		// The same through a renaming pattern.
		"ReturningARenamedDestructuredLeafConflicts": {
			src: `
				fn f(out: &mut {r: &{x: {v: number}}}) -> {v: number} {
					val b = {x: {v: 1}}
					out.r = &b
					val {x: y} = b
					return y
				}
			`,
			want: []string{"5:19-5:20: cannot move 'b.x' while it is borrowed"},
		},
		// A tuple leaf moves the whole tuple, which `out.r` borrows.
		"ReturningATupleLeafOfTheStoredLocalConflicts": {
			src: `
				fn f(out: &mut {r: &[{v: number}]}) -> {v: number} {
					val b = [{v: 1}]
					out.r = &b
					val [y] = b
					return y
				}
			`,
			want: []string{"5:16-5:17: cannot move 'b[0]' while it is borrowed"},
		},
		// The destructuring moves b.x, which `out.r` borrows, even though the returned y owns
		// b.y alone.
		"ReturningASiblingLeafConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {v: number} {
					val b = {x: {v: 1}, y: {v: 2}}
					out.r = &b.x
					val {x, y} = b
					return y
				}
			`,
			want: []string{"5:19-5:20: cannot move 'b.x' while it is borrowed"},
		},
		// Gathering both leaves into a returned literal reports the same move of b.x.
		"ReturningEveryLeafOfADestructuringConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {p: {v: number}, q: {v: number}} {
					val b = {x: {v: 1}, y: {v: 2}}
					out.r = &b.x
					val {x, y} = b
					val a = {p: x, q: y}
					return a
				}
			`,
			want: []string{"5:19-5:20: cannot move 'b.x' while it is borrowed"},
		},
		// A rest element takes the fields the pattern does not name, here b.y, which `out.r`
		// reads.
		"ReturningARestElementConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {y: {v: number}} {
					val b = {x: {v: 1}, y: {v: 2}}
					out.r = &b.y
					val {x, ...rest} = b
					return rest
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.y' while it is borrowed"},
		},
		// x takes b.x, which `out.r` borrows, even though the returned rest element does not.
		"ReturningARestElementWithoutTheStoredFieldConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {y: {v: number}} {
					val b = {x: {v: 1}, y: {v: 2}}
					out.r = &b.x
					val {x, ...rest} = b
					return rest
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.x' while it is borrowed"},
		},
		// A rest element that takes several fields moves each one. Here `rest` takes b.y and b.z,
		// and `out.r` reads b.z.
		"ReturningARestElementOfSeveralFieldsConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {y: {v: number}, z: {v: number}} {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					return rest
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// `rest.z` holds the data moved out of b.z, which `out.r` reads.
		"ReturningTheStoredFieldOfARestElementConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {v: number} {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					return rest.z
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// The rest element takes b.z, which `out.r` borrows, even though the return hands out
		// `rest.y` alone.
		"ReturningAnotherFieldOfARestElementConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {v: number} {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					return rest.y
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// Storing the rest element into a second caller-owned object hands the caller b.z
		// as owned data beside the borrow `out.r` holds.
		"StoringARestElementConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}, q: &mut {o: {y: {v: number}, z: {v: number}}}) {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					q.o = rest
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// Wrapping the rest element in a returned literal reports the same move of b.z.
		"ReturningARestElementInsideALiteralConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}) -> {w: {y: {v: number}, z: {v: number}}} {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					val a = {w: rest}
					return a
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// The rest element leaves inside a nested block, and E3 at the destructuring reports it.
		"ReturningARestElementFromANestedBlockConflicts": {
			src: `
				fn f(out: &mut {r: &{v: number}}, c: boolean) -> {v: number} {
					val b = {x: {v: 1}, y: {v: 2}, z: {v: 3}}
					out.r = &b.z
					val {x, ...rest} = b
					if c {
						if c {
							val w = rest
							return w.z
						}
					}
					return x
				}
			`,
			want: []string{"5:25-5:26: cannot move 'b.z' while it is borrowed"},
		},
		// q holds a new value before the return, but the move of p into q happens while `out.r`
		// borrows p.
		"ReturningALocalAfterReplacingTheMovedValueConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}) -> {x: number} {
					val p = {x: 1}
					out.r = &p
					var q = p
					q = {x: 2}
					return q
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		// Only one branch replaces q. The move of p into q is reported as above.
		"ReturningALocalReplacedOnOneBranchConflicts": {
			src: `
				fn f(out: &mut {r: &{x: number}}, c: boolean) -> {x: number} {
					val p = {x: 1}
					out.r = &p
					var q = p
					if c {
						q = {x: 2}
					}
					return q
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		// Storing q into module-level state hands p's data to a binding that outlives the
		// call, beside the borrow `out.r` holds.
		"StoringALocalTheStoredLocalMovedIntoModuleStateConflicts": {
			src: `
				var sink: {x: number} = {x: 0}
				fn f(out: &mut {r: &{x: number}}) {
					val p = {x: 1}
					out.r = &p
					val q = p
					sink = q
				}
			`,
			want: []string{"6:14-6:15: cannot move 'p' while it is borrowed"},
		},
		// A leaf that copies a borrow moves nothing, and its borrow edge says what it reaches.
		"DestructuringABorrowLeafOk": {
			src: `
				fn f(out: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					val {peer} = a
					out.r = peer
				}
			`,
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
		// f writes b and then reads it back through `p.r`, so its own immutable view changed.
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
		// that pair. The second borrow gets no diagnostic of its own, since the pair names it.
		"StoresOfMixedMutabilityConflict": {
			src: `
				fn f(p: &mut {r: &{value: number}}, q: &mut {r: &mut {value: number}}) {
					val mut b = {value: 1}
					p.r = &b
					q.r = &mut b
				}
			`,
			// The caller reads both `p.r` and `q.r` once f returns, so `&mut b` breaks E2
			// against the `&b` that `p.r` still holds.
			want: []string{"5:12-5:18: cannot borrow 'b' as mutable while it is borrowed as immutable"},
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
		// a still reaches b mutably when the store hands the caller an immutable borrow of b, so
		// the store breaks E2 even though f never reads `p.r` again.
		"WritingThroughALiveAliasConflicts": {
			src: `
				fn f(p: &mut {r: &{value: number}}) {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					a.peer.value = 5
				}
			`,
			want: []string{"5:12-5:14: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// The same alias with a later read through `p.r`. a's mutable borrow of b can change what
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
			want: []string{"5:12-5:14: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// Returning the alias hands the caller a writer of b beside the reader `p.r`.
		"ReturningALiveAliasConflicts": {
			src: `
				fn f<'x>(p: &'x mut {r: &{value: number}}) -> {peer: &'x mut {value: number}} {
					val mut b = {value: 1}
					val a = {peer: &mut b}
					p.r = &b
					return a
				}
			`,
			// a's mutable borrow of b is live until the return reads a, so the store's `&b`
			// breaks E2.
			want: []string{"5:12-5:14: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// The write on a later iteration runs while `p.r` holds the borrow of b the earlier
		// iteration stored.
		"StoreInsideALoopConflicts": {
			src: `
				fn f(p: &mut {r: &{value: number}}, xs: [number]) {
					val mut b = {value: 1}
					for x in xs {
						b.value = x
						p.r = &b
					}
				}
			`,
			want: []string{"5:7-5:14: cannot assign to 'b.value' while it is borrowed as immutable"},
		},
		// A repoint on one branch leaves the earlier store in place on the other, so `p.r` may
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
		// The store runs only when k holds, and the write only when it does not, so the write
		// never meets the stored borrow even though p is read afterwards.
		"StoreOnOneBranchLeavesTheOtherFreeOk": {
			src: `
				fn f(k: boolean, p: &mut {peer: &{value: number}}) {
					val mut c = {value: 0}
					if k {
						p.peer = &c
					} else {
						c.value = 5
					}
					val z = p
				}
			`,
		},
		// The second store into p.peer replaces the first, so c reaches the caller through q
		// alone and no immutable path to it is left.
		"OverwrittenFieldStoreIsNoPathOk": {
			src: `
				fn f(p: &mut {peer: &{value: number}}, q: &mut {peer: &mut {value: number}}) {
					val mut c = {value: 0}
					val mut d = {value: 1}
					p.peer = &c
					p.peer = &d
					q.peer = &mut c
				}
			`,
		},
		// a leaves through p.peer and the return, both mutable. c leaves immutably through `take`,
		// but `take` keeps nothing, so that path ends when the call returns and the mutable return
		// is the only path to c left.
		"ReturnOfALocalACallReadOk": {
			src: `
				declare fn take(x: {peer: &{value: number}}) -> undefined
				fn f(p: &mut {peer: &mut {value: number}}) {
					val mut a = {value: 0}
					val mut c = {value: 1}
					p.peer = &mut a
					take({peer: &c})
					val mut h = {x: &mut a, y: &mut c}
					return &mut h
				}
			`,
		},
		// a's paths agree, which keeps the return's exemption, and the returned value's own two
		// paths to e are still reported.
		"ReturnWithAnAgreeingSharedLocalStillChecksItsOwnPaths": {
			src: `
				fn f<'x>(p: &'x mut {peer: &mut {value: number}}) -> [&'x mut {value: number}, &'x {value: number}, &'x mut {value: number}] {
					val mut a = {value: 0}
					val mut e = {value: 1}
					p.peer = &mut a
					return [&mut a, &e, &mut e]
				}
			`,
			// `&e` lasts for the return statement, so `&mut e` beside it breaks E2.
			want: []string{"6:26-6:32: cannot borrow 'e' as mutable while it is borrowed as immutable"},
		},
		// A later store into the same field repoints it, so b is no longer reachable through
		// `p.r` and its loan ends there. Moving b before p is read again is fine.
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

// TestReturnAfterACallReadTheLocal asserts that a mutable return of a local checks after a
// call read it through an immutable borrow. `take` keeps nothing, so its path to c ends when the
// call returns.
func TestReturnAfterACallReadTheLocal(t *testing.T) {
	src := `
declare fn take(x: {peer: &{value: number}}) -> undefined
fn f(p: &mut {peer: &mut {value: number}}) {
	val mut c = {value: 1}
	take({peer: &c})
	return &mut c
}
`
	_, _, errs := inferSource(t, src)
	require.Empty(t, messagesWithSpan(t, errs))
}

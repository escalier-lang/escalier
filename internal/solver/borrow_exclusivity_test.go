package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// exclusivityDecls are the callees the exclusivity cases pass borrows to. Each spells out its
// parameters' mutability, since that is what decides whether a pair of arguments conflicts.
const exclusivityDecls = `
	declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined
	declare fn writeWrite(a: &mut {v: number}, b: &mut {v: number}) -> undefined
	declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
	declare fn write(a: &mut {v: number}) -> undefined
`

// TestBorrowExclusivity covers the mutable-XOR-shared rule: data one borrow can write through
// may not be reachable at the same time through a borrow that expects it to hold still. Two
// borrows that agree, both mutable or both shared, are allowed.
//
// Each case pins the full diagnostic, so the wording that says WHICH pair conflicts is part of
// what is asserted. A case that should be accepted asserts no diagnostic at all.
func TestBorrowExclusivity(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The canonical case. One argument reads x and the other writes it, so the callee holds
		// two views of one value and one of them can change it.
		"SharedAndMutableOfOnePlace": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					readWrite(&x, &mut x)
				}
			`,
			want: []string{"9:20-9:26: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Two writable views of one value both expect it to change, so neither is surprised by
		// the other's write. Rule 3 of planning/lifetimes/requirements.md allows this, where Rust
		// would not: one thread cannot observe a tear, and `&mut` is invariant in its referent, so
		// the two views agree on the type as well as on the mutability.
		"TwoMutableOfOnePlaceOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					writeWrite(&mut x, &mut x)
				}
			`,
			want: nil,
		},
		// Two readers see the same value, so nothing can disagree.
		"TwoSharedOfOnePlaceOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					readRead(&x, &x)
				}
			`,
			want: nil,
		},
		// The parameter decides, not the argument. One argument is written `&mut` and the other
		// `&`, which would be the conflicting pair if the argument decided. The callee takes both
		// as shared, so neither view can change the value.
		"MutableArgumentIntoASharedParamOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					readRead(&mut x, &x)
				}
			`,
			want: nil,
		},
		// Field paths keep disjoint data apart. x.p and x.q name different values, so writing
		// one cannot be observed through a read of the other.
		"DisjointFieldsOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {p: {v: 1}, q: {v: 2}}
					readWrite(&x.p, &mut x.q)
				}
			`,
			want: nil,
		},
		// The same field through both arguments is the same data.
		"SameFieldConflicts": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {p: {v: 1}, q: {v: 2}}
					readWrite(&x.p, &mut x.p)
				}
			`,
			want: []string{"9:22-9:30: cannot borrow 'x.p' as mutable while it is borrowed as immutable"},
		},
		// A whole binding contains its fields, so borrowing x.p and then x reaches overlapping
		// data even though the paths differ in length. The message names both places, since
		// only part of x is the part already borrowed and the reader cannot tell which from
		// the blamed borrow alone.
		"WholeBindingOverlapsItsField": {
			src: `
				declare fn readWhole(a: &{v: number}, b: &mut {p: {v: number}}) -> undefined
				fn g() {
					val mut x = {p: {v: 1}}
					readWhole(&x.p, &mut x)
				}
			`,
			want: []string{"5:22-5:28: cannot borrow 'x' as mutable while 'x.p' is borrowed as immutable"},
		},
		// Separate bindings share no data.
		"DisjointRootsOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val mut y = {v: 2}
					readWrite(&x, &mut y)
				}
			`,
			want: nil,
		},
		// One borrow filling two parameters is the shape #794 names. m is a single borrow, and
		// what makes the pair a conflict is that the callee may write through one view while
		// reading the other.
		"OneBorrowFillingASharedAndAMutableParam": {
			src: exclusivityDecls + `
				fn g(m: &mut {v: number}) {
					readWrite(m, m)
				}
			`,
			want: []string{"8:19-8:20: cannot borrow 'm' as mutable while it is borrowed as immutable"},
		},
		// The cross-statement form a call-site-only check misses. The conflict is reported at
		// the second borrow, which is where the program first holds two views of x.
		"BorrowsBoundToNamesConflict": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val b = &mut x
					readWrite(a, b)
				}
			`,
			want: []string{"10:14-10:20: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A borrow nothing reads again constrains nothing, which is the NLL rule. Here a is
		// never read at all, so it and b are never live at once.
		"UnreadFirstBorrowOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val b = &mut x
					write(b)
				}
			`,
			want: nil,
		},
		// A loan is live AT the statement that reads it, not only beyond it. b's last use is
		// the call, and the fresh `&x` in the same call is a second view of x while b can
		// still write. Asking only whether b outlives the call would miss this.
		"FreshBorrowBesideALoanOnItsLastUse": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val b = &mut x
					readWrite(&x, b)
				}
			`,
			want: []string{"10:16-10:18: cannot borrow 'x' as immutable while it is borrowed as mutable"},
		},
		// A reassignment repoints the binding, so the loan it held is gone. a borrows y by the
		// time b is created, and nothing then reaches x twice.
		"ReassignedBorrowDropsItsOldLoanOk": {
			src: exclusivityDecls + `
				fn g(x: mut {v: number}, y: mut {v: number}) {
					var a = &mut x
					a = &mut y
					val b = &mut x
					write(a)
				}
			`,
			want: nil,
		},
		// The reassignment does not silence a real conflict: a borrows x mutably after it, and
		// the shared b then reads data a can write.
		"ReassignedBorrowStillConflicts": {
			src: exclusivityDecls + `
				fn g(x: mut {v: number}, y: mut {v: number}) {
					var a = &mut y
					a = &mut x
					val b = &x
					readWrite(b, a)
				}
			`,
			want: []string{"10:14-10:16: cannot borrow 'x' as immutable while it is borrowed as mutable"},
		},
		// A reassignment ends the binding's loan from that point on, and no further back. The
		// read of x sits before it, while a still borrows x, so it reports even though a is
		// repointed later in the body.
		"AReassignmentDoesNotSilenceAnEarlierRead": {
			src: exclusivityDecls + `
				fn g(x: mut {v: number}, y: mut {v: number}) -> undefined {
					var a = &mut x
					val w = x
					write(a)
					a = &mut y
					write(a)
				}
			`,
			want: []string{"9:14-9:15: cannot use 'x' while it is borrowed as mutable"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestExplicitBorrowArgs covers #1541: a borrow parameter takes a borrow, so the call writes
// one. What stays implicit is a method call's receiver, whose mode the signature already names.
func TestExplicitBorrowArgs(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A bare place filling a shared parameter reports, naming the borrow it needs.
		"BarePlaceIntoASharedParam": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					write(&mut x)
					readRead(x, &x)
				}
			`,
			want: []string{"10:15-10:16: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		// The mutable parameter asks for `&mut`, so the message names that instead.
		"BarePlaceIntoAMutableParam": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					write(x)
				}
			`,
			want: []string{"9:12-9:13: this argument is borrowed by the callee, so write the borrow: `&mut`"},
		},
		// Writing the borrow is what the rule asks for.
		"WrittenBorrowsOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val mut y = {v: 2}
					readWrite(&x, &mut y)
				}
			`,
			want: nil,
		},
		// A value that is ALREADY a borrow is handed on rather than borrowed afresh, so there
		// is no borrow missing at this call and nothing to write.
		"PassingAnExistingBorrowOk": {
			src: exclusivityDecls + `
				fn g(m: &mut {v: number}) {
					write(m)
				}
			`,
			want: nil,
		},
		// An owned parameter is moved into the callee rather than borrowed, so it needs no
		// borrow written and the rule leaves it alone.
		"OwnedParamTakesABarePlaceOk": {
			src: `
				declare fn consume(a: {v: number}) -> undefined
				fn g() {
					val x = {v: 1}
					consume(x)
				}
			`,
			want: nil,
		},
		// The receiver of a method call keeps auto-borrowing. `bump(mut self)` names the mode
		// in the signature, and `c.bump()` has no second reading for a written borrow to
		// disambiguate.
		"MethodReceiverStillAutoBorrowsOk": {
			src: `
				class Counter {
					n: number,
					bump(mut self) -> undefined { self.n = 1 },
				}
				fn g() {
					val mut c = Counter(0)
					c.bump()
				}
			`,
			want: nil,
		},
		// A method's own arguments are arguments like any other, so the receiver's exemption
		// does not extend to them.
		"MethodArgumentsAreNotExempt": {
			src: `
				class Holder {
					v: number,
					take(self, a: &{v: number}) -> undefined {},
				}
				fn g() {
					val h = Holder(0)
					val x = {v: 1}
					h.take(x)
				}
			`,
			want: []string{"9:13-9:14: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// storeEffectDecls declares a callee that writes its second argument into its first, plus the
// helpers the cases read the aliased data back through. `'a` at both item and target's peer
// field is what makes the signature declare a store.
const storeEffectDecls = `
	declare fn store<'a, 'b, 'c>(
		target: &'c mut {peer: &'a mut {value: number}, spare: &'b mut {value: number}},
		item: &'a mut {value: number},
	) -> undefined
	declare fn readMutBorrow(x: &mut {value: number}) -> undefined
	declare fn readImmBorrow(x: &{value: number}) -> undefined
	declare fn touch<'d>(x: &'d mut {peer: &mut {value: number}, spare: &mut {value: number}}) -> undefined
`

// TestStoreEffectLoans covers the borrow a call's store effect creates. A signature that writes
// one argument into another leaves the target reaching the item, so the target holds a borrow
// of it that a second borrow or a plain read has to respect.
//
// Each case keeps the target live past the read, since a loan lasts only as long as the binding
// holding it. The last case drops that use to show the rule turning off.
func TestStoreEffectLoans(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A second mutable borrow of the stored item is a second writable path, since the
		// target still reaches the first.
		"SecondBorrowAfterAStore": {
			src: storeEffectDecls + `
				fn f(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					readImmBorrow(&b)
					touch(&mut a)
				}
			`,
			want: []string{"14:20-14:22: cannot borrow 'b' as immutable while it is borrowed as mutable"},
		},
		// Reading the item directly reaches the same data the target can write through, which
		// the loan-against-loan check does not see because a read is not a borrow.
		"UseAfterAStore": {
			src: storeEffectDecls + `
				fn g(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
					touch(&mut a)
				}
			`,
			want: []string{"14:14-14:15: cannot use 'b' while it is borrowed as mutable"},
		},
		// Repointing the field the store wrote to ends the loan there, so the item is reachable
		// one way again. This is the loan side of the strong update the borrow graph makes on
		// the same subtree.
		"RepointingTheStoredFieldReleasesTheItemOk": {
			src: storeEffectDecls + `
				fn f(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					a.peer = &mut c
					val y = b
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// A store into a SIBLING field leaves the loan at [peer] holding, since the update
		// reaches only the field it writes.
		"RepointingASiblingFieldKeepsTheLoan": {
			src: storeEffectDecls + `
				fn g(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					a.spare = &mut c
					val y = b
					touch(&mut a)
				}
			`,
			want: []string{"16:14-16:15: cannot use 'b' while it is borrowed as mutable"},
		},
		// A read walked BEFORE the repoint went through the loan while it still held, so it
		// keeps its diagnostic. The loan carries the sequence it ended at rather than leaving
		// the list, which is what lets a later store leave an earlier read alone.
		"AReadBeforeTheRepointKeepsItsDiagnostic": {
			src: storeEffectDecls + `
				fn h(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut c = {value: 3}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
					a.peer = &mut c
					touch(&mut a)
				}
			`,
			want: []string{"15:14-15:15: cannot use 'b' while it is borrowed as mutable"},
		},
		// Nothing reads the target after the store, so its borrow of the item is dead and the
		// item is reachable one way again. This is the same NLL rule a named borrow follows.
		"DeadTargetReleasesTheItemOk": {
			src: storeEffectDecls + `
				fn h(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					val y = b
				}
			`,
			want: nil,
		},
		// Without the store the target reaches nothing of b's, so both the borrow and the read
		// are the only path to it.
		"NoStoreLeavesTheItemFreeOk": {
			src: storeEffectDecls + `
				fn k(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					readMutBorrow(&mut b)
					val y = b
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// The store's own arguments are compared against each other by the loan-against-loan
		// check, not against the loan that same call creates. Reading b to pass `&mut b` is how
		// the store is written, so it reports nothing on its own.
		"TheStoreCallItselfIsQuietOk": {
			src: storeEffectDecls + `
				fn m(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b)
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// What the store puts in the target decides whether a write can go through it. A
		// signature storing a shared `&'a B` leaves the target able to read the item and not to
		// write it, so a second shared borrow is two readers of one value.
		"SharedItemStoredLeavesItReadableOk": {
			src: `
				declare fn store<'a, 'b, 'c>(
					target: &'c mut {peer: &'a {value: number}, spare: &'b mut {value: number}},
					item: &'a {value: number},
				) -> undefined
				declare fn readImmBorrow(x: &{value: number}) -> undefined
				declare fn hold<'d>(x: &'d mut {peer: &{value: number}, spare: &mut {value: number}}) -> undefined
				fn f(q: {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {value: 2}
					val mut a = {peer: &q, spare: &mut r}
					store(&mut a, &b)
					readImmBorrow(&b)
					hold(&mut a)
				}
			`,
			want: nil,
		},
		// A direct store puts the argument's own place in the target, field path and all, so
		// the target reaches b.inner and nothing else of b. Borrowing the disjoint b.other
		// reaches data the target cannot write.
		"StoreOfOneFieldLeavesItsSiblingFreeOk": {
			src: storeEffectDecls + `
				fn f(q: mut {value: number}, r: mut {value: number}) -> undefined {
					val mut b = {inner: {value: 1}, other: {value: 2}}
					val mut a = {peer: &mut q, spare: &mut r}
					store(&mut a, &mut b.inner)
					readMutBorrow(&mut b.other)
					touch(&mut a)
				}
			`,
			want: nil,
		},
		// A read is weighed against the borrows that existed when it was walked. The borrow on
		// the else arm comes later in the source, so it never reaches the read on the then arm.
		"ABorrowOnOneArmDoesNotReachTheOtherOk": {
			src: `
				declare fn readMutBorrow(x: &mut {value: number}) -> undefined
				fn f(cond: boolean, x: mut {value: number}) -> undefined {
					var a = &mut x
					if cond {
						val y = x
					} else {
						a = &mut x
						readMutBorrow(a)
					}
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

// TestMutSelfIsAMutableStoreSource covers the loan a method's own receiver creates when the
// method drains it into a parameter.
//
// A `mut self` receiver is a mutable RefType carrying no lifetime, the same shape an
// owned-mutable parameter takes, so the lifetime test that decides an ordinary parameter's
// mutability does not describe it. Reading it as shared would let the target hold a writable
// view while the check believed it held a read-only one.
//
// The later SHARED borrow of h is what pins this. It conflicts with a mutable loan and not with
// a shared one, so the diagnostic appears only when the receiver was read as mutable.
func TestMutSelfIsAMutableStoreSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Holder<'a> {
			peer: &'a mut {value: number},
			drain(mut self, out: &mut {slot: &'a mut {value: number}}) -> undefined { out.slot = self.peer },
		}
		declare fn touch<'e>(x: &'e mut {slot: &mut {value: number}}) -> undefined
		fn build(p: mut {value: number}) -> undefined {
			val mut h = Holder(&mut p)
			val mut o = {slot: &mut p}
			h.drain(&mut o)
			val again = &h
			touch(&mut o)
		}
	`)
	require.Equal(t, []string{
		"11:16-11:18: cannot borrow 'h' as immutable while it is borrowed as mutable",
	}, messagesWithSpan(t, errs))
}

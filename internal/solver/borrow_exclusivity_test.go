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
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

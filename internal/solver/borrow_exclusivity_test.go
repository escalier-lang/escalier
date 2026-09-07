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
`

// TestBorrowExclusivity covers the mutable-XOR-shared rule: data one borrow can write through
// may not be reachable through a second borrow at the same time.
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
			want: []string{"8:20-8:26: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Two writable views of one value disagree the moment either writes.
		"TwoMutableOfOnePlace": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					writeWrite(&mut x, &mut x)
				}
			`,
			want: []string{"8:25-8:31: cannot borrow 'x' as mutable more than once at a time"},
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
		// The parameter decides, not the argument. Both arguments are written `&mut`, but the
		// callee takes them as shared, so it can only read and neither view can change the value.
		"MutableArgumentsIntoSharedParamsOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					readRead(&mut x, &mut x)
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
			want: []string{"8:22-8:30: cannot borrow 'x.p' as mutable while it is borrowed as immutable"},
		},
		// A whole binding contains its fields, so borrowing x.p and then x reaches overlapping
		// data even though the paths differ in length.
		"WholeBindingOverlapsItsField": {
			src: `
				declare fn readWhole(a: &{v: number}, b: &mut {p: {v: number}}) -> undefined
				fn g() {
					val mut x = {p: {v: 1}}
					readWhole(&x.p, &mut x)
				}
			`,
			want: []string{"5:22-5:28: cannot borrow 'x' as mutable while it is borrowed as immutable"},
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
			want: []string{"7:19-7:20: cannot borrow 'm' as mutable while it is borrowed as immutable"},
		},
		// The same borrow filling two shared parameters is two readers.
		"OneBorrowFillingTwoSharedParamsOk": {
			src: exclusivityDecls + `
				fn g(m: &mut {v: number}) {
					readRead(m, m)
				}
			`,
			want: nil,
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
			want: []string{"9:14-9:20: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A borrow nothing reads again constrains nothing, which is the NLL rule. Here a is
		// never read after b is created, so the two are not live at once.
		"DeadFirstBorrowOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val b = &mut x
					readWrite(&x, b)
				}
			`,
			want: nil,
		},
		// Two bound borrows of disjoint fields stay apart across statements too.
		"BorrowsBoundToNamesOfDisjointFieldsOk": {
			src: exclusivityDecls + `
				fn g() {
					val mut x = {p: {v: 1}, q: {v: 2}}
					val a = &x.p
					val b = &mut x.q
					readWrite(a, b)
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

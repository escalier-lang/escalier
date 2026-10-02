package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// captureDecls are the callees the closure-capture cases pass places to.
const captureDecls = `
	declare fn readRead(a: &{v: number}, b: &{v: number}) -> undefined
	declare fn readWrite(a: &{v: number}, b: &mut {v: number}) -> undefined
	declare fn take(x: {v: number}) -> undefined
`

// TestClosureCapturedPlaces covers places reached through a closure's captured variables.
// Each case with a plain-function counterpart reports what that counterpart reports.
func TestClosureCapturedPlaces(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A closure that writes x takes a mutable loan of it, which conflicts with the live
		// immutable borrow a. The plain write is the same program without the closure.
		"WriteThroughCaptureBesideLiveBorrow": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { x.v = 5 }
					f()
					readRead(a, a)
				}
			`,
			want: []string{"9:14-9:31: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		"PlainWriteBesideLiveBorrow": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					x.v = 5
					readRead(a, a)
				}
			`,
			want: []string{"9:6-9:9: cannot assign to 'x.v' while it is borrowed as immutable"},
		},
		// Inside the closure, x is a parameter of the body, so the call's two arguments conflict
		// the way they do over a plain parameter.
		"CaptureBorrowedBothWaysInsideClosure": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val f = fn () { readWrite(&x, &mut x) }
					f()
				}
			`,
			want: []string{"8:36-8:42: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		"PlainBorrowedBothWays": {
			src: captureDecls + `
				fn g(x: &mut {v: number}) { readWrite(&x, &mut x) }
			`,
			want: []string{"6:47-6:53: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Inside the closure, the first call moves x and the second reads the moved value.
		"CaptureMovedTwiceInsideClosure": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () {
						take(x)
						take(x)
					}
					f()
				}
			`,
			want: []string{"10:12-10:13: use of moved value 'x'"},
		},
		"PlainMovedTwice": {
			src: captureDecls + `
				fn g(x: {v: number}) {
					take(x)
					take(x)
				}
			`,
			want: []string{"8:11-8:12: use of moved value 'x'"},
		},
		// A closure that only reads x takes an immutable loan, which sits beside a live `&`.
		"ReadThroughCaptureBesideLiveSharedBorrowOk": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { readRead(&x, &x) }
					f()
					readRead(a, a)
				}
			`,
			want: nil,
		},
		// A closure that writes x takes a mutable loan, which sits beside a live `&mut`.
		"WriteThroughCaptureBesideLiveMutableBorrowOk": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &mut x
					val f = fn () { x.v = 5 }
					f()
					a.v = 6
				}
			`,
			want: nil,
		},
		// f holds its loan of x while f is live, and the call after the move keeps it live.
		"MoveWhileAClosureHoldsTheCapture": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					take(x)
					f()
				}
			`,
			want: []string{"9:11-9:12: cannot move 'x' while it is borrowed"},
		},
		// Once f is called for the last time its loan of x ends, so x can move.
		"MoveAfterTheClosureIsLastCalledOk": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					f()
					take(x)
				}
			`,
			want: nil,
		},
		// A write beside the immutable loan a reading closure holds conflicts with it.
		"WriteWhileAClosureReadsTheCapture": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					x.v = 5
					f()
				}
			`,
			want: []string{"9:6-9:9: cannot assign to 'x.v' while it is borrowed as immutable"},
		},
		// A closure passed straight to a call holds its loans for the call's statement.
		"ClosureArgumentHoldsItsLoanForTheCall": {
			src: captureDecls + `
				declare fn run(a: &{v: number}, f: fn () -> undefined) -> undefined
				fn g() {
					val mut x = {v: 1}
					val a = &x
					run(a, fn () { x.v = 5 })
				}
			`,
			want: []string{"10:13-10:30: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// The closure argument's loan ends with the call, so x is free afterwards.
		"ClosureArgumentReleasesItsLoanAfterTheCallOk": {
			src: captureDecls + `
				declare fn run(f: fn () -> undefined) -> undefined
				fn g() {
					val mut x = {v: 1}
					run(fn () { x.v = 5 })
					val a = &x
					readRead(a, a)
				}
			`,
			want: nil,
		},
		// A closure inside a closure captures x through the middle closure's parameter.
		"NestedClosureCaptureBorrowedBothWays": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val f = fn () {
						val h = fn () { readWrite(&x, &mut x) }
						h()
					}
					f()
				}
			`,
			want: []string{"9:37-9:43: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A capture of the enclosing function's parameter is tracked like a capture of a local.
		"ClosureCapturingAParameterBorrowedBothWays": {
			src: captureDecls + `
				fn g(x: &mut {v: number}) {
					val f = fn () { readWrite(&x, &mut x) }
					f()
				}
			`,
			want: []string{"7:36-7:42: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

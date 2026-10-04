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
	declare fn bump(x: &mut {v: number}) -> undefined
`

// TestClosureCapturedPlaces covers places reached through a closure's captured variables.
// Each case with a plain-function counterpart reports what that counterpart reports.
func TestClosureCapturedPlaces(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Calling a closure that writes x takes a mutable loan of x for the call, which
		// conflicts with the live immutable borrow `a`. The plain write is the same program
		// without the closure.
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
			want: []string{"11:6-11:7: cannot borrow 'x' as mutable while it is borrowed as immutable"},
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
			want: []string{"10:6-10:9: cannot assign to 'x.v' while it is borrowed as immutable"},
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
			want: []string{"9:36-9:42: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		"PlainBorrowedBothWays": {
			src: captureDecls + `
				fn g(x: &mut {v: number}) { readWrite(&x, &mut x) }
			`,
			want: []string{"7:47-7:53: cannot borrow 'x' as mutable while it is borrowed as immutable"},
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
			want: []string{"11:12-11:13: use of moved value 'x'"},
		},
		"PlainMovedTwice": {
			src: captureDecls + `
				fn g(x: {v: number}) {
					take(x)
					take(x)
				}
			`,
			want: []string{"9:11-9:12: use of moved value 'x'"},
		},
		// Calling a closure that only reads x takes an immutable loan, which sits beside a live
		// `&`.
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
		// Calling a closure that writes x takes a mutable loan, which sits beside a live `&mut`.
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
		// Calling f reads x, so a call after x moves is a use after the move.
		"CallAfterMovingTheCapture": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					take(x)
					f()
				}
			`,
			want: []string{"11:6-11:7: use of moved value 'x'"},
		},
		// f reads x only while it runs, so x can move once the last call returns.
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
		// f reads x only while it runs, so a write between two calls is no conflict.
		"WriteBetweenCallsOfAReadingClosureOk": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					f()
					x.v = 5
					f()
				}
			`,
		},
		// Writing a closure that writes x takes no loan, so it can be written while `a` is
		// live. The call comes after a's last read.
		"WritingClosureCreatedBesideLiveBorrowOk": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { x.v = 5 }
					readRead(a, a)
					f()
				}
			`,
		},
		// A callee handed f may call it, so passing f takes the loans a call of f takes.
		"PassingAWritingClosureBesideLiveBorrow": {
			src: captureDecls + `
				declare fn call(f: fn () -> undefined) -> undefined
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { x.v = 5 }
					call(f)
					readRead(a, a)
				}
			`,
			want: []string{"12:11-12:12: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Copying f into another name calls nothing, so it takes no loan beside `a`.
		"CopyingAWritingClosureBesideLiveBorrowOk": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { x.v = 5 }
					val h = f
					readRead(a, a)
				}
			`,
		},
		// h holds f's closure, so calling h writes x beside the live `a`.
		"CallOfACopiedWritingClosureBesideLiveBorrow": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { x.v = 5 }
					val h = f
					h()
					readRead(a, a)
				}
			`,
			want: []string{"12:6-12:7: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Calling h reads x through f's closure, so a call after x moves is a use after the
		// move.
		"CallOfACopiedClosureAfterMovingTheCapture": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () { readRead(&x, &x) }
					val h = f
					take(x)
					h()
				}
			`,
			want: []string{"12:6-12:7: use of moved value 'x'"},
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
			want: []string{"11:13-11:30: cannot borrow 'x' as mutable while it is borrowed as immutable"},
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
			want: []string{"10:37-10:43: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A capture of the enclosing function's parameter is tracked like a capture of a local.
		"ClosureCapturingAParameterBorrowedBothWays": {
			src: captureDecls + `
				fn g(x: &mut {v: number}) {
					val f = fn () { readWrite(&x, &mut x) }
					f()
				}
			`,
			want: []string{"8:36-8:42: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// A closure that calls a nested closure writing x writes x when it is called.
		"NestedClosureWriteBesideLiveBorrow": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () {
						val h = fn () { x.v = 5 }
						h()
					}
					f()
					readRead(a, a)
				}
			`,
			want: []string{"14:6-14:7: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Calling the outer closure reads x through its nested closure, so a call after x
		// moves is a use after the move.
		"CallAfterMovingWhatANestedClosureReads": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					val f = fn () {
						val h = fn () { readRead(&x, &x) }
						h()
					}
					take(x)
					f()
				}
			`,
			want: []string{"14:6-14:7: use of moved value 'x'"},
		},
		// A `&mut` borrow of x inside the closure makes the loan a call takes mutable.
		"MutableBorrowInClosureBesideLiveBorrow": {
			src: captureDecls + `
				fn g() {
					val mut x = {v: 1}
					val a = &x
					val f = fn () { bump(&mut x) }
					f()
					readRead(a, a)
				}
			`,
			want: []string{"11:6-11:7: cannot borrow 'x' as mutable while it is borrowed as immutable"},
		},
		// Writing a closure that captures a moved value reads the moved value, and so does each
		// call of it.
		"ClosureCapturingAMovedValue": {
			src: captureDecls + `
				fn g() {
					val x = {v: 1}
					take(x)
					val f = fn () { readRead(&x, &x) }
					f()
				}
			`,
			want: []string{
				"10:14-10:40: use of moved value 'x'",
				"11:6-11:7: use of moved value 'x'",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

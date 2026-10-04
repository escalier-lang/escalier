package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapingClosureCapture covers a closure that outlives the body it is written in. One stored
// into a module-level binding or returned moves each owned local it captures, so a later use of
// the local reads a moved value. One stored into a caller-owned object borrows its captures, and
// the paths it hands the caller are weighed like a stored borrow's. A closure that stays in the
// body borrows its captures too.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A closure stored into a module-level binding moves p, so the read after it reads a moved value.
		"StoreIntoAGlobalMovesTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					val n = p.x
				}
			`,
			want: []string{"6:14-6:17: use of moved value 'p'"},
		},
		// A name bound to a closure carries the closure's captures to the store.
		"StoreOfABoundClosureIntoAGlobalMovesTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					sink = f
					val n = p.x
				}
			`,
			want: []string{"7:14-7:17: use of moved value 'p'"},
		},
		// A returned closure moves p as the frame ends, so nothing reads p afterwards.
		"ReturnOfAClosureLastInTheBodyOk": {
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					val n = p.x
					return f
				}
				fn go2() {
					val p = {x: 1}
					return fn () { return p.x }
				}
			`,
			want: nil,
		},
		// The return moves p only on its own branch.
		"UseOnTheBranchThatDoesNotReturnTheClosureOk": {
			src: `
				fn go(c: boolean) -> fn () -> number {
					val p = {x: 1}
					val f = fn () { return p.x }
					if c {
						return f
					}
					val n = p.x
					return fn () { return n }
				}
			`,
			want: nil,
		},
		// A closure stored into `out` borrows p, so the frame can still read p.
		"StoreIntoABorrowParameterBorrowsTheCaptureOk": {
			src: `
				fn go(out: &mut {cb: fn () -> number}) {
					val p = {x: 1}
					out.cb = fn () { return p.x }
					val n = p.x
				}
			`,
		},
		// The closure reads p only when the caller calls it, after the function returns, so a
		// write to p in the frame is no conflict.
		"WriteAfterStoringAReadingClosureOk": {
			src: `
				fn go(out: &mut {cb: fn () -> number}) {
					val mut p = {x: 1}
					out.cb = fn () { return p.x }
					p.x = 2
				}
			`,
		},
		// Returning p hands the caller p as an owned value beside the closure's borrow of it.
		"ReturningALocalAStoredClosureCapturesConflicts": {
			src: `
				fn go(out: &mut {cb: fn () -> number}) -> {x: number} {
					val p = {x: 1}
					out.cb = fn () { return p.x }
					return p
				}
			`,
			want: []string{"5:13-5:14: 'p' leaves the function both as an owned value and through a borrow"},
		},
		// The stored closure writes p while `q.r` hands the caller an immutable path to it.
		"StoringAWritingClosureBesideAnImmutableStoreConflicts": {
			src: `
				fn go(out: &mut {cb: fn () -> undefined}, q: &mut {r: &{x: number}}) {
					val mut p = {x: 1}
					out.cb = fn () { p.x = 2 }
					q.r = &p
				}
			`,
			want: []string{"5:12-5:14: 'p' leaves the function through a mutable path and an immutable one"},
		},
		// An owned parameter dies with the frame, so a closure stored into it borrows p.
		"StoreIntoAnOwnedParameterLeavesTheCaptureOk": {
			src: `
				fn go(box: mut {cb: fn () -> number}) {
					val p = {x: 1}
					box.cb = fn () { return p.x }
					val n = p.x
				}
			`,
			want: nil,
		},
		// A closure that stays in the body borrows p.
		"LocalClosureBorrowsTheCaptureOk": {
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					f()
					val n = p.x
				}
			`,
			want: nil,
		},
		// A captured primitive is copied rather than moved.
		"PrimitiveCaptureIsCopiedOk": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val n = 1
					sink = fn () { return n }
					val m = n
				}
			`,
			want: nil,
		},
		// A capture holding a borrow is not moved by a return.
		"BorrowedCaptureIsNotMovedOk": {
			src: `
				fn go(p: &{x: number}) {
					val f = fn () { return p.x }
					val n = p.x
					return f
				}
			`,
			want: nil,
		},
		// Reassigning f drops the closure that captured p, so returning f moves nothing.
		"ReassignedBindingNoLongerCarriesTheClosureOk": {
			src: `
				fn go() {
					val p = {x: 1}
					var f: fn () -> number = fn () { return p.x }
					f = fn () { return 0 }
					val n = p.x
					return f
				}
			`,
			want: nil,
		},
		// f holds the capturing closure on one branch, so storing f may move p.
		"StoreOfAClosureChosenOnABranchMayMoveTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go(c: boolean) {
					val p = {x: 1}
					var f: fn () -> number = fn () { return 0 }
					if c {
						f = fn () { return p.x }
					}
					sink = f
					val n = p.x
				}
			`,
			want: []string{"10:14-10:17: use of moved value 'p'"},
		},
		// A copy of the closure's name carries its captures.
		"StoreOfACopiedClosureMovesTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					val g = f
					sink = g
					val n = p.x
				}
			`,
			want: []string{"8:14-8:17: use of moved value 'p'"},
		},
		// A closure inside an object literal is part of the stored value.
		"StoreOfAClosureInALiteralMovesTheCapture": {
			src: `
				var sink: {cb: fn () -> number} = {cb: fn () { return 0 }}
				fn go() {
					val p = {x: 1}
					sink = {cb: fn () { return p.x }}
					val n = p.x
				}
			`,
			want: []string{"6:14-6:17: use of moved value 'p'"},
		},
		// A closure that calls another closure carries what that closure captures.
		"StoreOfAClosureCallingACapturingClosureMovesTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					sink = fn () { return f() }
					val n = p.x
				}
			`,
			want: []string{"7:14-7:17: use of moved value 'p'"},
		},
		// The first store moves p, so the second closure captures a moved value.
		"SecondEscapingClosureCapturesAMovedValue": {
			src: `
				var sink: fn () -> undefined = fn () {}
				var sink2: fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					sink2 = fn () { p.x = 3 }
				}
			`,
			want: []string{"7:14-7:31: use of moved value 'p'"},
		},
		// Each iteration's closure captures the p the previous iteration's store moved.
		"EscapingClosureInALoopCapturesAMovedValue": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go(xs: Array<number>) {
					val p = {x: 1}
					for x in xs {
						sink = fn () { return p.x }
					}
				}
			`,
			want: []string{"6:14-6:34: use of moved value 'p'"},
		},
		// A closure chosen by an `if`/`else` expression carries its captures to the store.
		"StoreOfAClosureFromAnIfElseMovesTheCapture": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go(c: boolean) {
					val p = {x: 1}
					val f = if c { (fn () { return p.x }) } else { (fn () { return 0 }) }
					sink = f
					val n = p.x
				}
			`,
			want: []string{"7:14-7:17: use of moved value 'p'"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

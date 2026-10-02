package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapingClosureCapture covers a closure that outlives the body it is written in. Such a
// closure moves each owned local it captures, so a later use of the local reads a moved value.
// A closure that stays in the body borrows its captures instead.
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
		// The caller keeps out, so a closure stored into it outlives p.
		"StoreIntoABorrowParameterMovesTheCapture": {
			src: `
				fn go(out: &mut {cb: fn () -> number}) {
					val p = {x: 1}
					out.cb = fn () { return p.x }
					val n = p.x
				}
			`,
			want: []string{"5:14-5:17: use of moved value 'p'"},
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
				fn go(out: &mut {o: {cb: fn () -> number}}) {
					val p = {x: 1}
					out.o = {cb: fn () { return p.x }}
					val n = p.x
				}
			`,
			want: []string{"5:14-5:17: use of moved value 'p'"},
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
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

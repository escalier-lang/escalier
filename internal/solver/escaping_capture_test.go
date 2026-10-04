package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapingClosureCapture covers a closure that outlives the body it is written in. A returned
// closure moves each owned local it captures, so a later use of the local reads a moved value.
// One stored into a module-level binding may run during any call after the store, so each such
// call borrows the owned locals it writes. A local it only reads stays free. One stored into a
// caller-owned object borrows its captures, and the paths it hands the caller are weighed like
// a stored borrow's. A closure that stays in the body borrows its captures too.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A closure stored into a module-level binding that only reads p leaves p free, so the
		// frame can read p.
		"ReadAfterStoringAReadingClosureIntoAGlobalOk": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					val n = p.x
				}
			`,
		},
		// The closure needs p unchanged only while it runs, and the frame cannot write p then.
		"WriteAfterStoringAReadingClosureIntoAGlobalOk": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go() {
					val mut p = {x: 1}
					sink = fn () { return p.x }
					p.x = 2
				}
			`,
		},
		// take receives p as its owner, and may call sink, which writes p.
		"MoveAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn take(x: {x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					take(p)
				}
			`,
			want: []string{"7:11-7:12: cannot move 'p' in a call that may run 'sink', which writes it"},
		},
		// A closure that only reads p does not keep p from moving.
		"MoveAfterStoringAReadingClosureIntoAGlobalOk": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				declare fn take(x: {x: number}) -> undefined
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					take(p)
				}
			`,
		},
		// readIt may call sink, which writes p while readIt reads it through `a`.
		"BorrowAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"8:6-8:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// Nothing can call sink while `a` is in use, so the borrow is no conflict.
		// A borrow written in the call's own arguments is live while the call may run sink.
		"BorrowInACallAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					readIt(&p)
				}
			`,
			want: []string{"7:6-7:16: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// noop may run sink, which writes p after the move gave it to q.
		"CallAfterMovingALocalAGlobalClosureWritesConflicts": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn noop() -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val q = p
					noop()
				}
			`,
			want: []string{"8:6-8:12: this call may run 'sink', which writes 'p' after it was moved"},
		},
		"UnusedBorrowAfterStoringAWritingClosureIntoAGlobalOk": {
			src: `
				var sink: fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
				}
			`,
		},
		// `a` is taken before the store and still in use at readIt, which may call sink.
		"StoringAWritingClosureBesideALiveBorrowConflicts": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val a = &p
					sink = f
					readIt(a)
				}
			`,
			want: []string{"9:6-9:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// A name bound to a closure carries the closure's captures to the store.
		"StoreOfABoundClosureIntoAGlobalBorrowsTheCapture": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					sink = f
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"9:6-9:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
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
		// A captured primitive is copied rather than borrowed.
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
		// f holds the capturing closure on one branch, so storing f may borrow p.
		"StoreOfAClosureChosenOnABranchMayBorrowTheCapture": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go(c: boolean) {
					val mut p = {x: 1}
					var f: fn () -> undefined = fn () {}
					if c {
						f = fn () { p.x = 2 }
					}
					sink = f
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"12:6-12:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// A copy of the closure's name carries its captures.
		"StoreOfACopiedClosureBorrowsTheCapture": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = f
					sink = g
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"10:6-10:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// A closure inside an object literal is part of the stored value.
		"StoreOfAClosureInALiteralBorrowsTheCapture": {
			src: `
				var sink: {cb: fn () -> undefined} = {cb: fn () {}}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = {cb: fn () { p.x = 2 }}
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"8:6-8:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// A closure that calls another closure carries what that closure captures.
		"StoreOfAClosureCallingACapturingClosureBorrowsTheCapture": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					sink = fn () { f() }
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"9:6-9:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// Two stored closures that both write p are two writers, which Rule 3 of
		// planning/lifetimes/requirements.md allows.
		"TwoGlobalClosuresWritingOneLocalOk": {
			src: `
				var sink: fn () -> undefined = fn () {}
				var sink2: fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					sink2 = fn () { p.x = 3 }
				}
			`,
		},
		// A closure that only reads p takes no loan, so storing one on every iteration is no
		// conflict.
		"StoreIntoAGlobalInALoopOk": {
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn go(xs: Array<number>) {
					val p = {x: 1}
					for x in xs {
						sink = fn () { return p.x }
					}
				}
			`,
		},
		// A closure chosen by an `if`/`else` expression carries its captures to the store.
		"StoreOfAClosureFromAnIfElseBorrowsTheCapture": {
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go(c: boolean) {
					val mut p = {x: 1}
					val f = if c { (fn () { p.x = 2 }) } else { (fn () {}) }
					sink = f
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"9:6-9:15: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

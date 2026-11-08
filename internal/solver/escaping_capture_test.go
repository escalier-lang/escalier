package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapingClosureCapture covers a closure that outlives the body it is written in. A returned
// closure moves each owned local it captures at the return. One stored into a caller-owned object
// borrows its captures, and the paths it hands the caller are weighed like a stored borrow's. A
// closure that stays in the body borrows its captures too.
//
// A module-level binding keeps its function for 'static. A closure over locals of the body stored
// there takes those locals over at the store, the way a returned closure takes them at the
// return, so each later use of one in the body is a use of a value the closure owns. A closure
// over a parameter, or over a borrow from outside the body, holds a borrow it cannot take, and
// the store is rejected.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// sink takes p over at the store, so the later read of p reads a value the closure owns.
		"ReadAfterStoringAReadingClosureIntoAGlobalUsesATakenLocal": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					val n = p.x
				}
			`,
			want: []string{"6:14-6:17: 'p' is used after 'sink' took it over for 'static"},
		},
		// sink takes p over at the store, so the later write of p writes a value the closure owns.
		"WriteAfterStoringAReadingClosureIntoAGlobalUsesATakenLocal": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val mut p = {x: 1}
					sink = fn () { return p.x }
					p.x = 2
				}
			`,
			want: []string{"6:6-6:7: 'p' is used after 'sink' took it over for 'static"},
		},
		// sink takes p over at the store, so take receives a value the closure owns.
		"MoveAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn take(x: {x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					take(p)
				}
			`,
			want: []string{"7:11-7:12: 'p' is used after 'sink' took it over for 'static"},
		},
		// A direct call to an overloaded name moves p as much as any other call, after sink took it
		// over.
		"MoveIntoAnOverloadAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn take(x: {x: number}) -> undefined
				declare fn take(x: string) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					take(p)
				}
			`,
			want: []string{"8:11-8:12: 'p' is used after 'sink' took it over for 'static"},
		},
		// The borrow of p after the store borrows a value sink took over.
		"BorrowAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"7:15-7:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// A borrow written in the call's own arguments borrows a value sink took over.
		"BorrowInACallAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					readIt(&p)
				}
			`,
			want: []string{"7:14-7:15: 'p' is used after 'sink' took it over for 'static"},
		},
		// Moving p into q after the store moves a value sink took over.
		"CallAfterMovingALocalAGlobalClosureWritesConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn noop() -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val q = p
					noop()
				}
			`,
			want: []string{"7:14-7:15: 'p' is used after 'sink' took it over for 'static"},
		},
		// A borrow of p after the store uses a value sink took over, even when nothing uses the
		// borrow.
		"UnusedBorrowAfterStoringAWritingClosureIntoAGlobalUsesATakenLocal": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
				}
			`,
			want: []string{"6:15-6:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// `a` borrows p before the store and is still in use at readIt, so the store cannot move p
		// into the closure.
		"StoringAWritingClosureBesideALiveBorrowConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val a = &p
					sink = f
					readIt(a)
				}
			`,
			want: []string{"8:13-8:14: cannot move 'p' while it is borrowed"},
		},
		// A name bound to a closure carries the closure's captures to the store, so sink takes p
		// over and the later borrow uses it.
		"StoreOfABoundClosureIntoAGlobalTakesTheCapture": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					sink = f
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"8:15-8:16: 'p' is used after 'sink' took it over for 'static"},
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
		// Returning p hands the caller p as an owned value beside the closure's borrow of it, so
		// the return breaks E3.
		"ReturningALocalAStoredClosureCapturesConflicts": {
			src: `
				fn go(out: &mut {cb: fn () -> number}) -> {x: number} {
					val p = {x: 1}
					out.cb = fn () { return p.x }
					return p
				}
			`,
			want: []string{"5:13-5:14: cannot move 'p' while it is borrowed"},
		},
		// The stored closure writes p while `q.r` hands the caller an immutable path to it. The
		// caller can call `out.cb` while it reads `q.r`, so `&p` breaks E2.
		"StoringAWritingClosureBesideAnImmutableStoreConflicts": {
			src: `
				fn go(out: &mut {cb: fn () -> undefined}, q: &mut {r: &{x: number}}) {
					val mut p = {x: 1}
					out.cb = fn () { p.x = 2 }
					q.r = &p
				}
			`,
			want: []string{"5:12-5:14: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// The writing closure stays in a local that the caller never reaches and that no call
		// runs, so `&p` hands the caller the only path to p.
		"KeepingAWritingClosureInALocalBesideAnImmutableStoreOk": {
			src: `
				fn go(q: &mut {r: &{x: number}}) {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					q.r = &p
				}
			`,
		},
		// An owned parameter dies with the frame, so a closure stored into it borrows p.
		"StoreIntoAnOwnedParameterLeavesTheCaptureOk": {
			src: `
				fn go(mut box: {cb: fn () -> number}) {
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
				var sink: 'static fn () -> number = fn () { return 0 }
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
		// f's annotation gives it a lifetime of its own, so the store cannot tell which closure f
		// holds and keeps the error for a function that may borrow.
		"StoreOfAClosureChosenOnABranchMayBorrowTheCapture": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
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
			want: []string{"10:13-10:14: `f` may hold borrows that do not live for 'static, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
		// A copy of the closure's name carries its captures, so sink takes p over.
		"StoreOfACopiedClosureTakesTheCapture": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
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
			want: []string{"9:15-9:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// A closure inside an object literal is part of the stored value, so sink takes p over.
		"StoreOfAClosureInALiteralTakesTheCapture": {
			src: `
				var sink: {cb: 'static fn () -> undefined} = {cb: fn () {}}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					sink = {cb: fn () { p.x = 2 }}
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"7:15-7:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// A closure that calls another closure carries what that closure captures, so sink takes p
		// over.
		"StoreOfAClosureCallingACapturingClosureTakesTheCapture": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					sink = fn () { f() }
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"8:15-8:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// sink takes p over, so the closure stored into sink2 captures a value the first closure
		// owns.
		"TwoGlobalClosuresWritingOneLocalConflict": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				var sink2: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					sink2 = fn () { p.x = 3 }
				}
			`,
			want: []string{"7:14-7:31: 'p' is used after 'sink' took it over for 'static"},
		},
		// Each iteration stores a closure that takes p over, so the second iteration captures a
		// value the first closure owns.
		"StoreIntoAGlobalInALoopConflicts": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(xs: Array<number>) {
					val p = {x: 1}
					for x in xs {
						sink = fn () { return p.x }
					}
				}
			`,
			want: []string{"6:14-6:34: 'p' is used after 'sink' took it over for 'static"},
		},
		// A closure chosen by an `if`/`else` expression carries its captures to the store, so sink
		// takes p over.
		"StoreOfAClosureFromAnIfElseTakesTheCapture": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn go(c: boolean) {
					val mut p = {x: 1}
					val f = if c { (fn () { p.x = 2 }) } else { (fn () {}) }
					sink = f
					val a = &p
					readIt(a)
				}
			`,
			want: []string{"8:15-8:16: 'p' is used after 'sink' took it over for 'static"},
		},
		// Nothing in go uses p after the store, so sink takes it over and the store checks.
		"StoreOfAClosureOverALocalDeadAfterTheStoreOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
				}
			`,
		},
		// A `var` the body neither reads nor writes after the store is taken over too.
		"StoreOfAClosureOverAVarDeadAfterTheStoreOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					var n = 1
					n = n + 1
					sink = fn () { return n }
				}
			`,
		},
		// The closure reads the binding n, so a later write of n would change what the closure
		// sees. The write uses a value sink took over.
		"StoreOfAClosureOverAVarWrittenAfterTheStoreConflicts": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					var n = 1
					sink = fn () { return n }
					n = 2
				}
			`,
			want: []string{"6:6-6:7: 'n' is used after 'sink' took it over for 'static"},
		},
		// g still borrows p after the store, so the store cannot move p into the closure.
		"StoreBesideAnotherClosureOfTheLocalConflicts": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() -> number {
					val p = {x: 1}
					val g = fn () { return p.x }
					sink = fn () { return p.x }
					return g()
				}
			`,
			want: []string{"6:13-6:33: cannot move 'p' while it is borrowed"},
		},
		// A parameter belongs to the caller, so the closure cannot take it over.
		"StoreOfAClosureOverAParameterRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(p: {x: number}) {
					sink = fn () { return p.x }
				}
			`,
			want: []string{"4:13-4:33: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
		// o holds a borrow of the caller's value, which the closure cannot take over.
		"StoreOfAClosureOverAnOutsideBorrowRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(p: &{x: number}) {
					val o = {r: p}
					sink = fn () { return o.r.x }
				}
			`,
			want: []string{"5:13-5:35: this closure borrows `o`, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

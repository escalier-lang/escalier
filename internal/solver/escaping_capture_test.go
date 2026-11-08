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
// there keeps its borrows of those locals for the rest of the program. Each borrow has the kind
// the closure's use gives it, immutable for a read and mutable for a write. Each later use of one
// of those locals in the body is checked against the kept borrow:
//
//   - After a closure that only reads p, a later read of p or `&p` checks. A write of p, `&mut p`,
//     or a move of p is rejected.
//   - After a closure that writes p, every later use of p is rejected.
//
// A closure over a parameter, or over a borrow from outside the body, holds a borrow the slot
// cannot keep, and the store is rejected.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// sink keeps an immutable borrow of p, and a read beside it checks.
		"ReadAfterStoringAReadingClosureIntoAGlobalOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					val n = p.x
				}
			`,
		},
		// sink keeps an immutable borrow of p, so a later write of p breaks E2.
		"WriteAfterStoringAReadingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val mut p = {x: 1}
					sink = fn () { return p.x }
					p.x = 2
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable by 'sink'"},
		},
		// sink keeps a mutable borrow of p, so a later read of p breaks E2.
		"ReadAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val n = p.x
				}
			`,
			want: []string{"6:14-6:17: cannot read 'p.x' while it is borrowed as mutable by 'sink'"},
		},
		// sink keeps a mutable borrow of p, so a later write of p breaks E2.
		"WriteAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					p.x = 3
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as mutable by 'sink'"},
		},
		// take moves p while sink still borrows it, which breaks E3.
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
			want: []string{"7:11-7:12: cannot move 'p' while it is borrowed by 'sink'"},
		},
		// A direct call to an overloaded name moves p as much as any other call.
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
			want: []string{"8:11-8:12: cannot move 'p' while it is borrowed by 'sink'"},
		},
		// `&p` makes an immutable path to p while sink keeps a mutable one.
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
			want: []string{"7:14-7:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// A borrow written in the call's own arguments conflicts the same way.
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
			want: []string{"7:13-7:15: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// Moving p into q breaks E3 against sink's borrow, whether or not anything runs sink.
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
			want: []string{"7:14-7:15: cannot move 'p' while it is borrowed by 'sink'"},
		},
		// sink's borrow of p never ends, so `&p` conflicts with it even when nothing uses `a`.
		"UnusedBorrowAfterStoringAWritingClosureIntoAGlobalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// `a` borrows p before the store and is still in use at readIt, so sink's mutable borrow
		// of p conflicts with it at the store.
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
			want: []string{"8:13-8:14: cannot borrow 'p' as mutable for 'sink' while it is borrowed as immutable"},
		},
		// A name bound to a closure carries the closure's captures to the store, so sink keeps
		// its borrow of p.
		"StoreOfABoundClosureIntoAGlobalKeepsTheCapture": {
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
			want: []string{"8:14-8:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// f still names the closure sink keeps, and calling it uses the closure's own borrow.
		"CallOfAStoredClosureThroughItsLocalNameOk": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					sink = f
					f()
				}
			`,
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
		// A copy of the closure's name carries its captures, so sink keeps its borrow of p.
		"StoreOfACopiedClosureKeepsTheCapture": {
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
			want: []string{"9:14-9:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// A closure inside an object literal is part of the stored value, so sink keeps its
		// borrow of p.
		"StoreOfAClosureInALiteralKeepsTheCapture": {
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
			want: []string{"7:14-7:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// A closure that calls another closure carries what that closure captures, so sink keeps
		// f's borrow of p.
		"StoreOfAClosureCallingACapturingClosureKeepsTheCapture": {
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
			want: []string{"8:14-8:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// sink keeps a mutable borrow of p, so the closure stored into sink2 makes a second
		// mutable path to p that sink may write through at any time.
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
			want: []string{"7:14-7:31: cannot borrow 'p' as mutable while it is borrowed as mutable by 'sink'"},
		},
		// Two closures that only read p each keep an immutable borrow of it.
		"TwoGlobalClosuresReadingOneLocalOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				var sink2: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					sink2 = fn () { return p.x }
				}
			`,
		},
		// Each iteration keeps another immutable borrow of p, and immutable borrows do not
		// conflict.
		"StoreOfAReadingClosureIntoAGlobalInALoopOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(xs: Array<number>) {
					val p = {x: 1}
					for x in xs {
						sink = fn () { return p.x }
					}
				}
			`,
		},
		// The closure an iteration stores still writes p on the next iteration, so the next
		// closure's mutable borrow of p conflicts with it.
		"StoreOfAWritingClosureIntoAGlobalInALoopConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go(xs: Array<number>) {
					val mut p = {x: 1}
					for x in xs {
						sink = fn () { p.x = 2 }
					}
				}
			`,
			want: []string{"6:14-6:31: cannot borrow 'p' as mutable while it is borrowed as mutable by 'sink'"},
		},
		// A closure chosen by an `if`/`else` expression carries its captures to the store, so sink
		// keeps its borrow of p.
		"StoreOfAClosureFromAnIfElseKeepsTheCapture": {
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
			want: []string{"8:14-8:16: cannot borrow 'p' as immutable while it is borrowed as mutable by 'sink'"},
		},
		// Nothing in go uses p after the store, so sink's borrow conflicts with nothing.
		"StoreOfAClosureOverALocalDeadAfterTheStoreOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
				}
			`,
		},
		// A `var` the body neither reads nor writes after the store is kept the same way.
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
		// The closure reads the binding n, so a later write of n changes what the closure
		// sees. sink keeps an immutable borrow of n, which the write breaks.
		"StoreOfAClosureOverAVarWrittenAfterTheStoreConflicts": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					var n = 1
					sink = fn () { return n }
					n = 2
				}
			`,
			want: []string{"6:6-6:7: cannot assign to 'n' while it is borrowed as immutable by 'sink'"},
		},
		// The closure assigns to n, so sink keeps a mutable borrow of n and a later read
		// breaks it.
		"StoreOfAClosureAssigningAVarReadAfterTheStoreConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					var n = 1
					sink = fn () { n = n + 1 }
					val m = n
				}
			`,
			want: []string{"6:14-6:15: cannot read 'n' while it is borrowed as mutable by 'sink'"},
		},
		// g and the closure sink keeps both only read p.
		"StoreBesideAnotherReadingClosureOfTheLocalOk": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() -> number {
					val p = {x: 1}
					val g = fn () { return p.x }
					sink = fn () { return p.x }
					return g()
				}
			`,
		},
		// The closure sink keeps writes p, so the call of g reads p while sink's mutable borrow
		// of it is live.
		"StoreOfAWritingClosureBesideAnotherClosureOfTheLocalConflicts": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() -> number {
					val mut p = {x: 1}
					val g = fn () { return p.x }
					sink = fn () { p.x = 2 }
					return g()
				}
			`,
			want: []string{"7:13-7:14: this call runs 'g', which reads 'p' while it is borrowed as mutable by 'sink'"},
		},
		// A parameter belongs to the caller, so sink cannot keep a borrow of it.
		"StoreOfAClosureOverAParameterRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(p: {x: number}) {
					sink = fn () { return p.x }
				}
			`,
			want: []string{"4:13-4:33: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
		// o holds a borrow of the caller's value, which sink cannot keep.
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

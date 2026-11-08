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
// A module-level binding holds only a function that borrows nothing shorter than the program, so
// each case that stores a closure over a local into `sink` asserts that the store is rejected.
// The dataflow pass still reads the store as a 'static borrow of the captures, so a case also
// asserts each conflict that borrow causes later in the body, such as a move of a captured local.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// The closure only reads p, so the frame's later read of p conflicts with nothing. The
		// store is rejected, and nothing later in the body is reported.
		"ReadAfterStoringAReadingClosureIntoAGlobalRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					sink = fn () { return p.x }
					val n = p.x
				}
			`,
			want: []string{"5:13-5:33: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// The closure needs p unchanged only while it runs, so the frame's write of p conflicts
		// with nothing. The store is rejected, and nothing later in the body is reported.
		"WriteAfterStoringAReadingClosureIntoAGlobalRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go() {
					val mut p = {x: 1}
					sink = fn () { return p.x }
					p.x = 2
				}
			`,
			want: []string{"5:13-5:33: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// take receives p as its owner, and may call sink, which writes p.
		// The store is rejected, and the dataflow pass still reports the later conflict.
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
			want: []string{"6:13-6:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level", "7:11-7:12: cannot move 'p' while it is borrowed"},
		},
		// A direct call to an overloaded name may run sink as much as any other call.
		// The store is rejected, and the dataflow pass still reports the later conflict.
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
			want: []string{"7:13-7:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level", "8:11-8:12: cannot move 'p' while it is borrowed"},
		},
		// readIt may call sink, which writes p while readIt reads it through `a`.
		// The store is rejected, and nothing later in the body is reported.
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
			want: []string{"6:13-6:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A borrow written in the call's own arguments is live while the call may run sink. The
		// store is rejected, and nothing later in the body is reported.
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
			want: []string{"6:13-6:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// noop may run sink, which writes p after the move gave it to q. The pass rejects the
		// move itself, since sink holds its borrow of p between calls.
		// The store is rejected, and the dataflow pass still reports the later conflict.
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
			want: []string{"6:13-6:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level", "7:14-7:15: cannot move 'p' while it is borrowed"},
		},
		// The store is rejected, and nothing later in the body is reported.
		"UnusedBorrowAfterStoringAWritingClosureIntoAGlobalRejected": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					val a = &p
				}
			`,
			want: []string{"5:13-5:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// `a` is taken before the store and still in use at readIt, which may call sink.
		// The store is rejected, and nothing later in the body is reported.
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
			want: []string{"8:13-8:14: `f` borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A name bound to a closure carries the closure's captures to the store.
		// The store is rejected, and nothing later in the body is reported.
		"StoreOfABoundClosureIntoAGlobalBorrowsTheCapture": {
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
			want: []string{"7:13-7:14: `f` borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
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
		// f holds the capturing closure on one branch, so storing f may borrow p.
		// The store is rejected, and nothing later in the body is reported.
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
		// A copy of the closure's name carries its captures.
		// The store is rejected, and nothing later in the body is reported.
		"StoreOfACopiedClosureBorrowsTheCapture": {
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
			want: []string{"8:13-8:14: `g` borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A closure inside an object literal is part of the stored value.
		// The store is rejected, and nothing later in the body is reported.
		"StoreOfAClosureInALiteralBorrowsTheCapture": {
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
			want: []string{"6:18-6:35: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A closure that calls another closure carries what that closure captures.
		// The store is rejected, and nothing later in the body is reported.
		"StoreOfAClosureCallingACapturingClosureBorrowsTheCapture": {
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
			want: []string{"7:13-7:26: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// Two stored closures that both write p are two writers, which Rule 3 of
		// planning/lifetimes/requirements.md allows. Each store is rejected, and nothing later in
		// the body is reported.
		"TwoGlobalClosuresWritingOneLocalRejected": {
			src: `
				var sink: 'static fn () -> undefined = fn () {}
				var sink2: 'static fn () -> undefined = fn () {}
				fn go() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					sink2 = fn () { p.x = 3 }
				}
			`,
			want: []string{"6:13-6:30: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level", "7:14-7:31: this closure borrows `p`, but `sink2` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A closure that only reads p takes no loan, so storing one on every iteration is no
		// conflict between iterations. The store is rejected, and nothing later in the body is
		// reported.
		"StoreIntoAGlobalInALoopRejected": {
			src: `
				var sink: 'static fn () -> number = fn () { return 0 }
				fn go(xs: Array<number>) {
					val p = {x: 1}
					for x in xs {
						sink = fn () { return p.x }
					}
				}
			`,
			want: []string{"6:14-6:34: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
		// A closure chosen by an `if`/`else` expression carries its captures to the store.
		// The store is rejected, and nothing later in the body is reported.
		"StoreOfAClosureFromAnIfElseBorrowsTheCapture": {
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
			want: []string{"6:22-6:39: this closure borrows `p`, but `sink` keeps it for 'static; pass a function that borrows nothing, or declare `p` at module level"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

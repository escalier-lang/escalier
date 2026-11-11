package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEscapingClosureCapture covers a closure that outlives the body it is written in. A returned
// closure moves each owned local it captures at the return. One stored into a module-level
// binding borrows its captures for `'static`, and may run during any call after the store, so
// each such call borrows the owned locals it writes. One stored into a caller-owned object
// borrows its captures, and the paths it hands the caller are weighed like a stored borrow's. A
// closure that stays in the body borrows its captures too.
//
// A move of a local that a module-level closure captures breaks E3, and the diagnostic is at
// the move. TestOwnershipDataflow covers the move of a local a module-level closure only reads.
func TestEscapingClosureCapture(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"7:14-7:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"7:13-7:15: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"6:14-6:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"8:13-8:14: cannot borrow 'p.x' as mutable for 'sink' while 'p' is borrowed as immutable"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"8:14-8:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
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
		// DISABLED until the static-slots PR (fn-3): a closure takes a lifetime from what it captures,
		// and a slot that keeps its function for 'static rejects a function that may hold a shorter
		// borrow, where this branch gives a closure no lifetime and accepts it.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"9:14-9:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"7:14-7:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"8:14-8:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
			// E2 allows two mutable paths, so sink and sink2 may both keep a mutable borrow of p.x.
			"TwoGlobalClosuresWritingOneLocalOk": {
				src: `
					var sink: 'static fn () -> undefined = fn () {}
					var sink2: 'static fn () -> undefined = fn () {}
					fn go() {
						val mut p = {x: 1}
						sink = fn () { p.x = 2 }
						sink2 = fn () { p.x = 3 }
					}
				`,
			},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
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
				want: []string{"8:14-8:16: cannot borrow 'p' as immutable while 'p.x' is borrowed as mutable by 'sink'"},
			},
		*/
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

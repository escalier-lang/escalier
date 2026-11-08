package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClosureRunsFollowHolders covers the closures a call runs when the closure reaches the
// called name through a copy, a field, a branch, a pattern, or another closure. Each rejected
// program pairs with an accepted twin where the name no longer holds the closure, or where no
// conflicting borrow is live at the call.
func TestClosureRunsFollowHolders(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"CopiedClosureWritesCaptureBesideLiveBorrow": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = f
					val r = &p
					g()
					return r.x
				}
			`,
			want: []string{"7:6-7:7: this call runs 'g', which writes 'p' while it is borrowed as immutable"},
		},
		"CopiedClosureCalledBeforeTheBorrowOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = f
					g()
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		"ClosureAssignedOnOneBranch": {
			src: `
				fn go(c: boolean) -> number {
					val mut p = {x: 1}
					var g: fn () -> undefined = fn () {}
					if c {
						g = fn () { p.x = 2 }
					}
					val r = &p
					g()
					return r.x
				}
			`,
			want: []string{"9:6-9:7: this call runs 'g', which writes 'p' while it is borrowed as immutable"},
		},
		"ReassignedNameNoLongerRunsTheClosureOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					var g = fn () { p.x = 2 }
					g = fn () {}
					val r = &p
					g()
					return r.x
				}
			`,
			want: nil,
		},
		"ClosureInAFieldPassedToACall": {
			src: `
				declare fn run(o: {cb: fn () -> undefined}) -> undefined
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					val r = &p
					run(o)
					return r.x
				}
			`,
			want: []string{"8:10-8:11: this call may run 'o', which writes 'p' while it is borrowed as immutable"},
		},
		"ClosureDestructuredFromAField": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					val {cb} = o
					val r = &p
					cb()
					return r.x
				}
			`,
			want: []string{"8:6-8:8: this call runs 'cb', which writes 'p' while it is borrowed as immutable"},
		},
		"ClosureCallingAClosureWritesItsCapture": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = fn () { f() }
					val r = &p
					g()
					return r.x
				}
			`,
			want: []string{"7:6-7:7: this call runs 'g', which writes 'p' while it is borrowed as immutable"},
		},
		"ClosureCallingAClosureBeforeTheBorrowOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = fn () { f() }
					g()
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		// g holds f's closure, which borrows p until g's last call, so moving p first
		// breaks E3.
		"MoveOfACaptureWhileAClosureCallingItsClosureIsLive": {
			src: `
				declare fn take(x: {x: number}) -> undefined
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val g = fn () { f() }
					take(p)
					g()
				}
			`,
			want: []string{"7:11-7:12: cannot move 'p' while it is borrowed"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestClosureRunsAtPlaces covers a call whose callee or argument is a member or index place,
// which runs or may run the closures held at that place. Each rejected program pairs with an
// accepted twin where no conflicting borrow is live at the call.
func TestClosureRunsAtPlaces(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"MemberCalleeWritesCaptureBesideLiveBorrow": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					val r = &p
					o.cb()
					return r.x
				}
			`,
			want: []string{"7:6-7:10: this call runs 'o.cb', which writes 'p' while it is borrowed as immutable"},
		},
		"MemberCalleeCalledBeforeTheBorrowOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					o.cb()
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		"MemberCalleeOfASiblingFieldOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f, other: fn () {}}
					val r = &p
					o.other()
					return r.x
				}
			`,
			want: nil,
		},
		"MemberArgumentWritesCaptureBesideLiveBorrow": {
			src: `
				declare fn run(cb: fn () -> undefined) -> undefined
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					val r = &p
					run(o.cb)
					return r.x
				}
			`,
			want: []string{"8:10-8:14: this call may run 'o.cb', which writes 'p' while it is borrowed as immutable"},
		},
		"MemberArgumentPassedBeforeTheBorrowOk": {
			src: `
				declare fn run(cb: fn () -> undefined) -> undefined
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {cb: f}
					run(o.cb)
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		"IndexCalleeWritesCaptureBesideLiveBorrow": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {list: [f]}
					val r = &p
					o.list[0]()
					return r.x
				}
			`,
			want: []string{"7:6-7:15: this call runs 'o.list[0]', which writes 'p' while it is borrowed as immutable"},
		},
		"IndexCalleeCalledBeforeTheBorrowOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {list: [f]}
					o.list[0]()
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		// A dynamic index makes the place the list, so the call may run any closure the list
		// holds.
		"DynamicIndexCalleeWritesCaptureBesideLiveBorrow": {
			src: `
				fn go(i: number) -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {list: [f]}
					val r = &p
					o.list[i]()
					return r.x
				}
			`,
			want: []string{"7:6-7:15: this call may run 'o.list', which writes 'p' while it is borrowed as immutable"},
		},
		"DynamicIndexCalleeCalledBeforeTheBorrowOk": {
			src: `
				fn go(i: number) -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val o = {list: [f]}
					o.list[i]()
					val r = &p
					return r.x
				}
			`,
			want: nil,
		},
		// A method read after a dynamic index reads no closure the element holds.
		"MethodCalleeAfterADynamicIndexOk": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				fn go(i: number) -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val o = {list: [b]}
					val r = &p
					o.list[i].size()
					return r.x
				}
			`,
			want: nil,
		},
		"FieldCalleeAfterADynamicIndex": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				fn go(i: number) -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val o = {list: [b]}
					val r = &p
					o.list[i].cb()
					return r.x
				}
			`,
			want: []string{"13:6-13:18: this call may run 'o.list', which writes 'p' while it is borrowed as immutable"},
		},
		// Writing the field replaces the closure it held, so the call runs only the new one.
		"FieldReassignedToAClosureThatDoesNotWriteOk": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut o = {cb: f}
					o.cb = fn () {}
					val r = &p
					o.cb()
					return r.x
				}
			`,
			want: nil,
		},
		"FieldReassignedToAClosureThatWrites": {
			src: `
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut o: {cb: fn () -> undefined} = {cb: fn () {}}
					o.cb = f
					val r = &p
					o.cb()
					return r.x
				}
			`,
			want: []string{"8:6-8:10: this call runs 'o.cb', which writes 'p' while it is borrowed as immutable"},
		},
		"InstanceFieldCalleeWritesCaptureBesideLiveBorrow": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val r = &p
					b.cb()
					return r.x
				}
			`,
			want: []string{"12:6-12:10: this call runs 'b.cb', which writes 'p' while it is borrowed as immutable"},
		},
		// The method's place `b.size` is disjoint from `b.cb`, which holds the closure.
		"InstanceMethodCalleeBesideAFieldHoldingAClosureOk": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val r = &p
					b.size()
					return r.x
				}
			`,
			want: nil,
		},
		// `view` returns a borrow that holds every closure b holds, at the whole place q. A
		// method read off q reads none of them.
		"MethodCalleeOfAViewHoldingAClosureOk": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				declare fn view<'a, 'b>(b: &'a mut Box<'b>) -> &'a mut Box<'b>
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val q = view(&mut b)
					val r = &p
					q.size()
					return r.x
				}
			`,
			want: nil,
		},
		// q holds b's closures at the whole place q, so a call of the field q.cb may run each
		// closure q holds rather than surely running one.
		"FieldCalleeOfAViewHoldingAClosure": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					size(&self) -> number { return 0 },
				}
				declare fn view<'a, 'b>(b: &'a mut Box<'b>) -> &'a mut Box<'b>
				fn go() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val mut b = Box(fn () {})
					b.cb = f
					val q = view(&mut b)
					val r = &p
					q.cb()
					return r.x
				}
			`,
			want: []string{"14:6-14:10: this call may run 'q.cb', which writes 'p' while it is borrowed as immutable"},
		},
		// v holds the closures of both b.cb and b.other at the whole place v. So `v.cb()` may
		// run g, which writes q, though the call never runs it when v.cb holds f.
		"FieldCalleeOfAViewMayRunASiblingsClosure": {
			src: `
				class Box<'a> {
					cb: 'a fn () -> undefined,
					other: 'a fn () -> undefined,
				}
				declare fn view<'a, 'b>(b: &'a mut Box<'b>) -> &'a mut Box<'b>
				fn go() -> number {
					val mut p = {x: 1}
					val mut q = {x: 1}
					val f = fn () { p.x = 2 }
					val g = fn () { q.x = 2 }
					val mut b = Box(fn () {}, fn () {})
					b.cb = f
					b.other = g
					val v = view(&mut b)
					val r = &q
					v.cb()
					return r.x
				}
			`,
			want: []string{"17:6-17:10: this call may run 'v.cb', which writes 'q' while it is borrowed as immutable"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestRetainedClosureWritesFollowCaptures covers a retained closure that writes a local
// through a closure it captures. The capture is the only use of the captured binding, so
// the stored closure reaches the inner closure's loans only through the capture. `onClick`
// keeps its handler for 'static, so it takes over the local the inner closure writes, and the
// later `render(&count)` borrows a value the handler owns.
func TestRetainedClosureWritesFollowCaptures(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"CapturedClosure": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				fn setup() {
					val mut count = {n: 0}
					val h = fn () { count.n = count.n + 1 }
					onClick(fn () { h() })
					render(&count)
				}
			`,
			want: []string{"8:14-8:19: 'count' is used after the 'handler' that 'onClick' retains took it over for 'static"},
		},
		"CapturedObjectHoldingAClosure": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				fn setup() {
					val mut count = {n: 0}
					val o = {cb: fn () { count.n = count.n + 1 }}
					onClick(fn () { o.cb() })
					render(&count)
				}
			`,
			want: []string{"8:14-8:19: 'count' is used after the 'handler' that 'onClick' retains took it over for 'static"},
		},
		"CapturedClosureThatOnlyReads": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				declare fn log(n: number) -> undefined
				fn setup() {
					val mut count = {n: 0}
					val h = fn () { log(count.n) }
					onClick(fn () { h() })
					render(&count)
				}
			`,
			want: []string{"9:14-9:19: 'count' is used after the 'handler' that 'onClick' retains took it over for 'static"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

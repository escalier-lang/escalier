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
				fn go() -> undefined {
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
// keeps its handler for 'static, so it keeps the inner closure's borrow of count for the rest of
// the program. The later `render(&count)` conflicts with that borrow when the inner closure
// writes count, and checks when it only reads count.
func TestRetainedClosureWritesFollowCaptures(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"CapturedClosure": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				fn setup() -> undefined {
					val mut count = {n: 0}
					val h = fn () { count.n = count.n + 1 }
					onClick(fn () { h() })
					render(&count)
				}
			`,
			want: []string{"8:13-8:19: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
		},
		"CapturedObjectHoldingAClosure": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				fn setup() -> undefined {
					val mut count = {n: 0}
					val o = {cb: fn () { count.n = count.n + 1 }}
					onClick(fn () { o.cb() })
					render(&count)
				}
			`,
			want: []string{"8:13-8:19: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
		},
		"CapturedClosureThatOnlyReadsOk": {
			src: `
				declare fn onClick(handler: 'static fn () -> undefined) -> undefined
				declare fn render(view: &{n: number}) -> undefined
				declare fn log(n: number) -> undefined
				fn setup() -> undefined {
					val mut count = {n: 0}
					val h = fn () { log(count.n) }
					onClick(fn () { h() })
					render(&count)
				}
			`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}

// TestClosureArgWritesMutableSlot covers a closure that a call's argument holds, where the
// call also borrows a place as mutable through a `&mut self` receiver or a `&mut` parameter.
// A closure that can remove a non-value element from an overlapping place is rejected. It can
// when it takes the place as `&mut` and the place's type holds a removable slot of a non-value
// element, such as an `Array<Flat>`, a `Map<string, Flat>` or a declared class. That covers a
// `&mut` borrow, a `&mut self` receiver, a `&mut` argument, and a call of another closure that
// does one of these, including a closure nested in the callback's own body.
//
// The accepted cases cover a closure that assigns the place, one that removes a value-type
// element, one that calls a `&mut` method of a class made of value-type fields, one that only
// reads the place, one that writes a disjoint place, and a call with no `&mut` slot.
func TestClosureArgWritesMutableSlot(t *testing.T) {
	tests := map[string]struct {
		// imports is written before src, on the line src leaves empty, so a case that imports
		// a module keeps the line numbers it would have without the import.
		imports string
		src     string
		want    []string
	}{
		"CompareFnPopsTheReceiver": {
			src: `
				type Flat = {x: number}
				export fn sortPop(mut arr: Array<Flat>, mut stash: Array<Flat>) -> undefined {
					arr.sort(fn (a, b) {
						match arr.pop() {
							p: Flat => { stash.push(p) },
							_ => {},
						}
						return 0
					})
				}
			`,
			want: []string{"4:15-10:7: this call may run the 'compareFn' passed to 'sort', which writes 'arr' while the call borrows it as mutable for 'self'"},
		},
		"CompareFnCallsANestedClosureThatPopsAFlat": {
			src: `
				type Flat = {x: number}
				export fn sortPop(mut arr: Array<Flat>) -> undefined {
					arr.sort(fn (a, b) {
						val pop = fn () { arr.pop() }
						pop()
						return 0
					})
				}
			`,
			want: []string{"4:15-8:7: this call may run the 'compareFn' passed to 'sort', which writes 'arr' while the call borrows it as mutable for 'self'"},
		},
		"CompareFnPassesAFlatReceiverToAMutParam": {
			src: `
				type Flat = {x: number}
				declare fn clear(a: &mut Array<Flat>) -> undefined
				export fn sortClear(arr: &mut Array<Flat>) -> undefined {
					arr.sort(fn (a, b) {
						clear(arr)
						return 0
					})
				}
			`,
			want: []string{"5:15-8:7: this call may run the 'compareFn' passed to 'sort', which writes 'arr' while the call borrows it as mutable for 'self'"},
		},
		"CallbackPopsAFlatArrayFieldOfTheReceiver": {
			src: `
				type Flat = {x: number}
				class Bag {
					items: Array<Flat>,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
				}
				export fn go(mut b: Bag) -> undefined {
					b.run(fn () { b.items.pop() })
				}
			`,
			want: []string{"8:12-8:35: this call may run the 'f' passed to 'run', which writes 'b.items' while the call borrows 'b' as mutable for 'self'"},
		},
		"CallbackBorrowsAFlatArrayFieldAsMutable": {
			src: `
				type Flat = {x: number}
				declare fn shrink(a: &mut Array<Flat>) -> undefined
				declare fn apply(target: &mut {items: Array<Flat>}, f: fn () -> undefined) -> undefined
				export fn go(mut b: {items: Array<Flat>}) -> undefined {
					apply(&mut b, fn () { shrink(&mut b.items) })
				}
			`,
			want: []string{"6:20-6:50: this call may run the 'f' passed to 'apply', which writes 'b.items' while the call borrows 'b' as mutable for 'target'"},
		},
		"CallbackDeletesFromAFlatMap": {
			imports: `import "std:map"`,
			src: `
				type Flat = {x: number}
				class Index {
					entries: map.Map<string, Flat>,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
				}
				export fn go(mut idx: Index) -> undefined {
					idx.run(fn () { idx.entries.delete("a") })
				}
			`,
			want: []string{"8:14-8:47: this call may run the 'f' passed to 'run', which writes 'idx.entries' while the call borrows 'idx' as mutable for 'self'"},
		},
		// Queue's internals are unknown, so take may remove a value it holds.
		"CallbackCallsAMutMethodOfADeclaredClass": {
			src: `
				declare class Queue {
					take(&mut self) -> number,
					run(&mut self, f: fn () -> undefined) -> undefined,
				}
				export fn go(mut q: Queue) -> undefined {
					q.run(fn () { q.take() })
				}
			`,
			want: []string{"7:12-7:30: this call may run the 'f' passed to 'run', which writes 'q' while the call borrows it as mutable for 'self'"},
		},
		"CompareFnReadsTheReceiverOk": {
			src: `
				export fn sortLen(mut arr: Array<number>) -> undefined {
					arr.sort(fn (a, b) { return arr.length })
				}
			`,
			want: nil,
		},
		"CompareFnPopsAnotherArrayOk": {
			src: `
				type Flat = {x: number}
				export fn sortPop(mut arr: Array<Flat>, mut other: Array<Flat>) -> undefined {
					arr.sort(fn (a, b) {
						other.pop()
						return 0
					})
				}
			`,
			want: nil,
		},
		// Popping a number copies it, so the number cannot end up with two owners.
		"CompareFnPopsABorrowedNumberReceiverOk": {
			src: `
				export fn sortPop(arr: &mut Array<number>) -> undefined {
					arr.sort(fn (a, b) {
						arr.pop()
						return 0
					})
				}
			`,
			want: nil,
		},
		"CompareFnCallsAClosureThatPopsANumberOk": {
			src: `
				export fn sortPop(mut arr: Array<number>) -> undefined {
					val pop = fn () { arr.pop() }
					arr.sort(fn (a, b) {
						pop()
						return 0
					})
				}
			`,
			want: nil,
		},
		"CompareFnCallsANestedClosureThatPopsANumberOk": {
			src: `
				export fn sortPop(mut arr: Array<number>) -> undefined {
					arr.sort(fn (a, b) {
						val pop = fn () { arr.pop() }
						pop()
						return 0
					})
				}
			`,
			want: nil,
		},
		"CompareFnPassesANumberReceiverToAMutParamOk": {
			src: `
				declare fn clear(a: &mut Array<number>) -> undefined
				export fn sortClear(arr: &mut Array<number>) -> undefined {
					arr.sort(fn (a, b) {
						clear(arr)
						return 0
					})
				}
			`,
			want: nil,
		},
		"CallbackDeletesFromANumberMapOk": {
			imports: `import "std:map"`,
			src: `
				class Counts {
					entries: map.Map<string, number>,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
				}
				export fn go(mut counts: Counts) -> undefined {
					counts.run(fn () { counts.entries.delete("a") })
				}
			`,
			want: nil,
		},
		"CallbackClearsAStringSetOk": {
			imports: `import "std:set"`,
			src: `
				class Tags {
					names: set.Set<string>,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
				}
				export fn go(mut t: Tags) -> undefined {
					t.run(fn () { t.names.clear() })
				}
			`,
			want: nil,
		},
		// bump writes a number field, and Box holds no removable slot.
		"CallbackCallsAMutMethodThatWritesANumberOk": {
			src: `
				class Box {
					x: number,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
					bump(&mut self) -> undefined { self.x = self.x + 1 },
				}
				export fn go(mut b: Box) -> undefined {
					b.run(fn () { b.bump() })
				}
			`,
			want: nil,
		},
		"CallbackPassesAClassOfValueFieldsToAMutParamOk": {
			src: `
				class Counter {
					n: number,
					label: string,
					run(&mut self, f: fn () -> undefined) -> undefined { f() },
				}
				declare fn reset(c: &mut Counter) -> undefined
				export fn go(mut c: Counter) -> undefined {
					c.run(fn () { reset(&mut c) })
				}
			`,
			want: nil,
		},
		// An assignment drops the old value of 'p.x', so no value ends up with two owners.
		"CallbackAssignsAMutParamOk": {
			src: `
				declare fn apply(target: &mut {x: number}, f: fn () -> undefined) -> undefined
				export fn go() -> undefined {
					val mut p = {x: 1}
					apply(&mut p, fn () { p.x = 2 })
				}
			`,
			want: nil,
		},
		"NamedCallbackAssignsAMutParamOk": {
			src: `
				declare fn apply(target: &mut {x: number}, f: fn () -> undefined) -> undefined
				export fn go() -> undefined {
					val mut p = {x: 1}
					val assign = fn () { p.x = 2 }
					apply(&mut p, assign)
				}
			`,
			want: nil,
		},
		"CallbackAssignsAFieldOfAMutParamOk": {
			src: `
				declare fn apply(target: &mut {a: {x: number}}, f: fn () -> undefined) -> undefined
				export fn go() -> undefined {
					val mut p = {a: {x: 1}}
					apply(&mut p, fn () { p.a.x = 2 })
				}
			`,
			want: nil,
		},
		"CallbackAssignsADisjointFieldOk": {
			src: `
				declare fn apply(target: &mut {x: number}, f: fn () -> undefined) -> undefined
				export fn go() -> undefined {
					val mut p = {a: {x: 1}, b: {x: 1}}
					apply(&mut p.a, fn () { p.b.x = 2 })
				}
			`,
			want: nil,
		},
		"CallbackPopsADisjointFieldOk": {
			src: `
				type Flat = {x: number}
				declare fn apply(target: &mut Array<Flat>, f: fn () -> undefined) -> undefined
				export fn go(mut b: {a: Array<Flat>, b: Array<Flat>}) -> undefined {
					apply(&mut b.a, fn () { b.b.pop() })
				}
			`,
			want: nil,
		},
		"CallbackReadsTheBorrowedFieldAndWritesAnotherOk": {
			src: `
				declare fn apply(target: &mut {x: number}, f: fn () -> undefined) -> undefined
				export fn go() -> undefined {
					val mut p = {a: {x: 1}, b: {x: 1}}
					apply(&mut p.a, fn () {
						val y = p.a.x
						p.b.x = y
					})
				}
			`,
			want: nil,
		},
		"CallbackWithoutAMutSlotOk": {
			src: `
				type Flat = {x: number}
				declare fn run(f: fn () -> undefined) -> undefined
				export fn go(mut arr: Array<Flat>) -> undefined {
					run(fn () { arr.pop() })
				}
			`,
			want: nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, msgs := inferAgainstTreeWithSpans(t, tc.imports+tc.src)
			require.Equal(t, tc.want, msgs)
		})
	}
}

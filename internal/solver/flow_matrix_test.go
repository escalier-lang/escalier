package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// flowCell is one cell of the flow matrix in planning/ownership_rules/requirements.md. It crosses
// one flow site that F1 lists with one column of the matrix. The column fixes the kind of source
// and destination, and the site fixes the operation that passes the value between them.
type flowCell struct {
	column string
	site   string
	// src is the Escalier program for the cell.
	src string
	// want lists every diagnostic the program reports, each with its span. It is empty when the
	// program checks clean.
	want []string
	// notApplicable says why no program can express the cell. A cell with a reason has no src.
	notApplicable string
}

// flowReceiverClass declares the class every receiver cell calls a method on. Its methods take
// `self` as `&`, `&mut`, and owned, one for each destination a receiver can have.
const flowReceiverClass = `
	class C {
		x: number,
		get(&self) -> number { return self.x },
		set(&mut self) -> undefined { self.x = 1 },
		take(self) -> number { return self.x },
	}`

// returnEndsFunction is the reason a flow-matrix cell does not apply.
const returnEndsFunction = "A `return` leaves the function, so nothing after it can use the source."

// runFlowMatrix checks each cell's program against the standard library and asserts the full
// list of diagnostics. A cell that does not apply is skipped with its reason.
func runFlowMatrix(t *testing.T, cells []flowCell) {
	for _, c := range cells {
		t.Run(c.column+"/"+c.site, func(t *testing.T) {
			if c.notApplicable != "" {
				require.Empty(t, c.src)
				require.Empty(t, c.want)
				t.Skip(c.notApplicable)
			}
			_, errs := inferAgainstTreeWithSpans(t, c.src)
			require.Equal(t, c.want, errs)
		})
	}
}

// TestFlowMatrix covers every column of the flow
// matrix in planning/ownership_rules/requirements.md, crossed with every flow site that F1 lists.
// Each column states its source, its destination, and what the program does after the flow. The
// cells the current solver gets wrong sit in the disabled tests after this one, grouped by the
// issue that enables them.
//
// An error the solver reports today may blame the source's annotation instead of the source
// expression. Each cell asserts the span the solver reports today.
func TestFlowMatrix(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// A value type copies into an owned destination, and the source stays usable. Rule F2.
		{
			column: "Copy",
			site:   "Binding",
			src: `
				fn g() -> number {
					val n = 1
					val d: number = n
					return d + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					val n = 1
					var d: number = 0
					d = n
					return d + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: number}) -> number {
					val n = 1
					o.f = n
					return o.f + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<number>) -> number {
					val n = 1
					a[0] = n
					return a[0] + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val n = 1
					val o = {f: n}
					return o.f + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val c = {f: 1}
					val {f: d} = c
					return d + c.f
				}
			`,
		},
		{
			column: "Copy",
			site:   "Argument",
			src: `
				declare fn take(d: number) -> undefined
				fn g() -> number {
					val n = 1
					take(n)
					return n
				}
			`,
		},
		{
			column: "Copy",
			site:   "Receiver",
			src: `
				fn g() -> number {
					val n = 1.5
					val s = n.toFixed(0)
					return n
				}
			`,
		},
		{
			column: "Copy",
			site:   "Return",
			src: `
				fn g() -> number {
					val n = 1
					return n
				}
			`,
		},
		{
			column: "Copy",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: number
				fn g() -> number {
					val n = 1
					sink = n
					return sink + n
				}
			`,
		},
		{
			column: "Copy",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val n = 1
					val f = fn () { return n }
					return f() + n
				}
			`,
		},
		// A `&T` copies into a `&T` destination, and the source stays usable. Rule F2.
		{
			column: "ImmutableCopy",
			site:   "Binding",
			src: `
				fn g(r: &{x: number}) -> number {
					val d: &{x: number} = r
					return d.x + r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}, r: &{x: number}) -> number {
					var d: &{x: number} = init
					d = r
					return d.x + r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}, r: &{x: number}) -> number {
					o.f = r
					return o.f.x + r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>, r: &{x: number}) -> number {
					a[0] = r
					return a[0].x + r.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "ImmutableCopy",
			site:   "LiteralElement",
			src: `
				fn g(r: &{x: number}) -> number {
					val o: {f: &{x: number}} = {f: r}
					return o.f.x + r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &{x: number}}) -> number {
					val {f: d}: {f: &{x: number}} = c
					return d.x + c.f.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "Argument",
			src: `
				declare fn take(d: &{x: number}) -> undefined
				fn g(r: &{x: number}) -> number {
					take(r)
					return r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(r: &C) -> number {
					return r.get() + r.x
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "Return",
			src: `
				fn g(r: &{x: number}) -> &{x: number} {
					return r
				}
			`,
		},
		{
			column: "ImmutableCopy",
			site:   "ClosureCapture",
			src: `
				fn g(r: &{x: number}) -> number {
					val f = fn () { return r.x }
					return f() + r.x
				}
			`,
		},
		// A `&mut T` copies into a `&mut T` destination, and both can write. Rules F2 and E2.
		{
			column: "MutableCopy",
			site:   "Binding",
			src: `
				fn g(m: &mut {x: number}) {
					val d: &mut {x: number} = m
					d.x = 1
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}, m: &mut {x: number}) {
					var d: &mut {x: number} = init
					d = m
					d.x = 1
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}, m: &mut {x: number}) {
					o.f = m
					o.f.x = 1
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&mut {x: number}>, m: &mut {x: number}) {
					a[0] = m
					a[0].x = 1
					m.x = 2
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "MutableCopy",
			site:   "LiteralElement",
			src: `
				fn g(m: &mut {x: number}) {
					val o: {f: &mut {x: number}} = {f: m}
					o.f.x = 1
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &mut {x: number}}) {
					val {f: d}: {f: &mut {x: number}} = c
					d.x = 1
					c.f.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "Argument",
			src: `
				declare fn take(d: &mut {x: number}) -> undefined
				fn g(m: &mut {x: number}) {
					take(m)
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(m: &mut C) {
					m.set()
					m.x = 2
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "Return",
			src: `
				fn g(m: &mut {x: number}) -> &mut {x: number} {
					m.x = 1
					return m
				}
			`,
		},
		{
			column: "MutableCopy",
			site:   "ClosureCapture",
			src: `
				fn g(m: &mut {x: number}) {
					val f = fn () { m.x = 1 }
					f()
					m.x = 2
				}
			`,
		},
		// A `&mut T` copies into a `&T` destination, narrowed to `&`, and both stay usable. Rules F2 and
		// T4.
		{
			column: "Narrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}, m: &mut {x: number}) -> number {
					o.f = m
					return o.f.x + m.x
				}
			`,
		},
		{
			column: "Narrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>, m: &mut {x: number}) -> number {
					a[0] = m
					return a[0].x + m.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "Narrow",
			site:   "LiteralElement",
			src: `
				fn g(m: &mut {x: number}) -> number {
					val o: {f: &{x: number}} = {f: m}
					return o.f.x + m.x
				}
			`,
		},
		{
			column: "Narrow",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &mut {x: number}}) -> number {
					val {f: d}: {f: &{x: number}} = c
					return d.x + c.f.x
				}
			`,
		},
		{
			column: "Narrow",
			site:   "Argument",
			src: `
				declare fn take(d: &{x: number}) -> undefined
				fn g(m: &mut {x: number}) -> number {
					take(m)
					return m.x
				}
			`,
		},
		{
			column: "Narrow",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(m: &mut C) -> number {
					return m.get() + m.x
				}
			`,
		},
		{
			column: "Narrow",
			site:   "Return",
			src: `
				fn g(m: &mut {x: number}) -> &{x: number} {
					return m
				}
			`,
		},
		{
			column: "Narrow",
			site:   "ClosureCapture",
			src: `
				fn g(m: &mut {x: number}) -> number {
					val f = fn () { return m.x }
					return f() + m.x
				}
			`,
		},
		// A `&T` cannot fill a `&mut T` destination. Rule T4.
		{
			column: "Widen",
			site:   "Binding",
			src: `
				fn g(r: &{x: number}) {
					val d: &mut {x: number} = r
				}
			`,
			want: []string{"2:13-2:25: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}, r: &{x: number}) {
					var d: &mut {x: number} = init
					d = r
				}
			`,
			want: []string{"2:37-2:49: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}, r: &{x: number}) {
					o.f = r
				}
			`,
			want: []string{"2:44-2:56: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&mut {x: number}>, r: &{x: number}) {
					a[0] = r
				}
			`,
			want: []string{"2:46-2:58: cannot constrain immutable object <: mutable object"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "Widen",
			site:   "LiteralElement",
			src: `
				fn g(r: &{x: number}) {
					val o: {f: &mut {x: number}} = {f: r}
				}
			`,
			want: []string{"2:13-2:25: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &{x: number}}) {
					val {f: d}: {f: &mut {x: number}} = c
				}
			`,
			want: []string{"2:17-2:29: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "Argument",
			src: `
				declare fn take(d: &mut {x: number}) -> undefined
				fn g(r: &{x: number}) {
					take(r)
				}
			`,
			want: []string{"3:13-3:25: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(r: &C) {
					r.set()
				}
			`,
			want: []string{"9:6-9:11: cannot constrain immutable C <: mutable C"},
		},
		{
			column: "Widen",
			site:   "Return",
			src: `
				fn g(r: &{x: number}) -> &mut {x: number} {
					return r
				}
			`,
			want: []string{"2:13-2:25: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "Widen",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g(r: &{x: number}) {
					sink = r
				}
			`,
			want: []string{"4:6-4:14: cannot constrain immutable object <: mutable object"},
		},
		// The closure writes through `r`, so its capture needs the `&mut` that `r` cannot give.
		{
			column: "Widen",
			site:   "ClosureCapture",
			src: `
				fn g(r: &{x: number}) {
					val f = fn () { r.x = 1 }
					f()
				}
			`,
			want: []string{"2:13-2:25: cannot constrain immutable object <: mutable object"},
		},
		// A written `&p` borrows an owned value into a `&T` destination, and the source stays usable.
		// Rules F2 and F4.
		{
			column: "Borrow",
			site:   "Binding",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val d: &{x: number} = &p
					return d.x + p.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) -> number {
					val p = {x: 1}
					var d: &{x: number} = init
					d = &p
					return d.x + p.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) -> number {
					val p = {x: 1}
					o.f = &p
					return o.f.x + p.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "Borrow",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val o: {f: &{x: number}} = {f: &p}
					return o.f.x + p.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val p = {f: {x: 1}}
					val {f: d} = &p
					return d.x + p.f.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "Argument",
			src: `
				declare fn take(d: &{x: number}) -> undefined
				fn g() -> number {
					val p = {x: 1}
					take(&p)
					return p.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val p = C(1)
					return (&p).get() + p.x
				}
			`,
		},
		{
			column: "Borrow",
			site:   "Return",
			src: `
				fn g() -> &{x: number} {
					val p = {x: 1}
					return &p
				}
			`,
		},
		{
			column: "Borrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g() -> number {
					val p = {x: 1}
					sink = &p
					return sink.x + p.x
				}
			`,
		},
		// A capture is never written with `&`. The closure borrows `p` in the mode its body needs.
		{
			column: "Borrow",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val f = fn () { return p.x }
					return f() + p.x
				}
			`,
		},
		// A written `&mut p` borrows a value owned by a `val mut` binding into a `&mut T` destination,
		// which can write. Rules F2 and T7.
		{
			column: "MutableBorrow",
			site:   "Binding",
			src: `
				fn g() {
					val mut p = {x: 1}
					val d: &mut {x: number} = &mut p
					d.x = 2
				}
			`,
		},
		{
			column: "MutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}) {
					val mut p = {x: 1}
					var d: &mut {x: number} = init
					d = &mut p
					d.x = 2
				}
			`,
		},
		{
			column: "MutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}) {
					val mut p = {x: 1}
					o.f = &mut p
					o.f.x = 2
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "MutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() {
					val mut p = {x: 1}
					val o: {f: &mut {x: number}} = {f: &mut p}
					o.f.x = 2
				}
			`,
		},
		{
			column: "MutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() {
					val mut p = {f: {x: 1}}
					val {f: d} = &mut p
					d.x = 2
				}
			`,
		},
		{
			column: "MutableBorrow",
			site:   "Argument",
			src: `
				declare fn take(d: &mut {x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					take(&mut p)
				}
			`,
		},
		{
			column: "MutableBorrow",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() {
					val mut p = C(1)
					val r = (&mut p).set()
				}
			`,
		},
		// A capture is never written with `&`. The closure borrows `p` in the mode its body needs.
		{
			column: "MutableBorrow",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					f()
				}
			`,
		},
		// A written `&mut p` of a value owned by a `val` binding is rejected. Rule T7.
		{
			column: "MutableBorrowThroughVal",
			site:   "Binding",
			src: `
				fn g() {
					val p: {x: number} = {x: 1}
					val d: &mut {x: number} = &mut p
				}
			`,
			want: []string{"4:32-4:38: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}) {
					val p: {x: number} = {x: 1}
					var d: &mut {x: number} = init
					d = &mut p
				}
			`,
			want: []string{"5:10-5:16: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}) {
					val p: {x: number} = {x: 1}
					o.f = &mut p
				}
			`,
			want: []string{"4:12-4:18: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "MutableBorrowThroughVal",
			site:   "LiteralElement",
			src: `
				fn g() {
					val p: {x: number} = {x: 1}
					val o: {f: &mut {x: number}} = {f: &mut p}
				}
			`,
			want: []string{"4:41-4:47: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "Destructuring",
			src: `
				fn g() {
					val p: {f: {x: number}} = {f: {x: 1}}
					val {f: d} = &mut p
				}
			`,
			want: []string{"4:19-4:25: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "Argument",
			src: `
				declare fn take(d: &mut {x: number}) -> undefined
				fn g() {
					val p: {x: number} = {x: 1}
					take(&mut p)
				}
			`,
			want: []string{"5:11-5:17: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() {
					val p = C(1)
					val r = (&mut p).set()
				}
			`,
			want: []string{"10:15-10:21: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// A capture is never written with `&`. The closure borrows `p` in the mode its body needs. A
		// `val` binding owns `p`, so the closure cannot write it.
		{
			column: "MutableBorrowThroughVal",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val p: {x: number} = {x: 1}
					val f = fn () { p.x = 2 }
					f()
				}
			`,
			want: []string{"4:22-4:29: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// An owned value written without `&` cannot fill a borrow destination. Rule F4. A receiver, a
		// pattern over a borrow, and a closure capture take their borrow without a written `&`, so those
		// cells check clean.
		{
			column: "UnwrittenBorrow",
			site:   "Destructuring",
			src: `
				fn g(r: &{f: {x: number}}) -> number {
					val {f: d} = r
					return d.x
				}
			`,
		},
		{
			column: "UnwrittenBorrow",
			site:   "Argument",
			src: `
				declare fn take(d: &{x: number}) -> undefined
				fn g() {
					val p = {x: 1}
					take(p)
				}
			`,
			want: []string{"5:11-5:12: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val p = C(1)
					return p.get() + p.x
				}
			`,
		},
		{
			column: "UnwrittenBorrow",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val f = fn () { return p.x }
					return f() + p.x
				}
			`,
		},
		// A field read through a borrow that yields an object, not a value type, is rejected at any
		// destination when it is written without `&`. Rules F4 and T6. A receiver and a pattern over a
		// borrow are exempt, so those cells check clean.
		{
			column: "UnwrittenFieldBorrow",
			site:   "Destructuring",
			src: `
				fn g(h: &{f: {g: {x: number}}}) -> number {
					val {g: d} = h.f
					return d.x
				}
			`,
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(h: &{f: C}) -> number {
					return h.f.get()
				}
			`,
		},
		// A capture borrows without a written `&`, like a receiver, so the closure reads `h.f` through
		// its copy of `h`.
		{
			column: "UnwrittenFieldBorrow",
			site:   "ClosureCapture",
			src: `
				fn g(h: &{f: {x: number}}) -> number {
					val f = fn () { return h.f.x }
					return f()
				}
			`,
		},
		// A `&T` cannot fill an owned destination. Rule T6.
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Binding",
			src: `
				fn g(r: &{x: number}) {
					val d: {x: number} = r
				}
			`,
			want: []string{"2:13-2:25: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Reassignment",
			src: `
				fn g(r: &{x: number}) {
					var d: {x: number} = {x: 0}
					d = r
				}
			`,
			want: []string{"2:13-2:25: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}, r: &{x: number}) {
					o.f = r
				}
			`,
			want: []string{"2:39-2:51: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>, r: &{x: number}) {
					a[0] = r
				}
			`,
			want: []string{"2:41-2:53: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &{x: number}}) {
					val {f: d}: {f: {x: number}} = c
				}
			`,
			want: []string{"2:17-2:29: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g(r: &{x: number}) {
					take(r)
				}
			`,
			want: []string{"3:13-3:25: cannot use borrowed &object as owned object"},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(r: &C) -> number {
					return r.take()
				}
			`,
			want: []string{"9:13-9:19: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "Return",
			src: `
				fn g(r: &{x: number}) -> {x: number} {
					return r
				}
			`,
			want: []string{"2:13-2:25: cannot use borrowed &object as owned object"},
		},
		// A capture of a borrow copies the borrow, so a returned closure holds `r` and gives T6 no owned
		// destination to reject.
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "ClosureCapture",
			src: `
				fn g(r: &{x: number}) {
					return fn () { return r.x }
				}
			`,
		},
		// A `&mut T` cannot fill an owned destination. Rule T6.
		{
			column: "MutableBorrowIntoOwned",
			site:   "Binding",
			src: `
				fn g(m: &mut {x: number}) {
					val d: {x: number} = m
				}
			`,
			want: []string{"2:13-2:29: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "Reassignment",
			src: `
				fn g(m: &mut {x: number}) {
					var d: {x: number} = {x: 0}
					d = m
				}
			`,
			want: []string{"2:13-2:29: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}, m: &mut {x: number}) {
					o.f = m
				}
			`,
			want: []string{"2:39-2:55: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>, m: &mut {x: number}) {
					a[0] = m
				}
			`,
			want: []string{"2:41-2:57: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "Destructuring",
			src: `
				fn g(c: {f: &mut {x: number}}) {
					val {f: d}: {f: {x: number}} = c
				}
			`,
			want: []string{"2:17-2:33: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g(m: &mut {x: number}) {
					take(m)
				}
			`,
			want: []string{"3:13-3:29: cannot use borrowed &mut object as owned object"},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(m: &mut C) -> number {
					return m.take()
				}
			`,
			want: []string{"9:13-9:19: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			column: "MutableBorrowIntoOwned",
			site:   "Return",
			src: `
				fn g(m: &mut {x: number}) -> {x: number} {
					return m
				}
			`,
			want: []string{"2:13-2:29: cannot use borrowed &mut object as owned object"},
		},
		// A capture of a borrow copies the borrow, so a returned closure holds `m` and gives T6 no owned
		// destination to reject.
		{
			column: "MutableBorrowIntoOwned",
			site:   "ClosureCapture",
			src: `
				fn g(m: &mut {x: number}) {
					return fn () { m.x = 1 }
				}
			`,
		},
		// An owned value moves into an owned destination, so a later use of the source is rejected. Rules
		// F2 and E1.
		{
			column: "Move",
			site:   "Binding",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val d = p
					return p.x
				}
			`,
			want: []string{"5:13-5:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					val p = {x: 1}
					var d: {x: number} = {x: 0}
					d = p
					return p.x
				}
			`,
			want: []string{"6:13-6:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) -> number {
					val p = {x: 1}
					o.f = p
					return p.x
				}
			`,
			want: []string{"5:13-5:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>) -> number {
					val p = {x: 1}
					a[0] = p
					return p.x
				}
			`,
			want: []string{"5:13-5:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val o = {f: p}
					return p.x
				}
			`,
			want: []string{"5:13-5:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val p = {f: {x: 1}}
					val {f: d} = p
					return p.f.x
				}
			`,
			want: []string{"5:13-5:18: use of moved value 'p.f'"},
		},
		{
			column: "Move",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() -> number {
					val p = {x: 1}
					take(p)
					return p.x
				}
			`,
			want: []string{"6:13-6:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val p = C(1)
					val n = p.take()
					return n + p.x
				}
			`,
			want: []string{"11:17-11:20: use of moved value 'p'"},
		},
		{
			column:        "Move",
			site:          "Return",
			notApplicable: returnEndsFunction,
		},
		{
			column: "Move",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() -> number {
					val p = {x: 1}
					sink = p
					return p.x
				}
			`,
			want: []string{"6:13-6:16: use of moved value 'p'"},
		},
		{
			column: "Move",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val f = fn () {
						val q = p
						return q.x
					}
					val n = f()
					return n + p.x
				}
			`,
			want: []string{"9:17-9:20: use of moved value 'p'"},
		},
		// A move in one branch of an `if` leaves the source moved after the branches join. Rule E1.
		{
			column: "MoveOnOneBranch",
			site:   "Binding",
			src: `
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						val d = p
					}
					return p.x
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "Reassignment",
			src: `
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						var d: {x: number} = {x: 0}
						d = p
					}
					return p.x
				}
			`,
			want: []string{"8:13-8:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}, c: boolean) -> number {
					val p = {x: 1}
					if c {
						o.f = p
					}
					return p.x
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>, c: boolean) -> number {
					val p = {x: 1}
					if c {
						a[0] = p
					}
					return p.x
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "LiteralElement",
			src: `
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						val o = {f: p}
					}
					return p.x
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "Destructuring",
			src: `
				fn g(c: boolean) -> number {
					val p = {f: {x: 1}}
					if c {
						val {f: d} = p
					}
					return p.f.x
				}
			`,
			want: []string{"7:13-7:18: use of moved value 'p.f'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						take(p)
					}
					return p.x
				}
			`,
			want: []string{"8:13-8:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g(c: boolean) -> number {
					val p = C(1)
					if c {
						val n = p.take()
					}
					return p.x
				}
			`,
			want: []string{"13:13-13:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "Return",
			src: `
				fn g(c: boolean) -> {x: number} {
					val p = {x: 1}
					if c {
						return p
					}
					return p
				}
			`,
		},
		{
			column: "MoveOnOneBranch",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						sink = p
					}
					return p.x
				}
			`,
			want: []string{"8:13-8:16: use of moved value 'p'"},
		},
		{
			column: "MoveOnOneBranch",
			site:   "ClosureCapture",
			src: `
				fn g(c: boolean) -> number {
					val p = {x: 1}
					if c {
						val f = fn () {
							val q = p
							return q.x
						}
						val n = f()
					}
					return p.x
				}
			`,
			want: []string{"11:13-11:16: use of moved value 'p'"},
		},
		// A move inside a loop body is rejected, since the next iteration uses the moved source. Rule E1.
		{
			column: "MoveInALoop",
			site:   "Binding",
			src: `
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						val d = p
					}
				}
			`,
			want: []string{"5:15-5:16: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "Reassignment",
			src: `
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						var d: {x: number} = {x: 0}
						d = p
					}
				}
			`,
			want: []string{"6:11-6:12: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) {
					val p = {x: 1}
					for i in [1, 2] {
						o.f = p
					}
				}
			`,
			want: []string{"5:13-5:14: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>) {
					val p = {x: 1}
					for i in [1, 2] {
						a[0] = p
					}
				}
			`,
			want: []string{"5:14-5:15: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "LiteralElement",
			src: `
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						val o = {f: p}
					}
				}
			`,
			want: []string{"5:19-5:20: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "Destructuring",
			src: `
				fn g() {
					val p = {f: {x: 1}}
					for i in [1, 2] {
						val {f: d} = p
					}
				}
			`,
			want: []string{"5:20-5:21: use of moved value 'p.f'"},
		},
		{
			column: "MoveInALoop",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						take(p)
					}
				}
			`,
			want: []string{"6:12-6:13: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() {
					val p = C(1)
					for i in [1, 2] {
						val n = p.take()
					}
				}
			`,
			want: []string{"11:15-11:16: use of moved value 'p'"},
		},
		{
			column: "MoveInALoop",
			site:   "Return",
			src: `
				fn g() -> {x: number} {
					val p = {x: 1}
					for i in [1, 2] {
						return p
					}
					return p
				}
			`,
		},
		{
			column: "MoveInALoop",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						sink = p
					}
				}
			`,
			want: []string{"6:14-6:15: use of moved value 'p'"},
		},
		// Reassigning a moved `var` gives it a new value, so a use after the reassignment is accepted.
		// Rule E1.
		{
			column:        "ReassignAfterAMove",
			site:          "Return",
			notApplicable: returnEndsFunction,
		},
		// Moving an owned field moves part of its owner, so a later use of the whole owner is rejected.
		// Rule E1.
		{
			column: "PartialMove",
			site:   "Binding",
			src: `
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					val d = p.a
					val e = p
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "Reassignment",
			src: `
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					var d: {x: number} = {x: 0}
					d = p.a
					val e = p
				}
			`,
			want: []string{"6:14-6:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) {
					val p = {a: {x: 1}, b: {x: 2}}
					o.f = p.a
					val e = p
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>) {
					val p = {a: {x: 1}, b: {x: 2}}
					a[0] = p.a
					val e = p
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "LiteralElement",
			src: `
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					val o = {f: p.a}
					val e = p
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "Destructuring",
			src: `
				fn g() {
					val p = {a: {f: {x: 1}}, b: {x: 2}}
					val {f: d} = p.a
					val e = p
				}
			`,
			want: []string{"5:14-5:15: use of partially moved value 'p'; field 'p.a.f' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					take(p.a)
					val e = p
				}
			`,
			want: []string{"6:14-6:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column: "PartialMove",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() {
					val p = {a: C(1), b: C(2)}
					val n = p.a.take()
					val e = p
				}
			`,
			want: []string{"11:14-11:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		{
			column:        "PartialMove",
			site:          "Return",
			notApplicable: returnEndsFunction,
		},
		{
			column: "PartialMove",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					sink = p.a
					val e = p
				}
			`,
			want: []string{"6:14-6:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		// Moving one owned field leaves its sibling fields usable. Rule E1.
		{
			column: "DisjointField",
			site:   "Binding",
			src: `
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					val d = p.a
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					var d: {x: number} = {x: 0}
					d = p.a
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					o.f = p.a
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>) -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					a[0] = p.a
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					val o = {f: p.a}
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val p = {a: {f: {x: 1}}, b: {x: 2}}
					val {f: d} = p.a
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					take(p.a)
					return p.b.x
				}
			`,
		},
		{
			column: "DisjointField",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val p = {a: C(1), b: C(2)}
					val n = p.a.take()
					return n + p.b.x
				}
			`,
		},
		{
			column:        "DisjointField",
			site:          "Return",
			notApplicable: returnEndsFunction,
		},
		{
			column: "DisjointField",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					sink = p.a
					return p.b.x
				}
			`,
		},
		// A function cannot move a module-level binding, since another call may still read it. Rule E1.
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Binding",
			src: `
				val cfg = {x: 1}
				fn g() {
					val d = cfg
				}
			`,
			want: []string{"4:14-4:17: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Reassignment",
			src: `
				val cfg = {x: 1}
				fn g() {
					var d: {x: number} = {x: 0}
					d = cfg
				}
			`,
			want: []string{"5:10-5:13: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "FieldStore",
			src: `
				val cfg = {x: 1}
				fn g(o: &mut {f: {x: number}}) {
					o.f = cfg
				}
			`,
			want: []string{"4:12-4:15: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "ElementStore",
			src: `
				val cfg = {x: 1}
				fn g(a: &mut Array<{x: number}>) {
					a[0] = cfg
				}
			`,
			want: []string{"4:13-4:16: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "LiteralElement",
			src: `
				val cfg = {x: 1}
				fn g() {
					val o = {f: cfg}
				}
			`,
			want: []string{"4:18-4:21: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Destructuring",
			src: `
				val cfg = {f: {x: 1}}
				fn g() {
					val {f: d} = cfg
				}
			`,
			want: []string{"4:19-4:22: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Return",
			src: `
				val cfg = {x: 1}
				fn g() -> {x: number} {
					return cfg
				}
			`,
			want: []string{"4:13-4:16: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "ModuleLevelStore",
			src: `
				val cfg = {x: 1}
				declare var sink: {x: number}
				fn g() {
					sink = cfg
				}
			`,
			want: []string{"5:13-5:16: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			column: "MoveOutOfModuleLevelState",
			site:   "ClosureCapture",
			src: `
				val cfg = {x: 1}
				fn g() {
					val f = fn () {
						val q = cfg
						return q.x
					}
				}
			`,
			want: []string{"5:15-5:18: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		// A borrow may outlive the binding that owns its source, since the source lives as long as
		// anything can reach it. Rule L1.
		{
			column: "OutliveOwner",
			site:   "Binding",
			src: `
				fn g() -> number {
					val d: &{x: number} = do {
						val p = {x: 1}
						&p
					}
					return d.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}, c: boolean) -> number {
					var d: &{x: number} = init
					if c {
						val p = {x: 1}
						d = &p
					}
					return d.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}, c: boolean) -> number {
					if c {
						val p = {x: 1}
						o.f = &p
					}
					return o.f.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "OutliveOwner",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val o: {f: &{x: number}} = do {
						val p = {x: 1}
						val inner: {f: &{x: number}} = {f: &p}
						inner
					}
					return o.f.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val {f: d} = do {
						val p = {f: {x: 1}}
						&p
					}
					return d.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "Argument",
			src: `
				declare fn keep(d: &'static {x: number}) -> undefined
				fn g() {
					val p = {x: 1}
					keep(&p)
				}
			`,
		},
		// The method returns a borrow of its receiver, which outlives the block that owns `p`.
		{
			column: "OutliveOwner",
			site:   "Receiver",
			src: `
				class M {
					x: number,
					me(&self) -> &M { return self },
				}
				fn g() -> number {
					val d = do {
						val p = M(1)
						p.me()
					}
					return d.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "Return",
			src: `
				fn make() -> &{x: number} {
					val p = {x: 1}
					return &p
				}
				fn g() -> number {
					val d = make()
					return d.x
				}
			`,
		},
		{
			column: "OutliveOwner",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g(c: boolean) -> number {
					if c {
						val p = {x: 1}
						sink = &p
					}
					return sink.x
				}
			`,
		},
		// The closure copies the borrow `r`, so it reads `p` after the block that owns `p` ends.
		{
			column: "OutliveOwner",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val f = do {
						val p = {x: 1}
						val r = &p
						val h = fn () { return r.x }
						h
					}
					return f()
				}
			`,
		},
		// A write to a source while an immutable borrow of it is live is rejected. Rule E2. A closure
		// writes its capture during each call, by E4.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Binding",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val d: &{x: number} = &p
					p.x = 2
					return d.x
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) -> number {
					val mut p = {x: 1}
					var d: &{x: number} = init
					d = &p
					p.x = 2
					return d.x
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteDuringImmutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) -> number {
					val mut p = {x: 1}
					o.f = &p
					p.x = 2
					return o.f.x
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					readWrite(&p, &mut p)
				}
			`,
			want: []string{"7:20-7:26: cannot borrow 'p' as mutable while it is borrowed as immutable"},
		},
		{
			column: "WriteDuringImmutableBorrow",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val r = &p
					val f = fn () { p.x = 2 }
					f()
					return r.x
				}
			`,
			want: []string{"6:6-6:7: this call runs 'f', which writes 'p' while it is borrowed as immutable"},
		},
		// A write that ends before an immutable borrow begins is accepted. Rule E2.
		{
			column: "SequentialPhases",
			site:   "Binding",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					p.x = 2
					val d: &{x: number} = &p
					return d.x
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) -> number {
					val mut p = {x: 1}
					p.x = 2
					var d: &{x: number} = init
					d = &p
					return d.x
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) -> number {
					val mut p = {x: 1}
					p.x = 2
					o.f = &p
					return o.f.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "SequentialPhases",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					p.x = 2
					val o: {f: &{x: number}} = {f: &p}
					return o.f.x
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val mut p = {f: {x: 1}}
					p.f.x = 2
					val {f: d} = &p
					return d.x
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				declare fn read(a: &{x: number}) -> undefined
				declare fn write(a: &mut {x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					write(&mut p)
					read(&p)
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val mut p = C(1)
					p.set()
					return p.get()
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "Return",
			src: `
				fn g() -> &{x: number} {
					val mut p = {x: 1}
					p.x = 2
					return &p
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g() -> number {
					val mut p = {x: 1}
					p.x = 2
					sink = &p
					return sink.x
				}
			`,
		},
		{
			column: "SequentialPhases",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					p.x = 2
					val f = fn () { return p.x }
					return f()
				}
			`,
		},
		// A write to a source while a mutable borrow of it is live is accepted, since every holder
		// already expects the value to change. Rule E2.
		{
			column: "WriteDuringMutableBorrow",
			site:   "Binding",
			src: `
				fn g() {
					val mut p = {x: 1}
					val d: &mut {x: number} = &mut p
					p.x = 2
					d.x = 3
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}) {
					val mut p = {x: 1}
					var d: &mut {x: number} = init
					d = &mut p
					p.x = 2
					d.x = 3
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}) {
					val mut p = {x: 1}
					o.f = &mut p
					p.x = 2
					o.f.x = 3
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "WriteDuringMutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() {
					val mut p = {x: 1}
					val o: {f: &mut {x: number}} = {f: &mut p}
					p.x = 2
					o.f.x = 3
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() {
					val mut p = {f: {x: 1}}
					val {f: d} = &mut p
					p.f.x = 2
					d.x = 3
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					writeWrite(&mut p, &mut p)
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "Receiver",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
					writeWith(&mut self, w: &mut R) -> undefined {},
					writeRead(&mut self, r: &R) -> undefined {},
					take(self) -> number { return self.x },
				}
				fn g() {
					val mut p = R(1)
					p.writeWith(&mut p)
				}
			`,
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live.
		{
			column: "WriteDuringMutableBorrow",
			site:   "Return",
			src: `
				fn pickMut(p: &mut {x: number}) -> &mut {x: number} {
					return p
				}
				fn g() {
					val mut p = {x: 1}
					val d = pickMut(&mut p)
					p.x = 2
					d.x = 3
				}
			`,
		},
		{
			column: "WriteDuringMutableBorrow",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val mut p = {x: 1}
					val f = fn () { p.x = 1 }
					f()
					p.x = 2
					f()
				}
			`,
		},
		// An immutable borrow of a source while a mutable borrow of it is live is rejected. Rule E2.
		// During a call of a closure that writes its capture, the capture counts as a mutable borrow, by
		// E4.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Binding",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val d: &mut {x: number} = &mut p
					val r = &p
					d.x = 2
					return r.x
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &mut {x: number}) -> number {
					val mut p = {x: 1}
					var d: &mut {x: number} = init
					d = &mut p
					val r = &p
					d.x = 2
					return r.x
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					writeRead(&mut p, &p)
				}
			`,
			want: []string{"7:24-7:26: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					val r = &p
					f()
					return r.x
				}
			`,
			want: []string{"6:6-6:7: this call runs 'f', which writes 'p' while it is borrowed as immutable"},
		},
		// A `'static` immutable borrow stays live for the rest of the program, so a later write to its
		// source is rejected. Rules L1 and E2.
		// E4 counts a capture as used only during a call that may run its closure. The write runs no
		// call, so it is accepted even though `sink` keeps the closure.
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "ClosureCapture",
			src: `
				var sink: fn () -> number = fn () { return 0 }
				fn g() {
					val mut p = {x: 1}
					sink = fn () { return p.x }
					p.x = 2
				}
			`,
		},
		// A `'static` mutable borrow stays live for the rest of the program, so a later immutable borrow
		// of its source is rejected. Rules L1 and E2.
		// E4 counts a capture as used only during a call that may run its closure. The call that takes
		// `&p` may run `sink`, which writes `p`, so the call conflicts.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "ClosureCapture",
			src: `
				var sink: fn () -> undefined = fn () {}
				declare fn readIt(a: &{x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					readIt(&p)
				}
			`,
			want: []string{"7:6-7:16: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// Borrows of different fields reach different values, so a write to one field while another is
		// borrowed is accepted. Rule E2.
		{
			column: "DisjointBorrows",
			site:   "Binding",
			src: `
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val d: &{x: number} = &p.a
					p.b.x = 3
					return d.x
				}
			`,
		},
		{
			column: "DisjointBorrows",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					var d: &{x: number} = init
					d = &p.a
					p.b.x = 3
					return d.x
				}
			`,
		},
		{
			column: "DisjointBorrows",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					o.f = &p.a
					p.b.x = 3
					return o.f.x
				}
			`,
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "DisjointBorrows",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val o: {f: &{x: number}} = {f: &p.a}
					p.b.x = 3
					return o.f.x
				}
			`,
		},
		{
			column: "DisjointBorrows",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val {a: d} = &p
					p.b.x = 3
					return d.x
				}
			`,
		},
		{
			column: "DisjointBorrows",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn g() {
					val mut p = {a: {x: 1}, b: {x: 2}}
					readWrite(&p.a, &mut p.b)
				}
			`,
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live.
		{
			column: "DisjointBorrows",
			site:   "Return",
			src: `
				fn pick(p: &{x: number}) -> &{x: number} {
					return p
				}
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val d = pick(&p.a)
					p.b.x = 3
					return d.x
				}
			`,
		},
		{
			column: "DisjointBorrows",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					sink = &p.a
					p.b.x = 3
					return sink.x
				}
			`,
		},
		// Moving a source while an immutable borrow of it is live is rejected. Rule E3.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Binding",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					val d = p
					return r.x
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					var d = {x: 0}
					d = p
					return r.x
				}
			`,
			want: []string{"6:10-6:11: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					val o = {f: p}
					return r.x
				}
			`,
			want: []string{"5:18-5:19: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val p = {f: {x: 1}}
					val r = &p
					val {f: d} = p
					return r.f.x
				}
			`,
			want: []string{"5:19-5:20: cannot move 'p.f' while it is borrowed"},
		},
		// Moving a source into a `val mut` binding while an immutable borrow of it is live is rejected,
		// since the new owner could write what the borrow reads. Rule E3.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Binding",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					val mut d = p
					d.x = 2
					return r.x
				}
			`,
			want: []string{"5:18-5:19: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					var mut d = {x: 0}
					d = p
					return r.x
				}
			`,
			want: []string{"6:10-6:11: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					val mut o = {f: p}
					return r.x
				}
			`,
			want: []string{"5:22-5:23: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val p = {f: {x: 1}}
					val r = &p
					val {mut f} = p
					f.x = 2
					return r.f.x
				}
			`,
			want: []string{"5:20-5:21: cannot move 'p.f' while it is borrowed"},
		},
		// Moving a source while a mutable borrow of it is live is rejected. Rule E3.
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Binding",
			src: `
				fn g() {
					val mut p = {x: 1}
					val r = &mut p
					val d = p
					r.x = 2
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Reassignment",
			src: `
				fn g() {
					val mut p = {x: 1}
					val r = &mut p
					var d = {x: 0}
					d = p
					r.x = 2
				}
			`,
			want: []string{"6:10-6:11: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) {
					val mut p = {x: 1}
					val r = &mut p
					o.f = p
					r.x = 2
				}
			`,
			want: []string{"5:12-5:13: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<{x: number}>) {
					val mut p = {x: 1}
					val r = &mut p
					arr[0] = p
					r.x = 2
				}
			`,
			want: []string{"5:15-5:16: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "LiteralElement",
			src: `
				fn g() {
					val mut p = {x: 1}
					val r = &mut p
					val o = {f: p}
					r.x = 2
				}
			`,
			want: []string{"5:18-5:19: cannot move 'p' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Destructuring",
			src: `
				fn g() {
					val mut p = {f: {x: 1}}
					val r = &mut p
					val {f: d} = p
					r.f.x = 2
				}
			`,
			want: []string{"5:19-5:20: cannot move 'p.f' while it is borrowed"},
		},
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					val r = &mut p
					take(p)
					r.x = 2
				}
			`,
			want: []string{"6:11-6:12: cannot move 'p' while it is borrowed"},
		},
	})
}

// TestFlowMatrixMissingBorrow covers the unwritten-borrow column at the sites other than a call
// argument. Each program fills a borrow destination with the owned `p`, and F4 reports the `&`
// the destination needs.
func TestFlowMatrixMissingBorrow(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "UnwrittenBorrow",
			site:   "Binding",
			src: `
				fn g() {
					val p = {x: 1}
					val d: &{x: number} = p
				}
			`,
			want: []string{"4:28-4:29: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) {
					val p = {x: 1}
					var d: &{x: number} = init
					d = p
				}
			`,
			want: []string{"5:10-5:11: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) {
					val p = {x: 1}
					o.f = p
				}
			`,
			want: []string{"4:12-4:13: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>) {
					val p = {x: 1}
					a[0] = p
				}
			`,
			want: []string{"4:13-4:14: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow.
		{
			column: "UnwrittenBorrow",
			site:   "LiteralElement",
			src: `
				fn g() {
					val p = {x: 1}
					val o: {f: &{x: number}} = {f: p}
				}
			`,
			want: []string{"4:37-4:38: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "Return",
			src: `
				fn g() -> &{x: number} {
					val p = {x: 1}
					return p
				}
			`,
			want: []string{"4:13-4:14: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g() {
					val p = {x: 1}
					sink = p
				}
			`,
			want: []string{"5:13-5:14: this value is borrowed by its destination, so write the borrow: `&`"},
		},
	})
}

// TestFlowMatrixMissingFieldBorrow covers the "unwritten field borrow" column at each flow site
// whose destination is a borrow or takes its type from the source. `h.f` reads an object
// through the borrow `h`, so each site reports the `&` the read needs. Rules F4 and T6.
func TestFlowMatrixMissingFieldBorrow(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "UnwrittenFieldBorrow",
			site:   "Binding",
			src: `
				fn g(h: &{f: {x: number}}) {
					val d = h.f
				}
			`,
			want: []string{"3:14-3:17: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}, h: &{f: {x: number}}) {
					var d: &{x: number} = init
					d = h.f
				}
			`,
			want: []string{"4:10-4:13: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}, h: &{f: {x: number}}) {
					o.f = h.f
				}
			`,
			want: []string{"3:12-3:15: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>, h: &{f: {x: number}}) {
					a[0] = h.f
				}
			`,
			want: []string{"3:13-3:16: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "LiteralElement",
			src: `
				fn g(h: &{f: {x: number}}) {
					val o = {g: h.f}
				}
			`,
			want: []string{"3:18-3:21: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "Argument",
			src: `
				declare fn take(d: &{x: number}) -> undefined
				fn g(h: &{f: {x: number}}) {
					take(h.f)
				}
			`,
			want: []string{"4:11-4:14: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "Return",
			src: `
				fn g(h: &{f: {x: number}}) -> &{x: number} {
					return h.f
				}
			`,
			want: []string{"3:13-3:16: this value is borrowed by its destination, so write the borrow: `&`"},
		},
		{
			column: "UnwrittenFieldBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g(h: &{f: {x: number}}) {
					sink = h.f
				}
			`,
			want: []string{"4:13-4:16: this value is borrowed by its destination, so write the borrow: `&`"},
		},
	})
}

// DISABLED until #2284. The module-level move check looks at bindings, stores, literals, and
// returns, but not at a call argument or an owned receiver, so each move of `cfg` below goes
// unreported.
/*
func TestFlowMatrixModuleLevelMove(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today the call moves `cfg` with no diagnostic.
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Argument",
			src: `
				val cfg = {x: 1}
				declare fn take(d: {x: number}) -> undefined
				fn g() {
					take(cfg)
				}
			`,
			want: []string{"5:11-5:14: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		// Today `take` moves `cfg` with no diagnostic.
		{
			column: "MoveOutOfModuleLevelState",
			site:   "Receiver",
			src: flowReceiverClass + `
				val cfg = C(1)
				fn g() -> number {
					return cfg.take()
				}
			`,
			want: []string{"10:13-10:16: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
	})
}
*/

// DISABLED until #1269. Reassigning a `var` after a move does not clear the move, so the use
// after the reassignment reports `use of moved value 'p'`. E1 rejects only a use of the moved
// value, and the reassignment gives `p` a new one.
/*
func TestFlowMatrixReassignAfterAMove(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "ReassignAfterAMove",
			site:   "Binding",
			src: `
				fn g() -> number {
					var p = {x: 1}
					val d = p
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "Reassignment",
			src: `
				fn g() -> number {
					var p = {x: 1}
					var d: {x: number} = {x: 0}
					d = p
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) -> number {
					var p = {x: 1}
					o.f = p
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<{x: number}>) -> number {
					var p = {x: 1}
					a[0] = p
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					var p = {x: 1}
					val o = {f: p}
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					var p = {f: {x: 1}}
					val {f: d} = p
					p = {f: {x: 2}}
					return p.f.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() -> number {
					var p = {x: 1}
					take(p)
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					var p = C(1)
					val n = p.take()
					p = C(2)
					return n + p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() -> number {
					var p = {x: 1}
					sink = p
					p = {x: 2}
					return p.x
				}
			`,
		},
		{
			column: "ReassignAfterAMove",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					var p = {x: 1}
					val f = fn () {
						val q = p
						return q.x
					}
					val n = f()
					p = {x: 2}
					return p.x
				}
			`,
		},
	})
}
*/

// DISABLED until #2249. Storing a borrow into module-level state records a move of the source,
// so the read of `r` after the store reports `use of moved value 'r'`. F2 copies a borrow, so
// `r` stays usable.
/*
func TestFlowMatrixModuleLevelBorrowCopy(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "ImmutableCopy",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g(r: &{x: number}) -> number {
					sink = r
					return sink.x + r.x
				}
			`,
		},
	})
}
*/

// DISABLED until #2249 and #2269. Each cell below stores a `&mut` parameter into module-level
// state, which fails today for two reasons. The store records a move of the source, which F2
// rules out for a borrow. A check that the single ownership pass replaces also rejects the
// store. The comment above each cell says what the solver reports today.
/*
func TestFlowMatrixModuleLevelMutableBorrow(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today any store into a module-level `&mut` binding reports
		// `cannot constrain immutable object <: mutable object`, and the store also moves `m`.
		{
			column: "MutableCopy",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g(m: &mut {x: number}) {
					sink = m
					sink.x = 1
					m.x = 2
				}
			`,
		},
		// Today the store moves `m`, and the alias-set transition check rejects the narrowing because the
		// stored borrow is `'static`.
		{
			column: "Narrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g(m: &mut {x: number}) -> number {
					sink = m
					return sink.x + m.x
				}
			`,
		},
	})
}
*/

// DISABLED until #2247. A `'static` borrow stays live for the rest of the program, since
// module-level state can read it at any later point. Today the exclusivity check treats it as
// ending at the store or call that takes it, so each write or borrow below reports nothing.
/*
func TestFlowMatrixStaticBorrow(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "WriteDuringImmutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g() -> number {
					val mut p = {x: 1}
					sink = &p
					p.x = 2
					return sink.x
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Binding",
			src: `
				fn g() {
					val mut p = {x: 1}
					val d: &'static {x: number} = &p
					p.x = 2
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &'static {x: number}) {
					val mut p = {x: 1}
					var d: &'static {x: number} = init
					d = &p
					p.x = 2
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &'static {x: number}}) {
					val mut p = {x: 1}
					o.f = &p
					p.x = 2
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Argument",
			src: `
				declare fn track(value: &'static {x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					track(&p)
					p.x = 2
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &'static {x: number}
				fn g() {
					val mut p = {x: 1}
					sink = &p
					p.x = 2
				}
			`,
			want: []string{"6:6-6:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Binding",
			src: `
				fn g() {
					val mut p = {x: 1}
					val d: &'static mut {x: number} = &mut p
					val r = &p
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Reassignment",
			src: `
				fn g(init: &'static mut {x: number}) {
					val mut p = {x: 1}
					var d: &'static mut {x: number} = init
					d = &mut p
					val r = &p
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &'static mut {x: number}}) {
					val mut p = {x: 1}
					o.f = &mut p
					val r = &p
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Argument",
			src: `
				declare fn track(value: &'static mut {x: number}) -> undefined
				fn g() {
					val mut p = {x: 1}
					track(&mut p)
					val r = &p
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
	})
}
*/

// DISABLED until #2247 and #2269. Each cell below takes a `'static` borrow, which today ends at
// the store or call that takes it. The cell also fails a check that the single ownership pass
// replaces, or holds the borrow where no current check tracks it. The comment above each cell
// says what the solver reports today.
/*
func TestFlowMatrixStaticBorrowAndOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today any store into a module-level `&mut` binding reports
		// `cannot constrain immutable object <: mutable object`.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g() -> number {
					val mut p = {x: 1}
					sink = &mut p
					val r = &p
					sink.x = 2
					return r.x
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&'static {x: number}>) {
					val mut p = {x: 1}
					arr[0] = &p
					p.x = 2
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow. Today the borrow a typed literal holds is not tracked, so the write to
		// `p.x` reports nothing.
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() {
					val mut p = {x: 1}
					val o: {f: &'static {x: number}} = {f: &p}
					p.x = 2
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// Today the borrow a pattern takes is not tracked, so the write to `p.f.x` reports nothing.
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() {
					val mut p = {f: {x: 1}}
					val {f: d}: &'static {f: {x: number}} = &p
					p.f.x = 2
				}
			`,
			want: []string{"5:6-5:11: cannot assign to 'p.f.x' while it is borrowed as immutable"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `keep` returns is not tied to `p`, so the write to `p.x`
		// reports nothing.
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Return",
			src: `
				fn keep(p: &'static {x: number}) -> &'static {x: number} {
					return p
				}
				fn g() {
					val mut p = {x: 1}
					val d = keep(&p)
					p.x = 2
				}
			`,
			want: []string{"8:6-8:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&'static mut {x: number}>) {
					val mut p = {x: 1}
					arr[0] = &mut p
					val r = &p
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow. Today the borrow a typed literal holds is not tracked, so `&p` reports
		// nothing.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() {
					val mut p = {x: 1}
					val o: {f: &'static mut {x: number}} = {f: &mut p}
					val r = &p
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// Today the borrow a pattern takes is not tracked, so `&p` reports nothing.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() {
					val mut p = {f: {x: 1}}
					val {f: d}: &'static mut {f: {x: number}} = &mut p
					val r = &p
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `keepMut` returns is not tied to `p`, so `&p` reports nothing.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Return",
			src: `
				fn keepMut(p: &'static mut {x: number}) -> &'static mut {x: number} {
					return p
				}
				fn g() {
					val mut p = {x: 1}
					val d = keepMut(&mut p)
					val r = &p
				}
			`,
			want: []string{"8:14-8:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// Today any store into a module-level `&mut` binding reports
		// `cannot constrain immutable object <: mutable object`.
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &'static mut {x: number}
				fn g() {
					val mut p = {x: 1}
					sink = &mut p
					val r = &p
				}
			`,
			want: []string{"6:14-6:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
	})
}
*/

// DISABLED until #2247 and #2289. Each cell below calls a method that takes `&'static self`. Today
// the receiver reports `lifetime 'static is used but not declared`, and a `'static` borrow ends at
// the call that takes it.
/*
func TestFlowMatrixStaticReceiver(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "WriteAfterStaticImmutableBorrow",
			site:   "Receiver",
			src: `
				class S {
					x: number,
					track(&'static self) -> undefined {},
					trackMut(&'static mut self) -> undefined {},
				}
				fn g() {
					val mut p = S(1)
					p.track()
					p.x = 2
				}
			`,
			want: []string{"10:6-10:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		{
			column: "ImmutableBorrowAfterStaticMutableBorrow",
			site:   "Receiver",
			src: `
				class S {
					x: number,
					track(&'static self) -> undefined {},
					trackMut(&'static mut self) -> undefined {},
				}
				fn g() {
					val mut p = S(1)
					p.trackMut()
					val r = &p
				}
			`,
			want: []string{"10:14-10:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
	})
}
*/

// DISABLED until #2269 and #2290. The cell writes an element of an array it borrows as `&mut`,
// which today is immutable, and also fails a check that the single ownership pass replaces. The
// comment above the cell says what the solver reports today.
/*
func TestFlowMatrixArrayElementMutAndOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// The destination is a slot the function can write, so the cell writes the moved value through
		// it. Today the write to `arr[0].x` reports
		// `cannot constrain immutable object <: mutable object`, and the frame-exit check reports
		// `'p' leaves the function both as an owned value and through a borrow`.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<{x: number}>) -> number {
					val p: {x: number} = {x: 1}
					val r = &p
					arr[0] = p
					arr[0].x = 2
					return r.x
				}
			`,
			want: []string{"5:15-5:16: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

// TestFlowMatrixBindingMutability covers the cells whose source a `val mut` binding owns. T3
// carries the binding's mutability through every owned layer, including an array's elements and a
// field's class instance, so each cell writes or `&mut` borrows one of those layers.
func TestFlowMatrixBindingMutability(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "DisjointBorrows",
			site:   "Receiver",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
					writeWith(&mut self, w: &mut R) -> undefined {},
					writeRead(&mut self, r: &R) -> undefined {},
					take(self) -> number { return self.x },
				}
				fn g() {
					val mut p: {a: R, b: R} = {a: R(1), b: R(2)}
					p.a.readWith(&mut p.b)
				}
			`,
		},
		{
			column: "ElementBorrows",
			site:   "Binding",
			src: `
				fn g() -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val d: &{x: number} = &xs[0]
					xs[1].x = 3
					return d.x
				}
			`,
		},
		{
			column: "ElementBorrows",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					var d: &{x: number} = init
					d = &xs[0]
					xs[1].x = 3
					return d.x
				}
			`,
		},
		{
			column: "ElementBorrows",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					o.f = &xs[0]
					xs[1].x = 3
					return o.f.x
				}
			`,
		},
		{
			column: "ElementBorrows",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				declare fn writeWrite(a: &mut {x: number}, b: &mut {x: number}) -> undefined
				declare fn writeRead(a: &mut {x: number}, b: &{x: number}) -> undefined
				fn g() {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					readWrite(&xs[0], &mut xs[1])
				}
			`,
		},
		{
			column: "DynamicElementBorrows",
			site:   "Binding",
			src: `
				fn g(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val d: &{x: number} = &xs[0]
					xs[i].x = 3
					return d.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		{
			column: "DynamicElementBorrows",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}, i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					var d: &{x: number} = init
					d = &xs[0]
					xs[i].x = 3
					return d.x
				}
			`,
			want: []string{"6:6-6:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		{
			column: "DynamicElementBorrows",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &{x: number}}, i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					o.f = &xs[0]
					xs[i].x = 3
					return o.f.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		{
			column: "DynamicElementBorrows",
			site:   "Argument",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				fn g(i: number) {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					readWrite(&xs[0], &mut xs[i])
				}
			`,
			want: []string{"5:24-5:34: cannot borrow 'xs' as mutable while 'xs[0]' is borrowed as immutable"},
		},
		{
			column: "DynamicElementBorrows",
			site:   "ClosureCapture",
			src: `
				fn g(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					val f = fn () { xs[i].x = 3 }
					f()
					return r.x
				}
			`,
			want: []string{"6:6-6:7: this call runs 'f', which writes 'xs' while 'xs[0]' is borrowed as immutable"},
		},
	})
}

// DISABLED until #2247. The cell writes an element of an array owned by a `val mut` binding
// while a `'static` borrow of another element is live. Today the exclusivity check treats the
// borrow as ending at the store that takes it, so the write reports nothing.
/*
func TestFlowMatrixBindingMutabilityAndStaticBorrow(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		{
			column: "DynamicElementBorrows",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &{x: number}
				fn g(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					sink = &xs[0]
					xs[i].x = 3
					return sink.x
				}
			`,
			want: []string{"6:6-6:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
	})
}
*/

// DISABLED until #2269. Each cell below writes a value that a `val mut` or `var mut` binding
// owns. The cell fails a check that the single ownership pass replaces, or holds a borrow where no
// current check tracks it. The comment above each cell says what the solver reports today.
/*
func TestFlowMatrixBindingMutabilityAndOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today the element store reports
		// `borrowed value 'xs' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "DynamicElementBorrows",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&{x: number}>, i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					arr[0] = &xs[0]
					xs[i].x = 3
					return arr[0].x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow. Today the borrow the literal holds is not tracked, so the write to
		// `xs[i].x` reports nothing.
		{
			column: "DynamicElementBorrows",
			site:   "LiteralElement",
			src: `
				fn g(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val o: {f: &{x: number}} = {f: &xs[0]}
					xs[i].x = 3
					return o.f.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		// A tuple pattern destructures a tuple, so the source is a tuple literal. Today the borrow the
		// pattern takes is not tracked, so the write to `xs[i].x` reports nothing.
		{
			column: "DynamicElementBorrows",
			site:   "Destructuring",
			src: `
				fn g(i: number) -> number {
					val mut xs = [{x: 1}, {x: 2}]
					val [d, e] = &xs
					xs[i].x = 3
					return d.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pick` returns is not tied to `xs`, so the write to `xs[i].x`
		// reports nothing.
		{
			column: "DynamicElementBorrows",
			site:   "Return",
			src: `
				fn pick(p: &{x: number}) -> &{x: number} {
					return p
				}
				fn g(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val d = pick(&xs[0])
					xs[i].x = 3
					return d.x
				}
			`,
			want: []string{"8:6-8:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		// The module-level binding is `var mut`, so it owns a mutable value. Today storing `p` into
		// module-level state while it is borrowed reports nothing.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "ModuleLevelStore",
			src: `
				declare var mut sink: {x: number}
				fn g() -> number {
					val p: {x: number} = {x: 1}
					val r = &p
					sink = p
					sink.x = 2
					return r.x
				}
			`,
			want: []string{"6:13-6:14: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

// DISABLED until #2269. The cell calls a `mut self` method on a value a `val` binding owns,
// which moves the value into the method's `self`. No current check reports that move while the
// value is borrowed, which the single ownership pass in #2269 does. The comment above the cell says
// what the solver reports today.
/*
func TestFlowMatrixMutSelfAndOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// The method takes `mut self`, so the call moves `p` into a mutable owner. Today moving `p`
		// through the receiver while it is borrowed reports nothing.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Receiver",
			src: `
				class W {
					x: number,
					consume(mut self) -> number {
						self.x = 2
						return self.x
					},
				}
				fn g() -> number {
					val p = W(1)
					val r = &p
					val n = p.consume()
					return n + r.x
				}
			`,
			want: []string{"12:14-12:15: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

// DISABLED until #2269. The cell writes a value that a `mut` parameter owns. It also trips the
// frame-exit check, which the ownership pass in #2269 replaces. The comment above the cell says
// what the solver reports today.
/*
func TestFlowMatrixMutParameter(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today the frame-exit check also reports
		// `'p' leaves the function both as an owned value and through a borrow`, though the function
		// returns a `number`.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Argument",
			src: `
				fn take(mut d: {x: number}) -> undefined {
					d.x = 2
				}
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					take(p)
					return r.x
				}
			`,
			want: []string{"8:11-8:12: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

// DISABLED until #2269, which turns on the single ownership pass the spec's checking model
// describes. Each cell below fails today because of a check that pass replaces or a case no
// current check covers. The comment above each cell says what the solver reports today.
/*
func TestFlowMatrixOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today the alias-set transition check reports
		// `cannot assign 'm' to immutable 'd': 'm' is still used mutably after this point`, though the
		// body only reads `m` afterwards.
		{
			column: "Narrow",
			site:   "Binding",
			src: `
				fn g(m: &mut {x: number}) -> number {
					val d: &{x: number} = m
					return d.x + m.x
				}
			`,
		},
		// Today the alias-set transition check reports
		// `cannot assign 'm' to immutable 'd': 'm' is still used mutably after this point`, though the
		// body only reads `m` afterwards.
		{
			column: "Narrow",
			site:   "Reassignment",
			src: `
				fn g(init: &{x: number}, m: &mut {x: number}) -> number {
					var d: &{x: number} = init
					d = m
					return d.x + m.x
				}
			`,
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`. L1 lets a borrow outlive
		// its owner, and the field store in the same column is accepted.
		{
			column: "Borrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>) -> number {
					val p = {x: 1}
					a[0] = &p
					return a[0].x + p.x
				}
			`,
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`. L1 lets a borrow outlive
		// its owner, and the field store in the same column is accepted.
		{
			column: "MutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&mut {x: number}>) {
					val mut p = {x: 1}
					a[0] = &mut p
					a[0].x = 2
				}
			`,
		},
		// Today returning `&mut` of a local reports `cannot constrain immutable object <: mutable object`
		// at the return type. L1 lets the borrow outlive `p`, and the immutable form in the borrow column
		// is accepted.
		{
			column: "MutableBorrow",
			site:   "Return",
			src: `
				fn g() -> &mut {x: number} {
					val mut p = {x: 1}
					return &mut p
				}
			`,
		},
		// Today any store into a module-level `&mut` binding reports
		// `cannot constrain immutable object <: mutable object`.
		{
			column: "MutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g() {
					val mut p = {x: 1}
					sink = &mut p
					sink.x = 2
				}
			`,
		},
		// Today the element store also reports that `p` does not live long enough to escape the function.
		{
			column: "MutableBorrowThroughVal",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&mut {x: number}>) {
					val p: {x: number} = {x: 1}
					a[0] = &mut p
				}
			`,
			want: []string{"4:13-4:19: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// Today returning `&mut` of a local also reports
		// `cannot constrain immutable object <: mutable object` at the return type.
		{
			column: "MutableBorrowThroughVal",
			site:   "Return",
			src: `
				fn g() -> &mut {x: number} {
					val p: {x: number} = {x: 1}
					return &mut p
				}
			`,
			want: []string{"4:13-4:19: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// Today the store into the module-level `&mut` binding also reports
		// `cannot constrain immutable object <: mutable object` on the whole assignment.
		{
			column: "MutableBorrowThroughVal",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g() {
					val p: {x: number} = {x: 1}
					sink = &mut p
				}
			`,
			want: []string{"5:13-5:19: cannot mutate 'p' because its binding is immutable; declare it as `val mut p`"},
		},
		// Today the literal takes the borrow as an element with no diagnostic.
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "LiteralElement",
			src: `
				fn g(r: &{x: number}) {
					val o = {f: r}
				}
			`,
			want: []string{"3:18-3:19: cannot use borrowed &object as owned object"},
		},
		// Today the owned module-level binding takes the borrow with no diagnostic.
		{
			column: "ImmutableBorrowIntoOwned",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g(r: &{x: number}) {
					sink = r
				}
			`,
			want: []string{"4:13-4:14: cannot use borrowed &object as owned object"},
		},
		// Today the literal takes the borrow as an element with no diagnostic.
		{
			column: "MutableBorrowIntoOwned",
			site:   "LiteralElement",
			src: `
				fn g(m: &mut {x: number}) {
					val o = {f: m}
				}
			`,
			want: []string{"3:18-3:19: cannot use borrowed &mut object as owned object"},
		},
		// Today the owned module-level binding takes the borrow with no diagnostic.
		{
			column: "MutableBorrowIntoOwned",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g(m: &mut {x: number}) {
					sink = m
				}
			`,
			want: []string{"4:13-4:14: cannot use borrowed &mut object as owned object"},
		},
		// Today the call of `f` also reports `use of moved value 'f'`, though each iteration binds a new
		// `f`.
		{
			column: "MoveInALoop",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val p = {x: 1}
					for i in [1, 2] {
						val f = fn () {
							val q = p
							return q.x
						}
						val n = f()
					}
				}
			`,
			want: []string{"5:15-8:8: use of moved value 'p'"},
		},
		// Today the closure's capture moves all of `p` instead of `p.a`, so the use reports
		// `use of moved value 'p'`.
		{
			column: "PartialMove",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val p = {a: {x: 1}, b: {x: 2}}
					val f = fn () {
						val q = p.a
						return q.x
					}
					val n = f()
					val e = p
				}
			`,
			want: []string{"9:14-9:15: use of partially moved value 'p'; field 'p.a' was moved out"},
		},
		// Today the closure's capture moves all of `p` instead of `p.a`, so the read of `p.b` reports
		// `use of moved value 'p'`.
		{
			column: "DisjointField",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {a: {x: 1}, b: {x: 2}}
					val f = fn () {
						val q = p.a
						return q.x
					}
					val n = f()
					return n + p.b.x
				}
			`,
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "OutliveOwner",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&{x: number}>, c: boolean) -> number {
					if c {
						val p = {x: 1}
						arr[0] = &p
					}
					return arr[0].x
				}
			`,
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element. The write is not reported.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&{x: number}>) -> number {
					val mut p = {x: 1}
					arr[0] = &p
					p.x = 2
					return arr[0].x
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow. Today the borrow a typed literal holds is not tracked, so the write to
		// `p.x` reports nothing.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val o: {f: &{x: number}} = {f: &p}
					p.x = 2
					return o.f.x
				}
			`,
			want: []string{"5:6-5:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// Today the borrow a pattern takes from `&p` is not tracked, so the write to `p.f.x` reports
		// nothing.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val mut p = {f: {x: 1}}
					val {f: d} = &p
					p.f.x = 2
					return d.x
				}
			`,
			want: []string{"5:6-5:11: cannot assign to 'p.f.x' while it is borrowed as immutable"},
		},
		// Today a method call's receiver borrow is not checked against its arguments, so `&mut p` reports
		// nothing.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Receiver",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
					writeWith(&mut self, w: &mut R) -> undefined {},
					writeRead(&mut self, r: &R) -> undefined {},
					take(self) -> number { return self.x },
				}
				fn g() {
					val mut p = R(1)
					p.readWith(&mut p)
				}
			`,
			want: []string{"11:17-11:23: cannot borrow 'p' as mutable while it is borrowed as immutable"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pick` returns is not tied to `p`, so the write to `p.x`
		// reports nothing.
		{
			column: "WriteDuringImmutableBorrow",
			site:   "Return",
			src: `
				fn pick(p: &{x: number}) -> &{x: number} {
					return p
				}
				fn g() -> number {
					val mut p = {x: 1}
					val d = pick(&p)
					p.x = 2
					return d.x
				}
			`,
			want: []string{"8:6-8:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "SequentialPhases",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&{x: number}>) -> number {
					val mut p = {x: 1}
					p.x = 2
					arr[0] = &p
					return arr[0].x
				}
			`,
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "WriteDuringMutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&mut {x: number}>) {
					val mut p = {x: 1}
					arr[0] = &mut p
					p.x = 2
					arr[0].x = 3
				}
			`,
		},
		// Today any store into a module-level `&mut` binding reports
		// `cannot constrain immutable object <: mutable object`.
		{
			column: "WriteDuringMutableBorrow",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: &mut {x: number}
				fn g() {
					val mut p = {x: 1}
					sink = &mut p
					p.x = 2
					sink.x = 3
				}
			`,
		},
		// Today the frame-exit check also reports
		// `'p' leaves the function through a mutable path and an immutable one`, though the function
		// returns a `number`.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: &mut {x: number}}) -> number {
					val mut p = {x: 1}
					o.f = &mut p
					val r = &p
					o.f.x = 2
					return r.x
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element. The frame-exit check also
		// reports `'p' leaves the function through a mutable path and an immutable one`, and the borrow
		// `&p` is not reported.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&mut {x: number}>) -> number {
					val mut p = {x: 1}
					arr[0] = &mut p
					val r = &p
					arr[0].x = 2
					return r.x
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// A literal with an expected type gives each element the type of the matching field, so this
		// literal holds a borrow. Today the borrow a typed literal holds is not tracked, so `&p` reports
		// nothing.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "LiteralElement",
			src: `
				fn g() -> number {
					val mut p = {x: 1}
					val o: {f: &mut {x: number}} = {f: &mut p}
					val r = &p
					o.f.x = 2
					return r.x
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// The leaf `d` borrows the field `p.f` from `&mut p`, so the report names that field. The
		// current checks do not track that borrow, so `&p` reports nothing.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Destructuring",
			src: `
				fn g() -> number {
					val mut p = {f: {x: 1}}
					val {f: d} = &mut p
					val r = &p
					d.x = 2
					return r.f.x
				}
			`,
			want: []string{"5:14-5:16: cannot borrow 'p' as immutable while 'p.f' is borrowed as mutable"},
		},
		// Today a method call's receiver borrow is not checked against its arguments, so `&p` reports
		// nothing.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Receiver",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
					writeWith(&mut self, w: &mut R) -> undefined {},
					writeRead(&mut self, r: &R) -> undefined {},
					take(self) -> number { return self.x },
				}
				fn g() {
					val mut p = R(1)
					p.writeRead(&p)
				}
			`,
			want: []string{"11:18-11:20: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pickMut` returns is not tied to `p`, so `&p` reports nothing.
		{
			column: "ImmutableBorrowDuringMutableBorrow",
			site:   "Return",
			src: `
				fn pickMut(p: &mut {x: number}) -> &mut {x: number} {
					return p
				}
				fn g() -> number {
					val mut p = {x: 1}
					val d = pickMut(&mut p)
					val r = &p
					d.x = 2
					return r.x
				}
			`,
			want: []string{"8:14-8:16: cannot borrow 'p' as immutable while it is borrowed as mutable"},
		},
		// Today the element store reports
		// `borrowed value 'p' does not live long enough to escape the function`, which the frame-exit
		// check reports for any borrow of a local stored into an element.
		{
			column: "DisjointBorrows",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<&{x: number}>) -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					arr[0] = &p.a
					p.b.x = 3
					return arr[0].x
				}
			`,
		},
		// Today the closure captures all of `xs` instead of `xs[1]`, so the call reports
		// `this call runs 'f', which writes 'xs' while 'xs[0]' is borrowed as immutable`.
		{
			column: "ElementBorrows",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					val f = fn () { xs[1].x = 3 }
					f()
					return r.x
				}
			`,
		},
		// Today the closure captures all of `p` instead of `p.b`, so the call reports
		// `this call runs 'f', which writes 'p' while 'p.a' is borrowed as immutable`.
		{
			column: "DisjointBorrows",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val mut p = {a: {x: 1}, b: {x: 2}}
					val r = &p.a
					val f = fn () { p.b.x = 3 }
					f()
					return r.x
				}
			`,
		},
		// Today a method call's receiver borrow is not checked against its arguments, so `&mut xs[i]`
		// reports nothing.
		{
			column: "DynamicElementBorrows",
			site:   "Receiver",
			src: `
				class R {
					x: number,
					readWith(&self, w: &mut R) -> undefined {},
					writeWith(&mut self, w: &mut R) -> undefined {},
					writeRead(&mut self, r: &R) -> undefined {},
					take(self) -> number { return self.x },
				}
				fn g(i: number) {
					val mut xs: Array<R> = [R(1), R(2)]
					xs[0].readWith(&mut xs[i])
				}
			`,
			want: []string{"11:21-11:31: cannot borrow 'xs' as mutable while 'xs[0]' is borrowed as immutable"},
		},
		// Today the frame-exit check also reports
		// `'p' leaves the function both as an owned value and through a borrow`, though the function
		// returns a `number`.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) -> number {
					val p = {x: 1}
					val r = &p
					o.f = p
					return r.x
				}
			`,
			want: []string{"5:12-5:13: cannot move 'p' while it is borrowed"},
		},
		// Today the frame-exit check also reports
		// `'p' leaves the function both as an owned value and through a borrow`, though the function
		// returns a `number`.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "ElementStore",
			src: `
				fn g(arr: &mut Array<{x: number}>) -> number {
					val p = {x: 1}
					val r = &p
					arr[0] = p
					return r.x
				}
			`,
			want: []string{"5:15-5:16: cannot move 'p' while it is borrowed"},
		},
		// Today the frame-exit check also reports
		// `'p' leaves the function both as an owned value and through a borrow`, though the function
		// returns a `number`.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Argument",
			src: `
				declare fn take(d: {x: number}) -> undefined
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					take(p)
					return r.x
				}
			`,
			want: []string{"6:11-6:12: cannot move 'p' while it is borrowed"},
		},
		// Today moving `p` through an owned receiver while it is borrowed reports nothing.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() -> number {
					val p = C(1)
					val r = &p
					val n = p.take()
					return n + r.x
				}
			`,
			want: []string{"11:14-11:15: cannot move 'p' while it is borrowed"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pick` returns is not tied to `p`, so the move reports
		// nothing.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "Return",
			src: `
				fn pick(p: &{x: number}) -> &{x: number} {
					return p
				}
				fn g() -> number {
					val p = {x: 1}
					val d = pick(&p)
					val q = p
					return d.x
				}
			`,
			want: []string{"8:14-8:15: cannot move 'p' while it is borrowed"},
		},
		// Today storing `p` into module-level state while it is borrowed reports nothing.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() -> number {
					val p = {x: 1}
					val r = &p
					sink = p
					return r.x
				}
			`,
			want: []string{"6:13-6:14: cannot move 'p' while it is borrowed"},
		},
		// Today the move is accepted, and the later call reports
		// `this call runs 'peek', which reads 'p' after it was moved`.
		{
			column: "MoveWhileImmutablyBorrowed",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val peek = fn () { return p.x }
					val q = p
					return peek()
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
		// The destination is a slot the function can write, so the cell writes the moved value through
		// it. Today the frame-exit check also reports
		// `'p' leaves the function both as an owned value and through a borrow`, though the function
		// returns a `number`.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "FieldStore",
			src: `
				fn g(o: &mut {f: {x: number}}) -> number {
					val p: {x: number} = {x: 1}
					val r = &p
					o.f = p
					o.f.x = 2
					return r.x
				}
			`,
			want: []string{"5:12-5:13: cannot move 'p' while it is borrowed"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pick` returns is not tied to `p`, so the move reports
		// nothing.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "Return",
			src: `
				fn pick(p: &{x: number}) -> &{x: number} {
					return p
				}
				fn g() -> number {
					val p = {x: 1}
					val d = pick(&p)
					val mut q = p
					return d.x
				}
			`,
			want: []string{"8:18-8:19: cannot move 'p' while it is borrowed"},
		},
		// Today the alias-set transition check reports `cannot assign 'p' to mutable 'q'`, and the later
		// call reports that `peek` reads `p` after it was moved.
		{
			column: "MoveToMutableWhileImmutablyBorrowed",
			site:   "ClosureCapture",
			src: `
				fn g() -> number {
					val p = {x: 1}
					val peek = fn () { return p.x }
					val mut q = p
					q.x = 2
					return peek()
				}
			`,
			want: []string{"5:18-5:19: cannot move 'p' while it is borrowed"},
		},
		// Today moving `p` through an owned receiver while it is borrowed reports nothing.
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Receiver",
			src: flowReceiverClass + `
				fn g() {
					val mut p = C(1)
					val r = &mut p
					val n = p.take()
					r.x = 2
				}
			`,
			want: []string{"11:14-11:15: cannot move 'p' while it is borrowed"},
		},
		// A return passes the borrow back to the caller, so the caller uses the source while the returned
		// borrow is live. Today the borrow `pickMut` returns is not tied to `p`, so the move reports
		// nothing.
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "Return",
			src: `
				fn pickMut(p: &mut {x: number}) -> &mut {x: number} {
					return p
				}
				fn g() {
					val mut p = {x: 1}
					val d = pickMut(&mut p)
					val q = p
					d.x = 2
				}
			`,
			want: []string{"8:14-8:15: cannot move 'p' while it is borrowed"},
		},
		// Today storing `p` into module-level state while it is borrowed reports nothing.
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "ModuleLevelStore",
			src: `
				declare var sink: {x: number}
				fn g() {
					val mut p = {x: 1}
					val r = &mut p
					sink = p
					r.x = 2
				}
			`,
			want: []string{"6:13-6:14: cannot move 'p' while it is borrowed"},
		},
		// Today the move is accepted, and the later call reports
		// `this call runs 'bump', which writes 'p' after it was moved`.
		{
			column: "MoveWhileMutablyBorrowed",
			site:   "ClosureCapture",
			src: `
				fn g() {
					val mut p = {x: 1}
					val bump = fn () { p.x = 2 }
					val q = p
					bump()
				}
			`,
			want: []string{"5:14-5:15: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

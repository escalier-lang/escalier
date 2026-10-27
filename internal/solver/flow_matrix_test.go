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
	// notApplicable says why the cell has no program. A cell with a reason has no src.
	notApplicable string
}

// runFlowMatrix checks each cell's program against the standard library and asserts the full
// list of diagnostics. A cell that does not apply is skipped with its reason.
func runFlowMatrix(t *testing.T, cells []flowCell) {
	for _, c := range cells {
		t.Run(c.column+"/"+c.site, func(t *testing.T) {
			if c.notApplicable != "" {
				require.Empty(t, c.src)
				t.Skip(c.notApplicable)
			}
			_, errs := inferAgainstTreeWithSpans(t, c.src)
			require.Equal(t, c.want, errs)
		})
	}
}

// TestFlowMatrix covers the copy and borrow columns of the flow matrix in the conformance tests
// of planning/ownership_rules/requirements.md, crossed with every flow site that F1 lists. Each
// column states its source, its destination, and what the program does after the flow. The cells
// the current solver gets wrong sit in the disabled tests after this one, grouped by the issue
// that enables them.
//
// An error the solver reports today may blame the source's annotation instead of the source
// expression. Each cell asserts the span the solver reports today.
func TestFlowMatrix(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// A value type copies into an owned destination, and the source stays usable. F2.
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
					return n
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
		// A `&T` copies into a `&T` destination, and the source stays usable. F2.
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
					return r.x
				}
			`,
		},
		{
			column:        "ImmutableCopy",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		// A `&mut T` copies into a `&mut T` destination, and both can write. F2 and E2.
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
		{
			column:        "MutableCopy",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		// A `&mut T` copies into a `&T` destination, narrowed to `&`, and both stay usable. F2 and T4.
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
					return m.x
				}
			`,
		},
		{
			column:        "Narrow",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		// A `&T` cannot fill a `&mut T` destination. T4.
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
		{
			column:        "Widen",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		// A written `&p` borrows an owned value into a `&T` destination, and the source stays usable. F2
		// and F4.
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
		{
			column:        "Borrow",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		{
			column:        "Borrow",
			site:          "ClosureCapture",
			notApplicable: "A capture is never written with `&`. The unwritten borrow column covers a capture that borrows.",
		},
		// A written `&mut p` borrows a value owned by a `val mut` binding into a `&mut T` destination,
		// which can write. F2 and T7.
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
		{
			column:        "MutableBorrow",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
				fn g() {
					val mut p = C(1)
					val r = (&mut p).set()
				}
			`,
		},
		{
			column:        "MutableBorrow",
			site:          "ClosureCapture",
			notApplicable: "A capture is never written with `&`. The unwritten borrow column covers a capture that borrows.",
		},
		// A written `&mut p` of a value owned by a `val` binding is rejected. T7.
		{
			column: "MutableBorrowThroughVal",
			site:   "Binding",
			src: `
				fn g() {
					val p: {x: number} = {x: 1}
					val d: &mut {x: number} = &mut p
				}
			`,
			want: []string{"4:32-4:38: cannot constrain immutable object <: mutable object"},
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
			want: []string{"5:10-5:16: cannot constrain immutable object <: mutable object"},
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
			want: []string{"4:12-4:18: cannot constrain immutable object <: mutable object"},
		},
		{
			column:        "MutableBorrowThroughVal",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
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
			want: []string{"4:19-4:25: cannot constrain immutable object <: mutable object"},
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
			want: []string{"5:11-5:17: cannot constrain immutable object <: mutable object"},
		},
		{
			column: "MutableBorrowThroughVal",
			site:   "Receiver",
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
				fn g() {
					val p = C(1)
					val r = (&mut p).set()
				}
			`,
			want: []string{"10:15-10:21: cannot constrain immutable C <: mutable C"},
		},
		{
			column:        "MutableBorrowThroughVal",
			site:          "ClosureCapture",
			notApplicable: "A capture is never written with `&`. The unwritten borrow column covers a capture that borrows.",
		},
		// An owned value written without `&` cannot fill a borrow destination. F4. A receiver, a pattern
		// over a borrow, and a capture borrow without a written `&`.
		{
			column:        "UnwrittenBorrow",
			site:          "LiteralElement",
			notApplicable: "A literal element is an owned destination, so it has no borrow form.",
		},
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		// A reference-shaped field read through a borrow, written without `&`, is rejected at any
		// destination. F4 and T6. A receiver and a pattern over a borrow are exempt.
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
				fn g(h: &{f: C}) -> number {
					return h.f.get()
				}
			`,
		},
		{
			column:        "UnwrittenFieldBorrow",
			site:          "ClosureCapture",
			notApplicable: "A closure captures a binding, never a field read, so no capture reads a field through a borrow.",
		},
		// A `&T` cannot fill an owned destination. T6.
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		{
			column:        "ImmutableBorrowIntoOwned",
			site:          "ClosureCapture",
			notApplicable: "A capture of a borrow copies the borrow, whether the closure stays in the body or is returned, so no capture puts a borrow in an owned destination.",
		},
		// A `&mut T` cannot fill an owned destination. T6.
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
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
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
		{
			column:        "MutableBorrowIntoOwned",
			site:          "ClosureCapture",
			notApplicable: "A capture of a borrow copies the borrow, whether the closure stays in the body or is returned, so no capture puts a borrow in an owned destination.",
		},
	})
}

// DISABLED until #2256, which reports a missing `&` at every flow site that borrows an owned
// value. Today only a call argument reports it, so each program below borrows `p` with no
// diagnostic.
/*
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
*/

// DISABLED until #2258, which reports a missing `&` on a reference-shaped field read through a
// borrow. Today `h.f` fills each destination below with no diagnostic, including a call
// argument.
/*
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

// DISABLED until #2269, which turns on the single ownership pass the spec's checking model
// describes. Each cell below fails today because of a check that pass replaces or a case no
// current check covers. The comment above each cell says what the solver reports today.
/*
func TestFlowMatrixOwnershipPass(t *testing.T) {
	runFlowMatrix(t, []flowCell{
		// Today any store into a module-level `&mut` binding reports `cannot constrain immutable object
		// <: mutable object`.
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
		// Today the alias-set transition check reports `cannot assign 'm' to immutable 'd': 'm' is still
		// used mutably after this point`, though the body only reads `m` afterwards.
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
		// Today the alias-set transition check reports `cannot assign 'm' to immutable 'd': 'm' is still
		// used mutably after this point`, though the body only reads `m` afterwards.
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
		// Today the store moves `m`, which #2249 fixes. The alias-set transition check also rejects the
		// narrowing because the stored borrow is `'static`.
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
		// Today the element store reports `borrowed value 'p' does not live long enough to escape the
		// function`. L1 lets a borrow outlive its owner, and the field store in the same column is
		// accepted.
		{
			column: "Borrow",
			site:   "ElementStore",
			src: `
				fn g(a: &mut Array<&{x: number}>) -> number {
					val p = {x: 1}
					a[0] = &p
					return p.x
				}
			`,
		},
		// Today the element store reports `borrowed value 'p' does not live long enough to escape the
		// function`. L1 lets a borrow outlive its owner, and the field store in the same column is
		// accepted.
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
		// Today any store into a module-level `&mut` binding reports `cannot constrain immutable object
		// <: mutable object`.
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
			want: []string{"4:13-4:19: cannot constrain immutable object <: mutable object"},
		},
		// Today returning `&mut` of a local also reports `cannot constrain immutable object <: mutable
		// object` at the return type.
		{
			column: "MutableBorrowThroughVal",
			site:   "Return",
			src: `
				fn g() -> &mut {x: number} {
					val p: {x: number} = {x: 1}
					return &mut p
				}
			`,
			want: []string{"4:13-4:19: cannot constrain immutable object <: mutable object"},
		},
		// Today the store into the module-level `&mut` binding also reports `cannot constrain immutable
		// object <: mutable object` on the whole assignment.
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
			want: []string{"5:13-5:19: cannot constrain immutable object <: mutable object"},
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
	})
}
*/

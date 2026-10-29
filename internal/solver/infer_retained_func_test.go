package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A parameter written `&'a fn (…) -> R` says the callee may keep the function for 'a.
// The `&'a` is a retention lifetime, not a borrow. TestInferRetainedFuncParam covers the
// signatures that carry one and asserts each renders back as written, with the lifetime
// named, elided, or quantified like a borrow's lifetime.
func TestInferRetainedFuncParam(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name:    "static retention on a declare fn",
			src:     `declare fn onClick(handler: &'static fn () -> undefined) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: &'static fn () -> undefined) -> undefined",
		},
		{
			name:    "static retention on a function with a body",
			src:     `fn f(g: &'static fn () -> undefined) { g() }`,
			binding: "f",
			want:    "fn (g: &'static fn () -> undefined) -> undefined",
		},
		// The retention lifetime and the borrow share 'a, so the signature quantifies it
		// once and names it at both positions.
		{
			name:    "named retention shared with a borrow",
			src:     `declare fn f<'a>(g: &'a fn () -> undefined, p: &'a {x: number}) -> undefined`,
			binding: "f",
			want:    "fn <'a>(g: &'a fn () -> undefined, p: &'a {x: number}) -> undefined",
		},
		// A retention lifetime that connects nothing elides to a bare `&`, the same way an
		// unconnected borrow lifetime does.
		{
			name:    "unconnected retention elides",
			src:     `declare fn f<'a>(g: &'a fn () -> undefined) -> undefined`,
			binding: "f",
			want:    "fn (g: &fn () -> undefined) -> undefined",
		},
		{
			name:    "bare retention",
			src:     `fn f(g: &fn () -> undefined) { g() }`,
			binding: "f",
			want:    "fn (g: &fn () -> undefined) -> undefined",
		},
		{
			name:    "retained function returned",
			src:     `fn f<'a>(g: &'a fn () -> undefined) -> &'a fn () -> undefined { return g }`,
			binding: "f",
			want:    "fn <'a>(g: &'a fn () -> undefined) -> &'a fn () -> undefined",
		},
		{
			name:    "retained generic callback",
			src:     `declare fn onClick(handler: &'static fn <T>(x: T) -> T) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: &'static fn <T>(x: T) -> T) -> undefined",
		},
		{
			name:    "retained function in a union",
			src:     `declare fn onClick(handler: (&'static fn () -> undefined) | number) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: (&'static fn () -> undefined) | number) -> undefined",
		},
		{
			name:    "retained function in a binding annotation",
			src:     `val h: &'static fn () -> undefined = fn () {}`,
			binding: "h",
			want:    "&'static fn () -> undefined",
		},
		// An alias whose body is a retained function substitutes its lifetime argument
		// into the retention lifetime.
		{
			name: "retained function behind a lifetime alias",
			src: `type Cb<'a> = &'a fn () -> undefined
declare fn onClick(handler: Cb<'static>) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: Cb<'static>) -> undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A function is a value type, so a call copies it into a `&'a fn` parameter with no
// written `&`. TestInferRetainedFuncArgNeedsNoBorrow covers calls that pass a function
// literal or a function-typed binding to such a parameter and asserts each is accepted.
func TestInferRetainedFuncArgNeedsNoBorrow(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "closure passed to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
}`,
		},
		{
			name: "closure passed beside a borrow sharing the lifetime",
			src: `declare fn f<'a>(g: &'a fn () -> undefined, p: &'a {x: number}) -> undefined
fn go() {
    val p = {x: 1}
    f(fn () {}, &p)
}`,
		},
		{
			name: "retained parameter passed on to a plain parameter",
			src: `fn h(k: fn () -> undefined) { k() }
fn f(g: &'static fn () -> undefined) { h(g) }`,
		},
		{
			name: "closure passed to a method",
			src: `class C { run<'a>(self, g: &'a fn (x: number) -> number) -> number { return g(1) } }
fn go(c: C) -> number { return c.run(fn (x) { return x }) }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
		})
	}
}

// TestInferRetainedFuncErrors covers the retention forms the checker rejects and asserts
// the full message of each.
func TestInferRetainedFuncErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "mutable borrow of a function",
			src:  `fn f(g: &mut fn () -> undefined) { g() }`,
			want: "Unsupported: mutable borrow of a function type",
		},
		{
			name: "two retention lifetimes",
			src:  `declare fn f(g: &'static (&'static fn () -> undefined)) -> undefined`,
			want: "Unsupported: second retention lifetime on a function type",
		},
		{
			name: "undeclared retention lifetime",
			src:  `fn f(g: &'a fn () -> undefined) { g() }`,
			want: "lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// TestRetainedFuncAtom asserts that a function type's retention lifetime keeps it apart from
// the same function type without one. The two compare unequal, sort apart, and do not fuse
// into one arrow.
func TestRetainedFuncAtom(t *testing.T) {
	fnWith := func(lt soltype.Lifetime) *soltype.FuncType {
		return &soltype.FuncType{Ret: &soltype.UndefinedType{}, Lt: lt}
	}
	retained := fnWith(soltype.Static)
	plain := fnWith(nil)

	require.False(t, equalType(retained, plain))
	require.NotZero(t, compareType(retained, plain))
	require.False(t, fusableFuncs(retained, plain))
}

// TestExtrudeRetainedFunc asserts that extruding a function type copies its retention
// lifetime down to the target level, the way a borrow's lifetime is copied.
func TestExtrudeRetainedFunc(t *testing.T) {
	c := &Context{}
	low := c.freshVar(0)
	lt := c.freshLifetime(1)
	fn := &soltype.FuncType{Ret: &soltype.UndefinedType{}, Lt: lt}

	require.Empty(t, c.Constrain(low, fn))

	require.Len(t, low.UpperBounds, 1)
	extruded, ok := low.UpperBounds[0].(*soltype.FuncType)
	require.True(t, ok)
	extrudedLt, ok := extruded.Lt.(*soltype.LifetimeVar)
	require.True(t, ok)
	require.NotSame(t, lt, extrudedLt)
	require.Equal(t, 0, extrudedLt.Level)
}

// TestExtrudeStaticRetainedFunc asserts that extruding a function type keeps a 'static
// retention lifetime as it is. The level-1 return forces the extrusion.
func TestExtrudeStaticRetainedFunc(t *testing.T) {
	c := &Context{}
	low := c.freshVar(0)
	fn := &soltype.FuncType{Ret: c.freshVar(1), Lt: soltype.Static}

	require.Empty(t, c.Constrain(low, fn))

	require.Len(t, low.UpperBounds, 1)
	extruded, ok := low.UpperBounds[0].(*soltype.FuncType)
	require.True(t, ok)
	require.Same(t, soltype.Static, extruded.Lt)
}

// A retention lifetime is covariant, like a borrow's, so a function flows into a retained slot
// only when it is kept at least that long. TestRetainedFuncSubtyping covers the signatures a
// body infers when it passes a parameter on to a `&'static fn` slot, and asserts each renders
// as written.
func TestRetainedFuncSubtyping(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// A bare `&` mints an inferred lifetime, and passing the parameter on to a `&'static fn`
		// slot forces it to 'static.
		{
			name: "bare retention passed on to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn f(g: &fn () -> undefined) { onClick(g) }`,
			want: "fn (g: &'static fn () -> undefined) -> undefined",
		},
		{
			name: "static retention passed on to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn f(g: &'static fn () -> undefined) { onClick(g) }`,
			want: "fn (g: &'static fn () -> undefined) -> undefined",
		},
		{
			name: "unannotated parameter passed on to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn f(g) { onClick(g) }`,
			want: "fn (g: &'static fn () -> undefined) -> undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A function parameter with no retention lifetime is only called during the call.
// TestCallOnlyFunc covers bodies that use such a parameter and asserts the full message, with
// its span, of each error they report.
func TestCallOnlyFunc(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "called",
			src:  `fn f(g: fn () -> undefined) { g() }`,
		},
		{
			name: "passed on to a parameter that only calls it",
			src: `declare fn each(f: fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) { each(g) }`,
		},
		// keep returns its argument at the argument's retention lifetime, and k is a local of
		// the body, so g is kept no longer than the call.
		{
			name: "kept by a local",
			src: `declare fn keep<'a>(f: &'a fn () -> undefined) -> &'a fn () -> undefined
fn f(g: fn () -> undefined) {
    val k = keep(g)
    k()
}`,
		},
		{
			name: "bound to a retained local",
			src: `fn f(g: fn () -> undefined) {
    val h: &fn () -> undefined = g
    h()
}`,
		},
		{
			name: "passed on to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) { onClick(g) }`,
			want: []string{"2:6-2:7: parameter 'g' is only called during the call, so it cannot be kept for 'static"},
		},
		{
			name: "named retention passed on to a static parameter",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
fn f<'a>(g: &'a fn () -> undefined) { onClick(g) }`,
			want: []string{"2:6-2:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
		{
			name: "stored into a static field of a borrowed parameter",
			src:  `fn f(g: fn () -> undefined, p: &mut {cb: &'static fn () -> undefined}) { p.cb = g }`,
			want: []string{"1:6-1:7: parameter 'g' is only called during the call, so it cannot be kept for 'static"},
		},
		{
			name: "stored into a retained field of a borrowed parameter",
			src:  `fn f<'a>(g: fn () -> undefined, p: &mut {cb: &'a fn () -> undefined}) { p.cb = g }`,
			want: []string{"1:10-1:11: parameter 'g' is only called during the call, so it cannot outlive the call"},
		},
		{
			name: "returned",
			src:  `fn f(g: fn () -> undefined) { return g }`,
			want: []string{"1:6-1:7: parameter 'g' is only called during the call, so it cannot outlive the call"},
		},
		// h is inferred together with f, so its parameter's lifetime is no longer than the call
		// of f.
		{
			name: "passed to a recursive sibling that only calls it",
			src: `fn f(g: fn () -> undefined, n: number) { if n > 0 { h(g, n) } }
fn h(x: &fn () -> undefined, n: number) {
    x()
    f(x, n - 1)
}`,
		},
		{
			name: "stored into a binding of the enclosing body",
			src: `fn outer() {
    var keep: &fn () -> undefined = fn () {}
    val inner = fn (g: fn () -> undefined) { keep = g }
    keep()
}`,
			want: []string{"3:21-3:22: parameter 'g' is only called during the call, so it cannot outlive the call"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A closure passed to a `&'static fn` parameter may run during any later call, so each later
// call writes what the closure writes. TestRetainedClosureBorrows covers the ways a call reaches
// such a parameter and asserts the full message, with its span, of each conflict reported.
func TestRetainedClosureBorrows(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// The first render runs before onClick keeps its handler, so only the second conflicts.
		{
			name: "call before the closure is retained",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn forEachItem(f: fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    forEachItem(fn () { count.n = count.n + 1 })
    render(&count)
    onClick(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"9:5-9:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "closure bound to a name",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    val h = fn () { count.n = count.n + 1 }
    onClick(h)
    render(&count)
}`,
			want: []string{"7:5-7:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "method that retains its argument",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
class Button { on(self, handler: &'static fn () -> undefined) -> undefined { onClick(handler) } }
fn setup(b: Button) {
    val mut count = {n: 0}
    b.on(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"7:5-7:19: this call may run the 'handler' that 'on' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "retention lifetime bound to outlive static",
			src: `declare fn onClick<'a: 'static>(handler: &'a fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"6:5-6:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "retained on one branch",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    if count.n > 0 {
        onClick(fn () { count.n = count.n + 1 })
    }
    render(&count)
}`,
			want: []string{"8:5-8:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "moved into a later call",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn take(view: {n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    take(count)
}`,
			want: []string{"6:10-6:15: cannot move 'count' in a call that may run the 'handler' that 'onClick' retains, which writes it"},
		},
		{
			name: "rest parameter",
			src: `declare fn onAll(...hs: Array<&'static fn () -> undefined>) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onAll(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"6:5-6:19: this call may run the 'hs' that 'onAll' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "union with null",
			src: `declare fn onClick(handler: (&'static fn () -> undefined) | null) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"6:5-6:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "object field",
			src: `declare fn mount(props: {onClick: &'static fn () -> undefined}) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    mount({onClick: fn () { count.n = count.n + 1 }})
    render(&count)
}`,
			want: []string{"6:5-6:19: this call may run the 'props' that 'mount' retains, which writes 'count' while it is borrowed as immutable"},
		},
		// A closure that only reads its capture needs it unchanged only while the closure runs,
		// and the frame cannot write it then.
		{
			name: "closure that only reads",
			src: `declare fn onClick(handler: &'static fn () -> undefined) -> undefined
declare fn bump(view: &mut {n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { val x = count.n })
    bump(&mut count)
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A closure passed to a `&'a fn` parameter holds its captures for 'a, which lasts as long as a
// binding that shares 'a is live. That binding is an argument or receiver whose parameter type
// mentions 'a, or the binding the call's result initializes when the return type mentions 'a.
// TestNamedRetentionBorrows covers each kind of holder and asserts the full message, with its
// span, of each conflict reported.
//
// A write to a captured local while the closure is retained is accepted, as E4 allows. A
// closure that writes the local holds a mutable path, and E2 allows several mutable paths at
// once. One that only reads it needs it unchanged only while it runs. So each rejected case
// takes an immutable borrow of a local the closure writes.
func TestNamedRetentionBorrows(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// The emitter borrow shares 'a with the handler, so the handler stays reachable while e
		// is live.
		{
			name: "borrowed object used after the conflicting borrow",
			src: `declare class Emitter { n: number }
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: &'a fn () -> undefined) -> undefined
declare fn emit(emitter: &mut Emitter) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup(em: &mut Emitter) {
    val mut count = {n: 0}
    val e = &mut em
    subscribe(e, fn () { count.n = count.n + 1 })
    render(&count)
    emit(e)
}`,
			want: []string{"9:5-9:19: this call may run the 'handler' that 'subscribe' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "borrowed object dead before the borrow",
			src: `declare class Emitter { n: number }
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: &'a fn () -> undefined) -> undefined
declare fn emit(emitter: &mut Emitter) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup(em: &mut Emitter) {
    val mut count = {n: 0}
    val e = &mut em
    subscribe(e, fn () { count.n = count.n + 1 })
    emit(e)
    render(&count)
}`,
		},
		{
			name: "borrowed object live across a write of the captured local",
			src: `declare class Emitter { n: number }
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: &'a fn () -> undefined) -> undefined
declare fn emit(emitter: &mut Emitter) -> undefined
fn setup(em: &mut Emitter) {
    val mut count = {n: 0}
    val e = &mut em
    subscribe(e, fn () { val x = count.n })
    count.n = 5
    emit(e)
}`,
		},
		// The result shares 'a with f, so the closure stays reachable while lazy is live.
		{
			name: "returned holder used after the conflicting borrow",
			src: `declare class Lazy<'a> { force(&self) -> number }
declare fn mapLazy<'a>(xs: &'a Array<number>, f: &'a fn (x: number) -> number) -> Lazy<'a>
declare fn render(view: &{n: number}) -> undefined
fn go() -> number {
    val xs = [1, 2, 3]
    val mut count = {n: 0}
    val lazy = mapLazy(&xs, fn (x) {
        count.n = count.n + 1
        return x
    })
    render(&count)
    return lazy.force()
}`,
			want: []string{"11:5-11:19: this call may run the 'f' that 'mapLazy' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "returned holder dead before the borrow",
			src: `declare class Lazy<'a> { force(&self) -> number }
declare fn mapLazy<'a>(xs: &'a Array<number>, f: &'a fn (x: number) -> number) -> Lazy<'a>
declare fn render(view: &{n: number}) -> undefined
fn go() -> number {
    val xs = [1, 2, 3]
    val mut count = {n: 0}
    val lazy = mapLazy(&xs, fn (x) {
        count.n = count.n + 1
        return x
    })
    val n = lazy.force()
    render(&count)
    return n
}`,
		},
		// The constructor returns Button<'a>, so b holds the handler.
		{
			name: "class constructed with a handler",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    onClick: &'a fn () -> undefined,
    click(&self) -> undefined { self.onClick() },
}
fn setup() {
    val mut count = {n: 0}
    val b = Button(fn () { count.n = count.n + 1 })
    render(&count)
    b.click()
}`,
			want: []string{"9:5-9:19: this call may run the 'onClick' that 'Button' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "class constructed with a handler and dead before the borrow",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    onClick: &'a fn () -> undefined,
    click(&self) -> undefined { self.onClick() },
}
fn setup() {
    val mut count = {n: 0}
    val b = Button(fn () { count.n = count.n + 1 })
    b.click()
    render(&count)
}`,
		},
		// The receiver's type is Button<'a>, so b holds the handler on() stores.
		{
			name: "class method that stores a handler",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    handler: &'a fn () -> undefined,
    on(&mut self, h: &'a fn () -> undefined) -> undefined { self.handler = h },
    click(&self) -> undefined { self.handler() },
}
fn setup() {
    val mut count = {n: 0}
    val mut b = Button(fn () {})
    b.on(fn () { count.n = count.n + 1 })
    render(&count)
    b.click()
}`,
			want: []string{"11:5-11:19: this call may run the 'h' that 'on' retains, which writes 'count' while it is borrowed as immutable"},
		},
		// The borrow of em ends with the call, but em's own type mentions 'a, so em holds the
		// handler.
		{
			name: "borrowed object whose type holds the handler",
			src: `declare fn render(view: &{n: number}) -> undefined
class Emitter<'a> {
    handler: &'a fn () -> undefined,
    emit(&self) -> undefined { self.handler() },
}
declare fn subscribe<'a>(emitter: &mut Emitter<'a>, handler: &'a fn () -> undefined) -> undefined
fn setup() {
    val mut count = {n: 0}
    val mut em = Emitter(fn () {})
    subscribe(&mut em, fn () { count.n = count.n + 1 })
    render(&count)
    em.emit()
}`,
			want: []string{"11:5-11:19: this call may run the 'handler' that 'subscribe' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "holder reassigned",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    onClick: &'a fn () -> undefined,
    click(&self) -> undefined { self.onClick() },
}
fn setup() {
    val mut count = {n: 0}
    var b = Button(fn () {})
    b.click()
    b = Button(fn () { count.n = count.n + 1 })
    render(&count)
    b.click()
}`,
			want: []string{"11:5-11:19: this call may run the 'onClick' that 'Button' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "holder copied",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    onClick: &'a fn () -> undefined,
    click(&self) -> undefined { self.onClick() },
}
fn setup() {
    val mut count = {n: 0}
    val b = Button(fn () { count.n = count.n + 1 })
    val c = b
    render(&count)
    c.click()
}`,
			want: []string{"10:5-10:19: this call may run the 'onClick' that 'Button' retains, which writes 'count' while it is borrowed as immutable"},
		},
		// No binding shares 'a, so the callee keeps the handler no longer than the call.
		{
			name: "retention lifetime no binding shares",
			src: `declare fn run<'a>(handler: &'a fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    run(fn () { count.n = count.n + 1 })
    render(&count)
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

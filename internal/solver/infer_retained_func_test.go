package solver

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A function type written `'a fn (…) -> R` may hold borrows that live for 'a. One written
// `fn (…) -> R` with no lifetime takes its lifetime by where it is written, the way an elided
// `&` does. In a parameter the lifetime is fresh and ends with the call. In a return type it is
// fresh when the signature has an input lifetime and 'static otherwise. In a module-level
// binding's annotation, or a field or element inside one, it is 'static. In an annotation
// inside a body it is fresh. TestInferRetainedFuncParam covers each of those positions and the
// written forms, and asserts each renders as expected, with a lifetime named, elided, or
// quantified like a borrow's lifetime.
func TestInferRetainedFuncParam(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name:    "static lifetime on a declare fn",
			src:     `declare fn onClick(handler: 'static fn () -> undefined) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: 'static fn () -> undefined) -> undefined",
		},
		{
			name:    "static lifetime on a function with a body",
			src:     `fn f(g: 'static fn () -> undefined) { g() }`,
			binding: "f",
			want:    "fn (g: 'static fn () -> undefined) -> undefined",
		},
		// The function's lifetime and the borrow share 'a, so the signature quantifies it once
		// and names it at both positions.
		{
			name:    "named lifetime shared with a borrow",
			src:     `declare fn f<'a>(g: 'a fn () -> undefined, p: &'a {x: number}) -> undefined`,
			binding: "f",
			want:    "fn <'a>(g: 'a fn () -> undefined, p: &'a {x: number}) -> undefined",
		},
		// A lifetime that connects nothing elides, the same way an unconnected borrow lifetime
		// does.
		{
			name:    "unconnected named lifetime elides",
			src:     `declare fn f<'a>(g: 'a fn () -> undefined) -> undefined`,
			binding: "f",
			want:    "fn (g: fn () -> undefined) -> undefined",
		},
		{
			name:    "elided lifetime on a parameter",
			src:     `fn f(g: fn () -> undefined) { g() }`,
			binding: "f",
			want:    "fn (g: fn () -> undefined) -> undefined",
		},
		{
			name:    "elided lifetime on a declared parameter",
			src:     `declare fn run(cb: fn () -> undefined) -> undefined`,
			binding: "run",
			want:    "fn (cb: fn () -> undefined) -> undefined",
		},
		{
			name:    "named lifetime returned",
			src:     `fn f<'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined { return g }`,
			binding: "f",
			want:    "fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined",
		},
		{
			name:    "static generic callback",
			src:     `declare fn onClick(handler: 'static fn <T>(x: T) -> T) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: 'static fn <T>(x: T) -> T) -> undefined",
		},
		{
			name:    "static function in a union",
			src:     `declare fn onClick(handler: ('static fn () -> undefined) | number) -> undefined`,
			binding: "onClick",
			want:    "fn (handler: ('static fn () -> undefined) | number) -> undefined",
		},
		{
			name:    "static function in a binding annotation",
			src:     `val h: 'static fn () -> undefined = fn () {}`,
			binding: "h",
			want:    "'static fn () -> undefined",
		},
		// A module-level binding lives for the whole program, so a function type written with no
		// lifetime in its annotation is 'static, as is one in a field or an element inside it.
		{
			name:    "elided lifetime in a module-level var",
			src:     `var sink: fn () -> undefined = fn () {}`,
			binding: "sink",
			want:    "fn () -> undefined",
		},
		{
			name:    "elided lifetime in a module-level array",
			src:     `var mut handlers: Array<fn () -> undefined> = []`,
			binding: "handlers",
			want:    "Array<fn () -> undefined>",
		},
		{
			name:    "elided lifetime in a module-level object field",
			src:     `var mut registry: {cb: fn () -> undefined} = {cb: fn () {}}`,
			binding: "registry",
			want:    "{cb: fn () -> undefined}",
		},
		// A signature with no input lifetime has nothing a returned function could borrow from,
		// so an elided return is 'static.
		{
			name:    "elided lifetime in a return annotation with no input lifetime",
			src:     `fn mk() -> fn () -> undefined { return fn () {} }`,
			binding: "mk",
			want:    "fn () -> fn () -> undefined",
		},
		{
			name:    "elided lifetime in a declared return annotation with no input lifetime",
			src:     `declare fn mk() -> fn () -> undefined`,
			binding: "mk",
			want:    "fn () -> fn () -> undefined",
		},
		// An overload's signature is bound from its annotations, not from its body, so the
		// first arm renders as written although its body returns g.
		{
			name: "elided lifetime in an overload's return annotation",
			src: `fn mk(g: fn () -> undefined) -> fn () -> undefined { return g }
fn mk(x: number) -> fn () -> undefined { return fn () {} }`,
			binding: "mk",
			want:    "(fn (g: fn () -> undefined) -> fn () -> undefined) & (fn (x: number) -> fn () -> undefined)",
		},
		// A returned parameter ties its lifetime to the result, the way a returned `&` parameter
		// does. An elided return annotation takes the same lifetime as the parameter.
		{
			name:    "elided parameter returned",
			src:     `fn f(g: fn () -> undefined) { return g }`,
			binding: "f",
			want:    "fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined",
		},
		{
			name:    "elided parameter returned through an elided return annotation",
			src:     `fn f(g: fn () -> undefined) -> fn () -> undefined { return g }`,
			binding: "f",
			want:    "fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined",
		},
		{
			name:    "elided parameter captured by a returned closure",
			src:     `fn f(g: fn () -> undefined) { return fn () { g() } }`,
			binding: "f",
			want:    "fn <'a>(g: 'a fn () -> undefined) -> 'a fn () -> undefined",
		},
		// A binding annotation inside a body takes a fresh lifetime, so the local holds the
		// call-only parameter without keeping it past the call.
		{
			name: "elided lifetime in a body-level binding annotation",
			src: `fn f(g: fn () -> undefined) {
    val k: fn () -> undefined = g
    val ks: Array<fn () -> undefined> = [g]
    k()
}`,
			binding: "f",
			want:    "fn (g: fn () -> undefined) -> undefined",
		},
		// A returned closure takes the locals it captures, as F3 states, so it holds no borrow.
		{
			name: "returned closure that captures an object local",
			src: `fn mk() {
    val o = {x: 1}
    return fn () { return o.x }
}`,
			binding: "mk",
			want:    "fn () -> fn () -> 1",
		},
		{
			name: "returned closure that writes a var local through a binding",
			src: `fn counter() {
    var n = 0
    val f = fn () {
        n = n + 1
        return n
    }
    return f
}`,
			binding: "counter",
			want:    "fn () -> fn () -> number",
		},
		// A closure returned from a returned closure borrows what the returned closure took, which
		// every call of it shares, so it keeps its lifetime. The lifetime connects nothing in the
		// signature, so it elides. TestReturnedClosureLifetime asserts which of these results
		// hold a borrow.
		{
			name: "closure returned from a returned closure",
			src: `fn outer() {
    val x = {n: 1}
    return fn () { return fn () { return x.n } }
}`,
			binding: "outer",
			want:    "fn () -> fn () -> fn () -> 1",
		},
		// A returned closure takes a local closure it captures, along with what that closure
		// borrows.
		{
			name: "returned closure that captures a local closure",
			src: `fn outer() {
    val x = {n: 1}
    val inner = fn () { return x.n }
    return fn () { return inner }
}`,
			binding: "outer",
			want:    "fn () -> fn () -> fn () -> 1",
		},
		// A returned closure takes a local it captures, but a borrow inside the local still
		// reaches what it borrows from outside the body.
		{
			name: "returned closure that captures an object holding a borrowed parameter",
			src: `fn f(p: &mut {a: number}) {
    val o = {r: p}
    return fn () { return o.r.a }
}`,
			binding: "f",
			want:    "fn (p: &mut {a: number}) -> fn () -> number",
		},
		{
			name: "returned closure that captures an array holding a borrowed parameter",
			src: `fn f(p: &{a: number}) {
    val o = [p]
    return fn () { return o[0].a }
}`,
			binding: "f",
			want:    "fn (p: &{a: number}) -> fn () -> number",
		},
		{
			name: "returned closure that captures a tuple holding a borrowed parameter",
			src: `fn f(p: &{a: number}) {
    val o: [&{a: number}, number] = [p, 1]
    return fn () { return o[1] }
}`,
			binding: "f",
			want:    "fn (p: &{a: number}) -> fn () -> number",
		},
		{
			name: "returned closure that captures a union holding a borrowed parameter",
			src: `fn f(p: &{a: number}, c: boolean) {
    val o: {r: &{a: number}} | number = if c { {r: p} } else { 5 }
    return fn () { return o }
}`,
			binding: "f",
			want:    "fn <'a>(p: &'a {a: number}, c: boolean) -> fn () -> {r: &'a {a: number}} | number",
		},
		{
			name: "returned closure that captures a borrow through a borrowed parameter",
			src: `fn f(p: &{a: {b: number}}) {
    val o = {r: &p.a}
    return fn () { return o.r.b }
}`,
			binding: "f",
			want:    "fn (p: &{a: {b: number}}) -> fn () -> number",
		},
		// q's type is a variable that a borrow annotation bounds, so q holds a caller's borrow.
		{
			name: "returned closure that captures an object holding a parameter used as a borrow",
			src: `fn f(p: &{a: number}, q) {
    val o = {r: q}
    val z: &{a: number} = o.r
    return fn () { return o.r }
}`,
			binding: "f",
			want:    "fn <T0>(p: &{a: number}, q: T0 & &{a: number}) -> fn () -> T0",
		},
		// x is a local of the body, so a returned closure takes it along with the borrow of it.
		{
			name: "returned closure that captures an object holding a borrow of a local",
			src: `fn f() {
    val x = {a: 1}
    val o = {r: &x}
    return fn () { return o.r.a }
}`,
			binding: "f",
			want:    "fn () -> fn () -> 1",
		},
		{
			name: "returned local that joins two closures",
			src: `fn mk(c: boolean) {
    val o = {x: 1}
    val a = fn () { return o.x }
    val b = fn () { return 2 }
    val h = if c { a } else { b }
    return h
}`,
			binding: "mk",
			want:    "fn (c: boolean) -> (fn () -> 1) | (fn () -> 2)",
		},
		{
			name: "returned local that joins a closure and a value",
			src: `fn mk(c: boolean) {
    val o = {x: 1}
    val a = fn () { return o.x }
    val h = if c { a } else { 5 }
    return h
}`,
			binding: "mk",
			want:    "fn (c: boolean) -> (fn () -> 1) | 5",
		},
		{
			name: "returned local that joins a closure and a call-only parameter",
			src: `fn mk(c: boolean, g: fn () -> number) {
    val o = {x: 1}
    val a = fn () { return o.x }
    val h = if c { a } else { g }
    return h
}`,
			binding: "mk",
			want:    "fn <'a>(c: boolean, g: 'a fn () -> number) -> (fn () -> 1) | ('a fn () -> number)",
		},
		{
			name: "returned object that holds a closure",
			src: `fn mk() {
    val o = {x: 1}
    val f = fn () { return o.x }
    return {cb: f, n: 1}
}`,
			binding: "mk",
			want:    "fn () -> {cb: fn () -> 1, n: 1}",
		},
		// A call only runs its callee, so an unannotated parameter that is only called takes a
		// function that may hold borrows.
		{
			name:    "unannotated parameter only called",
			src:     `fn f(g) { return g() }`,
			binding: "f",
			want:    "fn <T0>(g: fn () -> T0) -> T0",
		},
		// A parameter stored into a module-level slot must hold no borrow, so it renders
		// `'static fn`. A bare `fn` would read as a parameter only called during the call.
		{
			name: "unannotated parameter stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
fn f(g) { sink = g }`,
			binding: "f",
			want:    "fn (g: 'static fn () -> undefined) -> undefined",
		},
		// An alias whose body is a function with a lifetime substitutes its lifetime argument
		// into the function's lifetime.
		{
			name: "function behind a lifetime alias",
			src: `type Cb<'a> = 'a fn () -> undefined
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

// A function is a value type, so a call copies it into a `'a fn` parameter with no
// written `&`. TestInferRetainedFuncArgNeedsNoBorrow covers calls that pass a function
// literal or a function-typed binding to such a parameter and asserts each is accepted.
//
// A `'static fn` parameter accepts a closure that borrows nothing shorter than the program,
// such as one over a module-level binding.
func TestInferRetainedFuncArgNeedsNoBorrow(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
val mut count = {n: 0}
fn setup() {
    onClick(fn () { count.n = count.n + 1 })
}`,
		},
		{
			name: "closure passed beside a borrow sharing the lifetime",
			src: `declare fn f<'a>(g: 'a fn () -> undefined, p: &'a {x: number}) -> undefined
fn go() {
    val p = {x: 1}
    f(fn () {}, &p)
}`,
		},
		{
			name: "static parameter passed on to a call-only parameter",
			src: `fn h(k: fn () -> undefined) { k() }
fn f(g: 'static fn () -> undefined) { h(g) }`,
		},
		{
			name: "call-only parameter passed on to a call-only parameter",
			src: `fn h(k: fn () -> undefined) { k() }
fn f(g: fn () -> undefined) { h(g) }`,
		},
		{
			name: "closure passed to a method",
			src: `class C { run<'a>(self, g: 'a fn (x: number) -> number) -> number { return g(1) } }
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

// A function is not borrowed, so `&` before a function type is the same error as `&` before a
// primitive. TestInferRetainedFuncErrors covers the function type forms the checker rejects and
// asserts the full message of each.
func TestInferRetainedFuncErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "borrow of a function",
			src:  `fn f(g: &fn () -> undefined) { g() }`,
			want: "Unsupported: borrow of a non-borrowable type",
		},
		{
			name: "borrow of a function with a lifetime",
			src:  `fn f<'a>(g: &'a fn () -> undefined) { g() }`,
			want: "Unsupported: borrow of a non-borrowable type",
		},
		{
			name: "mutable borrow of a function",
			src:  `fn f(g: &mut fn () -> undefined) { g() }`,
			want: "Unsupported: borrow of a non-borrowable type",
		},
		{
			name: "undeclared lifetime",
			src:  `fn f(g: 'a fn () -> undefined) { g() }`,
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

// A function's lifetime is covariant, like a borrow's, so a function flows into a slot only
// when its borrows live at least as long as the slot's lifetime. TestRetainedFuncSubtyping
// covers the signatures a body infers when it passes a parameter on to a 'static slot, and
// asserts each renders as written.
func TestRetainedFuncSubtyping(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// A function in a module-level slot holds no borrow, so it passes to a `'static fn`
		// parameter.
		{
			name: "module-level function passed on to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
val handle: fn () -> undefined = fn () {}
fn f() { onClick(handle) }`,
			want: "fn () -> undefined",
		},
		{
			name: "static parameter passed on to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: 'static fn () -> undefined) { onClick(g) }`,
			want: "fn (g: 'static fn () -> undefined) -> undefined",
		},
		{
			name: "unannotated parameter passed on to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g) { onClick(g) }`,
			want: "fn (g: 'static fn () -> undefined) -> undefined",
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

// A function parameter written `fn` with no lifetime is only called during the call, unless
// the signature ties its lifetime to the result. TestCallOnlyFunc covers bodies that use such a
// parameter and asserts the full message, with its span, of each error they report.
func TestCallOnlyFunc(t *testing.T) {
	const static = "'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g: 'static fn () -> undefined` to keep it"
	const kept = "'g' may borrow values that live only during the call, but it is kept after the call returns; write `g: 'static fn () -> undefined` to keep it"
	const deferred = "'g' may borrow values that live only during the call, but it is read by %s, which can run after the call returns; write `g: 'static fn () -> undefined` to keep it"
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
			src: `declare fn keep<'a>(f: 'a fn () -> undefined) -> 'a fn () -> undefined
fn f(g: fn () -> undefined) {
    val k = keep(g)
    k()
}`,
		},
		{
			name: "bound to an unannotated local",
			src: `fn f(g: fn () -> undefined) {
    val k = g
    k()
}`,
		},
		// A binding annotation inside a body takes a fresh lifetime, so the local may hold g.
		{
			name: "bound to an annotated local",
			src: `fn f(g: fn () -> undefined) {
    val h: fn () -> undefined = g
    h()
}`,
		},
		// The signature ties g's lifetime to the result, so a caller keeps the result only
		// while g's borrows live.
		{
			name: "returned",
			src:  `fn f(g: fn () -> undefined) { return g }`,
		},
		// p's field takes a lifetime of its own, which the caller reads after the call, so g
		// must outlive the call.
		{
			name: "stored into an elided field of a borrowed parameter",
			src:  `fn f(g: fn () -> undefined, p: &mut {cb: fn () -> undefined}) { p.cb = g }`,
			want: []string{"1:6-1:7: " + kept},
		},
		{
			name: "returned through an elided return annotation",
			src:  `fn f(g: fn () -> undefined) -> fn () -> undefined { return g }`,
		},
		// An overload's body is checked against the arm's annotations, whose elided return
		// takes the parameter's lifetime too.
		{
			name: "returned through an overload's elided return annotation",
			src: `fn f(g: fn () -> undefined) -> fn () -> undefined { return g }
fn f(x: number) -> fn () -> undefined { return fn () {} }`,
		},
		{
			name: "captured by a returned closure",
			src:  `fn f(g: fn () -> undefined) { return fn () { g() } }`,
		},
		{
			name: "passed on to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) { onClick(g) }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "named lifetime passed on to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f<'a>(g: 'a fn () -> undefined) { onClick(g) }`,
			want: []string{"2:6-2:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
		{
			name: "stored into a static field of a borrowed parameter",
			src:  `fn f(g: fn () -> undefined, p: &mut {cb: 'static fn () -> undefined}) { p.cb = g }`,
			want: []string{"1:6-1:7: " + static},
		},
		// p holds g for 'a, which the signature names, so the fix names 'a too.
		{
			name: "stored into a named-lifetime field of a borrowed parameter",
			src:  `fn f<'a>(g: fn () -> undefined, p: &mut {cb: 'a fn () -> undefined}) { p.cb = g }`,
			want: []string{"1:10-1:11: 'g' may borrow values that live only during the call, but it is kept after the call returns; write `g: 'a fn () -> undefined` to keep it"},
		},
		// h is inferred together with f, so its parameter's lifetime is no longer than the call
		// of f.
		{
			name: "passed to a recursive sibling that only calls it",
			src: `fn f(g: fn () -> undefined, n: number) { if n > 0 { h(g, n) } }
fn h(x: fn () -> undefined, n: number) {
    x()
    f(x, n - 1)
}`,
		},
		{
			name: "stored into a binding of the enclosing body",
			src: `fn outer() {
    var keep: fn () -> undefined = fn () {}
    val inner = fn (g: fn () -> undefined) { keep = g }
    keep()
}`,
			want: []string{"3:21-3:22: " + kept},
		},
		// 'a belongs to outer, so inner's parameter lifetime would have to outlive a lifetime
		// of the enclosing body.
		{
			name: "stored into a named-lifetime field of an enclosing function's parameter",
			src: `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined}) {
    val inner = fn (g: fn () -> undefined) { p.cb = g }
}`,
			want: []string{"2:21-2:22: " + kept},
		},
		// A closure that calls g holds g's borrows, so it may be kept no longer than g.
		{
			name: "captured by a closure that is only called",
			src: `fn f(g: fn () -> undefined) {
    val h = fn () { g() }
    h()
}`,
		},
		{
			name: "captured by a closure passed to a parameter that only calls it",
			src: `declare fn each(f: fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) { each(fn () { g() }) }`,
		},
		{
			name: "captured by a closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) { onClick(fn () { g() }) }`,
			want: []string{"2:6-2:7: " + static},
		},
		// A `'static` parameter may be kept forever, so a closure that captures it may be too.
		{
			name: "static parameter captured by a closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
var sink: 'static fn () -> undefined = fn () {}
fn f(g: 'static fn () -> undefined) {
    onClick(fn () { g() })
    onClick(g)
    sink = fn () { g() }
}`,
		},
		{
			name: "captured by a named closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) {
    val h = fn () { g() }
    onClick(h)
}`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "captured through a second closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) {
    val h = fn () { g() }
    onClick(fn () { h() })
}`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "captured in an object by a closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f(g: fn () -> undefined) {
    val o = {cb: g}
    onClick(fn () { o.cb() })
}`,
			want: []string{"4:13-4:29: this closure borrows `o`, but `onClick` keeps it for 'static; pass a function that borrows nothing", "2:6-2:7: 'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g: 'static fn () -> undefined` to keep it"},
		},
		{
			name: "stored into a static module-level var",
			src: `var sink: 'static fn () -> undefined = fn () {}
fn f(g: fn () -> undefined) { sink = g }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "stored into a static module-level var through an annotated local",
			src: `var sink: 'static fn () -> undefined = fn () {}
fn f(g: fn () -> undefined) {
    val k: fn () -> undefined = g
    sink = k
}`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "pushed onto a static module-level array",
			src: `var mut handlers: Array<'static fn () -> undefined> = []
fn f(g: fn () -> undefined) { handlers.push(g) }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "stored into a static field of a module-level object",
			src: `var mut registry: {cb: 'static fn () -> undefined} = {cb: fn () {}}
fn f(g: fn () -> undefined) { registry.cb = g }`,
			want: []string{"2:6-2:7: " + static},
		},
		// A module-level slot written with no lifetime holds its function for 'static too.
		{
			name: "stored into a module-level var with an elided lifetime",
			src: `var sink: fn () -> undefined = fn () {}
fn f(g: fn () -> undefined) { sink = g }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "pushed onto a module-level array with an elided lifetime",
			src: `var mut handlers: Array<fn () -> undefined> = []
fn f(g: fn () -> undefined) { handlers.push(g) }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "stored into a module-level object field with an elided lifetime",
			src: `var mut registry: {cb: fn () -> undefined} = {cb: fn () {}}
fn f(g: fn () -> undefined) { registry.cb = g }`,
			want: []string{"2:6-2:7: " + static},
		},
		// A class field and a type alias hold their function for 'static when the lifetime is
		// elided, the way a module-level binding does.
		{
			name: "stored into a class field with an elided lifetime",
			src: `class Button { onClick: fn () -> undefined }
fn f(g: fn () -> undefined) { return Button(g) }`,
			want: []string{"2:6-2:7: " + static},
		},
		{
			name: "passed on to a parameter typed with an alias",
			src: `type Handler = fn () -> undefined
declare fn onClick(handler: Handler) -> undefined
fn f(g: fn () -> undefined) { onClick(g) }`,
			want: []string{"3:6-3:7: " + static},
		},
		// A generator's body starts on the first `next()`, after the call that made it returns.
		{
			name: "read by a generator",
			src: `gen fn f(g: fn () -> undefined) {
    g()
    yield 1
}`,
			want: []string{"2:5-2:6: " + fmt.Sprintf(deferred, "a generator")},
		},
		// An async body resumes after each `await`, once the call has returned.
		{
			name: "read by an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f(g: fn () -> undefined) {
    await tick()
    g()
}`,
			want: []string{"4:5-4:6: " + fmt.Sprintf(deferred, "an async function")},
		},
		{
			name: "read by a closure in an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f(g: fn () -> undefined) {
    val h = fn () { g() }
    await tick()
    h()
}`,
			want: []string{"3:21-3:22: " + fmt.Sprintf(deferred, "an async function")},
		},
		{
			name: "read by an async closure",
			src: `declare fn tick() -> Promise<undefined>
fn f(g: fn () -> undefined) {
    val h = async fn () {
        await tick()
        g()
    }
    h()
}`,
			want: []string{"5:9-5:10: " + fmt.Sprintf(deferred, "an async function")},
		},
		// The message names the first deferred body that reads g, here the async closure.
		{
			name: "read by an async closure and then a generator closure",
			src: `fn f(g: fn () -> undefined) {
    val h = async fn () { g() }
    val k = gen fn () {
        g()
        yield 1
    }
}`,
			want: []string{"2:27-2:28: " + fmt.Sprintf(deferred, "an async function")},
		},
		// An object, an array, and a closure that hold g each carry g into the async body.
		{
			name: "read through an object field by an async closure",
			src: `fn f(g: fn () -> undefined) {
    val o = {cb: g}
    val p = async fn () { o.cb() }
}`,
			want: []string{"3:27-3:28: " + fmt.Sprintf(deferred, "an async function")},
		},
		{
			name: "read through an array element by an async closure",
			src: `fn f(g: fn () -> undefined) {
    val arr = [g]
    val p = async fn () { arr[0]() }
}`,
			want: []string{"3:27-3:30: " + fmt.Sprintf(deferred, "an async function")},
		},
		{
			name: "read through a wrapping closure by an async closure",
			src: `fn f(g: fn () -> undefined) {
    val h = fn () { g() }
    val p = async fn () { h() }
}`,
			want: []string{"3:27-3:28: " + fmt.Sprintf(deferred, "an async function")},
		},
		// g reaches h only after the async closure reads h, and the error still reports g, since
		// the `var` keeps it past the call.
		{
			name: "assigned to a local an async closure read earlier",
			src: `fn f(g: fn () -> undefined) {
    var h: fn () -> undefined = fn () {}
    val p = async fn () { h() }
    h = g
}`,
			want: []string{"1:6-1:7: " + kept},
		},
		// h's annotation gives it a lifetime of its own, which g's bounds from below.
		{
			name: "read through an annotated local by an async closure",
			src: `fn f(g: fn () -> undefined) {
    val h: fn () -> undefined = g
    val p = async fn () { h() }
}`,
			want: []string{"3:27-3:28: " + fmt.Sprintf(deferred, "an async function")},
		},
		// The fix keeps the parameter's `?`.
		{
			name: "optional parameter stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
fn f(g?: fn () -> undefined) { if g != undefined { sink = g } }`,
			want: []string{"2:6-2:7: 'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g?: 'static fn () -> undefined` to keep it"},
		},
		// A closure's own parameter is bound inside the async body, so the async body does not
		// outlive the closure's call.
		{
			name: "read by a closure's own parameter inside an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f() {
    val run = fn (g: fn () -> undefined) { g() }
    await tick()
    run(fn () {})
}`,
		},
		{
			name: "unread by an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f(g: fn () -> undefined) { await tick() }`,
		},
		// A `'static fn` parameter's borrows live for the whole program, so an async body may
		// read it after the call returns.
		{
			name: "static parameter read by an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f(g: 'static fn () -> undefined) {
    await tick()
    g()
}`,
		},
		{
			name: "named lifetime read by an async function",
			src: `declare fn tick() -> Promise<undefined>
async fn f<'a>(g: 'a fn () -> undefined) {
    await tick()
    g()
}`,
		},
		{
			name: "named lifetime captured by a closure passed to a static parameter",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
fn f<'a>(g: 'a fn () -> undefined) { onClick(fn () { g() }) }`,
			want: []string{"2:6-2:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A deferred read of a call-only parameter blames the read, and any other kept parameter blames
// the parameter. TestCallOnlyFuncDeferredRelated asserts that the first error relates the
// parameter and that the second relates nothing.
func TestCallOnlyFuncDeferredRelated(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "read by a generator",
			src: `gen fn f(g: fn () -> undefined) {
    g()
    yield 1
}`,
			want: []string{"1:10-1:11"},
		},
		{
			name: "stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
fn f(g: fn () -> undefined) { sink = g }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), benchTree, Options{})
			require.Len(t, res.Errors, 1)
			var related []string
			for _, span := range res.Errors[0].Related() {
				related = append(related, spanWithSpanSource(t, span))
			}
			require.Equal(t, tt.want, related)
		})
	}
}

// A function type written with no lifetime in a module-level binding's annotation is 'static,
// so it holds only a function that borrows nothing shorter than the program, as E4 states. A
// slot written `'static fn` holds the same functions. A closure that captures nothing, or only
// reads value types from `val` bindings, holds no borrow. One that captures an object local, or
// reads or writes a captured `var`, borrows it, and such a slot rejects it.
// TestModuleLevelFuncHoldsNoBorrow covers the E4 example, the captures that decide whether a
// closure fits such a slot, and each kind of slot, and asserts the full message, with its span,
// of each error.
func TestModuleLevelFuncHoldsNoBorrow(t *testing.T) {
	const decls = `var sink: fn () -> undefined = fn () {}
declare fn run(cb: fn () -> undefined) -> undefined
declare fn keep(cb: 'static fn () -> undefined) -> undefined
`
	const fix = "pass a function that borrows nothing"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "closure that captures nothing",
			src: decls + `fn setup() {
    sink = fn () { console.log("hello") }
    run(fn () { console.log("hello") })
}`,
		},
		{
			name: "closure that copies a value type",
			src: decls + `fn setup() {
    val msg = "hello"
    val greet = fn () { console.log(msg) }
    sink = greet
    run(greet)
}`,
		},
		{
			name: "closure over a module-level object passed to a static parameter",
			src: decls + `val mut count = {n: 0}
fn setup() {
    keep(fn () { count.n = count.n + 1 })
}`,
		},
		// A local initialized from a call holds only the value the call returns, which here is
		// a number the closure copies.
		{
			name: "closure that copies a value type returned by a call",
			src: decls + `declare fn count() -> number
fn setup() {
    val n = count()
    keep(fn () { console.log(n) })
}`,
		},
		// A parameter's default is a closure over the parameters before it.
		{
			name: "closure in a parameter default that reads a borrowed parameter",
			src: decls + `fn setup(x: &{n: number}, g = fn () { console.log(x.n) }) {
    keep(g)
}`,
			want: []string{"4:31-4:57: this closure borrows `x`, but `keep` keeps it for 'static; " + fix},
		},
		// A module-level binding lives for the whole program, so reading one holds no borrow.
		{
			name: "closure that reads a module-level object",
			src: decls + `val cfg = {msg: "hello"}
fn setup() {
    sink = fn () { console.log(cfg.msg) }
}`,
		},
		// An unannotated parameter's type settles after the closure is written, so whether the
		// closure copies it or borrows it is decided once the body is checked.
		{
			name: "closure that captures an unannotated parameter later used as a number",
			src: decls + `fn setup(n) {
    sink = fn () { console.log(n) }
    return n + 1
}`,
		},
		{
			name: "closure that captures an unannotated parameter already used as a number",
			src: decls + `fn setup(n) {
    val m: number = n
    sink = fn () { console.log(n) }
}`,
		},
		{
			name: "bound closure that captures an unannotated parameter used as a number",
			src: decls + `fn setup(n) {
    val show = fn () { console.log(n) }
    sink = show
    return n * 2
}`,
		},
		{
			name: "closure that captures an unannotated parameter later used as an object",
			src: decls + `fn setup(n) {
    sink = fn () { console.log(n) }
    return n.x
}`,
			want: []string{"5:12-5:36: this closure borrows `n`, but `sink` keeps it for 'static; " + fix},
		},
		{
			name: "bound closure that captures an unannotated parameter used as an object",
			src: decls + `fn setup(n) {
    val show = fn () { console.log(n) }
    sink = show
    return n.x
}`,
			want: []string{"6:12-6:16: `show` borrows `n`, but `sink` keeps it for 'static; " + fix},
		},
		// A parameter nothing constrains may hold an object, so the closure may borrow it.
		{
			name: "closure that captures an unconstrained unannotated parameter",
			src: decls + `fn setup(n) {
    sink = fn () { console.log(n) }
}`,
			want: []string{"5:12-5:36: this closure borrows `n`, but `sink` keeps it for 'static; " + fix},
		},
		{
			name: "closure that captures an unannotated parameter and an object parameter",
			src: decls + `fn setup(n, p: {x: number}) {
    sink = fn () { console.log(n + p.x) }
    return n + 1
}`,
			want: []string{"5:12-5:42: this closure borrows `p`, but `sink` keeps it for 'static; " + fix},
		},
		// g's own check reports that it is kept for 'static, and the closure's borrow of n is
		// decided once n settles to a number.
		{
			name: "local joining an unannotated parameter's closure and a call-only parameter",
			src: decls + `fn setup(c: boolean, n, g: fn () -> undefined) {
    val a = fn () { console.log(n) }
    val h = if c { a } else { g }
    sink = h
    return n + 1
}`,
			want: []string{"4:25-4:26: 'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g: 'static fn () -> undefined` to keep it"},
		},
		{
			name: "local joining an object parameter's closure and a call-only parameter",
			src: decls + `fn setup(c: boolean, n, g: fn () -> undefined) {
    val a = fn () { console.log(n) }
    val h = if c { a } else { g }
    sink = h
    return n.x
}`,
			want: []string{
				"4:25-4:26: 'g' may borrow values that live only during the call, but it is stored where it lives for the whole program; write `g: 'static fn () -> undefined` to keep it",
				"7:12-7:13: `h` borrows `n`, but `sink` keeps it for 'static; " + fix,
			},
		},
		// hold keeps its argument for 'static, so the closure is rejected there, and its 'static
		// result fits the slot.
		{
			name: "closure that captures an unannotated parameter kept through a static result",
			src: decls + `declare fn hold(cb: 'static fn () -> undefined) -> 'static fn () -> undefined
fn setup(n) {
    sink = hold(fn () { console.log(n) })
    return n.x
}`,
			want: []string{"6:17-6:41: this closure borrows `n`, but `hold` keeps it for 'static; " + fix},
		},
		// A closure takes only the locals of the body that returns it. One returned from mk
		// still borrows o, which belongs to setup.
		{
			name: "closure returned from a local closure",
			src: decls + `fn setup() {
    val mut o = {x: 1}
    val mk = fn () { return fn () { o.x = 2 } }
    sink = mk()
}`,
			want: []string{"7:12-7:16: this function borrows `o`, but `sink` keeps it for 'static; " + fix + ", or declare `o` at module level"},
		},
		{
			name: "returned closure and the closure it returns",
			src: `var sinkAny: fn () -> unknown = fn () { return 0 }
fn outer() {
    val x = {n: 1}
    val inner = fn () { console.log(x.n) }
    return fn () { return inner }
}
fn setup() {
    sinkAny = outer()
    sinkAny = outer()()
}`,
			want: []string{"9:15-9:24: this function borrows `x`, but `sinkAny` keeps it for 'static; " + fix + ", or declare `x` at module level"},
		},
		// A `'static fn` parameter's borrows live for the whole program, so the body may keep it
		// anywhere.
		{
			name: "static parameter kept in every position",
			src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
var sink: fn () -> undefined = fn () {}
var mut handlers: Array<fn () -> undefined> = []
fn f(g: 'static fn () -> undefined) -> 'static fn () -> undefined {
    sink = g
    handlers.push(g)
    onClick(g)
    onClick(fn () { g() })
    return g
}`,
		},
		{
			name: "closure that captures a static parameter stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
fn f(g: 'static fn () -> undefined) { sink = fn () { g() } }`,
		},
		// A signature's own lifetime is reported by the signature's lifetime check, which names
		// the bound to declare.
		{
			name: "named-lifetime parameter stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
fn f<'a>(g: 'a fn () -> undefined) { sink = g }`,
			want: []string{"2:6-2:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
		// A class's lifetime has no such check, so the slot reports the function. The error
		// blames the field's annotation, where the function's type was written.
		{
			name: "named-lifetime field stored into a module-level slot",
			src: `var sink: fn () -> undefined = fn () {}
class Button<'a> {
    onClick: 'a fn () -> undefined,
    keep(&self) -> undefined { sink = self.onClick },
}`,
			want: []string{"3:14-3:35: this function may hold borrows that do not live for 'static, but `sink` keeps it for 'static; " + fix},
		},
		// A type parameter's bound is elided as a parameter's type is, so apply only calls f
		// during the call and keeps none of cfg.
		{
			name: "closure passed to a parameter typed by a function-bounded type parameter",
			src: `fn apply<F: fn () -> undefined>(f: F) { f() }
fn setup() {
    val mut cfg = {msg: "hello"}
    apply(fn () { console.log(cfg.msg) })
    cfg.msg = "bye"
}`,
		},
		// A union member that keeps its function for 'static reports a closure over a parameter
		// with the message a lone 'static slot gives, and accepts a function that borrows
		// nothing, as the lone slot does.
		{
			name: "closure over a parameter passed to a static parameter",
			src: `declare fn on(cb: 'static fn () -> undefined) -> undefined
fn setup(p: {a: number}) {
    on(fn () { console.log(p.a) })
}`,
			want: []string{"3:8-3:34: this closure borrows `p`, but `on` keeps it for 'static; pass a function that borrows nothing"},
		},
		{
			name: "closure over a parameter passed to a static union member",
			src: `declare fn on(cb: ('static fn () -> undefined) | null) -> undefined
fn setup(p: {a: number}) {
    on(fn () { console.log(p.a) })
}`,
			want: []string{"3:8-3:34: this closure borrows `p`, but `on` keeps it for 'static; pass a function that borrows nothing"},
		},
		{
			name: "function that borrows nothing passed to a static union member",
			src: `declare fn on(cb: ('static fn () -> undefined) | null) -> undefined
fn setup() {
    on(fn () { console.log("hello") })
}`,
		},
		// The lifetime does not decide the arm, but the chosen arm still reports a closure over a
		// parameter, which the slot cannot keep.
		{
			name: "closure over a parameter passed to an overload with a static parameter",
			src: `declare fn keep(cb: 'static fn () -> undefined) -> undefined
declare fn keep(cb: number) -> undefined
fn setup(p: {a: number}) {
    keep(fn () { console.log(p.a) })
}`,
			want: []string{"4:10-4:36: this closure borrows `p`, but `keep` keeps it for 'static; pass a function that borrows nothing"},
		},
		// An interface method's `&self` is an input lifetime, so its elided result may borrow
		// what the receiver does, as the class's method does.
		{
			name: "class method returning a closure matched against an interface",
			src: `interface I { fetch(&self) -> fn () -> number }
class C { x: number, fetch(&self) -> fn () -> number {
    val s = self
    return fn () { return s.x }
} }
val i: I = C(1)`,
		},
		// An options object written out in a parameter takes the parameter's elision, so its
		// callback is only called during the call.
		{
			name: "closure in an options object typed in the parameter",
			src: `declare fn mount(opts: {cb: fn () -> undefined}) -> undefined
fn f() {
    val mut p = {x: 1}
    mount({cb: fn () { p.x = 2 }})
}`,
		},
		// An unannotated local takes the type of the closure it starts with, which borrows
		// nothing, so a later closure that borrows does not fit it.
		{
			name: "closure assigned to a local that started with a closure that borrows nothing",
			src: `fn f() {
    val mut p = {x: 1}
    var g = fn () {}
    g = fn () { p.x = 2 }
}`,
			want: []string{"4:9-4:26: this closure borrows `p`, but `g` has the type of a closure that borrows nothing; annotate `g` with a function type to let it hold one that borrows"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A closure returned from a function takes the locals of that function it captures, as F3
// states, so it holds no borrow once returned. One that still borrows something the function
// did not own keeps its lifetime. The rendered signature cannot show that lifetime when it
// connects nothing, so TestReturnedClosureLifetime stores each result into a module-level slot
// and asserts the full message, with its span, of each error that reports.
func TestReturnedClosureLifetime(t *testing.T) {
	const fix = "pass a function that borrows nothing"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "returned closure that captures an object local",
			src: `var sink: fn () -> 1 = fn () { return 1 }
fn mk() {
    val o = {x: 1}
    return fn () { return o.x }
}
fn use() { sink = mk() }`,
		},
		{
			name: "closure returned from a returned closure",
			src: `var sink: fn () -> 1 = fn () { return 1 }
fn outer() {
    val x = {n: 1}
    return fn () { return fn () { return x.n } }
}
fn use() { sink = outer()() }`,
			want: []string{"6:19-6:28: this function borrows `x`, but `sink` keeps it for 'static; " + fix + ", or declare `x` at module level"},
		},
		{
			name: "returned closure that captures an object holding a borrowed parameter",
			src: `var sink: fn () -> number = fn () { return 1 }
fn f(p: &mut {a: number}) {
    val o = {r: p}
    return fn () { return o.r.a }
}
fn use() {
    val mut q = {a: 1}
    sink = f(&mut q)
}`,
			want: []string{"8:12-8:21: this function borrows `o`, but `sink` keeps it for 'static; " + fix},
		},
		// A declared function's elided return takes the lifetime of its input, and the checker
		// knows no binding the result borrows.
		{
			name: "result of a declared function with an input lifetime",
			src: `var sink: fn () -> undefined = fn () {}
declare fn watch(p: &{x: number}) -> fn () -> undefined
fn use() {
    val q = {x: 1}
    sink = watch(&q)
}`,
			want: []string{"5:12-5:21: this function may hold borrows that do not live for 'static, but `sink` keeps it for 'static; " + fix},
		},
		{
			name: "returned closure that captures an object holding a borrow of a local",
			src: `var sink: fn () -> 1 = fn () { return 1 }
fn f() {
    val x = {a: 1}
    val o = {r: &x}
    return fn () { return o.r.a }
}
fn use() { sink = f() }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A `fn` declaration at a script's top level is a closure over the script's earlier bindings,
// so it holds a borrow of each one the way a `val name = fn …` closure does.
// TestScriptFuncDeclHoldsBorrow covers the captures that make it borrow and the ones that keep
// it 'static, and asserts the full message, with its span, of each error.
func TestScriptFuncDeclHoldsBorrow(t *testing.T) {
	const decls = `var sink: fn () -> unknown = fn () { return 0 }
declare fn run(cb: fn () -> unknown) -> undefined
`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "declaration that captures nothing",
			src: decls + `fn one() { return 1 }
sink = one`,
		},
		{
			name: "declaration that copies a value type",
			src: decls + `val msg = "hi"
fn show() { return msg }
sink = show`,
		},
		{
			name: "declaration that reads an object",
			src: decls + `val o = {x: 1}
fn read() { return o.x }
run(read)
sink = read`,
			want: []string{"6:8-6:12: `read` borrows `o`, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
		{
			name: "declaration that writes a var",
			src: decls + `var n = 0
fn inc() { n = n + 1 }
run(inc)
sink = inc`,
			want: []string{"6:8-6:11: `inc` borrows `n`, but `sink` keeps it for 'static; pass a function that borrows nothing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferScriptSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// A top-level function and a module-level closure capture only module-level bindings, which
// live for 'static, so a slot that keeps a function for 'static accepts either one. A declared
// function with no input lifetime returns a 'static function, so the slot accepts its result
// too. TestStaticFuncKept covers each such slot and asserts that none reports an error.
func TestStaticFuncKept(t *testing.T) {
	src := `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
var sink: fn () -> undefined = fn () {}
fn handle() {}
val handleExpr = fn () {}
declare fn mk() -> fn () -> undefined
fn setup() {
    onClick(handle)
    sink = handle
    onClick(handleExpr)
    onClick(mk())
    sink = mk()
}`
	_, errs := inferAgainstTreeWithSpans(t, src)
	require.Empty(t, errs)
}

// A closure passed to a `'static fn` parameter may run during any later call. The parameter
// holds only a function that borrows nothing shorter than the program, so it rejects a closure
// that borrows a local. TestRetainedClosureBorrows covers the ways a call reaches such a
// parameter and asserts the full message, with its span, of each error.
//
// A parameter whose lifetime is only bounded by 'static, as `'a: 'static` bounds it, keeps the
// closure, so a later call may run the closure while `count` is borrowed.
func TestRetainedClosureBorrows(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// forEachItem only calls its closure during the call, so the closure's write to `count`
		// is over before render borrows it.
		{
			name: "closure only called during the call",
			src: `declare fn forEachItem(f: fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    forEachItem(fn () { count.n = count.n + 1 })
    render(&count)
}`,
		},
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					// onClick keeps the handler's mutable borrow of count, so render's immutable borrow
					// conflicts with it.
					{
						name: "closure retained before the borrow",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    onClick(fn () { count.n = count.n + 1 })
			    render(&count)
			}`,
						want: []string{"6:12-6:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "closure bound to a name",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    val h = fn () { count.n = count.n + 1 }
			    onClick(h)
			    render(&count)
			}`,
						want: []string{"7:12-7:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "method that retains its argument",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			class Button { on(self, handler: 'static fn () -> undefined) -> undefined { onClick(handler) } }
			fn setup(b: Button) {
			    val mut count = {n: 0}
			    b.on(fn () { count.n = count.n + 1 })
			    render(&count)
			}`,
						want: []string{"7:12-7:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'on' retains"},
					},
		*/
		{
			name: "retention lifetime bound to outlive static",
			src: `declare fn onClick<'a: 'static>(handler: 'a fn () -> undefined) -> undefined
declare fn render(view: &{n: number}) -> undefined
fn setup() {
    val mut count = {n: 0}
    onClick(fn () { count.n = count.n + 1 })
    render(&count)
}`,
			want: []string{"6:5-6:19: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "retained on one branch",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    if count.n > 0 {
			        onClick(fn () { count.n = count.n + 1 })
			    }
			    render(&count)
			}`,
						want: []string{"8:12-8:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "moved into a later call",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn take(view: {n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    onClick(fn () { count.n = count.n + 1 })
			    take(count)
			}`,
						want: []string{"6:10-6:15: cannot move 'count' while it is borrowed by the 'handler' that 'onClick' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "rest parameter",
						src: `declare fn onAll(...hs: Array<'static fn () -> undefined>) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    onAll(fn () { count.n = count.n + 1 })
			    render(&count)
			}`,
						want: []string{"6:12-6:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'hs' that 'onAll' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "union with null",
						src: `declare fn onClick(handler: ('static fn () -> undefined) | null) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    onClick(fn () { count.n = count.n + 1 })
			    render(&count)
			}`,
						want: []string{"6:12-6:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'handler' that 'onClick' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					{
						name: "object field",
						src: `declare fn mount(props: {onClick: 'static fn () -> undefined}) -> undefined
			declare fn render(view: &{n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    mount({onClick: fn () { count.n = count.n + 1 }})
			    render(&count)
			}`,
						want: []string{"6:12-6:18: cannot borrow 'count' as immutable while 'count.n' is borrowed as mutable by the 'props' that 'mount' retains"},
					},
		*/
		// DISABLED until the kept-borrows PR (fn-4): a 'static slot keeps the borrows a closure
		// over locals of the body holds, per place, and checks later uses against them under E2,
		// where this branch rejects the closure at the store.
		/*
					// A closure that only reads count keeps an immutable borrow, so `&mut count` conflicts
					// with it.
					{
						name: "closure that only reads",
						src: `declare fn onClick(handler: 'static fn () -> undefined) -> undefined
			declare fn bump(view: &mut {n: number}) -> undefined
			fn setup() {
			    val mut count = {n: 0}
			    onClick(fn () { val x = count.n })
			    bump(&mut count)
			}`,
						want: []string{"6:10-6:20: cannot borrow 'count' as mutable while 'count.n' is borrowed as immutable by the 'handler' that 'onClick' retains"},
					},
		*/
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// A closure passed to a `'a fn` parameter holds its captures for 'a, which lasts as long as a
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
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: 'a fn () -> undefined) -> undefined
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
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: 'a fn () -> undefined) -> undefined
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
declare fn subscribe<'a>(emitter: &'a mut Emitter, handler: 'a fn () -> undefined) -> undefined
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
declare fn mapLazy<'a>(xs: &'a Array<number>, f: 'a fn (x: number) -> number) -> Lazy<'a>
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
declare fn mapLazy<'a>(xs: &'a Array<number>, f: 'a fn (x: number) -> number) -> Lazy<'a>
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
    onClick: 'a fn () -> undefined,
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
    onClick: 'a fn () -> undefined,
    click(&self) -> undefined { self.onClick() },
}
fn setup() {
    val mut count = {n: 0}
    val b = Button(fn () { count.n = count.n + 1 })
    b.click()
    render(&count)
}`,
		},
		// The constructor's parameter shares the class's 'a, so the instance holds the handler
		// the constructor stores.
		{
			name: "explicit constructor that stores a handler",
			src: `declare fn render(view: &{n: number}) -> undefined
class Keyed<'a> {
    key: 'a fn () -> undefined,
    constructor(&mut self, key: 'a fn () -> undefined) { self.key = key },
    run(&self) -> undefined { self.key() },
}
fn setup() {
    val mut count = {n: 0}
    val k = Keyed(fn () { count.n = count.n + 1 })
    render(&count)
    k.run()
}`,
			want: []string{"10:5-10:19: this call may run the 'key' that 'Keyed' retains, which writes 'count' while it is borrowed as immutable"},
		},
		{
			name: "explicit constructor that stores a handler, run before the borrow",
			src: `declare fn render(view: &{n: number}) -> undefined
class Keyed<'a> {
    key: 'a fn () -> undefined,
    constructor(&mut self, key: 'a fn () -> undefined) { self.key = key },
    run(&self) -> undefined { self.key() },
}
fn setup() {
    val mut count = {n: 0}
    val k = Keyed(fn () { count.n = count.n + 1 })
    k.run()
    render(&count)
}`,
		},
		// The constructor's parameter has no lifetime written, so it gets its own, 'b, bounded by
		// 'b: 'a. Keyed renders as `new <'a, 'b: 'a>(key: 'b fn () -> undefined) -> Keyed<'a>`.
		//
		// DISABLED until #2565. The check that a call runs a retained function compares
		// lifetimes by identity, so it does not follow 'b: 'a to the instance, and this program
		// reports nothing. Section 2 of #2565 follows outlives bounds there.
		/*
			{
				name: "explicit constructor that stores a handler with no written lifetime",
				src: "declare fn render(view: &{n: number}) -> undefined\n" +
					"class Keyed<'a> {\n" +
					"    key: 'a fn () -> undefined,\n" +
					"    constructor(&mut self, key: fn () -> undefined) { self.key = key },\n" +
					"    run(&self) -> undefined { self.key() },\n" +
					"}\n" +
					"fn setup() {\n" +
					"    val mut count = {n: 0}\n" +
					"    val k = Keyed(fn () { count.n = count.n + 1 })\n" +
					"    render(&count)\n" +
					"    k.run()\n" +
					"}",
				want: []string{"10:5-10:19: this call may run the 'key' that 'Keyed' retains, which writes 'count' while it is borrowed as immutable"},
			},
		*/
		// The receiver's type is Button<'a>, so b holds the handler on() stores.
		{
			name: "class method that stores a handler",
			src: `declare fn render(view: &{n: number}) -> undefined
class Button<'a> {
    handler: 'a fn () -> undefined,
    on(&mut self, h: 'a fn () -> undefined) -> undefined { self.handler = h },
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
    handler: 'a fn () -> undefined,
    emit(&self) -> undefined { self.handler() },
}
declare fn subscribe<'a>(emitter: &mut Emitter<'a>, handler: 'a fn () -> undefined) -> undefined
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
    onClick: 'a fn () -> undefined,
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
    onClick: 'a fn () -> undefined,
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
			src: `declare fn run<'a>(handler: 'a fn () -> undefined) -> undefined
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

// TestBodilessSignatureFuncKeptForStatic covers a function whose lifetime comes from a
// signature with no body, a declared function or an interface method, stored into a 'static
// slot. Each store reports that the function may hold borrows.
func TestBodilessSignatureFuncKeptForStatic(t *testing.T) {
	const decls = `var sink: fn () -> undefined = fn () {}
declare fn id(g: fn () -> undefined) -> fn () -> undefined
interface Reg { add(g: fn () -> undefined) -> fn () -> undefined }
`
	const kept = "this function may hold borrows that do not live for 'static, but `sink` keeps it for 'static; pass a function that borrows nothing"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "declared function returns its parameter",
			src:  decls + `fn h(g: fn () -> undefined) { sink = id(g) }`,
			want: []string{"4:38-4:43: " + kept},
		},
		// The error underlines the interface method's return type rather than the call.
		{
			name: "interface method returns its parameter",
			src:  decls + `fn h(g: fn () -> undefined, r: Reg) { sink = r.add(g) }`,
			want: []string{"3:47-3:65: " + kept},
		},
		{
			name: "declared function returns a named lifetime parameter",
			src:  decls + `fn h<'a>(g: 'a fn () -> undefined) { sink = id(g) }`,
			want: []string{"4:45-4:50: " + kept},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

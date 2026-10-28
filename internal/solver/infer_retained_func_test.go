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

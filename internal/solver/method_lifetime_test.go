package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMethodLifetimesPerCall asserts that each call to a method gets its own copy of the
// lifetimes the method quantifies, the way a call to a free function instantiates the
// function's scheme. A wrapper that calls the method therefore infers the same signature as
// one calling the equivalent free function. A lifetime the class declares belongs to the
// instance and is not freshened.
func TestMethodLifetimesPerCall(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		value string
		want  string
	}{
		{
			name: "the receiver's lifetime reaches the result",
			src: `
				class View {
					p: {x: number},
					peek<'a>(&'a self) -> &'a {x: number} { return &self.p },
				}
				fn read(v: &View) { return v.peek() }
			`,
			value: "read",
			want:  "fn <'a>(v: &'a View) -> &'a {x: number}",
		},
		{
			name: "a receiver joined from two borrows ties both to the result",
			src: `
				class View {
					p: {x: number},
					peek<'a>(&'a self) -> &'a {x: number} { return &self.p },
				}
				fn read(a: &View, b: &View, k: boolean) {
					val v = if k { a } else { b }
					return v.peek()
				}
			`,
			value: "read",
			want:  "fn <'a: 'c, 'b: 'c, 'c>(a: &'a View, b: &'b View, k: boolean) -> &'c {x: number}",
		},
		{
			name: "a mutable receiver's lifetime reaches the result",
			src: `
				class View {
					p: {x: number},
					edit<'a>(&'a mut self) -> &'a mut {x: number} { return &mut self.p },
				}
				fn write(v: &mut View) { return v.edit() }
			`,
			value: "write",
			want:  "fn <'a>(v: &'a mut View) -> &'a mut {x: number}",
		},
		{
			name: "a call relates only its own receiver and arguments",
			src: `
				class Pair {
					a: {x: number},
					pick<'r>(&'r self, other: &'r {x: number}) -> &'r {x: number} { return other },
				}
				fn first(p: &Pair, s: &{x: number}) { return p.pick(s) }
				fn second(p: &Pair, l: &{x: number}) { return p.pick(l) }
			`,
			value: "second",
			want:  "fn <'a: 'c, 'b: 'c, 'c>(p: &'a Pair, l: &'b {x: number}) -> &'c {x: number}",
		},
		{
			name: "two calls in one body do not relate each other's arguments",
			src: `
				class Pair {
					a: {x: number},
					pick<'r>(&'r self, other: &'r {x: number}) -> &'r {x: number} { return other },
				}
				fn both(p: &Pair, a: &{x: number}, q: &Pair, b: &{x: number}) {
					val first = p.pick(a)
					return q.pick(b)
				}
			`,
			value: "both",
			want: "fn <'a: 'c, 'b: 'c, 'c>(p: &Pair, a: &{x: number}, q: &'a Pair, b: &'b {x: number}) " +
				"-> &'c {x: number}",
		},
		{
			name: "a class lifetime stays the instance's",
			src: `
				class Holder<'h> {
					peer: &'h {x: number},
					get(&'h self) -> &'h {x: number} { return self.peer },
				}
				fn g<'q>(h: &Holder<'q>) { return h.get() }
			`,
			value: "g",
			want:  "fn <'a>(h: &Holder<'a>) -> &'a {x: number}",
		},
		{
			name: "a static method's lifetimes are instantiated per call",
			src: `
				class K {
					static pick<'r>(a: &'r {x: number}, b: &'r {x: number}) -> &'r {x: number} { return a },
				}
				fn h(a: &{x: number}, b: &{x: number}) { return K.pick(a, b) }
			`,
			value: "h",
			want:  "fn <'a: 'c, 'b: 'c, 'c>(a: &'a {x: number}, b: &'b {x: number}) -> &'c {x: number}",
		},
		{
			name: "a method read as a value is instantiated at the read",
			src: `
				class View {
					p: {x: number},
					peek<'a>(&'a self) -> &'a {x: number} { return &self.p },
				}
				fn read(v: &View) {
					val f = v.peek
					return f()
				}
			`,
			value: "read",
			want:  "fn <'a>(v: &'a View) -> &'a {x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, values[tt.value])
		})
	}
}

// TestAStoredMethodValueBindsItsLifetimesPerCall covers the lifetimes a method value carries.
// A lifetime the receiver's type writes is fixed by the access that borrows the receiver, so
// the value reads at that borrow's lifetime. Any other lifetime stays on the value's own
// binder, so the value renders it as a rank-2 `fn <'a>` and each call instantiates it afresh,
// relating that call's argument to its own result.
func TestAStoredMethodValueBindsItsLifetimesPerCall(t *testing.T) {
	t.Parallel()

	const view = `
		class View {
			p: {x: number},
			peek<'a>(&'a self) -> &'a {x: number} { return &self.p },
			pick<'a>(&'a self, q: &'a {x: number}) -> &'a {x: number} { return q },
			id<'a>(&self, q: &'a {x: number}) -> &'a {x: number} { return q },
		}
	`
	tests := []struct {
		name  string
		src   string
		value string
		want  string
	}{
		{
			name: "a receiver lifetime is fixed by the access",
			src: view + `
				fn get(v: &View) {
					val f = v.peek
					return f
				}
			`,
			value: "get",
			want:  "fn <'a>(v: &'a View) -> fn () -> &'a {x: number}",
		},
		{
			name: "a lifetime the receiver does not write stays on the value's binder",
			src: view + `
				fn get(v: &View) {
					val f = v.id
					return f
				}
			`,
			value: "get",
			want:  "fn (v: &View) -> fn <'a>(q: &'a {x: number}) -> &'a {x: number}",
		},
		{
			name: "each call of the value binds its own lifetime",
			src: view + `
				fn both(v: &View, a: &{x: number}, b: &{x: number}) {
					val f = v.id
					val ra = f(a)
					val rb = f(b)
					return [ra, rb]
				}
			`,
			value: "both",
			want:  "fn <'a, 'b>(v: &View, a: &'a {x: number}, b: &'b {x: number}) -> [&'a {x: number}, &'b {x: number}]",
		},
		{
			// A parameter annotated with its own binder is quantified the same way, so the
			// annotation's `'a` names neither argument and each call binds it afresh.
			name: "a callback parameter's binder is instantiated per call",
			src: `fn both(f: fn <'a>(q: &'a {x: number}) -> &'a {x: number}, a: &{x: number}, b: &{x: number}) {
				val ra = f(a)
				val rb = f(b)
				return [ra, rb]
			}`,
			value: "both",
			want:  "fn <'b, 'c>(f: fn <'a>(q: &'a {x: number}) -> &'a {x: number}, a: &'b {x: number}, b: &'c {x: number}) -> [&'b {x: number}, &'c {x: number}]",
		},
		{
			// A check against the alias at one site instantiates its binder afresh, so
			// nothing `mk` records reaches the calls in `both`.
			name: "a declared callback type shared by two sites couples neither to the other",
			src: `type Cb = fn <'a>(q: &'a {x: number}) -> &'a {x: number}
				fn mk() -> Cb { return fn (q) { return q } }
				fn both(f: Cb, b: &{x: number}, c: &{x: number}) {
					val rb = f(b)
					val rc = f(c)
					return [rb, rc]
				}`,
			value: "both",
			want:  "fn <'a, 'b>(f: Cb, b: &'a {x: number}, c: &'b {x: number}) -> [&'a {x: number}, &'b {x: number}]",
		},
		{
			// `'a` is the receiver's, so both calls read at the access's borrow and each
			// argument has to outlive each result.
			name: "a receiver lifetime is shared by every call of the value",
			src: view + `
				fn both(v: &View, a: &{x: number}, b: &{x: number}) {
					val f = v.pick
					val ra = f(a)
					val rb = f(b)
					return [ra, rb]
				}
			`,
			value: "both",
			want:  "fn <'a: 'd & 'e, 'b: 'd & 'e, 'c: 'd & 'e, 'd, 'e>(v: &'a View, a: &'b {x: number}, b: &'c {x: number}) -> [&'d {x: number}, &'e {x: number}]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values[tt.value])
		})
	}
}

// TestAMethodCalledOnSelfBindsItsLifetimePerCall asserts that a method's own lifetime binder is
// instantiated afresh at each call inside the class body, so two calls of `self.id` relate each
// argument to its own result and neither to the other's.
func TestAMethodCalledOnSelfBindsItsLifetimePerCall(t *testing.T) {
	t.Parallel()

	src := `class View {
		p: {x: number},
		id<'a>(&self, q: &'a {x: number}) -> &'a {x: number} { return q },
		both(&self, a: &{x: number}, b: &{x: number}) {
			val ra = self.id(a)
			val rb = self.id(b)
			return [ra, rb]
		},
	}`
	res := InferModuleWithSource(parseModule(t, src), testStdlibSource())
	require.Empty(t, errorMessagesOf(res.Errors))
	def, ok := res.checker.ctx.classDef("View")
	require.True(t, ok)
	require.Equal(t,
		"{p: {x: number}, id<'a>(&self, q: &'a {x: number}) -> &'a {x: number}, both<'a, 'b>(&self, a: &'a {x: number}, b: &'b {x: number}) -> [&'a {x: number}, &'b {x: number}]}",
		printClassBody(def))
}

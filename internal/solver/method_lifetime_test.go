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

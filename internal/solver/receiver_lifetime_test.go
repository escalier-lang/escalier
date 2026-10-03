package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReceiverLifetimeDeclarations asserts that a lifetime written on a receiver resolves in
// the member's own lifetime scope. `&'a self` shares its `'a` with every other `'a` the
// signature writes, it counts as a use of the binder that declares it, and it reports when
// nothing declares it.
func TestReceiverLifetimeDeclarations(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// The receiver's 'a ties out.r to a borrow of c, so the call stores that borrow into the
		// caller's out. Nothing in f reaches c after the call, so out.r is the only path to it.
		{
			name: "a borrow of the instance stored through the receiver's lifetime leaves as the only path to it",
			src: `
				class C {
					p: {x: number},
					lend<'a>(&'a self, out: &mut {r: &'a {x: number}}) -> undefined { out.r = &self.p },
				}
				fn f(out: &mut {r: &{x: number}}) {
					val c = C({x: 1})
					c.lend(out)
				}
			`,
		},
		// The stored borrow is immutable, so writing c afterwards would change what out.r expects
		// to hold still. The store's loan lasts to the end of f and reports it.
		{
			name: "writing the instance after storing a borrow of it through the receiver's lifetime conflicts",
			src: `
				class C {
					p: {x: number},
					lend<'a>(&'a self, out: &mut {r: &'a {x: number}}) -> undefined { out.r = &self.p },
				}
				fn f(out: &mut {r: &{x: number}}) {
					val mut c = C({x: 1})
					c.lend(out)
					c.p = {x: 2}
				}
			`,
			want: []string{"9:6-9:9: cannot assign to 'c.p' while it is borrowed as immutable"},
		},
		{
			name: "a borrow stored at an unrelated lifetime does not escape",
			src: `
				class C {
					p: {x: number},
					lend<'a, 'b>(&'a self, out: &mut {r: &'b {x: number}}, q: &'b {x: number}) -> undefined {
						out.r = q
					},
				}
				fn f(out: &mut {r: &{x: number}}, q: &{x: number}) {
					val c = C({x: 1})
					c.lend(out, q)
				}
			`,
		},
		{
			name: "a binder used only by the receiver is used",
			src: `
				class C {
					p: {x: number},
					peek<'a>(&'a self) -> number { return self.p.x },
				}
			`,
		},
		{
			name: "a receiver lifetime with no binder is undeclared",
			src: `
				class C {
					p: {x: number},
					peek(&'a self) -> number { return self.p.x },
				}
			`,
			want: []string{"4:12-4:14: lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature"},
		},
		{
			name: "a receiver may name the class's own lifetime",
			src: `
				class Holder<'h> {
					peer: &'h {x: number},
					get(&'h self) -> &'h {x: number} { return self.peer },
				}
			`,
		},
		{
			name: "a binder used only by an interface method's receiver is used",
			src:  `interface Viewer { peek<'a>(&'a self) -> number }`,
		},
		{
			name: "a binder used only by an object type method's receiver is used",
			src:  `type Viewer = { peek<'a>(&'a self) -> number }`,
		},
		{
			name: "an interface method's receiver lifetime with no binder is undeclared",
			src:  `interface Viewer { peek(&'a self) -> number }`,
			want: []string{"1:26-1:28: lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

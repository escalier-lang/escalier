package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSignatureImpliesBodyLifetimes covers the relations a body may impose between the
// lifetimes its signature names. Each must follow from a declared bound, a chain of them, or
// a bound the signature's own types imply. An unnamed lifetime stays inferred, and a
// declared function with no body is not checked.
func TestSignatureImpliesBodyLifetimes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an undeclared relation between two named lifetimes reports",
			src:  `fn f<'a, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
			want: []string{"1:6-1:8: the body requires 'a to outlive 'b, but the signature does not declare it; add the bound 'a: 'b"},
		},
		{
			name: "a declared bound covers the relation",
			src:  `fn f<'a: 'b, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number} { return x }`,
		},
		{
			name: "a chain of declared bounds covers the relation",
			src: `fn f<'a: 'b, 'b: 'c, 'c>(x: &'a {x: number}, y: &'b {x: number}, z: &'c {x: number}) -> &'c {x: number} {
				val u: &'b {x: number} = x
				return u
			}`,
		},
		{
			name: "a parameter connected to a return-only lifetime is not assumed to outlive it",
			src: `fn f<'a, 'b, 'c>(x: &'a {x: number}, y: &'b {x: number}) -> &'c {x: number} {
				val w: &'c {x: number} = y
				val k: &'a {x: number} = w
				return y
			}`,
			// `val k: &'a {x: number} = w` with `w: &'c` requires 'c to outlive 'a as well.
			want: []string{
				"1:10-1:12: the body requires 'b to outlive 'a, but the signature does not declare it; add the bound 'b: 'a",
				"1:10-1:12: the body requires 'b to outlive 'c, but the signature does not declare it; add the bound 'b: 'c",
				"1:14-1:16: the body requires 'c to outlive 'a, but the signature does not declare it; add the bound 'c: 'a",
			},
		},
		{
			name: "forcing a named lifetime to 'static reports",
			src:  `fn f<'a>(x: &'a {x: number}) -> &'static {x: number} { return x }`,
			want: []string{"1:6-1:8: the body requires 'a to outlive 'static, but the signature does not declare it; add the bound 'a: 'static"},
		},
		{
			name: "a declared 'static bound covers forcing the lifetime to 'static",
			src:  `fn f<'a: 'static>(x: &'a {x: number}) -> &'static {x: number} { return x }`,
		},
		{
			name: "a nested borrow implies its inner lifetime outlives its outer one",
			src:  `fn f<'a, 'b>(p: &'a &'b {x: number}) -> &'a {x: number} { return p }`,
		},
		{
			name: "an unnamed lifetime stays inferred",
			src:  `fn f(x: &{x: number}, y: &{x: number}) -> &{x: number} { return x }`,
		},
		{
			name: "a declared function with no body is not checked",
			src:  `declare fn f<'a, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number}`,
		},
		// A closure's caller chooses the closure's own lifetimes, so storing g into p's field
		// needs 'b to outlive outer's 'a, which the closure's list has to declare.
		{
			name: "an undeclared relation to an enclosing function's lifetime reports",
			src: `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined}) {
				val inner = fn <'b>(g: 'b fn () -> undefined) { p.cb = g }
			}`,
			want: []string{"2:21-2:23: the body requires 'b to outlive 'a, but the signature does not declare it; add the bound 'b: 'a"},
		},
		{
			name: "a declared bound covers the relation to an enclosing function's lifetime",
			src: `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined}) {
				val inner = fn <'b: 'a>(g: 'b fn () -> undefined) { p.cb = g }
			}`,
		},
		// The closure's own 'a hides outer's, so its 'a is a separate lifetime and no bound can
		// name outer's.
		{
			name: "a relation to an enclosing lifetime the closure's own clause hides reports",
			src: `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined}) {
				val inner = fn <'a>(g: 'a fn () -> undefined) { p.cb = g }
			}`,
			want: []string{"2:21-2:23: the body requires 'a to outlive the enclosing function's 'a, which this signature's own 'a hides; rename it and add the bound"},
		},
		// outer declares 'c: 'a through `&'a &'c`, so the closure's bound 'b: 'c covers 'b: 'a.
		{
			name: "a bound on an enclosing lifetime covers what that lifetime outlives",
			src: `fn outer<'a, 'c>(p: &mut {cb: 'a fn () -> undefined}, q: &mut {cb: 'c fn () -> undefined}, r: &'a &'c {x: number}) {
				val inner = fn <'b: 'c>(g: 'b fn () -> undefined) {
					q.cb = g
					p.cb = g
				}
			}`,
		},
		{
			name: "a bound on an enclosing lifetime that does not outlive the other reports",
			src: `fn outer<'a, 'c>(p: &mut {cb: 'a fn () -> undefined}, q: &mut {cb: 'c fn () -> undefined}) {
				val inner = fn <'b: 'c>(g: 'b fn () -> undefined) {
					q.cb = g
					p.cb = g
				}
			}`,
			want: []string{"2:21-2:27: the body requires 'b to outlive 'a, but the signature does not declare it; add the bound 'b: 'a"},
		},
		// A class's lifetimes are not checked against a member's own, and neither are they
		// against a closure's in the member's body.
		{
			name: "a relation to a class lifetime from a closure in a member is not checked",
			src:  `class C<'a> { peer: &'a mut {value: number}, run(&mut self) -> undefined { val f = fn <'b>(p: &'b mut {value: number}) { self.peer = p } } }`,
		},
		{
			name: "a closure's own lifetime unrelated to the enclosing one of the same name is not checked against it",
			src: `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined}) {
				val inner = fn <'a>(x: &'a {x: number}) -> &'a {x: number} { return x }
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOwnedMutTypeFormStillChecks covers the owned-mutable type form `mut T` that the
// binding form replaces. The form stays accepted until it is removed, so each case
// asserts what a program written in it reports today. Each program reaches a path that
// only an owned-mutable cell in a type takes, which the binding form never builds.
func TestOwnedMutTypeFormStillChecks(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A `&mut` argument cannot fill an owned `mut` parameter, because a borrow
		// does not transfer ownership.
		"BorrowIntoMutParam": {
			src: "fn take(p: mut {x: number}) {}\nfn g(q: &mut {x: number}) { take(q) }",
			want: []string{
				"2:9-2:25: cannot use borrowed &mut object as owned mut object",
			},
		},
		// A rest slot written `mut Array<T>` gathers its arguments the way
		// `Array<T>` does.
		"MutRestSlot": {
			src: "fn f(...items: mut Array<number>) { items.push(1) }\nfn g() { f(1, 2) }",
		},
		// An overload arm with a `mut` parameter is tried with the owned-mutable
		// upgrade, so a fresh literal selects it.
		"OverloadWithMutParam": {
			src: "declare fn h(a: mut {x: number}) -> number\n" +
				"declare fn h(a: string) -> number\n" +
				"fn g() { val n = h({x: 1}) }",
		},
		// A `val mut` binding over an annotation that is already `mut T` keeps the
		// one cell rather than nesting a second.
		"ValMutOverMutAnnotation": {
			src: "fn g() { val mut p: mut {x: number} = {x: 1} }",
		},
		// A type parameter bounded by `mut Array<unknown>` spreads into a tuple
		// through the cell.
		"MutArrayBoundSpreads": {
			src: "declare fn h<A: mut Array<unknown>, B: mut Array<unknown>>(x: [...A, ...B]) -> number",
		},
		// `mut` over a name that does not resolve reports only the missing name.
		"MutOverUnknownName": {
			src:  "fn g(p: mut Nope) {}",
			want: []string{"1:13-1:17: cannot find type `Nope`"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

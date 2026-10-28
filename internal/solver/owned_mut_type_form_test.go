package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ownedMutTypeMsg is the message OwnedMutTypeError reports for a `mut T` annotation.
const ownedMutTypeMsg = "`mut` is not allowed in an owned type; the binding that owns a value decides whether it is mutable, so write `mut` on the binding, as in `mut p: T` or `val mut x: T`, or write `&mut T` for a mutable borrow"

// TestOwnedMutTypeRejected asserts that a `mut T` annotation is rejected in every position a
// type is written, as T1 and T3 state, with the error on the `mut` keyword. The annotation
// recovers to the type it wraps, so each program reports nothing else. A `&mut T` borrow is
// the one place `mut` appears in a type.
func TestOwnedMutTypeRejected(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"Parameter": {
			src:  "fn f(p: mut {x: number}) {}",
			want: []string{"1:9-1:12: " + ownedMutTypeMsg},
		},
		"Binding": {
			src:  "fn f() { val p: mut {x: number} = {x: 1} }",
			want: []string{"1:17-1:20: " + ownedMutTypeMsg},
		},
		"ValMutBinding": {
			src:  "fn f() { val mut p: mut {x: number} = {x: 1} }",
			want: []string{"1:21-1:24: " + ownedMutTypeMsg},
		},
		"DeclareVar": {
			src:  "declare var sink: mut {x: number}",
			want: []string{"1:19-1:22: " + ownedMutTypeMsg},
		},
		"Return": {
			src:  "fn f() -> mut {x: number} { return {x: 1} }",
			want: []string{"1:11-1:14: " + ownedMutTypeMsg},
		},
		"ObjectField": {
			src:  "type T = {a: mut {b: number}}",
			want: []string{"1:14-1:17: " + ownedMutTypeMsg},
		},
		"TupleElement": {
			src:  "type T = [mut {b: number}]",
			want: []string{"1:11-1:14: " + ownedMutTypeMsg},
		},
		"TypeArgument": {
			src:  "type T = Array<mut {b: number}>",
			want: []string{"1:16-1:19: " + ownedMutTypeMsg},
		},
		"AliasBody": {
			src:  "type T = mut {b: number}",
			want: []string{"1:10-1:13: " + ownedMutTypeMsg},
		},
		"ClassField": {
			src:  "class C { inner: mut {b: number} }",
			want: []string{"1:18-1:21: " + ownedMutTypeMsg},
		},
		"TypeParameterBound": {
			src:  "declare fn h<A: mut Array<unknown>>(x: A) -> number",
			want: []string{"1:17-1:20: " + ownedMutTypeMsg},
		},
		"RestParameter": {
			src:  "fn f(...items: mut Array<number>) {}",
			want: []string{"1:16-1:19: " + ownedMutTypeMsg},
		},
		"InsideABorrow": {
			src:  "fn f(p: &mut {a: mut {b: number}}) {}",
			want: []string{"1:18-1:21: " + ownedMutTypeMsg},
		},
		// `mut` over a name that does not resolve reports the missing name as well.
		"OverUnknownName": {
			src: "fn g(p: mut Nope) {}",
			want: []string{
				"1:9-1:12: " + ownedMutTypeMsg,
				"1:13-1:17: cannot find type `Nope`",
			},
		},
		"MutableBorrow": {
			src: "fn f(p: &mut {a: {b: number}}) { p.a.b = 1 }",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

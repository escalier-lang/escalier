package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeArgArityAtReference covers the argument-count check a reference gets from
// resolveTypeArgs. An alias, a class, and an enum all route through it, so the three report
// the same shape of message and differ only in the noun naming the declaration. The valid
// count is the range from the parameters with no default up to the whole list, so the message
// states a single count when every parameter is required and a range when a default makes one
// optional.
func TestTypeArgArityAtReference(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ClassTooManyArgs",
			src: `
				class Box<T> { value: T }
				declare fn f() -> Box<number, string>
			`,
			want: []string{"class `Box` expects 1 type arguments but got 2"},
		},
		{
			name: "ClassTooFewArgs",
			src: `
				class Pair<T, U> { a: T, b: U }
				declare fn f() -> Pair<number>
			`,
			want: []string{"class `Pair` expects 2 type arguments but got 1"},
		},
		// A bare reference to a class whose every parameter is required omits them all, so it
		// is the same out-of-range count as any other short reference.
		{
			name: "ClassBareWithRequiredParam",
			src: `
				class Box<T> { value: T }
				declare fn f() -> Box
			`,
			want: []string{"class `Box` expects 1 type arguments but got 0"},
		},
		{
			name: "ClassTooManyArgsWithDefault",
			src: `
				class Pair<T, U = T> { a: T, b: U }
				declare fn f() -> Pair<number, string, boolean>
			`,
			want: []string{"class `Pair` expects between 1 and 2 type arguments but got 3"},
		},
		{
			name: "NonGenericClassWithArgs",
			src: `
				class Point { x: number }
				declare fn f() -> Point<number>
			`,
			want: []string{"class `Point` expects 0 type arguments but got 1"},
		},
		// An enum registers as an alias whose body is the union of its variant handles, so its
		// reference path is the alias one. The message names it an enum all the same.
		{
			name: "EnumTooManyArgs",
			src: `
				enum Opt<T> { Some(value: T), None }
				declare fn f() -> Opt<number, string>
			`,
			want: []string{"enum `Opt` expects 1 type arguments but got 2"},
		},
		{
			name: "EnumTooFewArgs",
			src: `
				enum Both<T, U> { Pair(a: T, b: U) }
				declare fn f() -> Both<number>
			`,
			want: []string{"enum `Both` expects 2 type arguments but got 1"},
		},
		{
			name: "EnumBareWithRequiredParam",
			src: `
				enum Opt<T> { Some(value: T), None }
				declare fn f() -> Opt
			`,
			want: []string{"enum `Opt` expects 1 type arguments but got 0"},
		},
		{
			name: "NonGenericEnumWithArgs",
			src: `
				enum Color { Red, Green }
				declare fn f() -> Color<number>
			`,
			want: []string{"enum `Color` expects 0 type arguments but got 1"},
		},
		{
			name: "AliasTooManyArgs",
			src: `
				type Box<T> = {value: T}
				declare fn f() -> Box<number, string>
			`,
			want: []string{"type alias `Box` expects 1 type arguments but got 2"},
		},
		// A reference that names the declaration it sits inside is checked like any other, so
		// a self-referential class body has to write its own arguments.
		{
			name: "ClassSelfReferenceBare",
			src: `
				class Node<T> { value: T, next: Node }
			`,
			want: []string{"class `Node` expects 1 type arguments but got 0"},
		},
		{
			name: "ClassSelfReferenceWithArgAccepted",
			src: `
				class Node<T> { value: T, next: Node<T> }
			`,
		},
		{
			name: "EnumSelfReferenceBare",
			src: `
				enum List<T> { Cons(head: T, tail: List), Nil }
			`,
			want: []string{"enum `List` expects 1 type arguments but got 0"},
		},
		{
			name: "EnumSelfReferenceWithArgAccepted",
			src: `
				enum List<T> { Cons(head: T, tail: List<T>), Nil }
			`,
		},
		{
			name: "ClassExactCountAccepted",
			src: `
				class Pair<T, U> { a: T, b: U }
				declare fn f() -> Pair<number, string>
			`,
		},
		{
			name: "NonGenericClassBareAccepted",
			src: `
				class Point { x: number }
				declare fn f() -> Point
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, len(tt.want))
			for i, want := range tt.want {
				require.Equal(t, want, errs[i].Message())
			}
		})
	}
}

// TestTypeParamDefaultAtReference covers the other half of resolveTypeArgs, filling a trailing
// omitted argument from its parameter's default. A class and an enum fill theirs the way an
// alias does, so a bare `Box` against `class Box<T = number>` is `Box<number>` rather than the
// declaration handle, whose unconstrained parameter var coalesces to `never`.
func TestTypeParamDefaultAtReference(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ClassBareFillsDefault",
			src: `
				class Box<T = number> { value: T }
				declare fn f() -> Box
			`,
			want: "fn () -> Box<number>",
		},
		{
			name: "ClassPartialFillsTrailingDefault",
			src: `
				class Pair<T, U = string> { a: T, b: U }
				declare fn f() -> Pair<number>
			`,
			want: "fn () -> Pair<number, string>",
		},
		// A default may name a parameter declared before it, so the argument that filled the
		// earlier slot is substituted into it.
		{
			name: "ClassDefaultNamesEarlierParam",
			src: `
				class Pair<T, U = T> { a: T, b: U }
				declare fn f() -> Pair<number>
			`,
			want: "fn () -> Pair<number, number>",
		},
		// A parameter carrying both a bound and a default keeps them independent: the default
		// fills the omitted argument, and the bound still constrains what the constructor takes.
		{
			name: "ClassBoundedDefault",
			src: `
				class Box<T: number = 5> { value: T }
				declare fn f() -> Box
			`,
			want: "fn () -> Box<5>",
		},
		{
			name: "EnumBareFillsDefault",
			src: `
				enum Opt<T = number> { Some(value: T), None }
				declare fn f() -> Opt
			`,
			want: "fn () -> Opt<number>",
		},
		{
			name: "EnumDefaultNamesEarlierParam",
			src: `
				enum Both<T, U = T> { Pair(a: T, b: U) }
				declare fn f() -> Both<number>
			`,
			want: "fn () -> Both<number, number>",
		},
		{
			name: "AliasBareFillsDefault",
			src: `
				type Box<T = number> = {value: T}
				declare fn f() -> Box
			`,
			want: "fn () -> Box<number>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TestClassTypeParamDefaultIsPerReference checks that a default filled at one class reference
// does not reach another. `U = T` substitutes the argument that reference wrote for `T`, so `f`
// and `g` each carry their own second argument instead of sharing one var declared on the class.
func TestClassTypeParamDefaultIsPerReference(t *testing.T) {
	src := `
		class Pair<T, U = T> { a: T, b: U }
		declare fn f() -> Pair<number>
		declare fn g() -> Pair<string>
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> Pair<number, number>", values["f"])
	require.Equal(t, "fn () -> Pair<string, string>", values["g"])
}

// TestClassArityUncheckedForUnresolvedSibling pins the one reference the class count does not
// reach. The SCC pre-pass registers a bare ClassDef for every class in a dep_graph component so
// a forward reference resolves, and each class's parameters land when its own pass runs. `A` is
// walked first, so the `B<number, string>` in its body reaches `B` before `B`'s parameter list
// exists and there is nothing to check the two written arguments against. The reference still
// resolves, carrying exactly what it wrote.
func TestClassArityUncheckedForUnresolvedSibling(t *testing.T) {
	src := `
		class A { b: B<number, string> }
		class B<T> { a: A, v: T }
	`
	_, types, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "A", types["A"])
}

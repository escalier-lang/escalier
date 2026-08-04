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

// TestRequiredTypeParamAfterDefault covers a default declared before a parameter that has none,
// `<T = number, U>`. Arguments bind positionally, so omitting the argument for `T` would leave
// `U` reading the one written for `T`. The default can therefore never be reached, and counting
// it as optional would let `Pair<string>` pass while leaving `U` a fresh var that coalesces to
// `never`.
//
// Two things report. The declaration reports the unreachable default, once per default and on
// every path that resolves a `<…>` clause. A reference short of the last required parameter
// reports the count, which runs to that parameter rather than to the first defaulted one.
func TestRequiredTypeParamAfterDefault(t *testing.T) {
	const unreachable = "the default for type parameter `T` can never be used, " +
		"since `U` is declared after it and has no default"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "AliasDeclaration",
			src:  `type Pair<T = number, U> = {a: T, b: U}`,
			want: []string{unreachable},
		},
		{
			name: "ClassDeclaration",
			src:  `class Pair<T = number, U> { a: T, b: U }`,
			want: []string{unreachable},
		},
		{
			name: "EnumDeclaration",
			src:  `enum Pair<T = number, U> { Both(a: T, b: U) }`,
			want: []string{unreachable},
		},
		{
			name: "FuncAnnotationDeclaration",
			src:  `declare fn f<T = number, U>(a: T, b: U) -> U`,
			want: []string{unreachable},
		},
		// The count runs to `U`, the last required parameter, so one argument is short of the
		// range rather than inside it. Without that the reference would pass and `U` would be
		// synthesized from nothing.
		{
			name: "ReferenceShortOfLastRequired",
			src: `
				type Pair<T = number, U> = {a: T, b: U}
				declare fn f() -> Pair<string>
			`,
			want: []string{unreachable, "type alias `Pair` expects 2 type arguments but got 1"},
		},
		{
			name: "ClassReferenceShortOfLastRequired",
			src: `
				class Pair<T = number, U> { a: T, b: U }
				declare fn f() -> Pair<string>
			`,
			want: []string{unreachable, "class `Pair` expects 2 type arguments but got 1"},
		},
		{
			name: "EnumReferenceShortOfLastRequired",
			src: `
				enum Pair<T = number, U> { Both(a: T, b: U) }
				declare fn f() -> Pair<string>
			`,
			want: []string{unreachable, "enum `Pair` expects 2 type arguments but got 1"},
		},
		// The default is kept rather than dropped, so a reference that writes every argument
		// still resolves against the full parameter list and reports only the declaration.
		{
			name: "ReferenceWritingEveryArgument",
			src: `
				type Pair<T = number, U> = {a: T, b: U}
				declare fn f() -> Pair<string, boolean>
			`,
			want: []string{unreachable},
		},
		// Each unreachable default is named, so one pass over the reports fixes the clause. Both
		// name `V`, the first parameter after them with no default.
		{
			name: "TwoUnreachableDefaults",
			src:  `type Trio<T = number, U = string, V> = {a: T, b: U, c: V}`,
			want: []string{
				"the default for type parameter `T` can never be used, since `V` is declared after it and has no default",
				"the default for type parameter `U` can never be used, since `V` is declared after it and has no default",
			},
		},
		// A default reaching the end of the clause is what the rule allows, since every parameter
		// from it on can be filled.
		{
			name: "TrailingDefaultAccepted",
			src: `
				type Pair<T, U = number> = {a: T, b: U}
				declare fn f() -> Pair<string>
			`,
		},
		{
			name: "EveryParamDefaultedAccepted",
			src: `
				type Pair<T = string, U = number> = {a: T, b: U}
				declare fn f() -> Pair
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

// TestSurplusTypeArgIsResolved checks that an argument written past the parameter count is
// resolved before it is dropped, so a diagnostic inside it still reports. Dropping it unresolved
// would hide the second problem behind the count, and fixing the count would then surface an
// error the author had no reason to expect.
func TestSurplusTypeArgIsResolved(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "OnGenericClass",
			src: `
				class Box<T> { v: T }
				declare fn f() -> Box<number, Nonexistent>
			`,
			want: []string{
				"class `Box` expects 1 type arguments but got 2",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "OnNonGenericClass",
			src: `
				class Point { x: number }
				declare fn f() -> Point<Nonexistent>
			`,
			want: []string{
				"class `Point` expects 0 type arguments but got 1",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "OnAlias",
			src: `
				type Box<T> = {v: T}
				declare fn f() -> Box<number, Nonexistent>
			`,
			want: []string{
				"type alias `Box` expects 1 type arguments but got 2",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "OnEnum",
			src: `
				enum Opt<T> { Some(value: T), None }
				declare fn f() -> Opt<number, Nonexistent>
			`,
			want: []string{
				"enum `Opt` expects 1 type arguments but got 2",
				"Unsupported: TypeRefTypeAnn",
			},
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

// TestClassArityCheckedFromOtherDeclarations checks that the count reaches a reference written
// anywhere, not only in a `fn` or `val` annotation. A class's members resolve in whatever order
// the dep graph reaches its declaration, so a class body, an alias body, and an enum variant
// parameter list all commonly name a class whose own pass has not run yet. The count is read
// off the declaration's `<…>` clause when the class's identity is registered, which is before
// any of them, so each reference is checked all the same.
func TestClassArityCheckedFromOtherDeclarations(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "FromClassBodyOmitted",
			src: `
				class B<T> { v: T }
				class A { b: B }
			`,
			want: "class `B` expects 1 type arguments but got 0",
		},
		// The declaration order is reversed from the row above, and the diagnostic is the same.
		{
			name: "FromClassBodyOmittedReversed",
			src: `
				class A { b: B }
				class B<T> { v: T }
			`,
			want: "class `B` expects 1 type arguments but got 0",
		},
		{
			name: "FromClassBodyTooMany",
			src: `
				class A { b: B<number, string> }
				class B<T> { v: T }
			`,
			want: "class `B` expects 1 type arguments but got 2",
		},
		{
			name: "FromAliasBody",
			src: `
				class B<T> { v: T }
				type A = {b: B}
			`,
			want: "class `B` expects 1 type arguments but got 0",
		},
		{
			name: "FromEnumVariantParam",
			src: `
				class B<T> { v: T }
				enum A { X(b: B) }
			`,
			want: "class `B` expects 1 type arguments but got 0",
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

// TestClassArityRecoveryKeepsDeclarationVarOut checks what an omitted class type argument
// recovers to. `A` declares no type parameters, so its constructor must not be generic. Filling
// the omitted argument with `B`'s own parameter var would make it one, since that var is
// quantified at `B`'s boundary and generalizing `A`'s constructor would capture it. A fresh var
// keeps `A` monomorphic.
func TestClassArityRecoveryKeepsDeclarationVarOut(t *testing.T) {
	src := `
		class B<T> { v: T }
		class A { b: B }
	`
	values, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "class `B` expects 1 type arguments but got 0", errs[0].Message())
	require.Equal(t, "{new (b: B<unknown>) -> A}", values["A"])
}

// TestClassDefaultUnfilledForUnresolvedSibling pins what the count reaching a reference early
// does not buy. A class's Arity is registered with its identity, but its resolved TypeParams —
// and so the defaults hanging off them — land only when that class's own pass runs. `A` is
// walked before `B`, so the omitted argument has no default to read yet and recovers to a fresh
// var: `B<unknown>` where the declaration says `B<number>`. No arity error is reported, since
// omitting an argument for a defaulted parameter is a legal reference.
//
// The same reference written in a `fn` annotation does fill the default, which is what
// TestTypeParamDefaultAtReference covers. Closing the gap needs the check deferred until every
// class in the component has its parameters, the machinery PR19 adds for bounds.
func TestClassDefaultUnfilledForUnresolvedSibling(t *testing.T) {
	src := `
		class B<T = number> { v: T }
		class A { b: B }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{new (b: B<unknown>) -> A}", values["A"])
}

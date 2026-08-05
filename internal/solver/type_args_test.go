package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeArgArityAtReference covers the argument-count check a reference gets from
// resolveTypeArgs. The valid count is the range from the parameters with no default up to the
// whole list, so the message states a single count when every parameter is required and a range
// when a default makes one optional.
//
// The rows vary the direction of the miscount and the branch of the message, and cover each
// declaration sort once for the noun it renders. An alias, a class, and an enum all reach the
// one helper, so a second row for a sort would re-run the same comparison.
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
			want: []string{"class `Box` expects 1 type argument but got 2"},
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
			want: []string{"class `Box` expects 1 type argument but got 0"},
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
			want: []string{"enum `Opt` expects 1 type argument but got 2"},
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
			want: []string{"type alias `Box` expects 1 type argument but got 2"},
		},
		// A reference that names the declaration it sits inside is checked like any other, so
		// a self-referential class body has to write its own arguments.
		{
			name: "ClassSelfReferenceBare",
			src: `
				class Node<T> { value: T, next: Node }
			`,
			want: []string{"class `Node` expects 1 type argument but got 0"},
		},
		{
			name: "ClassSelfReferenceWithArgAccepted",
			src: `
				class Node<T> { value: T, next: Node<T> }
			`,
		},
		{
			name: "ClassExactCountAccepted",
			src: `
				class Pair<T, U> { a: T, b: U }
				declare fn f() -> Pair<number, string>
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
// every path that resolves a `<…>` clause — four rows, since each is a separate call into
// resolveTypeParams. A reference short of the last required parameter reports the count, which
// runs to that parameter rather than to the first defaulted one. That has two rows rather than
// three: an alias and an enum share `arityOfParams`, while a class reads the count its
// declaration registered through `arityOfParamDecls`.
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
//
// The zero-parameter row is the one that is not a repeat: resolveTypeArgs returns early for a
// declaration with no parameters, so the resolve has to happen before that return. The alias row
// guards the same behavior on the path whose logic moved.
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
				"class `Box` expects 1 type argument but got 2",
				"cannot find type `Nonexistent`",
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
				"cannot find type `Nonexistent`",
			},
		},
		{
			name: "OnAlias",
			src: `
				type Box<T> = {v: T}
				declare fn f() -> Box<number, Nonexistent>
			`,
			want: []string{
				"type alias `Box` expects 1 type argument but got 2",
				"cannot find type `Nonexistent`",
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

// TestClassTypeParamDefaultIsPerReference covers a default that names an earlier parameter, and
// checks that the argument filled from it at one reference does not reach another. `U = T`
// substitutes the argument that reference wrote for `T`, so `f` and `g` each carry their own
// second argument instead of sharing one var declared on the class.
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
		// `B` is declared after the class that names it, and the diagnostic is the same one
		// TestClassArityRecoveryKeepsDeclarationVarOut gets for the two declared the other way
		// round, so the count does not depend on which class the dep graph reaches first.
		{
			name: "FromClassBodyDeclaredLater",
			src: `
				class A { b: B }
				class B<T> { v: T }
			`,
			want: "class `B` expects 1 type argument but got 0",
		},
		{
			name: "FromClassBodyTooMany",
			src: `
				class A { b: B<number, string> }
				class B<T> { v: T }
			`,
			want: "class `B` expects 1 type argument but got 2",
		},
		{
			name: "FromAliasBody",
			src: `
				class B<T> { v: T }
				type A = {b: B}
			`,
			want: "class `B` expects 1 type argument but got 0",
		},
		{
			name: "FromEnumVariantParam",
			src: `
				class B<T> { v: T }
				enum A { X(b: B) }
			`,
			want: "class `B` expects 1 type argument but got 0",
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
	require.Equal(t, "class `B` expects 1 type argument but got 0", errs[0].Message())
	require.Equal(t, "{new (b: B<unknown>) -> A}", values["A"])
}

// TestClassDefaultFilledFromClassBody checks that a reference from one class's body fills an
// omitted argument from the referenced class's default. The module SCC pre-pass resolves every
// class's type parameters before any body runs, so `A`'s field annotation reads `B`'s resolved
// default no matter which declaration the dep graph walks first.
func TestClassDefaultFilledFromClassBody(t *testing.T) {
	src := `
		class B<T = number> { v: T }
		class A { b: B }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{new (b: B<number>) -> A}", values["A"])
}

// TestClassArityAcrossRemainingRefForms extends TestClassArityCheckedFromOtherDeclarations to
// the reference positions it does not cover — an `extends` edge, an `implements` edge, a
// type-parameter bound, a constructor parameter, and a method return. Every form resolves
// through buildClassInstance, so each reports the same too-few diagnostic.
func TestClassArityAcrossRemainingRefForms(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "Extends", src: `
			class Box<T> { value: T }
			class Wrapper extends Box {
				constructor(mut self) {},
			}
		`},
		{name: "Implements", src: `
			class Box<T> { value: T }
			class Wrapper implements Box { value: number }
		`},
		{name: "TypeParamBound", src: `
			class Box<T> { value: T }
			class Holder<U: Box> { value: U }
		`},
		{name: "ConstructorParam", src: `
			class Box<T> { value: T }
			class Holder {
				boxed: Box<number>,
				constructor(mut self, boxed: Box) { self.boxed = boxed },
			}
		`},
		{name: "MethodReturn", src: `
			class Box<T> { value: T }
			class Holder {
				boxed: Box<number>,
				unwrap(self) -> Box { return self.boxed },
			}
		`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Len(t, errs, 1)
			require.Equal(t, "class `Box` expects 1 type arguments but got 0", errs[0].Message())
		})
	}
}

// TestClassDefaultFilledDeclarationOrderIndependent is TestClassDefaultFilledFromClassBody with
// the declarations the other way round, so the fill does not depend on which class the dep
// graph reaches first. The pre-pass resolves every class's parameters before any body runs,
// which is what makes both orders read the same default.
func TestClassDefaultFilledDeclarationOrderIndependent(t *testing.T) {
	src := `
		class A { b: B }
		class B<T = number> { v: T }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{new (b: B<number>) -> A}", values["A"])
}

// TestMutuallyRecursiveGenericClassesResolve guards the pre-pass against a mutually-recursive
// group. Each class names the other with a full argument list while the group's parameters
// resolve, so a pre-pass that read a half-registered sibling would report a spurious mismatch
// or leak one class's parameter var into the other.
func TestMutuallyRecursiveGenericClassesResolve(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Node<T> { value: T, tail: Tail<T> }
		class Tail<T> { node: Node<T> }
	`)
	require.Empty(t, errs)
	require.Equal(t, "<T0> {new (value: T0, tail: Tail<T0>) -> Node<T0>}", values["Node"])
}

// TestClassArityAcrossMixedComponent covers a dep_graph component holding both sorts of key. A
// class member body creates a value dependency, so `class A<T>` whose method calls `B` and
// `class B` whose field names `A` put A's type key and B's value key in one SCC. Binding every
// non-value key before the value walk is what lets B's annotation find A registered with its
// parameters resolved. Inferring B's body first would leave `A` an unbound name, reported as an
// unresolved reference instead of the arity mismatch it is.
func TestClassArityAcrossMixedComponent(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "GenericClassFirst", src: `
			class A<T> {
				v: T,
				make(self) -> number { return B(1, A(2)).x },
			}
			class B {
				x: number,
				a: A,
			}
		`},
		{name: "GenericClassSecond", src: `
			class B {
				x: number,
				a: A,
			}
			class A<T> {
				v: T,
				make(self) -> number { return B(1, A(2)).x },
			}
		`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Len(t, errs, 1)
			require.Equal(t, "class `A` expects 1 type arguments but got 0", errs[0].Message())
			require.Equal(t, "{new (x: number, a: A<unknown>) -> B}", values["B"])
		})
	}
}

// TestClassTypeArgBounds covers the bound a generic class declares on a type parameter,
// `class Box<T: string>`. A reference supplying an argument outside the bound is rejected at
// the reference, and one inside it is accepted. Every reference form routes through
// buildClassInstance, so a self-reference in the class's own body is checked the same way an
// annotation elsewhere is. A bound may name a sibling parameter, so the reference's own
// arguments are substituted into the bound before the comparison.
func TestClassTypeArgBounds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ArgumentOutsideBound",
			src: `
				class Box<T: string> { v: T }
				fn take(b: Box<number>) -> number { return 1 }
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "ArgumentInsideBound",
			src: `
				class Box<T: string> { v: T }
				fn take(b: Box<"a">) -> number { return 1 }
			`,
		},
		{
			name: "UnboundedParamAcceptsAnyArgument",
			src: `
				class Box<T> { v: T }
				fn take(b: Box<number>) -> number { return 1 }
			`,
		},
		{
			name: "SelfReferenceInFieldChecked",
			src:  `class Box<T: string> { v: T, other: Box<number> }`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "SelfReferenceInFieldSatisfied",
			src:  `class Box<T: string> { v: T, other: Box<"a"> }`,
		},
		{
			name: "ExtendsEdgeChecked",
			src: `
				class Box<T: string> { v: T }
				class Sub extends Box<number> {
					constructor(mut self) {},
				}
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "MethodParamChecked",
			src: `
				class Box<T: string> { v: T }
				class Holder {
					x: number,
					take(self, b: Box<number>) -> number { return self.x },
				}
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "SiblingBoundViolated",
			src: `
				class P<A, B: A> { a: A, b: B }
				fn take(p: P<string, 1>) -> number { return 1 }
			`,
			want: []string{"cannot constrain 1 <: string"},
		},
		{
			name: "SiblingBoundSatisfied",
			src: `
				class P<A, B: A> { a: A, b: B }
				fn take(p: P<number, 1>) -> number { return 1 }
			`,
		},
		{
			name: "IntersectionBound",
			src: `
				class Box<T: string & "a"> { v: T }
				fn take(b: Box<"b">) -> number { return 1 }
			`,
			want: []string{`cannot constrain "b" <: "a"`},
		},
		{
			name: "CrossClassBoundChecked",
			src: `
				class Animal { name: string }
				class Pen<T: Animal> { pet: T }
				fn take(p: Pen<number>) -> number { return 1 }
			`,
			want: []string{"cannot constrain number <: Animal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, test.want, msgs)
		})
	}
}

// TestClassBoundDefersForUnfilledAliasSibling checks the deferral a class bound check inherits
// from the alias path. A class reference inside an alias body resolves while a sibling alias's
// Body is still nil, and a comparison run at that moment would expand the sibling to ErrorType,
// which absorbs. The queued comparison replays once every body in the component is filled, so
// the violation still reports.
func TestClassBoundDefersForUnfilledAliasSibling(t *testing.T) {
	src := `
		class Box<T: string> { v: T }
		type A = {b: Box<number>, other: Other}
		type Other = {a?: A}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain number <: string", errs[0].Message())
}

// TestClassBoundForwardingFn covers a generic function whose return names the class with the
// function's own unbounded parameter, `fn g<U>(u: U) -> Box<U>`. The comparison is live rather
// than a discarded trial, so it leaves Box's bound on U and the call site is where a violation
// surfaces: the declaration alone is clean, a string call passes, and a number call reports
// once at the argument.
func TestClassBoundForwardingFn(t *testing.T) {
	t.Run("DeclarationAloneClean", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T: string> { v: T }
			fn g<U>(u: U) -> Box<U> { return Box(u) }
		`)
		require.Empty(t, errs)
	})
	t.Run("CallInsideBound", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T: string> { v: T }
			fn g<U>(u: U) -> Box<U> { return Box(u) }
			val b = g("a")
		`)
		require.Empty(t, errs)
	})
	t.Run("CallOutsideBound", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T: string> { v: T }
			fn g<U>(u: U) -> Box<U> { return Box(u) }
			val b = g(1)
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain 1 <: string", errs[0].Message())
	})
}

// TestClassBoundConstructionNotDoubleReported guards the seam between the two enforcement
// paths. A construction's argument flows into the parameter var, whose upper bound is the
// declared constraint, so `Box(1)` against `class Box<T: string>` reports through inference.
// The reference check covers annotations only, so a construction with no annotation reports
// exactly once.
func TestClassBoundConstructionNotDoubleReported(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Box<T: string> { v: T }
		val b = Box(1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain 1 <: string", errs[0].Message())
}

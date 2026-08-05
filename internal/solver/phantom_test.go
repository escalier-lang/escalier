package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A phantom parameter's argument cannot appear in the type an instantiation denotes, so two
// instantiations that differ only in those arguments are one type and check against each other in
// both directions. Each case below names a different reason the argument never lands in that type.
//
// want holds the warnings reportPhantomParams draws for the same aliases. Both halves read the same
// marks, so a case that compares equal here is a case that warns.
func TestInferPhantomArgumentsCompareEqual(t *testing.T) {
	// The warning `type Deep<T> = {a: Deep<{b: T}>}` draws, shared by the cases that declare it on
	// the source's second line.
	deepIsPhantom := []string{
		"2:15-2:16: no argument passed to type parameter T can appear in the type, so " +
			"Deep<number> and Deep<string> are the same type",
	}
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The recursive reference hands `{b: T}` to the next unfolding, so the payload is always
			// one unfolding deeper than the structure emitted so far. `Deep<number>` and
			// `Deep<string>` are both `{a: {a: {a: …}}}`.
			name: "PayloadAlwaysOneUnfoldingAhead",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<number>
				val d: Deep<string> = make()
			`,
			want: deepIsPhantom,
		},
		{
			// The same pair the other way round, since the erasure is a property of the identity
			// rather than of one side of the constraint.
			name: "PayloadAlwaysOneUnfoldingAheadReversed",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<string>
				val d: Deep<number> = make()
			`,
			want: deepIsPhantom,
		},
		{
			// Mutual recursion. P's parameter reaches only Q's parameter and Q's reaches only P's, so
			// neither ever lands in the `{a: {b: {a: …}}}` the pair denotes. The fixed point has to
			// settle both together, since each is phantom only because the other is.
			name: "MutuallyRecursivePair",
			src: `
				type P<T> = {a: Q<T>}
				type Q<U> = {b: P<U>}
				declare fn make() -> P<number>
				val d: P<string> = make()
			`,
			want: []string{
				"3:12-3:13: no argument passed to type parameter U can appear in the type, so " +
					"Q<number> and Q<string> are the same type",
				"2:12-2:13: no argument passed to type parameter T can appear in the type, so " +
					"P<number> and P<string> are the same type",
			},
		},
		{
			// A parameter the body never mentions. `Ignore<T>` denotes `number` whatever T is, so no
			// recursion is involved at all.
			name: "ParameterUnusedInTheBody",
			src: `
				type Ignore<T> = number
				declare fn make() -> Ignore<number>
				val d: Ignore<string> = make()
			`,
			want: []string{"2:17-2:18: type parameter T is declared but never used"},
		},
		{
			// The erasure reaches a nested reference, so a relevant parameter carrying a phantom
			// instantiation still compares equal. Hold's own T is relevant, since it lands at `held`.
			name: "PhantomInstantiationInsideARelevantArgument",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				type Hold<T> = {held: T}
				declare fn make() -> Hold<Deep<number>>
				val d: Hold<Deep<string>> = make()
			`,
			want: deepIsPhantom,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

// An alias parameter a caller gains nothing by writing an argument for draws a warning. The two
// tiers differ in why the argument says nothing. An unused parameter occurs nowhere, and an
// unreachable one occurs in the body but only at positions the type an instantiation denotes never
// reaches.
func TestInferPhantomParamWarns(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The degenerate case. `Ignore<T>` denotes `number` whatever the argument is.
			name: "ParameterOccursNowhere",
			src:  `type Ignore<T> = number`,
			want: []string{"1:13-1:14: type parameter T is declared but never used"},
		},
		{
			// One parameter of several. U is written at `b`, so only T is reported.
			name: "OneParameterOfSeveralOccursNowhere",
			src:  `type Half<T, U> = {b: U}`,
			want: []string{"1:11-1:12: type parameter T is declared but never used"},
		},
		{
			// A parameter's own bound is not a use of it, so an F-bound leaves the parameter
			// unused when the body does not write it.
			name: "ParameterOccursOnlyInItsOwnBound",
			src:  `type Rec<T: {a: T}> = number`,
			want: []string{"1:10-1:19: type parameter T is declared but never used"},
		},
		{
			// The recursive reference hands `{b: T}` to the next unfolding forever, so T occurs in
			// the body and no argument passed to it lands in the type Deep denotes.
			name: "ParameterOccursOnlyAtAnUnreachablePosition",
			src:  `type Deep<T> = {a: Deep<{b: T}>}`,
			want: []string{
				"1:11-1:12: no argument passed to type parameter T can appear in the type, so " +
					"Deep<number> and Deep<string> are the same type",
			},
		},
		{
			// A leading underscore does not reach this tier. It says the author meant to leave a
			// parameter unwritten, which answers the unused warning about their own declaration.
			// This warning is about a caller who writes two instantiations that denote one type,
			// and the name the parameter was given is neither visible to them nor able to make
			// the arguments differ.
			name: "UnreachableParameterIsStillWarnedWhenUnderscored",
			src:  `type Deep<_T> = {a: Deep<{b: _T}>}`,
			want: []string{
				"1:11-1:13: no argument passed to type parameter _T can appear in the type, so " +
					"Deep<number> and Deep<string> are the same type",
			},
		},
		{
			// The rendered instantiations differ in the unreachable slot alone. U is written at
			// `here`, so it stays under its own name on both sides.
			name: "OtherParametersStayNamedInTheMessage",
			src:  `type Slot<T, U> = {here: U, deeper: Slot<{b: T}, U>}`,
			want: []string{
				"1:11-1:12: no argument passed to type parameter T can appear in the type, so " +
					"Slot<number, U> and Slot<string, U> are the same type",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
			require.Len(t, errs, 1)
			require.True(t, isWarning(errs[0]))
		})
	}
}

// An alias parameter that carries weight draws no warning, whatever the phantom marks say. The
// marks answer whether an argument reaches the denoted type through the parameter's own slot, so a
// parameter that bounds a sibling or supplies a sibling's default is marked phantom while still
// deciding what an instantiation denotes. A leading underscore quiets the unused tier, which is how
// a binder left to fill in later is written; it does not reach the unreachable tier.
func TestInferPhantomParamStaysSilent(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// The ordinary generic alias. T lands at `head`, inside the object the alias denotes.
			name: "ParameterReachesTheDenotedType",
			src:  `type List<T> = {head: T}`,
		},
		{
			// T is marked phantom, since no argument reaches the denoted type through T's slot.
			// It bounds U, so `Foo<number, 1>` and `Foo<string, 1>` differ in what they accept.
			name: "ParameterBoundsASibling",
			src:  `type Foo<T, U: T> = {x: U}`,
		},
		{
			// T is marked phantom for the same reason and supplies U's default, so `Pair<number>`
			// denotes `{b: number}` and `Pair<string>` denotes `{b: string}`.
			name: "ParameterSuppliesASiblingsDefault",
			src:  `type Pair<T, U = T> = {b: U}`,
		},
		{
			// The escape hatch on the unused tier.
			name: "UnusedParameterIsUnderscored",
			src:  `type Ignore<_T> = number`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Empty(t, messagesWithSpan(errs))
		})
	}
}

// A parameter that does reach the type an alias denotes stays relevant, so its argument stays in the
// canonical identity and a mismatch is still reported. `Nest<T>` writes T at `here`, inside the
// object every unfolding emits, which is what separates it from `Deep<T>` above.
func TestInferRelevantParameterKeepsItsArgument(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Nest<T> = {here: T, deeper: Nest<{b: T}>}
		declare fn make() -> Nest<number>
		val d: Nest<string> = make()
	`)
	require.Contains(t, messagesWithSpan(errs), "4:25-4:31: cannot constrain number <: string")
}

// The erasure is confined to the canonical identity constrain keys on. A reference still carries the
// arguments the source wrote, so a binding renders under those and not under an erased form.
func TestInferPhantomArgumentSurvivesInTheRenderedType(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Deep<T> = {a: Deep<{b: T}>}
		declare fn make() -> Deep<number>
		val d: Deep<string> = make()
		fn f(p: Deep<number>) -> Deep<number> { return p }
	`)
	require.Equal(t, []string{
		"2:13-2:14: no argument passed to type parameter T can appear in the type, so " +
			"Deep<number> and Deep<string> are the same type",
	}, messagesWithSpan(errs))
	require.Equal(t, "Deep<string>", values["d"])
	require.Equal(t, "fn (p: Deep<number>) -> Deep<number>", values["f"])
}

// The unused tier reaches a class and an enum as well as an alias, since a parameter no
// position of the declaration writes is dead weight whatever sort declares it.
//
// The unreachable tier stays with aliases. A class and an enum variant are nominal, so
// constrain compares a handle's arguments position by position and an argument to a
// parameter the body does write stays observable. `class Nest<T> {deeper: Nest<{b: T}>}`
// reports a mismatch between `Nest<number>` and `Nest<string>` where the alias of that
// shape settles them as one type.
func TestInferUnusedTypeParamOnClassAndEnum(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// No member, no super, and no constructor parameter writes T.
			name: "ClassParameterOccursNowhere",
			src:  `class Box<T> { x: number }`,
			want: []string{"1:11-1:12: type parameter T is declared but never used"},
		},
		{
			// One parameter of several. U is the field's type, so only T is reported.
			name: "ClassOneParameterOfSeveralOccursNowhere",
			src:  `class Two<T, U> { x: U }`,
			want: []string{"1:11-1:12: type parameter T is declared but never used"},
		},
		{
			// No variant carries a parameter at all.
			name: "EnumParameterOccursNowhere",
			src:  `enum Opt<T> { A }`,
			want: []string{"1:10-1:11: type parameter T is declared but never used"},
		},
		{
			// A variant writes U, so only T is reported.
			name: "EnumOneParameterOfSeveralOccursNowhere",
			src:  `enum Two<T, U> { A(v: U) }`,
			want: []string{"1:10-1:11: type parameter T is declared but never used"},
		},
		{
			// A field's type is a use.
			name: "ClassFieldWritesTheParameter",
			src:  `class Box<T> { x: T }`,
		},
		{
			// So is a method parameter, which the `self` receiver alone would not be.
			name: "ClassMethodWritesTheParameter",
			src:  `class Hold<T> { put(self, v: T) -> number { return 1 } }`,
		},
		{
			// So is a superclass type argument.
			name: "ClassSuperWritesTheParameter",
			src: `
				class Base<T> { x: T }
				class Sub<T> extends Base<T> { constructor(mut self) {} }
			`,
		},
		{
			// So is an `implements` type argument, the only position T occupies here.
			name: "ClassImplementsWritesTheParameter",
			src: `
				class Marker<T> { m: T }
				class Tag<T> implements Marker<T> { constructor(mut self) {} }
			`,
		},
		{
			// So is a constructor parameter, which lives on the class's value binding rather
			// than in either member object.
			name: "ClassConstructorWritesTheParameter",
			src:  `class Take<T> { x: number, constructor(mut self, v: T) { self.x = 1 } }`,
		},
		{
			// So is a constructor's `throws` clause. Every caller of the constructor has to
			// handle what it declares, so the parameter naming it is doing work.
			name: "ClassConstructorThrowsTheParameter",
			src: `
				declare fn boom() throws number
				class Boom<E> { x: number, constructor(mut self) throws E { self.x = 1
			boom() } }
			`,
		},
		{
			// A sibling's bound is a use, the same exemption the alias tier makes.
			name: "ClassParameterBoundsASibling",
			src:  `class Pair<T, U: T> { x: U }`,
		},
		{
			// As is a sibling's default.
			name: "ClassParameterSuppliesASiblingsDefault",
			src:  `class Def<T, U = T> { x: U }`,
		},
		{
			// A variant parameter is a use.
			name: "EnumVariantWritesTheParameter",
			src:  `enum Opt<T> { A(v: T) }`,
		},
		{
			name: "ClassParameterIsUnderscored",
			src:  `class Box<_T> { x: number }`,
		},
		{
			name: "EnumParameterIsUnderscored",
			src:  `enum Opt<_T> { A }`,
		},
		{
			// The class body drove the recursion, so T is written and the nominal comparison
			// keeps every argument to it observable. Nothing is reported.
			name: "ClassRecursionThroughAGrowingArgument",
			src:  `class Nest<T> { deeper: Nest<{b: T}> }`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

// A declaration that already drew a diagnostic reports no unused warning. Recovery drops
// the subtree it could not resolve, so a parameter written only there leaves no occurrence
// behind and would read as unused. Each case writes its parameter exactly once, inside the
// part that is dropped.
func TestInferUnusedTypeParamSkipsARecoveredDeclaration(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// resolveObjectTypeAnn recovers a property whose value did not resolve to a fresh
			// var and keeps the object shape, so the body resolves to `{a: t}` and the only
			// occurrence of T is what went missing.
			name: "AliasBodyWithARecoveredPropertyValue",
			src:  `type M<T> = {a: Nope<T>}`,
			want: []string{"1:17-1:24: cannot find type `Nope`"},
		},
		{
			// The union member recovers to a fresh var, so the body is `t | number` and the T
			// that member wrote is gone.
			name: "AliasBodyWithAnUnresolvableReference",
			src:  `type Foo<T> = number | Nope<T>`,
			want: []string{"1:24-1:31: cannot find type `Nope`"},
		},
		{
			// A bound that fails to resolve is left nil, so the T it wrote is lost along with
			// U's own reason to exist. The parameter list resolves before the body, so both
			// warnings would land had preBindAlias not opened its own window.
			name: "AliasParameterWithAnUnresolvableBound",
			src:  `type Foo<T, U: Nope<T>> = number`,
			want: []string{"1:16-1:23: cannot find type `Nope`"},
		},
		{
			// The same for a default, which is the other position resolveTypeParams fills.
			name: "AliasParameterWithAnUnresolvableDefault",
			src:  `type Bar<T, U = Nope<T>> = {x: U}`,
			want: []string{"1:17-1:24: cannot find type `Nope`"},
		},
		{
			// A type parameter does not name a class, so the extends edge is dropped and the
			// only occurrence of T goes with it.
			name: "ClassExtendingATypeParameter",
			src:  `class B<T> extends T { constructor(mut self) {} }`,
			want: []string{
				"1:20-1:21: `T` does not name a class and cannot be extended or implemented.",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

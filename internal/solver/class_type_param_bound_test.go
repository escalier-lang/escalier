package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestADeclaredTypeParamBoundRendersOnItsBinder covers where a declared bound appears in
// a class value's rendered type.
//
// A class's parameters are the binder of its constructor signature, so the value renders
// them there with the bound and default the declaration wrote, and coalescing keeps each
// one symbolic rather than merging it with its bound. Without that `peer: T` would read
// back as `peer: T & {value: number}`.
func TestADeclaredTypeParamBoundRendersOnItsBinder(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "OnAConstructorParameter",
			src:  `class Holder<T: {value: number}> { peer: T }`,
			want: "{new <T: {value: number}>(peer: T) -> Holder<T>}",
		},
		// An unbounded parameter has no bound to merge with, so it renders as it always
		// did and the slot its handle elides keeps eliding.
		{
			name: "AnUnboundedParameterRendersBare",
			src:  `class Holder<T> { peer: T }`,
			want: "{new <T>(peer: T) -> Holder<T>}",
		},
		{
			name: "ABoundBesideADefault",
			src:  `class Holder<T: {value: number} = {value: number}> { peer: T }`,
			want: "{new <T: {value: number} = {value: number}>(peer: T) -> Holder<T>}",
		},
		// Two bounds meet, so the binder joins them.
		{
			name: "TwoBoundsJoin",
			src: `class Holder<T: {a: number} & {b: string}> {
				peer: T,
			}`,
			want: "{new <T: {a: number} & {b: string}>(peer: T) -> Holder<T>}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, test.want, values["Holder"])
		})
	}
}

// A bound may name something the prefix binds later, which is why the bound renders
// after every binder is named rather than beside its own. The guard for that ordering is
// TestClassTypeParamBoundSeesTheClassLifetime, whose bound names the class's lifetime
// parameter. A bound naming a sibling TYPE parameter cannot serve as the guard, because
// the display merges two parameters one of which bounds the other: `class Holder<T, U: T>`
// renders `<T> {new (first: T, second: T) -> Holder<T, T>}` while accepting a
// `Holder<number, 1>`. That is #1789.

// TestADeclaredFunctionBoundRendersOnItsBinder asserts that a function's own parameters
// keep their bound on the binder. A FuncType carries its parameters, so they are found
// from the type and need no registry lookup. The class cases above are what that path
// does not cover.
func TestADeclaredFunctionBoundRendersOnItsBinder(t *testing.T) {
	values, _, errs := inferSource(t, `declare fn keep<T: {value: number}>(p: T) -> T`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn <T: {value: number}>(p: T) -> T", values["keep"])
}

// TestADeclaredBoundSurvivesASecondBinding asserts that a class value reached through
// another binding renders its bounds the way the class's own binding does.
//
// A class keeps its parameters in the Context registry under the variables the
// declaration minted, while a second binding holds a copy under variables of its own. The
// bound is written in terms of the declaration's variables, so it is rewritten to the
// copy's alongside the variable it belongs to. Without that it would name a variable no
// binder in the print reaches, which renders as the `t0` leak anchor.
func TestADeclaredBoundSurvivesASecondBinding(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "ABoundNamingItsOwnParameter",
			src:  `class Holder<T: {next: T}> { peer: T }`,
		},
		{
			name: "ABoundNamingASiblingParameter",
			src:  `class Holder<T: {value: number}, U: {o: T}> { a: T, b: U }`,
		},
		{
			name: "ABoundNamingAClassLifetime",
			src:  `class Holder<'a, T: &'a {value: number}> { peer: T }`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src+"\nval Alias = Holder")
			require.Empty(t, errorMessagesOf(errs))
			require.NotEmpty(t, values["Holder"])
			require.Equal(t, values["Holder"], values["Alias"])
			require.NotContains(t, values["Alias"], "t0",
				"a variable no binder names renders as the leak anchor")
		})
	}
}

// TestAVacuousSelfBoundIsDroppedFromTheBinder asserts that `f` renders
// `fn <U: string>(b: Box<U>) -> U`.
//
// `Box`'s `T: string` reaches `U` through the parameter annotation, and the comparison
// is a live constraint, so solving leaves `U` merged with an inference variable that
// bounds it from the other side. That variable says nothing a caller can use, and
// cleanBinderBounds drops both it and the bound naming it. Without that, `f` renders
// `fn <T0, U: string & T0>(b: Box<U>) -> U`, with a binder for a variable the signature
// has no other use for.
//
// This is the one shape in the repository that reaches cleanBinderBounds' same-class
// cleanup, found by instrumenting the drop and running the suite.
func TestAVacuousSelfBoundIsDroppedFromTheBinder(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Box<T: string> = {v: T}
		fn f<U>(b: Box<U>) -> U { return b.v }
	`)

	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn <U: string>(b: Box<U>) -> U", values["f"])
}

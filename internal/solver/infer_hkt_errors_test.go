package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferTypeConstructorErrors covers the diagnostics a type-constructor parameter reports:
// an argument that names no constructor of the right arity, an unresolvable name inside a
// constructor or an application, and a rigid head compared with a concrete instance or with
// another rigid head.
func TestInferTypeConstructorErrors(t *testing.T) {
	const prelude = `
type Functor<F<_>> = {map: fn <A, B>(fa: F<A>, f: fn (a: A) -> B) -> F<B>}
class Box<T> { value: T }
class Tagged<E, A> { tag: E, value: A }
type Bi<F<_, _>> = {x: F<number, string>}
`
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name:     "ObjectTypeIsNotAConstructor",
			src:      `type X = Functor<{a: number}>`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "UnknownConstructor",
			src:      `type X = Functor<Nope>`,
			wantErrs: []string{"cannot find type `Nope`"},
		},
		{
			name:     "ConstructorParamOfOtherArity",
			src:      `fn f<G<_, _>>(x: Functor<G>) -> number { return 1 }`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "OrdinaryParamIsNotAConstructor",
			src:      `fn f<T>(x: Functor<T>) -> number { return 1 }`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "TooManyConstructorArgs",
			src:      `type X = Functor<Box<number, string>>`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "UnknownFixedArg",
			src:      `type X = Functor<Tagged<Nope, _>>`,
			wantErrs: []string{"cannot find type `Nope`"},
		},
		{
			name:     "UnknownApplicationArg",
			src:      `fn f<F<_>>(x: F<Nope>) -> number { return 1 }`,
			wantErrs: []string{"cannot find type `Nope`"},
		},
		{
			name:     "BinaryParamRejectsUnaryConstructor",
			src:      `type X = Bi<Box>`,
			wantErrs: []string{"expected a type constructor taking 2 type arguments"},
		},
		{
			name: "BinaryParamAcceptsBinaryConstructor",
			src:  `type X = Bi<Tagged>`,
		},
		{
			name: "RigidHeadRejectsInstance",
			src: `fn bad<F<_>>(fa: F<number>) -> F<number> {
				val b: Box<number> = Box(1)
				return b
			}`,
			wantErrs: []string{"cannot constrain Box<number> <: F<number>"},
		},
		{
			name:     "TwoRigidHeadsDiffer",
			src:      `fn two<F<_>, G<_>>(fa: F<number>) -> G<number> { return fa }`,
			wantErrs: []string{"cannot constrain F<number> <: G<number>"},
		},
		{
			name: "InstanceHasTooFewArgs",
			src: `fn f<F<_, _>, A, B>(x: F<A, B>) -> F<A, B> { return x }
			val b: Box<number> = Box(1)
			val r = f(b)`,
			wantErrs: []string{"cannot constrain Box<number> <: t39<t40, t41>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, prelude+tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.wantErrs, errorMessagesOf(errs))
		})
	}
}

// TestConstrainTypeConstructors covers constrain on constructors and applications directly. Two
// constructors relate when they name one declaration and fix the same arguments, and two
// applications relate only when they apply the same number of arguments.
func TestConstrainTypeConstructors(t *testing.T) {
	num := &soltype.PrimType{Prim: soltype.NumPrim}
	str := &soltype.PrimType{Prim: soltype.StrPrim}
	ctor := func(name string, fixed ...soltype.Type) *soltype.TypeCtorType {
		return &soltype.TypeCtorType{Base: &soltype.ClassType{Name: name}, Fixed: fixed}
	}
	f := &soltype.SkolemType{ID: 9, Name: "F"}
	tests := []struct {
		name       string
		sub, super soltype.Type
		want       []string
	}{
		{name: "SameConstructor", sub: ctor("Box", nil), super: ctor("Box", nil), want: nil},
		{name: "DifferentConstructors", sub: ctor("Box", nil), super: ctor("Array", nil),
			want: []string{"cannot constrain Box <: Array"}},
		{name: "DifferentFixedArgs", sub: ctor("Tagged", num, nil), super: ctor("Tagged", str, nil),
			want: []string{"cannot constrain number <: string", "cannot constrain string <: number"}},
		{name: "DifferentHoles", sub: ctor("Tagged", num, nil), super: ctor("Tagged", nil, num),
			want: []string{"cannot constrain Tagged<number, _> <: Tagged<_, number>"}},
		{name: "ConstructorAgainstType", sub: ctor("Box", nil), super: num,
			want: []string{"cannot constrain Box <: number"}},
		{name: "ApplicationsOfDifferentArity",
			sub:   &soltype.TypeAppType{Head: f, Args: []soltype.Type{num}},
			super: &soltype.TypeAppType{Head: f, Args: []soltype.Type{num, num}},
			want:  []string{"cannot constrain F<number> <: F<number, number>"}},
		{name: "InstanceShorterThanApplication",
			sub:   &soltype.TypeAppType{Head: f, Args: []soltype.Type{num, num}},
			super: &soltype.ClassType{Name: "Box", TypeArgs: []soltype.Type{num}},
			want:  []string{"cannot constrain F<number, number> <: Box<number>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Messages((&Context{}).Constrain(tt.sub, tt.super)))
		})
	}
}

// TestNotATypeCtorErrorSpan asserts that the error spans the argument it names and carries no
// related spans.
func TestNotATypeCtorErrorSpan(t *testing.T) {
	_, _, errs := inferSource(t, `type Functor<F<_>> = {x: F<number>}
type X = Functor<number>`)
	require.Len(t, errs, 1)
	require.Equal(t, "2:18-2:24: expected a type constructor taking 1 type argument", msgWithSpan(t, errs[0]))
	require.Empty(t, errs[0].Related())
}

// TestInferTypeConstructorMatching covers how head matching reads an instance. An instance with
// more arguments than the application takes is tried at each choice of hole positions, so a
// constructor with a leading hole is found whichever argument binds the head first. An alias that
// splits no way is matched through its body. A class field typed `F<T>` makes the class
// invariant in T, since an application relates its arguments invariantly.
func TestInferTypeConstructorMatching(t *testing.T) {
	const prelude = `
type Functor<F<_>> = {map: fn <A, B>(fa: F<A>, f: fn (a: A) -> B) -> F<B>}
class Tagged<E, A> { tag: E, value: A }
fn id<F<_>, A>(fa: F<A>) -> F<A> { return fa }
val tf: Functor<Tagged<_, number>> = {map: fn (fa, f) { return Tagged(f(fa.tag), fa.value) }}
`
	tests := []struct {
		name     string
		src      string
		want     map[string]string
		wantErrs []string
	}{
		{
			name: "LeadingHoleValueFirst",
			src: `fn g<F<_>>(fa: F<string>, functor: Functor<F>) -> F<string> { return fa }
			val t: Tagged<string, number> = Tagged("a", 1)
			val r = g(t, tf)`,
			want: map[string]string{"r": "Tagged<string, number>"},
		},
		{
			name: "LeadingHoleFunctorFirst",
			src: `fn g<F<_>>(functor: Functor<F>, fa: F<string>) -> F<string> { return fa }
			val t: Tagged<string, number> = Tagged("a", 1)
			val r = g(tf, t)`,
			want: map[string]string{"r": "Tagged<string, number>"},
		},
		{
			name: "LeadingHoleDisagrees",
			src: `fn g<F<_>>(functor: Functor<F>, fa: F<string>) -> F<string> { return fa }
			val t: Tagged<string, string> = Tagged("a", "b")
			val r = g(tf, t)`,
			wantErrs: []string{"cannot constrain string <: number"},
		},
		{
			name: "AliasMatchedThroughBody",
			src: `type Nums = Array<number>
			val n: Nums = [1]
			val r = id(n)`,
			want: map[string]string{"r": "Array<number>"},
		},
		{
			name: "ApplicationFieldMakesClassInvariant",
			src: `class Wrap<F<_>, T> { inner: F<T> }
			fn w(x: Wrap<Array, 5>) -> Wrap<Array, number> { return x }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, prelude+tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errorMessagesOf(errs))
			} else {
				require.Equal(t, tt.wantErrs, errorMessagesOf(errs))
			}
			for name, want := range tt.want {
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestInferApplicationFieldVariance asserts that a class field typed `F<T>` gives the class the
// variance F's slot declares for T.
func TestInferApplicationFieldVariance(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "CovariantSlotWidens",
			src: `class Wrap<F<out _>, T> { inner: F<T> }
			fn w(x: Wrap<Array, 5>) -> Wrap<Array, number> { return x }`,
		},
		{
			// A write through the borrow reaches the field, so even a covariant slot is invariant.
			name: "MutBorrowRejectsWidening",
			src: `class Wrap<F<out _>, T> { inner: F<T> }
			fn w(x: mut Wrap<Array, 5>) -> mut Wrap<Array, number> { return x }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
		{
			name: "InvariantSlotRejectsWidening",
			src: `class Wrap<F<_>, T> { inner: F<T> }
			fn w(x: Wrap<Array, 5>) -> Wrap<Array, number> { return x }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.wantErrs, errorMessagesOf(errs))
		})
	}
}

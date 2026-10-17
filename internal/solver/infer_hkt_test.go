package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// hktPrelude declares a Functor abstraction over a type-constructor parameter and instances of it
// for a prelude class, a user class, a partially applied user class, and an enum. The Array
// instance maps through a declared helper because the test prelude's Array carries no `map`.
const hktPrelude = `
type Functor<F<_>> = {
	map: fn <A, B>(fa: F<A>, f: fn (a: A) -> B) -> F<B>,
}
fn double<F<_>>(functor: Functor<F>, fa: F<number>) -> F<number> {
	return functor.map(fa, fn (n) { return n * 2 })
}
declare fn mapArray<A, B>(xs: Array<A>, f: fn (a: A) -> B) -> Array<B>
val arrayFunctor: Functor<Array> = {
	map: fn (fa, f) { return mapArray(fa, f) },
}
class Box<T> { value: T }
val boxFunctor: Functor<Box> = {
	map: fn (fa, f) { return Box(f(fa.value)) },
}
class Tagged<E, A> { tag: E, value: A }
val taggedFunctor: Functor<Tagged<string, _>> = {
	map: fn (fa, f) { return Tagged(fa.tag, f(fa.value)) },
}
enum MyOption<T> { Some(value: T), None }
val optionFunctor: Functor<MyOption> = {
	map: fn (fa, f) {
		return match fa {
			MyOption.Some(value) => MyOption.Some(f(value)),
			MyOption.None() => MyOption.None(),
		}
	},
}
`

// TestInferTypeConstructorParams covers type-constructor parameters such as F in `<F<_>>`. The
// cases cover:
//
//   - the instances in hktPrelude, which check once `F<A>` is substituted to a concrete instance
//   - a body generic over F, where F stays rigid
//   - call sites that infer F by matching the head of a named instance
//   - partial application, where `Tagged<string, _>` fixes the leading argument
//   - arity errors and constructor arguments that name no constructor
func TestInferTypeConstructorParams(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want holds the rendered type of each listed value binding.
		want map[string]string
		// wantErrs is every diagnostic in order. nil expects none.
		wantErrs []string
	}{
		{
			name: "SignaturesRender",
			src:  ``,
			want: map[string]string{
				"double":        "fn <F<_>>(functor: Functor<F>, fa: F<number>) -> F<number>",
				"arrayFunctor":  "Functor<Array>",
				"taggedFunctor": "Functor<Tagged<string, _>>",
			},
		},
		{
			name: "ArrayHeadInferred",
			src: `val xs: Array<number> = [1, 2, 3]
			val r = double(arrayFunctor, xs)`,
			want: map[string]string{"r": "Array<number>"},
		},
		{
			name: "UserClassHeadInferred",
			src: `val b: Box<number> = Box(5)
			val r = double(boxFunctor, b)`,
			want: map[string]string{"r": "Box<number>"},
		},
		{
			name: "PartiallyAppliedHeadInferred",
			src: `val tg: Tagged<string, number> = Tagged("x", 5)
			val r = double(taggedFunctor, tg)`,
			want: map[string]string{"r": "Tagged<string, number>"},
		},
		{
			name: "EnumHeadInferred",
			src: `val o: MyOption<number> = MyOption.Some(5)
			val r = double(optionFunctor, o)`,
			want: map[string]string{"r": "MyOption<number>"},
		},
		{
			name: "HeadInferredWithoutInstance",
			src: `fn id<F<_>, A>(fa: F<A>) -> F<A> { return fa }
			val xs: Array<number> = [1]
			val r = id(xs)`,
			want: map[string]string{"r": "Array<number>"},
		},
		{
			// A body generic over G passes G on to double, whose own F binds to the rigid G.
			name: "RigidHeadPassedToGenericCall",
			src: `fn quad<G<_>>(functor: Functor<G>, ga: G<number>) -> G<number> {
				return double(functor, ga)
			}
			val xs: Array<number> = [1]
			val r = quad(arrayFunctor, xs)`,
			want: map[string]string{
				"quad": "fn <G<_>>(functor: Functor<G>, ga: G<number>) -> G<number>",
				"r":    "Array<number>",
			},
		},
		{
			// A copy of a generic signature keeps its parameter's arity.
			name: "CopiedSignatureKeepsArity",
			src: `fn wrap<F<_>>(fa: F<number>) -> F<number> { return fa }
			val w = wrap`,
			want: map[string]string{"w": "fn <F<_>>(fa: F<number>) -> F<number>"},
		},
		{
			// An argument list that stops early fills the rest from defaults.
			name: "PartialArgsFillDefaults",
			src: `class P<T, E = string> { t: T, e: E }
			type Y = Functor<P<_>>`,
		},
		{
			// A bare constructor fills a trailing default to reach the arity asked for.
			name: "BareConstructorFillsDefault",
			src: `class P<T, E = string> { t: T, e: E }
			type Y = Functor<P>`,
		},
		{
			// A default naming a sibling parameter cannot be fixed while that sibling is a hole.
			name: "DefaultNamingHoleNotFilled",
			src: `class D<T, U = Array<T>> { t: T, u: U }
			type Y = Functor<D>`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name: "ConstructorParamDefault",
			src: `type Q<F<_> = Array> = {x: F<number>}
			val q: Q = {x: [1]}`,
		},
		{
			name:     "ConstructorParamBoundUnsupported",
			src:      `type Q<F<_>: Array> = {x: F<number>}`,
			wantErrs: []string{"Unsupported: bound on a type constructor parameter"},
		},
		{
			name: "InstanceAndValueDisagree",
			src: `val b: Box<number> = Box(5)
			val r = double(arrayFunctor, b)`,
			wantErrs: []string{"cannot constrain Box<number> <: Array<number>"},
		},
		{
			name: "TwoArgumentsDisagree",
			src: `fn pick<F<_>>(fa: F<number>, fb: F<number>) -> F<number> { return fa }
			val xs: Array<number> = [1]
			val b: Box<number> = Box(5)
			val r = pick(xs, b)`,
			wantErrs: []string{"cannot constrain Box<number> <: Array<number>"},
		},
		{
			name:     "StructuralValueHasNoHead",
			src:      `val r = double(boxFunctor, {value: 5})`,
			wantErrs: []string{"cannot constrain object <: class Box<number>"},
		},
		{
			name:     "RigidHeadHasNoMembers",
			src:      `fn bad<F<_>>(fa: F<number>) -> number { return fa.length }`,
			wantErrs: []string{"cannot constrain F<number> <: object"},
		},
		{
			name:     "RigidHeadRejectsConcreteValue",
			src:      `fn bad<F<_>>(fa: F<number>) -> F<number> { return [1] }`,
			wantErrs: []string{"cannot constrain tuple <: F<number>"},
		},
		{
			// Arguments are invariant, since a constructor parameter declares no variance.
			name:     "ArgumentsAreInvariant",
			src:      `fn widen<F<_>>(fa: F<5>) -> F<number> { return fa }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
		{
			name:     "TypeArgumentIsNotAConstructor",
			src:      `type T = Functor<number>`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "ConstructorArityMismatch",
			src:      `type T = Functor<Tagged>`,
			wantErrs: []string{"expected a type constructor taking 1 type argument"},
		},
		{
			name:     "BareConstructorParam",
			src:      `fn f<F<_>>(x: F) -> number { return 1 }`,
			wantErrs: []string{"type constructor parameter `F` expects 1 type argument but got 0"},
		},
		{
			name:     "ConstructorParamTooManyArgs",
			src:      `fn f<F<_>>(x: F<number, string>) -> number { return 1 }`,
			wantErrs: []string{"type constructor parameter `F` expects 1 type argument but got 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, hktPrelude+tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errorMessagesOf(errs))
			} else {
				require.Equal(t, tt.wantErrs, errorMessagesOf(errs))
			}
			for name, want := range tt.want {
				require.Contains(t, values, name)
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestInferTypeConstructorVariance covers the variance a type-constructor parameter declares on
// each slot, as `F<out _>`. The cases cover subtyping between applications under each variance,
// the kind check on a named constructor argument, the kind check on a head a call infers, and
// passing one constructor parameter where another is expected.
func TestInferTypeConstructorVariance(t *testing.T) {
	const prelude = `
class Box<T> { value: T }
class Sink<T> { put: fn (x: T) -> undefined }
type CoFunctor<F<out _>> = {map: fn <A, B>(fa: F<A>, f: fn (a: A) -> B) -> F<B>}
type ContraBox<F<in _>> = {x: F<number>}
fn widen<F<out _>>(fa: F<5>) -> F<number> { return fa }
`
	tests := []struct {
		name string
		src  string
		// want holds the rendered type of each listed value binding.
		want map[string]string
		// wantErrs is every diagnostic in order. nil expects none.
		wantErrs []string
	}{
		{
			name: "CovariantWidens",
			src:  ``,
			want: map[string]string{"widen": "fn <F<out _>>(fa: F<5>) -> F<number>"},
		},
		{
			name:     "CovariantRejectsNarrowing",
			src:      `fn narrow<F<out _>>(fa: F<number>) -> F<5> { return fa }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
		{
			name: "ContravariantNarrows",
			src:  `fn narrow<F<in _>>(fa: F<number>) -> F<5> { return fa }`,
		},
		{
			name:     "ContravariantRejectsWidening",
			src:      `fn widen2<F<in _>>(fa: F<5>) -> F<number> { return fa }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
		{
			name: "CovariantConstructorAccepted",
			src:  `type X = CoFunctor<Box>`,
		},
		{
			name: "CovariantConstructorRejected",
			src:  `type X = CoFunctor<Sink>`,
			wantErrs: []string{
				"type constructor `Sink` is not covariant in argument 1, which its parameter declares",
			},
		},
		{
			name: "ContravariantConstructorAccepted",
			src:  `type X = ContraBox<Sink>`,
		},
		{
			name: "ContravariantConstructorRejected",
			src:  `type X = ContraBox<Box>`,
			wantErrs: []string{
				"type constructor `Box` is not contravariant in argument 1, which its parameter declares",
			},
		},
		{
			name: "InferredHeadChecked",
			src: `val b: Box<5> = Box(5)
			val r = widen(b)`,
			want: map[string]string{"r": "Box<number>"},
		},
		{
			name: "InferredHeadRejected",
			src: `val s: Sink<5> = Sink(fn (x) { return undefined })
			val r = widen(s)`,
			want: map[string]string{"r": "never"},
			wantErrs: []string{
				"type constructor `Sink` is not covariant in argument 1, which its parameter declares",
			},
		},
		{
			// An inference head bound to another head is checked against that head's declared
			// slots, so an invariant G cannot stand for widen's covariant F. The argument and the
			// return each bind F, so each reports.
			name: "InvariantHeadPassedToCovariantParam",
			src:  `fn outer<G<_>>(x: G<5>) -> G<number> { return widen(x) }`,
			wantErrs: []string{
				"type constructor `G` is not covariant in argument 1, which its parameter declares",
				"type constructor `G` is not covariant in argument 1, which its parameter declares",
			},
		},
		{
			name: "CovariantHeadPassedToCovariantParam",
			src:  `fn outer<G<out _>>(x: G<5>) -> G<number> { return widen(x) }`,
		},
		{
			name: "InOutSlotRenders",
			src: `fn keep<F<in out _>>(x: F<5>) -> F<5> { return x }
			val w = keep`,
			want: map[string]string{"w": "fn <F<in out _>>(x: F<5>) -> F<5>"},
		},
		{
			name: "ParamWithVarianceAccepted",
			src:  `fn outer<G<out _>>(x: CoFunctor<G>) -> number { return 1 }`,
		},
		{
			name: "InvariantParamRejected",
			src:  `fn outer<G<_>>(x: CoFunctor<G>) -> number { return 1 }`,
			wantErrs: []string{
				"type constructor `G` is not covariant in argument 1, which its parameter declares",
			},
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
				require.Contains(t, values, name)
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestInferBorrowedTypeApps covers a borrow of an application, as `mut F<A>` and `&F<A>`. A
// borrow's head matches under a borrow of the same kind, so `mut F<A>` against
// `mut Array<number>` binds F to Array. The borrow rules decide a borrow against an
// application first, so an owned `mut` value peels into an owned `F<A>`. Under a mutable
// borrow every argument is invariant, whatever its slot declares.
func TestInferBorrowedTypeApps(t *testing.T) {
	const prelude = `
class Box<T> { value: T }
fn idMut<F<_>, A>(fa: mut F<A>) -> mut F<A> { return fa }
fn idOwned<F<_>, A>(fa: F<A>) -> F<A> { return fa }
fn idRef<F<_>>(fa: &F<number>) -> &F<number> { return fa }
`
	tests := []struct {
		name string
		src  string
		// want holds the rendered type of each listed value binding.
		want map[string]string
		// wantErrs is every diagnostic in order. nil expects none.
		wantErrs []string
	}{
		{
			name: "MutBorrowRenders",
			src:  ``,
			want: map[string]string{"idMut": "fn <F<_>, A>(fa: mut F<A>) -> mut F<A>"},
		},
		{
			name: "SharedBorrowRenders",
			src:  ``,
			want: map[string]string{"idRef": "fn <F<_>, 'a>(fa: &'a F<number>) -> &'a F<number>"},
		},
		{
			name: "SharedBorrowMatched",
			src: `val a: Array<number> = [1]
			val r = idRef(&a)`,
			want: map[string]string{"r": "&Array<number>"},
		},
		{
			name: "MutValueDecaysToSharedBorrow",
			src: `val m: mut Array<number> = [1]
			val r = idRef(&m)`,
			want: map[string]string{"r": "&Array<number>"},
		},
		{
			name:     "MutBorrowMakesArgumentsInvariant",
			src:      `fn w<F<out _>>(fa: mut F<5>) -> mut F<number> { return fa }`,
			wantErrs: []string{"cannot constrain number <: 5"},
		},
		{
			name: "FieldReadThroughMutReceiverLendsBorrow",
			src: `type H<F<_>> = {items: F<number>}
			fn get<F<_>>(h: mut H<F>) -> mut F<number> { return h.items }`,
			want: map[string]string{"get": "fn <F<_>>(h: mut H<F>) -> mut F<number>"},
		},
		{
			name: "MutArrayMatched",
			src: `val m: mut Array<number> = [1]
			val r = idMut(m)`,
			want: map[string]string{"r": "mut Array<number>"},
		},
		{
			name: "MutClassMatched",
			src: `val m: mut Box<number> = Box(1)
			val r = idMut(m)`,
			want: map[string]string{"r": "mut Box<number>"},
		},
		{
			name: "OwnedMutValuePeelsIntoOwnedApp",
			src: `val m: mut Array<number> = [1]
			val r = idOwned(m)`,
			want: map[string]string{"r": "Array<number>"},
		},
		{
			name: "MutAppRejectsImmutable",
			src: `val a: Array<number> = [1]
			val r = idMut(a)`,
			wantErrs: []string{"cannot constrain immutable Array<number> <: mutable t20<t21>"},
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
				require.Contains(t, values, name)
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

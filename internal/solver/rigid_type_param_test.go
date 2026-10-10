package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestABodyIsCheckedForEveryInstantiation covers a generic declaration's body using one of
// its own type parameters in a way only some instantiations allow. The body reads the
// parameter as rigid, so such a use is reported where it is written, and the signature
// keeps only the bound the declaration states.
func TestABodyIsCheckedForEveryInstantiation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// binding names a value whose rendered type the case asserts. An empty binding
		// asserts no type.
		binding string
		want    string
		errs    []string
	}{
		{
			// f accepts only strings, so passing it a `U` that may be a number is the use
			// under test.
			name: "ACallNarrowingTheBound",
			src: `fn f<A: string>(a: A) -> A { return a }
fn g<U: number | string>(u: U) { return f(u) }`,
			binding: "g",
			want:    "fn <U: number | string>(u: U) -> U",
			errs:    []string{"2:41-2:45: cannot constrain number <: string"},
		},
		{
			// The body's use is reported once, at the use. A caller is checked against the
			// declared bound alone.
			name: "ACallerReadsOnlyTheDeclaredBound",
			src: `fn f<A: string>(a: A) -> A { return a }
fn g<U: number | string>(u: U) { return f(u) }
val r = g(5)`,
			errs: []string{"2:41-2:45: cannot constrain number <: string"},
		},
		{
			name: "AnUnboundedParameterPassedOn",
			src: `fn f<A: string>(a: A) -> A { return a }
fn g<U>(u: U) -> U { return f(u) }`,
			errs: []string{"2:29-2:33: cannot constrain U <: string"},
		},
		{
			name:    "AnOperatorNarrowingTheBound",
			src:     `fn g<U: number | string>(u: U) -> boolean { return u > 1 }`,
			binding: "g",
			want:    "fn <U: number | string>(u: U) -> boolean",
			errs:    []string{"1:52-1:53: cannot constrain string <: number"},
		},
		{
			name: "AMemberReadOnAnUnboundedParameter",
			src:  `fn g<U>(u: U) -> number { return u.a }`,
			errs: []string{"1:34-1:37: cannot constrain U <: object"},
		},
		{
			name: "AMemberReadTheBoundLacks",
			src:  `fn g<U: {b: string}>(u: U) -> number { return u.a }`,
			errs: []string{"1:49-1:50: object is missing property: a"},
		},
		{
			name:    "AMemberReadTheBoundHas",
			src:     `fn g<U: {a: number}>(u: U) -> number { return u.a }`,
			binding: "g",
			want:    "fn <U: {a: number}>(u: U) -> number",
		},
		{
			name: "AMethodBody",
			src: `fn f<A: string>(a: A) -> A { return a }
class C {
	g<U: number | string>(&self, u: U) -> U { return f(u) },
}`,
			errs: []string{"3:51-3:55: cannot constrain number <: string"},
		},
		{
			// The inner function holds its own parameter rigid while its body is inferred,
			// and the outer one's stays rigid around it.
			name: "ANestedGenericFunction",
			src: `fn g<U: number>(u: U) {
	val h = fn <V: string>(v: V) -> V { return v }
	return h(u)
}`,
			errs: []string{"3:9-3:13: cannot constrain number <: string"},
		},
		{
			name: "AGeneratorBody",
			src: `gen fn g<U: number>(u: U) -> Generator<number, string, undefined> {
	yield 1
	return u
}`,
			errs: []string{"3:9-3:10: cannot constrain number <: string"},
		},
		{
			// A union written through an alias still names U, so returning a U fits it.
			name: "AnAliasOfAUnionNamingTheParameter",
			src: `type Maybe<T> = T | null
fn g<U>(u: U) -> Maybe<U> { return u }`,
			binding: "g",
			want:    "fn <U>(u: U) -> Maybe<U>",
		},
		{
			name:    "AUnionNamingABoundingSibling",
			src:     `fn g<U, T: U>(t: T) -> U | null { return t }`,
			binding: "g",
			want:    "fn <U, T: U>(t: T) -> U | null",
		},
		{
			// A recursive call instantiates the function afresh, so it is checked like any
			// other call.
			name:    "ARecursiveCall",
			src:     `fn g<U: number>(u: U) -> U { return g(u) }`,
			binding: "g",
			want:    "fn <U: number>(u: U) -> U",
		},
		{
			// A relation between two of the declaration's own parameters has to follow from
			// the bounds the signature states, as `fn g<U, T: U>` does and this one does not.
			// The return operand is what carries the unrelated parameter, so it is blamed.
			name:    "TwoOwnParametersRelated",
			src:     `fn h<U, T>(t: T) -> U { return t }`,
			binding: "h",
			want:    "fn <U, T>(t: T) -> U",
			errs:    []string{"1:32-1:33: cannot constrain T <: U"},
		},
		{
			// The same relation forced through a local's annotation is blamed at the
			// initializer.
			name: "TwoOwnParametersRelatedThroughALocal",
			src: `fn h<U, T>(t: T) -> U {
	val u: U = t
	return u
}`,
			errs: []string{"2:13-2:14: cannot constrain T <: U"},
		},
		{
			// A declared bound between the two is what the relation has to follow from.
			name:    "TwoOwnParametersRelatedByADeclaredBound",
			src:     `fn h<U, T: U>(t: T) -> U { return t }`,
			binding: "h",
			want:    "fn <U, T: U>(t: T) -> U",
		},
		{
			// Each return point is blamed on its own.
			name: "TwoOwnParametersRelatedAtTwoReturns",
			src: `fn h<U, T>(t: T, c: boolean) -> U {
	if c {
		return t
	} else {
		return t
	}
}`,
			errs: []string{
				"3:10-3:11: cannot constrain T <: U",
				"5:10-5:11: cannot constrain T <: U",
			},
		},
		{
			name: "TwoOwnParametersRelatedInAGenerator",
			src: `gen fn g<U, T>(t: T) -> Generator<number, U, undefined> {
	yield 1
	return t
}`,
			errs: []string{"3:9-3:10: cannot constrain T <: U"},
		},
		{
			name: "TwoOwnParametersRelatedInAnAsyncFunction",
			src: `async fn h<U, T>(t: T) -> Promise<U> {
	return t
}`,
			errs: []string{"2:9-2:10: cannot constrain T <: U"},
		},
		{
			// A class's parameters are held rigid across its member bodies, so two of them
			// meet the same way a function's do.
			name: "TwoClassParametersRelated",
			src: `class C<T, U> {
	t: T,
	f(&self, t: T) -> U { return t },
}`,
			errs: []string{"3:31-3:32: cannot constrain T <: U"},
		},
		{
			// A declared lower bound never implies its converse, so `B >: T` does not let a
			// `B` stand where a `T` is expected. #1887 keeps this rejection once the solver
			// reads the bound.
			name: "ALowerBoundedParameterIsNotItsBound",
			src:  `fn f<B >: T, T>(x: B, y: T) -> T { return x }`,
			errs: []string{"1:43-1:44: cannot constrain B <: T"},
		},
		{
			// The declared lower bound is what `T <: B` follows from.
			name:    "ALowerBoundRelatesSiblings",
			src:     `fn f<B >: T, T>(x: B, y: T) -> B { return y }`,
			binding: "f",
			want:    "fn <B >: T, T>(x: B, y: T) -> B",
		},
		{
			name: "TwoMethodParametersRelated",
			src: `class C<T> {
	t: T,
	m<A, B>(&self, a: A) -> B { return a },
}`,
			errs: []string{"3:37-3:38: cannot constrain A <: B"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.errs, messagesWithSpan(t, errs))
			if tt.binding != "" {
				require.Equal(t, tt.want, values[tt.binding])
			}
		})
	}
}

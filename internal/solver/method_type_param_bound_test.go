package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMethodTypeParamBoundIsCheckedInItsBody covers a generic method whose body passes its
// own bounded type parameter to a sibling static of its class. The body reads the class's
// name before the class has a value, so the call is checked against the static's signature
// and an argument whose bound does not fit is reported at the call.
//
// A constructor call and a call to a free function in the same component read a binding var
// that is solved after the body. The parameter stays rigid, so the call is still rejected
// once the var is solved. The error is blamed inside the declaration whose signature solves
// the var, which is the parameter's bound for `Foo(a)` and the `add` declaration for
// `add(a, a)`.
func TestAMethodTypeParamBoundIsCheckedInItsBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "InstanceMethodBoundDoesNotFit",
			src: `class Foo {
				v: number,
				static add(a: number, b: number) -> number { return a + b },
				strs<A: string>(&self, a: A) { return Foo.add(a, a) },
			}`,
			want: []string{"4:43-4:56: cannot constrain string <: number"},
		},
		{
			name: "StaticMethodBoundDoesNotFit",
			src: `class Foo {
				v: number,
				static add(a: number, b: number) -> number { return a + b },
				static strs<A: string>(a: A) { return Foo.add(a, a) },
			}`,
			want: []string{"4:43-4:56: cannot constrain string <: number"},
		},
		{
			name: "InstanceMethodBoundFits",
			src: `class Foo {
				v: number,
				static add(a: number, b: number) -> number { return a + b },
				nums<A: number>(&self, a: A) -> number { return Foo.add(a, a) },
			}`,
		},
		{
			name: "StaticMethodBoundFits",
			src: `class Foo {
				v: number,
				static add(a: number, b: number) -> number { return a + b },
				static nums<A: number>(a: A) -> number { return Foo.add(a, a) },
			}`,
		},
		// The static is declared after the method that calls it, so the call reads its
		// signature before the static's own body is inferred.
		{
			name: "StaticDeclaredAfterTheCall",
			src: `class Foo {
				v: number,
				strs<A: string>(&self, a: A) { return Foo.add(a, a) },
				static add(a: number, b: number) -> number { return a + b },
			}`,
			want: []string{"3:43-3:56: cannot constrain string <: number"},
		},
		{
			name: "ConstructorCall",
			src: `class Foo {
				v: number,
				strs<A: string>(&self, a: A) { return Foo(a) },
			}`,
			want: []string{"3:13-3:19: cannot constrain string <: number"},
		},
		{
			name: "FreeFunctionInTheSameComponent",
			src: `fn add(a: number, b: number) -> number { if a > 0 { strs("x") } return a + b }
			fn strs<A: string>(a: A) { return add(a, a) }`,
			want: []string{"1:1-1:79: cannot constrain string <: number"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(t, errs))
		})
	}
}

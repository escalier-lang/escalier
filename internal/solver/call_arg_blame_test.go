package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestACallBlamesTheArgumentThatDoesNotFit covers where a call to a function with one
// signature reports an argument that does not fit its parameter. Each such argument is
// reported at its own span, whether its type comes from a literal, a parameter, a local's
// annotation, or a bounded type parameter, and whether or not the callee is generic. A
// callee with more than one signature reports at the call.
func TestACallBlamesTheArgumentThatDoesNotFit(t *testing.T) {
	const add = "fn add(a: number, b: number) -> number { return a + b }\n"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ALiteral",
			src:  add + `val x = add("s", 1)`,
			want: []string{`2:13-2:16: cannot constrain "s" <: number`},
		},
		{
			name: "AParameter",
			src:  add + `fn strs(a: string) { return add(1, a) }`,
			want: []string{"2:36-2:37: cannot constrain string <: number"},
		},
		{
			name: "AnAnnotatedLocal",
			src:  add + "fn g() {\n\tval s: string = \"x\"\n\treturn add(1, s)\n}",
			want: []string{"4:16-4:17: cannot constrain string <: number"},
		},
		{
			name: "ABoundedTypeParameter",
			src:  add + `fn strs<A: string>(a: A) { return add(1, a) }`,
			want: []string{"2:42-2:43: cannot constrain string <: number"},
		},
		{
			name: "EachArgumentThatDoesNotFit",
			src:  add + `fn strs(a: string) { return add(a, a) }`,
			want: []string{
				"2:33-2:34: cannot constrain string <: number",
				"2:36-2:37: cannot constrain string <: number",
			},
		},
		{
			name: "AConstructorCall",
			src:  "class Foo {\n\tv: number,\n}\nfn g(a: string) { return Foo(a) }",
			want: []string{"4:30-4:31: cannot constrain string <: number"},
		},
		{
			name: "AMethodCall",
			src: "class Foo {\n\tv: number,\n\tm(&self, x: number) -> number { return x },\n}\n" +
				"fn g(f: Foo, a: string) { return f.m(a) }",
			want: []string{"5:38-5:39: cannot constrain string <: number"},
		},
		{
			// Each lower bound of `f` is checked against the argument itself, so `1` fills
			// `x: 1` as well as `x: number`.
			name: "ACalleeWithTwoLowerBounds",
			src: "fn test(c: boolean) {\n\tval f1 = fn (x: number) { return 1 }\n" +
				"\tval f2 = fn (x: 1) { return 2 }\n\tval f = if c { f1 } else { f2 }\n\treturn f(1)\n}",
		},
		{
			// The argument's type is the parameter's own annotation, and `Exact<Point>` is still
			// reported, once. `f` has no value yet when its own body calls it, so the call is
			// checked after the body and blamed on the declaration.
			name: "AnArgumentTypedByTheParameterItFills",
			src: "class Point {\n\tx: number,\n}\n" +
				"fn f(p: Exact<Point>) -> number { return f(p) }",
			want: []string{"4:1-4:48: class Point is not final, so a subclass instance may carry " +
				"members Point does not declare and Exact<Point> cannot close it; declare Point as " +
				"`final` instead"},
		},
		{
			name: "AGenericCallee",
			src:  "fn first<T: number>(a: T, b: T) -> T { return a }\n" + `fn strs(a: string) { return first(1, a) }`,
			want: []string{"2:38-2:39: cannot constrain string <: number"},
		},
		{
			// The parameter's type reaches `T` only through the body, so the argument has to
			// reach the same copy of `T` that the return reads.
			name: "AGenericCalleeLinkingAParameterToItsTypeParameter",
			src:  "fn g<T>(p) -> T {\n\tval z: T = p.x\n\treturn z\n}\n" + `val n: number = g({x: "s"})`,
			want: []string{`5:23-5:26: cannot constrain "s" <: number`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(t, errs))
		})
	}
}

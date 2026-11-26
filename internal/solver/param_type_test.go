package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMissingParamType covers which closure parameters W3 asks a type for. A parameter
// takes its type from its annotation or from the function type the closure flows into, and
// one with neither reports MissingParamTypeError. expected lists every diagnostic the source
// reports.
func TestMissingParamType(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			name: "NoContext",
			src:  "val f = fn (p) { return p.x }",
			expected: []string{
				"1:13-1:14: parameter 'p' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
		{
			name: "NoContextWithAConcreteUse",
			src:  "fn g() -> undefined {\n\tval double = fn (x) { return x * 2 }\n}",
			expected: []string{
				"2:19-2:20: parameter 'x' has no type, and nothing the closure flows into supplies one; write `x: number`",
			},
		},
		{
			name: "CallArgument",
			src: "type Flat = {x: number}\n" +
				"declare val arr: Array<Flat>\n" +
				"declare fn each(xs: &Array<Flat>, f: fn (x: &Flat) -> undefined) -> undefined\n" +
				"fn g() -> undefined { each(&arr, fn (x) { val n = x.x }) }",
		},
		{
			name: "AnnotatedBinding",
			src:  "val h: fn (x: number) -> number = fn (x) { return x + 1 }",
		},
		{
			name: "GenericAnnotatedBinding",
			src:  "val id: fn <T>(x: T) -> T = fn (x) { return x }",
		},
		{
			name: "StoreIntoTypedField",
			src:  "fn st(o: &mut {f: fn (x: number) -> number}) -> undefined { o.f = fn (x) { return x + 1 } }",
		},
		{
			name: "ReturnOfWrittenReturnType",
			src:  "fn mk() -> fn (x: number) -> number { return fn (x) { return x + 1 } }",
		},
		{
			name: "AnnotatedParam",
			src:  "val f = fn (x: number) { return x + 1 }",
		},
		{
			// A type parameter types nothing about the closure's parameters.
			name: "GenericCallee",
			src:  "declare fn apply<F>(f: F) -> F\nfn g() -> undefined { apply(fn (x) { return x }) }",
			expected: []string{
				"2:33-2:34: parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
		{
			// A closure bound to a name is not the argument of the call that later uses it.
			name: "BoundBeforeItFlows",
			src: "declare fn measure(f: fn (s: {n: number}) -> number) -> number\n" +
				"fn g() -> number {\n\tval h = fn (s) { return s.n }\n\treturn measure(h)\n}",
			expected: []string{
				"3:14-3:15: parameter 's' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.expected, messagesWithSpan(t, errs))
		})
	}
}

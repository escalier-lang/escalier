package tests

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A `callable` call signature makes the class value itself callable, which is
// what `Symbol("x")` and `Number("1")` do in JavaScript. The signature joins the
// class's static object type, so a call against the class name resolves through
// it.
//
// A class with no `constructor` member also has to reach the call signature. The
// checker gives an emitted class an implicit zero-argument constructor, and
// Escalier spells construction as a call, so an implicit one on a `declare class`
// would answer `Tag("hi")` before the signature ever saw it.
//
// The two cases cover the two class paths: InferComponent walks a top-level
// class, inferClassDecl walks one declared inside a function body.
func TestCallableClassIsCalledThroughItsCallSignature(t *testing.T) {
	tests := map[string]struct {
		input       string
		bindingName string
	}{
		"TopLevel": {
			input: `
				declare class Tag {
					callable(label: string) -> number,
					readonly label: string,
				}
				val n = Tag("hi")
			`,
			bindingName: "n",
		},
		"InsideAFunctionBody": {
			input: `
				fn tag() -> number {
					declare class Tag {
						callable(label: string) -> number,
					}
					return Tag("hi")
				}
				val n = tag()
			`,
			bindingName: "n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			require.Equal(t, "number", got)
		})
	}
}

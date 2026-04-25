package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultMutabilityFromClass instantiates each class and asserts the
// printed type of the resulting binding for the three branches of the
// algorithm: no mut self methods → immutable instance; at least one mut
// self method → mutable instance; `data class` modifier → immutable
// instance regardless of methods.
func TestDefaultMutabilityFromClass(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"NoMutSelf_DefaultsImmutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val p = Point(5, 10)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"HasMutSelf_DefaultsMutable": {
			input: `
				class Counter(count: number) {
					count,
					increment(mut self) -> number { return self.count }
				}
				val c = Counter(0)
			`,
			bindingName:  "c",
			expectedType: "mut Counter",
		},
		"DataModifier_OverridesMutSelf": {
			input: `
				data class Config(host: string) {
					host,
					setHost(mut self, h: string) -> void {}
				}
				val cfg = Config("localhost")
			`,
			bindingName:  "cfg",
			expectedType: "Config",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedType, got,
				"unexpected type for %q", test.bindingName)
		})
	}
}

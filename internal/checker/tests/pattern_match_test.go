package tests

import (
	"strings"
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
)

func TestPatternMatchStructuralVsNominal(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string
	}{
		"Case6_PartialMatchSubsetOfFields": {
			input: `
				class User(name: string, age: number, email: string) { name, age, email }

				declare val user: User
				val result = match user {
					{name} => name,
				}
			`,
			expectedValues: map[string]string{
				"result": "string",
			},
		},
		"Case12_StructuralPatternMatchesGetter": {
			input: `
				class Circle(radius: number) {
					get area(self) -> number { return 3.14159 * radius * radius },
				}

				declare val circle: Circle
				val result = match circle {
					{area} => area,
				}
			`,
			expectedValues: map[string]string{
				"result": "number",
			},
		},
		"PartialMatchMultipleFields": {
			input: `
				class Point(x: number, y: number) { x, y }

				declare val p: Point
				val result = match p {
					{x, y} => x + y,
				}
			`,
			expectedValues: map[string]string{
				"result": "number",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key])
			}
		})
	}
}

func TestPatternMatchErrors(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrs   []string
		expectedValues map[string]string
	}{
		"PatternFieldNotFoundOnNominalType": {
			input: `
				class Point(x: number, y: number) { x, y }

				declare val p: Point
				val result = match p {
					{foo} => foo,
				}
			`,
			expectedErrs: []string{
				"Property foo does not exist on type {x: number, y: number}",
			},
			// Verify the unresolved pattern field resolves to undefined (not a leaked type var)
			expectedValues: map[string]string{
				"result": "undefined",
			},
		},
		"PatternFieldMatchesSetterOnly": {
			input: `
				declare val obj: {
					get readable() -> string,
					set writable(value: number) -> undefined
				}
				val result = match obj {
					{writable} => writable,
				}
			`,
			expectedErrs: []string{
				"writable",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes, inferErrors := inferModuleTypesAndErrors(t, test.input)
			for _, expected := range test.expectedErrs {
				found := false
				for _, err := range inferErrors {
					if strings.Contains(err.Message(), expected) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected error containing %q, got %v", expected, errMessages(inferErrors))
			}
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key])
			}
		})
	}
}

func errMessages(errors []Error) []string {
	msgs := make([]string, len(errors))
	for i, err := range errors {
		msgs[i] = err.Message()
	}
	return msgs
}

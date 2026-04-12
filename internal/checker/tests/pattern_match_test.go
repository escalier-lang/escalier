package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Run("PatternFieldNotFoundOnNominalType", func(t *testing.T) {
		t.Parallel()
		input := `
			class Point(x: number, y: number) { x, y }

			declare val p: Point
			val result = match p {
				{foo} => foo,
			}
		`
		_, inferErrors := inferModuleTypesAndErrors(t, input)
		require.GreaterOrEqual(t, len(inferErrors), 1)
		assert.Contains(
			t,
			inferErrors[0].Message(),
			"Property foo does not exist on type {x: number, y: number}",
		)
	})
}

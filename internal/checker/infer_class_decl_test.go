package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestCheckClassDeclNoErrors(t *testing.T) {
	tests := map[string]struct {
		input               string
		expectedTypes       map[string]string
		expectedTypeAliases map[string]string
	}{
		"SimpleDecl": {
			input: `
				class Point(x: number, y: number) {
					x,
					y: y,
					z: 0:number,
				}

				val p = Point(5, 10)
				val {x, y, z} = p
			`,
			expectedTypes: map[string]string{
				"p": "Point",
				"x": "number",
				"y": "number",
				"z": "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, z: number}",
			},
		},
		"SimpleDeclWithMethods": {
			input: `
				declare fn sqrt(x: number) -> number
				class Point(x: number, y: number) {
					x,
					y,
					length(self) {
						return sqrt(self.x * self.x + self.y * self.y)
					}
					add(self, other: Point) {
						return Point(self.x + other.x, self.y + other.y)
					}
				}

				val p = Point(5, 10)
				val len = p.length()
				val q = p.add(Point(1, 2))
			`,
			expectedTypes: map[string]string{
				"p":   "Point",
				"q":   "Point",
				"len": "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, length() -> number throws never, add(other: Point) -> Point throws never}",
			},
		},
	}

	schema := loadSchema(t)

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %s\n", i, err.String())
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			c.Schema = schema
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Collect actual types for verification
			actualTypes := make(map[string]string)
			for name, binding := range scope.Values {
				assert.NotNil(t, binding)
				actualTypes[name] = binding.Type.String()
			}

			// Verify that all expected types match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				assert.True(t, exists, "Expected variable %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
				}
			}

			for expectedName, expectedType := range test.expectedTypeAliases {
				actualTypeAlias, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualTypeAlias.Type.String(), "Type mismatch for type alias %s", expectedName)
				}
			}

			// Note: We don't check for unexpected variables since the scope includes
			// prelude functions and operators that are implementation details
		})
	}
}

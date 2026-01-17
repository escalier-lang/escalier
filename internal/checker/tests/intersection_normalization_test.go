package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

// TestNormalizeIntersectionType tests the post-inference normalization logic
func TestNormalizeIntersectionType(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedType string
	}{
		"normalizes duplicates": {
			input: `
				type Result = string & string
			`,
			expectedType: "string",
		},
		"normalizes to never when primitives conflict": {
			input: `
				type Result = string & number
			`,
			expectedType: "never",
		},
		"flattens nested intersections": {
			input: `
				type A = string & number
				type Result = boolean & A
			`,
			expectedType: "never",
		},
		"preserves object intersections": {
			input: `
				type Result = {a: string} & {b: number}
			`,
			expectedType: "{a: string} & {b: number}",
		},
		"handles any in intersection": {
			input: `
				type Result = any & string
			`,
			expectedType: "any",
		},
		"handles never in intersection": {
			input: `
				type Result = never & string
			`,
			expectedType: "never",
		},
		"handles readonly properties": {
			input: `
				type Result = {readonly a: string} & {b: number}
			`,
			expectedType: "{readonly a: string} & {b: number}",
		},
		"expands type aliases within intersection": {
			input: `
				type MyString = string
				type Result = MyString & string
			`,
			expectedType: "string",
		},
		"expands multiple type aliases to same underlying type": {
			input: `
				type MyString = string
				type YourString = string
				type Result = MyString & YourString
			`,
			expectedType: "string",
		},
		"not modify intersection of primitive and object": {
			input: `
				type Result = string & {__brand: "email"}
			`,
			expectedType: "string & {__brand: \"email\"}",
		},
		// NOTE: Phase 3 should fix this test case
		// "intersects properties with the same name": {
		// 	input: `
		// 		type Result = {a: string} & {a: number}
		// 	`,
		// 	expectedType: "{a: never}",
		// },
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: tc.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			binding, ok := scope.Types["Result"]
			assert.True(t, ok, "Expected Result type alias to be defined")

			// Prune to resolve type variables, then repeatedly call NormalizeIntersectionType
			// until the result stops changing
			resultType := type_system.Prune(binding.Type)

			// Keep normalizing until the result stabilizes
			maxIterations := 10
			for i := 0; i < maxIterations; i++ {
				if intersectionType, ok := resultType.(*type_system.IntersectionType); ok {
					previousStr := resultType.String()
					resultType = c.NormalizeIntersectionType(inferCtx, intersectionType)

					// Stop if no change
					if previousStr == resultType.String() {
						break
					}

					// Prune again in case we got a type variable
					resultType = type_system.Prune(resultType)
				} else {
					// Not an intersection type anymore, we're done
					break
				}
			}

			assert.Equal(t, tc.expectedType, resultType.String())
		})
	}
}

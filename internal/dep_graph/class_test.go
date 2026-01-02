package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestClassDeclDependencies(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected []string // expected dependency names
	}{
		"SimpleClass_NoDependencies": {
			input: `
				class Point(x: number, y: number) {
					x,
					y,
				}
			`,
			expected: []string{},
		},
		"ClassWithExtends_SimpleDependency": {
			input: `
				class Base {}
				class Child extends Base {}
			`,
			expected: []string{"Base"},
		},
		"ClassWithGenericExtends": {
			input: `
				class Container<T> {}
				type Item = string
				class SpecialContainer<T> extends Container<Item> {}
			`,
			expected: []string{"Container", "Item"},
		},
		"ClassWithTypeParamConstraints": {
			input: `
				type Serializable = {serialize: fn() -> string}
				class DataClass<T: Serializable>(value: T) {}
			`,
			expected: []string{"Serializable"},
		},
		"ClassWithFieldTypes": {
			input: `
				type Point = {x: number, y: number}
				class Entity {
					position :: Point,
				}
			`,
			expected: []string{"Point"},
		},
		"ClassWithMethodReturnTypes": {
			input: `
				type Result = {success: boolean}
				class Processor {
					process(self) -> Result {
						return {success: true}
					}
				}
			`,
			expected: []string{"Result"},
		},
		"ClassWithConstructorParamTypes": {
			input: `
				type Config = {debug: boolean}
				class App(config: Config) {
					config,
				}
			`,
			expected: []string{"Config"},
		},
		"ClassWithMultipleDependencies": {
			input: `
				class Base {}
				type Data = {value: number}
				type Error = {message: string}
				class Child extends Base(data: Data) {
					process(self) -> Error {
						return {message: "error"}
					}
				}
			`,
			expected: []string{"Base", "Data", "Error"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Empty(t, errors, "Expected no parsing errors")

			// Build the dependency graph
			depGraph := BuildDepGraph(module)

			// Find the last declaration (which should be our class under test)
			if len(depGraph.Decls) == 0 {
				t.Fatal("No declarations found")
			}
			lastDeclID := DeclID(len(depGraph.Decls) - 1)
			lastDecl := depGraph.Decls[lastDeclID]

			// Verify it's a class declaration
			_, ok := lastDecl.(*ast.ClassDecl)
			assert.True(t, ok, "Last declaration should be a ClassDecl")

			// Find dependencies
			deps := FindDeclDependencies(lastDeclID, depGraph)

			// Convert dependency DeclIDs to names
			var depNames []string
			for iter := deps.Iter(); iter.Next(); {
				depID := iter.Key()
				depDecl := depGraph.Decls[depID]
				switch d := depDecl.(type) {
				case *ast.VarDecl:
					if identPat, ok := d.Pattern.(*ast.IdentPat); ok {
						depNames = append(depNames, identPat.Name)
					}
				case *ast.FuncDecl:
					depNames = append(depNames, d.Name.Name)
				case *ast.TypeDecl:
					depNames = append(depNames, d.Name.Name)
				case *ast.InterfaceDecl:
					depNames = append(depNames, d.Name.Name)
				case *ast.EnumDecl:
					depNames = append(depNames, d.Name.Name)
				case *ast.ClassDecl:
					depNames = append(depNames, d.Name.Name)
				}
			}

			// Sort for consistent comparison
			assert.ElementsMatch(t, test.expected, depNames,
				"Dependencies should match expected")
		})
	}
}

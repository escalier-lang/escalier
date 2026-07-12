package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassDeclDependencies(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected []string // expected dependency names
	}{
		"SimpleClass_NoDependencies": {
			input: `
				class Point {
					x: number,
					y: number,
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
				class DataClass<T: Serializable> {
					value: T,
				}
			`,
			expected: []string{"Serializable"},
		},
		"ClassWithFieldTypes": {
			input: `
				type Point = {x: number, y: number}
				class Entity {
					position: Point,
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
				class App {
					config: Config,
				}
			`,
			expected: []string{"Config"},
		},
		"ClassWithMultipleDependencies": {
			input: `
				class Base {}
				type Data = {value: number}
				type Error = {message: string}
				class Child extends Base {
					data: Data,
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

			// Find the last binding key (which should be our class under test)
			// We need to iterate through all components to find the last one
			if len(depGraph.Components) == 0 {
				t.Fatal("No components found")
			}

			// Get the last binding key from the last component
			lastComponent := depGraph.Components[len(depGraph.Components)-1]
			if len(lastComponent) == 0 {
				t.Fatal("Last component is empty")
			}
			lastKey := lastComponent[len(lastComponent)-1]

			// Get the declaration for this key
			lastDecls := depGraph.GetDecls(lastKey)
			if len(lastDecls) == 0 {
				t.Fatal("No declarations found for last key")
			}
			lastDecl := lastDecls[0]

			// Verify it's a class declaration
			_, ok := lastDecl.(*ast.ClassDecl)
			assert.True(t, ok, "Last declaration should be a ClassDecl")

			// Find dependencies
			deps := FindDeclDependencies(lastKey, depGraph)

			// Convert dependency BindingKeys to names
			var depNames []string
			for iter := deps.Iter(); iter.Next(); {
				depKey := iter.Key()
				depDecls := depGraph.GetDecls(depKey)
				if len(depDecls) == 0 {
					continue
				}
				depDecl := depDecls[0]
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

// TestClassMemberNameNoSpuriousDependency checks that a class member whose name
// matches a top-level value binding does not create a dependency edge on that
// binding. A field, method, getter, or setter name is a label, not a reference,
// so it must not contribute to the dependency graph. A computed key such as
// `[bar]` is a real expression, so a reference inside it still counts. The cases
// cover each EnterClassElem branch. Regression test for issue #855, where the
// spurious edge scrambled SCC membership and inference order.
func TestClassMemberNameNoSpuriousDependency(t *testing.T) {
	tests := map[string]struct {
		input        string
		className    string
		collidingVal string
		// wantDep is true when the class should depend on collidingVal, as with
		// a computed key that references it. Otherwise the collision must not
		// produce an edge.
		wantDep bool
	}{
		"FieldNameMatchesVal": {
			input: `
				class A {
					x: number,
				}
				val a = A(1)
				val x = a.x
			`,
			className:    "A",
			collidingVal: "x",
		},
		"MethodNameMatchesVal": {
			input: `
				val process = 5
				class Worker {
					process(self) -> number {
						return 1
					}
				}
			`,
			className:    "Worker",
			collidingVal: "process",
		},
		"GetterNameMatchesVal": {
			input: `
				val fullName = "n"
				class Person {
					firstName: string,
					get fullName(self) -> string { return self.firstName },
				}
			`,
			className:    "Person",
			collidingVal: "fullName",
		},
		"SetterNameMatchesVal": {
			input: `
				val fullName = "n"
				class Person {
					firstName: string,
					set fullName(mut self, value: string) { self.firstName = value },
				}
			`,
			className:    "Person",
			collidingVal: "fullName",
		},
		"ComputedKeyRetainsDependency": {
			input: `
				val bar = "bar"
				class Foo {
					[bar]: number,
				}
			`,
			className:    "Foo",
			collidingVal: "bar",
			wantDep:      true,
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
			require.Empty(t, errors, "Expected no parsing errors")

			depGraph := BuildDepGraph(module)

			// The class must be present as both a type and a value binding, or the
			// dependency assertions below would pass vacuously against an empty set.
			typeKey := TypeBindingKey(test.className)
			valueKey := ValueBindingKey(test.className)
			require.True(t, depGraph.HasBinding(typeKey),
				"type:%s should exist in the graph", test.className)
			require.True(t, depGraph.HasBinding(valueKey),
				"value:%s should exist in the graph", test.className)

			collidingVal := ValueBindingKey(test.collidingVal)
			typeDeps := depGraph.GetDeps(typeKey)
			valueDeps := depGraph.GetDeps(valueKey)

			if test.wantDep {
				// A computed key is a real expression, so a reference inside it
				// still records a dependency on the referenced binding.
				require.True(t, typeDeps.Contains(collidingVal) || valueDeps.Contains(collidingVal),
					"class %s should depend on %q referenced by a computed key", test.className, test.collidingVal)
			} else {
				// A plain member name is a label, so it must not pull the
				// colliding value into the class's type or value dependencies.
				require.False(t, typeDeps.Contains(collidingVal),
					"type:%s should not depend on the colliding value binding", test.className)
				require.False(t, valueDeps.Contains(collidingVal),
					"value:%s should not depend on the colliding value binding", test.className)
			}
		})
	}
}

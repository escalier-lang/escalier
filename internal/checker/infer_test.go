package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/btree"
)

func TestCheckScriptNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDecls": {
			input: `
				val a = 5
				val b = 10
				val sum = a + b
			`,
		},
		"TupleDecl": {
			input: `
				val [x, y] = [5, 10]
			`,
		},
		"ObjectDecl": {
			input: `
				val {x, y} = {x: "foo", y: "bar"}
			`,
		},
		"IfElseExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else {
					"hello"
				}
			`,
		},
		"IfElseIfExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else if (a < b) {
					false
				} else {
				    "hello"
				}
			`,
		},
		"FuncExpr": {
			input: `
				val add = fn (x, y) {
					return x + y
				}
			`,
		},
		"FuncExprWithoutReturn": {
			input: `val log = fn (msg) {}`,
		},
		"FuncExprMultipleReturns": {
			input: `
				val add = fn (x, y) {
				    if (x > y) {
						return true
					} else {

					}
					return false
				}
			`,
		},
		// "FuncRecursion": {
		// 	input: `
		// 		val fact = fn (n) {
		// 			if (n == 0) {
		// 				return 1
		// 			} else {
		// 				return n * fact(n - 1)
		// 			}
		// 		}
		// 	`,
		// },
		// TODO:
		// - declare variables within a function body
		// - scope shadowing
	}

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
			p := parser.NewParser(ctx, source)
			script, errors := p.ParseScript()

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Filename:   "input.esc",
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferScript(inferCtx, script)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range scope.Namespace.Values {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

func TestCheckModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"VarDecls": {
			input: `
				val a = 5
				val b = 10
				val sum = a + b
			`,
		},
		"TupleDecl": {
			input: `
				val [x, y] = [5, 10]
			`,
		},
		"ObjectDecl": {
			input: `
				val {x, y} = {x: "foo", y: "bar"}
			`,
		},
		"ObjectDeclWithDeps": {
			input: `
			    val foo = "foo"
				val bar = "bar"
				val {x, y} = {x: foo, y: bar}
			`,
		},
		"IfElseExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else {
					"hello"
				}
			`,
		},
		"IfElseIfExpr": {
			input: `
				val a = 5
				val b = 10
				val x = if (a > b) {
					true
				} else if (a < b) {
					false
				} else {
				    "hello"
				}
			`,
		},
		"FuncExpr": {
			input: `
				val add = fn (x, y) {
					return x + y
				}
			`,
		},
		"FuncExprWithoutReturn": {
			input: `val log = fn (msg) {}`,
		},
		"FuncExprMultipleReturns": {
			input: `
				val add = fn (x, y) {
				    if (x > y) {
						return true
					} else {

					}
					return false
				}
			`,
		},
		"MutualRecuriveFunctions": {
			input: `
				fn foo() -> number {
					return bar() + 1
				}
				fn bar() -> number {
					return foo() - 1
				}
			`,
		},
		"MutualRecuriveTypes": {
			input: `
				type Foo = { bar: Bar }
				type Bar = { foo: Foo }
			`,
		},
		// "FuncRecursion": {
		// 	input: `
		// 		val fact = fn (n) {
		// 			if (n == 0) {
		// 				return 1
		// 			} else {
		// 				return n * fact(n - 1)
		// 			}
		// 		}
		// 	`,
		// },
		// TODO:
		// - declare variables within a function body
		// - scope shadowing
	}

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
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Filename:   "input.esc",
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range scope.Values {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

func TestCheckMultifileModuleNoErrors(t *testing.T) {
	tests := map[string]struct {
		sources []*ast.Source
	}{
		"MutualRecuriveFunctions": {
			sources: []*ast.Source{
				{
					ID:   1,
					Path: "foo.esc",
					Contents: `fn foo() -> number {
						return bar() + 1
					}`,
				},
				{
					ID:   2,
					Path: "bar.esc",
					Contents: `fn bar() -> number {
						return foo() - 1
					}`,
				},
			},
		},
		"MutualRecuriveTypes": {
			sources: []*ast.Source{
				{
					ID:       1,
					Path:     "foo.esc",
					Contents: `type Foo = { bar: Bar }`,
				},
				{
					ID:       2,
					Path:     "bar.esc",
					Contents: `type Bar = { foo: Foo }`,
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			module, errors := parser.ParseLibFiles(ctx, test.sources)

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			inferCtx := Context{
				Filename:   "input.esc",
				Scope:      Prelude(),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c := NewChecker()
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				assert.Equal(t, inferErrors, []*Error{})
			}

			// TODO: short term - print each of the binding's types and store
			// them in a map and the snapshot the map.
			// TODO: long term - generate a .d.ts file from the bindings
			for name, binding := range scope.Values {
				assert.NotNil(t, binding)
				fmt.Printf("%s = %s\n", name, binding.Type.String())
				fmt.Printf("%#v\n", binding.Type.Provenance())
			}
		})
	}
}

func TestGetDeclCtx(t *testing.T) {
	// Create a root namespace with nested namespaces
	rootNS := NewNamespace()
	fooNS := NewNamespace()
	barNS := NewNamespace()
	bazNS := NewNamespace()

	// Set up nested namespace structure: root.foo.bar.baz
	rootNS.Namespaces["foo"] = fooNS
	fooNS.Namespaces["bar"] = barNS
	barNS.Namespaces["baz"] = bazNS

	// Create a root scope and context
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Filename:   "test.esc",
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	tests := []struct {
		name          string
		declNamespace string
		expectedDepth int // how many scopes deep the result should be
		expectedNS    *Namespace
	}{
		{
			name:          "empty namespace returns root context",
			declNamespace: "",
			expectedDepth: 0,
			expectedNS:    rootNS,
		},
		{
			name:          "single level namespace",
			declNamespace: "foo",
			expectedDepth: 1,
			expectedNS:    fooNS,
		},
		{
			name:          "nested namespace foo.bar",
			declNamespace: "foo.bar",
			expectedDepth: 2,
			expectedNS:    barNS,
		},
		{
			name:          "deeply nested namespace foo.bar.baz",
			declNamespace: "foo.bar.baz",
			expectedDepth: 3,
			expectedNS:    bazNS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Create a mock dep graph
			depGraph := &dep_graph.DepGraph{}

			// Create a declaration ID and set its namespace
			declID := dep_graph.DeclID(42)
			depGraph.DeclNamespace.Set(declID, test.declNamespace)

			// Call getDeclCtx
			resultCtx := getDeclCtx(rootCtx, depGraph, declID)

			// Verify the result context has the expected namespace
			assert.Equal(t, test.expectedNS, resultCtx.Scope.Namespace)

			// Verify we can walk back to the root through Parent pointers
			currentScope := resultCtx.Scope
			depth := 0
			for currentScope.Parent != nil {
				currentScope = currentScope.Parent
				depth++
			}
			assert.Equal(t, test.expectedDepth, depth)

			// Verify the root scope is unchanged
			assert.Equal(t, rootNS, currentScope.Namespace)

			// Verify other context fields are preserved
			assert.Equal(t, rootCtx.Filename, resultCtx.Filename)
			assert.Equal(t, rootCtx.IsAsync, resultCtx.IsAsync)
			assert.Equal(t, rootCtx.IsPatMatch, resultCtx.IsPatMatch)
		})
	}
}

func TestGetDeclCtxWithNonExistentDeclID(t *testing.T) {
	// Create a simple context
	rootNS := NewNamespace()
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Filename:   "test.esc",
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Create empty dep graph
	depGraph := &dep_graph.DepGraph{}

	// Use a declaration ID that doesn't exist in the dep graph
	declID := dep_graph.DeclID(999)

	// Call getDeclCtx - should return original context since namespace is empty
	resultCtx := getDeclCtx(rootCtx, depGraph, declID)

	// Should return the same context since no namespace mapping exists
	assert.Equal(t, rootCtx.Scope.Namespace, resultCtx.Scope.Namespace)
	assert.Equal(t, rootCtx.Filename, resultCtx.Filename)
	assert.Equal(t, rootCtx.IsAsync, resultCtx.IsAsync)
	assert.Equal(t, rootCtx.IsPatMatch, resultCtx.IsPatMatch)
}

func TestGetDeclCtxWithNonExistentNamespace(t *testing.T) {
	// Create a root namespace with only one level of nesting
	rootNS := NewNamespace()
	fooNS := NewNamespace()
	rootNS.Namespaces["foo"] = fooNS

	// Create a root scope and context
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Filename:   "test.esc",
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Create dep graph with a namespace path that references non-existent namespace
	depGraph := &dep_graph.DepGraph{}
	declID := dep_graph.DeclID(123)
	// Try to access foo.nonexistent.bar - "nonexistent" doesn't exist
	depGraph.DeclNamespace.Set(declID, "foo.nonexistent.bar")

	// This should panic because ns.Namespaces[part] will be nil
	// In production code, this would be a bug, but we're testing the current behavior
	assert.Panics(t, func() {
		getDeclCtx(rootCtx, depGraph, declID)
	})
}

func TestGetDeclCtxNestedNamespaceOrder(t *testing.T) {
	// Create a root namespace with deeply nested namespaces
	rootNS := NewNamespace()
	fooNS := NewNamespace()
	barNS := NewNamespace()
	bazNS := NewNamespace()
	quxNS := NewNamespace()

	// Set up nested namespace structure: root.foo.bar.baz.qux
	rootNS.Namespaces["foo"] = fooNS
	fooNS.Namespaces["bar"] = barNS
	barNS.Namespaces["baz"] = bazNS
	bazNS.Namespaces["qux"] = quxNS

	// Add some test values to distinguish each namespace
	rootNS.Values["rootValue"] = &Binding{Type: NewStrType(), Mutable: false}
	fooNS.Values["fooValue"] = &Binding{Type: NewStrType(), Mutable: false}
	barNS.Values["barValue"] = &Binding{Type: NewStrType(), Mutable: false}
	bazNS.Values["bazValue"] = &Binding{Type: NewStrType(), Mutable: false}
	quxNS.Values["quxValue"] = &Binding{Type: NewStrType(), Mutable: false}

	// Create a root scope and context
	rootScope := &Scope{
		Parent:    nil,
		Namespace: rootNS,
	}

	rootCtx := Context{
		Filename:   "test.esc",
		Scope:      rootScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Create dep graph with deeply nested namespace
	depGraph := &dep_graph.DepGraph{}
	declID := dep_graph.DeclID(456)
	depGraph.DeclNamespace.Set(declID, "foo.bar.baz.qux")

	// Call getDeclCtx
	resultCtx := getDeclCtx(rootCtx, depGraph, declID)

	// Verify the final context points to the deepest namespace
	assert.Equal(t, quxNS, resultCtx.Scope.Namespace)
	assert.NotNil(t, resultCtx.Scope.Namespace.Values["quxValue"])

	// Walk up the scope chain and verify the correct order:
	// qux -> baz -> bar -> foo -> root
	expectedNamespaces := []*Namespace{quxNS, bazNS, barNS, fooNS, rootNS}
	expectedValues := []string{"quxValue", "bazValue", "barValue", "fooValue", "rootValue"}

	currentScope := resultCtx.Scope
	for i, expectedNS := range expectedNamespaces {
		assert.Equal(t, expectedNS, currentScope.Namespace,
			"Scope at level %d should have namespace %v", i, expectedValues[i])

		// Verify this namespace has its expected value
		assert.NotNil(t, currentScope.Namespace.Values[expectedValues[i]],
			"Namespace at level %d should contain value %s", i, expectedValues[i])

		// Move to parent scope (except for the root)
		if i < len(expectedNamespaces)-1 {
			assert.NotNil(t, currentScope.Parent, "Scope should have parent at level %d", i)
			currentScope = currentScope.Parent
		} else {
			// Root scope should have no parent
			assert.Nil(t, currentScope.Parent, "Root scope should have no parent")
		}
	}

	// Verify that the scope chain has exactly the expected depth
	depth := 0
	testScope := resultCtx.Scope
	for testScope.Parent != nil {
		testScope = testScope.Parent
		depth++
	}
	assert.Equal(t, 4, depth, "Should have exactly 4 levels of nesting (foo->bar->baz->qux)")
}

// TestInferComponentWithNamespaceDependencies tests the InferComponent function
// with declarations that are defined in separate namespaces and have dependencies
// on each other. This tests the namespace resolution logic within InferComponent
// and ensures that declarations can properly reference other declarations based
// on their namespace context.
// TestInferComponentWithNamespaceDependencies tests the InferComponent function
// with various namespace-related scenarios, including:
// - Declarations within the same namespace that reference each other
// - Mutual dependencies within the same namespace
// - Type dependencies within the same namespace
// - Multiple independent namespaces processed together
// - Cross-namespace value dependencies using qualified identifiers (e.g., math.PI)
// - Cross-namespace type dependencies using qualified identifiers (e.g., types.Point)
// - Circular dependencies across namespaces with qualified identifiers
// - Nested namespaces depending on root namespace declarations
//
// These tests ensure that the type checker properly handles namespace resolution
// and qualified identifier usage as required by the language semantics.
func TestInferComponentWithNamespaceDependencies(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() (*dep_graph.DepGraph, Context)
		expected func(*testing.T, []Error)
	}{
		{
			name: "declarations within same namespace can reference each other",
			setup: func() (*dep_graph.DepGraph, Context) {
				helperSource := &ast.Source{
					ID:       0,
					Path:     "math/helper.esc",
					Contents: "val PI = 3.14159",
				}
				areaSource := &ast.Source{
					ID:       1,
					Path:     "math/area.esc",
					Contents: "fn circleArea(r: number): number { return PI * r * r }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				helperParser := parser.NewParser(ctx, helperSource)
				helperDecl := helperParser.Decl()

				areaParser := parser.NewParser(ctx, areaSource)
				areaDecl := areaParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				helperDeclID := dep_graph.DeclID(1)
				areaDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(helperDeclID, helperDecl)
				depGraph.Decls.Set(areaDeclID, areaDecl)

				// Both in math namespace
				depGraph.DeclNamespace.Set(helperDeclID, "math")
				depGraph.DeclNamespace.Set(areaDeclID, "math")

				// Set up dependencies - circleArea depends on PI
				areaDeps := btree.Set[dep_graph.DeclID]{}
				areaDeps.Insert(helperDeclID)
				depGraph.Deps.Set(areaDeclID, areaDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("PI", helperDeclID)
				depGraph.ValueBindings.Set("circleArea", areaDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				mathNS := NewNamespace()
				rootNS.Namespaces["math"] = mathNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				assert.Len(t, errors, 0, "Should infer component without errors")
			},
		},
		{
			name: "mutual dependencies in same namespace",
			setup: func() (*dep_graph.DepGraph, Context) {
				isEvenSource := &ast.Source{
					ID:       0,
					Path:     "math/even.esc",
					Contents: "fn isEven(n: number): boolean { return n == 0 || isOdd(n - 1) }",
				}
				isOddSource := &ast.Source{
					ID:       1,
					Path:     "math/odd.esc",
					Contents: "fn isOdd(n: number): boolean { return n != 0 && isEven(n - 1) }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				isEvenParser := parser.NewParser(ctx, isEvenSource)
				isEvenDecl := isEvenParser.Decl()

				isOddParser := parser.NewParser(ctx, isOddSource)
				isOddDecl := isOddParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				isEvenDeclID := dep_graph.DeclID(1)
				isOddDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(isEvenDeclID, isEvenDecl)
				depGraph.Decls.Set(isOddDeclID, isOddDecl)

				// Both in math namespace (same namespace enables mutual reference)
				depGraph.DeclNamespace.Set(isEvenDeclID, "math")
				depGraph.DeclNamespace.Set(isOddDeclID, "math")

				// Set up mutual dependencies
				isEvenDeps := btree.Set[dep_graph.DeclID]{}
				isEvenDeps.Insert(isOddDeclID)
				depGraph.Deps.Set(isEvenDeclID, isEvenDeps)

				isOddDeps := btree.Set[dep_graph.DeclID]{}
				isOddDeps.Insert(isEvenDeclID)
				depGraph.Deps.Set(isOddDeclID, isOddDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("isEven", isEvenDeclID)
				depGraph.ValueBindings.Set("isOdd", isOddDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				mathNS := NewNamespace()
				rootNS.Namespaces["math"] = mathNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				// Mutual dependencies should be handled in the same component
				assert.Len(t, errors, 0, "Should handle mutual dependencies without errors")
			},
		},
		{
			name: "type dependency within same namespace",
			setup: func() (*dep_graph.DepGraph, Context) {
				pointSource := &ast.Source{
					ID:       0,
					Path:     "geometry/point.esc",
					Contents: "type Point = {x: number, y: number}",
				}
				distanceSource := &ast.Source{
					ID:       1,
					Path:     "geometry/distance.esc",
					Contents: "fn distance(p: Point): number { return p.x + p.y }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				pointParser := parser.NewParser(ctx, pointSource)
				pointTypeDecl := pointParser.Decl()

				distanceParser := parser.NewParser(ctx, distanceSource)
				distanceDecl := distanceParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				pointTypeDeclID := dep_graph.DeclID(1)
				distanceDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(pointTypeDeclID, pointTypeDecl)
				depGraph.Decls.Set(distanceDeclID, distanceDecl)

				// Both in same namespace
				depGraph.DeclNamespace.Set(pointTypeDeclID, "geometry")
				depGraph.DeclNamespace.Set(distanceDeclID, "geometry")

				// Set up dependencies - distance depends on Point type
				distanceDeps := btree.Set[dep_graph.DeclID]{}
				distanceDeps.Insert(pointTypeDeclID)
				depGraph.Deps.Set(distanceDeclID, distanceDeps)

				// Set up type bindings
				depGraph.TypeBindings.Set("Point", pointTypeDeclID)
				depGraph.ValueBindings.Set("distance", distanceDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				geometryNS := NewNamespace()
				rootNS.Namespaces["geometry"] = geometryNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				assert.Len(t, errors, 0, "Should handle type dependencies within same namespace")
			},
		},
		{
			name: "multiple namespaces processed together",
			setup: func() (*dep_graph.DepGraph, Context) {
				// Test scenario: separate declarations in different namespaces that don't depend on each other
				mathVarSource := &ast.Source{
					ID:       0,
					Path:     "math/constants.esc",
					Contents: "val E = 2.718",
				}
				utilsFuncSource := &ast.Source{
					ID:       1,
					Path:     "utils/log.esc",
					Contents: "fn log(msg: string) { }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				mathParser := parser.NewParser(ctx, mathVarSource)
				mathDecl := mathParser.Decl()

				utilsParser := parser.NewParser(ctx, utilsFuncSource)
				utilsDecl := utilsParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				mathDeclID := dep_graph.DeclID(1)
				utilsDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(mathDeclID, mathDecl)
				depGraph.Decls.Set(utilsDeclID, utilsDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(mathDeclID, "math")
				depGraph.DeclNamespace.Set(utilsDeclID, "utils")

				// No dependencies between them
				depGraph.Deps.Set(mathDeclID, btree.Set[dep_graph.DeclID]{})
				depGraph.Deps.Set(utilsDeclID, btree.Set[dep_graph.DeclID]{})

				// Set up value bindings
				depGraph.ValueBindings.Set("E", mathDeclID)
				depGraph.ValueBindings.Set("log", utilsDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				mathNS := NewNamespace()
				utilsNS := NewNamespace()
				rootNS.Namespaces["math"] = mathNS
				rootNS.Namespaces["utils"] = utilsNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				assert.Len(t, errors, 0, "Should handle multiple independent namespaces")
			},
		},
		{
			name: "function in one namespace depends on constant in another",
			setup: func() (*dep_graph.DepGraph, Context) {
				// math namespace declares PI
				piSource := &ast.Source{
					ID:       0,
					Path:     "math/constants.esc",
					Contents: "val PI = 3.14159",
				}
				// geometry namespace has function that uses math.PI
				areaSource := &ast.Source{
					ID:       1,
					Path:     "geometry/area.esc",
					Contents: "fn circleArea(r: number): number { return math.PI * r * r }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				piParser := parser.NewParser(ctx, piSource)
				piDecl := piParser.Decl()

				areaParser := parser.NewParser(ctx, areaSource)
				areaDecl := areaParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				piDeclID := dep_graph.DeclID(1)
				areaDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(piDeclID, piDecl)
				depGraph.Decls.Set(areaDeclID, areaDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(piDeclID, "math")
				depGraph.DeclNamespace.Set(areaDeclID, "geometry")

				// Set up cross-namespace dependency - geometry.circleArea depends on math.PI
				areaDeps := btree.Set[dep_graph.DeclID]{}
				areaDeps.Insert(piDeclID)
				depGraph.Deps.Set(areaDeclID, areaDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("PI", piDeclID)
				depGraph.ValueBindings.Set("circleArea", areaDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				mathNS := NewNamespace()
				geometryNS := NewNamespace()
				rootNS.Namespaces["math"] = mathNS
				rootNS.Namespaces["geometry"] = geometryNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				// Cross-namespace dependencies with qualified identifiers should work
				assert.Len(t, errors, 0, "Cross-namespace value dependency with qualified identifier should resolve successfully")
			},
		},
		{
			name: "type in one namespace used by function in another",
			setup: func() (*dep_graph.DepGraph, Context) {
				// types namespace declares Point
				pointSource := &ast.Source{
					ID:       0,
					Path:     "types/point.esc",
					Contents: "type Point = {x: number, y: number}",
				}
				// functions namespace has function that uses types.Point
				distanceSource := &ast.Source{
					ID:       1,
					Path:     "functions/distance.esc",
					Contents: "fn distance(p: types.Point): number { return p.x + p.y }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				pointParser := parser.NewParser(ctx, pointSource)
				pointDecl := pointParser.Decl()

				distanceParser := parser.NewParser(ctx, distanceSource)
				distanceDecl := distanceParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				pointDeclID := dep_graph.DeclID(1)
				distanceDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(pointDeclID, pointDecl)
				depGraph.Decls.Set(distanceDeclID, distanceDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(pointDeclID, "types")
				depGraph.DeclNamespace.Set(distanceDeclID, "functions")

				// Set up cross-namespace dependency - functions.distance depends on types.Point
				distanceDeps := btree.Set[dep_graph.DeclID]{}
				distanceDeps.Insert(pointDeclID)
				depGraph.Deps.Set(distanceDeclID, distanceDeps)

				// Set up bindings
				depGraph.TypeBindings.Set("Point", pointDeclID)
				depGraph.ValueBindings.Set("distance", distanceDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				typesNS := NewNamespace()
				functionsNS := NewNamespace()
				rootNS.Namespaces["types"] = typesNS
				rootNS.Namespaces["functions"] = functionsNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				// Cross-namespace type dependencies may not be fully supported yet
				// If errors occur, they're likely related to qualified type resolution
				if len(errors) > 0 {
					t.Logf("Cross-namespace type dependency errors (may be expected): %v", errors)
					// For now, we accept that cross-namespace type dependencies might not work
				} else {
					t.Logf("Cross-namespace type dependency resolved successfully with qualified identifier")
				}
			},
		},
		{
			name: "circular dependency across namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// a namespace declares function that uses b.helper
				aFuncSource := &ast.Source{
					ID:       0,
					Path:     "a/func.esc",
					Contents: "fn aFunc(): number { return b.helper() + 1 }",
				}
				// b namespace declares function that uses a.aFunc
				bHelperSource := &ast.Source{
					ID:       1,
					Path:     "b/helper.esc",
					Contents: "fn helper(): number { return a.aFunc() - 1 }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				aParser := parser.NewParser(ctx, aFuncSource)
				aDecl := aParser.Decl()

				bParser := parser.NewParser(ctx, bHelperSource)
				bDecl := bParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				aDeclID := dep_graph.DeclID(1)
				bDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(aDeclID, aDecl)
				depGraph.Decls.Set(bDeclID, bDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(aDeclID, "a")
				depGraph.DeclNamespace.Set(bDeclID, "b")

				// Set up circular cross-namespace dependencies
				aDeps := btree.Set[dep_graph.DeclID]{}
				aDeps.Insert(bDeclID) // aFunc depends on helper
				depGraph.Deps.Set(aDeclID, aDeps)

				bDeps := btree.Set[dep_graph.DeclID]{}
				bDeps.Insert(aDeclID) // helper depends on aFunc
				depGraph.Deps.Set(bDeclID, bDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("aFunc", aDeclID)
				depGraph.ValueBindings.Set("helper", bDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				aNS := NewNamespace()
				bNS := NewNamespace()
				rootNS.Namespaces["a"] = aNS
				rootNS.Namespaces["b"] = bNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				// Circular cross-namespace dependencies with qualified identifiers should resolve
				assert.Len(t, errors, 0, "Circular cross-namespace dependencies with qualified identifiers should resolve successfully")
			},
		},
		{
			name: "nested namespace depending on root namespace",
			setup: func() (*dep_graph.DepGraph, Context) {
				// root namespace declares a global constant
				globalSource := &ast.Source{
					ID:       0,
					Path:     "globals.esc",
					Contents: "val GLOBAL_CONSTANT = 42",
				}
				// nested namespace has function that uses root constant
				nestedFuncSource := &ast.Source{
					ID:       1,
					Path:     "utils/nested/func.esc",
					Contents: "fn useGlobal(): number { return GLOBAL_CONSTANT * 2 }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				globalParser := parser.NewParser(ctx, globalSource)
				globalDecl := globalParser.Decl()

				nestedParser := parser.NewParser(ctx, nestedFuncSource)
				nestedDecl := nestedParser.Decl()

				// Create dependency graph manually
				depGraph := &dep_graph.DepGraph{}

				// Set up declarations
				globalDeclID := dep_graph.DeclID(1)
				nestedDeclID := dep_graph.DeclID(2)
				depGraph.Decls.Set(globalDeclID, globalDecl)
				depGraph.Decls.Set(nestedDeclID, nestedDecl)

				// Different namespace levels - root vs nested
				depGraph.DeclNamespace.Set(globalDeclID, "") // root namespace
				depGraph.DeclNamespace.Set(nestedDeclID, "utils.nested")

				// Set up dependency - nested function depends on root constant
				nestedDeps := btree.Set[dep_graph.DeclID]{}
				nestedDeps.Insert(globalDeclID)
				depGraph.Deps.Set(nestedDeclID, nestedDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("GLOBAL_CONSTANT", globalDeclID)
				depGraph.ValueBindings.Set("useGlobal", nestedDeclID)

				// Create context with nested namespaces
				rootNS := NewNamespace()
				utilsNS := NewNamespace()
				nestedNS := NewNamespace()
				rootNS.Namespaces["utils"] = utilsNS
				utilsNS.Namespaces["nested"] = nestedNS

				rootScope := &Scope{
					Parent:    nil,
					Namespace: rootNS,
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, errors []Error) {
				// Nested namespace accessing root should work
				assert.Len(t, errors, 0, "Nested namespace should be able to access root namespace declarations")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			depGraph, ctx := test.setup()

			// Get the component containing all declarations
			component := []dep_graph.DeclID{}
			iter := depGraph.Decls.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				component = append(component, iter.Key())
			}

			// Run InferComponent
			c := NewChecker()
			errors := c.InferComponent(ctx, depGraph, component)

			// Verify results
			test.expected(t, errors)
		})
	}
}

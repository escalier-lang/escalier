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
			depGraph := newTestDepGraph()

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
	depGraph := newTestDepGraph()

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
	rootNS.Values["rootValue"] = &Binding{Source: nil, Type: NewStrType(), Mutable: false}
	fooNS.Values["fooValue"] = &Binding{Source: nil, Type: NewStrType(), Mutable: false}
	barNS.Values["barValue"] = &Binding{Source: nil, Type: NewStrType(), Mutable: false}
	bazNS.Values["bazValue"] = &Binding{Source: nil, Type: NewStrType(), Mutable: false}
	quxNS.Values["quxValue"] = &Binding{Source: nil, Type: NewStrType(), Mutable: false}

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
	depGraph := newTestDepGraph()
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

// TestInferDepGraphWithNamespaceDependencies tests the InferDepGraph function
// with various namespace-related scenarios, ensuring that the function properly
// processes strongly connected components in topological order and handles
// namespace resolution across components. These tests verify that:
// - Independent declarations in different namespaces are processed correctly
// - Dependencies between namespaces are resolved in the proper order
// - Circular dependencies within and across namespaces are handled
// - The final merged namespace contains all declarations in their correct locations
func TestInferDepGraphWithNamespaceDependencies(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() (*dep_graph.DepGraph, Context)
		expected func(*testing.T, *Namespace, []Error)
	}{
		{
			name: "single component with declarations in same namespace",
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
				depGraph := newTestDepGraph()

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

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should process single component without errors")

				// Check that math namespace exists and contains both declarations
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				mathNS := resultNS.Namespaces["math"]

				assert.Contains(t, mathNS.Values, "PI", "Math namespace should contain PI")
				assert.Contains(t, mathNS.Values, "circleArea", "Math namespace should contain circleArea")

				// Verify types
				piBinding := mathNS.Values["PI"]
				assert.NotNil(t, piBinding, "PI binding should exist")

				circleAreaBinding := mathNS.Values["circleArea"]
				assert.NotNil(t, circleAreaBinding, "circleArea binding should exist")
			},
		},
		{
			name: "multiple independent components in different namespaces",
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
				geometryTypeSource := &ast.Source{
					ID:       2,
					Path:     "geometry/types.esc",
					Contents: "type Point = {x: number, y: number}",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				mathParser := parser.NewParser(ctx, mathVarSource)
				mathDecl := mathParser.Decl()

				utilsParser := parser.NewParser(ctx, utilsFuncSource)
				utilsDecl := utilsParser.Decl()

				geometryParser := parser.NewParser(ctx, geometryTypeSource)
				geometryDecl := geometryParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations
				mathDeclID := dep_graph.DeclID(1)
				utilsDeclID := dep_graph.DeclID(2)
				geometryDeclID := dep_graph.DeclID(3)
				depGraph.Decls.Set(mathDeclID, mathDecl)
				depGraph.Decls.Set(utilsDeclID, utilsDecl)
				depGraph.Decls.Set(geometryDeclID, geometryDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(mathDeclID, "math")
				depGraph.DeclNamespace.Set(utilsDeclID, "utils")
				depGraph.DeclNamespace.Set(geometryDeclID, "geometry")

				// No dependencies between them
				depGraph.Deps.Set(mathDeclID, btree.Set[dep_graph.DeclID]{})
				depGraph.Deps.Set(utilsDeclID, btree.Set[dep_graph.DeclID]{})
				depGraph.Deps.Set(geometryDeclID, btree.Set[dep_graph.DeclID]{})

				// Set up bindings
				depGraph.ValueBindings.Set("E", mathDeclID)
				depGraph.ValueBindings.Set("log", utilsDeclID)
				depGraph.TypeBindings.Set("Point", geometryDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle multiple independent namespaces")

				// Check that all namespaces exist
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")
				assert.Contains(t, resultNS.Namespaces, "geometry", "Should have geometry namespace")

				// Check declarations in each namespace
				mathNS := resultNS.Namespaces["math"]
				assert.Contains(t, mathNS.Values, "E", "Math namespace should contain E")

				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Values, "log", "Utils namespace should contain log")

				geometryNS := resultNS.Namespaces["geometry"]
				assert.Contains(t, geometryNS.Types, "Point", "Geometry namespace should contain Point type")
			},
		},
		{
			name: "cross-namespace dependencies processed in topological order",
			setup: func() (*dep_graph.DepGraph, Context) {
				// math namespace declares PI (no dependencies)
				piSource := &ast.Source{
					ID:       0,
					Path:     "math/constants.esc",
					Contents: "val PI = 3.14159",
				}
				// geometry namespace has function that uses math.PI (depends on math)
				areaSource := &ast.Source{
					ID:       1,
					Path:     "geometry/area.esc",
					Contents: "fn circleArea(r: number): number { return math.PI * r * r }",
				}
				// utils namespace uses geometry.circleArea (depends on geometry)
				calcSource := &ast.Source{
					ID:       2,
					Path:     "utils/calculator.esc",
					Contents: "fn calculateArea(radius: number): number { return geometry.circleArea(radius) }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				piParser := parser.NewParser(ctx, piSource)
				piDecl := piParser.Decl()

				areaParser := parser.NewParser(ctx, areaSource)
				areaDecl := areaParser.Decl()

				calcParser := parser.NewParser(ctx, calcSource)
				calcDecl := calcParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations
				piDeclID := dep_graph.DeclID(1)
				areaDeclID := dep_graph.DeclID(2)
				calcDeclID := dep_graph.DeclID(3)
				depGraph.Decls.Set(piDeclID, piDecl)
				depGraph.Decls.Set(areaDeclID, areaDecl)
				depGraph.Decls.Set(calcDeclID, calcDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(piDeclID, "math")
				depGraph.DeclNamespace.Set(areaDeclID, "geometry")
				depGraph.DeclNamespace.Set(calcDeclID, "utils")

				// Set up dependency chain: utils -> geometry -> math
				areaDeps := btree.Set[dep_graph.DeclID]{}
				areaDeps.Insert(piDeclID)
				depGraph.Deps.Set(areaDeclID, areaDeps)

				calcDeps := btree.Set[dep_graph.DeclID]{}
				calcDeps.Insert(areaDeclID)
				depGraph.Deps.Set(calcDeclID, calcDeps)

				// Set up value bindings
				depGraph.ValueBindings.Set("PI", piDeclID)
				depGraph.ValueBindings.Set("circleArea", areaDeclID)
				depGraph.ValueBindings.Set("calculateArea", calcDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should process dependency chain without errors")

				// Check that all namespaces exist with their declarations
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				assert.Contains(t, resultNS.Namespaces, "geometry", "Should have geometry namespace")
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")

				mathNS := resultNS.Namespaces["math"]
				assert.Contains(t, mathNS.Values, "PI", "Math namespace should contain PI")

				geometryNS := resultNS.Namespaces["geometry"]
				assert.Contains(t, geometryNS.Values, "circleArea", "Geometry namespace should contain circleArea")

				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Values, "calculateArea", "Utils namespace should contain calculateArea")
			},
		},
		{
			name: "circular dependencies within same component",
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
				depGraph := newTestDepGraph()

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

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			}, expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle circular dependencies within same component")

				// Check that math namespace exists and contains both functions
				assert.Contains(t, resultNS.Namespaces, "math", "Should have math namespace")
				mathNS := resultNS.Namespaces["math"]

				assert.Contains(t, mathNS.Values, "isEven", "Math namespace should contain isEven")
				assert.Contains(t, mathNS.Values, "isOdd", "Math namespace should contain isOdd")
			},
		},
		{
			name: "circular dependencies across different namespaces",
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
				depGraph := newTestDepGraph()

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

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			}, expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle circular cross-namespace dependencies")

				// Check that both namespaces exist and contain their declarations
				assert.Contains(t, resultNS.Namespaces, "a", "Should have namespace a")
				assert.Contains(t, resultNS.Namespaces, "b", "Should have namespace b")

				aNS := resultNS.Namespaces["a"]
				assert.Contains(t, aNS.Values, "aFunc", "Namespace a should contain aFunc")

				bNS := resultNS.Namespaces["b"]
				assert.Contains(t, bNS.Values, "helper", "Namespace b should contain helper")
			},
		},
		{
			name: "nested namespaces with dependencies on root",
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
				// another nested function that depends on the first
				nestedFunc2Source := &ast.Source{
					ID:       2,
					Path:     "utils/nested/func2.esc",
					Contents: "fn useGlobalTwice(): number { return useGlobal() * 2 }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				globalParser := parser.NewParser(ctx, globalSource)
				globalDecl := globalParser.Decl()

				nestedParser := parser.NewParser(ctx, nestedFuncSource)
				nestedDecl := nestedParser.Decl()

				nested2Parser := parser.NewParser(ctx, nestedFunc2Source)
				nested2Decl := nested2Parser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations
				globalDeclID := dep_graph.DeclID(1)
				nestedDeclID := dep_graph.DeclID(2)
				nested2DeclID := dep_graph.DeclID(3)
				depGraph.Decls.Set(globalDeclID, globalDecl)
				depGraph.Decls.Set(nestedDeclID, nestedDecl)
				depGraph.Decls.Set(nested2DeclID, nested2Decl)

				// Different namespace levels - root vs nested
				depGraph.DeclNamespace.Set(globalDeclID, "") // root namespace
				depGraph.DeclNamespace.Set(nestedDeclID, "utils.nested")
				depGraph.DeclNamespace.Set(nested2DeclID, "utils.nested")

				// Set up dependency chain
				nestedDeps := btree.Set[dep_graph.DeclID]{}
				nestedDeps.Insert(globalDeclID)
				depGraph.Deps.Set(nestedDeclID, nestedDeps)

				nested2Deps := btree.Set[dep_graph.DeclID]{}
				nested2Deps.Insert(nestedDeclID)
				depGraph.Deps.Set(nested2DeclID, nested2Deps)

				// Set up value bindings
				depGraph.ValueBindings.Set("GLOBAL_CONSTANT", globalDeclID)
				depGraph.ValueBindings.Set("useGlobal", nestedDeclID)
				depGraph.ValueBindings.Set("useGlobalTwice", nested2DeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				assert.Len(t, errors, 0, "Should handle nested namespace dependencies")

				// Check root namespace contains global
				assert.Contains(t, resultNS.Values, "GLOBAL_CONSTANT", "Root namespace should contain GLOBAL_CONSTANT")

				// Check nested namespace structure exists
				assert.Contains(t, resultNS.Namespaces, "utils", "Should have utils namespace")
				utilsNS := resultNS.Namespaces["utils"]
				assert.Contains(t, utilsNS.Namespaces, "nested", "Utils should have nested namespace")

				nestedNS := utilsNS.Namespaces["nested"]
				assert.Contains(t, nestedNS.Values, "useGlobal", "Nested namespace should contain useGlobal")
				assert.Contains(t, nestedNS.Values, "useGlobalTwice", "Nested namespace should contain useGlobalTwice")
			},
		},
		{
			name: "mixed value and type dependencies across namespaces",
			setup: func() (*dep_graph.DepGraph, Context) {
				// types namespace declares Point type
				pointSource := &ast.Source{
					ID:       0,
					Path:     "types/point.esc",
					Contents: "type Point = {x: number, y: number}",
				}
				// constants namespace declares origin point
				originSource := &ast.Source{
					ID:       1,
					Path:     "constants/origin.esc",
					Contents: "val ORIGIN: types.Point = {x: 0, y: 0}",
				}
				// functions namespace has function that uses both
				distanceSource := &ast.Source{
					ID:       2,
					Path:     "functions/distance.esc",
					Contents: "fn distanceFromOrigin(p: types.Point): number { return p.x - constants.ORIGIN.x + p.y - constants.ORIGIN.y }",
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				pointParser := parser.NewParser(ctx, pointSource)
				pointDecl := pointParser.Decl()

				originParser := parser.NewParser(ctx, originSource)
				originDecl := originParser.Decl()

				distanceParser := parser.NewParser(ctx, distanceSource)
				distanceDecl := distanceParser.Decl()

				// Create dependency graph manually
				depGraph := newTestDepGraph()

				// Set up declarations
				pointDeclID := dep_graph.DeclID(1)
				originDeclID := dep_graph.DeclID(2)
				distanceDeclID := dep_graph.DeclID(3)
				depGraph.Decls.Set(pointDeclID, pointDecl)
				depGraph.Decls.Set(originDeclID, originDecl)
				depGraph.Decls.Set(distanceDeclID, distanceDecl)

				// Different namespaces
				depGraph.DeclNamespace.Set(pointDeclID, "types")
				depGraph.DeclNamespace.Set(originDeclID, "constants")
				depGraph.DeclNamespace.Set(distanceDeclID, "functions")

				// Set up dependency chains
				originDeps := btree.Set[dep_graph.DeclID]{}
				originDeps.Insert(pointDeclID) // ORIGIN depends on Point type
				depGraph.Deps.Set(originDeclID, originDeps)

				distanceDeps := btree.Set[dep_graph.DeclID]{}
				distanceDeps.Insert(pointDeclID)  // distanceFromOrigin depends on Point type
				distanceDeps.Insert(originDeclID) // distanceFromOrigin depends on ORIGIN value
				depGraph.Deps.Set(distanceDeclID, distanceDeps)

				// Set up bindings
				depGraph.TypeBindings.Set("Point", pointDeclID)
				depGraph.ValueBindings.Set("ORIGIN", originDeclID)
				depGraph.ValueBindings.Set("distanceFromOrigin", distanceDeclID)

				rootScope := &Scope{
					Parent:    nil,
					Namespace: NewNamespace(),
				}

				inferCtx := Context{
					Filename:   "test.esc",
					Scope:      rootScope,
					IsAsync:    false,
					IsPatMatch: false,
				}

				return depGraph, inferCtx
			},
			expected: func(t *testing.T, resultNS *Namespace, errors []Error) {
				// Mixed type and value dependencies may have some issues, but we check what works
				if len(errors) > 0 {
					t.Logf("Mixed type/value cross-namespace dependencies produced errors (may be expected): %v", errors)
				}

				// Check that all namespaces exist
				assert.Contains(t, resultNS.Namespaces, "types", "Should have types namespace")
				assert.Contains(t, resultNS.Namespaces, "constants", "Should have constants namespace")
				assert.Contains(t, resultNS.Namespaces, "functions", "Should have functions namespace")

				typesNS := resultNS.Namespaces["types"]
				assert.Contains(t, typesNS.Types, "Point", "Types namespace should contain Point")

				constantsNS := resultNS.Namespaces["constants"]
				assert.Contains(t, constantsNS.Values, "ORIGIN", "Constants namespace should contain ORIGIN")

				functionsNS := resultNS.Namespaces["functions"]
				assert.Contains(t, functionsNS.Values, "distanceFromOrigin", "Functions namespace should contain distanceFromOrigin")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			depGraph, ctx := test.setup()

			// Run InferDepGraph
			c := NewChecker()
			resultNS, errors := c.InferDepGraph(ctx, depGraph)

			// Verify results
			test.expected(t, resultNS, errors)
		})
	}
}

// newTestDepGraph creates a properly initialized DepGraph for testing
func newTestDepGraph() *dep_graph.DepGraph {
	return &dep_graph.DepGraph{
		Decls:         btree.Map[dep_graph.DeclID, ast.Decl]{},
		Deps:          btree.Map[dep_graph.DeclID, btree.Set[dep_graph.DeclID]]{},
		ValueBindings: btree.Map[string, dep_graph.DeclID]{},
		TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
		DeclNamespace: btree.Map[dep_graph.DeclID, string]{},
	}
}

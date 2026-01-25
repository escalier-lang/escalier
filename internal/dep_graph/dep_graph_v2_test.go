package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestBuildDepGraphV2_SimpleBindings(t *testing.T) {
	tests := map[string]struct {
		sources         []*ast.Source
		expectedKeys    []BindingKey
		expectedDeps    map[BindingKey][]BindingKey
		expectedNSCount int
	}{
		"VarDecl_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 5
						var b = 10
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("a"),
				ValueBindingKey("b"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("a"): {},
				ValueBindingKey("b"): {},
			},
			expectedNSCount: 1,
		},
		"FuncDecl_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn add(a, b) {
							return a + b
						}
						fn multiply(x, y) {
							return x * y
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("add"),
				ValueBindingKey("multiply"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("add"):      {},
				ValueBindingKey("multiply"): {},
			},
			expectedNSCount: 1,
		},
		"TypeDecl_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						type Color = "red" | "green" | "blue"
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("Point"),
				TypeBindingKey("Color"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"): {},
				TypeBindingKey("Color"): {},
			},
			expectedNSCount: 1,
		},
		"Mixed_Declarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type User = {name: string, age: number}
						val defaultUser = {name: "John", age: 30}
						fn createUser(name, age) {
							return {name, age}
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("User"),
				ValueBindingKey("defaultUser"),
				ValueBindingKey("createUser"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("User"):       {},
				ValueBindingKey("defaultUser"): {},
				ValueBindingKey("createUser"):  {},
			},
			expectedNSCount: 1,
		},
		"Empty_Module": {
			sources: []*ast.Source{
				{
					ID:       0,
					Path:     "test.esc",
					Contents: ``,
				},
			},
			expectedKeys:    []BindingKey{},
			expectedDeps:    map[BindingKey][]BindingKey{},
			expectedNSCount: 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Verify bindings
			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)

			// Verify dependencies
			for key, expectedDeps := range test.expectedDeps {
				actualDeps := graph.GetDeps(key)
				actualDepsSlice := make([]BindingKey, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}
				assert.ElementsMatch(t, expectedDeps, actualDepsSlice,
					"For key %s: expected deps %v, got %v", key, expectedDeps, actualDepsSlice)
			}

			// Verify namespace count
			assert.Len(t, graph.Namespaces, test.expectedNSCount,
				"Expected %d namespaces, got %d", test.expectedNSCount, len(graph.Namespaces))
		})
	}
}

func TestBuildDepGraphV2_Dependencies(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"VarDecl_DependsOnVar": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 5
						val b = a + 10
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("a"): {},
				ValueBindingKey("b"): {ValueBindingKey("a")},
			},
		},
		"VarDecl_DependsOnFunc": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn getValue() {
							return 42
						}
						val result = getValue()
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("getValue"): {},
				ValueBindingKey("result"):   {ValueBindingKey("getValue")},
			},
		},
		"FuncDecl_DependsOnVar": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val multiplier = 2
						fn double(x) {
							return x * multiplier
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("multiplier"): {},
				ValueBindingKey("double"):     {ValueBindingKey("multiplier")},
			},
		},
		"TypeDecl_DependsOnType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						type Line = {start: Point, end: Point}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"): {},
				TypeBindingKey("Line"):  {TypeBindingKey("Point")},
			},
		},
		"VarDecl_WithTypeAnnotation": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						val origin: Point = {x: 0, y: 0}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"):    {},
				ValueBindingKey("origin"): {TypeBindingKey("Point")},
			},
		},
		"FuncDecl_WithTypeAnnotations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Input = {value: number}
						type Output = {result: number}
						fn process(input: Input) -> Output throws never {
							return {result: input.value * 2}
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Input"):     {},
				TypeBindingKey("Output"):    {},
				ValueBindingKey("process"): {TypeBindingKey("Input"), TypeBindingKey("Output")},
			},
		},
		"ChainedDependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 1
						val b = a + 1
						val c = b + 1
						val d = c + 1
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("a"): {},
				ValueBindingKey("b"): {ValueBindingKey("a")},
				ValueBindingKey("c"): {ValueBindingKey("b")},
				ValueBindingKey("d"): {ValueBindingKey("c")},
			},
		},
		"MultipleDependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val x = 1
						val y = 2
						val z = 3
						val sum = x + y + z
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("x"):   {},
				ValueBindingKey("y"):   {},
				ValueBindingKey("z"):   {},
				ValueBindingKey("sum"): {ValueBindingKey("x"), ValueBindingKey("y"), ValueBindingKey("z")},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			for key, expectedDeps := range test.expectedDeps {
				actualDeps := graph.GetDeps(key)
				actualDepsSlice := make([]BindingKey, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}
				assert.ElementsMatch(t, expectedDeps, actualDepsSlice,
					"For key %s: expected deps %v, got %v", key, expectedDeps, actualDepsSlice)
			}
		})
	}
}

func TestBuildDepGraphV2_Namespaces(t *testing.T) {
	tests := map[string]struct {
		sources            []*ast.Source
		expectedKeys       []BindingKey
		expectedDeps       map[BindingKey][]BindingKey
		expectedNamespaces []string
	}{
		"SingleSubdirectory": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val config = {debug: true}
					`,
				},
				{
					ID:   1,
					Path: "utils/helpers.esc",
					Contents: `
						fn helper() {
							return 42
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("config"),
				ValueBindingKey("utils.helper"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("config"):       {},
				ValueBindingKey("utils.helper"): {},
			},
			expectedNamespaces: []string{"", "utils"},
		},
		"CrossNamespaceDependency": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val result = utils.helper()
					`,
				},
				{
					ID:   1,
					Path: "utils/helpers.esc",
					Contents: `
						fn helper() {
							return 42
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("result"),
				ValueBindingKey("utils.helper"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("result"):       {ValueBindingKey("utils.helper")},
				ValueBindingKey("utils.helper"): {},
			},
			expectedNamespaces: []string{"", "utils"},
		},
		"NestedNamespaces": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val server = core.network.createServer(8080)
					`,
				},
				{
					ID:   1,
					Path: "core/network/http.esc",
					Contents: `
						fn createServer(port) {
							return {port: port}
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("server"),
				ValueBindingKey("core.network.createServer"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("server"):                    {ValueBindingKey("core.network.createServer")},
				ValueBindingKey("core.network.createServer"): {},
			},
			expectedNamespaces: []string{"", "core.network"},
		},
		"TypeDependencyAcrossNamespaces": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val user: models.User = {id: 1, name: "Alice"}
					`,
				},
				{
					ID:   1,
					Path: "models/user.esc",
					Contents: `
						type User = {id: number, name: string}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("user"),
				TypeBindingKey("models.User"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("user"):     {TypeBindingKey("models.User")},
				TypeBindingKey("models.User"): {},
			},
			expectedNamespaces: []string{"", "models"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Verify bindings
			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)

			// Verify dependencies
			for key, expectedDeps := range test.expectedDeps {
				actualDeps := graph.GetDeps(key)
				actualDepsSlice := make([]BindingKey, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}
				assert.ElementsMatch(t, expectedDeps, actualDepsSlice,
					"For key %s: expected deps %v, got %v", key, expectedDeps, actualDepsSlice)
			}

			// Verify namespaces
			assert.ElementsMatch(t, test.expectedNamespaces, graph.Namespaces,
				"Expected namespaces %v, got %v", test.expectedNamespaces, graph.Namespaces)
		})
	}
}

func TestBuildDepGraphV2_OverloadedFunctions(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedKeys []BindingKey
		declCounts   map[BindingKey]int // Expected number of declarations per key
	}{
		"OverloadedFunction_TwoDeclarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						declare fn add(a: number, b: number) -> number throws never
						declare fn add(a: string, b: string) -> string throws never
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("add"),
			},
			declCounts: map[BindingKey]int{
				ValueBindingKey("add"): 2,
			},
		},
		"OverloadedFunction_ThreeDeclarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						declare fn process(a: number) -> number throws never
						declare fn process(a: string) -> string throws never
						declare fn process(a: boolean) -> boolean throws never
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("process"),
			},
			declCounts: map[BindingKey]int{
				ValueBindingKey("process"): 3,
			},
		},
		"MixedOverloadedAndRegular": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						declare fn overloaded(a: number) -> number throws never
						declare fn overloaded(a: string) -> string throws never
						fn regular(x) {
							return x
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("overloaded"),
				ValueBindingKey("regular"),
			},
			declCounts: map[BindingKey]int{
				ValueBindingKey("overloaded"): 2,
				ValueBindingKey("regular"):    1,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Verify bindings
			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)

			// Verify declaration counts
			for key, expectedCount := range test.declCounts {
				decls := graph.GetDecls(key)
				assert.Len(t, decls, expectedCount,
					"For key %s: expected %d declarations, got %d", key, expectedCount, len(decls))
			}
		})
	}
}

func TestBuildDepGraphV2_InterfaceMerging(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedKeys []BindingKey
		declCounts   map[BindingKey]int
	}{
		"InterfaceMerging_TwoDeclarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Foo {
							x: number,
						}
						interface Foo {
							y: string,
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("Foo"),
			},
			declCounts: map[BindingKey]int{
				TypeBindingKey("Foo"): 2,
			},
		},
		"InterfaceMerging_ThreeDeclarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Config {
							debug: boolean,
						}
						interface Config {
							port: number,
						}
						interface Config {
							host: string,
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("Config"),
			},
			declCounts: map[BindingKey]int{
				TypeBindingKey("Config"): 3,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Verify bindings
			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)

			// Verify declaration counts
			for key, expectedCount := range test.declCounts {
				decls := graph.GetDecls(key)
				assert.Len(t, decls, expectedCount,
					"For key %s: expected %d declarations, got %d", key, expectedCount, len(decls))
			}
		})
	}
}

func TestBuildDepGraphV2_ClassAndEnum(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedKeys []BindingKey
	}{
		"ClassDecl_ValueAndTypeBinding": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						class Point(x: number, y: number) {}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("Point"),
				ValueBindingKey("Point"),
			},
		},
		"EnumDecl_ValueAndTypeBinding": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						enum Color {
							Red(),
							Green(),
							Blue(),
						}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("Color"),
				ValueBindingKey("Color"),
			},
		},
		"MixedClassEnumType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						class User(name: string) {}
						enum Status {
							Active(),
							Inactive(),
						}
						type Config = {user: User, status: Status}
					`,
				},
			},
			expectedKeys: []BindingKey{
				TypeBindingKey("User"),
				ValueBindingKey("User"),
				TypeBindingKey("Status"),
				ValueBindingKey("Status"),
				TypeBindingKey("Config"),
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)
		})
	}
}

func TestBuildDepGraphV2_StronglyConnectedComponents(t *testing.T) {
	tests := map[string]struct {
		sources              []*ast.Source
		expectedMultiNodeSCC int  // Expected multi-node SCCs (actual cycles)
		expectedCycleKeys    [][]BindingKey // Expected cycles (order within cycle doesn't matter)
	}{
		"NoCycles": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 1
						val b = a + 1
						val c = b + 1
					`,
				},
			},
			// With threshold 0, all nodes are returned as single-node SCCs
			// But no multi-node cycles or self-references exist
			expectedMultiNodeSCC: 0,
			expectedCycleKeys:    [][]BindingKey{},
		},
		"SelfReference": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Node = {value: number, next: Node | null}
					`,
				},
			},
			expectedMultiNodeSCC: 1, // Self-reference counts as a cycle
			expectedCycleKeys: [][]BindingKey{
				{TypeBindingKey("Node")},
			},
		},
		"MutualRecursion": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type A = {b: B}
						type B = {a: A}
					`,
				},
			},
			expectedMultiNodeSCC: 1,
			expectedCycleKeys: [][]BindingKey{
				{TypeBindingKey("A"), TypeBindingKey("B")},
			},
		},
		"ThreeWayCycle": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type A = {b: B}
						type B = {c: C}
						type C = {a: A}
					`,
				},
			},
			expectedMultiNodeSCC: 1,
			expectedCycleKeys: [][]BindingKey{
				{TypeBindingKey("A"), TypeBindingKey("B"), TypeBindingKey("C")},
			},
		},
		"MultipleSeparateCycles": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type A = {b: B}
						type B = {a: A}
						type X = {y: Y}
						type Y = {x: X}
					`,
				},
			},
			expectedMultiNodeSCC: 2,
			expectedCycleKeys: [][]BindingKey{
				{TypeBindingKey("A"), TypeBindingKey("B")},
				{TypeBindingKey("X"), TypeBindingKey("Y")},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Count actual cycles (multi-node SCCs or self-referencing single-node SCCs)
			actualCycles := 0
			for _, scc := range graph.Components {
				if len(scc) > 1 {
					actualCycles++
				} else if len(scc) == 1 {
					// Check for self-reference
					deps := graph.GetDeps(scc[0])
					if deps.Contains(scc[0]) {
						actualCycles++
					}
				}
			}

			// Verify cycle count
			assert.Equal(t, test.expectedMultiNodeSCC, actualCycles,
				"Expected %d cycles, got %d", test.expectedMultiNodeSCC, actualCycles)

			// Verify cycle contents (unordered comparison within each cycle)
			for _, expectedCycle := range test.expectedCycleKeys {
				found := false
				for _, actualCycle := range graph.Components {
					if len(actualCycle) == len(expectedCycle) {
						// Check if this cycle matches (elements may be in any order)
						match := true
						for _, expectedKey := range expectedCycle {
							keyFound := false
							for _, actualKey := range actualCycle {
								if actualKey == expectedKey {
									keyFound = true
									break
								}
							}
							if !keyFound {
								match = false
								break
							}
						}
						if match {
							found = true
							break
						}
					}
				}
				assert.True(t, found, "Expected cycle %v not found in components %v", expectedCycle, graph.Components)
			}
		})
	}
}

func TestBuildDepGraphV2_LocalShadowing(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"ParameterShadowsGlobal": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val x = 10
						fn useX(x) {
							return x + 1
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("x"):    {},
				ValueBindingKey("useX"): {}, // x is shadowed by parameter
			},
		},
		"LocalVarShadowsGlobal": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val global = 10
						fn test() {
							val global = 20
							return global
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("global"): {},
				ValueBindingKey("test"):   {}, // global is shadowed locally
			},
		},
		"NestedFunctionShadowing": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val outer = 10
						fn test() {
							return fn(outer) {
								return outer + 1
							}
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("outer"): {},
				ValueBindingKey("test"):  {}, // outer is shadowed in nested function
			},
		},
		"NoShadowing_UsesGlobal": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val global = 10
						fn test() {
							val local = 20
							return global + local
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("global"): {},
				ValueBindingKey("test"):   {ValueBindingKey("global")},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			for key, expectedDeps := range test.expectedDeps {
				actualDeps := graph.GetDeps(key)
				actualDepsSlice := make([]BindingKey, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}
				assert.ElementsMatch(t, expectedDeps, actualDepsSlice,
					"For key %s: expected deps %v, got %v", key, expectedDeps, actualDepsSlice)
			}
		})
	}
}

func TestBuildDepGraphV2_DestructuringPatterns(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedKeys []BindingKey
		expectedDeps map[BindingKey][]BindingKey
	}{
		"TupleDestructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val point = [10, 20]
						val [x, y] = point
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("point"),
				ValueBindingKey("x"),
				ValueBindingKey("y"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("point"): {},
				ValueBindingKey("x"):     {ValueBindingKey("point")},
				ValueBindingKey("y"):     {ValueBindingKey("point")},
			},
		},
		"ObjectDestructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val config = {host: "localhost", port: 8080}
						val {host, port} = config
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("config"),
				ValueBindingKey("host"),
				ValueBindingKey("port"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("config"): {},
				ValueBindingKey("host"):   {ValueBindingKey("config")},
				ValueBindingKey("port"):   {ValueBindingKey("config")},
			},
		},
		"NestedDestructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val data = {coords: [5, 10]}
						val {coords: [x, y]} = data
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("data"),
				ValueBindingKey("x"),
				ValueBindingKey("y"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("data"): {},
				ValueBindingKey("x"):    {ValueBindingKey("data")},
				ValueBindingKey("y"):    {ValueBindingKey("data")},
			},
		},
		"RestPattern": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val nums = [1, 2, 3, 4, 5]
						val [first, ...rest] = nums
					`,
				},
			},
			expectedKeys: []BindingKey{
				ValueBindingKey("nums"),
				ValueBindingKey("first"),
				ValueBindingKey("rest"),
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("nums"):  {},
				ValueBindingKey("first"): {ValueBindingKey("nums")},
				ValueBindingKey("rest"):  {ValueBindingKey("nums")},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			graph := BuildDepGraphV2(module)

			// Verify bindings
			actualKeys := graph.AllBindings()
			assert.ElementsMatch(t, test.expectedKeys, actualKeys,
				"Expected keys %v, got %v", test.expectedKeys, actualKeys)

			// Verify dependencies
			for key, expectedDeps := range test.expectedDeps {
				actualDeps := graph.GetDeps(key)
				actualDepsSlice := make([]BindingKey, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}
				assert.ElementsMatch(t, expectedDeps, actualDepsSlice,
					"For key %s: expected deps %v, got %v", key, expectedDeps, actualDepsSlice)
			}
		})
	}
}

func TestBindingKey_Methods(t *testing.T) {
	tests := map[string]struct {
		key          BindingKey
		expectedKind DepKind
		expectedName string
	}{
		"ValueBindingKey": {
			key:          ValueBindingKey("foo"),
			expectedKind: DepKindValue,
			expectedName: "foo",
		},
		"TypeBindingKey": {
			key:          TypeBindingKey("Bar"),
			expectedKind: DepKindType,
			expectedName: "Bar",
		},
		"ValueBindingKey_Qualified": {
			key:          ValueBindingKey("utils.helper"),
			expectedKind: DepKindValue,
			expectedName: "utils.helper",
		},
		"TypeBindingKey_Qualified": {
			key:          TypeBindingKey("models.User"),
			expectedKind: DepKindType,
			expectedName: "models.User",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.expectedKind, test.key.Kind(),
				"Expected kind %v, got %v", test.expectedKind, test.key.Kind())
			assert.Equal(t, test.expectedName, test.key.Name(),
				"Expected name %s, got %s", test.expectedName, test.key.Name())
		})
	}
}

func TestNewBindingKey(t *testing.T) {
	tests := map[string]struct {
		qualifiedName string
		kind          DepKind
		expectedKey   BindingKey
	}{
		"ValueKind": {
			qualifiedName: "foo",
			kind:          DepKindValue,
			expectedKey:   BindingKey("value:foo"),
		},
		"TypeKind": {
			qualifiedName: "Bar",
			kind:          DepKindType,
			expectedKey:   BindingKey("type:Bar"),
		},
		"ValueKind_Qualified": {
			qualifiedName: "utils.helper",
			kind:          DepKindValue,
			expectedKey:   BindingKey("value:utils.helper"),
		},
		"TypeKind_Qualified": {
			qualifiedName: "models.User",
			kind:          DepKindType,
			expectedKey:   BindingKey("type:models.User"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actualKey := NewBindingKey(test.qualifiedName, test.kind)
			assert.Equal(t, test.expectedKey, actualKey,
				"Expected key %s, got %s", test.expectedKey, actualKey)
		})
	}
}

func TestDepGraphV2_HelperMethods(t *testing.T) {
	t.Run("HasBinding", func(t *testing.T) {
		sources := []*ast.Source{
			{
				ID:   0,
				Path: "test.esc",
				Contents: `
					val foo = 42
					type Bar = string
				`,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		module, errors := parser.ParseLibFiles(ctx, sources)
		assert.Len(t, errors, 0, "Parser errors: %v", errors)

		graph := BuildDepGraphV2(module)

		assert.True(t, graph.HasBinding(ValueBindingKey("foo")))
		assert.True(t, graph.HasBinding(TypeBindingKey("Bar")))
		assert.False(t, graph.HasBinding(ValueBindingKey("nonexistent")))
		assert.False(t, graph.HasBinding(TypeBindingKey("nonexistent")))
	})

	t.Run("GetNamespace", func(t *testing.T) {
		sources := []*ast.Source{
			{
				ID:   0,
				Path: "main.esc",
				Contents: `
					val root = 42
				`,
			},
			{
				ID:   1,
				Path: "utils/helpers.esc",
				Contents: `
					val helper = 1
				`,
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		module, errors := parser.ParseLibFiles(ctx, sources)
		assert.Len(t, errors, 0, "Parser errors: %v", errors)

		graph := BuildDepGraphV2(module)

		assert.Equal(t, "", graph.GetNamespace(ValueBindingKey("root")))
		assert.Equal(t, "utils", graph.GetNamespace(ValueBindingKey("utils.helper")))
	})
}

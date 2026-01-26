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
				TypeBindingKey("User"):         {},
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
				TypeBindingKey("Point"):   {},
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
				TypeBindingKey("Input"):    {},
				TypeBindingKey("Output"):   {},
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
		"ClassWithComputedMembers": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val bar = "bar"
						val baz = "baz"
						class Foo() {
							[bar]: 42:number,
							[baz](self) {
								return self[bar]
							}
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("bar"): {},
				ValueBindingKey("baz"): {},
				TypeBindingKey("Foo"):  {ValueBindingKey("bar"), ValueBindingKey("baz")},
				ValueBindingKey("Foo"): {ValueBindingKey("bar"), ValueBindingKey("baz")},
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
				ValueBindingKey("user"):       {TypeBindingKey("models.User")},
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
		expectedMultiNodeSCC int            // Expected multi-node SCCs (actual cycles)
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
		"LocalVarNotHoisted_InitializerUsesGlobal": {
			// Local variable declarations should NOT be hoisted.
			// When we have `val x = x + 1`, the `x` in the initializer
			// should reference the module-level `x`, not the local `x` being declared.
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val x = 10
						fn test() {
							val x = x + 1
							return x
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("x"):    {},
				ValueBindingKey("test"): {ValueBindingKey("x")}, // x in initializer references global x
			},
		},
		"LocalVarNotHoisted_TypeAnnotationUsesGlobal": {
			// Type annotations in local variable declarations should also
			// use the global type before the binding shadows it.
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Config = {debug: boolean}
						fn test() {
							val Config: Config = {debug: true}
							return Config
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Config"): {},
				ValueBindingKey("test"):  {TypeBindingKey("Config")}, // Type annotation uses global Config
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

func TestBuildDepGraphV2_TypeOfDependencies(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"TypeOf_SimpleValueBinding": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val config = {debug: true, port: 8080}
						type Config = typeof config
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("config"): {},
				TypeBindingKey("Config"):  {ValueBindingKey("config")},
			},
		},
		"TypeOf_QualifiedName": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						type HelperType = typeof utils.helper
					`,
				},
				{
					ID:   1,
					Path: "utils/helpers.esc",
					Contents: `
						val helper = {name: "helper"}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("HelperType"):    {ValueBindingKey("utils.helper")},
				ValueBindingKey("utils.helper"): {},
			},
		},
		"TypeOf_InUnionType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val defaultConfig = {mode: "default"}
						val customConfig = {mode: "custom", extra: true}
						type Config = typeof defaultConfig | typeof customConfig
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("defaultConfig"): {},
				ValueBindingKey("customConfig"):  {},
				TypeBindingKey("Config"):         {ValueBindingKey("defaultConfig"), ValueBindingKey("customConfig")},
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

func TestBuildDepGraphV2_ObjectTypeComputedKeys(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"ObjectType_ComputedKeyDependency": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val keyName = "myKey"
						type MyObj = {[keyName]: string}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("keyName"): {},
				TypeBindingKey("MyObj"):    {ValueBindingKey("keyName")},
			},
		},
		"ObjectType_MultipleComputedKeys": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val key1 = "first"
						val key2 = "second"
						type MultiKey = {[key1]: number, [key2]: string}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("key1"):    {},
				ValueBindingKey("key2"):    {},
				TypeBindingKey("MultiKey"): {ValueBindingKey("key1"), ValueBindingKey("key2")},
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

func TestBuildDepGraphV2_InterfaceExtends(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"Interface_SingleExtends": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Base {
							id: number,
						}
						interface Child extends Base {
							name: string,
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Base"):  {},
				TypeBindingKey("Child"): {TypeBindingKey("Base")},
			},
		},
		"Interface_MultipleExtends": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Identifiable {
							id: number,
						}
						interface Named {
							name: string,
						}
						interface Entity extends Identifiable, Named {
							createdAt: string,
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Identifiable"): {},
				TypeBindingKey("Named"):        {},
				TypeBindingKey("Entity"):       {TypeBindingKey("Identifiable"), TypeBindingKey("Named")},
			},
		},
		"Interface_ExtendsAcrossNamespaces": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						interface AppUser extends models.User {
							permissions: string,
						}
					`,
				},
				{
					ID:   1,
					Path: "models/user.esc",
					Contents: `
						interface User {
							id: number,
							name: string,
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("AppUser"):     {TypeBindingKey("models.User")},
				TypeBindingKey("models.User"): {},
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

func TestBuildDepGraphV2_ClassExtends(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"Class_ExtendsOtherClass": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						class Animal(name: string) {}
						class Dog(name: string, breed: string) extends Animal {}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Animal"):  {},
				ValueBindingKey("Animal"): {},
				TypeBindingKey("Dog"):     {TypeBindingKey("Animal")},
				ValueBindingKey("Dog"):    {TypeBindingKey("Animal")},
			},
		},
		"Class_ExtendsAcrossNamespaces": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						class AppEntity(id: number) extends models.Entity {}
					`,
				},
				{
					ID:   1,
					Path: "models/entity.esc",
					Contents: `
						class Entity(id: number) {}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("AppEntity"):      {TypeBindingKey("models.Entity")},
				ValueBindingKey("AppEntity"):     {TypeBindingKey("models.Entity")},
				TypeBindingKey("models.Entity"):  {},
				ValueBindingKey("models.Entity"): {},
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

func TestBuildDepGraphV2_EnumSpread(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"Enum_SpreadOtherEnum": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						enum BaseColors {
							Red(),
							Green(),
							Blue(),
						}
						enum ExtendedColors {
							...BaseColors,
							Yellow(),
							Purple(),
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("BaseColors"):      {},
				ValueBindingKey("BaseColors"):     {},
				TypeBindingKey("ExtendedColors"):  {TypeBindingKey("BaseColors")},
				ValueBindingKey("ExtendedColors"): {TypeBindingKey("BaseColors")},
			},
		},
		"Enum_MultipleSpread": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						enum Status {
							Active(),
							Inactive(),
						}
						enum Priority {
							Low(),
							High(),
						}
						enum TaskState {
							...Status,
							...Priority,
							Pending(),
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Status"):     {},
				ValueBindingKey("Status"):    {},
				TypeBindingKey("Priority"):   {},
				ValueBindingKey("Priority"):  {},
				TypeBindingKey("TaskState"):  {TypeBindingKey("Status"), TypeBindingKey("Priority")},
				ValueBindingKey("TaskState"): {TypeBindingKey("Status"), TypeBindingKey("Priority")},
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

func TestBuildDepGraphV2_TypeParameterConstraints(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"TypeParam_ConstraintDependsOnType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Serializable {
							serialize() -> string,
						}
						type Container<T: Serializable> = {value: T}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Serializable"): {},
				TypeBindingKey("Container"):    {TypeBindingKey("Serializable")},
			},
		},
		"TypeParam_MultipleConstraints": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Identifiable {
							id: number,
						}
						interface Named {
							name: string,
						}
						type Entity<T: Identifiable, U: Named> = {item: T, label: U}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Identifiable"): {},
				TypeBindingKey("Named"):        {},
				TypeBindingKey("Entity"):       {TypeBindingKey("Identifiable"), TypeBindingKey("Named")},
			},
		},
		"TypeParam_DefaultType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type DefaultValue = {value: number}
						type Optional<T = DefaultValue> = {item: T | null}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("DefaultValue"): {},
				TypeBindingKey("Optional"):     {TypeBindingKey("DefaultValue")},
			},
		},
		"FuncDecl_TypeParamConstraint": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Comparable {
							compareTo(other: Comparable) -> number,
						}
						fn sort<T: Comparable>(items: Array<T>) -> Array<T> throws never {
							return items
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Comparable"): {TypeBindingKey("Comparable")}, // Self-reference in method signature
				ValueBindingKey("sort"):      {TypeBindingKey("Comparable")},
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

func TestBuildDepGraphV2_IntraNamespaceDependencies(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"SameNamespace_ValueDependsOnValue": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "utils/math.esc",
					Contents: `
						val PI = 3.14159
						fn circleArea(radius) {
							return PI * radius * radius
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("utils.PI"):         {},
				ValueBindingKey("utils.circleArea"): {ValueBindingKey("utils.PI")},
			},
		},
		"SameNamespace_TypeDependsOnType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "models/user.esc",
					Contents: `
						type Address = {street: string, city: string}
						type User = {name: string, address: Address}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("models.Address"): {},
				TypeBindingKey("models.User"):    {TypeBindingKey("models.Address")},
			},
		},
		"SameNamespace_ValueDependsOnType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "config/settings.esc",
					Contents: `
						type Config = {debug: boolean, port: number}
						val defaultConfig: Config = {debug: false, port: 8080}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("config.Config"):         {},
				ValueBindingKey("config.defaultConfig"): {TypeBindingKey("config.Config")},
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

func TestBuildDepGraphV2_MemberExpressionChains(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"DeepNestedNamespace": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val result = a.b.c.helper()
					`,
				},
				{
					ID:   1,
					Path: "a/b/c/helpers.esc",
					Contents: `
						fn helper() {
							return 42
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("result"):       {ValueBindingKey("a.b.c.helper")},
				ValueBindingKey("a.b.c.helper"): {},
			},
		},
		"PartialMatch_LocalShadows": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						fn test() {
							val a = {b: {c: 5}}
							return a.b.c
						}
					`,
				},
				{
					ID:   1,
					Path: "a/b/c/value.esc",
					Contents: `
						val something = 100
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("test"):            {},
				ValueBindingKey("a.b.c.something"): {},
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

func TestBuildDepGraphV2_TypeShadowing(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"TypeParam_ShadowsGlobalType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						type Container<Point> = {value: Point}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"):     {},
				TypeBindingKey("Container"): {}, // Point is shadowed by type param
			},
		},
		"TypeParam_PartialShadowing": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type A = {a: number}
						type B = {b: string}
						type Wrapper<A> = {first: A, second: B}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("A"):       {},
				TypeBindingKey("B"):       {},
				TypeBindingKey("Wrapper"): {TypeBindingKey("B")}, // A is shadowed, B is not
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

// TestBuildDepGraphV2_LocalDeclShadowing tests that local declarations within
// function bodies properly shadow module-level bindings. This covers the
// EnterStmt method's handling of DeclStmt for FuncDecl, TypeDecl, ClassDecl,
// InterfaceDecl, and EnumDecl.
func TestBuildDepGraphV2_LocalDeclShadowing(t *testing.T) {
	tests := map[string]struct {
		sources      []*ast.Source
		expectedDeps map[BindingKey][]BindingKey
	}{
		"LocalFuncDecl_ShadowsGlobalFunc": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn helper() {
							return 42
						}
						fn test() {
							fn helper() {
								return 100
							}
							return helper()
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("helper"): {},
				ValueBindingKey("test"):   {}, // helper is shadowed by local fn
			},
		},
		"LocalFuncDecl_HoistedShadowing": {
			// Local function declarations are hoisted, so the local fn shadows
			// the global from the start of the block
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn helper() {
							return 42
						}
						fn test() {
							val x = helper()
							fn helper() {
								return 100
							}
							return x
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("helper"): {},
				ValueBindingKey("test"):   {ValueBindingKey("helper")}, // Function decls are hoisted like in JS
			},
		},
		"LocalTypeDecl_ShadowsGlobalType": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Config = {debug: boolean}
						fn test() {
							type Config = {verbose: boolean}
							val cfg: Config = {verbose: true}
							return cfg
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Config"): {},
				ValueBindingKey("test"):  {}, // Config is shadowed by local type
			},
		},
		"LocalTypeDecl_UsesGlobalBeforeShadow": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Config = {debug: boolean}
						fn test() {
							val cfg1: Config = {debug: true}
							type Config = {verbose: boolean}
							val cfg2: Config = {verbose: true}
							return cfg1
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Config"): {},
				ValueBindingKey("test"):  {TypeBindingKey("Config")}, // Uses global before local shadow
			},
		},
		"LocalClassDecl_ShadowsGlobalClass": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						class Point(x: number, y: number) {}
						fn test() {
							class Point(a: string) {}
							val p: Point = Point("hello")
							return p
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"):  {},
				ValueBindingKey("Point"): {},
				ValueBindingKey("test"):  {}, // Both type and value Point are shadowed
			},
		},
		"LocalClassDecl_UsesGlobalBeforeShadow": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						class Point(x: number, y: number) {}
						fn test() {
							val p1: Point = Point(1, 2)
							class Point(a: string) {}
							val p2: Point = Point("hello")
							return p1
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Point"):  {},
				ValueBindingKey("Point"): {},
				ValueBindingKey("test"):  {TypeBindingKey("Point"), ValueBindingKey("Point")}, // Uses global before shadow
			},
		},
		"LocalInterfaceDecl_ShadowsGlobalInterface": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Printable {
							print() -> string,
						}
						fn test() {
							interface Printable {
								display() -> string,
							}
							val obj: Printable = {display: fn() { return "hello" }}
							return obj
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Printable"): {},
				ValueBindingKey("test"):     {}, // Printable is shadowed by local interface
			},
		},
		"LocalInterfaceDecl_UsesGlobalBeforeShadow": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						interface Printable {
							print() -> string,
						}
						fn test() {
							val obj1: Printable = {print: fn() { return "hello" }}
							interface Printable {
								display() -> string,
							}
							val obj2: Printable = {display: fn() { return "world" }}
							return obj1
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Printable"): {},
				ValueBindingKey("test"):     {TypeBindingKey("Printable")}, // Uses global before shadow
			},
		},
		"LocalEnumDecl_ShadowsGlobalEnum": {
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
						fn test() {
							enum Color {
								Black(),
								White(),
							}
							val c: Color = Color.Black()
							return c
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Color"):  {},
				ValueBindingKey("Color"): {},
				ValueBindingKey("test"):  {}, // Both type and value Color are shadowed
			},
		},
		"LocalEnumDecl_UsesGlobalBeforeShadow": {
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
						fn test() {
							val c1: Color = Color.Red()
							enum Color {
								Black(),
								White(),
							}
							val c2: Color = Color.Black()
							return c1
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				TypeBindingKey("Color"):  {},
				ValueBindingKey("Color"): {},
				ValueBindingKey("test"):  {TypeBindingKey("Color"), ValueBindingKey("Color")}, // Uses global before shadow
			},
		},
		"NestedBlocks_LocalDeclsShadow": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn helper() {
							return 1
						}
						type Config = {a: number}
						fn test() {
							val x = helper()
							val y: Config = {a: 1}
							if true {
								fn helper() {
									return 2
								}
								type Config = {b: string}
								val z = helper()
								val w: Config = {b: "test"}
							}
							return x
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("helper"): {},
				TypeBindingKey("Config"):  {},
				ValueBindingKey("test"):   {ValueBindingKey("helper"), TypeBindingKey("Config")}, // Uses global outside nested block
			},
		},
		"MixedLocalDecls_AllTypes": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn globalFunc() { return 1 }
						type GlobalType = {x: number}
						class GlobalClass(n: number) {}
						interface GlobalInterface { foo() -> number }
						enum GlobalEnum { A(), B() }

						fn test() {
							fn globalFunc() { return 2 }
							type GlobalType = {y: string}
							class GlobalClass(s: string) {}
							interface GlobalInterface { bar() -> string }
							enum GlobalEnum { C(), D() }

							val a = globalFunc()
							val b: GlobalType = {y: "test"}
							val c: GlobalClass = GlobalClass("hi")
							val d: GlobalInterface = {bar: fn() { return "x" }}
							val e: GlobalEnum = GlobalEnum.C()
							return a
						}
					`,
				},
			},
			expectedDeps: map[BindingKey][]BindingKey{
				ValueBindingKey("globalFunc"):     {},
				TypeBindingKey("GlobalType"):      {},
				TypeBindingKey("GlobalClass"):     {},
				ValueBindingKey("GlobalClass"):    {},
				TypeBindingKey("GlobalInterface"): {},
				TypeBindingKey("GlobalEnum"):      {},
				ValueBindingKey("GlobalEnum"):     {},
				ValueBindingKey("test"):           {}, // All global bindings are shadowed
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

package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func parseModule(input string) *ast.Module {
	source := &ast.Source{
		Path:     "test.esc",
		Contents: input,
		ID:       0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	if len(errors) > 0 {
		panic(errors[0])
	}
	return module
}

// parseMultiFileModule parses multiple source files and returns a module
func parseMultiFileModule(sources map[string]string) *ast.Module {
	astSources := make([]*ast.Source, 0, len(sources))
	id := 0
	for path, contents := range sources {
		astSources = append(astSources, &ast.Source{
			Path:     path,
			Contents: contents,
			ID:       id,
		})
		id++
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	module, errors := parser.ParseLibFiles(ctx, astSources)
	if len(errors) > 0 {
		panic(errors[0])
	}
	return module
}

func TestFindCyclesV2(t *testing.T) {
	tests := map[string]struct {
		input             string
		expectedCycles    int
		expectProblematic bool
		description       string
	}{
		"TypeOnlyCycle_Allowed": {
			input: `
				type Foo = number | { bar: Bar }
				type Bar = string | { foo: Foo }
			`,
			expectedCycles:    0, // Type cycles are allowed, so no problematic cycles
			expectProblematic: false,
			description:       "Type-only cycles should be allowed",
		},
		"RecursiveType_Allowed": {
			input: `
				type Node = { value: string, children?: Array<Node> }
			`,
			expectedCycles:    0, // Self-referencing types are allowed
			expectProblematic: false,
			description:       "Self-referencing types should be allowed",
		},
		"SimpleValueCycle_Problematic": {
			input: `
				val a = b
				val b = a
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Simple value cycles should be problematic",
		},
		"ObjectPropertyCycle_Problematic": {
			input: `
				val obj1 = { foo: obj2.bar }
				val obj2 = { bar: obj1.foo }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Object property cycles should be problematic",
		},
		"DestructuringCycle_Problematic": {
			input: `
				val [p, q] = [x, 5]
				val [x, y] = [p, 10]
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Destructuring cycles should be problematic",
		},
		"FunctionCallCycle_OutsideFunction_Problematic": {
			input: `
				fn a() { return b }
				val b = a()
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Function call cycles outside function bodies should be problematic",
		},
		"FunctionExpressionCallCycle_OutsideFunction_Problematic": {
			input: `
				val a = fn() { return b }
				val b = a()
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Function expression call cycles outside function bodies should be problematic",
		},
		"FunctionWithParamCallCycle_OutsideFunction_Problematic": {
			input: `
				fn a(c: number) { return b }
				val b = a()
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Function call cycles outside function bodies should be problematic",
		},
		"FunctionWithParamExpressionCallCycle_OutsideFunction_Problematic": {
			input: `
				val a = fn(c: number) { return b }
				val b = a()
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Function expression call cycles outside function bodies should be problematic",
		},
		"MethodCallCycle_OutsideFunction_Problematic": {
			input: `
				val obj1 = { foo() { obj2.bar } }
				val obj2 = { bar: obj1.foo() }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Method call cycles outside function bodies should be problematic",
		},
		"DeepMethodCallCycle_OutsideFunction_Problematic": {
			input: `
				val obj1 = { a: { b() { obj2.c.d } } }
				val obj2 = { c: { d: obj1.a.b() } }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Method call cycles outside function bodies should be problematic",
		},
		"ObjectWithComputedKeyCycles_Problematic": {
			input: `
				val obj1 = { [obj2.key]: 5, key: "foo" }
				val obj2 = { [obj1.key]: true, key: "bar" }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Objects with cycles between computed keys should be problematic",
		},
		"TypeWithComputedKeyCycle_Problematic": {
			input: `
				val obj: Obj = { foo: "foo" }
				type Obj = { [obj.foo]: "foo" }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Type annotations that that use computed keys in a cycle should be problematic",
		},
		"TypeWithComputedKeyNoCycle_Allowed": {
			input: `
				val obj = { key: "foo" }
				type Obj = { [obj.key]: "foo" }
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Type annotations that that use computed keys not in a cycle should be allowed",
		},
		"TupleFuncCallCycle_OutsideFunction_Problematic": {
			input: `
				val tuple1 = [ fn () { tuple2[0] } ]
				val tuple2 = [ tuple1[0] ]
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Call cycles outside function bodies should be problematic",
		},
		"MutualRecursion_InsideFunction_Allowed": {
			input: `
				fn a() { b() }
				fn b() { a() }
			`,
			expectedCycles:    0, // Calls inside function bodies are allowed
			expectProblematic: false,
			description:       "Mutual recursion inside function bodies should be allowed",
		},
		"FunctionExpressionMutualRecursion_InsideFunction_Allowed": {
			input: `
				val c = fn() { d() }
				val d = fn() { c() }
			`,
			expectedCycles:    0, // Calls inside function bodies are allowed
			expectProblematic: false,
			description:       "Function expression mutual recursion inside function bodies should be allowed",
		},
		"MethodMutualRecursion_InsideFunction_Allowed": {
			input: `
				val obj1 = { foo() { obj2.bar() } }
				val obj2 = { bar() { obj1.foo() } }
			`,
			expectedCycles:    0, // Calls inside method bodies are allowed
			expectProblematic: false,
			description:       "Method mutual recursion inside function bodies should be allowed",
		},
		"TupleFuncMutualRecursion_InsideFunction_Allowed": {
			input: `
				val tuple1 = [ fn() { tuple2[0]() } ]
				val tuple2 = [ fn() { tuple1[0]() } ]
			`,
			expectedCycles:    0, // Calls inside method bodies are allowed
			expectProblematic: false,
			description:       "Mutual recursion inside function bodies defined in tuples should be allowed",
		},
		"MixedTypeValueCycle_OnlyValueProblematic": {
			input: `
				type UserType = { name: string, data: DataType }
				type DataType = { user: UserType, value: number }
				val user = { name: "test", data: data }
				val data = { user: user, value: 42 }
			`,
			expectedCycles:    1, // Only the value cycle should be problematic
			expectProblematic: true,
			description:       "Mixed type/value cycles should only report value cycles as problematic",
		},
		"ComplexNesting_FunctionCallsInBody_Allowed": {
			input: `
				fn process() {
					return fn() {
						helper()
					}
				}
				fn helper() {
					process()
				}
			`,
			expectedCycles:    0, // Calls are inside function bodies
			expectProblematic: false,
			description:       "Complex nested function calls inside bodies should be allowed",
		},
		"NoCycle_ShouldNotReport": {
			input: `
				val a = 1
				val b = a + 2
				val c = b * 3
				fn helper() { return c }
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "No cycles should not report any problems",
		},
		"TypeOfCycle_OutsideFunction_Problematic": {
			input: `
				val a: typeof b = 1
				val b: typeof a = 2
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "typeof creating value cycles outside function bodies should be problematic",
		},
		"TypeOfNoCycle_Allowed": {
			input: `
				val a = 42
				val b: typeof a = 10
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "typeof without cycles should be allowed",
		},
		"TypeOfInsideFunction_Allowed": {
			input: `
				val a = 1
				fn foo() {
					val b: typeof a = 2
					return b
				}
				val c = foo()
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "typeof inside function bodies should be allowed even with references",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseModule(test.input)
			depGraph := BuildDepGraphV2(module)
			cycles := depGraph.FindCyclesV2()

			assert.Equal(t, test.expectedCycles, len(cycles),
				"Expected %d problematic cycles, got %d. %s",
				test.expectedCycles, len(cycles), test.description)

			if test.expectProblematic {
				assert.Greater(t, len(cycles), 0,
					"Expected problematic cycles but none found. %s", test.description)

				// Verify that the cycle info contains meaningful data
				for _, cycle := range cycles {
					assert.Greater(t, len(cycle.Cycle), 1,
						"Cycle should contain more than one binding")
					assert.NotEmpty(t, cycle.Message,
						"Cycle should have a descriptive message")
				}
			} else {
				assert.Equal(t, 0, len(cycles),
					"Expected no problematic cycles but found %d. %s",
					len(cycles), test.description)
			}
		})
	}
}

func TestFindCyclesV2_EdgeCases(t *testing.T) {
	tests := map[string]struct {
		input             string
		expectedCycles    int
		expectProblematic bool
		description       string
	}{
		"SelfReference_Variable": {
			input: `
				val a = a
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Self-referencing variable should be detected as problematic",
		},
		"SelfReference_Function": {
			input: `
				val f = fn() { return f() }
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Self-referencing function expression should be allowed (call is inside function body)",
		},
		"IndirectCycle_ThreeBindings": {
			input: `
				val a = b
				val b = c
				val c = a
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Indirect cycle with three bindings should be detected",
		},
		"PartialCycle_WithNonCyclicDependencies": {
			input: `
				val independent = 42
				val a = b + independent
				val b = a + independent
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Cycle should be detected even with non-cyclic dependencies",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseModule(test.input)
			depGraph := BuildDepGraphV2(module)
			cycles := depGraph.FindCyclesV2()

			// Assert expected number of cycles
			assert.Equal(t, test.expectedCycles, len(cycles),
				"Expected %d problematic cycles, got %d. %s",
				test.expectedCycles, len(cycles), test.description)

			if test.expectProblematic {
				assert.Greater(t, len(cycles), 0,
					"Expected problematic cycles but none found. %s", test.description)

				// Verify that the cycle info contains meaningful data
				for _, cycle := range cycles {
					assert.GreaterOrEqual(t, len(cycle.Cycle), 1,
						"Cycle should contain at least one binding")
					assert.NotEmpty(t, cycle.Message,
						"Cycle should have a descriptive message")
				}
			} else {
				assert.Equal(t, 0, len(cycles),
					"Expected no problematic cycles but found %d. %s",
					len(cycles), test.description)
			}

			// Log results for debugging
			t.Logf("Test: %s", test.description)
			t.Logf("Found %d problematic cycles", len(cycles))
			for i, cycle := range cycles {
				names := GetBindingNames(cycle.Cycle)
				t.Logf("Cycle %d: %v - %s", i+1, names, cycle.Message)
			}
		})
	}
}

func TestBindingKeyHelpers(t *testing.T) {
	t.Run("IsValueBinding", func(t *testing.T) {
		valueKey := ValueBindingKey("foo")
		assert.True(t, valueKey.IsValueBinding(), "ValueBindingKey should return true for IsValueBinding")
		assert.False(t, valueKey.IsTypeBinding(), "ValueBindingKey should return false for IsTypeBinding")
	})

	t.Run("IsTypeBinding", func(t *testing.T) {
		typeKey := TypeBindingKey("Bar")
		assert.True(t, typeKey.IsTypeBinding(), "TypeBindingKey should return true for IsTypeBinding")
		assert.False(t, typeKey.IsValueBinding(), "TypeBindingKey should return false for IsValueBinding")
	})

	t.Run("GetBindingNames", func(t *testing.T) {
		keys := []BindingKey{
			ValueBindingKey("foo"),
			TypeBindingKey("Bar"),
			ValueBindingKey("baz"),
		}
		names := GetBindingNames(keys)
		expected := []string{"foo", "Bar", "baz"}
		assert.Equal(t, expected, names, "GetBindingNames should extract names correctly")
	})

	t.Run("GetBindingNames_EmptySlice", func(t *testing.T) {
		keys := []BindingKey{}
		names := GetBindingNames(keys)
		assert.Empty(t, names, "GetBindingNames should return empty slice for empty input")
		assert.NotNil(t, names, "GetBindingNames should return non-nil slice")
	})
}

func TestFindCyclesV2_NamespacedBindings(t *testing.T) {
	tests := map[string]struct {
		sources           map[string]string
		expectedCycles    int
		expectProblematic bool
		description       string
	}{
		"NamespacedValueCycle_Problematic": {
			sources: map[string]string{
				"utils/file1.esc": `
					val a = utils.b
				`,
				"utils/file2.esc": `
					val b = utils.a
				`,
			},
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Namespaced value cycles should be problematic",
		},
		"NamespacedTypeOnlyCycle_Allowed": {
			sources: map[string]string{
				"types/file1.esc": `
					type Foo = { bar: types.Bar }
				`,
				"types/file2.esc": `
					type Bar = { foo: types.Foo }
				`,
			},
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Namespaced type-only cycles should be allowed",
		},
		"CrossNamespaceCycle_Problematic": {
			sources: map[string]string{
				"ns1/file.esc": `
					val x = ns2.y
				`,
				"ns2/file.esc": `
					val y = ns1.x
				`,
			},
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Cross-namespace value cycles should be problematic",
		},
		"NamespacedMutualRecursion_InsideFunction_Allowed": {
			sources: map[string]string{
				"funcs/a.esc": `
					fn a() { funcs.b() }
				`,
				"funcs/b.esc": `
					fn b() { funcs.a() }
				`,
			},
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Namespaced mutual recursion inside function bodies should be allowed",
		},
		"NestedNamespace_ValueCycle_Problematic": {
			sources: map[string]string{
				"core/utils/file1.esc": `
					val helper1 = core.utils.helper2
				`,
				"core/utils/file2.esc": `
					val helper2 = core.utils.helper1
				`,
			},
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Nested namespace value cycles should be problematic",
		},
		"NestedNamespace_TypeOnlyCycle_Allowed": {
			sources: map[string]string{
				"api/types/file1.esc": `
					type Request = { response: api.types.Response }
				`,
				"api/types/file2.esc": `
					type Response = { request: api.types.Request }
				`,
			},
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Nested namespace type-only cycles should be allowed",
		},
		"MixedNamespaceAndRoot_Cycle_Problematic": {
			sources: map[string]string{
				"main.esc": `
					val rootVal = utils.utilVal
				`,
				"utils/util.esc": `
					val utilVal = rootVal
				`,
			},
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Cycles between root namespace and sub-namespace should be problematic",
		},
		"MultipleNamespaces_NoCycle_Allowed": {
			sources: map[string]string{
				"main.esc": `
					val a = 1
				`,
				"utils/helper.esc": `
					val b = 2
				`,
				"core/processor.esc": `
					val c = utils.b + a
				`,
			},
			expectedCycles:    0,
			expectProblematic: false,
			description:       "No cycles across multiple namespaces should be allowed",
		},
		"NamespacedFunctionCall_OutsideFunction_Problematic": {
			sources: map[string]string{
				"handlers/a.esc": `
					fn processA() { return handlers.dataB }
				`,
				"handlers/b.esc": `
					val dataB = handlers.processA()
				`,
			},
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Namespaced function call cycles outside function bodies should be problematic",
		},
		"ComplexCrossNamespaceDependencies_NoCycle": {
			sources: map[string]string{
				"models/user.esc": `
					type User = { name: string, config: config.Config }
				`,
				"config/settings.esc": `
					type Config = { theme: string }
				`,
				"services/auth.esc": `
					val authenticate = fn(user: models.User) { return user.config }
				`,
			},
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Complex cross-namespace dependencies without cycles should be allowed",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseMultiFileModule(test.sources)
			depGraph := BuildDepGraphV2(module)
			cycles := depGraph.FindCyclesV2()

			assert.Equal(t, test.expectedCycles, len(cycles),
				"Expected %d problematic cycles, got %d. %s",
				test.expectedCycles, len(cycles), test.description)

			if test.expectProblematic {
				assert.Greater(t, len(cycles), 0,
					"Expected problematic cycles but none found. %s", test.description)

				// Verify cycle info
				for _, cycle := range cycles {
					assert.GreaterOrEqual(t, len(cycle.Cycle), 1,
						"Cycle should contain at least one binding")
					assert.NotEmpty(t, cycle.Message,
						"Cycle should have a descriptive message")
				}
			} else {
				assert.Equal(t, 0, len(cycles),
					"Expected no problematic cycles but found %d. %s",
					len(cycles), test.description)
			}

			// Log cycle details for debugging
			for i, cycle := range cycles {
				names := GetBindingNames(cycle.Cycle)
				t.Logf("Cycle %d: %v - %s", i+1, names, cycle.Message)
			}
		})
	}
}

func TestFindCyclesV2_InterfaceMerging(t *testing.T) {
	tests := map[string]struct {
		input             string
		expectedCycles    int
		expectProblematic bool
		description       string
	}{
		"MergedInterfaces_TypeOnlyCycle_Allowed": {
			input: `
				interface User {
					name: string
				}
				interface User {
					age: number
				}
				type UserRef = User
			`,
			expectedCycles:    0,
			expectProblematic: false,
			description:       "Merged interfaces in type-only cycles should be allowed",
		},
		"MergedInterfaces_MixedCycle_Problematic": {
			input: `
				interface Config {
					value: number
				}
				interface Config {
					ref: typeof configInstance
				}
				val configInstance: Config = { value: 42, ref: configInstance }
			`,
			expectedCycles:    1,
			expectProblematic: true,
			description:       "Merged interfaces in mixed cycles should be problematic",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseModule(test.input)
			depGraph := BuildDepGraphV2(module)
			cycles := depGraph.FindCyclesV2()

			assert.Equal(t, test.expectedCycles, len(cycles),
				"Expected %d problematic cycles, got %d. %s",
				test.expectedCycles, len(cycles), test.description)

			if test.expectProblematic {
				assert.Greater(t, len(cycles), 0,
					"Expected problematic cycles but none found. %s", test.description)
			}
		})
	}
}

// Note: Function overload testing would require 'declare fn' syntax
// which may not be fully supported yet in Escalier's parser.
// Overload support will be tested through integration tests once
// the syntax is fully implemented.

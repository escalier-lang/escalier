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

	p := parser.NewParser(ctx, source)
	module, errors := p.ParseModule()
	if len(errors) > 0 {
		panic(errors[0])
	}
	return module
}

func TestFindCycles(t *testing.T) {
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseModule(test.input)
			depGraph := BuildDepGraph(module)
			cycles := depGraph.FindCycles()

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

func TestFindCycles_EdgeCases(t *testing.T) {
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
			depGraph := BuildDepGraph(module)
			cycles := depGraph.FindCycles()

			// Ensure no panics
			assert.NotPanics(t, func() {
				depGraph.FindCycles()
			}, "Cycle detection should not panic on edge cases")

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
				var allNames []string
				for _, declID := range cycle.Cycle {
					names := depGraph.getDeclNames(declID)
					allNames = append(allNames, names...)
				}
				t.Logf("Cycle %d: %v - %s", i+1, allNames, cycle.Message)
			}
		})
	}
}

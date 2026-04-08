package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inferModuleTypes parses the input source, runs type inference, and returns
// the inferred symbol-to-type map. Fails the test immediately on parse or
// inference errors.
func inferModuleTypes(t *testing.T, input string) map[string]string {
	t.Helper()

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

	if len(errors) > 0 {
		for i, err := range errors {
			t.Logf("Parse Error[%d]: %#v", i, err)
		}
	}
	require.Empty(t, errors)

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	inferErrors := c.InferModule(inferCtx, module)
	scope := inferCtx.Scope.Namespace

	if len(inferErrors) > 0 {
		for i, err := range inferErrors {
			t.Logf("Infer Error[%d]: %s", i, err.Message())
		}
	}
	require.Empty(t, inferErrors)

	actualTypes := make(map[string]string)
	for name, binding := range scope.Values {
		require.NotNil(t, binding)
		actualTypes[name] = binding.Type.String()
	}
	return actualTypes
}

func TestRowTypesPropertyAccess(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"ReadAccess": {
			input: `
				fn foo(obj) {
					return obj.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0",
			},
		},
		"MultipleReads": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.baz]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"WriteAccess": {
			input: `
				fn foo(obj) {
					obj.bar = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string, ...T0}) -> void",
			},
		},
		"ReadAndWrite": {
			input: `
				fn foo(obj) {
					val x = obj.bar
					obj.baz = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: number}) -> void",
			},
		},
		"NestedAccess": {
			input: `
				fn foo(obj) {
					return obj.foo.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {foo: {bar: T0, ...T1}, ...T2}) -> T0",
			},
		},
		"NestedWrite": {
			input: `
				fn foo(obj) {
					obj.foo.bar = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {foo: mut {bar: number, ...T0}, ...T1}) -> void",
			},
		},
		"MultipleParams": {
			input: `
				fn foo(a, b) {
					return [a.x, b.y]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(a: {x: T0, ...T1}, b: {y: T2, ...T3}) -> [T0, T2]",
			},
		},
		"DeeplyNested": {
			input: `
				fn foo(obj) {
					return obj.a.b.c
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(obj: {a: {b: {c: T0, ...T1}, ...T2}, ...T3}) -> T0",
			},
		},
		"NumericIndex": {
			input: `
				fn foo(obj) {
					return obj[0]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: Array<T0>) -> T0",
			},
		},
		"StringLiteralIndex": {
			input: `
				fn foo(obj) {
					return obj["bar"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0",
			},
		},
		"MultipleStringLiteralIndexes": {
			input: `
				fn foo(obj) {
					return [obj["bar"], obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"StringLiteralIndexWrite": {
			input: `
				fn foo(obj) {
					obj["bar"] = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string, ...T0}) -> void",
			},
		},
		"StringLiteralIndexReadAndWrite": {
			input: `
				fn foo(obj) {
					val x = obj["bar"]
					obj["baz"] = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: number}) -> void",
			},
		},
		"MixedDotAndBracketAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2>(obj: {bar: T0, ...T1, baz: T2}) -> [T0, T2]",
			},
		},
		"MixedDotReadBracketWrite": {
			input: `
				fn foo(obj) {
					val x = obj.bar
					obj["baz"] = 10
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: mut {bar: T0, ...T1, baz: number}) -> void",
			},
		},
		"MultipleNumericIndexes": {
			input: `
				fn foo(obj) {
					return [obj[0], obj[1]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: Array<T0>) -> [T0, T0]",
			},
		},
		"IdempotentPropertyAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.bar]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> [T0, T0]",
			},
		},
		"IdempotentMixedAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["bar"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> [T0, T0]",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

func TestRowTypesErrors(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedErrs []string
	}{
		"MutateAnnotatedImmutableParam": {
			input: `
				fn foo(obj: {bar: number}) {
					obj.bar = 5
				}
			`,
			expectedErrs: []string{"Cannot mutate immutable"},
		},
		"MutateAnnotatedImmutableParamIndex": {
			input: `
				fn foo(obj: {bar: number}) {
					obj["bar"] = 5
				}
			`,
			expectedErrs: []string{"Cannot mutate immutable"},
		},
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
					t.Logf("Parse Error[%d]: %#v", i, err)
				}
			}
			require.Empty(t, errors)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)

			require.Len(t, inferErrors, len(test.expectedErrs), "expected %d errors, got %d", len(test.expectedErrs), len(inferErrors))
			for i, expectedErr := range test.expectedErrs {
				if i < len(inferErrors) {
					assert.Contains(t, inferErrors[i].Message(), expectedErr)
				}
			}
		})
	}
}

// TestRowTypesKeyOf tests that keyof works on inferred open object types
// (which are wrapped in MutabilityType).
func TestRowTypesKeyOf(t *testing.T) {
	t.Run("KeyOfType unwraps MutabilityType", func(t *testing.T) {
		checker := NewChecker()
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Simulate an inferred open object wrapped in MutabilityType:
		// mut? {x: string, y: number}
		objType := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("x"), type_system.NewStrPrimType(nil)),
			type_system.NewPropertyElem(type_system.NewStrKey("y"), type_system.NewNumPrimType(nil)),
		})
		objType.Open = true
		mutType := &type_system.MutabilityType{
			Type:       objType,
			Mutability: type_system.MutabilityUncertain,
		}

		keyofType := type_system.NewKeyOfType(nil, mutType)
		result, errors := checker.ExpandType(ctx, keyofType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"x" | "y"`, result.String())
	})

	t.Run("KeyOfType on bare ObjectType still works", func(t *testing.T) {
		checker := NewChecker()
		ctx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		objType := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("a"), type_system.NewNumPrimType(nil)),
		})

		keyofType := type_system.NewKeyOfType(nil, objType)
		result, errors := checker.ExpandType(ctx, keyofType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"a"`, result.String())
	})
}

// TestRowTypesIntersectionAccess tests that getIntersectionAccess unwraps
// MutabilityType wrappers when classifying intersection parts.
func TestRowTypesIntersectionAccess(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"IntersectionWithInferredObject": {
			input: `
				fn foo(obj: {x: number} & {y: string}) {
					return [obj.x, obj.y]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: {x: number} & {y: string}) -> [number, string]",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

// TestRowTypesPassToTypedFunction tests Phase 3: passing inferred open-typed
// parameters to functions with typed parameters unifies correctly.
func TestRowTypesPassToTypedFunction(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"PassToTypedFunction": {
			input: `
				fn bar(x: {bar: string}) -> string { return x.bar }
				fn foo(obj) { bar(obj) }
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: {bar: string}) -> string",
				"foo": "fn <T0>(obj: {bar: string, ...T0}) -> void",
			},
		},
		"PropertiesSurviveFunctionCall": {
			input: `
				fn bar(x: {bar: string}) -> string { return x.bar }
				fn foo(obj) {
					obj.z = true
					bar(obj)
					obj.w = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {z: boolean, ...T0, bar: string, w: string}) -> void",
			},
		},
		"MultipleCallsMerge": {
			input: `
				fn bar(x: {x: number}) -> number { return x.x }
				fn baz(y: {y: string}) -> string { return y.y }
				fn foo(obj) {
					bar(obj)
					baz(obj)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {x: number, ...T0, y: string}) -> void",
			},
		},
		"NonObjectBinding": {
			input: `
				fn takes_num(x: number) -> number { return x }
				fn foo(obj) { takes_num(obj) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: number) -> void",
			},
		},
		"MultipleParameters": {
			input: `
				fn bar(x: {a: number}) -> number { return x.a }
				fn baz(y: {b: string}) -> string { return y.b }
				fn foo(a, b) {
					bar(a)
					baz(b)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(a: {a: number, ...T0}, b: {b: string, ...T1}) -> void",
			},
		},
		"OpenVsClosedSharedProperty": {
			input: `
				fn bar(x: {name: string}) -> string { return x.name }
				fn foo(obj) {
					obj.name = "hi"
					bar(obj)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {name: string, ...T0}) -> void",
			},
		},
		"OpenVsClosedExtraPropertiesInOpen": {
			input: `
				fn bar(x: {a: number}) -> number { return x.a }
				fn foo(obj) {
					obj.a = 1
					obj.b = "hi"
					bar(obj)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {a: number, ...T0, b: string}) -> void",
			},
		},
		"Aliasing": {
			input: `
				fn foo(obj) {
					val alias = obj
					alias.x = 1
					alias.y = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, ...T0, y: string}) -> void",
			},
		},
		// TODO: `val alias = obj` binds tvObj.Instance = tvAlias, making
		// tvAlias the representative. Since IsParam is on tvObj (now an
		// intermediate node), openClosedObjectForParam is never called
		// when bar(alias) unifies. Fix by propagating IsParam when binding
		// two TypeVars in bind(), similar to how constraints are propagated.
		// This should be addressed in Phase 6 or Phase 7.
		// "AliasingThroughTypedCall": {
		// 	input: `
		// 		fn bar(x: {a: number}) -> number { return x.a }
		// 		fn foo(obj) {
		// 			val alias = obj
		// 			bar(alias)
		// 			alias.x = 1
		// 			alias.y = "hello"
		// 		}
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"foo": "fn <T0>(obj: mut {a: number, ...T0, x: number, y: string}) -> void",
		// 	},
		// },
		"PassToMutableTypedFunction": {
			input: `
				fn bar(x: mut {a: number}) -> number { return x.a }
				fn foo(obj) {
					bar(obj)
					obj.b = "hi"
				}
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: mut {a: number}) -> number",
				"foo": "fn <T0>(obj: mut {a: number, ...T0, b: string}) -> void",
			},
		},
		"PassToMutableTypedFunctionNoLocalWrite": {
			// GeneralizeFuncType determines mutability from actual writes, not
			// from callee requirements, so foo's param is not mut here.
			input: `
				fn bar(x: mut {a: number}) -> number { return x.a }
				fn foo(obj) { bar(obj) }
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: mut {a: number}) -> number",
				"foo": "fn <T0>(obj: {a: number, ...T0}) -> void",
			},
		},
		"OpenVsOpenViaFunctionCall": {
			input: `
				fn bar(x) -> number { return x.a }
				fn foo(obj) {
					obj.b = "hi"
					bar(obj)
				}
			`,
			expectedTypes: map[string]string{
				"bar": "fn <T0>(x: {a: number, ...T0}) -> number",
				"foo": "fn <T0>(obj: mut {b: string, ...T0, a: number}) -> void",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

// TestRowTypesWriteAfterPass tests that writing to a property after passing
// the parameter to a typed function does not corrupt the callee's type.
// This exercises the openClosedObjectForParam path followed by markPropertyWritten.
func TestRowTypesWriteAfterPass(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"WriteAfterPass": {
			input: `
				fn bar(x: {name: string}) -> string { return x.name }
				fn foo(obj) {
					bar(obj)
					obj.name = "hi"
				}
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: {name: string}) -> string",
				// bar's annotation provides the concrete type; "hi" is compatible
				"foo": "fn <T0>(obj: mut {name: string, ...T0}) -> void",
			},
		},
		"WriteNewPropertyAfterPass": {
			input: `
				fn bar(x: {a: number}) -> number { return x.a }
				fn foo(obj) {
					bar(obj)
					obj.b = "hi"
				}
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: {a: number}) -> number",
				"foo": "fn <T0>(obj: mut {a: number, ...T0, b: string}) -> void",
			},
		},
		"WrittenFlagDoesNotLeakAcrossFunctions": {
			// foo writes to name after passing obj to bar. The Written flag must
			// not leak through bar's shared PropertyElem to baz, which only reads.
			// baz depends on foo (via `val _ = foo(obj)`) to force processing
			// order: foo is inferred before baz.
			input: `
				fn bar(x: {name: string}) -> string { return x.name }
				fn foo(obj) {
					bar(obj)
					obj.name = "hi"
				}
				fn baz(a, b) {
					foo(a)
					bar(b)
				}
			`,
			expectedTypes: map[string]string{
				"bar": "fn (x: {name: string}) -> string",
				"foo": "fn <T0>(obj: mut {name: string, ...T0}) -> void",
				// baz's second param must NOT be mut — baz never writes to b
				"baz": "fn <T0, T1>(a: mut {name: string, ...T0}, b: {name: string, ...T1}) -> void",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

// TestRowTypesStringLiteralIndexAfterExtends tests that string-literal index
// access on open objects checks Extends before adding a new property.
func TestRowTypesStringLiteralIndexAfterExtends(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"StringLiteralIndexFindsInheritedProperty": {
			input: `
				type Base = {name: string}
				fn foo(obj: Base) {
					return obj["name"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: Base) -> string",
			},
		},
		"StringLiteralIndexOnOpenObjectAddsProperty": {
			input: `
				fn foo(obj) {
					return obj["bar"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1}) -> T0",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

// TestRowTypesPropertyWidening tests Phase 4: literal widening to primitives,
// same-kind literal deduplication, and different-kind union accumulation.
func TestRowTypesPropertyWidening(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
		expectedError string // when expectedTypes is nil, check that at least one error contains this substring
	}{
		"LiteralWideningString": {
			input: `
				fn foo(obj) { obj.bar = "hello" }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string, ...T0}) -> void",
			},
		},
		"LiteralWideningNumber": {
			input: `
				fn foo(obj) { obj.bar = 42 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: number, ...T0}) -> void",
			},
		},
		"LiteralWideningBoolean": {
			input: `
				fn foo(obj) { obj.bar = true }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: boolean, ...T0}) -> void",
			},
		},
		"SameKindLiteralsCollapse": {
			input: `
				fn foo(obj) {
					obj.bar = "hello"
					obj.bar = "world"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string, ...T0}) -> void",
			},
		},
		"DifferentKindLiteralsProduceUnion": {
			input: `
				fn foo(obj) {
					obj.bar = "hello"
					obj.bar = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string | number, ...T0}) -> void",
			},
		},
		"ThreeWayWidening": {
			input: `
				fn foo(obj) {
					obj.bar = "a"
					obj.bar = 1
					obj.bar = true
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string | number | boolean, ...T0}) -> void",
			},
		},
		"BranchWidening": {
			input: `
				fn foo(obj, cond) {
					if cond { obj.bar = "hello" } else { obj.bar = 5 }
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string | number, ...T0}, cond: boolean) -> void",
			},
		},
		"NonLiteralTypesNotWidened": {
			input: `
				fn foo(obj, s: string) { obj.bar = s }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: string, ...T0}, s: string) -> void",
			},
		},
		"DeepWidenObjectLiteral": {
			input: `
				fn foo(obj) {
					obj.loc = {x: 0, y: 0}
					obj.col = "red"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {loc: {x: number, y: number}, ...T0, col: string}) -> void",
			},
		},
		"DeepWidenNestedLiterals": {
			input: `
				fn foo(obj) {
					obj.prop = {a: {b: {c: "hello", d: 5}}}
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {prop: {a: {b: {c: string, d: number}}}, ...T0}) -> void",
			},
		},
		"DeepWidenTupleLiterals": {
			input: `
				fn foo(obj) {
					obj.pair = [1, "hello"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {pair: [number, string], ...T0}) -> void",
			},
		},
		"DeepWidenNestedTupleInObject": {
			input: `
				fn foo(obj) {
					obj.data = {coords: [1, 2], label: "hi"}
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {data: {coords: [number, number], label: string}, ...T0}) -> void",
			},
		},
		"NormalTypeVarConflictStillErrors": {
			input: `
				val x: number = "hello"
			`,
			expectedTypes: nil,
			expectedError: `"hello" cannot be assigned to number`,
		},
		"ReadWidenedPropertyIntoNarrowType": {
			// After widening bar to string | number, reading it into a string
			// variable must produce a type error.
			input: `
				fn foo(obj) {
					obj.bar = "x"
					obj.bar = 1
					val s: string = obj.bar
				}
			`,
			expectedTypes: nil,
			expectedError: "cannot be assigned to string",
		},
		"ReadWidenedPropertyIntoDifferentType": {
			// After widening bar to string, reading it into a boolean variable
			// must produce a type error — not silently widen bar to string | boolean.
			input: `
				fn foo(obj) {
					obj.bar = "x"
					val b: boolean = obj.bar
				}
			`,
			expectedTypes: nil,
			expectedError: "cannot be assigned to boolean",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if test.expectedTypes == nil {
				source := &ast.Source{
					ID:       0,
					Path:     "input.esc",
					Contents: test.input,
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
				require.Empty(t, errors)

				c := NewChecker()
				inferCtx := Context{
					Scope:      Prelude(c),
					IsAsync:    false,
					IsPatMatch: false,
				}
				inferErrors := c.InferModule(inferCtx, module)
				require.NotEmpty(t, inferErrors, "Expected type error")
				if test.expectedError != "" {
					found := false
					for _, e := range inferErrors {
						if strings.Contains(e.Message(), test.expectedError) {
							found = true
							break
						}
					}
					assert.True(t, found, "Expected error containing %q, got: %v", test.expectedError, inferErrors)
				}
				return
			}

			actualTypes := inferModuleTypes(t, test.input)
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				require.True(t, exists, "Expected variable %s to be declared", expectedName)
				assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
			}
		})
	}
}

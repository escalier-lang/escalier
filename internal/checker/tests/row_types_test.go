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

// inferModuleTypesAndErrors parses the input source, runs type inference, and
// returns the inferred symbol-to-type map along with any inference errors.
// Parsing must succeed (parse errors cause a test failure via require.Empty).
// Use this helper for tests that expect successful parsing but want to inspect
// inference errors. For tests that require both parsing and inference to
// succeed with no errors, use inferModuleTypes instead.
func inferModuleTypesAndErrors(t *testing.T, input string) (map[string]string, []Error) {
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

	c := NewChecker(ctx)
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	inferErrors := c.InferModule(inferCtx, module)
	scope := inferCtx.Scope.Namespace

	actualTypes := make(map[string]string)
	for name, binding := range scope.Values {
		require.NotNil(t, binding)
		actualTypes[name] = binding.Type.String()
	}
	return actualTypes, inferErrors
}

// inferModuleTypes parses the input source, runs type inference, and returns
// the inferred symbol-to-type map. Fails the test immediately on parse or
// inference errors.
func inferModuleTypes(t *testing.T, input string) map[string]string {
	t.Helper()

	actualTypes, inferErrors := inferModuleTypesAndErrors(t, input)

	if len(inferErrors) > 0 {
		for i, err := range inferErrors {
			t.Logf("Infer Error[%d]: %s", i, err.Message())
		}
	}
	require.Empty(t, inferErrors)

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
				"foo": "fn <T0>(obj: {bar: T0}) -> T0",
			},
		},
		"MultipleReads": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.baz]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]",
			},
		},
		"WriteAccess": {
			input: `
				fn foo(obj) {
					obj.bar = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: string}) -> void",
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
				"foo": "fn <T0>(obj: mut {bar: T0, baz: number}) -> void",
			},
		},
		"NestedAccess": {
			input: `
				fn foo(obj) {
					return obj.foo.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {foo: {bar: T0}}) -> T0",
			},
		},
		"NestedWrite": {
			input: `
				fn foo(obj) {
					obj.foo.bar = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {foo: mut {bar: number}}) -> void",
			},
		},
		"MultipleParams": {
			input: `
				fn foo(a, b) {
					return [a.x, b.y]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(a: {x: T0}, b: {y: T1}) -> [T0, T1]",
			},
		},
		"DeeplyNested": {
			input: `
				fn foo(obj) {
					return obj.a.b.c
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {a: {b: {c: T0}}}) -> T0",
			},
		},
		"NumericIndex": {
			// With Phase 12, a single literal index infers a tuple.
			input: `
				fn foo(obj) {
					return obj[0]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: [T0]) -> T0",
			},
		},
		"StringLiteralIndex": {
			input: `
				fn foo(obj) {
					return obj["bar"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0}) -> T0",
			},
		},
		"MultipleStringLiteralIndexes": {
			input: `
				fn foo(obj) {
					return [obj["bar"], obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]",
			},
		},
		"StringLiteralIndexWrite": {
			input: `
				fn foo(obj) {
					obj["bar"] = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: string}) -> void",
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
				"foo": "fn <T0>(obj: mut {bar: T0, baz: number}) -> void",
			},
		},
		"MixedDotAndBracketAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["baz"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]",
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
				"foo": "fn <T0>(obj: mut {bar: T0, baz: number}) -> void",
			},
		},
		"MultipleNumericIndexes": {
			// With Phase 12, literal indexes infer a tuple, not an array.
			input: `
				fn foo(obj) {
					return [obj[0], obj[1]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: [T0, T1]) -> [T0, T1]",
			},
		},
		"IdempotentPropertyAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj.bar]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0}) -> [T0, T0]",
			},
		},
		"IdempotentMixedAccess": {
			input: `
				fn foo(obj) {
					return [obj.bar, obj["bar"]]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0}) -> [T0, T0]",
			},
		},
		"MixedStringAndNumericKeys": {
			input: `
				fn foo(obj) {
					val x = obj.bar
					return obj[0]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, 0: T1}) -> T1",
			},
		},
		"NumericIndexOnReopenedObject": {
			input: `
				fn bar(obj: {x: number}) -> number { return obj.x }
				fn foo(v) {
					bar(v)
					return v[0]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(v: {x: number, 0: T0}) -> T0",
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
		// Section 9a: missing property at call site mentions inferred provenance
		"MissingInferredProperty": {
			input: `
				fn foo(obj) {
					return obj.bar
				}
				fn main() {
					foo({baz: 5})
				}
			`,
			expectedErrs: []string{"is required because it is accessed at"},
		},
		// Section 9c: property type mismatch at call site between
		// inferred parameter and argument with wrong property type.
		// The inferred property type widens to absorb the conflicting
		// type, so no error occurs. This is expected behavior — the
		// PropertyTypeMismatchError enhancement fires when two closed
		// objects are unified directly (not through widenable type vars).
		// See also: TestRowTypesPassToTypedFunction for related tests.
		"ChainedDestructuringWithRestAndSpread": {
			// Rest from object destructuring spread into a tuple produces
			// a RestSpreadType wrapping a non-iterable object — see #411.
			input: `
				val foo = fn ({x, ...rest}) {
					return [x, ...rest]
				}
				val r = foo({x: 1, y: 2})
			`,
			expectedErrs: []string{"Object rest type is not iterable"},
		},
		"AliasedObjectRestSpreadIntoTuple": {
			// When the object rest is aliased via val before being spread,
			// bind() must propagate IsObjectRest to the new representative
			// TypeVar so the tuple spread check still catches it.
			input: `
				val foo = fn ({x, ...rest}) {
					val alias = rest
					return [x, ...alias]
				}
				val r = foo({x: 1, y: 2})
			`,
			expectedErrs: []string{"Object rest type is not iterable"},
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

			c := NewChecker(ctx)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		checker := NewChecker(ctx)
		inferCtx := Context{
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
		result, errors := checker.ExpandType(inferCtx, keyofType, 1)

		assert.Empty(t, errors)
		assert.Equal(t, `"x" | "y"`, result.String())
	})

	t.Run("KeyOfType on bare ObjectType still works", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		checker := NewChecker(ctx)
		inferCtx := Context{
			Scope:      NewScope(),
			IsAsync:    false,
			IsPatMatch: false,
		}

		objType := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(type_system.NewStrKey("a"), type_system.NewNumPrimType(nil)),
		})

		keyofType := type_system.NewKeyOfType(nil, objType)
		result, errors := checker.ExpandType(inferCtx, keyofType, 1)

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
				"foo": "fn (obj: {bar: string}) -> void",
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
				"foo": "fn (obj: mut {z: boolean, bar: string, w: string}) -> void",
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
				"foo": "fn (obj: {x: number, y: string}) -> void",
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
				"foo": "fn (a: {a: number}, b: {b: string}) -> void",
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
				"foo": "fn (obj: mut {name: string}) -> void",
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
				"foo": "fn (obj: mut {a: number, b: string}) -> void",
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
				"foo": "fn (obj: mut {x: number, y: string}) -> void",
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
				"foo": "fn (obj: mut {a: number, b: string}) -> void",
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
				"foo": "fn (obj: {a: number}) -> void",
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
				"bar": "fn (x: {a: number}) -> number",
				"foo": "fn (obj: mut {b: string, a: number}) -> void",
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
				"foo": "fn (obj: mut {name: string}) -> void",
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
				"foo": "fn (obj: mut {a: number, b: string}) -> void",
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
				"foo": "fn (obj: mut {name: string}) -> void",
				// Neither param is mut — baz never writes to a or b directly
				"baz": "fn (a: {name: string}, b: {name: string}) -> void",
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
				"foo": "fn <T0>(obj: {bar: T0}) -> T0",
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

// TestRowTypesMethodCallInference tests Phase 5: calling a method on an
// inferred object creates a FuncType binding with appropriate parameters and
// return type. Multiple calls with different arg types produce an intersection
// (overloaded signature) rather than widening to a union — this preserves
// per-call-site return type precision.
func TestRowTypesMethodCallInference(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicMethodCall": {
			input: `
				fn foo(obj) { val r = obj.process(42, "hello") }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {process: fn (arg0: 42, arg1: \"hello\") -> T0}) -> void",
			},
		},
		"MethodParameterIntersection": {
			input: `
				fn foo(obj) {
					obj.process(42)
					obj.process("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {process: (fn (arg0: 42) -> T0) & (fn (arg0: \"hello\") -> T1)}) -> void",
			},
		},
		"MethodReturnTypeIntersection": {
			input: `
				fn foo(obj) {
					val x: number = obj.getValue()
					val y: string = obj.getValue()
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: {getValue: (fn () -> number) & (fn () -> string)}) -> void",
			},
		},
		"MethodAndPropertyOnSameObject": {
			input: `
				fn foo(obj) {
					obj.x = 1
					val r = obj.process(obj.x)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, process: fn (arg0: number) -> T0}) -> void",
			},
		},
		"ZeroArgMethod": {
			input: `
				fn foo(obj) { val r = obj.getData() }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {getData: fn () -> T0}) -> void",
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
				"foo": "fn (obj: mut {bar: string}) -> void",
			},
		},
		"LiteralWideningNumber": {
			input: `
				fn foo(obj) { obj.bar = 42 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: number}) -> void",
			},
		},
		"LiteralWideningBoolean": {
			input: `
				fn foo(obj) { obj.bar = true }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: boolean}) -> void",
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
				"foo": "fn (obj: mut {bar: string}) -> void",
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
				"foo": "fn (obj: mut {bar: string | number}) -> void",
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
				"foo": "fn (obj: mut {bar: string | number | boolean}) -> void",
			},
		},
		"BranchWidening": {
			input: `
				fn foo(obj, cond) {
					if cond { obj.bar = "hello" } else { obj.bar = 5 }
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: string | number}, cond: boolean) -> void",
			},
		},
		"NonLiteralTypesNotWidened": {
			input: `
				fn foo(obj, s: string) { obj.bar = s }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: string}, s: string) -> void",
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
				"foo": "fn (obj: mut {loc: {x: number, y: number}, col: string}) -> void",
			},
		},
		"DeepWidenNestedLiterals": {
			input: `
				fn foo(obj) {
					obj.prop = {a: {b: {c: "hello", d: 5}}}
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {prop: {a: {b: {c: string, d: number}}}}) -> void",
			},
		},
		"DeepWidenTupleLiterals": {
			input: `
				fn foo(obj) {
					obj.pair = [1, "hello"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {pair: [number, string]}) -> void",
			},
		},
		"DeepWidenNestedTupleInObject": {
			input: `
				fn foo(obj) {
					obj.data = {coords: [1, 2], label: "hi"}
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {data: {coords: [number, number], label: string}}) -> void",
			},
		},
		"DeepWidenMethodGetterSetter": {
			input: `
				fn foo(obj) {
					obj.config = {
						_x: 0,
						getValue(self) { return self._x },
						get x(self) { return self._x },
						set x(mut self, v) { self._x = v },
					}
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {config: {_x: number, getValue(self) -> number, get x(self) -> number, set x(mut self, v: number) -> undefined}}) -> void",
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

				c := NewChecker(ctx)
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

// TestRowTypesClosing tests Phase 6: after a function body is fully inferred,
// open object types on parameters are closed. RestSpreadElems whose row
// variables don't escape to the return type are removed. Mutability is resolved
// based on whether properties were written to.
func TestRowTypesClosing(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"ClosedWithMut": {
			// Writes make the param mut; no return means row var removed
			input: `
				fn foo(obj) { obj.bar = 5 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {bar: number}) -> void",
			},
		},
		"ClosedWithoutMut": {
			// Read-only: no mut wrapper, row var removed
			input: `
				fn foo(obj) { val x = obj.bar }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0}) -> void",
			},
		},
		"MixedReadsAndWrites": {
			// Any write makes the whole object mut
			input: `
				fn foo(obj) {
					val x = obj.bar
					obj.baz = 5
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {bar: T0, baz: number}) -> void",
			},
		},
		"RestSpreadPreservedWhenInReturnType": {
			// When the object is returned, the row var escapes and is kept
			input: `
				fn foo(obj) {
					obj.x = 1
					return obj
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}",
			},
		},
		"MultipleParamsClosedIndependently": {
			// Each param is closed independently
			input: `
				fn foo(a, b) {
					a.x = 1
					b.y = "hi"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (a: mut {x: number}, b: mut {y: string}) -> void",
			},
		},
		"NestedFunctionClosedIndependently": {
			// Each function's params are closed after its own body
			input: `
				fn outer(a) {
					val inner = fn (b) { b.x = 1 }
					a.y = "hi"
					return inner
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn (a: mut {y: string}) -> fn (b: mut {x: number}) -> void",
			},
		},
		"ArrayElementWriteAccess": {
			// With Phase 12, a single literal index resolves to a tuple.
			// The nested open object is still closed.
			input: `
				fn foo(arr) { arr[0].x = 1 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (arr: [mut {x: number}]) -> void",
			},
		},
		"ArrayElementReadAccess": {
			// With Phase 12, a single literal index resolves to a tuple.
			// The nested open object is still closed.
			input: `
				fn foo(arr) { return arr[0].bar }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(arr: [{bar: T0}]) -> T0",
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

func TestRowTypesRowPolymorphism(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicRowPolymorphism": {
			// Extra properties passed by caller are preserved in the result
			input: `
				fn foo(obj) {
					obj.x = 1
					return obj
				}
				val r = foo({x: 1, y: 2})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}",
				"r":   "{x: number, y: 2}",
			},
		},
		"MultipleExtraProperties": {
			// Multiple extra properties are preserved
			input: `
				fn foo(obj) {
					obj.x = 1
					return obj
				}
				val r = foo({x: 1, y: 2, z: "hi"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}",
				"r":   "{x: number, y: 2, z: \"hi\"}",
			},
		},
		"NoReturn_RowVarRemoved": {
			// No return means row variable is removed
			input: `
				fn foo(obj) { obj.x = 1 }
				val r = foo({x: 1, y: 2})
			`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: mut {x: number}) -> void",
				"r":   "void",
			},
		},
		"DerivedReturn_RowVarDoesNotEscape": {
			// Return type is the type of a property, not the full object.
			// Literal types are preserved for regular type params.
			input: `
				fn foo(obj) { return obj.x }
				val r = foo({x: 5, y: "hi"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {x: T0}) -> T0",
				"r":   "5",
			},
		},
		"ReturnInStructure_RowVarDoesNotEscape": {
			// Row variable doesn't escape when returning a derived value.
			// Literal types are preserved for regular type params.
			input: `
				fn foo(obj) { return {y: obj.x} }
				val r = foo({x: 5, extra: "hi"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {x: T0}) -> {y: T0}",
				"r":   "{y: 5}",
			},
		},
		"ReadOnlyRowPolymorphism": {
			// Read-only access with return preserves extra properties
			input: `
				fn foo(obj) {
					val x = obj.x
					return obj
				}
				val r = foo({x: 1, y: "hello"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {x: T0, ...T1}) -> {x: T0, ...T1}",
				"r":   "{x: 1, y: \"hello\"}",
			},
		},
		"MultipleParamsRowPolymorphism": {
			// Each parameter gets its own row variable
			input: `
				fn foo(a, b) {
					a.x = 1
					b.y = "hi"
					return [a, b]
				}
				val r = foo({x: 0, extra1: true}, {y: "", extra2: 42})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(a: mut {x: number, ...T0}, b: mut {y: string, ...T1}) -> [{x: number, ...T0}, {y: string, ...T1}]",
				"r":   "[{x: number, extra1: true}, {y: string, extra2: 42}]",
			},
		},
		"NoExtraProperties": {
			// Calling with exact properties — row variable resolves to empty
			input: `
				fn foo(obj) {
					obj.x = 1
					return obj
				}
				val r = foo({x: 5})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: mut {x: number, ...T0}) -> {x: number, ...T0}",
				"r":   "{x: number}",
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

func TestVariadicTupleTypes(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"FixedVsVariadic_TrailingRest": {
			// [number, string, boolean] vs [number, ...R]
			// → R = [string, boolean]
			input: `
				fn foo<T>(items: [number, ...T]) { return items }
				val r = foo([1, "a", true])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T>(items: [number, ...T]) -> [number, ...T]",
				"r":   "[number, \"a\", true]",
			},
		},
		"FixedVsVariadic_AllAbsorbed": {
			// [1, "a", "b"] vs [number, ...Array<string>]
			input: `
				val x: [number, ...Array<string>] = [1, "a", "b"]
			`,
			expectedTypes: map[string]string{
				"x": "[number, ...Array<string>]",
			},
		},
		"VariadicVsVariadic_SamePrefix": {
			// Both have trailing rest with same prefix length
			input: `
				fn foo<R1, R2>(a: [number, ...R1], b: [string, ...R2]) {
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <R1, R2>(a: [number, ...R1], b: [string, ...R2]) -> [[number, ...R1], [string, ...R2]]",
			},
		},
		"Generalization_VariadicRest": {
			// fn foo<T>(items: [number, ...T]) { return items }
			// → type: fn <T>(items: [number, ...T]) -> [number, ...T]
			input: `
				fn foo<T>(items: [number, ...T]) { return items }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T>(items: [number, ...T]) -> [number, ...T]",
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

func TestVariadicVsFixed(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"VariadicSourceAssignedToShorterTarget": {
			// Source [number, string, ...T] has 2 mandatory prefix elements.
			// Target [x] only needs 1. The extra source elements are ignored.
			input: `
				fn foo<T>(items: [number, string, ...T]) -> [number, string, ...T] {
					return items
				}
				val [x] = foo([1, "a", true])
			`,
			expectedTypes: map[string]string{
				"x": "number",
			},
		},
		"VariadicSourceRestAbsorbsTargetElements": {
			// Source [number, ...T] with target [number, string].
			// Prefix: number↔number. Rest T absorbs [string].
			input: `
				fn foo<T>(items: [number, ...T]) -> [number, ...T] {
					return items
				}
				val [a, b] = foo([1, "hello"])
			`,
			expectedTypes: map[string]string{
				"a": "number",
				"b": "\"hello\"",
			},
		},
		"VariadicSourceRestAbsorbsNothing": {
			// Source [number, ...T] with target [number].
			// Prefix: number↔number. Rest absorbs nothing (empty tuple).
			input: `
				fn foo<T>(items: [number, ...T]) -> [number, ...T] {
					return items
				}
				val [a] = foo([1])
			`,
			expectedTypes: map[string]string{
				"a": "number",
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

func TestVariadicTupleSubtyping(t *testing.T) {
	t.Run("VariadicTupleAssignableToArray", func(t *testing.T) {
		t.Parallel()
		// [number, ...string[]] should be assignable to Array<number | string>
		actualTypes := inferModuleTypes(t, `
			val x: [number, ...Array<string>] = [1, "a", "b"]
			val y: Array<number | string> = x
		`)
		assert.Equal(t, "[number, ...Array<string>]", actualTypes["x"])
		assert.Equal(t, "Array<number | string>", actualTypes["y"])
	})

	t.Run("SingleElementConformsToVariadicTuple", func(t *testing.T) {
		t.Parallel()
		// [5] should conform to [number, ...Array<string>] because the
		// prefix `number` matches and ...Array<string> accepts zero elements.
		actualTypes := inferModuleTypes(t, `
			val x: [number, ...Array<string>] = [5]
		`)
		assert.Equal(t, "[number, ...Array<string>]", actualTypes["x"])
	})

	t.Run("IncompatibleElementsRejectAssignment", func(t *testing.T) {
		t.Parallel()
		// [1, 2, 3] should NOT conform to [number, ...Array<string>] because
		// 2 and 3 are numbers, not strings.
		_, inferErrors := inferModuleTypesAndErrors(t, `
			val x: [number, ...Array<string>] = [1, 2, 3]
		`)
		require.NotEmpty(t, inferErrors)
		// Optionally verify the error relates to type incompatibility
		assert.Contains(t, inferErrors[0].Message(), "cannot be assigned")
	})

	t.Run("TooFewElementsForVariadicTarget", func(t *testing.T) {
		t.Parallel()
		// [] cannot conform to [number, string, ...Array<boolean>] because
		// the target requires at least 2 elements (the prefix) and the
		// source provides 0.
		_, inferErrors := inferModuleTypesAndErrors(t, `
			val x: [number, string, ...Array<boolean>] = []
		`)
		require.NotEmpty(t, inferErrors)
	})

	t.Run("LeadingRest", func(t *testing.T) {
		t.Parallel()
		// [...number[], string] — the rest absorbs leading elements,
		// the last element must be string.
		actualTypes := inferModuleTypes(t, `
			val x: [...Array<number>, string] = [1, 2, "hello"]
		`)
		assert.Equal(t, "[...Array<number>, string]", actualTypes["x"])
	})

	t.Run("LeadingRestZeroAbsorbed", func(t *testing.T) {
		t.Parallel()
		// [...number[], string] with only the suffix element present.
		actualTypes := inferModuleTypes(t, `
			val x: [...Array<number>, string] = ["hello"]
		`)
		assert.Equal(t, "[...Array<number>, string]", actualTypes["x"])
	})

	t.Run("LeadingRestIncompatibleSuffix", func(t *testing.T) {
		t.Parallel()
		// [...number[], string] — the last element must be string, not number.
		_, inferErrors := inferModuleTypesAndErrors(t, `
			val x: [...Array<number>, string] = [1, 2, 3]
		`)
		require.NotEmpty(t, inferErrors)
	})

	t.Run("FixedOnBothSidesOfRest", func(t *testing.T) {
		t.Parallel()
		// [number, ...boolean[], string] — first must be number, last must
		// be string, middle elements absorbed by ...boolean[].
		actualTypes := inferModuleTypes(t, `
			val x: [number, ...Array<boolean>, string] = [1, true, false, "end"]
		`)
		assert.Equal(t, "[number, ...Array<boolean>, string]", actualTypes["x"])
	})

	t.Run("FixedOnBothSidesRestAbsorbsNothing", func(t *testing.T) {
		t.Parallel()
		// [number, ...boolean[], string] with no middle elements — the rest
		// absorbs nothing.
		actualTypes := inferModuleTypes(t, `
			val x: [number, ...Array<boolean>, string] = [1, "end"]
		`)
		assert.Equal(t, "[number, ...Array<boolean>, string]", actualTypes["x"])
	})

	t.Run("FixedOnBothSidesTooFewElements", func(t *testing.T) {
		t.Parallel()
		// [number, ...boolean[], string] requires at least 2 elements (the
		// prefix and suffix). A single-element source doesn't have enough.
		_, inferErrors := inferModuleTypesAndErrors(t, `
			val x: [number, ...Array<boolean>, string] = [1]
		`)
		require.NotEmpty(t, inferErrors)
	})

	t.Run("FixedOnBothSidesIncompatibleMiddle", func(t *testing.T) {
		t.Parallel()
		// [number, ...boolean[], string] — middle elements must be boolean.
		_, inferErrors := inferModuleTypesAndErrors(t, `
			val x: [number, ...Array<boolean>, string] = [1, "not bool", "end"]
		`)
		require.NotEmpty(t, inferErrors)
	})
}

func TestVariadicTupleIndexing(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"IndexWithinFixedPrefix": {
			// Indexing within the fixed prefix returns the element's type.
			input: `
				fn foo<T>(items: [number, string, ...T]) {
					val a = items[0]
					val b = items[1]
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T>(items: [number, string, ...T]) -> [number, string]",
			},
		},
		"IndexBeyondFixedPrefix": {
			// Indexing beyond the fixed prefix returns the rest spread's
			// element type (not a union of all element types).
			input: `
				fn foo(items: [number, ...Array<string>]) {
					val a = items[0]
					val b = items[2]
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, ...Array<string>]) -> [number, string]",
			},
		},
		"MethodAccessOnVariadicTuple": {
			// Method access resolves via Array<union of all element types>.
			input: `
				fn foo(items: [number, ...Array<string>]) {
					return items.length
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, ...Array<string>]) -> number",
			},
		},
		"IndexIntoResolvedTupleRest": {
			// When the rest resolves to a concrete tuple, indexing beyond
			// the fixed prefix returns the exact element type at that
			// offset within the rest tuple.
			input: `
				fn foo<T>(items: [number, ...T]) -> T { return items }
				val r = foo([1, "a", true])
				val a = r[0]
				val b = r[1]
			`,
			expectedTypes: map[string]string{
				"a": "\"a\"",
				"b": "true",
			},
		},
		"IndexIntoConcreteSpreadTuple": {
			// [number, ...[string, boolean]] — the spread is a concrete
			// tuple. Indexing should see the flattened view:
			// index 0 → number, index 1 → string, index 2 → boolean.
			input: `
				val items: [number, ...[string, boolean]] = [1, "a", true]
				val a = items[0]
				val b = items[1]
				val c = items[2]
			`,
			expectedTypes: map[string]string{
				"items": "[number, string, boolean]",
				"a":     "number",
				"b":     "string",
				"c":     "boolean",
			},
		},
		"IndexIntoNestedVariadicTuple": {
			// [number, ...[string, ...Array<boolean>]] — the spread is a
			// variadic tuple. Index 0 → number, index 1 → string (from the
			// nested fixed prefix), index 2+ → boolean (from the nested rest).
			input: `
				val items: [number, ...[string, ...Array<boolean>]] = [1, "a", true, false]
				val a = items[0]
				val b = items[1]
				val c = items[2]
				val d = items[3]
			`,
			expectedTypes: map[string]string{
				"a": "number",
				"b": "string",
				"c": "boolean",
				"d": "boolean",
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

func TestTupleArrayInference(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"TupleFromLiteralIndexes": {
			input: `
				fn foo(items) { return [items[0], items[1]] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, T1]) -> [T0, T1]",
			},
		},
		"TupleWithLength": {
			input: `
				fn foo(items) {
					val a = items[0]
					val l = items.length
					return [a, l]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: [T0]) -> [T0, number]",
			},
		},
		"ArrayFromNonLiteralIndex": {
			input: `
				fn foo(items, i: number) { return items[i] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: Array<T0>, i: number) -> T0",
			},
		},
		"MutTupleFromIndexAssignment": {
			// Index assignment forces mutability but keeps tuple shape.
			input: `
				fn foo(items) { items[0] = 42 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut [number]) -> void",
			},
		},
		"ArrayFromPush": {
			input: `
				fn foo(items) { items.push(42) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>) -> void",
			},
		},
		"ArrayFromLiteralIndexAndPush": {
			input: `
				fn foo(items) {
					val a = items[0]
					items.push("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<string>) -> void",
			},
		},
		"MultiplePushDifferentTypes": {
			input: `
				fn foo(items) {
					items.push(5)
					items.push("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number | string>) -> void",
			},
		},
		"MultiplePushSameType": {
			input: `
				fn foo(items) {
					items.push(5)
					items.push(10)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>) -> void",
			},
		},
		"PushAndUnshiftDifferentTypes": {
			input: `
				fn foo(items) {
					items.push(5)
					items.unshift("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number | string>) -> void",
			},
		},
		"MultiplePushWithLiteralIndex": {
			input: `
				fn foo(items) {
					val a = items[0]
					items.push(5)
					items.push("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number | string>) -> void",
			},
		},
		"IndexAssignmentDifferentTypes": {
			// Literal index assignments with different types produce a tuple, not an array.
			input: `
				fn foo(items) {
					items[0] = 5
					items[1] = "hello"
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut [number, string]) -> void",
			},
		},
		"PushThenPassToCallback": {
			// Push calls bind MethodElemVars, then passing to a callback triggers
			// deepCloneType on the ArrayConstraint during resolveCallSites.
			input: `
				fn foo(cb, items) {
					items.push(5)
					items.push("hello")
					cb(items)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(cb: fn (arg0: mut Array<number | string>) -> T0, items: mut Array<number | string>) -> void",
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

func TestTupleArrayInferenceEdgeCases(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"GapIndex": {
			// Only assigning index 1 creates a 2-tuple with a gap at index 0.
			input: `
				fn foo(items) { items[1] = 42 }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: mut [T0, number]) -> void",
			},
		},
		"ObjectLiteralWidening": {
			// Object literals in index assignment are widened.
			input: `
				fn foo(items) { items[0] = {x: 5, y: 10} }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut [{x: number, y: number}]) -> void",
			},
		},
		"ReadAndWriteDifferentIndexes": {
			// Read index 0 and assign index 1 — mixed read/write, mut tuple.
			input: `
				fn foo(items) {
					val a = items[0]
					items[1] = "hi"
					return a
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: mut [T0, string]) -> T0",
			},
		},
		"MultipleWritesSameIndex": {
			// Writing to the same index twice unifies the value types.
			input: `
				fn foo(items) {
					items[0] = 42
					items[0] = 99
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut [number]) -> void",
			},
		},
		"SparseIndexes": {
			// Non-contiguous literal indexes create a tuple with gaps.
			input: `
				fn foo(items) { return [items[0], items[3]] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(items: [T0, T1, T2, T3]) -> [T0, T3]",
			},
		},
		"IndexAssignmentAndPush": {
			// Index assignment + mutating method → mut Array (push forces array).
			input: `
				fn foo(items) {
					items[0] = 42
					items.push(99)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>) -> void",
			},
		},
		"IndexAssignmentAndPushDifferentTypes": {
			// Index assignment + push with different types → mut Array with union.
			input: `
				fn foo(items) {
					items[0] = 5
					items.push("hello")
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<string | number>) -> void",
			},
		},
		"SingleIndexReadOnly": {
			// A single literal index read with no mutation → 1-tuple.
			input: `
				fn foo(items) { return items[0] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: [T0]) -> T0",
			},
		},
		"ReturnTupleElement": {
			// Reading two elements, returning one — tuple with both type params.
			input: `
				fn foo(items) {
					val a = items[0]
					val b = items[1]
					return a
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, T1]) -> T0",
			},
		},
		"WriteOnlyIndex0": {
			// Writing without reading — mut tuple with widened type.
			input: `
				fn foo(items) { items[0] = "hello" }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut [string]) -> void",
			},
		},
		"ArrayFromMapOnlyGeneric": {
			// Calling .map() alone (no index access) should infer an array,
			// not a tuple. The identity callback unifies input and output types.
			input: `
				fn foo(items) { return items.map(fn (x) { return x }) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: Array<T0>) -> Array<T0>",
			},
		},
		"ArrayFromMapOnly": {
			// Calling .map() alone (no index access) should infer an array,
			// not a tuple. The identity callback unifies input and output types.
			input: `
				fn foo(items) { return items.map(fn (x) { return x * x }) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: Array<number>) -> Array<number>",
			},
		},
		"PushMultipleSameType": {
			// Pushing multiple values of the same type works.
			input: `
				fn foo(items) {
					items.push(5)
					items.push(10)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>) -> void",
			},
		},
		"LengthOnlyIsNotArray": {
			// Accessing .length alone should infer an open object, not a tuple
			// or array — .length is ambiguous (exists on strings, etc.).
			input: `
				fn foo(x) { return x.length }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(x: {length: T0}) -> T0",
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

func TestTupleArrayInferenceOnProperties(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"PropertyIndexedAsTuple": {
			// Indexing a property with a literal → tuple on the property.
			input: `fn foo(obj) { return obj.items[0] }`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {items: [T0]}) -> T0",
			},
		},
		"PropertyIndexedAsTupleTwoElements": {
			// Two literal indexes on a property → 2-tuple.
			input: `fn foo(obj) { return [obj.items[0], obj.items[1]] }`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {items: [T0, T1]}) -> [T0, T1]",
			},
		},
		"PropertyPushInfersArray": {
			// Calling .push() on a property → mut Array on the property.
			input: `fn foo(obj) { obj.items.push(42) }`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: {items: mut Array<number>}) -> void",
			},
		},
		"PropertyIndexAssignment": {
			// Index assignment on a property → mut tuple on the property.
			input: `fn foo(obj) { obj.items[0] = "hi" }`,
			expectedTypes: map[string]string{
				"foo": "fn (obj: {items: mut [string]}) -> void",
			},
		},
		"PropertyMapInfersArray": {
			// Calling .map() on a property → Array on the property.
			input: `
				fn foo(obj) { return obj.items.map(fn (x) { return x }) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {items: Array<T0>}) -> Array<T0>",
			},
		},
		"TwoPropertiesDifferentShapes": {
			// Two properties with different inference: tuple vs mut Array.
			input: `
				fn foo(obj) {
					val a = obj.names[0]
					obj.scores.push(100)
					return a
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {names: [T0], scores: mut Array<number>}) -> T0",
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

// TestTupleArrayInferenceFixedBugs covers regressions for specific bugs that
// were fixed in the tuple/array inference pipeline.
func TestTupleArrayInferenceFixedBugs(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		// asNonNegativeIntLiteral rejects indexes above maxTupleIndex (20),
		// so a huge literal index forces Array<T> via isNumericType.
		"LargeIndexForcesArray": {
			input: `
				fn foo(items) { return items[1001] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: Array<T0>) -> T0",
			},
		},
		// Non-literal index combined with index assignment should produce
		// mut Array<T>, not immutable Array<T>.
		"NonLiteralIndexWithAssignmentIsMutArray": {
			input: `
				fn foo(items, i: number) {
					items[i] = 42
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>, i: number) -> void",
			},
		},
		// A read-only gap index (no assignment) should produce an immutable
		// tuple, not a mutable one.
		"GapIndexReadOnly": {
			input: `
				fn foo(items) { return items[1] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, T1]) -> T1",
			},
		},
		// String literal used as index on an array-constrained param should
		// route to property access (e.g. items["length"] → number).
		"StringLiteralIndexRoutesToPropertyAccess": {
			input: `
				fn foo(items) {
					val a = items[0]
					return items["length"]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: [T0]) -> number",
			},
		},
		// Binding an ArrayConstraint to a variadic tuple should unify
		// literal indexes with the correct element types.
		"BindToVariadicTuple": {
			input: `
				fn bar(x: [number, string, ...Array<boolean>]) { }
				fn foo(items) {
					val a = items[0]
					val b = items[1]
					val c = items[3]
					bar(items)
					return [a, b, c]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, string, ...Array<boolean>]) -> [number, string, boolean]",
			},
		},
		// Array constraint resolved inside a function parameter that is a
		// callback receiving the constrained type via a TypeRefType wrapper
		// (tests resolveArrayConstraintsInType recursion into nested types).
		"ArrayConstraintResolvedInCallback": {
			input: `
				fn foo(items) {
					val a = items[0]
					items.push(a)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: mut Array<T0>) -> void",
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

func TestTupleArrayPassToTypedFunction(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"PassToTupleParam": {
			// Passing to a function with a tuple parameter infers a tuple
			input: `
				fn bar(items: [number, string]) -> number { return items[0] }
				fn foo(items) { bar(items) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, string]) -> void",
			},
		},
		"PassToArrayParam": {
			// Passing to a function with an Array parameter infers Array
			input: `
				fn bar(items: Array<number>) -> number { return items[0] }
				fn foo(items) { bar(items) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: Array<number>) -> void",
			},
		},
		"PassToMutArrayParam": {
			// Passing to a function with a mut Array parameter infers mut Array
			input: `
				fn bar(items: mut Array<number>) -> void { items[0] = 1 }
				fn foo(items) { bar(items) }
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: mut Array<number>) -> void",
			},
		},
		"IndexThenPassToArrayParam": {
			// Indexing creates an ArrayConstraint, then passing to Array<T>
			// forces array resolution
			input: `
				fn bar(items: Array<number>) -> number { return items[0] }
				fn foo(items) {
					val x = items[0]
					bar(items)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: Array<number>) -> void",
			},
		},
		"IndexThenPassToTupleParam": {
			// Indexing creates an ArrayConstraint, then passing to a tuple
			// binds to the tuple type
			input: `
				fn bar(items: [number, string]) -> number { return items[0] }
				fn foo(items) {
					val x = items[0]
					bar(items)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, string]) -> void",
			},
		},
		"PassToMultipleTypedFunctions": {
			// Passing to two functions with different array/tuple params
			input: `
				fn bar(items: Array<number>) -> number { return items[0] }
				fn baz(items: Array<number>) -> number { return items[0] }
				fn foo(items) {
					bar(items)
					baz(items)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: Array<number>) -> void",
			},
		},
		"IndexThenPassToVariadicTupleParam": {
			// Indexing and then passing to a variadic tuple parameter
			input: `
				fn bar(items: [number, ...Array<string>]) -> number { return items[0] }
				fn foo(items) {
					val x = items[0]
					bar(items)
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn (items: [number, ...Array<string>]) -> void",
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

func TestTupleRowPolymorphism(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"TupleWithReturn_RestPreserved": {
			// When the tuple parameter is returned, the rest variable is preserved
			input: `
				fn foo(items) {
					val x = items[0]
					return items
				}
				val r = foo([1, "hello", true])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, ...T1]) -> [T0, ...T1]",
				"r":   "[1, \"hello\", true]",
			},
		},
		"TupleWithoutReturn_RestRemoved": {
			// When the tuple parameter is not returned, the rest variable is removed
			input: `
				fn foo(items) { val x = items[0] }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: [T0]) -> void",
			},
		},
		"DerivedReturn_RestDoesNotEscape": {
			// Returning an element of the tuple — rest does not escape
			input: `
				fn foo(items) { return items[0] }
				val r = foo([42])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(items: [T0]) -> T0",
				"r":   "42",
			},
		},
		"MultipleTupleParamsWithReturn": {
			// Both rest variables appear in return type, both preserved
			input: `
				fn foo(a, b) {
					val x = a[0]
					val y = b[0]
					return [a, b]
				}
				val r = foo([1, 2], ["a", "b"])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1, T2, T3>(a: [T0, ...T1], b: [T2, ...T3]) -> [[T0, ...T1], [T2, ...T3]]",
				"r":   "[[1, 2], [\"a\", \"b\"]]",
			},
		},
		"NoExtraElements_RestResolvesToEmptyTuple": {
			// When caller passes exactly the right number of elements, rest = []
			input: `
				fn foo(items) {
					val x = items[0]
					return items
				}
				val r = foo([42])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, ...T1]) -> [T0, ...T1]",
				"r":   "[42]",
			},
		},
		"LiteralTypesPreservedThroughRest": {
			// Literal types are preserved through rest variable
			input: `
				fn foo(items) {
					val x = items[0]
					return items
				}
				val r = foo([1, "hello"])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(items: [T0, ...T1]) -> [T0, ...T1]",
				"r":   "[1, \"hello\"]",
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

// TestTupleArrayInferenceUnifyErrors verifies that element-type conflicts are
// properly reported when an array-constrained parameter is bound to an
// incompatible concrete type.
func TestTupleArrayInferenceUnifyErrors(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		// Binding an array-constrained param (with number element) to
		// Array<string> should produce a unification error.
		"ArrayConstraintBoundToIncompatibleArray": {
			input: `
				fn bar(items: Array<string>) { return items[0] }
				fn foo(items) {
					items[0] = 42
					bar(items)
				}
			`,
			expectErrors: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, errors := inferModuleTypesAndErrors(t, test.input)
			if test.expectErrors {
				assert.NotEmpty(t, errors, "Expected unification errors but got none")
			} else {
				assert.Empty(t, errors, "Expected no errors")
			}
		})
	}
}

func TestDestructuringObjectPatterns(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"ObjectDestructuringWithoutRest": {
			input: `
				val foo = fn ({bar, baz}) {
					return [bar, baz]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>({bar: T0, baz: T1}) -> [T0, T1]",
			},
		},
		"ObjectDestructuringWithRest": {
			input: `
				val foo = fn ({bar, ...rest}) {
					return rest
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>({bar: T0, ...rest: T1}) -> T1",
			},
		},
		"ObjectDestructuringWithRestUnused": {
			input: `
				val foo = fn ({bar, ...rest}) {
					return bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>({bar: T0, ...rest: T1}) -> T0",
			},
		},
		"ObjectDestructuringWithoutRestBodyConstraint": {
			input: `
				val foo = fn ({x, y}) {
					return x + y
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn ({x: number, y: number}) -> number",
			},
		},
		"ObjectDestructuringWithTypeAnnotation": {
			input: `
				val foo = fn ({bar, ...rest}: {bar: number, baz: string}) {
					return bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn ({bar: number, ...rest}) -> number",
			},
		},
		"RestPassedToIdentityFunction": {
			// Rest from destructuring passed to a simple function.
			input: `
				fn identity(obj) { return obj }
				val foo = fn ({x, ...rest}) {
					return identity(rest)
				}
				val r = foo({x: 1, y: 2, z: "hi"})
			`,
			expectedTypes: map[string]string{
				"r": "{y: 2, z: \"hi\"}",
			},
		},
		"TwoFunctionsWithRestCallingEachOther": {
			// One function destructures and passes rest to another
			// function that accesses a property on it.
			input: `
				fn getY(obj) { return obj.y }
				val foo = fn ({x, ...rest}) {
					return [x, getY(rest)]
				}
				val r = foo({x: 1, y: 2})
			`,
			expectedTypes: map[string]string{
				"r": "[1, 2]",
			},
		},
		"ChainedDestructuringWithRest": {
			// Both functions destructure with rest — rest from outer
			// is passed to inner which also has a rest element.
			input: `
				val foo = fn ({y, ...rest}) {
					return [y, rest]
				}
				val bar = fn ({x, ...rest}) {
					return [x, foo(rest)]
				}
				val r = bar({x: 1, y: 2, z: 3})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>({y: T0, ...rest: T1}) -> [T0, T1]",
				"bar": "fn <T0, T1, T2>({x: T0, ...rest: {y: T1, ...T2}}) -> [T0, [T1, T2]]",
				"r":   "[1, [2, {z: 3}]]",
			},
		},
		// ChainedDestructuringWithRestAndSpread moved to TestRowTypesErrors — see #411.
		"RestPassedToTypedFunction": {
			// Rest from destructuring passed to a function with a type annotation.
			// The rest gets typed as {y: number, z: string} from the process call,
			// but x stays generic since nothing constrains it.
			input: `
				fn process(obj: {y: number, z: string}) {
					return obj.y
				}
				val foo = fn ({x, ...rest}) {
					return process(rest)
				}
				val r = foo({x: true, y: 42, z: "hi"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>({x: T0, ...rest: {y: number, z: string}}) -> number",
				"r":   "number",
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

func TestDestructuringTuplePatterns(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"TupleDestructuringWithoutRest": {
			input: `
				val foo = fn ([a, b]) {
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>([a: T0, b: T1]) -> [T0, T1]",
			},
		},
		"TupleDestructuringWithBodyConstraint": {
			input: `
				val foo = fn ([a, b]) {
					return a + b
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn ([a: number, b: number]) -> number",
			},
		},
		"TupleDestructuringWithRest": {
			input: `
				val foo = fn ([first, ...rest]) {
					return rest
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>([first: T0, ...rest: T1]) -> T1",
			},
		},
		"TupleDestructuringWithRestUnused": {
			input: `
				val foo = fn ([first, ...rest]) {
					return first
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>([first: T0, ...rest: T1]) -> T0",
			},
		},
		"TupleDestructuringCalledWithRest": {
			input: `
				val foo = fn ([first, ...rest]) {
					return rest
				}
				val r = foo([1, 2, 3])
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>([first: T0, ...rest: T1]) -> T1",
				"r":   "[2, 3]",
			},
		},
		"TupleDestructuringCalledWithoutRest": {
			input: `
				val foo = fn ([a, b]) {
					return a + b
				}
				val r = foo([1, 2])
			`,
			expectedTypes: map[string]string{
				"foo": "fn ([a: number, b: number]) -> number",
				"r":   "number",
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

func TestObjectSpread(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicSpread": {
			input: `
				val r = {...{x: 1}, y: 2}
			`,
			expectedTypes: map[string]string{
				"r": "{x: 1, y: 2}",
			},
		},
		"SpreadWithInferredType": {
			input: `
				fn foo(obj) { return {...obj, extra: 1} }
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: T0) -> {...T0, extra: 1}",
			},
		},
		"MultipleSpreads": {
			input: `
				fn merge(a, b) { return {...a, ...b} }
				val r = merge({x: 1}, {y: "hello"})
			`,
			expectedTypes: map[string]string{
				"merge": "fn <T0, T1>(a: T0, b: T1) -> {...T0, ...T1}",
				"r":     "{x: 1, y: \"hello\"}",
			},
		},
		"PropertyAccessThroughSpread": {
			input: `
				val base = {x: 1, y: 2}
				val extended = {...base, z: 3}
				val v = extended.x
			`,
			expectedTypes: map[string]string{
				"base":     "{x: 1, y: 2}",
				"extended": "{x: 1, y: 2, z: 3}",
				"v":        "1",
			},
		},
		"SpreadCalledWithConcreteArgs": {
			input: `
				fn extend(obj) { return {...obj, extra: 1} }
				val r = extend({x: "hello", y: true})
			`,
			expectedTypes: map[string]string{
				"r": "{x: \"hello\", y: true, extra: 1}",
			},
		},
		"SpreadBeforeExplicitOverride": {
			// {...obj, x: 1} — explicit x comes after spread, so x:1 wins.
			input: `
				fn foo(obj) { return {...obj, x: 1} }
				val r = foo({x: 99, y: 2})
				val x = r.x
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: T0) -> {...T0, x: 1}",
				"x":   "1",
			},
		},
		"SpreadAfterExplicitOverride": {
			// {x: 1, ...obj} — spread comes after, so obj.x wins.
			input: `
				fn foo(obj) { return {x: 1, ...obj} }
				val r = foo({x: 99, y: 2})
				val x = r.x
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: T0) -> {x: 1, ...T0}",
				"x":   "99",
			},
		},
		"SpreadOverrideSemantics": {
			input: `
				val foo = {b: 5, c: 10}
				val bar = {a: 1, b: 2, ...foo, c: 3}
				val a = bar.a
				val b = bar.b
				val c = bar.c
			`,
			expectedTypes: map[string]string{
				"a": "1",
				"b": "5",
				"c": "3",
			},
		},
		"SpreadOverrideSemanticsWithDestructuring": {
			input: `
				val foo = {b: 5, c: 10}
				val bar = {a: 1, b: 2, ...foo, c: 3}
				val {a, b, c} = bar
			`,
			expectedTypes: map[string]string{
				"a": "1",
				"b": "5",
				"c": "3",
			},
		},
		"MultipleSpreadsOverrideSemantics": {
			// Two spreads with interleaved explicit property — last provider wins.
			input: `
				val foo = {b: 5, c: 10}
				val bar = {c: 20, d: 30}
				val result = {a: 1, ...foo, c: 3, ...bar}
				val {a, b, c, d} = result
			`,
			expectedTypes: map[string]string{
				"a": "1",
				"b": "5",
				"c": "20",
				"d": "30",
			},
		},
		"SpreadOfObjectWithMethod": {
			// Methods are copied as function-valued properties.
			input: `
				val obj = {x: 1, greet() { return "hello" }}
				val result = {...obj, y: 2}
				val g = result.greet
			`,
			expectedTypes: map[string]string{
				"g": "fn () -> \"hello\"",
			},
		},
		"SpreadOfObjectWithGetter": {
			// Getter's return value becomes a plain property.
			input: `
				val obj = {
					_x: 10,
					get x(self) { return self._x },
				}
				val result = {...obj, y: 2}
				val v = result.x
			`,
			expectedTypes: map[string]string{
				"v": "10",
			},
		},
		"SpreadOfObjectWithSetterOnly": {
			// Setter-only properties are omitted from the spread.
			// The explicit property 'x' in the target is preserved.
			input: `
				val obj = {
					set x(mut self, v: number) { },
				}
				val result = {x: "kept", ...obj}
				val v = result.x
			`,
			expectedTypes: map[string]string{
				"v": "\"kept\"",
			},
		},
		"SpreadOfAnnotatedVariable": {
			// Spread source has an explicit type annotation — ensures
			// getObjectAccess can look through a non-MutabilityType spread.
			input: `
				val base: {x: number, y: string} = {x: 1, y: "hi"}
				val ext = {...base, z: true}
				val v = ext.x
			`,
			expectedTypes: map[string]string{
				"v": "number",
			},
		},
		"SpreadOfTypeAlias": {
			// Spread source typed via a type alias.
			input: `
				type Point = {x: number, y: number}
				val p: Point = {x: 1, y: 2}
				val ext = {...p, z: 3}
				val v = ext.x
			`,
			expectedTypes: map[string]string{
				"v": "number",
			},
		},
		"SpreadNestedInSpreadSource": {
			// Nitpick: getSpreadPropertyType should handle nested RestSpreadElems.
			// val inner = {x: 1}; val outer = {...inner, y: 2}
			// val result = {...outer, z: 3}; result.x should find x through
			// the RestSpreadElem inside outer.
			input: `
				val inner = {x: 1}
				val outer = {...inner, y: 2}
				val result = {...outer, z: 3}
				val v = result.x
			`,
			expectedTypes: map[string]string{
				"v": "1",
			},
		},
		"SpreadPropertyAccessViaDestructuring": {
			// Verifies unification path handles MutabilityType-wrapped spread
			// sources (object literals produce MutabilityType wrappers).
			input: `
				val src = {x: 1, y: 2}
				val dest = {...src, z: 3}
				val {x, y, z} = dest
			`,
			expectedTypes: map[string]string{
				"x": "1",
				"y": "2",
				"z": "3",
			},
		},
		"SpreadPreservesSymbolKeys": {
			// Symbol-keyed properties from a spread source should be
			// accessible on the result via index access.
			input: `
				declare val arr: Array<number>
				val obj = {...arr, extra: 1}
				val iter = obj[Symbol.iterator]
			`,
			expectedTypes: map[string]string{
				"iter": "fn () -> ArrayIterator<number>",
			},
		},
		"SymbolKeyedGetterAccessViaSpread": {
			// Spreading an object with a symbol-keyed getter should
			// make the getter's return type accessible on the result.
			input: `
				declare val src: {get [Symbol.iterator]() -> number}
				val obj = {...src, x: 1}
				val v = obj[Symbol.iterator]
			`,
			expectedTypes: map[string]string{
				"v": "number",
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

func TestTupleSpreadRefined(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"SpreadOfTupleIntoTuple": {
			input: `
				val tup: [string, boolean] = ["hello", true]
				val result = [0, ...tup, 4]
			`,
			expectedTypes: map[string]string{
				"result": "[0, string, boolean, 4]",
			},
		},
		"SpreadOfArrayIntoTuple": {
			input: `
				val arr: Array<number> = [1, 2, 3]
				val result = [0, ...arr, 4]
			`,
			expectedTypes: map[string]string{
				"result": "[0, ...Array<number>, 4]",
			},
		},
		"MultipleTupleSpreads": {
			input: `
				fn concat(a: [number, string], b: [boolean]) {
					return [...a, ...b]
				}
			`,
			expectedTypes: map[string]string{
				"concat": "fn (a: [number, string], b: [boolean]) -> [number, string, boolean]",
			},
		},
		"TuplePlusArraySpread": {
			input: `
				fn prepend(tup: [number, string], arr: Array<boolean>) {
					return [...tup, ...arr]
				}
			`,
			expectedTypes: map[string]string{
				"prepend": "fn (tup: [number, string], arr: Array<boolean>) -> [number, string, ...Array<boolean>]",
			},
		},
		"SpreadWithLiteralElements": {
			input: `
				val arr: Array<number> = [1, 2, 3]
				val result = ["start", ...arr, "end"]
			`,
			expectedTypes: map[string]string{
				"result": "[\"start\", ...Array<number>, \"end\"]",
			},
		},
		"TwoArraySpreads": {
			// Two array spreads collapse to Array<T1 | T2> since
			// [...Array<T1>, ...Array<T2>] is not a valid type.
			input: `
				val a: Array<number> = [1, 2]
				val b: Array<string> = ["x", "y"]
				val result = [...a, ...b]
			`,
			expectedTypes: map[string]string{
				"result": "Array<number | string>",
			},
		},
		"SpreadOfSet": {
			// Spreading a Set extracts its element type via iterability.
			input: `
				declare val s: Set<number>
				val result = [...s]
			`,
			expectedTypes: map[string]string{
				"result": "Array<number>",
			},
		},
		"SpreadOfMap": {
			// Spreading a Map yields [K, V] tuples via iterability.
			input: `
				declare val m: Map<string, number>
				val result = [...m]
			`,
			expectedTypes: map[string]string{
				"result": "Array<[string, number]>",
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

func TestRowTypesOptionalChaining(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BasicOptionalChaining": {
			input: `
				fn foo(obj) {
					return obj?.bar
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0} | null | undefined) -> T0 | undefined",
			},
		},
		"NestedOptionalChaining": {
			input: `
				fn foo(a) {
					return a?.b?.c
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(a: {b: {c: T0} | null | undefined} | null | undefined) -> T0 | undefined",
			},
		},
		"AllOptional": {
			// Second ?. adds baz to the open ObjectType inside the union
			input: `
				fn foo(obj) {
					val x = obj?.bar
					val y = obj?.baz
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, baz: T1} | null | undefined) -> void",
			},
		},
		"OptionalChainingWithReturn": {
			// Returning the nullable object preserves the row variable
			input: `
				fn foo(obj) {
					val x = obj?.bar
					return obj
				}
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0, T1>(obj: {bar: T0, ...T1} | null | undefined) -> {bar: T0, ...T1} | null | undefined",
			},
		},
		"OptionalChainingRowPolyCall": {
			// Calling a function that uses ?. with extra properties
			input: `
				fn foo(obj) {
					return obj?.bar
				}
				val r = foo({bar: 1, baz: "hello"})
			`,
			expectedTypes: map[string]string{
				"foo": "fn <T0>(obj: {bar: T0} | null | undefined) -> T0 | undefined",
				"r":   "1 | undefined",
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

func TestRowTypesOptionalChainingErrors(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedErrs []string
	}{
		"MixOptionalAndNonOptional": {
			// After obj?.bar, obj is nullable — non-optional .baz should error
			input: `
				fn foo(obj) {
					val x = obj?.bar
					val y = obj.baz
				}
			`,
			expectedErrs: []string{"Expected an object type"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, inferErrors := inferModuleTypesAndErrors(t, test.input)

			require.Len(t, inferErrors, len(test.expectedErrs), "expected %d errors, got %d", len(test.expectedErrs), len(inferErrors))
			for i, expectedErr := range test.expectedErrs {
				if i < len(inferErrors) {
					assert.Contains(t, inferErrors[i].Message(), expectedErr)
				}
			}
		})
	}
}

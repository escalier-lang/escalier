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

// =============================================================================
// Phase 0.1: Verify Standard Library Types Load Correctly
// =============================================================================

func TestStdLibIteratorTypesLoaded(t *testing.T) {
	c := NewChecker()
	scope := Prelude(c)

	t.Run("Iterator", func(t *testing.T) {
		iteratorType := scope.GetTypeAlias("Iterator")
		require.NotNil(t, iteratorType, "Iterator type should be loaded")
		assert.Equal(t, 3, len(iteratorType.TypeParams), "Iterator<T, TReturn, TNext>")
	})

	t.Run("Iterable", func(t *testing.T) {
		iterableType := scope.GetTypeAlias("Iterable")
		require.NotNil(t, iterableType, "Iterable type should be loaded")
		assert.Equal(t, 3, len(iterableType.TypeParams), "Iterable<T, TReturn, TNext>")
	})

	t.Run("IterableIterator", func(t *testing.T) {
		iterableIteratorType := scope.GetTypeAlias("IterableIterator")
		require.NotNil(t, iterableIteratorType, "IterableIterator type should be loaded")
		assert.Equal(t, 3, len(iterableIteratorType.TypeParams), "IterableIterator<T, TReturn, TNext>")
	})

	t.Run("Generator", func(t *testing.T) {
		generatorType := scope.GetTypeAlias("Generator")
		require.NotNil(t, generatorType, "Generator type should be loaded")
		assert.Equal(t, 3, len(generatorType.TypeParams), "Generator<T, TReturn, TNext>")
	})

	// NOTE: AsyncGenerator requires ES2018+ lib files which are not currently loaded.
	// This test documents the current state. When ES2018+ support is added, update this test.

	t.Run("IteratorResult", func(t *testing.T) {
		iteratorResultType := scope.GetTypeAlias("IteratorResult")
		require.NotNil(t, iteratorResultType, "IteratorResult type should be loaded")
		assert.Equal(t, 2, len(iteratorResultType.TypeParams), "IteratorResult<T, TReturn>")
	})

	t.Run("SymbolConstructorHasIterator", func(t *testing.T) {
		symbolConstructor := scope.GetTypeAlias("SymbolConstructor")
		require.NotNil(t, symbolConstructor, "SymbolConstructor should be loaded")

		objType, ok := type_system.Prune(symbolConstructor.Type).(*type_system.ObjectType)
		require.True(t, ok, "SymbolConstructor should be an ObjectType")

		// Check that the 'iterator' property exists
		found := false
		for _, elem := range objType.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == "iterator" {
					found = true
					_, isUniqueSymbol := type_system.Prune(prop.Value).(*type_system.UniqueSymbolType)
					assert.True(t, isUniqueSymbol, "Symbol.iterator should be a unique symbol type")
					break
				}
			}
		}
		assert.True(t, found, "SymbolConstructor should have an 'iterator' property")
	})
}

// =============================================================================
// Phase 0.2: Verify Symbol.iterator Property Lookup
// =============================================================================

// helper to infer a module and return the types of declared variables
func inferModule(t *testing.T, input string) (map[string]string, []Error) {
	t.Helper()
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	if len(parseErrors) > 0 {
		for i, err := range parseErrors {
			t.Logf("Parse Error[%d]: %#v", i, err)
		}
	}
	require.Len(t, parseErrors, 0, "expected no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	inferErrors := c.InferModule(inferCtx, module)

	actualTypes := make(map[string]string)
	for name, binding := range inferCtx.Scope.Namespace.Values {
		actualTypes[name] = binding.Type.String()
	}

	return actualTypes, inferErrors
}

// helper to infer a script and return both types and errors
func inferScript(t *testing.T, input string) (map[string]string, []Error) {
	t.Helper()
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	if len(parseErrors) > 0 {
		for i, err := range parseErrors {
			t.Logf("Parse Error[%d]: %#v", i, err)
		}
	}
	require.Len(t, parseErrors, 0, "expected no parse errors")

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, inferErrors := c.InferScript(inferCtx, script)

	actualTypes := make(map[string]string)
	for name, binding := range scope.Namespace.Values {
		actualTypes[name] = binding.Type.String()
	}

	return actualTypes, inferErrors
}

func TestSymbolIteratorLookup(t *testing.T) {
	t.Run("ArraySymbolIteratorAccess", func(t *testing.T) {
		types, errors := inferScript(t, `
			declare val arr: Array<number>
			val iter = arr[Symbol.iterator]
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
		iterType, exists := types["iter"]
		assert.True(t, exists, "iter should be declared")
		if exists {
			// Should be a function type (the [Symbol.iterator] method)
			assert.True(t, strings.Contains(iterType, "fn"),
				"iter should be a function type, got: %s", iterType)
		}
	})
}

// =============================================================================
// Phase 0.3: GetIterableElementType
// =============================================================================

func TestGetIterableElementType(t *testing.T) {
	c := NewChecker()
	scope := Prelude(c)
	ctx := Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	t.Run("ArrayOfNumber", func(t *testing.T) {
		arrayType := &type_system.TypeRefType{
			Name:     type_system.NewIdent("Array"),
			TypeArgs: []type_system.Type{type_system.NewNumPrimType(nil)},
		}
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias, "Array type alias should exist")
		arrayType.TypeAlias = arrayAlias

		elemType := c.GetIterableElementType(ctx, arrayType)
		require.NotNil(t, elemType, "Array<number> should be iterable")
		assert.Equal(t, "number", elemType.String())
	})

	t.Run("ArrayOfString", func(t *testing.T) {
		arrayType := &type_system.TypeRefType{
			Name:     type_system.NewIdent("Array"),
			TypeArgs: []type_system.Type{type_system.NewStrPrimType(nil)},
		}
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		arrayType.TypeAlias = arrayAlias

		elemType := c.GetIterableElementType(ctx, arrayType)
		require.NotNil(t, elemType, "Array<string> should be iterable")
		assert.Equal(t, "string", elemType.String())
	})

	t.Run("StringIsIterable", func(t *testing.T) {
		strType := type_system.NewStrPrimType(nil)
		elemType := c.GetIterableElementType(ctx, strType)
		require.NotNil(t, elemType, "string should be iterable")
		assert.Equal(t, "string", elemType.String())
	})

	t.Run("NumberIsNotIterable", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		elemType := c.GetIterableElementType(ctx, numType)
		assert.Nil(t, elemType, "number should not be iterable")
	})

	t.Run("BooleanIsNotIterable", func(t *testing.T) {
		boolType := type_system.NewBoolPrimType(nil)
		elemType := c.GetIterableElementType(ctx, boolType)
		assert.Nil(t, elemType, "boolean should not be iterable")
	})

	t.Run("TupleWithRestSpread", func(t *testing.T) {
		// [number, ...string[]] should yield number | string
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		arrayOfString := type_system.NewTypeRefType(nil, "Array", arrayAlias, type_system.NewStrPrimType(nil))
		tupleType := type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewRestSpreadType(nil, arrayOfString),
		)
		elemType := c.GetIterableElementType(ctx, tupleType)
		require.NotNil(t, elemType, "[number, ...string[]] should be iterable")
		assert.Equal(t, "number | string", elemType.String())
	})

	t.Run("EmptyTuple", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil)
		elemType := c.GetIterableElementType(ctx, tupleType)
		require.NotNil(t, elemType)
		assert.Equal(t, "never", elemType.String())
	})

	t.Run("SingleElementTuple", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil, type_system.NewNumPrimType(nil))
		elemType := c.GetIterableElementType(ctx, tupleType)
		require.NotNil(t, elemType)
		assert.Equal(t, "number", elemType.String())
	})

	t.Run("TupleWithSpreadTuple", func(t *testing.T) {
		// [number, ...[string, boolean]] should yield number | string | boolean
		innerTuple := type_system.NewTupleType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewBoolPrimType(nil),
		)
		tupleType := type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewRestSpreadType(nil, innerTuple),
		)
		elemType := c.GetIterableElementType(ctx, tupleType)
		require.NotNil(t, elemType, "[number, ...[string, boolean]] should be iterable")
		assert.Equal(t, "number | string | boolean", elemType.String())
	})

	t.Run("UnionOfIterables", func(t *testing.T) {
		// string | Array<number> should yield string | number
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewTypeRefType(nil, "Array", arrayAlias, type_system.NewNumPrimType(nil)),
		)
		elemType := c.GetIterableElementType(ctx, unionType)
		require.NotNil(t, elemType, "string | Array<number> should be iterable")
		assert.Equal(t, "string | number", elemType.String())
	})

	t.Run("UnionWithNonIterable", func(t *testing.T) {
		// string | number should not be iterable (number is not iterable)
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewNumPrimType(nil),
		)
		elemType := c.GetIterableElementType(ctx, unionType)
		assert.Nil(t, elemType, "string | number should not be iterable")
	})
}

// =============================================================================
// Phase 3.1: Generator Type Helpers
// =============================================================================

func TestMakeGeneratorType(t *testing.T) {
	c := NewChecker()
	scope := Prelude(c)

	t.Run("Generator<number, string, boolean>", func(t *testing.T) {
		genAlias := scope.GetTypeAlias("Generator")
		genType := type_system.NewTypeRefType(nil, "Generator", genAlias,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil),
			type_system.NewBoolPrimType(nil),
		)
		assert.Equal(t, "Generator", type_system.QualIdentToString(genType.Name))
		require.Len(t, genType.TypeArgs, 3)
		assert.Equal(t, "number", genType.TypeArgs[0].String())
		assert.Equal(t, "string", genType.TypeArgs[1].String())
		assert.Equal(t, "boolean", genType.TypeArgs[2].String())
		assert.NotNil(t, genType.TypeAlias, "TypeAlias should be set")
	})

	t.Run("AsyncGenerator<number, void, undefined>", func(t *testing.T) {
		// AsyncGenerator alias is not loaded (requires ES2018+), so pass nil
		genType := type_system.NewTypeRefType(nil, "AsyncGenerator", nil,
			type_system.NewNumPrimType(nil),
			type_system.NewVoidType(nil),
			type_system.NewUndefinedType(nil),
		)
		assert.Equal(t, "AsyncGenerator", type_system.QualIdentToString(genType.Name))
		require.Len(t, genType.TypeArgs, 3)
		assert.Equal(t, "number", genType.TypeArgs[0].String())
		assert.Equal(t, "void", genType.TypeArgs[1].String())
		assert.Equal(t, "undefined", genType.TypeArgs[2].String())
	})
}

// =============================================================================
// Phase 3.2: GetIteratorReturnType
// =============================================================================

func TestGetIteratorReturnType(t *testing.T) {
	c := NewChecker()
	scope := Prelude(c)
	ctx := Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	t.Run("ArrayOfNumber", func(t *testing.T) {
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		arrayType := &type_system.TypeRefType{
			Name:      type_system.NewIdent("Array"),
			TypeArgs:  []type_system.Type{type_system.NewNumPrimType(nil)},
			TypeAlias: arrayAlias,
		}

		returnType := c.GetIteratorReturnType(ctx, arrayType)
		require.NotNil(t, returnType, "Array<number> should have an iterator return type")
	})

	t.Run("StringIteratorReturnType", func(t *testing.T) {
		strType := type_system.NewStrPrimType(nil)
		returnType := c.GetIteratorReturnType(ctx, strType)
		require.NotNil(t, returnType, "string should have an iterator return type")
	})

	t.Run("NumberHasNoIteratorReturnType", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		returnType := c.GetIteratorReturnType(ctx, numType)
		assert.Nil(t, returnType, "number should not have an iterator return type")
	})

	t.Run("TupleReturnType", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil),
		)
		returnType := c.GetIteratorReturnType(ctx, tupleType)
		require.NotNil(t, returnType, "tuple should have an iterator return type")
		assert.Equal(t, "void", returnType.String())
	})

	t.Run("UnionOfIterables", func(t *testing.T) {
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		// string | Array<number> — both iterable, so union of their TReturn types
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewTypeRefType(nil, "Array", arrayAlias, type_system.NewNumPrimType(nil)),
		)
		returnType := c.GetIteratorReturnType(ctx, unionType)
		require.NotNil(t, returnType, "string | Array<number> should have an iterator return type")
	})

	t.Run("UnionWithNonIterable", func(t *testing.T) {
		// string | number — number is not iterable, so no return type
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewNumPrimType(nil),
		)
		returnType := c.GetIteratorReturnType(ctx, unionType)
		assert.Nil(t, returnType, "string | number should not have an iterator return type")
	})
}

// =============================================================================
// Phase 3.2: GetAsyncIterableElementType
// =============================================================================

func TestGetAsyncIterableElementType(t *testing.T) {
	c := NewChecker()
	scope := Prelude(c)
	ctx := Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	t.Run("NumberIsNotAsyncIterable", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		elemType := c.GetAsyncIterableElementType(ctx, numType)
		assert.Nil(t, elemType, "number should not be async iterable")
	})

	t.Run("StringIsNotAsyncIterable", func(t *testing.T) {
		// string has [Symbol.iterator] but not [Symbol.asyncIterator]
		strType := type_system.NewStrPrimType(nil)
		elemType := c.GetAsyncIterableElementType(ctx, strType)
		assert.Nil(t, elemType, "string should not be async iterable (no ES2018+ support)")
	})

	t.Run("ArrayIsNotAsyncIterable", func(t *testing.T) {
		// Array has [Symbol.iterator] but not [Symbol.asyncIterator]
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		arrayType := &type_system.TypeRefType{
			Name:      type_system.NewIdent("Array"),
			TypeArgs:  []type_system.Type{type_system.NewNumPrimType(nil)},
			TypeAlias: arrayAlias,
		}
		elemType := c.GetAsyncIterableElementType(ctx, arrayType)
		assert.Nil(t, elemType, "Array should not be async iterable (no ES2018+ support)")
	})
}

// =============================================================================
// Phase 0.4: Array Spread Iterable Check
// =============================================================================

func TestArraySpreadRequiresIterable(t *testing.T) {
	t.Run("SpreadArray", func(t *testing.T) {
		// Valid: spreading an array
		_, errors := inferScript(t, `val arr = [1, 2, ...[3, 4]]`)
		assert.Empty(t, errors)
	})

	t.Run("SpreadIntoTypedArray", func(t *testing.T) {
		// Valid: spreading a declared array
		_, errors := inferScript(t, `
			declare val nums: Array<number>
			val arr = [1, 2, ...nums]
		`)
		assert.Empty(t, errors)
	})

	t.Run("SpreadString", func(t *testing.T) {
		// Valid: spreading a string (strings are iterable)
		_, errors := inferScript(t, `val arr = [..."hello"]`)
		assert.Empty(t, errors)
	})

	t.Run("SpreadNonIterable", func(t *testing.T) {
		// Invalid: spreading a number
		_, errors := inferScript(t, `val arr = [...5]`)
		assert.NotEmpty(t, errors, "spreading a number should produce an error")
		if len(errors) > 0 {
			assert.True(t, strings.Contains(errors[0].Message(), "not iterable"),
				"error should mention 'not iterable', got: %s", errors[0].Message())
		}
	})

	t.Run("SpreadNonIterableObject", func(t *testing.T) {
		// Invalid: spreading a non-iterable object in an array context
		_, errors := inferScript(t, `val arr = [...{a: 1}]`)
		assert.NotEmpty(t, errors, "spreading a non-iterable object should produce an error")
	})
}

// =============================================================================
// Phase 4.1: Infer For-In Loop Types
// =============================================================================

func TestForInLoopInference(t *testing.T) {
	t.Run("ForInArray", func(t *testing.T) {
		types, errors := inferScript(t, `
			declare val nums: Array<number>
			for n in nums {
				val x = n
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
		_ = types
	})

	t.Run("ForInString", func(t *testing.T) {
		_, errors := inferScript(t, `
			for ch in "hello" {
				val x = ch
			}
		`)
		assert.Empty(t, errors)
	})

	t.Run("ForInNonIterable", func(t *testing.T) {
		_, errors := inferScript(t, `
			for x in 42 {
			}
		`)
		assert.NotEmpty(t, errors, "for-in over a number should produce an error")
		if len(errors) > 0 {
			assert.True(t, strings.Contains(errors[0].Message(), "not iterable"),
				"error should mention 'not iterable', got: %s", errors[0].Message())
		}
	})

	t.Run("ForInTuple", func(t *testing.T) {
		_, errors := inferScript(t, `
			for x in [1, 2, 3] {
				val y = x
			}
		`)
		assert.Empty(t, errors)
	})
}

// =============================================================================
// Phase 4.2: Infer Yield Expressions
// =============================================================================

func TestYieldExprInference(t *testing.T) {
	t.Run("SimpleGenerator", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn count() {
				yield 1
				yield 2
				yield 3
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
		countType, exists := types["count"]
		assert.True(t, exists, "count should be declared")
		if exists {
			assert.True(t, strings.Contains(countType, "Generator"),
				"count should return a Generator type, got: %s", countType)
		}
	})

	t.Run("GeneratorWithReturn", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn myGen() {
				yield 1
				yield 2
				return "done"
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
		genType, exists := types["myGen"]
		assert.True(t, exists, "myGen should be declared")
		if exists {
			assert.True(t, strings.Contains(genType, "Generator"),
				"myGen should return a Generator type, got: %s", genType)
		}
	})

	t.Run("GeneratorYieldDifferentTypes", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn mixed() {
				yield 1
				yield "hello"
			}
		`)
		assert.Empty(t, errors)
		mixedType, exists := types["mixed"]
		assert.True(t, exists, "mixed should be declared")
		if exists {
			assert.True(t, strings.Contains(mixedType, "Generator"),
				"mixed should return a Generator type, got: %s", mixedType)
		}
	})

	t.Run("YieldFromArray", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn delegating() {
				declare val nums: Array<number>
				yield from nums
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
		delegatingType, exists := types["delegating"]
		assert.True(t, exists, "delegating should be declared")
		if exists {
			assert.True(t, strings.Contains(delegatingType, "Generator"),
				"delegating should return a Generator type, got: %s", delegatingType)
		}
	})

	t.Run("YieldFromNonIterable", func(t *testing.T) {
		_, errors := inferScript(t, `
			fn bad() {
				yield from 42
			}
		`)
		assert.NotEmpty(t, errors, "yield from a number should produce an error")
		if len(errors) > 0 {
			assert.True(t, strings.Contains(errors[0].Message(), "not iterable"),
				"error should mention 'not iterable', got: %s", errors[0].Message())
		}
	})
}

// =============================================================================
// Phase 4.3: Generator function detection
// =============================================================================

func TestGeneratorFunctionDetection(t *testing.T) {
	t.Run("NonGeneratorFunction", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn add(a: number, b: number) {
				return a + b
			}
		`)
		assert.Empty(t, errors)
		addType, exists := types["add"]
		assert.True(t, exists)
		if exists {
			assert.False(t, strings.Contains(addType, "Generator"),
				"add should NOT be a Generator, got: %s", addType)
		}
	})

	t.Run("NestedYieldDoesNotAffectOuter", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn outer() {
				fn inner() {
					yield 1
				}
				return inner
			}
		`)
		assert.Empty(t, errors)
		outerType, exists := types["outer"]
		assert.True(t, exists)
		if exists {
			// outer returns a function that returns a Generator, but outer itself is NOT a generator
			assert.True(t, strings.HasPrefix(outerType, "fn ()"),
				"outer should be a regular function, got: %s", outerType)
			assert.False(t, strings.HasPrefix(outerType, "fn () -> Generator"),
				"outer should NOT directly return a Generator (yield is in inner), got: %s", outerType)
		}
	})
}

// =============================================================================
// Phase 0.5: Module-level iterator inference
// =============================================================================

func TestModuleIterableInference(t *testing.T) {
	t.Run("SpreadArrayInModule", func(t *testing.T) {
		types, errors := inferModule(t, `
			declare val nums: Array<number>
			val arr = [1, 2, ...nums]
		`)
		assert.Empty(t, errors)
		arrType, exists := types["arr"]
		assert.True(t, exists, "arr should be declared")
		if exists {
			assert.True(t, strings.Contains(arrType, "number"),
				"arr should contain number, got: %s", arrType)
		}
	})

	t.Run("SpreadNonIterableInModule", func(t *testing.T) {
		_, errors := inferModule(t, `val arr = [...5]`)
		assert.NotEmpty(t, errors, "spreading a number should produce an error")
		if len(errors) > 0 {
			assert.True(t, strings.Contains(errors[0].Message(), "not iterable"),
				"error should mention 'not iterable', got: %s", errors[0].Message())
		}
	})
}

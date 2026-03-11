package tests

import (
	"context"
	"fmt"
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
			fmt.Printf("Parse Error[%d]: %#v\n", i, err)
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
			fmt.Printf("Parse Error[%d]: %#v\n", i, err)
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
				fmt.Printf("Error: %s\n", err.Message())
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

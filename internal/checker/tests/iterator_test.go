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

// assertHasError checks that at least one error in the slice contains the given substring.
func assertHasError(t *testing.T, errors []Error, substring string) {
	t.Helper()
	for _, err := range errors {
		if strings.Contains(err.Message(), substring) {
			return
		}
	}
	messages := make([]string, len(errors))
	for i, err := range errors {
		messages[i] = err.Message()
	}
	t.Errorf("expected an error containing %q, got: %v", substring, messages)
}

// =============================================================================
// Phase 0.1: Verify Standard Library Types Load Correctly
// =============================================================================

func TestStdLibIteratorTypesLoaded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
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

	c := NewChecker(ctx)
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, inferErrors := c.InferModule(inferCtx, module)

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

	c := NewChecker(ctx)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	scope := Prelude(c)
	inferCtx := Context{
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

		elemType := c.GetIterableElementType(inferCtx, arrayType)
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

		elemType := c.GetIterableElementType(inferCtx, arrayType)
		require.NotNil(t, elemType, "Array<string> should be iterable")
		assert.Equal(t, "string", elemType.String())
	})

	t.Run("StringIsIterable", func(t *testing.T) {
		strType := type_system.NewStrPrimType(nil)
		elemType := c.GetIterableElementType(inferCtx, strType)
		require.NotNil(t, elemType, "string should be iterable")
		assert.Equal(t, "string", elemType.String())
	})

	t.Run("NumberIsNotIterable", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		elemType := c.GetIterableElementType(inferCtx, numType)
		assert.Nil(t, elemType, "number should not be iterable")
	})

	t.Run("BooleanIsNotIterable", func(t *testing.T) {
		boolType := type_system.NewBoolPrimType(nil)
		elemType := c.GetIterableElementType(inferCtx, boolType)
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
		elemType := c.GetIterableElementType(inferCtx, tupleType)
		require.NotNil(t, elemType, "[number, ...string[]] should be iterable")
		assert.Equal(t, "number | string", elemType.String())
	})

	t.Run("EmptyTuple", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil)
		elemType := c.GetIterableElementType(inferCtx, tupleType)
		require.NotNil(t, elemType)
		assert.Equal(t, "never", elemType.String())
	})

	t.Run("SingleElementTuple", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil, type_system.NewNumPrimType(nil))
		elemType := c.GetIterableElementType(inferCtx, tupleType)
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
		elemType := c.GetIterableElementType(inferCtx, tupleType)
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
		elemType := c.GetIterableElementType(inferCtx, unionType)
		require.NotNil(t, elemType, "string | Array<number> should be iterable")
		assert.Equal(t, "string | number", elemType.String())
	})

	t.Run("UnionWithNonIterable", func(t *testing.T) {
		// string | number should not be iterable (number is not iterable)
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewNumPrimType(nil),
		)
		elemType := c.GetIterableElementType(inferCtx, unionType)
		assert.Nil(t, elemType, "string | number should not be iterable")
	})
}

// =============================================================================
// Phase 3.1: Generator Type Helpers
// =============================================================================

func TestMakeGeneratorType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
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

	t.Run("AsyncGenerator<number, undefined, undefined>", func(t *testing.T) {
		// AsyncGenerator alias is not loaded (requires ES2018+), so pass nil
		genType := type_system.NewTypeRefType(nil, "AsyncGenerator", nil,
			type_system.NewNumPrimType(nil),
			type_system.NewVoidType(nil),
			type_system.NewUndefinedType(nil),
		)
		assert.Equal(t, "AsyncGenerator", type_system.QualIdentToString(genType.Name))
		require.Len(t, genType.TypeArgs, 3)
		assert.Equal(t, "number", genType.TypeArgs[0].String())
		assert.Equal(t, "undefined", genType.TypeArgs[1].String())
		assert.Equal(t, "undefined", genType.TypeArgs[2].String())
	})
}

// =============================================================================
// Phase 3.2: GetIteratorReturnType
// =============================================================================

func TestGetIteratorReturnType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	scope := Prelude(c)
	inferCtx := Context{
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

		returnType := c.GetIteratorReturnType(inferCtx, arrayType)
		require.NotNil(t, returnType, "Array<number> should have an iterator return type")
		assert.Equal(t, "BuiltinIteratorReturn", returnType.String())
	})

	t.Run("StringIteratorReturnType", func(t *testing.T) {
		strType := type_system.NewStrPrimType(nil)
		returnType := c.GetIteratorReturnType(inferCtx, strType)
		require.NotNil(t, returnType, "string should have an iterator return type")
		assert.Equal(t, "BuiltinIteratorReturn", returnType.String())
	})

	t.Run("NumberHasNoIteratorReturnType", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		returnType := c.GetIteratorReturnType(inferCtx, numType)
		assert.Nil(t, returnType, "number should not have an iterator return type")
	})

	t.Run("TupleReturnType", func(t *testing.T) {
		tupleType := type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil),
		)
		returnType := c.GetIteratorReturnType(inferCtx, tupleType)
		require.NotNil(t, returnType, "tuple should have an iterator return type")
		assert.Equal(t, "undefined", returnType.String())
	})

	t.Run("UnionOfIterables", func(t *testing.T) {
		arrayAlias := scope.GetTypeAlias("Array")
		require.NotNil(t, arrayAlias)
		// string | Array<number> — both iterable, so union of their TReturn types
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewTypeRefType(nil, "Array", arrayAlias, type_system.NewNumPrimType(nil)),
		)
		returnType := c.GetIteratorReturnType(inferCtx, unionType)
		require.NotNil(t, returnType, "string | Array<number> should have an iterator return type")
		assert.Equal(t, "BuiltinIteratorReturn", returnType.String())
	})

	t.Run("UnionWithNonIterable", func(t *testing.T) {
		// string | number — number is not iterable, so no return type
		unionType := type_system.NewUnionType(nil,
			type_system.NewStrPrimType(nil),
			type_system.NewNumPrimType(nil),
		)
		returnType := c.GetIteratorReturnType(inferCtx, unionType)
		assert.Nil(t, returnType, "string | number should not have an iterator return type")
	})
}

// =============================================================================
// Phase 3.2: GetAsyncIterableElementType
// =============================================================================

func TestGetAsyncIterableElementType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	scope := Prelude(c)
	inferCtx := Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	t.Run("NumberIsNotAsyncIterable", func(t *testing.T) {
		numType := type_system.NewNumPrimType(nil)
		elemType := c.GetAsyncIterableElementType(inferCtx, numType)
		assert.Nil(t, elemType, "number should not be async iterable")
	})

	t.Run("StringIsNotAsyncIterable", func(t *testing.T) {
		// string has [Symbol.iterator] but not [Symbol.asyncIterator]
		strType := type_system.NewStrPrimType(nil)
		elemType := c.GetAsyncIterableElementType(inferCtx, strType)
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
		elemType := c.GetAsyncIterableElementType(inferCtx, arrayType)
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
		require.NotEmpty(t, errors, "spreading a number should produce an error")
		assertHasError(t, errors, "not iterable")
	})

	t.Run("SpreadNonIterableObject", func(t *testing.T) {
		// Invalid: spreading a non-iterable object in an array context
		_, errors := inferScript(t, `val arr = [...{a: 1}]`)
		require.NotEmpty(t, errors, "spreading a non-iterable object should produce an error")
		assertHasError(t, errors, "not iterable")
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
		require.NotEmpty(t, errors, "for-in over a number should produce an error")
		assertHasError(t, errors, "not iterable")
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
		assert.Empty(t, errors)
		assert.Equal(t,
			"fn () -> Generator<1 | 2 | 3, undefined, unknown>",
			types["count"])
	})

	t.Run("GeneratorWithReturn", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn myGen() {
				yield 1
				yield 2
				return "done"
			}
		`)
		assert.Empty(t, errors)
		assert.Equal(t,
			`fn () -> Generator<1 | 2, "done", unknown>`,
			types["myGen"])
	})

	t.Run("GeneratorYieldDifferentTypes", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn mixed() {
				yield 1
				yield "hello"
			}
		`)
		assert.Empty(t, errors)
		assert.Equal(t,
			`fn () -> Generator<1 | "hello", undefined, unknown>`,
			types["mixed"])
	})

	t.Run("YieldFromArray", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn delegating() {
				declare val nums: Array<number>
				yield from nums
			}
		`)
		assert.Empty(t, errors)
		assert.Equal(t,
			"fn () -> Generator<number, undefined, unknown>",
			types["delegating"])
	})

	t.Run("YieldFromNonIterable", func(t *testing.T) {
		_, errors := inferScript(t, `
			fn bad() {
				yield from 42
			}
		`)
		require.NotEmpty(t, errors, "yield from a number should produce an error")
		assertHasError(t, errors, "not iterable")
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
		assert.Equal(t,
			"fn (a: number, b: number) -> number",
			types["add"])
	})

	t.Run("AnnotatedGeneratorReturnType", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn count() -> Generator<number, string, never> {
				yield 1
				yield 2
				return "done"
			}
		`)
		assert.Empty(t, errors)
		assert.Equal(t,
			`fn () -> Generator<number, string, never>`,
			types["count"])
	})

	t.Run("AnnotatedGeneratorReturnTypeMismatch", func(t *testing.T) {
		// Annotated as Generator<string, ...> but yields numbers — should error.
		_, errors := inferScript(t, `
			fn count() -> Generator<string, undefined, never> {
				yield 1
				yield 2
			}
		`)
		require.NotEmpty(t, errors, "generator type argument mismatch should produce an error")
		assertHasError(t, errors, "cannot be assigned to")
	})

	t.Run("AnnotatedNonGeneratorReturnTypeMismatch", func(t *testing.T) {
		// A generator with a return annotation of `number` should produce an error
		// because the inferred type is Generator<number, undefined, unknown>, not number.
		_, errors := inferScript(t, `
			fn g() -> number {
				yield 1
			}
		`)
		require.NotEmpty(t, errors, "annotated return type mismatch should produce an error")
		assertHasError(t, errors, "cannot be assigned to")
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
		assert.Equal(t,
			"fn () -> fn () -> Generator<1, undefined, unknown>",
			types["outer"])
	})
}

// =============================================================================
// Driving a generator through .next()
// =============================================================================

// The third type argument of `Generator<T, TReturn, TNext>` is the send slot: the type
// `next(v)` accepts, and the type a `yield` expression evaluates to. TNext is inferred
// as `unknown` so a caller may send any value, and a return annotation naming a
// generator declares a narrower one.
//
// `next` is declared `next(...args: [] | [TNext])` and takes a `mut self`, so each case
// binds the generator through a `mut` annotation before calling it.
func TestGeneratorNextCall(t *testing.T) {
	tests := map[string]struct {
		input      string
		wantTypes  map[string]string
		wantErrors []string
	}{
		"NextOnInferredGenerator": {
			// A caller that drives the generator without sending anything, then one
			// that sends a string. Both are accepted because TNext is `unknown`.
			input: `
				gen fn count() {
					yield 1
					yield 2
				}
				val it: mut Generator<1 | 2, undefined, unknown> = count()
				val first = it.next()
				val second = it.next("resume")
			`,
			wantTypes: map[string]string{
				"count":  "fn () -> Generator<1 | 2, undefined, unknown>",
				"first":  "IteratorResult<1 | 2, undefined>",
				"second": "IteratorResult<1 | 2, undefined>",
			},
		},
		"YieldEvaluatesToTheInferredSendType": {
			// The send slot is what `yield 1` evaluates to, so returning that value
			// puts `unknown` in TReturn as well.
			input: `
				gen fn echo() {
					val sent = yield 1
					return sent
				}
			`,
			wantTypes: map[string]string{
				"echo": "fn () -> Generator<1, unknown, unknown>",
			},
		},
		"DeclaredSendTypeNarrowsBothEnds": {
			// A declared TNext types the `yield` inside the body and the argument
			// `next(v)` accepts, so sending a number is rejected.
			input: `
				fn stringSink() -> Generator<number, string, string> {
					val sent = yield 1
					return sent
				}
				val it: mut Generator<number, string, string> = stringSink()
				val accepted = it.next("go")
				val rejected = it.next(42)
			`,
			wantTypes: map[string]string{
				"stringSink": "fn () -> Generator<number, string, string>",
				"accepted":   "IteratorResult<number, string>",
			},
			wantErrors: []string{"42 cannot be assigned to string"},
		},
		"NestedGeneratorDoesNotInheritTheDeclaredSendType": {
			// A declared TNext reaches only the body that declares it. `inner` has no
			// annotation, so its own yield falls back to the inferred `unknown` rather
			// than picking up the `string` that `outer` declares.
			input: `
				fn outer() -> Generator<number, fn () -> Generator<1, unknown, unknown>, string> {
					val sent = yield 1
					fn inner() {
						val nested = yield 1
						return nested
					}
					return inner
				}
			`,
			wantTypes: map[string]string{
				"outer": "fn () -> Generator<number, fn () -> Generator<1, unknown, unknown>, string>",
			},
		},
		"AliasedAnnotationDeclaresTheSendType": {
			// An annotation may name the generator through an alias, so the alias is
			// resolved before its TNext is read. Without that, `sent` would be the
			// inferred `unknown` and returning it would not satisfy TReturn.
			input: `
				type G = Generator<number, string, string>
				fn f() -> G {
					val sent = yield 1
					return sent
				}
			`,
			wantTypes: map[string]string{"f": "fn () -> G"},
		},
		"GenericAliasedAnnotationDeclaresTheSendType": {
			// Resolving the alias substitutes its type arguments, so a generic alias
			// reads the N its use site supplied.
			input: `
				type G<N> = Generator<number, string, N>
				fn f() -> G<string> {
					val sent = yield 1
					return sent
				}
			`,
			wantTypes: map[string]string{"f": "fn () -> G<string>"},
		},
		"DelegationNarrowsTheInferredSendType": {
			// `yield from` forwards a sent value into the delegate, so the delegating
			// generator can only accept what the delegate accepts. Leaving TNext at
			// `unknown` here would let a caller send a number into a body that typed
			// it as a string.
			input: `
				gen fn inner() -> Generator<number, undefined, string> {
					val sent = yield 1
					return
				}
				gen fn outer() {
					yield from inner()
				}
				val it: mut Generator<number, undefined, string> = outer()
				val rejected = it.next(42)
			`,
			wantTypes: map[string]string{
				"outer": "fn () -> Generator<number, undefined, string>",
			},
			wantErrors: []string{"42 cannot be assigned to string"},
		},
		"DelegationLeavesANonGeneratorIterableAlone": {
			// An array's iterator ignores sent values, so delegating to one puts no
			// requirement on the slot and TNext stays `unknown`.
			input: `
				gen fn outer() {
					declare val nums: Array<number>
					yield from nums
				}
			`,
			wantTypes: map[string]string{
				"outer": "fn () -> Generator<number, undefined, unknown>",
			},
		},
		"DeclaredSendTypeMustReachEveryDelegate": {
			// A declared TNext is checked at the delegation rather than collected. A
			// generator promising to accept anything cannot forward into one that
			// accepts only strings.
			input: `
				gen fn inner() -> Generator<number, undefined, string> {
					val sent = yield 1
					return
				}
				gen fn outer() -> Generator<number, undefined, unknown> {
					yield from inner()
				}
			`,
			wantErrors: []string{"unknown cannot be assigned to string"},
		},
		"NeverSendTypeRejectsEverySentValue": {
			// `never` is uninhabited, so a generator declaring it cannot be sent
			// anything. This is what an inferred TNext of `never` would impose on
			// every generator.
			input: `
				declare val it: mut Generator<number, string, never>
				val r = it.next(1)
			`,
			wantErrors: []string{"1 cannot be assigned to never"},
		},
		"NextTakesAtMostOneArgument": {
			// `[] | [TNext]` fixes the arity at zero or one, so a second argument is
			// rejected on arity rather than on the type of the extra value.
			input: `
				declare val it: mut Generator<number, string, string>
				val r = it.next("a", "b")
			`,
			wantErrors: []string{
				`Invalid number of arguments for function: fn (...value: [] | [string]) -> IteratorResult<number, string>. Expected: 1, got: 2`,
			},
		},
		"BranchesOfEqualArityResolveByArgumentType": {
			// `next`'s `[] | [TNext]` has one branch per arity, but a rest parameter
			// may offer several of the same length. Each argument picks the branch it
			// satisfies rather than making the call ambiguous.
			input: `
				declare fn pick(...args: [string] | [number]) -> boolean
				val fromFirst = pick("x")
				val fromSecond = pick(42)
			`,
			wantTypes: map[string]string{
				"fromFirst":  "boolean",
				"fromSecond": "boolean",
			},
		},
		"NoBranchAcceptsTheArgument": {
			// An argument no branch accepts reports the type it failed against, not a
			// bare arity complaint that would read as "Expected: 1, got: 1".
			input: `
				declare fn pick(...args: [string] | [number]) -> boolean
				val r = pick(true)
			`,
			wantErrors: []string{"true cannot be assigned to string"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			types, errors := inferScript(t, test.input)

			messages := make([]string, len(errors))
			for i, err := range errors {
				messages[i] = err.Message()
			}
			wantErrors := test.wantErrors
			if wantErrors == nil {
				wantErrors = []string{}
			}
			require.Equal(t, wantErrors, messages)

			for binding, want := range test.wantTypes {
				require.Equal(t, want, types[binding], "unexpected type for %q", binding)
			}
		})
	}
}

// A declared TNext is one type object shared by the signature's Generator and by
// every `yield` in the body. inferExpr stamps the expression's provenance onto
// whatever it returns, so a `yield` that returned that object directly would leave
// the annotation blaming the body's last `yield` — and any later diagnostic about the
// declared type would point at the wrong span.
func TestDeclaredSendTypeKeepsItsOwnProvenance(t *testing.T) {
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: `
		fn f() -> Generator<number, string, string> {
			val a = yield 1
			val b = yield 2
			return a
		}
	`}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script, parseErrors := parser.NewParser(ctx, source).ParseScript()
	require.Len(t, parseErrors, 0, "expected no parse errors")

	c := NewChecker(ctx)
	scope, inferErrors := c.InferScript(Context{Scope: Prelude(c)}, script)
	require.Empty(t, inferErrors)

	fnType, isFunc := type_system.Prune(scope.Namespace.Values["f"].Type).(*type_system.FuncType)
	require.True(t, isFunc)
	genRef, isTypeRef := type_system.Prune(fnType.Return).(*type_system.TypeRefType)
	require.True(t, isTypeRef)
	require.Len(t, genRef.TypeArgs, 3)

	declaredNext := genRef.TypeArgs[2]
	require.Equal(t, "string", declaredNext.String())
	nodeProv, isNodeProv := declaredNext.Provenance().(*ast.NodeProvenance)
	require.True(t, isNodeProv)
	require.IsType(t, &ast.StringTypeAnn{}, nodeProv.Node, "TNext should be blamed on its annotation, not a yield")
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
		require.NotEmpty(t, errors, "spreading a number should produce an error")
		assertHasError(t, errors, "not iterable")
	})
}

// =============================================================================
// Phase 6.2: Additional Type Checker Tests
// =============================================================================

func TestForInLoopTypeBinding(t *testing.T) {
	t.Run("ForInArrayElementType", func(t *testing.T) {
		// Verify the loop variable gets the correct element type
		_, errors := inferScript(t, `
			val items: Array<number> = [1, 2, 3]
			for item in items {
				val x: number = item
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
	})

	t.Run("ForInNonIterableObject", func(t *testing.T) {
		_, errors := inferScript(t, `
			val obj = {a: 1}
			for item in obj { }
		`)
		require.NotEmpty(t, errors, "for-in over a non-iterable object should produce an error")
		assertHasError(t, errors, "not iterable")
	})

	t.Run("ForInDestructuring", func(t *testing.T) {
		// Destructuring the element in a for-in loop
		_, errors := inferScript(t, `
			declare val pairs: Array<[string, number]>
			for [key, value] in pairs {
				val k: string = key
				val v: number = value
			}
		`)
		if len(errors) > 0 {
			for _, err := range errors {
				t.Logf("Error: %s", err.Message())
			}
		}
		assert.Empty(t, errors)
	})
}

func TestGeneratorFunctionTypes(t *testing.T) {
	t.Run("BasicGeneratorReturnType", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn count() {
				yield 1
				yield 2
			}
		`)
		assert.Empty(t, errors)
		countType, exists := types["count"]
		assert.True(t, exists)
		if exists {
			assert.True(t, strings.Contains(countType, "Generator"),
				"count should return a Generator type, got: %s", countType)
		}
	})

	t.Run("GeneratorWithParams", func(t *testing.T) {
		types, errors := inferScript(t, `
			fn range(start: number, end: number) {
				yield start
				yield end
			}
		`)
		assert.Empty(t, errors)
		rangeType, exists := types["range"]
		assert.True(t, exists)
		if exists {
			assert.True(t, strings.Contains(rangeType, "Generator"),
				"range should return a Generator, got: %s", rangeType)
		}
	})
}

// =============================================================================
// Phase 6.3: Error Case Tests
// =============================================================================

func TestForAwaitOutsideAsync(t *testing.T) {
	_, errors := inferScript(t, `
		fn notAsync() {
			declare val asyncItems: Array<number>
			for await item in asyncItems { }
		}
	`)
	require.NotEmpty(t, errors, "'for await' outside async should produce an error")
	assertHasError(t, errors, "'for await' is only allowed in async functions")
}

func TestYieldAtModuleScope(t *testing.T) {
	_, errors := inferScript(t, `
		yield 1
	`)
	require.NotEmpty(t, errors, "yield at module scope should produce an error")
	assertHasError(t, errors, "'yield' can only be used inside a function")
}

func TestYieldInNestedCallback(t *testing.T) {
	// yield in a nested function should make the inner function a generator,
	// not the outer function
	types, errors := inferScript(t, `
		fn outer() {
			val callback = fn() {
				yield 1
			}
			return callback
		}
	`)
	assert.Empty(t, errors)
	outerType, exists := types["outer"]
	assert.True(t, exists)
	if exists {
		// outer should NOT be a generator - it returns a generator function,
		// but its own return type should not start with Generator<
		assert.True(t, strings.HasPrefix(outerType, "fn () -> fn ()"),
			"outer should return a function, not be a generator itself, got: %s", outerType)
	}
}

// =============================================================================
// Phase 6.4: Edge Case Tests
// =============================================================================

func TestEmptyIterable(t *testing.T) {
	_, errors := inferScript(t, `
		val empty: Array<number> = []
		for item in empty {
			val x = item
		}
		val spread = [...empty]
	`)
	if len(errors) > 0 {
		for _, err := range errors {
			t.Logf("Error: %s", err.Message())
		}
	}
	assert.Empty(t, errors)
}

func TestForInWithGeneratorResult(t *testing.T) {
	// Iterating over a generator function's result
	_, errors := inferScript(t, `
		fn nums() {
			yield 1
			yield 2
			yield 3
		}
		for n in nums() {
			val x = n
		}
	`)
	if len(errors) > 0 {
		for _, err := range errors {
			t.Logf("Error: %s", err.Message())
		}
	}
	assert.Empty(t, errors)
}

func TestYieldFromGenerator(t *testing.T) {
	// yield from another generator
	types, errors := inferScript(t, `
		fn inner() {
			yield 1
			yield 2
		}
		fn outer() {
			yield from inner()
			yield 3
		}
	`)
	assert.Empty(t, errors)
	outerType, exists := types["outer"]
	assert.True(t, exists)
	if exists {
		assert.True(t, strings.Contains(outerType, "Generator<"),
			"outer should be a Generator, got: %s", outerType)
		assert.True(t, strings.Contains(outerType, "1") && strings.Contains(outerType, "2"),
			"outer should include delegated yields from inner(), got: %s", outerType)
		assert.True(t, strings.Contains(outerType, "3"),
			"outer should include direct yield 3, got: %s", outerType)
	}
}

func TestMultipleForInLoops(t *testing.T) {
	_, errors := inferScript(t, `
		declare val nums: Array<number>
		declare val strs: Array<string>
		for n in nums {
			val x: number = n
		}
		for s in strs {
			val y: string = s
		}
	`)
	if len(errors) > 0 {
		for _, err := range errors {
			t.Logf("Error: %s", err.Message())
		}
	}
	assert.Empty(t, errors)
}

func TestNestedForInLoops(t *testing.T) {
	_, errors := inferScript(t, `
		declare val matrix: Array<Array<number>>
		for row in matrix {
			for cell in row {
				val x: number = cell
			}
		}
	`)
	if len(errors) > 0 {
		for _, err := range errors {
			t.Logf("Error: %s", err.Message())
		}
	}
	assert.Empty(t, errors)
}

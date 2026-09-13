package solver

import (
	"testing"

	"errors"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Two runs against one directory share the parsed module and stay independent.
// The solver records inferred types in a side table rather than on AST nodes, so
// sharing the nodes shares nothing a run can write.
func TestACachedModuleServesIndependentRuns(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, withPreludeClasses(map[string]string{
		"std/box.esc": `export declare class Box<T> { get(self) -> T }`,
	}))

	// The two runs instantiate the same declaration at different arguments. A
	// shared inferred type would show one run's argument in the other's answer.
	first := InferModuleWithSource(parseModule(t, `
		import "std:box"
		declare val b: box.Box<number>
		val v = b.get()
	`), StdlibSource(dir))
	second := InferModuleWithSource(parseModule(t, `
		import "std:box"
		declare val b: box.Box<string>
		val v = b.get()
	`), StdlibSource(dir))

	require.Empty(t, errorMessagesOf(first.Errors))
	require.Empty(t, errorMessagesOf(second.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, first.Scope, "v")))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, second.Scope, "v")))
}

// A second load of the same source reuses the first parse.
func TestTheParseCacheServesASecondLoad(t *testing.T) {
	t.Parallel()

	cache := newParsedModuleCache(1 << 20)
	parses := 0
	parse := func(int) (*ast.Module, error) {
		parses++
		return &ast.Module{}, nil
	}

	first, err := cache.get("export val a: number = 1", parse)
	require.NoError(t, err)
	second, err := cache.get("export val a: number = 1", parse)
	require.NoError(t, err)

	require.Equal(t, 1, parses)
	require.Same(t, first, second)
}

// Two paths holding the same source share one parse. Each test seeding a stdlib
// tree writes the same prelude to a fresh temporary directory, so keying on the
// path would pin an identical AST per test and serve no hit between them.
func TestTheParseCacheSharesAcrossPaths(t *testing.T) {
	t.Parallel()

	const pkg = `export declare class Box<T> { get(self) -> T }`
	first := seedStdlib(t, withPreludeClasses(map[string]string{"std/box.esc": pkg}))
	second := seedStdlib(t, withPreludeClasses(map[string]string{"std/box.esc": pkg}))
	require.NotEqual(t, first, second, "the two trees are different directories")

	for _, dir := range []string{first, second} {
		res := InferModuleWithSource(parseModule(t, `
			import "std:box"
			declare val b: box.Box<number>
			val v = b.get()
		`), StdlibSource(dir))
		require.Empty(t, errorMessagesOf(res.Errors))
		require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "v")))
	}
}

// Changed content is a different key, so nothing stale survives an edit and no
// file stamp has to be compared.
func TestTheParseCacheRereadsChangedContent(t *testing.T) {
	t.Parallel()

	cache := newParsedModuleCache(1 << 20)
	parses := 0
	parse := func(int) (*ast.Module, error) {
		parses++
		return &ast.Module{}, nil
	}

	_, err := cache.get("export val a: number = 1", parse)
	require.NoError(t, err)
	_, err = cache.get("export val a: number = 2", parse)
	require.NoError(t, err)

	require.Equal(t, 2, parses)
}

// Every distinct source parses under an id of its own, so a span from one
// package never reads as a span from another.
func TestTheParseCacheGivesEachSourceItsOwnID(t *testing.T) {
	t.Parallel()

	cache := newParsedModuleCache(1 << 20)
	var ids []int
	parse := func(sourceID int) (*ast.Module, error) {
		ids = append(ids, sourceID)
		return &ast.Module{}, nil
	}

	for _, contents := range []string{"a", "b", "a", "c", "b"} {
		_, err := cache.get(contents, parse)
		require.NoError(t, err)
	}
	// Three distinct sources, three ids, none repeated.
	require.Equal(t, []int{1 << 20, 1<<20 + 1, 1<<20 + 2}, ids)
}

// A failed parse is not cached, and the id it would have used is not consumed.
func TestTheParseCacheDoesNotHoldAFailure(t *testing.T) {
	t.Parallel()

	cache := newParsedModuleCache(1 << 20)
	_, err := cache.get("bad", func(int) (*ast.Module, error) {
		return nil, errFailedParse
	})
	require.Error(t, err)

	var usedID int
	module, err := cache.get("bad", func(sourceID int) (*ast.Module, error) {
		usedID = sourceID
		return &ast.Module{}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, module)
	require.Equal(t, 1<<20, usedID)
}

var errFailedParse = errors.New("parse failed")

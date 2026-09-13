package solver

import (
	"testing"

	"errors"
	"os"
	"path/filepath"

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

// A second load of one path reuses the first parse rather than re-reading.
func TestTheParseCacheServesASecondLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "box.esc")
	require.NoError(t, os.WriteFile(path, []byte("export val a: number = 1"), 0o644))

	cache := newParsedModuleCache()
	parses := 0
	parse := func() (*ast.Module, error) {
		parses++
		return &ast.Module{}, nil
	}

	first, err := cache.get(path, parse)
	require.NoError(t, err)
	second, err := cache.get(path, parse)
	require.NoError(t, err)

	require.Equal(t, 1, parses)
	require.Same(t, first, second)
}

// A file that changed under the cache is parsed again. A language server
// outlives an edit, so a stale parse would otherwise survive a regeneration.
func TestTheParseCacheRereadsAChangedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "box.esc")
	require.NoError(t, os.WriteFile(path, []byte("export val a: number = 1"), 0o644))

	cache := newParsedModuleCache()
	parses := 0
	parse := func() (*ast.Module, error) {
		parses++
		return &ast.Module{}, nil
	}

	_, err := cache.get(path, parse)
	require.NoError(t, err)

	// A longer body changes the size, which the stamp compares before the
	// modification time a coarse clock may not have advanced.
	require.NoError(t, os.WriteFile(path, []byte("export val a: number = 1\nexport val b: number = 2"), 0o644))
	_, err = cache.get(path, parse)
	require.NoError(t, err)

	require.Equal(t, 2, parses)
}

// A failed parse is not cached, so a later load retries rather than inheriting
// the failure.
func TestTheParseCacheDoesNotHoldAFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "box.esc")
	require.NoError(t, os.WriteFile(path, []byte("export val a: number = 1"), 0o644))

	cache := newParsedModuleCache()
	_, err := cache.get(path, func() (*ast.Module, error) {
		return nil, errFailedParse
	})
	require.Error(t, err)

	module, err := cache.get(path, func() (*ast.Module, error) {
		return &ast.Module{}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, module)
}

var errFailedParse = errors.New("parse failed")

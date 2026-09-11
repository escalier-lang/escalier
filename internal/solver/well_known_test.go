package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// wellKnownChecker builds a checker over a seeded pseudo-package tree, so a test
// reaches a handle without inferring a module that imports anything.
func wellKnownChecker(t *testing.T, files map[string]string) *checker {
	t.Helper()
	c := newChecker()
	c.source = StdlibSource(seedStdlib(t, files))
	return c
}

// preludeTree is a prelude package declaring every name in the closed set, for a
// test that cares which handle comes back rather than what any of them holds.
var preludeTree = map[string]string{
	"std/prelude.esc": `
		export declare class Array<T> {
			at(self, index: number) -> T | undefined,
		}
		export declare class Promise<T> {
			then(self, onfulfilled: fn (value: T) -> unknown) -> unknown,
		}
		export declare type Iterable<T> = {next: fn () -> T}
		export declare type AsyncIterable<T> = {next: fn () -> T}
		export declare type Generator<T> = {next: fn () -> T}
		export declare type AsyncGenerator<T> = {next: fn () -> T}
	`,
}

// A handle is read from the prelude package, so a rule reaches it with nothing
// imported.
func TestWellKnownTypeResolvesWithoutAnImport(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/prelude.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	got, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Equal(t, "Array", soltype.Print(got))
	require.Empty(t, errorMessagesOf(c.errs))
}

// The prelude package is loaded once, so a second consultation reads the scope
// the first one filled rather than inferring the package again.
func TestWellKnownTypeLoadsThePreludeOnce(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/prelude.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	first, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	second, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Same(t, first, second)
}

// Every name in the closed set is reachable when the prelude declares it.
func TestWellKnownTypeReachesEveryNameInTheSet(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, preludeTree)
	for _, name := range wellKnownNames {
		_, ok := c.wellKnownType(name)
		require.True(t, ok, "no handle for %s", name)
	}
	require.Len(t, wellKnownNames, 6)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A prelude that does not declare the name answers false and reports nothing. The
// tree is what would have to change, and a program that never names the type is
// owed no diagnostic about it.
func TestWellKnownTypeDeclinesANameThePreludeOmits(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/prelude.esc": `export val unrelated: number = 1`,
	})
	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A tree with no prelude at all degrades the same way, silently. The solver's own
// tests infer against no stdlib, so a report here would land on every one of them.
func TestWellKnownTypeDeclinesWithNoPrelude(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{})
	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A name the prelude seeds as an opaque stub is not a handle. `Promise` is one of
// the five names addStdlibTypePlaceholders binds in the parent scope, and the
// lookup reads the prelude package's own bindings rather than walking up to it.
func TestWellKnownTypeDoesNotReadThePlaceholderStub(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/prelude.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	_, ok := c.wellKnownType(wellKnownPromise)
	require.False(t, ok, "the stub in the parent scope is not a handle")
}

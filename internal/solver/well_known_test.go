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

// A handle is read from the package declaring the type, so a rule reaches it with
// nothing imported.
func TestWellKnownTypeLoadsWithoutAnImport(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
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

// The second consultation reads the cache rather than loading again.
func TestWellKnownTypeCachesTheHandle(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
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

// A tree that does not declare the type reports once, naming both the type and the
// package expected to supply it, and repeats nothing on a second consultation.
func TestWellKnownTypeReportsAMissingDeclarationOnce(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `export val unrelated: number = 1`,
	})
	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	_, ok = c.wellKnownType(wellKnownArray)
	require.False(t, ok)

	require.Equal(t, []string{
		"the standard library does not supply Array, expected in std:array: " +
			"the package declares no such type",
	}, errorMessagesOf(c.errs))
}

// A tree missing the package altogether degrades the same way, carrying the
// loader's own reason.
func TestWellKnownTypeReportsAMissingPackage(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{})
	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	require.Len(t, c.errs, 1)
	require.Contains(t, c.errs[0].Message(),
		"the standard library does not supply Array, expected in std:array:")
}

// Every name in the closed set names a package, so no handle is unreachable by
// construction.
func TestWellKnownOwnersAreComplete(t *testing.T) {
	t.Parallel()

	for _, name := range []wellKnownName{
		wellKnownArray, wellKnownPromise, wellKnownIterable,
		wellKnownAsyncIterable, wellKnownGenerator, wellKnownAsyncGenerator,
	} {
		require.Contains(t, wellKnownOwner, name)
	}
	require.Len(t, wellKnownOwner, 6)
}

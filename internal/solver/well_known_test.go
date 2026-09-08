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
	c := newTestChecker()
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

// The set is closed, so a name outside it names no package and is unreachable this
// way. It reports nothing, since no tree could have supplied it and the caller asked
// for something that does not exist.
func TestWellKnownTypeDeclinesANameOutsideTheSet(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	_, ok := c.wellKnownType(wellKnownName("Bogus"))
	require.False(t, ok)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A package that published a surface may still have reported diagnostics of its own.
// Both import call sites pass those on, so a handle does too: the type resolves and
// the package's own diagnostic is not swallowed.
func TestWellKnownTypeReportsThePackagesOwnDiagnostics(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
			export val broken: number = "not a number"
		`,
	})
	got, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Equal(t, "Array", soltype.Print(got))
	// The package wraps its diagnostics in one that names the file it loaded, whose
	// path is a temp directory, so the two stable halves are matched separately.
	require.Len(t, c.errs, 1)
	require.Contains(t, c.errs[0].Message(), `package "std:array"`)
	require.Contains(t, c.errs[0].Message(), `has 1 error(s):`+"\n"+`  cannot constrain "not a number" <: number`)
}

// Loading a handle's package mid-walk must leave the caller's file scopes in
// place. bindFileImports installs the loaded module's over them, so without a
// restore every later declaration would lose its own file's imports.
func TestWellKnownTypeLeavesFileScopesAlone(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	before := map[int]*Scope{7: nil}
	c.fileScopes = before

	_, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Equal(t, before, c.fileScopes, "the caller's file scopes survive the load")
}

// A consultation raised while the owning package is itself loading finds no
// surface yet. That absence is temporary, so it must not be cached as a fact
// about the tree.
func TestWellKnownTypeDoesNotCacheACycleMiss(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	// Mark the package as in flight, which is the state loadPackage reads to
	// recognize a cycle.
	c.packages.markLoading("std:array", "std/array.esc")
	c.loadStack = append(c.loadStack, "std:array")

	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	require.NotContains(t, c.ctx.wellKnown, wellKnownArray,
		"a cycle leaves the cache untouched so a later consultation can answer")

	// With the load finished, the same consultation answers.
	c.loadStack = c.loadStack[:len(c.loadStack)-1]
	c.packages = NewPackageRegistry()
	got, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Equal(t, "Array", soltype.Print(got))
}

// A trial's bounds are truncated when it is discarded, so a handle resolved under
// one would cache a type whose bounds no longer exist. A trial answers nothing and
// leaves the load to a consultation outside one.
func TestWellKnownTypeAnswersNothingInsideAProbe(t *testing.T) {
	t.Parallel()

	c := wellKnownChecker(t, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
		`,
	})
	p := c.openProbe()
	_, ok := c.wellKnownType(wellKnownArray)
	require.False(t, ok)
	require.NotContains(t, c.ctx.wellKnown, wellKnownArray)
	c.closeProbe(p, false)

	got, ok := c.wellKnownType(wellKnownArray)
	require.True(t, ok)
	require.Equal(t, "Array", soltype.Print(got))
}

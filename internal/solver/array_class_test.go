package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// arrayClassOf settles the array class over a seeded pseudo-package tree and
// returns the name the run recorded, which is empty when it resolved none.
func arrayClassOf(t *testing.T, files map[string]string) (string, *checker) {
	t.Helper()
	c := newChecker()
	c.source = StdlibSource(seedStdlib(t, files))
	c.preludeScope()
	return c.ctx.arrayClass, c
}

// The class is read from the prelude package, so a rule reaches it with nothing
// imported. What is recorded is the registry key rather than the written name,
// since two packages may each declare an `Array` and the rules compare against
// one of them.
func TestResolveArrayClassNeedsNoImport(t *testing.T) {
	t.Parallel()

	name, c := arrayClassOf(t, map[string]string{
		"std/prelude.esc": `
			export declare class Array<T> {
				at(self, index: number) -> T | undefined,
			}
		`,
	})
	require.Equal(t, "import:std:prelude.Array", name)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A prelude declaring no `Array` leaves the name empty and reports nothing. The
// rules that read it decline, and a program that never mentions an array is owed
// no diagnostic about one.
func TestResolveArrayClassDeclinesWithoutAnArray(t *testing.T) {
	t.Parallel()

	name, c := arrayClassOf(t, map[string]string{
		"std/prelude.esc": `export val unrelated: number = 1`,
	})
	require.Empty(t, name)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A tree with no prelude at all degrades the same way. The solver's own tests
// infer against no stdlib, so a report here would land on every one of them.
func TestResolveArrayClassDeclinesWithNoPrelude(t *testing.T) {
	t.Parallel()

	name, c := arrayClassOf(t, map[string]string{})
	require.Empty(t, name)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A name bound as something other than a class is not an array class. The
// prelude seeds several opaque stubs, and a stub is nothing a rule can read an
// element off.
func TestResolveArrayClassDeclinesANonClassBinding(t *testing.T) {
	t.Parallel()

	name, _ := arrayClassOf(t, map[string]string{
		"std/prelude.esc": `export type Array = number`,
	})
	require.Empty(t, name)
}

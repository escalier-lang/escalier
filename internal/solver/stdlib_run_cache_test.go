package solver

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAmbientCheckerMatchesFreshLoad asserts that a run started from the cached
// checker infers what a run that loads the tree itself infers, on the run that fills
// the cache and on one that reads it.
func TestAmbientCheckerMatchesFreshLoad(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"ambient values", `
			val n = parseInt("42")
			val r = Math.max(1, 2)
		`},
		{"generic stdlib method", `
			val xs: Array<number> = [1, 2, 3]
			val y = xs.at(0)
		`},
		{"pseudo-package import", `
			import "std:math"
			val p = math.PI
		`},
		{"diagnostic", `
			val s: string = Math.max(1, 2)
		`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			wantValues, wantMsgs := inferFresh(t, tt.src)
			for range 2 {
				values, msgs := inferAgainstTree(t, tt.src)
				require.Equal(t, wantValues, values)
				require.Equal(t, wantMsgs, msgs)
			}
		})
	}
}

// TestAmbientCheckerForksAreIndependent asserts that two runs from one cached checker
// hold separate registries, so a definition one run fills is not the other's.
func TestAmbientCheckerForksAreIndependent(t *testing.T) {
	first := InferModuleAgainstStdlib(parseModule(t, `val a = 1`), benchTree, Options{})
	second := InferModuleAgainstStdlib(parseModule(t, `val b = 2`), benchTree, Options{})

	require.NotSame(t, first.checker.ctx, second.checker.ctx)
	firstArray, ok := first.checker.ctx.classDef(first.checker.ctx.arrayClass)
	require.True(t, ok)
	secondArray, ok := second.checker.ctx.classDef(second.checker.ctx.arrayClass)
	require.True(t, ok)
	require.NotSame(t, firstArray, secondArray)
}

// inferFresh is inferAgainstTree on a checker that loads the tree itself rather than
// one forked from the cache.
func inferFresh(t *testing.T, src string) (map[string]string, []string) {
	t.Helper()
	module := parseModule(t, src)
	roots := append(pseudoPackageImportsOf(module), ambientPackages...)
	sort.Strings(roots)
	groups, err := BuildPackageClosure(benchTree, roots)
	require.NoError(t, err)
	res := inferModuleWithGroups(module, StdlibSource(benchTree), StdlibGroupSource(benchTree), groups, Options{})
	msgs := make([]string, len(res.Errors))
	for i, e := range res.Errors {
		msgs[i] = e.Message()
	}
	return renderedValues(res), msgs
}

package solver

import (
	"os"
	"path/filepath"
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
			wantValues, wantMsgs := inferFresh(t, benchTree, tt.src)
			for range 2 {
				values, msgs := inferCached(t, benchTree, tt.src)
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

// TestAmbientCheckerReloadsEditedPrelude asserts that editing the prelude in place
// misses the cache. The prelude loads outside the ambient packages' group, so the
// closure alone does not name it.
func TestAmbientCheckerReloadsEditedPrelude(t *testing.T) {
	dir := seedStdlib(t, withPreludeClasses(map[string]string{
		"std/prelude.esc": "export declare val marker: number\n",
	}))
	values, msgs := inferCached(t, dir, `val m = marker`)
	require.Empty(t, msgs)
	require.Equal(t, "number", values["m"])

	path := filepath.Join(dir, "std", "prelude.esc")
	edited := "export declare val marker: string\n" + minimalPreludeClasses
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o644))
	values, msgs = inferCached(t, dir, `val m = marker`)
	require.Empty(t, msgs)
	require.Equal(t, "string", values["m"])
}

// TestAmbientCheckerReportsLoadErrorAtImport asserts that an ambient package which
// loads with a diagnostic reports it at the import naming it, on a cached run as on
// a fresh one, and with no span when nothing imports it.
func TestAmbientCheckerReportsLoadErrorAtImport(t *testing.T) {
	dir := seedStdlib(t, withPreludeClasses(map[string]string{
		"std/math.esc": `export val PI: number = "pi"`,
	}))
	path := filepath.Join(dir, "std", "math.esc")
	loadErr := `package "std:math" (` + path + `) has 1 error(s):` + "\n" +
		`  cannot constrain "pi" <: number`
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "imported",
			src:  "import \"std:math\"\nval p = math.PI",
			want: []string{"1:1-1:18: " + loadErr},
		},
		{
			name: "not imported",
			src:  `val p = 1`,
			want: []string{"1:1-1:1: " + loadErr},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, fresh := inferFresh(t, dir, tt.src)
			require.Equal(t, tt.want, fresh)
			for range 2 {
				_, cached := inferCached(t, dir, tt.src)
				require.Equal(t, tt.want, cached)
			}
		})
	}
}

// inferCached infers src against dir through InferModuleAgainstStdlib, which starts
// from the cached checker. It returns the module's rendered value bindings and its
// diagnostics with their spans.
func inferCached(t *testing.T, dir, src string) (map[string]string, []string) {
	t.Helper()
	res := InferModuleAgainstStdlib(parseModule(t, src), dir, Options{})
	return renderedValues(res), messagesWithSpan(t, res.Errors)
}

// inferFresh is inferCached on a checker that loads the tree itself rather than one
// forked from the cache.
func inferFresh(t *testing.T, dir, src string) (map[string]string, []string) {
	t.Helper()
	module := parseModule(t, src)
	roots := append(pseudoPackageImportsOf(module), ambientPackages...)
	sort.Strings(roots)
	groups, err := BuildPackageClosure(dir, roots)
	require.NoError(t, err)
	res := inferModuleWithGroups(module, StdlibSource(dir), StdlibGroupSource(dir), groups, Options{})
	return renderedValues(res), messagesWithSpan(t, res.Errors)
}

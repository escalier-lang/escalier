package solver

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
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
			wantValues, wantMsgs := inferFresh(t, benchTree, tt.src, Options{})
			for range 2 {
				values, msgs := inferCached(t, benchTree, tt.src, Options{})
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
	values, msgs := inferCached(t, dir, `val m = marker`, Options{})
	require.Empty(t, msgs)
	require.Equal(t, "number", values["m"])

	path := filepath.Join(dir, "std", "prelude.esc")
	edited := "export declare val marker: string\n" + minimalPreludeClasses
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o644))
	values, msgs = inferCached(t, dir, `val m = marker`, Options{})
	require.Empty(t, msgs)
	require.Equal(t, "string", values["m"])
}

// TestAmbientCheckerReportsLoadErrorAtImport asserts that an ambient package which
// loads with a diagnostic reports it at the import naming it, on a cached run as on
// a fresh one, and with no span when nothing imports it.
func TestAmbientCheckerReportsLoadErrorAtImport(t *testing.T) {
	inferDir := seedStdlib(t, withPreludeClasses(map[string]string{
		"std/math.esc": `export val PI: number = "pi"`,
	}))
	inferErr := `package "std:math" (` + filepath.Join(inferDir, "std", "math.esc") +
		`) has 1 error(s):` + "\n" + `  cannot constrain "pi" <: number`
	jsDir := seedStdlib(t, withJsPreludeClasses(map[string]string{
		"std/math.esc": `export declare val PI: number`,
	}))
	jsErr := `exported value "PI" in pseudo-package file ` + filepath.Join(jsDir, "std", "math.esc") +
		" is missing an `@js(\"...\")` decorator"
	jsOpts := Options{CheckJsDecorators: true}

	cases := []struct {
		name string
		dir  string
		opts Options
		src  string
		want []string
	}{
		{
			name: "inference error imported",
			dir:  inferDir,
			src:  "import \"std:math\"\nval p = math.PI",
			want: []string{"1:1-1:18: " + inferErr},
		},
		{
			name: "inference error not imported",
			dir:  inferDir,
			src:  `val p = 1`,
			want: []string{"1:1-1:1: " + inferErr},
		},
		{
			name: "loader rule imported",
			dir:  jsDir,
			opts: jsOpts,
			src:  "import \"std:math\"\nval p = math.PI",
			want: []string{"1:1-1:18: " + jsErr},
		},
		{
			name: "loader rule not imported",
			dir:  jsDir,
			opts: jsOpts,
			src:  `val p = 1`,
			want: []string{"1:1-1:1: " + jsErr},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, fresh := inferFresh(t, tt.dir, tt.src, tt.opts)
			require.Equal(t, tt.want, fresh)
			for range 2 {
				_, cached := inferCached(t, tt.dir, tt.src, tt.opts)
				require.Equal(t, tt.want, cached)
			}
		})
	}
}

// inferCached infers src against dir through InferModuleAgainstStdlib, which starts
// from the cached checker. It returns the module's rendered value bindings and its
// diagnostics with their spans.
func inferCached(t *testing.T, dir, src string, opts Options) (map[string]string, []string) {
	t.Helper()
	res := InferModuleAgainstStdlib(parseModule(t, src), dir, opts)
	return renderedValues(res), messagesWithSpan(t, res.Errors)
}

// inferFresh is inferCached on a checker that loads the tree itself rather than one
// forked from the cache.
func inferFresh(t *testing.T, dir, src string, opts Options) (map[string]string, []string) {
	t.Helper()
	module := parseModule(t, src)
	roots := append(pseudoPackageImportsOf(module), ambientPackages...)
	sort.Strings(roots)
	groups, err := BuildPackageClosure(dir, roots)
	require.NoError(t, err)
	res := inferModuleWithGroups(module, StdlibSource(dir), StdlibGroupSource(dir), groups, opts)
	return renderedValues(res), messagesWithSpan(t, res.Errors)
}

// TestAmbientCheckerRunInfoHoldsOnlyTheModule asserts that a cached run's Info records
// types for the module's own nodes alone, none for the stdlib nodes the cached load
// inferred.
func TestAmbientCheckerRunInfoHoldsOnlyTheModule(t *testing.T) {
	module := parseModule(t, `val xs: Array<number> = [1, 2]`)
	res := InferModuleAgainstStdlib(module, benchTree, Options{})
	require.NotEmpty(t, res.Info.types)
	for node := range res.Info.types {
		require.Equal(t, module.Files[0].SourceID, node.Span().SourceID)
	}
}

func init() {
	// Every solver test that runs against a cached stdlib checker fails when it writes
	// to a variable the stdlib load minted.
	frozenVarChecks = true
}

// TestFixturesLeaveStdlibVarsUntouched asserts that checking each fixture's library
// against the committed tree writes to no variable the stdlib load minted.
// checkThawed panics on such a write, and the subtest reports the panic as its
// failure.
func TestFixturesLeaveStdlibVarsUntouched(t *testing.T) {
	paths, err := filepath.Glob("../../fixtures/*/lib")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, lib := range paths {
		t.Run(filepath.Base(filepath.Dir(lib)), func(t *testing.T) {
			srcs := map[string]string{}
			err := filepath.WalkDir(lib, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || filepath.Ext(path) != ".esc" {
					return err
				}
				contents, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(lib, path)
				srcs[filepath.ToSlash(rel)] = string(contents)
				return err
			})
			require.NoError(t, err)
			module, parseErrs := parser.ParseLibFiles(context.Background(), sourcesOf(srcs))
			if len(parseErrs) > 0 {
				t.Skip("the fixture's library does not parse")
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatal(r)
				}
			}()
			InferModuleAgainstStdlib(module, benchTree, Options{})
		})
	}
}

// sourcesOf returns srcs as sources numbered in path order.
func sourcesOf(srcs map[string]string) []*ast.Source {
	paths := make([]string, 0, len(srcs))
	for path := range srcs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	sources := make([]*ast.Source, len(paths))
	for id, path := range paths {
		sources[id] = &ast.Source{ID: id, Path: path, Contents: srcs[path]}
	}
	return sources
}

// TestCheckThawedRejectsStdlibState asserts that a run forked from the cached checker
// panics on work that would write to a variable or definition the stdlib load created.
func TestCheckThawedRejectsStdlibState(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	// Each case readies a write on run and returns the panic it expects with the write.
	cases := []struct {
		name  string
		ready func(run *checker) (want string, write func())
	}{
		{
			name: "type variable bound",
			ready: func(run *checker) (string, func()) {
				var v *soltype.TypeVarType
				for frozen := range run.ctx.frozenVars {
					if v == nil || frozen.ID < v.ID {
						v = frozen
					}
				}
				return fmt.Sprintf("a type bound wrote stdlib type variable %d", v.ID), func() {
					run.ctx.addUpperBound(v, &soltype.PrimType{Prim: soltype.NumPrim})
				}
			},
		},
		{
			name: "class variance",
			ready: func(run *checker) (string, func()) {
				def := run.ctx.classes[run.ctx.arrayClass]
				def.varianceProvisional = true
				def.varianceTriedAt = -1
				return "settleVariance wrote stdlib class " + def.varianceSelf, func() {
					run.ctx.settleVariance(def)
				}
			},
		},
		{
			name: "alias uniform knot",
			ready: func(run *checker) (string, func()) {
				name := slices.Min(slices.Collect(maps.Keys(run.ctx.aliases)))
				delete(run.ctx.uniformKnots, name)
				return "probing the uniform knot for stdlib alias " + name, func() {
					run.ctx.uniformKnotOf(name)
				}
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			run, ok := ambientChecker(benchTree, StdlibSource(benchTree), groups, Options{})
			require.True(t, ok)
			want, write := tt.ready(run)
			require.PanicsWithValue(t, want, write)
		})
	}
}

// TestAmbientCheckerSettlesLoadedDefinitions asserts that the cached checker holds no
// provisional class, and a memoized uniform knot for every alias.
func TestAmbientCheckerSettlesLoadedDefinitions(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	c := loadAmbientChecker(benchTree, groups, Options{})
	for name, def := range c.ctx.classes {
		require.False(t, def.varianceProvisional, name)
	}
	for name := range c.ctx.aliases {
		require.Contains(t, c.ctx.uniformKnots, name)
	}
}

// TestSettleLoadedDefinitionsMeasuresAProvisionalClass asserts that settling a loaded
// checker measures a provisional class again even when its last try was at the current
// count. A class left provisional holds Invariant throughout, which the measurement
// replaces with the variance its body gives.
func TestSettleLoadedDefinitionsMeasuresAProvisionalClass(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	c := loadAmbientChecker(benchTree, groups, Options{})
	def := c.ctx.classes[c.ctx.arrayClass]
	measured := slices.Clone(def.Variance)
	require.NotEqual(t, uniformVariance(len(measured), Invariant), measured)

	// The settling below is the load's own, which runs before the checker is frozen.
	c.ctx.frozenClasses = nil
	def.Variance = uniformVariance(len(measured), Invariant)
	def.varianceProvisional = true
	def.varianceTriedAt = c.ctx.measuredClasses
	c.definitionsSettled = false
	c.settleLoadedDefinitions()

	require.False(t, def.varianceProvisional)
	require.Equal(t, measured, def.Variance)
}

// TestAmbientCheckerServesRuns asserts that the cache serves the committed tree, so the
// tests comparing cached runs with fresh ones compare against a cached run rather than
// the fallback to a fresh load.
func TestAmbientCheckerServesRuns(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	source := StdlibSource(benchTree)

	first, ok := ambientChecker(benchTree, source, groups, Options{})
	require.True(t, ok)
	second, ok := ambientChecker(benchTree, source, groups, Options{})
	require.True(t, ok)
	require.NotSame(t, first, second)
	require.NotSame(t, first.ctx, second.ctx)
}

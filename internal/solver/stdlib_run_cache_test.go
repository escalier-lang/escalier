package solver

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
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
// share the stdlib's definitions and hold separate registries, so a class one run
// declares is not registered on the other.
func TestAmbientCheckerForksAreIndependent(t *testing.T) {
	first := InferModuleAgainstStdlib(parseModule(t, `class Mine {}`), benchTree, Options{})
	second := InferModuleAgainstStdlib(parseModule(t, `val b = 2`), benchTree, Options{})

	require.NotSame(t, first.checker.ctx, second.checker.ctx)
	firstArray, ok := first.checker.ctx.classDef(first.checker.ctx.arrayClass)
	require.True(t, ok)
	secondArray, ok := second.checker.ctx.classDef(second.checker.ctx.arrayClass)
	require.True(t, ok)
	require.Same(t, firstArray, secondArray)
	require.Equal(t, len(second.checker.ctx.classes)+1, len(first.checker.ctx.classes))
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
	for _, fixture := range fixtureLibraries(t) {
		t.Run(fixture.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatal(r)
				}
			}()
			InferModuleAgainstStdlib(fixture.parse(t), benchTree, Options{})
		})
	}
}

// TestForkLeavesCachedCheckerUnchanged asserts that inferring a fixture's library
// on a fork that shares a loaded checker changes nothing that checker holds. The
// checker is snapshotted once with a deep copy, and after each fixture every object the
// copy holds is compared with the object it was copied from. The first fixture that
// writes to the checker is the first to fail.
func TestForkLeavesCachedCheckerUnchanged(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	for _, opts := range []Options{{}, {NoAmbientNames: true}} {
		loaded := loadAmbientChecker(benchTree, groups, opts)
		g := newGraphCopier(isASTPointer)
		copyPtr(g, loaded)
		for _, fixture := range fixtureLibraries(t) {
			t.Run(fmt.Sprintf("%s/NoAmbientNames=%t", fixture.name, opts.NoAmbientNames), func(t *testing.T) {
				module := fixture.parse(t)
				if !loadsGroups(t, module, groups) {
					t.Skip("the fixture imports a package outside the ambient packages' closure")
				}
				inferModuleOn(loaded.fork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups), module)
				require.Empty(t, diffAgainstSnapshot(g))
			})
		}
	}
}

// TestForkMatchesDeepFork asserts that a fixture's library infers the same bindings and
// diagnostics on a fork that shares the loaded checker as on a deep copy of it. The
// shared forks run concurrently, so a race between them shows under -race.
func TestForkMatchesDeepFork(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	loaded := loadAmbientChecker(benchTree, groups, Options{})
	for _, fixture := range fixtureLibraries(t) {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			// Inference writes to the AST it walks, so each run reads its own parse.
			copiedModule, sharedModule := fixture.parse(t), fixture.parse(t)
			if !loadsGroups(t, copiedModule, groups) {
				t.Skip("the fixture imports a package outside the ambient packages' closure")
			}
			copied := inferModuleOn(loaded.deepFork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups), copiedModule)
			shared := inferModuleOn(loaded.fork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups), sharedModule)
			require.Equal(t, renderedValues(copied), renderedValues(shared))
			require.Equal(t, messagesWithSpan(t, copied.Errors), messagesWithSpan(t, shared.Errors))
		})
	}
}

// TestForkCopiesEveryContainer asserts that each map a loaded checker or its Context
// holds reaches a fork as a map of its own, apart from the read-only ones listed below,
// and that each slice reaches it with no spare capacity. A container field added later
// fails here until fork copies it or this test names it read-only.
func TestForkCopiesEveryContainer(t *testing.T) {
	groups, err := BuildPackageClosure(benchTree, ambientPackages)
	require.NoError(t, err)
	loaded := loadAmbientChecker(benchTree, groups, Options{})
	run := loaded.fork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups)

	readOnly := set.FromSlice([]string{
		// The run's own groups replace the loaded checker's.
		"checker.groups",
		// Written once while the prelude loads.
		"Context.wellKnownSymbolMembers",
		"Context.wellKnownSymbolTypes",
		// Read by the frozen-state checks alone.
		"Context.frozenVars",
		"Context.frozenLts",
		"Context.frozenClasses",
		"Context.frozenAliases",
	})
	var check func(path string, loadedValue, runValue reflect.Value)
	check = func(path string, loadedValue, runValue reflect.Value) {
		switch loadedValue.Kind() {
		case reflect.Map:
			if loadedValue.IsNil() || readOnly.Contains(path) {
				return
			}
			require.NotEqual(t, loadedValue.UnsafePointer(), runValue.UnsafePointer(), path)
		case reflect.Slice:
			require.Equal(t, runValue.Len(), runValue.Cap(), path)
		case reflect.Struct:
			for i := range loadedValue.NumField() {
				name := path + "." + loadedValue.Type().Field(i).Name
				check(name, loadedValue.Field(i), runValue.Field(i))
			}
		}
	}
	check("checker", reflect.ValueOf(loaded).Elem(), reflect.ValueOf(run).Elem())
	check("Context", reflect.ValueOf(loaded.ctx).Elem(), reflect.ValueOf(run.ctx).Elem())
	require.NotEqual(t, reflect.ValueOf(loaded.ctx.packages.packages).UnsafePointer(),
		reflect.ValueOf(run.ctx.packages.packages).UnsafePointer())
}

// loadsGroups reports whether module, checked against the committed tree, loads the
// packages groups names and no others.
func loadsGroups(t *testing.T, module *ast.Module, groups PackageGroups) bool {
	t.Helper()
	roots := set.FromSlice(append(pseudoPackageImportsOf(module), ambientPackages...)).ToSlice()
	sort.Strings(roots)
	own, err := BuildPackageClosure(benchTree, roots)
	require.NoError(t, err)
	return reflect.DeepEqual(own, groups)
}

// deepFork returns a deep copy of c that runs with source, groupSource, and groups, the
// baseline TestForkMatchesDeepFork compares fork against. AST nodes are shared.
func (c *checker) deepFork(source ModuleSource, groupSource GroupSource, groups PackageGroups) *checker {
	f := copyPtr(newGraphCopier(isASTPointer), c)
	f.ctx.fusionRecorder = f.recordFusionEdge
	f.source, f.groupSource, f.groups = source, groupSource, groups
	return f
}

type fixtureLibrary struct {
	name string
	srcs map[string]string
}

// parse returns a fresh parse of the library.
func (f fixtureLibrary) parse(t *testing.T) *ast.Module {
	t.Helper()
	module, parseErrs := parser.ParseLibFiles(context.Background(), sourcesOf(f.srcs))
	require.Empty(t, parseErrs)
	return module
}

// fixtureLibraries returns, in name order, the library of every fixture whose library
// parses. A fixture with parse errors is left out.
func fixtureLibraries(t *testing.T) []fixtureLibrary {
	t.Helper()
	libs, err := filepath.Glob("../../fixtures/*/lib")
	require.NoError(t, err)
	require.NotEmpty(t, libs)
	var out []fixtureLibrary
	for _, lib := range libs {
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
		if _, parseErrs := parser.ParseLibFiles(context.Background(), sourcesOf(srcs)); len(parseErrs) > 0 {
			continue
		}
		out = append(out, fixtureLibrary{name: filepath.Base(filepath.Dir(lib)), srcs: srcs})
	}
	return out
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

// TestCheckThawedRejectsStdlibState asserts that a run forked from a loaded checker
// panics on work that would write to a variable or definition the stdlib load created.
// Each case forks a checker loaded for it alone. The class case readies its write by
// changing a definition the loaded checker holds, which would reach every later run of
// the cache's.
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
			run := loadAmbientChecker(benchTree, groups, Options{}).fork(StdlibSource(benchTree), StdlibGroupSource(benchTree), groups)
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

// TestForkReportsHeldDiagnosticsPerRun asserts that each fork of one loaded
// checker reports the diagnostics its load held at that fork's own import, so a run
// that claims them leaves them unclaimed for the next.
func TestForkReportsHeldDiagnosticsPerRun(t *testing.T) {
	dir := seedStdlib(t, withPreludeClasses(map[string]string{
		"std/math.esc": `export val PI: number = "pi"`,
	}))
	groups, err := BuildPackageClosure(dir, ambientPackages)
	require.NoError(t, err)
	loaded := loadAmbientChecker(dir, groups, Options{})
	loadErr := `package "std:math" (` + filepath.Join(dir, "std", "math.esc") +
		`) has 1 error(s):` + "\n" + `  cannot constrain "pi" <: number`

	for range 2 {
		run := loaded.fork(StdlibSource(dir), StdlibGroupSource(dir), groups)
		res := inferModuleOn(run, parseModule(t, "import \"std:math\"\nval p = math.PI"))
		require.Equal(t, []string{"1:1-1:18: " + loadErr}, messagesWithSpan(t, res.Errors))
	}
}

func TestClipValues(t *testing.T) {
	require.Nil(t, clipValues[string, int](nil))

	roomy := make([]int, 1, 4)
	roomy[0] = 7
	clipped := clipValues(map[string][]int{"a": roomy})
	require.Equal(t, map[string][]int{"a": {7}}, clipped)
	require.Equal(t, 1, cap(clipped["a"]))
}

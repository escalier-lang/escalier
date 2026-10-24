package solver

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// stdlib_load_bench_test.go measures what reading the pseudo-packages a program
// imports costs one inference run against the committed tree.
//
// It drives InferModuleAgainstStdlib, the entry point a run with a stdlib
// directory uses. InferModuleWithSource would load each package on its own and
// measure a path no such run takes.
//
// Warm is the figure to watch, since a language server pays it on every
// re-inference. A run whose closure the cache in stdlib_run_cache.go already holds
// reads the loaded packages rather than inferring them, so Warm measures the rest of
// the run.
//
// Cold adds reading the import graph, parsing the files, and inferring the packages,
// which a process pays once per distinct tree because of the caches in
// stdlib_closure.go, stdlib_parse_cache.go, and stdlib_run_cache.go. It is what a
// language server pays at startup.
//
// Run with:
//
//	go test ./internal/solver/ -run '^$' -bench StdlibClosureLoad -benchtime 10x
//
// The committed tree carries known diagnostics, which the benchmark neither
// asserts on nor is affected by. Reporting one costs nothing against inferring
// the declaration that produced it.

// benchTree is the committed tree, relative to this package's directory.
const benchTree = "../interop/data"

func BenchmarkStdlibClosureLoad(b *testing.B) {
	cases := []struct {
		name string
		uris []string
	}{
		// What every program pays, since the ambient packages load whether or not
		// anything imports them.
		{"NoImports", nil},
		// What a program importing one package pays. Its closure is `web:fetch`
		// and what that reaches, not the whole tree.
		{"OnePackage", []string{"web:fetch"}},
		// The upper bound, and what a program touching every area would pay.
		{"EveryPackage", committedPackageURIs(b)},
	}

	for _, tt := range cases {
		module := benchEntryModule(b, tt.uris)

		b.Run(tt.name+"/Warm", func(b *testing.B) {
			// Prime the caches, so the loop measures what a run pays once the tree is
			// loaded.
			InferModuleAgainstStdlib(module, benchTree, Options{})
			b.ResetTimer()
			for range b.N {
				InferModuleAgainstStdlib(module, benchTree, Options{})
			}
		})

		b.Run(tt.name+"/Cold", func(b *testing.B) {
			for range b.N {
				b.StopTimer()
				restore := emptyTheStdlibCaches()
				b.StartTimer()

				InferModuleAgainstStdlib(module, benchTree, Options{})

				b.StopTimer()
				restore()
				b.StartTimer()
			}
		})
	}
}

// committedPackageURIs returns every pseudo-package the committed tree holds a
// file for, sorted.
//
// `std:prelude` is left out. It is ambient, so a program reaches it without an
// import and naming it in one would not be how any program is written.
func committedPackageURIs(b *testing.B) []string {
	b.Helper()
	var uris []string
	for _, scheme := range []string{"std", "web"} {
		paths, err := filepath.Glob(filepath.Join(benchTree, scheme, "*.esc"))
		require.NoError(b, err)
		for _, path := range paths {
			name := strings.TrimSuffix(filepath.Base(path), ".esc")
			if scheme == "std" && name == "prelude" {
				continue
			}
			uris = append(uris, scheme+":"+name)
		}
	}
	sort.Strings(uris)
	require.NotEmpty(b, uris, "the committed tree holds no package files")
	return uris
}

// benchEntryModule parses a module importing each uri, which is what drives the
// load. Its own body is one declaration, so what the benchmark times is the
// pseudo-packages rather than the entry module.
func benchEntryModule(b *testing.B, uris []string) *ast.Module {
	b.Helper()
	lines := make([]string, 0, len(uris)+1)
	for _, uri := range uris {
		lines = append(lines, fmt.Sprintf("import %q", uri))
	}
	lines = append(lines, "val x: number = 1")

	ctx := context.Background()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{{
		ID: 0, Path: "input.esc", Contents: strings.Join(lines, "\n") + "\n",
	}})
	require.Empty(b, parseErrs)
	return module
}

// emptyTheStdlibCaches swaps in a parse cache, an import graph cache, and a checker
// cache holding nothing, and returns a function restoring the ones the process was
// using.
//
// A cold load is the first in a process. Source ids restart from the base, which
// the swapped-in parse cache alone hands out, so no id it issues reaches an entry
// the restored cache holds.
func emptyTheStdlibCaches() func() {
	prevParses, prevGraphs := stdlibParses, packageGraphs
	stdlibParses = newParsedModuleCache(stdlibSourceIDBase)
	packageGraphs = newPackageGraphCache()

	ambientCheckers.mu.Lock()
	prevEntries, prevOrder := ambientCheckers.entries, ambientCheckers.order
	ambientCheckers.entries, ambientCheckers.order = map[ambientKey]*ambientEntry{}, nil
	ambientCheckers.mu.Unlock()

	return func() {
		stdlibParses, packageGraphs = prevParses, prevGraphs
		ambientCheckers.mu.Lock()
		ambientCheckers.entries, ambientCheckers.order = prevEntries, prevOrder
		ambientCheckers.mu.Unlock()
	}
}

package dts_to_esc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// committedTree is the generated tree this test reads, relative to this
// package's directory.
const committedTree = "../interop/data"

// declaringPackage maps each exported top-level name in the committed tree to
// the package URI that declares it.
//
// A name declared by two packages fails rather than resolving by iteration
// order. The partition already rejects a symbol listed twice, so a duplicate
// here means two packages generated the same name by other means.
func declaringPackage(t *testing.T, modules map[string]*ast.Module) map[string]string {
	t.Helper()
	owner := map[string]string{}
	for _, uri := range PackageList() {
		module, held := modules[uri]
		if !held {
			continue
		}
		names := DeclaredNames(module).ToSlice()
		sort.Strings(names)
		for _, name := range names {
			if first, dup := owner[name]; dup {
				t.Fatalf("%s is declared by both %s and %s", name, first, uri)
			}
			owner[name] = uri
		}
	}
	return owner
}

// parseCommittedTree parses every generated package, keyed by package URI.
func parseCommittedTree(t *testing.T) map[string]*ast.Module {
	t.Helper()
	modules := map[string]*ast.Module{}
	id := 0
	for _, uri := range PackageList() {
		pkg, ok := PackageForURI(uri)
		require.True(t, ok)
		path := filepath.Join(committedTree, filepath.FromSlash(pkg.File))
		contents, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			require.True(t, unroutedPackages.Contains(uri),
				"%s has no committed file; a package the tier checks cannot read is a "+
					"package whose references go unchecked", uri)
			continue
		}
		require.NoError(t, err)
		id++
		module, parseErrs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
			ID: id, Path: filepath.Base(path), Contents: string(contents),
		}})
		require.Empty(t, parseErrs, "%s does not parse", pkg.File)
		modules[uri] = module
	}
	require.Len(t, modules, len(PackageList())-unroutedPackages.Len(),
		"every routed package has a committed file")
	return modules
}

// unroutedPackages are partition entries the pinned `.d.ts` set routes nothing
// to, so `generate` writes no file for them. Each is listed by name, so a
// package that stops generating for any other reason fails rather than being
// skipped.
var unroutedPackages = set.FromSlice([]string{
	// Temporal is not in the pinned TypeScript lib set. The entry reserves the
	// package name for the version that ships it.
	"std:temporal",
})

// importBindings returns the names a module's own import header binds. After
// qualification a reference reads `set.Set`, so `set` appears among the
// collected names as the head. It is a binding rather than a type name, and
// resolving it against what packages declare would find the unrelated `set`
// that `std:reflect` exports.
func importBindings(module *ast.Module) set.Set[string] {
	bindings := set.NewSet[string]()
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			bindings.Add(ast.DeriveImportName(stmt.PackageName))
		}
	}
	return bindings
}

// No declaration in the committed tree names something absent from an
// environment it claims.
//
// This is what the annotations buy, and it is the property the tier order used
// to carry: a file checked against a worker is checkable as long as nothing it
// reaches is a window's alone.
//
// The generator runs the same check over what it is about to write. This reads
// the committed files, so a hand-edit to a generated package fails here rather
// than surviving until the next regeneration.
func TestNoReferenceEscapesItsEnvironments(t *testing.T) {
	modules := parseCommittedTree(t)
	mods := make(map[string]*StandaloneModule, len(modules))
	for uri, module := range modules {
		mods[uri] = &StandaloneModule{Module: module}
	}

	violations, err := CheckEnvs(mods)
	require.NoError(t, err)

	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, v.String())
	}
	require.Empty(t, lines, "references escaping their environments:\n  %s",
		strings.Join(lines, "\n  "))
}

// `web:core` refers to nothing outside itself and the `std:*` surface, which is
// the property that lets a runtime with no DOM load it.
//
// It survives the tier order because it is a statement about one package rather
// than about a rank. Every other package's reach is answered by its
// annotations, and this one is answered by naming the package.
func TestTheCorePackageIsSelfContained(t *testing.T) {
	modules := parseCommittedTree(t)
	owner := declaringPackage(t, modules)

	module, held := modules[coreURI]
	require.True(t, held, "%s has no committed file", coreURI)

	var outside []string
	bindings := importBindings(module)
	for _, name := range TypeRefNames(module).ToSlice() {
		declaredIn, known := owner[name]
		if !known || declaredIn == coreURI || bindings.Contains(name) {
			continue
		}
		if SchemeOf(declaredIn) == "std" {
			continue
		}
		outside = append(outside, fmt.Sprintf("%s -> %s", name, declaredIn))
	}
	sort.Strings(outside)
	require.Empty(t, outside, "%s refers outside itself and the language surface:\n  %s",
		coreURI, strings.Join(outside, "\n  "))
}

// Every import line in the committed tree names a package the partition holds.
//
// This reads the written headers rather than the references behind them, so it
// catches a hand-edit to a generated file that the reference graph would not
// see. #1403 item 6.
func TestEveryCommittedImportNamesAKnownPackage(t *testing.T) {
	modules := parseCommittedTree(t)

	for uri, module := range modules {
		for _, file := range module.Files {
			for _, stmt := range file.Imports {
				_, held := PackageForURI(stmt.PackageName)
				require.True(t, held,
					"%s imports %s, which the partition does not hold", uri, stmt.PackageName)
			}
		}
	}
}

// Every package a file references is one it imports. A reference with no import
// resolves nothing, which is the state the whole tree was in before #1403.
func TestEveryCrossPackageReferenceIsImported(t *testing.T) {
	modules := parseCommittedTree(t)
	owner := declaringPackage(t, modules)

	var missing []string
	for _, uri := range PackageList() {
		module, held := modules[uri]
		if !held {
			continue
		}
		imported := set.NewSet[string]()
		for _, file := range module.Files {
			for _, stmt := range file.Imports {
				imported.Add(stmt.PackageName)
			}
		}
		bindings := importBindings(module)
		for _, name := range TypeRefNames(module).ToSlice() {
			declaredIn, known := owner[name]
			// A prelude name is ambient. The solver copies the prelude's exports
			// into the scope every package's inference descends from, so reaching
			// one takes no import and no qualifier.
			if !known || declaredIn == uri || declaredIn == preludeURI ||
				imported.Contains(declaredIn) || bindings.Contains(name) {
				continue
			}
			missing = append(missing, fmt.Sprintf("%s names %s from %s without importing it",
				uri, name, declaredIn))
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "references with no import:\n  %s", strings.Join(missing, "\n  "))
}

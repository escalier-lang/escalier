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

// knownUpwardRefs records every reference in the committed tree that names a
// package above the referring package's tier. Each is a declaration the
// pinned `.d.ts` types against a browser type that the portable runtimes
// implement differently or not at all, so each is answered by an overlay
// `replace` rather than by moving a package between tiers.
//
// The list is empty for the core tier, which is what makes `web:core`
// loadable on a runtime that has no DOM. Emptying it for the portable tier
// is #1403 item 5, which is also where the check that forbids these edges
// lands. Until then this test pins the set so a new one is caught.
var knownUpwardRefs = map[string][]string{
	// Node's FormData has no HTMLFormElement constructor, and
	// XMLHttpRequestBodyInit names one transitively.
	"web:fetch": {"FormData", "XMLHttpRequestBodyInit"},
	// FileReader fires progress events, and Node 22 defines neither it nor
	// ProgressEvent. Both leave the portable tier together, or neither does.
	"web:file": {"ProgressEvent"},
	// EventCounts is the performance-timeline map keyed by DOM event names.
	"web:performance": {"EventCounts"},
	// URL.createObjectURL takes a MediaSource in the browser and a Blob
	// everywhere else.
	"web:url": {"MediaSource"},
	// MessageEvent.source is a WindowProxy or ServiceWorker in the browser
	// and always null off it. BinaryType is the socket's payload mode.
	"web:websocket": {"BinaryType", "MessageEvent"},
}

// declaringPackage maps each exported top-level name in the committed tree to
// the package URI that declares it.
//
// A name declared by two packages fails rather than resolving to whichever the
// walk reached last. The partition rejects a symbol listed twice, so a
// duplicate here means two packages generated the same name by other means,
// and every tier answer about that name would depend on iteration order.
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

// No package refers to a name declared in a package above its own tier,
// except for the references knownUpwardRefs records.
//
// This is what the tier order buys. A file importing only the portable tier
// is checkable against Node, which holds exactly as long as no portable
// declaration names a browser type.
func TestNoReferenceGoesUpATier(t *testing.T) {
	modules := parseCommittedTree(t)
	owner := declaringPackage(t, modules)

	var upward []string
	for _, uri := range PackageList() {
		module, held := modules[uri]
		if !held {
			require.True(t, unroutedPackages.Contains(uri), "%s has no committed file", uri)
			continue
		}
		tier, ok := TierOf(uri)
		require.True(t, ok, "%s has no tier", uri)

		allowed := set.FromSlice(knownUpwardRefs[uri])
		bindings := importBindings(module)
		for _, name := range TypeRefNames(module).ToSlice() {
			declaredIn, known := owner[name]
			if !known || declaredIn == uri || bindings.Contains(name) {
				continue
			}
			refTier, ok := TierOf(declaredIn)
			if !ok || refTier <= tier || allowed.Contains(name) {
				continue
			}
			upward = append(upward, fmt.Sprintf("%s (%s) -> %s (%s) via %s",
				uri, tier, declaredIn, refTier, name))
		}
	}
	sort.Strings(upward)
	require.Empty(t, upward, "references going up a tier:\n  %s", strings.Join(upward, "\n  "))
}

// Every name knownUpwardRefs excuses is still an upward reference. An entry
// left behind after the overlay that answers it lands would silently excuse a
// reference that came back.
func TestKnownUpwardRefsAreAllStillUpward(t *testing.T) {
	modules := parseCommittedTree(t)
	owner := declaringPackage(t, modules)

	for uri, names := range knownUpwardRefs {
		module, held := modules[uri]
		require.True(t, held, "%s names no committed package", uri)
		tier, ok := TierOf(uri)
		require.True(t, ok)
		refs := TypeRefNames(module)

		for _, name := range names {
			require.True(t, refs.Contains(name), "%s no longer refers to %s", uri, name)
			declaredIn, known := owner[name]
			require.True(t, known, "%s is declared by no package", name)
			refTier, ok := TierOf(declaredIn)
			require.True(t, ok)
			require.Greater(t, refTier, tier,
				"%s is in %s, which is not above %s's %s tier", name, refTier, uri, tier)
		}
	}
}

// The core tier refers to nothing outside itself and the language tier, which
// is the property that lets a runtime with no DOM load it.
func TestTheCoreTierIsSelfContained(t *testing.T) {
	modules := parseCommittedTree(t)
	owner := declaringPackage(t, modules)

	for _, uri := range PackagesInTier(TierCore) {
		module, held := modules[uri]
		require.True(t, held, "%s has no committed file", uri)
		require.Empty(t, knownUpwardRefs[uri], "the core tier allows no exception")

		var outside []string
		bindings := importBindings(module)
		for _, name := range TypeRefNames(module).ToSlice() {
			declaredIn, known := owner[name]
			if !known || declaredIn == uri || bindings.Contains(name) {
				continue
			}
			if tier, ok := TierOf(declaredIn); ok && tier <= TierCore {
				continue
			}
			outside = append(outside, fmt.Sprintf("%s -> %s", name, declaredIn))
		}
		sort.Strings(outside)
		require.Empty(t, outside, "%s refers outside the core tier:\n  %s",
			uri, strings.Join(outside, "\n  "))
	}
}

// Every import line in the committed tree names a package the partition holds,
// and no line goes up a tier except the ones AcceptedUpwardEdges records.
//
// This reads the written headers rather than the references behind them, so it
// catches a hand-edit to a generated file that the reference graph would not
// see. #1403 item 6.
func TestEveryCommittedImportRespectsTheTiers(t *testing.T) {
	modules := parseCommittedTree(t)

	var upward []string
	for uri, module := range modules {
		tier, ok := TierOf(uri)
		require.True(t, ok, "%s has no tier", uri)
		accepted := set.FromSlice(AcceptedUpwardEdges[uri])

		for _, file := range module.Files {
			for _, stmt := range file.Imports {
				target := stmt.PackageName
				_, held := PackageForURI(target)
				require.True(t, held, "%s imports %s, which the partition does not hold", uri, target)

				targetTier, ok := TierOf(target)
				require.True(t, ok, "%s has no tier", target)
				if targetTier <= tier {
					continue
				}
				// An accepted edge is one whose every forcing reference is
				// recorded. Checking that the importer has any accepted entry
				// would excuse a second, unrelated upward import from it.
				if acceptedEdgeTarget(t, modules, uri, target, accepted) {
					continue
				}
				upward = append(upward, fmt.Sprintf("%s (%s) imports %s (%s)",
					uri, tier, target, targetTier))
			}
		}
	}
	sort.Strings(upward)
	require.Empty(t, upward, "import lines going up a tier:\n  %s", strings.Join(upward, "\n  "))
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
			if !known || declaredIn == uri || imported.Contains(declaredIn) || bindings.Contains(name) {
				continue
			}
			missing = append(missing, fmt.Sprintf("%s names %s from %s without importing it",
				uri, name, declaredIn))
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "references with no import:\n  %s", strings.Join(missing, "\n  "))
}

// acceptedEdgeTarget reports whether every reference forcing the edge from uri
// to target is one AcceptedUpwardEdges records for uri.
func acceptedEdgeTarget(t *testing.T, modules map[string]*ast.Module, uri, target string, accepted set.Set[string]) bool {
	t.Helper()
	owner := declaringPackage(t, modules)
	forcing := 0
	for _, name := range TypeRefNames(modules[uri]).ToSlice() {
		if owner[name] != target {
			continue
		}
		forcing++
		if !accepted.Contains(name) {
			return false
		}
	}
	return forcing > 0
}

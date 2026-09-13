package solver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_scc.go groups the pseudo-packages a stdlib directory holds into the
// sets that have to load together.
//
// Two packages naming each other cannot load one at a time: whichever goes
// first reaches a name the other has not declared yet. They load as one merged
// module instead, which is what lets the dep graph's own ordering resolve the
// references across them.
//
// A cycle is permitted inside a tier and refused across one. The browser tier
// is mutually recursive for real reasons — `web:webgl` takes dom's
// `HTMLCanvasElement` as a texture source while dom's `getContext` hands back a
// `WebGLRenderingContext` — so refusing every cycle would refuse the tree. A
// cycle that crosses a tier is a different thing: it would make a browser
// declaration reachable from a package claiming to run on Node, so it is
// refused even though loading it would succeed.

// sccSchemes are the schemes a cycle may run through. `node:` is reserved and
// populated by nothing, so it contributes no packages and no edges.
var sccSchemes = []string{"std", "web"}

// PackageGroups maps each pseudo-package URI in one stdlib directory to the
// group it loads with, sorted. A package in no cycle maps to itself alone.
type PackageGroups map[string][]string

// GroupOf returns the group a URI loads with, and false for a URI the directory
// does not hold.
func (g PackageGroups) GroupOf(uri string) ([]string, bool) {
	group, ok := g[uri]
	return group, ok
}

// CrossTierCycleError reports a group whose members do not share a tier.
//
// The generated tree cannot produce one: `generate` refuses an import edge that
// goes up a tier, so no cycle it writes can span two. A hand-written stdlib
// directory can, and this is what stops one from smuggling a browser reference
// into the portable tier and having it resolve.
type CrossTierCycleError struct {
	// Members are the group's URIs, sorted.
	Members []string
	// Tiers names each member's tier, in the same order as Members.
	Tiers []string
	span  ast.Span
}

func (e *CrossTierCycleError) Message() string {
	pairs := make([]string, len(e.Members))
	for i, uri := range e.Members {
		pairs[i] = fmt.Sprintf("%s (%s)", uri, e.Tiers[i])
	}
	return fmt.Sprintf(
		"import cycle spans more than one runtime tier: %s; a cycle may not cross a tier, "+
			"since every member of one loads whenever any member does",
		strings.Join(pairs, ", "))
}
func (e *CrossTierCycleError) Span() ast.Span      { return e.span }
func (e *CrossTierCycleError) Related() []ast.Span { return nil }
func (e *CrossTierCycleError) isSolverError()      {}

// BuildPackageGroups scans dir for pseudo-packages, reads each one's import
// header, and condenses the resulting graph into the groups that load together.
func BuildPackageGroups(dir string) (PackageGroups, error) {
	edges, err := buildPackageGraph(dir)
	if err != nil {
		return nil, err
	}
	groups := PackageGroups{}
	for _, scc := range condense(edges) {
		sort.Strings(scc)
		for _, uri := range scc {
			groups[uri] = scc
		}
	}
	return groups, nil
}

// buildPackageGraph reads every `.esc` file under dir's scheme subdirectories
// and returns the URIs each one imports.
func buildPackageGraph(dir string) (map[string][]string, error) {
	edges := map[string][]string{}
	for _, scheme := range sccSchemes {
		schemeDir := filepath.Join(dir, scheme)
		entries, err := os.ReadDir(schemeDir)
		if err != nil {
			// A scheme with no directory contributes no packages, which is what a
			// tree holding only `std/` looks like.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot scan %s: %w", schemeDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".esc") {
				continue
			}
			uri := scheme + ":" + strings.TrimSuffix(entry.Name(), ".esc")
			imports, err := readPackageImports(filepath.Join(schemeDir, entry.Name()))
			if err != nil {
				return nil, err
			}
			edges[uri] = imports
		}
	}
	return edges, nil
}

// readPackageImports returns the pseudo-package URIs one file imports.
//
// A parse error is ignored. This pass reads import statements alone, and the
// same file is parsed again when it actually loads, where an error is reported
// against the importing statement with its span. An error before the last
// import can truncate the list and put the file in a smaller group than it
// belongs to, which changes the shape of the failure rather than hiding it:
// the file does not load either way.
func readPackageImports(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	module, _ := parser.ParseLibFiles(context.Background(), []*ast.Source{{
		ID: 0, Path: filepath.Base(path), Contents: string(contents),
	}})

	var imports []string
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			scheme, _, ok := splitScheme(stmt.PackageName)
			if ok && slices.Contains(sccSchemes, scheme) {
				imports = append(imports, stmt.PackageName)
			}
		}
	}
	return imports, nil
}

// condense runs the shared strongly-connected-components pass over the graph. A
// URI reached only as an import target, never as a key, comes back as a group
// of its own.
func condense(edges map[string][]string) [][]string {
	nodes := set.NewSet[string]()
	for from, targets := range edges {
		nodes.Add(from)
		for _, to := range targets {
			nodes.Add(to)
		}
	}
	sorted := nodes.ToSlice()
	sort.Strings(sorted)
	return graph.StronglyConnectedComponents(sorted, func(uri string) []string {
		return edges[uri]
	})
}

// CheckGroupTiers returns an error for each group whose members span more than
// one tier. A URI with no tier is skipped: a directory a test wrote holds
// packages the partition never heard of, and refusing those would make the
// check about the partition rather than about tiers.
func CheckGroupTiers(groups PackageGroups, span ast.Span) []SolverError {
	var errs []SolverError
	seen := set.NewSet[string]()
	for _, uri := range sortedKeys(groups) {
		group := groups[uri]
		key := strings.Join(group, ",")
		if len(group) < 2 || seen.Contains(key) {
			continue
		}
		seen.Add(key)

		tiers := make([]string, 0, len(group))
		distinct := set.NewSet[string]()
		for _, member := range group {
			tier, ok := dts_to_esc.TierOf(member)
			if !ok {
				tiers = nil
				break
			}
			tiers = append(tiers, tier.String())
			distinct.Add(tier.String())
		}
		if tiers == nil || distinct.Len() < 2 {
			continue
		}
		errs = append(errs, &CrossTierCycleError{Members: group, Tiers: tiers, span: span})
	}
	return errs
}

func sortedKeys(groups PackageGroups) []string {
	keys := make([]string, 0, len(groups))
	for uri := range groups {
		keys = append(keys, uri)
	}
	sort.Strings(keys)
	return keys
}

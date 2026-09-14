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
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_closure.go works out which pseudo-packages a module's imports reach,
// so they can be read before any of them is inferred.
//
// Loading one package at a time cannot resolve a cycle: whichever goes first
// reaches a name the other has not declared yet. Reading the whole closure
// first and inferring it as one module lets the dep graph's own ordering
// resolve every reference, however the packages refer to each other.
//
// Nothing about a cycle needs refusing. A pseudo-package declares and runs
// nothing, so it has no initialization order to get wrong, and the dep graph
// already allows a cycle between declarations that build nothing.
//
// The closure is what a module's imports reach, not the whole directory. A
// program importing `web:fetch` reads that package and what it names, and a
// browser package it never mentions is neither read nor inferred.

// closureSchemes are the schemes a pseudo-package import may name. `node:` is
// reserved and populated by nothing, so it contributes no packages and no edges.
var closureSchemes = []string{"std", "web"}

// PackageGroups maps each pseudo-package a module's imports reach to the whole
// set they load with, sorted. Every reachable URI maps to the same set.
type PackageGroups map[string][]string

// GroupOf returns the set a URI loads with, and false for a URI the module's
// imports do not reach.
func (g PackageGroups) GroupOf(uri string) ([]string, bool) {
	group, ok := g[uri]
	return group, ok
}

// BuildPackageClosure scans dir for pseudo-packages, reads each one's import
// header, and returns everything the roots reach.
//
// A root naming a package the directory does not hold contributes nothing. The
// load reports that URI with a span, which is where the reader can act on it.
func BuildPackageClosure(dir string, roots []string) (PackageGroups, error) {
	edges, err := buildPackageGraph(dir)
	if err != nil {
		return nil, err
	}
	closure := reachable(edges, roots)
	groups := PackageGroups{}
	for _, uri := range closure {
		groups[uri] = closure
	}
	return groups, nil
}

// reachable returns every URI the roots reach through edges, sorted, roots
// included.
func reachable(edges map[string][]string, roots []string) []string {
	seen := set.NewSet[string]()
	stack := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, held := edges[root]; held {
			stack = append(stack, root)
		}
	}
	for len(stack) > 0 {
		uri := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen.Contains(uri) {
			continue
		}
		seen.Add(uri)
		for _, next := range edges[uri] {
			if _, held := edges[next]; held && !seen.Contains(next) {
				stack = append(stack, next)
			}
		}
	}
	out := seen.ToSlice()
	sort.Strings(out)
	return out
}

// buildPackageGraph reads every `.esc` file under dir's scheme subdirectories
// and returns the URIs each one imports.
func buildPackageGraph(dir string) (map[string][]string, error) {
	edges := map[string][]string{}
	for _, scheme := range closureSchemes {
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
// The parse goes through the shared cache, under the same basename and contents
// StdlibSource parses with. The closure is built from the same files the load
// then reads, so each is parsed once and both steps use that one result.
//
// A parse error is ignored, since the same file is parsed again when it loads
// and the error is reported there with a span. Truncating the import list puts
// the file in a smaller closure than it belongs to, which changes the shape of
// the failure rather than hiding it.
func readPackageImports(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	base := filepath.Base(path)
	module, err := stdlibParses.get(base, string(contents), func(sourceID int) (*ast.Module, error) {
		parsed, parseErrs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
			ID: sourceID, Path: base, Contents: string(contents),
		}})
		if len(parseErrs) > 0 {
			return nil, fmt.Errorf("parse errors in %s", path)
		}
		return parsed, nil
	})
	if err != nil {
		return nil, nil
	}

	var imports []string
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			scheme, _, ok := splitScheme(stmt.PackageName)
			if ok && slices.Contains(closureSchemes, scheme) {
				imports = append(imports, stmt.PackageName)
			}
		}
	}
	return imports, nil
}

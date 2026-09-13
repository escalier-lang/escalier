package solver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_group_load.go loads a group of mutually-importing pseudo-packages as
// one module.
//
// The members do not flatten into one namespace. Each parses under the
// synthetic path `<pkg>/index.esc`, so `deriveNamespaceFromPath` yields `<pkg>`
// and that member's declarations land in their own `Namespaces` entry, which is
// also the dep graph's binding-key prefix. A member reaches a sibling as
// `<pkg>.<name>`, the same spelling an importer writes. The file on disk stays
// flat at `<scheme>/<pkg>.esc`; the synthetic path only steers where the
// declarations land.

// GroupSource reads every member of a package group and returns them as one
// module, plus the path each member was read from.
type GroupSource func(uris []string) (module *ast.Module, paths map[string]string, err error)

// StdlibGroupSource returns a GroupSource reading pseudo-packages from dir.
func StdlibGroupSource(dir string) GroupSource {
	nextSourceID := stdlibSourceIDBase + groupSourceIDOffset
	return func(uris []string) (*ast.Module, map[string]string, error) {
		sources := make([]*ast.Source, 0, len(uris))
		paths := make(map[string]string, len(uris))
		for _, uri := range uris {
			path, err := resolveStdlibPath(dir, uri)
			if err != nil {
				return nil, nil, err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("reading %s: %w", path, err)
			}
			_, pkg, _ := splitScheme(uri)
			paths[uri] = path
			sources = append(sources, &ast.Source{
				ID: nextSourceID,
				// `<pkg>/index.esc`, so the member's declarations land under `<pkg>`
				// rather than at the top level where they would collide.
				Path:     filepath.Join(pkg, "index.esc"),
				Contents: string(contents),
			})
			nextSourceID++
		}
		module, parseErrs := parser.ParseLibFiles(context.Background(), sources)
		if len(parseErrs) > 0 {
			messages := make([]string, 0, len(parseErrs))
			for _, pe := range parseErrs {
				messages = append(messages, pe.String())
			}
			return nil, nil, fmt.Errorf("parse errors in %s: %s",
				strings.Join(uris, ", "), strings.Join(messages, "; "))
		}
		return module, paths, nil
	}
}

// groupSourceIDOffset keeps a merged load's source ids clear of the ones the
// single-package source hands out, so a span from one never reads as a span
// from the other.
const groupSourceIDOffset = 1 << 16

// loadPackageGroup infers every member of a group as one module and publishes a
// namespace per member.
//
// All members are marked loading before inference and all are published after,
// so a lookup from outside the group during the load sees the cycle sentinel
// rather than an empty surface. A member reached from inside the group resolves
// through the merged module scope instead and never reaches the registry.
func (c *checker) loadPackageGroup(group []string, span ast.Span) []SolverError {
	if c.groupSource == nil {
		return []SolverError{&UnresolvedPackageError{
			URI:    group[0],
			Reason: "this inference run was given no group source",
			span:   span,
		}}
	}

	module, paths, err := c.groupSource(group)
	if err != nil {
		return []SolverError{&UnresolvedPackageError{
			URI: group[0], Reason: err.Error(), span: span,
		}}
	}

	for _, uri := range group {
		c.packages.markLoading(uri, paths[uri])
	}
	c.loadStack = append(c.loadStack, group...)

	prevURI := c.pkgURI
	c.pkgURI = c.groupKeyURI(group)
	prevErrs := c.errs
	c.errs = nil

	prevGroup := c.activeGroup
	c.activeGroup = set.FromSlice(group)

	scope := c.preludeScope().Child()
	c.bindFileImports(scope, module)
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))

	errs := c.errs
	c.errs = prevErrs
	c.pkgURI = prevURI
	c.activeGroup = prevGroup
	c.loadStack = c.loadStack[:len(c.loadStack)-len(group)]

	// One surface for the whole module, then split by the namespace each member's
	// declarations landed under. exportedSurface has already dropped every
	// unexported declaration, so what a member publishes is its public half.
	//
	// The URI is the one inference ran under, since a type registers under a key
	// carrying it and exportedSurface reads that key back. Every member of a
	// group registers under the same one.
	merged := exportedSurface(c.groupKeyURI(group), module, scope)
	for _, uri := range group {
		_, pkg, _ := splitScheme(uri)
		member, held := merged.Nested[pkg]
		if !held {
			member = newNamespace(uri)
		}
		member.Name = uri
		c.packages.publish(uri, member)
	}

	if len(errs) > 0 {
		return []SolverError{&PackageInferenceError{
			URI:      strings.Join(group, ", "),
			Path:     paths[group[0]],
			Messages: messagesOf(errs),
			span:     span,
		}}
	}
	return nil
}

// sortedGroup returns a copy of group in sorted order, so a diagnostic and a
// publish order do not depend on how the caller assembled it.
func sortedGroup(group []string) []string {
	out := append([]string(nil), group...)
	sort.Strings(out)
	return out
}

// InferModuleAgainstStdlib infers module against the pseudo-packages in dir.
//
// It is the entry point a run with a stdlib directory uses, rather than
// InferModuleWithSource, because loading a package needs two things the
// directory supplies and a bare ModuleSource cannot: the groups that have to
// load together, and a reader for a whole group at once.
//
// A group spanning more than one tier is refused before anything loads, so the
// diagnostic names the cycle rather than whatever the first member happened to
// fail on.
func InferModuleAgainstStdlib(module *ast.Module, dir string) *ModuleResult {
	groups, err := BuildPackageGroups(dir)
	if err != nil {
		result := InferModuleWithSource(module, StdlibSource(dir))
		result.Errors = append(result.Errors, &UnresolvedPackageError{
			URI: dir, Reason: err.Error(), span: ast.Span{},
		})
		return result
	}
	if tierErrs := CheckGroupTiers(groups, ast.Span{}); len(tierErrs) > 0 {
		result := InferModuleWithSource(module, StdlibSource(dir))
		result.Errors = append(result.Errors, tierErrs...)
		return result
	}
	return inferModuleWithGroups(module, StdlibSource(dir), StdlibGroupSource(dir), groups)
}

// groupKeyURI is the URI a group's declarations register their type keys under.
// One member's, since the merged module is inferred once and a type key carries
// the URI inference ran under. The first member sorted, so the key is the same
// whichever member an importer named first.
func (c *checker) groupKeyURI(group []string) string {
	return group[0]
}

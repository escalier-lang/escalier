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
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
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
//
// The merged parse goes through the shared cache, so a second load of the same
// members reuses it. That matters because the whole group re-parses otherwise:
// a member's own file is cached under its basename by readPackageImports, but a
// group parses under synthetic paths and merges, which is a different module.
func StdlibGroupSource(dir string) GroupSource {
	return func(uris []string) (*ast.Module, map[string]string, error) {
		paths := make(map[string]string, len(uris))
		synthetic := make([]string, 0, len(uris))
		bodies := make([]string, 0, len(uris))
		for _, uri := range uris {
			path, err := resolveStdlibPath(dir, uri)
			if err != nil {
				return nil, nil, err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("reading %s: %w", path, err)
			}
			paths[uri] = path
			// `<namespace>/index.esc`, so the member's declarations land under a
			// namespace of its own rather than at the top level where two members
			// would collide.
			synthetic = append(synthetic, filepath.Join(groupNamespace(uri), "index.esc"))
			bodies = append(bodies, string(contents))
		}

		module, err := stdlibParses.getGroup(synthetic, bodies, func(firstSourceID int) (*ast.Module, error) {
			sources := make([]*ast.Source, 0, len(uris))
			for i := range uris {
				sources = append(sources, &ast.Source{
					ID:       firstSourceID + groupSourceIDOffset + i,
					Path:     synthetic[i],
					Contents: bodies[i],
				})
			}
			parsed, parseErrs := parser.ParseLibFiles(context.Background(), sources)
			if len(parseErrs) > 0 {
				messages := make([]string, 0, len(parseErrs))
				for _, pe := range parseErrs {
					messages = append(messages, pe.String())
				}
				return nil, fmt.Errorf("parse errors in %s: %s",
					strings.Join(uris, ", "), strings.Join(messages, "; "))
			}
			return parsed, nil
		})
		if err != nil {
			return nil, nil, err
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
// All members are marked loading before inference and published after, so a
// lookup from outside during the load sees the cycle sentinel rather than an
// empty surface. A member reached from inside resolves through the merged
// module scope and never touches the registry.
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

	// Each member's declarations sit under its own namespace in the merged module,
	// which is what ties a declaration back to the file that wrote it.
	var loaderErrs []SolverError
	if c.checkJsDecorators {
		for _, uri := range group {
			loaderErrs = append(loaderErrs, jsDecoratorErrors(paths[uri], module, groupNamespace(uri), span)...)
		}
	}

	for _, uri := range group {
		c.ctx.packages.markLoading(uri, paths[uri])
	}
	c.loadStack = append(c.loadStack, group...)

	prevURI := c.pkgURI
	c.pkgURI = c.groupKeyURI(group)
	prevErrs := c.errs
	c.errs = nil

	prevGroup := c.activeGroup
	c.activeGroup = set.FromSlice(group)

	scope := c.packageParentScope(group).Child()
	// Minted before the imports bind, so a member importing a sibling binds the
	// object the walk then fills. preBindPathNamespaces reuses these rather than
	// minting its own for the same prefixes.
	//
	// Put back on the way out, the way the URI and the active group are, so a load
	// nested inside this one leaves the outer group's objects in place for the walk
	// that has yet to fill them.
	prevMembers := c.memberNamespaces
	c.memberNamespaces = map[string]*Namespace{}
	for _, uri := range group {
		c.memberNamespaces[groupNamespace(uri)] = newNamespace(groupNamespace(uri))
	}
	defer func() { c.memberNamespaces = prevMembers }()

	fileScopes := c.bindFileImports(scope, module)
	// A `web:*` member reaches `web:prelude`'s exports bare. When the group holds
	// `web:prelude` its declarations are in this module, so the member's file scope
	// reads them through the prelude member's namespace, which the walk fills.
	// memberImportNamespaces gives the dep graph the same edges, so a prelude
	// declaration is defined before a reader needs it.
	readers := webPreludeReaders(module, group)
	if preludeNS, held := c.memberNamespaces[groupNamespace(webPreludeURI)]; held {
		for sourceID := range readers {
			fileScopes[sourceID].defineUnprefixed(preludeNS)
		}
	}
	// Inference only reads the graph, and a group's graph depends on its module and
	// members alone, so every run loading the same cached group shares one graph.
	g := stdlibParses.depGraph(module, group, func() *dep_graph.DepGraph {
		return dep_graph.BuildDepGraphWithImports(module, memberImportNamespaces(module, c.activeGroup, readers))
	})
	c.inferDepGraph(scope, 0, module, g)

	errs := c.errs
	c.errs = prevErrs
	c.pkgURI = prevURI
	c.activeGroup = prevGroup
	c.loadStack = c.loadStack[:len(c.loadStack)-len(group)]

	// One surface for the whole module, then split by the namespace each member's
	// declarations landed under. exportedSurface has already dropped every
	// unexported declaration, so what a member publishes is its public half.
	//
	// The URI names the merged Namespace alone. Both sorts bind in the scope under
	// the namespace-qualified name, so what separates one member's declarations
	// from another's is the namespace, and the split below reads that.
	merged := exportedSurface(c.groupKeyURI(group), module, scope)
	for _, uri := range group {
		member, held := merged.Nested[groupNamespace(uri)]
		if !held {
			member = newNamespace(uri)
		}
		member.Name = uri
		// The merged surface was built under the group's key, so each member's own
		// value is found here, against the member's URI.
		setOwnNameValue(uri, member)
		c.ctx.packages.publish(uri, member)
	}

	if len(errs) > 0 {
		return append(loaderErrs, &PackageInferenceError{
			URI:      strings.Join(group, ", "),
			Path:     paths[group[0]],
			Messages: messagesOf(errs),
			span:     span,
		})
	}
	return loaderErrs
}

// memberImportNamespaces maps each file of a merged group to the imports it
// writes for other members, each naming the namespace that member's
// declarations bind under. `import "std:weak_ref"` in `std:map` maps `weak_ref`
// to `std__weak_ref`. An import of a package outside the group is left out,
// since its declarations are not in the module. Each file in preludeReaders also
// maps dep_graph.UnprefixedImport to `web:prelude`'s namespace, matching the bare
// bindings loadPackageGroup makes for it.
func memberImportNamespaces(module *ast.Module, group set.Set[string], preludeReaders set.Set[int]) map[int]map[string]string {
	out := map[int]map[string]string{}
	for _, file := range module.Files {
		if preludeReaders.Contains(file.SourceID) {
			out[file.SourceID] = map[string]string{dep_graph.UnprefixedImport: groupNamespace(webPreludeURI)}
		}
		for _, stmt := range file.Imports {
			if !group.Contains(stmt.PackageName) {
				continue
			}
			if out[file.SourceID] == nil {
				out[file.SourceID] = map[string]string{}
			}
			out[file.SourceID][stmt.LocalName()] = groupNamespace(stmt.PackageName)
		}
	}
	return out
}

// webPreludeReaders returns the source IDs of the files of module, a merged group's
// module, that read `web:prelude`'s exports bare, as dts_to_esc.ReadsPreludeBare
// decides. It is empty when group does not hold `web:prelude`, since its exports are
// then not in the module.
func webPreludeReaders(module *ast.Module, group []string) set.Set[int] {
	readers := set.NewSet[int]()
	if !slices.Contains(group, webPreludeURI) {
		return readers
	}
	memberOf := make(map[string]string, len(group))
	for _, uri := range group {
		memberOf[groupNamespace(uri)] = uri
	}
	for _, file := range module.Files {
		if dts_to_esc.ReadsPreludeBare(memberOf[file.Namespace], webPreludeURI) {
			readers.Add(file.SourceID)
		}
	}
	return readers
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
// It is the entry point a run with a stdlib directory uses rather than
// InferModuleWithSource, because loading a package needs two things a bare
// ModuleSource cannot supply: the closure a module's imports reach, and a
// reader for the whole closure at once. opts adjusts the run.
func InferModuleAgainstStdlib(module *ast.Module, dir string, opts Options) *ModuleResult {
	roots := closureRoots(moduleImports(module))
	source := packageSource(dir, opts)
	groups, err := BuildPackageClosure(dir, roots)
	if err != nil {
		// Nothing is known about what the imports reach, so every package loads
		// alone. That is right for the tree a readable directory would have held
		// and reports honestly for one that cycles.
		result := inferModuleWithGroups(module, source, nil, nil, opts)
		result.Errors = append(result.Errors, &UnresolvedPackageError{
			URI: dir, Reason: err.Error(), span: ast.Span{},
		})
		return result
	}
	if c, ok := ambientChecker(dir, source, groups, withOwnershipDataflowResolved(opts)); ok {
		return inferModuleOn(c, module)
	}
	opts.stdlibDir = dir
	return inferModuleWithGroups(module, source, StdlibGroupSource(dir), groups, opts)
}

// InferScriptAgainstStdlib infers script against the pseudo-packages in dir. It is
// the script counterpart to InferModuleAgainstStdlib, and its closure grows from the
// roots closureRoots gives the script's imports. opts adjusts the run.
func InferScriptAgainstStdlib(script *ast.Script, dir string, opts Options) (*Scope, *Info, []SolverError) {
	source := packageSource(dir, opts)
	groups, err := BuildPackageClosure(dir, closureRoots(script.Imports))
	if err != nil {
		scope, info, errs := inferScriptWithGroups(script, source, nil, nil, opts)
		return scope, info, append(errs, &UnresolvedPackageError{
			URI: dir, Reason: err.Error(), span: ast.Span{},
		})
	}
	if c, ok := ambientChecker(dir, source, groups, withOwnershipDataflowResolved(opts)); ok {
		return inferScriptOn(c, script)
	}
	opts.stdlibDir = dir
	return inferScriptWithGroups(script, source, StdlibGroupSource(dir), groups, opts)
}

// closureRoots returns, sorted, the packages a run whose imports are imports roots
// its closure at.
//
// Every run loads `web:prelude` and the packages literalClassPackages lists, so they
// root the closure beside the pseudo-packages imports names. A run importing an npm
// package also loads the globals scope that package's `.d.ts` reads, so
// ambientPackages root it too. A package the closure leaves out would load alone and
// miss the group its imports form.
func closureRoots(imports []*ast.ImportStmt) []string {
	roots := set.FromSlice(append(literalClassPackages(), webPreludeURI))
	for _, uri := range pseudoPackageImports(imports) {
		roots.Add(uri)
	}
	if slices.ContainsFunc(imports, func(stmt *ast.ImportStmt) bool {
		return !IsSchemePrefixedImport(stmt.PackageName)
	}) {
		for _, uri := range ambientPackages {
			roots.Add(uri)
		}
	}
	return sortedSet(roots)
}

// sortedSet returns the members of s, sorted.
func sortedSet(s set.Set[string]) []string {
	out := s.ToSlice()
	sort.Strings(out)
	return out
}

// packageSource returns the ModuleSource a run against the pseudo-packages in dir
// loads through. A URI with a scheme reads from dir. Any other URI reads from the
// `node_modules` directories at and above opts.PackageDir, and resolves nothing
// when opts.PackageDir is empty.
func packageSource(dir string, opts Options) ModuleSource {
	stdlib := StdlibSource(dir)
	if opts.PackageDir == "" {
		return stdlib
	}
	npm := NodeModulesSource(opts.PackageDir)
	return func(uri string) (*ast.Module, string, error) {
		if IsSchemePrefixedImport(uri) {
			return stdlib(uri)
		}
		return npm(uri)
	}
}

// pseudoPackageImports returns the pseudo-package URIs imports names, sorted. These
// are the roots the closure grows from.
//
// Every scheme counts, not `std:` alone. A file importing `web:dom` roots the
// closure there the same way, and `node:` will when it is populated.
func pseudoPackageImports(imports []*ast.ImportStmt) []string {
	roots := set.NewSet[string]()
	for _, stmt := range imports {
		if IsSchemePrefixedImport(stmt.PackageName) {
			roots.Add(stmt.PackageName)
		}
	}
	out := roots.ToSlice()
	sort.Strings(out)
	return out
}

// moduleImports returns every import the files of module write, in file order.
func moduleImports(module *ast.Module) []*ast.ImportStmt {
	var imports []*ast.ImportStmt
	for _, file := range module.Files {
		imports = append(imports, file.Imports...)
	}
	return imports
}

// groupKeyURI is the URI a group's declarations register their type keys under.
// One member's, since the merged module is inferred once and a type key carries
// the URI inference ran under. The first member sorted, so the key is the same
// whichever member an importer named first.
func (c *checker) groupKeyURI(group []string) string {
	return group[0]
}

// groupNamespace is the namespace one member's declarations land under inside a
// merged module.
//
// It carries the scheme, so `std:url` and `web:url` land apart. Neither is the
// name anything writes: a member reaches a sibling by whatever name its own
// import binds, and bindPseudoPackageImport binds that name in the member's file
// scope to the namespace this one keys.
func groupNamespace(uri string) string {
	scheme, pkg, ok := splitScheme(uri)
	if !ok {
		return ast.DeriveImportName(uri)
	}
	return scheme + "__" + ast.DeriveImportName(pkg)
}

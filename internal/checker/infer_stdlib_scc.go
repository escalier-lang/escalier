package checker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// schemesWithSCCSupport lists the URI schemes whose pseudo-packages
// participate in cycle-aware loading. node:* is reserved and skipped
// until the node loader lands; mixing schemes is allowed by the graph
// (e.g. a web:* package importing std:*), but cycles always stay
// confined to these schemes — user packages don't appear in this graph.
var schemesWithSCCSupport = []string{"std", "web"}

// stdlibSCCCache memoizes the per-stdlib-dir SCC graph for the process
// lifetime. The scan is the same for every Checker that resolves to
// the same on-disk directory, so we only pay the parse cost once per
// dir per process (instead of once per Checker). Tests that point at a
// fresh tempdir via ESCALIER_STDLIB_DIR get their own cache entry.
var (
	stdlibSCCCacheMu sync.Mutex
	stdlibSCCCache   = map[string]*stdlibSCCEntry{}
)

type stdlibSCCEntry struct {
	once  sync.Once
	byURI map[string][]string
	err   error
}

// stdlibSCCFor returns the SCC containing uri. The returned slice
// always contains uri itself; length > 1 means the package is part
// of a real cycle and must be loaded via loadStdlibSCC.
func (c *Checker) stdlibSCCFor(uri string) ([]string, error) {
	dir, err := c.getStdlibDir()
	if err != nil {
		return nil, err
	}
	entry := getStdlibSCCEntry(dir)
	entry.once.Do(func() {
		entry.byURI, entry.err = buildStdlibPkgGraph(c.ctx, dir)
	})
	if entry.err != nil {
		return nil, entry.err
	}
	if scc, ok := entry.byURI[uri]; ok {
		return scc, nil
	}
	// URI isn't in the discovered graph (e.g. a not-yet-existing pkg);
	// treat as a singleton so the normal load path runs and reports the
	// not-found diagnostic.
	return []string{uri}, nil
}

func getStdlibSCCEntry(dir string) *stdlibSCCEntry {
	stdlibSCCCacheMu.Lock()
	defer stdlibSCCCacheMu.Unlock()
	if e, ok := stdlibSCCCache[dir]; ok {
		return e
	}
	e := &stdlibSCCEntry{}
	stdlibSCCCache[dir] = e
	return e
}

// buildStdlibPkgGraph scans every `.esc` file under the stdlib data
// directory's recognized scheme subdirectories, parses just its
// imports, and computes the SCC each URI belongs to.
func buildStdlibPkgGraph(ctx context.Context, dir string) (map[string][]string, error) {

	edges := map[string][]string{}
	for _, scheme := range schemesWithSCCSupport {
		schemeDir := filepath.Join(dir, scheme)
		entries, err := os.ReadDir(schemeDir)
		if err != nil {
			// Missing scheme subdir is fine — just no packages there.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot scan %s/: %w", schemeDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".esc") {
				continue
			}
			pkg := strings.TrimSuffix(e.Name(), ".esc")
			uri := scheme + ":" + pkg
			path := filepath.Join(schemeDir, e.Name())
			imports, ierr := extractPseudoPackageImports(ctx, path)
			if ierr != nil {
				return nil, ierr
			}
			edges[uri] = imports
		}
	}

	sccs := tarjanSCCs(edges)
	out := map[string][]string{}
	for _, scc := range sccs {
		// Deterministic order so tests and diagnostics are stable.
		sort.Strings(scc)
		for _, uri := range scc {
			out[uri] = scc
		}
	}
	return out, nil
}

// extractPseudoPackageImports parses path and returns the package
// names of every `import "<scheme>:..."` statement whose scheme is in
// schemesWithSCCSupport.
func extractPseudoPackageImports(ctx context.Context, path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	src := &ast.Source{ID: 0, Path: filepath.Base(path), Contents: string(contents)}
	mod, _ := parser.ParseLibFiles(ctx, []*ast.Source{src})
	// Parse errors are intentionally ignored here: the SCC scan only
	// cares about import statements. The same file is parsed again at
	// real-load time, and any parse errors surface there with the
	// proper diagnostic plumbing.
	//
	// Edge case: imports live at the top of `.esc` files, so any parse
	// error before the last import statement may truncate parsed.Imports
	// — the file could be misclassified into a smaller SCC than it
	// actually belongs to. The real-load parse re-reports the same error
	// with the proper anchor, so the user is never silently mis-served:
	// the worst outcome is a slightly different shape of failure
	// diagnostic. The file isn't compiling either way.

	var out []string
	for _, file := range mod.Files {
		for _, imp := range file.Imports {
			scheme, _, ok := splitScheme(imp.PackageName)
			if !ok {
				continue
			}
			if slices.Contains(schemesWithSCCSupport, scheme) {
				out = append(out, imp.PackageName)
			}
		}
	}
	return out, nil
}

// tarjanSCCs runs Tarjan's strongly-connected-components algorithm
// over the adjacency list `edges`. Nodes referenced only as targets
// (not present as keys) are included as singletons. Returns one slice
// per SCC.
//
// The DFS is implemented recursively; Go grows goroutine stacks
// dynamically (8KB → many MB), so depth is bounded only by available
// memory and is far beyond any realistic stdlib graph size.
func tarjanSCCs(edges map[string][]string) [][]string {
	index := 0
	indices := map[string]int{}
	lowlink := map[string]int{}
	onStack := set.NewSet[string]()
	stack := []string{}
	var sccs [][]string

	// Gather all nodes (sources + targets) so isolated importees are
	// represented even if they have no outgoing edges.
	nodeSet := set.NewSet[string]()
	for n, succs := range edges {
		nodeSet.Add(n)
		for _, s := range succs {
			nodeSet.Add(s)
		}
	}
	nodes := nodeSet.ToSlice()
	sort.Strings(nodes) // deterministic traversal

	var strongconnect func(v string)
	strongconnect = func(v string) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack.Add(v)

		for _, w := range edges[v] {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack.Contains(w) {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack.Remove(w)
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for _, n := range nodes {
		if _, seen := indices[n]; !seen {
			strongconnect(n)
		}
	}
	return sccs
}

// loadStdlibSCC parses and infers every URI in sccURIs as a single
// merged module so cross-package type references resolve through the
// dep_graph's own SCC handling. After this returns successfully, every
// URI in sccURIs has a populated namespace in PackageRegistry. If any
// step fails, all members are removed from PackageRegistry so a
// subsequent import re-attempts the load (matching the single-package
// path's rollback behavior in loadStdlibPackage).
//
// `span` is the original importing-file span; surfaces as the anchor
// on any diagnostics that arise during the merged load.
func (c *Checker) loadStdlibSCC(sccURIs []string, span ast.Span) []Error {
	// Invariant: SCC members are always loaded together (either all
	// registered or all absent), so checking the first is equivalent to
	// checking every member. The lazy stdlibSCCOnce ensures the SCC
	// graph is computed before any singleton load runs, so a member can
	// never have been loaded via the singleton path.
	if c.PackageRegistry.Has(sccURIs[0]) {
		return nil
	}

	// Stage every member as in-progress so that any non-SCC Lookup that
	// fires during the merged load sees the sentinel (and treats the
	// URI as cyclic) rather than an empty namespace. Intra-SCC imports
	// are short-circuited earlier via activeSCC and never reach Lookup.
	mergedNs := type_system.NewNamespace()
	type sccMember struct {
		uri, scheme, pkg, path string
		ns                     *type_system.Namespace
	}
	members := make([]sccMember, 0, len(sccURIs))

	// Rollback helper: drop every registry entry for this SCC. Called
	// on any error return after staging so a subsequent import re-tries
	// the load instead of finding a half-baked entry.
	rollback := func() {
		for _, uri := range sccURIs {
			delete(c.PackageRegistry.packages, uri)
		}
	}

	for _, uri := range sccURIs {
		scheme, pkg, _ := splitScheme(uri)
		path, resolveErrs := c.resolveStdlibPath(scheme, pkg, span)
		if len(resolveErrs) > 0 {
			rollback()
			return resolveErrs
		}
		pkgNs := type_system.NewNamespace()
		members = append(members, sccMember{uri: uri, scheme: scheme, pkg: pkg, path: path, ns: pkgNs})

		// Pre-create the <pkg> sub-namespace on mergedNs so InferModule
		// lands `<pkg>.<name>` declarations into the same Namespace
		// pointer we publish below — and so the dep_graph sees the
		// member's binding keys at the same flat `<pkg>.<name>` path
		// source files reference them under.
		if err := mergedNs.SetNamespace(pkg, pkgNs); err != nil {
			rollback()
			return []Error{&GenericError{message: err.Error(), span: span}}
		}

		c.PackageRegistry.MarkInProgress(uri)
	}

	// Activate the intra-SCC skip so file-scope imports don't shadow
	// the live module-scope bindings. SCCs are disjoint by construction
	// (Tarjan output), so a nested loadStdlibSCC cannot share URIs with
	// an outer one; replacing the map is safe under save/restore.
	prev := c.activeSCC
	c.activeSCC = set.FromSlice(sccURIs)
	defer func() { c.activeSCC = prev }()

	sources := make([]*ast.Source, 0, len(members))
	for _, m := range members {
		contents, readErr := os.ReadFile(m.path)
		if readErr != nil {
			rollback()
			return []Error{&GenericError{
				message: fmt.Sprintf("failed to read stdlib file %s: %s", m.path, readErr.Error()),
				span:    span,
			}}
		}
		sourceID := c.stdlibNextSourceID
		c.stdlibNextSourceID++
		// Path is `<pkg>/index.esc` so deriveNamespaceFromPath yields
		// `<pkg>` — that's the namespace key declarations from this file
		// land under in mod.Namespaces, and also the binding-key prefix
		// the dep_graph uses for cycle detection. Source files reference
		// SCC siblings as `<pkg>.<name>`, so the dep_graph's qualified
		// lookup hits the canonical key directly. The actual on-disk
		// `.esc` file is flat (`<scheme>/<pkg>.esc`); this synthetic path
		// only steers ParseLibFiles' namespace inference.
		sources = append(sources, &ast.Source{
			ID:       sourceID,
			Path:     m.pkg + "/index.esc",
			Contents: string(contents),
		})
	}

	mod, parseErrs := parser.ParseLibFiles(c.ctx, sources)
	if len(parseErrs) > 0 {
		rollback()
		errs := make([]Error, 0, len(parseErrs))
		for _, pe := range parseErrs {
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("parse error in stdlib SCC: %s", pe.String()),
				span:    span,
			})
		}
		return errs
	}

	// §3.4 loader rules per member, so diagnostic messages name the
	// originating URI (e.g. `web:dom`) instead of an opaque SCC label.
	globals := c.knownJSGlobals()
	var decErrs []Error
	for _, m := range members {
		ns, ok := mod.Namespaces.Get(m.pkg)
		if !ok {
			continue
		}
		for _, decl := range ns.Decls {
			decErrs = append(decErrs, validateJsDecorator(m.uri, decl, span, globals)...)
		}
	}
	if len(decErrs) > 0 {
		rollback()
		return decErrs
	}

	pkgScope := &Scope{Parent: c.GlobalScope, Namespace: mergedNs}
	pkgCtx := Context{Scope: pkgScope, IsAsync: false, IsPatMatch: false}
	_, inferErrs := c.InferModule(pkgCtx, mod)
	if len(inferErrs) > 0 {
		rollback()
		return inferErrs
	}

	// Publish the populated namespaces, swapping each sentinel for its
	// real pointer.
	for _, m := range members {
		if updateErr := c.PackageRegistry.Update(m.uri, m.ns); updateErr != nil {
			rollback()
			return []Error{&GenericError{
				message: fmt.Sprintf("cannot publish stdlib SCC member %s: %s", m.uri, updateErr.Error()),
				span:    span,
			}}
		}
	}
	return nil
}

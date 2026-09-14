package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// import_header.go gives each generated package the `import` header its
// declarations require, and rewrites every cross-package reference to go
// through the binding that import makes.
//
// A package binds under its own name, so `import "web:file"` binds `file` and
// `Blob` is written `file.Blob`. The two halves are one change: an import with
// unqualified references resolves nothing, and the reverse names nothing.

// preludeURI is the package every scope already holds, so a declaration reaches
// `Promise` by writing `Promise`. It takes no import and no qualifier, and it
// imports nothing itself, since a package it imported would be inferred while
// that scope is still empty.
const preludeURI = "std:prelude"

// coreURI is the package whose exports an importer binds unprefixed. It still
// takes an import, which is the whole difference between it and the prelude.
//
// The qualifier is dropped because `core` names no domain: it names what the
// portable tier needs from the DOM, and `core.Event` says less than `Event`. A
// package whose name does say something keeps its prefix, `error.RangeError`
// being the shape that reads well.
const coreURI = "web:core"

// declaringPackages maps each top-level name in the converted tree to the URI
// of the package declaring it.
//
// A name two packages declare fails rather than resolving by iteration order.
// The partition already rejects a symbol routed twice, so a duplicate here
// means two packages produced the same name by other means.
func declaringPackages(mods map[string]*StandaloneModule) (map[string]string, error) {
	uris := make([]string, 0, len(mods))
	for uri := range mods {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	owner := make(map[string]string)
	for _, uri := range uris {
		names := DeclaredNames(mods[uri].Module).ToSlice()
		sort.Strings(names)
		for _, name := range names {
			if first, dup := owner[name]; dup {
				return nil, fmt.Errorf(
					"converter: %q is declared by both %s and %s; a name belongs to one package",
					name, first, uri)
			}
			owner[name] = uri
		}
	}
	return owner, nil
}

// ImportGraph returns, for each package, the sorted URIs it has to import for
// its declarations to resolve. It reads the references the converted
// declarations make, not what the `.d.ts` file happened to include.
func ImportGraph(mods map[string]*StandaloneModule) (map[string][]string, error) {
	owner, err := declaringPackages(mods)
	if err != nil {
		return nil, err
	}
	graph := make(map[string][]string, len(mods))
	for uri, mod := range mods {
		needed := set.NewSet[string]()
		if uri == preludeURI {
			// The prelude imports nothing. A name it reaches that another package
			// declares is a routing mistake, since that package would be inferred
			// before the prelude scope it needs exists.
			graph[uri] = nil
			continue
		}
		for _, name := range TypeRefNames(mod.Module).ToSlice() {
			declaredIn, known := owner[name]
			if !known || declaredIn == uri || declaredIn == preludeURI {
				continue
			}
			// web:core is imported like any other package. Only its qualifier is
			// dropped, so it is not skipped the way the prelude is.
			needed.Add(declaredIn)
		}
		targets := needed.ToSlice()
		sort.Strings(targets)
		graph[uri] = targets
	}
	return graph, nil
}

// TierViolation is one import edge whose target sits above its source in the
// tier order, so the target is unavailable on a runtime the source claims.
type TierViolation struct {
	From     string
	FromTier Tier
	To       string
	ToTier   Tier
	// Names are the references that force the edge, sorted.
	Names []string
}

func (v TierViolation) String() string {
	return fmt.Sprintf("%s (%s tier) imports %s (%s tier) for %s",
		v.From, v.FromTier, v.To, v.ToTier, strings.Join(v.Names, ", "))
}

// CheckTiers returns every import edge that goes up a tier, sorted. Direct
// edges are enough: a path cannot rise unless one of its edges does.
func CheckTiers(mods map[string]*StandaloneModule, graph map[string][]string) ([]TierViolation, error) {
	owner, err := declaringPackages(mods)
	if err != nil {
		return nil, err
	}
	var violations []TierViolation
	for from, targets := range graph {
		fromTier, ok := TierOf(from)
		if !ok {
			return nil, fmt.Errorf("converter: %s has no tier; add one to internal/dts_to_esc/tier.go", from)
		}
		for _, to := range targets {
			toTier, ok := TierOf(to)
			if !ok {
				return nil, fmt.Errorf("converter: %s has no tier; add one to internal/dts_to_esc/tier.go", to)
			}
			if toTier <= fromTier {
				continue
			}
			var names []string
			for _, name := range TypeRefNames(mods[from].Module).ToSlice() {
				if owner[name] == to {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			if acceptsUpwardEdge(from, names) {
				continue
			}
			violations = append(violations, TierViolation{
				From: from, FromTier: fromTier, To: to, ToTier: toTier, Names: names,
			})
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].From != violations[j].From {
			return violations[i].From < violations[j].From
		}
		return violations[i].To < violations[j].To
	})
	return violations, nil
}

// AddImportHeaders writes each package's import header and qualifies every
// reference that header covers.
func AddImportHeaders(mods map[string]*StandaloneModule) error {
	owner, err := declaringPackages(mods)
	if err != nil {
		return err
	}
	graph, err := ImportGraph(mods)
	if err != nil {
		return err
	}
	for uri, mod := range mods {
		if err := checkBindingCollisions(uri, graph[uri]); err != nil {
			return err
		}
		qualifyCrossPackageRefs(mod, uri, owner)
		setImportHeader(mod, graph[uri])
	}
	return nil
}

// checkBindingCollisions refuses a header binding two packages under one name.
//
// A binding name drops the scheme, so `std:url` and `web:url` both bind `url`.
// No package in the pinned set imports both, and one that did would have its
// second import silently shadow the first.
func checkBindingCollisions(uri string, targets []string) error {
	seen := map[string]string{}
	for _, target := range targets {
		binding := ast.DeriveImportName(target)
		if first, dup := seen[binding]; dup {
			return fmt.Errorf(
				"converter: %s imports both %s and %s, which bind the same name %q; "+
					"one of them needs an alias before the header can name both",
				uri, first, target, binding)
		}
		seen[binding] = target
	}
	return nil
}

// setImportHeader replaces a module's imports with one bare import per URI it
// depends on, in sorted order. A converted module carries a single file, which
// is where the header lands.
func setImportHeader(mod *StandaloneModule, targets []string) {
	imports := make([]*ast.ImportStmt, 0, len(targets))
	for _, uri := range targets {
		imports = append(imports, ast.NewImportStmt(uri, "", nil, ast.Span{}))
	}
	if len(mod.Module.Files) == 0 {
		mod.Module.Files = []*ast.File{{}}
	}
	mod.Module.Files[0].Imports = imports
}

// qualifyCrossPackageRefs rewrites every reference to a name another package
// declares so it goes through that package's binding, turning `Blob` into
// `file.Blob`. A reference already written qualified has its head qualified
// instead, and the member rides along.
//
// The prelude and `web:core` are skipped, since both bind their exports
// unprefixed and a qualifier on one would name nothing.
func qualifyCrossPackageRefs(mod *StandaloneModule, uri string, owner map[string]string) {
	qualifiers := map[string]string{}
	for _, name := range TypeRefNames(mod.Module).ToSlice() {
		declaredIn, known := owner[name]
		if !known || declaredIn == uri || declaredIn == preludeURI || declaredIn == coreURI {
			continue
		}
		qualifiers[name] = ast.DeriveImportName(declaredIn)
	}
	if len(qualifiers) == 0 {
		return
	}
	// One table serves both rules, which look up the same thing and differ in
	// what they do with it. A reference resolving through its head is prefixed;
	// one resolving through its last segment has its head replaced, which is
	// what namespace flattening leaves behind. declaredNames guards the second,
	// so a local `Ns.Widget` keeps its `Ns`.
	rw := &refRewriter{
		qualifiers:          qualifiers,
		flattenedQualifiers: qualifiers,
		declaredNames:       declaredNamesOf(owner),
	}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			rw.rewriteDecl(decl)
		}
		return true
	})
}

// declaredNamesOf is every name the tree declares, which says whether a
// qualified reference's head resolves to anything.
func declaredNamesOf(owner map[string]string) set.Set[string] {
	names := set.NewSet[string]()
	for name := range owner {
		names.Add(name)
	}
	return names
}

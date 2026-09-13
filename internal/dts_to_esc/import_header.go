package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// import_header.go gives each generated package the `import` header its own
// declarations require, and rewrites every cross-package reference to go
// through the binding that import makes.
//
// A package binds under its own name, so `import "web:core"` binds `core` and
// a reference to `Event` inside another package is written `core.Event`. The
// header and the qualifier are one change: an import with unqualified
// references resolves nothing, and a qualifier with no import names nothing.

// declaringPackages maps each top-level name in the converted tree to the URI
// of the package that declares it.
//
// A name two packages declare is an error rather than a last-writer-wins
// choice. The partition rejects a symbol routed twice, so a duplicate here
// means two packages produced the same name by other means, and every
// reference to it would resolve by iteration order.
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
// its own declarations to resolve.
//
// The graph is computed from the references the converted declarations make,
// so it says what the package needs rather than what the `.d.ts` file it came
// from happened to include.
func ImportGraph(mods map[string]*StandaloneModule) (map[string][]string, error) {
	owner, err := declaringPackages(mods)
	if err != nil {
		return nil, err
	}
	graph := make(map[string][]string, len(mods))
	for uri, mod := range mods {
		needed := set.NewSet[string]()
		for _, name := range TypeRefNames(mod.Module).ToSlice() {
			if declaredIn, known := owner[name]; known && declaredIn != uri {
				needed.Add(declaredIn)
			}
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

// CheckTiers returns every import edge that goes up a tier, sorted.
//
// Checking direct edges is enough. The tier order is monotone along an edge,
// so a path cannot rise unless one of its edges does.
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
//
// Both halves run over one package before the next, so a reference is
// qualified against the same graph the header was built from.
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
		qualifyCrossPackageRefs(mod, uri, owner)
		setImportHeader(mod, graph[uri])
	}
	return nil
}

// setImportHeader replaces a module's imports with one bare import per URI it
// depends on, in sorted order.
//
// The module carries a single file, so the header lands there. A converted
// package with no file gets one, since an import statement has nowhere else to
// live.
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
// declares so it goes through that package's binding, turning `Event` into
// `core.Event`.
//
// A reference the tree already writes qualified is left alone. `Intl.Collator`
// has `Intl` as its head, and `Intl` is a name `std:intl` declares, so the
// head is what gets qualified and the member rides along.
func qualifyCrossPackageRefs(mod *StandaloneModule, uri string, owner map[string]string) {
	qualifiers := map[string]string{}
	for _, name := range TypeRefNames(mod.Module).ToSlice() {
		declaredIn, known := owner[name]
		if !known || declaredIn == uri {
			continue
		}
		qualifiers[name] = ast.DeriveImportName(declaredIn)
	}
	if len(qualifiers) == 0 {
		return
	}
	// The same table serves both rules. A reference resolving through its head
	// is prefixed; one resolving through its last segment has its head replaced,
	// which is what a flattened namespace leaves behind.
	rw := &refRewriter{qualifiers: qualifiers, flattenedQualifiers: qualifiers}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			rw.rewriteDecl(decl)
		}
		return true
	})
}

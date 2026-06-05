package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/tidwall/btree"
)

// InferModule builds the dep graph for a single parsed module, infers every
// top-level declaration in dep_graph SCC order, populates Info, and returns the
// populated module Scope (a child of the prelude, so operators and the
// stdlib-type placeholders resolve through the parent), the Info side table, and
// any SolverErrors.
//
// PR-5 replaces PR-2's source-order loop with dep_graph SCC ordering: a decl that
// forward-references a name defined later in the source, or that mutually
// recurses with another decl, now infers correctly because BuildDepGraph
// topologically orders the strongly connected components and inferComponent makes
// every member of a group visible before inferring any of their bodies.
// Inference is MONOMORPHIC — M1 ships no schemes, so a group's vars stay as their
// coalesced monomorphic types; the generalization that yields <T0> rendering is
// M3.
func InferModule(module *ast.Module) (*Scope, *Info, []SolverError) {
	return InferModules([]*ast.Module{module})
}

// InferModules infers a multi-file program spread across several parsed modules,
// closing the M2 exit criterion "a multi-file module resolves via the dep graph".
// It merges the modules into one combined *ast.Module (unioning their namespaces,
// files, and source tables) and drives the SAME dep_graph-ordered walk as the
// single-module path, so a `val`/`fn` in one file resolving a top-level
// `val`/`fn` in another is just an ordinary cross-component reference that
// BuildDepGraph topologically orders — no per-module passes, no cross-module
// patching.
//
// Cross-file references in M2 use root-namespace short names (a file referring to
// `foo` defined in a sibling file): both files derive the same namespace from
// their paths, so their declarations land in the same dep_graph namespace and the
// reference resolves through the shared BindingKey space. Qualified
// namespace-member access (`Foo.bar`) is M4; third-party `@types`/`.d.ts`
// ingestion (which would engage internal/resolver) is M7 — M2 engages neither.
func InferModules(modules []*ast.Module) (*Scope, *Info, []SolverError) {
	merged := mergeModules(modules)
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferDepGraph(scope, 0, merged, dep_graph.BuildDepGraph(merged))
	return scope, c.info, c.errs
}

// mergeModules unions several parsed modules into one. Declarations from files
// that share a namespace (e.g. two root-level files, both in the "" namespace)
// are concatenated, so BuildDepGraph sees the whole program and resolves
// references that span files. A single-module call returns its module untouched —
// the merge only allocates when there is more than one.
//
// The merge builds fresh Namespace values rather than mutating any input
// module's, so InferModules never disturbs its arguments (a caller may infer the
// same modules again, or hold them for the old checker). File and source tables
// are unioned; a SourceID collision keeps the last writer, which callers avoid by
// assigning each file a distinct ID.
func mergeModules(modules []*ast.Module) *ast.Module {
	if len(modules) == 1 {
		return modules[0]
	}
	var namespaces btree.Map[string, *ast.Namespace]
	files := []*ast.File{}
	sources := map[int]*ast.Source{}
	for _, module := range modules {
		module.Namespaces.Scan(func(name string, ns *ast.Namespace) bool {
			merged, ok := namespaces.Get(name)
			if !ok {
				merged = &ast.Namespace{Decls: []ast.Decl{}, Exported: ns.Exported}
				namespaces.Set(name, merged)
			}
			merged.Decls = append(merged.Decls, ns.Decls...)
			merged.Exported = merged.Exported || ns.Exported
			return true
		})
		files = append(files, module.Files...)
		for id, src := range module.Sources {
			sources[id] = src
		}
	}
	return ast.NewModuleWithFiles(namespaces, files, sources)
}

// inferDepGraph infers every component of g into scope. BuildDepGraph returns
// Components in topological order (a dependency's component precedes its
// dependents'), so a straight iteration types each binding only after everything
// it refers to — mirroring the old checker's InferDepGraph loop, over soltype
// instead of type_system. The shared `handled` set guards against inferring a
// single declaration twice when it contributes to several binding keys (a
// destructuring `val [a, b] = …` registers under both value:a and value:b), and
// drives the reconciliation pass that reports any top-level declaration the dep
// graph did not model.
func (c *checker) inferDepGraph(scope *Scope, lvl int, module *ast.Module, g *dep_graph.DepGraph) {
	handled := set.NewSet[ast.Decl]()
	for _, component := range g.Components {
		c.inferComponent(scope, lvl, g, component, handled)
	}
	// Reconcile against the source: BuildDepGraph only produces binding keys for
	// the decl kinds it models, so a kind it does not descend into — e.g. a
	// NamespaceDecl — yields no component and would vanish without a diagnostic.
	// Report every top-level declaration that no component visited, so an
	// unsupported decl always fails cleanly rather than being silently dropped.
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if !handled.Contains(d) {
				c.reportUnsupported(d, d)
			}
		}
		return true
	})
}

// componentBinding tracks the in-flight state of one value binding while its
// strongly connected component is inferred.
type componentBinding struct {
	v       *soltype.TypeVarType    // this binding's fresh var; every definition of this name is constrained <: it
	sources []provenance.Provenance // every contributing decl, in source order (primary + rejected overload/duplicate arms)
	// primary is the first successfully-inferred decl under this key. M2 keeps it
	// as THE definition and rejects every later arm, so one handle suffices: it is
	// read in phase 3 to recover a VarDecl's Pattern for Info recording, and (via
	// isVar) to tell an overload arm from a duplicate.
	//
	// M3 retires it. Once overloads are represented as an IntersectionType of the
	// per-arm signatures (the overload-intersection representation noted in
	// inferComponent's doc; see planning/intersection_types), arms are merged
	// rather than discarded, so a single "primary" no longer captures the binding —
	// the full arm set does, and `sources` already holds it. The phase-3 VarDecl
	// pattern recording would then key off the lone value decl directly instead.
	primary ast.Decl
	bound   bool // a definition was inferred and constrained
	isVar   bool // the primary definition is a `val`/`var`
}

// inferComponent infers one strongly connected component — a group of
// mutually-recursive (or, in the singleton case, independent) top-level bindings
// — and binds each name in scope. It follows the spike's LetRecGroup discipline
// with NO placeholder/patching phase (the single biggest simplification the
// simple-sub bridge buys over the old checker's two-phase
// placeholder/definition pass):
//
//  1. give every VALUE binding in the component a fresh var at lvl+1 and define
//     it in scope BEFORE any body is inferred, so a mutually-recursive reference
//     resolves through the var;
//  2. infer each declaration's definition at lvl+1 and constrain it <: its var;
//  3. rebind each name to the coalesced MONOMORPHIC type of its var.
//
// M2 does NOT generalize (M1 ships no schemes): step 3 freezes each binding at
// its coalesced monomorphic type rather than wrapping it in a PolyScheme. The
// generalization that turns these into reusable polymorphic bindings — the <T0>
// rendering — is M3. M2's contribution is correct ordering and recursive
// resolution.
func (c *checker) inferComponent(
	scope *Scope, lvl int, g *dep_graph.DepGraph,
	component []dep_graph.BindingKey, handled set.Set[ast.Decl],
) {
	inner := lvl + 1

	// Phase 1: a fresh var per value binding, all defined before any body so a
	// mutually-recursive reference resolves through the var. M2 only infers value
	// bindings; type-sort keys are handled (as unsupported) after the value walk.
	bindings := make(map[dep_graph.BindingKey]*componentBinding, len(component))
	for _, key := range component {
		if key.Kind() != dep_graph.DepKindValue {
			continue
		}
		v := c.freshAt(inner)
		bindings[key] = &componentBinding{v: v}
		scope.defineValue(key.Name(), ValueBinding{Type: v})
	}

	// Phase 2: infer each declaration's definition and constrain it <: its var.
	for _, key := range component {
		b, isValue := bindings[key]
		if !isValue {
			continue // non-value keys handled below
		}
		for _, d := range g.GetDecls(key) {
			if handled.Contains(d) {
				// Already inferred under another binding key. The only M2 decl that
				// registers under several keys is a destructuring `val [a, b] = …`,
				// which is unsupported and produces no binding either way; skipping
				// the re-inference avoids reporting its errors once per name.
				continue
			}
			handled.Add(d)
			t, src, ok := c.inferDeclDef(scope, inner, d)
			if !ok {
				continue
			}
			// Accumulate every contributing decl's provenance — including the overload
			// or duplicate arms rejected below — so a future multi-target
			// go-to-definition can reach all of them. Only the primary's type is kept;
			// M2 has no overload-merge (that is M3), so the extra arms still error.
			b.sources = append(b.sources, src)
			if !b.bound {
				c.constrain(d, t, b.v)
				b.primary = d
				b.bound = true
				_, b.isVar = d.(*ast.VarDecl)
				continue
			}
			// The binding already has its primary definition. A repeated FuncDecl is
			// a top-level overload: M2 has no overload-intersection representation
			// (that is M3), so it keeps the first arm — leaving the binding callable
			// with that signature — and reports each extra arm, rather than merging
			// the arms into the same var (which yields an uncallable union binding).
			// Any other repeat — a duplicate `val`/`var`, or a decl redeclaring a
			// variable binding — likewise keeps the first and reports.
			if _, isFunc := d.(*ast.FuncDecl); isFunc && !b.isVar {
				c.report(&OverloadNotSupportedError{
					errSpan: errSpan{span: d.Span()},
					Name:    key.Name(),
				})
				continue
			}
			c.report(&DuplicateDeclarationError{
				errSpan: errSpan{span: d.Span()},
				Name:    key.Name(),
			})
		}
	}

	// Non-value keys (type aliases, classes, …) are outside the M2 subset. Report
	// each contributing decl once, skipping any already handled by a value key (a
	// class/enum contributes both a value and a type key for the same decl).
	for _, key := range component {
		if _, isValue := bindings[key]; isValue {
			continue
		}
		for _, d := range g.GetDecls(key) {
			if handled.Contains(d) {
				continue
			}
			handled.Add(d)
			c.reportUnsupported(d, d)
		}
	}

	// Phase 3: rebind each value name to its coalesced monomorphic type. A binding
	// whose declarations all failed to produce a definition (missing initializer,
	// destructuring, unsupported kind) is removed rather than left as a `never`
	// placeholder, matching PR-2: a later reference to it is then a genuine
	// unknown-identifier error instead of resolving to a stray binding.
	for _, key := range component {
		b, isValue := bindings[key]
		if !isValue {
			continue
		}
		if !b.bound {
			scope.removeValue(key.Name())
			continue
		}
		t := coalesce(b.v, soltype.Positive)
		scope.defineValue(key.Name(), ValueBinding{Type: t, Sources: b.sources})
		// Record the binding's final (coalesced) type in Info on the name node. The
		// raw initializer path (inferDeclDef) no longer records it, so this is where
		// the var-free type lands — and it is correct even for a `val` in a
		// recursive group, where coalescing at definition time would have frozen it
		// to `never`. A VarDecl records on its pattern, a FuncDecl on its name, so a
		// top-level `fn` is queryable through Info exactly like a `val`.
		switch d := b.primary.(type) {
		case *ast.VarDecl:
			c.recordType(d.Pattern, t)
		case *ast.FuncDecl:
			if d.Name != nil {
				c.recordType(d.Name, t)
			}
		}
	}
}

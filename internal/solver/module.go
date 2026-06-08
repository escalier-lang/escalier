package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
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
//
// Multi-file (PR-6) needs no separate entry point: parser.ParseLibFiles already
// assembles several sources into one *ast.Module, unioning files that share a
// path-derived namespace into the same Namespace.Decls. BuildDepGraph then spans
// every file, so a `val`/`fn` in one file resolving a top-level `val`/`fn` in
// another is just an ordinary cross-component reference the SCC ordering already
// handles — that is the M2 "multi-file module resolves via the dep graph" exit
// criterion. Cross-file references in M2 use root-namespace short names; qualified
// namespace-member access (`Foo.bar`) is M4, and third-party `@types`/`.d.ts`
// ingestion (internal/resolver) is M7 — M2 engages neither.
func InferModule(module *ast.Module) (*Scope, *Info, []SolverError) {
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	return scope, c.info, c.errs
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
				c.reportUnsupported(d)
			}
		}
		return true
	})
}

// overloadArm is one collected arm of an overload set (PR6): a top-level FuncDecl's
// raw inferred type plus whether its signature is fully annotated (the recursion
// gate) and the decl itself (for per-arm Info recording in phase 3).
type overloadArm struct {
	decl      *ast.FuncDecl
	t         soltype.Type // the arm's RAW (variable-carrying) inferred FuncType
	annotated bool
}

// componentBinding tracks the in-flight state of one value binding while its
// strongly connected component is inferred.
type componentBinding struct {
	v       *soltype.TypeVarType    // this binding's fresh var; every definition of this name is constrained <: it
	sources []provenance.Provenance // every contributing decl, in source order (primary + overload/duplicate arms)
	// primary is the first successfully-inferred decl under this key — read in phase
	// 3 to recover a VarDecl's Pattern (or a single FuncDecl's Name) for Info
	// recording, and (via isVar) to tell an overload arm from a duplicate.
	primary ast.Decl
	bound   bool             // a definition was inferred and constrained
	isVar   bool             // the primary definition is a `val`/`var`
	kind    ast.VariableKind // PR8: the primary definition's kind — VarKind ⇒ reassignable
	// recovered marks that a contributing definition was WHOLLY the ErrorType recovery
	// sentinel (e.g. `val a = <unknown ident>`). ErrorType absorbs in constrain, so it
	// leaves no bound on the group var; phase 3 uses this to recover the binding AS
	// ErrorType rather than freezing an unbound var to `never` (which would cascade
	// `<: never` at every later use). See PR8 / inferComponent phase 3.
	recovered bool
	// arms collects every top-level FuncDecl under this key (PR6). len <= 1 is an
	// ordinary function (or a non-function binding, len == 0); len > 1 is an overload
	// set, bound as a multi-scheme ValueBinding in phase 3. arms[i] lines up with the
	// FuncDecl's entry in sources.
	arms []overloadArm
	// prebound marks an overload set whose arms are ALL fully annotated, so phase 1
	// could build their signatures (body-free) and pre-bind the whole set in scope
	// (b.arms holds the signature types). References WITHIN the component — recursive
	// calls and value captures — then resolve against the set rather than a single
	// first-arm var; phase 2 only checks each arm's body. An overload with any
	// un-annotated arm cannot be pre-bound (its signature isn't known without inferring
	// the body), so it stays on the ordinary group-var path and cannot be mutually
	// recursive (the gate forbids it). See checkOverloadAnnotations / annotatedOverloadArms.
	prebound bool
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

	// Recursion gate (PR6): an overloaded function in a mutually-recursive component
	// (more than one binding) must have fully-annotated arms, since the overload set
	// has to be ground before bodies are inferred — fixed-point iteration over
	// overload choices is not guaranteed to converge under subtyping. The gate reports
	// the offending participants and returns the set of keys to degrade to a single
	// arm (so a later reference still resolves). Self-recursion (a singleton
	// component) is softer and is not gated.
	rejected := c.checkOverloadAnnotations(g, component)

	// Phase 1: a fresh var per value binding, all defined before any body so a
	// mutually-recursive reference resolves through the var. M2 only infers value
	// bindings; type-sort keys are handled (as unsupported) after the value walk.
	bindings := make(map[dep_graph.BindingKey]*componentBinding, len(component))
	for _, key := range component {
		if key.Kind() != dep_graph.DepKindValue {
			continue
		}
		// PR6: a fully-annotated overload set is pre-bound to the whole SET, built from
		// arm signatures alone (body-free), so references within the component resolve
		// against every arm via resolveOverload instead of seeing only a single
		// first-arm group var (which would make a recursive arm — or a value capture —
		// type-check against the wrong overload). The recursion gate guarantees a
		// mutually-recursive overload IS fully annotated, so this is exactly the set it
		// requires to be ground before bodies are inferred.
		if !rejected.Contains(key) {
			if armDecls := annotatedOverloadArms(g, key); armDecls != nil {
				arms := make([]overloadArm, len(armDecls))
				schemes := make([]TypeScheme, len(armDecls))
				for i, fd := range armDecls {
					// Build the signature body-free under a discarded probe: the arm is fully
					// annotated, so the signature is concrete (no bounds to roll back), and
					// discarding keeps phase 2's inferFunc — which re-derives the signature
					// while checking the body — the single reporter of any signature error.
					p := c.openProbe()
					sig := c.inferFunc(scope, inner, fd.FuncSig, nil, fd)
					c.closeProbe(p, false)
					arms[i] = overloadArm{decl: fd, t: sig, annotated: true}
					schemes[i] = monoScheme(sig)
				}
				bindings[key] = &componentBinding{arms: arms, bound: true, prebound: true}
				scope.defineValue(key.Name(), ValueBinding{Schemes: schemes})
				continue
			}
		}
		v := c.freshAt(inner)
		bindings[key] = &componentBinding{v: v}
		// Pre-bind the group var as a MonoScheme so a mutually-recursive reference
		// resolves through the var itself (instantiate returns it unchanged) before
		// generalization happens in phase 3.
		scope.defineValue(key.Name(), ValueBinding{Schemes: []TypeScheme{monoScheme(v)}})
	}

	// Phase 2: infer each declaration's definition and constrain it <: its var.
	for _, key := range component {
		b, isValue := bindings[key]
		if !isValue {
			continue // non-value keys handled below
		}
		if b.prebound {
			// The signatures are already bound (phase 1); only check each arm's body. The
			// body sees the whole overload set, so a recursive call resolves through
			// resolveOverload against every arm. inferFunc re-derives the (concrete)
			// signature and constrains the body against the return annotation, reporting
			// any body error exactly once.
			for _, arm := range b.arms {
				handled.Add(arm.decl)
				b.sources = append(b.sources, &ast.NodeProvenance{Node: arm.decl})
				c.inferFunc(scope, inner, arm.decl.FuncSig, arm.decl.Body, arm.decl)
			}
			continue
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
			// Accumulate every contributing decl's provenance — every overload arm and
			// any duplicate arm — so a future multi-target go-to-definition can reach
			// all of them. sources[i] lines up with the overload arm at arms[i].
			b.sources = append(b.sources, src)
			// PR8: a definition that is wholly the ErrorType recovery sentinel leaves no
			// bound on the group var (ErrorType absorbs in constrain). Remember it so
			// phase 3 can recover the binding as ErrorType instead of `never`.
			if _, isErr := t.(*soltype.ErrorType); isErr {
				b.recovered = true
			}
			fd, isFunc := d.(*ast.FuncDecl)
			if !b.bound {
				c.constrain(d, t, b.v)
				b.primary = d
				b.bound = true
				vd, isVarDecl := d.(*ast.VarDecl)
				b.isVar = isVarDecl
				// PR8: carry the decl's kind so phase 3 can gate reassignment — a top-level
				// `var` is reassignable (e.g. from a function body that closes over it);
				// a `val`/`fn` is not. A FuncDecl leaves kind at its ValKind zero value.
				if isVarDecl {
					b.kind = vd.Kind
				}
				// PR6: the first FuncDecl arm of a (possibly overloaded) function.
				if isFunc {
					b.arms = append(b.arms, overloadArm{decl: fd, t: t, annotated: isFullyAnnotated(fd.FuncSig)})
				}
				continue
			}
			// The binding already has its primary definition. A repeated FuncDecl is a
			// top-level OVERLOAD (PR6): collect the arm (already inferred independently
			// above) rather than rejecting it; phase 3 binds the full set as a
			// multi-scheme overload binding. Any other repeat — a duplicate `val`/`var`,
			// or a FuncDecl repeating a variable binding (a value cannot be overloaded) —
			// keeps the first and reports.
			if isFunc && !b.isVar {
				b.arms = append(b.arms, overloadArm{decl: fd, t: t, annotated: isFullyAnnotated(fd.FuncSig)})
				continue
			}
			c.report(&DuplicateDeclarationError{
				Decl:     d,
				Previous: b.primary,
				Name:     key.Name(),
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
			c.reportUnsupported(d)
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
		// Overload set (PR6): a name with more than one FuncDecl arm, unless the
		// recursion gate rejected it (a mutually-recursive unannotated overload, which
		// degrades to its first arm below). Generalize each arm into its own scheme and
		// bind the name to the multi-scheme overload set (b.IsOverloaded()); record each
		// arm's display type on its own FuncDecl name for Info.
		//
		// Sources is built from the arms here (not b.sources), so Schemes[i], the arm at
		// arms[i], and Sources[i] all line up — b.sources also carries any rejected
		// duplicate-declaration decls, which would desync the per-scheme index a
		// multi-target go-to-definition relies on.
		if len(b.arms) > 1 && !rejected.Contains(key) {
			schemes := make([]TypeScheme, len(b.arms))
			srcs := make([]provenance.Provenance, len(b.arms))
			for i, arm := range b.arms {
				sc := c.generalize(arm.t, lvl)
				if ps, ok := sc.(*PolyScheme); ok {
					ps.Annotated = arm.annotated
				}
				schemes[i] = sc
				srcs[i] = &ast.NodeProvenance{Node: arm.decl}
				if arm.decl.Name != nil {
					c.recordType(arm.decl.Name, schemeType(sc))
				}
			}
			scope.defineValue(key.Name(), ValueBinding{Schemes: schemes, Sources: srcs})
			continue
		}
		// Generalize the group var at the component's level (was: coalesce to a
		// monotype). Every variable at Level > lvl becomes a quantified type
		// parameter, captured outer variables do not — turning M2's monomorphic
		// freeze into real let-polymorphism (PR1). A rejected overload degrades here to
		// its first arm: b.v carries only the primary (first arm) definition.
		//
		// PR8: a binding whose definition was wholly the ErrorType sentinel left its
		// group var with no bound (ErrorType absorbs in constrain), so generalizing it
		// would freeze the binding to `never` and cascade `<: never` at every later use
		// (a reassignment, a call arg, …). Recover it AS the error sentinel instead,
		// matching the body-level path (inferVarDecl) and keeping downstream uses
		// absorbing. Guarded by an empty group var so a binding that ALSO picked up a
		// real type (a recovered overload arm alongside a good one) still generalizes.
		var scheme TypeScheme
		if b.recovered && len(b.v.LowerBounds) == 0 {
			scheme = &MonoScheme{Ty: &soltype.ErrorType{}}
		} else {
			scheme = c.generalize(b.v, lvl)
		}
		scope.defineValue(key.Name(), ValueBinding{Schemes: []TypeScheme{scheme}, Sources: b.sources, Kind: b.kind})
		// Record the binding's final (coalesced) DISPLAY type in Info on the name
		// node, so it is queryable even for a `val` in a recursive group (where
		// coalescing at definition time would have frozen it to `never`). A VarDecl
		// records on its pattern, a FuncDecl on its name, so a top-level `fn` is
		// queryable through Info exactly like a `val`.
		//
		// NOTE: for a GENERALIZED binding this display type RETAINS its quantified
		// type-parameter variables (it is not var-free), so consumers must render it
		// with soltype.PrintAsScheme — plain soltype.Print renders those vars as the
		// raw `t{ID}` debug form. (renderScheme is the canonical renderer.)
		display := schemeType(scheme)
		switch d := b.primary.(type) {
		case *ast.VarDecl:
			c.recordType(d.Pattern, display)
		case *ast.FuncDecl:
			if d.Name != nil {
				c.recordType(d.Name, display)
			}
		}
	}
}

// checkOverloadAnnotations enforces the PR6 recursion gate and returns the set of
// overloaded keys that failed it (to be degraded to their first arm in phase 3). A
// singleton component is never gated — self-recursion is softer — so only a
// genuinely mutually-recursive group (more than one binding) requires its overloaded
// members to have fully-annotated arms. For each overloaded member whose arms are
// not all annotated, it reports an UnannotatedRecursiveOverloadError blaming the
// first unannotated arm.
func (c *checker) checkOverloadAnnotations(
	g *dep_graph.DepGraph, component []dep_graph.BindingKey,
) set.Set[dep_graph.BindingKey] {
	rejected := set.NewSet[dep_graph.BindingKey]()
	if len(component) <= 1 {
		return rejected // self-recursion is softer; only mutual recursion is gated
	}
	for _, key := range component {
		funcs := pureFuncOverloadDecls(g, key)
		if len(funcs) <= 1 {
			continue // not an overload set (a mixed val/var name is a duplicate, not gated here)
		}
		for _, fd := range funcs {
			if !isFullyAnnotated(fd.FuncSig) {
				c.report(&UnannotatedRecursiveOverloadError{Decl: fd, Name: key.Name()})
				rejected.Add(key)
				break // one diagnostic per overloaded binding (blame the first gap)
			}
		}
	}
	return rejected
}

// pureFuncOverloadDecls returns the FuncDecls bound to key when the name is bound
// ONLY by FuncDecls (a candidate overload set — the slice may have length 1), or nil
// when a `val`/`var` shares the name (then it is a duplicate-declaration situation,
// not an overload). Shared by the recursion gate and annotatedOverloadArms so both
// classify a name as an overload set the same way.
func pureFuncOverloadDecls(g *dep_graph.DepGraph, key dep_graph.BindingKey) []*ast.FuncDecl {
	if key.Kind() != dep_graph.DepKindValue {
		return nil
	}
	decls := g.GetDecls(key)
	funcs := make([]*ast.FuncDecl, 0, len(decls))
	for _, d := range decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			return nil // a val/var shares this name: not a pure overload set
		}
		funcs = append(funcs, fd)
	}
	return funcs
}

// annotatedOverloadArms returns key's FuncDecls when it is a pure function-overload
// set that can be PRE-BOUND from signatures alone (PR6): more than one FuncDecl, NO
// `val`/`var` mixed under the name, and every arm fully annotated (so its signature
// is known without inferring the body). Returns nil otherwise — those keys take the
// ordinary group-var path, where each arm is inferred independently and the set is
// assembled in phase 3.
func annotatedOverloadArms(g *dep_graph.DepGraph, key dep_graph.BindingKey) []*ast.FuncDecl {
	funcs := pureFuncOverloadDecls(g, key)
	if len(funcs) <= 1 {
		return nil // not an overload set
	}
	for _, fd := range funcs {
		if !isFullyAnnotated(fd.FuncSig) {
			return nil // an un-annotated arm can't be pre-bound from its signature
		}
	}
	return funcs
}

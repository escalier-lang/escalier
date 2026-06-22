package solver

import (
	"sort"

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
// Inference is MONOMORPHIC — M1 ships no schemes, so each binding's var stays as its
// coalesced monomorphic type; the generalization that yields <T0> rendering is
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
	// M4 E3: dep_graph fans one top-level destructuring `val {x, y} = …` across one
	// SCC component per leaf key. Its initializer is typed and its pattern bound
	// once, memoized here on the first leaf reached. Each leaf component then
	// constrains its own projected type into its binding var.
	destructured := map[*ast.VarDecl]*moduleDestructure{}
	for _, component := range g.Components {
		c.inferComponent(scope, lvl, module, g, component, handled, destructured)
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
	// recording, and (via isVarDecl) to tell an overload arm from a duplicate.
	primary ast.Decl
	// infoNode is the AST node phase 3 records the binding's generalized display type
	// on, for Info and go-to-definition. It is the binding's own name-bearing node: a
	// VarDecl's pattern, a FuncDecl's name, or — for a top-level destructuring — the
	// individual pattern leaf this key binds, e.g. the `a` IdentPat of `val [a, b] = …`
	// (M4 E3). A destructuring records per leaf rather than on the whole decl pattern,
	// which its sibling leaf keys share.
	infoNode ast.Node
	bound    bool // a definition was inferred and constrained
	// isVarDecl reports whether the primary decl is a VarDecl, i.e. a `val` or a `var`
	// rather than a function. It is NOT the `var`-vs-`val` distinction — that is kind.
	isVarDecl bool
	kind      ast.VariableKind // PR8: the primary definition's kind — VarKind ⇒ reassignable
	// recovered marks that a contributing definition was WHOLLY the ErrorType recovery
	// sentinel (e.g. `val a = <unknown ident>`). ErrorType absorbs in constrain, so it
	// leaves no bound on the binding var; phase 3 uses this to recover the binding AS
	// ErrorType rather than freezing an unbound var to `never` (which would cascade
	// `<: never` at every later use). See PR8 / inferComponent phase 3.
	recovered bool
	// arms collects every top-level FuncDecl under this key (PR6). len <= 1 is an
	// ordinary function (or a non-function binding, len == 0); len > 1 is an overload
	// set, bound as a multi-scheme ValueBinding in phase 3. arms[i] lines up with the
	// FuncDecl's entry in sources.
	arms []overloadArm
	// signatureBound marks an overload set whose arms are ALL fully annotated, so phase
	// 1 could build their signatures (body-free) and pre-bind the whole set in scope
	// (b.arms holds the signature types). References WITHIN the component — recursive
	// calls and value captures — then resolve against the set rather than a single
	// first-arm var; phase 2 only checks each arm's body. An overload with any
	// un-annotated arm cannot be signature-bound (its signature isn't known without
	// inferring the body), so it stays on the ordinary group-var path and cannot be
	// mutually recursive (the gate forbids it). See checkOverloadAnnotations / annotatedOverloadArms.
	signatureBound bool
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
	scope *Scope, lvl int, module *ast.Module, g *dep_graph.DepGraph,
	component []dep_graph.BindingKey, handled set.Set[ast.Decl],
	destructured map[*ast.VarDecl]*moduleDestructure,
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
		// first-arm binding var (which would make a recursive arm — or a value capture —
		// type-check against the wrong overload). The recursion gate guarantees a
		// mutually-recursive overload IS fully annotated, so this is exactly the set it
		// requires to be ground before bodies are inferred.
		if !rejected.Contains(key) {
			if armDecls := annotatedOverloadArms(g, key); armDecls != nil {
				sortArmDecls(module, armDecls)
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
				bindings[key] = &componentBinding{arms: arms, bound: true, signatureBound: true}
				scope.defineValue(key.Name(), ValueBinding{Schemes: schemes})
				continue
			}
		}
		v := c.freshAt(inner)
		bindings[key] = &componentBinding{v: v}
		// Pre-bind the binding var as a MonoScheme so a mutually-recursive reference
		// resolves through the var itself (instantiate returns it unchanged) before
		// generalization happens in phase 3.
		scope.defineValue(key.Name(), ValueBinding{Schemes: []TypeScheme{monoScheme(v)}})
	}

	// Phase 2: infer each declaration's definition and constrain it <: its var.
	for _, key := range component {
		// Each top-level binding is its own named-lifetime scope, mirroring inferFunc:
		// two declarations that both write `&'a` get independent lifetimes rather than
		// sharing one through a stale map. A function decl re-clears this inside inferFunc.
		c.namedLifetimes = nil
		b, isValue := bindings[key]
		if !isValue {
			continue // non-value keys handled below
		}
		if b.signatureBound {
			// The schemes are already bound in scope from phase 1, so phase 2 does not
			// re-bind them; it re-infers each arm with its body. inferFunc re-derives the
			// signature and checks the body against the return annotation. Phase 1 discarded
			// its signature errors under a probe, so this pass is the single reporter of BOTH
			// the signature and the body errors. The body sees the whole overload set, so a
			// recursive call resolves through resolveOverload against every arm.
			for _, arm := range b.arms {
				handled.Add(arm.decl)
				b.sources = append(b.sources, &ast.NodeProvenance{Node: arm.decl})
				c.inferFunc(scope, inner, arm.decl.FuncSig, arm.decl.Body, arm.decl)
			}
			continue
		}
		for _, d := range g.GetDecls(key) {
			// M4 E3: a top-level destructuring `val [a, b] = …` / `val {x, y} = …`
			// registers one decl under one leaf key per bound name. Bind it per key it
			// appears under, before the handled-dedup that gates ordinary decls, so a
			// destructuring decl sharing a name with another decl still binds its other
			// leaves and the collision reports as a duplicate rather than as an
			// unsupported pattern.
			if vd := asDestructureDecl(d); vd != nil {
				c.bindModuleDestructureLeaf(scope, inner, vd, g, key, b, handled, destructured)
				continue
			}
			if handled.Contains(d) {
				// Already inferred under another key this decl registers under, e.g. a
				// class contributing both a value and a type key. Skipping avoids
				// re-inferring and re-reporting it.
				continue
			}
			handled.Add(d)
			t, src, ok := c.inferDeclDef(scope, inner, d)
			if !ok {
				continue
			}
			// Accumulate every contributing decl's provenance in encounter order: the
			// primary, every overload arm, and every decl later rejected as a duplicate.
			// This append is unconditional, so a duplicate lands here even though it is NOT
			// added to arms. b.sources can therefore DESYNC from arms when a duplicate
			// interleaves: `fn f; val f; fn f` yields sources [fn, val, fn] against arms
			// [fn, fn]. So do NOT index b.sources by arm position. Today only Sources[0],
			// the primary decl, is ever read, by bindingDecl; phase 3's overload branch
			// rebuilds its per-scheme sources from arms rather than from this list. The full
			// list is kept only for a future multi-target go-to-definition that wants to
			// reach every contributing decl, duplicates included.
			b.sources = append(b.sources, src)
			// PR8: a definition that is wholly the ErrorType recovery sentinel leaves no
			// bound on the binding var (ErrorType absorbs in constrain). Remember it so
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
				b.isVarDecl = isVarDecl
				// Record where phase 3 stamps the display type: a `val`/`var` on its
				// pattern, a `fn` on its name. A destructuring leaf overrides this with its
				// own leaf node in bindModuleDestructureLeaf.
				if isVarDecl {
					b.infoNode = vd.Pattern
				} else if isFunc {
					b.infoNode = fd.Name
				}
				// PR8: carry the decl's kind so phase 3 can gate reassignment — a top-level
				// `var` is reassignable (e.g. from a function body that closes over it);
				// a `val`/`fn` is not. A FuncDecl leaves kind at its ValKind zero value.
				if isVarDecl {
					b.kind = vd.Kind
					// M4 B3: an un-annotated `var` widens at coalesce time, so its literal
					// initializer reads back as the primitive (`var a = 5` ⇒ number) and a
					// later reassignment of the same primitive checks. An annotated `var`
					// adopts its annotation, which needs no widening.
					if vd.Kind == ast.VarKind && vd.TypeAnn == nil {
						b.v.Widenable = true
					}
				}
				// PR6: when the primary decl is a function, record it as the first arm. If
				// more FuncDecls follow under this name it becomes an overload set; otherwise
				// it stays a lone function.
				if isFunc {
					b.arms = append(b.arms, overloadArm{decl: fd, t: t, annotated: isFullyAnnotated(fd.FuncSig)})
				}
				continue
			}
			// Past the first decl, the binding already has its primary definition. PR6
			// treats a repeated FuncDecl as another overload arm. It was already inferred
			// independently above, so collect it and let phase 3 bind the full set as a
			// multi-scheme overload binding. Every other repeat keeps the first decl and
			// reports a duplicate — a second `val`/`var`, or a FuncDecl colliding with a
			// variable binding, since a value cannot be overloaded.
			if isFunc && !b.isVarDecl {
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
		// A PR6 overload set is a name with more than one FuncDecl arm. This branch binds
		// such a set when the recursion gate did not reject it. A rejected set is a
		// mutually-recursive unannotated overload. It is excluded here and degrades to its
		// first arm below.
		//
		// Binding an accepted set takes four steps, each detailed at its block below:
		//
		//  1. sort the arms into source-position order;
		//  2. reject any two arms with indistinguishable parameter types;
		//  3. generalize each arm into its own scheme and record its display type for Info;
		//  4. bind the name to the multi-scheme overload set that b.IsOverloaded() detects.
		//
		// Sources is built from the arms here rather than from b.sources, so Schemes[i],
		// the arm at arms[i], and Sources[i] all line up. b.sources also carries any
		// rejected duplicate-declaration decls, which would desync the per-scheme index
		// that a multi-target go-to-definition relies on.
		if len(b.arms) > 1 && !rejected.Contains(key) {
			// A signature-bound set was already sorted in phase 1, so this stable re-sort is
			// a no-op for it. It also orders the ordinary group-var path's arms the same way.
			sortArms(module, b.arms)
			// An overload set compiles to a single runtime function that dispatches on
			// argument types, so two arms accepting exactly the same arguments cannot be told
			// apart at codegen. Report a DuplicateOverloadError on each such arm, pointing at
			// the earlier arm it duplicates. The set is still bound below, a best-effort
			// recovery so later references and value-position use still resolve.
			for i := range b.arms {
				fi, ok := b.arms[i].t.(*soltype.FuncType)
				if !ok {
					continue
				}
				for j := range i {
					fj, ok := b.arms[j].t.(*soltype.FuncType)
					if !ok {
						continue
					}
					if equallySpecific(fj, fi) {
						c.report(&DuplicateOverloadError{
							Decl:     b.arms[i].decl,
							Previous: b.arms[j].decl,
							Name:     key.Name(),
						})
						break
					}
				}
			}
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
			scope.defineValue(key.Name(), ValueBinding{Schemes: schemes, Sources: srcs, ModuleLevel: true})
			continue
		}
		// Generalize the binding var at the component's level (was: coalesce to a
		// monotype). Every variable at Level > lvl becomes a quantified type
		// parameter, captured outer variables do not — turning M2's monomorphic
		// freeze into real let-polymorphism (PR1). A rejected overload degrades here to
		// its first arm: b.v carries only the primary (first arm) definition.
		//
		// PR8: a binding whose definition was wholly the ErrorType sentinel left its
		// binding var with no bound (ErrorType absorbs in constrain), so generalizing it
		// would freeze the binding to `never` and cascade `<: never` at every later use
		// (a reassignment, a call arg, …). Recover it AS the error sentinel instead,
		// matching the body-level path (inferVarDecl) and keeping downstream uses
		// absorbing. Guarded by an empty binding var so a binding that ALSO picked up a
		// real type (a recovered overload arm alongside a good one) still generalizes.
		var scheme TypeScheme
		if b.recovered && len(b.v.LowerBounds) == 0 {
			scheme = &MonoScheme{Ty: &soltype.ErrorType{}}
		} else {
			scheme = c.generalize(b.v, lvl)
		}
		scope.defineValue(key.Name(), ValueBinding{Schemes: []TypeScheme{scheme}, Sources: b.sources, Kind: b.kind, ModuleLevel: true})
		// Record the binding's final (coalesced) DISPLAY type in Info on infoNode, so
		// it is queryable even for a `val` in a recursive group (where coalescing at
		// definition time would have frozen it to `never`). infoNode is the binding's
		// name-bearing node — a VarDecl's pattern, a FuncDecl's name, or a destructuring
		// leaf — so a top-level `fn` is queryable through Info exactly like a `val`.
		//
		// NOTE: for a GENERALIZED binding this display type RETAINS its quantified
		// type-parameter variables (it is not var-free), so consumers must render it
		// with soltype.PrintAsScheme — plain soltype.Print renders those vars as the
		// raw `t{ID}` debug form. (renderScheme is the canonical renderer.)
		display := schemeType(scheme)
		if b.infoNode != nil {
			c.recordType(b.infoNode, display)
		}
	}
}

// moduleDestructure memoizes a top-level destructuring decl's typed pattern (M4
// E3). dep_graph fans one `val {x, y} = …` across one component per bound name, so
// every leaf key points at the same decl. The pattern must be typed and bound only
// ONCE — typing the initializer or re-binding the pattern per leaf would re-report
// its errors and duplicate work — so the first leaf key whose component is processed
// in topological order types it and stores the result here, keyed by the decl. Every
// later leaf key reuses this memo and only constrains its own projected type into its
// binding var. There is nothing special about which leaf is "first"; the memo just
// makes whichever one runs first the single typing site.
//
// leaves maps each bound name to that projection plus its pattern leaf node. ok is
// false when the decl had no initializer, in which case MissingInitializerError was
// already reported. recovered marks an initializer that fell back to the ErrorType
// sentinel, so each leaf recovers AS the sentinel rather than freezing to `never`.
type moduleDestructure struct {
	leaves    map[string]destructureLeaf
	recovered bool
	ok        bool
}

// destructureLeaf is one bound leaf of a memoized destructuring pattern: its
// projected type and the pattern leaf node it was bound from.
type destructureLeaf struct {
	t    soltype.Type
	node ast.Node
}

// asDestructureDecl returns d as a destructuring VarDecl, a top-level `val`/`var`
// whose pattern is not a plain IdentPat, or nil when d is not one. dep_graph
// registers such a decl under one value key per bound name, and phase 2 binds each
// leaf under the key it appears. Detecting it per decl rather than per key lets a
// destructuring decl that shares a key with another decl bind its leaves and report
// the collision as a duplicate.
func asDestructureDecl(d ast.Decl) *ast.VarDecl {
	vd, ok := d.(*ast.VarDecl)
	if !ok || vd.Pattern == nil {
		return nil
	}
	if _, named := varName(vd); named {
		return nil // a plain IdentPat `val x = …` takes the ordinary path
	}
	return vd
}

// bindModuleDestructureLeaf binds one leaf of a top-level destructuring decl into
// its pre-bound binding var (M4 E3). destructurePattern types the decl's pattern
// once and memoizes it. This call looks up THIS key's projected leaf type and
// constrains it into b.v, so phase 3 generalizes the leaf independently. `key` is
// this leaf's binding key. Its bound name is the key name minus the decl's namespace
// prefix, matching the name dep_graph registered the leaf under.
func (c *checker) bindModuleDestructureLeaf(
	scope *Scope, lvl int, d *ast.VarDecl, g *dep_graph.DepGraph,
	key dep_graph.BindingKey, b *componentBinding, handled set.Set[ast.Decl],
	destructured map[*ast.VarDecl]*moduleDestructure,
) {
	md := c.destructurePattern(scope, lvl, d, handled, destructured)
	if !md.ok {
		return // no initializer, so the leaf stays unbound and is removed in phase 3
	}
	leaf, found := md.leaves[leafName(g, key)]
	if !found {
		// The pattern bound no leaf for this name, because destructurePattern already
		// reported a field the scrutinee lacks or a wrong tuple arity. Leave the leaf
		// unbound. Phase 3 removes it.
		return
	}
	if b.bound {
		// Another declaration already bound this name. A destructured leaf is neither
		// an overload arm nor a merge, so the collision is a duplicate declaration.
		// Keep the first binding and blame the destructuring decl.
		c.report(&DuplicateDeclarationError{Decl: d, Previous: b.primary, Name: key.Name()})
		return
	}
	c.constrain(d, leaf.t, b.v)
	b.primary = d
	b.infoNode = leaf.node
	b.isVarDecl = true
	b.kind = d.Kind
	b.bound = true
	b.sources = append(b.sources, &ast.NodeProvenance{Node: d})
	if md.recovered {
		b.recovered = true
	}
	// M4 B3: an un-annotated `var` leaf widens at coalesce time, so a literal it
	// binds reads back as the primitive and a later reassignment of the same
	// primitive checks. An annotated `var` adopts its annotation, which needs no
	// widening. A `val` leaf is a fixed singleton.
	if d.Kind == ast.VarKind && d.TypeAnn == nil {
		b.v.Widenable = true
	}
}

// destructurePattern types a top-level destructuring decl's initializer and binds
// its pattern ONCE, memoizing the per-leaf projected types so each leaf component
// reuses them (M4 E3). The initializer flows through the shared inferVarDeclInit
// core, so an annotation is honored and a `var` initializer is widened, exactly as
// the body-level path does. The pattern is bound with a collecting emit that records
// each leaf's projection without defining it in scope. Phase 1 already pre-bound
// each leaf name as its binding var, and phase 3 redefines it generalized. The decl
// is added to handled so the reconciliation pass does not report it as an unmodeled
// top-level declaration.
func (c *checker) destructurePattern(
	scope *Scope, lvl int, d *ast.VarDecl, handled set.Set[ast.Decl],
	destructured map[*ast.VarDecl]*moduleDestructure,
) *moduleDestructure {
	if md, done := destructured[d]; done {
		return md
	}
	handled.Add(d)
	md := &moduleDestructure{leaves: map[string]destructureLeaf{}}
	destructured[d] = md
	initType, ok := c.inferVarDeclInit(scope, lvl, d)
	if !ok {
		return md // MissingInitializerError already reported, so md.ok stays false
	}
	md.ok = true
	_, md.recovered = initType.(*soltype.ErrorType)
	emit := func(_ *Scope, name string, t soltype.Type, node ast.Node) {
		md.leaves[name] = destructureLeaf{t: t, node: node}
	}
	c.bindPatternWith(scope, lvl, d.Pattern, initType, nil, emit)
	return md
}

// leafName returns a destructuring leaf key's bound name: the key's qualified name
// with the decl's namespace prefix stripped, the inverse of dep_graph's
// ModuleBindingVisitor.qualifyName. A leaf bound under namespace `A.B` as `x` has
// key name `A.B.x`, so stripping `A.B.` recovers the pattern leaf name `x`.
func leafName(g *dep_graph.DepGraph, key dep_graph.BindingKey) string {
	name := key.Name()
	if ns := g.GetNamespace(key); ns != "" {
		return name[len(ns)+1:]
	}
	return name
}

// checkOverloadAnnotations enforces the PR6 recursion gate. It returns the set of
// overloaded keys that failed the gate; phase 3 degrades each of them to its first arm.
// A singleton component is never gated, since self-recursion is softer. Only a
// genuinely mutually-recursive group of more than one binding requires its overloaded
// members to have fully-annotated arms. For each overloaded member whose arms are not
// all annotated, it reports an UnannotatedRecursiveOverloadError blaming the first
// unannotated arm.
func (c *checker) checkOverloadAnnotations(
	g *dep_graph.DepGraph, component []dep_graph.BindingKey,
) set.Set[dep_graph.BindingKey] {
	rejected := set.NewSet[dep_graph.BindingKey]()
	if len(component) <= 1 {
		return rejected // self-recursion is softer; only mutual recursion is gated
	}
	for _, key := range component {
		funcs := funcOnlyDecls(g, key)
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

// funcOnlyDecls returns the FuncDecls bound to key when the name is bound ONLY by
// FuncDecls, or nil when any `val`/`var` shares the name. A func-only result is a
// candidate overload set. Its slice may have length 1, so it is not necessarily an
// overload yet. A mixed result is nil because the clash is a duplicate declaration, not
// an overload. Shared by the recursion gate and annotatedOverloadArms so both classify
// a name the same way.
func funcOnlyDecls(g *dep_graph.DepGraph, key dep_graph.BindingKey) []*ast.FuncDecl {
	if key.Kind() != dep_graph.DepKindValue {
		return nil
	}
	decls := g.GetDecls(key)
	funcs := make([]*ast.FuncDecl, 0, len(decls))
	for _, d := range decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			return nil // a val/var shares this name: the key is not function-only
		}
		funcs = append(funcs, fd)
	}
	return funcs
}

// annotatedOverloadArms returns key's FuncDecls when it is a function-only overload
// set that can be PRE-BOUND from signatures alone (PR6): more than one FuncDecl, NO
// `val`/`var` mixed under the name, and every arm fully annotated (so its signature
// is known without inferring the body). Returns nil otherwise — those keys take the
// ordinary group-var path, where each arm is inferred independently and the set is
// assembled in phase 3.
func annotatedOverloadArms(g *dep_graph.DepGraph, key dep_graph.BindingKey) []*ast.FuncDecl {
	funcs := funcOnlyDecls(g, key)
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

// armPosLess orders two overload arms by SOURCE POSITION: file path (alphabetical),
// then line, then column. This is the canonical "declaration order" the overload
// resolver falls back to when specificity is a tie (see overload.go) — pinned to
// position rather than to the order sources happened to reach the parser, so a name
// whose arms span several files in a lib/ resolves as "first matching arm, reading
// top-to-bottom, file by file alphabetically". module maps a Span's SourceID back to
// its path; arms within one file compare by line then column.
func armPosLess(module *ast.Module, a, b *ast.FuncDecl) bool {
	as, bs := a.Span(), b.Span()
	ap, bp := module.GetSourcePath(as.SourceID), module.GetSourcePath(bs.SourceID)
	if ap != bp {
		return ap < bp
	}
	if as.Start.Line != bs.Start.Line {
		return as.Start.Line < bs.Start.Line
	}
	return as.Start.Column < bs.Start.Column
}

// sortArmDecls stable-sorts overload arm declarations into source-position order
// (armPosLess). Used in phase 1 to order a signature-bound set's signatures so recursive
// resolution within the component matches the final exported order.
func sortArmDecls(module *ast.Module, decls []*ast.FuncDecl) {
	sort.SliceStable(decls, func(i, j int) bool {
		return armPosLess(module, decls[i], decls[j])
	})
}

// sortArms stable-sorts collected overload arms into source-position order
// (armPosLess), keeping each arm's scheme/source/Info index aligned. Used in phase 3
// before the multi-scheme binding is assembled.
func sortArms(module *ast.Module, arms []overloadArm) {
	sort.SliceStable(arms, func(i, j int) bool {
		return armPosLess(module, arms[i].decl, arms[j].decl)
	})
}

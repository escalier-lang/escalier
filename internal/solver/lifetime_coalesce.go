package solver

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Display-time lifetime coalescing (M4 D4). The structural coalescers (coalesce /
// coalesceScheme) rebuild a type through the shared visitor, which carries every
// RefType lifetime through unchanged because a Lifetime is not a Type. They leave
// the RAW lifetime variables in place — a borrow parameter's originated lifetime, a
// multi-source join variable, and any instantiation-freshened intermediary. This
// pass runs once over the finished coalesced type and resolves those lifetimes to
// their display form, the lifetime-sort analogue of how the var arms resolve a type
// variable.
//
// It has three jobs, all keyed off a single occurrence analysis plus a
// connected-component grouping of the lifetime bound graph:
//
//  1. Naming. A borrow originates at a parameter, so a lifetime occurring in a
//     NEGATIVE (input) position is a "param lifetime". It is the only kind named in
//     the output. The printer assigns it 'a, 'b, … from the variables this pass
//     leaves in the type.
//
//  2. Elision. A param lifetime whose borrow never reaches an output connects
//     nothing — it occurs in no positive position and its bound-graph component
//     holds no output lifetime — so it is dropped, the lifetime-sort analogue of
//     single-polarity type-variable elimination. Dropping branches on Mut: a
//     mutable borrow becomes owned-mutable (RefType{Mut: true, Lt: nil}); an
//     immutable borrow drops the RefType wrapper entirely (its bare inner), because
//     RefType{false, nil} is the forbidden degenerate cell NewRef rejects.
//
//  3. Join expansion. A non-param lifetime — a join variable minted at a return or
//     branch, or a lifetime freshened when a borrow-passing function was
//     instantiated — is not itself nameable. It expands to the union of the param
//     lifetimes it shares a bound-graph component with, so a return uniting two
//     borrows coalesces to ('a | 'b). The expansion follows the UNDIRECTED bound
//     graph: instantiation interposes intermediary variables between a call's
//     argument lifetime and the callee's freshened parameter lifetime, related only
//     by a mix of upper and lower bounds, so reachability cannot be confined to one
//     bound direction. A lifetime forced to 'static renders 'static and absorbs.
func coalesceLifetimes(t soltype.Type) soltype.Type {
	occ := map[*soltype.LifetimeVar]occPolarity{}
	t.Accept(&ltOccVisitor{occ: occ}, soltype.Positive)

	a := newLtAnalysis(occ)
	return t.Accept(&ltRewriter{a: a}, soltype.Positive)
}

// ltOccVisitor records the polarities each lifetime variable occurs in
// structurally. A RefType lifetime is COVARIANT — it lives on the wrapper, not in
// the inner — so it is recorded in the borrow's own polarity; the mut-driven write
// view that flips the inner never touches it.
type ltOccVisitor struct {
	occ map[*soltype.LifetimeVar]occPolarity
}

func (v *ltOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if r, ok := t.(*soltype.RefType); ok {
		if lv, ok := r.Lt.(*soltype.LifetimeVar); ok {
			if pol == soltype.Positive {
				v.occ[lv] |= occPos
			} else {
				v.occ[lv] |= occNeg
			}
		}
	}
	return soltype.EnterResult{}
}

func (v *ltOccVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// ltAnalysis is the precomputed input the rewriter reads: per-variable structural
// occurrence, the connected-component grouping of the lifetime bound graph, and the
// set of component roots that hold an output (positive) lifetime.
type ltAnalysis struct {
	occ      map[*soltype.LifetimeVar]occPolarity
	uf       *unionFind                   // components over lifetime bound edges
	vars     map[int]*soltype.LifetimeVar // every lifetime var reachable, by ID
	posRoots set.Set[int]                 // component roots reaching a positive occurrence
}

// newLtAnalysis builds the bound-graph components from the structurally-occurring
// lifetime variables. It walks each occurring variable's bounds transitively in
// BOTH directions, unioning a variable with every lifetime variable it is bounded
// by or bounds, so an instantiation intermediary ends up in the same component as
// the argument and parameter lifetimes it bridges. A component root is marked
// positive when any structurally-positive lifetime falls in it; that is what keeps
// a connected param lifetime from being elided.
func newLtAnalysis(occ map[*soltype.LifetimeVar]occPolarity) *ltAnalysis {
	uf := newUnionFind()
	vars := map[int]*soltype.LifetimeVar{}
	var visit func(v *soltype.LifetimeVar)
	visitBound := func(v *soltype.LifetimeVar, b soltype.Lifetime) {
		bv, ok := b.(*soltype.LifetimeVar)
		if !ok {
			return
		}
		uf.union(v.ID, bv.ID)
		visit(bv)
	}
	visit = func(v *soltype.LifetimeVar) {
		if _, seen := vars[v.ID]; seen {
			return
		}
		vars[v.ID] = v
		for _, b := range v.LowerBounds {
			visitBound(v, b)
		}
		for _, b := range v.UpperBounds {
			visitBound(v, b)
		}
	}
	for v := range occ {
		visit(v)
	}

	posRoots := set.NewSet[int]()
	for v, pols := range occ {
		if pols&occPos != 0 {
			posRoots.Add(uf.find(v.ID))
		}
	}
	return &ltAnalysis{occ: occ, uf: uf, vars: vars, posRoots: posRoots}
}

// isParam reports whether v is a borrow-origin (param) lifetime: one occurring in a
// negative position. Only param lifetimes are named.
func (a *ltAnalysis) isParam(v *soltype.LifetimeVar) bool {
	return a.occ[v]&occNeg != 0
}

// kept reports whether a param lifetime survives elision: its bound-graph component
// reaches an output, so the borrow flows somewhere observable. A param occurring
// only on its parameter, connected to no output, is elided.
func (a *ltAnalysis) kept(v *soltype.LifetimeVar) bool {
	return a.posRoots.Contains(a.uf.find(v.ID))
}

// componentParams returns the kept param lifetimes sharing v's component, sorted by
// ID. Sorting yields a canonical union member order, so a join expanded here renders
// the same ('a | 'b) regardless of bound-list order and ltEqual's positional member
// compare stays order-insensitive (closes the order gap noted in coalesce.go).
func (a *ltAnalysis) componentParams(v *soltype.LifetimeVar) []soltype.Lifetime {
	root := a.uf.find(v.ID)
	var ids []int
	for id, lv := range a.vars {
		if a.uf.find(id) != root || !a.isParam(lv) || !a.kept(lv) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	members := make([]soltype.Lifetime, len(ids))
	for i, id := range ids {
		members[i] = a.vars[id]
	}
	return members
}

// resolveLt maps a lifetime variable to its display form, or reports elide=true when
// the borrow connects nothing and the wrapper should drop.
func (a *ltAnalysis) resolveLt(v *soltype.LifetimeVar) (lt soltype.Lifetime, elide bool) {
	if forcedToStatic(v) {
		return soltype.Static, false
	}
	if a.isParam(v) {
		if a.kept(v) {
			return v, false // a named param renders under its own quantified name
		}
		return nil, true // connect-nothing param: elide
	}
	// A non-param lifetime (join or instantiation intermediary) is not nameable; it
	// expands to the union of the param lifetimes in its component.
	members := a.componentParams(v)
	switch len(members) {
	case 0:
		return nil, true
	case 1:
		return members[0], false
	default:
		return &soltype.LifetimeUnion{Lifetimes: members}, false
	}
}

// ltRewriter applies the analysis to a coalesced type, resolving each RefType's
// lifetime and eliding the wrapper where the borrow connects nothing. It runs in
// ExitType so a nested borrow is resolved before the borrow that contains it.
type ltRewriter struct {
	a *ltAnalysis
}

func (r *ltRewriter) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	return soltype.EnterResult{}
}

func (r *ltRewriter) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	rt, ok := t.(*soltype.RefType)
	if !ok || rt.Lt == nil {
		return t
	}
	lv, ok := rt.Lt.(*soltype.LifetimeVar)
	if !ok {
		return t // already a concrete display lifetime ('static)
	}
	resolved, elide := r.a.resolveLt(lv)
	if elide {
		if rt.Mut {
			// NewRef would collapse (true, nil) back to its inner; keep the
			// owned-mutable wrapper by constructing it directly.
			return &soltype.RefType{Mut: true, Lt: nil, Inner: rt.Inner}
		}
		// RefType{false, nil} is the forbidden degenerate cell — drop the wrapper.
		return rt.Inner
	}
	return &soltype.RefType{Mut: rt.Mut, Lt: resolved, Inner: rt.Inner}
}

// forcedToStatic reports whether a lifetime variable has 'static among its bounds,
// in which case it coalesces to 'static — the escape-to-static outcome. Both bound
// directions are checked: the escape constraint `v <: 'static` adds 'static as an
// upper bound, while a lower-bound 'static can arise from a join member.
func forcedToStatic(v *soltype.LifetimeVar) bool {
	return soltype.ContainsLifetime(v.LowerBounds, soltype.Static) ||
		soltype.ContainsLifetime(v.UpperBounds, soltype.Static)
}

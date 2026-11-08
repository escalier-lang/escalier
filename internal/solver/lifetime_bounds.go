package solver

import (
	"maps"
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ltBoundSet is a directed outlives graph over lifetime-variable IDs, condensed over
// outlives-equivalent lifetimes. An edge a -> b reads
// "'a outlives 'b", written 'a: 'b. That is the direction constrainLt records as a in
// b.LowerBounds and b in a.UpperBounds.
//
// A mutual constraint 'a: 'b together with 'b: 'a makes the two lifetimes outlive each
// other, so they are equal. Such a cycle collapses into one strongly-connected-component
// representative. Every edge, query, and rendering is keyed by representative ID, not
// raw lifetime ID.
type ltBoundSet struct {
	edges  map[int]set.Set[int]         // rep -> reps it outlives, condensed
	rep    map[int]int                  // lifetime ID -> its SCC representative ID
	vars   map[int]*soltype.LifetimeVar // representative ID -> a member var, for rendering
	static set.Set[int]                 // representative IDs forced to 'static, the absorbing bottom
}

// buildLtBoundSet walks the occurring lifetime variables' bound lists directionally
// and returns the condensed outlives graph over them. It records an edge per real
// outlives relation rather than the symmetric merge newLtAnalysis's union-find does,
// then condenses each strongly connected component to one representative so the result
// is a DAG.
//
// The occurrence map's keys seed the walk. The polarity values are unused here, since
// the outlives edges live entirely in the bound lists.
func buildLtBoundSet(occ map[*soltype.LifetimeVar]occPolarity) *ltBoundSet {
	vars := map[int]*soltype.LifetimeVar{}
	rawEdges := map[int]set.Set[int]{}

	addEdge := func(from, to int) {
		s, ok := rawEdges[from]
		if !ok {
			s = set.NewSet[int]()
			rawEdges[from] = s
		}
		s.Add(to)
	}

	var visit func(v *soltype.LifetimeVar)
	visit = func(v *soltype.LifetimeVar) {
		if _, seen := vars[v.ID]; seen {
			return
		}
		vars[v.ID] = v
		// v outlives each of its upper bounds, so record an edge v -> ub. Each of v's
		// lower bounds outlives v, so record lb -> v. constrainLt writes both directions
		// of a var-to-var relation, so reading either list alone would suffice for the
		// edges. Walking both is what discovers a variable reachable only through a
		// lower-bound link.
		for _, b := range v.UpperBounds {
			if bv, ok := b.(*soltype.LifetimeVar); ok {
				addEdge(v.ID, bv.ID)
				visit(bv)
			}
		}
		for _, b := range v.LowerBounds {
			if bv, ok := b.(*soltype.LifetimeVar); ok {
				addEdge(bv.ID, v.ID)
				visit(bv)
			}
		}
	}
	for v := range occ {
		visit(v)
	}

	rep := condenseSCCs(slices.Sorted(maps.Keys(vars)), rawEdges)

	edges := map[int]set.Set[int]{}
	for from, tos := range rawEdges {
		rf := rep[from]
		for to := range tos {
			rt := rep[to]
			if rf == rt {
				continue // an intra-component edge is collapsed away
			}
			s, ok := edges[rf]
			if !ok {
				s = set.NewSet[int]()
				edges[rf] = s
			}
			s.Add(rt)
		}
	}

	// A variable is forced to 'static only by an UPPER-bound 'static, the escape
	// constraint v <: 'static. A lower-bound 'static means 'static outlives v, which is
	// trivially true and forces nothing, so forcedToStatic, which reads both directions,
	// must not be used here.
	static := set.NewSet[int]()
	for id, v := range vars {
		if soltype.ContainsLifetime(v.UpperBounds, soltype.Static) {
			static.Add(rep[id])
		}
	}
	// Propagate 'static backward along outlives edges. A lifetime that outlives a
	// 'static-forced lifetime is itself 'static, since only 'static outlives 'static.
	// This closure keeps the static set correct even when the caller has not already
	// propagated the escape constraint through the graph.
	work := static.ToSlice()
	for len(work) > 0 {
		cur := work[len(work)-1]
		work = work[:len(work)-1]
		for from, tos := range edges {
			if !static.Contains(from) && tos.Contains(cur) {
				static.Add(from)
				work = append(work, from)
			}
		}
	}

	repVars := map[int]*soltype.LifetimeVar{}
	for id, v := range vars {
		if rep[id] == id {
			repVars[id] = v
		}
	}

	return &ltBoundSet{edges: edges, rep: rep, vars: repVars, static: static}
}

// condenseSCCs finds the strongly connected components of the directed graph given by
// adjacency edges over the node IDs, and returns a map from every node ID to its
// component representative, the smallest ID in the component. A cycle in the outlives
// relation means mutually-outliving, hence equal, lifetimes, so folding each component
// to one representative turns the raw graph into a DAG. The shared Tarjan pass finds the
// components; the smallest-ID representative mirrors unionFind's smaller-id rule.
func condenseSCCs(nodeIDs []int, edges map[int]set.Set[int]) map[int]int {
	// Seed the traversal in ascending ID order so a run is reproducible.
	sorted := append([]int(nil), nodeIDs...)
	sort.Ints(sorted)

	rep := map[int]int{}
	for _, scc := range graph.StronglyConnectedComponents(sorted, func(v int) []int {
		return edges[v].ToSlice()
	}) {
		r := slices.Min(scc)
		for _, w := range scc {
			rep[w] = r
		}
	}
	return rep
}

// weakComponents labels each representative with its weakly-connected component,
// treating every condensed outlives edge as undirected, and returns a map from
// representative ID to the smallest representative ID in its component. That
// smallest-ID leader mirrors the representative rule condenseSCCs uses. An
// instantiation links a join to the argument lifetime it feeds through a shared
// intermediary that outlives both, with no directed edge between the two, so
// connectivity — not directed reachability — is what gathers a join's members.
func (s *ltBoundSet) weakComponents() map[int]int {
	adj := map[int][]int{}
	for from, tos := range s.edges {
		for to := range tos {
			adj[from] = append(adj[from], to)
			adj[to] = append(adj[to], from)
		}
	}

	comp := map[int]int{}
	visited := set.NewSet[int]()
	for _, start := range slices.Sorted(maps.Keys(s.vars)) {
		if visited.Contains(start) {
			continue
		}
		// BFS the whole component from start, then key every member to the min ID.
		members := []int{start}
		visited.Add(start)
		for i := 0; i < len(members); i++ {
			for _, nbr := range adj[members[i]] {
				if !visited.Contains(nbr) {
					visited.Add(nbr)
					members = append(members, nbr)
				}
			}
		}
		leader := slices.Min(members)
		for _, m := range members {
			comp[m] = leader
		}
	}
	return comp
}

// repOf maps a raw lifetime ID to its component representative, or to itself when the
// ID is not in this set.
func (s *ltBoundSet) repOf(id int) int {
	if r, ok := s.rep[id]; ok {
		return r
	}
	return id
}

// reaches reports whether to is reachable from from over the condensed edges.
func (s *ltBoundSet) reaches(from, to int) bool {
	if from == to {
		return true
	}
	visited := set.NewSet[int]()
	stack := []int{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited.Contains(n) {
			continue
		}
		visited.Add(n)
		for nbr := range s.edges[n] {
			if nbr == to {
				return true
			}
			stack = append(stack, nbr)
		}
	}
	return false
}

// implies reports whether this set proves 'a: 'b, that a outlives b. Both IDs map to
// their representatives first, so two outlives-equivalent lifetimes share a
// representative and implies reports the cycle as equality in both directions. A
// 'static-forced source outlives everything, so it implies every target. Otherwise the
// answer is reachability from a's representative to b's.
func (s *ltBoundSet) implies(a, b int) bool {
	ra, rb := s.repOf(a), s.repOf(b)
	if ra == rb {
		return true
	}
	if s.static.Contains(ra) {
		return true
	}
	return s.reaches(ra, rb)
}

// alphaEqualTypes reports whether a and b are equal up to a consistent renaming of their
// lifetime variables — alpha-equivalence over the lifetime quantifier. Two borrows minted
// in independent schemes carry distinct LifetimeVar identities, so the pointer-identity
// equalType always reports them unequal even when they denote the same borrow. This is
// the cross-scheme lifetime equality overload-arm comparison needs.
//
// It refines equalType only for borrows. A type with no lifetime variable compares
// exactly as equalType does, so callers on the coalesce and lattice hot path keep pointer
// identity and two independent param lifetimes stay distinct there. The comparison runs
// in two steps:
//
//  1. Compare structure while equalTypeWith discovers the lifetime bijection, binding
//     each matched borrow's lifetime on one side to the other's. Matching object
//     properties by name means the bijection follows structure, not the order the two
//     types happen to list their properties. A variable reused on one side must be reused
//     the same way on the other, so two structurally-identical borrows whose
//     lifetime-sharing patterns differ do not match.
//  2. Compare the outlives relation among the paired lifetimes, so two signatures that
//     agree structurally but differ in which lifetime outlives which are still unequal.
//
// overload-arm comparison calls this once per parameter through structuralSubtype, so a
// lifetime shared across two parameters is not distinguished there. That is intended. A
// borrow's lifetime is erased at codegen, which dispatches on parameter shape, so two arms
// whose parameters are alpha-equal are indistinguishable however their lifetimes relate.
func alphaEqualTypes(a, b soltype.Type) bool {
	lt := &ltPairing{}
	if !equalTypeWith(a, b, &alphaCtx{lt: lt}) {
		return false
	}
	if len(lt.aVars) == 0 {
		return true // no borrows: equalTypeWith settled it structurally
	}
	return sameOutlivesUnderPairing(lt)
}

// sameOutlivesUnderPairing reports whether the two sides' paired lifetimes carry the same
// outlives relation and the same 'static forcings. It builds each side's condensed
// outlives graph over its paired variables, then for every ordered pair of paired
// lifetimes checks that one side proves the outlives relation exactly when the other
// does. implies folds in reachability, the mutual-outlives equality a condensed cycle
// leaves behind, and 'static absorption, so a relation routed through an unpositioned
// intermediary or realized as a cycle is compared correctly without a separate edge set.
func sameOutlivesUnderPairing(p *ltPairing) bool {
	absA := buildLtBoundSet(occOf(p.aVars))
	absB := buildLtBoundSet(occOf(p.bVars))

	for i := range p.aVars {
		ai, bi := p.aVars[i].ID, p.bVars[i].ID
		if absA.static.Contains(absA.repOf(ai)) != absB.static.Contains(absB.repOf(bi)) {
			return false
		}
		for j := range p.aVars {
			aj, bj := p.aVars[j].ID, p.bVars[j].ID
			if absA.implies(ai, aj) != absB.implies(bi, bj) {
				return false
			}
		}
	}
	return true
}

// occOf seeds a bound-set walk from the given variables. The polarity is irrelevant to
// the outlives graph, which lives in the variables' bound lists, so every variable is
// recorded as occNeg.
func occOf(vars []*soltype.LifetimeVar) map[*soltype.LifetimeVar]occPolarity {
	occ := map[*soltype.LifetimeVar]occPolarity{}
	for _, v := range vars {
		occ[v] = occNeg
	}
	return occ
}

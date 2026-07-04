package solver

import (
	"maps"
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ltBoundSet is a directed outlives graph over lifetime-variable IDs, condensed over
// outlives-equivalent lifetimes and then transitively reduced. An edge a -> b reads
// "'a outlives 'b", written 'a: 'b. That is the direction constrainLt records as a in
// b.LowerBounds and b in a.UpperBounds.
//
// A mutual constraint 'a: 'b together with 'b: 'a makes the two lifetimes outlive each
// other, so they are equal. Such a cycle collapses into one strongly-connected-component
// representative. Every edge, query, and rendering is keyed by representative ID, not
// raw lifetime ID.
type ltBoundSet struct {
	edges      map[int]set.Set[int]         // rep -> reps it outlives, condensed then reduced
	rep        map[int]int                  // lifetime ID -> its SCC representative ID
	components map[int][]int                // rep -> member IDs, only for multi-member SCCs
	vars       map[int]*soltype.LifetimeVar // representative ID -> a member var, for rendering
	static     set.Set[int]                 // representative IDs forced to 'static, the absorbing bottom
}

// buildLtBoundSet walks the occurring lifetime variables' bound lists directionally
// and returns the condensed outlives graph over them. It records an edge per real
// outlives relation rather than the symmetric merge newLtAnalysis's union-find does,
// then condenses each strongly connected component to one representative so the result
// is a DAG that reduce can act on.
//
// The occurrence map's keys seed the walk. The polarity values are unused here, since
// the outlives edges live entirely in the bound lists. The result is not yet reduced;
// call reduce for the transitively-reduced canonical form.
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

	// Record each multi-member component's members under its representative. A
	// multi-member SCC is a set of mutually-outliving, hence equal, lifetimes whose
	// intra-component edges the condensation dropped. components is where those
	// equalities are kept so subsumes can recover them. A singleton SCC asserts no
	// equality, so it is omitted.
	grouped := map[int][]int{}
	for id, r := range rep {
		grouped[r] = append(grouped[r], id)
	}
	components := map[int][]int{}
	for r, members := range grouped {
		if len(members) > 1 {
			slices.Sort(members)
			components[r] = members
		}
	}

	return &ltBoundSet{edges: edges, rep: rep, components: components, vars: repVars, static: static}
}

// condenseSCCs finds the strongly connected components of the directed graph given by
// adjacency edges over the node IDs, and returns a map from every node ID to its
// component representative, the smallest ID in the component. A cycle in the outlives
// relation means mutually-outliving, hence equal, lifetimes, so folding each component
// to one representative turns the raw graph into a DAG. Uses Tarjan's algorithm.
func condenseSCCs(nodeIDs []int, edges map[int]set.Set[int]) map[int]int {
	index := map[int]int{}
	lowlink := map[int]int{}
	onStack := set.NewSet[int]()
	var stack []int
	next := 0
	rep := map[int]int{}

	var strongconnect func(v int)
	strongconnect = func(v int) {
		index[v] = next
		lowlink[v] = next
		next++
		stack = append(stack, v)
		onStack.Add(v)

		for w := range edges[v] {
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack.Contains(w) {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}

		if lowlink[v] != index[v] {
			return // v is not a component root, so its members stay on the stack
		}
		// v roots a component. Pop the stack down to v and key every member to the
		// smallest ID among them, mirroring unionFind's smaller-id representative rule.
		r := v
		start := len(stack)
		for {
			start--
			if stack[start] < r {
				r = stack[start]
			}
			if stack[start] == v {
				break
			}
		}
		for _, w := range stack[start:] {
			onStack.Remove(w)
			rep[w] = r
		}
		stack = stack[:start]
	}

	// Seed the traversal in ascending ID order so a run is reproducible.
	sorted := append([]int(nil), nodeIDs...)
	sort.Ints(sorted)
	for _, id := range sorted {
		if _, seen := index[id]; !seen {
			strongconnect(id)
		}
	}
	return rep
}

// repOf maps a raw lifetime ID to its component representative, or to itself when the
// ID is not in this set.
func (s *ltBoundSet) repOf(id int) int {
	if r, ok := s.rep[id]; ok {
		return r
	}
	return id
}

// reduce transitively reduces the condensed graph in place, so 'a: 'b, 'b: 'c, 'a: 'c
// keeps only 'a: 'b and 'b: 'c. An edge a -> c drops when a longer path a -> … -> c
// already proves the same reachability. The reduction is well-defined only because
// buildLtBoundSet condensed every cycle, leaving a DAG.
//
// Every edge incident to a 'static-forced node is dropped first. 'static is the
// absorbing bottom of the outlives order. An edge into such a node is subsumed by the
// node's own staticness, and an edge out of one is trivially true, so neither is a
// bound worth rendering or comparing.
func (s *ltBoundSet) reduce() {
	for from, tos := range s.edges {
		if s.static.Contains(from) {
			delete(s.edges, from)
			continue
		}
		for to := range tos {
			if s.static.Contains(to) {
				tos.Remove(to)
			}
		}
		if tos.Len() == 0 {
			delete(s.edges, from)
		}
	}

	type edge struct{ from, to int }
	var redundant []edge
	for from, tos := range s.edges {
		for to := range tos {
			// from -> to is redundant when some other successor of from already reaches
			// to. Reachability is read from the pre-reduction graph. On a DAG, dropping a
			// shortcut edge never removes the longer path that made it redundant, so
			// collecting first and deleting after is safe.
			for mid := range tos {
				if mid != to && s.reaches(mid, to) {
					redundant = append(redundant, edge{from, to})
					break
				}
			}
		}
	}
	for _, e := range redundant {
		s.edges[e.from].Remove(e.to)
		if s.edges[e.from].Len() == 0 {
			delete(s.edges, e.from)
		}
	}
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

// outlivesRelation is a single "'from outlives 'to" assertion in raw lifetime-ID
// space, the coordinate system implies and subsumes both speak. from and to are raw
// IDs, not representatives; implies maps them into a set's own representative space.
type outlivesRelation struct{ from, to int }

// assertedOutlives returns every outlives relation this set asserts, as raw-ID pairs.
// Two kinds contribute:
//
//   - each kept edge, keyed by representative;
//   - both directions of every mutual-outlives equality a multi-member component
//     condensed away, recovered from components.
//
// 'static forcings are not returned, since a forcing is not a pairwise outlives
// relation; subsumes checks those against static directly.
func (s *ltBoundSet) assertedOutlives() []outlivesRelation {
	var rels []outlivesRelation
	for from, tos := range s.edges {
		for to := range tos {
			rels = append(rels, outlivesRelation{from, to})
		}
	}
	for rep, members := range s.components {
		for _, m := range members {
			if m == rep {
				continue
			}
			rels = append(rels, outlivesRelation{m, rep}, outlivesRelation{rep, m})
		}
	}
	return rels
}

// subsumes reports whether this set proves every relation the other set asserts, so
// "the inferred bound set satisfies the declared one" is inferred.subsumes(declared).
// other.assertedOutlives enumerates the outlives relations — kept edges plus the
// mutual-outlives equalities its components condensed away — and each must hold here
// via implies. A 'static forcing is the one kind that is not a pairwise outlives
// relation, so it is checked separately: this set must force to 'static every lifetime
// other does.
func (s *ltBoundSet) subsumes(other *ltBoundSet) bool {
	for _, r := range other.assertedOutlives() {
		if !s.implies(r.from, r.to) {
			return false
		}
	}
	for id := range other.static {
		if !s.static.Contains(s.repOf(id)) {
			return false
		}
	}
	return true
}

// canonicalEdges returns the condensed edges as (from, to) representative-ID pairs
// sorted ascending, giving stable rendering and order-insensitive equality. This is the
// lifetime-sort analogue of a canonical union member order.
func (s *ltBoundSet) canonicalEdges() [][2]int {
	var out [][2]int
	for from, tos := range s.edges {
		for to := range tos {
			out = append(out, [2]int{from, to})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

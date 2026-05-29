package simplesub

import "sort"

// ---- Occurrence analysis ----

type polKey struct {
	id  int
	pol Polarity
}

// analyze records, for each variable, the polarities it occurs in (following
// bounds in the relevant direction). This drives single-polarity elimination.
func analyze(st SimpleType, pol Polarity, occurrences map[int]map[Polarity]bool, seen map[polKey]bool) {
	switch t := st.(type) {
	case *Variable:
		if occurrences[t.id] == nil {
			occurrences[t.id] = map[Polarity]bool{}
		}
		occurrences[t.id][pol] = true
		pk := polKey{t.id, pol}
		if seen[pk] {
			return
		}
		seen[pk] = true
		for _, b := range t.boundsAt(pol) {
			analyze(b, pol, occurrences, seen)
		}
	case *Function:
		for _, p := range t.params {
			analyze(p, pol.flip(), occurrences, seen)
		}
		analyze(t.ret, pol, occurrences, seen)
	case *Tuple:
		for _, e := range t.elems {
			analyze(e, pol, occurrences, seen)
		}
	case *Record:
		for _, f := range t.fields {
			analyze(f, pol, occurrences, seen) // fields are covariant
		}
	case *Mut:
		// inner occurs both covariantly (read) and contravariantly (write), so
		// variables inside a Mut are bipolar — the source of invariance.
		analyze(t.inner, pol, occurrences, seen)
		analyze(t.inner, pol.flip(), occurrences, seen)
	case *Alias:
		analyze(t.body, pol, occurrences, seen)
	}
}

// ---- Co-occurrence merging ----

// collectVars gathers every variable reachable from st, following both bounds.
func collectVars(st SimpleType, set map[int]*Variable) {
	switch t := st.(type) {
	case *Variable:
		if _, ok := set[t.id]; ok {
			return
		}
		set[t.id] = t
		for _, b := range t.lowerBounds {
			collectVars(b, set)
		}
		for _, b := range t.upperBounds {
			collectVars(b, set)
		}
	case *Function:
		for _, p := range t.params {
			collectVars(p, set)
		}
		collectVars(t.ret, set)
	case *Tuple:
		for _, e := range t.elems {
			collectVars(e, set)
		}
	case *Record:
		for _, f := range t.fields {
			collectVars(f, set)
		}
	case *Mut:
		collectVars(t.inner, set)
	case *Alias:
		collectVars(t.body, set)
	}
}

func containsVarBound(bounds []SimpleType, v *Variable) bool {
	for _, b := range bounds {
		if bv, ok := b.(*Variable); ok && bv.id == v.id {
			return true
		}
	}
	return false
}

// symmetrize records every var-to-var bound in both directions: if v <: w is
// stored on v.upperBounds, ensure w.lowerBounds contains v (and vice versa).
// constrain records bounds one-sided, but coalescing/co-occurrence needs each
// variable to see all of its own subtyping facts. Mirroring is idempotent, so a
// single pass over a snapshot of the edges suffices.
func symmetrize(vars map[int]*Variable) {
	type edge struct {
		from  *Variable
		to    *Variable
		upper bool // add 'to' to from.upperBounds (else lowerBounds)
	}
	var edges []edge
	for _, v := range vars {
		for _, u := range v.upperBounds {
			if uv, ok := u.(*Variable); ok {
				edges = append(edges, edge{uv, v, false}) // v <: uv  =>  uv has lower v
			}
		}
		for _, l := range v.lowerBounds {
			if lv, ok := l.(*Variable); ok {
				edges = append(edges, edge{lv, v, true}) // lv <: v  =>  lv has upper v
			}
		}
	}
	for _, e := range edges {
		if e.upper {
			if !containsVarBound(e.from.upperBounds, e.to) {
				e.from.upperBounds = append(e.from.upperBounds, e.to)
			}
		} else {
			if !containsVarBound(e.from.lowerBounds, e.to) {
				e.from.lowerBounds = append(e.from.lowerBounds, e.to)
			}
		}
	}
}

// gatherGroup collects the transitive same-polarity variable closure of v: the
// variables that end up in the same flattened union (positive) or intersection
// (negative) as v.
func gatherGroup(v *Variable, pol Polarity, g map[int]bool) {
	if g[v.id] {
		return
	}
	g[v.id] = true
	for _, b := range v.boundsAt(pol) {
		if bv, ok := b.(*Variable); ok {
			gatherGroup(bv, pol, g)
		}
	}
}

// collectCoOcc records, per (variable, polarity), the set of variables that
// co-occur with it (share a union/intersection node).
func collectCoOcc(st SimpleType, pol Polarity, coOcc map[polKey]map[int]bool, seen map[polKey]bool) {
	switch t := st.(type) {
	case *Variable:
		g := map[int]bool{}
		gatherGroup(t, pol, g)
		for a := range g {
			for b := range g {
				if a != b {
					k := polKey{a, pol}
					if coOcc[k] == nil {
						coOcc[k] = map[int]bool{}
					}
					coOcc[k][b] = true
				}
			}
		}
		pk := polKey{t.id, pol}
		if seen[pk] {
			return
		}
		seen[pk] = true
		for _, b := range t.boundsAt(pol) {
			collectCoOcc(b, pol, coOcc, seen)
		}
	case *Function:
		for _, p := range t.params {
			collectCoOcc(p, pol.flip(), coOcc, seen)
		}
		collectCoOcc(t.ret, pol, coOcc, seen)
	case *Tuple:
		for _, e := range t.elems {
			collectCoOcc(e, pol, coOcc, seen)
		}
	case *Record:
		for _, f := range t.fields {
			collectCoOcc(f, pol, coOcc, seen)
		}
	case *Mut:
		collectCoOcc(t.inner, pol, coOcc, seen)
		collectCoOcc(t.inner, pol.flip(), coOcc, seen)
	case *Alias:
		collectCoOcc(t.body, pol, coOcc, seen)
	}
}

type unionFind struct{ parent map[int]int }

func newUnionFind() *unionFind { return &unionFind{parent: map[int]int{}} }

func (u *unionFind) find(x int) int {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p != x {
		r := u.find(p)
		u.parent[x] = r
		return r
	}
	return x
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if rb < ra { // keep the smaller id as representative for stable naming
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}

// mutualCoOcc reports whether a and b co-occur in every polarity each occurs in
// — the condition under which they can be soundly merged.
func mutualCoOcc(a, b int, occurrences map[int]map[Polarity]bool, coOcc map[polKey]map[int]bool) bool {
	if len(occurrences[a]) == 0 || len(occurrences[b]) == 0 {
		return false
	}
	for pol := range occurrences[a] {
		if !coOcc[polKey{a, pol}][b] {
			return false
		}
	}
	for pol := range occurrences[b] {
		if !coOcc[polKey{b, pol}][a] {
			return false
		}
	}
	return true
}

func mergeCoOccurring(vars map[int]*Variable, occurrences map[int]map[Polarity]bool, coOcc map[polKey]map[int]bool) *unionFind {
	uf := newUnionFind()
	ids := make([]int, 0, len(vars))
	for id := range vars {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if mutualCoOcc(ids[i], ids[j], occurrences, coOcc) {
				uf.union(ids[i], ids[j])
			}
		}
	}
	return uf
}

package solver

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Co-occurrence merging (PR2). Single-polarity elimination already lives in
// coalesceScheme — a variable occurring in only one polarity is dropped in favour
// of its bound. The remaining simplification is merging DISTINCT quantified
// variables that always appear together so a signature renders with one type
// parameter instead of several: outer's `fn <T0, T1>(y: T0 & T1) -> [T0, T1]`
// collapses to `fn <T0>(y: T0) -> [T0, T0]`.
//
// The algorithm ports internal/simplesub/simplify.go to soltype:
//
//  1. collect every variable reachable from the scheme body,
//  2. symmetrize the var↔var subtyping edges (constrain records each on one
//     endpoint only; co-occurrence needs both endpoints to see it),
//  3. record, per variable, the polarities it occurs in,
//  4. record, per (variable, polarity), the variables sharing its union /
//     intersection group,
//  5. union two variables when they co-occur in every polarity each occurs in.
//
// coalesceScheme then resolves each variable to its union-find representative for
// both the retain decision and the printed name, so every member of a class
// renders as the same type parameter.

// unionFind is the disjoint-set forest keyed by TypeVarType.ID. The smaller id is
// kept as a class's representative so naming is stable across runs.
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

// coKey keys the co-occurrence table by (variable id, polarity): the set of
// variables that share a union (positive) or intersection (negative) node with
// this variable in this polarity.
type coKey struct {
	id  int
	pol soltype.Polarity
}

// varCollector gathers every TypeVarType reachable from a type — structural
// children via Accept, plus both bound lists walked here (a var's bounds are a side
// graph, not tree children, so the visitor handles them explicitly).
type varCollector struct {
	out  map[int]*soltype.TypeVarType
	seen set.Set[*soltype.TypeVarType]
}

func (vc *varCollector) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural / atom node: let Accept descend
	}
	if vc.seen.Contains(v) {
		return soltype.EnterResult{SkipChildren: true}
	}
	vc.seen.Add(v)
	vc.out[v.ID] = v
	for _, b := range v.LowerBounds {
		b.Accept(vc, pol)
	}
	for _, b := range v.UpperBounds {
		b.Accept(vc, pol)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (vc *varCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// mirror holds the var↔var subtyping edges in both directions. constrain records a
// var-to-var bound on only one endpoint, so co-occurrence analysis would miss half
// the edges; mirror precomputes, per variable, the var bounds stored on the OTHER
// endpoint. This is the symmetric bound view the spike obtained by mutating the
// bound lists, computed here without touching the canonical scheme body.
type mirror struct {
	lower map[int][]*soltype.TypeVarType // extra lower-bound vars: u <: v recorded on u.UpperBounds
	upper map[int][]*soltype.TypeVarType // extra upper-bound vars: v <: u recorded on u.LowerBounds
}

func buildMirror(vars map[int]*soltype.TypeVarType) *mirror {
	m := &mirror{
		lower: map[int][]*soltype.TypeVarType{},
		upper: map[int][]*soltype.TypeVarType{},
	}
	for _, u := range vars {
		for _, b := range u.UpperBounds {
			if w, ok := b.(*soltype.TypeVarType); ok { // u <: w  =>  w has lower bound u
				m.lower[w.ID] = append(m.lower[w.ID], u)
			}
		}
		for _, b := range u.LowerBounds {
			if w, ok := b.(*soltype.TypeVarType); ok { // w <: u  =>  w has upper bound u
				m.upper[w.ID] = append(m.upper[w.ID], u)
			}
		}
	}
	return m
}

// effectiveBounds returns v's stored bounds at pol plus the mirrored var bounds —
// the symmetric view occurrence and co-occurrence analysis walk.
func (m *mirror) effectiveBounds(v *soltype.TypeVarType, pol soltype.Polarity) []soltype.Type {
	var stored []soltype.Type
	var mirrored []*soltype.TypeVarType
	if pol == soltype.Positive {
		stored, mirrored = v.LowerBounds, m.lower[v.ID]
	} else {
		stored, mirrored = v.UpperBounds, m.upper[v.ID]
	}
	if len(mirrored) == 0 {
		return stored
	}
	out := make([]soltype.Type, 0, len(stored)+len(mirrored))
	out = append(out, stored...)
	for _, w := range mirrored {
		out = append(out, w)
	}
	return out
}

// symOccVisitor records which polarities each variable occurs in, walking the
// symmetric bound graph (mirror.effectiveBounds) so a variable reached only through
// a one-sided edge — outer's param var, reached positively via a mirrored lower
// bound — is still seen in that polarity. The (var, pol) seen-set keeps a cyclic
// graph terminating.
type symOccVisitor struct {
	m    *mirror
	occ  map[int]occPolarity // keyed by TypeVarType.ID
	seen set.Set[occKey]
}

func (o *symOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural / atom node: let Accept descend
	}
	if pol == soltype.Positive {
		o.occ[v.ID] |= occPos
	} else {
		o.occ[v.ID] |= occNeg
	}
	k := occKey{v, pol}
	if o.seen.Contains(k) {
		return soltype.EnterResult{SkipChildren: true}
	}
	o.seen.Add(k)
	for _, b := range o.m.effectiveBounds(v, pol) {
		b.Accept(o, pol)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (o *symOccVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// gatherGroup collects the transitive same-polarity variable closure of v: the
// variables that end up in the same flattened union (positive) or intersection
// (negative) as v, following the symmetric var bounds.
func (m *mirror) gatherGroup(v *soltype.TypeVarType, pol soltype.Polarity, g set.Set[int]) {
	if g.Contains(v.ID) {
		return
	}
	g.Add(v.ID)
	for _, b := range m.effectiveBounds(v, pol) {
		if bv, ok := b.(*soltype.TypeVarType); ok {
			m.gatherGroup(bv, pol, g)
		}
	}
}

// coOccVisitor records, per (variable, polarity), the set of variables co-occurring
// with it. Walking the body structurally, at each variable it gathers the
// same-polarity group and links every pair within it.
type coOccVisitor struct {
	m     *mirror
	coOcc map[coKey]set.Set[int]
	seen  set.Set[coKey]
}

func (c *coOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural / atom node: let Accept descend
	}
	g := set.NewSet[int]()
	c.m.gatherGroup(v, pol, g)
	for a := range g {
		for b := range g {
			if a == b {
				continue
			}
			ck := coKey{a, pol}
			if c.coOcc[ck] == nil {
				c.coOcc[ck] = set.NewSet[int]()
			}
			c.coOcc[ck].Add(b)
		}
	}
	k := coKey{v.ID, pol}
	if c.seen.Contains(k) {
		return soltype.EnterResult{SkipChildren: true}
	}
	c.seen.Add(k)
	for _, b := range c.m.effectiveBounds(v, pol) {
		b.Accept(c, pol)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (c *coOccVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// occHas reports whether o records an occurrence in polarity pol.
func occHas(o occPolarity, pol soltype.Polarity) bool {
	if pol == soltype.Positive {
		return o&occPos != 0
	}
	return o&occNeg != 0
}

// mutualCoOcc reports whether a and b co-occur in every polarity each occurs in —
// the condition under which they merge soundly. A variable pair failing it (e.g.
// two tuple elements that share no union node) stays distinct.
func mutualCoOcc(a, b int, occ map[int]occPolarity, coOcc map[coKey]set.Set[int]) bool {
	oa, ob := occ[a], occ[b]
	if oa == 0 || ob == 0 {
		return false
	}
	for _, pol := range []soltype.Polarity{soltype.Positive, soltype.Negative} {
		if occHas(oa, pol) {
			s := coOcc[coKey{a, pol}]
			if s == nil || !s.Contains(b) {
				return false
			}
		}
		if occHas(ob, pol) {
			s := coOcc[coKey{b, pol}]
			if s == nil || !s.Contains(a) {
				return false
			}
		}
	}
	return true
}

// mergeCoOccurring unions every mutually-co-occurring pair among ids.
func mergeCoOccurring(ids []int, occ map[int]occPolarity, coOcc map[coKey]set.Set[int]) *unionFind {
	uf := newUnionFind()
	sort.Ints(ids)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if mutualCoOcc(ids[i], ids[j], occ, coOcc) {
				uf.union(ids[i], ids[j])
			}
		}
	}
	return uf
}

// schemeSimplification is the co-occurrence merging input coalesceScheme threads
// into schemeCoalescer: the union-find collapsing quantifiable variables that
// always occur together, the merged occurrence map keyed by representative id (so
// single-polarity elimination sees a class's combined polarities), and the id→var
// table to resolve a representative id back to its variable for naming.
type schemeSimplification struct {
	uf        *unionFind
	mergedOcc map[int]occPolarity // keyed by representative id
	idToVar   map[int]*soltype.TypeVarType
}

// rep resolves a variable to its class representative variable.
func (s *schemeSimplification) rep(v *soltype.TypeVarType) *soltype.TypeVarType {
	return s.idToVar[s.uf.find(v.ID)]
}

// simplifyScheme runs the co-occurrence analysis over a generalized scheme's raw
// body. Only quantifiable variables (Level > genLevel) are merge candidates:
// captured outer variables are not type parameters and never merge into one, which
// also keeps every representative quantifiable so the retain decision stays uniform.
func simplifyScheme(body soltype.Type, genLevel int) *schemeSimplification {
	vars := map[int]*soltype.TypeVarType{}
	body.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)

	m := buildMirror(vars)

	occByID := map[int]occPolarity{}
	body.Accept(&symOccVisitor{m: m, occ: occByID, seen: set.NewSet[occKey]()}, soltype.Positive)

	coOcc := map[coKey]set.Set[int]{}
	body.Accept(&coOccVisitor{m: m, coOcc: coOcc, seen: set.NewSet[coKey]()}, soltype.Positive)

	candidates := make([]int, 0, len(vars))
	for id, v := range vars {
		if v.Level > genLevel {
			candidates = append(candidates, id)
		}
	}
	uf := mergeCoOccurring(candidates, occByID, coOcc)

	mergedOcc := map[int]occPolarity{}
	for id := range vars {
		rep := uf.find(id)
		mergedOcc[rep] |= occByID[id]
	}
	return &schemeSimplification{uf: uf, mergedOcc: mergedOcc, idToVar: vars}
}

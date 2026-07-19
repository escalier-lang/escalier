package solver

import (
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Co-occurrence merging (PR2). Single-polarity elimination already lives in
// coalesceScheme — a variable occurring in only one polarity is dropped in favour
// of its bound. The remaining simplification is merging DISTINCT quantified
// variables that always appear together so a signature renders with one type
// parameter instead of several.
//
// Two variables CO-OCCUR when they share a lattice node in every polarity each
// appears in. That means the same union node wherever they appear positively, and
// the same intersection node wherever they appear negatively. No caller can then
// distinguish them, so they can safely collapse to one type parameter. For
// example:
//
//	val outer = fn (y) {
//		val getY = fn () { return y }
//		return [getY(), getY()]
//	}
//
// The parameter `y` reaches both tuple elements through two fresh result
// variables, so outer's raw scheme carries three distinct quantified variables and
// renders the non-compact `fn <T0, T1>(y: T0 & T1) -> [T0, T1]`. Those three always
// occur together, so merging collapses them to `fn <T0>(y: T0) -> [T0, T0]`.
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
// the edges. mirror precomputes, per variable, the var bounds stored on the OTHER
// endpoint.
//
// It is a derived view, not a change to the stored bounds. coalesce and extrude
// read the same bound lists, and coalesce inlines a variable to the union of its
// lower bounds or the intersection of its uppers. Adding the reverse edge to those
// lists would pull a spurious variable into that union or intersection and change
// the rendered type. So the symmetric view is built here, leaving the canonical
// scheme body untouched. The spike instead mutated the bound lists in place.
type mirror struct {
	lower map[int][]soltype.Type // extra lower-bound vars: u <: v recorded on u.UpperBounds
	upper map[int][]soltype.Type // extra upper-bound vars: v <: u recorded on u.LowerBounds
}

func buildMirror(vars map[int]*soltype.TypeVarType) *mirror {
	m := &mirror{
		lower: map[int][]soltype.Type{},
		upper: map[int][]soltype.Type{},
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
	var stored, mirrored []soltype.Type
	if pol == soltype.Positive {
		stored, mirrored = v.LowerBounds, m.lower[v.ID]
	} else {
		stored, mirrored = v.UpperBounds, m.upper[v.ID]
	}
	if len(mirrored) == 0 {
		return stored
	}
	return slices.Concat(stored, mirrored)
}

// recordMutWriteView reflects a `mut` borrow's INVARIANCE into a polarity-recording
// visitor (#737). The C2 constrain rule makes every field a `mut` target names
// invariant — it adds a covariant read view and, under the mut-context flag, a
// contravariant write view — but RefType.Accept walks the inner only covariantly
// (the read view; see visitor.go). So a variable that appears only inside a `mut`
// field,
// such as the `v` in `fn (obj, v) { obj.x = v }`, would be seen in one polarity and
// dropped by single-polarity elimination, severing the link between the field and
// the value written into it.
//
// This adds the missing write view: for a `mut` borrow it walks the inner in the
// FLIPPED polarity and returns true, signalling the caller to return the default
// EnterResult so Accept still performs the covariant walk. The inner ends up
// recorded in BOTH polarities, so such a variable is retained as a type parameter.
// Shared by the occurrence and co-occurrence visitors so both agree.
//
// The flipped descent is safe to run inline for two reasons:
//   - It is a fixed structural property of the `mut` constructor, not a fact derived
//     from the constraint graph. A mut borrow's inner is invariant no matter what
//     bounds exist.
//   - It is stateless. It records occurrences through the normal variable-recording
//     path and mutates no stored bound lists, so it touches nothing coalesce or
//     extrude reads.
//
// This is the contrast with the reverse var↔var bound edges, which buildMirror keeps
// in a separate derived view precisely because writing them back to the bound lists
// would corrupt those passes.
func recordMutWriteView(v soltype.TypeVisitor, t soltype.Type, pol soltype.Polarity) bool {
	if rt, ok := t.(*soltype.RefType); ok && rt.Mut {
		rt.Inner.Accept(v, pol.Flip())
		return true
	}
	return false
}

// symOccVisitor records which polarities each variable occurs in. It walks the
// symmetric bound graph through mirror.effectiveBounds, so a variable reached only
// through a one-sided edge is still seen in that polarity.
//
// It does not reconstruct those reverse var↔var edges itself. buildMirror derives
// them once into a separate view, and this visitor only reads that view. Two reasons
// keep the reconstruction out of here:
//   - The same reverse-edge graph is also consumed by coOccVisitor and gatherGroup,
//     so building it once and sharing it avoids duplicating the work.
//   - The reverse edges must stay off the canonical bound lists, because coalesce and
//     extrude read those lists and an extra edge would pull a spurious variable into a
//     rendered union or intersection.
//
// The (var, pol) seen-set keeps a cyclic graph terminating.
type symOccVisitor struct {
	m    *mirror
	occ  map[int]occPolarity // keyed by TypeVarType.ID
	seen set.Set[occKey]
}

func (o *symOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if recordMutWriteView(o, t, pol) {
		return soltype.EnterResult{} // let Accept do the covariant read-view walk
	}
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

// gatherGroup collects every variable that ends up in the same flattened union or
// intersection node as v once coalescing runs. It starts from v and follows the
// symmetric var↔var bounds at one fixed polarity. A positive walk follows lower
// bounds, a negative walk upper bounds. It keeps following bounds-of-bounds, so the
// result is the full transitive set, not just v's direct neighbours. Those
// variables are exactly the ones that co-occur with v in that polarity.
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
// with it. Walking the body structurally, at each NEWLY-visited variable it gathers
// the same-polarity group and links every pair within it.
//
// Cost: the seen gate runs the gather-and-link once per distinct (variable,
// polarity), not once per occurrence. The link step is O(g²) for a group of size g,
// so the worst case is roughly O(Σ g²) over all groups — cubic when the whole scheme
// collapses into a single co-occurring component. The variable count is that of one
// rendered signature, so this stays cheap in practice.
type coOccVisitor struct {
	m     *mirror
	coOcc map[coKey]set.Set[int]
	seen  set.Set[coKey]
}

func (c *coOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if recordMutWriteView(c, t, pol) {
		return soltype.EnterResult{} // let Accept do the covariant read-view walk
	}
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural / atom node: let Accept descend
	}
	// Gather and record below the seen gate. gatherGroup is a pure function of
	// (v, pol) and the pair-recording is an idempotent set union, so running them
	// once per distinct (v, pol) yields the same coOcc as running them on every
	// entry — a variable reached n times no longer recomputes its group n times.
	k := coKey{v.ID, pol}
	if c.seen.Contains(k) {
		return soltype.EnterResult{SkipChildren: true}
	}
	c.seen.Add(k)
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
	for _, b := range c.m.effectiveBounds(v, pol) {
		b.Accept(c, pol)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (c *coOccVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// occHas reports whether o records an occurrence in polarity pol. o is a bitset,
// so o&occPos masks off the positive bit and is non-zero exactly when it is set.
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
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if mutualCoOcc(ids[i], ids[j], occ, coOcc) {
				uf.union(ids[i], ids[j])
			}
		}
	}
	return uf
}

// schemeSimplification is the co-occurrence merging input coalesceScheme threads
// into schemeCoalescer.
type schemeSimplification struct {
	// uf collapses quantifiable variables that always occur together into one class.
	uf *unionFind

	// mergedOcc is the occurrence map keyed by representative id, so single-polarity
	// elimination sees a class's combined polarities.
	mergedOcc map[int]occPolarity

	// idToVar resolves a representative id back to its variable for naming.
	idToVar map[int]*soltype.TypeVarType
}

// rep resolves a variable to its class representative variable: the canonical
// member of the merged class v belongs to. union keeps the smallest id as the
// class root, so every member resolves to the same representative and renders as
// one type parameter. A variable that never merged is its own representative.
func (s *schemeSimplification) rep(v *soltype.TypeVarType) *soltype.TypeVarType {
	return s.idToVar[s.uf.find(v.ID)]
}

// simplifyScheme runs the co-occurrence analysis over a generalized scheme's raw
// body. Only quantifiable variables (Level > genLevel) are merge candidates:
// captured outer variables are not type parameters and never merge into one, which
// also keeps every representative quantifiable so the retain decision stays uniform.
//
// A generic function's own TypeParams binder vars in keep are excluded from the
// candidates too. They are distinct declared parameters that coalesceScheme holds
// symbolic, so merging one into another var's class would rename it or let it mask
// another var's name. Excluding them keeps each a singleton class that is its own
// representative.
func simplifyScheme(body soltype.Type, genLevel int, keep set.Set[*soltype.TypeVarType]) *schemeSimplification {
	vars := map[int]*soltype.TypeVarType{}
	body.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)

	m := buildMirror(vars)

	// record which polarities each variable occurs in
	occByID := map[int]occPolarity{}
	body.Accept(&symOccVisitor{m: m, occ: occByID, seen: set.NewSet[occKey]()}, soltype.Positive)

	// record set of type vars co-occuring with each type var
	coOcc := map[coKey]set.Set[int]{}
	body.Accept(&coOccVisitor{m: m, coOcc: coOcc, seen: set.NewSet[coKey]()}, soltype.Positive)

	candidates := make([]int, 0, len(vars))
	for id, v := range vars {
		if v.Level > genLevel && !keep.Contains(v) {
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

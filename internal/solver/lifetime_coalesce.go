package solver

import (
	"maps"
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Display-time lifetime coalescing (M4 D4). The structural coalescers (coalesce /
// coalesceScheme) rebuild a type through the shared visitor, which carries every
// RefType lifetime through unchanged because a Lifetime is not a Type. They leave
// the RAW lifetime variables in place: a borrow parameter's originated lifetime, a
// multi-source join variable, and any instantiation-freshened intermediary. This
// pass runs once over the finished coalesced type and resolves those lifetimes to
// their display form, the lifetime-sort analogue of how the var arms resolve a type
// variable.
//
// It has three jobs, all keyed off a single occurrence analysis plus a grouping of
// the lifetime bound graph built by buildLtBoundSet. That builder condenses each
// mutual-outlives cycle to one strongly-connected-component representative, so the
// grouping is over a DAG rather than the raw graph constrainLt records. The three
// jobs group by connectivity in that condensed graph:
//
//  1. Naming. A borrow originates at a parameter, so a lifetime occurring in a
//     NEGATIVE position is a "param lifetime". It is the only kind named in the
//     output. The printer assigns it 'a, 'b, … from the variables this pass leaves
//     in the type.
//
//  2. Elision. A param lifetime whose borrow never reaches an output connects
//     nothing. It occurs in no positive position, and its connected component in the
//     condensed graph holds no output lifetime, so it is dropped. This is the
//     lifetime-sort analogue of single-polarity type-variable elimination. The drop
//     branches on the borrow's Mut flag:
//     - A mutable borrow becomes owned-mutable, RefType{Mut: true, Lt: nil}.
//     - An immutable borrow drops the RefType wrapper entirely and returns its
//     bare inner, because RefType{Mut: false, Lt: nil} is the forbidden
//     degenerate cell NewRef rejects.
//
//  3. Join naming. A non-param lifetime is a join variable minted at a return or
//     branch, or a lifetime freshened when a borrow-passing function was instantiated.
//     It resolves to the param lifetimes sharing its connected component in the
//     condensed graph. A join reaching one param renders under that param's name. A
//     join reaching two or more keeps its own name, and displayLtBounds renders each
//     source param's outlives edge as an `'a: 'c` bound, so a return uniting two
//     borrows renders `<'a: 'c, 'b: 'c, 'c>`. The grouping is by connectivity because
//     instantiation interposes an intermediary between a call's argument lifetime and
//     the join it feeds. That intermediary outlives both the caller's param lifetime
//     and the join, so the param and the join are joined only through it, with no
//     direct outlives edge either way. A lifetime forced to 'static renders 'static
//     and absorbs.
//
// coalesceLifetimes resolves the borrow lifetimes left raw by the structural
// coalescers. pol is the root polarity the type was coalesced at, threaded through
// so the occurrence walk and the rewrite classify lifetimes from the same root the
// coalesced type was built from. Every caller coalesces a display type from the
// Positive root today, so this is Positive in practice. Threading it keeps the
// lifetime analysis consistent with the coalescing polarity rather than assuming it.
func coalesceLifetimes(t soltype.Type, pol soltype.Polarity) soltype.Type {
	occ := map[*soltype.LifetimeVar]occPolarity{}
	t.Accept(&ltOccVisitor{occ: occ}, pol)

	a := newLtAnalysis(occ)
	return t.Accept(&ltRewriter{a: a}, pol)
}

// ltOccVisitor records the polarities each lifetime variable occurs in
// structurally. A RefType lifetime is COVARIANT, since it lives on the wrapper, not
// in the inner, so it is recorded in the borrow's own polarity. The mut-driven write
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
// occurrence, the condensed outlives graph the grouping is built from, the
// connected-component leader of each representative, and the set of component leaders
// that hold a positive output lifetime.
type ltAnalysis struct {
	occ      map[*soltype.LifetimeVar]occPolarity
	bs       *ltBoundSet  // condensed outlives graph; rep IDs collapse mutual-outlives cycles
	comp     map[int]int  // representative ID -> connected-component leader ID in bs
	posComps set.Set[int] // component leaders reaching a positive occurrence
}

// newLtAnalysis builds the grouping from the structurally-occurring lifetime
// variables. buildLtBoundSet walks each occurring variable's bounds in both
// directions and condenses every mutual-outlives cycle to one representative, so the
// grouping runs over a DAG. bs.weakComponents then labels each representative with its
// connected component. A component leader is marked positive when any
// structurally-positive lifetime falls in it; that is what keeps a connected param
// lifetime from being elided.
//
// The grouping is by connectivity rather than directed reachability because a
// borrow-passing function's instantiation interposes an intermediary between a call's
// argument lifetime and the join it feeds. The intermediary outlives both the
// argument lifetime and the join, so the two sit in one component yet neither reaches
// the other along outlives edges. Condensing mutual-outlives cycles to one
// representative first is what leaves a DAG for reduce and for the directional bound
// rendering that layers on top.
func newLtAnalysis(occ map[*soltype.LifetimeVar]occPolarity) *ltAnalysis {
	bs := buildLtBoundSet(occ)
	comp := bs.weakComponents()

	posComps := set.NewSet[int]()
	for v, pols := range occ {
		// pols is a bitset of the polarities v occurred in. `&occPos != 0` tests
		// whether the positive flag is set, tolerating a co-set occNeg bit, so a
		// both-polarity v still counts. A v that occurs positively reaches an output,
		// so mark its component leader positive — kept reads this to gate elision.
		if pols&occPos != 0 {
			posComps.Add(comp[bs.repOf(v.ID)])
		}
	}
	return &ltAnalysis{occ: occ, bs: bs, comp: comp, posComps: posComps}
}

// isParam reports whether v is a param lifetime: one that originates at a borrow
// parameter and so occurs in a negative position. Only param lifetimes are named.
func (a *ltAnalysis) isParam(v *soltype.LifetimeVar) bool {
	return a.occ[v]&occNeg != 0
}

// leaderOf maps a lifetime variable to its connected-component leader in the condensed
// graph, mapping through the variable's representative first.
func (a *ltAnalysis) leaderOf(v *soltype.LifetimeVar) int {
	return a.comp[a.bs.repOf(v.ID)]
}

// kept reports whether a param lifetime survives elision: its connected component
// reaches an output, so the borrow flows somewhere observable. A param occurring
// only on its parameter, connected to no output, is elided.
func (a *ltAnalysis) kept(v *soltype.LifetimeVar) bool {
	return a.posComps.Contains(a.leaderOf(v))
}

// componentParams returns the kept param lifetimes in v's connected component, keyed
// by SCC representative so mutually-outliving params list once. Each entry emits a
// param var, since the representative itself can be a non-param bridge var named in no
// parameter slot. The result is sorted by variable ID. resolveLt reads only the count:
// one member means v reborrows a single source and renders under that source's name,
// while two or more means v is a genuine multi-source join.
//
// A 'static-forced param renders as 'static rather than a name, so it is not a named
// source and is excluded. This keeps the count of named sources consistent with what
// survives in the resolved type, so a join whose only other source escaped to 'static
// collapses to its single remaining name rather than taking a fresh one.
func (a *ltAnalysis) componentParams(v *soltype.LifetimeVar) []*soltype.LifetimeVar {
	leader := a.leaderOf(v)
	byRep := map[int]*soltype.LifetimeVar{}
	for p := range a.occ {
		if !a.isParam(p) || !a.kept(p) || forcedToStatic(p) {
			continue
		}
		pr := a.bs.repOf(p.ID)
		if a.comp[pr] != leader {
			continue
		}
		if cur, ok := byRep[pr]; !ok || p.ID < cur.ID {
			byRep[pr] = p
		}
	}
	members := make([]*soltype.LifetimeVar, 0, len(byRep))
	for _, p := range byRep {
		members = append(members, p)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
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
	// A non-param lifetime is a join variable minted at a return or branch, or an
	// instantiation intermediary. It resolves to the param lifetimes it reaches.
	members := a.componentParams(v)
	switch len(members) {
	case 0:
		return nil, true // reaches no param: elide
	case 1:
		// A single reached param means v reborrows one source, so it renders under that
		// source's name rather than a fresh one. Mutually-outliving params condense to
		// one member here, so a join over equal borrows also lands in this arm.
		return members[0], false
	default:
		// A genuine multi-source join keeps its own name. The param lifetimes that
		// outlive it render as `'a: 'c` bounds in the quantifier prefix, computed by
		// displayLtBounds.
		return v, false
	}
}

// ltOutlivesRelation builds the outlives relation among the named lifetime variables
// occurring in a display type, as both the printer and the declared-bound check read
// it. It returns the analysis, the survivors sorted by ID, and a predicate outlives(u,
// w) that holds when u outlives w. A nil predicate means t carries no lifetime variable.
//
// The relation draws on two sources of outlives facts:
//
//   - A directed edge the solved graph records, read by implies. This covers a
//     declared or inferred bound between two named lifetimes, such as `'b: 'a`.
//   - A source lifetime feeding a multi-source join, recovered by componentParams from
//     the join's connected component. An instantiation interposes an intermediary that
//     outlives both the source and the join, so no directed edge links them, yet each
//     source outlives the join.
//
// Two survivors sharing a representative are equal lifetimes, so outlives reports no
// relation between them. pol is the polarity t was built at, threaded so the occurrence
// walk starts from the same root.
func ltOutlivesRelation(t soltype.Type, pol soltype.Polarity) (*ltAnalysis, []*soltype.LifetimeVar, func(u, w *soltype.LifetimeVar) bool) {
	occ := map[*soltype.LifetimeVar]occPolarity{}
	t.Accept(&ltOccVisitor{occ: occ}, pol)
	if len(occ) == 0 {
		return nil, nil, nil
	}
	a := newLtAnalysis(occ)
	bs := a.bs

	survivors := slices.Collect(maps.Keys(occ))
	sort.Slice(survivors, func(i, j int) bool { return survivors[i].ID < survivors[j].ID })

	// joinSources maps each multi-source join survivor to the SCC representatives of the
	// params feeding it. An instantiation interposes an intermediary that outlives both a
	// source param and the join, so implies does not link them; componentParams recovers
	// the sources from the join's connected component. Precomputed once so the outlives
	// relation below reads it instead of rescanning occ per pair.
	joinSources := map[*soltype.LifetimeVar]set.Set[int]{}
	for _, w := range survivors {
		if a.isParam(w) {
			continue
		}
		members := a.componentParams(w)
		if len(members) < 2 {
			continue
		}
		reps := set.NewSet[int]()
		for _, m := range members {
			reps.Add(bs.repOf(m.ID))
		}
		joinSources[w] = reps
	}

	outlives := func(u, w *soltype.LifetimeVar) bool {
		if bs.repOf(u.ID) == bs.repOf(w.ID) {
			return false
		}
		if bs.implies(u.ID, w.ID) {
			return true
		}
		reps, ok := joinSources[w]
		return ok && reps.Contains(bs.repOf(u.ID))
	}
	return a, survivors, outlives
}

// displayLtBounds returns the transitively-reduced outlives relation among the named
// lifetime survivors of a coalesced display type, keyed by variable for the printer's
// `'a: 'c` prefix. bounds[u] lists the survivors u directly outlives. The relation comes
// from ltOutlivesRelation, materialized into edges once before the reduction reads it.
func displayLtBounds(t soltype.Type, pol soltype.Polarity) map[*soltype.LifetimeVar][]*soltype.LifetimeVar {
	a, survivors, outlives := ltOutlivesRelation(t, pol)
	if outlives == nil {
		return nil
	}
	bs := a.bs

	// edges materializes the full outlives relation among survivors once, so the
	// transitive reduction below reads it rather than recomputing outlives.
	edges := map[*soltype.LifetimeVar]set.Set[*soltype.LifetimeVar]{}
	for _, u := range survivors {
		targets := set.NewSet[*soltype.LifetimeVar]()
		for _, v := range survivors {
			if outlives(u, v) {
				targets.Add(v)
			}
		}
		edges[u] = targets
	}

	bounds := map[*soltype.LifetimeVar][]*soltype.LifetimeVar{}
	for _, u := range survivors {
		var direct []*soltype.LifetimeVar
		seenRep := set.NewSet[int]()
		for _, v := range survivors {
			if !edges[u].Contains(v) {
				continue
			}
			// Drop u -> v when a survivor w sits between them, so 'a: 'b, 'b: 'c renders
			// without the redundant 'a: 'c. w must condense away from both endpoints, or
			// it is not a genuine intermediate.
			redundant := false
			for _, w := range survivors {
				if bs.repOf(w.ID) == bs.repOf(u.ID) || bs.repOf(w.ID) == bs.repOf(v.ID) {
					continue
				}
				if edges[u].Contains(w) && edges[w].Contains(v) {
					redundant = true
					break
				}
			}
			if redundant {
				continue
			}
			// Two survivors sharing a representative name one lifetime, so keep only the
			// first to reach it.
			if seenRep.Contains(bs.repOf(v.ID)) {
				continue
			}
			seenRep.Add(bs.repOf(v.ID))
			direct = append(direct, v)
		}
		if len(direct) > 0 {
			bounds[u] = direct
		}
	}
	return bounds
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
		// Keep the `&` on an elided borrow by parking the lifetime on the Anon
		// sentinel. The printer renders it as the bare `&`/`&mut` with no
		// lifetime name, so the displayed type still records owned vs borrowed.
		// Dropping to nil instead would collapse the wrapper to owned-mutable for
		// mut and to the bare inner for immutable, hiding the borrow at call sites.
		return &soltype.RefType{Mut: rt.Mut, Lt: soltype.Anon, Inner: rt.Inner}
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

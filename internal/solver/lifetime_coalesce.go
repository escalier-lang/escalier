package solver

import (
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
//  3. Join expansion. A non-param lifetime is not itself nameable. It is either a
//     join variable minted at a return or branch, or a lifetime freshened when a
//     borrow-passing function was instantiated. It expands to the union of the param
//     lifetimes sharing its connected component in the condensed graph, so a return
//     uniting two borrows coalesces to ('a | 'b). The grouping is by connectivity
//     because instantiation interposes an intermediary between a call's argument
//     lifetime and the join it feeds. That intermediary outlives both the caller's
//     param lifetime and the join, so the param and the join are joined only through
//     it, with no direct outlives edge either way. A lifetime forced to 'static
//     renders 'static and absorbs. The union is a conservative rendering of the
//     directional bound set the condensed graph carries. A later change keeps the
//     join lifetime named and renders each kept param's outlives edges as `'a: 'c`
//     bounds instead of collapsing to the union ('a | 'b).
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

// componentParams returns the kept param lifetimes in v's connected component, sorted
// by variable ID for a canonical union member order. Members are keyed by SCC
// representative so mutually-outliving params list once, but emit a param var, since
// the representative itself can be a non-param bridge var named in no parameter slot.
func (a *ltAnalysis) componentParams(v *soltype.LifetimeVar) []soltype.Lifetime {
	leader := a.leaderOf(v)
	byRep := map[int]*soltype.LifetimeVar{}
	for p := range a.occ {
		if !a.isParam(p) || !a.kept(p) {
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
	members := make([]soltype.Lifetime, 0, len(byRep))
	for _, p := range byRep {
		members = append(members, p)
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].(*soltype.LifetimeVar).ID < members[j].(*soltype.LifetimeVar).ID
	})
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
	// A non-param lifetime is a join variable or an instantiation intermediary. It is
	// not nameable, so it expands to the union of the param lifetimes in its component.
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

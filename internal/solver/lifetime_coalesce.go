package solver

import (
	"maps"
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Display-time lifetime coalescing. The structural coalescers (coalesce /
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
//  2. Elision. A param lifetime connects nothing when it is written at one borrow and
//     never reaches an output. It occurs in no positive position, its connected
//     component in the condensed graph holds no output lifetime, and no second borrow
//     shares its name, so it is dropped. This is the lifetime-sort analogue of
//     single-polarity type-variable elimination. A lifetime written at two or more
//     borrows survives, since repeating one name across borrows says the regions are
//     the same, which is a constraint on the caller whether or not it reaches an
//     output. The drop branches on the borrow's Mut flag:
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
func coalesceLifetimes(t soltype.Type, pol soltype.Polarity, keepLts set.Set[*soltype.LifetimeVar]) soltype.Type {
	occ, noElide := walkLtOcc(t, pol)
	a := newLtAnalysis(occ, noElide)
	a.keepLts = keepLts
	return t.Accept(&ltRewriter{a: a}, pol)
}

// ltOccVisitor records where each lifetime variable occurs, producing the two facts the
// analysis needs. A RefType lifetime is COVARIANT, since it lives on the wrapper, not in
// the inner, so it is recorded in the borrow's own polarity. The mut-driven write view
// that flips the inner never touches it.
//
// The two facts are separate because a complement affects them differently:
//
//   - occ records the STRUCTURAL POSITION, meaning whether the borrow sits in a
//     parameter or in an output. That is a dataflow fact, not the variance the walk's
//     polarity carries, so EnterType converts one into the other. `Negative` means the
//     borrow originates at a parameter, so its lifetime is nameable. `Positive` means
//     the borrow reaches an output, so its lifetime is not elided.
//   - noElide holds the lifetimes that must keep their name whatever their position.
//     Two writes put a lifetime there, described below.
//
// A complement does not move a borrow between a parameter and an output, so the flip
// NegationType.Accept applies has to be undone before the polarity can be read as a
// position. negTypeDepth below carries what that costs. The recovered position decides which
// connected component counts as output-reaching, which in turn governs which lifetimes
// survive elision and which outlives bounds ltOutlivesRelation asserts.
//
// noElide exists because position alone is not enough. Two kinds of lifetime reach no
// output and still have to keep their name.
//
// A complement encloses the first kind. A complemented borrow reaching no output is
// genuinely connect-nothing, and the elision rule above drops those. Eliding under a
// complement changes the type rather than merely dropping a name, since `~(&'a T)` rendered
// as `~(&T)` is the complement of any borrow of T rather than of the `'a` one.
//
// A repeated name is the second. One name written at two parameter positions is a constraint
// the caller has to satisfy — the two regions must be the same — so it is named even when it
// reaches no output. `fn f<'a>(x: &'a mut B, y: &'a mut B)` renders its `'a` for that reason,
// while the single-write `fn f(x: &mut B)` elides. A write is any of the three positions
// EnterType records: a borrow's lifetime slot and an alias or class reference's lifetime
// argument, so `fn f<'a>(a: mut Holder<'a>, b: mut Holder<'a>)` counts too. negPolSeen is the
// running per-lifetime tally that second entry is derived from.
//
// Only a parameter lifetime enters by the second route, since the count runs in the negative
// arm alone. resolveLt relies on that: its non-param branch reads noElide to mean the
// complement case, which is the only one that can reach it.
type ltOccVisitor struct {
	occ     map[*soltype.LifetimeVar]occPolarity
	noElide set.Set[*soltype.LifetimeVar]
	// negPolSeen counts how many times each lifetime is written at NEGATIVE POLARITY, the
	// position a borrow the caller supplies sits in. The second write is what puts the
	// lifetime in noElide. A nested function type flips polarity, so a borrow written at a
	// CALLBACK's parameter is positive and does not count here.
	negPolSeen map[*soltype.LifetimeVar]int
	// negTypeDepth counts the NegationType nodes enclosing the node being visited.
	// NegationType.Accept flips the polarity its operand is visited at, which is right for
	// variance and wrong for reading position, so record converts polarity back into position
	// by flipping once per enclosing complement. Two complements cancel, so only the parity
	// matters. A non-zero depth also marks every lifetime written under it as never-elidable.
	//
	// It shares a prefix with negPolSeen and nothing else. This one is about NegationType,
	// that one is about polarity.
	negTypeDepth int
}

func (v *ltOccVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if _, isNeg := t.(*soltype.NegationType); isNeg {
		v.negTypeDepth++
	}
	// A lifetime is written at three kinds of node: the lifetime slot of a borrow, and the
	// lifetime-argument list of an alias or class reference. `&'b mut Holder<'a>` writes 'a
	// as an alias argument, and a signature that also writes it at a borrow ties the two
	// together, so all three positions have to be recorded for the relation to be visible.
	switch t := t.(type) {
	case *soltype.RefType:
		v.record(t.Lt, pol)
	case *soltype.AliasType:
		for _, arg := range t.LifetimeArgs {
			v.record(arg, pol)
		}
	case *soltype.ClassType:
		for _, arg := range t.LifetimeArgs {
			v.record(arg, pol)
		}
	}
	return soltype.EnterResult{}
}

// record notes one written occurrence of lt at the walk polarity pol, ignoring 'static and
// an already-resolved display lifetime, neither of which is a variable to name. It converts
// the walk's variance into the structural position the write sits in, then marks the write
// never-elidable when a complement encloses it or when it is the second parameter-position
// write of the same name.
func (v *ltOccVisitor) record(lt soltype.Lifetime, pol soltype.Polarity) {
	lv, ok := lt.(*soltype.LifetimeVar)
	if !ok {
		return
	}
	// Recover the position this write structurally sits in. See negTypeDepth.
	position := pol
	if v.negTypeDepth%2 == 1 {
		position = position.Flip()
	}
	if position == soltype.Positive {
		v.occ[lv] |= occPos
	} else {
		v.occ[lv] |= occNeg
		v.negPolSeen[lv]++
		if v.negPolSeen[lv] > 1 {
			v.noElide.Add(lv)
		}
	}
	if v.negTypeDepth > 0 {
		v.noElide.Add(lv)
	}
}

// ExitType pops the complement EnterType pushed. The two stay balanced because this
// visitor never sets SkipChildren, so Accept runs both halves for every node it enters.
func (v *ltOccVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if _, isNeg := t.(*soltype.NegationType); isNeg {
		v.negTypeDepth--
	}
	return t
}

// walkLtOcc runs the occurrence walk over t from the root polarity pol, returning the
// structural positions and the set of lifetimes that must keep their name.
func walkLtOcc(t soltype.Type, pol soltype.Polarity) (
	map[*soltype.LifetimeVar]occPolarity,
	set.Set[*soltype.LifetimeVar],
) {
	v := &ltOccVisitor{
		occ:        map[*soltype.LifetimeVar]occPolarity{},
		noElide:    set.NewSet[*soltype.LifetimeVar](),
		negPolSeen: map[*soltype.LifetimeVar]int{},
	}
	t.Accept(v, pol)
	return v.occ, v.noElide
}

// ltAnalysis is the precomputed input the rewriter reads: per-variable structural
// occurrence, the condensed outlives graph the grouping is built from, the
// connected-component leader of each representative, and the set of component leaders
// that hold a positive output lifetime.
type ltAnalysis struct {
	occ     map[*soltype.LifetimeVar]occPolarity
	noElide set.Set[*soltype.LifetimeVar] // lifetimes that must keep their name; never elided
	// keepLts holds the lifetimes a caller pins by identity, whatever their occurrences say.
	// freezeClassBody passes a class's own lifetime parameters: a field such as
	// `peer: &'a mut B` writes 'a once and in an output position, so the elision rule would
	// drop it and leave the frozen body with nothing for an instance's argument to replace.
	// Nil for every caller that pins nothing.
	keepLts  set.Set[*soltype.LifetimeVar]
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
func newLtAnalysis(
	occ map[*soltype.LifetimeVar]occPolarity,
	noElide set.Set[*soltype.LifetimeVar],
) *ltAnalysis {
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
	return &ltAnalysis{occ: occ, noElide: noElide, bs: bs, comp: comp, posComps: posComps}
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
//
// A lifetime noElide holds is never elided, even when it connects nothing. Two writes put
// one there. A complement enclosing it means dropping the name would change the type, so
// `declare fn f<'a>() -> ~(&'a T)` keeps `'a` in the quantifier prefix while the same
// signature over a plain borrow elides it. A second parameter-position write of the same
// name means the caller has a constraint to satisfy, so `fn f<'a>(x: &'a mut B, y: &'a mut B)`
// keeps `'a` too. The non-param branch below reads noElide as the complement case alone,
// which holds because only a parameter lifetime enters by the second route.
func (a *ltAnalysis) resolveLt(v *soltype.LifetimeVar) (lt soltype.Lifetime, elide bool) {
	if forcedToStatic(v) {
		return soltype.Static, false
	}
	// A pinned lifetime renders under its own name wherever it occurs. A class parameter is
	// the pinned case: it is named by the declaration, not by the member the walk is
	// coalescing, so the member's own occurrences do not decide whether it survives.
	if a.keepLts.Contains(v) {
		return v, false
	}
	if a.isParam(v) {
		if a.kept(v) || a.noElide.Contains(v) {
			return v, false // a named param renders under its own quantified name
		}
		return nil, true // connect-nothing param: elide
	}
	// A non-param lifetime is a join variable minted at a return or branch, or an
	// instantiation intermediary. It resolves to the param lifetimes it reaches.
	members := a.componentParams(v)
	switch len(members) {
	case 0:
		if a.noElide.Contains(v) {
			return v, false // complemented and reaching no param: keep its own name
		}
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
	occ, noElide := walkLtOcc(t, pol)
	if len(occ) == 0 {
		return nil, nil, nil
	}
	a := newLtAnalysis(occ, noElide)
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
	switch t := t.(type) {
	case *soltype.ClassType:
		args, changed := r.resolveArgs(t.LifetimeArgs)
		if !changed {
			return t
		}
		cp := *t
		cp.LifetimeArgs = args
		return &cp
	case *soltype.AliasType:
		args, changed := r.resolveArgs(t.LifetimeArgs)
		if !changed {
			return t
		}
		cp := *t
		cp.LifetimeArgs = args
		return &cp
	}
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

// resolveArgs rewrites a class or alias reference's lifetime arguments to their display
// form, so `Holder<'x>` constrained to outlive 'static renders `Holder<'static>` the way the
// borrow `&'x B` under the same constraint renders `&'static B`. changed is false when every
// argument came back unchanged, which lets the caller keep the node it was handed.
//
// An argument the analysis would elide keeps its variable. Eliding drops the `'a` from a
// borrow and leaves the `&`, but an argument position has no such reduced form to fall back
// on, so dropping it would leave the reference short an argument.
func (r *ltRewriter) resolveArgs(args []soltype.Lifetime) ([]soltype.Lifetime, bool) {
	if len(args) == 0 {
		return args, false
	}
	out := make([]soltype.Lifetime, len(args))
	changed := false
	for i, arg := range args {
		out[i] = arg
		lv, isVar := arg.(*soltype.LifetimeVar)
		if !isVar {
			continue
		}
		resolved, elide := r.a.resolveLt(lv)
		if elide || resolved == arg {
			continue
		}
		out[i] = resolved
		changed = true
	}
	return out, changed
}

// forcedToStatic reports whether a lifetime variable has 'static among its bounds,
// in which case it coalesces to 'static — the escape-to-static outcome. Both bound
// directions are checked: the escape constraint `v <: 'static` adds 'static as an
// upper bound, while a lower-bound 'static can arise from a join member.
func forcedToStatic(v *soltype.LifetimeVar) bool {
	return soltype.ContainsLifetime(v.LowerBounds, soltype.Static) ||
		soltype.ContainsLifetime(v.UpperBounds, soltype.Static)
}

package solver

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// coalesce walks a bound-carrying soltype.Type and returns a *coalesced*
// soltype.Type in which every TypeVarType has been inlined to its bounds
// (Delta #1 in m1-implementation-plan §2.2): positive position ⇒ the union of
// the variable's lower bounds, negative position ⇒ the intersection of its
// upper bounds, with empty bounds collapsing to never (⊥, positive) or unknown
// (⊤, negative).
//
// It is a package-private free function in M1 — it needs no Context (no shared
// counters or occurrence state until M3 reintroduces them). Unlike the spike,
// M1's coalescer is uniformly inlining: no bipolar-variable retention, no
// occurrence-analysis input, no named-ref output node. That whole
// polymorphism-rendering bundle lands in M3 (§3.3).
//
// M1 had no `seen` recursion guard: the M1 type set has no recursive formers
// (no aliases, no recursive types), so a uniform-inline walk terminates on a
// bound graph built from non-recursive source. M2's SCC driver (PR-5) breaks
// that assumption — a mutually-recursive group can build a cyclic var↔var bound
// graph (constrain appends var-to-var bounds and terminates on cycles via its
// own coinductive seen-set; coalesce would not) — so PR-5 pulls forward the
// path-scoped recursion guard the plan slated for M3 (m2-implementation-plan §7).
// See coalesceRec for the guard's behavior. M3 still owns the *precise* μ-bound
// recursive rendering; this guard only keeps the monomorphic walk total.
func coalesce(t soltype.Type, pol soltype.Polarity) soltype.Type {
	// The uniform-inlining coalesce is coalesceKeeping with a kept-flow map of nil and, as
	// the retained set, a generic function's own TypeParams binder vars. Holding those
	// symbolic keeps a rank-2 callback type such as `<T>(x: T) -> T` intact instead of
	// inlining its `T` binder to never. acceptTypeParamVar panics on a non-variable binder.
	// funcTypeParamVars is empty for a monomorphic type, so this is inert on the common path.
	return coalesceKeeping(t, pol, funcTypeParamVars(t), nil)
}

// coalesceKeeping is coalesce with a set of variables held symbolic rather than inlined to
// their bounds, plus a kept-flow map naming the kept vars that flow into each other var
// through the upper-bound graph. It is the generic-class analogue of coalesce: a class
// member whose type flows from a class type parameter reads as that parameter once the
// intermediate vars are inlined, but only if the parameter var survives and its inbound flow
// is recovered. B8's freezeClassBody passes the class's own TypeParam vars — and each
// method's own TypeParams vars — as keep, so `class Box<T> { read(self) { self.v } }` stores
// `read`'s return as `T` rather than collapsing the intermediate var to `never`.
// projectClassMember then substitutes `T` for the instance's argument. A nil keep and nil
// flow reduce it to the plain uniform-inlining coalesce.
func coalesceKeeping(t soltype.Type, pol soltype.Polarity, keep set.Set[*soltype.TypeVarType], flow map[*soltype.TypeVarType][]*soltype.TypeVarType) soltype.Type {
	c := t.Accept(&coalescer{seen: set.NewSet[*soltype.TypeVarType](), keep: keep, flow: flow}, pol)
	c = bubbleOwnedMut(c)            // #779: lift an owned-mut cell out of an immutable container
	return coalesceLifetimes(c, pol) // D4: resolve borrow lifetimes to their display form
}

// coalescer is the soltype-visitor form of coalesce. The structural arms and the
// variance flip come from soltype.Accept (the shared rewriting visitor); the var
// node — whose bounds are a side graph, not tree children — is the whole content
// here, handled in EnterType. seen is the path-scoped set of variables currently
// being inlined: it holds only the variables on the *current* recursion path
// (added before descending into bounds, removed after), so a variable reused in
// independent branches — e.g. the identity function's shared param (negative) and
// return (positive) var — is unaffected; only re-entering a variable already on
// the path is a genuine cycle.
type coalescer struct {
	seen set.Set[*soltype.TypeVarType]
	// keep holds variables retained symbolically rather than inlined to their bounds —
	// a generic class's own type-parameter vars, set only by coalesceKeeping. It is nil
	// for a plain coalesce, and a nil Set reads as empty, so the check below is inert on
	// that path.
	keep set.Set[*soltype.TypeVarType]
	// flow maps a var to the kept vars flowing into it through the upper-bound graph,
	// recovered as positive-position lower-bound contributions (see keptFlowMap). nil on
	// the plain coalesce path, where ranging over the nil map yields nothing.
	flow map[*soltype.TypeVarType][]*soltype.TypeVarType
}

func (c *coalescer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		// Atom or structural node — let Accept rebuild it from coalesced children
		// (including an overload-arm Union/Intersection input — the scoped lattice exception; see overloadIntersection).
		return soltype.EnterResult{}
	}
	// A retained type-parameter var stays symbolic: return it unchanged so a member typed
	// through it survives coalescing for per-instance projection (B8).
	if c.keep.Contains(v) {
		return soltype.EnterResult{Type: v, SkipChildren: true}
	}
	// Re-entering a variable already on the current path is an ungrounded recursive
	// position (no concrete type breaks the cycle). It collapses to the polarity
	// identity — the same value the position degenerates to when its bounds are
	// empty — which keeps the inline walk total. A precise μ-bound rendering of
	// such recursion is M3.
	if c.seen.Contains(v) {
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	c.seen.Add(v)
	defer c.seen.Remove(v) // path-scoped: pop on the way back up (panic-safe)
	// Uniform inline: drop the variable, keep only its (recursively coalesced)
	// bounds in the current polarity.
	bs := v.BoundsAt(pol)
	bounds := make([]soltype.Type, 0, len(bs))
	for _, b := range bs {
		bounds = append(bounds, b.Accept(c, pol))
	}
	// A kept type-parameter var flowing into v is a lower-bound contribution the var-var
	// edge stored on the parameter's side rather than on v (see keptFlowMap). It is a
	// positive-position value, so add it only in Positive position and recurse so a kept
	// var stays symbolic through the keep check above.
	if pol == soltype.Positive {
		for _, kv := range c.flow[v] {
			bounds = append(bounds, kv.Accept(c, pol))
		}
	}
	if len(bounds) == 0 {
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	return soltype.EnterResult{Type: widenVar(v, pol, combine(pol, dedup(bounds), v.Open)), SkipChildren: true}
}

func (c *coalescer) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	// Borrow lifetimes are left raw here and resolved by the coalesceLifetimes
	// post-pass, which needs the whole type to analyze lifetime occurrence (D4).
	return t
}

// bubbleOwnedMut rewrites a coalesced display type so no owned-mutable cell ever
// sits inside an immutable object or tuple (#779). `mut` is deep, so an owned-mut
// field is equivalent to making the whole container `mut`: `{p: mut {x}}` means the
// same as `mut {p: {x}}`. The nested form is the one the C3 field-write fold produces
// for `obj.p.x = 5`, and it is no longer a valid annotation, so the rendered
// signature must take the bubbled-up form to stay re-writable.
//
// It runs at DISPLAY time only, over the already-coalesced type — the operative
// bounds the solver checks against are untouched, so this changes only how an
// inferred signature is printed, never what it accepts. A `&`/`&mut` borrow field
// carries a lifetime and references external storage, so it is left in place; only an
// owned-mut cell (Mut set, Lt nil) bubbles.
func bubbleOwnedMut(t soltype.Type) soltype.Type {
	return t.Accept(&mutBubbler{}, soltype.Positive)
}

// mutBubbler is the rewriting visitor behind bubbleOwnedMut. The lift happens in
// ExitType, bottom-up: by the time an object/tuple is exited its children are already
// bubbled, so a child that bubbled to an owned-mut cell is visible here and lifts the
// cell one level further out.
type mutBubbler struct{}

func (b *mutBubbler) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	return soltype.EnterResult{}
}

func (b *mutBubbler) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.ObjectType:
		anyMut := false
		elems := make([]soltype.ObjTypeElem, len(t.Elems))
		for i, e := range t.Elems {
			// Only a property can hold an owned-mut cell to bubble. A method, getter, or
			// setter — carried by the object a class-body `self` binds to — has no field
			// cell, so it passes through unchanged (M5 B3).
			p, ok := e.(*soltype.PropertyElem)
			if !ok {
				elems[i] = e
				continue
			}
			ft := p.Type
			if inner, isMut, lt := soltype.UnwrapRef(ft); isMut && lt == nil {
				anyMut = true
				ft = inner // strip the redundant cell; the container's `mut` covers it
			}
			elems[i] = &soltype.PropertyElem{Name: p.Name, Type: ft, Optional: p.Optional, Readonly: p.Readonly}
		}
		obj := &soltype.ObjectType{Elems: elems, Inexact: t.Inexact}
		if anyMut {
			return soltype.NewRef(true, nil, obj)
		}
		return obj
	case *soltype.TupleType:
		anyMut := false
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			if inner, isMut, lt := soltype.UnwrapRef(e); isMut && lt == nil {
				anyMut = true
				e = inner
			}
			elems[i] = e
		}
		tup := &soltype.TupleType{Elems: elems, Inexact: t.Inexact}
		if anyMut {
			return soltype.NewRef(true, nil, tup)
		}
		return tup
	case *soltype.RefType:
		// An owned-mut wrapper over an inner that itself bubbled to owned-mut would be a
		// redundant `mut mut {…}`. Collapse it so the wrapper stays single.
		if t.Mut && t.Lt == nil {
			if inner, isMut, lt := soltype.UnwrapRef(t.Inner); isMut && lt == nil {
				if ri, ok := inner.(soltype.RefInner); ok {
					return &soltype.RefType{Mut: true, Lt: nil, Inner: ri}
				}
			}
		}
		return t
	default:
		return t
	}
}

// widenVar lowers a widenable `var` binding's coalesced value to its primitive
// (M4 B3) when it is read in covariant (Positive) position — `var a = 5` ⇒
// number, `var p = {x: 0}` ⇒ {x: number}. It runs AFTER combine, so a union of
// literals from distinct branches (`var a = if c { 1 } else { 2 }`) is left as
// `1 | 2`: widen passes a UnionType through, matching the reassignment rule that
// rejects `a = 3` there. It is a no-op for a non-widenable var, in negative
// position, or on a type carrying no literal (a function, a captured var).
//
// Both coalescers call it for parallelism with v.Open. The schemeCoalescer call
// is the live one: a widenable var is always a binding var, which generalizes to
// a PolyScheme and so renders through coalesceScheme. The plain coalescer call is
// DEFENSIVE — no current path coalesces a widenable var outside a scheme — kept
// so the flag is honored identically should one arise. TestWidenVar exercises the
// helper logic directly to cover both. The helper has no test reaching the plain
// coalescer through real source, by the same PolyScheme reasoning.
func widenVar(v *soltype.TypeVarType, pol soltype.Polarity, t soltype.Type) soltype.Type {
	if v.Widenable && pol == soltype.Positive {
		return widen(t)
	}
	return t
}

// occPolarity is the set of polarities a variable occurs in within a type — the
// occurrence input single-polarity elimination needs to decide which variables a
// generalized scheme can drop.
type occPolarity uint8

const (
	occPos occPolarity = 1 << iota
	occNeg
)

func (o occPolarity) both() bool { return o == occPos|occNeg }

// occKey keys the occurrence walk's seen-set by (variable, polarity) so a cyclic
// var↔var bound graph terminates while still recording every polarity a variable
// is reached in.
type occKey struct {
	v   *soltype.TypeVarType
	pol soltype.Polarity
}

// coalesceScheme coalesces a generalized scheme's RAW body for DISPLAY, retaining
// the variables that are genuine type parameters as named references while
// inlining the rest to their bounds. A variable is retained iff its co-occurrence
// representative is quantifiable (Level > genLevel) AND occurs in both polarities
// (single-polarity elimination); every other variable is inlined exactly as
// coalesce does — so on a body with no both-polarity quantifiable variable this
// reduces, node for node, to coalesce(t, Positive), keeping every monomorphic
// render unchanged.
//
// simplifyScheme (PR2) runs the co-occurrence analysis up front and hands the
// coalescer the resulting merge classes, which it only reads. Distinct quantified
// variables that always appear together resolve to one representative and so share
// a single type parameter. That collapses outer's
// `fn <T0, T1>(y: T0 & T1) -> [T0, T1]` to `fn <T0>(y: T0) -> [T0, T0]`.
//
// The retain decision degenerates to PR1's when nothing merges and symmetrization
// surfaces no extra occurrence. Each variable is then its own representative with
// its own polarities, so the check is exactly PR1's per-variable both-polarities
// test.
func coalesceScheme(t soltype.Type, genLevel int) soltype.Type {
	keep := funcTypeParamVars(t)
	simp := simplifyScheme(t, genLevel, keep)
	c := t.Accept(&schemeCoalescer{
		simp:     simp,
		genLevel: genLevel,
		keep:     keep,
		cleaned:  cleanBinderBounds(keep, simp),
		seen:     set.NewSet[*soltype.TypeVarType](),
	}, soltype.Positive)
	c = bubbleOwnedMut(c) // #779: lift an owned-mut cell out of an immutable container
	// A scheme display is always coalesced from the Positive root.
	return coalesceLifetimes(c, soltype.Positive) // D4: resolve borrow lifetimes to their display form
}

// funcTypeParamVars collects every generic function's own TypeParams binder var
// reachable from t, descending structural children and each var's bound side-graph.
func funcTypeParamVars(t soltype.Type) set.Set[*soltype.TypeVarType] {
	keep := set.NewSet[*soltype.TypeVarType]()
	t.Accept(&typeParamCollector{keep: keep, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	return keep
}

// typeParamCollector gathers FuncType.TypeParams binder vars for funcTypeParamVars,
// walking each var's bounds explicitly since they are a side graph, not tree children.
type typeParamCollector struct {
	keep set.Set[*soltype.TypeVarType]
	seen set.Set[*soltype.TypeVarType]
}

func (tc *typeParamCollector) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.FuncType:
		for _, tp := range t.TypeParams {
			tc.keep.Add(tp.Var)
		}
		return soltype.EnterResult{} // descend into params, return, and type-param bounds
	case *soltype.TypeVarType:
		if tc.seen.Contains(t) {
			return soltype.EnterResult{SkipChildren: true}
		}
		tc.seen.Add(t)
		for _, b := range t.LowerBounds {
			b.Accept(tc, pol)
		}
		for _, b := range t.UpperBounds {
			b.Accept(tc, pol)
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (tc *typeParamCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// schemeCoalescer is the soltype-visitor form of coalesceScheme. It has the same
// shape as coalescer. The structural arms and the pol.Flip() variance come from
// soltype.Accept, and the var node's side-graph bounds are walked here in
// EnterType.
//
// It adds the retain decision. A variable is KEPT as a named type parameter when
// its representative is quantifiable at genLevel and occurs in both polarities. A
// kept variable is merged with its coalesced bounds rather than inlined. Every
// other variable is inlined exactly as coalescer does. So on a body with no
// both-polarity quantifiable variable, this reduces node for node to a plain
// coalesce.
//
// Each variable resolves through simp to its co-occurrence representative, so
// every member of a merged class renders as the same parameter.
type schemeCoalescer struct {
	simp     *schemeSimplification
	genLevel int
	// keep holds a generic function's own TypeParams binder vars, held symbolic rather
	// than inlined to their bounds so the function's declared quantifier survives
	// coalescing. It is the value-path analogue of coalescer.keep for a class body.
	keep set.Set[*soltype.TypeVarType]
	// cleaned maps a binder var to a display copy whose bounds drop the same-class
	// artifact vars merged into it, so the copy renders `<T>` rather than `<T0, T: T0>`.
	// A binder with no such bound is absent here and keeps its original pointer.
	cleaned map[*soltype.TypeVarType]*soltype.TypeVarType
	seen    set.Set[*soltype.TypeVarType]
}

func (c *schemeCoalescer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		// Atom or structural node — let Accept rebuild it from coalesced children
		// (including an overload-arm Union/Intersection input — the scoped lattice exception; see overloadIntersection).
		return soltype.EnterResult{}
	}
	// A generic function's own type-parameter var stays symbolic: return it unchanged so
	// the declared quantifier survives rather than inlining a return-only param to never.
	// A binder whose bounds folded away a same-class artifact renders through its cleaned
	// copy so the vacuous `T: T0` constraint disappears.
	if c.keep.Contains(v) {
		return soltype.EnterResult{Type: c.displayBinder(v), SkipChildren: true}
	}
	rep := c.simp.rep(v)
	retain := rep.Level > c.genLevel && c.simp.mergedOcc[rep.ID].both() && !hasEqualBounds(rep)
	// A non-binder var whose class representative is a binder renders under that binder's
	// display copy, so an artifact reached in a structural position reads as the declared
	// parameter rather than a second name for the same type.
	rep = c.displayBinder(rep)
	if c.seen.Contains(rep) {
		// A cycle back to a variable already on the path: a retained type parameter
		// keeps its name (a rough μ-reference, refined in M3's precise μ-rendering),
		// an inlined variable collapses to the polarity identity.
		if retain {
			return soltype.EnterResult{Type: rep, SkipChildren: true}
		}
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	c.seen.Add(rep)
	defer c.seen.Remove(rep) // path-scoped: pop on the way back up (panic-safe)

	// v's own bounds, not the representative's.
	bs := v.BoundsAt(pol)

	// Pre-size parts with rep at index 0 when retaining, rather than appending then
	// prepending. At the front, rep appears first in the union or intersection combine
	// builds, and dedup keeps it distinct from any bound that cycles back to it.
	n := len(bs)
	if retain {
		n++
	}
	parts := make([]soltype.Type, 0, n)
	if retain {
		parts = append(parts, rep)
	}
	// Recursively coalesce each bound. When a bound is another member of v's class
	// whose rep is already on the path, the seen guard short-circuits it to the name
	// and its own bounds go unwalked. No information is lost. constrain copies a
	// concrete bound to every variable along a var↔var subtyping chain, so the class's
	// reachable concrete bounds already sit on v, the first member reached. This holds
	// because the body is propagation-closed, meaning every variable already carries
	// the bounds propagated to it. coalesceScheme renders a component only after it is
	// fully constrained, so that is always true here.
	for _, b := range bs {
		parts = append(parts, b.Accept(c, pol))
	}
	if len(parts) == 0 {
		// Only reachable with !retain and no bounds — empty bounds under retain
		// already leave parts=[rep]. Collapse to the polarity identity.
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	return soltype.EnterResult{Type: widenVar(v, pol, combine(pol, dedup(parts), v.Open)), SkipChildren: true}
}

func (c *schemeCoalescer) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	// Borrow lifetimes are left raw here and resolved by the coalesceLifetimes
	// post-pass, which needs the whole type to analyze lifetime occurrence (D4).
	return t
}

// displayBinder maps a binder var to its cleaned display copy when one exists, else
// returns it unchanged. One pointer is shared across every occurrence, so it names once.
func (c *schemeCoalescer) displayBinder(v *soltype.TypeVarType) *soltype.TypeVarType {
	if cv, ok := c.cleaned[v]; ok {
		return cv
	}
	return v
}

// cleanBinderBounds returns a display copy of each binder var whose bounds name a var
// in its own merged class, with those same-class var bounds dropped. Such a bound is
// the vacuous half of a mutual cycle `T <: β <: … <: T` and prints as `T: T0`; dropping
// it also removes the artifact var β the printer would name T0. A concrete bound already
// sits on the binder, so nothing real is lost. A binder needing no change is omitted.
func cleanBinderBounds(keep set.Set[*soltype.TypeVarType], simp *schemeSimplification) map[*soltype.TypeVarType]*soltype.TypeVarType {
	out := map[*soltype.TypeVarType]*soltype.TypeVarType{}
	for v := range keep {
		rep := simp.rep(v)
		up, upChanged := dropSameClassVars(v.UpperBounds, rep, simp)
		lo, loChanged := dropSameClassVars(v.LowerBounds, rep, simp)
		if !upChanged && !loChanged {
			continue
		}
		cp := *v
		cp.UpperBounds = up
		cp.LowerBounds = lo
		out[v] = &cp
	}
	return out
}

// dropSameClassVars returns bounds with every var whose class representative is rep
// removed, plus whether anything was dropped. A non-var bound and a var in a different
// class pass through unchanged.
func dropSameClassVars(bounds []soltype.Type, rep *soltype.TypeVarType, simp *schemeSimplification) ([]soltype.Type, bool) {
	changed := false
	out := make([]soltype.Type, 0, len(bounds))
	for _, b := range bounds {
		if bv, ok := b.(*soltype.TypeVarType); ok && simp.rep(bv) == rep {
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return bounds, false
	}
	return out, true
}

// schemeType returns a scheme's coalesced DISPLAY type (variable-free except for
// retained type parameters), the soltype handed to soltype.PrintAsScheme and
// recorded in Info. A MonoScheme coalesces uniformly (no retained parameters); a
// PolyScheme retains its quantified parameters via coalesceScheme.
func schemeType(s TypeScheme) soltype.Type {
	switch sc := s.(type) {
	case *MonoScheme:
		return coalesce(sc.Ty, soltype.Positive)
	case *PolyScheme:
		return sc.display()
	}
	panic(fmt.Sprintf("schemeType: unknown TypeScheme %T", s))
}

// renderScheme renders a scheme to its Escalier type-annotation string, with a
// <T0, …> quantifier prefix when generalization left type parameters behind.
//
// For a PolyScheme it names only the variables generalization quantified — those
// with Level > sc.Level, the exact retention criterion coalesceScheme uses — so a
// variable that escaped coalescing (a captured var at Level <= sc.Level that was
// not inlined) renders as the raw t{ID} debug form instead of being disguised as a
// spurious type parameter. A MonoScheme coalesces to a var-free type, so plain
// PrintAsScheme suffices.
func renderScheme(s TypeScheme) string {
	switch sc := s.(type) {
	case *MonoScheme:
		t := coalesce(sc.Ty, soltype.Positive)
		return soltype.PrintAsSchemeWith(t, func(*soltype.TypeVarType) bool { return true },
			displayLtBounds(t, soltype.Positive))
	case *PolyScheme:
		t := sc.display()
		return soltype.PrintAsSchemeWith(t, func(v *soltype.TypeVarType) bool {
			return v.Level > sc.Level
		}, displayLtBounds(t, soltype.Positive))
	}
	panic(fmt.Sprintf("renderScheme: unknown TypeScheme %T", s))
}

// hasEqualBounds reports whether v's lower and upper bound sets are non-empty and
// structurally equal, which pins it to a single concrete type: it has no freedom as a
// type parameter and is inlined rather than retained. This arises for the receiver
// var of a deep-mut nested write (#779): `obj.p.x = 5` makes obj.p invariant inside
// the mut container, and the residual write-back gives it equal lower and upper
// bounds `{x: number, ...}`. Retaining it would surface a spurious `T0 & {x: number}`
// where the pinned `{x: number}` is exact. A var with a genuine type-parameter role,
// such as the `v` in `fn (obj, v) { obj.x = v }`, has no such matched bounds — its
// invariance comes from the field write-view with no concrete bound on both sides —
// so it is still retained.
func hasEqualBounds(v *soltype.TypeVarType) bool {
	lo := withoutSelf(v, v.LowerBounds)
	hi := withoutSelf(v, v.UpperBounds)
	if len(lo) == 0 || len(hi) == 0 {
		return false
	}
	return sameBoundSet(lo, hi)
}

// withoutSelf drops a vacuous self-reference (v <: v) from a bound list. The deep-mut
// write chain can leave a var with a self-edge among its bounds; it constrains
// nothing, so hasEqualBounds ignores it when comparing the lower and upper bound sets.
func withoutSelf(v *soltype.TypeVarType, bounds []soltype.Type) []soltype.Type {
	out := bounds[:0:0]
	for _, b := range bounds {
		if bv, ok := b.(*soltype.TypeVarType); ok && bv == v {
			continue
		}
		out = append(out, b)
	}
	return out
}

// sameBoundSet reports whether two bound lists hold structurally-equal types as sets,
// ignoring order and multiplicity.
func sameBoundSet(a, b []soltype.Type) bool {
	return boundsSubset(a, b) && boundsSubset(b, a)
}

func boundsSubset(a, b []soltype.Type) bool {
	for _, x := range a {
		found := false
		for _, y := range b {
			if equalType(x, y) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// emptyOf returns the lattice identity for a polarity: never (⊥, the identity of
// |) for a positive position with no lower bounds, unknown (⊤, the identity of &)
// for a negative position with no upper bounds. Shared by the empty-bounds and
// recursion-cycle cases, which collapse to the same value.
func emptyOf(pol soltype.Polarity) soltype.Type {
	if pol == soltype.Positive {
		return &soltype.NeverType{}
	}
	return &soltype.UnknownType{}
}

// combine builds a soltype.UnionType (Positive) or soltype.IntersectionType
// (Negative) of parts, returning the sole element directly when only one
// remains. The UnionType/IntersectionType nodes ship in M1 (soltype/type.go) so
// combine can always return a native soltype.Type.
//
// In Negative position the object parts are first folded into a single object by
// foldUsageBounds (B1) so member-access requirements on one receiver render as one
// compact object rather than an intersection of one-property objects. The folded
// object closes to exact unless `open` is set — an `open` parameter (B2) stays
// row-polymorphic (inexact). This is the DISPLAY fold; sealUsageObjects runs the
// same foldUsageBounds operatively on the stored bounds at generalization.
func combine(pol soltype.Polarity, parts []soltype.Type, open bool) soltype.Type {
	if pol == soltype.Negative {
		parts = foldUsageBounds(parts, open)
	}
	// Route through the M6 PR1 smart constructors so the coalesced output is
	// flattened, deduped, lattice-identity-pruned, and canonically ordered.
	// The Context is nil here. Members are already coalesced and concrete, so
	// the core normalization is enough. Subsumption is reserved for the
	// Context-bearing mint sites resolveTypeAnn in PR2 and joinBorrows in PR6.
	// The single-member collapse is handled by the constructor.
	//
	// Coalesced unions are exact by default. An inferred shape is closed
	// unless PR4 threads an inexact source flag through to here.
	if pol == soltype.Positive {
		return newUnion(nil, parts, false)
	}
	return newIntersection(nil, parts)
}

// foldUsageBounds folds the INEXACT ObjectType parts of an upper-bound list into a
// single object — the meet of the member-access requirements — leaving every other
// part untouched. The folded object is exact unless `open`.
//
// Two callers fold with this one helper, so the exactness rule lives in one place:
//   - sealUsageObjects (poly.go) is the OPERATIVE seal. It writes the fold back
//     into a closed usage var's stored upper bounds at generalization, so
//     freshenAbove copies a sealed exact requirement at each call site and a caller
//     passing extra fields is rejected.
//   - combine (above) is the DISPLAY fold. It runs during coalescing on a var's
//     already-coalesced upper bounds, folding the vars sealUsageObjects skipped
//     such as open params, for a compact rendered type.
//
// Both pass the var's Open flag, so the operative and display folds agree on
// exactness.
//
// Only inexact objects fold: an inexact object is a member-access requirement
// ("has at least these fields"), and merging several is the receiver's combined
// width requirement. An EXACT object on the bounds is an already-closed shape, not
// a width requirement, so it passes through unchanged — folding it would be wrong
// (`{x} & {y}` over exact objects is uninhabited, not `{x, y}`) and would feed a
// non-member object to mergeObjectGroup/AsProperty.
//
// Member-access requirements on one receiver arrive as separate inexact
// one-property objects: A1's inferMember lowers `obj.a; obj.b` to the upper bounds
// `{a: β, ...}` and `{b: γ, ...}` on the receiver var. Folding them yields
// `{a: β, b: γ}` instead of the non-compact `{a: β, ...} & {b: γ, ...}`. A
// property appearing in several parts becomes the intersection of its types,
// because `obj <: {a: β}` and `obj <: {a: γ}` together require `obj.a <: β & γ`.
//
// Policy A (exact-types spec §8.1): the folded usage object closes to EXACT once
// body inference has produced every selection on the receiver. The per-access
// requirements stay inexact (A1); only this folded result is sealed. The `open`
// parameter marker (B2) is the opt-out: when set, the folded object stays inexact
// so the param is row-polymorphic and callers may pass objects with extra fields.
//
// Whole-object `mut` merge (M4 C3): the field-write path records a write `obj.x =
// 5` as a MUTABLE inexact requirement `mut {x: number, ...}` on the receiver var,
// alongside the bare inexact reads. When ANY write is present, every selection —
// reads and writes alike — folds into ONE object wrapped in `mut`, following
// internal/checker rather than the spike's per-field partition: `obj.x = 5; obj.y =
// 10` ⇒ `mut {x, y}` and the mixed `val x = obj.bar; obj.baz = 5` ⇒
// `mut {bar, baz}` — a single object, not `{bar} & mut {baz}`. With
// no write the reads fold into a bare (immutable) object, the pre-C3 behavior. The
// tradeoff: wrapping the whole object in `mut` makes read-only fields invariant
// rather than covariant; for a generalized function this is invisible because each
// read-only field is a fresh-per-call type parameter.
//
// This is NOT recursive: it folds the objects of ONE var's bound list and does not
// descend into property types. Nesting (`p.a.b`) is reached by the callers' walks
// over the var graph — sealUsageObjects's loop over every collected var for the
// operative seal, and coalesce / coalesceScheme's recursive bound coalescing for
// display.
func foldUsageBounds(parts []soltype.Type, open bool) []soltype.Type {
	var objs []*soltype.ObjectType
	var others []soltype.Type
	mut := false
	for _, p := range parts {
		if o, isWrite, ok := usageObject(p); ok {
			objs = append(objs, o)
			mut = mut || isWrite
			continue
		}
		others = append(others, p)
	}
	if len(objs) == 0 {
		return parts // nothing to fold; leave the bound list as-is
	}
	mergedObj := mergeObjectGroup(objs, open)
	merged := soltype.Type(mergedObj)
	if mut {
		// NewRef does not collapse a (true, nil) cell — an owned-mutable object — so
		// the wrapper survives. mergeObjectGroup returns a *ObjectType, a RefInner.
		merged = soltype.NewRef(true, nil, mergedObj)
	}
	return append([]soltype.Type{merged}, others...)
}

// usageObject classifies a coalesced upper bound as a member-access requirement on
// a receiver, the unit foldUsageBounds folds. It distinguishes the two requirement
// shapes the inference walk mints:
//   - a bare inexact object is a member READ — `obj.x` lowers to {x: β, ...}
//     (valueProp); ok=true, write=false.
//   - a `mut`-wrapped inexact object is a field WRITE — `obj.x = v` lowers to
//     mut {x: widen(v), ...} (inferMemberAssign); ok=true, write=true.
//
// Everything else is not a usage requirement and returns ok=false: an EXACT object
// is an already-closed shape (folding it would be wrong), an immutable borrow is not
// a member requirement, and a non-object bound is unrelated. Centralizing the shape
// test here keeps the two requirement forms named in one place rather than as inline
// type-switches, so a future requirement shape is added here, not hunted for.
func usageObject(t soltype.Type) (obj *soltype.ObjectType, write bool, ok bool) {
	if o, isObj := t.(*soltype.ObjectType); isObj && o.Inexact {
		return o, false, true
	}
	if inner, isMut, _ := soltype.UnwrapRef(t); isMut {
		if o, isObj := inner.(*soltype.ObjectType); isObj && o.Inexact {
			return o, true, true
		}
	}
	return nil, false, false
}

// mergeObjectGroup is the property-union step inside foldUsageBounds: it folds the
// already-selected inexact objects into one object. The property sets are unioned
// and a property shared by several objects becomes the intersection of its types,
// after dropping structurally-equal duplicates — so two writes of the same widened
// primitive (`obj.x = 5; obj.x = 10`, both `number`) give `x: number`, not the
// redundant `x: number & number`, while two distinct requirements still intersect.
// Property order is alphabetical for stable rendering. A property is optional in the
// result only when it is optional in every object that carries it. The result is
// exact (closed) unless `open`, in which case it stays inexact.
//
// This is NOT recursive: each property's type is copied through verbatim, never
// descended into. Nesting is handled by the var-graph walks in sealUsageObjects,
// coalesce, and coalesceScheme — see foldUsageBounds.
func mergeObjectGroup(objs []*soltype.ObjectType, open bool) *soltype.ObjectType {
	types := map[string][]soltype.Type{} // property name → its distinct types, in first-seen order
	optional := map[string]bool{}        // property name → optional in every object seen so far
	readonly := map[string]bool{}        // property name → readonly in any object seen so far
	var order []string
	for _, o := range objs {
		for _, elem := range o.Elems {
			pe := soltype.AsProperty(elem)
			if _, seen := types[pe.Name]; !seen {
				order = append(order, pe.Name)
				optional[pe.Name] = pe.Optional // first occurrence seeds the value
			} else {
				optional[pe.Name] = optional[pe.Name] && pe.Optional // optional iff optional in all
			}
			// Conservative `||`: a merged field is readonly if any contributing
			// object marks it so. Sound today only because requirement-builders
			// always mint Readonly:false; a builder that ever emits true would
			// poison co-folded writable uses with a spurious subtype error.
			readonly[pe.Name] = readonly[pe.Name] || pe.Readonly
			types[pe.Name] = appendDistinct(types[pe.Name], pe.Type)
		}
	}
	sort.Strings(order)
	elems := make([]soltype.ObjTypeElem, len(order))
	for i, name := range order {
		// Route the per-property intersection through newIntersection so a
		// shared property's type is normalized like every other lattice mint.
		// Context is nil because the per-property folded types are already
		// coalesced, so the core normalization is enough.
		elems[i] = &soltype.PropertyElem{Name: name, Type: newIntersection(nil, types[name]), Optional: optional[name], Readonly: readonly[name]}
	}
	// Closed (Inexact: false) by Policy A; an `open` param leaves it inexact (B2).
	return &soltype.ObjectType{Elems: elems, Inexact: open}
}

// appendDistinct appends t to parts unless a structurally-equal type is already
// present, so a property folded from several requirements with the same type does
// not accumulate redundant intersection members (mergeObjectGroup).
func appendDistinct(parts []soltype.Type, t soltype.Type) []soltype.Type {
	for _, p := range parts {
		if equalType(p, t) {
			return parts
		}
	}
	return append(parts, t)
}

// dedup removes structurally-equal parts, preserving first-occurrence order.
// The spike deduplicated by rendered string (via type_system.PrintType); M1
// has no printer in `solver` yet (it ships in PR4, in `soltype`), so M1
// deduplicates by structural equality instead.
func dedup(parts []soltype.Type) []soltype.Type {
	out := make([]soltype.Type, 0, len(parts))
	for _, p := range parts {
		dup := false
		for _, kept := range out {
			if equalType(p, kept) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}

// equalType is structural equality over the coalesced type set. A monomorphic
// coalesce produces no TypeVarTypes, but coalesceScheme RETAINS quantified type
// parameters as named references, so a generalized scheme's display type can carry
// variables — compared here by pointer identity (the same var is one type
// parameter), which is what lets dedup collapse `T0 & T0` to `T0`.
//
// Lifetimes compare by pointer identity too, which two borrows minted in one coalesce
// share whenever they denote the same borrow. That identity keying is what dedup and the
// lattice's canonical member order rely on, and it keeps two independent param lifetimes
// with no bound between them distinct. alphaEqualTypes is the cross-scheme variant that
// compares lifetimes up to renaming instead.
func equalType(a, b soltype.Type) bool {
	return equalTypeWith(a, b, &alphaCtx{})
}

// alphaCtx carries the bijections equalTypeWith uses to compare two types up to a
// consistent renaming of their bound variables. tvAToB and tvBToA pair the positional
// type parameters of two generic FuncTypes, bound at each function boundary so a
// parameter's identity is its position rather than its variable id. lt pairs borrow
// lifetimes for alphaEqualTypes. A nil lt selects pointer-identity lifetime equality,
// the within-coalesce default equalType uses. The type-parameter maps start nil and are
// allocated on the first binding, so the monomorphic common case allocates nothing.
type alphaCtx struct {
	lt     *ltPairing
	tvAToB map[int]int
	tvBToA map[int]int
	// ltpAToB and ltpBToA pair the positional lifetime parameters of two generic
	// FuncTypes, the lifetime-sort twin of tvAToB/tvBToA. They are bound at each
	// function boundary so a lifetime parameter's identity is its position rather than
	// its variable id. They are separate from lt. lt discovers a bijection over borrow
	// lifetimes for alphaEqualTypes, while these are declared bindings over a function's
	// own quantified lifetime params. They start nil and allocate on first binding, so
	// the common lifetime-param-free case allocates nothing.
	ltpAToB map[int]int
	ltpBToA map[int]int
}

// bindTypeParams pairs two generic FuncTypes' type parameters positionally, so every
// later occurrence of one side's parameter must match the other's bound partner. The
// bindings persist for the rest of the walk. Type-variable ids are unique across the
// comparison, so a parameter bound here is never confused with one from another
// function.
func (ctx *alphaCtx) bindTypeParams(as, bs []*soltype.TypeParam) {
	if ctx.tvAToB == nil {
		ctx.tvAToB = map[int]int{}
		ctx.tvBToA = map[int]int{}
	}
	for i := range as {
		ctx.tvAToB[as[i].Var.ID] = bs[i].Var.ID
		ctx.tvBToA[bs[i].Var.ID] = as[i].Var.ID
	}
}

// sameTypeVar reports whether two type variables are equal under the type-parameter
// bijection. A variable bound as one side's parameter must map to the other's partner. A
// variable bound on neither side is a shared or free variable and compares by pointer
// identity, the rule the rest of equalType keys variables by.
func (ctx *alphaCtx) sameTypeVar(a, b *soltype.TypeVarType) bool {
	if j, ok := ctx.tvAToB[a.ID]; ok {
		return j == b.ID
	}
	if _, ok := ctx.tvBToA[b.ID]; ok {
		return false // b is a bound parameter, a is not — mismatch
	}
	return a == b
}

// bindLifetimeParams pairs two generic FuncTypes' lifetime parameters positionally, the
// lifetime-sort twin of bindTypeParams, so every later occurrence of one side's lifetime
// parameter must match the other's bound partner. The bindings persist for the rest of
// the walk. Lifetime-variable ids are unique across the comparison, so a parameter bound
// here is never confused with one from another function.
func (ctx *alphaCtx) bindLifetimeParams(as, bs []*soltype.LifetimeParam) {
	if ctx.ltpAToB == nil {
		ctx.ltpAToB = map[int]int{}
		ctx.ltpBToA = map[int]int{}
	}
	for i := range as {
		ctx.ltpAToB[as[i].Var.ID] = bs[i].Var.ID
		ctx.ltpBToA[bs[i].Var.ID] = as[i].Var.ID
	}
}

// sameLifetime reports lifetime equality under the alpha context. A lifetime bound as
// one side's parameter must map to the other's partner through the lifetime-parameter
// bijection, so two borrowing methods differing only in lifetime-variable id compare
// equal. A lifetime bound on neither side falls back to ltEqualWith, which keys a
// LifetimeVar by pointer under a nil borrow pairing and by first-appearance index under
// one.
func (ctx *alphaCtx) sameLifetime(a, b soltype.Lifetime) bool {
	av, aok := a.(*soltype.LifetimeVar)
	bv, bok := b.(*soltype.LifetimeVar)
	if aok {
		if j, ok := ctx.ltpAToB[av.ID]; ok {
			return bok && j == bv.ID
		}
	}
	if bok {
		if _, ok := ctx.ltpBToA[bv.ID]; ok {
			return false // b is a bound lifetime parameter, a is not — mismatch
		}
	}
	return ltEqualWith(a, b, ctx.lt)
}

// ltPairing is the bijection alphaEqualTypes discovers between the lifetime variables of
// two types compared up to renaming. equalTypeWith fills it in as it walks: the first
// time it matches a borrow on each side it binds their lifetime variables together, and
// every later occurrence must respect that binding. aToB and bToA are the two directions
// of the bijection, keyed by lifetime-variable ID. aVars and bVars list the bound
// variables in binding order, so index i on each side names one paired lifetime.
// sameOutlivesUnderPairing compares the outlives relation over those pairs. The pairing
// sits on alphaCtx.lt. A nil lt selects pointer-identity lifetime equality, the
// within-coalesce default equalType uses.
type ltPairing struct {
	aToB  map[int]int
	bToA  map[int]int
	aVars []*soltype.LifetimeVar
	bVars []*soltype.LifetimeVar
}

// pair records or rechecks that a and b are the same lifetime under the bijection. A
// variable already bound to a different partner fails, which is what keeps a borrow that
// shares one lifetime across two positions from matching a side that uses two distinct
// lifetimes there. Because the walk matches structure in the same order on both sides,
// binding a and b together the first time they are matched pairs corresponding lifetimes
// regardless of the order the two types happen to list their object properties.
func (p *ltPairing) pair(a, b *soltype.LifetimeVar) bool {
	if j, ok := p.aToB[a.ID]; ok {
		return j == b.ID
	}
	if _, ok := p.bToA[b.ID]; ok {
		return false // b is already bound to a different left-side lifetime
	}
	p.aToB[a.ID] = b.ID
	p.bToA[b.ID] = a.ID
	p.aVars = append(p.aVars, a)
	p.bVars = append(p.bVars, b)
	return true
}

// equalTypeWith is equalType threading an alphaCtx. Its lt pairing keys a borrow's
// lifetime by first-appearance index when set, so alphaEqualTypes can compare borrows
// across schemes whose lifetime variables have independent identities. With a nil lt it
// keys lifetimes by pointer. Its type-parameter bijection compares two generic
// FuncTypes up to alpha-renaming of their positional TypeParams, so a parameter's
// identity is its position rather than its variable id.
func equalTypeWith(a, b soltype.Type, ctx *alphaCtx) bool {
	switch a := a.(type) {
	case *soltype.TypeVarType:
		b, ok := b.(*soltype.TypeVarType)
		return ok && ctx.sameTypeVar(a, b)
	case *soltype.PrimType:
		b, ok := b.(*soltype.PrimType)
		return ok && a.Prim == b.Prim
	case *soltype.LitType:
		b, ok := b.(*soltype.LitType)
		return ok && a.Equal(b)
	case *soltype.Void:
		_, ok := b.(*soltype.Void)
		return ok
	case *soltype.NullType:
		_, ok := b.(*soltype.NullType)
		return ok
	case *soltype.UndefinedType:
		_, ok := b.(*soltype.UndefinedType)
		return ok
	case *soltype.NeverType:
		_, ok := b.(*soltype.NeverType)
		return ok
	case *soltype.UnknownType:
		_, ok := b.(*soltype.UnknownType)
		return ok
	case *soltype.ErrorType:
		_, ok := b.(*soltype.ErrorType)
		return ok
	case *soltype.FuncType:
		b, ok := b.(*soltype.FuncType)
		if !ok || len(a.Params) != len(b.Params) || a.Inexact != b.Inexact || len(a.TypeParams) != len(b.TypeParams) || len(a.LifetimeParams) != len(b.LifetimeParams) {
			return false
		}
		if len(a.TypeParams) > 0 {
			// Bind the two functions' type parameters positionally, then compare each
			// one's constraint (its variable's upper bounds) and default under that
			// binding. Binding all of them first lets a later parameter's constraint or
			// default reference an earlier one.
			ctx.bindTypeParams(a.TypeParams, b.TypeParams)
			for i := range a.TypeParams {
				at, bt := a.TypeParams[i], b.TypeParams[i]
				if !equalTypeSliceWith(at.Var.UpperBounds, bt.Var.UpperBounds, ctx) {
					return false
				}
				if (at.Default == nil) != (bt.Default == nil) {
					return false
				}
				if at.Default != nil && !equalTypeWith(at.Default, bt.Default, ctx) {
					return false
				}
			}
		}
		if len(a.LifetimeParams) > 0 {
			// Bind the two functions' lifetime parameters positionally, then compare each
			// one's outlives bounds under that binding, so two borrowing methods differing
			// only in lifetime-variable id compare equal. Binding all of them first lets a
			// later parameter's `'b: 'a` bound reference an earlier one.
			ctx.bindLifetimeParams(a.LifetimeParams, b.LifetimeParams)
			for i := range a.LifetimeParams {
				if !sameLifetimeSlice(a.LifetimeParams[i].Bounds, b.LifetimeParams[i].Bounds, ctx) {
					return false
				}
			}
		}
		// Receiver presence distinguishes an instance method from a static one, and the
		// receiver type carries its mutability and borrow, so `(self) -> T`, `(mut self)
		// -> T`, and `() -> T` are all distinct.
		if !equalSelfParam(a.SelfParam, b.SelfParam, ctx) {
			return false
		}
		for i := range a.Params {
			if a.Params[i].Optional != b.Params[i].Optional || a.Params[i].Rest != b.Params[i].Rest || !equalTypeWith(a.Params[i].Type, b.Params[i].Type, ctx) {
				return false
			}
		}
		return equalTypeWith(a.Ret, b.Ret, ctx)
	case *soltype.TupleType:
		b, ok := b.(*soltype.TupleType)
		// Inexact flags must be equal — an open tuple never equals a closed one,
		// mirroring the ObjectType/FuncType arms' Inexact discriminator.
		if !ok || a.Inexact != b.Inexact || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !equalTypeWith(a.Elems[i], b.Elems[i], ctx) {
				return false
			}
		}
		return true
	case *soltype.ObjectType:
		b, ok := b.(*soltype.ObjectType)
		// Inexact flags must be equal — an open object never equals a closed one.
		// This mirrors the FuncType arm's a.Inexact discriminator.
		if !ok || a.Inexact != b.Inexact || len(a.Elems) != len(b.Elems) {
			return false
		}
		// Objects are equal up to member order, so each a-member must find a b-member
		// that shares its name and equals it kind-for-kind. Equal lengths plus that
		// match on every a-member is a full structural match. Comparing against every
		// same-named b-member, rather than the first, disambiguates a getter and setter
		// that share a name.
		for _, ae := range a.Elems {
			name := soltype.ObjElemName(ae)
			found := false
			for _, be := range b.Elems {
				if soltype.ObjElemName(be) == name && equalObjElem(ae, be, ctx) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case *soltype.ClassType:
		b, ok := b.(*soltype.ClassType)
		// Nominal identity is the qualified name plus the Final exactness flag. The
		// lifetime arguments then compare positionally, then the type arguments. A
		// ClassType's Lt is always nil today, so it is not compared.
		if !ok || a.Name != b.Name || a.Final != b.Final {
			return false
		}
		if !sameLifetimeSlice(a.LifetimeArgs, b.LifetimeArgs, ctx) {
			return false
		}
		return equalTypeSliceWith(a.TypeArgs, b.TypeArgs, ctx)
	case *soltype.AliasType:
		b, ok := b.(*soltype.AliasType)
		// Two alias references are equal when they name the same alias, their lifetime
		// arguments compare equal positionally, and their type arguments do too. The Name is
		// the handle's identity. An alias carries no exactness flag.
		if !ok || a.Name != b.Name {
			return false
		}
		if !sameLifetimeSlice(a.LifetimeArgs, b.LifetimeArgs, ctx) {
			return false
		}
		return equalTypeSliceWith(a.TypeArgs, b.TypeArgs, ctx)
	case *soltype.PromiseType:
		b, ok := b.(*soltype.PromiseType)
		return ok && equalTypeWith(a.Inner, b.Inner, ctx)
	case *soltype.RefType:
		b, ok := b.(*soltype.RefType)
		// Mut must match — a mutable borrow never equals an immutable one — and the
		// lifetimes must match: D2 mints borrow lifetimes, so two borrows differing only
		// in lifetime are NOT equal. Without the Lt check, dedup would collapse them and
		// silently drop a lifetime the solver computed. ltEqualWith compares a LifetimeVar
		// by pointer under a nil pairing and by first-appearance index under one.
		return ok && a.Mut == b.Mut && ctx.sameLifetime(a.Lt, b.Lt) && equalTypeWith(a.Inner, b.Inner, ctx)
	case *soltype.UnionType:
		b, ok := b.(*soltype.UnionType)
		// Inexact flags must match, since an open union never equals a closed
		// one. newUnion imposes canonical member order at construction, so the
		// positional equalTypeSliceWith is order-stable and two unions over the
		// same member set compare equal whatever order their members were minted in.
		return ok && a.Inexact == b.Inexact && equalTypeSliceWith(a.Types, b.Types, ctx)
	case *soltype.IntersectionType:
		b, ok := b.(*soltype.IntersectionType)
		return ok && equalTypeSliceWith(a.Types, b.Types, ctx)
	case *soltype.KeyofType:
		// Two inert `keyof` residuals are equal when they carry the same exactness over
		// equal operands. This compares the residual structurally without reducing it,
		// matching how the operator flows through the solver untouched in M9 PR1a.
		b, ok := b.(*soltype.KeyofType)
		return ok && a.Exact == b.Exact && equalTypeWith(a.Operand, b.Operand, ctx)
	case *soltype.IndexType:
		// Two inert `T[K]` residuals are equal when they carry the same exactness over equal
		// targets and equal indices, compared structurally without reducing the access, the
		// two-child analogue of the KeyofType arm.
		b, ok := b.(*soltype.IndexType)
		return ok && a.Exact == b.Exact && equalTypeWith(a.Target, b.Target, ctx) && equalTypeWith(a.Index, b.Index, ctx)
	case *soltype.TypeofType:
		// Two `typeof` queries are equal when they name the same value and resolve to equal
		// types, compared without unwrapping — the query flows through the solver untouched.
		b, ok := b.(*soltype.TypeofType)
		return ok && a.Ident == b.Ident && equalTypeWith(a.Ty, b.Ty, ctx)
	case *soltype.RestSpreadType:
		// Two `...P` spread elements are equal when their operands are, compared structurally
		// without reducing. The enclosing TupleType arm compares element lists positionally, so a
		// spread element reaches here in place, the spread twin of the plain element comparison.
		b, ok := b.(*soltype.RestSpreadType)
		return ok && equalTypeWith(a.Operand, b.Operand, ctx)
	}
	return false
}

// equalObjElem reports structural equality of two object members. It returns false
// on a kind mismatch, so the caller matches a-members to b-members by name and kind
// together. Each kind compares its own payload:
//
//   - a property compares its type, optionality, and readonly flag;
//   - a method compares its static flag and each overload signature positionally,
//     since arm order is significant;
//   - a getter compares its return type;
//   - a setter compares its parameter type;
//   - a constructor compares its call signature.
//
// It panics on an unknown element kind, matching AsProperty.
func equalObjElem(a, b soltype.ObjTypeElem, ctx *alphaCtx) bool {
	switch a := a.(type) {
	case *soltype.PropertyElem:
		b, ok := b.(*soltype.PropertyElem)
		return ok && a.Optional == b.Optional && a.Readonly == b.Readonly && equalTypeWith(a.Type, b.Type, ctx)
	case *soltype.MethodElem:
		b, ok := b.(*soltype.MethodElem)
		if !ok || a.Static != b.Static || len(a.Signatures) != len(b.Signatures) {
			return false
		}
		for i := range a.Signatures {
			if !equalTypeWith(a.Signatures[i], b.Signatures[i], ctx) {
				return false
			}
		}
		return true
	case *soltype.GetterElem:
		b, ok := b.(*soltype.GetterElem)
		return ok && equalSelfParam(a.SelfParam, b.SelfParam, ctx) && equalTypeWith(a.Type, b.Type, ctx)
	case *soltype.SetterElem:
		b, ok := b.(*soltype.SetterElem)
		return ok && equalSelfParam(a.SelfParam, b.SelfParam, ctx) && equalTypeWith(a.Param, b.Param, ctx)
	case *soltype.ConstructorElem:
		b, ok := b.(*soltype.ConstructorElem)
		return ok && equalTypeWith(a.Fn, b.Fn, ctx)
	}
	panic(fmt.Sprintf("equalObjElem: unhandled ObjTypeElem %T", a))
}

// equalSelfParam reports whether two receivers match. Presence must agree, so an
// instance member never equals a static one, and when both are present their receiver
// types must be equal. It is shared by the method, getter, and setter comparisons.
func equalSelfParam(a, b *soltype.FuncParam, ctx *alphaCtx) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return equalTypeWith(a.Type, b.Type, ctx)
}

// ltEqualWith reports lifetime equality for equalTypeWith's RefType arm. Under a nil
// pairing it is ltEqual, keying a LifetimeVar by pointer. Under a pairing it binds the
// two variables together through the bijection, so two borrows minted in independent
// schemes match when they occupy corresponding positions and a variable reused on one
// side must be reused the same way on the other. A borrow whose lifetime is not a
// variable — 'static, an owned-mutable nil, an anonymous marker, or a union — falls back
// to ltEqual's by-value rule in both modes.
func ltEqualWith(a, b soltype.Lifetime, p *ltPairing) bool {
	if p == nil {
		return ltEqual(a, b)
	}
	av, aok := a.(*soltype.LifetimeVar)
	bv, bok := b.(*soltype.LifetimeVar)
	if !aok && !bok {
		return ltEqual(a, b)
	}
	if !aok || !bok {
		return false // a variable never pairs with a non-variable lifetime
	}
	return p.pair(av, bv)
}

// ltEqual reports lifetime equality for equalType's RefType arm (D2). Each lifetime
// form has its own equality rule:
//   - A LifetimeVar is identity-keyed. Two are equal only when they are the same
//     pointer.
//   - 'static is a value, so any two StaticLifetimes are equal.
//   - A nil lifetime is an owned-mutable borrow. It equals only another nil.
//
// This mirrors how the rest of equalType keys variables by pointer and primitives by
// value.
func ltEqual(a, b soltype.Lifetime) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if soltype.IsStaticLifetime(a) || soltype.IsStaticLifetime(b) {
		return soltype.IsStaticLifetime(a) && soltype.IsStaticLifetime(b)
	}
	// AnonLifetime is a display marker for an elided borrow. All instances denote
	// the same "no name" marker, so they compare equal by value, mirroring 'static.
	if soltype.IsAnonLifetime(a) || soltype.IsAnonLifetime(b) {
		return soltype.IsAnonLifetime(a) && soltype.IsAnonLifetime(b)
	}
	return a == b
}

func equalTypeSliceWith(a, b []soltype.Type, ctx *alphaCtx) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalTypeWith(a[i], b[i], ctx) {
			return false
		}
	}
	return true
}

// sameLifetimeSlice compares two lifetime slices positionally under the alpha context,
// so a class's lifetime arguments and a lifetime parameter's outlives bounds compare up
// to the lifetime-parameter bijection. It is the lifetime-sort twin of
// equalTypeSliceWith.
func sameLifetimeSlice(a, b []soltype.Lifetime, ctx *alphaCtx) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !ctx.sameLifetime(a[i], b[i]) {
			return false
		}
	}
	return true
}

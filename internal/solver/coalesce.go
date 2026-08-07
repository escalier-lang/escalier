package solver

import (
	"fmt"
	"slices"
	"sort"
	"strconv"

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

// muBinders turns a cycle in the bound graph into a finite μ-knot. Both coalescers embed it, so
// that rule lives in one place.
//
// A binder is OPEN while the walk is inlining the variable it stands for. Re-entering that variable
// is the cycle the knot represents. ref hands back a μ-variable so the body names itself there
// rather than degenerating to `never` or `unknown`, and tie wraps the finished body in a
// RecursiveType.
// A binder that no cycle reached names nothing, so tie returns such a body unwrapped.
type muBinders struct {
	// open holds the binder for each variable currently on the walk's path. Both coalescers guard
	// re-entry with a seen-set keyed by the variable alone, so a variable has at most one open
	// binder and the polarity it was entered at rides on the binder itself.
	open map[*soltype.TypeVarType]*muBinder
	// count numbers the μ-variables this walk has minted, so nested knots draw distinct ids and
	// distinct display names.
	count int
}

// muBinder is one open μ-binder: the polarity its origin variable was entered at, and the
// μ-variable a cycle back to that origin renders as.
type muBinder struct {
	pol soltype.Polarity
	// v is minted on the first cycle that reaches this binder and stays nil otherwise, which is
	// both the signal tie reads and the reason the ids count knots rather than path entries.
	v *soltype.RecursiveVarType
}

// push opens the binder for a variable the walk is about to inline at pol. The caller pops it on
// the way back up, so a binder is open exactly while its origin sits on the path.
func (m *muBinders) push(v *soltype.TypeVarType, pol soltype.Polarity) *muBinder {
	if m.open == nil {
		m.open = map[*soltype.TypeVarType]*muBinder{}
	}
	b := &muBinder{pol: pol}
	m.open[v] = b
	return b
}

func (m *muBinders) pop(v *soltype.TypeVarType) {
	delete(m.open, v)
}

// ref returns the μ-variable to stand in for a cycle back to v at pol, or nil when no knot can be
// tied there. The polarity must match the one v's open binder was pushed at. A cycle reached at the
// opposite polarity would name a body built from v's other bound direction, its lower bounds where
// the position needs its uppers. The caller falls back to the polarity identity there.
func (m *muBinders) ref(v *soltype.TypeVarType, pol soltype.Polarity) soltype.Type {
	b, ok := m.open[v]
	if !ok || b.pol != pol {
		return nil
	}
	if b.v == nil {
		b.v = &soltype.RecursiveVarType{ID: m.count, Name: muBinderName(m.count)}
		m.count++
	}
	return b.v
}

// tie closes a knot over body, or returns body unchanged when no cycle named this binder. A body
// that is nothing but the bare μ-variable pins no type at all, since `μX0.X0` has no unfolding that
// mentions a type. It collapses to the polarity identity instead, the value an empty-bounds
// position takes.
func (b *muBinder) tie(pol soltype.Polarity, body soltype.Type) soltype.Type {
	if b.v == nil {
		return body
	}
	if body == soltype.Type(b.v) {
		return emptyOf(pol)
	}
	return &soltype.RecursiveType{Binder: b.v, Body: body}
}

// muBinderName is the display name for the i-th μ-variable a walk mints: X0, X1, …, so a recursive
// type renders `μX0.{next: X0}`. It follows typeParamName's generated-name convention on a
// different letter, so a knot's binder never reads as a type parameter.
func muBinderName(i int) string {
	return "X" + strconv.Itoa(i)
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
	// mu holds the μ-binder open for each variable on that same path, so a cycle back to one
	// renders as a reference the finished body closes over as a knot.
	mu muBinders
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
	// Re-entering a variable already on the current path is a recursive position: the bound graph
	// loops back on itself with no concrete type breaking the cycle. It renders as a reference to
	// the μ-binder minted for that variable, so the finished body closes over the loop as a knot
	// and `fn f() { return {next: f()} }` reads `fn () -> {next: μX0.{next: X0}}`. A cycle the
	// binder cannot cover, meaning one reached at the opposite polarity, collapses to the polarity
	// identity instead. See muBinders.ref. That is the same value the position takes when its
	// bounds are empty.
	if c.seen.Contains(v) {
		if ref := c.mu.ref(v, pol); ref != nil {
			return soltype.EnterResult{Type: ref, SkipChildren: true}
		}
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	c.seen.Add(v)
	defer c.seen.Remove(v) // path-scoped: pop on the way back up (panic-safe)
	binder := c.mu.push(v, pol)
	defer c.mu.pop(v) // path-scoped like `seen`, so a binder is open only while v is on the path
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
	inlined := widenVar(v, pol, combine(pol, dedup(bounds), v.Open))
	return soltype.EnterResult{Type: binder.tie(pol, inlined), SkipChildren: true}
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
	// mu is coalescer.mu's twin, keyed by the co-occurrence representative the seen-set uses.
	mu muBinders
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
		// A cycle back to a variable already on the path. A retained type parameter keeps its name.
		// The quantifier already binds it, so the name IS the recursive reference and no separate
		// binder is needed. An inlined variable has no name to come back to, so the cycle renders
		// as a reference to the μ-binder minted for it and the finished body closes over the loop
		// as a knot. A cycle the binder cannot cover, meaning one reached at the opposite polarity,
		// collapses to the polarity identity. See muBinders.ref.
		if retain {
			return soltype.EnterResult{Type: rep, SkipChildren: true}
		}
		if ref := c.mu.ref(rep, pol); ref != nil {
			return soltype.EnterResult{Type: ref, SkipChildren: true}
		}
		return soltype.EnterResult{Type: emptyOf(pol), SkipChildren: true}
	}
	c.seen.Add(rep)
	defer c.seen.Remove(rep) // path-scoped: pop on the way back up (panic-safe)
	binder := c.mu.push(rep, pol)
	defer c.mu.pop(rep) // path-scoped like `seen`, so a binder is open only while rep is on the path

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
	inlined := widenVar(v, pol, combine(pol, dedup(parts), v.Open))
	return soltype.EnterResult{Type: binder.tie(pol, inlined), SkipChildren: true}
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
//
// It passes the printer no source names, so a class value binding's parameters render
// positionally. renderSchemeWith renders them under the names the declaration wrote.
func renderScheme(s TypeScheme) string {
	return renderSchemeWith(s, nil)
}

// renderSchemeWith renders a scheme like renderScheme, under the source names of the type
// parameters the scheme's declaration wrote. A class, alias, or enum keeps those parameters
// in the Context registry rather than in the type, so declaredFor looks them up from the
// scheme's display type. It is called with that type once it is derived, since the lookup
// reads the class or alias the type names. Pass nil to name nothing from the source, which
// is right for every function: a FuncType carries its own parameters and names them itself.
func renderSchemeWith(s TypeScheme, declaredFor func(soltype.Type) []*soltype.TypeParam) string {
	var t soltype.Type
	// A MonoScheme coalesces to a var-free type, so every free variable left in it is a
	// parameter. A PolyScheme names only the variables generalization quantified — those with
	// Level > sc.Level, the exact retention criterion coalesceScheme uses — so a variable that
	// escaped coalescing renders as the raw t{ID} debug form instead of being disguised as a
	// spurious type parameter.
	var isParam func(*soltype.TypeVarType) bool
	switch sc := s.(type) {
	case *MonoScheme:
		t = coalesce(sc.Ty, soltype.Positive)
		isParam = func(*soltype.TypeVarType) bool { return true }
	case *PolyScheme:
		t = sc.display()
		isParam = func(v *soltype.TypeVarType) bool { return v.Level > sc.Level }
	default:
		panic(fmt.Sprintf("renderSchemeWith: unknown TypeScheme %T", s))
	}
	var declared []*soltype.TypeParam
	if declaredFor != nil {
		declared = declaredFor(t)
	}
	return soltype.PrintAsSchemeWith(t, isParam, displayLtBounds(t, soltype.Positive), declared)
}

// renderValueBinding renders a value binding's scheme under the source type-parameter names
// of the declaration it came from, so `class Node<T> {value: T}` binds a value that renders
// `<T> {new (value: T) -> Node<T>}`.
func (c *checker) renderValueBinding(s TypeScheme) string {
	return renderSchemeWith(s, c.declaredTypeParams)
}

// declaredTypeParams returns the type parameters written by the declaration a display type
// stands for, or nil when it stands for none. A class, alias, or enum keeps its parameters
// in the Context registry rather than in the type, so the printer cannot reach them from the
// type alone and a caller reads them from here.
//
// A class VALUE binding is an object holding the constructor, whose return is the class's own
// handle, so the class is reached through that return: `class Node<T>` binds the value
// `{new (value: T) -> Node<T>}` and its parameters are found under Node.
func (c *checker) declaredTypeParams(t soltype.Type) []*soltype.TypeParam {
	switch t := t.(type) {
	case *soltype.ClassType:
		if def, ok := c.ctx.classDef(t.Name); ok {
			return paramsForArgs(def.TypeParams, t.TypeArgs)
		}
	case *soltype.AliasType:
		if def, ok := c.ctx.aliasDef(t.Name); ok {
			return paramsForArgs(def.TypeParams, t.TypeArgs)
		}
	case *soltype.ObjectType:
		for _, elem := range t.Elems {
			ctor, isCtor := elem.(*soltype.ConstructorElem)
			if !isCtor {
				continue
			}
			if cls, isClass := ctor.Fn.Ret.(*soltype.ClassType); isClass {
				return c.declaredTypeParams(cls)
			}
			return nil
		}
	}
	return nil
}

// paramsForArgs returns tps with each parameter's variable replaced by the variable a
// reference passes in that argument position. The printer names a variable by identity, and a
// binding that holds an instantiated copy of a class value carries freshened variables rather
// than the declaration's own. Reading the name off the argument position covers both:
// `class Box<T> {value: T}` and a later `val Alias = Box` each render
// `<T> {new (value: T) -> Box<T>}`, where matching on the declaration's variable would leave
// the second positional.
//
// A position holding something other than a variable keeps the parameter as declared, so
// `Box<5>` names nothing. So does a reference whose argument count does not match the
// declaration's, which covers the argument-less handle an alias binds: `type Alias<T> = {v: T}`
// renders its body under the declared variables.
func paramsForArgs(tps []*soltype.TypeParam, args []soltype.Type) []*soltype.TypeParam {
	if len(args) != len(tps) {
		return tps
	}
	out := make([]*soltype.TypeParam, len(tps))
	for i, tp := range tps {
		v, isVar := args[i].(*soltype.TypeVarType)
		if !isVar || v == tp.Var {
			out[i] = tp
			continue
		}
		cp := *tp
		cp.Var = v
		out[i] = &cp
	}
	return out
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

// bijection is a partial one-to-one correspondence between the bound names of two types
// being compared, keyed by each name's integer id. It is the storage every kind of binder
// in alphaCtx shares. aToB maps a left-side id to the right-side id it is bound to and
// bToA maps back, so a lookup in either direction is a map hit. Both maps start nil and
// are allocated by the first bind, which is what keeps a comparison over types carrying no
// binder of that kind allocation-free.
type bijection struct {
	aToB map[int]int
	bToA map[int]int
}

// bind records that left-side name a corresponds to right-side name b, allocating both
// directions on the first call. Binding a name that already has a partner drops the old
// pairing from both maps first, so neither name is left with a reciprocal entry pointing
// at a partner it no longer has. Without that removal a stale bToA entry would make decide
// reject a later pair by rule 2, reporting a mismatch against a binding that had already
// been replaced.
//
// The binder kinds alphaCtx pairs today never rebind a name to a different partner, since
// each function boundary, mapped type, and μ-knot draws ids unique to the comparison. The
// removal keeps the one-to-one invariant true for a kind that does.
func (p *bijection) bind(a, b int) {
	if p.aToB == nil {
		p.aToB = map[int]int{}
		p.bToA = map[int]int{}
	}
	if oldB, ok := p.aToB[a]; ok {
		delete(p.bToA, oldB)
	}
	if oldA, ok := p.bToA[b]; ok {
		delete(p.aToB, oldA)
	}
	p.aToB[a] = b
	p.bToA[b] = a
}

// decide reports the bijection's verdict on whether a and b denote corresponding names. It
// applies the two rules every binder kind repeats, and reports decided false for the case
// only the caller can settle:
//
//  1. a is bound, so b must be the partner it was bound to.
//  2. a is unbound but b is bound, so b already corresponds to some other left-side name
//     and this pair is a mismatch.
//  3. neither is bound, so decided is false and the caller applies its own rule.
func (p *bijection) decide(a, b int) (same, decided bool) {
	if j, ok := p.aToB[a]; ok {
		return j == b, true
	}
	if _, ok := p.bToA[b]; ok {
		return false, true
	}
	return false, false
}

// sameByID is decide with id equality as the caller's rule. A pair neither side has bound
// matches when the two names drew the same id. That case arises when a reference is
// compared away from the construct that binds it. Every binder kind uses this rule except
// the type-parameter one, which falls back to pointer identity instead.
func (p *bijection) sameByID(a, b int) bool {
	if same, decided := p.decide(a, b); decided {
		return same
	}
	return a == b
}

// partner returns the right-side name a is bound to, if a is bound at all.
func (p *bijection) partner(a int) (int, bool) {
	j, ok := p.aToB[a]
	return j, ok
}

// boundRight reports whether right-side name b is bound to some left-side name.
func (p *bijection) boundRight(b int) bool {
	_, ok := p.bToA[b]
	return ok
}

// alphaCtx carries the bijections equalTypeWith uses to compare two types up to a
// consistent renaming of their bound variables. Each field pairs one kind of bound name.
// lt pairs borrow lifetimes for alphaEqualTypes. A nil lt selects pointer-identity
// lifetime equality, the within-coalesce default equalType uses.
type alphaCtx struct {
	lt *ltPairing
	// tv pairs the positional type parameters of two generic FuncTypes, bound at each
	// function boundary so a parameter's identity is its position rather than its
	// variable id.
	tv bijection
	// ltp pairs the positional lifetime parameters of two generic FuncTypes, the
	// lifetime-sort twin of tv. It is bound at each function boundary so a lifetime
	// parameter's identity is its position rather than its variable id. It is separate
	// from lt. lt discovers a bijection over borrow lifetimes for alphaEqualTypes, while
	// ltp records declared bindings over a function's own quantified lifetime params.
	ltp bijection
	// `infer` pairs the `infer` declarations of two conditionals, bound when a pair of
	// declaring clauses meets rather than at a declaration list, since a clause sits at
	// an arbitrary depth inside an Extends operand.
	infer bijection
	// `key` pairs the key bindings of two mapped types, bound when a pair of mapped types
	// meets, the way bindTypeParams binds a function's parameters.
	key bijection
	// `rec` pairs the binders of two μ-knots, bound when a pair of knots meets, the way
	// bindMappedKeys pairs two mapped types' keys. This bijection is what makes two knots
	// equal up to a consistent renaming of their binders.
	rec bijection
}

// bindTypeParams pairs two generic FuncTypes' type parameters positionally, so every
// later occurrence of one side's parameter must match the other's bound partner. The
// bindings persist for the rest of the walk. Type-variable ids are unique across the
// comparison, so a parameter bound here is never confused with one from another
// function.
func (ctx *alphaCtx) bindTypeParams(as, bs []*soltype.TypeParam) {
	for i := range as {
		ctx.tv.bind(as[i].Var.ID, bs[i].Var.ID)
	}
}

// sameTypeVar reports whether two type variables are equal under the type-parameter
// bijection. A variable bound as one side's parameter must map to the other's partner. A
// variable bound on neither side is a shared or free variable and compares by pointer
// identity, the rule the rest of equalType keys variables by. Pointer identity is why this
// is the one kind that cannot use sameByID.
func (ctx *alphaCtx) sameTypeVar(a, b *soltype.TypeVarType) bool {
	if same, decided := ctx.tv.decide(a.ID, b.ID); decided {
		return same
	}
	return a == b
}

// sameInferDecl reports whether two `infer` nodes stand for corresponding declarations. Meeting a
// pair of declaring clauses records the correspondence, the way bindTypeParams records a function's
// parameters, and every later reference is checked against it. Two conditionals resolved separately
// from the same source therefore compare equal even though each drew its own declaration id, which
// is what lets `fn (k: if T : [infer U] { U } else { X }) -> if T : [infer U] { U } else { X }`
// accept `return k`. A reference reached with no correspondence recorded — a branch compared on its
// own, away from the conditional that declares its names — falls back to id equality, the rule
// sameTypeVar applies to a variable bound on neither side.
func (ctx *alphaCtx) sameInferDecl(a, b *soltype.InferType) bool {
	if same, decided := ctx.infer.decide(a.ID, b.ID); decided {
		return same
	}
	if !a.Binder {
		return a.ID == b.ID
	}
	ctx.infer.bind(a.ID, b.ID)
	return true
}

// bindMappedKeys pairs the key bindings of two mapped types, so every later reference to one side's
// key must match the other's partner. Two mapped types resolved separately from the same source
// therefore compare equal even though each drew its own binding id, the same reason
// sameInferDecl pairs two conditionals' capture declarations.
func (ctx *alphaCtx) bindMappedKeys(a, b *soltype.MappedKeyType) {
	ctx.key.bind(a.ID, b.ID)
}

// sameMappedKey reports whether two mapped-type key references stand for corresponding bindings
// under the pairing bindMappedKeys recorded. A reference may be reached with no pairing recorded,
// which happens when a value position is compared on its own, away from the mapped type that binds
// its key. That case falls back to id equality, the rule sameTypeVar applies to a variable bound on
// neither side.
func (ctx *alphaCtx) sameMappedKey(a, b *soltype.MappedKeyType) bool {
	return ctx.key.sameByID(a.ID, b.ID)
}

// bindRecursiveBinders pairs the binders of two μ-knots, so every later reference to one side's
// binder must match the other's partner. Two knots built by separate coalescing walks therefore
// compare equal even though each numbered its binders from zero, the same reason bindMappedKeys
// pairs two mapped types' key bindings.
func (ctx *alphaCtx) bindRecursiveBinders(a, b *soltype.RecursiveVarType) {
	ctx.rec.bind(a.ID, b.ID)
}

// sameRecursiveVar reports whether two μ-variables stand for corresponding binders under the
// pairing the enclosing pair of knots recorded. A reference may be reached with no pairing, which
// happens when a knot's body is compared on its own, away from the knot that binds it. That case
// falls back to id equality, the rule sameMappedKey applies to an unpaired binding.
func (ctx *alphaCtx) sameRecursiveVar(a, b *soltype.RecursiveVarType) bool {
	return ctx.rec.sameByID(a.ID, b.ID)
}

// bindLifetimeParams pairs two generic FuncTypes' lifetime parameters positionally, the
// lifetime-sort twin of bindTypeParams, so every later occurrence of one side's lifetime
// parameter must match the other's bound partner. The bindings persist for the rest of
// the walk. Lifetime-variable ids are unique across the comparison, so a parameter bound
// here is never confused with one from another function.
func (ctx *alphaCtx) bindLifetimeParams(as, bs []*soltype.LifetimeParam) {
	for i := range as {
		ctx.ltp.bind(as[i].Var.ID, bs[i].Var.ID)
	}
}

// sameLifetime reports lifetime equality under the alpha context. A lifetime bound as
// one side's parameter must map to the other's partner through the lifetime-parameter
// bijection, so two borrowing methods differing only in lifetime-variable id compare
// equal. A lifetime bound on neither side falls back to ltEqualWith, which keys a
// LifetimeVar by pointer under a nil borrow pairing and by first-appearance index under
// one.
//
// This reads the ltp bijection through partner and boundRight rather than through decide,
// because either side may be a lifetime with no variable id at all. 'static, an
// owned-mutable nil, an anonymous marker, and a union are all such lifetimes. A
// non-variable side is unbound by construction, and a bound variable facing one is a
// mismatch.
func (ctx *alphaCtx) sameLifetime(a, b soltype.Lifetime) bool {
	av, aok := a.(*soltype.LifetimeVar)
	bv, bok := b.(*soltype.LifetimeVar)
	if aok {
		if j, ok := ctx.ltp.partner(av.ID); ok {
			return bok && j == bv.ID
		}
	}
	if bok {
		if ctx.ltp.boundRight(bv.ID) {
			return false // b is a bound lifetime parameter, a is not — mismatch
		}
	}
	return ltEqualWith(a, b, ctx.lt)
}

// ltPairing is the bijection alphaEqualTypes discovers between the lifetime variables of
// two types compared up to renaming. equalTypeWith fills it in as it walks: the first
// time it matches a borrow on each side it binds their lifetime variables together, and
// every later occurrence must respect that binding. The embedded bijection holds the two
// directions, keyed by lifetime-variable ID. aVars and bVars list the bound variables in
// binding order, so index i on each side names one paired lifetime.
// sameOutlivesUnderPairing compares the outlives relation over those pairs. The pairing
// sits on alphaCtx.lt. A nil lt selects pointer-identity lifetime equality, the
// within-coalesce default equalType uses.
type ltPairing struct {
	bijection
	aVars []*soltype.LifetimeVar
	bVars []*soltype.LifetimeVar
}

// pair records or rechecks that a and b are the same lifetime under the bijection. A
// variable already bound to a different partner fails, which is what keeps a borrow that
// shares one lifetime across two positions from matching a side that uses two distinct
// lifetimes there. Because the walk matches structure in the same order on both sides,
// binding a and b together the first time they are matched pairs corresponding lifetimes
// regardless of the order the two types happen to list their object properties. A pair
// neither side has bound is recorded, and appending to aVars and bVars keeps the binding
// order sameOutlivesUnderPairing walks.
func (p *ltPairing) pair(a, b *soltype.LifetimeVar) bool {
	if same, decided := p.decide(a.ID, b.ID); decided {
		return same
	}
	p.bind(a.ID, b.ID)
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
		if !equalTypeWith(a.Ret, b.Ret, ctx) {
			return false
		}
		// ThrowsOrNever reads both sides through the nil-is-never collapse, so a function
		// written with no clause equals one written `throws never`.
		return equalTypeWith(a.ThrowsOrNever(), b.ThrowsOrNever(), ctx)
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
		// An unreduced residual object compares position for position rather than by member name.
		// A spread-carrying object needs that because element order is significant, since a later
		// `...B` overrides an earlier key. A mapped-carrying object needs it because its member names
		// no field, so the name-keyed path below has no key to match on.
		if soltype.HasResidualElem(a.Elems) || soltype.HasResidualElem(b.Elems) {
			for i := range a.Elems {
				if !equalObjElem(a.Elems[i], b.Elems[i], ctx) {
					return false
				}
			}
			return true
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
	case *soltype.ArrayType:
		b, ok := b.(*soltype.ArrayType)
		return ok && equalTypeWith(a.Elem, b.Elem, ctx)
	case *soltype.PromiseType:
		b, ok := b.(*soltype.PromiseType)
		// ErrOrNever reads both sides through the nil-is-never collapse, so two promises
		// differing only in whether the rejection slot was written compare equal here.
		return ok && equalTypeWith(a.Inner, b.Inner, ctx) &&
			equalTypeWith(a.ErrOrNever(), b.ErrOrNever(), ctx)
	case *soltype.GeneratorType:
		b, ok := b.(*soltype.GeneratorType)
		// ThrowsOrNever reads both sides through the nil-is-never collapse, so two
		// generators differing only in whether the raise slot was written compare equal.
		return ok && a.Async == b.Async &&
			equalTypeWith(a.Yield, b.Yield, ctx) &&
			equalTypeWith(a.Ret, b.Ret, ctx) &&
			equalTypeWith(a.Next, b.Next, ctx) &&
			equalTypeWith(a.ThrowsOrNever(), b.ThrowsOrNever(), ctx)
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
		return ok && a.Inexact == b.Inexact && equalTypeWith(a.Operand, b.Operand, ctx)
	case *soltype.IndexType:
		// Two inert `T[K]` residuals are equal when they carry the same exactness over equal
		// targets and equal indices, compared structurally without reducing the access, the
		// two-child analogue of the KeyofType arm.
		b, ok := b.(*soltype.IndexType)
		return ok && a.Inexact == b.Inexact && equalTypeWith(a.Target, b.Target, ctx) && equalTypeWith(a.Index, b.Index, ctx)
	case *soltype.TypeofType:
		// Two `typeof` queries are equal when they name the same value and resolve to equal
		// types, compared without unwrapping — the query flows through the solver untouched.
		b, ok := b.(*soltype.TypeofType)
		return ok && a.Ident == b.Ident && equalTypeWith(a.Ty, b.Ty, ctx)
	case *soltype.CondType:
		// Two inert conditional residuals are equal when all four operands are equal, compared
		// structurally without deciding either branch, the four-child analogue of the KeyofType arm.
		// Distribute must match too: it changes what a union Check reduces to, so a distributive
		// conditional is a different type from an otherwise identical non-distributive one.
		b, ok := b.(*soltype.CondType)
		return ok && a.Distribute == b.Distribute &&
			equalTypeWith(a.Check, b.Check, ctx) && equalTypeWith(a.Extends, b.Extends, ctx) &&
			equalTypeWith(a.Then, b.Then, ctx) && equalTypeWith(a.Else, b.Else, ctx)
	case *soltype.InferType:
		// Two `infer` nodes are equal when they play the same role — a declaring clause never equals
		// a reference to the name it declares — and their declarations correspond under the pairing
		// sameInferDecl builds. The names themselves are not compared, the same way two functions'
		// type parameters compare by position rather than by name.
		b, ok := b.(*soltype.InferType)
		return ok && a.Binder == b.Binder && ctx.sameInferDecl(a, b)
	case *soltype.MappedKeyType:
		// Two mapped-type key references are equal when their bindings correspond under the pairing
		// the enclosing pair of mapped types recorded.
		b, ok := b.(*soltype.MappedKeyType)
		return ok && ctx.sameMappedKey(a, b)
	case *soltype.RecursiveType:
		// Two μ-knots are equal when their bodies are equal under a pairing of their binders, so
		// `μX0.{next: X0}` equals `μX1.{next: X1}`. That is the alpha-equivalence a generic
		// function's positional type parameters compare under. Pairing the binders before
		// descending is what lets each side's references to its own binder match the other's.
		b, ok := b.(*soltype.RecursiveType)
		if !ok {
			return false
		}
		ctx.bindRecursiveBinders(a.Binder, b.Binder)
		return equalTypeWith(a.Body, b.Body, ctx)
	case *soltype.RecursiveVarType:
		// Two references to a μ-binder are equal when their binders correspond under the pairing
		// the enclosing pair of knots recorded. The binder names are not compared, the same way two
		// functions' type parameters compare by position rather than by name.
		b, ok := b.(*soltype.RecursiveVarType)
		return ok && ctx.sameRecursiveVar(a, b)
	case *soltype.RestSpreadType:
		// Two `...P` spread elements are equal when their operands are, compared structurally
		// without reducing. The enclosing TupleType arm compares element lists positionally, so a
		// spread element reaches here in place, the spread twin of the plain element comparison.
		b, ok := b.(*soltype.RestSpreadType)
		return ok && equalTypeWith(a.Operand, b.Operand, ctx)
	case *soltype.TemplateLitType:
		// Two template literals are equal when their fixed segments match and their interpolations
		// are equal position-for-position, compared structurally without reducing the template.
		b, ok := b.(*soltype.TemplateLitType)
		return ok && slices.Equal(a.Quasis, b.Quasis) && equalTypeSliceWith(a.Interps, b.Interps, ctx)
	case *soltype.StringIntrinsicType:
		// Two string-intrinsic residuals are equal when they name the same operator over equal
		// operands, compared structurally without reducing, the single-child analogue of the
		// KeyofType arm.
		b, ok := b.(*soltype.StringIntrinsicType)
		return ok && a.Kind == b.Kind && equalTypeWith(a.Operand, b.Operand, ctx)
	case *soltype.ExactnessType:
		// Two exactness residuals are equal when they name the same operator over equal operands,
		// compared structurally without reducing, the single-child analogue of the KeyofType arm.
		b, ok := b.(*soltype.ExactnessType)
		return ok && a.Kind == b.Kind && equalTypeWith(a.Operand, b.Operand, ctx)
	}
	return false
}

// equalOptionalType compares two operands a node carries only when the source wrote them. Two
// absent operands are equal, one absent and one present are not, and two present operands compare
// structurally.
func equalOptionalType(a, b soltype.Type, ctx *alphaCtx) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return equalTypeWith(a, b, ctx)
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
	case *soltype.MappedElem:
		// Two inert mapped members are equal when every operand is equal and the modifiers match,
		// compared structurally without emitting a field. Pairing the key bindings first lets each
		// side's references to its own key match the other's. The enclosing objects compare their
		// inexact markers, so this member carries none.
		b, ok := b.(*soltype.MappedElem)
		if !ok || a.Optional != b.Optional || a.Readonly != b.Readonly {
			return false
		}
		ctx.bindMappedKeys(a.Key, b.Key)
		return equalTypeWith(a.Keys, b.Keys, ctx) && equalTypeWith(a.Value, b.Value, ctx) &&
			equalOptionalType(a.Name, b.Name, ctx) && equalOptionalType(a.Check, b.Check, ctx) &&
			equalOptionalType(a.Extends, b.Extends, ctx)
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
		// ThrowsOrNever reads both sides through the nil-is-never collapse, so two getters
		// differing only in whether the clause was written compare equal.
		b, ok := b.(*soltype.GetterElem)
		return ok && equalSelfParam(a.SelfParam, b.SelfParam, ctx) && equalTypeWith(a.Type, b.Type, ctx) &&
			equalTypeWith(a.ThrowsOrNever(), b.ThrowsOrNever(), ctx)
	case *soltype.SetterElem:
		b, ok := b.(*soltype.SetterElem)
		return ok && equalSelfParam(a.SelfParam, b.SelfParam, ctx) && equalTypeWith(a.Param, b.Param, ctx) &&
			equalTypeWith(a.ThrowsOrNever(), b.ThrowsOrNever(), ctx)
	case *soltype.ConstructorElem:
		b, ok := b.(*soltype.ConstructorElem)
		return ok && equalTypeWith(a.Fn, b.Fn, ctx)
	case *soltype.SpreadElem:
		b, ok := b.(*soltype.SpreadElem)
		return ok && equalTypeWith(a.Type, b.Type, ctx)
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

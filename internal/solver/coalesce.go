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
	c := t.Accept(&coalescer{seen: set.NewSet[*soltype.TypeVarType]()}, pol)
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
}

func (c *coalescer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		// Atom or structural node — let Accept rebuild it from coalesced children
		// (including an overload-arm Union/Intersection input — the scoped lattice exception; see overloadIntersection).
		return soltype.EnterResult{}
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
	c := t.Accept(&schemeCoalescer{
		simp:     simplifyScheme(t, genLevel),
		genLevel: genLevel,
		seen:     set.NewSet[*soltype.TypeVarType](),
	}, soltype.Positive)
	// A scheme display is always coalesced from the Positive root.
	return coalesceLifetimes(c, soltype.Positive) // D4: resolve borrow lifetimes to their display form
}

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
	seen     set.Set[*soltype.TypeVarType]
}

func (c *schemeCoalescer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		// Atom or structural node — let Accept rebuild it from coalesced children
		// (including an overload-arm Union/Intersection input — the scoped lattice exception; see overloadIntersection).
		return soltype.EnterResult{}
	}
	rep := c.simp.rep(v)
	retain := rep.Level > c.genLevel && c.simp.mergedOcc[rep.ID].both()
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
		return soltype.PrintAsScheme(coalesce(sc.Ty, soltype.Positive))
	case *PolyScheme:
		return soltype.PrintAsSchemeWith(sc.display(), func(v *soltype.TypeVarType) bool {
			return v.Level > sc.Level
		})
	}
	panic(fmt.Sprintf("renderScheme: unknown TypeScheme %T", s))
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
	if len(parts) == 1 {
		return parts[0]
	}
	if pol == soltype.Positive {
		return &soltype.UnionType{Types: parts}
	}
	return &soltype.IntersectionType{Types: parts}
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
			types[pe.Name] = appendDistinct(types[pe.Name], pe.Type)
		}
	}
	sort.Strings(order)
	elems := make([]soltype.ObjTypeElem, len(order))
	for i, name := range order {
		ts := types[name]
		ty := ts[0]
		if len(ts) > 1 {
			ty = &soltype.IntersectionType{Types: ts}
		}
		elems[i] = &soltype.PropertyElem{Name: name, Type: ty, Optional: optional[name]}
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
func equalType(a, b soltype.Type) bool {
	switch a := a.(type) {
	case *soltype.TypeVarType:
		b, ok := b.(*soltype.TypeVarType)
		return ok && a == b
	case *soltype.PrimType:
		b, ok := b.(*soltype.PrimType)
		return ok && a.Prim == b.Prim
	case *soltype.LitType:
		b, ok := b.(*soltype.LitType)
		return ok && a.Equal(b)
	case *soltype.Void:
		_, ok := b.(*soltype.Void)
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
		if !ok || len(a.Params) != len(b.Params) || a.Inexact != b.Inexact {
			return false
		}
		for i := range a.Params {
			if a.Params[i].Optional != b.Params[i].Optional || a.Params[i].Rest != b.Params[i].Rest || !equalType(a.Params[i].Type, b.Params[i].Type) {
				return false
			}
		}
		return equalType(a.Ret, b.Ret)
	case *soltype.TupleType:
		b, ok := b.(*soltype.TupleType)
		// Inexact flags must be equal — an open tuple never equals a closed one,
		// mirroring the ObjectType/FuncType arms' Inexact discriminator.
		if !ok || a.Inexact != b.Inexact || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !equalType(a.Elems[i], b.Elems[i]) {
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
		// Objects are equal up to property order, so match each property by name
		// via ObjectType.Prop rather than by position. The solver dedups property
		// names on construction, so names are unique. Equal lengths plus every
		// a-property matching a b-property by name, type, and optionality is then a
		// full structural match. Optional mirrors the FuncType arm's param-Optional
		// discriminator.
		for _, ae := range a.Elems {
			ap := soltype.AsProperty(ae)
			bp, ok := b.Prop(ap.Name)
			if !ok || ap.Optional != bp.Optional || !equalType(ap.Type, bp.Type) {
				return false
			}
		}
		return true
	case *soltype.PromiseType:
		b, ok := b.(*soltype.PromiseType)
		return ok && equalType(a.Inner, b.Inner)
	case *soltype.RefType:
		b, ok := b.(*soltype.RefType)
		// Mut must match — a mutable borrow never equals an immutable one — and the
		// lifetimes must match: D2 mints borrow lifetimes, so two borrows differing only
		// in lifetime are NOT equal. Without the Lt check, dedup would collapse them and
		// silently drop a lifetime the solver computed. ltEqual compares a LifetimeVar by
		// pointer and 'static by value.
		return ok && a.Mut == b.Mut && ltEqual(a.Lt, b.Lt) && equalType(a.Inner, b.Inner)
	case *soltype.UnionType:
		b, ok := b.(*soltype.UnionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	case *soltype.IntersectionType:
		b, ok := b.(*soltype.IntersectionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	}
	return false
}

// ltEqual reports lifetime equality for equalType's RefType arm (D2). Each lifetime
// form has its own equality rule:
//   - A LifetimeVar is identity-keyed. Two are equal only when they are the same
//     pointer.
//   - 'static is a value, so any two StaticLifetimes are equal.
//   - A nil lifetime is an owned-mutable borrow. It equals only another nil.
//   - A LifetimeUnion is the union form a join variable coalesces to in D3, such as
//     'a | 'b. Two are equal when they hold the same members, pairwise equal in
//     order. This lets two RefTypes carrying the same coalesced union dedup.
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
	if ua, ok := a.(*soltype.LifetimeUnion); ok {
		ub, ok := b.(*soltype.LifetimeUnion)
		if !ok || len(ua.Lifetimes) != len(ub.Lifetimes) {
			return false
		}
		for i := range ua.Lifetimes {
			if !ltEqual(ua.Lifetimes[i], ub.Lifetimes[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func equalTypeSlice(a, b []soltype.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalType(a[i], b[i]) {
			return false
		}
	}
	return true
}

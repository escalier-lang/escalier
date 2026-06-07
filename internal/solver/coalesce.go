package solver

import (
	"fmt"

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
	return coalesceRec(t, pol, set.NewSet[*soltype.TypeVarType]())
}

// coalesceRec is coalesce's worker, threading the path-scoped set of type
// variables currently being inlined. seen holds only the variables on the
// *current* recursion path (added on entry, removed on exit), so a variable
// reused in independent branches — e.g. the identity function's shared param
// (negative) and return (positive) var — is unaffected; only re-entering a
// variable already on the path is a genuine cycle.
func coalesceRec(t soltype.Type, pol soltype.Polarity, seen set.Set[*soltype.TypeVarType]) soltype.Type {
	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.Void,
		*soltype.NeverType, *soltype.UnknownType:
		return t // atoms pass through
	case *soltype.FuncType:
		params := make([]*soltype.FuncParam, len(t.Params))
		for i, p := range t.Params {
			// Params are contravariant, so flip polarity. Exact/Optional are surface
			// markers, not bound-carrying, so they ride through coalescing unchanged.
			params[i] = &soltype.FuncParam{Pattern: p.Pattern, Type: coalesceRec(p.Type, pol.Flip(), seen), Optional: p.Optional}
		}
		return &soltype.FuncType{Params: params, Ret: coalesceRec(t.Ret, pol, seen), Exact: t.Exact} // covariant return
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = coalesceRec(e, pol, seen) // covariant elements
		}
		return &soltype.TupleType{Elems: elems}
	case *soltype.RecordType:
		fields := make([]*soltype.RecordField, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = &soltype.RecordField{Name: f.Name, Type: coalesceRec(f.Type, pol, seen)} // covariant fields
		}
		return &soltype.RecordType{Fields: fields}
	case *soltype.TypeVarType:
		// Re-entering a variable already on the current path is an ungrounded
		// recursive position (no concrete type breaks the cycle). It collapses to
		// the polarity identity — the same value the position degenerates to when
		// its bounds are empty — which keeps the inline walk total. A precise
		// μ-bound rendering of such recursion is M3.
		if seen.Contains(t) {
			return emptyOf(pol)
		}
		seen.Add(t)
		defer seen.Remove(t)
		// Uniform inline: drop the variable, keep only its (recursively
		// coalesced) bounds in the current polarity.
		bounds := make([]soltype.Type, 0, len(t.BoundsAt(pol)))
		for _, b := range t.BoundsAt(pol) {
			bounds = append(bounds, coalesceRec(b, pol, seen))
		}
		if len(bounds) == 0 {
			return emptyOf(pol)
		}
		return combine(pol, dedup(bounds))
	}
	panic(fmt.Sprintf("coalesce: unhandled %T", t))
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

// analyzeOccurrences walks the bound graph rooted at t (the same traversal
// coalesce uses: covariant children at pol, contravariant func params flipped,
// each variable's BoundsAt(pol)) recording into occ which polarities every
// variable occurs in. A variable appearing in both a covariant and a
// contravariant position — the identity function's shared param/return var — comes
// out as occPos|occNeg; a result/indirection variable that only ever flows
// outward comes out as occPos alone. coalesceScheme then retains the former as a
// quantified type parameter and inlines the latter to its bound.
func analyzeOccurrences(t soltype.Type, pol soltype.Polarity, occ map[*soltype.TypeVarType]occPolarity, seen set.Set[occKey]) {
	switch t := t.(type) {
	case *soltype.FuncType:
		for _, p := range t.Params {
			analyzeOccurrences(p.Type, pol.Flip(), occ, seen) // params contravariant
		}
		analyzeOccurrences(t.Ret, pol, occ, seen) // covariant return
	case *soltype.TupleType:
		for _, e := range t.Elems {
			analyzeOccurrences(e, pol, occ, seen)
		}
	case *soltype.RecordType:
		for _, f := range t.Fields {
			analyzeOccurrences(f.Type, pol, occ, seen)
		}
	case *soltype.TypeVarType:
		if pol == soltype.Positive {
			occ[t] |= occPos
		} else {
			occ[t] |= occNeg
		}
		k := occKey{t, pol}
		if seen.Contains(k) {
			return
		}
		seen.Add(k)
		for _, b := range t.BoundsAt(pol) {
			analyzeOccurrences(b, pol, occ, seen)
		}
	}
}

// coalesceScheme coalesces a generalized scheme's RAW body for DISPLAY, retaining
// the variables that are genuine type parameters as named references while
// inlining the rest to their bounds. A variable is retained iff it is
// quantifiable (Level > genLevel) AND occurs in both polarities (single-polarity
// elimination); every other variable is inlined exactly as coalesce does — so on
// a body with no both-polarity quantifiable variable this reduces, node for node,
// to coalesce(t, Positive), keeping every monomorphic render unchanged.
//
// This is the minimum needed to render a generalized scheme without the
// always-pre-bound SCC indirection variable (a positive-only var) corrupting the
// output. The remaining simplification — CO-OCCURRENCE merging of *distinct*
// variables that always appear together, and the `parameter-only var ⇒ unknown`
// case for variables genuinely used in one polarity — is PR2's; PR1 retains a
// type parameter only where it is literally the same variable across positions,
// so renders stay non-compact until then.
func coalesceScheme(t soltype.Type, genLevel int) soltype.Type {
	occ := map[*soltype.TypeVarType]occPolarity{}
	analyzeOccurrences(t, soltype.Positive, occ, set.NewSet[occKey]())
	return coalesceSchemeRec(t, soltype.Positive, genLevel, occ, set.NewSet[*soltype.TypeVarType]())
}

func coalesceSchemeRec(
	t soltype.Type, pol soltype.Polarity, genLevel int,
	occ map[*soltype.TypeVarType]occPolarity, seen set.Set[*soltype.TypeVarType],
) soltype.Type {
	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.Void,
		*soltype.NeverType, *soltype.UnknownType:
		return t
	case *soltype.FuncType:
		params := make([]*soltype.FuncParam, len(t.Params))
		for i, p := range t.Params {
			params[i] = &soltype.FuncParam{Pattern: p.Pattern, Type: coalesceSchemeRec(p.Type, pol.Flip(), genLevel, occ, seen), Optional: p.Optional}
		}
		return &soltype.FuncType{Params: params, Ret: coalesceSchemeRec(t.Ret, pol, genLevel, occ, seen), Exact: t.Exact}
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = coalesceSchemeRec(e, pol, genLevel, occ, seen)
		}
		return &soltype.TupleType{Elems: elems}
	case *soltype.RecordType:
		fields := make([]*soltype.RecordField, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = &soltype.RecordField{Name: f.Name, Type: coalesceSchemeRec(f.Type, pol, genLevel, occ, seen)}
		}
		return &soltype.RecordType{Fields: fields}
	case *soltype.TypeVarType:
		retain := t.Level > genLevel && occ[t].both()
		if seen.Contains(t) {
			// A cycle back to a variable already on the path: a retained type
			// parameter keeps its name (a rough μ-reference, refined in M3's precise
			// μ-rendering), an inlined variable collapses to the polarity identity.
			if retain {
				return t
			}
			return emptyOf(pol)
		}
		seen.Add(t)
		defer seen.Remove(t)
		bounds := make([]soltype.Type, 0, len(t.BoundsAt(pol)))
		for _, b := range t.BoundsAt(pol) {
			bounds = append(bounds, coalesceSchemeRec(b, pol, genLevel, occ, seen))
		}
		if retain {
			// Keep the variable as a named type parameter, merged with its coalesced
			// bounds (variable first, so it names earliest): v | bounds in positive
			// position, v & bounds in negative. Empty bounds ⇒ just the variable.
			return combine(pol, dedup(append([]soltype.Type{t}, bounds...)))
		}
		if len(bounds) == 0 {
			return emptyOf(pol)
		}
		return combine(pol, dedup(bounds))
	}
	panic(fmt.Sprintf("coalesceScheme: unhandled %T", t))
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
		return coalesceScheme(sc.Body, sc.Level)
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
		return soltype.PrintAsSchemeWith(coalesceScheme(sc.Body, sc.Level), func(v *soltype.TypeVarType) bool {
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
func combine(pol soltype.Polarity, parts []soltype.Type) soltype.Type {
	if len(parts) == 1 {
		return parts[0]
	}
	if pol == soltype.Positive {
		return &soltype.UnionType{Types: parts}
	}
	return &soltype.IntersectionType{Types: parts}
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
	case *soltype.FuncType:
		b, ok := b.(*soltype.FuncType)
		if !ok || len(a.Params) != len(b.Params) || a.Exact != b.Exact {
			return false
		}
		for i := range a.Params {
			if a.Params[i].Optional != b.Params[i].Optional || !equalType(a.Params[i].Type, b.Params[i].Type) {
				return false
			}
		}
		return equalType(a.Ret, b.Ret)
	case *soltype.TupleType:
		b, ok := b.(*soltype.TupleType)
		if !ok || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !equalType(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case *soltype.RecordType:
		b, ok := b.(*soltype.RecordType)
		if !ok || len(a.Fields) != len(b.Fields) {
			return false
		}
		// Records are equal up to field order — match by name (RecordType.Field),
		// not position. Well-formed records have unique field names (the solver
		// dedups on construction), so equal lengths plus every a-field matching a
		// b-field by name is a full structural match.
		for _, f := range a.Fields {
			bt, ok := b.Field(f.Name)
			if !ok || !equalType(f.Type, bt) {
				return false
			}
		}
		return true
	case *soltype.UnionType:
		b, ok := b.(*soltype.UnionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	case *soltype.IntersectionType:
		b, ok := b.(*soltype.IntersectionType)
		return ok && equalTypeSlice(a.Types, b.Types)
	}
	return false
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

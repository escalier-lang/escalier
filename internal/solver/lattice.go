package solver

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// newUnion and newIntersection are the M6 PR1 smart constructors. They are
// the single mint path for UnionType and IntersectionType. Every site that
// builds a lattice node routes through them, so coalesced output, the
// shared-property meet in mergeObjectGroup, PR2's annotation input, and PR6's
// permissive borrow join all produce well-formed, canonical, deduplicated
// lattice nodes without re-spelling the rules.
//
// Normalization splits into a Context-free core and a Context-gated
// subsumed-member elimination step. The core covers flatten, lattice
// identities, ErrorType elision, dedup, canonical order, and collapse. Every
// caller needs it. Subsumption runs only when the caller passes a Context,
// because the subtype test it uses calls c.constrain under a probe. combine
// and mergeObjectGroup pass nil. resolveTypeAnn from PR2 and joinBorrows from
// PR6 pass their checker's Context.
//
// Canonical member order keeps equalType positional and cheap. Two unions
// over the same members hold them in the same order, so equalTypeSlice
// already returns true. Canonical order also makes rendering deterministic,
// so `number | string` and `string | number` print identically, and it lets
// the canonical type serve as a stable key for caching.
func newUnion(c *Context, parts []soltype.Type, inexact bool) soltype.Type {
	flat, inexact := flattenUnion(parts, inexact)
	pruned, hadError := pruneUnion(flat)
	pruned = dedup(pruned)
	if c != nil {
		pruned = subsumeMembers(c, pruned, unionDrops)
	}
	sortTypes(pruned)
	return collapseUnion(pruned, inexact, hadError)
}

// newIntersection is the meet twin of newUnion. An IntersectionType carries
// no exactness flag, since exactness is a property of the result rather than
// the meet, so the API is one argument shorter.
func newIntersection(c *Context, parts []soltype.Type) soltype.Type {
	flat := flattenIntersection(parts)
	pruned, hadError := pruneIntersection(flat)
	pruned = dedup(pruned)
	if c != nil {
		pruned = subsumeMembers(c, pruned, intersectionDrops)
	}
	sortTypes(pruned)
	return collapseIntersection(pruned, hadError)
}

// flattenUnion splices nested UnionType members into the outer member list
// and carries an inner inexact flag out to the caller. The splice is
// recursive, so a UnionType whose members include another UnionType is fully
// unwrapped in one pass. An inexact nested member at any depth makes the
// outer union inexact, since `... | (A | ...)` collapses to `A | ...`. When
// no member nests, the input slice is reused, so the common case pays no
// allocation.
//
// Recursion matters when a caller hands flatten an unnormalized member, such
// as a raw `&UnionType{Types: [...]}` constructed in a test or rebuilt by a
// visitor that bypasses newUnion. The smart constructor's own output is
// always flat, so a chain of normal newUnion calls would never trigger the
// recursive case, but the recursion keeps the flatness invariant true for
// every input.
func flattenUnion(parts []soltype.Type, inexact bool) ([]soltype.Type, bool) {
	if !anyUnion(parts) {
		return parts, inexact
	}
	flat := make([]soltype.Type, 0, len(parts))
	var splice func(p soltype.Type)
	splice = func(p soltype.Type) {
		u, ok := p.(*soltype.UnionType)
		if !ok {
			flat = append(flat, p)
			return
		}
		if u.Inexact {
			inexact = true
		}
		for _, m := range u.Types {
			splice(m)
		}
	}
	for _, p := range parts {
		splice(p)
	}
	return flat, inexact
}

// flattenIntersection is the meet twin of flattenUnion. The splice is
// recursive for the same reason: a raw or visitor-rebuilt IntersectionType
// whose members include another IntersectionType is fully unwrapped in one
// pass. There is no exactness flag to carry.
func flattenIntersection(parts []soltype.Type) []soltype.Type {
	if !anyIntersection(parts) {
		return parts
	}
	flat := make([]soltype.Type, 0, len(parts))
	var splice func(p soltype.Type)
	splice = func(p soltype.Type) {
		i, ok := p.(*soltype.IntersectionType)
		if !ok {
			flat = append(flat, p)
			return
		}
		for _, m := range i.Types {
			splice(m)
		}
	}
	for _, p := range parts {
		splice(p)
	}
	return flat
}

func anyUnion(parts []soltype.Type) bool {
	for _, p := range parts {
		if _, ok := p.(*soltype.UnionType); ok {
			return true
		}
	}
	return false
}

func anyIntersection(parts []soltype.Type) bool {
	for _, p := range parts {
		if _, ok := p.(*soltype.IntersectionType); ok {
			return true
		}
	}
	return false
}

// pruneUnion drops the union's lattice identity never, which is ⊥, and
// elides ErrorType. ErrorType is the join identity and the absorbing recovery
// sentinel. It is dropped unless every other member was also dropped, in
// which case the collapse step keeps a single ErrorType as the sole
// survivor. The hadError return signals that case to the collapse step.
//
// Reuses the input slice when nothing was dropped.
func pruneUnion(parts []soltype.Type) ([]soltype.Type, bool) {
	hadError := false
	drop := func(p soltype.Type) bool {
		if _, isNever := p.(*soltype.NeverType); isNever {
			return true
		}
		if _, isError := p.(*soltype.ErrorType); isError {
			hadError = true
			return true
		}
		return false
	}
	return filterDropped(parts, drop), hadError
}

// pruneIntersection is the meet twin of pruneUnion. It drops unknown, the
// identity of &, and elides ErrorType under the same sole-survivor rule.
func pruneIntersection(parts []soltype.Type) ([]soltype.Type, bool) {
	hadError := false
	drop := func(p soltype.Type) bool {
		if _, isUnknown := p.(*soltype.UnknownType); isUnknown {
			return true
		}
		if _, isError := p.(*soltype.ErrorType); isError {
			hadError = true
			return true
		}
		return false
	}
	return filterDropped(parts, drop), hadError
}

// filterDropped returns parts with every element the drop callback flagged
// removed, preserving order. It reuses the input slice when nothing was
// dropped, so the common case pays no allocation.
func filterDropped(parts []soltype.Type, drop func(soltype.Type) bool) []soltype.Type {
	firstDrop := -1
	for i, p := range parts {
		if drop(p) {
			firstDrop = i
			break
		}
	}
	if firstDrop < 0 {
		return parts
	}
	out := make([]soltype.Type, 0, len(parts)-1)
	out = append(out, parts[:firstDrop]...)
	for _, p := range parts[firstDrop+1:] {
		if !drop(p) {
			out = append(out, p)
		}
	}
	return out
}

func collapseUnion(pruned []soltype.Type, inexact, hadError bool) soltype.Type {
	if len(pruned) == 0 {
		if hadError {
			return &soltype.ErrorType{}
		}
		// Empty union ⇒ never, the identity of |. An inexact-but-empty union is
		// still never, since the inexactness flag has no carrier without
		// members. A caller that needs a union whose only content is the open
		// tail should write unknown directly.
		return &soltype.NeverType{}
	}
	if len(pruned) == 1 && !inexact {
		// A single exact-union member collapses to that member. An inexact
		// single-member union keeps its wrapper, since the `... | T` tail
		// makes it strictly weaker than the bare T.
		return pruned[0]
	}
	return &soltype.UnionType{Types: pruned, Inexact: inexact}
}

func collapseIntersection(pruned []soltype.Type, hadError bool) soltype.Type {
	if len(pruned) == 0 {
		if hadError {
			return &soltype.ErrorType{}
		}
		// Empty intersection ⇒ unknown, the identity of &.
		return &soltype.UnknownType{}
	}
	if len(pruned) == 1 {
		return pruned[0]
	}
	return &soltype.IntersectionType{Types: pruned}
}

// subsumeMembers drops every member m for which drops(m, sibling) returns
// true for some kept sibling. The drops callback names the direction of the
// subtype check. A union drops m when m <: sibling, since the sibling is
// wider. An intersection drops m when sibling <: m, since the sibling is
// narrower and already constrains the value below m.
//
// The pass is concrete-gated. A member that still carries a free type
// variable is left alone, since trialling subtype against an inference
// variable could pin it speculatively. The trial uses a discard-only probe
// so a successful trial leaves no bound mutation behind.
//
// When two members mutually subsume, the survivor must be deterministic. The
// pass pre-sorts the input by compareType, so the iteration order is
// canonical and newUnion([A, B]) and newUnion([B, A]) drop the same member
// when A and B subsume each other but differ structurally. That is the
// canonicalization contract the M6 plan asserts.
func subsumeMembers(c *Context, parts []soltype.Type, drops func(c *Context, m, sibling soltype.Type) bool) []soltype.Type {
	if len(parts) < 2 {
		return parts
	}
	parts = append([]soltype.Type(nil), parts...)
	sortTypes(parts)
	hasVar := make([]bool, len(parts))
	for i, p := range parts {
		hasVar[i] = soltype.HasTypeVar(p)
	}
	dropped := set.NewSet[int]()
	for i, a := range parts {
		if dropped.Contains(i) || hasVar[i] {
			continue
		}
		for j, b := range parts {
			if i == j || dropped.Contains(j) || hasVar[j] {
				continue
			}
			if drops(c, a, b) {
				dropped.Add(i)
				break
			}
		}
	}
	if dropped.Len() == 0 {
		return parts
	}
	out := make([]soltype.Type, 0, len(parts)-dropped.Len())
	for i, p := range parts {
		if !dropped.Contains(i) {
			out = append(out, p)
		}
	}
	return out
}

// unionDrops returns true when union member m should be dropped because the
// sibling subsumes it. The check is m <: sibling.
func unionDrops(c *Context, m, sibling soltype.Type) bool {
	return subtypeUnderProbe(c, m, sibling)
}

// intersectionDrops returns true when intersection member m should be
// dropped because the sibling subsumes it from below. The check is sibling
// <: m. The sibling is narrower, so it already implies m, and m is the wider
// one to discard.
func intersectionDrops(c *Context, m, sibling soltype.Type) bool {
	return subtypeUnderProbe(c, sibling, m)
}

// subtypeUnderProbe trials sub <: super under a discard-only probe and
// reports whether the trial succeeded. A successful trial means subsumption
// holds. The probe is always Discarded so the bound mutations it would
// otherwise leave behind never become visible.
//
// The probe push/pop runs directly on *Context. openProbe and closeProbe are
// the checker-level path and additionally snapshot c.errs, which subsumption
// does not need. (*Context).constrain returns its errors through the return
// value and never appends to checker state.
func subtypeUnderProbe(c *Context, sub, super soltype.Type) bool {
	p := newProbe(c.probe)
	c.probe = p
	errs := c.constrain(sub, super, set.NewSet[constraintKey](), false)
	c.probe = p.parent
	p.Discard()
	return len(errs) == 0
}

// sortTypes orders parts in place under compareType. The sort is stable so a
// list already in canonical order keeps its pointer order across passes,
// which keeps a downstream rebuild identity-preserving when possible.
func sortTypes(parts []soltype.Type) {
	sort.SliceStable(parts, func(i, j int) bool {
		return compareType(parts[i], parts[j]) < 0
	})
}

// compareType is the deterministic total order canonical member order is
// built on. It is consistent with equalType: two equalType-equal types
// compare equal. The ordering ranks by a concrete-kind tag and tie-breaks
// structurally, so distinct types that print identically still compare
// strictly. Two RefTypes whose only difference is a pair of distinct unnamed
// LifetimeVars is the case that motivates avoiding a printer-string
// tie-break: under top-level Print they render the same string but they are
// not equalType-equal, and a string fallback would call them equal. The
// comparator never calls the printer.
func compareType(a, b soltype.Type) int {
	if equalType(a, b) {
		return 0
	}
	ka, kb := typeKindOrder(a), typeKindOrder(b)
	if ka != kb {
		return ka - kb
	}
	return compareSameKind(a, b)
}

// compareSameKind is the per-kind structural tie-breaker. The payload-free
// kinds NeverType, UnknownType, ErrorType, and Void cannot reach this
// function, because equalType already returned true for any two of them above.
// The remaining kinds compare by their fields in declaration order, with
// nested types recursing through compareType.
func compareSameKind(a, b soltype.Type) int {
	switch a := a.(type) {
	case *soltype.PrimType:
		b := b.(*soltype.PrimType)
		return int(a.Prim) - int(b.Prim)
	case *soltype.LitType:
		return compareLit(a.Lit, b.(*soltype.LitType).Lit)
	case *soltype.TypeVarType:
		b := b.(*soltype.TypeVarType)
		return a.ID - b.ID
	case *soltype.RefType:
		b := b.(*soltype.RefType)
		if a.Mut != b.Mut {
			return boolOrder(a.Mut) - boolOrder(b.Mut)
		}
		if c := compareLifetime(a.Lt, b.Lt); c != 0 {
			return c
		}
		return compareType(a.Inner, b.Inner)
	case *soltype.TupleType:
		b := b.(*soltype.TupleType)
		if a.Inexact != b.Inexact {
			return boolOrder(a.Inexact) - boolOrder(b.Inexact)
		}
		if c := len(a.Elems) - len(b.Elems); c != 0 {
			return c
		}
		return compareTypeSlice(a.Elems, b.Elems)
	case *soltype.ObjectType:
		b := b.(*soltype.ObjectType)
		if a.Inexact != b.Inexact {
			return boolOrder(a.Inexact) - boolOrder(b.Inexact)
		}
		return compareObjectFields(a, b)
	case *soltype.PromiseType:
		return compareType(a.Inner, b.(*soltype.PromiseType).Inner)
	case *soltype.FuncType:
		b := b.(*soltype.FuncType)
		if a.Inexact != b.Inexact {
			return boolOrder(a.Inexact) - boolOrder(b.Inexact)
		}
		if c := len(a.Params) - len(b.Params); c != 0 {
			return c
		}
		for i := range a.Params {
			if c := compareFuncParam(a.Params[i], b.Params[i]); c != 0 {
				return c
			}
		}
		return compareType(a.Ret, b.Ret)
	case *soltype.UnionType:
		b := b.(*soltype.UnionType)
		if a.Inexact != b.Inexact {
			return boolOrder(a.Inexact) - boolOrder(b.Inexact)
		}
		if c := len(a.Types) - len(b.Types); c != 0 {
			return c
		}
		return compareTypeSlice(a.Types, b.Types)
	case *soltype.IntersectionType:
		b := b.(*soltype.IntersectionType)
		if c := len(a.Types) - len(b.Types); c != 0 {
			return c
		}
		return compareTypeSlice(a.Types, b.Types)
	}
	return 0
}

func compareTypeSlice(a, b []soltype.Type) int {
	for i := range a {
		if c := compareType(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// compareFuncParam orders parameters by surface marker first. Rest comes
// first, then Optional, then the parameter type. Pattern is intentionally
// ignored, since an inferred or unnamed pattern would otherwise discriminate
// two type-equal parameters.
func compareFuncParam(a, b *soltype.FuncParam) int {
	if a.Rest != b.Rest {
		return boolOrder(a.Rest) - boolOrder(b.Rest)
	}
	if a.Optional != b.Optional {
		return boolOrder(a.Optional) - boolOrder(b.Optional)
	}
	return compareType(a.Type, b.Type)
}

// compareObjectFields orders two objects by property name, then by each
// property's optional flag and type. Property order in the slice is
// presentation only, so the comparator walks both objects in name-sorted
// order.
func compareObjectFields(a, b *soltype.ObjectType) int {
	if c := len(a.Elems) - len(b.Elems); c != 0 {
		return c
	}
	an := sortedPropertyNames(a)
	bn := sortedPropertyNames(b)
	for i := range an {
		if c := stringCompare(an[i], bn[i]); c != 0 {
			return c
		}
		ap, _ := a.Prop(an[i])
		bp, _ := b.Prop(bn[i])
		if ap.Optional != bp.Optional {
			return boolOrder(ap.Optional) - boolOrder(bp.Optional)
		}
		if c := compareType(ap.Type, bp.Type); c != 0 {
			return c
		}
	}
	return 0
}

func sortedPropertyNames(o *soltype.ObjectType) []string {
	names := make([]string, len(o.Elems))
	for i, e := range o.Elems {
		names[i] = soltype.AsProperty(e).Name
	}
	sort.Strings(names)
	return names
}

func stringCompare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareLit orders literal values within the LitType kind. NumLit comes
// first, then StrLit, then BoolLit; within a sort, values compare by their
// own ordering.
func compareLit(a, b soltype.Lit) int {
	ka, kb := litKindOrder(a), litKindOrder(b)
	if ka != kb {
		return ka - kb
	}
	switch a := a.(type) {
	case *soltype.NumLit:
		b := b.(*soltype.NumLit)
		switch {
		case a.Value < b.Value:
			return -1
		case a.Value > b.Value:
			return 1
		}
		return 0
	case *soltype.StrLit:
		return stringCompare(a.Value, b.(*soltype.StrLit).Value)
	case *soltype.BoolLit:
		b := b.(*soltype.BoolLit)
		return boolOrder(a.Value) - boolOrder(b.Value)
	}
	return 0
}

func litKindOrder(l soltype.Lit) int {
	switch l.(type) {
	case *soltype.NumLit:
		return 0
	case *soltype.StrLit:
		return 1
	case *soltype.BoolLit:
		return 2
	}
	return 3
}

// compareLifetime orders the lifetime forms a RefType.Lt can take. A nil
// slot, which marks an owned value, sorts first. Then 'static. Then
// LifetimeVar, ordered by ID. Then LifetimeUnion, ordered by length first
// and members second.
func compareLifetime(a, b soltype.Lifetime) int {
	ka, kb := lifetimeKindOrder(a), lifetimeKindOrder(b)
	if ka != kb {
		return ka - kb
	}
	switch a := a.(type) {
	case nil:
		return 0
	case *soltype.StaticLifetime:
		return 0
	case *soltype.LifetimeVar:
		b := b.(*soltype.LifetimeVar)
		return a.ID - b.ID
	case *soltype.LifetimeUnion:
		b := b.(*soltype.LifetimeUnion)
		if c := len(a.Lifetimes) - len(b.Lifetimes); c != 0 {
			return c
		}
		for i := range a.Lifetimes {
			if c := compareLifetime(a.Lifetimes[i], b.Lifetimes[i]); c != 0 {
				return c
			}
		}
		return 0
	}
	return 0
}

func lifetimeKindOrder(lt soltype.Lifetime) int {
	switch lt.(type) {
	case nil:
		return 0
	case *soltype.StaticLifetime:
		return 1
	case *soltype.LifetimeVar:
		return 2
	case *soltype.LifetimeUnion:
		return 3
	}
	return 4
}

// typeKindOrder ranks a soltype concrete kind for compareType. The lattice
// bounds and the error sentinel come first, then TypeVarType so quantified
// parameters lead in a rendered union, then primitives and literals, then
// the remaining structural kinds, then the lattice forms, and finally
// NullType and Void. A union renders its parameters and data members
// before its absence markers. NullType precedes Void by convention, so
// `T0 | number | null | void` is the canonical render.
func typeKindOrder(t soltype.Type) int {
	switch t.(type) {
	case *soltype.NeverType:
		return 0
	case *soltype.UnknownType:
		return 1
	case *soltype.ErrorType:
		return 2
	case *soltype.TypeVarType:
		return 3
	case *soltype.PrimType:
		return 4
	case *soltype.LitType:
		return 5
	case *soltype.RefType:
		return 6
	case *soltype.TupleType:
		return 7
	case *soltype.ObjectType:
		return 8
	case *soltype.PromiseType:
		return 9
	case *soltype.FuncType:
		return 10
	case *soltype.UnionType:
		return 11
	case *soltype.IntersectionType:
		return 12
	case *soltype.NullType:
		return 13
	case *soltype.Void:
		return 14
	}
	return 15
}

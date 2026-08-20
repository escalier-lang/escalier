package solver

import (
	"slices"
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
// over the same members hold them in the same order, so equalTypeSliceWith
// already returns true. Canonical order also makes rendering deterministic,
// so `number | string` and `string | number` print identically, and it lets
// the canonical type serve as a stable key for caching.
func newUnion(c *Context, parts []soltype.Type, inexact bool) soltype.Type {
	return newUnionWithTail(c, parts, unionTail{open: inexact})
}

// newBoundedUnion mints `A | ... : R`, an inexact union whose tail members are drawn from
// bound. A nil bound leaves the tail unbounded, which is what newUnion's inexact form
// mints, so a caller that computes a bound and may come up empty needs no branch.
func newBoundedUnion(c *Context, parts []soltype.Type, bound soltype.Type) soltype.Type {
	return newUnionWithTail(c, parts, unionTail{open: true, bound: bound})
}

// newUnionWithTail is the core newUnion and newBoundedUnion share. It runs the same
// normalization for either and differs only in the tail it carries through.
func newUnionWithTail(c *Context, parts []soltype.Type, tail unionTail) soltype.Type {
	flat, tail := flattenUnion(parts, tail)
	pruned, hadError := pruneUnion(flat)
	pruned = dedup(pruned)
	if c != nil {
		pruned = subsumeMembers(c, pruned, unionDrops)
	}
	sortTypes(pruned)
	return collapseUnion(pruned, tail, hadError)
}

// unionTail is a union's open-tail marker as it moves through the mint pipeline. open
// says a trailing `...` is present. bound names the type the tail's unnamed members are
// drawn from, and a nil bound leaves them unbounded, so the tail admits every value.
// The zero value is the exact union's tail, which is no tail at all.
type unionTail struct {
	open  bool
	bound soltype.Type
}

// tailOf reads a union's tail back out, the inverse of what collapseUnion writes.
func tailOf(u *soltype.UnionType) unionTail {
	return unionTail{open: u.Inexact, bound: u.TailBound}
}

// merge folds another tail into t, the rule flattenUnion applies when it splices a
// nested union's members into the outer list. Two bounded tails join their bounds,
// since `... : string | ... : number` may hold a member of either. An unbounded tail absorbs
// a bounded one, since nothing says what the unbounded one holds.
//
// keyofUnion is the other caller, and the join is wider than that reduction wants, since
// a key set of a union is a meet. The doc comment there records the gap.
func (t unionTail) merge(other unionTail) unionTail {
	if !other.open {
		return t
	}
	if !t.open {
		return other
	}
	if t.bound == nil || other.bound == nil {
		return unionTail{open: true}
	}
	return unionTail{open: true, bound: newUnion(nil, []soltype.Type{t.bound, other.bound}, false)}
}

// newIntersection is the meet twin of newUnion. An IntersectionType carries
// no exactness flag, since exactness is a property of the result rather than
// the meet, so the API is one argument shorter.
//
// TODO(#927): collapse an uninhabited intersection to never. Two members that no value satisfies
// together, such as `number & string`, survive the pipeline below, so a conditional capture that
// meets contravariant candidates renders `number & string` where TypeScript renders `never`.
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

// newNegation is the complement twin of newUnion and newIntersection, the single
// mint path for a NegationType in coalesced output. Three operands have a
// complement the surface type set already names. It returns that name for them and
// wraps everything else.
//
//   - `¬never` is `unknown`, since `never` admits no value and `unknown` admits
//     every one.
//   - `¬unknown` is `never`, the same identity read the other way.
//   - `¬(A | B | ...)` is `never`. A union whose open tail carries no bound is the
//     top of the subtype lattice, since that tail accepts every value, so its
//     complement admits none. TestOpenUnionIsTopForSubtypingOnly pins that reading.
//     A bounded tail is not top and gets no fold. `¬("a" | ... : string)` rejects every
//     string and admits `5`, so it wraps like any other operand.
//   - `¬¬T` is T, since complementing twice returns the original set.
//
// It does NOT push the complement through a union or an intersection. `¬(A | B)`
// is a faithful render of what the bound says, and De Morgan's law would only
// trade one node for two.
//
// normal.go's negate is the solver-internal twin. That one wraps unconditionally,
// because the parts it negates are already known to be neither lattice bound nor
// complement.
func newNegation(inner soltype.Type) soltype.Type {
	switch inner := inner.(type) {
	case *soltype.NeverType:
		return &soltype.UnknownType{}
	case *soltype.UnknownType:
		return &soltype.NeverType{}
	case *soltype.UnionType:
		if inner.Inexact && inner.TailBound == nil {
			return &soltype.NeverType{}
		}
	case *soltype.NegationType:
		return inner.Inner
	}
	return &soltype.NegationType{Inner: inner}
}

// flattenUnion splices nested UnionType members into the outer member list
// and carries an inner tail out to the caller. The splice is recursive, so a
// UnionType whose members include another UnionType is fully unwrapped in one
// pass. An inexact nested member at any depth makes the outer union inexact,
// since `... | (A | ...)` collapses to `A | ...`, and unionTail.merge decides
// what bounds the result. When no member nests, the input slice is reused, so
// the common case pays no allocation.
//
// Recursion matters when a caller hands flatten an unnormalized member, such
// as a raw `&UnionType{Types: [...]}` constructed in a test or rebuilt by a
// visitor that bypasses newUnion. The smart constructor's own output is
// always flat, so a chain of normal newUnion calls would never trigger the
// recursive case, but the recursion keeps the flatness invariant true for
// every input.
func flattenUnion(parts []soltype.Type, tail unionTail) ([]soltype.Type, unionTail) {
	if !anyUnion(parts) {
		return parts, tail
	}
	flat := make([]soltype.Type, 0, len(parts))
	var splice func(p soltype.Type)
	splice = func(p soltype.Type) {
		u, ok := p.(*soltype.UnionType)
		if !ok {
			flat = append(flat, p)
			return
		}
		tail = tail.merge(tailOf(u))
		for _, m := range u.Types {
			splice(m)
		}
	}
	for _, p := range parts {
		splice(p)
	}
	return flat, tail
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

// narrowTailBound removes from a tail's bound every value the union's named members already
// admit, and returns nil once nothing is left. A nil result drops the tail, so `"y" | ... : "y"`
// is exactly `"y"`. A partial overlap narrows instead: `1 | ... : (1 | 2)` becomes `1 | ... : 2`,
// which reads tighter and denotes the same thing, since both admit `1` alone or `1 | 2`.
//
// Equality decides membership rather than a subtype test, so this needs no Context and runs on
// every mint. A bound that is a subtype of the named members by some other route, such as
// `string | ... : "a"`, goes undetected. An inexact union bound is left alone, since its own tail
// says nothing about what it holds, and a `never` bound holds nothing and drops.
//
// An unchanged bound comes back as the same pointer, which is how finalSubsumer tells whether
// the walk changed anything.
func narrowTailBound(pruned []soltype.Type, bound soltype.Type) soltype.Type {
	if bound == nil {
		return nil
	}
	if _, empty := bound.(*soltype.NeverType); empty {
		return nil
	}
	if u, ok := bound.(*soltype.UnionType); ok && !u.Inexact {
		kept := make([]soltype.Type, 0, len(u.Types))
		for _, member := range u.Types {
			if !slices.ContainsFunc(pruned, func(p soltype.Type) bool { return equalType(p, member) }) {
				kept = append(kept, member)
			}
		}
		switch {
		case len(kept) == 0:
			return nil
		case len(kept) == len(u.Types):
			return bound
		}
		// The filtered members carry no tail of their own, so this mint reaches collapseUnion
		// with a nil bound and does not come back here.
		return newUnion(nil, kept, false)
	}
	if slices.ContainsFunc(pruned, func(p soltype.Type) bool { return equalType(p, bound) }) {
		return nil
	}
	return bound
}

func collapseUnion(pruned []soltype.Type, tail unionTail, hadError bool) soltype.Type {
	if tail.bound != nil {
		if tail.bound = narrowTailBound(pruned, tail.bound); tail.bound == nil {
			tail = unionTail{}
		}
	}
	if len(pruned) == 0 {
		// ErrorType absorbs, so a union whose every other member was pruned comes back
		// as the sole surviving error whatever its tail says.
		if hadError {
			return &soltype.ErrorType{}
		}
		if !tail.open || tail.bound == nil {
			// Empty union ⇒ never, the identity of |. An empty union with an unbounded
			// tail is still never, since that tail says nothing a member could stand
			// for. A caller that needs a union whose only content is an unbounded tail
			// should write unknown directly.
			return &soltype.NeverType{}
		}
	}
	if len(pruned) == 1 && !tail.open {
		// A single exact-union member collapses to that member. An inexact
		// single-member union keeps its wrapper, since the `... | T` tail
		// makes it strictly weaker than the bare T.
		return pruned[0]
	}
	// A bounded tail with no named member survives as `... : R`, a union naming no member
	// and drawing every one it has from R. `("a" | ... : string) ∩ ¬"a"` reduces to
	// `... : (string & ¬"a")`, the string keys other than "a", which has no other spelling.
	return &soltype.UnionType{Types: pruned, Inexact: tail.open, TailBound: tail.bound}
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
// A free lifetime variable disqualifies a member the same way. Two borrows
// that differ only in lifetime, such as `&'a mut {x: number}` and
// `&'b mut {x: number}`, mutually subsume by a discardable lifetime
// constraint, so dropping one would silently lose a distinct lifetime the
// signature quantifies over. Gating on the lifetime sort keeps both borrows.
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
	// hasVar[i] is true when member i still carries a free type or lifetime
	// variable, so it is skipped by the concrete gate below.
	hasVar := make([]bool, len(parts))
	for i, p := range parts {
		hasVar[i] = !concreteMember(p)
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

// subtypeHolds reports whether sub <: super, decided speculatively so the answer
// costs no bound. The trial runs under a discard-only probe, so a variable the
// decision reached keeps the bounds it had. Every display-time pass that compares
// one type against another asks through here.
func subtypeHolds(c *Context, sub, super soltype.Type) bool {
	return !hasHardError(c.trialUnderProbe(sub, super))
}

// concreteMember reports whether t carries no free type or lifetime variable. A
// member failing it is left out of every speculative comparison, for the two
// reasons subsumeMembers gives: trialling against an inference variable could pin
// it, and two members differing only in a lifetime variable would compare equal.
func concreteMember(t soltype.Type) bool {
	return !soltype.HasTypeVar(t) && !soltype.HasLifetimeVar(t)
}

// unionDrops returns true when union member m should be dropped because the
// sibling subsumes it. The check is m <: sibling.
func unionDrops(c *Context, m, sibling soltype.Type) bool {
	return subtypeHolds(c, m, sibling)
}

// intersectionDrops returns true when intersection member m should be
// dropped because the sibling subsumes it from below. The check is sibling
// <: m. The sibling is narrower, so it already implies m, and m is the wider
// one to discard.
func intersectionDrops(c *Context, m, sibling soltype.Type) bool {
	return subtypeHolds(c, sibling, m)
}

// subsumeFinal re-mints every UnionType and IntersectionType node in a finalized
// display type through newUnion / newIntersection with the ambient Context, so a
// member a concrete sibling subsumes is dropped. An inferred `1 | number` becomes
// `number`; an inferred `{x, ...} & {x, y, ...}` becomes `{x, y, ...}`.
//
// It is also where the disjointness-aware negation simplification runs, so an
// intersection carrying a complement is collapsed before subsumption runs over what
// is left. See simplify.go.
func (c *checker) subsumeFinal(t soltype.Type) soltype.Type {
	return t.Accept(&finalSubsumer{ctx: c.ctx}, soltype.Positive)
}

// finalSubsumer is the rewriting visitor behind subsumeFinal. It re-mints a
// lattice node in ExitType, after its children, so each member is itself
// already subsumed before the enclosing node runs its own subsumption.
type finalSubsumer struct{ ctx *Context }

func (s *finalSubsumer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	return soltype.EnterResult{}
}

// ExitType subsumes a lattice node's members and rebuilds it only when something
// changed. The input is a coalesced display type, so it is already flattened,
// pruned, deduped, and canonically ordered. newUnion's other normalization steps
// would be no-ops here, so only subsumeMembers runs. It removes members and never
// rewrites one, so comparing how many members came back against how many went in
// says whether anything changed, and the members themselves need no comparison.
// When nothing changed the original node is returned, preserving pointer identity
// up the spine.
//
// A union asks about its tail as well as its members, because this walk rewrites
// the bound before the union itself. A bound that folds to `never` on the way up
// leaves a tail holding nothing, and a bound that folds to a named member leaves
// one contributing nothing. Neither drops a member, so a member count alone would
// miss both and render `... : never` or `"y" | ... : "y"`.
func (s *finalSubsumer) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.UnionType:
		kept := subsumeMembers(s.ctx, t.Types, unionDrops)
		if len(kept) == len(t.Types) && narrowTailBound(kept, t.TailBound) == t.TailBound {
			return t
		}
		return collapseUnion(kept, tailOf(t), false)
	case *soltype.IntersectionType:
		members, changed, provedEmpty := simplifyNegations(s.ctx, t.Types)
		if provedEmpty {
			return &soltype.NeverType{}
		}
		kept := subsumeMembers(s.ctx, members, intersectionDrops)
		if !changed && len(kept) == len(t.Types) {
			return t
		}
		return collapseIntersection(kept, false)
	case *soltype.NegationType:
		// The operand was already rewritten by this walk, so a complement whose
		// operand collapsed to a lattice bound is folded here rather than left as the
		// meaningless `¬never`. `¬(string & ¬string)` reads `unknown`.
		return foldNegation(t)
	}
	return t
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
// kinds NeverType, UnknownType, ErrorType, and UndefinedType cannot reach this
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
		b := b.(*soltype.PromiseType)
		if c := compareType(a.Inner, b.Inner); c != 0 {
			return c
		}
		// ErrOrNever reads both sides through the nil-is-never collapse, so two promises
		// differing only in whether the rejection slot was written compare equal here.
		return compareType(a.ErrOrNever(), b.ErrOrNever())
	case *soltype.GeneratorType:
		b := b.(*soltype.GeneratorType)
		if a.Async != b.Async {
			return boolOrder(a.Async) - boolOrder(b.Async)
		}
		if c := compareType(a.Yield, b.Yield); c != 0 {
			return c
		}
		if c := compareType(a.Ret, b.Ret); c != 0 {
			return c
		}
		if c := compareType(a.Next, b.Next); c != 0 {
			return c
		}
		return compareType(a.ThrowsOrNever(), b.ThrowsOrNever())
	case *soltype.ArrayType:
		return compareType(a.Elem, b.(*soltype.ArrayType).Elem)
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
		if c := compareType(a.Ret, b.Ret); c != 0 {
			return c
		}
		// ThrowsOrNever reads both sides through the nil-is-never collapse, so two functions
		// differing only in whether the clause was written compare equal here.
		return compareType(a.ThrowsOrNever(), b.ThrowsOrNever())
	case *soltype.UnionType:
		b := b.(*soltype.UnionType)
		if a.Inexact != b.Inexact {
			return boolOrder(a.Inexact) - boolOrder(b.Inexact)
		}
		// An unbounded tail sorts before a bounded one, so two unions over the same
		// members order by what their tails admit.
		if (a.TailBound == nil) != (b.TailBound == nil) {
			return boolOrder(a.TailBound != nil) - boolOrder(b.TailBound != nil)
		}
		if a.TailBound != nil {
			if c := compareType(a.TailBound, b.TailBound); c != 0 {
				return c
			}
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
	case *soltype.NegationType:
		// Two complements order by their operands, so the order over negated members mirrors
		// the order over the members themselves and `¬number` precedes `¬string`.
		return compareType(a.Inner, b.(*soltype.NegationType).Inner)
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

// compareObjectFields orders two objects member against member. Element order in
// the slice is presentation only, so each object's members are sorted by
// compareObjElem first, then compared position by position.
func compareObjectFields(a, b *soltype.ObjectType) int {
	if c := len(a.Elems) - len(b.Elems); c != 0 {
		return c
	}
	ae := sortedObjElems(a.Elems)
	be := sortedObjElems(b.Elems)
	for i := range ae {
		if c := compareObjElem(ae[i], be[i]); c != 0 {
			return c
		}
	}
	return 0
}

// sortedObjElems returns a copy of the members ordered by compareObjElem, so a
// comparison reads both objects in one canonical order without mutating either.
func sortedObjElems(elems []soltype.ObjTypeElem) []soltype.ObjTypeElem {
	out := append([]soltype.ObjTypeElem(nil), elems...)
	sort.SliceStable(out, func(i, j int) bool {
		return compareObjElem(out[i], out[j]) < 0
	})
	return out
}

// compareObjElem orders two object members by name, then by kind, then by the
// fields of that kind. Ordering a member by kind lets a getter and a setter that
// share one name sort into a fixed order, which the name alone cannot settle.
func compareObjElem(a, b soltype.ObjTypeElem) int {
	if c := stringCompare(soltype.ObjElemName(a), soltype.ObjElemName(b)); c != 0 {
		return c
	}
	if c := objElemKindOrder(a) - objElemKindOrder(b); c != 0 {
		return c
	}
	switch a := a.(type) {
	case *soltype.PropertyElem:
		b := b.(*soltype.PropertyElem)
		if a.Optional != b.Optional {
			return boolOrder(a.Optional) - boolOrder(b.Optional)
		}
		if a.Readonly != b.Readonly {
			return boolOrder(a.Readonly) - boolOrder(b.Readonly)
		}
		return compareType(a.Type, b.Type)
	case *soltype.MethodElem:
		b := b.(*soltype.MethodElem)
		if a.Static != b.Static {
			return boolOrder(a.Static) - boolOrder(b.Static)
		}
		if c := len(a.Signatures) - len(b.Signatures); c != 0 {
			return c
		}
		for i := range a.Signatures {
			if c := compareType(a.Signatures[i], b.Signatures[i]); c != 0 {
				return c
			}
		}
		return 0
	case *soltype.GetterElem:
		b := b.(*soltype.GetterElem)
		if c := compareSelfParam(a.SelfParam, b.SelfParam); c != 0 {
			return c
		}
		if c := compareType(a.Type, b.Type); c != 0 {
			return c
		}
		// ThrowsOrNever reads both sides through the nil-is-never collapse, so two
		// getters differing only in whether the clause was written compare equal.
		return compareType(a.ThrowsOrNever(), b.ThrowsOrNever())
	case *soltype.SetterElem:
		b := b.(*soltype.SetterElem)
		if c := compareSelfParam(a.SelfParam, b.SelfParam); c != 0 {
			return c
		}
		if c := compareType(a.Param, b.Param); c != 0 {
			return c
		}
		return compareType(a.ThrowsOrNever(), b.ThrowsOrNever())
	case *soltype.ConstructorElem:
		return compareType(a.Fn, b.(*soltype.ConstructorElem).Fn)
	case *soltype.SpreadElem:
		return compareType(a.Type, b.(*soltype.SpreadElem).Type)
	case *soltype.MappedElem:
		// A mapped member computes the whole member list rather than naming one field.
		// Ordering by its value type gives a stable key, and equalObjElem settles whether
		// two are truly equal.
		return compareType(a.Value, b.(*soltype.MappedElem).Value)
	}
	return 0
}

// objElemKindOrder ranks the member kinds so compareObjElem can order two members
// that share a name but not a kind.
func objElemKindOrder(e soltype.ObjTypeElem) int {
	switch e.(type) {
	case *soltype.PropertyElem:
		return 0
	case *soltype.MethodElem:
		return 1
	case *soltype.GetterElem:
		return 2
	case *soltype.SetterElem:
		return 3
	case *soltype.ConstructorElem:
		return 4
	case *soltype.SpreadElem:
		return 5
	case *soltype.MappedElem:
		return 6
	}
	return 7
}

// compareSelfParam orders two method or accessor receivers. A missing receiver
// sorts before a present one, and two present receivers order by compareFuncParam.
func compareSelfParam(a, b *soltype.FuncParam) int {
	if (a == nil) != (b == nil) {
		return boolOrder(a != nil) - boolOrder(b != nil)
	}
	if a == nil {
		return 0
	}
	return compareFuncParam(a, b)
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
// lifetime, which marks an owned value, sorts first. Then 'static. Then
// LifetimeVar, ordered by ID.
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
	}
	return 3
}

// typeKindOrder ranks a soltype concrete kind for compareType. The lattice
// bounds and the error sentinel come first, then TypeVarType so quantified
// parameters lead in a rendered union, then primitives and literals, then
// the remaining structural kinds, then the lattice forms, and finally
// NullType and UndefinedType. A union renders its parameters and data
// members before its absence markers. NullType precedes UndefinedType, so
// `T0 | number | null | undefined` is the canonical render.
//
// NegationType is ranked with UnionType and IntersectionType among the
// lattice forms. The slot makes the order over member lists holding
// negations stable, so `¬A` sorts to one position whatever order the members
// were minted in.
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
	case *soltype.GeneratorType:
		return 10
	case *soltype.ArrayType:
		return 11
	case *soltype.FuncType:
		return 12
	case *soltype.UnionType:
		return 13
	case *soltype.IntersectionType:
		return 14
	case *soltype.NegationType:
		return 15
	case *soltype.NullType:
		return 16
	case *soltype.UndefinedType:
		return 17
	}
	return 18
}

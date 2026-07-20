package solver

import (
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Overload resolution, introduced in PR6. A name with more than one top-level FuncDecl
// is an overload set. Its ValueBinding carries one TypeScheme per arm ordered by source
// position. armPosLess in module.go defines that order as file path, then line, then column.
// A set whose arms span several files in a lib/ therefore reads top-to-bottom, file by
// file alphabetically, independent of the order sources reached the parser.
//
// Resolution is a phase distinct from constrain. The disjunction "callable in several
// ways" stays out of the subtype lattice. It is driven by the PR5 probe. Each candidate
// is trialled under a probe and the losers are rolled back, so a failed trial leaves no
// bounds on the argument variables and no stray Info or Prov entries.
//
// Specificity ordering is the one documented rule, reused by M4 object-arg and M5 method
// overloads. When every argument carries type information, meaning none is an
// unconstrained variable, candidates are tried most-specific-first. Arm A is more
// specific than B when they share an arity and every parameter of A is a structural
// subtype of B's, with at least one strict. So a concrete `(x: number)` outranks a
// generic `(x: T)`.
//
// Specificity is only a partial order. Incomparable arms fall back to declaration order
// through a stable sort. Arms are incomparable when they have different literal tags,
// disjoint shapes, or different arities.
//
// When an argument is still a fully-unconstrained variable, the call cannot rank arms
// confidently, so resolution defers to plain declaration-order first-match. This is the
// documented MVP fallback, with no speculative pinning and backtracking. The first arm
// whose argument constraints succeed wins. Its bounds are committed and the rest roll back.

// resolveOverload picks one arm of the overload set b for the call and returns that
// arm's instantiated return type. It commits the winning arm's argument constraints and
// rolls back every losing trial. When no arm accepts the arguments, it reports a
// NoMatchingOverloadError and returns the recovery placeholder.
func (c *checker) resolveOverload(lvl int, b ValueBinding, args []soltype.Type, call *ast.CallExpr) soltype.Type {
	for _, idx := range c.overloadOrder(b.Schemes, args) {
		// Open the probe BEFORE instantiating so the instantiation's side-table writes are
		// journaled and rolled back with a losing trial. Those writes are freshenAbove's
		// FromInstantiation Prov entries, which snapshotProv records only when a probe is
		// active. This upholds the guarantee that a losing trial leaves no Info or Prov
		// entries.
		p := c.openProbe()
		inst, ok := c.instantiate(b.Schemes[idx], lvl).(*soltype.FuncType)
		if !ok {
			// An overload arm that is not a function scheme cannot match a call. This should
			// not arise, since every arm comes from a FuncDecl. Skip it rather than
			// type-assert-panic on a malformed set.
			c.closeProbe(p, false)
			continue
		}
		matched, diags := c.tryOverloadArm(args, inst)
		c.closeProbe(p, matched)
		if matched {
			// The winning arm's argument constraints can accept while emitting a warning,
			// such as an AmbiguousUnionCommitWarning from a union-typed param. tryOverloadArm
			// runs the error-returning engine, so those diagnostics never reached c.errs.
			// Blame them at the call so a losing arm's rolled-back diagnostics stay dropped
			// while the winner's survive.
			c.blameConstraintErrors(call, diags)
			return inst.Ret
		}
	}
	return c.report(&NoMatchingOverloadError{Call: call, Candidates: b.Schemes})
}

// tryOverloadArm reports whether inst accepts a call with the given argument types,
// applying the argument constraints to inst's params as it goes. inst is a
// freshly-instantiated arm. It runs under a probe opened by the caller, so on a false
// return closeProbe(_, false) rolls back every bound it appended. It uses the
// error-returning Context.Constrain rather than the accumulating checker.constrain, so a
// rejected argument never reaches c.errs even before the probe's errs rollback.
//
// On a match it also returns the warnings the accepting constraints produced, so the
// caller can surface them at the call. An arg that passes still counts as a match even when
// it warns, since hasHardError draws the accept line, so the returned slice holds only
// warnings. A non-match returns nil, keeping a rejected arm's diagnostics dropped.
//
// Arity follows the direct-call accept-set from #677, reusing acceptSet so the overload
// arity gate and the FuncType<:FuncType constraint gate can never drift. A count below
// acceptSet's lower bound is too few, and a count above its upper bound is too many.
// Either is a non-match, unless the arm is inexact or has a rest. Extra arguments that
// such an arm absorbs impose no per-element constraint here. That per-element check needs
// Array types, which arrive in M4.
func (c *checker) tryOverloadArm(args []soltype.Type, inst *soltype.FuncType) (bool, []SolverError) {
	n := len(args)
	if lo, hi := acceptSet(inst); n < lo || n > hi {
		return false, nil
	}
	var diags []SolverError
	for i, arg := range args {
		if i >= len(inst.Params) || inst.Params[i].Rest {
			// Past the fixed params, or AT a trailing rest param. The rest or inexact tail
			// absorbs this and every later argument. Don't constrain a scalar argument
			// against the rest param's ARRAY element type, since Params[i].Type is `T[]`.
			// Per-element checking is M4.
			break
		}
		errs := c.ctx.Constrain(arg, inst.Params[i].Type)
		if hasHardError(errs) {
			return false, nil
		}
		diags = append(diags, errs...)
	}
	return true, diags
}

// overloadOrder returns the indices of schemes in the order arms should be tried. When
// every argument carries type information to rank by, the order is most-specific-first,
// and declaration order breaks specificity ties. When any argument is an unconstrained
// variable, the order is plain declaration order.
func (c *checker) overloadOrder(schemes []TypeScheme, args []soltype.Type) []int {
	if hasUnconstrainedArg(args) {
		order := make([]int, len(schemes))
		for i := range order {
			order[i] = i
		}
		return order // an unconstrained arg can't rank arms, so try in declaration order
	}
	cands := make([]soltype.Type, len(schemes))
	for i, s := range schemes {
		// A non-function arm's body is not a FuncType, so leave its slot a nil
		// interface. specificityOrder ranks a nil candidate last. Boxing a typed
		// nil *FuncType here would instead pass typeSpecificity a non-nil interface
		// over a nil pointer and panic on the param access.
		if ft := schemeFunc(s); ft != nil {
			cands[i] = ft
		}
	}
	return specificityOrder(cands)
}

// specificityOrder returns cands' indices most-specific-first, with declaration order
// breaking ties. It ranks each candidate by its DOMINATION COUNT — the number of other
// candidates strictly more specific than it, i.e. that "dominate" it — and sorts ascending
// on that count, so a candidate dominated by nobody comes first and the generic catch-all
// that every other candidate beats comes last.
//
// For example, given candidates number, T, and string: each concrete candidate is
// dominated by nobody, since number and string are incomparable, so both get count 0; the
// variable T is dominated by both, count 2. Ascending order is therefore number, string,
// T — the concretes first with their tie kept in declaration order, the variable last.
//
// The domination count exists to dodge a partial-order sort hazard. The typeSpecificity
// relation is only a partial order: some pairs are incomparable, the tie cases such as
// different literal tags, disjoint shapes, or different function arities, and
// incomparability is not transitive, so A incomparable to B and B to C does not make A
// incomparable to C. Feeding typeSpecificity straight to sort.SliceStable would therefore
// violate the strict-weak-ordering contract and yield undefined results. Projecting each
// candidate onto its integer domination count yields a TOTAL order on integers, so the
// sort is well-defined. A candidate dominated by none gets count 0 and sorts first; equal
// counts keep declaration order through the stable sort.
//
// overloadOrder uses this for direct calls, and constrain's IntersectionType-sub and
// UnionType-super exists arms use it for their member trials, so every trial site resolves
// in the same order. A nil candidate is a non-function overload arm and sorts last.
func specificityOrder(cands []soltype.Type) []int {
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	dominators := make([]int, len(cands))
	for i := range cands {
		if cands[i] == nil {
			dominators[i] = len(cands) + 1 // a non-rankable candidate sorts last
			continue
		}
		for j := range cands {
			if i == j || cands[j] == nil {
				continue
			}
			if typeSpecificity(cands[j], cands[i]) < 0 {
				dominators[i]++
			}
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return dominators[order[a]] < dominators[order[b]]
	})
	return order
}

// typeSpecificity compares two trial candidates by specificity. It returns -1 when a is
// strictly more specific than b, +1 when b is, and 0 when the two are incomparable or
// equally specific.
//
// Two functions compare parameter-wise through moreSpecific, so a concrete (x: number)
// still outranks a generic (x: T). Any other pair compares through structuralSubtype: a
// is more specific when it is a structural subtype of b and b is not a structural subtype
// of a. So a literal outranks its primitive, 1 before number, and a concrete type outranks
// a bare variable. structuralSubtype ranks disjoint shapes as incomparable, which yields a
// 0 tie that the stable sort resolves in declaration order.
func typeSpecificity(a, b soltype.Type) int {
	if fa, ok := a.(*soltype.FuncType); ok {
		if fb, ok := b.(*soltype.FuncType); ok {
			return moreSpecific(fa, fb)
		}
	}
	aSubB := structuralSubtype(a, b)
	bSubA := structuralSubtype(b, a)
	switch {
	case aSubB && !bSubA:
		return -1
	case bSubA && !aSubB:
		return 1
	default:
		return 0
	}
}

// hasUnconstrainedArg reports whether any top-level call argument is a fully-unconstrained
// inference variable — a bare var with no bounds either way, which carries no type
// information to rank overloads by. overloadOrder treats a true result as "can't rank the
// arms" and falls back to declaration order (see there and #723).
//
// The check is shallow by design: a compound that merely WRAPS a bare var (a tuple,
// record, or func) doesn't count, since structuralSubtype ranks such shapes as a tie and
// the order collapses to declaration order anyway.
func hasUnconstrainedArg(args []soltype.Type) bool {
	return slices.ContainsFunc(args, isUnconstrainedVar)
}

// isUnconstrainedVar reports whether t is a type variable with no bounds in either
// direction. Such an argument carries no type information to rank overloads by.
func isUnconstrainedVar(t soltype.Type) bool {
	v, ok := t.(*soltype.TypeVarType)
	return ok && len(v.LowerBounds) == 0 && len(v.UpperBounds) == 0
}

// schemeFunc extracts the FuncType body of an overload arm's scheme, the raw
// variable-carrying signature, or nil when the scheme's body is not a function. The
// specificity comparator uses it to rank the signatures directly rather than
// instantiating them, so ranking mints no fresh vars and writes no Prov entries.
func schemeFunc(s TypeScheme) *soltype.FuncType {
	var body soltype.Type
	switch sc := s.(type) {
	case *MonoScheme:
		body = sc.Ty
	case *PolyScheme:
		body = sc.Body
	}
	ft, _ := body.(*soltype.FuncType)
	return ft
}

// moreSpecific compares two overload arms by specificity. It returns -1 when a is
// strictly more specific than b, +1 when b is, and 0 on a tie. A tie means the arms are
// either incomparable or equally specific. Arms of different arity are a tie here. The
// arity gate in tryOverloadArm already keeps a call from reaching an arm of the wrong
// arity, so cross-arity ordering only needs to preserve declaration order. Same-arity
// arms compare pointwise. a is more specific when every a.param is a structural subtype
// of the matching b.param and at least one direction is strict.
func moreSpecific(a, b *soltype.FuncType) int {
	if len(a.Params) != len(b.Params) {
		return 0
	}
	aSubB, bSubA := true, true
	for i := range a.Params {
		// Each flag is monotonic (only ever flips true→false), so guard its call: once a
		// direction is broken, further structuralSubtype calls for it are wasted. When BOTH
		// are broken the result is already a 0 tie, so stop early.
		if aSubB && !structuralSubtype(a.Params[i].Type, b.Params[i].Type) {
			aSubB = false
		}
		if bSubA && !structuralSubtype(b.Params[i].Type, a.Params[i].Type) {
			bSubA = false
		}
		if !aSubB && !bSubA {
			return 0
		}
	}
	if aSubB && !bSubA {
		return -1
	}
	if bSubA && !aSubB {
		return 1
	}
	return 0
}

// equallySpecific reports whether two arms are INDISTINGUISHABLE for overload dispatch:
// they share an arity and each parameter is structurally equivalent to its counterpart (a
// mutual structural subtype — so two unconstrained-var params count as equal, both TOP).
// Neither arm is then more specific and no argument could select one over the other.
//
// This is a STRICT subset of moreSpecific's 0 tie, which also covers incomparable arms of
// disjoint shape (number vs string) that a call CAN still tell apart. The overload-set
// builder in module.go uses it to reject a set whose arms codegen could not dispatch,
// since codegen dispatches on parameter types.
func equallySpecific(a, b *soltype.FuncType) bool {
	if len(a.Params) != len(b.Params) {
		return false
	}
	for i := range a.Params {
		if !structuralSubtype(a.Params[i].Type, b.Params[i].Type) ||
			!structuralSubtype(b.Params[i].Type, a.Params[i].Type) {
			return false
		}
	}
	return true
}

// structuralSubtype is a pure, non-mutating structural subtype test used ONLY for
// ranking overload specificity. It never appends a bound, so it is safe to call on the
// raw scheme bodies, which carry quantified variables, without corrupting them.
//
// A type variable is treated as TOP. A concrete type is a subtype of a variable, so a
// concrete param outranks a generic one. A variable is a subtype of nothing concrete.
// Two variables tie. Beyond that it mirrors constrain's atom arms, namely structural
// equality plus a literal under its primitive. Anything richer ranks as incomparable and
// returns false, deferring to declaration order.
//
// Two overload arms come from independent schemes, so a borrow parameter of one carries
// a LifetimeVar distinct from the matching borrow of the other. Structural equality here
// is therefore alpha-equivalence over lifetimes, not pointer identity, so two arms whose
// borrow parameters differ only in lifetime identity rank as equally specific rather than
// as two incomparable arms.
//
// It is deliberately NOT the full subtype relation. Resolution correctness comes from
// the constrain trial in tryOverloadArm, not from this comparator.
func structuralSubtype(a, b soltype.Type) bool {
	if _, ok := b.(*soltype.TypeVarType); ok {
		return true
	}
	if _, ok := a.(*soltype.TypeVarType); ok {
		return false
	}
	if alphaEqualTypes(a, b) {
		return true
	}
	if l, ok := a.(*soltype.LitType); ok {
		if p, ok := b.(*soltype.PrimType); ok {
			return primOf(l.Lit) == p.Prim
		}
	}
	if ao, ok := a.(*soltype.ObjectType); ok {
		if bo, ok := b.(*soltype.ObjectType); ok {
			return objectSubsumes(ao, bo)
		}
	}
	return false
}

// objectSubsumes reports whether object a ranks strictly more specific than object b for
// overload specificity (the #723 object-argument case). "More specific" means a narrower
// parameter, one accepting fewer arguments, so it wins dispatch when both arms match an
// argument. It ranks two axes and deliberately ignores a third:
//
//   - Required-field count. When a's required properties are a strict superset of b's, a is
//     stricter and ranks more specific, the object analogue of a concrete param outranking a
//     generic one. a must carry every required property of b, as required and with an
//     alpha-equal type. A missing, optional, or differently-typed match makes the pair
//     incomparable, so this returns false.
//   - Exactness, only when the required-field sets are equal. An exact a is more specific
//     than an inexact b over the same fields, since it accepts no extra properties.
//   - Field types, which are NOT ranked. Arms whose shared fields differ in type tie here.
//     Disjoint types make the arms accept disjoint arguments, so the trial picks the match.
//     Overlapping types fall back to declaration order.
//
// Only required properties count. An optional property widens an object rather than
// narrowing it. `{x, y?}` accepts both `{x}` and `{x, y}`, so it is not more specific than
// `{x}`. Counting it would rank the wider arm as narrower.
//
// This is a heuristic, not the full subtype relation. Resolution correctness comes from the
// constrain trial in tryOverloadArm. objectSubsumes only orders the arms that could match.
// Two objects with identical fields and exactness are alpha-equal and never reach here, since
// structuralSubtype resolves them to a tie first. A non-property member on either side makes
// the pair incomparable and ranks as false.
func objectSubsumes(a, b *soltype.ObjectType) bool {
	bReq := 0
	for _, be := range b.Elems {
		bp, ok := be.(*soltype.PropertyElem)
		if !ok {
			return false
		}
		if bp.Optional {
			continue
		}
		bReq++
		ap, ok := a.Prop(bp.Name)
		if !ok || ap.Optional {
			return false
		}
		if !alphaEqualTypes(ap.Type, bp.Type) {
			return false
		}
	}
	aReq := 0
	for _, ae := range a.Elems {
		ap, ok := ae.(*soltype.PropertyElem)
		if !ok {
			return false
		}
		if !ap.Optional {
			aReq++
		}
	}
	if aReq > bReq {
		return true
	}
	return !a.Inexact && b.Inexact
}

// overloadIntersection synthesizes the value-position type of an overloaded name, the
// IntersectionType of its arms in declaration order, each instantiated at lvl. This is
// the one scoped lattice exception, described at constrain's IntersectionType arm and in
// the doc.go note. It is built LAZILY, only where an overloaded name is used as a value
// in inferIdent or recorded as a call's callee, never eagerly per binding. b.Schemes
// stays the source of truth.
func (c *checker) overloadIntersection(lvl int, b ValueBinding) soltype.Type {
	arms := make([]soltype.Type, len(b.Schemes))
	for i, s := range b.Schemes {
		arms[i] = c.instantiate(s, lvl)
	}
	return &soltype.IntersectionType{Types: arms}
}

// overloadDisplayType builds the COALESCED display type of an overloaded name, the
// IntersectionType of its arms' display types, one schemeType per arm. Unlike
// overloadIntersection it mints no fresh inference variables, because it coalesces the
// schemes rather than instantiating them. So it is the right form for an Info record
// such as an overloaded call's callee, where the recorded type is for tooling and hover
// and no inference flows out of it. This also avoids a second per-arm instantiation per
// call.
func overloadDisplayType(b ValueBinding) soltype.Type {
	arms := make([]soltype.Type, len(b.Schemes))
	for i, s := range b.Schemes {
		arms[i] = schemeType(s)
	}
	return &soltype.IntersectionType{Types: arms}
}

// isFullyAnnotated reports whether a function signature is ground from its annotations
// alone, with every parameter typed and a return type present. The recursion gate
// checkOverloadAnnotations requires this of every arm of an overloaded function in a
// mutually-recursive group, since an un-annotated arm's signature is not known before
// its body is inferred.
func isFullyAnnotated(sig ast.FuncSig) bool {
	if sig.Return == nil {
		return false
	}
	for _, p := range sig.Params {
		if p.TypeAnn == nil {
			return false
		}
	}
	return true
}

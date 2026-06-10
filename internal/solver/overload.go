package solver

import (
	"slices"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Overload resolution, introduced in PR6. A name with more than one top-level FuncDecl
// is an overload set. Its ValueBinding carries one TypeScheme per arm, which is what
// b.IsOverloaded() reports, ordered by source position. armPosLess in module.go defines
// that order as file path, then line, then column. A set whose arms span several files
// in a lib/ therefore reads top-to-bottom, file by file alphabetically, independent of
// the order sources reached the parser.
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
// whose argument constraints succeed wins. Its bounds commit and the rest roll back.

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
		matched := c.tryOverloadArm(args, inst)
		c.closeProbe(p, matched)
		if matched {
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
// Arity follows the direct-call accept-set from #677, reusing acceptSet so the overload
// arity gate and the FuncType<:FuncType constraint gate can never drift. A count below
// acceptSet's lower bound is too few, and a count above its upper bound is too many.
// Either is a non-match, unless the arm is inexact or has a rest. Extra arguments that
// such an arm absorbs impose no per-element constraint here. That per-element check needs
// Array types, which arrive in M4.
func (c *checker) tryOverloadArm(args []soltype.Type, inst *soltype.FuncType) bool {
	n := len(args)
	if lo, hi := acceptSet(inst); n < lo || n > hi {
		return false
	}
	for i, arg := range args {
		if i >= len(inst.Params) || inst.Params[i].Rest {
			// Past the fixed params, or AT a trailing rest param. The rest or inexact tail
			// absorbs this and every later argument. Don't constrain a scalar argument
			// against the rest param's ARRAY element type, since Params[i].Type is `T[]`.
			// Per-element checking is M4.
			break
		}
		if errs := c.ctx.Constrain(arg, inst.Params[i].Type); len(errs) > 0 {
			return false
		}
	}
	return true
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
	funcs := make([]*soltype.FuncType, len(schemes))
	for i, s := range schemes {
		funcs[i] = schemeFunc(s)
	}
	return specificityOrder(funcs)
}

// specificityOrder returns funcs' indices most-specific-first, with declaration order
// breaking ties. It ranks each arm by its domination count, the number of other arms
// strictly more specific than it, and sorts ascending on that count.
//
// This is the load-bearing fix for the partial-order sort hazard. The moreSpecific
// relation is only a partial order, so feeding it straight to sort.SliceStable would
// violate the strict-weak-ordering contract, because incomparability is non-transitive.
// The domination count is a total order on integers, so the sort is well-defined. An arm
// dominated by none gets count 0 and sorts first. Equal counts keep declaration order
// through the stable sort.
//
// overloadOrder uses this for direct calls and constrain's IntersectionType arm uses it
// for value-position calls, so both resolve in the same order. A nil entry is a
// non-function arm and sorts last.
func specificityOrder(funcs []*soltype.FuncType) []int {
	order := make([]int, len(funcs))
	for i := range order {
		order[i] = i
	}
	dominators := make([]int, len(funcs))
	for i := range funcs {
		if funcs[i] == nil {
			dominators[i] = len(funcs) + 1 // non-function arms sort last
			continue
		}
		for j := range funcs {
			if i == j || funcs[j] == nil {
				continue
			}
			if moreSpecific(funcs[j], funcs[i]) < 0 {
				dominators[i]++
			}
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return dominators[order[a]] < dominators[order[b]]
	})
	return order
}

// hasUnconstrainedArg reports whether any call argument is a fully-unconstrained
// inference variable, a bare var with no bounds in either direction. Such an argument
// carries no type information to rank overloads by. A literal, a concrete type, or a var
// already pinned by some bound is fine. An untouched parameter var is not, so its
// presence makes the call fall back to declaration-order first-match. That fallback
// over-narrows the enclosing function, since it pins the arg to the first arm. The real
// fix is to defer resolution until the arg is grounded, tracked in #723.
//
// The check is intentionally shallow and looks at top-level args only. A structural
// argument that merely WRAPS an unconstrained var does NOT count, for example a tuple,
// record, or func holding a bare var. That is harmless, because such a compound never
// disambiguates overloads under the specificity comparator. structuralSubtype returns
// false for compound shapes, so they rank as a tie and the order collapses to
// declaration order, exactly what this fallback would produce. Recursing would only
// defer more calls with no change in the resolved arm.
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
		if !structuralSubtype(a.Params[i].Type, b.Params[i].Type) {
			aSubB = false
		}
		if !structuralSubtype(b.Params[i].Type, a.Params[i].Type) {
			bSubA = false
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

// equallySpecific reports whether two arms are INDISTINGUISHABLE for overload dispatch.
// They share an arity and each parameter is a structural subtype of its counterpart in
// BOTH directions, so neither arm is more specific and no argument could ever select one
// over the other. This is a strict subset of moreSpecific's tie result, the 0 case,
// which ALSO covers incomparable arms of disjoint shape such as number versus string
// that a call CAN still tell apart. Two type-variable params count as equal, since
// structuralSubtype treats a var as TOP in both directions, so two fully-generic arms
// collide too. The overload-set builder in module.go uses this to reject a set whose
// arms codegen could not tell apart, because codegen dispatches on parameter types.
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
// It is deliberately NOT the full subtype relation. Resolution correctness comes from
// the constrain trial in tryOverloadArm, not from this comparator.
func structuralSubtype(a, b soltype.Type) bool {
	if _, ok := b.(*soltype.TypeVarType); ok {
		return true
	}
	if _, ok := a.(*soltype.TypeVarType); ok {
		return false
	}
	if equalType(a, b) {
		return true
	}
	if l, ok := a.(*soltype.LitType); ok {
		if p, ok := b.(*soltype.PrimType); ok {
			return primOf(l.Lit) == p.Prim
		}
	}
	return false
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

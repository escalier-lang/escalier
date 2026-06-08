package solver

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Overload resolution (PR6). A name with more than one top-level FuncDecl is an
// OVERLOAD SET: its ValueBinding carries one TypeScheme per arm (b.IsOverloaded()),
// in declaration order. Resolution is a phase DISTINCT from constrain — the
// disjunction "callable in several ways" stays out of the subtype lattice — driven
// by the PR5 probe: each candidate is trialled under a probe and the losers rolled
// back, so a failed trial leaves no bounds on the argument variables and no stray
// Info/Prov entries.
//
// Specificity ordering (the one documented rule, reused by M4 object-arg and M5
// method overloads): for a call whose arguments are GROUND ENOUGH, candidates are
// tried most-specific-first, where arm A is more specific than B iff they share an
// arity and A's parameters are pointwise structural subtypes of B's (with at least
// one strict) — a concrete `(x: number)` outranks a generic `(x: T)`. Specificity
// is a partial order, so incomparable arms (different literal tags, disjoint shapes,
// different arities) fall back to DECLARATION ORDER via a stable sort. When an
// argument is still a fully-unconstrained variable the call is not ground enough to
// rank confidently, so resolution defers to plain declaration-order first-match
// (the documented MVP fallback — no speculative pinning + backtrack). The first arm
// whose argument constraints succeed wins; its bounds commit and the rest roll back.

// resolveOverload picks one arm of the overload set b for the call and returns its
// (instantiated) return type, committing the winning arm's argument constraints and
// rolling back every losing trial. When no arm accepts the arguments it reports a
// NoMatchingOverloadError and returns the recovery placeholder.
func (c *checker) resolveOverload(lvl int, b ValueBinding, args []soltype.Type, call *ast.CallExpr) soltype.Type {
	for _, idx := range c.overloadOrder(b.Schemes, args) {
		inst, ok := c.instantiate(b.Schemes[idx], lvl).(*soltype.FuncType)
		if !ok {
			// An overload arm that is not a function scheme cannot match a call. This
			// should not arise (every arm comes from a FuncDecl), but skip rather than
			// type-assert-panic on a malformed set.
			continue
		}
		p := c.openProbe()
		matched := c.tryOverloadArm(args, inst)
		c.closeProbe(p, matched)
		if matched {
			return inst.Ret
		}
	}
	return c.report(&NoMatchingOverloadError{Call: call, Candidates: b.Schemes})
}

// tryOverloadArm reports whether inst (a freshly-instantiated arm) accepts a call
// with the given argument types, applying the argument constraints to inst's params
// as it goes. It runs under a probe opened by the caller, so on failure (a false
// return) closeProbe(_, false) rolls back every bound it appended. It uses the
// error-RETURNING Context.Constrain (not the accumulating checker.constrain) so a
// rejected argument never reaches c.errs even before the probe's errs rollback.
//
// Arity follows the direct-call rule (#677): too few (below requiredCount) or too
// many (above the declared params, unless the arm is inexact or has a rest) is a
// non-match. Extra arguments an inexact/rest arm absorbs impose no per-element
// constraint here (that check needs Array types — M4).
func (c *checker) tryOverloadArm(args []soltype.Type, inst *soltype.FuncType) bool {
	n := len(args)
	if n < requiredCount(inst) {
		return false
	}
	if !inst.Inexact && !hasRest(inst) && n > len(inst.Params) {
		return false
	}
	for i, arg := range args {
		if i >= len(inst.Params) {
			break // extra args absorbed by a rest/inexact tail
		}
		if errs := c.ctx.Constrain(arg, inst.Params[i].Type); len(errs) > 0 {
			return false
		}
	}
	return true
}

// overloadOrder returns the indices of schemes in the order arms should be tried:
// most-specific-first (stable, so declaration order breaks specificity ties) when
// the arguments are ground enough to rank, else plain declaration order.
func (c *checker) overloadOrder(schemes []TypeScheme, args []soltype.Type) []int {
	order := make([]int, len(schemes))
	for i := range order {
		order[i] = i
	}
	if !groundEnough(args) {
		return order // not ground enough to rank: declaration-order first-match
	}
	sort.SliceStable(order, func(i, j int) bool {
		fi := schemeFunc(schemes[order[i]])
		fj := schemeFunc(schemes[order[j]])
		if fi == nil || fj == nil {
			return false // a non-function arm cannot be ranked; leave order unchanged
		}
		return moreSpecific(fi, fj) < 0
	})
	return order
}

// groundEnough reports whether the call arguments carry enough type information to
// rank overloads confidently — i.e. no argument is a fully-unconstrained inference
// variable (a bare var with no bounds either way). A literal, a concrete type, or a
// var already pinned by some bound is ground enough; an untouched parameter var is
// not, so the call falls back to declaration-order first-match.
func groundEnough(args []soltype.Type) bool {
	for _, a := range args {
		if v, ok := a.(*soltype.TypeVarType); ok && len(v.LowerBounds) == 0 && len(v.UpperBounds) == 0 {
			return false
		}
	}
	return true
}

// schemeFunc extracts the FuncType body of an overload arm's scheme (the raw,
// variable-carrying signature), or nil when the scheme's body is not a function.
// Used by the specificity comparator, which ranks the signatures directly rather
// than instantiating them (no fresh vars minted, no Prov entries written during
// ranking).
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

// moreSpecific compares two overload arms by specificity: -1 when a is strictly more
// specific than b, +1 when b is, 0 on a tie (incomparable or equally specific). Arms
// of different arity are a tie here — the arity gate in tryOverloadArm already keeps
// a call from reaching an arm of the wrong arity, so cross-arity ordering only needs
// to preserve declaration order. Same-arity arms compare pointwise: a is more
// specific iff every a.param is a structural subtype of the matching b.param and at
// least one direction is strict.
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

// structuralSubtype is a PURE (non-mutating) structural subtype test used ONLY for
// ranking overload specificity — it never appends a bound, so it is safe to call on
// the raw scheme bodies (which carry quantified variables) without corrupting them.
// A type variable is treated as TOP: a concrete type is a subtype of a variable
// (so a concrete param outranks a generic one), a variable is a subtype of nothing
// concrete, and two variables tie. Beyond that it mirrors constrain's atom arms
// (structural equality, plus a literal under its primitive); anything richer ranks
// as incomparable (false), deferring to declaration order. It is deliberately NOT
// the full subtype relation — resolution correctness comes from the constrain trial
// in tryOverloadArm, not from this comparator.
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

// overloadIntersection synthesizes the value-position type of an overloaded name —
// the IntersectionType of its arms (declaration order preserved), each instantiated
// at lvl. This is the one scoped lattice exception (see constrain's IntersectionType
// arm and the doc.go note): it is built LAZILY, only where an overloaded name is
// used as a value (inferIdent) or recorded as a call's callee, never eagerly per
// binding — b.Schemes stays the source of truth.
func (c *checker) overloadIntersection(lvl int, b ValueBinding) soltype.Type {
	arms := make([]soltype.Type, len(b.Schemes))
	for i, s := range b.Schemes {
		arms[i] = c.instantiate(s, lvl)
	}
	return &soltype.IntersectionType{Types: arms}
}

// isFullyAnnotated reports whether a function signature is ground from its
// annotations alone — every parameter typed and a return type present. The
// recursion gate (checkOverloadAnnotations) requires this of every arm of an
// overloaded function in a mutually-recursive group, since an un-annotated arm's
// signature is not known before its body is inferred.
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

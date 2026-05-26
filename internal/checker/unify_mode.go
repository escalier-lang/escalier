package checker

import "github.com/escalier-lang/escalier/internal/type_system"

// Context.QueryUnify reports whether the checker is currently in query
// mode — i.e. unifyInner is running as a pure structural-subtype
// predicate and must refuse every side effect. `Unify` runs with this
// false (the default — TypeVars bind, Widenable TypeVars widen, lifetimes
// reconcile). `Check` sets it to true for the duration of a single query.
//
// The mutation surface inside unifyInner / unifyMatched / bind that
// query mode disables:
//
//  1. TypeVar binding (`tv.Instance = ...` in bind, plus the helpers
//     reached from bind: openClosedObjectForParam,
//     handleArrayConstraintBinding). bind returns a regular
//     CannotUnifyTypesError so the caller reports a structural
//     mismatch like any other.
//  2. Widenable TypeVar widening at unify.go's concrete-vs-concrete
//     failure fallback. Speculative callers want the current shape,
//     not "would succeed if we mutated".
//  3. Lifetime reconciliation via UnifyLifetimes (which itself binds
//     LifetimeVars). Structural subtyping doesn't depend on lifetime
//     equality.
//
// The flag lives on Context (not Checker) so a value-copied ctx with
// QueryUnify=true auto-propagates through every recursive unifyInner
// call without a save/restore dance, and so the flag's scope is
// structurally tied to the query that set it.
//
// Prune's path compression is intentionally permitted in query mode:
// it rewrites Instance chains that are already aliased, so it can't
// change what any TypeVar resolves to — only how many hops the next
// resolution takes.

// Check reports whether t1 is a structural subtype of t2 without
// committing any inference side effects. It is the pure counterpart
// of Unify: same recursion, same case analysis, but TypeVar binding,
// Widenable widening, and lifetime reconciliation are all refused.
//
// An unbound TypeVar on either side (after Prune) makes Check return
// false rather than bind — if the caller wanted to commit a binding
// they would have called Unify.
//
// Safe to call during placeholder phases, speculative overload
// ranking, simplification, and any other context that needs a
// side-effect-free subtype query.
func (c *Checker) Check(ctx Context, t1, t2 type_system.Type) bool {
	ctx.QueryUnify = true
	return len(c.unifyInner(ctx, t1, t2, make(unifySeen))) == 0
}

// MutuallyAssignable reports whether t1 and t2 are structural subtypes
// of each other (i.e. equivalent modulo aliasing). Equivalent to
// Check(t1, t2) && Check(t2, t1); exposed as a sibling so callers
// don't forget the second direction.
func (c *Checker) MutuallyAssignable(ctx Context, t1, t2 type_system.Type) bool {
	return c.Check(ctx, t1, t2) && c.Check(ctx, t2, t1)
}

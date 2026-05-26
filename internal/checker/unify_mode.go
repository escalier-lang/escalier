package checker

import "github.com/escalier-lang/escalier/internal/type_system"

// queryUnify reports whether the checker is currently in query mode —
// i.e. unifyInner is running as a pure structural-subtype predicate
// and must refuse every side effect. `Unify` runs with this false
// (the default — TypeVars bind, Widenable TypeVars widen, lifetimes
// reconcile). `Check` swaps it to true for the duration of a single
// query.
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
// The flag lives on *Checker rather than threading through every
// recursive call because the recursion has ~80+ internal call sites
// and the mode is invariant across the whole traversal. The Checker
// is single-threaded so a per-call field swap is safe.
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
	prev := c.queryUnify
	c.queryUnify = true
	defer func() { c.queryUnify = prev }()
	return len(c.unifyInner(ctx, t1, t2, make(unifySeen))) == 0
}

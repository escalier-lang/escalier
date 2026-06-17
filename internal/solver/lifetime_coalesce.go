package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// coalesceRefLifetime rewrites a coalesced RefType's lifetime to its display form
// (M4 D3). The structural coalescers rebuild a RefType through the shared visitor,
// which carries the lifetime through unchanged because a Lifetime is not a Type; this
// runs in their ExitType to resolve that lifetime the way the var arms resolve a
// type variable. A non-RefType, or a borrow with no lifetime, passes through
// untouched. The RefType lifetime is covariant — the mut-driven write view never
// touches it — so it coalesces in the borrow's own polarity, pol.
//
// Naming and single-polarity elision of param lifetimes are deferred to D4; D3
// resolves only the join-and-escape shape: a join variable expands to the union of
// the param lifetimes it reaches, and any lifetime forced to 'static renders
// 'static.
func coalesceRefLifetime(t soltype.Type, pol soltype.Polarity) soltype.Type {
	r, ok := t.(*soltype.RefType)
	if !ok || r.Lt == nil {
		return t
	}
	lt := coalesceLifetime(r.Lt, pol)
	if lt == r.Lt {
		return r
	}
	return &soltype.RefType{Mut: r.Mut, Lt: lt, Inner: r.Inner}
}

// coalesceLifetime resolves a single lifetime to its display form. A 'static
// renders 'static and a nil lifetime stays nil. A lifetime variable resolves by
// its origin:
//
//   - A param lifetime (a borrow origin) is kept as itself — the printer renders it
//     under its raw 'l{ID} debug name now, its quantified name in D4 — unless it is
//     forced to 'static, in which case its borrow escapes and it renders 'static.
//   - A join lifetime (a multi-source return/branch, Join set) expands to the union
//     of the param lifetimes it reaches through its polarity-relevant bounds, so a
//     return uniting two borrows coalesces to `('a | 'b)`. A reachable 'static
//     absorbs the whole union to 'static.
func coalesceLifetime(lt soltype.Lifetime, pol soltype.Polarity) soltype.Lifetime {
	v, ok := lt.(*soltype.LifetimeVar)
	if !ok {
		return lt // 'static, or a nil slot handled by the caller
	}
	if !v.Join {
		if lifetimeForced(v) {
			return soltype.Static
		}
		return v
	}
	members, static := reachableParamLifetimes(v, pol, map[*soltype.LifetimeVar]bool{})
	if static {
		return soltype.Static
	}
	switch len(members) {
	case 0:
		// A join reaching no param lifetime carries no nameable lifetime — drop it,
		// yielding an owned-mutable borrow (the wrapper's lifetime goes nil).
		return nil
	case 1:
		return members[0]
	default:
		return &soltype.LifetimeUnion{Lifetimes: members}
	}
}

// reachableParamLifetimes collects the param lifetimes a join variable reaches
// through its polarity-relevant bounds — lower bounds in Positive position, upper
// in Negative — following nested join variables transitively. It reports whether
// 'static is reached, which absorbs the union. The seen-set keys by variable
// identity so a cyclic bound graph terminates. A forced param member renders
// 'static, which sets the static flag rather than adding the member.
func reachableParamLifetimes(v *soltype.LifetimeVar, pol soltype.Polarity, seen map[*soltype.LifetimeVar]bool) ([]soltype.Lifetime, bool) {
	if seen[v] {
		return nil, false
	}
	seen[v] = true
	var members []soltype.Lifetime
	static := false
	for _, b := range v.BoundsAt(pol) {
		if soltype.IsStaticLifetime(b) {
			static = true
			continue
		}
		bv, ok := b.(*soltype.LifetimeVar)
		if !ok {
			continue
		}
		if !bv.Join {
			if lifetimeForced(bv) {
				static = true
				continue
			}
			members = append(members, bv)
			continue
		}
		sub, subStatic := reachableParamLifetimes(bv, pol, seen)
		members = append(members, sub...)
		static = static || subStatic
	}
	return members, static
}

// lifetimeForced reports whether a lifetime variable has 'static among its bounds,
// in which case it coalesces to 'static — the escape-to-static outcome. Both bound
// directions are checked: the escape constraint `v <: 'static` adds 'static as an
// upper bound, while a lower-bound 'static can arise from a join member.
func lifetimeForced(v *soltype.LifetimeVar) bool {
	return soltype.ContainsLifetime(v.LowerBounds, soltype.Static) ||
		soltype.ContainsLifetime(v.UpperBounds, soltype.Static)
}

package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Complements in the operator layer.
//
// The operator reducers meet the same surface union and intersection nodes they always have, plus
// one more kind, the complement `¬T`. This file holds the three places a complement changes what a
// reduction produces.
//
//  1. A meet carrying a complement is a set difference. reduceDifference computes it, so a key set
//     written `keyof T ∩ ¬K` enumerates the keys of T other than K.
//  2. A distributive conditional that filters its own operand is a set difference too.
//     nativeDifference rewrites one whose operand is not ground into that meet, which is what makes
//     `Exclude`, `Omit`, and `NonNullable` total over a type variable.
//  3. An `infer` pattern matches against structure a value carries. A complement names the values
//     its operand rejects and carries no structure of its own, so positiveSkeleton drops it before
//     the match.
//
// # Which reading each utility type takes
//
// TypeScript defines `Exclude<U, V>` as the distributive conditional `U extends V ? never : U`,
// which filters whole members of a union. The set difference `U ∩ ¬V` cuts inside a member as well.
// The two agree whenever every member of U is either wholly inside V or disjoint from it, and
// diverge otherwise. `Exclude<string, "a">` is `string` under the filter, since `string` is not a
// subtype of `"a"`, and `string ∩ ¬"a"` under the difference.
//
// Escalier takes the filter when the operand is ground and the difference when it is not:
//
//   - A ground operand reduces the way TypeScript's does, so ported code keeps its meaning.
//   - A type-variable operand has no members to filter, so the filter is stuck. The difference is
//     an answer, and it reduces to the filter's answer once the variable grounds to a union whose
//     members are each inside or disjoint from V.
//
// The fork is therefore only reachable where the filter produced nothing at all.

// reduceDifference reduces a meet carrying at least one complement — `A ∩ ¬B`, the values of A that
// are not values of B. It is the reduction behind every Boolean key set: `Omit<T, "b">` maps over
// `keyof T ∩ ¬"b"`, which reduces to `"a" | "c"` for `type T = {a: X, b: Y, c: Z}` so the mapped
// type has keys to iterate.
//
// A meet with no complement is returned untouched, keeping its pointer, since an ordinary
// intersection is not something reduction works out.
//
// The difference distributes over the positive side's members, `(A | B) ∩ ¬X` being
// `(A ∩ ¬X) | (B ∩ ¬X)`, and each member is settled against each excluded type by two subtype
// questions the normal-form layer decides:
//
//   - `m <: x` means every value of m is excluded, so m contributes nothing.
//   - `m <: ¬x` means the two are disjoint, so x removes nothing from m and the complement is
//     dropped from m's result.
//   - Neither holds when x removes part of m, and that part is not expressible as a union of the
//     members at hand, so m keeps the complement and renders as `m ∩ ¬x`.
//
// An operand that is not ground leaves the whole difference standing, so it reduces later once the
// operand grounds. That residual is the `∩ ¬` form itself rather than a stuck operator, which is
// what a caller such as a mapped-type key set or a constraint reads.
//
// An inexact positive side names only some of its members, and which of the unnamed ones the
// exclusion removes cannot be worked out, so the result keeps the open tail. `("a" | ...) ∩ ¬"a"`
// reduces to the inexact empty set rather than to `never`.
func (e *typeEvaluator) reduceDifference(t *soltype.IntersectionType) soltype.Type {
	positives := make([]soltype.Type, 0, len(t.Types))
	var excluded []soltype.Type
	for _, m := range t.Types {
		if neg, ok := m.(*soltype.NegationType); ok {
			excluded = append(excluded, e.reduce(neg.Inner))
			continue
		}
		positives = append(positives, e.reduce(m))
	}
	if len(excluded) == 0 {
		return t
	}
	base := newIntersection(nil, positives)
	if !groundDifference(base, excluded) {
		return newIntersection(nil, append([]soltype.Type{base}, complementsOf(excluded)...))
	}
	members, inexact := unionMembers(base)
	survivors := make([]soltype.Type, 0, len(members))
	for _, m := range members {
		if narrowed, keep := e.excludeFrom(m, excluded); keep {
			survivors = append(survivors, narrowed)
		}
	}
	return newUnion(nil, survivors, inexact)
}

// excludeFrom settles one member of a difference's positive side against every excluded type. It
// reports whether the member survives at all, and returns what is left of it: the member itself
// when no exclusion cuts into it, and the member met with the complements of those that do.
func (e *typeEvaluator) excludeFrom(m soltype.Type, excluded []soltype.Type) (soltype.Type, bool) {
	overlapping := make([]soltype.Type, 0, len(excluded))
	for _, x := range excluded {
		if e.ctx.condExtends(m, x, e.seen) {
			return nil, false
		}
		if e.ctx.condExtends(m, &soltype.NegationType{Inner: x}, e.seen) {
			continue
		}
		overlapping = append(overlapping, &soltype.NegationType{Inner: x})
	}
	if len(overlapping) == 0 {
		return m, true
	}
	return newIntersection(nil, append([]soltype.Type{m}, overlapping...)), true
}

// groundDifference reports whether a difference's operands are concrete enough to settle. Every
// member of the positive side is asked two subtype questions per excluded type, and neither can be
// decided over a type variable or an unreduced operator.
func groundDifference(base soltype.Type, excluded []soltype.Type) bool {
	if !condOperandGround(base) {
		return false
	}
	for _, x := range excluded {
		if !condOperandGround(x) {
			return false
		}
	}
	return true
}

// complementsOf wraps each type in a complement, rebuilding the negated half of a difference the
// reduction left standing.
func complementsOf(types []soltype.Type) []soltype.Type {
	out := make([]soltype.Type, len(types))
	for i, t := range types {
		out[i] = &soltype.NegationType{Inner: t}
	}
	return out
}

// nativeDifference rewrites a distributive conditional that filters its own operand into the set
// difference or intersection it denotes, and reports whether the conditional had that shape.
//
//	if U : V { never } else { U }   ⟹  U ∩ ¬V
//	if U : V { U } else { never }   ⟹  U ∩ V
//
// Those are `Exclude<U, V>` and `Extract<U, V>` as TypeScript's library writes them, and the same
// two shapes back `NonNullable<T>`, whose V is `null | undefined`, and `Omit<T, Ks>`, whose key set
// filters `keyof T` through the first one.
//
// reduceCond calls this only for a conditional it could not decide, so the rewrite fires exactly
// where the filter had no answer. The header of this file states why the two readings may then
// diverge and why the divergence is not reachable from a ground operand.
//
// The conditional must be distributive, which is to say its Check was written as a bare type
// parameter. That is the shape whose branches read the operand member by member, and it is what
// makes the filter a set operation rather than a single yes-or-no test over the whole operand. A
// conditional over any other Check keeps its own semantics and stays symbolic.
//
// A branch is compared against the Check as the source wrote it, since both are reduced from the
// same node and a naked type parameter reduces to itself.
func (e *typeEvaluator) nativeDifference(t *soltype.CondType, check, extends soltype.Type) (soltype.Type, bool) {
	if !t.Distribute || containsInfer(check) || containsInfer(extends) {
		return nil, false
	}
	switch {
	case isNever(t.Then) && equalType(t.Check, t.Else):
		return newIntersection(nil, []soltype.Type{check, &soltype.NegationType{Inner: extends}}), true
	case isNever(t.Else) && equalType(t.Check, t.Then):
		return newIntersection(nil, []soltype.Type{check, extends}), true
	}
	return nil, false
}

// positiveSkeleton returns the part of a conditional's Check an `infer` pattern matches against,
// and reports whether any of it is left to match. A pattern binds a capture by aligning against
// structure the scrutinee carries, and a complement names the values its operand rejects rather
// than any structure of its own. So `¬X` matches no pattern position, and a meet contributes only
// its positive members: `(Array<number> ∩ ¬X) extends Array<infer R>` binds R to number.
//
// A Check that is nothing but complements has no skeleton to align, so the match fails and the
// conditional takes its Else branch.
func positiveSkeleton(check soltype.Type) (soltype.Type, bool) {
	switch t := check.(type) {
	case *soltype.NegationType:
		return nil, false
	case *soltype.IntersectionType:
		positives := make([]soltype.Type, 0, len(t.Types))
		for _, m := range t.Types {
			if _, negated := m.(*soltype.NegationType); !negated {
				positives = append(positives, m)
			}
		}
		if len(positives) == 0 {
			return nil, false
		}
		if len(positives) == len(t.Types) {
			return check, true
		}
		return newIntersection(nil, positives), true
	default:
		return check, true
	}
}

// isNever reports whether t is the empty type, the branch a set-difference conditional drops its
// operand through.
func isNever(t soltype.Type) bool {
	_, ok := t.(*soltype.NeverType)
	return ok
}

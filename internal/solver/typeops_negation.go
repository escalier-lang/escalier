package solver

import (
	"slices"

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

// reduceIntersection reduces a meet. A meet carrying a complement is a set difference, which
// reduceDifference settles. Any other meet has its members reduced in place, so a member that is
// itself an operator reaches its value. `"a" ∩ keyof {a: number, b: string}` reduces to `"a"`. That
// meet is the shape keyofUnion mints for a union operand it could not read every member of.
//
// A meet that neither folds to a key set nor has any member reduce comes back untouched, keeping
// its pointer, so a plain intersection of concrete types costs nothing.
func (e *typeEvaluator) reduceIntersection(t *soltype.IntersectionType) soltype.Type {
	reduced := make([]soltype.Type, len(t.Types))
	changed := false
	for i, m := range t.Types {
		reduced[i] = e.reduce(m)
		changed = changed || reduced[i] != m
	}
	// A member may have reduced to a difference of its own, as an `Exclude<T, string>` member does,
	// so the members are spliced together before the meet is read for a complement.
	flat := flattenIntersection(reduced)
	if slices.ContainsFunc(flat, isNegation) {
		return e.reduceDifference(flat)
	}
	if folded, ok := meetKeySets(flat); ok {
		return folded
	}
	if !changed {
		return t
	}
	return newIntersection(nil, flat)
}

// meetKeySets folds a meet whose every member is an enumerable key set into the keys they share,
// and reports whether it had that shape. Take `keyof (T | {a: number, b: string})`, which reduces to
// `"a" ∩ keyof T`. Once T grounds to an object carrying "a" and "b", that meet reads
// `"a" ∩ ("a" | "b")`. Folding it to `"a"` is what leaves a mapped type over it a key to iterate.
//
// A member with an open key set stops the fold. Such a set names some of its keys and leaves the
// rest to a tail, so a key the other members name may or may not be among them, and the meet is
// undecided. The whole meet stays as it is in that case, which is what an undecided key set reduces
// to elsewhere too.
func meetKeySets(members []soltype.Type) (soltype.Type, bool) {
	if len(members) < 2 {
		return nil, false
	}
	var shared []soltype.Type
	for i, m := range members {
		keys, inexact, ok := literalKeys(m)
		if !ok || inexact {
			return nil, false
		}
		if i == 0 {
			// literalKeys hands back the member's own slice, and newUnion sorts its input in
			// place, so seed the accumulator with a copy.
			shared = append([]soltype.Type(nil), keys...)
			continue
		}
		shared = intersectTypes(shared, keys)
	}
	return newUnion(nil, shared, false), true
}

// reduceDifference settles a meet carrying at least one complement — `A ∩ ¬B`, the values of A that
// are not values of B. It is the reduction behind every Boolean key set: `Omit<T, "b">` maps over
// `keyof T ∩ ¬"b"`, which reduces to `"a" | "c"` for `type T = {a: X, b: Y, c: Z}` so the mapped
// type has keys to iterate.
//
// Each member of the meet is already reduced and grounds through groundReduced, so an alias named
// on either side expands to the type whose values the difference is taken over.
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
// An inexact positive side names only some of its members. The rest sit in an open tail, and what
// the exclusion removes from those cannot be worked out. The result union keeps the tail, which
// stands for whatever those undecided members contribute, so `("a" | "b" | ...) ∩ ¬"a"` reduces to
// `"b" | ...`. When no named member survives, there is no union left to carry the tail, since an
// empty union is `never` however the marker is set. The difference then stays as it stands rather
// than claiming the tail is empty too.
func (e *typeEvaluator) reduceDifference(members []soltype.Type) soltype.Type {
	positives := make([]soltype.Type, 0, len(members))
	var excluded []soltype.Type
	for _, m := range members {
		if neg, ok := m.(*soltype.NegationType); ok {
			excluded = append(excluded, e.groundReduced(neg.Inner))
			continue
		}
		positives = append(positives, e.groundReduced(m))
	}
	base, folded := meetKeySets(positives)
	if !folded {
		base = newIntersection(nil, positives)
	}
	unreduced := func() soltype.Type {
		return newIntersection(nil, append([]soltype.Type{base}, complementsOf(excluded)...))
	}
	if !groundDifference(base, excluded) {
		return unreduced()
	}
	baseMembers, inexact := unionMembers(base)
	survivors := make([]soltype.Type, 0, len(baseMembers))
	for _, m := range baseMembers {
		if narrowed, keep := e.excludeFrom(m, excluded); keep {
			survivors = append(survivors, narrowed)
		}
	}
	if len(survivors) == 0 && inexact {
		return unreduced()
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
// The shape test reads the stored branches and the stored Check rather than their reduced forms,
// so it states what the source wrote. The result is built from the reduced operands.
func (e *typeEvaluator) nativeDifference(t *soltype.CondType, check, extends soltype.Type) (soltype.Type, bool) {
	if !t.Distribute || containsInfer(check) || containsInfer(extends) {
		return nil, false
	}
	switch {
	case isNeverType(t.Then) && equalType(t.Check, t.Else):
		return newIntersection(nil, []soltype.Type{check, &soltype.NegationType{Inner: extends}}), true
	case isNeverType(t.Else) && equalType(t.Check, t.Then):
		return newIntersection(nil, []soltype.Type{check, extends}), true
	}
	return nil, false
}

// positiveSkeleton returns the part of a conditional's Check an `infer` pattern matches against,
// and reports whether any of it is left to match. A pattern binds a capture by aligning against
// structure the scrutinee carries, and a complement names the values its operand rejects rather
// than any structure of its own. So `¬X` matches no pattern position, and a meet contributes only
// its positive members. Matching `Array<number> ∩ ¬X` against the pattern `Array<infer R>` binds R
// to number.
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

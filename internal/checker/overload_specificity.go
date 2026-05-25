package checker

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// specificity is the result of comparing two overload arms. Its
// underlying int values match the sort.Interface convention so it
// can be compared with `< 0` / `> 0` directly.
type specificity int

const (
	aMoreSpecific specificity = -1
	tie           specificity = 0
	bMoreSpecific specificity = 1
)

// compareOverloadArms compares two overload arms by specificity.
// Returns aMoreSpecific when a is strictly more specific than b,
// bMoreSpecific when b is strictly more specific than a, and tie
// when the two arms are considered equally specific (callers should
// use a stable sort so declared source order is preserved on ties).
//
// Rules in descending priority — checked in order, the first
// discriminating rule wins:
//
//  1. **Literal-typed params before non-literal.** Count the number
//     of *LitType params (after Prune) in each arm; the arm with
//     more literal params is more specific. Handles e.g.
//     `createElement(tag: "canvas")` vs.
//     `createElement(tag: string)`.
//
//  2. **Bounded generics before unbounded.** Count the number of
//     type params whose Constraint is non-nil and not NeverType.
//     A bounded `<K: keyof T>` ranks ahead of an unbounded `<T>`
//     or a fallback like `(x: string)`.
//
//  3. **Fewer required params is more specific.** Optional params
//     (FuncParam.Optional) and rest params (*RestSpreadType) do
//     not count as required. Matches TS's "more required args
//     before fewer" rule.
//
//  4. **Fewer type-param-typed params is more specific.** Count
//     params whose top-level type is a `TypeRefType` to one of
//     the fn's own type params. A concrete `(value: string)` ranks
//     ahead of a generic `<T>(value: T)` because `T` provides no
//     constraint on what the caller may pass.
//
//  5. Source order tiebreaker. Returns 0 here; the caller's stable
//     sort preserves declared order.
//
// This comparator is used both for free-fn overload arms (sorted
// when the intersection is constructed in infer_module.go /
// generalize.go) and for the PR-C method-elem merge pass. Keeping
// a single comparator means free-fn and method overload dispatch
// have identical semantics.
//
// TODO(#655): rules 1–2 are a top-level literal-count heuristic and
// miss unions, object fields, and other nested narrowing. Replace
// with a subtype-based comparison once §4.6 lands.
func compareOverloadArms(a, b *type_system.FuncType) specificity {
	if a == nil || b == nil {
		// Nil sorts last — should never happen with well-formed
		// intersections, but defensive against malformed input.
		if a == nil && b == nil {
			return tie
		}
		if a == nil {
			return bMoreSpecific
		}
		return aMoreSpecific
	}

	aLits, bLits := countLiteralParams(a), countLiteralParams(b)
	if aLits != bLits {
		if aLits > bLits {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	aBounded, bBounded := countBoundedTypeParams(a), countBoundedTypeParams(b)
	if aBounded != bBounded {
		if aBounded > bBounded {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	aReq, bReq := countRequiredParams(a), countRequiredParams(b)
	if aReq != bReq {
		if aReq < bReq {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	aTPRefs, bTPRefs := countTypeParamRefParams(a), countTypeParamRefParams(b)
	if aTPRefs != bTPRefs {
		if aTPRefs < bTPRefs {
			return aMoreSpecific
		}
		return bMoreSpecific
	}

	return tie
}

// sortOverloadArms returns the arms ordered most-specific-first
// using compareOverloadArms. Sort is stable, so arms that compare
// equal preserve their input order — the declared-source-order
// tiebreaker for rule 4. Returns the input slice (sorted in place);
// callers that need to preserve the original slice should clone
// before calling.
func sortOverloadArms(arms []*type_system.FuncType) []*type_system.FuncType {
	sort.SliceStable(arms, func(i, j int) bool {
		return compareOverloadArms(arms[i], arms[j]) < 0
	})
	return arms
}

func countLiteralParams(fn *type_system.FuncType) int {
	n := 0
	for _, p := range fn.Params {
		if _, ok := type_system.Prune(p.Type).(*type_system.LitType); ok {
			n++
		}
	}
	return n
}

func countBoundedTypeParams(fn *type_system.FuncType) int {
	n := 0
	for _, tp := range fn.TypeParams {
		if tp.Constraint == nil {
			continue
		}
		if _, isNever := type_system.Prune(tp.Constraint).(*type_system.NeverType); isNever {
			continue
		}
		n++
	}
	return n
}

// countTypeParamRefParams counts params whose top-level type (after Prune)
// is a TypeRefType naming one of fn's own type params. Used by rule 3 to
// rank concrete-typed params ahead of generic ones.
func countTypeParamRefParams(fn *type_system.FuncType) int {
	if len(fn.TypeParams) == 0 {
		return 0
	}
	tpNames := make(map[string]struct{}, len(fn.TypeParams))
	for _, tp := range fn.TypeParams {
		tpNames[tp.Name] = struct{}{}
	}
	n := 0
	for _, p := range fn.Params {
		ref, ok := type_system.Prune(p.Type).(*type_system.TypeRefType)
		if !ok {
			continue
		}
		ident, ok := ref.Name.(*type_system.Ident)
		if !ok {
			continue
		}
		if _, ok := tpNames[ident.Name]; ok {
			n++
		}
	}
	return n
}

func countRequiredParams(fn *type_system.FuncType) int {
	n := 0
	for _, p := range fn.Params {
		if p.Optional {
			continue
		}
		if _, isRest := type_system.Prune(p.Type).(*type_system.RestSpreadType); isRest {
			continue
		}
		n++
	}
	return n
}

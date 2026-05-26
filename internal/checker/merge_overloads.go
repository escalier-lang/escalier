package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// declSpan extracts a usable span for a declaration-time diagnostic
// (e.g. overload merging) from a Provenance. Returns DEFAULT_SPAN when
// the provenance does not carry a node.
func declSpan(p provenance.Provenance) ast.Span {
	if node := GetNode(p); node != nil {
		return node.Span()
	}
	return DEFAULT_SPAN
}

// OverloadReceiverMutMismatchError is reported when two same-named
// method arms inside a single class / interface / declare-class /
// declare-interface body declare different receiver shapes (one
// `self`, the other `mut self`). Overload resolution dispatches on
// argument shape only; allowing the receiver shape to vary across
// arms would force callers to know the dispatch outcome before they
// know whether the call needs a `mut` binding.
//
// The mismatched arm is dropped from the merged signature; the first
// arm's receiver shape wins. Subsequent reads of the method see only
// the surviving arms.
type OverloadReceiverMutMismatchError struct {
	Name          string
	FirstReceiver string // "self" or "mut self"
	OtherReceiver string // "self" or "mut self"
	span          ast.Span
}

func (e OverloadReceiverMutMismatchError) Span() ast.Span {
	return e.span
}
func (e OverloadReceiverMutMismatchError) Message() string {
	return "Method '" + e.Name + "' overload arms disagree on receiver shape: " +
		"first arm declares `" + e.FirstReceiver + "`, but a later arm declares `" +
		e.OtherReceiver + "`. All overload arms must share the same receiver shape."
}

// MergeMethodOverloads scans elems for same-named MethodElems and
// collapses them into a single MethodElem whose Signatures slice carries
// each overload arm in most-specific-first order (per
// compareOverloadArms / sortOverloadArms).
//
// PropertyElem, GetterElem, SetterElem, ConstructorElem, CallableElem,
// IndexSignatureElem, RestSpreadElem, and MappedElem pass through
// untouched. A PropertyElem and a MethodElem sharing a name is a
// pre-existing checker concern and is left alone here.
//
// Receiver mutability must be uniform across arms. A mismatched arm is
// dropped from the merged signature (preserving the first arm's
// receiver shape so downstream code still type-checks) and an
// OverloadReceiverMutMismatchError is reported at `span`.
//
// The merged MethodElem replaces the first occurrence in `elems`;
// later occurrences are removed. Non-overload-bearing inputs round-
// trip unchanged.
func (c *Checker) MergeMethodOverloads(elems []type_system.ObjTypeElem, span ast.Span) ([]type_system.ObjTypeElem, []Error) {
	indicesByName := map[type_system.ObjTypeKey][]int{}
	for i, e := range elems {
		if me, ok := e.(*type_system.MethodElem); ok {
			indicesByName[me.Name] = append(indicesByName[me.Name], i)
		}
	}

	hasDuplicates := false
	for _, idxs := range indicesByName {
		if len(idxs) > 1 {
			hasDuplicates = true
			break
		}
	}
	if !hasDuplicates {
		return elems, nil
	}

	var errors []Error
	// mergedAt records, for each "first occurrence" index in `elems`,
	// the multi-arm MethodElem that should replace it in the output.
	mergedAt := map[int]*type_system.MethodElem{}
	// dropIdx records the trailing same-named MethodElem indices whose
	// signatures have been folded into the merged elem at the first
	// occurrence (or rejected by the receiver-mutability check). The
	// output pass at the bottom of the function skips these indices so
	// each name appears once.
	dropIdx := set.NewSet[int]()

	// Drive the merge by elem order rather than ranging over the map so
	// errors and merged-elem placement are deterministic (Go map
	// iteration order is randomized).
	seen := set.NewSet[type_system.ObjTypeKey]()
	for _, e := range elems {
		me, ok := e.(*type_system.MethodElem)
		if !ok {
			continue
		}
		if seen.Contains(me.Name) {
			continue
		}
		seen.Add(me.Name)
		idxs := indicesByName[me.Name]
		if len(idxs) < 2 {
			continue
		}
		firstMe := elems[idxs[0]].(*type_system.MethodElem)
		// This pass runs pre-merge: every input MethodElem has exactly
		// one arm. Inspecting Signatures[0] is always safe here.
		firstSig := firstMe.Signatures[0]
		firstMut := type_system.ReceiverIsMut(firstSig)
		arms := []*type_system.FuncType{firstSig}

		for _, j := range idxs[1:] {
			me := elems[j].(*type_system.MethodElem)
			sig := me.Signatures[0]
			armMut := type_system.ReceiverIsMut(sig)
			dropIdx.Add(j)
			if armMut != firstMut {
				errors = append(errors, OverloadReceiverMutMismatchError{
					Name:          firstMe.Name.String(),
					FirstReceiver: receiverShapeLabel(firstMut),
					OtherReceiver: receiverShapeLabel(armMut),
					span:          armSpanOr(sig, span),
				})
				continue
			}
			arms = append(arms, sig)
		}

		c.sortOverloadArms(arms)
		mergedAt[idxs[0]] = &type_system.MethodElem{
			Name:       firstMe.Name,
			Signatures: arms,
		}
	}

	out := make([]type_system.ObjTypeElem, 0, len(elems))
	for i, e := range elems {
		if dropIdx.Contains(i) {
			continue
		}
		if me, ok := mergedAt[i]; ok {
			out = append(out, me)
			continue
		}
		out = append(out, e)
	}
	return out, errors
}

// armSpanOr returns the span of the AST node backing `sig` (when the
// FuncType carries node provenance) or `fallback` otherwise. Used to
// point overload-merging diagnostics at the offending arm instead of
// the enclosing decl.
func armSpanOr(sig *type_system.FuncType, fallback ast.Span) ast.Span {
	if node := GetNode(sig.Provenance()); node != nil {
		return node.Span()
	}
	return fallback
}

func receiverShapeLabel(mut bool) string {
	if mut {
		return "mut self"
	}
	return "self"
}

package interop

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// CheckSet verifies overload-set shape: counts match and each override
// signature is equivalent to the original at the same index. Called
// from merge.go once per method/function slot.
//
// On mismatched counts, no per-signature checks run (there's no
// defensible pairing) — the user must redeclare the full set.
func CheckSet(override, original []*type_system.FuncType, path Path, origin Origin) error {
	if len(override) != len(original) {
		return &ErrSignatureMismatch{
			Path:           path,
			Field:          "overload count",
			Override:       fmt.Sprintf("%d", len(override)),
			Original:       fmt.Sprintf("%d", len(original)),
			OverrideOrigin: origin,
		}
	}
	bracket := len(override) > 1
	for i := range override {
		field, ok := funcSignatureEquivalent(override[i], original[i])
		if ok {
			continue
		}
		if bracket {
			field = fmt.Sprintf("overload[%d]/%s", i, field)
		}
		return &ErrSignatureMismatch{
			Path:           path,
			Field:          field,
			Override:       override[i].String(),
			Original:       original[i].String(),
			OverrideOrigin: origin,
		}
	}
	return nil
}

// Check is the per-signature helper used by CheckSet and exposed
// directly for the §10 implements check.
func Check(override, original *type_system.FuncType, path Path, origin Origin) error {
	field, ok := funcSignatureEquivalent(override, original)
	if ok {
		return nil
	}
	return &ErrSignatureMismatch{
		Path:           path,
		Field:          field,
		Override:       override.String(),
		Original:       original.String(),
		OverrideOrigin: origin,
	}
}

// funcSignatureEquivalent compares two FuncTypes for the consistency
// contract: arity (excluding any `this`/`self` receiver), per-position
// non-receiver param types and Optional flag, and return type.
// Parameter names are ignored. SelfParam mode is intentionally excluded — that's the field
// the override is allowed to change. Type parameters must match in
// arity, and in the per-pair comparison their declared constraints
// and defaults are compared structurally. Lifetime parameters are not
// compared: TypeScript has no lifetime syntax, so overrides will
// routinely add lifetimes the original lacks.
func funcSignatureEquivalent(a, b *type_system.FuncType) (field string, ok bool) {
	if a == nil || b == nil {
		return "nilFunc", a == b
	}

	// Per-signature generics arity.
	if len(a.TypeParams) != len(b.TypeParams) {
		return "typeParams", false
	}
	for i := range a.TypeParams {
		ap, bp := a.TypeParams[i], b.TypeParams[i]
		if (ap.Constraint == nil) != (bp.Constraint == nil) {
			return fmt.Sprintf("typeParam[%d]/constraint", i), false
		}
		if ap.Constraint != nil && !ap.Constraint.Equals(bp.Constraint) {
			return fmt.Sprintf("typeParam[%d]/constraint", i), false
		}
		if (ap.Default == nil) != (bp.Default == nil) {
			return fmt.Sprintf("typeParam[%d]/default", i), false
		}
		if ap.Default != nil && !ap.Default.Equals(bp.Default) {
			return fmt.Sprintf("typeParam[%d]/default", i), false
		}
	}
	if len(a.Params) != len(b.Params) {
		return "arity", false
	}
	// Param types are compared via Type.Equals — which is strict on
	// lifetime annotations. Overrides that add lifetimes to nested
	// FuncType-valued params relative to a TS-derived original will
	// therefore mismatch; a lifetime-erased equivalence variant for
	// the override path is tracked in §5.13.
	for i := range a.Params {
		if a.Params[i].Optional != b.Params[i].Optional {
			return fmt.Sprintf("param[%d]", i), false
		}
		if a.Params[i].Type == nil || b.Params[i].Type == nil {
			if a.Params[i].Type != b.Params[i].Type {
				return fmt.Sprintf("param[%d]", i), false
			}
			continue
		}
		if !a.Params[i].Type.Equals(b.Params[i].Type) {
			return fmt.Sprintf("param[%d]", i), false
		}
	}
	if (a.Return == nil) != (b.Return == nil) {
		return "return", false
	}
	if a.Return != nil && !a.Return.Equals(b.Return) {
		return "return", false
	}
	return "", true
}

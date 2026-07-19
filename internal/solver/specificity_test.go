package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6 PR2.5: shared specificity ordering for the trial-and-commit sites ---
//
// The overload resolver (overloadOrder), the IntersectionType-sub exists arm, and the
// UnionType-super exists arm all order their candidate members through specificityOrder,
// so most-specific-first is the one ordering rule every trial site shares. These tests
// pin the ordering primitive directly, independent of any single trial site. The
// observable end-to-end payoff at the overload site lives in infer_overload_test.go
// (TestInferOverloadSpecificityBeatsDeclarationOrder and TestInferOverloadThreeArmSpecificity).

// typeSpecificity ranks a literal below its primitive, a concrete type below a variable,
// and two functions parameter-wise; disjoint shapes and equal types tie.
func TestTypeSpecificity(t *testing.T) {
	v := &soltype.TypeVarType{}
	fnLit := &soltype.FuncType{Params: []*soltype.FuncParam{{Type: numLit(1)}}, Ret: num()}
	fnPrim := &soltype.FuncType{Params: []*soltype.FuncParam{{Type: num()}}, Ret: num()}

	tests := []struct {
		name     string
		a, b     soltype.Type
		expected int
	}{
		{"literal more specific than its primitive", numLit(1), num(), -1},
		{"primitive less specific than its literal", num(), numLit(1), 1},
		{"concrete more specific than a variable", num(), v, -1},
		{"variable less specific than a concrete", v, num(), 1},
		{"disjoint primitives tie", num(), str(), 0},
		{"equal types tie", num(), num(), 0},
		{"function with a literal param outranks one with the primitive", fnLit, fnPrim, -1},
		{"function with the primitive param is outranked", fnPrim, fnLit, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, typeSpecificity(tt.a, tt.b))
		})
	}
}

// specificityOrder lists candidates most-specific-first, and adding a less-specific
// candidate does not change which candidate ranks first. This is the property the
// trial-and-commit sites rely on: a new, less-specific union member or overload arm never
// steals the win from a more-specific one.
func TestSpecificityOrderStability(t *testing.T) {
	// Most-specific-first: the literal outranks its primitive whatever the input order.
	require.Equal(t, []int{1, 0}, specificityOrder([]soltype.Type{num(), numLit(1)}))
	require.Equal(t, []int{0, 1}, specificityOrder([]soltype.Type{numLit(1), num()}))

	// Adding a variable, which every concrete candidate outranks, leaves the literal first
	// and the primitive second; the variable sorts last. The literal's win is unchanged.
	v := &soltype.TypeVarType{}
	require.Equal(t, []int{1, 0, 2}, specificityOrder([]soltype.Type{num(), numLit(1), v}))

	// A nil candidate, a non-function overload arm, sorts last without disturbing the rest.
	require.Equal(t, []int{1, 0, 2}, specificityOrder([]soltype.Type{num(), numLit(1), nil}))

	// A union of a concrete and a bare variable orders the concrete first and the variable
	// last. The union-super exists rule reads this order directly, so a concrete member is
	// trialled before the variable and the variable is only reached as a last-resort
	// catch-all.
	v2 := &soltype.TypeVarType{}
	require.Equal(t, []int{1, 0}, specificityOrder([]soltype.Type{v2, num()}))
}

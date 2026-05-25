package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// Helpers ----------------------------------------------------------------

func mkParam(t type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{
		Pattern: type_system.NewIdentPat("p"),
		Type:    t,
	}
}

func mkOptParam(t type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{
		Pattern:  type_system.NewIdentPat("p"),
		Type:     t,
		Optional: true,
	}
}

func mkRestParam(elem type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{
		Pattern: type_system.NewIdentPat("rest"),
		Type:    type_system.NewRestSpreadType(nil, elem),
	}
}

func mkFn(typeParams []*type_system.TypeParam, params []*type_system.FuncParam) *type_system.FuncType {
	return type_system.NewFuncType(
		nil,
		typeParams,
		params,
		type_system.NewVoidType(nil),
		type_system.NewNeverType(nil),
	)
}

func TestCompareOverloadArms(t *testing.T) {
	str := type_system.NewStrPrimType(nil)
	num := type_system.NewNumPrimType(nil)
	canvasLit := type_system.NewStrLitType(nil, "canvas")
	divLit := type_system.NewStrLitType(nil, "div")

	tests := []struct {
		name string
		a    *type_system.FuncType
		b    *type_system.FuncType
		want specificity
	}{
		// Rule 1: literal-typed params before non-literal.
		{
			name: "literal vs string param: literal wins",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: aMoreSpecific,
		},
		{
			name: "string vs literal param: literal wins (swapped)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			want: bMoreSpecific,
		},
		{
			name: "two literals beats one literal",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit), mkParam(divLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit), mkParam(str)}),
			want: aMoreSpecific,
		},
		// Subtype check handles unions and nested literals
		// the old literal-count heuristic missed.
		{
			name: "union of literals beats string prim",
			a: mkFn(nil, []*type_system.FuncParam{mkParam(
				type_system.NewUnionType(nil, canvasLit, divLit),
			)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: aMoreSpecific,
		},
		{
			name: "object with literal field beats object with prim field",
			a: mkFn(nil, []*type_system.FuncParam{mkParam(
				type_system.NewObjectType(nil, []type_system.ObjTypeElem{
					&type_system.PropertyElem{Name: type_system.NewStrKey("kind"), Value: canvasLit},
				}),
			)}),
			b: mkFn(nil, []*type_system.FuncParam{mkParam(
				type_system.NewObjectType(nil, []type_system.ObjTypeElem{
					&type_system.PropertyElem{Name: type_system.NewStrKey("kind"), Value: str},
				}),
			)}),
			want: aMoreSpecific,
		},
		{
			name: "tuple of literals beats tuple of prims",
			a: mkFn(nil, []*type_system.FuncParam{mkParam(
				type_system.NewTupleType(nil, canvasLit, divLit),
			)}),
			b: mkFn(nil, []*type_system.FuncParam{mkParam(
				type_system.NewTupleType(nil, str, str),
			)}),
			want: aMoreSpecific,
		},
		{
			// Disjoint literal tags are incomparable under subtype
			// (neither <: the other) and source order breaks the tie.
			name: "disjoint literal arms tie under subtype",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(divLit)}),
			want: tie,
		},
		{
			// Bounded TP substitutes its constraint; unbounded TP is top.
			// `string <: top` and not vice versa, so bounded wins.
			name: "bounded <K: string> beats unbounded <T>",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: str}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))},
			),
			want: aMoreSpecific,
		},
		{
			// Never constraint is treated as unbounded, matching the
			// pre-§4.6 comparator's behavior.
			name: "<K: never> ties with <T> (both treated as top)",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: type_system.NewNeverType(nil)}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))},
			),
			want: tie,
		},
		{
			// Two bounded TPs compare by their constraints.
			// `"canvas" <: string` so the canvas-bounded arm wins.
			name: "<K: \"canvas\"> beats <L: string>",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: canvasLit}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "L", Constraint: str}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "L", nil))},
			),
			want: aMoreSpecific,
		},

		// Rule 2: fewer required params is more specific.
		{
			name: "one required param beats two required params",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkParam(num)}),
			want: aMoreSpecific,
		},
		{
			name: "optional params do not count toward required",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkOptParam(num)}),
			want: tie,
		},
		{
			name: "rest params do not count toward required",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkRestParam(num)}),
			want: tie,
		},

		// Rule 3: concrete-typed params beat type-param-typed params.
		{
			name: "concrete (value: string) beats generic <T>(value: T)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))},
			),
			want: aMoreSpecific,
		},
		{
			name: "rule 3 ignores type params that aren't referenced by any param",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(str)},
			),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: tie,
		},

		// Rule 4: source order tiebreaker — comparator returns 0,
		// stable sort handles the rest.
		{
			// Duplicate-decl detection (#654) will reject identical signatures
			// at the merge site, so this input won't reach the comparator
			// from there. It remains valid coverage for sortOverloadArms,
			// which can be handed arbitrary FuncType slices.
			name: "identical arms tie (source order tiebreaker)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: tie,
		},

		// Nil defensiveness.
		{
			name: "nil sorts last (a nil)",
			a:    nil,
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: bMoreSpecific,
		},
		{
			name: "nil sorts last (b nil)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    nil,
			want: aMoreSpecific,
		},
		{
			name: "two nils tie",
			a:    nil,
			b:    nil,
			want: tie,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareOverloadArms(tt.a, tt.b)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSortOverloadArms_StableSourceOrderOnTies(t *testing.T) {
	str := type_system.NewStrPrimType(nil)
	canvasLit := type_system.NewStrLitType(nil, "canvas")
	divLit := type_system.NewStrLitType(nil, "div")

	// Three arms: two same-shape literal arms (tied) and one string arm.
	// Sort should put both literal arms before the string arm, and
	// preserve declared order between the two literal arms.
	canvasArm := mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)})
	divArm := mkFn(nil, []*type_system.FuncParam{mkParam(divLit)})
	strArm := mkFn(nil, []*type_system.FuncParam{mkParam(str)})

	arms := []*type_system.FuncType{strArm, canvasArm, divArm}
	sorted := sortOverloadArms(arms)

	require.Len(t, sorted, 3)
	require.Same(t, canvasArm, sorted[0], "canvas literal arm declared before div, must come first")
	require.Same(t, divArm, sorted[1], "div literal arm comes after canvas, before string fallback")
	require.Same(t, strArm, sorted[2], "string fallback is least specific")
}

func TestSortOverloadArms_AcrossAllRules(t *testing.T) {
	str := type_system.NewStrPrimType(nil)
	num := type_system.NewNumPrimType(nil)
	canvasLit := type_system.NewStrLitType(nil, "canvas")

	// Four arms covering each rule, declared in least-specific-first
	// order so the sort actually has to do work.
	twoRequired := mkFn(nil, []*type_system.FuncParam{mkParam(str), mkParam(num)})        // 2 required
	unboundedGeneric := mkFn(                                                             // 1 required, 1 TP-ref
		[]*type_system.TypeParam{{Name: "T"}},
		[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))},
	)
	oneRequiredStr := mkFn(nil, []*type_system.FuncParam{mkParam(str)})   // 1 required, 0 TP-ref
	literalArm := mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}) // subtype-narrower than oneRequiredStr

	arms := []*type_system.FuncType{twoRequired, unboundedGeneric, oneRequiredStr, literalArm}
	sorted := sortOverloadArms(arms)

	// Expected most-specific-first order:
	//   1. literalArm        (rule 1 subtype: "canvas" <: string)
	//   2. oneRequiredStr    (rule 3: 0 TP refs beats unboundedGeneric)
	//   3. unboundedGeneric  (rule 2 ties with oneRequiredStr; rule 3 loses)
	//   4. twoRequired       (rule 2: 2 required, least specific)
	require.Same(t, literalArm, sorted[0])
	require.Same(t, oneRequiredStr, sorted[1])
	require.Same(t, unboundedGeneric, sorted[2])
	require.Same(t, twoRequired, sorted[3])
}

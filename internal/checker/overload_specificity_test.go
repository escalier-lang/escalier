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
		want int // -1 a more specific; 1 b more specific; 0 tie
	}{
		// Rule 1: literal-typed params before non-literal.
		{
			name: "literal vs string param: literal wins",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: -1,
		},
		{
			name: "string vs literal param: literal wins (swapped)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			want: 1,
		},
		{
			name: "two literals beats one literal",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit), mkParam(divLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit), mkParam(str)}),
			want: -1,
		},
		{
			name: "two literal arms compare equal under rule 1 (same count)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(divLit)}),
			want: 0,
		},

		// Rule 2: bounded generics before unbounded.
		{
			name: "bounded <K: string> beats unbounded <T>",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: str}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))},
			),
			want: -1,
		},
		{
			name: "bounded type param with NeverType constraint counts as unbounded",
			a: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: str}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: type_system.NewNeverType(nil)}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			want: -1,
		},
		{
			name: "rule 2 only applies when rule 1 ties",
			// a has unbounded generic but a literal param; b has bounded generic
			// but a non-literal param. Rule 1 (more literals) picks a first.
			a: mkFn(
				[]*type_system.TypeParam{{Name: "T"}},
				[]*type_system.FuncParam{mkParam(canvasLit)},
			),
			b: mkFn(
				[]*type_system.TypeParam{{Name: "K", Constraint: str}},
				[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
			),
			want: -1,
		},

		// Rule 3: fewer required params is more specific.
		{
			name: "one required param beats two required params",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkParam(num)}),
			want: -1,
		},
		{
			name: "optional params do not count toward required",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkOptParam(num)}),
			want: 0,
		},
		{
			name: "rest params do not count toward required",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str), mkRestParam(num)}),
			want: 0,
		},

		// Rule 4: source order tiebreaker — comparator returns 0,
		// stable sort handles the rest.
		{
			name: "identical arms tie (source order tiebreaker)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: 0,
		},

		// Nil defensiveness.
		{
			name: "nil sorts last (a nil)",
			a:    nil,
			b:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			want: 1,
		},
		{
			name: "nil sorts last (b nil)",
			a:    mkFn(nil, []*type_system.FuncParam{mkParam(str)}),
			b:    nil,
			want: -1,
		},
		{
			name: "two nils tie",
			a:    nil,
			b:    nil,
			want: 0,
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

	// Five arms covering each rule, declared in least-specific to
	// most-specific order so the sort actually has to do work.
	twoRequired := mkFn(nil, []*type_system.FuncParam{mkParam(str), mkParam(num)})                                               // 0 lit, 0 bounded, 2 req
	oneRequiredStr := mkFn(nil, []*type_system.FuncParam{mkParam(str)})                                                          // 0 lit, 0 bounded, 1 req
	unboundedGeneric := mkFn([]*type_system.TypeParam{{Name: "T"}}, []*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "T", nil))}) // 0 lit, 0 bounded, 1 req (tied with above on rules 1-3)
	boundedGeneric := mkFn(
		[]*type_system.TypeParam{{Name: "K", Constraint: str}},
		[]*type_system.FuncParam{mkParam(type_system.NewTypeRefType(nil, "K", nil))},
	) // 0 lit, 1 bounded, 1 req
	literalArm := mkFn(nil, []*type_system.FuncParam{mkParam(canvasLit)}) // 1 lit, ...

	arms := []*type_system.FuncType{twoRequired, oneRequiredStr, unboundedGeneric, boundedGeneric, literalArm}
	sorted := sortOverloadArms(arms)

	// Expected most-specific-first order:
	//   1. literalArm        (rule 1 wins)
	//   2. boundedGeneric    (rule 2 wins among the rest)
	//   3. oneRequiredStr    (rule 3: 1 required, declared before unboundedGeneric)
	//   4. unboundedGeneric  (rule 3: 1 required, ties with above → source order)
	//   5. twoRequired       (rule 3: 2 required)
	require.Same(t, literalArm, sorted[0])
	require.Same(t, boundedGeneric, sorted[1])
	require.Same(t, oneRequiredStr, sorted[2])
	require.Same(t, unboundedGeneric, sorted[3])
	require.Same(t, twoRequired, sorted[4])
}

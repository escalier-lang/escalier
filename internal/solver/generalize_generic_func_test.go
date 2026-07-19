package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A generic FuncType round-trips through value-binding generalization: generalize
// coalesces the binding var whose lower bound is the function value, and the
// function's own TypeParams binder vars are held symbolic rather than inlined to
// their bounds. So the declared quantifier is the function's ONLY quantifier and the
// signature renders `fn <T>(x: T) -> T`, not the double-quantified
// `fn <T0, T: T0>(x: T) -> T`. The return-only shape is the regression that used to
// inline its parameter var to never and trip acceptTypeParamVar's binder-must-stay-a-
// var guard (PR1).
func TestGeneralizeRetainsFuncTypeParams(t *testing.T) {
	tests := []struct {
		name string
		// build returns a generic FuncType, taking a fresh-var factory so each
		// parameter var is minted above the generalize level.
		build func(fresh func() *soltype.TypeVarType) *soltype.FuncType
		want  string
	}{
		{
			name: "both polarities",
			build: func(fresh func() *soltype.TypeVarType) *soltype.FuncType {
				vT := fresh()
				return &soltype.FuncType{
					TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}},
					Params:     []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: vT}},
					Ret:        vT,
				}
			},
			want: "fn <T>(x: T) -> T",
		},
		{
			name: "return only",
			build: func(fresh func() *soltype.TypeVarType) *soltype.FuncType {
				vT := fresh()
				return &soltype.FuncType{
					TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}},
					Params:     []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: num()}},
					Ret:        vT,
				}
			},
			want: "fn <T>(x: number) -> T",
		},
		{
			name: "param only",
			build: func(fresh func() *soltype.TypeVarType) *soltype.FuncType {
				vT := fresh()
				return &soltype.FuncType{
					TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}},
					Params:     []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: vT}},
					Ret:        num(),
				}
			},
			want: "fn <T>(x: T) -> number",
		},
		{
			name: "constrained param keeps its bound off the use site",
			build: func(fresh func() *soltype.TypeVarType) *soltype.FuncType {
				vT := fresh()
				vT.UpperBounds = []soltype.Type{num()}
				return &soltype.FuncType{
					TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}},
					Params:     []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: vT}},
					Ret:        vT,
				}
			},
			want: "fn <T: number>(x: T) -> T",
		},
		{
			name: "two distinct params",
			build: func(fresh func() *soltype.TypeVarType) *soltype.FuncType {
				vT := fresh()
				vU := fresh()
				return &soltype.FuncType{
					TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}, {Name: "U", Var: vU}},
					Params: []*soltype.FuncParam{
						{Pattern: &soltype.IdentPat{Name: "x"}, Type: vT},
						{Pattern: &soltype.IdentPat{Name: "y"}, Type: vU},
					},
					Ret: &soltype.TupleType{Elems: []soltype.Type{vT, vU}},
				}
			},
			want: "fn <T, U>(x: T, y: U) -> [T, U]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			fn := tt.build(func() *soltype.TypeVarType { return c.freshAt(1) })
			// Model the value-binding path: the SCC driver constrains the function value
			// into a binding var, then generalizes that var at the component's level.
			vb := c.freshAt(1)
			vb.LowerBounds = []soltype.Type{fn}

			var scheme TypeScheme
			require.NotPanics(t, func() { scheme = c.generalize(vb, 0) })
			require.Equal(t, tt.want, renderScheme(scheme))
		})
	}
}

// funcTypeParamVars descends a binding var's bound side-graph to reach the value
// FuncType, so a function's own type parameter is collected even though it is not a
// structural child of the binding var. It also reaches a generic function nested in a
// parameter's constraint.
func TestFuncTypeParamVarsDescendsBoundGraph(t *testing.T) {
	c := newChecker()
	vT := c.freshAt(1)
	fn := &soltype.FuncType{
		TypeParams: []*soltype.TypeParam{{Name: "T", Var: vT}},
		Params:     []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: vT}},
		Ret:        vT,
	}
	vb := c.freshAt(1)
	vb.LowerBounds = []soltype.Type{fn}

	keep := funcTypeParamVars(vb)
	require.True(t, keep.Contains(vT), "the type parameter reached through the binding var's bound is kept")
	require.Equal(t, 1, keep.Len())
}

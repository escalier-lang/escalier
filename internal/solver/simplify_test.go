package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// fparam builds a named function parameter for a hand-rolled FuncType body.
func fparam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t}
}

// Co-occurrence merging collapses distinct quantified variables that always appear
// together. The shape mirrors `outer` from the polymorphism suite: a parameter
// variable a flows to two result variables b, c (a's upper bounds), both returned
// in a tuple. PR1 retained all three as separate type parameters
// (`fn <T0, T1>(x: T0 & T1) -> [T0, T1]`); PR2 merges them to one.
func TestCoalesceSchemeMergesCoOccurring(t *testing.T) {
	b := &soltype.TypeVarType{ID: 2, Level: 1}
	c := &soltype.TypeVarType{ID: 3, Level: 1}
	a := &soltype.TypeVarType{ID: 1, Level: 1, UpperBounds: []soltype.Type{b, c}}
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{fparam("x", a)},
		Ret:    &soltype.TupleType{Elems: []soltype.Type{b, c}},
	}
	scheme := &PolyScheme{Level: 0, Body: body}

	require.Equal(t, "fn <T0>(x: T0) -> [T0, T0]", renderScheme(scheme))

	// All three variables resolve to one representative.
	simp := simplifyScheme(body, 0)
	require.Equal(t, simp.uf.find(1), simp.uf.find(2))
	require.Equal(t, simp.uf.find(2), simp.uf.find(3))
}

// Variables that do NOT co-occur stay distinct: two independent parameters each
// flowing to its own tuple slot share no union/intersection group, so each remains
// its own type parameter.
func TestCoalesceSchemeKeepsDistinctParams(t *testing.T) {
	a := &soltype.TypeVarType{ID: 1, Level: 1}
	b := &soltype.TypeVarType{ID: 2, Level: 1}
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{fparam("a", a), fparam("b", b)},
		Ret:    &soltype.TupleType{Elems: []soltype.Type{a, b}},
	}
	scheme := &PolyScheme{Level: 0, Body: body}

	require.Equal(t, "fn <T0, T1>(a: T0, b: T1) -> [T0, T1]", renderScheme(scheme))

	simp := simplifyScheme(body, 0)
	require.NotEqual(t, simp.uf.find(1), simp.uf.find(2))
}

// Captured variables (Level <= genLevel) are not merge candidates: a parameter
// variable at the generalize level never folds into a quantified one, so the
// representative of every quantifiable variable stays quantifiable.
func TestSimplifySchemeExcludesCapturedVars(t *testing.T) {
	captured := &soltype.TypeVarType{ID: 1, Level: 0} // at genLevel: not quantifiable
	quantified := &soltype.TypeVarType{ID: 2, Level: 1}
	body := &soltype.TupleType{Elems: []soltype.Type{captured, quantified}}

	simp := simplifyScheme(body, 0)
	require.Equal(t, 1, simp.uf.find(1), "a captured var is its own (singleton) class")
	require.Equal(t, 2, simp.uf.find(2), "a quantified var is its own class when nothing co-occurs")
}

// The union-find keeps the smaller id as a class's representative, so naming is
// stable regardless of union order.
func TestUnionFindSmallerIdRepresentative(t *testing.T) {
	uf := newUnionFind()
	uf.union(5, 3)
	uf.union(3, 8)
	require.Equal(t, 3, uf.find(5))
	require.Equal(t, 3, uf.find(8))
	require.Equal(t, 3, uf.find(3))
}

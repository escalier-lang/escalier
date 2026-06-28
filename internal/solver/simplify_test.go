package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
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
// flowing to its own tuple element share no union/intersection group, so each remains
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

// runCoOcc runs the occurrence and co-occurrence passes over body exactly as
// simplifyScheme does, returning the raw maps so a test can inspect which variables
// the analysis treats as co-occurring.
func runCoOcc(body soltype.Type) (map[int]occPolarity, map[coKey]set.Set[int]) {
	vars := map[int]*soltype.TypeVarType{}
	body.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	m := buildMirror(vars)
	occ := map[int]occPolarity{}
	body.Accept(&symOccVisitor{m: m, occ: occ, seen: set.NewSet[occKey]()}, soltype.Positive)
	coOcc := map[coKey]set.Set[int]{}
	body.Accept(&coOccVisitor{m: m, coOcc: coOcc, seen: set.NewSet[coKey]()}, soltype.Positive)
	return occ, coOcc
}

// The co-occurrence pass records a UNION of per-group peers, and the merge decision
// is made by mutualCoOcc's bidirectional, all-polarities check — not by membership
// in any single group. This pins that semantics on the `outer` shape: param a flows
// to result vars b and c, both returned in a tuple.
//
// gatherGroup follows a transitive but ASYMMETRIC closure, so at positive polarity a
// sits in its own group {a}, in b's group {a, b}, and in c's group {a, c}. So a
// co-occurs with b in one positive group and with c in another, while b and c never
// share a positive group. The union records a↔b and a↔c but never b↔c, which is what
// keeps the analysis from merging genuinely unrelated peers.
//
// A rule requiring a peer to appear in EVERY group a is part of (intersection /
// per-group counting) would reject the a–b and a–c pairs, since a sits in three
// positive groups but co-occurs with each result var in only one. The scheme would
// then regress to the non-compact `fn <T0, T1>(x: T0 & T1) -> [T0, T1]`. The
// assertions below fail under that rule, so they document why union accumulation
// plus a transitive union-find is required.
func TestCoOccUnionMergesTransitively(t *testing.T) {
	b := &soltype.TypeVarType{ID: 2, Level: 1}
	c := &soltype.TypeVarType{ID: 3, Level: 1}
	a := &soltype.TypeVarType{ID: 1, Level: 1, UpperBounds: []soltype.Type{b, c}}
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{fparam("x", a)},
		Ret:    &soltype.TupleType{Elems: []soltype.Type{b, c}},
	}

	occ, coOcc := runCoOcc(body)

	// a shares a positive group with each result var; b and c never share one. The
	// full element sets capture both facts: a co-occurs with exactly {b, c}, while
	// each result var co-occurs with exactly {a}, never with the other.
	require.ElementsMatch(t, []int{2, 3}, coOcc[coKey{1, soltype.Positive}].ToSlice(), "a co-occurs with b and c positively")
	require.ElementsMatch(t, []int{1}, coOcc[coKey{2, soltype.Positive}].ToSlice(), "b co-occurs with a only")
	require.ElementsMatch(t, []int{1}, coOcc[coKey{3, soltype.Positive}].ToSlice(), "c co-occurs with a only")

	// The merge relation: a is mutually-co-occurring with each result var, but b and c
	// are NOT mutually-co-occurring — they only merge transitively through a.
	require.True(t, mutualCoOcc(1, 2, occ, coOcc), "a and b co-occur in every polarity each occurs in")
	require.True(t, mutualCoOcc(1, 3, occ, coOcc), "a and c co-occur in every polarity each occurs in")
	require.False(t, mutualCoOcc(2, 3, occ, coOcc), "b and c never directly co-occur")

	// The union-find still collapses all three into one class via a.
	simp := simplifyScheme(body, 0)
	require.Equal(t, simp.uf.find(1), simp.uf.find(2))
	require.Equal(t, simp.uf.find(1), simp.uf.find(3))
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

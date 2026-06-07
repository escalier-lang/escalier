package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A MonoScheme instantiates to its exact type — same pointer, no freshening — so a
// monomorphic binding behaves as M2's plain type did.
func TestInstantiateMonoSchemeReturnsSameType(t *testing.T) {
	c := newChecker()
	ty := &soltype.PrimType{Prim: soltype.NumPrim}
	got := c.instantiate(monoScheme(ty), 0)
	require.Same(t, ty, got)
}

// A PolyScheme instantiates each quantified variable (Level > scheme.Level) to a
// FRESH variable, so two uses never share a variable; the freshened function keeps
// the same shape (param var == return var for identity).
func TestInstantiatePolySchemeFreshensQuantifiedVars(t *testing.T) {
	c := newChecker()
	// A generalized identity: fn(a) -> a, with `a` quantifiable (Level 1 > 0).
	a := &soltype.TypeVarType{ID: 100, Level: 1}
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: a}},
		Ret:    a,
	}
	scheme := &PolyScheme{Level: 0, Body: body}

	first := c.instantiate(scheme, 0).(*soltype.FuncType)
	second := c.instantiate(scheme, 0).(*soltype.FuncType)

	fv1 := first.Params[0].Type.(*soltype.TypeVarType)
	fv2 := second.Params[0].Type.(*soltype.TypeVarType)
	// Each instantiation freshens `a` to a new variable...
	require.NotSame(t, a, fv1)
	require.NotSame(t, fv1, fv2)
	// ...but within one instantiation the param var and return var stay shared.
	require.Same(t, fv1, first.Ret)
	require.Same(t, fv2, second.Ret)
}

// freshenAbove SHARES variables at or below the limit (captured outer variables)
// while freshening those above it — the level discipline that keeps a captured
// param monomorphic when an inner function is instantiated.
func TestFreshenAboveSharesCapturedVars(t *testing.T) {
	c := newChecker()
	captured := &soltype.TypeVarType{ID: 1, Level: 1}   // at the limit → shared
	quantified := &soltype.TypeVarType{ID: 2, Level: 2} // above the limit → freshened
	body := &soltype.TupleType{Elems: []soltype.Type{captured, quantified}}

	got := c.freshenAbove(1, body, 5, map[*soltype.TypeVarType]*soltype.TypeVarType{}).(*soltype.TupleType)
	require.Same(t, captured, got.Elems[0], "a captured var (Level <= lim) is shared")
	fresh := got.Elems[1].(*soltype.TypeVarType)
	require.NotSame(t, quantified, fresh, "a quantified var (Level > lim) is freshened")
	require.Equal(t, 5, fresh.Level, "the fresh var sits at the instantiation level")
}

// freshenAbove freshens a variable's BOUNDS too, and terminates on a cyclic bound
// graph (the fresh var is cached before its bounds are freshened).
func TestFreshenAboveFreshensBoundsAndHandlesCycles(t *testing.T) {
	c := newChecker()
	v := &soltype.TypeVarType{ID: 1, Level: 2}
	v.LowerBounds = []soltype.Type{&soltype.PrimType{Prim: soltype.NumPrim}}
	v.UpperBounds = []soltype.Type{v} // self-referential bound

	require.NotPanics(t, func() {
		got := c.freshenAbove(0, v, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{}).(*soltype.TypeVarType)
		require.NotSame(t, v, got)
		require.Len(t, got.LowerBounds, 1)
		require.Equal(t, &soltype.PrimType{Prim: soltype.NumPrim}, got.LowerBounds[0])
		// The cyclic upper bound is rewritten to the fresh var, not the original.
		require.Same(t, got, got.UpperBounds[0])
	})
}

// instantiate records a FromInstantiation provenance edge for each freshened
// variable, pointing back at the variable it was copied from — the first interior
// Origin (M3, PR1). A MonoScheme instantiation records nothing (it freshens no
// variable).
func TestInstantiateRecordsFromInstantiation(t *testing.T) {
	c := newChecker()
	a := &soltype.TypeVarType{ID: 1, Level: 1}
	scheme := &PolyScheme{Level: 0, Body: a}

	got := c.instantiate(scheme, 0)
	o, ok := c.prov[got]
	require.True(t, ok, "the freshened var should carry a provenance edge")
	fi, ok := o.(FromInstantiation)
	require.True(t, ok, "the edge is FromInstantiation")
	require.Same(t, a, fi.From, "it points back at the original quantified var")

	// NodeFor still resolves only FromAST leaves; the interior chain renderer is M11.5.
	_, ok = c.prov.NodeFor(got)
	require.False(t, ok)
}

// generalize wraps a type in a PolyScheme at the given level; the body is kept RAW
// (the same variables, for instantiation) rather than coalesced.
func TestGeneralizeWrapsRawBody(t *testing.T) {
	c := newChecker()
	a := &soltype.TypeVarType{ID: 1, Level: 1}
	scheme := c.generalize(a, 0)
	ps, ok := scheme.(*PolyScheme)
	require.True(t, ok)
	require.Equal(t, 0, ps.Level)
	require.Same(t, a, ps.Body, "generalize keeps the raw body for instantiation")
}

// IsOverloaded reflects the scheme-slice cardinality: one scheme is an ordinary
// binding, more than one is an overload set (PR6). PR1 only ever builds the former.
func TestValueBindingIsOverloaded(t *testing.T) {
	ordinary := ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.NeverType{})}}
	require.False(t, ordinary.IsOverloaded())
	overloaded := ValueBinding{Schemes: []TypeScheme{
		monoScheme(&soltype.NeverType{}), monoScheme(&soltype.NeverType{}),
	}}
	require.True(t, overloaded.IsOverloaded())
}

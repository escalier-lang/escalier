package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// ltVarsFromEdges builds one LifetimeVar per ID mentioned in edges or staticIDs and
// records each edge (x, y) as "'x outlives 'y" the way constrainLt does: y joins
// x.UpperBounds and x joins y.LowerBounds. Each ID in staticIDs gains 'static as an
// upper bound, the escape constraint that forces it to 'static. It returns the
// occurrence map buildLtBoundSet consumes; the polarity value is irrelevant to graph
// construction, so every variable is recorded as occNeg.
func ltVarsFromEdges(edges [][2]int, staticIDs ...int) map[*soltype.LifetimeVar]occPolarity {
	vars := map[int]*soltype.LifetimeVar{}
	get := func(id int) *soltype.LifetimeVar {
		v, ok := vars[id]
		if !ok {
			v = &soltype.LifetimeVar{ID: id}
			vars[id] = v
		}
		return v
	}
	for _, e := range edges {
		x, y := get(e[0]), get(e[1])
		x.UpperBounds = append(x.UpperBounds, y)
		y.LowerBounds = append(y.LowerBounds, x)
	}
	for _, id := range staticIDs {
		get(id).UpperBounds = append(get(id).UpperBounds, soltype.Static)
	}
	occ := map[*soltype.LifetimeVar]occPolarity{}
	for _, v := range vars {
		occ[v] = occNeg
	}
	return occ
}

// buildReduced builds a bound set from edges plus 'static-forced IDs and reduces it.
func buildReduced(edges [][2]int, staticIDs ...int) *ltBoundSet {
	s := buildLtBoundSet(ltVarsFromEdges(edges, staticIDs...))
	s.reduce()
	return s
}

// Transitive reduction and 'static edge removal both collapse the edge set to its
// non-redundant, meaningful bounds.
func TestLtBoundSetReduce(t *testing.T) {
	tests := []struct {
		name      string
		edges     [][2]int
		staticIDs []int
		want      [][2]int
	}{
		{
			// '1: '2, '2: '3, '1: '3 — the shortcut '1: '3 drops.
			name:  "transitive shortcut drops",
			edges: [][2]int{{1, 2}, {2, 3}, {1, 3}},
			want:  [][2]int{{1, 2}, {2, 3}},
		},
		{
			// A diamond keeps its four rim edges and drops only the '1: '4 shortcut.
			name:  "diamond keeps rim, drops shortcut",
			edges: [][2]int{{1, 2}, {1, 3}, {2, 4}, {3, 4}, {1, 4}},
			want:  [][2]int{{1, 2}, {1, 3}, {2, 4}, {3, 4}},
		},
		{
			// The multi-source join '1: '3, '2: '3 keeps '1 and '2 independent.
			name:  "independent join sources stay unrelated",
			edges: [][2]int{{1, 3}, {2, 3}},
			want:  [][2]int{{1, 3}, {2, 3}},
		},
		{
			// An edge into a 'static-forced node drops.
			name:      "edge into static drops",
			edges:     [][2]int{{1, 2}},
			staticIDs: []int{2},
			want:      nil,
		},
		{
			// An edge out of a 'static-forced node is trivially true and drops.
			name:      "edge out of static drops",
			edges:     [][2]int{{1, 2}},
			staticIDs: []int{1},
			want:      nil,
		},
		{
			// A mutual-outlives cycle condenses to one node, so no edge survives.
			name:  "two-node cycle collapses",
			edges: [][2]int{{1, 2}, {2, 1}},
			want:  nil,
		},
		{
			// A three-node cycle likewise folds to one node.
			name:  "three-node cycle collapses",
			edges: [][2]int{{1, 2}, {2, 3}, {3, 1}},
			want:  nil,
		},
		{
			// A cycle that also outlives an outside lifetime keeps the one condensed edge.
			name:  "cycle with outgoing edge",
			edges: [][2]int{{1, 2}, {2, 1}, {2, 3}},
			want:  [][2]int{{1, 3}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, buildReduced(tt.edges, tt.staticIDs...).canonicalEdges())
		})
	}
}

// canonicalEdges sorts by (from, to), so the edge order is the same no matter the order
// the bounds were recorded in.
func TestLtBoundSetCanonicalEdgeOrderShuffleInvariant(t *testing.T) {
	want := [][2]int{{1, 2}, {1, 4}, {2, 3}}
	require.Equal(t, want, buildReduced([][2]int{{1, 2}, {2, 3}, {1, 4}}).canonicalEdges())
	require.Equal(t, want, buildReduced([][2]int{{1, 4}, {2, 3}, {1, 2}}).canonicalEdges())
}

// A mutual-outlives cycle condenses to its smallest-ID representative, so both members
// share one representative and implies reports the equality in both directions.
func TestLtBoundSetMutualOutlivesIsEquality(t *testing.T) {
	s := buildReduced([][2]int{{1, 2}, {2, 1}})

	require.Equal(t, 1, s.repOf(1), "the smaller ID is the representative")
	require.Equal(t, 1, s.repOf(2))
	require.True(t, s.implies(1, 2), "equal lifetimes outlive each other")
	require.True(t, s.implies(2, 1))
}

// A three-node cycle folds to one node and reduce terminates on it rather than looping
// over the collapsed cycle. Every member inherits the component's outgoing relations.
func TestLtBoundSetThreeNodeCycleReducesWithoutLooping(t *testing.T) {
	s := buildLtBoundSet(ltVarsFromEdges([][2]int{{1, 2}, {2, 3}, {3, 1}, {3, 4}}))
	require.NotPanics(t, func() { s.reduce() })

	require.Equal(t, 1, s.repOf(1))
	require.Equal(t, 1, s.repOf(2))
	require.Equal(t, 1, s.repOf(3))
	require.True(t, s.implies(2, 4), "'2 shares the cycle's component, so it outlives '4")
}

// implies answers reachability over the reduced graph, including reflexivity and the
// transitive relation the reduction elided from the edge set.
func TestLtBoundSetImpliesReachability(t *testing.T) {
	s := buildReduced([][2]int{{1, 2}, {2, 3}})

	require.True(t, s.implies(1, 2), "'1: '2 is a direct edge")
	require.True(t, s.implies(2, 3), "'2: '3 is a direct edge")
	require.True(t, s.implies(1, 3), "'1: '3 holds transitively though the edge was reduced away")
	require.True(t, s.implies(1, 1), "outlives is reflexive")
	require.False(t, s.implies(3, 1), "'3 does not outlive '1")
	require.False(t, s.implies(2, 1), "'2 does not outlive '1")
}

// A 'static-forced lifetime outlives every other lifetime, so it implies every target
// regardless of the edges.
func TestLtBoundSetImpliesStaticAbsorbs(t *testing.T) {
	s := buildReduced([][2]int{{2, 3}}, 1)

	require.True(t, s.static.Contains(s.repOf(1)), "'1 is forced to 'static")
	require.True(t, s.implies(1, 2), "'static outlives '2")
	require.True(t, s.implies(1, 3), "'static outlives '3")
	require.False(t, s.implies(2, 1), "'2 does not outlive the 'static lifetime")
}

// 'static forcing propagates backward: a lifetime that outlives a 'static-forced
// lifetime is itself forced to 'static, since only 'static outlives 'static.
func TestLtBoundSetStaticPropagatesBackward(t *testing.T) {
	// '1: '2, '2: 'static forces '1 to 'static too.
	s := buildReduced([][2]int{{1, 2}}, 2)

	require.True(t, s.static.Contains(s.repOf(1)), "'1 outlives the 'static '2, so '1 is 'static")
	require.True(t, s.implies(1, 3), "the now-'static '1 outlives an unrelated '3")
}

// A lower-bound 'static is 'static outliving the variable, which is trivially true and
// forces nothing. The variable must not be treated as absorbing.
func TestLtBoundSetLowerBoundStaticDoesNotForce(t *testing.T) {
	v := &soltype.LifetimeVar{ID: 1, LowerBounds: []soltype.Lifetime{soltype.Static}}
	other := &soltype.LifetimeVar{ID: 2}
	s := buildLtBoundSet(map[*soltype.LifetimeVar]occPolarity{v: occNeg, other: occNeg})
	s.reduce()

	require.False(t, s.static.Contains(s.repOf(1)), "a lower-bound 'static is not an escape")
	require.False(t, s.implies(1, 2), "'1 does not outlive an unrelated '2")
}

// subsumes requires this set to prove every relation the other set asserts: kept edges,
// mutual-outlives equalities condensed away, and 'static forcings.
func TestLtBoundSetSubsumes(t *testing.T) {
	chain := buildReduced([][2]int{{1, 2}, {2, 3}})

	require.True(t, chain.subsumes(buildReduced([][2]int{{1, 3}})),
		"the chain proves the transitively-implied '1: '3")
	require.False(t, buildReduced([][2]int{{1, 3}}).subsumes(chain),
		"the lone edge proves neither '1: '2 nor '2: '3")
	require.False(t, chain.subsumes(buildReduced([][2]int{{3, 1}})),
		"the chain does not prove the reversed '3: '1")
}

// A declared mutual-outlives bound is not satisfied vacuously: the collapsed equality
// must actually hold in the subsuming set, even though it survives as no edge.
func TestLtBoundSetSubsumesChecksCondensedEqualities(t *testing.T) {
	declaredEqual := buildReduced([][2]int{{1, 2}, {2, 1}}) // '1 and '2 must be equal

	require.False(t, buildReduced([][2]int{{1, 2}}).subsumes(declaredEqual),
		"proving only '1: '2 does not prove the required '2: '1")
	require.True(t, declaredEqual.subsumes(declaredEqual),
		"an equal-lifetime set proves its own equality")
}

// A declared 'static forcing is not satisfied vacuously: it survives as no edge, so a
// set that does not force the same lifetime to 'static must fail to subsume it.
func TestLtBoundSetSubsumesChecksStaticForcing(t *testing.T) {
	declaredStatic := buildReduced(nil, 1)      // '1: 'static
	notStatic := buildReduced([][2]int{{1, 2}}) // '1 relates to '2 but is not 'static

	require.False(t, notStatic.subsumes(declaredStatic),
		"a non-'static '1 does not satisfy a declared '1: 'static")
	require.True(t, declaredStatic.subsumes(declaredStatic))
}

// alphaEqualTypes equates two borrows minted in independent schemes when they denote
// the same borrow up to lifetime renaming, where the pointer-identity equalType reports
// them unequal. Both signatures render `fn <'a>(p: &'a mut {x}) -> &'a mut {x}`, each
// sharing one lifetime between its parameter and return, but their lifetime variables are
// distinct identities, so only alphaEqualTypes sees them as equal.
func TestAlphaEqualTypesBorrowsAcrossSchemes(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	fnA := borrowFn(mutPointRef(a), a)
	fnB := borrowFn(mutPointRef(b), b)

	require.False(t, equalType(fnA, fnB),
		"pointer-identity equality distinguishes the two independent lifetimes")
	require.True(t, alphaEqualTypes(fnA, fnB),
		"the two signatures are equal up to lifetime renaming")
}

// The walk binds each matched borrow's lifetime into a bijection, so which parameter a
// return borrows from is part of the comparison. Two signatures that both take two
// borrows but return the FIRST versus the SECOND are not alpha-equivalent, even though
// both carry exactly two lifetimes.
func TestAlphaEqualTypesPairingIsBijection(t *testing.T) {
	c := newChecker()
	a, b := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	fnRetFirst := &soltype.FuncType{
		Params: borrowFn(num(), a, b).Params, // p: &'a mut {x}, q: &'b mut {x}
		Ret:    mutPointRef(a),               // returns the first borrow
	}
	cc, d := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	fnRetFirst2 := &soltype.FuncType{
		Params: borrowFn(num(), cc, d).Params,
		Ret:    mutPointRef(cc), // also returns its first borrow
	}
	e, f := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	fnRetSecond := &soltype.FuncType{
		Params: borrowFn(num(), e, f).Params,
		Ret:    mutPointRef(f), // returns its SECOND borrow
	}

	require.True(t, alphaEqualTypes(fnRetFirst, fnRetFirst2),
		"both return their first parameter's borrow")
	require.False(t, alphaEqualTypes(fnRetFirst, fnRetSecond),
		"returning the first versus the second parameter's borrow is a different relation")
}

// Two independent param lifetimes with no bound between them stay distinct, so a
// signature over two DISTINCT borrows is not alpha-equivalent to one that reuses a
// single borrow for both parameters. The bijection refuses to bind the reused lifetime
// to two different partners.
func TestAlphaEqualTypesIndependenceWithinScheme(t *testing.T) {
	c := newChecker()
	a, b := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	distinct := borrowFn(num(), a, b) // two independent borrows
	shared := borrowFn(num(), a, a)   // one borrow used for both parameters

	require.False(t, alphaEqualTypes(distinct, shared),
		"two independent lifetimes do not collapse to one")
}

// The reduced outlives relation is part of the comparison. Two structurally identical
// two-borrow signatures are alpha-equivalent only when they carry the same outlives
// bound; adding `'a: 'b` to one side breaks the equality.
func TestAlphaEqualTypesComparesOutlivesRelation(t *testing.T) {
	c := newChecker()
	a, b := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	c.ctx.constrainLt(a, b) // 'a outlives 'b
	bound := borrowFn(num(), a, b)

	e, f := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	c.ctx.constrainLt(e, f)
	bound2 := borrowFn(num(), e, f)

	g, h := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	free := borrowFn(num(), g, h) // same shape, no bound between the two lifetimes

	require.True(t, alphaEqualTypes(bound, bound2),
		"two signatures with the same 'a: 'b bound are alpha-equivalent")
	require.False(t, alphaEqualTypes(bound, free),
		"a signature with 'a: 'b differs from one with no bound")
}

// The lifetime bijection follows structure, so matching two objects with the same
// borrow-typed properties in different declaration order still pairs the properties by
// name. equalType already treats objects as equal up to property order, and
// alpha-equivalence must not regress that for borrow-typed fields.
func TestAlphaEqualTypesObjectPropertyOrderInsensitive(t *testing.T) {
	c := newChecker()
	a, b := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	objMN := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "m", Type: mutPointRef(a)},
		&soltype.PropertyElem{Name: "n", Type: mutPointRef(b)},
	}}
	cc, d := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	objNM := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{ // same fields, swapped order
		&soltype.PropertyElem{Name: "n", Type: mutPointRef(d)},
		&soltype.PropertyElem{Name: "m", Type: mutPointRef(cc)},
	}}
	fnA := &soltype.FuncType{Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: objMN}}, Ret: num()}
	fnB := &soltype.FuncType{Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: objNM}}, Ret: num()}

	require.True(t, alphaEqualTypes(fnA, fnB),
		"borrow-typed object properties pair by name, not by declaration order")
}

// Two lifetimes that mutually outlive each other are equal, so a signature whose two
// borrows are mutually-outliving shares one lifetime and is not alpha-equivalent to one
// with two independent borrows. The outlives comparison reads this equality through
// implies, which reports a condensed cycle as equality in both directions.
func TestAlphaEqualTypesMutualOutlivesSharesLifetime(t *testing.T) {
	c := newChecker()
	a, b := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	c.ctx.constrainLt(a, b)
	c.ctx.constrainLt(b, a) // 'a and 'b mutually outlive, hence are equal
	mutual := borrowFn(num(), a, b)

	e, f := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	independent := borrowFn(num(), e, f) // two distinct lifetimes, unrelated

	require.False(t, alphaEqualTypes(mutual, independent),
		"one shared borrow does not equal two independent ones")

	g, h := c.ctx.freshLifetime(0), c.ctx.freshLifetime(0)
	c.ctx.constrainLt(g, h)
	c.ctx.constrainLt(h, g)
	mutual2 := borrowFn(num(), g, h)

	require.True(t, alphaEqualTypes(mutual, mutual2),
		"two signatures whose paired borrows are each mutually-outliving are alpha-equal")
}

// inferredValueType returns the coalesced display type of a top-level value binding, the
// inferred type a hover would show. alphaEqualTypes compares two of these.
func inferredValueType(t *testing.T, scope *Scope, name string) soltype.Type {
	t.Helper()
	b, ok := scope.GetValue(name)
	require.True(t, ok, "no value binding for %q", name)
	require.Len(t, b.Schemes, 1, "%q should be a single-scheme binding", name)
	return schemeType(b.Schemes[0])
}

// Two source functions that declare their borrow lifetime with different names infer to
// the same signature. Inference mints an independent lifetime variable per function, so
// pointer-identity equalType reports them unequal, but alphaEqualTypes equates them up to
// the renaming. A third function borrowing a differently-shaped object stays distinct.
func TestAlphaEqualTypesInferredRenamedLifetimes(t *testing.T) {
	scope, _, errs := InferModule(parseModule(t, `
		fn f<'a>(p: &'a mut {x: number}) -> &'a mut {x: number} { return p }
		fn g<'b>(q: &'b mut {x: number}) -> &'b mut {x: number} { return q }
		fn h<'c>(r: &'c mut {y: number}) -> &'c mut {y: number} { return r }
	`))
	require.Empty(t, errs)
	f := inferredValueType(t, scope, "f")
	g := inferredValueType(t, scope, "g")
	h := inferredValueType(t, scope, "h")

	require.False(t, equalType(f, g),
		"inference minted independent lifetime variables, so pointer identity differs")
	require.True(t, alphaEqualTypes(f, g),
		"'a and 'b name the same borrow signature")
	require.False(t, alphaEqualTypes(f, h),
		"the borrowed object shapes differ, {x: number} versus {y: number}")
}

// Two source functions that each return the first of their two borrows are
// alpha-equivalent under a lifetime renaming, even with differently-named lifetime
// parameters. A third that returns its second borrow carries a different lifetime
// relation and is not equivalent to them.
func TestAlphaEqualTypesInferredReturnChoice(t *testing.T) {
	scope, _, errs := InferModule(parseModule(t, `
		fn pair1<'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'a mut {x: number} { return p }
		fn pair2<'c, 'd>(r: &'c mut {x: number}, s: &'d mut {x: number}) -> &'c mut {x: number} { return r }
		fn pair3<'e, 'f>(u: &'e mut {x: number}, v: &'f mut {x: number}) -> &'f mut {x: number} { return v }
	`))
	require.Empty(t, errs)
	pair1 := inferredValueType(t, scope, "pair1")
	pair2 := inferredValueType(t, scope, "pair2")
	pair3 := inferredValueType(t, scope, "pair3")

	require.True(t, alphaEqualTypes(pair1, pair2),
		"both return the first of their two borrows")
	require.False(t, alphaEqualTypes(pair1, pair3),
		"returning the first versus the second borrow is a different lifetime relation")
}

// On a type with no lifetime variable, alphaEqualTypes reduces to equalType, so a caller
// gains lifetime alpha-equivalence for borrows without changing equality anywhere else.
func TestAlphaEqualTypesReducesToEqualTypeWithoutBorrows(t *testing.T) {
	obj := exactObj(propElem("x", num()))
	obj2 := exactObj(propElem("x", num()))

	require.True(t, alphaEqualTypes(obj, obj2))
	require.True(t, alphaEqualTypes(num(), num()))
	require.False(t, alphaEqualTypes(num(), str()))
	require.False(t, alphaEqualTypes(num(), obj))
}

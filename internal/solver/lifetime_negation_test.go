package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// negRef complements a borrow, building the `¬(&'a mut {x: number})` these tests need.
// It builds the NegationType node directly rather than calling soltype.NewNegation,
// which enforces the ¬Ref exclusion invariant and panics on a borrow operand. The
// invariant holds for the solver, so no source program produces this shape. The
// display passes must classify it correctly before the invariant can be lifted, and
// building the node here is what lets these tests run ahead of that.
func negRef(lt soltype.Lifetime) soltype.Type {
	return &soltype.NegationType{Inner: mutPointRef(lt)}
}

// A complemented borrow keeps its lifetime name in the rendered signature, wherever the
// complement sits.
//
// The Accept walk threads one polarity and it means variance. coalesceLifetimes needs a
// different fact, the dataflow position saying whether a borrow sits in a parameter or in
// an output. The two agree at every former whose flip is a real position change, such as
// a function parameter. They disagree under a complement, which inverts variance without
// moving the borrow, so ltOccVisitor in lifetime_coalesce.go undoes that flip to recover
// the position.
//
// ltOccVisitor also adds the borrow's lifetime to its noElide set. That set holds every
// lifetime a complement encloses, and resolveLt consults it at each point it would
// otherwise drop a lifetime, so a lifetime in the set survives whatever its position.
// Eliding one under a complement yields a different type, since `¬(&'a T)` rendered as
// `¬(&T)` is the complement of any borrow of T rather than of the `'a` one.
//
// The noElide set alone carries every row below, so none of them guards the position
// correction. That is pinned by TestComplementedBorrowAssertsNoOutlivesRelation and
// TestComplementedBorrowGroupsLikeAnOrdinaryParam, where position decides which component
// counts as output-reaching.
func TestComplementedBorrowKeepsLifetimeName(t *testing.T) {
	num := &soltype.PrimType{Prim: soltype.NumPrim}

	tests := []struct {
		name string
		// build returns the signature under test, given one param lifetime to hang
		// borrows off.
		build func(a *soltype.LifetimeVar) *soltype.FuncType
		want  string
	}{
		{
			// The uncomplemented baseline: a borrow in and the same borrow out, which
			// already renders under one name.
			name:  "borrow param to borrow return",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType { return borrowFn(mutPointRef(a), a) },
			want:  "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		},
		{
			// The complement sits in the return, so the parameter's lifetime still
			// reaches an output and the signature must keep recording that connection.
			name:  "borrow param to complemented return",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType { return borrowFn(negRef(a), a) },
			want:  "fn <'a>(p: &'a mut {x: number}) -> ¬&'a mut {x: number}",
		},
		{
			name: "complemented return with no param",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType {
				return &soltype.FuncType{Ret: negRef(a)}
			},
			want: "fn <'a>() -> ¬&'a mut {x: number}",
		},
		{
			name: "complemented param to non-borrow return",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType {
				return &soltype.FuncType{
					Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: negRef(a)}},
					Ret:    num,
				}
			},
			want: "fn <'a>(p: ¬&'a mut {x: number}) -> number",
		},
		{
			// The worst row. Stripping the name here leaves a returned borrow with no
			// named source, which is the outcome elision exists to prevent, since it
			// hides a borrow at the call site.
			name: "complemented param to borrow return",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType {
				return &soltype.FuncType{
					Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: negRef(a)}},
					Ret:    mutPointRef(a),
				}
			},
			want: "fn <'a>(p: ¬&'a mut {x: number}) -> &'a mut {x: number}",
		},
		{
			// The control. The noElide set holds only the borrows a complement
			// encloses, so an uncomplemented borrow reaching no output still renders
			// with its lifetime elided.
			name: "uncomplemented connect-nothing borrow still elides",
			build: func(a *soltype.LifetimeVar) *soltype.FuncType {
				return borrowFn(num, a)
			},
			want: "fn (p: &mut {x: number}) -> number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			a := c.ctx.freshLifetime(0)
			require.Equal(t, tt.want, renderScheme(&MonoScheme{Ty: tt.build(a)}))
		})
	}
}

// Two complements around one borrow cancel, and the coalescer folds `¬¬T` to T before
// the lifetime pass runs, so that shape never reaches the occurrence walk. Nesting does
// reach it through an intervening former. Here a complement encloses a function whose
// parameter is itself a complemented borrow, so the walk sees two complements.
//
// Two flips cancel, so undoing them returns the inner borrow to the negative position it
// structurally sits in. Neither borrow reaches an output, so the name here comes from the
// noElide set rather than from the position. This row passes with the position correction
// disabled, so treat it as covering the nesting shape, not the parity rule.
func TestNestedComplementsKeepLifetimeName(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	num := &soltype.PrimType{Prim: soltype.NumPrim}
	inner := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "q"}, Type: negRef(a)}},
		Ret:    num,
	}
	fn := borrowFn(&soltype.NegationType{Inner: inner}, a)

	require.Equal(t,
		"fn <'a>(p: &'a mut {x: number}) -> ¬(fn (q: ¬&'a mut {x: number}) -> number)",
		renderScheme(&MonoScheme{Ty: fn}))
}

// The coalescer folds `¬¬T` to T, so a borrow under two immediately-nested complements
// reaches the lifetime pass with no complement around it at all and takes the ordinary
// polarity reading.
func TestDoubleComplementFoldsBeforeLifetimePass(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	fn := borrowFn(&soltype.NegationType{Inner: negRef(a)}, a)

	require.Equal(t,
		"fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// The polarity flip a complement applies does reach a borrow's lifetime, even though
// RefType.Accept never walks it. NegationType.Accept flips the polarity before
// descending, and the extruder reads the lifetime off the RefType node in its own
// EnterType, where the flip has already applied.
//
// extrudeLt wires the origin lifetime to its fresh proxy through the bound direction
// the polarity picks. At Positive the proxy becomes an upper bound of the origin, and
// at Negative a lower bound. So extruding the same borrow from level 5 to level 0 at
// one root polarity leaves the origin with opposite bounds depending on whether a
// complement encloses it, which is the outlives direction flipping.
func TestComplementFlipsExtrudedLifetimeDirection(t *testing.T) {
	tests := []struct {
		name              string
		complemented      bool
		wantLow, wantHigh int
	}{
		{name: "bare borrow", complemented: false, wantLow: 0, wantHigh: 1},
		{name: "complemented borrow", complemented: true, wantLow: 1, wantHigh: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			a := c.ctx.freshLifetime(5)
			var ty soltype.Type = mutPointRef(a)
			if tt.complemented {
				ty = &soltype.NegationType{Inner: ty}
			}
			require.Equal(t, 5, soltype.LevelOf(ty),
				"the borrow must be inner to the extrusion level, or the walk prunes it")

			e := &extruder{
				c:       c.ctx,
				lvl:     0,
				cache:   map[extrudeKey]*soltype.TypeVarType{},
				ltCache: map[ltExtrudeKey]*soltype.LifetimeVar{},
			}
			ty.Accept(e, soltype.Positive)

			require.Len(t, a.LowerBounds, tt.wantLow)
			require.Len(t, a.UpperBounds, tt.wantHigh)
		})
	}
}

// ltOutlivesRelation feeds the declared-bound check as well as the printer, so the
// mis-classification reaches further than rendering. A lifetime read as an output-only
// lifetime is taken for a multi-source join, and componentParams then reports every
// param in the join's connected component as outliving it. That invents outlives
// relations inference never proved.
//
// Below, `m`, `a`, `b` and `d` name the lifetime variables the test builds, not the
// names they render under. `m` is an instantiation intermediary outliving `a`, `b` and
// `d`, which puts all four in one connected component while leaving no directed edge
// among `a`, `b` and `d`. Reading the complemented `d` as a join over `a` and `b` would
// assert that `a` and `b` outlive `d`, and checkDeclaredLifetimeBounds would accept a
// declared bound the body never establishes. Classifying `d` as a param instead leaves
// the relation empty.
func TestComplementedBorrowAssertsNoOutlivesRelation(t *testing.T) {
	c := newChecker()
	m := c.ctx.freshJoinLifetime(0) // minted first so it holds the smallest ID
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	d := c.ctx.freshLifetime(0)
	c.ctx.constrainLt(m, a)
	c.ctx.constrainLt(m, b)
	c.ctx.constrainLt(m, d)

	fn := &soltype.FuncType{
		Params: []*soltype.FuncParam{
			{Pattern: &soltype.IdentPat{Name: "p"}, Type: mutPointRef(a)},
			{Pattern: &soltype.IdentPat{Name: "q"}, Type: mutPointRef(b)},
			{Pattern: &soltype.IdentPat{Name: "r"}, Type: negRef(d)},
		},
		Ret: &soltype.PrimType{Prim: soltype.NumPrim},
	}

	_, survivors, outlives := ltOutlivesRelation(fn, soltype.Positive)
	require.Len(t, survivors, 3)
	for _, u := range survivors {
		for _, w := range survivors {
			require.False(t, outlives(u, w),
				"no outlives relation holds among the three param lifetimes")
		}
	}

	// The rendered signature carries the same fact: no bound in the quantifier prefix.
	// It also shows the two signals acting separately. All three borrows reach no
	// output, so all three are connect-nothing. That verdict elides the borrows on `p`
	// and `q` on position alone. Only the borrow on `r` keeps a name, because `d` is in
	// the noElide set, which overrides the same verdict for it. It is the sole surviving
	// lifetime, so the printer gives it the first display name, `'a`.
	require.Equal(t,
		"fn <'a>(p: &mut {x: number}, q: &mut {x: number}, r: ¬&'a mut {x: number}) -> number",
		renderScheme(&MonoScheme{Ty: fn}))
}

// Classifying a complemented borrow's lifetime as a param puts it on the same footing
// as an uncomplemented one, including where the connectivity-based grouping is loose.
// componentParams gathers every kept param in a join's connected component, so a param
// linked to the join only through an instantiation intermediary is still reported as a
// source. That looseness is deliberate, since an intermediary is exactly how a call's
// argument lifetime reaches the join it feeds. It applies to a complemented borrow and
// a plain one alike, which is what this test pins.
//
// The graph below wires `m` as an intermediary outliving `a` and `b`, and `j` as a
// genuine join over `a` and `x`. `b` reaches `j` only through `m`, and both signatures
// still bound it to the join. Reading `b` as an output-only lifetime instead would make
// it a join over `a` and `x` and assert the reverse, that `a` and `x` outlive `b`,
// neither of which inference proves.
//
// Those are the variables the test builds, not the names they render under. The printer
// assigns display names in first-appearance order, so `a`, `b`, `x` and `j` render as
// `'a`, `'b`, `'c` and `'d`.
func TestComplementedBorrowGroupsLikeAnOrdinaryParam(t *testing.T) {
	// build wires the graph above and returns the signature, wrapping the second
	// parameter's borrow in a complement when complemented is set.
	build := func(complemented bool) *soltype.FuncType {
		c := newChecker()
		m := c.ctx.freshJoinLifetime(0) // minted first so it holds the smallest ID
		a := c.ctx.freshLifetime(0)
		b := c.ctx.freshLifetime(0)
		x := c.ctx.freshLifetime(0)
		j := c.ctx.freshJoinLifetime(0)
		c.ctx.constrainLt(m, a)
		c.ctx.constrainLt(m, b)
		c.ctx.constrainLt(a, j)
		c.ctx.constrainLt(x, j)

		second := soltype.Type(mutPointRef(b))
		if complemented {
			second = negRef(b)
		}
		return &soltype.FuncType{
			Params: []*soltype.FuncParam{
				{Pattern: &soltype.IdentPat{Name: "p"}, Type: mutPointRef(a)},
				{Pattern: &soltype.IdentPat{Name: "q"}, Type: second},
				{Pattern: &soltype.IdentPat{Name: "s"}, Type: mutPointRef(x)},
			},
			Ret: mutPointRef(j),
		}
	}

	require.Equal(t,
		"fn <'a: 'd, 'b: 'd, 'c: 'd, 'd>(p: &'a mut {x: number}, q: &'b mut {x: number}, s: &'c mut {x: number}) -> &'d mut {x: number}",
		renderScheme(&MonoScheme{Ty: build(false)}))
	require.Equal(t,
		"fn <'a: 'd, 'b: 'd, 'c: 'd, 'd>(p: &'a mut {x: number}, q: ¬&'b mut {x: number}, s: &'c mut {x: number}) -> &'d mut {x: number}",
		renderScheme(&MonoScheme{Ty: build(true)}))
}

package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// negRef complements a borrow, building the `~(&'a mut {x: number})` the graph-level tests
// below need. Those tests wire a lifetime graph directly rather than inferring one from
// source, so they assemble the type to match.
func negRef(lt soltype.Lifetime) soltype.Type {
	return &soltype.NegationType{Inner: mutPointRef(lt)}
}

// A complemented borrow keeps its lifetime name in the rendered signature, wherever the
// complement sits. ltOccVisitor in lifetime_coalesce.go carries the design: keeping the
// name takes two separate facts, a recovered dataflow position and the noElide set.
//
// The noElide set alone carries every row below, so none of them guards the position
// correction. That is pinned by TestComplementedBorrowAssertsNoOutlivesRelation and
// TestComplementedBorrowGroupsLikeAnOrdinaryParam, where position decides which component
// counts as output-reaching.
func TestComplementedBorrowKeepsLifetimeName(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The uncomplemented baseline: a borrow in and the same borrow out, which
			// already renders under one name.
			name: "borrow param to borrow return",
			src:  `declare fn f<'a>(p: &'a mut {x: number}) -> &'a mut {x: number}`,
			want: "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		},
		{
			// The complement sits in the return, so the parameter's lifetime still
			// reaches an output and the signature must keep recording that connection.
			name: "borrow param to complemented return",
			src:  `declare fn f<'a>(p: &'a mut {x: number}) -> ~(&'a mut {x: number})`,
			want: "fn <'a>(p: &'a mut {x: number}) -> ~&'a mut {x: number}",
		},
		{
			name: "complemented return with no param",
			src:  `declare fn f<'a>() -> ~(&'a mut {x: number})`,
			want: "fn <'a>() -> ~&'a mut {x: number}",
		},
		{
			name: "complemented param to non-borrow return",
			src:  `declare fn f<'a>(p: ~(&'a mut {x: number})) -> number`,
			want: "fn <'a>(p: ~&'a mut {x: number}) -> number",
		},
		{
			// The worst row. Stripping the name here leaves a returned borrow with no
			// named source, which is the outcome elision exists to prevent, since it
			// hides a borrow at the call site.
			name: "complemented param to borrow return",
			src:  `declare fn f<'a>(p: ~(&'a mut {x: number})) -> &'a mut {x: number}`,
			want: "fn <'a>(p: ~&'a mut {x: number}) -> &'a mut {x: number}",
		},
		{
			// The control. The noElide set holds only the borrows a complement
			// encloses, so an uncomplemented borrow reaching no output still renders
			// with its lifetime elided.
			name: "uncomplemented connect-nothing borrow still elides",
			src:  `declare fn f<'a>(p: &'a mut {x: number}) -> number`,
			want: "fn (p: &mut {x: number}) -> number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// Two complements around one borrow cancel, and the coalescer folds `~~T` to T before
// the lifetime pass runs, so that shape never reaches the occurrence walk. Nesting does
// reach it through an intervening former. Here a complement encloses a function whose
// parameter is itself a complemented borrow, so the walk sees two complements.
//
// Two flips cancel, so undoing them returns the inner borrow to the negative position it
// structurally sits in. Neither borrow reaches an output, so the name here comes from the
// noElide set rather than from the position. This row passes with the position correction
// disabled, so treat it as covering the nesting shape, not the parity rule.
//
// The type is assembled rather than written as source because a nested function
// annotation opens its own lifetime scope, which f's `<'a>` does not reach into. Naming
// `'a` on the inner borrow reports it undeclared there, and declaring `<'b>` on the inner
// function binds a second lifetime. Either spelling renders
//
//	fn <'a>(p: &mut {x: number}) -> ~(fn (q: ~&'a mut {x: number}) -> number)
//
// where the parameter's borrow has elided, because with two independent lifetimes it
// reaches no output. The assertion below carries one lifetime across both positions, so
// the parameter keeps its name. That shared lifetime is the shape under test.
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
		"fn <'a>(p: &'a mut {x: number}) -> ~(fn (q: ~&'a mut {x: number}) -> number)",
		renderScheme(&MonoScheme{Ty: fn}))
}

// The coalescer folds `~~T` to T, so a borrow under two immediately-nested complements
// reaches the lifetime pass with no complement around it, and takes the ordinary
// polarity reading.
//
// The type is assembled rather than written as source because newNegation folds `~~T`
// while resolving the annotation. A source spelling would therefore never hand the
// coalescer the doubled form this test is about, and would pass whatever the coalescer
// did with it.
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
//
// The types are assembled rather than written as source because the test drives the
// extruder itself, at a chosen level and root polarity. Source reaches extrusion only as
// a side effect of inference, which picks both.
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

// ltOutlivesRelation feeds the declared-bound check as well as the printer, so a
// mis-read position reaches further than rendering.
//
// Below, `m`, `a`, `b` and `d` name the lifetime variables the test builds, not the
// names they render under. `m` is an instantiation intermediary outliving `a`, `b` and
// `d`, which puts all four in one connected component while leaving no directed edge
// among `a`, `b` and `d`. Reading the complemented `d` as a join over `a` and `b` would
// assert that `a` and `b` outlive `d`, and checkDeclaredLifetimeBounds would accept a
// declared bound the body never establishes.
//
// The graph is wired directly rather than inferred from source because `m` names no
// borrow, and a signature declaring a lifetime it never borrows under is rejected with
// "lifetime parameter 'm is declared but never used". A real intermediary comes from
// instantiating a borrow-passing function at a call site, which would make this a test
// about inference rather than about the classifier.
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
		"fn <'a>(p: &mut {x: number}, q: &mut {x: number}, r: ~&'a mut {x: number}) -> number",
		renderScheme(&MonoScheme{Ty: fn}))
}

// componentParams gathers every kept param in a join's connected component, so a param
// linked to the join only through an instantiation intermediary is still reported as a
// source. That looseness is deliberate, since an intermediary is exactly how a call's
// argument lifetime reaches the join it feeds. This test pins that a complemented borrow
// meets it on the same footing as a plain one.
//
// The graph below wires `m` as an intermediary outliving `a` and `b`, and `j` as a
// genuine join over `a` and `x`. `b` reaches `j` only through `m`, and both signatures
// still bound it to the join. Those are the variables the test builds, and the printer
// assigns display names in first-appearance order, so `a`, `b`, `x` and `j` render as
// `'a`, `'b`, `'c` and `'d`.
//
// The graph is wired directly for the same reason as
// TestComplementedBorrowAssertsNoOutlivesRelation: `m` names no borrow, so no signature
// can declare it.
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
		"fn <'a: 'd, 'b: 'd, 'c: 'd, 'd>(p: &'a mut {x: number}, q: ~&'b mut {x: number}, s: &'c mut {x: number}) -> &'d mut {x: number}",
		renderScheme(&MonoScheme{Ty: build(true)}))
}

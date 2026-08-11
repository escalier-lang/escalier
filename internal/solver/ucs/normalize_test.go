package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// coreMatch builds the core split a `match` lowers to: one branch per arm, in source
// order, with a guard as a node inside its branch and no `else`, since a catch-all arm
// is an ordinary branch of the core. It stands in for the desugarer, which normalize
// does not need.
func coreMatch(target ast.Expr, arms ...*ast.MatchCase) *CoreSplit {
	expr := ast.NewMatch(target, arms, span(1, 1, 40))
	origin := At(OriginMatchArm, expr)

	branches := make([]*CoreBranch, len(arms))
	for i, armCase := range arms {
		armOrigin := At(OriginMatchArm, armCase)
		var cont Core = &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: armOrigin}
		if armCase.Guard != nil {
			cont = &CoreGuard{
				Cond:   armCase.Guard,
				Cont:   cont,
				Origin: At(OriginGuard, armCase.Guard),
			}
		}
		branches[i] = &CoreBranch{
			Pattern: armCase.Pattern,
			Cont:    cont,
			Arm:     armCase,
			Origin:  armOrigin,
		}
	}

	return &CoreSplit{Scrutinee: NewRoot(target, origin), Branches: branches, Origin: origin}
}

// normalized renders the normalized form of a core term, which is what the shape
// snapshots below lock.
func normalized(c Core) string { return Print(Normalize(c), DefaultPrintOptions()) }

// greaterThan builds the `x > y` guard condition the worked examples use.
func greaterThan() ast.Expr {
	return ast.NewBinary(ident("x"), ident("y"), ast.GreaterThan, ast.Span{})
}

// The catch-all arm of a flat literal match becomes the split's default tail, which is
// the first worked example in planning/ucs/implementation_plan.md.
func TestNormalizeFlatLiteralMatch(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), nil, str("one"), span(2, 5, 18)),
		matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20)),
	)

	// Before normalization:
	//
	//   split n {
	//     pat 1 => leaf "one"
	//     pat _ => leaf "other"
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {
  1 => leaf "one"
} default leaf "other"`))
}

// Arms that test one scrutinee against different tags become branches of one split
// rather than a chain of one-branch splits, so a consumer visits `n` once.
func TestNormalizeMergesSameScrutineeArms(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), nil, str("one"), span(2, 5, 18)),
		matchCase(numPat(2), nil, str("two"), span(3, 5, 18)),
		matchCase(numPat(3), nil, str("three"), span(4, 5, 20)),
		matchCase(wildcardPat(), nil, str("other"), span(5, 5, 20)),
	)

	// Before normalization:
	//
	//   split n {
	//     pat 1 => leaf "one"
	//     pat 2 => leaf "two"
	//     pat 3 => leaf "three"
	//     pat _ => leaf "other"
	//   }
	norm := Normalize(core)
	split, ok := norm.(*NormSplit)
	require.True(t, ok, "the top-level term is a split")
	require.Len(t, split.Branches, 3)
	snaps.MatchInlineSnapshot(t, Print(norm, DefaultPrintOptions()), snaps.Inline(`split n {
  1 => leaf "one"
  2 => leaf "two"
  3 => leaf "three"
} default leaf "other"`))
}

// A guard names where a failed condition continues, which is what makes the form
// backtracking-free. Here the continuation is the split's own tail, the fourth worked
// example in the plan.
func TestNormalizeGuardFallsToTheTail(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(objPat("x", "y"), greaterThan(), ident("x"), span(2, 5, 30)),
		matchCase(wildcardPat(), nil, num(0), span(3, 5, 16)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {x, y} => guard (x > y) => leaf x
	//     pat _ => leaf 0
	//   }
	norm := Normalize(core)
	snaps.MatchInlineSnapshot(t, Print(norm, DefaultPrintOptions()), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; guard (x > y) {
    leaf x
  } default leaf 0
} default leaf 0`))

	// The guard and the split reach the same node, so the arm the guard falls into is
	// the arm the split falls into and neither is duplicated.
	split := norm.(*NormSplit)
	guard := split.Branches[0].Cont.(*NormBind).Cont.(*NormBind).Cont.(*NormGuard)
	require.Same(t, split.Default, guard.Default)
}

// An unguarded catch-all arm always runs, so it becomes the tail and every arm after it
// is unreachable. The split stays, because it is what names the scrutinee.
func TestNormalizeDropsArmsAfterACatchAll(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(identPat("all"), nil, ident("all"), span(2, 5, 20)),
		matchCase(numPat(1), nil, str("one"), span(3, 5, 18)),
	)

	// Before normalization:
	//
	//   split n {
	//     pat all => leaf all
	//     pat 1 => leaf "one"
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {} default bind all = n; leaf all`))
}

// A guarded catch-all does not end the split. Its guard can fail, so the arms after it
// stay reachable and become what it falls into.
func TestNormalizeKeepsArmsAfterAGuardedCatchAll(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(identPat("all"), greaterThan(), ident("all"), span(2, 5, 30)),
		matchCase(numPat(1), nil, str("one"), span(3, 5, 18)),
	)

	// Before normalization:
	//
	//   split n {
	//     pat all => guard (x > y) => leaf all
	//     pat 1 => leaf "one"
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {} default bind all = n; guard (x > y) {
  leaf all
} default split n {
  1 => leaf "one"
} default ✗`))
}

// An `if val` writes its own `else`, which the core keeps as the split's fallthrough
// and normalization moves into the default tail.
func TestNormalizeThreadsTheElseIntoTheTail(t *testing.T) {
	target := ident("p")
	cons := ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(ident("cons"), ast.Span{})}}
	alt := blockBody(ident("alt"))
	expr := ast.NewIfVal(objPat("x", "y"), target, cons, &alt, span(1, 1, 45))
	origin := At(OriginIfVal, expr)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{{
			Pattern: expr.Pattern,
			Cont:    &BodyLeaf{Body: ast.BlockOrExpr{Block: &cons}, Arm: expr, Origin: origin},
			Arm:     expr,
			Origin:  origin,
		}},
		Else:   &BodyLeaf{Body: alt, Arm: expr, Origin: origin},
		Origin: origin,
	}

	// Before normalization, where the `else` is the split's fallthrough rather than a
	// branch:
	//
	//   split p {
	//     pat {x, y} => leaf { cons }
	//   } else leaf { alt }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; leaf { cons }
} default leaf { alt }`))
}

// A `val … else` normalizes the same way, keeping its binding-escape leaf on the
// success path and its fallback in the tail.
func TestNormalizeValElse(t *testing.T) {
	target := ident("p")
	decl := ast.NewVarDecl(ast.ValKind, objPat("x", "y"), nil, target, false, false, span(1, 1, 35))
	decl.Else = &ast.Block{Stmts: []ast.Stmt{ast.NewReturnStmt(nil, ast.Span{})}}
	origin := At(OriginValElse, decl)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{{
			Pattern: decl.Pattern,
			Cont:    &EscapeLeaf{Arm: decl, Origin: origin},
			Arm:     decl,
			Origin:  origin,
		}},
		Else:   &FallbackLeaf{Body: ast.BlockOrExpr{Block: decl.Else}, Arm: decl, Origin: origin},
		Origin: origin,
	}

	// Before normalization:
	//
	//   split p {
	//     pat {x, y} => escape
	//   } else fallback { return }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; escape
} default fallback { return }`))
}

// A nested pattern becomes one split per tag-level, each over the projection the level
// above it matched. This is the second worked example in
// planning/ucs/implementation_plan.md: `l` splits on the `Line` tag, then `l.start`
// splits on the `{x, y}` shape, so nothing sees the deep shape at once.
func TestNormalizeFlattensANestedObjectPattern(t *testing.T) {
	start := objPat("x", "y")
	core := coreMatch(
		ident("l"),
		matchCase(instancePat("Line", fieldPat("start", start)), nil, ident("body"), span(2, 5, 34)),
	)

	// Before normalization the arm's pattern is one deep shape:
	//
	//   split l {
	//     pat Line {start: {x, y}} => leaf body
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split l {
  Line => split l.start {
    {x, y} => bind x = l.start.x, y = l.start.y; leaf body
  } default ✗
} default ✗`))

	// The projection the inner split tests points at the sub-pattern it came from, so a
	// message about that split blames the `{x, y}` the user wrote rather than the
	// internal path `l.start`.
	outer := Normalize(core).(*NormSplit)
	inner := outer.Branches[0].Cont.(*NormSplit)
	require.Same(t, start, inner.Scrutinee.Origin.Node)
	require.Same(t, start, inner.Origin.Node)
}

// A tuple element flattens the same way a field does, splitting on the element the outer
// test projected. The outer test counts the elements and says nothing about their shapes.
func TestNormalizeFlattensANestedTuplePattern(t *testing.T) {
	core := coreMatch(
		ident("xs"),
		matchCase(
			tuplePat(tuplePat(identPat("a"), identPat("b")), identPat("c")),
			nil, ident("a"), span(2, 5, 30),
		),
	)

	// Before normalization:
	//
	//   split xs {
	//     pat [[a, b], c] => leaf a
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split xs {
  [_, _] => split xs.0 {
    [_, _] => bind a = xs.0.0, b = xs.0.1, c = xs.1; leaf a
  } default ✗
} default ✗`))
}

// Flattening recurses to whatever depth the pattern has, one tag-level per split, and
// each split names the projection the one above it matched.
func TestNormalizeFlattensToArbitraryDepth(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(fieldPat("a", fieldPat("b", fieldPat("c", numPat(1)))), nil, str("deep"), span(2, 5, 32)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {a: {b: {c: 1}}} => leaf "deep"
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {a} => split p.a {
    {b} => split p.a.b {
      {c} => split p.a.b.c {
        1 => leaf "deep"
      } default ✗
    } default ✗
  } default ✗
} default ✗`))
}

// A projected split fails into the branch's fallthrough, the same continuation a failed
// guard takes, so flattening adds no backtracking. Here that is the arm below, and the
// projected split and the outer split reach the same node rather than two copies of it.
func TestNormalizeNestedSplitFallsToTheArmBelow(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(fieldPat("x", numPat(1)), nil, str("one"), span(2, 5, 22)),
		matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {x: 1} => leaf "one"
	//     pat _ => leaf "other"
	//   }
	norm := Normalize(core)
	snaps.MatchInlineSnapshot(t, Print(norm, DefaultPrintOptions()), snaps.Inline(`split p {
  {x} => split p.x {
    1 => leaf "one"
  } default leaf "other"
} default leaf "other"`))

	outer := norm.(*NormSplit)
	inner := outer.Branches[0].Cont.(*NormSplit)
	require.Same(t, outer.Default, inner.Default)
}

// The names an arm binds sit under the splits its sub-patterns became, not over them. A
// projected split's default runs the arm below, so a name in scope over that default
// would put one arm's leaves in the scope of another arm's body.
func TestNormalizeKeepsTheFallthroughOutOfTheArmsScope(t *testing.T) {
	pattern := ast.NewObjectPat([]ast.ObjPatElem{
		shorthandElem("x"),
		keyValueElem("y", tuplePat(identPat("z"))),
	}, ast.Span{})
	core := coreMatch(
		ident("p"),
		matchCase(pattern, nil, str("one"), span(2, 5, 28)),
		matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {x, y: [z]} => leaf "one"
	//     pat _ => leaf "other"
	//   }
	norm := Normalize(core)
	snaps.MatchInlineSnapshot(t, Print(norm, DefaultPrintOptions()), snaps.Inline(`split p {
  {x, y} => split p.y {
    [_] => bind z = p.y.0, x = p.x; leaf "one"
  } default leaf "other"
} default leaf "other"`))

	// `x` is named at the outer tag-level and still binds below the split over `p.y`,
	// which is what keeps that split's default clear of it.
	_, isSplit := norm.(*NormSplit).Branches[0].Cont.(*NormSplit)
	require.True(t, isSplit, "the branch continues into a split rather than a bind")
}

// pathNodes collects the distinct *Scrutinee nodes a normalized term names, keyed by the
// projection path each one renders to. seen keeps a term the rewrite shared from being
// walked twice.
func pathNodes(t Norm, seen set.Set[Norm], out map[string]set.Set[*Scrutinee]) {
	if t == nil || seen.Contains(t) {
		return
	}
	seen.Add(t)
	add := func(s *Scrutinee) {
		path := scrutineeString(s)
		if _, found := out[path]; !found {
			out[path] = set.NewSet[*Scrutinee]()
		}
		out[path].Add(s)
	}
	switch n := t.(type) {
	case *NormSplit:
		add(n.Scrutinee)
		for _, branch := range n.Branches {
			pathNodes(branch.Cont, seen, out)
		}
		pathNodes(n.Default, seen, out)
	case *NormGuard:
		pathNodes(n.Cont, seen, out)
		pathNodes(n.Default, seen, out)
	case *NormBind:
		add(n.Source)
		pathNodes(n.Cont, seen, out)
	}
}

// Merging copies a branch, and each copy names the same projections. Every path is one
// node across the copies, which is what lets a consumer evaluate `p.y` once and read the
// split over it and the binds under it off that one value.
func TestNormalizeNamesOneScrutineePerPath(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(fieldPat("x", numPat(1)), nil, str("first"), span(2, 5, 22)),
		matchCase(fieldPat("y", objPat("z")), nil, str("second"), span(3, 5, 26)),
		matchCase(wildcardPat(), nil, str("other"), span(4, 5, 20)),
	)

	// The second arm is built twice, once for its own branch of the split over `p` and
	// once inside the first arm's fallthrough, so its projections are the ones a
	// per-copy read would duplicate.
	nodes := map[string]set.Set[*Scrutinee]{}
	pathNodes(Normalize(core), set.NewSet[Norm](), nodes)

	require.Contains(t, nodes, "p.y.z")
	for path, found := range nodes {
		require.Equal(t, 1, found.Len(), "the term names one scrutinee node for %s", path)
	}
}

// Two arms whose patterns nest under the same tag chain rather than sharing one projected
// split. The second arm's `{a}` is already proved, so the first arm falls straight into a
// second split over `p.a`.
func TestNormalizeChainsArmsThatNestUnderOneTag(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(fieldPat("a", objPat("x")), nil, str("obj"), span(2, 5, 26)),
		matchCase(fieldPat("a", tuplePat(identPat("y"))), nil, str("tuple"), span(3, 5, 26)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {a: {x}} => leaf "obj"
	//     pat {a: [y]} => leaf "tuple"
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {a} => split p.a {
    {x} => bind x = p.a.x; leaf "obj"
  } default split p.a {
    [_] => bind y = p.a.0; leaf "tuple"
  } default ✗
} default ✗`))
}

// The leaves a nested split binds are in scope for the guard below it, since the guard
// sits inside the innermost split rather than above the ones that project its values.
func TestNormalizeGuardReadsNestedBinds(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(fieldPat("start", objPat("x", "y")), greaterThan(), ident("x"), span(2, 5, 40)),
		matchCase(wildcardPat(), nil, num(0), span(3, 5, 16)),
	)

	// Before normalization:
	//
	//   split p {
	//     pat {start: {x, y}} guard (x > y) => leaf x
	//     pat _ => leaf 0
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {start} => split p.start {
    {x, y} => bind x = p.start.x, y = p.start.y; guard (x > y) {
      leaf x
    } default leaf 0
  } default leaf 0
} default leaf 0`))
}

// A pattern that tests no tag of its own has no split to become. A bare rest is the only
// one, and it stays a nameless bind that hands the pattern to the solver's walk.
func TestNormalizeKeepsATagLessPatternWhole(t *testing.T) {
	rest := ast.NewRestPat(identPat("rest"), ast.Span{})
	core := coreMatch(ident("xs"), matchCase(rest, nil, ident("rest"), span(2, 5, 24)))

	// Before normalization:
	//
	//   split xs {
	//     pat ...rest => leaf rest
	//   }
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split xs {} default bind ...rest = xs; leaf rest`))
}

// Merging must not cost a branch its provenance. Every branch of the merged split still
// points at the arm the user wrote, and the origin tags still name the construct each
// arm lowered from.
func TestNormalizeKeepsArmBackReferences(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	two := matchCase(numPat(2), greaterThan(), str("two"), span(3, 5, 26))
	other := matchCase(wildcardPat(), nil, str("other"), span(4, 5, 20))
	core := coreMatch(ident("n"), one, two, other)

	// Before normalization:
	//
	//   split n {
	//     pat 1 => leaf "one"
	//     pat 2 => guard (x > y) => leaf "two"
	//     pat _ => leaf "other"
	//   }
	norm := Normalize(core)
	split := norm.(*NormSplit)
	require.Same(t, one, split.Branches[0].Arm)
	require.Same(t, two, split.Branches[1].Arm)

	opts := DefaultPrintOptions()
	opts.ShowArms = true
	opts.ShowOrigins = true
	snaps.MatchInlineSnapshot(t, Print(norm, opts), snaps.Inline(`split n [match arm] {
  1 [match arm] arm=2:5-2:18 => leaf "one" [match arm] arm=2:5-2:18
  2 [match arm] arm=3:5-3:26 => guard (x > y) [guard] {
    leaf "two" [match arm] arm=3:5-3:26
  } default leaf "other" [match arm] arm=4:5-4:20
} default leaf "other" [match arm] arm=4:5-4:20`))
}

// A split flattening introduced blames the sub-pattern it tests and still points back at
// the arm the user wrote. The `at=` span is the nested `{x, y}`, and `arm=` is the whole
// arm, so a message about the projected split can name either.
func TestNormalizeFlattenedSplitKeepsItsArmAndPattern(t *testing.T) {
	x := ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), false, nil, nil, span(2, 20, 21))
	y := ast.NewObjShorthandPat(ast.NewIdentifier("y", ast.Span{}), false, nil, nil, span(2, 23, 24))
	start := ast.NewObjectPat([]ast.ObjPatElem{x, y}, span(2, 19, 25))
	pattern := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("start", start)}, span(2, 5, 26))
	armCase := matchCase(pattern, nil, ident("x"), span(2, 5, 31))
	core := coreMatch(ident("p"), armCase)

	// Before normalization:
	//
	//   split p {
	//     pat {start: {x, y}} => leaf x
	//   }
	opts := DefaultPrintOptions()
	opts.ShowArms = true
	opts.ShowSpans = true
	norm := Normalize(core)
	snaps.MatchInlineSnapshot(t, Print(norm, opts), snaps.Inline(`split p at=1:1-1:40 {
  {start} at=2:5-2:31 arm=same => split p.start at=2:19-2:25 {
    {x, y} at=2:19-2:25 arm=2:5-2:31 => bind x = p.start.x at=2:20-2:21, y = p.start.y at=2:23-2:24; leaf x at=2:5-2:31 arm=same
  } default ✗
} default ✗`))

	inner := norm.(*NormSplit).Branches[0].Cont.(*NormSplit)
	require.Same(t, armCase, inner.Branches[0].Arm)
	require.Same(t, start, inner.Scrutinee.Origin.Node)
}

// Normalization rewrites the splits around a leaf and leaves the leaf itself alone, so
// the arm body a consumer infers is the node the core held.
func TestNormalizeKeepsLeafNodes(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), nil, str("one"), span(2, 5, 18)),
		matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20)),
	)

	// Before normalization:
	//
	//   split n {
	//     pat 1 => leaf "one"
	//     pat _ => leaf "other"
	//   }
	matched := core.Branches[0].Cont
	tailLeaf := core.Branches[1].Cont

	split := Normalize(core).(*NormSplit)
	require.Same(t, matched, split.Branches[0].Cont)
	require.Same(t, tailLeaf, split.Default)
}

// A core split with no branches still becomes a split, so the scrutinee it tests stays
// named. Nothing covers the value, which the printer renders `✗`.
func TestNormalizeEmptySplit(t *testing.T) {
	core := coreMatch(ident("p"))

	// Before normalization the split is empty too, written `split p {}`. It survives
	// because it is what names the scrutinee.
	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {} default ✗`))
}

// A term that is not a split normalizes to itself, so a caller can hand normalize any
// core term rather than only the top-level split.
func TestNormalizeBareTerms(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	leaf := &BodyLeaf{Body: exprBody(num(1)), Origin: origin}

	require.Nil(t, Normalize(nil))
	require.Same(t, leaf, Normalize(leaf))

	bind := &CoreBind{
		Name:   "x",
		Source: NewRoot(ident("p"), origin),
		Cont:   leaf,
		Origin: origin,
	}
	require.Equal(t, "bind x = p; leaf 1", normalized(bind))
}

// A narrowing annotation is a tag the branch tests, so the branch it sits on can fail and
// whatever the surface wrote below it stays reachable. `if val x: number = u` runs its
// consequent only for a `u` holding a `number`, and the `match` arm `x: number => x` runs
// only for the same values. All three forms reach the one tag.
func TestNormalizeAnnotationIsARefutableTag(t *testing.T) {
	tests := map[string]struct {
		core *CoreSplit
		want string
	}{
		// An `if val` writes the annotation inside its pattern.
		"IfVal": {
			core: DesugarIfVal(findExpr[*ast.IfValExpr](t, `if val x: number = u { cons } else { alt }`)),
			want: `split u {
  : number => bind x = u; leaf { cons }
} default leaf { alt }`,
		},
		// A `val … else` writes it on the declaration. Both reach the same tag.
		"ValElse": {
			core: mustDesugarValElse(t, `val x: number = u else { 0 }`),
			want: `split u {
  : number => bind x = u; escape
} default fallback { 0 }`,
		},
		// A `match` arm writes it inside its pattern, the node an `if val` uses. The arm
		// below the annotated one keeps a branch of its own rather than being dropped as
		// unreachable, which is what a catch-all arm above it would have done.
		"Match": {
			core: DesugarMatch(findExpr[*ast.MatchExpr](t, `match u { x: number => x, other => other }`)),
			want: `split u {
  : number => bind x = u; leaf x
} default bind other = u; leaf other`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, normalized(test.core))
		})
	}
}

// A pattern with no annotation names no tag at all, so its branch always runs and becomes
// the split's tail. The `else` below it is then unreachable and normalization drops it,
// which is what tells a consumer the failure path can never be taken.
func TestNormalizeUnannotatedBindingDropsTheElse(t *testing.T) {
	core := DesugarIfVal(findExpr[*ast.IfValExpr](t, `if val x = u { cons } else { alt }`))

	require.Equal(t, "split u {} default bind x = u; leaf { cons }", normalized(core))
}

// A destructuring pattern keeps its own shape as the branch's tag. The declaration's
// annotation is left off the branch, so no branch ends up carrying two tags.
func TestNormalizeDestructuringKeepsItsOwnTag(t *testing.T) {
	core := mustDesugarValElse(t, `val [a, b]: [number, string] = u else { return }`)

	require.Equal(t, `split u {
  [_, _] => bind a = u.0, b = u.1; escape
} default fallback { return }`, normalized(core))
}

// mustDesugarValElse lowers the first declaration of src, which the tests above write as a
// `val … else` so the lowering applies.
func mustDesugarValElse(t *testing.T, src string) *CoreSplit {
	t.Helper()
	core, ok := DesugarValElse(findVarDecl(t, src))
	require.True(t, ok)
	return core
}

// A type annotation on a pattern leaf is not a tag the branch tests. Only the annotation a
// refutable form writes on its whole binding becomes an AnnTest. A leaf's own annotation
// stays on the bind, where the solver reads it off the pattern node, so the branch's tag is
// the container's shape as it would be without any annotation.
func TestNormalizeNestedAnnotationIsNotATag(t *testing.T) {
	// `{a: x: string}` annotates a key-value pattern's value, and `{b::number}` a shorthand
	// element. The two carry their annotation on different nodes, so both are checked.
	core := DesugarIfVal(findExpr[*ast.IfValExpr](t, `if val {a: x: string, b::number} = p { cons } else { alt }`))

	require.Nil(t, core.Branches[0].Ann)
	branch := Normalize(core).(*NormSplit).Branches[0]
	require.IsType(t, &ObjectTest{}, branch.Test)

	value := branch.Cont.(*NormBind)
	require.Equal(t, "x", value.Name)
	require.NotNil(t, value.Pat.(*ast.IdentPat).TypeAnn)

	shorthand := value.Cont.(*NormBind)
	require.Equal(t, "b", shorthand.Name)
	require.NotNil(t, shorthand.Elem.TypeAnn)
}

// A tuple element's annotation behaves the same way: the branch tests the tuple's arity and
// each annotated element is an ordinary bind on its projection.
func TestNormalizeNestedTupleAnnotationIsNotATag(t *testing.T) {
	core := DesugarIfVal(findExpr[*ast.IfValExpr](t, `if val [a: string, b: number] = p { cons } else { alt }`))

	require.Nil(t, core.Branches[0].Ann)
	branch := Normalize(core).(*NormSplit).Branches[0]
	require.IsType(t, &TupleTest{}, branch.Test)

	first := branch.Cont.(*NormBind)
	require.Equal(t, "a", first.Name)
	require.NotNil(t, first.Pat.(*ast.IdentPat).TypeAnn)

	second := first.Cont.(*NormBind)
	require.Equal(t, "b", second.Name)
	require.NotNil(t, second.Pat.(*ast.IdentPat).TypeAnn)
}

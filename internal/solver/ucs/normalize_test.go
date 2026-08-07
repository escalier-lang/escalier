package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
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

// Two arms of the same shape overlap: the second matches whenever the first does. The
// first's test already proved the second's, so the guard falls straight into the second
// arm's continuation instead of testing `{x, y}` again.
func TestNormalizeOverlappingArmsOfTheSameShape(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(objPat("x", "y"), greaterThan(), ident("x"), span(2, 5, 30)),
		matchCase(objPat("x", "y"), nil, num(0), span(3, 5, 22)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; guard (x > y) {
    leaf x
  } default bind x = p.x, y = p.y; leaf 0
  {x, y} => bind x = p.x, y = p.y; leaf 0
} default ✗`))
}

// Two arms whose shapes overlap without either proving the other keep their tests. A
// value with an `x` and a `y` reaches the second arm when the guard fails, so the
// fallthrough tests `{x}` rather than assuming it.
func TestNormalizeOverlappingArmsOfDifferentShapes(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(objPat("x", "y"), greaterThan(), ident("x"), span(2, 5, 30)),
		matchCase(objPat("x"), nil, num(1), span(3, 5, 20)),
		matchCase(wildcardPat(), nil, num(0), span(4, 5, 16)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; guard (x > y) {
    leaf x
  } default split p {
    {x} => bind x = p.x; leaf 1
  } default leaf 0
  {x} => bind x = p.x; leaf 1
} default leaf 0`))
}

// An arm the guarded one rules out is dropped from what the guard falls into. No value
// is both 1 and 2, so a failed guard on the `1` arm continues past the `2` arm.
func TestNormalizeDropsArmsTheGuardedTestRulesOut(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), greaterThan(), str("one"), span(2, 5, 26)),
		matchCase(numPat(2), nil, str("two"), span(3, 5, 18)),
		matchCase(wildcardPat(), nil, str("other"), span(4, 5, 20)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {
  1 => guard (x > y) {
    leaf "one"
  } default leaf "other"
  2 => leaf "two"
} default leaf "other"`))
}

// Two guarded arms of the same tag chain: the first proves the second's test, so the
// second runs without re-testing, and its own guard still falls through to the arm
// below. Making an arm unconditional must not cut off what it falls into.
func TestNormalizeChainsGuardsOnOneTag(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), ident("f"), str("a"), span(2, 5, 24)),
		matchCase(numPat(1), ident("g"), str("b"), span(3, 5, 24)),
		matchCase(wildcardPat(), nil, str("c"), span(4, 5, 16)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {
  1 => guard (f) {
    leaf "a"
  } default guard (g) {
    leaf "b"
  } default leaf "c"
  1 => guard (g) {
    leaf "b"
  } default leaf "c"
} default leaf "c"`))
}

// A branch holding a sub-pattern this stage does not flatten is never made
// unconditional. Passing the `{x}` test says nothing about whether the literal `2`
// matched, so the arm below the second one stays reachable.
func TestNormalizeKeepsATestWhoseBranchStillHasASubPattern(t *testing.T) {
	first := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", numPat(1))}, ast.Span{})
	second := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", numPat(2))}, ast.Span{})
	core := coreMatch(
		ident("p"),
		matchCase(first, ident("g"), str("a"), span(2, 5, 26)),
		matchCase(second, nil, str("b"), span(3, 5, 22)),
		matchCase(wildcardPat(), nil, str("c"), span(4, 5, 16)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x} => bind 1 = p.x; guard (g) {
    leaf "a"
  } default split p {
    {x} => bind 2 = p.x; leaf "b"
  } default leaf "c"
  {x} => bind 2 = p.x; leaf "b"
} default leaf "c"`))
}

// An unguarded catch-all arm always runs, so it becomes the tail and every arm after it
// is unreachable. The split stays, because it is what names the scrutinee.
func TestNormalizeDropsArmsAfterACatchAll(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(identPat("all"), nil, ident("all"), span(2, 5, 20)),
		matchCase(numPat(1), nil, str("one"), span(3, 5, 18)),
	)

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

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {} default bind all = n; guard (x > y) {
  leaf all
} default split n {
  1 => leaf "one"
} default ✗`))
}

// A nested sub-pattern is left whole inside its branch. The branch tests the `Line` tag
// alone and keeps `{x, y}` as a nameless bind on the projection `l.start`, which the
// stage that flattens nesting turns into a split of its own.
func TestNormalizeKeepsANestedPatternWhole(t *testing.T) {
	pattern := ast.NewInstancePat(
		ast.NewIdentifier("Line", ast.Span{}),
		ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("start", objPat("x", "y"))}, ast.Span{}),
		ast.Span{},
	)
	body := ast.NewArray([]ast.Expr{ident("x"), ident("y")}, ast.Span{})
	core := coreMatch(ident("l"), matchCase(pattern, nil, body, span(2, 5, 40)))

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split l {
  Line => bind {x, y} = l.start; leaf [x, y]
} default ✗`))
}

// Every pattern kind reaches its leaves through a projection off the branch's
// scrutinee, whatever tag the branch tests. The cases are the worked examples in the
// plan, minus the nested flattening a later stage adds.
func TestNormalizePatternKinds(t *testing.T) {
	tests := []struct {
		name   string
		target string
		arms   []*ast.MatchCase
		want   string
	}{
		{
			name:   "instance pattern",
			target: "p",
			arms: []*ast.MatchCase{matchCase(
				ast.NewInstancePat(
					ast.NewIdentifier("Point", ast.Span{}),
					objPat("x", "y"),
					ast.Span{},
				),
				nil,
				ast.NewArray([]ast.Expr{ident("x"), ident("y")}, ast.Span{}),
				span(2, 5, 30),
			)},
			want: "split p {\n  Point => bind x = p.x, y = p.y; leaf [x, y]\n} default ✗",
		},
		{
			name:   "extractor pattern",
			target: "r",
			arms: []*ast.MatchCase{
				matchCase(
					ast.NewExtractorPat(
						ast.NewIdentifier("Ok", ast.Span{}),
						[]ast.Pat{identPat("v")},
						ast.Span{},
					),
					nil,
					ident("v"),
					span(2, 5, 16),
				),
				matchCase(
					ast.NewExtractorPat(
						ast.NewIdentifier("Err", ast.Span{}),
						[]ast.Pat{wildcardPat()},
						ast.Span{},
					),
					nil,
					num(0),
					span(3, 5, 16),
				),
			},
			want: "split r {\n  Ok(_) => bind v = r.#0; leaf v\n  Err(_) => leaf 0\n} default ✗",
		},
		{
			name:   "tuple pattern",
			target: "xs",
			arms: []*ast.MatchCase{matchCase(
				ast.NewTuplePat([]ast.Pat{identPat("a"), identPat("b")}, ast.Span{}),
				nil,
				ident("a"),
				span(2, 5, 22),
			)},
			want: "split xs {\n  [_, _] => bind a = xs.0, b = xs.1; leaf a\n} default ✗",
		},
		{
			name:   "tuple rest pattern",
			target: "xs",
			arms: []*ast.MatchCase{matchCase(
				ast.NewTuplePat(
					[]ast.Pat{identPat("first"), ast.NewRestPat(identPat("rest"), ast.Span{})},
					ast.Span{},
				),
				nil,
				ident("first"),
				span(2, 5, 30),
			)},
			want: "split xs {\n  [_, ...] => bind first = xs.0, rest = xs[1..]; leaf first\n} default ✗",
		},
		{
			name:   "object rest pattern",
			target: "p",
			arms: []*ast.MatchCase{matchCase(
				ast.NewObjectPat(
					[]ast.ObjPatElem{shorthandElem("x"), objRestElem("rest")},
					ast.Span{},
				),
				nil,
				ident("rest"),
				span(2, 5, 28),
			)},
			want: "split p {\n  {x, ...} => bind x = p.x, rest = p \\ {x}; leaf rest\n} default ✗",
		},
		{
			name:   "renaming object pattern",
			target: "p",
			arms: []*ast.MatchCase{matchCase(
				ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", identPat("a"))}, ast.Span{}),
				nil,
				ident("a"),
				span(2, 5, 24),
			)},
			want: "split p {\n  {x} => bind a = p.x; leaf a\n} default ✗",
		},
		{
			name:   "wildcard field",
			target: "p",
			arms: []*ast.MatchCase{matchCase(
				ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", wildcardPat())}, ast.Span{}),
				nil,
				num(0),
				span(2, 5, 24),
			)},
			want: "split p {\n  {x} => leaf 0\n} default ✗",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core := coreMatch(ident(test.target), test.arms...)
			require.Equal(t, test.want, normalized(core))
		})
	}
}

// A field with a default is optional: `{x = 0}` binds even when the field is absent, so
// the test must not demand it. The two spellings of a default, on a shorthand element
// and on a renamed field's value, both mark the key optional.
func TestNormalizeMarksDefaultedFieldsOptional(t *testing.T) {
	shorthand := ast.NewObjShorthandPat(
		ast.NewIdentifier("x", ast.Span{}), false, nil, num(0), ast.Span{},
	)
	renamed := ast.NewObjKeyValuePat(
		ast.NewIdentifier("y", ast.Span{}),
		ast.NewIdentPat("b", false, nil, num(1), ast.Span{}),
		ast.Span{},
	)
	pattern := ast.NewObjectPat([]ast.ObjPatElem{shorthand, renamed}, ast.Span{})
	core := coreMatch(ident("p"), matchCase(pattern, nil, num(0), span(2, 5, 30)))

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x?, y?} => bind x = p.x, b = p.y; leaf 0
} default ✗`))
}

// A rest anywhere but last names no suffix a projection can reach, so the test covers
// the elements before it and nothing after it binds. The pattern is unsupported
// downstream, and the pass that lowers the surface is what reports it.
func TestNormalizeNonTrailingTupleRest(t *testing.T) {
	pattern := ast.NewTuplePat([]ast.Pat{
		identPat("first"),
		ast.NewRestPat(identPat("mid"), ast.Span{}),
		identPat("last"),
	}, ast.Span{})
	core := coreMatch(ident("xs"), matchCase(pattern, nil, ident("first"), span(2, 5, 34)))

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split xs {
  [_, ...] => bind first = xs.0; leaf first
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

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; escape
} default fallback { return }`))
}

// Merging must not cost a branch its provenance. Every branch of the merged split still
// points at the arm the user wrote, and the origin tags still name the construct each
// arm lowered from.
func TestNormalizeKeepsArmBackReferences(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	two := matchCase(numPat(2), greaterThan(), str("two"), span(3, 5, 26))
	other := matchCase(wildcardPat(), nil, str("other"), span(4, 5, 20))
	core := coreMatch(ident("n"), one, two, other)

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

// Every projection under a branch hangs off the one scrutinee node the split tests, so
// a consumer evaluates `f()` once and reads both fields off that one value.
func TestNormalizeSharesTheScrutinee(t *testing.T) {
	target := ast.NewCall(ident("f"), []ast.Expr{}, false, ast.Span{})
	core := coreMatch(target, matchCase(objPat("x", "y"), nil, ident("x"), span(2, 5, 24)))

	split := Normalize(core).(*NormSplit)
	first := split.Branches[0].Cont.(*NormBind)
	second := first.Cont.(*NormBind)
	require.Same(t, split.Scrutinee, first.Source.Parent)
	require.Same(t, split.Scrutinee, second.Source.Parent)
	require.Equal(t, "f().x", first.Source.String())
	require.Equal(t, "f().y", second.Source.String())
}

// Normalization rewrites the splits around a leaf and leaves the leaf itself alone, so
// the arm body a consumer infers is the node the core held.
func TestNormalizeKeepsLeafNodes(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), nil, str("one"), span(2, 5, 18)),
		matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20)),
	)
	matched := core.Branches[0].Cont
	tailLeaf := core.Branches[1].Cont

	split := Normalize(core).(*NormSplit)
	require.Same(t, matched, split.Branches[0].Cont)
	require.Same(t, tailLeaf, split.Default)
}

// A projection points at the pattern leaf it came from rather than at the whole arm, so
// a message about one field blames the field the user wrote.
func TestNormalizeProjectionsPointAtTheirPatternLeaf(t *testing.T) {
	value := ast.NewIdentPat("a", false, nil, nil, span(2, 12, 13))
	pattern := ast.NewObjectPat([]ast.ObjPatElem{ast.NewObjKeyValuePat(
		ast.NewIdentifier("x", ast.Span{}), value, ast.Span{},
	)}, ast.Span{})
	core := coreMatch(ident("p"), matchCase(pattern, nil, ident("a"), span(2, 5, 24)))

	split := Normalize(core).(*NormSplit)
	bind := split.Branches[0].Cont.(*NormBind)
	require.Same(t, value, bind.Pat)
	require.Same(t, value, bind.Source.Origin.Node)
	require.Equal(t, OriginMatchArm, bind.Source.Origin.Kind)
}

// A shorthand element carries an annotation, a default, and a `mut` marker, none of
// which the bound name holds. The bind points at the element so the solver can read
// them when it binds the leaf.
func TestNormalizeKeepsShorthandElements(t *testing.T) {
	elem := ast.NewObjShorthandPat(
		ast.NewIdentifier("x", ast.Span{}), true, ast.NewNumberTypeAnn(ast.Span{}), nil, span(2, 7, 20),
	)
	pattern := ast.NewObjectPat([]ast.ObjPatElem{elem}, ast.Span{})
	core := coreMatch(ident("p"), matchCase(pattern, nil, ident("x"), span(2, 5, 30)))

	split := Normalize(core).(*NormSplit)
	bind := split.Branches[0].Cont.(*NormBind)
	require.Equal(t, "x", bind.Name)
	require.Nil(t, bind.Pat)
	require.Same(t, elem, bind.Elem)
	require.Same(t, elem, bind.Source.Origin.Node)
}

// A core split with no branches still becomes a split, so the scrutinee it tests stays
// named. Nothing covers the value, which the printer renders `✗`.
func TestNormalizeEmptySplit(t *testing.T) {
	core := coreMatch(ident("p"))

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

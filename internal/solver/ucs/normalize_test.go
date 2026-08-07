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

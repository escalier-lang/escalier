package ucs

import (
	"math/big"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// Two arms of the same shape overlap: the second matches whenever the first does. The
// first's test already proved the second's, so the guard falls straight into the second
// arm's continuation instead of testing `{x, y}` again.
//
// That leaves no branch for the second arm. One would be dead, since reaching it means
// the identical test above it failed, and it would carry a second copy of the arm's
// binds and body.
func TestSpecializeOverlappingArmsOfTheSameShape(t *testing.T) {
	core := coreMatch(
		ident("p"),
		matchCase(objPat("x", "y"), greaterThan(), ident("x"), span(2, 5, 30)),
		matchCase(objPat("x", "y"), nil, num(0), span(3, 5, 22)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; guard (x > y) {
    leaf x
  } default bind x = p.x, y = p.y; leaf 0
} default ✗`))
}

// Two arms whose shapes overlap without either proving the other keep their tests. A
// value with an `x` and a `y` reaches the second arm when the guard fails, so the
// fallthrough tests `{x}` rather than assuming it.
func TestSpecializeOverlappingArmsOfDifferentShapes(t *testing.T) {
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
func TestSpecializeDropsArmsTheGuardedTestRulesOut(t *testing.T) {
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
//
// The chain leaves one branch rather than two, since the second arm's continuation
// already runs inside the first branch's fallthrough.
func TestSpecializeChainsGuardsOnOneTag(t *testing.T) {
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
} default leaf "c"`))
}

// Only a branch this rewrite duplicated is dropped. Two unguarded arms of the same tag
// inline nothing, since the first cannot fail, so the second keeps its branch even
// though nothing reaches it. That branch is the only record of an arm the user wrote
// dead, and the coverage check is what reports it.
func TestSpecializeKeepsAnArmNothingInlined(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), nil, str("a"), span(2, 5, 20)),
		matchCase(numPat(1), nil, str("b"), span(3, 5, 20)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {
  1 => leaf "a"
  1 => leaf "b"
} default ✗`))
}

// The two rules meet on one split. The guarded arm's fallthrough takes the arm below it,
// so that arm's branch goes. The third arm reaches nothing and nothing inlines it, since
// the second arm cannot fail and ends the fallthrough, so its branch stays for the
// coverage check to report.
func TestSpecializeKeepsTheArmNoFallthroughCouldTake(t *testing.T) {
	core := coreMatch(
		ident("n"),
		matchCase(numPat(1), ident("f"), str("a"), span(2, 5, 24)),
		matchCase(numPat(1), nil, str("b"), span(3, 5, 20)),
		matchCase(numPat(1), nil, str("c"), span(4, 5, 20)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split n {
  1 => guard (f) {
    leaf "a"
  } default leaf "b"
  1 => leaf "c"
} default ✗`))
}

// A tag the matched test proved is not re-tested, and a branch whose pattern nests below
// that tag is no exception. The second arm's `{x}` is cleared in the first arm's
// fallthrough, leaving only the split over `p.x` to run there, and the arm's branch in
// the outer split goes with it, since that fallthrough already runs its continuation.
//
// Clearing the test does not make the arm cover what reaches it. Its split over `p.x`
// matches 2 alone, so `"c"` still runs when the guard fails and `p.x` is neither literal.
func TestSpecializeClearsANestedBranchsTest(t *testing.T) {
	first := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", numPat(1))}, ast.Span{})
	second := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", numPat(2))}, ast.Span{})
	core := coreMatch(
		ident("p"),
		matchCase(first, ident("g"), str("a"), span(2, 5, 26)),
		matchCase(second, nil, str("b"), span(3, 5, 22)),
		matchCase(wildcardPat(), nil, str("c"), span(4, 5, 16)),
	)

	snaps.MatchInlineSnapshot(t, normalized(core), snaps.Inline(`split p {
  {x} => split p.x {
    1 => guard (g) {
      leaf "a"
    } default split p.x {
      2 => leaf "b"
    } default leaf "c"
  } default split p.x {
    2 => leaf "b"
  } default leaf "c"
} default leaf "c"`))
}

// A tag one arm proves is not always a tag another arm's value shares. Only a pair the
// two relations can decide changes the fallthrough, and every other pair is left to
// re-test.
func TestTestRelations(t *testing.T) {
	one := &LitTest{Lit: ast.NewNumber(1, ast.Span{})}
	alsoOne := &LitTest{Lit: ast.NewNumber(1, ast.Span{})}
	two := &LitTest{Lit: ast.NewNumber(2, ast.Span{})}
	xy := &ObjectTest{Keys: keys("x", "y")}
	yx := &ObjectTest{Keys: keys("y", "x")}
	x := &ObjectTest{Keys: keys("x")}
	xOptional := &ObjectTest{Keys: []ObjectKey{{Name: "x", Optional: true}}}
	pair := &TupleTest{Len: 2}
	pairPrefix := &TupleTest{Len: 2, Rest: TrailingRest}
	point := &ClassTest{Name: ast.NewIdentifier("Point", ast.Span{})}
	line := &ClassTest{Name: ast.NewIdentifier("Line", ast.Span{})}
	ok1 := &ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 1}
	ok2 := &ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 2}

	tests := []struct {
		name     string
		a, b     Test
		implies  bool
		disjoint bool
	}{
		{name: "a literal proves itself", a: one, b: alsoOne, implies: true},
		{name: "two literals are disjoint", a: one, b: two, disjoint: true},
		{name: "an object proves the same keys in any order", a: xy, b: yx, implies: true},
		{name: "a wider object does not prove a narrower one", a: xy, b: x},
		{name: "an optional key is a different test", a: x, b: xOptional},
		{name: "a tuple proves the same length", a: pair, b: &TupleTest{Len: 2}, implies: true},
		{name: "an exact tuple does not prove a prefix", a: pair, b: pairPrefix},
		{name: "a class proves itself", a: point, b: point, implies: true},
		{name: "two classes may share a value", a: point, b: line},
		{name: "an extractor proves itself at one arity", a: ok1, b: ok1, implies: true},
		{name: "arity separates two extractor tests", a: ok1, b: ok2},
		{name: "a literal and an object may share a value", a: one, b: x},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.implies, testImplies(test.a, test.b), "testImplies")
			require.Equal(t, test.disjoint, testsDisjoint(test.a, test.b), "testsDisjoint")
		})
	}
}

// capturedBy asks the reverse of what specialize asks: whether an earlier test takes
// every value of a later one, which is what makes the later branch unreachable. Getting
// the direction wrong would drop a branch that can still run, so the wider-then-narrower
// order is pinned here.
func TestCapturedBy(t *testing.T) {
	one := candidate{index: 0, test: &LitTest{Lit: ast.NewNumber(1, ast.Span{})}}
	two := candidate{index: 1, test: &LitTest{Lit: ast.NewNumber(2, ast.Span{})}}
	catchAll := candidate{index: 2}

	tests := []struct {
		name     string
		earlier  []candidate
		test     Test
		captured bool
	}{
		{name: "no earlier branch", test: one.test},
		{name: "the same tag", earlier: []candidate{one}, test: one.test, captured: true},
		{name: "a different tag", earlier: []candidate{two}, test: one.test},
		{
			name:     "one of several earlier tags",
			earlier:  []candidate{two, one},
			test:     one.test,
			captured: true,
		},
		{name: "an earlier branch with no test", earlier: []candidate{catchAll}, test: one.test},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.captured, capturedBy(test.earlier, test.test))
		})
	}
}

// A literal test compares values rather than nodes, so the `1` of one arm and the `1`
// of another are the same tag. Every literal kind compares by its own value.
func TestLitEqual(t *testing.T) {
	tests := []struct {
		name  string
		a, b  ast.Lit
		equal bool
	}{
		{name: "numbers", a: ast.NewNumber(1, ast.Span{}), b: ast.NewNumber(1, ast.Span{}), equal: true},
		{name: "different numbers", a: ast.NewNumber(1, ast.Span{}), b: ast.NewNumber(2, ast.Span{})},
		{name: "strings", a: ast.NewString("a", ast.Span{}), b: ast.NewString("a", ast.Span{}), equal: true},
		{name: "different strings", a: ast.NewString("a", ast.Span{}), b: ast.NewString("b", ast.Span{})},
		{name: "booleans", a: ast.NewBoolean(true, ast.Span{}), b: ast.NewBoolean(true, ast.Span{}), equal: true},
		{name: "different booleans", a: ast.NewBoolean(true, ast.Span{}), b: ast.NewBoolean(false, ast.Span{})},
		{name: "regexes", a: ast.NewRegex("/a/", ast.Span{}), b: ast.NewRegex("/a/", ast.Span{}), equal: true},
		{
			name:  "bigints",
			a:     ast.NewBigInt(*big.NewInt(1), ast.Span{}),
			b:     ast.NewBigInt(*big.NewInt(1), ast.Span{}),
			equal: true,
		},
		{
			name: "different bigints",
			a:    ast.NewBigInt(*big.NewInt(1), ast.Span{}),
			b:    ast.NewBigInt(*big.NewInt(2), ast.Span{}),
		},
		{name: "null", a: ast.NewNull(ast.Span{}), b: ast.NewNull(ast.Span{}), equal: true},
		{name: "undefined", a: ast.NewUndefined(ast.Span{}), b: ast.NewUndefined(ast.Span{}), equal: true},
		{name: "null and undefined", a: ast.NewNull(ast.Span{}), b: ast.NewUndefined(ast.Span{})},
		{name: "a number and a string", a: ast.NewNumber(1, ast.Span{}), b: ast.NewString("1", ast.Span{})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.equal, litEqual(test.a, test.b))
		})
	}
}

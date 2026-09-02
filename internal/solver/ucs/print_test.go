package ucs

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// The tests below hand-build IR for the worked examples in
// planning/ucs/implementation_plan.md, then lock the printer's rendering with an
// inline snapshot. They construct each term directly rather than running a desugarer
// or a normalizer over source, so a snapshot pins the printer's output for a shape
// the test states outright.

// In a literal match the catch-all arm is still an ordinary branch of the core.
// Only normalization moves it into the default tail.
func TestPrintCoreLiteralMatch(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(45, 58))
	other := matchCase(wildcardPat(), nil, str("other"), span(85, 100))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one, other}, span(1, 40))
	origin := At(OriginMatchArm, expr)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{
			{
				Pattern: one.Pattern,
				Cont:    &BodyLeaf{Body: one.Body, Arm: one, Origin: At(OriginMatchArm, one)},
				Arm:     one,
				Origin:  At(OriginMatchArm, one),
			},
			{
				Pattern: other.Pattern,
				Cont:    &BodyLeaf{Body: other.Body, Arm: other, Origin: At(OriginMatchArm, other)},
				Arm:     other,
				Origin:  At(OriginMatchArm, other),
			},
		},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split n {
  pat 1 => leaf "one"
  pat _ => leaf "other"
}`))
}

// A nested pattern stays one deep shape in the core. Normalization is what flattens
// it into successive one-tag-level splits.
func TestPrintCoreNestedPattern(t *testing.T) {
	pattern := ast.NewInstancePat(
		ast.NewIdentifier("Line", ast.Span{}),
		ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("start", objPat("x", "y"))}, ast.Span{}),
		ast.Span{},
	)
	body := ast.NewArray([]ast.Expr{ident("x"), ident("y")}, ast.Span{})
	line := matchCase(pattern, nil, body, span(45, 80))
	target := ident("l")
	expr := ast.NewMatch(target, []*ast.MatchCase{line}, span(1, 50))
	origin := At(OriginMatchArm, expr)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{{
			Pattern: line.Pattern,
			Cont:    &BodyLeaf{Body: line.Body, Arm: line, Origin: At(OriginMatchArm, line)},
			Arm:     line,
			Origin:  At(OriginMatchArm, line),
		}},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split l {
  pat Line {start: {x, y}} => leaf [x, y]
}`))
}

// A guard becomes a node inside its branch, after the bindings the pattern
// introduces, so the condition can read `x` and `y`. A core guard names no failure
// continuation. The branches after it already express where a failed guard goes.
func TestPrintCoreGuardedArm(t *testing.T) {
	guardCond := ast.NewBinary(ident("x"), ident("y"), ast.GreaterThan, ast.Span{})
	guarded := matchCase(objPat("x", "y"), guardCond, ident("x"), span(45, 70))
	fallthroughArm := matchCase(wildcardPat(), nil, num(0), span(85, 96))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{guarded, fallthroughArm}, span(1, 50))
	origin := At(OriginMatchArm, expr)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{
			{
				Pattern: guarded.Pattern,
				Cont: &CoreGuard{
					Cond:   guardCond,
					Cont:   &BodyLeaf{Body: guarded.Body, Arm: guarded, Origin: At(OriginMatchArm, guarded)},
					Origin: At(OriginGuard, guardCond),
				},
				Arm:    guarded,
				Origin: At(OriginMatchArm, guarded),
			},
			{
				Pattern: fallthroughArm.Pattern,
				Cont:    &BodyLeaf{Body: fallthroughArm.Body, Arm: fallthroughArm, Origin: At(OriginMatchArm, fallthroughArm)},
				Arm:     fallthroughArm,
				Origin:  At(OriginMatchArm, fallthroughArm),
			},
		},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split p {
  pat {x, y} => guard (x > y) => leaf x
  pat _ => leaf 0
}`))
}

// `if val {x, y} = p { cons } else { alt }` lowers to the same two-branch split a
// two-arm match produces. The `else` is the split's fallthrough rather than a branch.
func TestPrintCoreIfVal(t *testing.T) {
	target := ident("p")
	cons := ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(ident("cons"), ast.Span{})}}
	alt := blockBody(ident("alt"))
	expr := ast.NewIfVal(objPat("x", "y"), target, cons, &alt, span(1, 45))
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

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split p {
  pat {x, y} => leaf { cons }
} else leaf { alt }`))
}

// `val {x, y} = p else { return }` lowers to the same split, but its success path
// carries no body. The bindings escape into the enclosing block, and the `else`
// diverges rather than covering the scrutinee.
func TestPrintCoreValElse(t *testing.T) {
	target := ident("p")
	decl := ast.NewVarDecl(ast.ValKind, objPat("x", "y"), nil, target, false, false, span(1, 35))
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
		Else: &FallbackLeaf{
			Body:   ast.BlockOrExpr{Block: decl.Else},
			Arm:    decl,
			Origin: origin,
		},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split p {
  pat {x, y} => escape
} else fallback { return }`))
}

// In the normalized literal match the catch-all arm has become the default tail.
func TestPrintNormLiteralMatch(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(45, 58))
	other := matchCase(wildcardPat(), nil, str("other"), span(85, 100))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one, other}, span(1, 40))
	origin := At(OriginMatchArm, expr)

	norm := &NormSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*NormBranch{{
			Test:   &LitTest{Lit: ast.NewNumber(1, ast.Span{})},
			Cont:   &BodyLeaf{Body: one.Body, Arm: one, Origin: At(OriginMatchArm, one)},
			Arm:    one,
			Origin: At(OriginMatchArm, one),
		}},
		Default: &BodyLeaf{Body: other.Body, Arm: other, Origin: At(OriginMatchArm, other)},
		Origin:  origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split n {
  1 => leaf "one"
} default leaf "other"`))
}

// Flattening `match l { Line { start: {x, y} } => [x, y] }` gives a split on `l`
// and then a split on the projected `l.start`, so no split sees more than one
// tag-level. Each tail is `✗` because no arm covers the remaining values.
func TestPrintNormNestedPattern(t *testing.T) {
	pattern := ast.NewInstancePat(
		ast.NewIdentifier("Line", ast.Span{}),
		ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("start", objPat("x", "y"))}, ast.Span{}),
		ast.Span{},
	)
	body := ast.NewArray([]ast.Expr{ident("x"), ident("y")}, ast.Span{})
	line := matchCase(pattern, nil, body, span(45, 80))
	target := ident("l")
	expr := ast.NewMatch(target, []*ast.MatchCase{line}, span(1, 50))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, line)

	root := NewRoot(target, origin)
	start := root.Project(FieldStep{Name: "start"}, armOrigin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &ClassTest{Name: ast.NewIdentifier("Line", ast.Span{})},
			Cont: &NormSplit{
				Scrutinee: start,
				Branches: []*NormBranch{{
					Test: &ObjectTest{Keys: keys("x", "y")},
					Cont: &NormBind{
						Name:   "x",
						Source: start.Project(FieldStep{Name: "x"}, armOrigin),
						Cont: &NormBind{
							Name:   "y",
							Source: start.Project(FieldStep{Name: "y"}, armOrigin),
							Cont:   &BodyLeaf{Body: line.Body, Arm: line, Origin: armOrigin},
							Origin: armOrigin,
						},
						Origin: armOrigin,
					},
					Arm:    line,
					Origin: armOrigin,
				}},
				Origin: armOrigin,
			},
			Arm:    line,
			Origin: armOrigin,
		}},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split l {
  Line => split l.start {
    {x, y} => bind x = l.start.x, y = l.start.y; leaf [x, y]
  } default ✗
} default ✗`))
}

// An extractor pattern tests an extractor tag and reaches its arguments as
// positional results, so `Ok(v)` binds `v` from `r.0`.
func TestPrintNormExtractorPattern(t *testing.T) {
	okPat := ast.NewExtractorPat(ast.NewIdentifier("Ok", ast.Span{}), []ast.Pat{identPat("v")}, ast.Span{})
	errPat := ast.NewExtractorPat(ast.NewIdentifier("Err", ast.Span{}), []ast.Pat{wildcardPat()}, ast.Span{})
	okArm := matchCase(okPat, nil, ident("v"), span(45, 56))
	errArm := matchCase(errPat, nil, num(0), span(85, 96))
	target := ident("r")
	expr := ast.NewMatch(target, []*ast.MatchCase{okArm, errArm}, span(1, 40))
	origin := At(OriginMatchArm, expr)
	okOrigin := At(OriginMatchArm, okArm)
	errOrigin := At(OriginMatchArm, errArm)

	root := NewRoot(target, origin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{
			{
				Test: &ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 1},
				Cont: &NormBind{
					Name:   "v",
					Pat:    identPat("v"),
					Source: root.Project(ExtractStep{Index: 0}, okOrigin),
					Cont:   &BodyLeaf{Body: okArm.Body, Arm: okArm, Origin: okOrigin},
					Origin: okOrigin,
				},
				Arm:    okArm,
				Origin: okOrigin,
			},
			{
				Test:   &ExtractorTest{Name: ast.NewIdentifier("Err", ast.Span{}), Arity: 1},
				Cont:   &BodyLeaf{Body: errArm.Body, Arm: errArm, Origin: errOrigin},
				Arm:    errArm,
				Origin: errOrigin,
			},
		},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split r {
  Ok(_) => bind v = r.#0; leaf v
  Err(_) => leaf 0
} default ✗`))
}

// A tuple rest relaxes its split to an inexact prefix and binds the suffix past that
// prefix, so `[first, ...rest]` binds `first` from `xs.0` and `rest` from `xs[1..]`.
func TestPrintNormTupleRest(t *testing.T) {
	pattern := ast.NewTuplePat([]ast.Pat{identPat("first"), ast.NewRestPat(identPat("rest"), ast.Span{})}, ast.Span{})
	armCase := matchCase(pattern, nil, ident("first"), span(45, 70))
	target := ident("xs")
	expr := ast.NewMatch(target, []*ast.MatchCase{armCase}, span(1, 40))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, armCase)

	root := NewRoot(target, origin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &TupleTest{Len: 1, Rest: TrailingRest},
			Cont: &NormBind{
				Name:   "first",
				Source: root.Project(IndexStep{Index: 0}, armOrigin),
				Cont: &NormBind{
					Name:   "rest",
					Source: root.Project(SuffixStep{From: 1}, armOrigin),
					Cont:   &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: armOrigin},
					Origin: armOrigin,
				},
				Origin: armOrigin,
			},
			Arm:    armCase,
			Origin: armOrigin,
		}},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split xs {
  [_, ...] => bind first = xs.0, rest = xs[1..]; leaf first
} default ✗`))
}

// An object rest has no positional slice, so `{x, ...rest}` binds `rest` from the
// scrutinee with the keys named here removed.
func TestPrintNormObjectRest(t *testing.T) {
	pattern := ast.NewObjectPat([]ast.ObjPatElem{shorthandElem("x"), objRestElem("rest")}, ast.Span{})
	armCase := matchCase(pattern, nil, ident("rest"), span(45, 68))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{armCase}, span(1, 40))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, armCase)

	root := NewRoot(target, origin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &ObjectTest{Keys: keys("x"), Rest: TrailingRest},
			Cont: &NormBind{
				Name:   "x",
				Source: root.Project(FieldStep{Name: "x"}, armOrigin),
				Cont: &NormBind{
					Name:   "rest",
					Source: root.Project(RemainderStep{Exclude: set.FromSlice([]string{"x"})}, armOrigin),
					Cont:   &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: armOrigin},
					Origin: armOrigin,
				},
				Origin: armOrigin,
			},
			Arm:    armCase,
			Origin: armOrigin,
		}},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split p {
  {x, ...} => bind x = p.x, rest = p \ {x}; leaf rest
} default ✗`))
}

// A normalized guard names where a failed test continues, which is the fallthrough
// the branch would have taken in source order. Here that is the split's own tail, so
// the later unguarded arm stays reachable.
func TestPrintNormGuardedArm(t *testing.T) {
	guardCond := ast.NewBinary(ident("x"), ident("y"), ast.GreaterThan, ast.Span{})
	guarded := matchCase(objPat("x", "y"), guardCond, ident("x"), span(45, 70))
	fallthroughArm := matchCase(wildcardPat(), nil, num(0), span(85, 96))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{guarded, fallthroughArm}, span(1, 50))
	origin := At(OriginMatchArm, expr)
	guardedOrigin := At(OriginMatchArm, guarded)

	root := NewRoot(target, origin)
	tail := &BodyLeaf{
		Body:   fallthroughArm.Body,
		Arm:    fallthroughArm,
		Origin: At(OriginMatchArm, fallthroughArm),
	}

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &ObjectTest{Keys: keys("x", "y")},
			Cont: &NormBind{
				Name:   "x",
				Source: root.Project(FieldStep{Name: "x"}, guardedOrigin),
				Cont: &NormBind{
					Name:   "y",
					Source: root.Project(FieldStep{Name: "y"}, guardedOrigin),
					Cont: &NormGuard{
						Cond:    guardCond,
						Cont:    &BodyLeaf{Body: guarded.Body, Arm: guarded, Origin: guardedOrigin},
						Default: tail,
						Origin:  At(OriginGuard, guardCond),
					},
					Origin: guardedOrigin,
				},
				Origin: guardedOrigin,
			},
			Arm:    guarded,
			Origin: guardedOrigin,
		}},
		Default: tail,
		Origin:  origin,
	}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split p {
  {x, y} => bind x = p.x, y = p.y; guard (x > y) {
    leaf x
  } default leaf 0
} default leaf 0`))
}

// A split with no branches collapses to `{}`, and a missing tail renders `✗`.
func TestPrintNormEmptySplit(t *testing.T) {
	origin := Invented(OriginMatchArm)
	norm := &NormSplit{Scrutinee: NewRoot(ident("p"), origin), Origin: origin}

	snaps.MatchInlineSnapshot(t, norm.String(), snaps.Inline(`split p {} default ✗`))
}

// ShowOrigins turns on the provenance tag a diagnostic keys its wording off. The
// invented fallthrough is marked synthetic so a message never anchors a span the
// user cannot see.
func TestPrintShowsOriginTags(t *testing.T) {
	target := ident("p")
	cons := ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(ident("cons"), ast.Span{})}}
	expr := ast.NewIfVal(objPat("x", "y"), target, cons, nil, span(1, 32))
	origin := At(OriginIfVal, expr)
	invented := Invented(OriginIfVal)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{{
			Pattern: expr.Pattern,
			Cont:    &BodyLeaf{Body: ast.BlockOrExpr{Block: &cons}, Arm: expr, Origin: origin},
			Arm:     expr,
			Origin:  origin,
		}},
		Else:   &BodyLeaf{Body: exprBody(ast.NewLitExpr(ast.NewUndefined(ast.Span{}))), Origin: invented},
		Origin: origin,
	}

	opts := DefaultPrintOptions()
	opts.ShowOrigins = true
	snaps.MatchInlineSnapshot(t, Print(core, opts), snaps.Inline(`split p [if val] {
  pat {x, y} [if val] => leaf { cons } [if val]
} else leaf undefined [synthetic if val]`))
}

// ShowArms turns on the surface-arm back-reference, rendered as the arm's span. The
// synthetic fallthrough points at no arm, so it renders `arm=none`.
func TestPrintShowsArmBackReferences(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(45, 58))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one}, span(1, 30))
	origin := At(OriginMatchArm, expr)

	norm := &NormSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*NormBranch{{
			Test:   &LitTest{Lit: ast.NewNumber(1, ast.Span{})},
			Cont:   &BodyLeaf{Body: one.Body, Arm: one, Origin: At(OriginMatchArm, one)},
			Arm:    one,
			Origin: At(OriginMatchArm, one),
		}},
		Default: &BodyLeaf{
			Body:   exprBody(ast.NewLitExpr(ast.NewUndefined(ast.Span{}))),
			Origin: Invented(OriginMatchArm),
		},
		Origin: origin,
	}

	opts := DefaultPrintOptions()
	opts.ShowArms = true
	snaps.MatchInlineSnapshot(t, Print(norm, opts), snaps.Inline(`split n {
  1 arm=45-58 => leaf "one" arm=45-58
} default leaf undefined arm=none`))
}

// ShowSpans turns on the span each node itself blames, which is the only way to see
// the span of a node that carries no arm back-reference. The guard here renders the
// condition's span, `x > y` at 2:14-2:19, while its branch renders the whole arm's.
//
// A synthetic node owns no span, so it renders the one its cause chain reaches
// behind `at~`. The invented fallthrough below was minted from the `match`, so it
// renders that expression's span.
func TestPrintShowsSpans(t *testing.T) {
	guardCond := ast.NewBinary(ident("x"), ident("y"), ast.GreaterThan, span(54, 59))
	guarded := matchCase(objPat("x", "y"), guardCond, ident("x"), span(45, 70))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{guarded}, span(1, 40))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, guarded)

	core := &CoreSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*CoreBranch{{
			Pattern: guarded.Pattern,
			Cont: &CoreGuard{
				Cond:   guardCond,
				Cont:   &BodyLeaf{Body: guarded.Body, Arm: guarded, Origin: armOrigin},
				Origin: At(OriginGuard, guardCond),
			},
			Arm:    guarded,
			Origin: armOrigin,
		}},
		Else:   &BodyLeaf{Body: exprBody(num(0)), Origin: InventedFrom(OriginMatchArm, origin)},
		Origin: origin,
	}

	opts := DefaultPrintOptions()
	opts.ShowSpans = true
	snaps.MatchInlineSnapshot(t, Print(core, opts), snaps.Inline(`split p at=1-40 {
  pat {x, y} at=45-70 => guard (x > y) at=54-59 => leaf x at=45-70
} else leaf 0 at~1-40`))
}

// With both options on, an arm back-reference that repeats the node's own span
// collapses to `arm=same`, and one that differs still prints in full. The second
// branch below is what normalization produces when it merges two arms: the branch's
// origin points at the merged split while its back-reference keeps the arm the user
// typed, and a reader needs both spans to see that.
func TestPrintCollapsesAnArmThatRepeatsTheNodeSpan(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(45, 58))
	two := matchCase(numPat(2), nil, str("two"), span(85, 98))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one, two}, span(1, 40))
	origin := At(OriginMatchArm, expr)

	norm := &NormSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*NormBranch{
			{
				Test:   &LitTest{Lit: ast.NewNumber(1, ast.Span{})},
				Cont:   &BodyLeaf{Body: one.Body, Arm: one, Origin: At(OriginMatchArm, one)},
				Arm:    one,
				Origin: At(OriginMatchArm, one),
			},
			{
				Test:   &LitTest{Lit: ast.NewNumber(2, ast.Span{})},
				Cont:   &BodyLeaf{Body: two.Body, Arm: two, Origin: At(OriginMatchArm, two)},
				Arm:    two,
				Origin: origin,
			},
		},
		Origin: origin,
	}

	opts := DefaultPrintOptions()
	opts.ShowSpans = true
	opts.ShowArms = true
	snaps.MatchInlineSnapshot(t, Print(norm, opts), snaps.Inline(`split n at=1-40 {
  1 at=45-58 arm=same => leaf "one" at=45-58 arm=same
  2 at=1-40 arm=85-98 => leaf "two" at=85-98 arm=same
} default ✗`))
}

// A node whose cause chain reaches no surface node at all names no position, so it
// renders `at=none` rather than inventing one.
func TestPrintShowsNoSpanForAnUncausedSyntheticNode(t *testing.T) {
	leaf := &BodyLeaf{Body: exprBody(num(0)), Origin: Invented(OriginIfVal)}

	opts := DefaultPrintOptions()
	opts.ShowSpans = true
	require.Equal(t, "leaf 0 at=none", Print(leaf, opts))
}

// An empty Indent falls back to two spaces, so a zero-value PrintOptions still
// renders nested splits readably.
func TestPrintDefaultsIndentWhenEmpty(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(45, 58))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one}, span(1, 30))
	origin := At(OriginMatchArm, expr)

	norm := &NormSplit{
		Scrutinee: NewRoot(target, origin),
		Branches: []*NormBranch{{
			Test:   &LitTest{Lit: ast.NewNumber(1, ast.Span{})},
			Cont:   &BodyLeaf{Body: one.Body, Arm: one, Origin: At(OriginMatchArm, one)},
			Arm:    one,
			Origin: At(OriginMatchArm, one),
		}},
		Origin: origin,
	}

	// String() renders through DefaultPrintOptions, so it is the reference the
	// substitution has to reproduce. It is the expected value; the empty-Indent
	// call is what this test exercises.
	require.Equal(t, norm.String(), Print(norm, PrintOptions{}))
}

// A typed-nil arm renders `arm=none` instead of panicking, so a lowering bug shows up
// as a printable IR rather than a crash inside the printer.
func TestPrintToleratesATypedNilArm(t *testing.T) {
	var typedNil *ast.MatchCase
	leaf := &BodyLeaf{Body: exprBody(num(1)), Arm: typedNil, Origin: Invented(OriginMatchArm)}

	opts := DefaultPrintOptions()
	opts.ShowArms = true
	require.Equal(t, "leaf 1 arm=none", Print(leaf, opts))
}

// A body holding a block renders on one line, so the surrounding split stays readable.
// The block needs more than one statement to show it, since the separator between two
// statements is the only place the two rendering modes differ.
func TestPrintBlockBodyRendersOnOneLine(t *testing.T) {
	body := ast.NewDo(ast.Block{Stmts: []ast.Stmt{
		ast.NewExprStmt(num(1), ast.Span{}),
		ast.NewReturnStmt(num(2), ast.Span{}),
	}}, ast.Span{})
	armCase := matchCase(wildcardPat(), nil, body, span(45, 60))
	leaf := &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: At(OriginMatchArm, armCase)}

	require.Equal(t, "leaf do { 1; return 2 }", leaf.String())

	// The same node spans four lines under the printer's default options, which is what
	// the IR cannot embed in a line of its own nesting.
	spread, err := printer.Print(body, printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "do {\n    1\n    return 2\n}", spread)
}

// A node the source printer cannot render prints as its kind in angle brackets, so a
// lowering bug shows up as a printable IR rather than a crash or an error return.
func TestPrintUnrenderedExpressionFallsBackToItsKind(t *testing.T) {
	require.Equal(t, "<nil>", exprString(nil))
	require.Equal(t, "<nil>", patString(nil))
	require.Equal(t, "<empty>", bodyString(ast.BlockOrExpr{Block: nil, Expr: nil}))
}

// A core branch can bind the leaves its pattern introduced, the same way a
// normalized branch does. A run of consecutive binds folds onto one `bind` clause so
// a branch that names several fields stays on one line, and the guard after the run
// reads what the run bound.
func TestPrintCoreBindRun(t *testing.T) {
	guardCond := ast.NewBinary(ident("x"), ident("y"), ast.GreaterThan, ast.Span{})
	armCase := matchCase(objPat("x", "y"), guardCond, ident("x"), span(45, 70))
	target := ident("p")
	origin := At(OriginMatchArm, armCase)
	root := NewRoot(target, origin)

	core := &CoreSplit{
		Scrutinee: root,
		Branches: []*CoreBranch{{
			Pattern: armCase.Pattern,
			Cont: &CoreBind{
				Name:   "x",
				Source: root.Project(FieldStep{Name: "x"}, origin),
				Cont: &CoreBind{
					Name:   "y",
					Source: root.Project(FieldStep{Name: "y"}, origin),
					Cont: &CoreGuard{
						Cond:   guardCond,
						Cont:   &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: origin},
						Origin: At(OriginGuard, guardCond),
					},
					Origin: origin,
				},
				Origin: origin,
			},
			Arm:    armCase,
			Origin: origin,
		}},
		Origin: origin,
	}

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split p {
  pat {x, y} => bind x = p.x, y = p.y; guard (x > y) => leaf x
}`))
}

// Every node renders through String(), so a failing require.Equal on any of them
// prints the term rather than a struct address.
func TestStringOnEveryNode(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 8)))
	root := NewRoot(ident("p"), origin)
	leaf := &BodyLeaf{Body: exprBody(num(1)), Arm: nil, Origin: origin}

	tests := []struct {
		name string
		in   fmt.Stringer
		want string
	}{
		{"CoreSplit", &CoreSplit{Scrutinee: root, Origin: origin}, "split p {}"},
		{
			"CoreBranch",
			&CoreBranch{Pattern: wildcardPat(), Cont: leaf, Origin: origin},
			"pat _ => leaf 1",
		},
		{
			"CoreGuard",
			&CoreGuard{Cond: ident("g"), Cont: leaf, Origin: origin},
			"guard (g) => leaf 1",
		},
		{
			"CoreBind",
			&CoreBind{Name: "x", Source: root, Cont: leaf, Origin: origin},
			"bind x = p; leaf 1",
		},
		{"NormSplit", &NormSplit{Scrutinee: root, Origin: origin}, "split p {} default ✗"},
		{
			"NormBranch",
			&NormBranch{Test: &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, Cont: leaf, Origin: origin},
			"1 => leaf 1",
		},
		{
			"NormGuard",
			&NormGuard{Cond: ident("g"), Cont: leaf, Origin: origin},
			"guard (g) {\n  leaf 1\n} default ✗",
		},
		{
			"NormBind",
			&NormBind{Name: "x", Source: root, Cont: leaf, Origin: origin},
			"bind x = p; leaf 1",
		},
		{"BodyLeaf", leaf, "leaf 1"},
		{"EscapeLeaf", &EscapeLeaf{Origin: origin}, "escape"},
		{"FallbackLeaf", &FallbackLeaf{Body: exprBody(num(0)), Origin: origin}, "fallback 0"},
		{"Scrutinee", root, "p"},
		{"ObjectTest", &ObjectTest{Keys: keys("x")}, "{x}"},
		{"TupleTest", &TupleTest{Len: 1}, "[_]"},
		{"LitTest", &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, "1"},
		{"ClassTest", &ClassTest{Name: ast.NewIdentifier("Point", ast.Span{})}, "Point"},
		{
			"ExtractorTest",
			&ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 1},
			"Ok(_)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.in.String())
		})
	}
}

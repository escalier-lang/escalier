package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// The tests below hand-build IR for the worked examples in
// planning/ucs/implementation_plan.md, then lock the printer's rendering with an
// inline snapshot. Nothing here calls a desugarer or a normalizer, because neither
// exists yet. These snapshots are the shapes their output is checked against once
// they do.

func identPat(name string) *ast.IdentPat {
	return ast.NewIdentPat(name, false, nil, nil, ast.Span{})
}

func wildcardPat() *ast.WildcardPat {
	return ast.NewWildcardPat(ast.Span{})
}

func numPat(value float64) *ast.LitPat {
	return ast.NewLitPat(ast.NewNumber(value, ast.Span{}), ast.Span{})
}

func shorthandElem(key string) ast.ObjPatElem {
	return ast.NewObjShorthandPat(ast.NewIdentifier(key, ast.Span{}), false, nil, nil, ast.Span{})
}

func keyValueElem(key string, value ast.Pat) ast.ObjPatElem {
	return ast.NewObjKeyValuePat(ast.NewIdentifier(key, ast.Span{}), value, ast.Span{})
}

func objRestElem(name string) ast.ObjPatElem {
	return ast.NewObjRestPat(identPat(name), ast.Span{})
}

// objPat builds an object pattern of shorthand keys, the `{x, y}` form.
func objPat(keys ...string) *ast.ObjectPat {
	elems := make([]ast.ObjPatElem, len(keys))
	for i, key := range keys {
		elems[i] = shorthandElem(key)
	}
	return ast.NewObjectPat(elems, ast.Span{})
}

// exprBody wraps a bare expression as an arm body.
func exprBody(e ast.Expr) ast.BlockOrExpr {
	return ast.BlockOrExpr{Expr: e}
}

// blockBody wraps a single expression statement as a block arm body, the shape an
// `if val` consequent takes.
func blockBody(e ast.Expr) ast.BlockOrExpr {
	block := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(e, ast.Span{})}}
	return ast.BlockOrExpr{Block: block}
}

// matchCase builds a surface arm for a branch to point back at.
func matchCase(pattern ast.Pat, guard ast.Expr, body ast.Expr, s ast.Span) *ast.MatchCase {
	return ast.NewMatchCase(pattern, guard, exprBody(body), s)
}

// In a literal match the catch-all arm is still an ordinary branch of the core.
// Only normalization moves it into the default tail.
func TestPrintCoreLiteralMatch(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	other := matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one, other}, span(1, 1, 40))
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
	line := matchCase(pattern, nil, body, span(2, 5, 40))
	target := ident("l")
	expr := ast.NewMatch(target, []*ast.MatchCase{line}, span(1, 1, 50))
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
	guarded := matchCase(objPat("x", "y"), guardCond, ident("x"), span(2, 5, 30))
	fallthroughArm := matchCase(wildcardPat(), nil, num(0), span(3, 5, 16))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{guarded, fallthroughArm}, span(1, 1, 50))
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

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split p {
  pat {x, y} => leaf { cons }
} else leaf { alt }`))
}

// `val {x, y} = p else { return }` lowers to the same split, but its success path
// carries no body. The bindings escape into the enclosing block, and the `else`
// diverges rather than covering the scrutinee.
func TestPrintCoreValElse(t *testing.T) {
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
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	other := matchCase(wildcardPat(), nil, str("other"), span(3, 5, 20))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one, other}, span(1, 1, 40))
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
	line := matchCase(pattern, nil, body, span(2, 5, 40))
	target := ident("l")
	expr := ast.NewMatch(target, []*ast.MatchCase{line}, span(1, 1, 50))
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
	okArm := matchCase(okPat, nil, ident("v"), span(2, 5, 16))
	errArm := matchCase(errPat, nil, num(0), span(3, 5, 16))
	target := ident("r")
	expr := ast.NewMatch(target, []*ast.MatchCase{okArm, errArm}, span(1, 1, 40))
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
					Source: root.Project(ResultStep{Index: 0}, okOrigin),
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
	armCase := matchCase(pattern, nil, ident("first"), span(2, 5, 30))
	target := ident("xs")
	expr := ast.NewMatch(target, []*ast.MatchCase{armCase}, span(1, 1, 40))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, armCase)

	root := NewRoot(target, origin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &TupleTest{Len: 1, Exactness: InexactPrefix},
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
	armCase := matchCase(pattern, nil, ident("rest"), span(2, 5, 28))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{armCase}, span(1, 1, 40))
	origin := At(OriginMatchArm, expr)
	armOrigin := At(OriginMatchArm, armCase)

	root := NewRoot(target, origin)

	norm := &NormSplit{
		Scrutinee: root,
		Branches: []*NormBranch{{
			Test: &ObjectTest{Keys: keys("x"), Exactness: InexactPrefix},
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
	guarded := matchCase(objPat("x", "y"), guardCond, ident("x"), span(2, 5, 30))
	fallthroughArm := matchCase(wildcardPat(), nil, num(0), span(3, 5, 16))
	target := ident("p")
	expr := ast.NewMatch(target, []*ast.MatchCase{guarded, fallthroughArm}, span(1, 1, 50))
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
	expr := ast.NewIfVal(objPat("x", "y"), target, cons, nil, span(1, 1, 32))
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
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one}, span(1, 1, 30))
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
  1 arm=2:5-2:18 => leaf "one" arm=2:5-2:18
} default leaf undefined arm=none`))
}

// An empty Indent falls back to two spaces, so a zero-value PrintOptions still
// renders nested splits readably.
func TestPrintDefaultsIndentWhenEmpty(t *testing.T) {
	one := matchCase(numPat(1), nil, str("one"), span(2, 5, 18))
	target := ident("n")
	expr := ast.NewMatch(target, []*ast.MatchCase{one}, span(1, 1, 30))
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

	require.Equal(t, norm.String(), Print(norm, PrintOptions{}))
}

// An operator operand is parenthesized, so a guard's grouping survives into the
// snapshot. Without it `!(x > y)` and `(!x) > y` would both render `!x > y`.
func TestPrintParenthesizesOperatorOperands(t *testing.T) {
	x, y := ident("x"), ident("y")
	notGreater := ast.NewUnary(ast.LogicalNot, ast.NewBinary(x, y, ast.GreaterThan, ast.Span{}), ast.Span{})
	greaterOfNot := ast.NewBinary(ast.NewUnary(ast.LogicalNot, x, ast.Span{}), y, ast.GreaterThan, ast.Span{})

	tests := []struct {
		name string
		in   ast.Expr
		want string
	}{
		{"negated comparison", notGreater, "!(x > y)"},
		{"comparison of a negation", greaterOfNot, "(!x) > y"},
		{
			"right-nested conjunction",
			ast.NewBinary(x, ast.NewBinary(y, ident("z"), ast.LogicalAnd, ast.Span{}), ast.LogicalOr, ast.Span{}),
			"x || (y && z)",
		},
		{
			"left-nested disjunction",
			ast.NewBinary(ast.NewBinary(x, y, ast.LogicalOr, ast.Span{}), ident("z"), ast.LogicalAnd, ast.Span{}),
			"(x || y) && z",
		},
		{
			// A plain operand needs no parentheses, so the common guard stays readable.
			"simple operands",
			ast.NewBinary(x, y, ast.GreaterThan, ast.Span{}),
			"x > y",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, exprString(test.in))
		})
	}
}

// A binding leaf renders its `mut` prefix, its type annotation, and its default. The
// default is what makes a field optional, so dropping it would let `{x = 0}` and
// `{x}` share a snapshot even though they match different values.
func TestPrintPatternLeafExtras(t *testing.T) {
	numberAnn := &ast.NumberTypeAnn{}
	stringAnn := &ast.StringTypeAnn{}

	tests := []struct {
		name string
		in   ast.Pat
		want string
	}{
		{"plain shorthand", objPat("x"), "{x}"},
		{
			"shorthand with a default",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), false, nil, num(0), ast.Span{}),
			}, ast.Span{}),
			"{x = 0}",
		},
		{
			"shorthand with an annotation",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), false, numberAnn, nil, ast.Span{}),
			}, ast.Span{}),
			"{x: number}",
		},
		{
			"mutable shorthand with both",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), true, numberAnn, num(0), ast.Span{}),
			}, ast.Span{}),
			"{mut x: number = 0}",
		},
		{
			"ident leaf with both",
			ast.NewIdentPat("a", false, stringAnn, str("hi"), ast.Span{}),
			`a: string = "hi"`,
		},
		{
			"tuple element with a default",
			ast.NewTuplePat([]ast.Pat{ast.NewIdentPat("a", false, nil, num(1), ast.Span{})}, ast.Span{}),
			"[a = 1]",
		},
		{
			// An annotation this renderer does not spell out still shows its presence.
			"unrendered annotation",
			ast.NewIdentPat("a", false, &ast.KeyOfTypeAnn{}, nil, ast.Span{}),
			"a: <KeyOfTypeAnn>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, patString(test.in))
		})
	}
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

// A form the compact AST renderer does not spell out prints as its node kind, so an
// unrecognized body still leaves the surrounding split readable.
func TestPrintUnrenderedExpressionFallsBackToItsKind(t *testing.T) {
	body := ast.NewDo(ast.Block{}, ast.Span{})
	armCase := matchCase(wildcardPat(), nil, body, span(2, 5, 20))
	leaf := &BodyLeaf{Body: armCase.Body, Arm: armCase, Origin: At(OriginMatchArm, armCase)}

	require.Equal(t, "leaf <DoExpr>", leaf.String())
}

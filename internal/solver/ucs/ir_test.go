package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// span builds a single-line span in source 0, so a test that asserts an arm
// back-reference has a stable, readable rendering.
func span(line, start, end int) ast.Span {
	return ast.NewSpan(
		ast.Location{Line: line, Column: start},
		ast.Location{Line: line, Column: end},
		0,
	)
}

func ident(name string) *ast.IdentExpr {
	return ast.NewIdent(name, ast.Span{})
}

func num(value float64) ast.Expr {
	return ast.NewLitExpr(ast.NewNumber(value, ast.Span{}))
}

func str(value string) ast.Expr {
	return ast.NewLitExpr(ast.NewString(value, ast.Span{}))
}

// arm builds a `match` arm with the given span and a wildcard pattern, standing in
// for the surface node a branch or leaf points back at.
func arm(s ast.Span) *ast.MatchCase {
	body := ast.BlockOrExpr{Expr: ident("body")}
	return ast.NewMatchCase(ast.NewWildcardPat(ast.Span{}), nil, body, s)
}

func TestNewRootIsARootScrutinee(t *testing.T) {
	target := ident("p")
	root := NewRoot(target, At(OriginMatchArm, arm(span(1, 1, 8))))

	require.True(t, root.IsRoot())
	require.Nil(t, root.Parent)
	require.Nil(t, root.Step)
	require.Same(t, target, root.Target)
}

// TestProjectSharesItsParent locks the once-evaluation guarantee. Sibling
// projections hold the same parent pointer, so a consumer evaluates the parent once
// and reads both projections off it.
func TestProjectSharesItsParent(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	root := NewRoot(ident("p"), origin)
	x := root.Project(FieldStep{Name: "x"}, origin)
	y := root.Project(FieldStep{Name: "y"}, origin)

	require.False(t, x.IsRoot())
	require.Same(t, root, x.Parent)
	require.Same(t, root, y.Parent)
	require.Nil(t, x.Target)
	require.Equal(t, FieldStep{Name: "x"}, x.Step)
	require.Equal(t, "p.x", x.String())
	require.Equal(t, "p.y", y.String())
}

func TestAtRecordsTheSurfaceNode(t *testing.T) {
	node := arm(span(3, 5, 20))
	origin := At(OriginIfVal, node)

	require.Equal(t, OriginIfVal, origin.Kind)
	require.Same(t, node, origin.Node)
	require.False(t, origin.Synthetic)
}

func TestInventedRecordsNoSurfaceNode(t *testing.T) {
	origin := Invented(OriginValElse)

	require.Equal(t, OriginValElse, origin.Kind)
	require.Nil(t, origin.Node)
	require.True(t, origin.Synthetic)
}

// TestEveryNodeCarriesProvenance checks that each core and normalized node exposes
// its Origin through Term, which is the accessor diagnostics read.
func TestEveryNodeCarriesProvenance(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(2, 5, 18)))
	scrutinee := NewRoot(ident("p"), origin)

	terms := map[string]Term{
		"CoreSplit":    &CoreSplit{Scrutinee: scrutinee, Origin: origin},
		"CoreBranch":   &CoreBranch{Pattern: ast.NewWildcardPat(ast.Span{}), Origin: origin},
		"CoreGuard":    &CoreGuard{Cond: ident("g"), Origin: origin},
		"CoreBind":     &CoreBind{Name: "x", Source: scrutinee, Origin: origin},
		"NormSplit":    &NormSplit{Scrutinee: scrutinee, Origin: origin},
		"NormBranch":   &NormBranch{Test: &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, Origin: origin},
		"NormGuard":    &NormGuard{Cond: ident("g"), Origin: origin},
		"NormBind":     &NormBind{Name: "x", Source: scrutinee, Origin: origin},
		"BodyLeaf":     &BodyLeaf{Body: ast.BlockOrExpr{Expr: num(1)}, Origin: origin},
		"EscapeLeaf":   &EscapeLeaf{Origin: origin},
		"FallbackLeaf": &FallbackLeaf{Body: ast.BlockOrExpr{Expr: num(0)}, Origin: origin},
		"Scrutinee":    scrutinee,
	}

	for name, term := range terms {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, origin, term.Prov())
		})
	}
}

// TestLeavesBelongToBothForms checks that the three leaf types terminate a core term
// and a normalized term alike, which is why there is one set of leaves rather than a
// parallel pair.
func TestLeavesBelongToBothForms(t *testing.T) {
	leaves := []any{
		&BodyLeaf{Body: ast.BlockOrExpr{Expr: num(1)}},
		&EscapeLeaf{},
		&FallbackLeaf{Body: ast.BlockOrExpr{Expr: num(0)}},
	}

	for _, leaf := range leaves {
		require.Implements(t, (*Core)(nil), leaf)
		require.Implements(t, (*Norm)(nil), leaf)
	}
}

func TestBodySpan(t *testing.T) {
	exprSpan := span(2, 14, 19)
	blockSpan := span(3, 1, 12)

	found, ok := BodySpan(ast.BlockOrExpr{Expr: ast.NewIdent("x", exprSpan)})
	require.True(t, ok)
	require.Equal(t, exprSpan, found)

	found, ok = BodySpan(ast.BlockOrExpr{Block: &ast.Block{Span: blockSpan}})
	require.True(t, ok)
	require.Equal(t, blockSpan, found)

	_, ok = BodySpan(ast.BlockOrExpr{})
	require.False(t, ok)
}

func TestOriginKindString(t *testing.T) {
	tests := []struct {
		kind OriginKind
		want string
	}{
		{OriginMatchArm, "match arm"},
		{OriginIfVal, "if val"},
		{OriginValElse, "val else"},
		{OriginGuard, "guard"},
		{OriginKind(99), "unknown origin"},
	}

	for _, test := range tests {
		require.Equal(t, test.want, test.kind.String())
	}
}

func TestExactnessString(t *testing.T) {
	require.Equal(t, "exact", Exact.String())
	require.Equal(t, "inexact prefix", InexactPrefix.String())
	require.Equal(t, "unknown exactness", Exactness(99).String())
}

func TestScrutineePaths(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	root := func(name string) *Scrutinee { return NewRoot(ident(name), origin) }

	tests := []struct {
		name string
		in   *Scrutinee
		want string
	}{
		{
			"root target",
			root("p"),
			"p",
		},
		{
			// A side-effecting target is one shared node, so no test or bind re-runs it.
			"root call target",
			NewRoot(ast.NewCall(ident("f"), nil, false, ast.Span{}), origin),
			"f()",
		},
		{
			"object field",
			root("p").Project(FieldStep{Name: "x"}, origin),
			"p.x",
		},
		{
			"nested object field",
			root("l").Project(FieldStep{Name: "start"}, origin).Project(FieldStep{Name: "x"}, origin),
			"l.start.x",
		},
		{
			"tuple element",
			root("xs").Project(IndexStep{Index: 0}, origin),
			"xs.0",
		},
		{
			// The `v` of `Ok(v)` is the extractor's first positional result.
			"extractor result",
			root("r").Project(ResultStep{Index: 0}, origin),
			"r.0",
		},
		{
			// The `rest` of `[first, ...rest]` is the suffix past the fixed prefix.
			"tuple suffix",
			root("xs").Project(SuffixStep{From: 1}, origin),
			"xs[1..]",
		},
		{
			// The `rest` of `{x, ...rest}` is the scrutinee minus the keys named here.
			"object remainder",
			root("p").Project(RemainderStep{Exclude: set.FromSlice([]string{"x"})}, origin),
			`p \ {x}`,
		},
		{
			// Excluded keys render in sorted order so the path is deterministic.
			"object remainder with several keys",
			root("p").Project(RemainderStep{Exclude: set.FromSlice([]string{"y", "x"})}, origin),
			`p \ {x, y}`,
		},
		{
			"nil scrutinee",
			nil,
			"<nil>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.in.String())
		})
	}
}

func TestTagTests(t *testing.T) {
	tests := []struct {
		name string
		in   Test
		want string
	}{
		{"empty object", &ObjectTest{}, "{}"},
		{"object shape", &ObjectTest{Keys: []string{"x", "y"}}, "{x, y}"},
		{
			// `{x, ...rest}` tests an object with at least an `x` field.
			"inexact object shape",
			&ObjectTest{Keys: []string{"x"}, Exactness: InexactPrefix},
			"{x, ...}",
		},
		{"empty tuple", &TupleTest{}, "[]"},
		{"tuple shape", &TupleTest{Len: 2}, "[_, _]"},
		{
			// `[first, ...rest]` tests a tuple at least one element long.
			"inexact tuple shape",
			&TupleTest{Len: 1, Exactness: InexactPrefix},
			"[_, ...]",
		},
		{"number literal", &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, "1"},
		{"string literal", &LitTest{Lit: ast.NewString("one", ast.Span{})}, `"one"`},
		{"class tag", &ClassTest{Name: ast.NewIdentifier("Point", ast.Span{})}, "Point"},
		{
			"extractor tag",
			&ExtractorTest{Name: ast.NewIdentifier("Ok", ast.Span{}), Arity: 1},
			"Ok(_)",
		},
		{
			"nullary extractor tag",
			&ExtractorTest{Name: ast.NewIdentifier("None", ast.Span{})},
			"None()",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, testString(test.in))
		})
	}
}

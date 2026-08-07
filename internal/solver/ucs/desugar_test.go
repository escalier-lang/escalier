package ucs

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// The tests below state their input as Escalier source and parse it, rather than
// hand-building an AST the way the printer tests do. A desugarer's job is to read
// the nodes the parser produces, so a snapshot is only worth as much as the surface
// it ran on. Parsing also gives every arm a real span, which is what the
// back-reference assertions read.
//
// A `match` arm's span starts where the token before it ended, so the first arm of
// a multi-line `match` renders `arm=1:10-2:12` — from just past the opening brace to
// the end of the arm. Parser.matchCase reads its start location before skipping the
// whitespace ahead of the pattern. The desugarer copies whatever span the arm
// carries, so the snapshots below show that start.

// parseScript parses an in-memory Escalier script. Test sources hold one conditional
// each, so the finders below can take the first node of a kind and get the one the
// test means.
func parseScript(t *testing.T, src string) *ast.Script {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	script, errs := parser.NewParser(ctx, source).ParseScript()
	require.Empty(t, errs, "expected no parse errors")
	return script
}

// nodeFinder collects the expressions and variable declarations of a script in
// source order, using the shared AST visitor rather than a walk of its own.
type nodeFinder struct {
	ast.DefaultVisitor
	exprs []ast.Expr
	decls []*ast.VarDecl
}

func (f *nodeFinder) EnterExpr(e ast.Expr) bool {
	f.exprs = append(f.exprs, e)
	return true
}

func (f *nodeFinder) EnterDecl(d ast.Decl) bool {
	if found, is := d.(*ast.VarDecl); is {
		f.decls = append(f.decls, found)
	}
	return true
}

// walk parses src and returns the finder that walked it.
func walk(t *testing.T, src string) *nodeFinder {
	t.Helper()
	finder := &nodeFinder{}
	for _, stmt := range parseScript(t, src).Stmts {
		stmt.Accept(finder)
	}
	return finder
}

// findExpr returns the first expression of type T in src.
func findExpr[T ast.Expr](t *testing.T, src string) T {
	t.Helper()
	var zero T
	for _, e := range walk(t, src).exprs {
		if found, is := e.(T); is {
			return found
		}
	}
	require.Fail(t, "the parsed script holds no expression of the wanted kind", "wanted %T", zero)
	return zero
}

// findVarDecl returns the first variable declaration in src.
func findVarDecl(t *testing.T, src string) *ast.VarDecl {
	t.Helper()
	decls := walk(t, src).decls
	require.NotEmpty(t, decls, "no variable declaration in the parsed script")
	return decls[0]
}

// showProvenance renders a term with both its origin tags and its arm
// back-references, the two facts a desugarer snapshot asserts beyond the shape.
func showProvenance(term Term) string {
	opts := DefaultPrintOptions()
	opts.ShowOrigins = true
	opts.ShowArms = true
	return Print(term, opts)
}

// A literal `match` becomes one branch per arm, in source order. The catch-all is an
// ordinary branch and the split writes no `else`, so nothing has moved into a
// default tail yet.
func TestDesugarMatchLiteralArms(t *testing.T) {
	expr := findExpr[*ast.MatchExpr](t, `match n {
	1 => "one",
	_ => "other",
}`)

	core := DesugarMatch(expr)

	require.Nil(t, core.Else)
	snaps.MatchInlineSnapshot(t, showProvenance(core), snaps.Inline(`split n [match arm] {
  pat 1 [match arm] arm=1:10-2:12 => leaf "one" [match arm] arm=1:10-2:12
  pat _ [match arm] arm=2:13-3:14 => leaf "other" [match arm] arm=2:13-3:14
}`))
}

// A guarded arm puts the guard between its branch and its body, so the condition
// runs with `x` and `y` in scope. The guard's origin blames the condition the user
// wrote rather than the whole arm.
func TestDesugarMatchGuardedArm(t *testing.T) {
	expr := findExpr[*ast.MatchExpr](t, `match p {
	{x, y} if x > y => x,
	_ => 0,
}`)

	core := DesugarMatch(expr)

	snaps.MatchInlineSnapshot(t, showProvenance(core), snaps.Inline(`split p [match arm] {
  pat {x, y} [match arm] arm=1:10-2:22 => guard (x > y) [guard] => leaf x [match arm] arm=1:10-2:22
  pat _ [match arm] arm=2:23-3:8 => leaf 0 [match arm] arm=2:23-3:8
}`))
}

// A nested pattern stays one deep shape on its branch. Flattening it into successive
// one-tag-level splits is normalization's job, not the desugarer's.
func TestDesugarMatchKeepsNestedPatternWhole(t *testing.T) {
	expr := findExpr[*ast.MatchExpr](t, `match l {
	Line { start: {x, y} } => [x, y],
}`)

	core := DesugarMatch(expr)

	snaps.MatchInlineSnapshot(t, core.String(), snaps.Inline(`split l {
  pat Line {start: {x, y}} => leaf [x, y]
}`))
}

// A `match` with no arms lowers to a split with no branches. It covers nothing, and
// the coverage check is what reports that later.
func TestDesugarMatchWithNoArms(t *testing.T) {
	expr := findExpr[*ast.MatchExpr](t, `match n {}`)

	core := DesugarMatch(expr)

	require.Empty(t, core.Branches)
	require.Equal(t, "split n {}", core.String())
}

// `if val` lowers to the same two-branch split a two-arm `match` produces. The
// `else` is the split's fallthrough rather than a second branch.
func TestDesugarIfVal(t *testing.T) {
	expr := findExpr[*ast.IfValExpr](t, `if val {x, y} = p { cons } else { alt }`)

	core := DesugarIfVal(expr)

	snaps.MatchInlineSnapshot(t, showProvenance(core), snaps.Inline(`split p [if val] {
  pat {x, y} [if val] arm=1:1-1:40 => leaf { cons } [if val] arm=1:1-1:40
} else leaf { alt } [if val] arm=1:1-1:40`))
}

// An `if val` with no `else` still falls through, evaluating to `undefined` when the
// pattern does not match, so the desugarer invents that leaf. It is marked synthetic
// and points at no arm, since the user wrote neither.
func TestDesugarIfValInventsItsFallthrough(t *testing.T) {
	expr := findExpr[*ast.IfValExpr](t, `if val {x, y} = p { cons }`)

	core := DesugarIfVal(expr)

	snaps.MatchInlineSnapshot(t, showProvenance(core), snaps.Inline(`split p [if val] {
  pat {x, y} [if val] arm=1:1-1:27 => leaf { cons } [if val] arm=1:1-1:27
} else leaf undefined [synthetic if val] arm=none`))

	// The invented leaf has no span of its own, so a diagnostic about it follows the
	// cause chain back to the `if val` and underlines that.
	invented := core.Else.Prov()
	require.True(t, invented.Synthetic)
	_, direct := invented.SourceSpan()
	require.False(t, direct)
	nearest, ok := invented.NearestSpan()
	require.True(t, ok)
	require.Equal(t, expr.Span(), nearest)
}

// `val pat = init else { … }` lowers to the same split, but its success path carries
// no body. The bindings escape into the enclosing block, and the `else` is a fallback
// rather than an arm that covers the scrutinee.
func TestDesugarValElse(t *testing.T) {
	decl := findVarDecl(t, `val {x, y} = p else { fallback }`)

	core, ok := DesugarValElse(decl)

	require.True(t, ok)
	snaps.MatchInlineSnapshot(t, showProvenance(core), snaps.Inline(`split p [val else] {
  pat {x, y} [val else] arm=1:1-1:33 => escape [val else] arm=1:1-1:33
} else fallback { fallback } [val else] arm=1:1-1:33`))
}

// A narrowing annotation on a `val … else` sits on the declaration, not on the
// pattern, so the branch pattern renders without it. A consumer that needs the
// annotation reads it off the arm back-reference, which is the declaration.
func TestDesugarValElseLeavesTheAnnotationOnTheDeclaration(t *testing.T) {
	decl := findVarDecl(t, `val x: number = u else { 0 }`)

	core, ok := DesugarValElse(decl)

	require.True(t, ok)
	require.Equal(t, "split u {\n  pat x => escape\n} else fallback { 0 }", core.String())
	require.Same(t, decl, core.Branches[0].Arm)
	require.NotNil(t, decl.TypeAnn)
}

// Only a declaration that wrote an `else` is a `val … else`. A plain `val` and a
// declaration the parser left without an initializer both lower to nothing, so a
// caller keeps its ordinary declaration path for them.
func TestDesugarValElseRejectsOtherDeclarations(t *testing.T) {
	withoutElse := findVarDecl(t, `val {x, y} = p`)
	withoutInit := findVarDecl(t, `val {x, y} = p else { fallback }`)
	withoutInit.Init = nil

	tests := []struct {
		name string
		decl *ast.VarDecl
	}{
		{"NoElse", withoutElse},
		{"NoInitializer", withoutInit},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, ok := DesugarValElse(test.decl)
			require.False(t, ok)
			require.Nil(t, core)
		})
	}
}

// Lowering points at the surface nodes rather than copying them. Sharing the target
// expression is what keeps a side-effecting scrutinee evaluated once, and sharing the
// pattern and body nodes is what lets a later stage read their annotations and infer
// their types against the source the user wrote.
func TestDesugarPointsAtTheSurfaceNodes(t *testing.T) {
	expr := findExpr[*ast.MatchExpr](t, `match f() {
	{x, y} if x > y => x,
}`)

	core := DesugarMatch(expr)

	require.Same(t, expr.Target, core.Scrutinee.Target)
	require.True(t, core.Scrutinee.IsRoot())

	arm := expr.Cases[0]
	branch := core.Branches[0]
	require.Same(t, arm.Pattern, branch.Pattern)
	require.Same(t, arm, branch.Arm)

	guard, isGuard := branch.Cont.(*CoreGuard)
	require.True(t, isGuard)
	require.Same(t, arm.Guard, guard.Cond)

	leaf, isLeaf := guard.Cont.(*BodyLeaf)
	require.True(t, isLeaf)
	require.Equal(t, arm.Body, leaf.Body)
	require.Same(t, arm, leaf.Arm)
}

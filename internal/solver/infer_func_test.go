package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// AST builders for hand-assembling test inputs live in astbuild.go (a non-test
// file, so they're shared across the package). They stamp builderSpan(), which
// testSpan() also returns, so error-span assertions below stay consistent.

// render coalesces a (possibly variable-carrying) inferred type at Positive
// polarity — the binding view — and prints it. soltype.Print panics on a raw
// TypeVarType, so every function/call result must be coalesced before printing.
func render(t soltype.Type) string {
	return soltype.Print(coalesce(t, soltype.Positive))
}

// --- FuncExpr ---

func TestInferFuncExprAnnotated(t *testing.T) {
	c := newChecker()
	// fn (x: number) { x }
	e := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(exprStmt(identExpr("x"))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> number", render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

// An un-annotated param gets a fresh var. M2 is monomorphic — without
// generalization (M3) the var coalesces to the lattice bounds (unknown in
// contravariant param position, never in covariant return position) rather than
// a <T0> quantifier.
func TestInferFuncExprUnannotatedIsMonomorphic(t *testing.T) {
	c := newChecker()
	// fn (x) { x }
	e := funcExpr([]*ast.Param{param("x", nil)}, nil, block(exprStmt(identExpr("x"))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: unknown) -> never", render(got))
}

func TestInferFuncExprMultiParam(t *testing.T) {
	c := newChecker()
	// fn (x: number, y: string) { y }
	e := funcExpr(
		[]*ast.Param{param("x", numAnn()), param("y", strAnn())},
		nil,
		block(exprStmt(identExpr("y"))),
	)

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number, y: string) -> string", render(got))
}

func TestInferFuncExprReturnAnnotationAccepted(t *testing.T) {
	c := newChecker()
	// fn (x: number) -> number { x }
	e := funcExpr([]*ast.Param{param("x", numAnn())}, numAnn(), block(exprStmt(identExpr("x"))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> number", render(got))
}

func TestInferFuncExprReturnAnnotationMismatch(t *testing.T) {
	c := newChecker()
	// fn () -> number { "hello" }
	e := funcExpr(nil, numAnn(), block(exprStmt(strExpr("hello"))))

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, `cannot constrain "hello" <: number`, c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// A body-level val is visible to later statements and to the tail expression
// that becomes the function's result.
func TestInferFuncExprBodyValDecl(t *testing.T) {
	c := newChecker()
	// fn () { val y = 5; y }
	e := funcExpr(nil, nil, block(
		ast.NewDeclStmt(valDecl("y", nil, numExpr(5)), testSpan()),
		exprStmt(identExpr("y")),
	))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> 5", render(got))
}

// A bodyless (declare/ambient) function adopts its return annotation without
// constraining a synthetic Void against it (which would error spuriously).
func TestInferFuncDeclBodylessReturnAnnotation(t *testing.T) {
	c := newChecker()
	// declare fn now() -> number
	d := ast.NewFuncDecl(
		ast.NewIdentifier("now", testSpan()), nil, nil,
		nil, numAnn(), nil,
		nil, // no body
		false, true, false, testSpan(),
	)

	b := c.inferFuncDecl(NewScope(), 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> number", render(b.Type))
}

// A param that arrives without a pattern must report a clean error, not panic on
// p.Span() (which dereferences the nil pattern). Not reachable from the real
// parser, but the walk must uphold M2's "never a panic" guarantee.
func TestInferFuncExprNilParamPatternNoPanic(t *testing.T) {
	c := newChecker()
	e := funcExpr([]*ast.Param{{Pattern: nil}}, nil, block(exprStmt(numExpr(1))))

	require.NotPanics(t, func() { c.inferExpr(NewScope(), 0, e) })
	require.Len(t, c.errs, 1)
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferFuncExprDestructuringParamUnsupported(t *testing.T) {
	c := newChecker()
	// fn ([a, b]) { ... } — destructuring params are M4.
	tuplePat := ast.NewTuplePat([]ast.Pat{
		ast.NewIdentPat("a", false, nil, nil, testSpan()),
		ast.NewIdentPat("b", false, nil, nil, testSpan()),
	}, testSpan())
	e := funcExpr([]*ast.Param{{Pattern: tuplePat}}, nil, block(exprStmt(numExpr(1))))

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: TuplePat", c.errs[0].Message())
}

// --- CallExpr ---

func TestInferCallResolvesReturn(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { x })(5)
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(exprStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{numExpr(5)}, false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

func TestInferCallArgMismatch(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { x })("hello")
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(exprStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{strExpr("hello")}, false, testSpan())

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, `cannot constrain "hello" <: number`, c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferCallArityMismatch(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { x })(1, 2)
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(exprStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{numExpr(1), numExpr(2)}, false, testSpan())

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "cannot constrain function of arity 1 <: function of arity 2", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferCallThroughBinding(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("inc", ValueBinding{Type: &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "n"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}}},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
	}})
	// inc(7)
	e := ast.NewCall(identExpr("inc"), []ast.Expr{numExpr(7)}, false, testSpan())

	got := c.inferExpr(scope, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", render(got))
}

// --- Block / statements ---

func TestInferBlockResultIsLastStmt(t *testing.T) {
	c := newChecker()
	// { 1; "two" }
	b := block(exprStmt(numExpr(1)), exprStmt(strExpr("two")))

	got := c.inferBlock(NewScope(), 0, b)
	require.Empty(t, c.errs)
	require.Equal(t, `"two"`, render(got))
}

func TestInferBlockEmptyIsVoid(t *testing.T) {
	c := newChecker()
	got := c.inferBlock(NewScope(), 0, block())
	require.Empty(t, c.errs)
	require.Equal(t, "void", render(got))
}

func TestInferBlockReturnStmt(t *testing.T) {
	c := newChecker()
	// { return 5 }
	got := c.inferBlock(NewScope(), 0, block(returnStmt(numExpr(5))))
	require.Empty(t, c.errs)
	require.Equal(t, "5", render(got))
}

// Each val introduces a fresh, independent binding; a later val of the same name
// rebinds it (overwrite, no constraint linking the two), so the tail sees the
// later type even though it is unrelated to the earlier one (§3.2).
func TestInferBlockRedeclarationRebinds(t *testing.T) {
	c := newChecker()
	// { val x = "hello"; val x = 5; x }
	b := block(
		ast.NewDeclStmt(valDecl("x", nil, strExpr("hello")), testSpan()),
		ast.NewDeclStmt(valDecl("x", nil, numExpr(5)), testSpan()),
		exprStmt(identExpr("x")),
	)
	got := c.inferBlock(NewScope(), 0, b)
	require.Empty(t, c.errs)
	require.Equal(t, "5", render(got))
}

func TestInferStmtBodyDeclNotAllowed(t *testing.T) {
	c := newChecker()
	// A nested FuncDecl as a body statement is a permanent language error.
	inner := ast.NewFuncDecl(ast.NewIdentifier("f", testSpan()), nil, nil, nil, nil, nil,
		block(), false, false, false, testSpan())
	s := ast.NewDeclStmt(inner, testSpan())

	got := c.inferStmt(NewScope(), 0, s)
	require.IsType(t, &soltype.Void{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Declaration not allowed in function body: FuncDecl", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// --- FuncDecl ---

func TestInferFuncDecl(t *testing.T) {
	c := newChecker()
	// fn id(x: number) { x }
	d := ast.NewFuncDecl(
		ast.NewIdentifier("id", testSpan()), nil, nil,
		[]*ast.Param{param("x", numAnn())}, nil, nil,
		block(exprStmt(identExpr("x"))),
		false, false, false, testSpan(),
	)

	b := c.inferFuncDecl(NewScope(), 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> number", render(b.Type))
}

// A recursive reference resolves when the name is pre-bound (the SCC wiring that
// arranges this for real top-level groups is PR-5; here we bind the name by hand
// to exercise the body walk seeing itself).
func TestInferFuncDeclSelfReference(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	self := c.freshAt(1)
	scope.defineValue("loop", ValueBinding{Type: self})
	// fn loop(x: number) { loop }
	d := ast.NewFuncDecl(
		ast.NewIdentifier("loop", testSpan()), nil, nil,
		[]*ast.Param{param("x", numAnn())}, nil, nil,
		block(exprStmt(identExpr("loop"))),
		false, false, false, testSpan(),
	)

	b := c.inferFuncDecl(scope, 0, d)
	require.Empty(t, c.errs)
	require.IsType(t, &soltype.FuncType{}, b.Type)
}

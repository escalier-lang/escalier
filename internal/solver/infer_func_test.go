package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// render coalesces a (possibly variable-carrying) inferred type at Positive
// polarity — the binding view — and prints it via soltype.Print, giving the
// stable, var-free form a binding would render as. soltype.Print also handles a
// raw TypeVarType safely (rendering it as t{ID}), so coalescing here is for the
// consistent binding-view rendering, not to avoid a panic.
func render(t soltype.Type) string {
	return soltype.Print(coalesce(t, soltype.Positive))
}

// renderBinding renders a value binding's (sole, in PR1) scheme to its Escalier
// type string — the test-side view of what a name resolves to, including any
// <T0, …> quantifier prefix generalization left behind.
func renderBinding(b ValueBinding) string {
	return renderScheme(b.Schemes[0])
}

// --- FuncExpr ---

func TestInferFuncExprAnnotated(t *testing.T) {
	c := newChecker()
	// fn (x: number) { return x }
	e := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(returnStmt(identExpr("x"))))

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
	// fn (x) { return x }
	e := funcExpr([]*ast.Param{param("x", nil)}, nil, block(returnStmt(identExpr("x"))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: unknown) -> never", render(got))
}

func TestInferFuncExprMultiParam(t *testing.T) {
	c := newChecker()
	// fn (x: number, y: string) { return y }
	e := funcExpr(
		[]*ast.Param{param("x", numAnn()), param("y", strAnn())},
		nil,
		block(returnStmt(identExpr("y"))),
	)

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number, y: string) -> string", render(got))
}

func TestInferFuncExprReturnAnnotationAccepted(t *testing.T) {
	c := newChecker()
	// fn (x: number) -> number { return x }
	e := funcExpr([]*ast.Param{param("x", numAnn())}, numAnn(), block(returnStmt(identExpr("x"))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> number", render(got))
}

func TestInferFuncExprReturnAnnotationMismatch(t *testing.T) {
	c := newChecker()
	// fn () -> number { return "hello" }
	e := funcExpr(nil, numAnn(), block(returnStmt(strExpr("hello"))))

	c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, `cannot constrain "hello" <: number`, c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// A body-level val is visible to later statements, including the return that
// becomes the function's result.
func TestInferFuncExprBodyValDecl(t *testing.T) {
	c := newChecker()
	// fn () { val y = 5; return y }
	e := funcExpr(nil, nil, block(
		ast.NewDeclStmt(valDecl("y", nil, numExpr(5)), testSpan()),
		returnStmt(identExpr("y")),
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

	ty, _ := c.inferFuncDecl(NewScope(), 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> number", render(ty))
}

// A bodyless function with an UNSUPPORTED return annotation recovers to `unknown`
// (the honest "couldn't resolve the declared return"), not the synthetic `void`
// (which would falsely signal "returns nothing" to callers). The annotation error
// is still reported once. A `declare fn` is a no-body site, so inferFuncDecl types it
// body-free and this recovery path runs; the decl here is built with a nil body to
// exercise it directly.
func TestInferFuncDeclBodylessUnsupportedReturnRecoversToUnknown(t *testing.T) {
	c := newChecker()
	// declare fn now() -> bigint   (bigint is unsupported in M2)
	d := ast.NewFuncDecl(
		ast.NewIdentifier("now", testSpan()), nil, nil,
		nil, ast.NewBigintTypeAnn(testSpan()), nil,
		nil, // no body
		false, true, false, testSpan(),
	)

	ty, _ := c.inferFuncDecl(NewScope(), 0, d)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: BigintTypeAnn", c.errs[0].Message())
	require.Equal(t, "fn () -> unknown", render(ty))
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

// A destructuring parameter binds its leaves and renders its pattern (M4 E1). The
// leaves below go unused, so each element coalesces to `unknown`. The point is that
// the param is accepted and rendered, not reported as unsupported.
func TestInferFuncExprDestructuringParam(t *testing.T) {
	c := newChecker()
	// fn ([a, b]) { 1 }
	tuplePat := ast.NewTuplePat([]ast.Pat{
		ast.NewIdentPat("a", false, nil, nil, testSpan()),
		ast.NewIdentPat("b", false, nil, nil, testSpan()),
	}, testSpan())
	e := funcExpr([]*ast.Param{{Pattern: tuplePat}}, nil, block(exprStmt(numExpr(1))))

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "fn ([a, b]: [unknown, unknown]) -> void", render(got))
}

// A generic function `fn <T>(x: T) -> T` resolves its type-parameter list into the
// function's own FuncType.TypeParams, so the parameter and return read the one shared
// `T` var rather than reporting the parameter feature as unsupported.
func TestInferFuncExprGenericResolves(t *testing.T) {
	c := newChecker()
	// fn <T>(x: T) -> T { return x }
	tp := ast.NewTypeParam("T", nil, nil)
	tRef := func() ast.TypeAnn { return ast.NewRefTypeAnn(ast.NewIdentifier("T", testSpan()), nil, testSpan()) }
	e := ast.NewFuncExpr(nil, []*ast.TypeParam{&tp}, []*ast.Param{param("x", tRef())}, tRef(),
		nil, false, block(returnStmt(identExpr("x"))), testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	ft, ok := got.(*soltype.FuncType)
	require.True(t, ok)
	require.Len(t, ft.TypeParams, 1)
	// The parameter and return read the same var the type-param list minted, so the
	// param type, the return type, and the TypeParams binder are one identity.
	require.Same(t, ft.TypeParams[0].Var, ft.Params[0].Type)
	require.Same(t, ft.TypeParams[0].Var, ft.Ret)
}

// --- CallExpr ---

func TestInferCallResolvesReturn(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { return x })(5)
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(returnStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{numExpr(5)}, false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", render(got))
	require.Same(t, got, c.info.TypeOf(e))
}

func TestInferCallArgMismatch(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { return x })("hello")
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(returnStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{strExpr("hello")}, false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, `cannot constrain "hello" <: number`, c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
	// The result is still the callee's return type despite the bad argument.
	require.Equal(t, "number", render(got))
}

// Too-many args at a direct call is the PR4 extra-arg lint (TooManyArgsError, the
// uniform too-many message), not a FuncArityMismatch — and the constraint receives
// only the arity-matched prefix, so the lint is the SOLE diagnostic.
func TestInferCallTooManyArgs(t *testing.T) {
	c := newChecker()
	// (fn (x: number) { return x })(1, 2)
	callee := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(returnStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{numExpr(1), numExpr(2)}, false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Too many arguments: expected at most 1, but got 2", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
	// Error recovery: the result is still the callee's return type, not `never`.
	require.Equal(t, "number", render(got))
}

// Too-few args at a direct call is the PR4 too-few lint (NotEnoughArgsError, the
// symmetric twin of TooManyArgsError) — the demand is padded to the callee's arity
// so the lint is the SOLE diagnostic, not a doubled lint + FuncArityMismatch.
func TestInferCallTooFewArgs(t *testing.T) {
	c := newChecker()
	// (fn (x: number, y: number) { return x })(1)
	callee := funcExpr([]*ast.Param{param("x", numAnn()), param("y", numAnn())}, nil, block(returnStmt(identExpr("x"))))
	e := ast.NewCall(callee, []ast.Expr{numExpr(1)}, false, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Not enough arguments: expected at least 2, but got 1", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
	// Error recovery: the result is still the callee's return type, not `never`.
	require.Equal(t, "number", render(got))
}

func TestInferCallThroughBinding(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("inc", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "n"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}}},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
	})}})
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

	got, diverges := c.inferBlock(NewScope(), 0, b)
	require.Empty(t, c.errs)
	require.False(t, diverges)
	require.Equal(t, `"two"`, render(got))
}

func TestInferBlockEmptyIsVoid(t *testing.T) {
	c := newChecker()
	got, diverges := c.inferBlock(NewScope(), 0, block())
	require.Empty(t, c.errs)
	require.False(t, diverges)
	require.Equal(t, "void", render(got))
}

func TestInferBlockReturnStmt(t *testing.T) {
	c := newChecker()
	// A return is only legal inside a function body, so push a func context the way
	// inferFunc does — otherwise the walk (correctly) reports ReturnOutsideFunction.
	saved := c.pushFuncCtx(false, nil)
	// { return 5 }
	got, diverges := c.inferBlock(NewScope(), 0, block(returnStmt(numExpr(5))))
	c.popFuncCtx(saved)
	require.Empty(t, c.errs)
	// The block's tail VALUE is still 5 (a value-position block keeps it), but the
	// block DIVERGES — a value-position consumer (an if/else branch) drops it from
	// its branch union. inferFunc itself ignores the tail; the return reaches the
	// function's type as a collected return point.
	require.True(t, diverges)
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
	got, diverges := c.inferBlock(NewScope(), 0, b)
	require.Empty(t, c.errs)
	require.False(t, diverges)
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
	// fn id(x: number) { return x }
	d := ast.NewFuncDecl(
		ast.NewIdentifier("id", testSpan()), nil, nil,
		[]*ast.Param{param("x", numAnn())}, nil, nil,
		block(returnStmt(identExpr("x"))),
		false, false, false, testSpan(),
	)

	ty, _ := c.inferFuncDecl(NewScope(), 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> number", render(ty))
}

// A truly recursive function: foo's body calls itself. PR-3 has no SCC driver
// (that's PR-5), so foo is pre-bound to a fresh var the way inferComponent will,
// letting the body reference itself through inferCall. With unconditional
// recursion the function never returns — a base case would need a conditional,
// which arrives in a later milestone — so its return type coalesces to `never`.
// (`foo(x + 1)` would be the textbook shape, but `+` is a BinaryExpr, which
// PR-3 doesn't type yet; `foo(x)` exercises the same recursive-call path.)
func TestInferFuncDeclSelfReference(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("foo", ValueBinding{Schemes: []TypeScheme{monoScheme(c.freshAt(1))}})
	// fn foo(x: number) { return foo(x) }
	d := ast.NewFuncDecl(
		ast.NewIdentifier("foo", testSpan()), nil, nil,
		[]*ast.Param{param("x", numAnn())}, nil, nil,
		block(returnStmt(ast.NewCall(identExpr("foo"), []ast.Expr{identExpr("x")}, false, testSpan()))),
		false, false, false, testSpan(),
	)

	ty, _ := c.inferFuncDecl(scope, 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn (x: number) -> never", render(ty))
}

// A FuncExpr may be assigned to a body-level `val` inside a FuncDecl (the way a
// function value is introduced in a body, since body decls are VarDecl-only).
// The bound name resolves to the function type, and the enclosing function
// returns it.
func TestInferFuncDeclBodyFuncValDecl(t *testing.T) {
	c := newChecker()
	// fn outer() { val f = fn (x: number) { return x }; return f }
	inner := funcExpr([]*ast.Param{param("x", numAnn())}, nil, block(returnStmt(identExpr("x"))))
	d := ast.NewFuncDecl(
		ast.NewIdentifier("outer", testSpan()), nil, nil,
		nil, nil, nil,
		block(
			ast.NewDeclStmt(valDecl("f", nil, inner), testSpan()),
			returnStmt(identExpr("f")),
		),
		false, false, false, testSpan(),
	)

	ty, _ := c.inferFuncDecl(NewScope(), 0, d)
	require.Empty(t, c.errs)
	require.Equal(t, "fn () -> fn (x: number) -> number", render(ty))
}

package solver

import "github.com/escalier-lang/escalier/internal/ast"

// AST builders — convenience constructors for hand-assembling the small ast
// nodes the solver walk operates on, without going through the parser. They live
// in a non-test file (rather than alongside the tests) so both the package's
// test files and non-test code can share them.
//
// Every builder stamps builderSpan() on the nodes it creates: a hand-built node
// has no real source position, so a single shared placeholder span keeps
// constructed nodes comparable and lets error-span assertions check against
// builderSpan(). Code that needs real positions should construct nodes directly
// via the ast.New* constructors with a genuine span.

// builderSpan is the fixed placeholder span stamped on every builder-made node.
func builderSpan() ast.Span {
	return ast.NewSpan(ast.Location{Line: 1, Column: 1}, ast.Location{Line: 1, Column: 2}, 0)
}

func numExpr(v float64) *ast.LiteralExpr { return ast.NewLitExpr(ast.NewNumber(v, builderSpan())) }
func strExpr(s string) *ast.LiteralExpr  { return ast.NewLitExpr(ast.NewString(s, builderSpan())) }
func identExpr(name string) *ast.IdentExpr {
	return ast.NewIdent(name, builderSpan())
}

func numAnn() ast.TypeAnn { return ast.NewNumberTypeAnn(builderSpan()) }
func strAnn() ast.TypeAnn { return ast.NewStringTypeAnn(builderSpan()) }

func param(name string, ann ast.TypeAnn) *ast.Param {
	return &ast.Param{Pattern: ast.NewIdentPat(name, false, nil, nil, builderSpan()), TypeAnn: ann}
}

func block(stmts ...ast.Stmt) *ast.Block {
	return &ast.Block{Stmts: stmts, Span: builderSpan()}
}

func exprStmt(e ast.Expr) ast.Stmt   { return ast.NewExprStmt(e, builderSpan()) }
func returnStmt(e ast.Expr) ast.Stmt { return ast.NewReturnStmt(e, builderSpan()) }

func valDecl(name string, ann ast.TypeAnn, init ast.Expr) *ast.VarDecl {
	return ast.NewVarDecl(ast.ValKind, ast.NewIdentPat(name, false, nil, nil, builderSpan()),
		ann, init, false, false, builderSpan())
}

func funcExpr(params []*ast.Param, ret ast.TypeAnn, body *ast.Block) *ast.FuncExpr {
	return ast.NewFuncExpr(nil, nil, params, ret, nil, false, body, builderSpan())
}

func tupleExpr(elems ...ast.Expr) *ast.TupleExpr { return ast.NewArray(elems, builderSpan()) }

// prop builds a `name: value` object property with an identifier key.
func prop(name string, value ast.Expr) *ast.PropertyExpr {
	return ast.NewProperty(ast.NewIdent(name, builderSpan()), false, false, value, builderSpan())
}

func objExpr(elems ...ast.ObjExprElem) *ast.ObjectExpr { return ast.NewObject(elems, builderSpan()) }

// memberExpr builds a non-optional `obj.prop` field read.
func memberExpr(obj ast.Expr, name string) *ast.MemberExpr {
	return ast.NewMember(obj, ast.NewIdentifier(name, builderSpan()), false, builderSpan())
}

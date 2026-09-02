package ucs

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// span builds a span in source 0, so a test that asserts an arm
// back-reference has a stable, readable rendering.
func span(start, end int) ast.Span {
	return ast.NewSpan(
		ast.Location{Offset: start},
		ast.Location{Offset: end},
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

func shorthandElem(key string) ast.ObjPatElem {
	return ast.NewObjShorthandPat(ast.NewIdentifier(key, ast.Span{}), false, nil, nil, ast.Span{})
}

// objPat builds an object pattern of shorthand keys, the `{x, y}` form.
func objPat(keys ...string) *ast.ObjectPat {
	elems := make([]ast.ObjPatElem, len(keys))
	for i, key := range keys {
		elems[i] = shorthandElem(key)
	}
	return ast.NewObjectPat(elems, ast.Span{})
}

// fieldPat builds `{key: value}`, an object pattern of one named field whose value is a
// sub-pattern.
func fieldPat(key string, value ast.Pat) *ast.ObjectPat {
	return ast.NewObjectPat([]ast.ObjPatElem{keyValueElem(key, value)}, ast.Span{})
}

// tuplePat builds a tuple pattern of the given elements, the `[a, b]` form.
func tuplePat(elems ...ast.Pat) *ast.TuplePat {
	return ast.NewTuplePat(elems, ast.Span{})
}

// instancePat builds `Name { … }`, the pattern that tests a nominal class tag.
func instancePat(name string, object *ast.ObjectPat) *ast.InstancePat {
	return ast.NewInstancePat(ast.NewIdentifier(name, ast.Span{}), object, ast.Span{})
}

func identPat(name string) *ast.IdentPat {
	return ast.NewIdentPat(name, false, nil, nil, ast.Span{})
}

func wildcardPat() *ast.WildcardPat {
	return ast.NewWildcardPat(ast.Span{})
}

func numPat(value float64) *ast.LitPat {
	return ast.NewLitPat(ast.NewNumber(value, ast.Span{}), ast.Span{})
}

func keyValueElem(key string, value ast.Pat) ast.ObjPatElem {
	return ast.NewObjKeyValuePat(ast.NewIdentifier(key, ast.Span{}), value, ast.Span{})
}

func objRestElem(name string) ast.ObjPatElem {
	return ast.NewObjRestPat(identPat(name), ast.Span{})
}

// blockBody wraps a single expression statement as a block arm body, the shape an
// `if val` consequent takes.
func blockBody(e ast.Expr) ast.BlockOrExpr {
	block := &ast.Block{Stmts: []ast.Stmt{ast.NewExprStmt(e, ast.Span{})}}
	return ast.BlockOrExpr{Block: block}
}

// exprBody wraps a bare expression as an arm body.
func exprBody(e ast.Expr) ast.BlockOrExpr {
	return ast.BlockOrExpr{Expr: e}
}

// keys builds the required-field list of an object test.
func keys(names ...string) []ObjectKey {
	out := make([]ObjectKey, len(names))
	for i, name := range names {
		out[i] = ObjectKey{Name: name}
	}
	return out
}

// matchCase builds a surface arm for a branch to point back at.
func matchCase(pattern ast.Pat, guard ast.Expr, body ast.Expr, s ast.Span) *ast.MatchCase {
	return ast.NewMatchCase(pattern, guard, exprBody(body), s)
}

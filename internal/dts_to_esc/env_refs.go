package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// env_refs.go collects the declarations one declaration or member names, which
// is what the environment check resolves against EnvIndex.
//
// typeRefCollector in references.go answers a different question and cannot be
// reused. It records both ends of a qualified reference, because the import
// header needs the head to decide which package to import. Here the head is the
// package qualifier and names no declaration at this site, so recording it
// turns `crypto.Crypto` into a reference to the global `crypto` as well.

// declRefCollector records the declaration each type reference names, with the
// type parameters in scope at that point excluded.
type declRefCollector struct {
	ast.DefaultVisitor
	names set.Set[string]
	// bound counts how many enclosing binders hold each type-parameter name, so
	// a name leaves scope when its binder does rather than on the first exit.
	bound map[string]int
}

func (c *declRefCollector) push(params []*ast.TypeParam) {
	for _, p := range params {
		c.bound[p.Name]++
	}
}

func (c *declRefCollector) pop(params []*ast.TypeParam) {
	for _, p := range params {
		if c.bound[p.Name]--; c.bound[p.Name] <= 0 {
			delete(c.bound, p.Name)
		}
	}
}

func (c *declRefCollector) EnterDecl(d ast.Decl) bool {
	c.push(typeParamsOfDecl(d))
	return true
}

func (c *declRefCollector) ExitDecl(d ast.Decl) { c.pop(typeParamsOfDecl(d)) }

// EnterExpr brings a function expression's own type parameters into scope. A
// class member holds its signature as a `FuncExpr`, which nothing else binds.
func (c *declRefCollector) EnterExpr(e ast.Expr) bool {
	if fn, ok := e.(*ast.FuncExpr); ok {
		c.push(fn.TypeParams)
	}
	return true
}

func (c *declRefCollector) ExitExpr(e ast.Expr) {
	if fn, ok := e.(*ast.FuncExpr); ok {
		c.pop(fn.TypeParams)
	}
}

func (c *declRefCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	if fn, ok := t.(*ast.FuncTypeAnn); ok {
		c.push(fn.TypeParams)
	}
	switch n := t.(type) {
	case *ast.TypeOfTypeAnn:
		// `typeof X` names a value a package declares, reached the way a type is.
		c.add(n.Value)
	case *ast.TypeRefTypeAnn:
		c.add(n.Name)
	}
	return true
}

func (c *declRefCollector) ExitTypeAnn(t ast.TypeAnn) {
	if fn, ok := t.(*ast.FuncTypeAnn); ok {
		c.pop(fn.TypeParams)
	}
}

// add records the declaration a qualified identifier names, which is its last
// segment. `crypto.Crypto` names `Crypto`, and a bare `Event` names itself.
func (c *declRefCollector) add(q ast.QualIdent) {
	name, ok := lastIdent(q)
	if !ok {
		return
	}
	if _, shadowed := c.bound[name]; shadowed {
		return
	}
	c.names.Add(name)
}

// lastIdent returns the rightmost segment of a qualified identifier.
func lastIdent(q ast.QualIdent) (string, bool) {
	switch n := q.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.Member:
		if n.Right == nil {
			return "", false
		}
		return n.Right.Name, true
	default:
		return "", false
	}
}

// declRefNames returns the declarations decl names, its class members included.
func declRefNames(decl ast.Decl) set.Set[string] {
	c := &declRefCollector{names: set.NewSet[string](), bound: map[string]int{}}
	decl.Accept(c)
	return c.names
}

// classElemRefNames returns the declarations one member's signature names.
//
// The class's own type parameters are pushed first, since the walk starts at
// the member rather than at the class and would otherwise read `E` in
// `class Box<E> { v: E }` as a reference to a declaration called `E`.
func classElemRefNames(cls *ast.ClassDecl, elem ast.ClassElem) set.Set[string] {
	c := &declRefCollector{names: set.NewSet[string](), bound: map[string]int{}}
	c.push(cls.TypeParams)
	elem.Accept(c)
	return c.names
}

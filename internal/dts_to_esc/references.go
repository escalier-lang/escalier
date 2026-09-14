package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// references.go collects the type names a module refers to, which is what
// says which packages it has to import and whether any of those sits above
// it in the tier order.

// typeRefCollector gathers the head name of every type reference a walk
// reaches. It is the AST visitor rather than a hand-rolled traversal, so a
// reference in a slot added later is picked up without a change here.
type typeRefCollector struct {
	ast.DefaultVisitor
	names set.Set[string]
	// bound counts the type parameters in scope by name. A reference to one names
	// the binder rather than a declaration, so it forces no import. `std:math`
	// declares a top-level `E`, which `Promise<T, E>` would otherwise pull in.
	bound map[string]int
}

func (c *typeRefCollector) push(params []*ast.TypeParam) {
	for _, tp := range params {
		c.bound[tp.Name]++
	}
}

func (c *typeRefCollector) pop(params []*ast.TypeParam) {
	for _, tp := range params {
		c.bound[tp.Name]--
		if c.bound[tp.Name] <= 0 {
			delete(c.bound, tp.Name)
		}
	}
}

// typeParamsOfDecl returns the type parameters a declaration binds over its own
// body.
func typeParamsOfDecl(d ast.Decl) []*ast.TypeParam {
	switch d := d.(type) {
	case *ast.ClassDecl:
		return d.TypeParams
	case *ast.TypeDecl:
		return d.TypeParams
	case *ast.InterfaceDecl:
		return d.TypeParams
	case *ast.EnumDecl:
		return d.TypeParams
	case *ast.FuncDecl:
		return d.TypeParams
	}
	return nil
}

func (c *typeRefCollector) ExitDecl(d ast.Decl) {
	c.pop(typeParamsOfDecl(d))
}

func (c *typeRefCollector) ExitTypeAnn(t ast.TypeAnn) {
	if fn, ok := t.(*ast.FuncTypeAnn); ok {
		c.pop(fn.TypeParams)
	}
}

// EnterDecl brings the declaration's own type parameters into scope for the
// walk over its body, and returns true so that walk runs.
func (c *typeRefCollector) EnterDecl(d ast.Decl) bool {
	c.push(typeParamsOfDecl(d))
	return true
}

// EnterExpr brings a function expression's own type parameters into scope. A
// class member holds its signature as a `FuncExpr`, which nothing else binds.
func (c *typeRefCollector) EnterExpr(e ast.Expr) bool {
	if fn, ok := e.(*ast.FuncExpr); ok {
		c.push(fn.TypeParams)
	}
	return true
}

func (c *typeRefCollector) ExitExpr(e ast.Expr) {
	if fn, ok := e.(*ast.FuncExpr); ok {
		c.pop(fn.TypeParams)
	}
}

// EnterTypeAnn records a `TypeRefTypeAnn`'s name and keeps walking, so the
// arguments of `Foo<Bar>` are collected beside `Foo` itself.
//
// A qualified reference contributes both ends. The head is what an import
// normally brings into scope, and the last segment matters where namespace
// flattening left the head naming nothing: `namespace Intl { type
// LocalesArgument }` becomes a top-level `LocalesArgument` while references
// still read `Intl.LocalesArgument`.
func (c *typeRefCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	c.enterFuncTypeParams(t)
	// `typeof X` names a value a package declares, reached the way a type is.
	if typeOf, ok := t.(*ast.TypeOfTypeAnn); ok {
		if name, ok := headIdent(typeOf.Value); ok {
			if _, shadowed := c.bound[name]; !shadowed {
				c.names.Add(name)
			}
		}
		if member, ok := typeOf.Value.(*ast.Member); ok {
			c.names.Add(member.Right.Name)
		}
		return true
	}
	ref, ok := t.(*ast.TypeRefTypeAnn)
	if !ok {
		return true
	}
	if name, ok := headIdent(ref.Name); ok {
		if _, shadowed := c.bound[name]; !shadowed {
			c.names.Add(name)
		}
	}
	if member, ok := ref.Name.(*ast.Member); ok {
		c.names.Add(member.Right.Name)
	}
	return true
}

// enterFuncTypeParams binds a function type's own parameters over its body.
func (c *typeRefCollector) enterFuncTypeParams(t ast.TypeAnn) {
	if fn, ok := t.(*ast.FuncTypeAnn); ok {
		c.push(fn.TypeParams)
	}
}

// headIdent returns the leftmost segment of a qualified identifier.
func headIdent(q ast.QualIdent) (string, bool) {
	for {
		switch n := q.(type) {
		case *ast.Ident:
			return n.Name, true
		case *ast.Member:
			q = n.Left
		default:
			return "", false
		}
	}
}

// TypeRefNames returns every type name the module's declarations refer to.
// A name the module declares itself is included; the caller resolves each
// name against what every package declares and drops the local ones.
func TypeRefNames(module *ast.Module) set.Set[string] {
	c := &typeRefCollector{names: set.NewSet[string](), bound: map[string]int{}}
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			decl.Accept(c)
		}
		return true
	})
	return c.names
}

// DeclaredNames returns every top-level name the module declares, which is
// the other half of the reference graph: what a package offers, against what
// its siblings ask for.
//
// `ast.DeclNames` reads what each declaration binds, so a destructuring `val`
// contributes every leaf and a `DeclareModuleDecl` or `DeclareGlobalDecl`
// contributes nothing. Neither of those binds a name a sibling package can
// refer to.
func DeclaredNames(module *ast.Module) set.Set[string] {
	names := set.NewSet[string]()
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			for _, name := range ast.DeclNames(decl) {
				names.Add(name)
			}
		}
		return true
	})
	return names
}

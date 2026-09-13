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
	// bound counts the type parameters in scope by name. A reference to one of
	// them names the parameter rather than a declaration, so it contributes no
	// edge. `Promise<T, E>` would otherwise be read as needing whichever package
	// declares `E`, and `std:math` declares one.
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

// EnterDecl visits the slots the AST walk does not reach on its own: a type
// parameter's constraint and default, and a signature's `throws` clause.
// `Accept` skips all three, so `class Box<T: HTMLElement>` would otherwise
// name `HTMLElement` with nothing recording it. #1587 covers closing the gap
// in the walk itself, which is where it belongs.
//
// It returns true, so the ordinary walk still runs and the slots it does
// reach are collected once each. A name recorded twice costs nothing, since
// the result is a set.
func (c *typeRefCollector) EnterDecl(d ast.Decl) bool {
	c.push(typeParamsOfDecl(d))
	switch d := d.(type) {
	case *ast.ClassDecl:
		c.visitTypeParams(d.TypeParams)
	case *ast.TypeDecl:
		c.visitTypeParams(d.TypeParams)
	case *ast.InterfaceDecl:
		c.visitTypeParams(d.TypeParams)
	case *ast.EnumDecl:
		c.visitTypeParams(d.TypeParams)
	case *ast.FuncDecl:
		c.visitTypeParams(d.TypeParams)
		if d.Throws != nil {
			d.Throws.Accept(c)
		}
	}
	return true
}

func (c *typeRefCollector) visitTypeParams(params []*ast.TypeParam) {
	for _, tp := range params {
		if tp.Constraint != nil {
			tp.Constraint.Accept(c)
		}
		if tp.Default != nil {
			tp.Default.Accept(c)
		}
	}
}

// EnterTypeAnn records a `TypeRefTypeAnn`'s name and keeps walking, so the
// arguments of `Foo<Bar>` are collected beside `Foo` itself.
//
// A qualified reference contributes both ends. The head is the name an import
// normally has to bring into scope. The last segment matters too, because
// namespace flattening turns `namespace Intl { type LocalesArgument }` into a
// top-level `LocalesArgument` while leaving references to it written
// `Intl.LocalesArgument`. There `Intl` names nothing and the last segment is
// what resolves.
func (c *typeRefCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	c.enterFuncTypeParams(t)
	// `typeof X` names the value X, which a package declares and an importer has
	// to reach the same way it reaches a type.
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

// enterFuncTypeParams binds a function type's own parameters over its body, the
// way a declaration's are bound over its.
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
// A `DeclareModuleDecl` and a `DeclareGlobalDecl` contribute nothing. Neither
// binds a name a sibling package can refer to.
func DeclaredNames(module *ast.Module) set.Set[string] {
	names := set.NewSet[string]()
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			addDeclName(names, decl)
		}
		return true
	})
	return names
}

func addDeclName(names set.Set[string], decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		addPatNames(names, d.Pattern)
	case *ast.FuncDecl:
		names.Add(d.Name.Name)
	case *ast.TypeDecl:
		names.Add(d.Name.Name)
	case *ast.InterfaceDecl:
		names.Add(d.Name.Name)
	case *ast.EnumDecl:
		names.Add(d.Name.Name)
	case *ast.ClassDecl:
		names.Add(d.Name.Name)
	case *ast.NamespaceDecl:
		// The namespace's own name is what a sibling refers to. Its members are
		// read off it and are not top-level names of their own.
		names.Add(d.Name.Name)
	}
}

// addPatNames records the identifiers a `val` or `var` pattern binds. The
// generated tree writes a bare name, and a destructuring pattern is handled
// so a hand-authored package cannot slip a binding past the graph.
func addPatNames(names set.Set[string], pat ast.Pat) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		names.Add(p.Name)
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			addPatNames(names, elem)
		}
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				addPatNames(names, e.Value)
			case *ast.ObjShorthandPat:
				names.Add(e.Key.Name)
			case *ast.ObjRestPat:
				addPatNames(names, e.Pattern)
			}
		}
	case *ast.RestPat:
		addPatNames(names, p.Pattern)
	}
}

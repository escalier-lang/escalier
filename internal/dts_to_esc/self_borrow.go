package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// borrowSelfInCallbacks respells a callback parameter typed as its class's own instance as
// `&Self`, in every instance method of every class the module declares. A callback is a
// function type reached from a method parameter's type without passing through another
// function type, and a callback parameter is one of its parameters. A parameter named
// `this` is not one.
//
// The iteration methods hand the callback the receiver itself. TypeScript writes that
// parameter as the class at its own type parameters, so `forEach` on `Array<T>` takes
// `callbackfn: (value: T, index: number, array: T[]) => void`. Converted as written it
// would read `array: mut Array<T>`, which gives the callback a mutable handle on an
// instance the method only borrows immutably. `&Self` is an immutable borrow of the
// receiver's own class instead, so the callback can read the instance but neither change
// it nor keep it past the call.
//
// It runs after rewriteReadonlyTwinRefs, so the `mut` that pass wraps around a twin's
// mutable name is already in place and is replaced along with the reference.
func borrowSelfInCallbacks(mod *StandaloneModule) {
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			if class, ok := decl.(*ast.ClassDecl); ok {
				borrowSelfInClass(class)
			}
		}
		return true
	})
}

// borrowSelfInClass applies borrowSelfInCallbacks to one class.
func borrowSelfInClass(class *ast.ClassDecl) {
	for _, elem := range class.Body {
		method, ok := elem.(*ast.MethodElem)
		if !ok || method.Static || method.Fn == nil {
			continue
		}
		visitor := &selfBorrowVisitor{class: class, shadowed: typeParamNameSet(method.Fn.TypeParams)}
		for _, param := range method.Fn.Params {
			if param.TypeAnn != nil {
				param.TypeAnn.Accept(visitor)
			}
		}
	}
}

// selfBorrowVisitor rewrites the callbacks one method parameter's type holds. It stops at
// each function type, so a function type nested inside a callback is not itself treated
// as a callback.
type selfBorrowVisitor struct {
	ast.DefaultVisitor
	class *ast.ClassDecl
	// shadowed holds the type parameter names the method declares, which hide the class's
	// parameters of the same name.
	shadowed set.Set[string]
}

func (v *selfBorrowVisitor) EnterTypeAnn(t ast.TypeAnn) bool {
	fn, ok := t.(*ast.FuncTypeAnn)
	if !ok {
		return true
	}
	shadowed := v.shadowed.Union(typeParamNameSet(fn.TypeParams))
	for _, param := range fn.Params {
		// A `this` parameter is left as written. On an event listener it is the target the
		// listener was added to, and the listener runs after the method that registered it
		// has returned, so it is not the receiver the method lends out.
		if pat, ok := param.Pattern.(*ast.IdentPat); ok && pat.Name == "this" {
			continue
		}
		if isOwnInstance(v.class, param.TypeAnn, shadowed) {
			span := param.TypeAnn.Span()
			self := ast.NewRefTypeAnn(ast.NewIdentifier("Self", span), nil, span)
			param.TypeAnn = ast.NewBorrowTypeAnn(false, nil, self, span)
		}
	}
	return false
}

// typeParamNameSet returns the names a type parameter list declares.
func typeParamNameSet(params []*ast.TypeParam) set.Set[string] {
	names := set.NewSet[string]()
	for _, tp := range params {
		names.Add(tp.Name)
	}
	return names
}

// isOwnInstance reports whether t names class at exactly its own type parameters, in their
// declared order, either directly or under `mut`. An argument naming a parameter in
// shadowed refers to a type parameter of the method or the callback rather than the class's.
// `Self` counts as well, which is how a TypeScript `this` type converts.
func isOwnInstance(class *ast.ClassDecl, t ast.TypeAnn, shadowed set.Set[string]) bool {
	if mutable, ok := t.(*ast.MutableTypeAnn); ok {
		t = mutable.Target
	}
	ref, ok := t.(*ast.TypeRefTypeAnn)
	if !ok || ref.Lifetime != nil || len(ref.LifetimeArgs) > 0 {
		return false
	}
	name, ok := ref.Name.(*ast.Ident)
	if !ok {
		return false
	}
	if name.Name == "Self" {
		return len(ref.TypeArgs) == 0
	}
	if name.Name != class.Name.Name || len(ref.TypeArgs) != len(class.TypeParams) {
		return false
	}
	for i, arg := range ref.TypeArgs {
		argRef, ok := arg.(*ast.TypeRefTypeAnn)
		if !ok || len(argRef.TypeArgs) > 0 {
			return false
		}
		argName, ok := argRef.Name.(*ast.Ident)
		if !ok || argName.Name != class.TypeParams[i].Name || shadowed.Contains(argName.Name) {
			return false
		}
	}
	return true
}

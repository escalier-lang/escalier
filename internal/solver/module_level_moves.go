package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A move takes an owned value out of the place holding it and leaves the destination its sole
// owner. A module-level binding or an import lives outside every call frame, so a move out of
// one would leave two owners of the same object. ModuleLevelMoveError rejects that move, and a
// borrow reads the value instead.
//
//	val xs: Array<number> = [1]
//	fn f() {
//		val a = xs   // ERROR: moves `xs` out of the module
//		val b = &xs  // borrows it
//	}
//
// The function may run again, and other code may read the binding later, so the move is
// rejected wherever it happens. The rule is about the binding rather than a path through the
// body, so each flow site that moves a value checks it as the site is walked, and the
// dataflow pass does not.

// ModuleLevelMoveError is reported when a function body moves an owned value out of a module-level
// binding or an import, or when a module-level `mut` binding takes one.
type ModuleLevelMoveError struct {
	// Source is the source text of the moved place, such as `xs` or `cfg.items`.
	Source string
	// IntoMut marks a move into a module-level `mut` binding rather than one inside a body.
	IntoMut bool
	node    ast.Node
}

func (*ModuleLevelMoveError) isSolverError()      {}
func (e *ModuleLevelMoveError) Span() ast.Span    { return e.node.Span() }
func (*ModuleLevelMoveError) Related() []ast.Span { return nil }
func (e *ModuleLevelMoveError) Message() string {
	if e.IntoMut {
		return fmt.Sprintf("cannot move module-level value '%s' into a `mut` binding, since the module keeps it. Borrow it with `&%s`", e.Source, e.Source)
	}
	return fmt.Sprintf("cannot move module-level value '%s' inside a function. Borrow it with `&%s`", e.Source, e.Source)
}

// rejectModuleLevelMutMove reports a ModuleLevelMoveError for each place rooted at a
// module-level binding or an import that source moves into a `mut` binding at module level.
// Code outside a function body consumes nothing, so the place stays readable after the move,
// and a write through the `mut` binding would change a value that place still reads. scope
// resolves the root.
func (c *checker) rejectModuleLevelMutMove(scope *Scope, source ast.Expr) {
	for _, r := range movedValues(source) {
		b, ok := c.placeRootBinding(scope, r)
		if !ok {
			continue
		}
		t := c.info.TypeOf(r)
		if _, isIdent := r.(*ast.IdentExpr); isIdent {
			t = bindingType(b)
		}
		if !c.holdsConcreteOwned(t, set.NewSet[*soltype.TypeVarType]()) {
			continue
		}
		text, _ := printer.Print(r, printer.DefaultOptions())
		c.report(&ModuleLevelMoveError{Source: text, IntoMut: true, node: r})
	}
}

// placeRootBinding returns the value binding the place e is rooted at. e is a binding's name, or
// a member or index chain off one. A class or enum name is not such a binding, and neither is a
// class value.
func (c *checker) placeRootBinding(scope *Scope, e ast.Expr) (ValueBinding, bool) {
	for {
		switch x := e.(type) {
		case *ast.IdentExpr:
			b, found := c.lookupValueBinding(scope, x.Name)
			if !found || declaredAsClassOrEnum(b) || c.isClassValue(bindingType(b)) {
				return ValueBinding{}, false
			}
			return b, true
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			return ValueBinding{}, false
		}
	}
}

// moduleLevelRootInBody returns the binding the place e is rooted at, when e is read inside a
// function body and that binding is a module-level `val` or `var` or an import. e is such a
// binding's name, or a static member or index chain off one. Inside a body, such a binding is a
// name the rename pass left unnumbered, or a module-level name a module's function captures. A
// member chain off a namespace is rooted at the namespace's value it reads first. A class or enum
// name is not such a binding, and neither is a class value. scope resolves the root.
//
// It answers false for code outside a function body, which consumes nothing.
// rejectModuleLevelMutMove checks a move into a module-level `mut` binding instead.
func (c *checker) moduleLevelRootInBody(scope *Scope, e ast.Expr) (ValueBinding, bool) {
	if c.fn == nil || c.fn.cfg == nil || scope == nil {
		return ValueBinding{}, false
	}
	// members holds the static member names read off the root, in order from the root.
	var members []string
	for {
		switch x := e.(type) {
		case *ast.IdentExpr:
			id := c.varIDs.Of(x)
			if id > 0 && !(c.fn.capturesModule && c.fn.captureVarIDs.Contains(id)) {
				return ValueBinding{}, false
			}
			b, found := c.lookupValueBinding(scope, x.Name)
			if !found {
				ns, isNamespace := scope.GetNamespace(x.Name)
				if !isNamespace {
					return ValueBinding{}, false
				}
				// A path that reaches no value leaves b the zero binding, whose type is
				// nil and so holds nothing that moves.
				b, _ = namespaceMember(ns, members)
			}
			if declaredAsClassOrEnum(b) || c.isClassValue(bindingType(b)) {
				return ValueBinding{}, false
			}
			return b, true
		case *ast.MemberExpr:
			if x.Prop == nil {
				return ValueBinding{}, false
			}
			members = append([]string{x.Prop.Name}, members...)
			e = x.Object
		case *ast.IndexExpr:
			members = nil
			e = x.Object
		default:
			return ValueBinding{}, false
		}
	}
}

// namespaceMember returns the value binding the member names in path read off ns, descending
// through nested namespaces until a name binds a value. It reports false when path reaches no
// value of ns.
func namespaceMember(ns *Namespace, path []string) (ValueBinding, bool) {
	for _, name := range path {
		if b, ok := ns.Values[name]; ok {
			return b, true
		}
		nested, ok := ns.Nested[name]
		if !ok {
			return ValueBinding{}, false
		}
		ns = nested
	}
	return ValueBinding{}, false
}

// declaredAsClassOrEnum reports whether some declaration of b is a class or an enum.
func declaredAsClassOrEnum(b ValueBinding) bool {
	for _, src := range b.Sources {
		if np, ok := src.(*ast.NodeProvenance); ok {
			switch np.Node.(type) {
			case *ast.ClassDecl, *ast.EnumDecl:
				return true
			}
		}
	}
	return false
}

// movesModuleLevelPlace reports whether moving source, a value of type sourceT, moves it out of a
// place rooted at a module-level `val` or `var` or an import. A bare name is judged by its
// binding's type. The value moves only when its type is known to be an owned reference shape,
// so a value whose type is still unresolved is not taken to move. scope resolves the root.
func (c *checker) movesModuleLevelPlace(scope *Scope, source ast.Expr, sourceT soltype.Type) bool {
	b, ok := c.moduleLevelRootInBody(scope, source)
	if !ok {
		return false
	}
	if _, isIdent := source.(*ast.IdentExpr); isIdent {
		sourceT = bindingType(b)
	}
	return c.holdsConcreteOwned(sourceT, set.NewSet[*soltype.TypeVarType]())
}

// isClassValue reports whether t is an object type with a constructor or call signature, the
// type of a class's value or of a callable object.
func (c *checker) isClassValue(t soltype.Type) bool {
	obj, ok := c.expandAliasChain(t).(*soltype.ObjectType)
	if !ok {
		return false
	}
	for _, elem := range obj.Elems {
		switch elem.(type) {
		case *soltype.ConstructorElem, *soltype.CallableElem:
			return true
		}
	}
	return false
}

// holdsConcreteOwned reports whether t is a concrete owned reference shape, or a type variable
// with such a lower bound, following lower bounds that are themselves variables. seen holds the
// variables on the current path.
func (c *checker) holdsConcreteOwned(t soltype.Type, seen set.Set[*soltype.TypeVarType]) bool {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return t != nil && c.isConcreteOwned(t)
	}
	if seen.Contains(v) {
		return false
	}
	seen.Add(v)
	defer seen.Remove(v)
	return slices.ContainsFunc(v.LowerBounds, func(lb soltype.Type) bool {
		return c.holdsConcreteOwned(lb, seen)
	})
}

// rejectModuleLevelMove reports a ModuleLevelMoveError for each value that moving source moves
// out of a place rooted at a module-level `val` or `var` or an import. The value moved is
// source itself, or a result of one of its branches when source is a conditional, `match`,
// `do`, or `try`. scope resolves the root.
func (c *checker) rejectModuleLevelMove(scope *Scope, source ast.Expr) {
	for _, r := range branchResults(source) {
		if c.movesModuleLevelPlace(scope, r, c.info.TypeOf(r)) {
			c.reportModuleLevelMove(r)
		}
	}
}

// movedValues returns the expressions whose values source moves. It starts from the results of
// source's branches. An object or tuple literal among them contributes its property values and
// elements instead, found the same way. A shorthand property contributes its key, which names
// the binding it reads. A spread contributes nothing.
func movedValues(source ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, r := range branchResults(source) {
		switch lit := r.(type) {
		case *ast.ObjectExpr:
			for _, elem := range lit.Elems {
				prop, ok := elem.(*ast.PropertyExpr)
				if !ok {
					continue
				}
				if prop.Value != nil {
					out = append(out, movedValues(prop.Value)...)
				} else if key, ok := prop.Name.(*ast.IdentExpr); ok {
					out = append(out, key)
				}
			}
		case *ast.TupleExpr:
			for _, elem := range lit.Elems {
				out = append(out, movedValues(elem)...)
			}
		default:
			out = append(out, r)
		}
	}
	return out
}

// branchResults returns the expressions whose value e produces. It descends through the
// branches liveness.BranchResults returns, and through a type cast, until it reaches an
// expression that does neither.
func branchResults(e ast.Expr) []ast.Expr {
	return appendBranchResults(nil, e)
}

// appendBranchResults appends to results the expressions branchResults returns for e.
func appendBranchResults(results []ast.Expr, e ast.Expr) []ast.Expr {
	if cast, ok := e.(*ast.TypeCastExpr); ok {
		return appendBranchResults(results, cast.Expr)
	}
	branches, ok := liveness.BranchResults(e)
	if !ok {
		// A branch with no result expression moves nothing, and the other branches still move
		// what they produce.
		if e == nil {
			return results
		}
		return append(results, e)
	}
	for _, branch := range branches {
		results = appendBranchResults(results, branch)
	}
	return results
}

// reportModuleLevelMove reports a ModuleLevelMoveError blaming source, the expression whose
// value moves.
func (c *checker) reportModuleLevelMove(source ast.Node) {
	// A place expression is a name, member read, or index read, all of which the printer
	// renders.
	text, _ := printer.Print(source, printer.DefaultOptions())
	c.report(&ModuleLevelMoveError{Source: text, node: source})
}

// rejectModuleLevelDestructure reports whether init is a place rooted at a module-level `val` or
// `var` or an import. It reports a ModuleLevelMoveError when the destructuring of init into pat
// takes some leaf of it by value. scope resolves the root and the leaf bindings.
func (c *checker) rejectModuleLevelDestructure(scope *Scope, pat ast.Pat, init ast.Expr) bool {
	if _, ok := c.moduleLevelRootInBody(scope, init); !ok {
		return false
	}
	ownedLeaf := false
	ast.ForEachLeafBinding(pat, func(name string, _ ast.Node) {
		if b, found := scope.GetValue(name); found && c.isConcreteOwned(bindingType(b)) {
			ownedLeaf = true
		}
	})
	if ownedLeaf {
		c.reportModuleLevelMove(init)
	}
	return true
}

// rejectModuleLevelSpread reports a ModuleLevelMoveError when the spread operand src, whose type
// is t, moves a value out of a place rooted at a module-level binding or an import. A spread
// copies the elements or fields of src into the new literal. When every element or field of src
// is a value type, the spread copies values only and moves nothing. A bare name's type is read
// from the binding scope resolves it to.
func (c *checker) rejectModuleLevelSpread(scope *Scope, src ast.Expr, t soltype.Type) {
	if b, ok := c.moduleLevelRootInBody(scope, src); ok {
		srcT := t
		if _, isIdent := src.(*ast.IdentExpr); isIdent {
			srcT = bindingType(b)
		}
		if c.holdsOnlyValues(srcT) {
			return
		}
	}
	c.rejectModuleLevelMove(scope, src)
}

// holdsOnlyValues reports whether every element or field of t is a value type. It answers for
// an `Array<T>` instance, a tuple, and an object, and false for any other type.
func (c *checker) holdsOnlyValues(t soltype.Type) bool {
	t = c.expandAliasChain(t)
	if elem, ok := c.ctx.arrayElem(t); ok {
		return isValueType(elem)
	}
	switch t := t.(type) {
	case *soltype.TupleType:
		for _, elem := range t.Elems {
			if !isValueType(elem) {
				return false
			}
		}
		return true
	case *soltype.ObjectType:
		for _, elem := range t.Elems {
			prop, ok := elem.(*soltype.PropertyElem)
			if !ok || !isValueType(prop.Type) {
				return false
			}
		}
		return true
	}
	return false
}

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
// body, so the emitter reports it where it settles a flow as a move, and the dataflow pass
// does not.

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
	root, isIdent := chainOf(e, nil).root.(*ast.IdentExpr)
	if !isIdent {
		return ValueBinding{}, false
	}
	b, found := c.lookupValueBinding(scope, root.Name)
	if !found || declaredAsClassOrEnum(b) || c.isClassValue(bindingType(b)) {
		return ValueBinding{}, false
	}
	return b, true
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
	chain := chainOf(e, nil)
	root, isIdent := chain.root.(*ast.IdentExpr)
	if !isIdent {
		return ValueBinding{}, false
	}

	// members holds the static member names read off the root before the first index, in
	// order from the root.
	var members []string
	indexed := false
	for _, link := range chain.links {
		if link.key == nil && link.dynamic {
			return ValueBinding{}, false
		}
		indexed = indexed || link.key != nil
		if !indexed {
			members = append(members, link.seg.name)
		}
	}

	id := c.varIDs.Of(root)
	if id > 0 && !(c.fn.capturesModule && c.fn.captureVarIDs.Contains(id)) {
		return ValueBinding{}, false
	}
	b, found := c.lookupValueBinding(scope, root.Name)
	if !found {
		ns, isNamespace := scope.GetNamespace(root.Name)
		if !isNamespace {
			return ValueBinding{}, false
		}
		// A path that reaches no value leaves b the zero binding, whose type is nil and so
		// holds nothing that moves.
		b, _ = namespaceMember(ns, members)
	}
	if declaredAsClassOrEnum(b) || c.isClassValue(bindingType(b)) {
		return ValueBinding{}, false
	}
	return b, true
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

// reportModuleLevelMove reports a ModuleLevelMoveError when ev, the event a flow through dest
// settled as in table, moves a value out of a place rooted at a module-level binding the body
// captures. t is the type of the value that flows. A flow into a captureDest is a closure's
// capture of the binding, which reads the binding when the closure runs and is not reported.
// A closure whose body moves the capture flows through an owned destination instead, and the
// error blames the expression in the closure's body that moves it.
func (c *checker) reportModuleLevelMove(table *ownershipEvents, ev ownershipEvent, dest flowDest, t soltype.Type) {
	mv, ok := ev.(*moveEvent)
	if !ok {
		return
	}
	if _, isCapture := dest.(*captureDest); isCapture {
		return
	}
	b, ok := table.moduleRoots[mv.place.root]
	if !ok {
		return
	}
	if _, isIdent := mv.node.(*ast.IdentExpr); isIdent && len(mv.place.path) == 0 {
		t = bindingType(b)
	}
	blame := mv.node
	if closure, isClosure := blame.(ast.Func); isClosure {
		blame = c.capturesMovedBy[closure][table.names[mv.place.root]]
	}
	c.reportOwnedModuleMove(table, b, blame, t)
}

// reportUnplacedModuleMove reports a ModuleLevelMoveError when the flow of source into holder
// through dest moves a value out of a place rooted at a module-level binding or an import
// whose name the rename pass left unnumbered. source names no place of the body, so the flow
// emits no event, and it is classified here instead. A flow whose destination is not settled
// yet, such as an argument of a callee whose signature is not known, is not reported.
func (c *checker) reportUnplacedModuleMove(source ast.Expr, holder eventHolder, dest flowDest) {
	b, ok := c.moduleLevelRootInBody(c.fn.scope, source)
	if !ok {
		return
	}
	t := c.info.TypeOf(source)
	if _, isIdent := source.(*ast.IdentExpr); isIdent {
		t = bindingType(b)
	}
	flow := flowBase{eventBase: eventBase{node: source}, holder: holder}
	ev, _, settled := c.classifyFlow(c.fn.events, flow, t, dest, false, false)
	if _, moves := ev.(*moveEvent); settled && moves {
		c.reportOwnedModuleMove(c.fn.events, b, source, t)
	}
}

// reportOwnedModuleMove reports a ModuleLevelMoveError blaming node, an expression whose value
// of type t moves out of a place rooted at the module-level binding b. It reports nothing when
// t is not known to be owned, or when b is a class, an enum, or a class value. A spread whose
// elements or fields are all value types copies them and moves nothing. table holds the flow's
// event, and each node is blamed at most once in it.
func (c *checker) reportOwnedModuleMove(table *ownershipEvents, b ValueBinding, node ast.Node, t soltype.Type) {
	if node == nil || declaredAsClassOrEnum(b) || c.isClassValue(bindingType(b)) {
		return
	}
	if !c.holdsConcreteOwned(t, set.NewSet[*soltype.TypeVarType]()) {
		return
	}
	if table.spreads.Contains(node) && c.spreadCopiesValues(t) {
		return
	}
	if table.moduleMoves.Contains(node) {
		return
	}
	table.moduleMoves.Add(node)

	// A moved place is a name, member read, or index read, all of which the printer renders.
	text, _ := printer.Print(node, printer.DefaultOptions())
	c.report(&ModuleLevelMoveError{Source: text, node: node})
}

// removeModuleLevelMoveErrors removes from c.errs each ModuleLevelMoveError that blames one of
// nodes. The errors left keep their order.
func (c *checker) removeModuleLevelMoveErrors(nodes set.Set[ast.Node]) {
	if nodes.Len() == 0 {
		return
	}
	c.errs = slices.DeleteFunc(c.errs, func(e SolverError) bool {
		m, ok := e.(*ModuleLevelMoveError)
		return ok && nodes.Contains(m.node)
	})
}

// spreadCopiesValues reports whether a spread of a value of type t copies only value types
// into the literal that holds it. An iterable copies the elements syncElemType finds, and an
// object copies its fields.
func (c *checker) spreadCopiesValues(t soltype.Type) bool {
	if elem, ok := c.syncElemType(t); ok {
		return c.holdsValueType(elem)
	}
	obj, ok := c.expandAliasChain(t).(*soltype.ObjectType)
	if !ok {
		return false
	}
	return !slices.ContainsFunc(obj.Elems, func(elem soltype.ObjTypeElem) bool {
		prop, isProp := elem.(*soltype.PropertyElem)
		return !isProp || !c.holdsValueType(prop.Type)
	})
}

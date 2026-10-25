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
// owner. Inside a function body the move engine tracks moves out of the body's own locals. A
// module-level binding or an import lives outside every call frame, so a move out of one would
// leave two owners of the same object. ModuleLevelMoveError rejects that move, and a borrow reads the
// value instead.
//
//	val xs: Array<number> = [1]
//	fn f() {
//		val a = xs   // ERROR: moves `xs` out of the module
//		val b = &xs  // borrows it
//	}
//
// A closure that moves a local it captures takes that local when the closure is written, as a
// Rust `move` closure does. The enclosing body records the move at the statement that writes the
// closure, so a later use of the local there is a use-after-move. The closure owns the value from then on, and its body
// moves it out on the first call, so a call of the closure moves the binding it is stored in.
//
//	fn f() {
//		val a: Array<number> = [1]
//		val g = fn() -> Array<number> { return a }  // moves `a` into `g`
//		val m = g()                                 // moves `g`
//		val n = g()                                 // ERROR: use of moved value 'g'
//	}

// ModuleLevelMoveError is reported when a function body moves an owned value out of a module-level
// binding or an import.
type ModuleLevelMoveError struct {
	// Source is the source text of the moved place, such as `xs` or `cfg.items`.
	Source string
	node   ast.Node
}

func (*ModuleLevelMoveError) isSolverError()      {}
func (e *ModuleLevelMoveError) Span() ast.Span    { return e.node.Span() }
func (*ModuleLevelMoveError) Related() []ast.Span { return nil }
func (e *ModuleLevelMoveError) Message() string {
	return fmt.Sprintf("cannot move module-level value '%s' inside a function. Borrow it with `&%s`", e.Source, e.Source)
}

// moduleLevelRootInBody returns the binding the place e is rooted at, when e is read inside a
// function body and that binding is a module-level `val` or `var` or an import. e is such a
// binding's name, or a static member or index chain off one. Inside a body, such a binding is a
// name the rename pass left unnumbered, or a module-level name a module's function captures. A
// member chain off a namespace is rooted at the namespace's value it reads first. A class or enum
// name is not such a binding, and neither is a class value. scope resolves the root.
//
// It answers false for code outside a function body, since that code moves nothing the move
// engine tracks.
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

// rejectModuleLevelMove reports whether moving source moves a value out of a place rooted at a
// module-level `val` or `var` or an import. The value moved is source itself, or a result of
// one of its branches when source is a conditional, `match`, `do`, or `try`. It reports a
// ModuleLevelMoveError for each such place. The caller records no move for source when it
// answers true. scope resolves the root.
func (c *checker) rejectModuleLevelMove(scope *Scope, source ast.Expr) bool {
	moved := false
	for _, r := range branchResults(source) {
		if c.movesModuleLevelPlace(scope, r, c.info.TypeOf(r)) {
			c.reportModuleLevelMove(r)
			moved = true
		}
	}
	return moved
}

// branchResults returns the expressions whose value e produces. It descends through the
// branches of a conditional, `match`, `do`, and `try`, and through a type cast. Any other
// expression produces its own value, so it returns e alone.
func branchResults(e ast.Expr) []ast.Expr {
	var results []ast.Expr
	var visit func(ast.Expr)
	visit = func(e ast.Expr) {
		switch e := e.(type) {
		case nil:
		case *ast.IfElseExpr:
			visit(liveness.BlockResultExpr(e.Cons))
			visit(liveness.BlockOrExprResultExpr(e.Alt))
		case *ast.IfValExpr:
			visit(liveness.BlockResultExpr(e.Cons))
			visit(liveness.BlockOrExprResultExpr(e.Alt))
		case *ast.MatchExpr:
			for _, mc := range e.Cases {
				visit(liveness.BlockOrExprResultExpr(&mc.Body))
			}
		case *ast.DoExpr:
			visit(liveness.BlockResultExpr(e.Body))
		case *ast.TryCatchExpr:
			visit(liveness.BlockResultExpr(e.Try))
			for _, mc := range e.Catch {
				visit(liveness.BlockOrExprResultExpr(&mc.Body))
			}
		case *ast.TypeCastExpr:
			visit(e.Expr)
		default:
			results = append(results, e)
		}
	}
	visit(e)
	return results
}

// reportModuleLevelMove reports a ModuleLevelMoveError blaming source, the expression whose
// value moves, and records source in moduleLevelMoves.
func (c *checker) reportModuleLevelMove(source ast.Node) {
	if c.fn.moduleLevelMoves == nil {
		c.fn.moduleLevelMoves = set.NewSet[ast.Node]()
	}
	c.fn.moduleLevelMoves.Add(source)
	// A place expression is a name, member read, or index read, all of which the printer
	// renders.
	text, _ := printer.Print(source, printer.DefaultOptions())
	c.report(&ModuleLevelMoveError{Source: text, node: source})
}

// rejectModuleLevelDestructure reports whether init is a place rooted at a module-level `val` or
// `var` or an import. It reports a ModuleLevelMoveError when the destructuring of init into pat
// takes some leaf of it by value. scope resolves the root and the leaf bindings. The caller
// records no move for such an init.
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

// movedCaptures maps the name of each local of the enclosing body that the current closure
// moves, wholly or in part, to the expression that moves it. Only a capture of a concrete owned
// type counts. The current body must be a closure, whose captures are locals of the body that
// encloses it.
func (c *checker) movedCaptures() map[string]ast.Node {
	// Places are visited in VarID order, so a capture moved through several places blames
	// the same one on every run.
	ids := make([]liveness.VarID, 0, len(c.fn.movePlaces))
	for id := range c.fn.movePlaces {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var moved map[string]ast.Node
	for _, id := range ids {
		p := c.fn.movePlaces[id]
		// A capture whose type is a bare type variable may hold a value type, so the closure
		// is not taken to move it.
		if !c.fn.captureVarIDs.Contains(p.root) || !c.isConcreteOwned(c.fn.varIDTypes[p.root]) {
			continue
		}
		name := c.fn.varIDNames[p.root]
		if _, seen := moved[name]; seen {
			continue
		}
		if moved == nil {
			moved = map[string]ast.Node{}
		}
		moved[name] = c.fn.moveNodes[id]
	}
	return moved
}

// funcMovesCaptures reports whether decl declares a closure whose body moves a local it
// captures. decl is a script's `fn` declaration, or a `val`/`var` declaration whose initializer
// is the function expression.
func (c *checker) funcMovesCaptures(decl ast.Node) bool {
	if vd, ok := decl.(*ast.VarDecl); ok {
		decl = vd.Init
	}
	fn, ok := decl.(ast.Func)
	return ok && len(c.capturesMovedBy[fn]) > 0
}

// consumeOnceCallee moves the local binding call's callee names when that binding holds a
// closure whose body moves a local it captures. ref is the call's statement.
//
// The read of the callee is recorded as a use, so a second call is a use-after-move. The read
// is recorded here because recordUse records only a read of a reference-shaped value, and a
// function value is not one, so the walk records no use of the callee by itself.
// The use blames the callee name rather than the whole call, so in a second `g()` the
// use-after-move error points at `g`, the binding that moved.
func (c *checker) consumeOnceCallee(call *ast.CallExpr, ref liveness.StmtRef) {
	ident, ok := call.Callee.(*ast.IdentExpr)
	if !ok || c.varIDs.Of(ident) <= 0 {
		return
	}
	decl, ok := c.info.DeclOf(ident)
	if !ok || !c.funcMovesCaptures(decl) {
		return
	}
	p := movePlace{root: c.varIDs.Of(ident)}
	c.fn.useSites = append(c.fn.useSites, moveUse{place: p, ref: ref, node: ident, loanSeqAt: c.fn.loanSeq + 1})
	c.recordMovePlace(p, call, ref)
}

// consumeSpreadIntoLiteral is consumeIntoLiteral for the operand src of a spread, whose type is
// t. A spread copies the elements or fields of src into the new literal. When src is rooted at a
// module-level binding and every element or field of it is a value type, the
// spread copies values only and moves nothing. A bare name's type is read from the binding scope
// resolves it to.
func (c *checker) consumeSpreadIntoLiteral(scope *Scope, src ast.Expr, t soltype.Type, ref liveness.StmtRef, hasRef bool) {
	if b, ok := c.moduleLevelRootInBody(scope, src); ok {
		srcT := t
		if _, isIdent := src.(*ast.IdentExpr); isIdent {
			srcT = bindingType(b)
		}
		if c.holdsOnlyValues(srcT) {
			return
		}
	}
	c.consumeIntoLiteral(scope, src, t, ref, hasRef)
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

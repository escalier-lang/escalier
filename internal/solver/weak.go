package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A weak variable is a type variable that belongs to a binding which did not generalize,
// as generalizes decides. `val mut seen = Map()` binds `seen` to `Map<K, V>`, where K and V
// are weak. Every function that reads `seen` constrains the same K and V, so
// `seen.set("k", 1)` in one function and `seen.set("k", "s")` in another both reach V.
//
// A weak variable sits at the level of the scope that declares the binding. For a
// module-level binding that is the module's level, the level every component generalizes
// at, so no later component quantifies it.

// keyVarDecl returns the declaration that key's binding var stands for when that is a
// `val` or `var`, or nil when it is another kind of declaration.
func keyVarDecl(g *dep_graph.DepGraph, key dep_graph.BindingKey) *ast.VarDecl {
	decls := g.GetDecls(key)
	if len(decls) == 0 {
		return nil
	}
	vd, _ := decls[0].(*ast.VarDecl)
	return vd
}

// declLevel returns the level a module-level declaration d is inferred at, for a component
// at lvl. A `val` or `var` that does not generalize is inferred at lvl, which makes the
// variables its initializer mints weak. Every other declaration is inferred at lvl+1.
func declLevel(d ast.Decl, lvl int) int {
	if vd, ok := d.(*ast.VarDecl); ok && !generalizes(vd) {
		return lvl
	}
	return lvl + 1
}

// weakBinding is a `val` or `var` that did not generalize, waiting for the module or
// script that declares it to finish so its display type can be sealed from the bounds its
// weak variables hold then.
type weakBinding struct {
	scheme *PolyScheme
	// infoNode is the node the binding's display type is recorded on, or nil when it has
	// none.
	infoNode ast.Node
	// writable marks a binding written `mut` or declared with `var`, which can replace or
	// write what it holds.
	writable bool
}

// newWeakBinding returns the weakBinding for a binding declared by d and bound to scheme,
// or false when scheme quantifies over nothing settleWeakDisplay can reseal, as the
// MonoScheme of a binding that recovered to the error sentinel does.
func newWeakBinding(d *ast.VarDecl, scheme TypeScheme, infoNode ast.Node) (weakBinding, bool) {
	sc, ok := scheme.(*PolyScheme)
	if !ok {
		return weakBinding{}, false
	}
	writable := d.Kind == ast.VarKind || hasMutLeaf(d.Pattern)
	return weakBinding{scheme: sc, infoNode: infoNode, writable: writable}, true
}

// noteWeakBinding records the weak module-level binding b for settleWeakBindings. scheme
// is the scheme b was bound to.
func (c *checker) noteWeakBinding(b *componentBinding, scheme TypeScheme) {
	vd, ok := b.primary.(*ast.VarDecl)
	if !ok {
		return
	}
	if w, ok := newWeakBinding(vd, scheme, b.infoNode); ok {
		c.weakBindings = append(c.weakBindings, w)
	}
}

// noteWeakLocal records the weak body-level binding d, bound to scheme, for
// settleWeakLocals.
func (c *checker) noteWeakLocal(d *ast.VarDecl, scheme TypeScheme) {
	if w, ok := newWeakBinding(d, scheme, d.Pattern); ok {
		c.weakLocals = append(c.weakLocals, w)
	}
}

// weakReader is a generalized module-level binding whose type reads a weak variable, such
// as `fn get() { return seen.get("k") }` over a weak `seen`.
type weakReader struct {
	scheme *PolyScheme
	// infoNode is the node the binding's display type is recorded on, or nil when it has
	// none.
	infoNode ast.Node
}

// noteWeakReader records the generalized module-level binding b for settleWeakBindings when
// its type reaches a weak variable at or below lvl. scheme is the scheme b was bound to.
// Only a weak binding of the module mints a variable at lvl that a later binding can
// read, so the walk is skipped until the module has one.
func (c *checker) noteWeakReader(b *componentBinding, scheme TypeScheme, lvl int) {
	sc, ok := scheme.(*PolyScheme)
	if !ok || len(c.weakBindings) == 0 {
		return
	}
	if len(scanWeakVars(b.v, lvl)) > 0 {
		c.weakReaders = append(c.weakReaders, weakReader{scheme: sc, infoNode: b.infoNode})
	}
}

// settleWeakBindings runs once every component of a module at lvl is inferred:
//
//  1. It seals again the display type of each binding noteWeakReader recorded and records
//     it, so Info shows the type the module settled the weak variables on rather than the
//     type they had when the reader's component closed.
//  2. It settles each weak binding noteWeakBinding recorded, as settleWeakDisplay does.
//  3. It reports a weak variable of such a binding that received both an owned value and a
//     borrow.
//  4. It settles each body-level weak binding the module's functions declare.
func (c *checker) settleWeakBindings(lvl int) {
	for _, r := range c.weakReaders {
		// A weak variable is not one of the reader's type parameters, so it is inlined to
		// the bounds it holds now.
		r.scheme.coalesced = c.subsumeFinal(inlineWeakVars(coalesceScheme(r.scheme.Body, r.scheme.Level, r.scheme.declared, r.scheme.declaredLts), lvl))
		if r.infoNode != nil {
			c.recordType(r.infoNode, r.scheme.coalesced)
		}
	}
	for _, w := range c.weakBindings {
		c.settleWeakDisplay(w)
		root, isVar := w.scheme.Body.(*soltype.TypeVarType)
		if !isVar || w.infoNode == nil {
			continue
		}
		// Every write into the binding reaches its weak variables as a lower bound. A
		// variable that collects an owned value from one write and a borrow from another
		// holds a union no single ownership describes, the same fault a join of such
		// branches reports.
		if slices.ContainsFunc(scanWeakVars(root, lvl), mixesOwnership) {
			c.report(&MixedOwnershipError{Node: w.infoNode})
		}
	}
	c.settleWeakLocals()
}

// settleWeakLocals settles each body-level weak binding noteWeakLocal recorded, as
// settleWeakDisplay does, and clears the list. It runs once the whole module or script is
// walked. A local's type can read a variable an enclosing scope settles, such as a weak
// module-level binding's, so the body that declares the local ending is not enough.
func (c *checker) settleWeakLocals() {
	for _, w := range c.weakLocals {
		c.settleWeakDisplay(w)
	}
	c.weakLocals = nil
}

// settleWeakDisplay seals w's display type from the bounds its weak variables hold now and
// records it on w's node. generalize sealed the display while the weak variables had only
// the initializer's bounds, so it showed none of the writes that followed.
//
// No use is left to add a bound, so the body is first coalesced as if its weak variables
// were quantified, which simplifies them the way a generalized binding's variables are. A
// variable that only receives values reads as `never`, and a recursive type folds to its
// smallest μ form. A weak variable that survives that pass occurs in both polarities. It
// is not a type parameter of the binding, so the second pass inlines it to its bounds.
//
// The settled type widens its literals, as a `var` does, when the binding is writable.
// `val mut seen = Map()` written with `seen.set("k", 1)` settles to `Map<string, number>`.
// An immutable binding keeps its literals the way one with a literal initializer does, so
// `val b = Box(5)` settles to `Box<5>` as `val p = [5, 10]` keeps `[5, 10]`.
func (c *checker) settleWeakDisplay(w weakBinding) {
	sc := w.scheme
	simplified := coalesceScheme(sc.Body, sc.Level-1, sc.declared, sc.declaredLts)
	display := inlineWeakVars(simplified, sc.Level)
	if w.writable {
		display = c.widenLiterals(display)
	}
	sc.coalesced = c.subsumeFinal(display)
	if w.infoNode != nil {
		c.recordType(w.infoNode, sc.coalesced)
	}
}

// widenLiterals returns t with each literal widened to its primitive, as widen does for
// one. A function type is kept whole, and so is a class type argument the class's measured
// variance makes contravariant, since widening either would change what the value accepts.
func (c *checker) widenLiterals(t soltype.Type) soltype.Type {
	return t.Accept(&literalWidener{ctx: c.ctx}, soltype.Positive)
}

// literalWidener is the visitor behind widenLiterals.
type literalWidener struct {
	ctx *Context
}

func (w *literalWidener) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.FuncType:
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.LitType, *soltype.UniqueSymbolType, *soltype.RegexType:
		return soltype.EnterResult{Type: widen(t), SkipChildren: true}
	case *soltype.ClassType:
		return soltype.EnterResult{Type: w.widenClassArgs(t), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

// widenClassArgs returns ct with each type argument widened, except one the class's measured
// variance makes contravariant.
func (w *literalWidener) widenClassArgs(ct *soltype.ClassType) soltype.Type {
	def, _ := w.ctx.classDef(ct.Name)
	args := make([]soltype.Type, len(ct.TypeArgs))
	for i, arg := range ct.TypeArgs {
		if def.varianceAt(i, false) == Contravariant {
			args[i] = arg
			continue
		}
		args[i] = arg.Accept(w, soltype.Positive)
	}
	out := *ct
	out.TypeArgs = args
	return &out
}

func (w *literalWidener) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// scanWeakVars returns the weak variables the type of the binding var v reaches, in the
// order a walk reaches them. A weak variable is one at or below lvl, and v itself is never
// returned. The walk starts covariant and follows each variable's bounds in the direction
// its polarity reads, lower bounds when positive and upper bounds when negative.
func scanWeakVars(v *soltype.TypeVarType, lvl int) []*soltype.TypeVarType {
	s := &weakVarScanner{
		lvl:     lvl,
		root:    v,
		seen:    set.NewSet[polarVar](),
		reached: set.NewSet[*soltype.TypeVarType](),
		binders: set.NewSet[*soltype.TypeVarType](),
	}
	v.Accept(s, soltype.Positive)
	return s.out
}

// polarVar is a variable reached at one polarity.
type polarVar struct {
	v   *soltype.TypeVarType
	pol soltype.Polarity
}

// weakVarScanner is the visitor behind scanWeakVars.
type weakVarScanner struct {
	lvl     int
	root    *soltype.TypeVarType
	seen    set.Set[polarVar]
	reached set.Set[*soltype.TypeVarType]
	// binders holds the type-parameter variables of each function type the walk entered.
	binders set.Set[*soltype.TypeVarType]
	out     []*soltype.TypeVarType
}

func (s *weakVarScanner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		key := polarVar{v: t, pol: pol}
		if s.seen.Contains(key) {
			return soltype.EnterResult{SkipChildren: true}
		}
		s.seen.Add(key)
		if t != s.root && t.Level <= s.lvl && !s.binders.Contains(t) && !s.reached.Contains(t) {
			s.reached.Add(t)
			s.out = append(s.out, t)
		}
		bounds := t.LowerBounds
		if pol == soltype.Negative {
			bounds = t.UpperBounds
		}
		for _, b := range bounds {
			b.Accept(s, pol)
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.FuncType:
		// A function type's own type parameters are quantified by the function, so they
		// are not weak however low their level.
		for _, tp := range t.TypeParams {
			s.binders.Add(tp.Var)
		}
	}
	return soltype.EnterResult{}
}

func (s *weakVarScanner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// weakAssignTarget returns the type a reassignment of the weak module-level binding b is
// checked against. Its weak variables stay variables, so the source's type flows into
// them. The exception is the binding var and each variable that only passes the
// initializer's type through to it, which are coalesced to their bounds. `var a = 5` then
// checks `a = "x"` against `number` and rejects it, where `var id = makeId()` checks
// `id = fn (n: number) -> number { … }` against `fn (x: T) -> T` and settles T on
// `number`.
//
// A binding that recovered to the error sentinel holds a MonoScheme, and its type is
// returned as it is.
func (c *checker) weakAssignTarget(b ValueBinding) soltype.Type {
	sc, ok := b.Schemes[0].(*PolyScheme)
	if !ok {
		return schemeType(b.Schemes[0])
	}
	keep := set.NewSet[*soltype.TypeVarType]()
	passThrough := set.NewSet[*soltype.TypeVarType]()
	if root, isVar := sc.Body.(*soltype.TypeVarType); isVar {
		collectPassThrough(root, passThrough)
		for _, v := range scanWeakVars(root, sc.Level) {
			if !passThrough.Contains(v) {
				keep.Add(v)
			}
		}
	}
	return coalesceKeeping(sc.Body, soltype.Positive, keep, nil, nil)
}

// collectPassThrough adds v to out, and every variable that is a lower bound of v, directly
// or through a chain of variable lower bounds.
func collectPassThrough(v *soltype.TypeVarType, out set.Set[*soltype.TypeVarType]) {
	if out.Contains(v) {
		return
	}
	out.Add(v)
	for _, lb := range v.LowerBounds {
		if lv, isVar := lb.(*soltype.TypeVarType); isVar {
			collectPassThrough(lv, out)
		}
	}
}

// inlineWeakVars returns t with each type variable at or below lvl replaced by the
// coalesced bounds its polarity reads, the union of its lower bounds where t produces it
// and the intersection of its upper bounds where t consumes it. Every other node is kept.
//
// A variable a function type binds as its own type parameter stays, since it is not weak.
// `var id = fn <T>(x: T) -> T { … }` keeps its T.
func inlineWeakVars(t soltype.Type, lvl int) soltype.Type {
	return t.Accept(&weakVarInliner{lvl: lvl, binders: funcTypeParamVars(t)}, soltype.Positive)
}

// weakVarInliner is the visitor behind inlineWeakVars. binders holds the type-parameter
// variables of every function type in the walked type.
type weakVarInliner struct {
	lvl     int
	binders set.Set[*soltype.TypeVarType]
}

func (w *weakVarInliner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if v, ok := t.(*soltype.TypeVarType); ok && v.Level <= w.lvl && !w.binders.Contains(v) {
		return soltype.EnterResult{Type: coalesce(v, pol), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (w *weakVarInliner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// mixesOwnership reports whether the values that flow into v are some owned and some
// borrowed, by the rule checkUniformOwnership applies to the branches of a join.
func mixesOwnership(v *soltype.TypeVarType) bool {
	sawOwned, sawBorrowed := false, false
	collectBranchOwnership(coalesce(v, soltype.Positive), &sawOwned, &sawBorrowed)
	return sawOwned && sawBorrowed
}

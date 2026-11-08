package solver

import (
	"math"
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
// at, so no later component quantifies it. A later binding's variable that a bound ties to a
// weak variable is lowered to the weak variable's level when that binding generalizes, as
// lowerWeakBoundVars describes, so it is weak too.

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
	// reassignable marks a binding declared with `var`, which can be rebound to any value
	// of its type.
	reassignable bool
	// mutable marks a binding written `mut`, which can write the owned layers of what it
	// holds.
	mutable bool
}

// newWeakBinding returns the weakBinding for a binding declared by d and bound to scheme,
// or false when scheme quantifies over nothing settleWeakDisplay can reseal, as the
// MonoScheme of a binding that recovered to the error sentinel does.
func newWeakBinding(d *ast.VarDecl, scheme TypeScheme, infoNode ast.Node) (weakBinding, bool) {
	sc, ok := scheme.(*PolyScheme)
	if !ok {
		return weakBinding{}, false
	}
	return weakBinding{
		scheme:       sc,
		infoNode:     infoNode,
		reassignable: d.Kind == ast.VarKind,
		mutable:      hasMutLeaf(d.Pattern),
	}, true
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
	settler := newWeakSettler()
	for _, r := range c.weakReaders {
		r.scheme.coalesced = c.subsumeFinal(settler.display(r.scheme, lvl))
		if r.infoNode != nil {
			c.recordType(r.infoNode, r.scheme.coalesced)
		}
	}
	for _, w := range c.weakBindings {
		c.settleWeakDisplay(w, settler)
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
	c.settleWeakLocals(settler)
}

// settleWeakLocals settles each body-level weak binding noteWeakLocal recorded through
// settler, as settleWeakDisplay does, and clears the list. It runs once the whole module or
// script is walked. A local's type can read a variable an enclosing scope settles, such as a
// weak module-level binding's, so the body that declares the local ending is not enough.
func (c *checker) settleWeakLocals(settler *weakSettler) {
	for _, w := range c.weakLocals {
		c.settleWeakDisplay(w, settler)
	}
	c.weakLocals = nil
}

// settleWeakDisplay seals w's display type from the bounds its weak variables hold now and
// records it on w's node. generalize sealed the display while the weak variables had only
// the initializer's bounds, so it showed none of the writes that followed.
//
// Each weak variable reads as the one type settler gives it, so `fn (x) { return x }`
// called with `1` settles to `fn (x: 1) -> 1`.
//
// The settled type widens the literals in each slot the binding can write, the way T9
// widens a literal initializer. widenLiterals says which slots those are.
// `val mut seen = Map()` written with `seen.set("k", 1)` settles to `Map<string, number>`,
// `val b = Box(5)` keeps `Box<5>`, and `val mut p = fetchNum()` keeps `Promise<5>`.
func (c *checker) settleWeakDisplay(w weakBinding, settler *weakSettler) {
	sc := w.scheme
	display := settler.display(sc, sc.Level)
	if w.reassignable || w.mutable {
		display = c.widenLiterals(display, w.reassignable, w.mutable)
	}
	sc.coalesced = c.subsumeFinal(display)
	if w.infoNode != nil {
		c.recordType(w.infoNode, sc.coalesced)
	}
}

// widenLiterals returns t with each literal in a writable slot widened to its primitive, as
// widen does for one. A slot is writable when a write can replace the value in it:
//
//   - With replace set, t itself is such a slot, as the value of a `var` binding is.
//   - With mutable set, t's owned layers are writable. Each field that is not `readonly`, each
//     tuple element, and each class argument the class's MutVariance makes invariant is a
//     slot. A class argument it makes covariant, such as the `T` of `Promise<T>`, can only be
//     read, so it is not.
//
// Every covariant position inside a writable slot is writable too, since a write replaces the
// slot's whole value. A `&` borrow in a writable slot is one: a write can store a borrow of any
// value of the widened type, and `&T` is covariant in `T`. A `&` borrow outside a writable slot
// keeps its literals, which belong to the place it points to. A `&mut` borrow is invariant in
// what it points to, so it is kept whole, as a function type is. A class argument the class
// makes contravariant is kept, since widening it would change what the value accepts.
func (c *checker) widenLiterals(t soltype.Type, replace, mutable bool) soltype.Type {
	return t.Accept(&literalWidener{ctx: c.ctx, replace: replace, mutable: mutable}, soltype.Positive)
}

// literalWidener is the visitor behind widenLiterals. replace and mutable describe the node
// being walked, the way widenLiterals' parameters describe t.
type literalWidener struct {
	ctx     *Context
	replace bool
	mutable bool
}

func (w *literalWidener) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.FuncType:
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.RefType:
		return soltype.EnterResult{Type: w.widenRef(t), SkipChildren: true}
	case *soltype.LitType, *soltype.UniqueSymbolType, *soltype.RegexType:
		if w.replace {
			return soltype.EnterResult{Type: widen(t), SkipChildren: true}
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ObjectType:
		return soltype.EnterResult{Type: w.widenFields(t), SkipChildren: true}
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = w.widenSlot(elem, w.mutable, w.mutable)
		}
		out := *t
		out.Elems = elems
		return soltype.EnterResult{Type: &out, SkipChildren: true}
	case *soltype.ClassType:
		return soltype.EnterResult{Type: w.widenClassArgs(t), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

// widenRef returns ref with the literals its referent holds widened when ref is a `&` borrow in
// a writable slot, and ref unchanged otherwise.
func (w *literalWidener) widenRef(ref *soltype.RefType) soltype.Type {
	if !ref.Mut && w.replace {
		widened := ref.Inner.Accept(&literalWidener{ctx: w.ctx, replace: true}, soltype.Positive)
		if inner, ok := widened.(soltype.RefInner); ok {
			return soltype.NewRef(false, ref.Lt, inner)
		}
	}
	return ref
}

// widenSlot returns t widened as a slot that a write can replace when writable is set and
// whose owned layers are writable when mutable is set.
func (w *literalWidener) widenSlot(t soltype.Type, writable, mutable bool) soltype.Type {
	inner := &literalWidener{ctx: w.ctx, replace: w.replace || writable, mutable: mutable}
	return t.Accept(inner, soltype.Positive)
}

// widenFields returns obj with each property's type widened. A property that is not
// `readonly` is a slot a mutable owner can write. Every other member is kept.
func (w *literalWidener) widenFields(obj *soltype.ObjectType) soltype.Type {
	elems := make([]soltype.ObjTypeElem, len(obj.Elems))
	for i, elem := range obj.Elems {
		prop, ok := elem.(*soltype.PropertyElem)
		if !ok {
			elems[i] = elem
			continue
		}
		cp := *prop
		cp.Type = w.widenSlot(prop.Type, w.mutable && !prop.Readonly, w.mutable)
		elems[i] = &cp
	}
	out := *obj
	out.Elems = elems
	return &out
}

// widenClassArgs returns ct with each type argument widened. An argument the class's
// variance makes contravariant is kept. One that MutVariance makes invariant is a slot a
// mutable owner can write. Any other argument can only be read, so its owned layers are not
// writable through it.
func (w *literalWidener) widenClassArgs(ct *soltype.ClassType) soltype.Type {
	def, _ := w.ctx.classDef(ct.Name)
	args := make([]soltype.Type, len(ct.TypeArgs))
	for i, arg := range ct.TypeArgs {
		switch {
		case def.varianceAt(i, w.mutable) == Contravariant:
			args[i] = arg
		case w.mutable && def.varianceAt(i, true) == Invariant:
			args[i] = w.widenSlot(arg, true, true)
		default:
			args[i] = w.widenSlot(arg, false, false)
		}
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

// weakAssignTarget returns the type a reassignment of the weak binding b is checked
// against. Its weak variables stay variables, so the source's type flows into
// them. The exception is the binding var and each variable that only passes the
// initializer's type through to it, which are coalesced to their bounds. `var a = 5` then
// checks `a = "x"` against `number` and rejects it, where `var id = makeId()` checks
// `id = fn (n: number) -> number { … }` against `fn (x: T) -> T` and settles T on
// `number`.
//
// When b.InitVars is set, only the weak variables in that range stay variables, and every
// other one is coalesced to its bounds.
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
			if !passThrough.Contains(v) && (b.InitVars == nil || b.InitVars.contains(v)) {
				keep.Add(v)
			}
		}
	}
	return coalesceKeeping(sc.Body, soltype.Positive, keep, nil, nil)
}

// varIDRange is the half-open range of variable IDs [lo, hi).
type varIDRange struct {
	lo, hi int
}

func (r *varIDRange) contains(v *soltype.TypeVarType) bool {
	return v.ID >= r.lo && v.ID < r.hi
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

// weakSettler gives each weak variable the one type it stands for, the same in every
// position. A weak variable is not a type parameter of anything that reads it, so a weak
// binding and every function that reads it show it as that one type. One weakSettler serves
// a whole module or script, so a weak variable is settled once and each display that reaches
// it reuses the result.
//
// A weak variable's type is the union of the values that flowed into it. When no value
// flowed into it, the type is the intersection of what its uses require, and it is `never`
// when it has neither. Each bound is coalesced with the weak variables it holds kept as
// variables, and each of those is then settled the same way. Given
// `declare fn mk<T>() -> fn (x: T) -> T` and `val g = mk()`, g's variable has the lower
// bound `fn (x: T) -> T`, and a call `g(5)` gives T the lower bound 5. g and a function that
// returns g both settle to `fn (x: 5) -> 5`.
//
// A weak variable that reaches itself through its bounds settles to a μ type in its smallest
// form. Given `fn f() { return {next: f()} }` and `fn id(x) { return x }`, the weak binding
// `val d = id(f())` reads as `μX0.{next: X0}`.
type weakSettler struct {
	// settled holds each weak variable's settled type. It is keyed by the variable alone,
	// though each call names the level at or below which a variable is weak. A variable's
	// bounds hold only variables at or below its own level, apart from a function's own
	// type parameters, which binders excludes. So every variable a settle reaches is weak
	// at any level the variable itself is weak at, and the settled type is the same.
	settled map[*soltype.TypeVarType]soltype.Type
	// binders holds the type parameters of each function type a display or a bound reaches.
	// A function binds them, so they stay variables wherever they occur. A type parameter
	// can also occur in a bound a settle reads apart from its function type, as the
	// parameter of `fn <T>(x: T) -> T` stored in a weak map does.
	binders set.Set[*soltype.TypeVarType]
	// open holds each variable whose settled type is being built, with its depth on the walk.
	open map[*soltype.TypeVarType]*openWeakVar
	// depth is the number of open variables.
	depth int
	// minRef is the lowest depth of an open variable the type being built refers back to.
	// A type that refers to an enclosing open variable is not complete on its own, so it is
	// not saved in settled.
	minRef int
	// muCount numbers the μ-variables minted since the walk last had no open variable.
	muCount int
	// empty holds each weak variable that settled on no type, since its bounds hold none.
	empty set.Set[*soltype.TypeVarType]
}

// openWeakVar is a weak variable whose settled type is being built.
type openWeakVar struct {
	depth int
	// ref is the μ-variable a reference back to the variable renders as. It is nil until a
	// bound reaches the variable again.
	ref *soltype.RecursiveVarType
}

func newWeakSettler() *weakSettler {
	return &weakSettler{
		settled: map[*soltype.TypeVarType]soltype.Type{},
		open:    map[*soltype.TypeVarType]*openWeakVar{},
		minRef:  math.MaxInt,
		empty:   set.NewSet[*soltype.TypeVarType](),
		binders: set.NewSet[*soltype.TypeVarType](),
	}
}

// display returns sc's body coalesced at sc's own level, with each weak variable replaced by
// its settled type. A weak variable is a variable at or below lvl that is not a function's own
// type parameter.
func (s *weakSettler) display(sc *PolyScheme, lvl int) soltype.Type {
	// A class value's type names the class's own parameters, which coalescing shows with the
	// bounds the class wrote. A weak variable in that place stands for its settled type
	// instead, so it is not treated as one of them.
	declared := slices.DeleteFunc(slices.Clone(sc.declared), func(tp *soltype.TypeParam) bool {
		return tp.Var.Level <= lvl
	})
	s.addBinders(funcTypeParamVars(sc.Body))
	keep := schemeBinders(sc.Body, declared)
	addWeakVars(keep, sc.Body, lvl)
	coalesced := coalesceSchemeKeeping(sc.Body, sc.Level, declared, sc.declaredLts, keep, true)
	return coalesceLifetimes(s.replace(coalesced, lvl), soltype.Positive, classKeepLifetimes(sc.declaredLts))
}

// addBinders adds each variable in vars to s.binders.
func (s *weakSettler) addBinders(vars set.Set[*soltype.TypeVarType]) {
	for v := range vars {
		s.binders.Add(v)
	}
}

// addWeakVars adds to keep each variable at or below lvl that t reaches.
func addWeakVars(keep set.Set[*soltype.TypeVarType], t soltype.Type, lvl int) {
	vars := map[int]*soltype.TypeVarType{}
	t.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	for _, v := range vars {
		if v.Level <= lvl {
			keep.Add(v)
		}
	}
}

// replace returns t with each weak variable replaced by its settled type. A function's own
// type parameters stay variables, since the function binds them.
func (s *weakSettler) replace(t soltype.Type, lvl int) soltype.Type {
	s.addBinders(funcTypeParamVars(t))
	return t.Accept(&weakVarReplacer{s: s, lvl: lvl}, soltype.Positive)
}

// settle returns the type the weak variable v stands for, as weakSettler describes.
func (s *weakSettler) settle(v *soltype.TypeVarType, lvl int) soltype.Type {
	if t, ok := s.settled[v]; ok {
		return t
	}
	if o, ok := s.open[v]; ok {
		if o.ref == nil {
			o.ref = &soltype.RecursiveVarType{ID: s.muCount, Name: muBinderName(s.muCount)}
			s.muCount++
		}
		s.minRef = min(s.minRef, o.depth)
		return o.ref
	}
	o := &openWeakVar{depth: s.depth}
	s.open[v] = o
	s.depth++
	outerRef := s.minRef
	s.minRef = math.MaxInt

	body := s.settleAt(v, soltype.Positive, lvl)
	if isEmptyAt(body, soltype.Positive) {
		body = s.settleAt(v, soltype.Negative, lvl)
		if isEmptyAt(body, soltype.Negative) {
			body = &soltype.NeverType{}
			s.empty.Add(v)
		}
	}
	if o.ref != nil {
		body = (&muBinder{pol: soltype.Positive, v: o.ref}).tie(soltype.Positive, body)
	} else {
		body = foldUnrolledKnot(body)
	}

	delete(s.open, v)
	s.depth--
	innerRef := s.minRef
	if innerRef >= o.depth {
		s.settled[v] = body
		innerRef = math.MaxInt
	}
	s.minRef = min(outerRef, innerRef)
	if s.depth == 0 {
		s.muCount = 0
	}
	return body
}

// settleAt returns v coalesced at pol, with each other weak variable its bounds reach
// settled. It is `never` at a positive position when v has no lower bound, and `unknown` at
// a negative one when v has no upper bound.
//
// Inside an upper bound, a weak variable that settled on no type reads as the identity of
// its position, `never` where it is produced and `unknown` where it is consumed. Such a
// variable places no requirement on v, so `fn (x: R) -> R` with R unsettled reads as
// `fn (x: never) -> unknown`, the function type every function is a subtype of.
func (s *weakSettler) settleAt(v *soltype.TypeVarType, pol soltype.Polarity, lvl int) soltype.Type {
	keep := funcTypeParamVars(v)
	s.addBinders(keep)
	addWeakVars(keep, v, lvl)
	keep.Remove(v)
	coalesced := coalesceStructure(v, pol, keep, nil)
	s.addBinders(funcTypeParamVars(coalesced))
	return coalesced.Accept(&weakVarReplacer{s: s, lvl: lvl, inUpper: pol == soltype.Negative}, pol)
}

// isEmptyAt reports whether t is the identity of a position at pol: `never`, or a union of
// `never`s, at a positive position, and `unknown`, or an intersection of `unknown`s, at a
// negative one.
func isEmptyAt(t soltype.Type, pol soltype.Polarity) bool {
	switch t := t.(type) {
	case *soltype.UnionType:
		return pol == soltype.Positive && !slices.ContainsFunc(t.Types, func(m soltype.Type) bool { return !isEmptyAt(m, pol) })
	case *soltype.IntersectionType:
		return pol == soltype.Negative && !slices.ContainsFunc(t.Types, func(m soltype.Type) bool { return !isEmptyAt(m, pol) })
	}
	if pol == soltype.Positive {
		return isNeverType(t)
	}
	return isUnknown(t)
}

// foldUnrolledKnot returns the μ type t is one unfolding of, or t when it is none.
// `{next: μX0.{next: X0}}` folds to `μX0.{next: X0}`.
func foldUnrolledKnot(t soltype.Type) soltype.Type {
	if _, isKnot := t.(*soltype.RecursiveType); isKnot {
		return t
	}
	finder := &outerKnotCollector{}
	t.Accept(finder, soltype.Positive)
	for _, knot := range finder.knots {
		if equalType(unfoldRecursive(knot), t) {
			return knot
		}
	}
	return t
}

// outerKnotCollector collects the outermost μ types a type holds.
type outerKnotCollector struct {
	knots []*soltype.RecursiveType
}

func (f *outerKnotCollector) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if knot, ok := t.(*soltype.RecursiveType); ok {
		f.knots = append(f.knots, knot)
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *outerKnotCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// weakVarReplacer is the visitor behind weakSettler.replace and weakSettler.settleAt. It
// replaces each weak variable with its settled type. A variable in the settler's binders is
// not weak. With inUpper set, a weak variable that settled on no type reads as the identity of
// its position, as settleAt describes.
type weakVarReplacer struct {
	s       *weakSettler
	lvl     int
	inUpper bool
}

func (w *weakVarReplacer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok || v.Level > w.lvl || w.s.binders.Contains(v) {
		return soltype.EnterResult{}
	}
	settled := w.s.settle(v, w.lvl)
	if w.inUpper && w.s.empty.Contains(v) {
		settled = emptyOf(pol)
	}
	return soltype.EnterResult{Type: settled, SkipChildren: true}
}

// ExitType drops the identity members a replacement leaves in a union or intersection, so
// `{set: fn () -> number} & unknown` reads as `{set: fn () -> number}`.
func (w *weakVarReplacer) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	switch t := t.(type) {
	case *soltype.UnionType:
		return withoutMembers(t.Types, isNeverType, func(ts []soltype.Type) soltype.Type { return &soltype.UnionType{Types: ts} }, &soltype.NeverType{}, t)
	case *soltype.IntersectionType:
		return withoutMembers(t.Types, isUnknown, func(ts []soltype.Type) soltype.Type { return &soltype.IntersectionType{Types: ts} }, &soltype.UnknownType{}, t)
	}
	return t
}

// withoutMembers returns the type members makes from ts with each member drop reports
// removed. It returns orig when nothing is removed, the one member left when one is, and
// empty when none is.
func withoutMembers(
	ts []soltype.Type,
	drop func(soltype.Type) bool,
	members func([]soltype.Type) soltype.Type,
	empty soltype.Type,
	orig soltype.Type,
) soltype.Type {
	kept := slices.DeleteFunc(slices.Clone(ts), drop)
	switch len(kept) {
	case len(ts):
		return orig
	case 0:
		return empty
	case 1:
		return kept[0]
	}
	return members(kept)
}

// mixesOwnership reports whether the values that flow into v are some owned and some
// borrowed, by the rule checkUniformOwnership applies to the branches of a join.
func mixesOwnership(v *soltype.TypeVarType) bool {
	sawOwned, sawBorrowed := false, false
	collectBranchOwnership(coalesce(v, soltype.Positive), &sawOwned, &sawBorrowed)
	return sawOwned && sawBorrowed
}

// isUnknown reports whether t is `unknown`, the top of the subtype lattice.
func isUnknown(t soltype.Type) bool {
	_, top := t.(*soltype.UnknownType)
	return top
}

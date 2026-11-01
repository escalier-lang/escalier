package solver

import (
	"maps"
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
// Only a weak binding of the module mints a variable at lvl that a binding can read, so
// the walk is skipped until the module has one. It reads weakBindingVars, which holds the
// binding var of a weak binding later in b's own component before noteWeakBinding records
// that binding.
func (c *checker) noteWeakReader(b *componentBinding, scheme TypeScheme, lvl int) {
	sc, ok := scheme.(*PolyScheme)
	if !ok || c.weakBindingVars.Len() == 0 {
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
		// A weak variable is not one of the reader's type parameters, so it shows the type
		// the bounds it holds now give it.
		r.scheme.coalesced = c.subsumeFinal(simplifyWeakReader(r.scheme, lvl))
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
// is not a type parameter of the binding, so it is replaced by the one type it stands for
// in both, as simplifyWithWeakVars describes. `fn (x) { return x }` called with `1`
// settles to `fn (x: 1) -> 1`.
//
// The settled type widens the literals in each slot the binding can write, the way T9
// widens a literal initializer. widenLiterals says which slots those are.
// `val mut seen = Map()` written with `seen.set("k", 1)` settles to `Map<string, number>`,
// `val b = Box(5)` keeps `Box<5>`, and `val mut p = fetchNum()` keeps `Promise<5>`.
func (c *checker) settleWeakDisplay(w weakBinding) {
	sc := w.scheme
	display := simplifyWithWeakVars(sc, sc.Level-1, sc.Level)
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

// simplifyWithWeakVars returns sc's body coalesced above lvl, with each type variable at or
// below weakLvl replaced by the one type it stands for, the same in every position.
//
// Coalescing rebuilds such a variable as a fresh one with no bounds and puts the types that
// flowed into it beside it, so `fn (x: T) -> T` with `T` written 1 coalesces to
// `fn (x: t) -> t | 1`. The variable's type is read from there. It is the union of the types
// beside it where it is produced, or the intersection of those beside it where it is
// consumed when it is never produced. A variable with neither reads its own bounds the same
// way, and is `never` when it has no bound either.
func simplifyWithWeakVars(sc *PolyScheme, lvl, weakLvl int) soltype.Type {
	simplified := coalesceScheme(sc.Body, lvl, sc.declared, sc.declaredLts, true)
	s := &weakCompanionScanner{
		lvl:      weakLvl,
		binders:  funcTypeParamVars(simplified),
		produced: map[*soltype.TypeVarType][]soltype.Type{},
		consumed: map[*soltype.TypeVarType][]soltype.Type{},
	}
	simplified.Accept(s, soltype.Positive)
	return simplified.Accept(&weakVarInliner{lvl: weakLvl, binders: s.binders, scan: s}, soltype.Positive)
}

// weakCompanionScanner records, for each type variable at or below lvl, the other members of
// each union it is produced in and of each intersection it is consumed in.
type weakCompanionScanner struct {
	lvl      int
	binders  set.Set[*soltype.TypeVarType]
	produced map[*soltype.TypeVarType][]soltype.Type
	consumed map[*soltype.TypeVarType][]soltype.Type
}

func (s *weakCompanionScanner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	var members []soltype.Type
	into := s.produced
	switch t := t.(type) {
	case *soltype.UnionType:
		if pol != soltype.Positive {
			return soltype.EnterResult{}
		}
		members = t.Types
	case *soltype.IntersectionType:
		if pol != soltype.Negative {
			return soltype.EnterResult{}
		}
		members, into = t.Types, s.consumed
	default:
		return soltype.EnterResult{}
	}
	for i, m := range members {
		v, ok := m.(*soltype.TypeVarType)
		if !ok || !s.isWeak(v) {
			continue
		}
		for j, other := range members {
			if j != i {
				into[v] = append(into[v], other)
			}
		}
	}
	return soltype.EnterResult{}
}

func (s *weakCompanionScanner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

func (s *weakCompanionScanner) isWeak(v *soltype.TypeVarType) bool {
	return v.Level <= s.lvl && !s.binders.Contains(v)
}

// typeOf returns the one type the weak variable v stands for, as simplifyWithWeakVars
// describes.
func (s *weakCompanionScanner) typeOf(v *soltype.TypeVarType) soltype.Type {
	if ts := s.produced[v]; len(ts) > 0 {
		return joinAll(ts)
	}
	if ts := s.consumed[v]; len(ts) > 0 {
		if len(ts) == 1 {
			return ts[0]
		}
		return &soltype.IntersectionType{Types: ts}
	}
	return boundsType(v, soltype.Positive)
}

// boundsType returns the type v's bounds give it at polarity pol. At a positive position
// that is the union of v's lower bounds, and at a negative one the intersection of its
// upper bounds. When the bounds pol reads are empty, it is the type the other bounds give,
// and with no bound of either kind it is `never`.
func boundsType(v *soltype.TypeVarType, pol soltype.Polarity) soltype.Type {
	side, ok := boundsSide(v, pol)
	if !ok {
		return &soltype.NeverType{}
	}
	return coalesce(v, side)
}

// boundsSide returns the polarity of the bounds boundsType reads for v at pol: positive
// for v's lower bounds and negative for its upper bounds. It returns false when v has no
// bound of either kind.
func boundsSide(v *soltype.TypeVarType, pol soltype.Polarity) (soltype.Polarity, bool) {
	lower := coalesce(v, soltype.Positive)
	upper := coalesce(v, soltype.Negative)
	switch {
	case pol == soltype.Negative && !isUnknown(upper):
		return soltype.Negative, true
	case !isNeverType(lower):
		return soltype.Positive, true
	case !isUnknown(upper):
		return soltype.Negative, true
	}
	return soltype.Positive, false
}

// joinAll returns the union of ts, or its one member.
func joinAll(ts []soltype.Type) soltype.Type {
	if len(ts) == 1 {
		return ts[0]
	}
	return &soltype.UnionType{Types: ts}
}

// weakVarInliner is the visitor behind simplifyWithWeakVars. It replaces each weak variable
// with the type scan gives it.
type weakVarInliner struct {
	lvl     int
	binders set.Set[*soltype.TypeVarType]
	scan    *weakCompanionScanner
}

func (w *weakVarInliner) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if v, ok := t.(*soltype.TypeVarType); ok && w.scan.isWeak(v) {
		return soltype.EnterResult{Type: w.scan.typeOf(v), SkipChildren: true}
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

// isUnknown reports whether t is `unknown`, the top of the subtype lattice.
func isUnknown(t soltype.Type) bool {
	_, top := t.(*soltype.UnknownType)
	return top
}

// lowerWeakInputs lowers to lvl each type variable above lvl that t reaches and whose
// values flow into a variable at or below lvl, so generalizing t at lvl does not quantify
// it. A variable's values flow into u when u is an upper bound of it, directly or through a
// chain of variables that are each an upper bound of the one before. A variable a
// function type quantifies itself and a rigid type parameter are left alone, and neither
// starts a chain.
func (c *checker) lowerWeakInputs(t soltype.Type, lvl int) {
	vars := map[int]*soltype.TypeVarType{}
	t.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	binders := funcTypeParamVars(t)
	isBinder := func(v *soltype.TypeVarType) bool { return binders.Contains(v) || v.Rigid != nil }
	m := buildMirror(vars)
	// The walk starts from the variables in ID order, so the extrusions lowerToLevel
	// appends land in the same order on every run.
	var work []*soltype.TypeVarType
	for _, id := range slices.Sorted(maps.Keys(vars)) {
		if v := vars[id]; v.Level <= lvl && !isBinder(v) {
			work = append(work, v)
		}
	}
	// Walk from each variable at or below lvl down its chains of variable lower bounds. A
	// variable reached above lvl feeds one at or below it. Lowering it puts it at lvl, and
	// the walk continues from it.
	reached := set.NewSet[*soltype.TypeVarType]()
	for len(work) > 0 {
		u := work[len(work)-1]
		work = work[:len(work)-1]
		if reached.Contains(u) {
			continue
		}
		reached.Add(u)
		for _, lb := range m.effectiveBounds(u, soltype.Positive) {
			lv, isVar := lb.(*soltype.TypeVarType)
			if !isVar || isBinder(lv) {
				continue
			}
			if lv.Level > lvl {
				c.lowerToLevel(lv, lvl)
			}
			work = append(work, lv)
		}
	}
}

// lowerToLevel moves v, a variable above lvl, to lvl. Each of v's bounds that holds a type
// or lifetime variable above lvl is replaced by its extrusion to lvl. An extrusion is a copy
// whose inner variables are fresh variables at lvl, each linked to the variable it copies
// in the direction the bound's polarity reads. The variables v's bounds held keep their
// levels, so lowering v does not stop the scheme quantifying them.
func (c *checker) lowerToLevel(v *soltype.TypeVarType, lvl int) {
	c.ctx.checkThawed(v, "lowerToLevel")
	v.Level = lvl
	cache := map[extrudeKey]*soltype.TypeVarType{}
	extrudeBounds := func(bounds []soltype.Type, pol soltype.Polarity) []soltype.Type {
		out := slices.Clone(bounds)
		for i, b := range out {
			if soltype.LevelOf(b) > lvl {
				out[i] = c.ctx.extrude(b, pol, lvl, cache)
			}
		}
		return out
	}
	v.LowerBounds = extrudeBounds(v.LowerBounds, soltype.Positive)
	v.UpperBounds = extrudeBounds(v.UpperBounds, soltype.Negative)
}

// simplifyWeakReader returns sc's body coalesced at sc's level, with each type variable at
// or below weakLvl replaced by the type boundsType gives it at the polarity it occurs at. A
// variable at or below weakLvl inside the bounds a replacement reads is replaced the same
// way.
//
// Coalescing reads a variable through the classes of variables that always occur
// together, and drops a quantified variable that occurs at one polarity in favor of its
// bounds. A variable at or below weakLvl is held out of both and replaced afterwards.
// In
//
//	val mut seen = Map()
//	fn remember(value) { seen.set("last", value) }
//	fn go() {
//	    val opts = {retries: 3}
//	    remember(&opts)
//	}
//
// `value`'s only upper bound is `seen`'s value type, which has no upper bound of its own,
// so `remember` renders `fn (value: &'static {retries: 3}) -> undefined` from the borrow
// `go` passes.
func simplifyWeakReader(sc *PolyScheme, weakLvl int) soltype.Type {
	vars := map[int]*soltype.TypeVarType{}
	sc.Body.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	keep := schemeKeepSet(sc.Body, sc.declared)
	weak := map[int]*soltype.TypeVarType{}
	for id, v := range vars {
		if v.Level <= weakLvl && !keep.Contains(v) {
			weak[id] = v
		}
	}
	for _, v := range weak {
		keep.Add(v)
	}
	simplified := coalesceSchemeKeeping(sc.Body, sc.Level, sc.declared, sc.declaredLts, keep, true)
	inliner := &weakReaderInliner{
		weak: weak,
		held: set.FromSlice(slices.Collect(maps.Values(weak))),
		open: set.NewSet[*soltype.TypeVarType](),
	}
	return simplified.Accept(inliner, soltype.Positive)
}

// weakReaderInliner is the visitor behind simplifyWeakReader. weak maps the ID of each
// variable it replaces to the variable, since coalescing can hand back a copy of a kept
// variable in its place.
type weakReaderInliner struct {
	weak map[int]*soltype.TypeVarType
	// held holds the variables weak maps to.
	held set.Set[*soltype.TypeVarType]
	// open holds each variable whose replacement is being built, so a bound that reaches
	// one of them again is coalesced instead of replaced a second time.
	open set.Set[*soltype.TypeVarType]
}

func (w *weakReaderInliner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.UnionType:
		if pol == soltype.Positive {
			return w.enterMembers(t.Types, pol)
		}
	case *soltype.IntersectionType:
		if pol == soltype.Negative {
			return w.enterMembers(t.Types, pol)
		}
	case *soltype.TypeVarType:
		if orig, isWeak := w.weak[t.ID]; isWeak {
			return soltype.EnterResult{Type: w.replacement(orig, pol), SkipChildren: true}
		}
	}
	return soltype.EnterResult{}
}

// enterMembers rewrites the members of a union at a positive position or of an
// intersection at a negative one. A weak variable with no bound at pol adds nothing to such
// a node, so it is dropped when another member is left to give the position a type. Only a
// weak variable that is the node's sole content reads its other bounds, as boundsType does.
// In
//
//	val mut seen = Map()
//	fn remember(mut value) {
//	    value.retries = 0
//	    seen.set("last", value)
//	}
//
// `value`'s upper bounds are `{retries: number}` and `seen`'s value type, which has no
// upper bound. `value` reads `{retries: number}`, not its meet with the values passed to
// `remember`.
func (w *weakReaderInliner) enterMembers(members []soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	rest := make([]soltype.Type, 0, len(members))
	for _, m := range members {
		if v, ok := m.(*soltype.TypeVarType); ok {
			if orig, isWeak := w.weak[v.ID]; isWeak {
				if side, bounded := boundsSide(orig, pol); !bounded || side != pol {
					continue
				}
			}
		}
		rest = append(rest, m)
	}
	if len(rest) == 0 || len(rest) == len(members) {
		return soltype.EnterResult{}
	}
	var joined soltype.Type
	if pol == soltype.Positive {
		joined = newUnion(nil, rest)
	} else {
		joined = newIntersection(nil, rest)
	}
	return soltype.EnterResult{Type: joined.Accept(w, pol), SkipChildren: true}
}

// replacement returns the type boundsType gives the weak variable v at pol, with each other
// weak variable in the bounds it reads replaced by its own replacement. In
//
//	fn pick(f) { return fn (x) { return x } }
//	fn h(y) { return g(y) }
//	val g = pick(h)
//	val n = h(5)
//	val s = h("a")
//
// h's binding var is weak, since h flows into pick's `f`. Its replacement is h's function
// type, whose `y` is weak too. `y`'s upper bounds reach no type, so `y` reads its lower
// bounds, the arguments h is called with, and h renders `fn (y: 5 | "a") -> 5 | "a"`.
// Coalescing h's function type alone would read `y` by its upper bounds and show
// `unknown`.
func (w *weakReaderInliner) replacement(v *soltype.TypeVarType, pol soltype.Polarity) soltype.Type {
	side, ok := boundsSide(v, pol)
	if !ok {
		return &soltype.NeverType{}
	}
	if w.open.Contains(v) {
		return coalesce(v, side)
	}
	w.open.Add(v)
	defer w.open.Remove(v)
	// coalesce keeps the type parameters of each function type it reaches, so they are
	// kept here too.
	held := w.held.Union(funcTypeParamVars(v))
	held.Remove(v)
	return coalesceKeeping(v, side, held, nil, nil).Accept(w, side)
}

func (w *weakReaderInliner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

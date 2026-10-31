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
}

// newWeakBinding returns the weakBinding for a binding bound to scheme, or false when
// scheme quantifies over nothing settleWeakDisplay can reseal, as the MonoScheme of a
// binding that recovered to the error sentinel does.
func newWeakBinding(scheme TypeScheme, infoNode ast.Node) (weakBinding, bool) {
	sc, ok := scheme.(*PolyScheme)
	if !ok {
		return weakBinding{}, false
	}
	return weakBinding{scheme: sc, infoNode: infoNode}, true
}

// noteWeakBinding records the weak module-level binding b for settleWeakBindings and
// widens its writable slots, as widenWeakSlots does. scheme is the scheme b was bound to.
func (c *checker) noteWeakBinding(b *componentBinding, scheme TypeScheme) {
	vd, ok := b.primary.(*ast.VarDecl)
	if !ok {
		return
	}
	if w, ok := newWeakBinding(scheme, b.infoNode); ok {
		// A leaf of a destructuring is `mut` on its own, so b.mut is read in place of the
		// pattern's. The binding var is minted before the initializer is inferred, so it is
		// owned apart from the initializer's range.
		owns := func(v *soltype.TypeVarType) bool { return v == b.v || b.initVars.contains(v) }
		c.widenWeakSlots(w.scheme, vd, b.mut, owns)
		c.weakBindings = append(c.weakBindings, w)
	}
}

// noteWeakLocal records the weak body-level binding d, bound to scheme, for
// settleWeakLocals and widens its writable slots, as widenWeakSlots does. initVars is the
// range of ids of the variables d's initializer and binding var minted.
func (c *checker) noteWeakLocal(d *ast.VarDecl, scheme TypeScheme, initVars varRange) {
	if w, ok := newWeakBinding(scheme, d.Pattern); ok {
		c.widenWeakSlots(w.scheme, d, hasMutLeaf(d.Pattern), initVars.contains)
		c.weakLocals = append(c.weakLocals, w)
	}
}

// widenWeakSlots makes each value that reaches a writable slot of the weak binding d
// declares widen as widenLiterals widens that slot, so a read of the binding sees the type
// it settles to. sc is the binding's scheme, mutable marks a binding written `mut`, and owns
// reports whether a type variable belongs to the binding alone.
//
// A variable the binding owns stands for its slot. It is given the slot's mode in
// slotModes, so every lower bound it gains later is widened by widenSlotBound. Each lower
// bound it already holds is replaced by its widened form and checked again against the
// variable's upper bounds. A failed check is reported against d's initializer.
//
// Any other variable in a slot can belong to another binding too. The slot holds a fresh
// variable in its place that only receives that variable's values, as widenSlotBound
// describes.
func (c *checker) widenWeakSlots(sc *PolyScheme, d *ast.VarDecl, mutable bool, owns func(*soltype.TypeVarType) bool) {
	mode := slotMode{replace: d.Kind == ast.VarKind, mutable: mutable}
	if !mode.writable() {
		return
	}
	m := &slotMarker{c: c, owns: owns, site: d}
	if d.Init != nil {
		m.site = d.Init
	}
	// A type that is not a variable has no lower bound to widen, so the scheme takes the
	// widened type in its place. No use has read the scheme yet.
	if body, changed := m.widen(sc.Body, mode); changed {
		sc.Body = body
		sc.coalesced = nil
		sc.coalesced = c.subsumeFinal(sc.display())
	}
}

// varRange is a half-open range of type variable ids, the ids the variables one stretch of
// inference minted.
type varRange struct{ from, to int }

// contains reports whether v's id is in r.
func (r varRange) contains(v *soltype.TypeVarType) bool { return r.from <= v.ID && v.ID < r.to }

// slotMarker is the walk widenWeakSlots runs over a weak binding's type.
type slotMarker struct {
	c *checker
	// owns reports whether a variable belongs to the binding alone.
	owns func(*soltype.TypeVarType) bool
	// site is the node a failed check of a widened bound is reported against.
	site ast.Node
}

// widen returns t widened as a slot of mode, and reports whether that changed it.
func (m *slotMarker) widen(t soltype.Type, mode slotMode) (soltype.Type, bool) {
	changed := false
	onVar := func(v *soltype.TypeVarType, vm slotMode) soltype.Type {
		if m.owns(v) {
			m.mark(v, vm)
			return v
		}
		nv := m.c.ctx.newSlotVar(v, vm)
		m.c.constrain(m.site, v, nv)
		return nv
	}
	return m.c.ctx.widenLiterals(t, mode, onVar, &changed), changed
}

// mark gives v the slot mode mode, joined with any mode v already has, and replaces each
// lower bound v holds with its widened form.
func (m *slotMarker) mark(v *soltype.TypeVarType, mode slotMode) {
	old, had := m.c.ctx.slotModes[v]
	if had && old.covers(mode) {
		return
	}
	mode = old.union(mode)
	m.c.ctx.setSlotMode(v, mode)
	for i := range len(v.LowerBounds) {
		if widened, changed := m.widen(v.LowerBounds[i], mode); changed {
			queued := len(m.c.ctx.pendingRigidLts)
			m.c.blameConstraintErrors(m.site, m.c.ctx.replaceLowerBound(v, i, widened))
			m.c.siteRigidLifetimeChecks(m.site, queued)
		}
	}
}

// setSlotMode records mode as the slot mode of v.
func (c *Context) setSlotMode(v *soltype.TypeVarType, mode slotMode) {
	if c.slotModes == nil {
		c.slotModes = map[*soltype.TypeVarType]slotMode{}
	}
	c.slotModes[v] = mode
}

// newSlotVar returns a fresh variable at v's level with the slot mode mode. The caller
// constrains v into it, so it receives v's values widened as a slot of mode widens them.
func (c *Context) newSlotVar(v *soltype.TypeVarType, mode slotMode) *soltype.TypeVarType {
	nv := c.freshVar(v.Level)
	c.setSlotMode(nv, mode)
	return nv
}

// widenSlotBound returns sub widened as a lower bound of a variable whose slot mode is
// mode, along with the errors that constraining the variables it mints reports.
//
// A literal in a writable slot widens as widenLiterals widens it. A variable in a writable
// slot is kept when its own slot mode covers the slot's, since every value that reaches it
// is widened already. Any other variable is replaced by a fresh variable of the slot's mode,
// and the original is constrained into it. The fresh variable then holds the widened form
// of each value the original holds. The original keeps its literals, which every other
// place it reaches still reads.
// With `fn put(x) { seen.set("k", [x]) }`, `put(1)` leaves `x` at `1` and adds `[number]`
// to the values of a weak `val mut seen = Map()`.
func (c *Context) widenSlotBound(sub soltype.Type, mode slotMode, seen *seenPairs) (soltype.Type, []SolverError) {
	var errs []SolverError
	onVar := func(v *soltype.TypeVarType, vm slotMode) soltype.Type {
		if own, ok := c.slotModes[v]; ok && own.covers(vm) {
			return v
		}
		nv := c.newSlotVar(v, vm)
		errs = append(errs, c.constrain(v, nv, seen, false)...)
		return nv
	}
	return c.widenLiterals(sub, mode, onVar, nil), errs
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
		r.scheme.coalesced = c.subsumeFinal(simplifyWithWeakVars(r.scheme, r.scheme.Level, lvl))
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
// Each literal in a slot the binding can write is widened in the bounds already, since
// widenWeakSlots widened the slots when the binding was noted. `val mut seen = Map()` written with
// `seen.set("k", 1)` settles to `Map<string, number>`, `val b = Box(5)` keeps `Box<5>`,
// and `val mut p = fetchNum()` keeps `Promise<5>`.
func (c *checker) settleWeakDisplay(w weakBinding) {
	sc := w.scheme
	sc.coalesced = c.subsumeFinal(simplifyWithWeakVars(sc, sc.Level-1, sc.Level))
	if w.infoNode != nil {
		c.recordType(w.infoNode, sc.coalesced)
	}
}

// widenLiterals returns t with each literal in a writable slot widened to its primitive, as
// widen does for one. mode describes t itself. A slot is writable when a write can replace
// the value in it:
//
//   - With mode.replace set, t itself is such a slot, as the value of a `var` binding is.
//   - With mode.mutable set, t's owned layers are writable. Each field that is not
//     `readonly`, each tuple element, and each class argument the class's MutVariance makes
//     invariant is a slot. A class argument it makes covariant, such as the `T` of
//     `Promise<T>`, can only be read, so it is not.
//
// Every covariant position inside a writable slot is writable too, since a write replaces the
// slot's whole value. A function type and a borrow are kept whole, since the borrowed place
// decides the type behind a borrow. A class argument the class makes contravariant is kept,
// since widening it would change what the value accepts.
//
// onVar returns the type that replaces a type variable in a writable slot of the given
// mode. When `changed` is not nil, it is set once a literal widens or onVar returns a type
// other than its variable.
func (c *Context) widenLiterals(t soltype.Type, mode slotMode, onVar slotVarFunc, changed *bool) soltype.Type {
	return t.Accept(&literalWidener{ctx: c, mode: mode, onVar: onVar, changed: changed}, soltype.Positive)
}

// slotVarFunc is the callback widenLiterals passes each type variable it reaches in a
// writable slot, with that slot's mode.
type slotVarFunc func(v *soltype.TypeVarType, mode slotMode) soltype.Type

// slotMode describes a slot of a weak binding by what a write through the binding can do
// there.
type slotMode struct {
	// replace marks a slot a write can replace whole.
	replace bool
	// mutable marks a slot whose owned layers a write can change.
	mutable bool
}

// writable reports whether a write can change anything in a slot of mode m.
func (m slotMode) writable() bool { return m.replace || m.mutable }

// covers reports whether a slot of mode m widens everything a slot of mode o does.
func (m slotMode) covers(o slotMode) bool {
	return (m.replace || !o.replace) && (m.mutable || !o.mutable)
}

// union returns the mode of a slot that is written both as m and as o.
func (m slotMode) union(o slotMode) slotMode {
	return slotMode{replace: m.replace || o.replace, mutable: m.mutable || o.mutable}
}

// literalWidener is the visitor behind widenLiterals. mode describes the node being walked,
// the way widenLiterals' mode describes t.
type literalWidener struct {
	ctx     *Context
	mode    slotMode
	onVar   slotVarFunc
	changed *bool
}

func (w *literalWidener) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// Nothing below a slot no write reaches is writable either.
	if !w.mode.writable() {
		return soltype.EnterResult{SkipChildren: true}
	}
	switch t := t.(type) {
	case *soltype.FuncType, *soltype.RefType:
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.TypeVarType:
		out := w.onVar(t, w.mode)
		if out != t {
			w.markChanged()
		}
		return soltype.EnterResult{Type: out, SkipChildren: true}
	case *soltype.LitType, *soltype.UniqueSymbolType, *soltype.RegexType:
		if w.mode.replace {
			w.markChanged()
			return soltype.EnterResult{Type: widen(t), SkipChildren: true}
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ObjectType:
		return soltype.EnterResult{Type: w.widenFields(t), SkipChildren: true}
	case *soltype.TupleType:
		return soltype.EnterResult{Type: w.widenElems(t), SkipChildren: true}
	case *soltype.ClassType:
		return soltype.EnterResult{Type: w.widenClassArgs(t), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (w *literalWidener) markChanged() {
	if w.changed != nil {
		*w.changed = true
	}
}

// widenSlot returns t widened as a slot that a write can replace when writable is set and
// whose owned layers are writable when mutable is set.
func (w *literalWidener) widenSlot(t soltype.Type, writable, mutable bool) soltype.Type {
	inner := *w
	inner.mode = slotMode{replace: w.mode.replace || writable, mutable: mutable}
	return t.Accept(&inner, soltype.Positive)
}

// widenElems returns tup with each element widened as a slot a mutable owner can write, or
// tup itself when no element changes.
func (w *literalWidener) widenElems(tup *soltype.TupleType) soltype.Type {
	var elems []soltype.Type
	for i, elem := range tup.Elems {
		widened := w.widenSlot(elem, w.mode.mutable, w.mode.mutable)
		if widened == elem {
			continue
		}
		if elems == nil {
			elems = slices.Clone(tup.Elems)
		}
		elems[i] = widened
	}
	if elems == nil {
		return tup
	}
	out := *tup
	out.Elems = elems
	return &out
}

// widenFields returns obj with each property's type widened, or obj itself when no
// property changes. A property that is not `readonly` is a slot a mutable owner can write.
// Every other member is kept.
func (w *literalWidener) widenFields(obj *soltype.ObjectType) soltype.Type {
	var elems []soltype.ObjTypeElem
	for i, elem := range obj.Elems {
		prop, ok := elem.(*soltype.PropertyElem)
		if !ok {
			continue
		}
		widened := w.widenSlot(prop.Type, w.mode.mutable && !prop.Readonly, w.mode.mutable)
		if widened == prop.Type {
			continue
		}
		if elems == nil {
			elems = slices.Clone(obj.Elems)
		}
		cp := *prop
		cp.Type = widened
		elems[i] = &cp
	}
	if elems == nil {
		return obj
	}
	out := *obj
	out.Elems = elems
	return &out
}

// widenClassArgs returns ct with each type argument widened, or ct itself when no argument
// changes. An argument the class's variance makes contravariant is kept. One that
// MutVariance makes invariant is a slot a mutable owner can write. Any other argument can
// only be read, so its owned layers are not writable through it.
func (w *literalWidener) widenClassArgs(ct *soltype.ClassType) soltype.Type {
	def, _ := w.ctx.classDef(ct.Name)
	var args []soltype.Type
	for i, arg := range ct.TypeArgs {
		widened := arg
		switch {
		case def.varianceAt(i, w.mode.mutable) == Contravariant:
		case w.mode.mutable && def.varianceAt(i, true) == Invariant:
			widened = w.widenSlot(arg, true, true)
		default:
			widened = w.widenSlot(arg, false, false)
		}
		if widened == arg {
			continue
		}
		if args == nil {
			args = slices.Clone(ct.TypeArgs)
		}
		args[i] = widened
	}
	if args == nil {
		return ct
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
	if lower := coalesce(v, soltype.Positive); !isNeverType(lower) {
		return lower
	}
	if upper := coalesce(v, soltype.Negative); !isUnknown(upper) {
		return upper
	}
	return &soltype.NeverType{}
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

package solver

import (
	"fmt"
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

// weakBinding is a module-level `val` or `var` that did not generalize.
type weakBinding struct {
	name   string
	decl   *ast.VarDecl
	scheme TypeScheme
	v      *soltype.TypeVarType
	// infoNode is the node the binding's display type is recorded on, or nil when it has
	// none.
	infoNode ast.Node
	// mut marks a binding written `mut`, which can write the value it owns.
	mut bool
	// exportedWeak lists the weak variables of an exported binding that an importer could
	// observe change, as exportedWeakVars selects them when the binding's component closed.
	exportedWeak []*soltype.TypeVarType
}

// noteWeakBinding records the weak module-level binding name for settleWeakBindings. b is
// the binding's in-flight state, scheme the scheme it was bound to, and lvl the level of
// its component.
func (c *checker) noteWeakBinding(name string, b *componentBinding, scheme TypeScheme, lvl int) {
	vd, ok := b.primary.(*ast.VarDecl)
	if !ok {
		return
	}
	w := weakBinding{name: name, decl: vd, scheme: scheme, v: b.v, infoNode: b.infoNode, mut: b.mut}
	if vd.Export() {
		w.exportedWeak = c.scanWeakVars(b.v, lvl, b.mut).exportedWeakVars()
	}
	c.weakBindings = append(c.weakBindings, w)
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
func (c *checker) noteWeakReader(b *componentBinding, scheme TypeScheme, lvl int) {
	sc, ok := scheme.(*PolyScheme)
	if !ok || c.weakBindingVars.Len() == 0 {
		return
	}
	if len(c.scanWeakVars(b.v, lvl, false).vars) > 0 {
		c.weakReaders = append(c.weakReaders, weakReader{scheme: sc, infoNode: b.infoNode})
	}
}

// settleWeakBindings runs once every component of a module at lvl is inferred. It seals
// again the display type of each binding noteWeakReader recorded and records it, so Info
// shows the type the module settled the weak variables on rather than the type they had
// when the reader's component closed. For each weak binding noteWeakBinding recorded, it:
//
//  1. seals the binding's display type again and records it, so Info shows the type the
//     module's functions settled its weak variables on rather than the type they had when
//     the binding's component closed;
//  2. reports a weak variable that received both an owned value and a borrow;
//  3. reports an exported binding that noteWeakBinding found weak variables in, since its
//     type then depends on how the module's functions use it rather than on its
//     declaration; and
//  4. reports any other binding that still has an unresolved weak variable a write could
//     reach, as weakVarScan defines both terms.
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
		// generalize sealed the display while the weak variables had only the
		// initializer's bounds, so it is sealed again from the bounds they hold now.
		//
		// No use is left to add a bound, so the body is first coalesced as if its weak
		// variables were quantified, which simplifies them the way a generalized
		// binding's variables are. A variable that only receives values reads as
		// `never`, and a recursive type folds to its smallest μ form. A weak variable
		// that survives that pass occurs in both polarities. It is not a type parameter
		// of the binding, so the second pass inlines it to its bounds.
		if sc, ok := w.scheme.(*PolyScheme); ok {
			simplified := coalesceScheme(sc.Body, sc.Level-1, sc.declared, sc.declaredLts)
			sc.coalesced = c.subsumeFinal(inlineWeakVars(simplified, lvl))
		}
		if w.infoNode != nil {
			c.recordType(w.infoNode, schemeType(w.scheme))
		}
		scan := c.scanWeakVars(w.v, lvl, w.mut)
		// Every write into the binding reaches its weak variables as a lower bound. A
		// variable that collects an owned value from one write and a borrow from another
		// holds a union no single ownership describes, the same fault a join of such
		// branches reports.
		if slices.ContainsFunc(scan.vars, mixesOwnership) {
			c.report(&MixedOwnershipError{Node: w.infoNode})
		}
		uninferred := w.exportedWeak
		if uninferred == nil {
			uninferred = scan.unresolvedWritable()
		}
		if len(uninferred) > 0 {
			c.report(&UninferredWeakBindingError{
				Decl:     w.decl,
				Node:     w.infoNode,
				Name:     w.name,
				TypeText: c.renderWeakAnnotation(w.v, scan.unresolvedAmong(uninferred)),
			})
		}
	}
}

// weakVarScan is what scanWeakVars found. Two terms describe a weak variable:
//   - It is writable when it sits in a position where a write or a call can still add a
//     value to it, such as a parameter of a function type or a type argument a mutable
//     binding's class can write. scanWeakVars reaches such a variable at negative
//     polarity.
//   - It is unresolved when no concrete type reaches it, as a lower or an upper bound,
//     directly or through other variables. Nothing written into it and nothing read out
//     of it says what it holds.
//
// The K and V of `val mut seen = Map()` are writable, and they stay unresolved until a
// function calls `seen.set`. A container that is never written is therefore unresolved.
// The K and V of the immutable `val seen = Map()` are not writable, so no use can give
// them a value and the map's type settles at `Map<never, never>`.
type weakVarScan struct {
	// vars holds every weak variable reached, in the order the walk reached them.
	vars     []*soltype.TypeVarType
	writable set.Set[*soltype.TypeVarType]
}

// unresolvedAmong returns the members of vs that are unresolved, in order.
func (s weakVarScan) unresolvedAmong(vs []*soltype.TypeVarType) []*soltype.TypeVarType {
	var out []*soltype.TypeVarType
	for _, v := range vs {
		if !reachesConcrete(v, soltype.Positive, set.NewSet[*soltype.TypeVarType]()) &&
			!reachesConcrete(v, soltype.Negative, set.NewSet[*soltype.TypeVarType]()) {
			out = append(out, v)
		}
	}
	return out
}

// unresolvedWritable returns the weak variables that are both unresolved and writable, in
// the order the walk reached them.
func (s weakVarScan) unresolvedWritable() []*soltype.TypeVarType {
	var out []*soltype.TypeVarType
	for _, v := range s.unresolvedAmong(s.vars) {
		if s.writable.Contains(v) {
			out = append(out, v)
		}
	}
	return out
}

// exportedWeakVars returns the weak variables an importer of the binding could observe
// change, in the order the walk reached them. Those are the writable ones and the
// unresolved ones, since a module that never decides a variable leaves the exported type
// to whatever its uses add later.
func (s weakVarScan) exportedWeakVars() []*soltype.TypeVarType {
	unresolved := set.FromSlice(s.unresolvedAmong(s.vars))
	var out []*soltype.TypeVarType
	for _, v := range s.vars {
		if s.writable.Contains(v) || unresolved.Contains(v) {
			out = append(out, v)
		}
	}
	return out
}

// scanWeakVars walks the type of the binding var v and reports the weak variables it
// reaches. A weak variable is one at or below lvl, and v itself is never reported. mut
// says whether the binding is written `mut`, which lets it write what it owns.
//
// The walk starts covariant and follows each variable's bounds in the direction its
// polarity reads, lower bounds when positive and upper bounds when negative. A class
// type argument is walked by the variance the class measured for its parameter,
// through the mutable vector inside a mutable binding or `&mut` borrow. A property a
// mutable binding can write is walked in both polarities.
func (c *checker) scanWeakVars(v *soltype.TypeVarType, lvl int, mut bool) weakVarScan {
	s := &weakVarScanner{
		ctx:     c.ctx,
		lvl:     lvl,
		root:    v,
		mut:     mut,
		seen:    set.NewSet[weakScanKey](),
		reached: set.NewSet[*soltype.TypeVarType](),
		out:     weakVarScan{writable: set.NewSet[*soltype.TypeVarType]()},
	}
	v.Accept(s, soltype.Positive)
	return s.out
}

// weakScanKey is a variable reached at one polarity and mutability.
type weakScanKey struct {
	v   *soltype.TypeVarType
	pol soltype.Polarity
	mut bool
}

// weakVarScanner is the visitor behind scanWeakVars. mut holds whether the node being
// walked sits where the binding can write it.
type weakVarScanner struct {
	ctx     *Context
	lvl     int
	root    *soltype.TypeVarType
	mut     bool
	seen    set.Set[weakScanKey]
	reached set.Set[*soltype.TypeVarType]
	out     weakVarScan
}

// walk visits t at polarity pol with the scanner's mutability set to mut, restoring the
// enclosing mutability afterwards.
func (s *weakVarScanner) walk(t soltype.Type, pol soltype.Polarity, mut bool) {
	prev := s.mut
	s.mut = mut
	t.Accept(s, pol)
	s.mut = prev
}

func (s *weakVarScanner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	skip := soltype.EnterResult{SkipChildren: true}
	switch t := t.(type) {
	case *soltype.TypeVarType:
		key := weakScanKey{v: t, pol: pol, mut: s.mut}
		if s.seen.Contains(key) {
			return skip
		}
		s.seen.Add(key)
		if t != s.root && t.Level <= s.lvl {
			if !s.reached.Contains(t) {
				s.reached.Add(t)
				s.out.vars = append(s.out.vars, t)
			}
			if pol == soltype.Negative {
				s.out.writable.Add(t)
			}
		}
		bounds := t.LowerBounds
		if pol == soltype.Negative {
			bounds = t.UpperBounds
		}
		for _, b := range bounds {
			b.Accept(s, pol)
		}
		return skip
	case *soltype.RefType:
		s.walk(t.Inner, pol, t.Mut)
		return skip
	case *soltype.ClassType:
		def, _ := s.ctx.classDef(t.Name)
		for i, arg := range t.TypeArgs {
			switch def.varianceAt(i, s.mut) {
			case Covariant:
				s.walk(arg, pol, s.mut)
			case Contravariant:
				s.walk(arg, pol.Flip(), s.mut)
			case Invariant:
				s.walk(arg, pol, s.mut)
				s.walk(arg, pol.Flip(), s.mut)
			}
		}
		return skip
	case *soltype.ObjectType:
		if s.mut {
			for _, elem := range t.Elems {
				if prop, ok := elem.(*soltype.PropertyElem); ok && !prop.Readonly {
					s.walk(prop.Type, pol.Flip(), s.mut)
				}
			}
		}
	}
	return soltype.EnterResult{}
}

func (s *weakVarScanner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// reachesConcrete reports whether a type other than a variable bounds v, directly or
// through a chain of variable bounds. pol picks the direction, lower bounds for Positive and
// upper bounds for Negative. seen holds the variables already visited.
func reachesConcrete(v *soltype.TypeVarType, pol soltype.Polarity, seen set.Set[*soltype.TypeVarType]) bool {
	if seen.Contains(v) {
		return false
	}
	seen.Add(v)
	bounds := v.LowerBounds
	if pol == soltype.Negative {
		bounds = v.UpperBounds
	}
	for _, b := range bounds {
		bv, isVar := b.(*soltype.TypeVarType)
		if !isVar || reachesConcrete(bv, pol, seen) {
			return true
		}
	}
	return false
}

// renderWeakAnnotation renders the type of the binding var v as an annotation to suggest
// for it. Each variable in unresolved stays a variable rather than collapsing to `never`.
// One that fills a class or alias parameter takes that parameter's name, so
// `val mut seen = Map()` renders `Map<K, V>`. Any other takes a generated name, T0 and up,
// in the order unresolved lists them.
func (c *checker) renderWeakAnnotation(v *soltype.TypeVarType, unresolved []*soltype.TypeVarType) string {
	t := coalesceKeeping(v, soltype.Positive, set.FromSlice(unresolved), nil, nil)
	params := c.declaredTypeParams(t)
	named := set.NewSet[*soltype.TypeVarType]()
	for _, p := range params {
		named.Add(p.Var)
	}
	generated := 0
	for _, u := range unresolved {
		if !named.Contains(u) {
			params = append(params, &soltype.TypeParam{Name: fmt.Sprintf("T%d", generated), Var: u})
			generated++
		}
	}
	return soltype.PrintWithDeclaredParams(t, params, c.declaredLifetimeParams(t))
}

// weakAssignTarget returns the type a reassignment of the weak binding b is checked
// against. Its weak variables that sit inside a type constructor stay variables, so the
// source's type flows into them. The binding var and any variable that only passes the
// initializer's type through to it are coalesced to their bounds. `var a = 5` then checks
// `a = "x"` against `number` and rejects it, where `var id = makeId()` checks
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
		for _, v := range c.scanWeakVars(root, sc.Level, b.Mut).vars {
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
func inlineWeakVars(t soltype.Type, lvl int) soltype.Type {
	return t.Accept(&weakVarInliner{lvl: lvl}, soltype.Positive)
}

// weakVarInliner is the visitor behind inlineWeakVars.
type weakVarInliner struct {
	lvl int
}

func (w *weakVarInliner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if v, ok := t.(*soltype.TypeVarType); ok && v.Level <= w.lvl {
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

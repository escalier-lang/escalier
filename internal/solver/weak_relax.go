package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A relaxed binding is a `val` whose initializer is not a syntactic value, such as
// `val g = makeId()`. Its initializer is inferred as a generalizing binding's is. Once it is
// inferred, lowerStoringVars makes weak each variable of its type that a value of the type
// can store, and the binding generalizes over the rest. This is the relaxed value
// restriction. With `fn makeId() { return fn (x) { return x } }`, nothing in `makeId()`
// can store a value, so `g` generalizes to `fn <T0>(x: T0) -> T0`.

// relaxes reports whether the `val` declaration d is a relaxed binding. It is one when it
// does not generalize, as generalizes decides, is not a `var`, has no `mut` leaf, and binds
// a single identifier.
func relaxes(d *ast.VarDecl) bool {
	if generalizes(d) || d.Kind != ast.ValKind || hasMutLeaf(d.Pattern) {
		return false
	}
	_, isIdent := d.Pattern.(*ast.IdentPat)
	return isIdent
}

// weakAtDecl reports whether the `val` or `var` declaration d is weak before its
// initializer is inferred. Such a declaration infers its initializer at the binding's own
// level, so every variable the initializer mints is weak.
func weakAtDecl(d *ast.VarDecl) bool {
	return !generalizes(d) && !relaxes(d)
}

// lowerStoringVars moves to lvl each variable of t above lvl that a value of t can store,
// along with every variable reachable from such a variable's bounds. It moves each
// lifetime variable such a position reaches as well. A variable at lvl is weak, so the
// binding that generalizes t at lvl does not quantify it. It reports whether any variable
// moved.
//
// A value of t can store a value of a variable when one of these mentions the variable:
//   - a function that may hold state, which is any function type not marked Pure,
//   - a `&mut` borrow,
//   - a class argument the class's MutVariance makes invariant,
//   - a getter or setter of an object, and
//   - a generator.
//
// Only a position that produces a value counts. A function the value accepts as an
// argument is not held by the value, so its own state does not matter. A type alias is
// read through its body. An alias whose body is not resolved yet counts as a position that
// can store a value.
func (c *checker) lowerStoringVars(t soltype.Type, lvl int) bool {
	s := &storingVarScanner{
		ctx:       c.ctx,
		lvl:       lvl,
		seen:      set.NewSet[storingVisit](),
		expanded:  set.NewSet[storingAlias](),
		binders:   set.NewSet[*soltype.TypeVarType](),
		ltBinders: set.NewSet[*soltype.LifetimeVar](),
		out:       set.NewSet[*soltype.TypeVarType](),
		lts:       set.NewSet[*soltype.LifetimeVar](),
	}
	t.Accept(s, soltype.Positive)
	if s.out.Len() == 0 && s.lts.Len() == 0 {
		return false
	}
	l := &varLowerer{ctx: c.ctx, lvl: lvl, binders: s.binders, ltBinders: s.ltBinders}
	for _, v := range s.out.ToSlice() {
		l.lower(v)
	}
	for _, lv := range s.lts.ToSlice() {
		l.lowerLt(lv)
	}
	return true
}

// storingVisit is a variable reached at one polarity, inside or outside a position that
// can store a value.
type storingVisit struct {
	v       *soltype.TypeVarType
	pol     soltype.Polarity
	storing bool
}

// storingAlias is an alias reached at one polarity, inside or outside a position that can
// store a value.
type storingAlias struct {
	name    string
	pol     soltype.Polarity
	storing bool
}

// storingVarScanner is the visitor behind lowerStoringVars. storing marks a walk inside a
// position that can store a value, where every variable above lvl it reaches goes in out.
type storingVarScanner struct {
	ctx     *Context
	lvl     int
	storing bool
	seen    set.Set[storingVisit]
	// expanded holds each alias whose body the walk has read, so a recursive alias is read
	// once.
	expanded set.Set[storingAlias]
	// binders and ltBinders hold the type and lifetime parameters of each function type the
	// walk entered. The function quantifies them, so they are never moved.
	binders   set.Set[*soltype.TypeVarType]
	ltBinders set.Set[*soltype.LifetimeVar]
	out       set.Set[*soltype.TypeVarType]
	// lts holds each lifetime variable above lvl that a position that can store a value
	// reaches.
	lts set.Set[*soltype.LifetimeVar]
}

// storingFrom walks t at pol inside a position that can store a value.
func (s *storingVarScanner) storingFrom(t soltype.Type, pol soltype.Polarity) {
	inner := *s
	inner.storing = true
	t.Accept(&inner, pol)
}

func (s *storingVarScanner) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		key := storingVisit{v: t, pol: pol, storing: s.storing}
		if t.Level <= s.lvl || s.binders.Contains(t) || s.seen.Contains(key) {
			return soltype.EnterResult{SkipChildren: true}
		}
		s.seen.Add(key)
		if s.storing {
			s.out.Add(t)
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
		for _, tp := range t.TypeParams {
			s.binders.Add(tp.Var)
		}
		for _, lp := range t.LifetimeParams {
			s.ltBinders.Add(lp.Var)
		}
		s.addLts(t.Lt)
		if !s.storing && pol == soltype.Positive && !t.Pure {
			s.storingFrom(t, pol)
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.RefType:
		s.addLts(t.Lt)
		if !s.storing && t.Mut {
			s.storingFrom(t, pol)
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.GeneratorType:
		if !s.storing && pol == soltype.Positive {
			s.storingFrom(t, pol)
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.ObjectType:
		if !s.storing && pol == soltype.Positive && hasAccessor(t) {
			s.storingFrom(t, pol)
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.ClassType:
		s.addLts(t.LifetimeArgs...)
		s.classArgs(t, pol)
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.AliasType:
		s.addLts(t.LifetimeArgs...)
		key := storingAlias{name: t.Name, pol: pol, storing: s.storing}
		body, ok := s.ctx.aliasBody(t)
		// An alias the walk has read once, as a recursive alias reaches itself, is not read
		// again. Its arguments may differ from the first use's, so they count as positions
		// that can store a value, as an alias with no body yet does.
		if !ok || s.expanded.Contains(key) {
			for _, arg := range t.TypeArgs {
				s.storingFrom(arg, pol)
				s.storingFrom(arg, pol.Flip())
			}
			return soltype.EnterResult{SkipChildren: true}
		}
		s.expanded.Add(key)
		body.Accept(s, pol)
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *storingVarScanner) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// addLts adds each lifetime variable in lts to s.lts when the walk is inside a position that
// can store a value.
func (s *storingVarScanner) addLts(lts ...soltype.Lifetime) {
	if !s.storing {
		return
	}
	for _, lt := range lts {
		if lv, isVar := lt.(*soltype.LifetimeVar); isVar && lv.Level > s.lvl && !s.ltBinders.Contains(lv) {
			s.lts.Add(lv)
		}
	}
}

// classArgs walks each type argument of ct at the polarity the class's variance gives it.
// An argument MutVariance makes invariant is walked as a position that can store a value,
// at both polarities. A class whose variance is not settled yet reads as invariant in every
// argument.
func (s *storingVarScanner) classArgs(ct *soltype.ClassType, pol soltype.Polarity) {
	def, _ := s.ctx.settledClassDef(ct.Name)
	for i, arg := range ct.TypeArgs {
		switch {
		case def.varianceAt(i, true) == Invariant:
			s.storingFrom(arg, pol)
			s.storingFrom(arg, pol.Flip())
		case def.varianceAt(i, false) == Contravariant:
			arg.Accept(s, pol.Flip())
		case def.varianceAt(i, false) == Bivariant:
		default:
			arg.Accept(s, pol)
		}
	}
}

// hasAccessor reports whether obj has a getter or a setter.
func hasAccessor(obj *soltype.ObjectType) bool {
	for _, elem := range obj.Elems {
		switch elem.(type) {
		case *soltype.GetterElem, *soltype.SetterElem:
			return true
		}
	}
	return false
}

// varLowerer moves variables to lvl. A variable at lvl may only have bounds that mention
// variables at lvl or below, so moving one also moves every variable its bounds reach.
type varLowerer struct {
	ctx *Context
	lvl int
	// binders and ltBinders hold the type and lifetime parameters of the function types
	// lowerStoringVars walked. A function quantifies its own, so they are never moved.
	binders   set.Set[*soltype.TypeVarType]
	ltBinders set.Set[*soltype.LifetimeVar]
}

// lower moves v to lvl, and every variable above lvl that v's bounds reach.
func (l *varLowerer) lower(v *soltype.TypeVarType) {
	if v.Level <= l.lvl || l.binders.Contains(v) {
		return
	}
	l.ctx.lowerLevel(v, l.lvl)
	for _, b := range v.LowerBounds {
		b.Accept(l, soltype.Positive)
	}
	for _, b := range v.UpperBounds {
		b.Accept(l, soltype.Negative)
	}
}

// lowerLt moves the lifetime variable in lt to lvl when it is above lvl, and every lifetime
// variable above lvl that its bounds reach.
func (l *varLowerer) lowerLt(lt soltype.Lifetime) {
	lv, isVar := lt.(*soltype.LifetimeVar)
	if !isVar || lv.Level <= l.lvl || l.ltBinders.Contains(lv) {
		return
	}
	l.ctx.checkThawedLt(lv, "lowerStoringVars")
	lv.Level = l.lvl
	for _, b := range lv.LowerBounds {
		l.lowerLt(b)
	}
	for _, b := range lv.UpperBounds {
		l.lowerLt(b)
	}
}

func (l *varLowerer) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		l.lower(t)
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.FuncType:
		for _, tp := range t.TypeParams {
			l.binders.Add(tp.Var)
		}
		for _, lp := range t.LifetimeParams {
			l.ltBinders.Add(lp.Var)
		}
		l.lowerLt(t.Lt)
	case *soltype.RefType:
		l.lowerLt(t.Lt)
	case *soltype.ClassType:
		for _, lt := range t.LifetimeArgs {
			l.lowerLt(lt)
		}
	case *soltype.AliasType:
		for _, lt := range t.LifetimeArgs {
			l.lowerLt(lt)
		}
	}
	return soltype.EnterResult{}
}

func (l *varLowerer) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// capturesNoState reports whether the function fn, written in scope, holds no state a call
// can change. It holds none when it captures no `var`, no `mut` binding, and no value that
// may hold state, as mayHoldState decides, and writes no place it captures. The captures
// count those of the functions nested in fn. A function whose captures the pre-pass did
// not record counts as holding state, and so does one that captures a binding of the
// module component being inferred, since that binding's type is not known yet.
func (c *checker) capturesNoState(scope *Scope, fn ast.Func) bool {
	captures, ok := c.closureCaptureInfo[fn]
	if !ok {
		return false
	}
	self := ""
	if fd, isDecl := fn.(*ast.FuncDecl); isDecl && fd.Name != nil {
		self = fd.Name.Name
	}
	for _, capture := range captures {
		if capture.IsMutable {
			return false
		}
		// A call of fn's own name runs fn, so it holds no state fn does not.
		if capture.Name == self {
			continue
		}
		b, found := c.lookupValueBinding(scope, capture.Name)
		if !found || b.Kind == ast.VarKind || b.Mut {
			return false
		}
		if c.isPendingBinding(b) {
			return false
		}
		for _, sc := range b.Schemes {
			if c.mayHoldState(sc) {
				return false
			}
		}
	}
	return true
}

// mayHoldState reports whether a value of sc, one scheme of a binding, may hold state a
// call can change. A value does when it reaches a function type not marked Pure, a `&mut`
// borrow, a class instance, a getter, a setter, or a generator. A variable reaches what
// its lower and upper bounds reach, since either side can say what the value is, and an
// alias reaches what its body reaches.
func (c *checker) mayHoldState(sc TypeScheme) bool {
	var t soltype.Type
	switch sc := sc.(type) {
	case *MonoScheme:
		t = sc.Ty
	case *PolyScheme:
		t = sc.Body
	default:
		return true
	}
	f := &stateFinder{
		ctx:      c.ctx,
		seen:     set.NewSet[*soltype.TypeVarType](),
		expanded: set.NewSet[string](),
	}
	t.Accept(f, soltype.Positive)
	return f.found
}

// isPendingBinding reports whether b is a binding of the module component being inferred,
// which reads as its bare binding var until the component is.
func (c *checker) isPendingBinding(b ValueBinding) bool {
	if len(b.Schemes) != 1 {
		return false
	}
	mono, isMono := b.Schemes[0].(*MonoScheme)
	if !isMono {
		return false
	}
	v, isVar := mono.Ty.(*soltype.TypeVarType)
	return isVar && c.pendingBindingVars.Contains(v)
}

// stateFinder is the visitor behind mayHoldState. It sets found on reaching a type that may
// hold state.
type stateFinder struct {
	ctx  *Context
	seen set.Set[*soltype.TypeVarType]
	// expanded holds the name of each alias whose body the walk has read, so a recursive
	// alias is read once.
	expanded set.Set[string]
	found    bool
}

func (f *stateFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if f.found {
		return soltype.EnterResult{SkipChildren: true}
	}
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if f.seen.Contains(t) {
			return soltype.EnterResult{SkipChildren: true}
		}
		f.seen.Add(t)
		for _, b := range t.LowerBounds {
			b.Accept(f, soltype.Positive)
		}
		for _, b := range t.UpperBounds {
			b.Accept(f, soltype.Positive)
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.FuncType:
		// A function marked Pure holds no state, whatever the values it returns hold. A call
		// builds those values afresh.
		f.found = !t.Pure
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.RefType:
		if t.Mut {
			f.found = true
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.GeneratorType, *soltype.ClassType:
		// A class instance's fields and methods are not in its type, so it may hold state.
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.AliasType:
		// The walk reads an alias's body once, as a recursive alias reaches itself. A later
		// use reads only its own arguments, which may differ from the first use's.
		if f.expanded.Contains(t.Name) {
			return soltype.EnterResult{}
		}
		f.expanded.Add(t.Name)
		body, ok := f.ctx.aliasBody(t)
		f.found = !ok
		if ok {
			body.Accept(f, soltype.Positive)
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ObjectType:
		if hasAccessor(t) {
			f.found = true
			return soltype.EnterResult{SkipChildren: true}
		}
		// A constructor builds a fresh value, so a class value's constructor holds no
		// state. Its other members still count.
		for _, elem := range t.Elems {
			switch elem := elem.(type) {
			case *soltype.ConstructorElem:
			case *soltype.PropertyElem:
				elem.Type.Accept(f, soltype.Positive)
			case *soltype.MethodElem:
				for _, sig := range elem.Signatures {
					sig.Accept(f, soltype.Positive)
				}
			case *soltype.CallableElem:
				for _, sig := range elem.Signatures {
					sig.Accept(f, soltype.Positive)
				}
			default:
				f.found = true
			}
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *stateFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

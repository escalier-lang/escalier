package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// DeclSignature is the signature the checker gave a declared function or method.
type DeclSignature struct {
	// Type is the function's generalized type. A variable generalization quantified is
	// still in it, so it renders through PrintWritten rather than through soltype.Print.
	Type *soltype.FuncType
	// ClassTypeParams and ClassLifetimeParams are the parameters of the class a method
	// belongs to, which are in scope in its signature. Both are nil for a function.
	ClassTypeParams     []*soltype.TypeParam
	ClassLifetimeParams []*soltype.LifetimeParam
}

// PrintWritten renders s one part at a time, the way soltype.PrintFuncSignature does, and
// leaves out each lifetime that elision assigns. Such a lifetime, as elidedLifetimes lists
// them, gets no binder and renders as a bare borrow.
func (s DeclSignature) PrintWritten() soltype.FuncSignature {
	bounds := displayLtBounds(s.Type, soltype.Positive)
	elided := s.elidedLifetimes(bounds)
	return soltype.PrintFuncSignature(s.Type, s.ClassTypeParams, s.ClassLifetimeParams, bounds, elided)
}

// FuncDeclSignature returns the signature of the function d declares at a module's or a
// script's top level. ok is false when the checker recorded no function type for d.
func (i *Info) FuncDeclSignature(d *ast.FuncDecl) (DeclSignature, bool) {
	ft, ok := i.TypeOf(d.Name).(*soltype.FuncType)
	if !ok {
		return DeclSignature{}, false
	}
	return DeclSignature{Type: ft}, true
}

// MethodSignature returns the signature of the method m, which class declares. A method
// declared under one name more than once is an overload set, and each declaration gets the
// signature of its own arm. ok is false when the checker registered no such method, when
// m or a method before it on the same side of the class has a computed key, whose name only
// the checker resolves, and on a table built by NewInfo.
func (i *Info) MethodSignature(class *ast.ClassDecl, m *ast.MethodElem) (DeclSignature, bool) {
	if i.ctx == nil {
		return DeclSignature{}, false
	}
	instance, ok := i.TypeOf(class.Name).(*soltype.ClassType)
	if !ok {
		return DeclSignature{}, false
	}
	def, ok := i.ctx.classDef(instance.Name)
	if !ok {
		return DeclSignature{}, false
	}
	name, ok := objKeyName(m.Name)
	if !ok {
		return DeclSignature{}, false
	}
	// The registered arms of a name are in declaration order, so m's arm is the count of
	// methods on the same side of the class written under its name before it.
	arm := 0
	for _, elem := range class.Body {
		if elem == m {
			break
		}
		if other, isMethod := elem.(*ast.MethodElem); isMethod && other.Static == m.Static {
			otherName, named := objKeyName(other.Name)
			if !named {
				return DeclSignature{}, false
			}
			if otherName == name {
				arm++
			}
		}
	}
	members := def.Body
	if m.Static {
		members = def.Static
	}
	if members == nil {
		return DeclSignature{}, false
	}
	for _, elem := range members.Elems {
		if me, isMethod := elem.(*soltype.MethodElem); isMethod && me.Name == name && arm < len(me.Signatures) {
			return DeclSignature{
				Type:                me.Signatures[arm],
				ClassTypeParams:     def.TypeParams,
				ClassLifetimeParams: def.LifetimeParams,
			}, true
		}
	}
	return DeclSignature{}, false
}

// elidedLifetimes returns the lifetimes of s that elision assigns when the signature leaves
// them out. ltBounds is the outlives relation displayLtBounds computes for s.Type. Each
// lifetime returned is one the checker inferred rather than one the source named, and has
// no outlives bound. It is one of these:
//
//   - a lifetime that occurs once among the receiver and the parameters and not in the
//     return or throws type, which elision gives each lifetime a parameter leaves out;
//   - a lifetime that occurs once among the receiver and the parameters, which hold no other
//     lifetime, since elision ties an output lifetime to the one input lifetime;
//   - the lifetime of a borrowed receiver that the output borrows, since elision ties a
//     returned borrow to the receiver's lifetime. A returned function type is tied to every
//     input instead, so a lifetime it carries stays written.
//
// Elision reads only the signature's own level, so a lifetime that also occurs inside a
// function type's parameters or return, in a type parameter's bound or default, or as a
// class's or alias's lifetime argument, stays written.
func (s DeclSignature) elidedLifetimes(ltBounds map[*soltype.LifetimeVar][]*soltype.LifetimeVar) set.Set[*soltype.LifetimeVar] {
	ft := s.Type
	in := newLifetimeUses()
	if ft.SelfParam != nil {
		ft.SelfParam.Type.Accept(in, soltype.Negative)
	}
	for _, p := range ft.Params {
		p.Type.Accept(in, soltype.Negative)
	}
	out := newLifetimeUses()
	ft.Ret.Accept(out, soltype.Positive)
	ft.ThrowsOrNever().Accept(out, soltype.Positive)
	// A type parameter's bounds and default sit outside the signature's own level, so the
	// walk over them starts one level down and records every lifetime as nested.
	bound := newLifetimeUses()
	bound.depth = 1
	for _, tp := range ft.TypeParams {
		for _, b := range slices.Concat(tp.DeclaredUpperBounds(), tp.DeclaredLowerBounds()) {
			b.Accept(bound, soltype.Positive)
		}
		if tp.Default != nil {
			tp.Default.Accept(bound, soltype.Positive)
		}
	}

	written := set.NewSet[*soltype.LifetimeVar]()
	for lv, bounds := range ltBounds {
		written.Add(lv)
		for _, b := range bounds {
			written.Add(b)
		}
	}
	for _, lp := range ft.LifetimeParams {
		if lp.Name != "" || len(lp.Bounds) > 0 {
			written.Add(lp.Var)
		}
		for _, b := range lp.Bounds {
			if lv, ok := b.(*soltype.LifetimeVar); ok {
				written.Add(lv)
			}
		}
	}
	var receiverLt *soltype.LifetimeVar
	if ft.SelfParam != nil {
		if ref, ok := ft.SelfParam.Type.(*soltype.RefType); ok {
			receiverLt, _ = ref.Lt.(*soltype.LifetimeVar)
		}
	}

	elided := set.NewSet[*soltype.LifetimeVar]()
	for lv, uses := range in.atTop {
		switch {
		case uses != 1, written.Contains(lv), in.nested.Contains(lv), out.nested.Contains(lv), bound.nested.Contains(lv):
		case out.atTop[lv] == 0, in.positions == 1:
			elided.Add(lv)
		case lv == receiverLt && !out.funcLts.Contains(lv):
			elided.Add(lv)
		}
	}
	return elided
}

// lifetimeUses records where the lifetimes of one side of a signature occur, the inputs or
// the outputs. The signature's own level is every position outside a function type's
// parameters and return, where elision assigns a lifetime the signature leaves out.
type lifetimeUses struct {
	// atTop counts each variable's occurrences as the lifetime of a borrow or of a function
	// type at the signature's own level.
	atTop map[*soltype.LifetimeVar]int
	// positions counts the lifetimes at the signature's own level, a variable or not,
	// other than 'static.
	positions int
	// funcLts holds the variables that are the lifetime of a function type at the
	// signature's own level.
	funcLts set.Set[*soltype.LifetimeVar]
	// nested holds the variables that occur anywhere else: inside a function type's
	// parameters or return, in a lifetime bound, or as the lifetime or a lifetime argument
	// of a class or alias.
	nested set.Set[*soltype.LifetimeVar]
	// depth counts the function types around the type being visited.
	depth int
}

func newLifetimeUses() *lifetimeUses {
	return &lifetimeUses{
		atTop:   map[*soltype.LifetimeVar]int{},
		funcLts: set.NewSet[*soltype.LifetimeVar](),
		nested:  set.NewSet[*soltype.LifetimeVar](),
	}
}

func (u *lifetimeUses) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		u.note(t.Lt)
	case *soltype.FuncType:
		u.note(t.Lt)
		if lv, ok := t.Lt.(*soltype.LifetimeVar); ok && u.depth == 0 {
			u.funcLts.Add(lv)
		}
		u.depth++
		for _, lp := range t.LifetimeParams {
			for _, b := range lp.Bounds {
				u.noteNested(b)
			}
		}
	case *soltype.ClassType:
		u.noteNested(t.Lt)
		for _, a := range t.LifetimeArgs {
			u.noteNested(a)
		}
	case *soltype.AliasType:
		for _, a := range t.LifetimeArgs {
			u.noteNested(a)
		}
	}
	return soltype.EnterResult{}
}

func (u *lifetimeUses) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if _, ok := t.(*soltype.FuncType); ok {
		u.depth--
	}
	return t
}

// note records lt as the lifetime of a borrow or a function type at the current depth. A
// nil lifetime and 'static are written or hold no borrow, so elision never assigns them,
// and they are skipped.
func (u *lifetimeUses) note(lt soltype.Lifetime) {
	if lt == nil {
		return
	}
	if _, static := lt.(*soltype.StaticLifetime); static {
		return
	}
	if u.depth > 0 {
		u.noteNested(lt)
		return
	}
	u.positions++
	if lv, ok := lt.(*soltype.LifetimeVar); ok {
		u.atTop[lv]++
	}
}

// noteNested records lt as occurring where elision does not reach.
func (u *lifetimeUses) noteNested(lt soltype.Lifetime) {
	if lv, ok := lt.(*soltype.LifetimeVar); ok {
		u.nested.Add(lv)
	}
}

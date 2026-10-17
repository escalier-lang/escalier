package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeCtorArg resolves ta as the argument of a type-constructor parameter whose arguments
// have the declared variances slots. It returns one of:
//
//   - the variable of a constructor parameter in scope, as F in `Functor<F>` inside `<F<_>>`
//   - a TypeCtorType for a named class or alias, as `Array` in `Functor<Array>`
//
// A written `_` marks a hole an application fills, so `Promise<_, FetchError>` is a constructor
// taking one argument. A reference that writes no arguments leaves every parameter a hole, except
// that trailing parameters with a default are filled from it when that brings the hole count down
// to arity. A mismatched count is reported and the argument recovers to a fresh variable.
//
// A constructor parameter in scope is accepted when its own slots declare at least the variance
// each of slots asks for. A named constructor is checked against slots once inference finishes,
// since a class's variance is measured from its body.
func (c *checker) resolveTypeCtorArg(scope *Scope, ta ast.TypeAnn, slots []soltype.SlotVariance, lvl int) soltype.Type {
	arity := len(slots)
	ref, ok := ta.(*ast.TypeRefTypeAnn)
	if !ok {
		c.report(&NotATypeCtorError{Ann: ta, Arity: arity})
		return c.freshAt(lvl)
	}
	name := ast.QualIdentToString(ref.Name)
	b, ok := c.lookupClassBinding(scope, name)
	if !ok {
		c.report(&UnknownTypeError{Ref: ref, Name: name})
		return c.freshAt(lvl)
	}
	if v, isVar := b.Type.(*soltype.TypeVarType); isVar {
		if got := c.ctx.ctorParamSlotsOf(v); len(got) == arity && len(ref.TypeArgs) == 0 {
			if slot, ok := slotsSatisfy(got, slots); !ok {
				c.report(&KindMismatchError{Ctor: name, Slot: slot, Want: slots[slot], site: ta})
			}
			return v
		}
		c.report(&NotATypeCtorError{Ann: ta, Arity: arity})
		return c.freshAt(lvl)
	}
	var base soltype.Type
	var params []*soltype.TypeParam
	switch h := b.Type.(type) {
	case *soltype.ClassType:
		if def, ok := c.ctx.classDef(h.Name); ok {
			params = def.TypeParams
		}
		base = &soltype.ClassType{Name: h.Name, Defaults: paramDefaults(params), Final: h.Final, Variant: h.Variant}
	case *soltype.AliasType:
		if def, ok := c.ctx.aliasDef(h.Name); ok && def != nil {
			params = def.TypeParams
		}
		base = &soltype.AliasType{Name: h.Name, Defaults: paramDefaults(params)}
	default:
		c.report(&NotATypeCtorError{Ann: ta, Arity: arity})
		return c.freshAt(lvl)
	}
	if len(ref.TypeArgs) > len(params) {
		c.report(&NotATypeCtorError{Ann: ta, Arity: arity})
		return c.freshAt(lvl)
	}
	fixed := make([]soltype.Type, len(params))
	for i, arg := range ref.TypeArgs {
		if isWildcardAnn(arg) {
			continue
		}
		resolved, ok := c.resolveTypeAnn(scope, arg, lvl)
		if !ok {
			resolved = c.freshAt(lvl)
		}
		fixed[i] = resolved
	}
	// A reference that writes arguments fills every parameter after them from its default, as an
	// ordinary reference does, so `P<_>` serves for `class P<T, E = string>`. A bare reference
	// leaves every parameter a hole and fills trailing defaults only until the holes left match
	// the arity asked for, so a bare `Promise` serves where one argument is expected.
	for i := len(params) - 1; i >= len(ref.TypeArgs); i-- {
		if len(ref.TypeArgs) == 0 && len(params)-countFilled(fixed) <= arity {
			break
		}
		if !ctorDefaultUsable(params, i) {
			break
		}
		fixed[i] = params[i].Default
	}
	ctor := &soltype.TypeCtorType{Base: base, Fixed: fixed}
	if ctor.Arity() != arity {
		c.report(&NotATypeCtorError{Ann: ta, Arity: arity})
		return c.freshAt(lvl)
	}
	c.deferredKindChecks = append(c.deferredKindChecks, deferredKindCheck{ctor: ctor, slots: slots, site: ta})
	return ctor
}

// deferredKindCheck is one named constructor waiting to be checked against the slots of the
// parameter it was passed to.
type deferredKindCheck struct {
	ctor  *soltype.TypeCtorType
	slots []soltype.SlotVariance
	site  ast.Node
}

// runDeferredKindChecks checks each constructor resolveTypeCtorArg queued against its slots and
// clears the queue. It runs once every class's variance is final.
func (c *checker) runDeferredKindChecks() {
	pending := c.deferredKindChecks
	c.deferredKindChecks = nil
	for _, k := range pending {
		if slot, ok := c.ctx.ctorSatisfiesSlots(k.ctor, k.slots); !ok {
			c.report(&KindMismatchError{Ctor: soltype.Print(k.ctor), Slot: slot, Want: k.slots[slot], site: k.site})
		}
	}
}

// slotsSatisfy reports whether a constructor parameter declaring have may stand where want is
// expected. Each slot of want that declares a variance needs the same variance in have, and an
// invariant slot of want accepts any. It returns the first slot that fails.
func slotsSatisfy(have, want []soltype.SlotVariance) (int, bool) {
	for i := range want {
		w := soltype.SlotAt(want, i)
		if w != soltype.SlotInvariant && soltype.SlotAt(have, i) != w {
			return i, false
		}
	}
	return 0, true
}

// ctorSatisfiesSlots reports whether ctor has the variance each of slots declares, and the first
// slot where it does not. A covariant slot holds when applying ctor to a subtype in that slot
// gives a subtype instance. The check applies ctor to two skolems, lo and hi with lo <: hi, and
// asks whether `ctor<lo> <: ctor<hi>` holds under a probe. A contravariant slot asks the reverse.
// Every other hole takes one skolem shared by both sides.
func (c *Context) ctorSatisfiesSlots(ctor *soltype.TypeCtorType, slots []soltype.SlotVariance) (int, bool) {
	for i := range slots {
		want := soltype.SlotAt(slots, i)
		if want == soltype.SlotInvariant {
			continue
		}
		hi := c.freshSkolem("hi")
		lo := c.freshSkolem("lo")
		lo.Upper = hi
		loArgs := make([]soltype.Type, len(slots))
		hiArgs := make([]soltype.Type, len(slots))
		for j := range slots {
			if j == i {
				loArgs[j], hiArgs[j] = lo, hi
				continue
			}
			shared := c.freshSkolem("x")
			loArgs[j], hiArgs[j] = shared, shared
		}
		loInst, _ := ctor.Apply(loArgs)
		hiInst, _ := ctor.Apply(hiArgs)
		sub, super := loInst, hiInst
		if want == soltype.SlotContravariant {
			sub, super = hiInst, loInst
		}
		if hasHardError(c.trialUnderProbe(sub, super)) {
			return i, false
		}
	}
	return 0, true
}

// KindMismatchError reports a constructor that lacks the variance a type-constructor parameter
// declares for one of its arguments, as `Box` would for `F<out _>` when Box is invariant in T.
// Ctor is the constructor as the message names it, and Slot is the index of that argument.
type KindMismatchError struct {
	Ctor string
	Slot int
	Want soltype.SlotVariance
	site ast.Node
}

func (*KindMismatchError) isSolverError()        {}
func (e *KindMismatchError) Span() ast.Span      { return spanOfNode(e.site) }
func (e *KindMismatchError) Related() []ast.Span { return nil }
func (e *KindMismatchError) Message() string {
	want := "covariant"
	if e.Want.Variance() == soltype.SlotContravariant {
		want = "contravariant"
	}
	return "type constructor `" + e.Ctor + "` is not " + want + " in argument " +
		strconv.Itoa(e.Slot+1) + ", which its parameter declares"
}

// countFilled returns the number of non-nil entries in fixed.
func countFilled(fixed []soltype.Type) int {
	n := 0
	for _, f := range fixed {
		if f != nil {
			n++
		}
	}
	return n
}

// resolveTypeApp resolves `F<A, …>` for a reference whose name binds the variable v of a
// type-constructor parameter. It reports a count that differs from the parameter's arity and
// recovers to a fresh variable.
func (c *checker) resolveTypeApp(scope *Scope, ref *ast.TypeRefTypeAnn, v *soltype.TypeVarType, lvl int) soltype.Type {
	slots := c.ctx.ctorParamSlotsOf(v)
	arity := len(slots)
	if len(ref.TypeArgs) != arity {
		c.report(&TypeArgArityMismatchError{
			Ref:      ref,
			Kind:     TypeCtorParamKind,
			Name:     ast.QualIdentToString(ref.Name),
			Required: arity,
			Total:    arity,
			Got:      len(ref.TypeArgs),
		})
		return c.freshAt(lvl)
	}
	args := make([]soltype.Type, arity)
	for i, arg := range ref.TypeArgs {
		resolved, ok := c.resolveTypeAnn(scope, arg, lvl)
		if !ok {
			resolved = c.freshAt(lvl)
		}
		args[i] = resolved
	}
	t := soltype.NewTypeApp(v, args, slots)
	c.recordProv(t, ref, AnnotationType)
	return t
}

// NotATypeCtorError reports an argument written where a type-constructor parameter expects a
// constructor taking Arity arguments, when the argument names no such constructor.
type NotATypeCtorError struct {
	Ann   ast.TypeAnn
	Arity int
}

func (*NotATypeCtorError) isSolverError()        {}
func (e *NotATypeCtorError) Span() ast.Span      { return e.Ann.Span() }
func (e *NotATypeCtorError) Related() []ast.Span { return nil }
func (e *NotATypeCtorError) Message() string {
	return "expected a type constructor taking " + pluralArgs(e.Arity)
}

// pluralArgs renders n as a count of type arguments, as "1 type argument" or "2 type arguments".
func pluralArgs(n int) string {
	if n == 1 {
		return "1 type argument"
	}
	return strconv.Itoa(n) + " type arguments"
}

// reduceTypeApp returns what app denotes once its head is known. A head that is an inference
// variable is known when head matching bound it, which leaves either a constructor or a rigid
// head among its bounds. A constructor gives the instance it names, and a rigid head gives the
// application of that head. It reports false for a head that is still open or that is rigid
// itself.
func (c *Context) reduceTypeApp(app *soltype.TypeAppType) (soltype.Type, bool) {
	v, ok := app.Head.(*soltype.TypeVarType)
	if !ok || c.isRigidHead(v) {
		return nil, false
	}
	head, ok := c.headBoundOf(v)
	if !ok {
		return nil, false
	}
	return soltype.NewTypeApp(head, app.Args, app.Variances), true
}

// headBoundOf returns the first bound of v that can stand as a head: a constructor or a rigid
// head.
func (c *Context) headBoundOf(v *soltype.TypeVarType) (soltype.Type, bool) {
	for _, bounds := range [][]soltype.Type{v.LowerBounds, v.UpperBounds} {
		for _, b := range bounds {
			if _, ok := b.(*soltype.TypeCtorType); ok || c.isRigidHead(b) {
				return b, true
			}
		}
	}
	return nil, false
}

// constrainTypeApp decides a constraint that has an application or a constructor on at least one
// side. It reports false when no rule here applies, so the rest of constrain decides the pair.
// That covers a variable on the other side, which records the whole node as a bound, a union,
// intersection, or complement on the other side, which the lattice rules take apart first, and a
// borrow on the other side, which the borrow rules take apart first.
//
// The rules are:
//
//   - An application whose head is bound to a constructor reduces to the instance it names, and
//     the constraint is decided on that.
//   - Two applications `F<A> <: G<B>` relate when the heads unify and each argument relates to
//     its counterpart by the variance both sides declare for it. A slot the two sides disagree on
//     is invariant.
//   - An application against a named instance, as `F<A> <: Array<number>`, matches heads. The
//     instance splits into a constructor taking as many arguments as the application supplies,
//     which binds F, and the arguments it leaves for those holes, which relate to A by the
//     application's declared variance. The split fixes the leading arguments, so
//     `Promise<number, E>` against `F<A>` binds F to `Promise<_, E>`. Binding an inference head
//     checks the constructor against that declared variance first.
//   - Two constructors relate when they name the same declaration and their fixed arguments are
//     equal.
func (c *Context) constrainTypeApp(sub, super soltype.Type, seen *seenPairs, mutCtx bool) ([]SolverError, bool) {
	if _, ok := sub.(*soltype.TypeVarType); ok {
		return nil, false
	}
	if _, ok := super.(*soltype.TypeVarType); ok {
		return nil, false
	}
	subCtor, subIsCtor := sub.(*soltype.TypeCtorType)
	superCtor, superIsCtor := super.(*soltype.TypeCtorType)
	if subIsCtor || superIsCtor {
		if subIsCtor && superIsCtor && subCtor.Name() == superCtor.Name() && len(subCtor.Fixed) == len(superCtor.Fixed) {
			var errs []SolverError
			for i, f := range subCtor.Fixed {
				g := superCtor.Fixed[i]
				if (f == nil) != (g == nil) {
					return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}, true
				}
				if f != nil {
					errs = append(errs, c.constrainInvariant(f, g, seen, mutCtx)...)
				}
			}
			return errs, true
		}
		return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}, true
	}
	subApp, subIsApp := sub.(*soltype.TypeAppType)
	superApp, superIsApp := super.(*soltype.TypeAppType)
	if subIsApp {
		if reduced, ok := c.reduceTypeApp(subApp); ok {
			return c.constrain(reduced, super, seen, mutCtx), true
		}
	}
	if superIsApp {
		if reduced, ok := c.reduceTypeApp(superApp); ok {
			return c.constrain(sub, reduced, seen, mutCtx), true
		}
	}
	if isLatticeNode(sub) || isLatticeNode(super) {
		return nil, false
	}
	// A borrow against an application is decided by the borrow rules, which peel an owned
	// `mut` value into a bare destination and relate two borrows by their inners.
	if isBorrow(sub) || isBorrow(super) {
		return nil, false
	}
	switch {
	case subIsApp && superIsApp:
		if len(subApp.Args) != len(superApp.Args) {
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}, true
		}
		if errs := c.unifyHeads(subApp.Head, subApp.Variances, superApp.Head, superApp.Variances, seen, mutCtx); len(errs) > 0 {
			if _, kind := errs[0].(*KindMismatchError); kind {
				return errs, true
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}, true
		}
		variances := make([]soltype.SlotVariance, len(subApp.Args))
		for i := range variances {
			variances[i] = subApp.Slot(i).Meet(superApp.Slot(i))
		}
		return c.constrainArgs(subApp.Args, superApp.Args, variances, seen, mutCtx), true
	case subIsApp:
		return c.matchHead(subApp, super, true, seen, mutCtx), true
	case superIsApp:
		return c.matchHead(superApp, sub, false, seen, mutCtx), true
	}
	return nil, false
}

// matchHead decides app against other, a type on the opposite side of the constraint, by
// splitting other into a constructor and the arguments it applies. appIsSub reports whether app
// is the constraint's subtype side.
func (c *Context) matchHead(app *soltype.TypeAppType, other soltype.Type, appIsSub bool, seen *seenPairs, mutCtx bool) []SolverError {
	sub, super := soltype.Type(app), other
	if !appIsSub {
		sub, super = other, app
	}
	ctor, args, ok := splitInstance(other, len(app.Args))
	if !ok {
		return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
	}
	if errs := c.unifyHeads(app.Head, app.Variances, ctor, nil, seen, mutCtx); len(errs) > 0 {
		if _, kind := errs[0].(*KindMismatchError); kind {
			return errs
		}
		return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
	}
	if appIsSub {
		return c.constrainArgs(app.Args, args, app.Variances, seen, mutCtx)
	}
	return c.constrainArgs(args, app.Args, app.Variances, seen, mutCtx)
}

// splitInstance splits a class or alias instance into a constructor taking arity arguments and
// the arguments that fill it. The constructor keeps the instance's leading arguments fixed and
// leaves the last arity as holes. It reports false for any other type and for an instance with
// fewer than arity arguments.
func splitInstance(t soltype.Type, arity int) (*soltype.TypeCtorType, []soltype.Type, bool) {
	var base soltype.Type
	var targs []soltype.Type
	switch inst := t.(type) {
	case *soltype.ClassType:
		cp := *inst
		cp.TypeArgs = nil
		base, targs = &cp, inst.TypeArgs
	case *soltype.AliasType:
		cp := *inst
		cp.TypeArgs = nil
		base, targs = &cp, inst.TypeArgs
	default:
		return nil, nil, false
	}
	if len(targs) < arity {
		return nil, nil, false
	}
	split := len(targs) - arity
	fixed := make([]soltype.Type, len(targs))
	copy(fixed, targs[:split])
	return &soltype.TypeCtorType{Base: base, Fixed: fixed}, targs[split:], true
}

// unifyHeads requires two heads to denote the same constructor. aSlots and bSlots are the
// variances each head's application declares, and nil for a constructor head, which declares
// none. A head that is an inference variable is bound to the other head in both directions,
// which may be a constructor or a rigid head. A rigid head unifies only with itself or with an
// inference variable, so a generic body can pass its own F to another function generic over a
// constructor.
//
// Every binding of an inference head is checked against the slots that head's application
// declares, so an `F<out _>` binds only to a covariant constructor. A constructor is checked by
// ctorSatisfiesSlots. Another head is checked by the slots it declares, and two inference heads
// must each declare what the other asks for, since either may be bound to a constructor later
// through the other's application alone.
func (c *Context) unifyHeads(a soltype.Type, aSlots []soltype.SlotVariance, b soltype.Type, bSlots []soltype.SlotVariance, seen *seenPairs, mutCtx bool) []SolverError {
	if a == b {
		return nil
	}
	aRigid, bRigid := c.isRigidHead(a), c.isRigidHead(b)
	if (aRigid && !c.isInferenceHead(b)) || (bRigid && !c.isInferenceHead(a)) {
		return []SolverError{&CannotConstrainError{Sub: a, Super: b}}
	}
	if err := c.checkHeadBinding(a, aSlots, b, bSlots); err != nil {
		return []SolverError{err}
	}
	if err := c.checkHeadBinding(b, bSlots, a, aSlots); err != nil {
		return []SolverError{err}
	}
	return c.constrainInvariant(a, b, seen, mutCtx)
}

// checkHeadBinding reports the KindMismatchError that binding the head `from` to the head `to`
// would violate, or nil. Only an inference head is bound, so any other `from` passes.
func (c *Context) checkHeadBinding(from soltype.Type, fromSlots []soltype.SlotVariance, to soltype.Type, toSlots []soltype.SlotVariance) SolverError {
	if !c.isInferenceHead(from) {
		return nil
	}
	if ctor, ok := to.(*soltype.TypeCtorType); ok {
		if slot, ok := c.ctorSatisfiesSlots(ctor, fromSlots); !ok {
			return &KindMismatchError{Ctor: soltype.Print(ctor), Slot: slot, Want: fromSlots[slot]}
		}
		return nil
	}
	if slot, ok := slotsSatisfy(toSlots, fromSlots); !ok {
		return &KindMismatchError{Ctor: describe(c.nameRigidParams(to)), Slot: slot, Want: fromSlots[slot]}
	}
	return nil
}

// isInferenceHead reports whether t is an inference variable a head can be bound to, rather than
// a constructor or a rigid head.
func (c *Context) isInferenceHead(t soltype.Type) bool {
	v, ok := t.(*soltype.TypeVarType)
	return ok && !c.isRigidHead(v)
}

// isRigidHead reports whether t is a head no constraint may bind: a skolem, or the variable of a
// type parameter a generic body holds rigid.
func (c *Context) isRigidHead(t soltype.Type) bool {
	switch h := t.(type) {
	case *soltype.SkolemType:
		return true
	case *soltype.TypeVarType:
		_, rigid := c.rigidParams[h]
		return rigid
	}
	return false
}

// constrainInvariant requires a and b to be equal, as `a <: b` and `b <: a`.
func (c *Context) constrainInvariant(a, b soltype.Type, seen *seenPairs, mutCtx bool) []SolverError {
	errs := c.constrain(a, b, seen, mutCtx)
	return append(errs, c.constrain(b, a, seen, mutCtx)...)
}

// constrainArgs relates each sub-side argument in subs to its super-side counterpart in supers by
// the variance variances gives it. A missing entry in variances is invariant, and so is every
// argument under a mutable borrow, where a write through the borrow reaches the argument.
// subs and supers have the same length.
func (c *Context) constrainArgs(subs, supers []soltype.Type, variances []soltype.SlotVariance, seen *seenPairs, mutCtx bool) []SolverError {
	var errs []SolverError
	for i := range subs {
		v := soltype.SlotAt(variances, i)
		if mutCtx {
			v = soltype.SlotInvariant
		}
		switch v {
		case soltype.SlotCovariant:
			errs = append(errs, c.constrain(subs[i], supers[i], seen, mutCtx)...)
		case soltype.SlotContravariant:
			errs = append(errs, c.constrain(supers[i], subs[i], seen, mutCtx)...)
		default:
			errs = append(errs, c.constrainInvariant(subs[i], supers[i], seen, mutCtx)...)
		}
	}
	return errs
}

// isBorrow reports whether t is a RefType.
func isBorrow(t soltype.Type) bool {
	_, ok := t.(*soltype.RefType)
	return ok
}

// isLatticeNode reports whether t is a union, an intersection, or a complement.
func isLatticeNode(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.UnionType, *soltype.IntersectionType, *soltype.NegationType:
		return true
	}
	return false
}

// ctorDefaultUsable reports whether parameter i has a default that a constructor can fix as it
// is. A default naming another of the declaration's parameters is not usable, since that
// parameter may be a hole with no argument to substitute.
func ctorDefaultUsable(params []*soltype.TypeParam, i int) bool {
	if params[i].Default == nil {
		return false
	}
	for _, v := range typeVarsIn(params[i].Default) {
		for _, p := range params {
			if p.Var == v {
				return false
			}
		}
	}
	return true
}

package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// headKind is what a type-constructor parameter declares about the constructors it stands for:
// the variance of each argument, and the bound every instance has to satisfy. A named
// constructor declares nothing, so its headKind is the zero value.
type headKind struct {
	Slots []soltype.SlotVariance
	// Bound is the parameter's declared bound, a TypeCtorType, or nil when it declares none.
	Bound soltype.Type
}

// kindOfApp returns the headKind app's parameter declares. An application that records no bound
// takes its head's when the head is a constructor parameter with one, since a reference written
// before a sibling's bound resolved recorded none.
func (c *Context) kindOfApp(app *soltype.TypeAppType) headKind {
	return headKind{Slots: app.Variances, Bound: c.boundOfApp(app)}
}

// boundOfApp returns the bound app's parameter declares, read from app or, when app records none,
// from its head's parameter.
func (c *Context) boundOfApp(app *soltype.TypeAppType) soltype.Type {
	if app.Bound != nil {
		return app.Bound
	}
	if v, ok := app.Head.(*soltype.TypeVarType); ok {
		return c.ctorParamKindOf(v).Bound
	}
	return nil
}

// resolveTypeCtor resolves ta as a type constructor taking arity arguments. It returns one of:
//
//   - the variable of a constructor parameter in scope, as F in `Functor<F>` inside `<F<_>>`
//   - a TypeCtorType for a named class or alias, as `Array` in `Functor<Array>`
//
// A written `_` marks a hole an application fills, so `Promise<_, FetchError>` is a constructor
// taking one argument. A reference that writes no arguments leaves every parameter a hole, except
// that trailing parameters with a default are filled from it when that brings the hole count down
// to arity. A mismatched count is reported and the argument recovers to a fresh variable.
func (c *checker) resolveTypeCtor(scope *Scope, ta ast.TypeAnn, arity int, lvl int) soltype.Type {
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
		if len(c.ctx.ctorParamSlotsOf(v)) == arity && len(ref.TypeArgs) == 0 {
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
	return ctor
}

// resolveTypeCtorArg resolves ta as the argument of a type-constructor parameter that declares
// want, and queues the check that the argument satisfies want. The check runs once inference
// finishes, since a class's variance is measured from its body.
func (c *checker) resolveTypeCtorArg(scope *Scope, ta ast.TypeAnn, want headKind, lvl int) soltype.Type {
	arg := c.resolveTypeCtor(scope, ta, len(want.Slots), lvl)
	c.queueKindCheck(arg, want, ta)
	return arg
}

// queueKindCheck queues the check that head, a constructor argument written at site, satisfies
// want. A head that failed to resolve was already reported and queues nothing.
func (c *checker) queueKindCheck(head soltype.Type, want headKind, site ast.Node) {
	switch h := head.(type) {
	case *soltype.TypeCtorType:
	case *soltype.TypeVarType:
		if len(c.ctx.ctorParamSlotsOf(h)) == 0 {
			return
		}
	default:
		return
	}
	c.deferredKindChecks = append(c.deferredKindChecks, deferredKindCheck{head: head, want: want, site: site})
}

// deferredKindCheck is one constructor argument waiting to be checked against want, what the
// parameter it was passed to declares. head is a TypeCtorType or a constructor parameter's
// variable.
type deferredKindCheck struct {
	head soltype.Type
	want headKind
	site ast.Node
}

// runDeferredKindChecks runs each check resolveTypeCtorArg queued and clears the queue. It runs
// once every class's variance is final.
func (c *checker) runDeferredKindChecks() {
	pending := c.deferredKindChecks
	c.deferredKindChecks = nil
	for _, k := range pending {
		// A parameter's own declaration is read here rather than when the check was queued,
		// since a sibling's bound may resolve after a reference to it.
		var have headKind
		name := soltype.Print(k.head)
		if v, ok := k.head.(*soltype.TypeVarType); ok {
			have = c.ctx.ctorParamKindOf(v)
			name = headSourceName(k.site)
		}
		if err := c.ctx.headSatisfies(k.head, have, k.want); err != nil {
			completeKindError(err, name, k.site)
			c.report(err)
		}
	}
}

// headSourceName returns the name a constructor argument written at site spells.
func headSourceName(site ast.Node) string {
	if ref, ok := site.(*ast.TypeRefTypeAnn); ok {
		return ast.QualIdentToString(ref.Name)
	}
	return ""
}

// completeKindError sets the constructor name and the reporting node of err, a KindMismatchError
// or a BoundMismatchError. A nil site leaves the node for blameConstraintErrors to set.
func completeKindError(err SolverError, name string, site ast.Node) {
	switch e := err.(type) {
	case *KindMismatchError:
		e.Ctor, e.site = name, site
	case *BoundMismatchError:
		e.Ctor, e.site = name, site
	}
}

// headSatisfies returns the error that standing head where a parameter declaring want is expected
// would report, or nil when head satisfies want. have is what head declares when it is a
// constructor parameter. The error names no constructor and no site, which completeKindError
// fills in.
//
// A named constructor satisfies want when it has each variance want's slots declare and when
// every instance of it satisfies want's bound. A constructor parameter satisfies want when its own
// slots declare at least that variance and its own bound implies want's bound.
func (c *Context) headSatisfies(head soltype.Type, have, want headKind) SolverError {
	if ctor, ok := head.(*soltype.TypeCtorType); ok {
		if slot, ok := c.ctorSatisfiesSlots(ctor, want.Slots); !ok {
			return &KindMismatchError{Slot: slot, Want: want.Slots[slot]}
		}
		if !c.ctorSatisfiesBound(ctor, want.Bound) {
			return &BoundMismatchError{Bound: soltype.Print(want.Bound)}
		}
		return nil
	}
	if slot, ok := slotsSatisfy(have.Slots, want.Slots); !ok {
		return &KindMismatchError{Slot: slot, Want: want.Slots[slot]}
	}
	if want.Bound == nil {
		return nil
	}
	haveBound, ok := have.Bound.(*soltype.TypeCtorType)
	if !ok || !c.ctorSatisfiesBound(haveBound, want.Bound) {
		return &BoundMismatchError{Bound: soltype.Print(want.Bound)}
	}
	return nil
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

// ctorSatisfiesBound reports whether every instance of ctor satisfies bound, a TypeCtorType of the
// same arity. A nil bound is satisfied by any constructor. The check applies both to the same
// fresh skolems and asks whether `ctor<x> <: bound<x>` holds under a probe.
func (c *Context) ctorSatisfiesBound(ctor *soltype.TypeCtorType, bound soltype.Type) bool {
	if bound == nil {
		return true
	}
	boundCtor, ok := bound.(*soltype.TypeCtorType)
	if !ok || boundCtor.Arity() != ctor.Arity() {
		return false
	}
	args := make([]soltype.Type, ctor.Arity())
	for i := range args {
		args[i] = c.freshSkolem("x")
	}
	inst, _ := ctor.Apply(args)
	boundInst, _ := boundCtor.Apply(args)
	return !hasHardError(c.trialUnderProbe(inst, boundInst))
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

// BoundMismatchError reports a constructor whose instances do not satisfy the bound a
// type-constructor parameter declares, as `Box` would for `F<_>: Sized` when Box has no `size`.
// Ctor and Bound are the constructor and the bound as the message names them.
type BoundMismatchError struct {
	Ctor  string
	Bound string
	site  ast.Node
}

func (*BoundMismatchError) isSolverError()        {}
func (e *BoundMismatchError) Span() ast.Span      { return spanOfNode(e.site) }
func (e *BoundMismatchError) Related() []ast.Span { return nil }
func (e *BoundMismatchError) Message() string {
	return "type constructor `" + e.Ctor + "` does not satisfy the bound `" + e.Bound + "` its parameter declares"
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
	t := soltype.NewTypeApp(v, args, slots, c.ctx.ctorParamKindOf(v).Bound)
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
	// A rigid head carries its own parameter's bound, which implies the one app records.
	bound := app.Bound
	if hv, ok := head.(*soltype.TypeVarType); ok {
		if own := c.ctorParamKindOf(hv).Bound; own != nil {
			bound = own
		}
	}
	return soltype.NewTypeApp(head, app.Args, app.Variances, bound), true
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
//   - An application whose head is rigid and whose parameter declares a bound relates to any
//     other type through the bound's instance, so `F<A> <: T` holds when `Sized<A> <: T` does
//     under `F<_>: Sized`.
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
	// A rigid head stands for every constructor satisfying its parameter's bound, so what is read
	// from `F<A>` is what every instance of the bound offers. `F<A> <: T` holds when
	// `Bound<A> <: T` does.
	if subIsApp && !superIsApp && c.isRigidHead(subApp.Head) {
		if bound, ok := c.boundOfApp(subApp).(*soltype.TypeCtorType); ok {
			if inst, ok := bound.Apply(subApp.Args); ok {
				return c.constrain(inst, super, seen, mutCtx), true
			}
		}
	}
	switch {
	case subIsApp && superIsApp:
		if len(subApp.Args) != len(superApp.Args) {
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}, true
		}
		if errs := c.unifyHeads(subApp.Head, c.kindOfApp(subApp), superApp.Head, c.kindOfApp(superApp), seen, mutCtx); len(errs) > 0 {
			return errs, true
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
	if errs := c.unifyHeads(app.Head, c.kindOfApp(app), ctor, headKind{}, seen, mutCtx); len(errs) > 0 {
		switch errs[0].(type) {
		case *KindMismatchError, *BoundMismatchError:
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

// unifyHeads requires two heads to denote the same constructor. aKind and bKind are what each
// head's application declares, and the zero headKind for a constructor head, which declares
// nothing. A head that is an inference variable is bound to the other head in both directions,
// which may be a constructor or a rigid head. A rigid head unifies only with itself or with an
// inference variable, so a generic body can pass its own F to another function generic over a
// constructor.
//
// Every binding of an inference head is checked by headSatisfies against what that head's
// application declares, so an `F<out _>` binds only to a covariant constructor. Two inference
// heads must each declare what the other asks for, since either may be bound to a constructor
// later through the other's application alone.
func (c *Context) unifyHeads(a soltype.Type, aKind headKind, b soltype.Type, bKind headKind, seen *seenPairs, mutCtx bool) []SolverError {
	if a == b {
		return nil
	}
	aRigid, bRigid := c.isRigidHead(a), c.isRigidHead(b)
	if (aRigid && !c.isInferenceHead(b)) || (bRigid && !c.isInferenceHead(a)) {
		return []SolverError{&CannotConstrainError{Sub: a, Super: b}}
	}
	if err := c.checkHeadBinding(a, aKind, b, bKind); err != nil {
		return []SolverError{err}
	}
	if err := c.checkHeadBinding(b, bKind, a, aKind); err != nil {
		return []SolverError{err}
	}
	return c.constrainInvariant(a, b, seen, mutCtx)
}

// checkHeadBinding returns the error binding the head `from` to the head `to` would report, or
// nil. fromKind and toKind are what each head's application declares. Only an inference head is bound, so any other `from` passes.
func (c *Context) checkHeadBinding(from soltype.Type, fromKind headKind, to soltype.Type, toKind headKind) SolverError {
	if !c.isInferenceHead(from) {
		return nil
	}
	err := c.headSatisfies(to, toKind, fromKind)
	if err == nil {
		return nil
	}
	name := soltype.Print(to)
	if _, ok := to.(*soltype.TypeCtorType); !ok {
		name = describe(c.nameRigidParams(to))
	}
	completeKindError(err, name, nil)
	return err
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

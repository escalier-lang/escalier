package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// memberHalf is one direction of access to a named member: the read `a.f` performs, or the
// write `a.f = …` performs. An override is checked one half at a time, because a class
// splits the two across separate members whenever it declares a getter/setter pair, and a
// `readonly` field carries the read alone.
type memberHalf int

const (
	readHalf memberHalf = iota
	writeHalf
)

// subtypeGoal is one obligation a redeclared member owes the member it overrides. An access
// half contributes one goal for the value it carries and one for what reaching it may raise.
//
// sub and super are the two operands constrain is called with. A write obligation is
// contravariant, so there the inherited member's type is the sub operand. shownSub and
// shownSuper are the same two types held the other way, subclass first, which is the order a
// diagnostic names them in. position says which of a member's types they are, so the message
// can distinguish a rejected value type from a rejected `throws`. subElem is the redeclared
// member the diagnostic is blamed on, and owner the ancestor declaring the inherited one.
type subtypeGoal struct {
	sub, super           soltype.Type
	shownSub, shownSuper soltype.Type
	position             string
	subElem              soltype.ObjTypeElem
	owner                *soltype.ClassType
}

// memberBlameKey names one instance member of a class declaration, so a diagnostic about
// that member can be blamed on the source it was written at. A getter and a setter may
// share a name, so the setter half is keyed apart from every other kind.
type memberBlameKey struct {
	name   string
	setter bool
}

// pendingOverrideCheck is one subclass waiting to have its members checked against the
// ones they override. def and self are the class's registered definition and nominal
// handle, and decl the declaration a diagnostic is blamed on.
type pendingOverrideCheck struct {
	def  *ClassDef
	self *soltype.ClassType
	decl *ast.ClassDecl
}

// queueInheritedMemberCheck records a class whose `extends` edge has been resolved, to be
// checked once every class the module declares is inferred.
//
// The check cannot run inside inferClassDecl. A class's superclass edge and body are filled
// in at the class's VALUE key, while an `extends` clause registers a dependency on the
// superclass's TYPE key alone. A subclass's value key is therefore free to be inferred
// before its superclass's. Given `class Dog extends Pet` and `class Pet extends Animal`, the
// driver reaches Dog while Pet still carries no superclass edge and no members. Running
// every queued check after the last component removes that hazard, since by then each
// ancestor's edge and body are final.
func (c *checker) queueInheritedMemberCheck(def *ClassDef, self *soltype.ClassType, decl *ast.ClassDecl) {
	if def.Body == nil || len(def.Supers) == 0 {
		return
	}
	c.pendingOverrides = append(c.pendingOverrides, pendingOverrideCheck{def: def, self: self, decl: decl})
}

// checkQueuedInheritedMembers runs every queued override check and clears the queue. The
// module and script drivers call it once, after the last declaration is inferred.
func (c *checker) checkQueuedInheritedMembers() {
	pending := c.pendingOverrides
	c.pendingOverrides = nil
	for _, p := range pending {
		c.checkInheritedMembers(p.def, p.self, p.decl)
	}
}

// checkInheritedMembers checks every member a class declares against the same-named member
// its superclass chain declares, and reports the ones an instance of the class cannot stand
// in for. The nominal subtype rule decides `Dog <: Animal` on the declared `extends` edge
// alone, so without this check a subclass can contradict its own superclass edge:
//
//	class Animal { pos: {x: number}, … }
//	class Dog extends Animal { pos: {x: number, y: number}, … }
//
// Reading `pos` off a `Dog` through an `Animal`-typed binding would then hand out a
// `{x: number, y: number}` typed as `{x: number}`, which exactness makes a real mismatch
// rather than a widening.
//
// A member the class declares at a compatible type reports nothing. That covers the common
// case of redeclaring an inherited field at exactly its inherited type. A name the
// superclass chain does not carry is new rather than an override, so it is skipped.
//
// Every class body is frozen by the time this runs, so each member is compared at the type
// member lookup will read rather than at the fresh variable a field held before the
// constructor assigned it.
func (c *checker) checkInheritedMembers(def *ClassDef, self *soltype.ClassType, decl *ast.ClassDecl) {
	if def.Body == nil || len(def.Supers) == 0 {
		return
	}
	blame := instanceMemberNodes(decl)
	rigid := c.ctx.skolemizeClassParams(def)
	checked := set.NewSet[string]()
	for _, elem := range def.Body.Elems {
		name := soltype.ObjElemName(elem)
		if name == "" || checked.Contains(name) {
			continue
		}
		checked.Add(name)
		c.checkOverriddenName(def, self, name, blame, rigid)
	}
}

// checkOverriddenName checks one name the class declares against the same name on its
// superclass chain, and reports at most one diagnostic for it.
//
// Each access half is checked on its own. The superclass chain fixes what an instance of
// the class must still support, so a half the chain does not expose leaves nothing to keep
// and is skipped. A half it does expose has to survive the redeclaration in two ways:
//
//  1. The class has to keep offering that half at all. A redeclaration shadows the
//     inherited member rather than merging with it, so `class Dog extends Animal { readonly
//     pos: … }` over a writable `Animal.pos` leaves an `Animal`-typed reference able to
//     write a field a `Dog` refuses. The same goes for a subclass that redeclares one half
//     of an inherited getter/setter pair and drops the other.
//  2. The type it offers has to fit. A read is covariant and a write contravariant, the
//     ordinary override rule.
//
// The two are checked in that order, over both halves, so a member whose form disagrees is
// reported as a form mismatch rather than through whichever type comparison the mismatch
// happens to fail first.
func (c *checker) checkOverriddenName(
	def *ClassDef,
	self *soltype.ClassType,
	name string,
	blame map[memberBlameKey]ast.Node,
	rigid *typeSubst,
) {
	var goals []subtypeGoal
	for _, half := range []memberHalf{readHalf, writeHalf} {
		superHalf, owner, inherited := c.inheritedHalf(def, self, name, half)
		if !inherited {
			continue
		}
		subHalf, declared := declaredHalf(def.Body, name, half)
		if !declared || formWeakens(subHalf, superHalf) {
			// The class either does not offer this half at all, or offers it on terms the
			// inherited member does not impose. No type comparison applies to that, so name
			// the two forms instead.
			c.report(&OverrideFormMismatchError{
				Member:     name,
				Class:      self.Name,
				SuperClass: owner.Name,
				Form:       memberForm(firstNamed(def.Body, name)),
				SuperForm:  memberForm(superHalf),
				Node:       overrideBlame(blame, name, subHalf),
			})
			return
		}
		goals = append(goals, halfGoals(subHalf, superHalf, half, owner)...)
	}
	for _, goal := range goals {
		// The trial rolls its bounds back, so measuring an override records nothing on the
		// types the class registered.
		if !hasHardError(c.ctx.trialUnderProbe(rigid.apply(goal.sub), rigid.apply(goal.super))) {
			continue
		}
		c.report(&IncompatibleOverrideError{
			Member:     name,
			Class:      self.Name,
			SuperClass: goal.owner.Name,
			Position:   goal.position,
			// The rendered types carry the skolems too, so a member typed through a class
			// type parameter reads under that parameter's name rather than as a bare
			// variable id.
			SubType:   rigid.apply(goal.shownSub),
			SuperType: rigid.apply(goal.shownSuper),
			Node:      overrideBlame(blame, name, goal.subElem),
		})
		return // one diagnostic per member, whichever obligation rejected first
	}
}

// halfGoals returns the subtype obligations a redeclared member owes the inherited member
// serving the same access half. A read is covariant in the value it yields and a write
// contravariant in the value it accepts. What reaching either raises is covariant in both
// cases, since a `throws` flows out to the caller whether it came from a read or a write.
//
// Each goal carries the redeclared member it came from and the ancestor declaring the
// inherited one, so a rejection is rendered against the half that produced it.
func halfGoals(subElem, superElem soltype.ObjTypeElem, half memberHalf, owner *soltype.ClassType) []subtypeGoal {
	value := subtypeGoal{
		shownSub: halfType(subElem, half), shownSuper: halfType(superElem, half),
		position: "type", subElem: subElem, owner: owner,
	}
	value.sub, value.super = value.shownSub, value.shownSuper
	if half == writeHalf {
		value.sub, value.super = value.shownSuper, value.shownSub
	}
	throws := subtypeGoal{
		shownSub: elemThrows(subElem), shownSuper: elemThrows(superElem),
		position: "throws type", subElem: subElem, owner: owner,
	}
	throws.sub, throws.super = throws.shownSub, throws.shownSuper
	return []subtypeGoal{value, throws}
}

// inheritedHalf finds the member on def's superclass chain that serves one access half of
// name, projected to the arguments sub writes. It returns that member and the ancestor
// instance declaring it, or inherited=false when no ancestor offers the half.
//
// Each `extends` edge is re-expressed at sub's arguments through substituteSuperArgs before
// the walk descends into it, so `class Dog<T> extends Animal<T>` compares its own members
// against an Animal whose `T` is Dog's `T`. visited holds the ancestor names already
// reached, bounding the walk on a cyclic hierarchy the same way constrainNominalWalk does.
func (c *checker) inheritedHalf(
	def *ClassDef,
	sub *soltype.ClassType,
	name string,
	half memberHalf,
) (soltype.ObjTypeElem, *soltype.ClassType, bool) {
	return c.inheritedHalfWalk(def, sub, name, half, set.NewSet[string]())
}

func (c *checker) inheritedHalfWalk(
	def *ClassDef,
	sub *soltype.ClassType,
	name string,
	half memberHalf,
	visited set.Set[string],
) (soltype.ObjTypeElem, *soltype.ClassType, bool) {
	for _, superType := range def.Supers {
		superInstance := substituteSuperArgs(def, sub, superType)
		if visited.Contains(superInstance.Name) {
			continue
		}
		visited.Add(superInstance.Name)
		superDef, ok := c.ctx.classDef(superInstance.Name)
		if !ok || superDef.Body == nil {
			continue
		}
		if member, found := declaredHalf(superDef.Body, name, half); found {
			return c.projectClassMember(superDef, superInstance, member), superInstance, true
		}
		if member, owner, found := c.inheritedHalfWalk(superDef, superInstance, name, half, visited); found {
			return member, owner, true
		}
	}
	return nil, nil, false
}

// declaredHalf returns the member of obj that serves one access half of name. An accessor
// declared for that half wins over a field or method sharing the name. That is the same
// tie-break ObjectType.ReadMember and WriteMember apply. found is false when no member named
// name serves the half.
func declaredHalf(obj *soltype.ObjectType, name string, half memberHalf) (soltype.ObjTypeElem, bool) {
	var found soltype.ObjTypeElem
	for _, elem := range obj.Elems {
		if soltype.ObjElemName(elem) != name || !servesHalf(elem, half) {
			continue
		}
		if isAccessor(elem) {
			return elem, true
		}
		if found == nil {
			found = elem
		}
	}
	return found, found != nil
}

// servesHalf reports whether a member provides one access half. A field provides the read
// always and the write unless it is `readonly`. A method and a getter provide the read
// alone, and a setter the write alone.
func servesHalf(elem soltype.ObjTypeElem, half memberHalf) bool {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		return half == readHalf || !elem.Readonly
	case *soltype.MethodElem:
		return half == readHalf
	case *soltype.GetterElem:
		return half == readHalf
	case *soltype.SetterElem:
		return half == writeHalf
	}
	// An index signature, a spread, or a constructor is not a named instance member an
	// `extends` edge can override.
	return false
}

// isAccessor reports whether a member is one half of a getter/setter pair.
func isAccessor(elem soltype.ObjTypeElem) bool {
	switch elem.(type) {
	case *soltype.GetterElem, *soltype.SetterElem:
		return true
	}
	return false
}

// formWeakens reports whether a redeclared member promises less, or demands more, than the
// inherited one in a way no subtype obligation covers. Two cases reach it.
//
//   - The redeclared member is an optional field and the inherited one is not. The
//     superclass view reads the member as always present, while the subclass may omit it.
//   - The redeclared member takes a `mut self` receiver and the inherited one takes a plain
//     `self`. The inherited member is reachable from an immutable superclass reference, and
//     the redeclared one is not.
func formWeakens(subElem, superElem soltype.ObjTypeElem) bool {
	if isOptional(subElem) && !isOptional(superElem) {
		return true
	}
	return receiverMut(subElem) && !receiverMut(superElem)
}

// isOptional reports whether a member may be absent, which only a `f?: T` field can be.
func isOptional(elem soltype.ObjTypeElem) bool {
	prop, ok := elem.(*soltype.PropertyElem)
	return ok && prop.Optional
}

// receiverMut reports whether reaching a member needs a mutable reference to the instance.
// A `mut self` receiver on a method, getter, or setter is what demands one. A field carries
// no receiver, so it reads as false.
func receiverMut(elem soltype.ObjTypeElem) bool {
	switch elem := elem.(type) {
	case *soltype.MethodElem:
		return len(elem.Signatures) > 0 && selfParamMut(elem.Signatures[0].SelfParam)
	case *soltype.GetterElem:
		return selfParamMut(elem.SelfParam)
	case *soltype.SetterElem:
		return selfParamMut(elem.SelfParam)
	}
	return false
}

// halfType returns the type one access half of a member carries: the value a read yields or
// the value a write accepts. A method's read yields its signature with the receiver dropped,
// which is the callable value `a.f` evaluates to.
func halfType(elem soltype.ObjTypeElem, half memberHalf) soltype.Type {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		return elem.Type
	case *soltype.GetterElem:
		return elem.Type
	case *soltype.SetterElem:
		return elem.Param
	case *soltype.MethodElem:
		switch len(elem.Signatures) {
		case 0:
		case 1:
			return callableView(elem.Signatures[0])
		default:
			// An overload set reads as the intersection of its arms, matching what
			// memberReadContribution hands a caller. Comparing the intersections rather than
			// arm against arm is what keeps the check independent of the order the arms are
			// written in.
			arms := make([]soltype.Type, len(elem.Signatures))
			for i, sig := range elem.Signatures {
				arms[i] = callableView(sig)
			}
			return &soltype.IntersectionType{Types: arms}
		}
	}
	return &soltype.ErrorType{}
}

// elemThrows returns the type reaching a member may raise. Only an accessor carries one, so
// a field and a method read as `never`, which any `throws` position admits.
func elemThrows(elem soltype.ObjTypeElem) soltype.Type {
	switch elem := elem.(type) {
	case *soltype.GetterElem:
		return elem.ThrowsOrNever()
	case *soltype.SetterElem:
		return elem.ThrowsOrNever()
	}
	return &soltype.NeverType{}
}

// memberForm names the form a member takes, so a diagnostic about two members whose forms
// disagree can say what each one is. A field's form spells out its `readonly` and optional
// modifiers, since dropping either is one of the disagreements the check reports.
func memberForm(elem soltype.ObjTypeElem) string {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		switch {
		case elem.Optional && elem.Readonly:
			return "an optional readonly field"
		case elem.Optional:
			return "an optional writable field"
		case elem.Readonly:
			return "a readonly field"
		}
		return "a writable field"
	case *soltype.MethodElem:
		return mutReceiverForm("a method", elem)
	case *soltype.GetterElem:
		return mutReceiverForm("a getter", elem)
	case *soltype.SetterElem:
		// A setter writes, so its receiver is always `mut self` and saying so adds nothing.
		return "a setter"
	}
	return "a member"
}

// mutReceiverForm appends the receiver to a member's form when reaching it needs a mutable
// reference, so a diagnostic about two members that differ only there says which is which.
func mutReceiverForm(base string, elem soltype.ObjTypeElem) string {
	if receiverMut(elem) {
		return base + " taking `mut self`"
	}
	return base
}

// firstNamed returns the first member of obj called name, the member a diagnostic falls
// back to naming when the class offers no member for the half under discussion. The caller
// reaches it only for a name obj carries, so the nil return is unreachable in practice and
// renders as the generic form.
func firstNamed(obj *soltype.ObjectType, name string) soltype.ObjTypeElem {
	for _, elem := range obj.Elems {
		if soltype.ObjElemName(elem) == name {
			return elem
		}
	}
	return nil
}

// overrideBlame returns the source node an override diagnostic points at: the declaration
// of the member under discussion, falling back to the other half's declaration when the
// class offers no member for that half. It returns nil when the class declares the name in
// neither form, which leaves the diagnostic on the class declaration's own span.
func overrideBlame(blame map[memberBlameKey]ast.Node, name string, elem soltype.ObjTypeElem) ast.Node {
	_, isSetter := elem.(*soltype.SetterElem)
	if node, ok := blame[memberBlameKey{name: name, setter: isSetter}]; ok {
		return node
	}
	return blame[memberBlameKey{name: name, setter: !isSetter}]
}

// instanceMemberNodes maps each instance member a class declaration writes to the source
// node that declares it, so an override diagnostic points at the member rather than at the
// whole class. A static member is left out, since `extends` relates instances.
func instanceMemberNodes(decl *ast.ClassDecl) map[memberBlameKey]ast.Node {
	nodes := map[memberBlameKey]ast.Node{}
	for _, elem := range decl.Body {
		var key ast.ObjKey
		var static bool
		setter := false
		switch elem := elem.(type) {
		case *ast.FieldElem:
			key, static = elem.Name, elem.Static
		case *ast.MethodElem:
			key, static = elem.Name, elem.Static
		case *ast.GetterElem:
			key, static = elem.Name, elem.Static
		case *ast.SetterElem:
			key, static, setter = elem.Name, elem.Static, true
		default:
			continue
		}
		if name, ok := objKeyName(key); ok && !static {
			nodes[memberBlameKey{name: name, setter: setter}] = elem
		}
	}
	return nodes
}

// skolemizeClassParams returns a substitution replacing a class's own type-parameter
// variables with fresh skolems, or nil for a non-generic class. An override obligation
// checked through it has to hold for every instantiation of the class rather than being
// satisfied by recording a bound on the parameter. For
//
//	class Box<T> { value: T, … }
//	class StrBox<T> extends Box<T> { value: string, … }
//
// the obligation is `string <: T`. Against the bare parameter variable that records `string`
// as a lower bound of `T` and reports nothing. Against a skolem it is rejected, which is
// what a `StrBox<number>` read through `Box<number>` needs it to be.
//
// Each parameter's declared constraint becomes its skolem's upper bound, seeded through the
// same substitution so a bound naming a sibling parameter reaches that sibling's skolem.
// This mirrors what skolemizeFuncBinder does for a function's own binder.
func (c *Context) skolemizeClassParams(def *ClassDef) *typeSubst {
	if len(def.TypeParams) == 0 {
		return nil
	}
	sks := make([]*soltype.SkolemType, len(def.TypeParams))
	args := make([]soltype.Type, len(def.TypeParams))
	for i, tp := range def.TypeParams {
		sks[i] = c.freshSkolem(tp.Name)
		args[i] = sks[i]
	}
	subst := newTypeSubst(def.TypeParams, args, nil, nil)
	for i, tp := range def.TypeParams {
		// resolveTypeParams records at most one upper bound per parameter, itself an
		// IntersectionType for a `<T: A & B>` bound, so the first bound is the whole
		// declared constraint.
		if len(tp.Var.UpperBounds) > 0 {
			sks[i].Upper = tp.Var.UpperBounds[0].Accept(subst, soltype.Positive)
		}
	}
	return subst
}

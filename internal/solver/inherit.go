package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// memberHalf is one direction of access to a named member: the read `a.f` performs, or the
// write `a.f = …` performs. An override is checked one half at a time, since a getter/setter
// pair splits the two across separate members and a `readonly` field carries the read alone.
type memberHalf int

const (
	readHalf memberHalf = iota
	writeHalf
)

// subtypeGoal is one obligation a redeclared member owes the member it overrides. An access
// half contributes one goal for the value it carries and one for what reaching it may raise.
//
// sub and super are constrain's operands, so a contravariant write obligation holds the
// inherited type as sub. shownSub and shownSuper are that pair in declaration order, subclass
// first, which is how a diagnostic names them. position distinguishes a rejected value type
// from a rejected `throws`. owner is the ancestor declaring the inherited member.
type subtypeGoal struct {
	sub, super           soltype.Type
	shownSub, shownSuper soltype.Type
	position             string
	subElem              soltype.ObjTypeElem
	owner                *soltype.ClassType
}

// memberBlameKey names one instance member of a class declaration, so a diagnostic can be
// blamed on the source it was written at. A getter and a setter may share a name, so the
// setter half is keyed apart from every other kind.
type memberBlameKey struct {
	name   string
	setter bool
}

// pendingOverrideCheck is one subclass waiting to have its members checked against the ones
// they override.
type pendingOverrideCheck struct {
	def  *ClassDef
	self *soltype.ClassType
	decl *ast.ClassDecl
}

// queueInheritedMemberCheck records a class whose `extends` edge has been resolved, to be
// checked once every class the module declares is inferred.
//
// The check cannot run inside inferClassDecl. A class's superclass edge and body are filled
// in at its VALUE key, while an `extends` clause registers a dependency on the superclass's
// TYPE key alone, so a subclass's value key is free to be inferred first. Given `class Dog
// extends Pet` and `class Pet extends Animal`, the driver reaches Dog while Pet still carries
// no edge and no members. Running the queue after the last component removes that hazard,
// since by then every ancestor is final.
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
// alone, so this is what stops a subclass contradicting that edge. IncompatibleOverrideError
// spells out the case it catches. A name the chain does not carry is new rather than an
// override, so it is skipped.
//
// Telling a redeclaration from an inherited member rests on ClassDef.Body holding the class's
// OWN declarations, with everything inherited reached by walking Supers. A body carrying its
// ancestors' members too would make each inherited member look like a redeclaration of
// itself, so the whole-body views that need inheritance walk the chain instead.
//
// Every class body is frozen by the time this runs, so a member is compared at the type
// lookup will read rather than at the variable a field held before the constructor ran.
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
// superclass chain, reporting at most one diagnostic for it.
//
// Each access half is checked on its own. A half the chain does not expose leaves nothing to
// keep, so it is skipped. A half it does expose has to survive the redeclaration twice over:
//
//  1. The class has to keep offering it. A redeclaration shadows the inherited member rather
//     than merging with it, so a `readonly` field over a writable one, or one half of an
//     inherited accessor pair, drops an access the superclass view still performs.
//  2. The type it offers has to fit, covariantly for a read and contravariantly for a write.
//
// Every form check runs before any type comparison, so a member whose form disagrees is
// reported as a form mismatch rather than through whichever comparison it happens to fail.
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
			// the two forms instead. The form is read off whichever member the class declares
			// under this name, since the half under discussion may have no member at all.
			own, _ := def.Body.Member(name)
			c.report(&OverrideFormMismatchError{
				Member:     name,
				Class:      self.Name,
				SuperClass: owner.Name,
				Form:       memberForm(own),
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
// contravariant in the value it accepts. What reaching either raises is covariant either way,
// since a `throws` flows out to the caller. Each goal carries the members it came from, so a
// rejection is rendered against the half that produced it.
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
// name, projected to the arguments sub writes, along with the ancestor declaring it.
//
// Each `extends` edge is re-expressed at sub's arguments through substituteSuperArgs before
// the walk descends into it, so `class Dog<T> extends Animal<T>` compares against an Animal
// whose `T` is Dog's `T`. The visited set bounds the walk on a cyclic hierarchy, the way
// constrainNominalWalk does.
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
			return projectClassMember(superDef, superInstance, member), superInstance, true
		}
		if member, owner, found := c.inheritedHalfWalk(superDef, superInstance, name, half, visited); found {
			return member, owner, true
		}
	}
	return nil, nil, false
}

// declaredHalf returns the member of obj that serves one access half of name, or found=false
// when none does. An accessor declared for that half wins over a field or method sharing the
// name, the same tie-break ObjectType.ReadMember and WriteMember apply.
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
// always and the write unless it is `readonly`. A method and a getter provide the read alone,
// and a setter the write alone.
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
	// An index signature, a spread, and a constructor are not members an override can name.
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
// inherited one in a way no subtype obligation covers:
//
//   - it is an optional field where the inherited one is required, so it may be absent while
//     the superclass view reads it as always present;
//   - it takes a `mut self` receiver where the inherited one takes a plain `self`, so an
//     immutable superclass reference can no longer reach it.
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

// receiverMut reports whether reaching a member needs a mutable reference to the instance. A
// `mut self` receiver on a method, getter, or setter demands one, and a field carries no
// receiver at all.
//
// One `mut self` arm makes a whole overload set demand a mutable reference. buildMemberSigs
// rejects a set whose arms disagree but appends the offending arm anyway, so reading a single
// arm would answer by declaration order on exactly the input that already disagrees.
func receiverMut(elem soltype.ObjTypeElem) bool {
	switch elem := elem.(type) {
	case *soltype.MethodElem:
		for _, sig := range elem.Signatures {
			if selfParamMut(sig.SelfParam) {
				return true
			}
		}
		return false
	case *soltype.GetterElem:
		return selfParamMut(elem.SelfParam)
	case *soltype.SetterElem:
		return selfParamMut(elem.SelfParam)
	}
	return false
}

// halfType returns the type one access half of a member carries: the value a read yields or
// the value a write accepts. A method's read yields its signature with the receiver dropped,
// the callable value `a.f` evaluates to.
func halfType(elem soltype.ObjTypeElem, half memberHalf) soltype.Type {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		return elem.Type
	case *soltype.GetterElem:
		return elem.Type
	case *soltype.SetterElem:
		return elem.Param
	case *soltype.MethodElem:
		return methodReadType(elem)
	}
	return &soltype.ErrorType{}
}

// elemThrows returns the type reaching a member may raise. Only an accessor carries one, so a
// field and a method read as `never`, which any `throws` position admits.
func elemThrows(elem soltype.ObjTypeElem) soltype.Type {
	switch elem := elem.(type) {
	case *soltype.GetterElem:
		return elem.ThrowsOrNever()
	case *soltype.SetterElem:
		return elem.ThrowsOrNever()
	}
	return &soltype.NeverType{}
}

// memberForm names the form a member takes, so a diagnostic about two disagreeing members can
// say what each one is. A field's form spells out its `readonly` and optional modifiers, since
// dropping either is one of the disagreements reported.
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

// mutReceiverForm names the receiver when reaching a member needs a mutable reference, so two
// members differing only there do not render alike.
func mutReceiverForm(base string, elem soltype.ObjTypeElem) string {
	if receiverMut(elem) {
		return base + " taking `mut self`"
	}
	return base
}

// overrideBlame returns the source node an override diagnostic points at, the declaration of
// the member under discussion. It falls back to the other half's declaration, and then to nil,
// which leaves the diagnostic on the zero span.
func overrideBlame(blame map[memberBlameKey]ast.Node, name string, elem soltype.ObjTypeElem) ast.Node {
	_, isSetter := elem.(*soltype.SetterElem)
	if node, ok := blame[memberBlameKey{name: name, setter: isSetter}]; ok {
		return node
	}
	return blame[memberBlameKey{name: name, setter: !isSetter}]
}

// instanceMemberNodes maps each instance member a class declaration writes to the node that
// declares it, so an override diagnostic points at the member rather than the whole class. A
// static member is left out, since `extends` relates instances.
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

// skolemizeClassParams returns skolemizeParams over a class's own type parameters, or nil for
// a non-generic class so the result can be applied through typeSubst.apply either way. An
// obligation checked through it has to hold for every instantiation rather than being
// satisfied by recording a bound. Given
//
//	class Box<T> { value: T, … }
//	class StrBox<T> extends Box<T> { value: string, … }
//
// the obligation is `string <: T`. Against the bare parameter variable that records `string`
// as a lower bound and reports nothing. Against a skolem it is rejected, which is what a
// `StrBox<number>` read through `Box<number>` needs.
func (c *Context) skolemizeClassParams(def *ClassDef) *typeSubst {
	if len(def.TypeParams) == 0 {
		return nil
	}
	return c.skolemizeParams(def.TypeParams)
}

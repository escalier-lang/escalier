package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// pendingImplementsCheck is one class waiting to be checked against its `implements` clause.
// ownNames holds the names the class declares itself. On a `declare` class the body also
// carries the members the clause contributed, and those are left out of the comparison.
type pendingImplementsCheck struct {
	def      *ClassDef
	self     *soltype.ClassType
	decl     *ast.ClassDecl
	targets  []implementsTarget
	ownNames set.Set[string]
}

// queueImplementsCheck records a class with an `implements` clause, to be checked once every
// class the module declares is inferred. ownNames is the set of names the class body declared
// before addImplementedMembers ran.
func (c *checker) queueImplementsCheck(
	def *ClassDef,
	self *soltype.ClassType,
	decl *ast.ClassDecl,
	targets []implementsTarget,
	ownNames set.Set[string],
) {
	if def.Body == nil || len(targets) == 0 {
		return
	}
	c.pendingImplements = append(c.pendingImplements, pendingImplementsCheck{
		def: def, self: self, decl: decl, targets: targets, ownNames: ownNames,
	})
}

// checkQueuedImplements runs every queued `implements` check and clears the queue. The module
// and script drivers call it once, after the last declaration is inferred.
func (c *checker) checkQueuedImplements() {
	pending := c.pendingImplements
	c.pendingImplements = nil
	for _, p := range pending {
		c.checkImplements(p)
	}
}

// checkImplements checks a class against every entry of its `implements` clause and reports
// each member the class does not provide in a form the entry accepts.
//
// The keyword means two things. A class with a body has to provide every member the entry
// declares, so a missing member is reported. A `declare` class takes a member it does not
// declare from the entry instead, so only a member it does declare is compared. In both cases
// a member the class inherits through `extends` counts as one it provides.
//
// Every comparison runs with the class's type parameters held rigid, the way the override
// check holds them, so `class Box<T> implements HasValue<string> { value: T }` is rejected
// rather than satisfied by recording `string` as a bound on `T`.
func (c *checker) checkImplements(p pendingImplementsCheck) {
	view := c.implementingView(p)
	rigid := c.ctx.skolemizeClassParams(p.def)
	for _, target := range p.targets {
		ic := implementsComparison{
			c:         c,
			view:      view,
			rigid:     rigid,
			declare:   p.decl.Declare(),
			className: p.decl.Name.Name,
			ifaceName: ast.QualIdentToString(target.ref.Name),
			node:      target.ref,
		}
		var members, written []soltype.ObjTypeElem
		if target.class != nil {
			members = c.classTargetMembers(target.class, p.self)
		} else {
			members, written, ic.receivers = c.aliasTargetMembers(target.alias, p.self)
		}
		for i, elem := range members {
			err := ic.check(elem)
			// `members` reads every reference to the interface as the class. That is what
			// `Self` means, but not always what the interface's name written out means. Given
			// `interface Node { next: Node }`, a class field `next: Node` fits the member as
			// written and not as `members` reads it, so a member fitting either is accepted.
			if err != nil && written != nil && ic.check(written[i]) == nil {
				continue
			}
			if err != nil {
				c.report(err)
			}
		}
	}
}

// implementingView returns the members an `implements` entry is compared against: the ones
// the class declares itself, followed by the ones it inherits and does not shadow. A member a
// `declare` class took from its own clause is left out. Every `Self` reads as the class.
func (c *checker) implementingView(p pendingImplementsCheck) *soltype.ObjectType {
	var own []soltype.ObjTypeElem
	for _, elem := range p.def.Body.Elems {
		if p.ownNames.Contains(soltype.ObjElemName(elem)) {
			own = append(own, projectSelf(p.def, p.self, elem))
		}
	}
	return c.ctx.withInherited(p.def, p.self, &soltype.ObjectType{Elems: own})
}

// classTargetMembers returns every member an `implements` entry naming the class ct declares:
// its own members and the ones it inherits, projected to the arguments ct writes. Each `Self`
// reads as recv, the implementing class.
func (c *checker) classTargetMembers(ct, recv *soltype.ClassType) []soltype.ObjTypeElem {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil
	}
	own := make([]soltype.ObjTypeElem, len(def.Body.Elems))
	for i, elem := range def.Body.Elems {
		own[i] = projectClassMember(def, ct, projectSelf(def, recv, elem))
	}
	if len(def.Supers) == 0 {
		return own
	}
	taken := memberNames(def.Body)
	visited := set.NewSet[string]()
	visited.Add(ct.Name)
	return append(own, c.ctx.chainElems(def, ct, recv, taken, visited)...)
}

// aliasTargetMembers returns every member an `implements` entry naming an interface or alias
// declares, including the ones the interface's `extends` clause names, in two readings. It
// also returns the receiver each interface wrote for its methods and accessors.
//
// Inside an interface body `Self` resolves to a reference to that interface, and nothing
// tells it apart from the interface's name written out. `asSelf` reads every reference to the
// interface or to one it extends as recv, the implementing class. `written` leaves them as the
// source wrote them. The two slices pair up by index.
func (c *checker) aliasTargetMembers(
	target soltype.Type,
	recv *soltype.ClassType,
) (asSelf, written []soltype.ObjTypeElem, receivers map[memberBlameKey]*ast.MethodReceiver) {
	expanded := set.NewSet[string]()
	written = c.aliasMembers(target, expanded)
	subst := &aliasSelfSubst{names: expanded, recv: recv}
	asSelf = make([]soltype.ObjTypeElem, len(written))
	for i, elem := range written {
		asSelf[i] = soltype.AcceptObjElem(elem, subst, soltype.Positive)
	}
	receivers = map[memberBlameKey]*ast.MethodReceiver{}
	c.collectAliasReceivers(target, set.NewSet[string](), receivers)
	return asSelf, written, receivers
}

// collectAliasReceivers adds to `out` the receiver each interface reachable from t wrote for
// its methods and accessors. It visits an interface's parents before the interface itself, so
// a member the interface redeclares replaces the receiver its parent wrote. aliasMembers gives
// the members the same precedence. `seen` holds the alias names already visited.
func (c *checker) collectAliasReceivers(t soltype.Type, seen set.Set[string], out map[memberBlameKey]*ast.MethodReceiver) {
	switch t := t.(type) {
	case *soltype.AliasType:
		if seen.Contains(t.Name) {
			return
		}
		seen.Add(t.Name)
		def, ok := c.ctx.aliasDef(t.Name)
		if !ok {
			return
		}
		c.collectAliasReceivers(def.Body, seen, out)
		for key, r := range def.Receivers {
			out[key] = r
		}
	case *soltype.IntersectionType:
		for _, operand := range t.Types {
			c.collectAliasReceivers(operand, seen, out)
		}
	}
}

// aliasSelfSubst replaces every reference to an alias in names with recv.
type aliasSelfSubst struct {
	names set.Set[string]
	recv  *soltype.ClassType
}

func (s *aliasSelfSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if alias, ok := t.(*soltype.AliasType); ok && s.names.Contains(alias.Name) {
		return soltype.EnterResult{Type: s.recv, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *aliasSelfSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// implementsComparison compares the members of one `implements` entry against the class.
//
//   - `view` is the class's member view from implementingView.
//   - `rigid` holds the class's type parameters as skolems.
//   - `receivers` holds the receiver an interface entry wrote for each method and accessor.
//     It is nil for a class entry, whose members carry their receivers themselves.
//   - `declare` marks a `declare` class, where a member the class does not provide is taken
//     from the entry rather than missing.
//   - `node` is the clause entry a diagnostic points at.
type implementsComparison struct {
	c         *checker
	view      *soltype.ObjectType
	rigid     *typeSubst
	receivers map[memberBlameKey]*ast.MethodReceiver
	declare   bool
	className string
	ifaceName string
	node      ast.Node
}

// check compares one member the entry declares against the member the class provides under
// its name, and returns the diagnostic for a mismatch or nil.
//
// Each comparison asks whether the class's member can stand in for the entry's. A read is
// covariant, so a method's or getter's type and a field's read flow from the class to the
// entry. A write is contravariant, so a setter's parameter flows from the entry to the class.
// A writable field is read and written, so it has to match in both directions.
//
// An overloaded method on either side is left unchecked, since comparing overload sets arm
// by arm is deferred to #651. The generated lib reaches this through `CanvasDrawImage`, whose
// `drawImage` declares three arms.
func (ic implementsComparison) check(ifaceElem soltype.ObjTypeElem) SolverError {
	name := soltype.ObjElemName(ifaceElem)
	if name == "" {
		return nil
	}
	lookup := ic.view.ReadMember
	if _, isSetter := ifaceElem.(*soltype.SetterElem); isSetter {
		lookup = ic.view.WriteMember
	}
	classElem, found := lookup(name)
	if !found {
		if ic.declare || isOptional(ifaceElem) || isOptionalMethod(ifaceElem) {
			return nil
		}
		return ic.mismatch(name, "missing")
	}

	switch ie := ifaceElem.(type) {
	case *soltype.MethodElem:
		if len(ie.Signatures) != 1 {
			return nil
		}
		switch ce := classElem.(type) {
		case *soltype.MethodElem:
			if len(ce.Signatures) != 1 {
				return nil
			}
			if !ic.receiverMatches(ifaceElem, ie.Signatures[0].SelfParam, ce.Signatures[0].SelfParam) {
				return ic.mismatch(name, "self receiver does not match")
			}
			if !ic.fits(methodReadType(ce), methodReadType(ie)) {
				return ic.mismatch(name, "signature does not match")
			}
		case *soltype.PropertyElem:
			if ce.Optional {
				return ic.mismatch(name, "property is optional but interface requires it")
			}
			if !ic.fits(ce.Type, methodReadType(ie)) {
				return ic.mismatch(name, "property does not satisfy method signature")
			}
		default:
			return ic.mismatch(name, "is not a method")
		}
	case *soltype.GetterElem:
		switch ce := classElem.(type) {
		case *soltype.GetterElem:
			if !ic.receiverMatches(ifaceElem, ie.SelfParam, ce.SelfParam) {
				return ic.mismatch(name, "self receiver does not match")
			}
			if !ic.fits(ce.Type, ie.Type) {
				return ic.mismatch(name, "getter return type does not match")
			}
		case *soltype.PropertyElem:
			if ce.Optional {
				return ic.mismatch(name, "property is optional but interface requires it")
			}
			if !ic.fits(ce.Type, ie.Type) {
				return ic.mismatch(name, "property type does not match getter")
			}
		default:
			return ic.mismatch(name, "is not a getter or property")
		}
	case *soltype.SetterElem:
		switch ce := classElem.(type) {
		case *soltype.SetterElem:
			if !ic.receiverMatches(ifaceElem, ie.SelfParam, ce.SelfParam) {
				return ic.mismatch(name, "self receiver does not match")
			}
			if !ic.fits(ie.Param, ce.Param) {
				return ic.mismatch(name, "setter argument type does not match")
			}
		case *soltype.PropertyElem:
			if ce.Optional {
				return ic.mismatch(name, "property is optional but interface requires it")
			}
			if ce.Readonly {
				return ic.readonlyMismatch(name)
			}
			if !ic.fits(ie.Param, ce.Type) {
				return ic.mismatch(name, "property type does not match setter")
			}
		default:
			return ic.mismatch(name, "is not a setter or property")
		}
	case *soltype.PropertyElem:
		if getter, ok := classElem.(*soltype.GetterElem); ok {
			return ic.checkAccessorsAgainstField(ie, getter)
		}
		ce, ok := classElem.(*soltype.PropertyElem)
		if !ok {
			return ic.mismatch(name, "member is not a property")
		}
		if ce.Optional && !ie.Optional {
			return ic.mismatch(name, "property is optional but interface requires it")
		}
		if ce.Readonly && !ie.Readonly {
			if err := ic.readonlyMismatch(name); err != nil {
				return err
			}
		}
		if !ic.fits(ce.Type, ie.Type) {
			return ic.mismatch(name, "property type does not match")
		}
		// A readonly field is only read, so the class may narrow it. A writable one is
		// written through as well, so a class narrowing it would refuse a write the entry's
		// type permits.
		if !ie.Readonly && !ic.fits(ie.Type, ce.Type) {
			return ic.mismatch(name, "is a mutable property, so its type has to match the interface's exactly")
		}
	}
	return nil
}

// readonlyMismatch returns the mismatch for a class member that cannot be written standing in
// for an entry member that can, or nil on a `declare` class.
//
// A `declare` class describes an object the runtime already provides, and TypeScript lets
// such a class narrow an implemented member to readonly. The generated lib carries classes
// that do: `declare class ByteLengthQueuingStrategy implements QueuingStrategy<…>` declares
// `readonly highWaterMark: number` where the interface declares `highWaterMark?: number`.
func (ic implementsComparison) readonlyMismatch(name string) SolverError {
	if ic.declare {
		return nil
	}
	return ic.mismatch(name, "is readonly but interface lets it be written")
}

// checkAccessorsAgainstField compares an entry's field against the getter the class declares
// under its name, together with the setter when the field is writable. A read through the
// entry reaches the getter, so its type has to fit the field's and its receiver has to be
// `&self`, since reading a field takes no mutable reference. A write reaches the setter, so
// the field's type has to fit the setter's parameter. The two halves are checked separately,
// so a getter and setter whose types differ still stand in for a field whose type lies
// between them.
func (ic implementsComparison) checkAccessorsAgainstField(ie *soltype.PropertyElem, getter *soltype.GetterElem) SolverError {
	name := ie.Name
	if paramReceiverForm(getter.SelfParam) != (receiverForm{present: true}) {
		return ic.mismatch(name, "self receiver does not match")
	}
	if !ic.fits(getter.Type, ie.Type) {
		return ic.mismatch(name, "getter return type does not match")
	}
	if ie.Readonly {
		return nil
	}
	setter, ok := ic.view.WriteMember(name)
	if s, isSetter := setter.(*soltype.SetterElem); ok && isSetter {
		if !ic.fits(ie.Type, s.Param) {
			return ic.mismatch(name, "setter argument type does not match")
		}
		return nil
	}
	return ic.readonlyMismatch(name)
}

// receiverMatches reports whether the class member's receiver takes the instance the same
// way as the entry member's. ifaceSelf and classSelf are the two members' receivers. A member
// lowered from an interface body carries no receiver, so its receiver is read from
// ic.receivers instead. A member that table has no entry for is not compared.
//
// The receivers have to agree exactly:
//
//   - A `&mut self` member cannot stand in for a `&self` one, since a caller holding an
//     immutable reference could not reach it.
//   - A `&self` member cannot stand in for a `&mut self` one either. The rule asks for
//     agreement rather than for a receiver callers can always reach, the same rule the
//     checker package applies.
//   - A consuming `self` and a borrowing `&self` differ in whether the caller keeps the
//     instance, which matters in both directions.
//   - A member with no receiver only matches another with none.
func (ic implementsComparison) receiverMatches(ifaceElem soltype.ObjTypeElem, ifaceSelf, classSelf *soltype.FuncParam) bool {
	want := paramReceiverForm(ifaceSelf)
	if ic.receivers != nil {
		_, isSetter := ifaceElem.(*soltype.SetterElem)
		recv, recorded := ic.receivers[memberBlameKey{name: soltype.ObjElemName(ifaceElem), setter: isSetter}]
		if !recorded {
			return true
		}
		want = astReceiverForm(recv)
	}
	return want == paramReceiverForm(classSelf)
}

// receiverForm is how a member takes its instance. `present` is false for a member with no
// receiver. mutBorrow marks `&mut self`, and `consumes` marks `self` and `mut self`.
type receiverForm struct {
	present, mutBorrow, consumes bool
}

// paramReceiverForm returns the form of a resolved receiver. self is nil for a member with no
// receiver.
func paramReceiverForm(self *soltype.FuncParam) receiverForm {
	return receiverForm{
		present:   self != nil,
		mutBorrow: selfParamMut(self),
		consumes:  selfParamConsumes(self),
	}
}

// astReceiverForm returns the form of a written receiver. recv is nil for a member that wrote
// none.
func astReceiverForm(recv *ast.MethodReceiver) receiverForm {
	if recv == nil {
		return receiverForm{}
	}
	return receiverForm{
		present:   true,
		mutBorrow: recv.Mut && !recv.Consumes(),
		consumes:  recv.Consumes(),
	}
}

// fits reports whether sub is a subtype of super with the class's type parameters held
// rigid. The trial rolls its bounds back, so the comparison records nothing on either type.
func (ic implementsComparison) fits(sub, super soltype.Type) bool {
	return !hasHardError(ic.c.ctx.trialUnderProbe(ic.rigid.apply(sub), ic.rigid.apply(super)))
}

func (ic implementsComparison) mismatch(member, reason string) SolverError {
	return &ClassDoesNotImplementInterfaceError{
		Class:     ic.className,
		Interface: ic.ifaceName,
		Member:    member,
		Reason:    reason,
		Node:      ic.node,
	}
}

// isOptionalMethod reports whether a member is a `m?(…)` method, which a class may leave out.
func isOptionalMethod(elem soltype.ObjTypeElem) bool {
	method, ok := elem.(*soltype.MethodElem)
	return ok && method.Optional
}

// memberNames returns the name of every member of obj.
func memberNames(obj *soltype.ObjectType) set.Set[string] {
	names := set.NewSet[string]()
	addElemNames(names, obj)
	return names
}

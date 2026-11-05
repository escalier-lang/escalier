package solver

import (
	"iter"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// interfaces.go binds an `interface` declaration to the object type its body
// describes.
//
// An interface is structural and transparent, so it binds to an AliasType over
// that object rather than to a nominal identity of its own. `interface Point {x:
// number}` and `type Point = {x: number}` give an annotation the same type, and
// a matching object literal satisfies either.
//
// One name may be declared several times. dep_graph groups every `interface
// Point` in a module under one type key, so a shell carries the whole group and
// the bound type is their merged member list.

// interfaceShell is one interface name's pre-bound identity, completed by
// inferInterfaceBody once every sibling identity in the component is bound.
type interfaceShell struct {
	// decls are every declaration of this name, in source order. The merge reads
	// them in that order, so a later member of the same name wins.
	decls []*ast.InterfaceDecl
	lvl   int
	// qname is the dep_graph-qualified name the AliasDef registers under and the
	// Name every AliasType handle pointing at it carries.
	qname string
	// declScope is scope, or a child holding a generic interface's type
	// parameters, so the body reads each `T` as the var the parameter binder
	// minted.
	declScope *Scope
	// ns is the interface's dep_graph namespace. The body resolves a bare
	// reference against it first, so a namespaced interface reaches a sibling.
	ns string
	// def is the registered AliasDef with a nil Body. inferInterfaceBody fills it.
	def *AliasDef
	// symbolOwner is the path of the value that names the unique symbols the interface
	// declares, such as `i` for `declare val i: I`, and empty when no value does. See
	// symbolOwners.
	symbolOwner string
	// parents are the types the `extends` clauses resolve to and own the members the body
	// declares. inferInterfaceBody sets both for an interface with an `extends` clause.
	parents []soltype.Type
	own     *soltype.ObjectType
}

// preBindInterface resolves the group's type parameters, registers an AliasDef
// whose Body is still nil, and binds the name to an AliasType handle. Registering
// the name before any body resolves is what lets a sibling interface, or this one,
// name it while its own body is still being walked.
//
// The type parameters come from the first declaration, and every later one must
// write the same list. A declaration that disagrees reports
// InterfaceTypeParamMismatchError and contributes no members, since its body reads
// parameter names the shared list does not bind.
func (c *checker) preBindInterface(scope *Scope, lvl int, decls []*ast.InterfaceDecl, ns string) *interfaceShell {
	first := decls[0]

	// Reported against the first declaration of the name. Interface declarations of one name
	// merge into a single binding, so reporting per declaration would repeat one problem.
	c.reportSelfTypeName(InterfaceDeclKind, first.Name)

	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	qname := c.qualifyDecl(ns, first.Name.Name)

	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(first.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, first.TypeParams)
	}

	// Every declaration's body resolves against the one parameter list above, so a
	// declaration writing different parameter names would read them as unbound, and
	// a bound or default it writes would be ignored. Drop it here rather than let
	// its body report a missing type for each use, or merge under a bound the source
	// never agreed on.
	merged := decls[:1]
	for _, d := range decls[1:] {
		if reason, mismatch := typeParamMismatch(first.TypeParams, d.TypeParams); mismatch {
			c.report(&InterfaceTypeParamMismatchError{
				Name:   first.Name.Name,
				Reason: reason,
				span:   d.Span(),
			})
			continue
		}
		merged = append(merged, d)
	}
	decls = merged

	def := &AliasDef{TypeParams: typeParams, Level: lvl - 1}
	c.ctx.registerAlias(qname, def)

	t := &soltype.AliasType{Name: qname}
	sources := make([]provenance.Provenance, 0, len(decls))
	for _, d := range decls {
		sources = append(sources, &ast.NodeProvenance{Node: d})
	}
	c.declTarget(scope).defineType(declScopeKey(ns, first.Name.Name), TypeBinding{Type: t, Sources: sources})
	c.recordType(first.Name, t)

	return &interfaceShell{
		decls:       decls,
		lvl:         lvl,
		qname:       qname,
		declScope:   declScope,
		ns:          ns,
		def:         def,
		symbolOwner: c.symbolOwners[declScopeKey(ns, first.Name.Name)],
	}
}

// inferInterfaceBody resolves each declaration's members and stores their merge on
// the shell's AliasDef. It runs after every identity in the recursive group is
// pre-bound, so a member naming a sibling resolves.
//
// An `extends` clause contributes its target's members ahead of the body's own, so
// `interface Sq extends Rect {s: number}` binds the flattened surface rather than a
// subtyping relation. A member the body declares under a name `extends` also
// supplied replaces it.
func (c *checker) inferInterfaceBody(sh *interfaceShell) {
	prevNS := c.classNamespace
	c.classNamespace = sh.ns
	defer func() { c.classNamespace = prevNS }()

	// The shared object-member builder decides what a later declaration does to an
	// earlier one of the same name. It appends a method's signatures rather than
	// replacing it, so overloads split across declarations accumulate, and it
	// tracks the read and write accesses separately, so a getter in one
	// declaration and a setter in another form a pair rather than one dropping
	// the other.
	b := newObjElemBuilder(0)
	var parents []soltype.Type

	// Bind `Self` to the interface's own handle for the member bodies, so a member such as
	// `clone(&self) -> Self` resolves. The binding sits in a child scope that the `extends`
	// references below do not see, since `Self` there would name the interface being built.
	selfAlias := &soltype.AliasType{
		Name:     sh.qname,
		TypeArgs: typeParamVars(sh.def.TypeParams),
		Defaults: paramDefaults(sh.def.TypeParams),
	}
	bodyScope := sh.declScope.Child()
	bodyScope.defineType(selfTypeName, TypeBinding{Type: selfAlias})
	savedSelfAlias := c.selfAlias
	c.selfAlias = selfAlias
	defer func() { c.selfAlias = savedSelfAlias }()
	for _, decl := range sh.decls {
		for _, ext := range decl.Extends {
			if parent, ok := c.resolveTypeAnn(sh.declScope, ext, sh.lvl); ok {
				parents = append(parents, parent)
			}
		}
		// A nil TypeAnn is parser error recovery for a body that failed to parse,
		// already reported. The declaration then contributes no members.
		if decl.TypeAnn == nil {
			continue
		}
		recordInterfaceReceivers(sh.def, decl.TypeAnn)
		resolved, ok := c.resolveOwnedObjectTypeAnn(bodyScope, decl.TypeAnn, sh.lvl, sh.symbolOwner)
		if !ok {
			continue
		}
		obj, isObj := resolved.(*soltype.ObjectType)
		if !isObj {
			continue
		}
		for _, elem := range obj.Elems {
			b.addElem(elem)
		}
	}

	// An interface is inexact. It describes the members a value has to carry, not
	// the members it may carry, so a value with more than the interface names still
	// satisfies it.
	own := &soltype.ObjectType{Elems: b.result(), Inexact: true}
	if len(parents) == 0 {
		sh.def.Body = own
		return
	}
	// An `extends` clause meets the parents with the body rather than copying their
	// members in. Both halves are inexact, so the meet requires every member either
	// side names and admits the rest, which is what member inheritance means here.
	//
	// The parents stay as the references they resolved to, so a parent still being
	// built is named rather than read. That is what lets `interface A {b: B}` and
	// `interface B extends A {x}` resolve each other.
	sh.def.Body = extendedInterfaceBody(parents, own)
	sh.parents, sh.own = parents, own
	// Every interface in the libraries a package load reads is written `declare`. They come from
	// TypeScript's own declarations, which TypeScript has already checked, and comparing
	// their overrides member by member costs minutes on the DOM interfaces. Such an
	// interface is left out. Every other interface is queued.
	if len(c.loadStack) == 0 || !allDeclared(sh.decls) {
		c.pendingInterfaceOverrides = append(c.pendingInterfaceOverrides, sh)
	}
}

// checkInterfaceOverrides checks every member an interface declares against the same-named
// member its parents declare, and reports one the parent's member does not admit. It reads
// sh.parents and sh.own, which inferInterfaceBody sets, and every parent's body has to be
// built first.
//
// The rules are the ones checkOverriddenName applies to a class. Each access half the parent
// exposes has to be offered in a form at least as strong and at a type that fits, covariantly
// for a read and contravariantly for a write. The interface's type parameters are compared as
// skolems, so the obligation holds for every instantiation.
func (c *checker) checkInterfaceOverrides(sh *interfaceShell) {
	blame := interfaceMemberNodes(sh.decls)
	var rigid *typeSubst
	if len(sh.def.TypeParams) > 0 {
		rigid = c.ctx.skolemizeParams(sh.def.TypeParams)
	}
	name := sh.decls[0].Name.Name
	checked := set.NewSet[string]()
	for _, elem := range sh.own.Elems {
		member := soltype.ObjElemName(elem)
		if member == "" || checked.Contains(member) {
			continue
		}
		checked.Add(member)
		c.checkInterfaceOverriddenName(sh, name, member, blame, rigid)
	}
}

// checkInterfaceOverriddenName checks one member the interface named name declares against
// the same member on its parents, reporting at most one diagnostic for it.
func (c *checker) checkInterfaceOverriddenName(
	sh *interfaceShell,
	name, member string,
	blame map[memberBlameKey]ast.Node,
	rigid *typeSubst,
) {
	for _, parent := range sh.parents {
		// owners[i] is the parent type declaring the member goals[i] compares against.
		var goals []subtypeGoal
		var owners []soltype.Type
		for _, half := range []memberHalf{readHalf, writeHalf} {
			for _, inherited := range c.interfaceParentHalves(parent, member, half, set.NewSet[string]()) {
				superHalf, owner := inherited.elem, inherited.owner
				subHalf, declared := declaredHalf(sh.own, member, half)
				if !declared || formWeakens(subHalf, superHalf) {
					own, _ := sh.own.Member(member)
					c.report(&OverrideFormMismatchError{
						Kind:       InterfaceDeclKind,
						Member:     member,
						Class:      name,
						SuperClass: soltype.Print(rigid.apply(owner)),
						Form:       memberForm(own),
						SuperForm:  memberForm(superHalf),
						Node:       interfaceOverrideBlame(sh, blame, member, subHalf),
					})
					return
				}
				for _, goal := range halfGoals(subHalf, superHalf, half, nil) {
					goals = append(goals, goal)
					owners = append(owners, owner)
				}
			}
		}
		for i, goal := range goals {
			if !hasHardError(c.ctx.trialUnderProbe(rigid.apply(goal.sub), rigid.apply(goal.super))) {
				continue
			}
			c.report(&IncompatibleOverrideError{
				Kind:       InterfaceDeclKind,
				Member:     member,
				Class:      name,
				SuperClass: soltype.Print(rigid.apply(owners[i])),
				Position:   goal.position,
				SubType:    rigid.apply(goal.shownSub),
				SuperType:  rigid.apply(goal.shownSuper),
				Node:       interfaceOverrideBlame(sh, blame, member, goal.subElem),
			})
			return
		}
	}
}

// inheritedInterfaceHalf is one member a parent type offers for an access half, along with
// the type that declares it.
type inheritedInterfaceHalf struct {
	elem  soltype.ObjTypeElem
	owner soltype.Type
}

// interfaceParentHalves returns every member serving one access half of name on the parent
// type t, each with the type that declares it. An interface parent is expanded to its body.
// A class parent is read through its projected body.
//
// A parent whose body is itself an `extends` meet answers with the member its own arm
// declares when there is one. That member is checked against the parent's own parents when
// the parent is checked, so it is the only one an override has to fit. Otherwise every arm
// that offers the member answers, since a value of the parent is a value of each arm.
//
// seen holds the alias names expanded on the path to t, so an `extends` cycle stops.
func (c *checker) interfaceParentHalves(t soltype.Type, name string, half memberHalf, seen set.Set[string]) []inheritedInterfaceHalf {
	switch body := c.expandAliasChainFrom(t, seen).(type) {
	case *soltype.ObjectType:
		if elem, ok := declaredHalf(body, name, half); ok {
			return []inheritedInterfaceHalf{{elem: elem, owner: t}}
		}
	case *soltype.IntersectionType:
		var found []inheritedInterfaceHalf
		for _, arm := range armsByPrecedence(body) {
			for _, inherited := range c.interfaceParentHalves(arm, name, half, seen.Clone()) {
				// The arm holding the parent's own members is a bare object, which names
				// nothing. The member is then the parent's own, and it is the only answer.
				if _, bare := inherited.owner.(*soltype.ObjectType); bare {
					return []inheritedInterfaceHalf{{elem: inherited.elem, owner: t}}
				}
				found = append(found, inherited)
			}
		}
		return found
	case *soltype.ClassType:
		if obj, ok := c.ctx.projectClassBody(body); ok {
			if elem, found := declaredHalf(obj, name, half); found {
				return []inheritedInterfaceHalf{{elem: elem, owner: t}}
			}
		}
	}
	return nil
}

// allDeclared reports whether every declaration in decls is written `declare`.
func allDeclared(decls []*ast.InterfaceDecl) bool {
	for _, d := range decls {
		if !d.Declare() {
			return false
		}
	}
	return true
}

// interfaceMemberNodes maps each member the declarations write to the node that declares it,
// so an override diagnostic points at the member. A later declaration of a name replaces an
// earlier one's entry, matching the merge.
func interfaceMemberNodes(decls []*ast.InterfaceDecl) map[memberBlameKey]ast.Node {
	nodes := map[memberBlameKey]ast.Node{}
	for _, decl := range decls {
		if decl.TypeAnn == nil {
			continue
		}
		for _, elem := range decl.TypeAnn.Elems {
			key, _, setter, ok := interfaceMemberKey(elem)
			if !ok {
				continue
			}
			if name, ok := objKeyName(key); ok {
				nodes[memberBlameKey{name: name, setter: setter}] = elem
			}
		}
	}
	return nodes
}

// interfaceOverrideBlame returns the node an interface override diagnostic points at: the
// member's declaration, or the first declaration of the interface when its key names no
// member objKeyName can read.
func interfaceOverrideBlame(sh *interfaceShell, blame map[memberBlameKey]ast.Node, name string, elem soltype.ObjTypeElem) ast.Node {
	if node := overrideBlame(blame, name, elem); node != nil {
		return node
	}
	return sh.decls[0]
}

// extendedInterfaceBody returns the body of an interface with an `extends` clause: the meet
// of parents and own, with own as the last arm. armsByPrecedence reads the arms back in the
// order this builds them.
func extendedInterfaceBody(parents []soltype.Type, own *soltype.ObjectType) *soltype.IntersectionType {
	return &soltype.IntersectionType{Types: append(parents, soltype.Type(own))}
}

// armsByPrecedence yields the arms of `it`, most specific first. For a body
// extendedInterfaceBody built, that is the interface's own members, then each parent from
// the last listed to the first. A lookup that stops at the first arm declaring a member
// therefore reads the declaration the interface itself wrote over one a parent supplies.
func armsByPrecedence(it *soltype.IntersectionType) iter.Seq2[int, soltype.Type] {
	return slices.Backward(it.Types)
}

// recordInterfaceReceivers stores on def the receiver each method, getter, and setter in body
// writes. A later declaration of a name replaces an earlier one's entry.
func recordInterfaceReceivers(def *AliasDef, body *ast.ObjectTypeAnn) {
	for _, elem := range body.Elems {
		if _, isProp := elem.(*ast.PropertyTypeAnn); isProp {
			continue
		}
		// A receiver no setter may declare is already reported where the body is lowered.
		// Leaving it unrecorded keeps `implements` from comparing against it and reporting
		// the same mistake again on each implementing class.
		if setter, ok := elem.(*ast.SetterTypeAnn); ok && invalidSetterReceiver(setter.Receiver) {
			continue
		}
		key, recv, setter, ok := interfaceMemberKey(elem)
		if !ok {
			continue
		}
		name, ok := objKeyName(key)
		if !ok {
			continue
		}
		if def.Receivers == nil {
			def.Receivers = map[memberBlameKey]*ast.MethodReceiver{}
		}
		def.Receivers[memberBlameKey{name: name, setter: setter}] = recv
	}
}

// typeParamMismatch reports whether a later declaration's type parameters can
// merge under the first declaration's list, and why not when they cannot.
//
// The first declaration's list is the one every body resolves against, so a later
// declaration writing a bound or a default is writing something nothing reads.
// Rejecting that is what stops `Box<T: string>` and `Box<T: number>` merging under
// whichever came first.
func typeParamMismatch(first, later []*ast.TypeParam) (string, bool) {
	if len(first) != len(later) {
		return "they differ in how many it writes", true
	}
	for i := range later {
		if first[i].Name != later[i].Name {
			return "they differ in the names they write", true
		}
		if later[i].Variance != first[i].Variance {
			return "they differ in variance", true
		}
		if later[i].UpperBound != nil || later[i].LowerBound != nil {
			return "only the first declaration may write a bound", true
		}
		if later[i].Default != nil {
			return "only the first declaration may write a default", true
		}
	}
	return "", false
}

// InterfaceTypeParamMismatchError reports a declaration whose type-parameter list
// differs from the first declaration of the same interface. One name binds one
// parameter list, so the declarations have to agree on it.
type InterfaceTypeParamMismatchError struct {
	// Name is the interface every declaration in the group declares.
	Name string
	// Reason says how this declaration's list differs from the first's.
	Reason string
	span   ast.Span
}

func (e *InterfaceTypeParamMismatchError) Message() string {
	return "declarations of interface " + e.Name + " must agree on their type parameters: " + e.Reason
}
func (e *InterfaceTypeParamMismatchError) Span() ast.Span      { return e.span }
func (e *InterfaceTypeParamMismatchError) Related() []ast.Span { return nil }
func (e *InterfaceTypeParamMismatchError) isSolverError()      {}

// interfaceMemberKey returns the key a field, method, getter, or setter of an interface body
// writes, the receiver a method or accessor writes, and whether the member is a setter. ok is
// false for any other element, such as an index signature.
func interfaceMemberKey(elem ast.ObjTypeAnnElem) (key ast.ObjKey, recv *ast.MethodReceiver, setter, ok bool) {
	switch elem := elem.(type) {
	case *ast.PropertyTypeAnn:
		return elem.Name, nil, false, true
	case *ast.MethodTypeAnn:
		return elem.Name, elem.Receiver, false, true
	case *ast.GetterTypeAnn:
		return elem.Name, elem.Receiver, false, true
	case *ast.SetterTypeAnn:
		return elem.Name, elem.Receiver, true, true
	}
	return nil, nil, false, false
}

package solver

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file decides which values inside a container a callee could remove from it. A
// callee that holds `&mut arr` with `arr: Array<Flat>` can call `arr.pop()` and hand the
// element to a new owner. A callee that holds `&mut b` with `b: {inner: Flat}` cannot do
// the same with `b.inner`. Reading `b.inner` through the borrow yields a borrow, which
// cannot fill an owned slot, so the callee can only overwrite the field.

// exprHoldsRemovableSlot reports whether the value of e has a type that
// holdsRemovableSlot accepts. A nil e has no recorded type, and an e with no recorded type
// answers true.
func (c *checker) exprHoldsRemovableSlot(e ast.Expr) bool {
	t := c.info.TypeOf(e)
	return t == nil || c.holdsRemovableSlot(t)
}

// holdsRemovableSlot reports whether a callee given a mutable borrow of a value of type t
// could take a value out of a slot somewhere inside it and hand that value to a new owner.
// An element of an `Array`, a `Map` or a `Set` is such a value. A field of an object or
// class and a slot of a tuple are not.
//
// The walk goes through fields, tuple slots, union members and mutable borrows. It stops at
// an immutable borrow, since nothing can be removed through one. A type whose slots the walk
// cannot see answers true. That covers a declared class, an object with a method or getter,
// an index signature, a type parameter, and a type operator left unreduced.
func (c *checker) holdsRemovableSlot(t soltype.Type) bool {
	return c.walkRemovableSlots(t, false)
}

// exprHoldsRemovableNonValueSlot reports whether the value of e has a type that
// holdsRemovableNonValueSlot accepts. A nil e has no recorded type, and an e with no recorded
// type answers true.
func (c *checker) exprHoldsRemovableNonValueSlot(e ast.Expr) bool {
	t := c.info.TypeOf(e)
	return t == nil || c.holdsRemovableNonValueSlot(t)
}

// holdsRemovableNonValueSlot reports whether a callee given a mutable borrow of a value of
// type t could take a value that is not a value type out of a slot inside it and hand that
// value to a new owner. It answers as holdsRemovableSlot does, except for an `Array`, a `Set`
// or a `Map` whose type arguments are all value types, as isValueShaped decides. Such a
// collection holds no such slot, since removing an element copies it. So `Array<number>`,
// `Set<string>` and `Map<string, number>` answer false, and `Array<Flat>` answers true.
func (c *checker) holdsRemovableNonValueSlot(t soltype.Type) bool {
	return c.walkRemovableSlots(t, true)
}

// walkRemovableSlots runs removableSlotVisitor over t and reports whether it found a removable
// slot. `nonValueOnly` sets the visitor's field of the same name.
func (c *checker) walkRemovableSlots(t soltype.Type, nonValueOnly bool) bool {
	v := &removableSlotVisitor{
		c:            c,
		nonValueOnly: nonValueOnly,
		aliases:      set.NewSet[string](),
		classes:      set.NewSet[string](),
	}
	t.Accept(v, soltype.Positive)
	return v.found
}

// removableSlotVisitor is the walk of holdsRemovableSlot and holdsRemovableNonValueSlot.
// `found` is set once the walk reaches a removable slot, and every node after that is skipped.
type removableSlotVisitor struct {
	c     *checker
	found bool
	// nonValueOnly is set when a collection whose type arguments are all value types counts
	// as holding no removable slot. collectionElems lists the collections.
	nonValueOnly bool
	// `aliases` and `classes` hold the names the walk has expanded. A name reached again
	// is not expanded again, but its type arguments are still walked. Those arguments are
	// all that a second expansion could add, since the body around them was walked once.
	aliases set.Set[string]
	classes set.Set[string]
}

func (v *removableSlotVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	skip := soltype.EnterResult{SkipChildren: true}
	descend := soltype.EnterResult{}
	if v.found {
		return skip
	}

	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.NullType, *soltype.UndefinedType,
		*soltype.NeverType, *soltype.ErrorType, *soltype.UniqueSymbolType,
		*soltype.TemplateLitType, *soltype.StringIntrinsicType, *soltype.RegexType,
		*soltype.RecursiveVarType:
		return skip
	case *soltype.FuncType, *soltype.FunctionTopType:
		// A function value is not a slot of the container, and a callee cannot remove
		// anything through the types its parameters name.
		return skip
	case *soltype.UnionType, *soltype.IntersectionType, *soltype.RecursiveType, *soltype.TypeofType:
		return descend
	case *soltype.RefType:
		if !t.Mut && t.Lt != nil {
			return skip
		}
		return descend
	case *soltype.TupleType:
		// An inexact tuple `[A, ...]` may hold elements its type does not list, and the walk
		// cannot see what they hold. A rest element `...Array<T>` reaches the default case
		// when the walk descends into the elements.
		if t.Inexact {
			break
		}
		return descend
	case *soltype.ObjectType:
		if !onlyFixedMembers(t.Elems) {
			break
		}
		return descend
	case *soltype.AliasType:
		if v.aliases.Contains(t.Name) {
			return descend
		}
		v.aliases.Add(t.Name)
		body, ok := v.c.ctx.aliasBody(t)
		if !ok {
			break
		}
		body.Accept(v, pol)
		return skip
	case *soltype.ClassType:
		// This comes before the check of `classes`. A collection reached again can carry
		// different type arguments, and those arguments need checking as values.
		if v.nonValueOnly {
			if elems, isCollection := v.c.collectionElems(t); isCollection {
				if !slices.ContainsFunc(elems, func(elem soltype.Type) bool { return !v.c.isValueShaped(elem) }) {
					return skip
				}
				break
			}
		}
		if v.classes.Contains(t.Name) {
			return descend
		}
		v.classes.Add(t.Name)
		body, ok := v.c.fieldClassBody(t)
		if !ok {
			break
		}
		for _, elem := range body.Elems {
			if prop, isProp := elem.(*soltype.PropertyElem); isProp {
				prop.Type.Accept(v, pol)
			}
		}
		return skip
	case *soltype.TypeVarType:
		if v.c.isValueShaped(t) {
			return skip
		}
		// A type parameter's variable stands for every type a caller can pass, and the walk
		// cannot see what that holds. Coalescing it would read its empty lower bounds as
		// `never`.
		if t.Rigid != nil {
			break
		}
		// An inference variable reads as what its lower bounds hold.
		coalesced := coalesce(t, soltype.Positive)
		if _, still := coalesced.(*soltype.TypeVarType); still {
			break
		}
		coalesced.Accept(v, pol)
		return skip
	}
	v.found = true
	return skip
}

func (v *removableSlotVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// onlyFixedMembers reports whether every member in elems leaves the object's slots in
// place. A property does, and so do a call signature and a `new` signature, which act on
// no instance. Every other member is rejected, including each method and getter. One that
// takes `&mut self` can hand a value it holds to a new owner, as `Array.pop` does, and an
// object type annotation does not record a member's receiver. So `peek(&self)` cannot be
// told apart from a method that takes `&mut self`.
func onlyFixedMembers(elems []soltype.ObjTypeElem) bool {
	for _, elem := range elems {
		switch elem.(type) {
		case *soltype.PropertyElem, *soltype.ConstructorElem, *soltype.CallableElem:
		default:
			return false
		}
	}
	return true
}

// fieldClassBody returns the projected instance body of ct when ct is a class declared in
// Escalier source without `declare`, and so is each class it extends. It returns false for
// any other class, such as `Array` or `Map`, whose internals are unknown. The methods of a
// class declared in Escalier are checked like any other function, so its slots are exactly
// its fields.
func (c *checker) fieldClassBody(ct *soltype.ClassType) (*soltype.ObjectType, bool) {
	seen := set.NewSet[string]()
	pending := []string{ct.Name}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen.Contains(name) {
			continue
		}
		seen.Add(name)
		def, ok := c.ctx.classDef(name)
		if !ok || def.varianceDecl == nil || def.varianceDecl.Declare() || def.EdgesPending {
			return nil, false
		}
		for _, super := range def.Supers {
			pending = append(pending, super.Name)
		}
	}

	body, ok := c.ctx.projectClassBody(ct)
	if !ok || soltype.HasResidualElem(body.Elems) {
		return nil, false
	}
	return body, true
}

// collectionElems returns the type arguments of ct when ct is the prelude's `Array`, the
// `Set` that `std:set` exports, or the `Map` that `std:map` exports. Each type argument is a
// type the collection stores. That is the element of an `Array` or a `Set`, and the key and
// the value of a `Map`. It returns false for any other class.
func (c *checker) collectionElems(ct *soltype.ClassType) ([]soltype.Type, bool) {
	// Every registered class has a qualified name, so ct.Name is never "" and never matches
	// the "" that a missing class answers.
	switch ct.Name {
	case c.ctx.arrayClass, c.loadedClassName("std:set", "Set"), c.loadedClassName("std:map", "Map"):
		return ct.TypeArgs, true
	}
	return nil, false
}

// loadedClassName returns the qualified name of the class that the package `uri` exports as
// `name`. It returns "" when the run has not loaded the package, is still loading it, or the
// package exports no class under that name. It never loads a package.
func (c *checker) loadedClassName(uri, name string) string {
	ns, loaded := c.ctx.packages.Lookup(uri)
	if !loaded || ns == nil {
		return ""
	}
	// A name the package does not export reads as a binding with no type, which is not a
	// class either.
	cls, isClass := ns.Types[name].Type.(*soltype.ClassType)
	if !isClass {
		return ""
	}
	return cls.Name
}

// isFixedSlot reports whether `link` reads a field of an object or class, or a slot of a
// tuple, rather than an element a callee could remove. In `b.inner` with
// `b: {inner: Flat}` and in `p[0]` with `p: [Flat, Flat]` the link reads a fixed slot. In
// `arr[0]` with `arr: Array<Flat>` it does not. An object whose members onlyFixedMembers
// rejects has no fixed slot, since one of its methods may hand a field to a new owner.
func (c *checker) isFixedSlot(link placeLink) bool {
	if link.dynamic || link.seg.kind != namedSeg {
		return false
	}
	t := c.info.TypeOf(link.object)
	if t == nil {
		return false
	}

	found := false
	for _, member := range c.unionMembers(c.ctx.expandedCarrier(groundedOrSkolem(t))) {
		switch member := c.ctx.expandedCarrier(member).(type) {
		case *soltype.ObjectType:
			if !onlyFixedMembers(member.Elems) || !hasProperty(member.Elems, link.seg.name) {
				return false
			}
		case *soltype.TupleType:
			if !fixedTupleSlot(member, link.seg.name) {
				return false
			}
		case *soltype.ClassType:
			body, ok := c.fieldClassBody(member)
			if !ok || !hasProperty(body.Elems, link.seg.name) {
				return false
			}
		default:
			return false
		}
		found = true
	}
	return found
}

// hasProperty reports whether elems holds a property named `name`.
func hasProperty(elems []soltype.ObjTypeElem, name string) bool {
	for _, elem := range elems {
		if prop, ok := elem.(*soltype.PropertyElem); ok && prop.Name == name {
			return true
		}
	}
	return false
}

// fixedTupleSlot reports whether `name` is the index of a slot of tup that comes before any
// rest element.
func fixedTupleSlot(tup *soltype.TupleType, name string) bool {
	i, err := strconv.Atoi(name)
	if err != nil || i < 0 || i >= len(tup.Elems) {
		return false
	}
	for _, elem := range tup.Elems[:i+1] {
		if _, rest := elem.(*soltype.RestSpreadType); rest {
			return false
		}
	}
	return true
}

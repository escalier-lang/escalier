package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file decides what kind of value a type holds: a value type that a flow copies, a
// borrow, or an owned reference shape that a flow moves.

// isBorrowType reports whether t is a borrow — a RefType carrying a lifetime.
// Owned-immutable collapses to the bare inner and owned-mutable is a RefType with
// a nil lifetime, so only a non-nil Lt marks a borrow.
func isBorrowType(t soltype.Type) bool {
	r, ok := t.(*soltype.RefType)
	return ok && r.Lt != nil
}

// isReferenceShaped reports whether t is a reference-shaped value: an object, tuple, class
// instance, borrow, owned RefType, or type-parameter variable. Any other type is a value type,
// which copies: a primitive, a function, or a promise. An alias or interface is decided by
// what it names.
//
// A bare type variable counts here but is excluded by isConcreteOwned, so the two are
// conservative in opposite directions on an unresolved variable. This side treats the
// variable as one that may resolve to a movable shape.
func (c *checker) isReferenceShaped(t soltype.Type) bool {
	switch c.expandAliasChain(t).(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.ClassType, *soltype.RefType, *soltype.TypeVarType:
		return true
	}
	return false
}

// isConcreteOwned reports whether t is a CONCRETE owned reference shape — an owned
// object, tuple, class instance, or owned RefType — excluding a bare type variable. An alias
// or interface is decided by what it names.
//
// Excluding the bare type variable is the opposite of isReferenceShaped, which includes
// it, so the two are conservative in opposite directions on an unresolved variable. This
// side confirms ownership only for a shape it can see, so a caller does not move a value
// that may resolve to a value type.
func (c *checker) isConcreteOwned(t soltype.Type) bool {
	t = c.expandAliasChain(t)
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.ClassType:
		return true
	case *soltype.RefType:
		return !isBorrowType(t)
	}
	return false
}

// isOwnedMovable reports whether t is an owned reference-shaped value, the kind a
// move at an owned destination consumes. Value types copy and borrows alias, so
// neither moves at an owned site. An owned object, tuple, class instance, or owned-mutable
// RefType moves, as does a bare type-parameter variable: generic code treats a `T` value as
// non-duplicable, the conservative affine assumption that makes
// `fn dup<T>(x: T) -> [T, T]` a double move.
//
// A borrow is decided by borrowOfType, so an alias naming a borrow is one too.
//
// A union is never movable, since isValueShaped reports true for every union.
func (c *checker) isOwnedMovable(t soltype.Type) bool {
	if _, isBorrow := c.borrowOfType(t); isBorrow {
		return false
	}
	return !c.isValueShaped(t)
}

// isValueShaped reports whether a value of type t copies when it flows. It holds for a type
// isReferenceShaped rejects, and for a variable every value of which is a value type, as
// holdsOnlyValueTypes decides. isReferenceShaped rejects every union, so isValueShaped
// reports true for any union. A caller that decides a union by its members walks them before
// it calls this.
func (c *checker) isValueShaped(t soltype.Type) bool {
	if !c.isReferenceShaped(t) {
		return true
	}
	v, isVar := t.(*soltype.TypeVarType)
	return isVar && c.holdsOnlyValueTypes(v, set.NewSet[*soltype.TypeVarType]())
}

// holdsOnlyValueTypes reports whether every value v can hold is a value type, following
// lower bounds that are themselves variables. A field read records its type as a variable
// whose lower bound is the field's declared type, so in
//
//	val mut b = {v: 1}
//	val n = b.v
//
// the type of `b.v` is a variable bounded below by `1`. Reading it copies the number and
// moves nothing.
//
// A variable with no lower bound holds no known value. That is the case for a type
// parameter's variable, so it keeps the conservative answer and stays movable. A variable
// reached again on the same path is a cycle in the bound graph, and it answers false for
// the same reason.
func (c *checker) holdsOnlyValueTypes(v *soltype.TypeVarType, seen set.Set[*soltype.TypeVarType]) bool {
	if len(v.LowerBounds) == 0 || seen.Contains(v) {
		return false
	}
	seen.Add(v)
	defer seen.Remove(v)
	for _, lb := range v.LowerBounds {
		if lbVar, ok := lb.(*soltype.TypeVarType); ok {
			if !c.holdsOnlyValueTypes(lbVar, seen) {
				return false
			}
			continue
		}
		if c.isReferenceShaped(lb) {
			return false
		}
	}
	return true
}

// holdsFunc reports whether a value of type t can be a function. An alias is expanded first.
// A union or an intersection can be one when a member can. A variable can be one when one of
// its lower bounds, followed through other variables, can. `seen` holds the variables on the
// current path, so a cycle in the bound graph answers false.
func (c *checker) holdsFunc(t soltype.Type, seen set.Set[*soltype.TypeVarType]) bool {
	member := func(m soltype.Type) bool { return c.holdsFunc(m, seen) }
	switch t := c.expandAliasChain(t).(type) {
	case *soltype.FuncType:
		return true
	case *soltype.UnionType:
		return slices.ContainsFunc(t.Types, member)
	case *soltype.IntersectionType:
		return slices.ContainsFunc(t.Types, member)
	case *soltype.TypeVarType:
		if seen.Contains(t) {
			return false
		}
		seen.Add(t)
		defer seen.Remove(t)
		return slices.ContainsFunc(t.LowerBounds, member)
	}
	return false
}

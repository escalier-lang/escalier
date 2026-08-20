package soltype

// A complement may name a borrow. `¬(&'a T)` denotes every value that is not a borrow
// of T under `'a`, so it admits a borrow of another type, a borrow of T under a different
// lifetime, and every value that is not a borrow.
//
// The lifetime is part of what the complement names, not something being complemented
// itself. The outlives lattice has a join and a meet but no complement, so `¬'a` names
// nothing, and nothing asks for one. In constrainImplied a negated atom always crosses
// the `<:` and lands as a positive atom on the other side, where it is met or joined,
// and meetRefs and joinRefs already provide both.
//
// The polarity flip reaches a borrow's lifetime, contrary to what the shape of
// RefType.Accept suggests. NegationType.Accept flips the polarity before descending, and
// every pass that reads a lifetime reads it off the RefType node in its own EnterType,
// where the flip has already applied. The extruder is the clearest case. It calls
// extrudeLt with the polarity it was handed. Accept not walking Lt does not matter,
// because no pass relies on Accept to reach it.
//
// Excluding one borrow from another leaves an unreduced residual. valueFamilyOf draws a
// borrow over an object, a tuple, or a class instance into refCellFamily, which decides
// such a borrow against every primitive. refCellFamily carries only the cross-family
// rule, because two distinct borrows are not disjoint, so `Exclude<&'a T, &'b T>` keeps
// the shape `&'a T & ¬(&'b T)`. That matches how an object behaves, since objects are
// absent from the families for the same reason.
//
// Negation INSIDE a borrow is allowed too. A NegationType is not a RefInner, so the
// complement sits under a union or an intersection, as in
// `mut 'a ({x: number} | ¬{y: string})`, and normalizes by the ordinary rules.

// NewNegation builds the complement ¬t.
func NewNegation(t Type) Type {
	return &NegationType{Inner: t}
}

package soltype

import "fmt"

// The ¬Ref exclusion invariant: no complement ever names a borrow.
//
// A borrow carries two sorts, and only the type sort is a Boolean algebra. The
// outlives lattice has a join and a meet but no complement, so `¬'a` names nothing.
//
// The decision procedure never asks for a complemented lifetime. In constrainImplied a
// negated atom always crosses the `<:` and lands as a positive atom on the other side,
// where it is met or joined, and meetRefs and joinRefs already provide both.
//
// The polarity flip a complement applies DOES reach a borrow's lifetime, contrary to
// what the shape of RefType.Accept suggests. NegationType.Accept flips the polarity
// before descending, and every pass that reads a lifetime reads it off the RefType node
// in its own EnterType, where the flip has already applied. The extruder is the clearest
// case, calling extrudeLt with the polarity it was handed. Accept not walking Lt does
// not matter, because no pass relies on Accept to reach it.
//
// The second reason to exclude a complemented borrow is that the result often would not
// reduce. valueFamilyOf draws a borrow over an object, a tuple, or a class instance into
// refCellFamily, so such a borrow is disjoint from every primitive and a complement of it
// does decide against one. refCellFamily carries only the cross-family rule, because two
// distinct borrows are not disjoint. So excluding one borrow from another still yields
// back the same `T & ¬(&'a U)` rather than a simplified type.
//
// See escalier-lang/escalier#1125 for the measurements behind both reasons.
//
// Negation INSIDE a borrow is allowed. A NegationType is not a RefInner, so the
// complement sits under a union or an intersection, as in
// `mut 'a ({x: number} | ¬{y: string})`, and normalizes by the ordinary rules.

// AssertNegatable panics when t is a borrow, the one operand a complement may not
// name. It panics rather than reporting an error, following AsProperty. Call it
// wherever a complement is built or taken apart.
func AssertNegatable(t Type) {
	if r, ok := t.(*RefType); ok {
		panic(fmt.Sprintf("AssertNegatable: forbidden complement of the borrow %s", Print(r)))
	}
}

// NewNegation builds the complement ¬t, enforcing the invariant where the operand is
// chosen. A site that RE-MINTS an existing complement around a rewritten operand
// chooses nothing and does not go through it. NegationType.Accept and the solver's
// foldNegation are both that shape, and coalescing a `¬α` whose α inlines to a
// borrow lands there.
func NewNegation(t Type) Type {
	AssertNegatable(t)
	return &NegationType{Inner: t}
}

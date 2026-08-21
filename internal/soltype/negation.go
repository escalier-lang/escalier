package soltype

// A complement may name a borrow. `~(&'a T)` denotes every value that is not a borrow
// of T under `'a`, so it admits a borrow of another type, a borrow of T under a different
// lifetime, and every value that is not a borrow.
//
// The lifetime is part of what the complement names, not something being complemented
// itself. The outlives lattice has a join and a meet but no complement, so `~'a` names
// nothing, and nothing asks for one. In constrainImplied a negated atom always crosses
// the `<:` and lands as a positive atom on the other side, where it is met or joined,
// and meetRefs and joinRefs already provide both.
//
// Excluding one borrow from another leaves an unreduced residual, since refCellFamily
// carries only the cross-family rule and two distinct borrows are not disjoint. So
// `Exclude<&'a T, &'b T>` keeps the shape `&'a T & ~(&'b T)`, as excluding one object
// from another does.
//
// Negation INSIDE a borrow is allowed too. A NegationType is not a RefInner, so the
// complement sits under a union or an intersection, as in
// `mut 'a ({x: number} | ~{y: string})`, and normalizes by the ordinary rules.

// NewNegation builds the complement ~t.
func NewNegation(t Type) Type {
	return &NegationType{Inner: t}
}

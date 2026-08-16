package soltype

import "fmt"

// The ¬Ref exclusion invariant: no complement ever names a borrow.
//
// A borrow carries two sorts, and only the type sort is a Boolean algebra. The
// outlives lattice has a join and a meet but no complement, so `¬'a` names nothing.
// Lifetime coalescing also mis-classifies a borrow under a complement and drops its
// name from the rendered signature. See escalier-lang/escalier#1125, which carries
// the measurements and a fix for the second half.
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

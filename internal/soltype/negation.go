package soltype

import "fmt"

// The ¬Ref exclusion invariant: no complement ever names a borrow.
//
// A borrow carries two sorts. Its Inner is a type and its Lt is a lifetime. Only
// the type sort is a Boolean algebra. The outlives lattice the lifetime sort is
// solved in has a join and a meet but no complement, so `¬'a` names nothing. A
// `¬(mut 'a T)` node would therefore carry a lifetime with no reading.
//
// The polarity machinery makes the same point from the other side. A complement
// shrinks as its operand grows, so NegationType.Accept visits its operand at the
// flipped polarity. RefType.Accept does not walk Lt at all, because a Lifetime is
// not a Type, so that flip stops at the wrapper. A `¬(mut 'a T)` would keep 'a
// pointing the way the un-negated borrow left it, and every outlives constraint
// derived from it would run backwards.
//
// Both readings say the same thing. A complement of a borrow has no sound
// lifetime, and a solver that admitted one would owe a rule flipping the
// lifetime's outlives direction. Ruling the node out at construction is what
// removes that obligation rather than discharging it. A borrow stays an opaque
// atom of the structural constraint rules, and the normalization layer never takes
// one apart.
//
// Negation INSIDE a borrow is a different node and is allowed. A NegationType is
// not itself a RefInner, so the complement sits under a union or an intersection,
// which are. `mut 'a ({x: number} | ¬{y: string})` puts it in the pure type sort,
// where it normalizes by the ordinary rules while the wrapper stays opaque.

// AssertNegatable panics when t is a borrow, the one operand a complement may not
// name. Call it wherever a complement is about to be built or taken apart.
//
// It panics rather than reporting an error, following AsProperty. A caller that
// reaches here has already built a type the invariant forbids, so the bug is in
// the layer that built it.
func AssertNegatable(t Type) {
	if r, ok := t.(*RefType); ok {
		panic(fmt.Sprintf("AssertNegatable: forbidden complement of the borrow %s", Print(r)))
	}
}

// NewNegation builds the complement ¬t, enforcing the ¬Ref exclusion invariant
// where the operand is chosen. Every site that decides what to complement goes
// through it.
//
// A site that RE-MINTS an existing complement around a rewritten operand does not,
// because it chooses nothing. NegationType.Accept and the solver's foldNegation
// are both that shape: some rewriter already replaced the operand, and the
// decision to complement it was made wherever the node was first built. Coalescing
// a `¬α` whose α inlines to a borrow lands there, and rejecting it would fail a
// display pass over a type the solver reached legitimately.
func NewNegation(t Type) Type {
	AssertNegatable(t)
	return &NegationType{Inner: t}
}

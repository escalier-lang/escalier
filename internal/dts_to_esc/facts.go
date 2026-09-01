package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ecma262"
)

// facts.go is the ECMA-262 classification source of
// planning/ecma-262/implementation_plan.md §7, requirements.md FR8. It ranks
// spec-derived receiver mutability above the converter's name tiers and below
// the explicit signals an author wrote into the `.d.ts`.
//
// Receiver mutability is the only determination applied from a fact without
// review. Parameter disposition, the return-borrow seed, `throws`, and
// `rejects` reach the `.esc` through the hand-curated override layer of §11
// instead.

// ReceiverFacts answers the receiver mutability an ECMA-262 fact publishes for
// one member of one owner. The owner is the member's dotted runtime path, so
// `Array.prototype.push` is looked up as the member `push` of the owner
// `Array`, and a builtin nested in a namespace is the longer owner
// `Intl.DateTimeFormat`.
//
// The nil value answers no member, which leaves every method to the name
// tiers. A caller with no fact source passes nil rather than branching.
type ReceiverFacts struct {
	join *ecma262.Join
}

// NewReceiverFacts indexes facts by the member each one addresses. A nil fact
// set returns a nil source, which answers no member.
func NewReceiverFacts(facts *ecma262.Facts) *ReceiverFacts {
	if facts == nil {
		return nil
	}
	return &ReceiverFacts{join: ecma262.NewJoin(facts)}
}

// Instance reports the receiver mutability published for an instance member of
// owner, true for `mutBorrow`. ok is false when no fact addresses the member,
// and when the one that does carries `none`, which is a builtin taking no
// receiver at all rather than one it leaves unwritten.
//
// Only instance members resolve here. A static and a namespace function have
// no receiver for the converter to annotate. An accessor's polarity is fixed by
// tier 3, which is consulted before this lookup.
func (f *ReceiverFacts) Instance(owner string, member ecma262.MemberKey) (mut bool, ok bool) {
	if f == nil || owner == "" {
		return false, false
	}
	ref := ecma262.MemberRef{
		Owner:    owner,
		Member:   member,
		Sort:     ecma262.SortInstance,
		Accessor: ecma262.NotAccessor,
	}
	_, fact, found := f.join.Lookup(ref)
	if !found {
		return false, false
	}
	switch fact.Receiver {
	case ecma262.RecvMutBorrow:
		return true, true
	case ecma262.RecvBorrow:
		return false, true
	default:
		return false, false
	}
}

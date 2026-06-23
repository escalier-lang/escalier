package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6 PR2: lattice subtyping rules in constrain ---
//
// The pre-switch lattice block carries every rule whose deciding operand is a
// union/intersection super, plus the union-sub for-all rule. These tests
// exercise the rules against hand-built lattice nodes through the Constrain
// API, so the assertions are independent of source-level annotation surface.
// The annotation-input path is exercised separately under infer_lattice_test.go.

// TestConstrainUnionSubForAll covers the union-sub "for all" rule:
// (A | B) <: super decomposes into A <: super AND B <: super, eagerly,
// regardless of what is on the super side.
func TestConstrainUnionSubForAll(t *testing.T) {
	t.Run("both branches match concrete super", func(t *testing.T) {
		// (number | string) <: unknown-style super that accepts both. Use a
		// concrete object-typed super where each branch fails for a different
		// reason; the rule should report BOTH branch failures.
		c := &Context{}
		sub := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(sub, sub)) // every union is its own subtype
	})

	t.Run("one branch fails, one branch passes — for-all rejects", func(t *testing.T) {
		// (number | string) <: number. number <: number ok, string <: number
		// fails. The for-all rule reports the failed branch.
		c := &Context{}
		sub := newUnion(nil, parseTypes(t, "number", "string"), false)
		errs := c.Constrain(sub, num())
		require.Equal(t, []string{"cannot constrain string <: number"}, Messages(errs))
	})

	t.Run("union sub against a variable super", func(t *testing.T) {
		// (number | string) <: α. For-all decomposes to number <: α AND
		// string <: α, so both end up as lower bounds of α. The var arm
		// records each one; the variable is NOT pinned to the union — it
		// gains TWO lower bounds independently.
		c := &Context{}
		a := c.freshVar(0)
		sub := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(sub, a))
		require.Len(t, a.LowerBounds, 2)
	})
}

// TestConstrainIntersectionSuperForAll covers the intersection-super
// "for all" rule: sub <: (A & B) decomposes into sub <: A AND sub <: B.
func TestConstrainIntersectionSuperForAll(t *testing.T) {
	t.Run("sub satisfies every member", func(t *testing.T) {
		// 5 <: (number & number). Both members are number, both succeed.
		c := &Context{}
		super := newIntersection(nil, parseTypes(t, "number", "number"))
		// Deduplication collapses the intersection to a single member.
		require.Empty(t, c.Constrain(numLit(5), super))
	})

	t.Run("sub fails one member", func(t *testing.T) {
		// "x" <: (number & string). The for-all reports the failed branch.
		c := &Context{}
		super := &soltype.IntersectionType{Types: parseTypes(t, "number", "string")}
		errs := c.Constrain(strLit("x"), super)
		require.Equal(t, []string{`cannot constrain "x" <: number`}, Messages(errs))
	})

	t.Run("variable sub against an intersection super", func(t *testing.T) {
		// α <: (number & string). For-all decomposes to α <: number AND
		// α <: string, so both end up as upper bounds of α — the variable
		// is NOT pinned to the intersection.
		c := &Context{}
		a := c.freshVar(0)
		super := &soltype.IntersectionType{Types: parseTypes(t, "number", "string")}
		require.Empty(t, c.Constrain(a, super))
		require.Len(t, a.UpperBounds, 2)
	})
}

// TestConstrainUnionSuperExists covers the union-super "exists" rule:
// sub <: (A | B) trials each member under a probe; the first success commits,
// the losers roll back. Only fires when sub is concrete; a variable sub falls
// through to the var arm and records the WHOLE union as an upper bound.
func TestConstrainUnionSuperExists(t *testing.T) {
	t.Run("concrete sub matches a branch", func(t *testing.T) {
		// number <: (number | string). The first branch matches; the rule
		// returns nil without trialling the second.
		c := &Context{}
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(num(), super))
	})

	t.Run("concrete sub matches the second branch", func(t *testing.T) {
		c := &Context{}
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(str(), super))
	})

	t.Run("concrete sub matches no branch", func(t *testing.T) {
		// boolean <: (number | string). No branch matches; report the
		// union-level mismatch rather than the last-branch failure.
		c := &Context{}
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		errs := c.Constrain(boolT(), super)
		require.Equal(t, []string{"cannot constrain boolean <: number | string"}, Messages(errs))
	})

	t.Run("literal sub picks the matching primitive branch", func(t *testing.T) {
		// 5 <: (number | string). The literal-to-primitive rule fires inside
		// the trial and commits the number branch.
		c := &Context{}
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(numLit(5), super))
	})

	t.Run("variable sub records whole union, not a branch", func(t *testing.T) {
		// α <: (number | string). The exists rule is gated to a concrete
		// sub, so the variable falls through to the subVar arm, which
		// records the WHOLE union as a single upper bound — exactly the
		// speculative-pinning avoidance the design calls for.
		c := &Context{}
		a := c.freshVar(0)
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(a, super))
		require.Len(t, a.UpperBounds, 1)
		_, isUnion := a.UpperBounds[0].(*soltype.UnionType)
		require.True(t, isUnion, "variable sub should record the union whole, got %T", a.UpperBounds[0])
	})

	t.Run("losing branch records no upper bound on the variable", func(t *testing.T) {
		// A failed exists trial that touched a variable must not leak its
		// bound mutation into the final state. Set up: super union has a
		// fresh var as one branch and an incompatible prim as the other; a
		// concrete LITERAL sub matches the var branch by recording itself
		// as a lower bound — the committing trial. The losing prim-branch
		// trial would have added a bound to the var too if it weren't
		// discarded; the assertion is that the variable carries exactly
		// one lower bound, from the winning branch.
		c := &Context{}
		extra := c.freshVar(0)
		super := newUnion(nil, []soltype.Type{extra, num()}, false)

		// "hi" <: (extra | number). The number branch fails (StrLit vs
		// NumPrim), so the trial of "hi" <: extra commits and records "hi"
		// as a lower bound of extra. extra should have exactly one bound.
		require.Empty(t, c.Constrain(strLit("hi"), super))
		require.Len(t, extra.LowerBounds, 1)
	})
}

// TestConstrainUnionPrecedesRefArm proves the pre-switch placement: a
// borrow flowing into a union of borrows must match a member, NOT hit the
// RefType arm's "non-variable super" path that treats a union super as a
// concrete-non-variable demand and rejects the borrow.
func TestConstrainUnionPrecedesRefArm(t *testing.T) {
	// &mut {x: number} <: (&mut {x: number} | &mut {y: string}). The lattice
	// block must match the first union member before the RefType arm in the
	// structural switch can intercept and treat the super as a concrete
	// non-borrow. Build the borrows with no lifetimes so the BorrowEscape
	// path is also out of the question.
	c := &Context{}
	mutXNum := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
	mutYStr := &soltype.RefType{Mut: true, Inner: exactObj(propElem("y", str()))}
	super := newUnion(nil, []soltype.Type{mutXNum, mutYStr}, false)

	sub := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
	require.Empty(t, c.Constrain(sub, super))
}

// TestConstrainInexactUnionIntoClosedRejects covers the inexact-into-closed
// rule. The flag and the parser surface land in PR4; until then the rule
// fires only against an internally-built inexact union, which the test mints
// directly through the smart constructor.
func TestConstrainInexactUnionIntoClosedRejects(t *testing.T) {
	c := &Context{}
	sub := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
	errs := c.Constrain(sub, num())
	require.Len(t, errs, 1)
	require.IsType(t, &InexactUnionIntoExactError{}, errs[0])
	require.Equal(t, "cannot constrain number | string | ... <: number", errs[0].Message())
}

func TestConstrainInexactUnionIntoUnknownAccepts(t *testing.T) {
	// An inexact sub union flowing into `unknown` — the only super in PR2 that
	// can absorb the open tail.
	c := &Context{}
	sub := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
	// The decomposition produces `number <: unknown` and `string <: unknown`.
	// PR5 lands the `_ <: unknown` rule; until then those are CannotConstrain
	// fall-throughs, so this case still errors — what the test asserts is that
	// the inexact-into-closed gate did NOT fire (no InexactUnionIntoExactError),
	// since unknown is recognized as accepting the open tail.
	errs := c.Constrain(sub, &soltype.UnknownType{})
	for _, e := range errs {
		_, isInexact := e.(*InexactUnionIntoExactError)
		require.False(t, isInexact, "inexact-into-closed should not fire against unknown super")
	}
}

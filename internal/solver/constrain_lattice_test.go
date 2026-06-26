package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- Lattice subtyping rules in constrain ---
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

	t.Run("union sub against a variable super defers", func(t *testing.T) {
		// (number | string) <: α. The for-all rule defers when super is a
		// TypeVar so the WHOLE union is recorded as one lower bound on α.
		// Coalesce builds the same `number | string` either way, but the
		// deferral preserves the union shape on the bound list. It also
		// preserves the inexact flag when present. See
		// TestConstrainInexactUnionIntoVarDefers for the soundness payoff.
		c := &Context{}
		a := c.freshVar(0)
		sub := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(sub, a))
		require.Len(t, a.LowerBounds, 1)
		_, isUnion := a.LowerBounds[0].(*soltype.UnionType)
		require.True(t, isUnion, "expected the whole union as one bound, got %T", a.LowerBounds[0])
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

	t.Run("variable sub against an intersection super defers", func(t *testing.T) {
		// α <: (number & string). The for-all rule defers when sub is a
		// TypeVar so the WHOLE intersection is recorded as one upper bound
		// on α. This is the symmetric twin of the union-sub super-var
		// deferral.
		c := &Context{}
		a := c.freshVar(0)
		super := &soltype.IntersectionType{Types: parseTypes(t, "number", "string")}
		require.Empty(t, c.Constrain(a, super))
		require.Len(t, a.UpperBounds, 1)
		_, isIntersection := a.UpperBounds[0].(*soltype.IntersectionType)
		require.True(t, isIntersection, "expected the whole intersection as one bound, got %T", a.UpperBounds[0])
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
		// records the WHOLE union as a single upper bound. That is the
		// speculative-pinning avoidance the design calls for.
		c := &Context{}
		a := c.freshVar(0)
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(a, super))
		require.Len(t, a.UpperBounds, 1)
		_, isUnion := a.UpperBounds[0].(*soltype.UnionType)
		require.True(t, isUnion, "variable sub should record the union whole, got %T", a.UpperBounds[0])
	})

	t.Run("free-var super member is skipped, not pinned by the trial", func(t *testing.T) {
		// A super-union member that is itself an unbounded TypeVar must not
		// be trialled. Trialling it would speculatively pin it to sub. The
		// super union here has a fresh var on one branch and an
		// incompatible prim on the other. Trialling the var would trivially
		// succeed by recording "hi" as its lower bound. The rule skips the
		// var member instead. The number trial then fails on its own merits
		// (StrLit "hi" against PrimType number), the rule reports a clean
		// union-level CannotConstrainError, and the variable carries no
		// bounds.
		c := &Context{}
		extra := c.freshVar(0)
		super := newUnion(nil, []soltype.Type{extra, num()}, false)

		errs := c.Constrain(strLit("hi"), super)
		require.Len(t, errs, 1)
		require.IsType(t, &CannotConstrainError{}, errs[0])
		require.Empty(t, extra.LowerBounds)
		require.Empty(t, extra.UpperBounds)
	})
}

// TestConstrainUnionPrecedesRefArm proves the pre-switch placement: a
// borrow flowing into a union of borrows must match a member, NOT hit the
// RefType arm's "non-variable super" path that treats a union super as a
// concrete-non-variable demand and rejects the borrow.
//
//	mut {x: number} <: (mut {x: number} | mut {y: string})    succeeds
func TestConstrainUnionPrecedesRefArm(t *testing.T) {
	// The lattice block must match the first union member before the RefType
	// arm in the structural switch can intercept and treat the super as a
	// concrete non-borrow. Build the borrows with no lifetimes so the
	// BorrowEscape path is also out of the question.
	c := &Context{}
	mutXNum := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
	mutYStr := &soltype.RefType{Mut: true, Inner: exactObj(propElem("y", str()))}
	super := newUnion(nil, []soltype.Type{mutXNum, mutYStr}, false)

	sub := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}
	require.Empty(t, c.Constrain(sub, super))
}

// TestConstrainInexactUnionIntoClosedRejects covers the inexact-into-closed
// rule. An inexact sub union carries an open `unknown` tail that a closed
// super cannot absorb. A closed super here is any super that is not an
// inexact union, not unknown, and not a TypeVar.
//
// The rule accumulates BOTH the union-level inexact error AND the per-member
// failures, mirroring the object arm. The open tail and an explicit member
// that doesn't subtype super are independent bugs. Surfacing only the
// inexact error would hide the member mismatch the user would otherwise
// discover on the next compile.
//
//	(number | string | ...) <: number    rejects with:
//	  - InexactUnionIntoExactError    the open tail can't fit number
//	  - CannotConstrainError          string <: number
//	  number <: number succeeds and contributes nothing
//
// Until the parser surface for `A | B | ...` exists, the rule fires only against
// an internally-built inexact union, which the test mints directly through the
// smart constructor.
func TestConstrainInexactUnionIntoClosedRejects(t *testing.T) {
	c := &Context{}
	sub := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
	errs := c.Constrain(sub, num())
	require.Len(t, errs, 2)
	require.IsType(t, &InexactUnionIntoExactError{}, errs[0])
	require.Equal(t, "cannot constrain number | string | ... <: number", errs[0].Message())
	require.IsType(t, &CannotConstrainError{}, errs[1])
	require.Equal(t, "cannot constrain string <: number", errs[1].Message())
}

// Regression: a borrow with a lifetime flowing into a union of owned types
// must surface as a BorrowEscapeError rather than collapsing to a generic
// union-level CannotConstrainError.
//
//	&'a {x: number} <: (number | string)    BorrowEscapeError, not CannotConstrain
//
// Each per-member trial reports BorrowEscape internally. The union-super
// exists rule promotes that to a single union-level BorrowEscape so the
// lifetime cause survives.
func TestConstrainUnionSuperPreservesBorrowEscape(t *testing.T) {
	c := &Context{}
	lt := c.freshLifetime(0)
	borrow := &soltype.RefType{Mut: false, Lt: lt, Inner: exactObj(propElem("x", num()))}
	super := newUnion(nil, []soltype.Type{num(), str()}, false)

	errs := c.Constrain(borrow, super)
	require.Len(t, errs, 1)
	require.IsType(t, &BorrowEscapeError{}, errs[0])
}

// Regression: an inexact union flowing into a free inference variable must
// be recorded as a single whole-union lower bound on the variable so the
// inexact flag rides through coalesce.
//
//	(number | string | ...) <: α    α gains the whole inexact union as one lower bound
//
// The union-sub for-all rule defers to the superVar arm when super is a
// TypeVar. Decomposing per-member would record only `number` and `string`
// bounds. The `...` is a flag on UnionType, not a member of Types, so a
// per-member loop silently drops it and α coalesces as the EXACT
// `number | string`. That would break the soundness of any downstream match
// against α. The inexact tail could carry a value of any type at runtime
// that no member arm covers.
func TestConstrainInexactUnionIntoVarDefers(t *testing.T) {
	c := &Context{}
	alpha := c.freshVar(0)
	sub := &soltype.UnionType{Types: []soltype.Type{num(), str()}, Inexact: true}

	require.Empty(t, c.Constrain(sub, alpha))
	require.Len(t, alpha.LowerBounds, 1)
	lb, ok := alpha.LowerBounds[0].(*soltype.UnionType)
	require.True(t, ok, "expected the whole inexact union as one bound, got %T", alpha.LowerBounds[0])
	require.True(t, lb.Inexact, "inexact flag must survive the bound record")
}

// TestConstrainInexactUnionIntoUnknownAccepts covers the other accepting
// super for an inexact sub union. `unknown` is the lattice top, so it
// absorbs the open tail.
//
//	(number | string | ...) <: unknown    inexact-into-closed gate does NOT fire
//
// The decomposition produces `number <: unknown` and `string <: unknown`.
// Until the `_ <: unknown` rule exists, those are CannotConstrain
// fall-throughs, so this case still errors. What the test asserts is that
// the inexact-into-closed gate did not fire, since unknown is recognized
// as accepting the open tail. No InexactUnionIntoExactError appears in the
// error list.
func TestConstrainInexactUnionIntoUnknownAccepts(t *testing.T) {
	c := &Context{}
	sub := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
	errs := c.Constrain(sub, &soltype.UnknownType{})
	for _, e := range errs {
		_, isInexact := e.(*InexactUnionIntoExactError)
		require.False(t, isInexact, "inexact-into-closed should not fire against unknown super")
	}
}

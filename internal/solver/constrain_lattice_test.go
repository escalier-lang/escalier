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

	t.Run("free-var super member is trialled last as a catch-all", func(t *testing.T) {
		// A super-union member that is itself an unbounded TypeVar is trialled after every
		// concrete member, since specificityOrder ranks a variable below every concrete. The
		// super union here has a fresh var on one branch and an incompatible prim on the
		// other. The number trial fails on its own merits (StrLit "hi" against PrimType
		// number), so the rule falls through to the var branch: `"hi" <: extra` records "hi"
		// as extra's lower bound and commits. The var member is a last-resort catch-all, not
		// a speculative first pin, because it is reached only when no concrete member matches.
		c := &Context{}
		extra := c.freshVar(0)
		super := newUnion(nil, []soltype.Type{extra, num()}, false)

		hi := strLit("hi")
		require.Empty(t, c.Constrain(hi, super))
		require.Len(t, extra.LowerBounds, 1)
		require.Same(t, hi, extra.LowerBounds[0])
		require.Empty(t, extra.UpperBounds)
	})

	t.Run("concrete match commits before the var member and leaves it unpinned", func(t *testing.T) {
		// 5 <: (T | number). The number member is trialled before the bare var T, since
		// specificityOrder ranks a variable below every concrete, so `5 <: number` commits
		// and the trial never reaches `5 <: T`. T is left with no bounds, proving the var
		// member is a last-resort catch-all rather than a speculative first pin.
		c := &Context{}
		tv := c.freshVar(0)
		super := newUnion(nil, []soltype.Type{tv, num()}, false)

		require.Empty(t, c.Constrain(numLit(5), super))
		require.Empty(t, tv.LowerBounds)
		require.Empty(t, tv.UpperBounds)
	})
}

// TestConstrainInexactUnionSuperAcceptsViaTail covers the union-super exists rule for
// an INEXACT super. The open tail is unknown-typed, so a concrete sub that matches no
// named member is still subsumed by the tail and accepted, rather than reported as a
// union-level CannotConstrainError.
//
//	boolean <: (number | string | ...)    accepted via the open tail
//
// This is the dual of TestConstrainInexactUnionIntoClosedRejects, where an inexact SUB
// into a closed super is rejected because that tail can't be absorbed. The parser surface
// for `A | B | ...` lands in PR4, so until then the rule fires only against an
// internally-built inexact union, minted directly through the smart constructor.
func TestConstrainInexactUnionSuperAcceptsViaTail(t *testing.T) {
	c := &Context{}
	t.Run("non-member accepted via the open tail", func(t *testing.T) {
		super := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
		require.Empty(t, c.Constrain(boolT(), super))
	})
	t.Run("a named member still commits its branch", func(t *testing.T) {
		super := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
		require.Empty(t, c.Constrain(num(), super))
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
// The flag and the parser surface for `A | B | ...` land in PR4. Until then
// the rule fires only against an internally-built inexact union, which the
// test mints directly through the smart constructor.
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

// Regression: a borrow with a lifetime that would satisfy a union member once
// its lifetime is stripped must surface as a union-level BorrowEscapeError
// rather than collapsing to a generic CannotConstrainError. The shape here is
// the meaningful one: branch 2 of the union matches the borrow's inner, so the
// lifetime is the genuine blocker.
//
//	&'a {x: number} <: (number | {x: number})    BorrowEscapeError, not CannotConstrain
//
// The checker decides this by peeling the borrow's inner against the whole
// union: one matching branch promotes a union-level BorrowEscape so the lifetime
// cause survives. Contrast TestBorrowEscapePromotionByPeeledInner, where no
// branch matches the inner, so the union error stays a shape mismatch.
func TestConstrainUnionSuperPreservesBorrowEscape(t *testing.T) {
	c := &Context{}
	lt := c.freshLifetime(0)
	borrow := &soltype.RefType{Mut: false, Lt: lt, Inner: exactObj(propElem("x", num()))}
	super := newUnion(nil, []soltype.Type{num(), exactObj(propElem("x", num()))}, false)

	errs := c.Constrain(borrow, super)
	require.Len(t, errs, 1)
	require.Equal(t,
		"borrowed value object does not live long enough to satisfy number | object",
		errs[0].Message())
}

// TestBorrowEscapePromotionByPeeledInner pins the firing condition for both the
// single-trial RefType arm and the union-level promotion. BorrowEscapeError is
// emitted only when peeling the borrow's inner would have satisfied the
// destination — when the lifetime is the genuine blocker. When the inner is
// itself a shape mismatch, the clearer shape error surfaces instead, so
// "does not live long enough" never blames the lifetime for a mismatch that
// extending it could not fix.
func TestBorrowEscapePromotionByPeeledInner(t *testing.T) {
	// A fresh immutable borrow of `{x: number}` with a lifetime, rebuilt per
	// case so trials never share bound state across the table.
	borrow := func(c *Context) *soltype.RefType {
		return &soltype.RefType{Mut: false, Lt: c.freshLifetime(0), Inner: exactObj(propElem("x", num()))}
	}

	tests := []struct {
		name  string
		super func() soltype.Type
		want  string
	}{
		{
			// Inner {x: number} <: number fails — shape, not lifetime. Surface the
			// shape error rather than BorrowEscape.
			name:  "non-union shape mismatch surfaces the shape error",
			super: func() soltype.Type { return num() },
			want:  "cannot constrain object <: number",
		},
		{
			// Inner {x: number} <: {x: number} succeeds — the lifetime IS the blocker.
			name:  "non-union shape match keeps BorrowEscape",
			super: func() soltype.Type { return exactObj(propElem("x", num())) },
			want:  "borrowed value object does not live long enough to satisfy object",
		},
		{
			// Every union branch is a shape mismatch — the lifetime is incidental.
			name:  "union with no matching branch surfaces the shape error",
			super: func() soltype.Type { return newUnion(nil, []soltype.Type{num(), str()}, false) },
			want:  "cannot constrain object <: number | string",
		},
		{
			// Branch 2 matches the inner — the lifetime IS the blocker.
			name:  "union with a matching branch keeps BorrowEscape",
			super: func() soltype.Type {
				return newUnion(nil, []soltype.Type{num(), exactObj(propElem("x", num()))}, false)
			},
			want: "borrowed value object does not live long enough to satisfy number | object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(borrow(c), tt.super())
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
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

// TestConstrainInexactUnionIntoUnknownAccepts covers the other accepting super for
// an inexact sub union. unknown is the lattice top, so it absorbs the open tail.
//
//	(number | string | ...) <: unknown    accepted with no error
//
// The `_ <: unknown` rule short-circuits any sub against an unknown super, so the
// constraint succeeds before the inexact-into-closed gate is reached. The gate
// must not fire against unknown, since unknown accepts the open tail.
func TestConstrainInexactUnionIntoUnknownAccepts(t *testing.T) {
	c := &Context{}
	sub := &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true}
	require.Empty(t, c.Constrain(sub, &soltype.UnknownType{}))
}

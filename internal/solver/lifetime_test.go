package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A lifetime variable on the LEFT of an outlives constraint gains the right as an
// upper bound; 'static on the right is the top of the lattice, so the constraint
// holds with NO bound recorded.
func TestConstrainLtVarOutlivesStatic(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	static := &soltype.StaticLifetime{}

	c.ctx.constrainLt(a, static)

	require.Equal(t, []soltype.Lifetime{static}, a.UpperBounds, "a <: 'static records 'static as a's upper bound")
	require.Empty(t, a.LowerBounds)
}

// A var on the left gains an upper bound; a var on the right gains a lower bound.
// A var-to-var constraint records BOTH directions so each variable sees the full
// relationship at coalescing.
func TestConstrainLtVarToVarRecordsBothDirections(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, b) // a outlives b

	require.Equal(t, []soltype.Lifetime{b}, a.UpperBounds, "a gains b as an upper bound")
	require.Empty(t, a.LowerBounds)
	require.Equal(t, []soltype.Lifetime{a}, b.LowerBounds, "b gains a as a lower bound")
	require.Empty(t, b.UpperBounds)
}

// Transitivity: with a <: b already recorded, constraining x <: a propagates
// through a's existing upper bounds so x <: b is recorded too.
func TestConstrainLtPropagatesTransitively(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()
	x := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, b) // a <: b
	c.ctx.constrainLt(x, a) // x <: a, which must propagate x <: b through a's uppers

	require.Contains(t, x.UpperBounds, soltype.Lifetime(a), "x gains a directly")
	require.Contains(t, x.UpperBounds, soltype.Lifetime(b), "x gains b transitively through a")
}

// Two DISTINCT 'static values denote the one lattice top, so constraining a
// variable against each in turn records a single 'static upper bound — dedup is by
// value, not pointer. Origination sites mint a fresh &StaticLifetime{} per call, so
// pointer-identity dedup would wrongly pile up duplicate 'static bounds.
func TestConstrainLtStaticDedupsByValue(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, &soltype.StaticLifetime{})
	c.ctx.constrainLt(a, &soltype.StaticLifetime{}) // a different 'static instance
	c.ctx.constrainLt(a, soltype.Static)            // and the canonical singleton

	require.Len(t, a.UpperBounds, 1, "all three 'static constraints collapse to one upper bound")
	require.True(t, soltype.IsStaticLifetime(a.UpperBounds[0]))
}

// A repeated outlives constraint does not re-append a bound already present.
func TestConstrainLtDedupsBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, b)
	c.ctx.constrainLt(a, b) // identical constraint again

	require.Len(t, a.UpperBounds, 1, "the duplicate upper bound is not re-appended")
	require.Len(t, b.LowerBounds, 1, "the duplicate lower bound is not re-appended")
}

// A transitive cycle terminates: 'a <: 'b together with 'b <: 'a must not loop,
// and each direction is recorded exactly once.
func TestConstrainLtCycleTerminates(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	require.NotPanics(t, func() {
		c.ctx.constrainLt(a, b)
		c.ctx.constrainLt(b, a) // closes the cycle
	})

	require.Len(t, a.UpperBounds, 1)
	require.Len(t, a.LowerBounds, 1)
	require.Len(t, b.UpperBounds, 1)
	require.Len(t, b.LowerBounds, 1)
}

// Constraining a lifetime against ITSELF is a no-op — neither a bound nor a loop.
func TestConstrainLtReflexiveIsNoOp(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, a)

	require.Empty(t, a.UpperBounds)
	require.Empty(t, a.LowerBounds)
}

// A discarded probe truncates every lifetime bound the trial appended back to the
// pre-probe length, exactly as it does for type-variable bounds — the second sort
// rides the same journal discipline. Bounds added before the probe survive.
func TestProbeDiscardRestoresLifetimeBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	// Pre-probe bound on a, permanent.
	c.ctx.constrainLt(a, b)
	require.Len(t, a.UpperBounds, 1)

	p := c.openProbe()
	x := c.ctx.freshLifetime()
	c.ctx.constrainLt(a, x) // a.UpperBounds: +1 ⇒ 2; x.LowerBounds: +1 ⇒ 1
	require.Len(t, a.UpperBounds, 2)
	require.Len(t, x.LowerBounds, 1)
	require.Len(t, p.ltEntries, 2, "both touched lifetime vars are journaled")

	c.closeProbe(p, false) // discard

	require.Len(t, a.UpperBounds, 1, "the speculative upper bound on a is truncated away")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0], "the pre-probe bound survives")
	require.Empty(t, x.LowerBounds, "x's only bound was speculative")
}

// A committed lifetime-bound mutation survives — discard is what reverts, not the
// journal's existence.
func TestProbeCommitKeepsLifetimeBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	p := c.openProbe()
	c.ctx.constrainLt(a, b)
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(p, true) // commit

	require.Len(t, a.UpperBounds, 1, "a committed probe makes the lifetime bound permanent")
}

// A committed child hands its lifetime-bound rollback obligation up to the parent,
// so the parent's later discard still reverts the committed child's lifetime work.
func TestProbeLifetimeCommittedChildCoveredByParentDiscard(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	parent := c.openProbe()
	child := c.openProbe()
	c.ctx.constrainLt(a, b) // child mutates a and b
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(child, true) // child commits — a/b become the parent's obligation

	c.closeProbe(parent, false) // parent discards
	require.Empty(t, a.UpperBounds, "the parent discard reverts the committed child's lifetime bound")
	require.Empty(t, b.LowerBounds)
}

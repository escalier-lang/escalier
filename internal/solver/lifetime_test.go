package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A lifetime variable on the LEFT of an outlives constraint gains the right as an
// upper bound. 'static is the top of the lattice, so `a <: 'static` always holds;
// it is still recorded as a's upper bound, which is what drives a to 'static at
// coalescing.
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

// Test 1 — the lower-bound propagation branch. TestConstrainLtPropagatesTransitively
// exercises propagation through the SUPER variable's upper bounds; this exercises the
// distinct `subVar.LowerBounds` loop: with lb <: a already recorded, constraining
// a <: super must propagate lb <: super through a's existing lower bound.
func TestConstrainLtPropagatesThroughLowerBounds(t *testing.T) {
	c := newChecker()
	lb := c.ctx.freshLifetime()
	a := c.ctx.freshLifetime()
	super := c.ctx.freshLifetime()

	c.ctx.constrainLt(lb, a)    // lb <: a ⇒ a gains lb as a lower bound
	c.ctx.constrainLt(a, super) // a <: super ⇒ a's lower-bound loop propagates lb <: super

	require.Contains(t, lb.UpperBounds, soltype.Lifetime(a), "lb gains a directly")
	require.Contains(t, lb.UpperBounds, soltype.Lifetime(super), "lb gains super transitively through a's lower-bound propagation")
	require.Contains(t, super.LowerBounds, soltype.Lifetime(lb), "super sees lb as a lower bound from the same propagation")
}

// Test 2 — a probe discard rolls back vars touched TRANSITIVELY, not just the ones
// named at the constrainLt call site. With a <: b set pre-probe, a single
// constrainLt(x, a) under the probe touches x, a, AND b (x <: a <: b), and the
// discard must truncate every probe-era bound while leaving the pre-probe ones.
func TestProbeDiscardRollsBackTransitivelyTouchedLifetimes(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, b) // pre-probe: a.upper=[b], b.lower=[a]
	require.Len(t, a.UpperBounds, 1)
	require.Len(t, b.LowerBounds, 1)

	p := c.openProbe()
	x := c.ctx.freshLifetime()
	c.ctx.constrainLt(x, a) // x <: a, transitively recording x <: b; touches x, a, b
	require.Contains(t, x.UpperBounds, soltype.Lifetime(a))
	require.Contains(t, x.UpperBounds, soltype.Lifetime(b), "x gained b transitively under the probe")
	require.Len(t, a.LowerBounds, 1, "a gained x as a probe-era lower bound")
	require.Len(t, b.LowerBounds, 2, "b gained x transitively under the probe")

	c.closeProbe(p, false) // discard

	require.Empty(t, x.UpperBounds, "x was minted and constrained entirely under the probe")
	require.Len(t, a.UpperBounds, 1, "a's pre-probe upper bound survives")
	require.Empty(t, a.LowerBounds, "a's probe-era lower bound x is truncated")
	require.Len(t, b.LowerBounds, 1, "b's transitive probe-era lower bound is truncated")
	require.Equal(t, soltype.Lifetime(a), b.LowerBounds[0], "b's pre-probe lower bound survives")
}

// Test 3 — recordLt journals a lifetime var at most once per probe, even across
// several appends to it, so the single snapshot truncates every later append on
// discard. Mirrors the type sort's TestProbeRecordDedupsPerVariable. Each
// constrainLt(a, …) also touches its super var, so the probe holds three entries
// total; the point is that `a` appears in exactly one of them.
func TestProbeRecordLtDedupsPerLifetimeVar(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()
	d := c.ctx.freshLifetime()

	p := c.openProbe()
	c.ctx.constrainLt(a, b) // a.upper += b
	c.ctx.constrainLt(a, d) // a.upper += d — a SECOND append to a
	require.Len(t, a.UpperBounds, 2)

	aEntries := 0
	for _, e := range p.ltEntries {
		if e.v == a {
			aEntries++
		}
	}
	require.Equal(t, 1, aEntries, "a is journaled exactly once despite two appends")

	c.closeProbe(p, false) // discard
	require.Empty(t, a.UpperBounds, "both speculative bounds on a are truncated via the single journal entry")
}

// Test 5 — a probe built directly as &Probe{} (bypassing newProbe) is safe for the
// lifetime sort too: ltTouched is lazily created on first recordLt, so there is no
// nil-map panic. Mirrors the type sort's TestProbeBareLiteralIsNilMapSafe.
func TestProbeBareLiteralLifetimeIsNilMapSafe(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	c.ctx.probe = &Probe{} // deliberately skip newProbe
	require.NotPanics(t, func() {
		c.ctx.constrainLt(a, b) // appends bounds ⇒ recordLt(a), recordLt(b)
	})
	require.Len(t, a.UpperBounds, 1)

	c.ctx.probe.Discard()
	require.Empty(t, a.UpperBounds, "the bare-literal probe still rolls back the lifetime bound")
	require.Empty(t, b.LowerBounds)
}

// Test 6a — a discarded child reverts only ITS OWN lifetime appends, leaving the
// parent's journal and the var's parent-era bounds intact. Mirrors the type sort's
// TestDiscardedChildLeavesParentJournalIntact.
func TestProbeLifetimeDiscardedChildLeavesParentJournalIntact(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()
	d := c.ctx.freshLifetime()

	parent := c.openProbe()
	c.ctx.constrainLt(a, b) // parent: a.upper=[b]
	require.Len(t, a.UpperBounds, 1)

	child := c.openProbe()
	c.ctx.constrainLt(a, d) // child: a.upper=[b, d]
	require.Len(t, a.UpperBounds, 2)
	c.closeProbe(child, false) // child discards ⇒ back to [b]
	require.Len(t, a.UpperBounds, 1, "the child discard reverts only the child's lifetime bound")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0])

	c.closeProbe(parent, false) // parent discards ⇒ back to empty
	require.Empty(t, a.UpperBounds, "the parent discard reverts its own lifetime bound")
}

// Test 6b — when the parent has NOT touched a lifetime var the committed child did,
// the child's snapshot is inherited so the parent discard reverts the child's bound
// to the var's pre-child length. Mirrors TestCommittedChildInheritsUntouchedVarSnapshot.
func TestProbeLifetimeCommittedChildInheritsUntouchedVarSnapshot(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	parent := c.openProbe()
	child := c.openProbe()
	c.ctx.constrainLt(a, b) // only the child touches a and b
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(child, true) // commit: parent inherits a and b at snapshot 0

	c.closeProbe(parent, false) // discard
	require.Empty(t, a.UpperBounds, "the inherited child snapshot truncates a back to empty")
	require.Empty(t, b.LowerBounds)
}

// Test 7 — re-constraining a lifetime bound already present journals nothing: the
// ContainsLifetime guard skips the append, so no recordLt fires and a discard is a
// clean no-op that leaves the pre-probe bound untouched. This verifies the
// "no journal entry without an append" invariant for the lifetime sort.
func TestProbeReconstrainingPresentLifetimeBoundJournalsNothing(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime()
	b := c.ctx.freshLifetime()

	c.ctx.constrainLt(a, b) // pre-probe: a.upper=[b], b.lower=[a]

	p := c.openProbe()
	c.ctx.constrainLt(a, b) // identical constraint: both bounds already present
	require.Empty(t, p.ltEntries, "re-constraining a present bound journals nothing")
	require.Len(t, a.UpperBounds, 1, "no duplicate bound is appended")

	c.closeProbe(p, false) // discard is a clean no-op
	require.Len(t, a.UpperBounds, 1, "the pre-probe bound is untouched by the no-op trial")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0])
}

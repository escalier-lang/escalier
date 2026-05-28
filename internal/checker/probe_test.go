package checker

import (
	"context"
	"testing"
	"time"

	ts "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// TestProbeCommitKeepsBindings: a probe that succeeds and is committed
// leaves TypeVar.Instance set, so subsequent Prune sees the bound type.
func TestProbeCommitKeepsBindings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	inferCtx := Context{}

	tv := c.FreshVar(nil)
	numType := ts.NewNumPrimType(nil)

	probe := c.Probe(inferCtx, tv, numType)
	require.True(t, probe.Success(), "probing TV =:= number should succeed")
	probe.Commit()

	require.Same(t, numType, ts.Prune(tv),
		"after Commit, tv must resolve to the bound type")
}

// TestProbeDiscardRollsBackBindings: a successful probe that is discarded
// leaves the TypeVar unbound, as if the probe never happened.
func TestProbeDiscardRollsBackBindings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	inferCtx := Context{}

	tv := c.FreshVar(nil)
	numType := ts.NewNumPrimType(nil)

	probe := c.Probe(inferCtx, tv, numType)
	require.True(t, probe.Success())
	require.Same(t, numType, tv.Instance,
		"during probe, tv.Instance is tentatively set")
	probe.Discard()

	require.Nil(t, tv.Instance,
		"after Discard, tv.Instance must be restored to nil")
}

// TestProbeFailureLeavesNoPollution: a probe that fails (because the
// inputs are incompatible) and is then discarded must leave every
// TypeVar it touched in its pre-probe state.
//
// Scenario: a fresh TypeVar appears inside both sides of a unification
// where the rest is incompatible. Without journaling, the unifier would
// bind the TypeVar before discovering the mismatch and leave the binding
// behind.
func TestProbeFailureLeavesNoPollution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	inferCtx := Context{}

	tv := c.FreshVar(nil)
	numType := ts.NewNumPrimType(nil)
	strType := ts.NewStrPrimType(nil)

	// Tuple unification: [tv, "x"] vs [number, "y"]. Element 0 unifies
	// (binds tv = number); element 1 fails ("x" vs "y" literal strings).
	// Build the literal types so the second pair is genuinely
	// incompatible.
	strX := ts.NewStrLitType(nil, "x")
	strY := ts.NewStrLitType(nil, "y")
	tup1 := ts.NewTupleType(nil, tv, strX)
	tup2 := ts.NewTupleType(nil, numType, strY)

	probe := c.Probe(inferCtx, tup1, tup2)
	require.False(t, probe.Success(), "probe must report failure")
	probe.Discard()

	require.Nil(t, tv.Instance,
		"after Discard, the speculatively-bound tv must be unbound")
	// Sanity check: tv didn't accidentally adopt strType either.
	_ = strType
}

// TestProbeNestedDiscardRestoresOuterView: nested probes share a single
// journal. An inner Discard rolls back only the inner records; an outer
// Discard rolls back the inner+outer records together.
func TestProbeNestedDiscardRestoresOuterView(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	inferCtx := Context{}

	tvOuter := c.FreshVar(nil)
	tvInner := c.FreshVar(nil)
	numType := ts.NewNumPrimType(nil)
	strType := ts.NewStrPrimType(nil)

	// Outer probe binds tvOuter = number, then nests an inner probe
	// that binds tvInner = string. After inner Discard, tvOuter is
	// still bound (inner Discard only rolls back tvInner). After outer
	// Discard, both are unbound.
	outer := c.Probe(inferCtx, tvOuter, numType)
	require.True(t, outer.Success())

	// Inner probe sees the outer-set journal via ctx-propagation: pass
	// the same ctx, and Probe reuses the existing BindJournal. The
	// inner ctx is constructed by Probe(*ctx), so we set BindJournal
	// here for the inner call.
	innerCtx := inferCtx
	innerCtx.BindJournal = outer.scope.journal
	inner := c.Probe(innerCtx, tvInner, strType)
	require.True(t, inner.Success())

	require.Same(t, numType, tvOuter.Instance)
	require.Same(t, strType, tvInner.Instance)

	inner.Discard()
	require.Same(t, numType, tvOuter.Instance,
		"inner Discard must not roll back outer bindings")
	require.Nil(t, tvInner.Instance,
		"inner Discard must roll back tvInner")

	outer.Discard()
	require.Nil(t, tvOuter.Instance,
		"outer Discard must roll back tvOuter")
}

package solver

import (
	"math"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A discarded probe truncates every bound the trial appended back to the exact
// pre-probe length, while leaving bounds added BEFORE the probe untouched. The
// bounds flow through the real constrain path (the append sites that call
// recordMutation), not hand-appends, so this also proves the wiring.
func TestProbeDiscardRestoresBoundLengths(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)
	b := c.freshAt(0)

	// Pre-probe bound on a: number <: a ⇒ a gains one lower bound, permanently.
	require.Empty(t, c.ctx.Constrain(num(), a))
	require.Len(t, a.LowerBounds, 1)

	p := c.openProbe()
	require.Empty(t, c.ctx.Constrain(str(), a)) // a.LowerBounds: +1 ⇒ 2
	require.Empty(t, c.ctx.Constrain(b, num())) // b.UpperBounds: +1 ⇒ 1
	require.Len(t, a.LowerBounds, 2)
	require.Len(t, b.UpperBounds, 1)

	c.closeProbe(p, false) // discard

	require.Len(t, a.LowerBounds, 1, "the speculative lower bound on a is truncated away")
	require.Equal(t, num(), a.LowerBounds[0], "the pre-probe bound survives")
	require.Empty(t, b.UpperBounds, "b's only bound was speculative")
	require.Nil(t, c.ctx.probe, "the active probe is cleared after close")
}

// A committed top-level probe keeps every mutation: discard is what reverts, not
// the journal's existence.
func TestProbeCommitKeepsBounds(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)

	p := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a))
	require.Len(t, a.LowerBounds, 1)
	c.closeProbe(p, true) // commit

	require.Len(t, a.LowerBounds, 1, "a committed probe makes the bound permanent")
	require.Nil(t, c.ctx.probe)
}

// record snapshots a variable at most once per probe (first touch), so repeated
// appends to the same var all roll back to the single pre-touch length.
func TestProbeRecordDedupsPerVariable(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)

	p := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a))
	require.Empty(t, c.ctx.Constrain(str(), a))
	require.Empty(t, c.ctx.Constrain(boolT(), a))
	require.Len(t, a.LowerBounds, 3)
	require.Len(t, p.entries, 1, "a is journaled exactly once despite three appends")
	c.closeProbe(p, false)

	require.Empty(t, a.LowerBounds, "all three speculative bounds are truncated")
}

// A discarded probe runs the registered side-table cleanups so neither Info nor
// Prov keeps a stray entry from a losing trial — covering both the "node had no
// prior entry" (delete) and "node had a prior entry" (restore) cases.
func TestProbeDiscardRunsSideTableCleanups(t *testing.T) {
	c := newChecker()
	fresh := &ast.IdentExpr{Name: "fresh"}
	existing := &ast.IdentExpr{Name: "existing"}

	// `existing` already has Info/Prov entries before the probe opens.
	pre := num()
	c.recordType(existing, pre)
	c.recordProv(pre, existing, LiteralInference)

	p := c.openProbe()
	// A brand-new node typed under the probe...
	speculative := str()
	c.recordType(fresh, speculative)
	c.recordProv(speculative, fresh, LiteralInference)
	// ...and an overwrite of the pre-existing node's entries.
	c.recordType(existing, speculative)
	c.recordProv(pre, existing, ParamBinding) // re-record same type pointer, different kind

	require.Same(t, speculative, c.info.TypeOf(fresh))
	require.Same(t, speculative, c.info.TypeOf(existing))

	c.closeProbe(p, false) // discard

	require.Nil(t, c.info.TypeOf(fresh), "a node first typed under the probe loses its entry")
	require.NotContains(t, c.prov, speculative, "its provenance is gone too")

	require.Same(t, pre, c.info.TypeOf(existing), "the pre-probe Info entry is restored")
	o, ok := c.prov[pre].(FromAST)
	require.True(t, ok)
	require.Equal(t, LiteralInference, o.Kind, "the pre-probe Prov entry (kind) is restored")
}

// A committed probe's side-table writes survive — only a discard reverts them.
func TestProbeCommitKeepsSideTableWrites(t *testing.T) {
	c := newChecker()
	n := &ast.IdentExpr{Name: "x"}

	p := c.openProbe()
	ty := num()
	c.recordType(n, ty)
	c.recordProv(ty, n, LiteralInference)
	c.closeProbe(p, true) // commit

	require.Same(t, ty, c.info.TypeOf(n), "committed Info write survives")
	require.Contains(t, c.prov, ty, "committed Prov write survives")
}

// A nested probe that COMMITS hands its rollback obligation up to its parent, so
// the parent's later DISCARD still reverts the committed child's mutations.
func TestCommittedChildCoveredByParentDiscard(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)

	parent := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a)) // parent mutates a (1 bound)

	child := c.openProbe()
	require.Empty(t, c.ctx.Constrain(str(), a)) // child mutates a too (2 bounds)
	c.closeProbe(child, true)                   // child commits — but a is now the parent's obligation
	require.Same(t, parent, c.ctx.probe, "after closing the child, the parent is active again")
	require.Len(t, a.LowerBounds, 2)

	c.closeProbe(parent, false) // parent discards
	require.Empty(t, a.LowerBounds, "the parent discard reverts BOTH its own and the committed child's bounds")
	require.Nil(t, c.ctx.probe)
}

// When the parent has NOT touched a variable the committed child did, the child's
// snapshot is inherited so the parent discard reverts the child's bounds to the
// var's pre-child length (here: empty).
func TestCommittedChildInheritsUntouchedVarSnapshot(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0) // only the child will touch a

	parent := c.openProbe()
	child := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a))
	require.Len(t, a.LowerBounds, 1)
	c.closeProbe(child, true) // commit: parent inherits a at snapshot 0

	c.closeProbe(parent, false) // discard
	require.Empty(t, a.LowerBounds, "the inherited child snapshot truncates a back to empty")
}

// A discarded child leaves the parent's own journal — and the variable's
// parent-era bounds — intact; only the child's appends are reverted.
func TestDiscardedChildLeavesParentJournalIntact(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)

	parent := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a)) // parent: a has 1 bound

	child := c.openProbe()
	require.Empty(t, c.ctx.Constrain(str(), a)) // child: a has 2
	c.closeProbe(child, false)                  // child discards ⇒ back to 1
	require.Len(t, a.LowerBounds, 1, "the child discard reverts only the child's bound")

	c.closeProbe(parent, false) // parent discards ⇒ back to 0
	require.Empty(t, a.LowerBounds, "the parent discard reverts its own bound")
}

// The active-probe pointer follows the open/close stack exactly: nil → p1 → p2 →
// back to p1 → back to nil, regardless of commit/discard outcome.
func TestProbePointerFollowsOpenCloseStack(t *testing.T) {
	c := newChecker()
	require.Nil(t, c.ctx.probe)

	p1 := c.openProbe()
	require.Same(t, p1, c.ctx.probe)
	require.Nil(t, p1.parent)

	p2 := c.openProbe()
	require.Same(t, p2, c.ctx.probe)
	require.Same(t, p1, p2.parent)

	c.closeProbe(p2, true) // commit
	require.Same(t, p1, c.ctx.probe, "closing the inner probe restores the outer")

	c.closeProbe(p1, false) // discard
	require.Nil(t, c.ctx.probe, "closing the outer probe restores the non-speculative state")
}

// Commit and Discard are idempotent and mutually exclusive: a second outcome call
// is a no-op, so a double-close (or commit-then-discard) can't double-truncate.
func TestProbeOutcomeIsIdempotent(t *testing.T) {
	c := newChecker()
	a := c.freshAt(0)

	p := c.openProbe()
	require.Empty(t, c.ctx.Constrain(num(), a))
	require.Len(t, a.LowerBounds, 1)
	c.ctx.probe = p.parent // detach without going through closeProbe

	p.Commit()
	p.Discard() // no-op: already done
	require.Len(t, a.LowerBounds, 1, "Discard after Commit does not revert")

	q := c.freshAt(0)
	p2 := newProbe(c.ctx, nil) // no parent, but a real Context: this probe is installed and used
	c.ctx.probe = p2
	require.Empty(t, c.ctx.Constrain(num(), q))
	p2.Discard()
	p2.Discard() // no-op: already done
	require.Empty(t, q.LowerBounds)
}

// Bounds appended by extrude (extrusion of a higher-level super down to subVar's level)
// are journaled too: extrude mutates the ORIGINAL variable's bounds, and a
// discard must truncate those back. Constraining a low-level var against a
// higher-level var forces extrusion through the recorded append sites.
func TestProbeRollsBackExtrudeBounds(t *testing.T) {
	c := newChecker()
	low := c.freshAt(0)
	high := c.freshAt(1) // higher level ⇒ constraining low <: high extrudes high down

	p := c.openProbe()
	require.Empty(t, c.ctx.Constrain(low, high))
	// The constraint wired bounds onto both vars (extrusion + bound propagation).
	require.NotEmpty(t, high.LowerBounds)
	c.closeProbe(p, false)

	require.Empty(t, low.LowerBounds)
	require.Empty(t, low.UpperBounds)
	require.Empty(t, high.LowerBounds, "extrude's append to the original var is rolled back")
	require.Empty(t, high.UpperBounds)
}

// A discarded probe drops diagnostics the trial accumulated via the
// error-collecting walk (c.report / c.constrain), so a losing speculative
// candidate leaves no spurious errors behind; a committed probe keeps them.
func TestProbeRollsBackErrs(t *testing.T) {
	c := newChecker()
	n := &ast.IdentExpr{Name: "boom"}

	// A pre-probe diagnostic stays regardless of the trial's outcome.
	c.report(&UnsupportedNodeError{Node: n})
	require.Len(t, c.errs, 1)

	discarded := c.openProbe()
	c.report(&UnsupportedNodeError{Node: n}) // accumulated under the probe
	require.Len(t, c.errs, 2)
	c.closeProbe(discarded, false) // discard
	require.Len(t, c.errs, 1, "the trial's diagnostic is dropped; the pre-probe one survives")

	committed := c.openProbe()
	c.report(&UnsupportedNodeError{Node: n})
	c.closeProbe(committed, true) // commit
	require.Len(t, c.errs, 2, "a committed probe keeps its diagnostics")
}

// A committed child's diagnostics are covered by a later parent discard, exactly
// like its bound mutations — the errs snapshot rides the same nesting handoff.
func TestProbeErrsCommittedChildCoveredByParentDiscard(t *testing.T) {
	c := newChecker()
	n := &ast.IdentExpr{Name: "boom"}

	parent := c.openProbe()
	c.report(&UnsupportedNodeError{Node: n}) // parent-era diagnostic

	child := c.openProbe()
	c.report(&UnsupportedNodeError{Node: n}) // child-era diagnostic
	c.closeProbe(child, true)                // child commits
	require.Len(t, c.errs, 2)

	c.closeProbe(parent, false) // parent discards
	require.Empty(t, c.errs, "the parent discard drops both its own and the committed child's diagnostics")
}

// A probe built directly as &Probe{} (bypassing newProbe) is still safe: touched
// is lazily created on first record, so there is no nil-map panic.
func TestProbeBareLiteralIsNilMapSafe(t *testing.T) {
	c := newChecker()
	v := c.freshAt(0)

	c.ctx.probe = &Probe{} // deliberately skip newProbe
	require.NotPanics(t, func() {
		require.Empty(t, c.ctx.Constrain(num(), v)) // appends a bound ⇒ record(v)
	})
	require.Len(t, v.LowerBounds, 1)

	c.ctx.probe.Discard()
	require.Empty(t, v.LowerBounds, "the bare-literal probe still rolls back")
}

// newProbe snapshots the Context's memo-promotion state and Discard puts it back, while Commit
// leaves it alone. A committed trial is part of the derivation the caller keeps, so the goals it
// closed coinductive assumptions on stay folded into the caller's running minimum. A discarded one
// is not, so they must not reach the caller's promotion check.
func TestProbeRestoresShallowestAssumedOnDiscardOnly(t *testing.T) {
	t.Run("discard restores", func(t *testing.T) {
		c := &Context{shallowestAssumed: math.MaxInt}
		p := newProbe(c, c.probe)
		c.shallowestAssumed = 3
		p.Discard()
		require.Equal(t, math.MaxInt, c.shallowestAssumed)
	})

	t.Run("commit keeps", func(t *testing.T) {
		c := &Context{shallowestAssumed: math.MaxInt}
		p := newProbe(c, c.probe)
		c.shallowestAssumed = 3
		p.Commit()
		require.Equal(t, 3, c.shallowestAssumed)
	})

	// The nesting case Discard's doc rests on: a parent's snapshot predates its child entirely,
	// so a parent discard puts back the value from before both without any handoff at commit.
	t.Run("committed child unwinds when its parent discards", func(t *testing.T) {
		c := &Context{shallowestAssumed: math.MaxInt}
		parent := newProbe(c, c.probe)
		c.shallowestAssumed = 5
		child := newProbe(c, parent)
		c.shallowestAssumed = 2
		child.Commit()
		require.Equal(t, 2, c.shallowestAssumed, "a commit keeps what the child closed on")
		parent.Discard()
		require.Equal(t, math.MaxInt, c.shallowestAssumed)
	})

	// A bare &Probe{} carries no Context, so it skips the restore rather than panicking, the same
	// way it lazily creates its touched sets.
	t.Run("bare probe skips the restore", func(t *testing.T) {
		c := &Context{shallowestAssumed: math.MaxInt}
		p := &Probe{}
		c.shallowestAssumed = 3
		p.Discard()
		require.Equal(t, 3, c.shallowestAssumed)
	})
}

// TestTrialBindsWatched pins the asymmetry the try/catch difference turns on. The trial
// reports a binding only for the variables the caller watches, so one constraint answers
// "bound" or "not bound" depending on which side of it the caller cares about.
//
// memberSubtracted reads it that way. It watches the caught member's own variables and leaves
// the arm's alone, so `Timeout<number> <: Failure<T>` subtracts the member by binding the
// arm's class parameter, where `T <: "boom"` for an abstract member does not.
func TestTrialBindsWatched(t *testing.T) {
	t.Run("a constraint over no variable binds nothing", func(t *testing.T) {
		c := &Context{}
		ok, bound := c.trialBindsWatched(&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}, num(), nil)
		require.True(t, ok)
		require.False(t, bound)
	})

	t.Run("a watched variable reports its binding", func(t *testing.T) {
		c := &Context{}
		v := c.freshVar(0)
		ok, bound := c.trialBindsWatched(&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}, v,
			[]*soltype.TypeVarType{v})
		require.True(t, ok)
		require.True(t, bound)
		// The probe rolls the trial back, so the bound it saw does not outlive the trial.
		require.Empty(t, v.LowerBounds)
	})

	t.Run("an unwatched variable's binding goes unreported", func(t *testing.T) {
		c := &Context{}
		v := c.freshVar(0)
		ok, bound := c.trialBindsWatched(&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}, v, nil)
		require.True(t, ok)
		require.False(t, bound)
		require.Empty(t, v.LowerBounds)
	})

	t.Run("a failing trial reports the failure", func(t *testing.T) {
		c := &Context{}
		ok, bound := c.trialBindsWatched(str(), num(), nil)
		require.False(t, ok)
		require.False(t, bound)
	})
}

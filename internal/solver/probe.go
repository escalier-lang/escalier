package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Probe is the M3 (PR5) speculation journal: it records the mutations a
// tentative inference does so a *discarded* trial leaves no trace. Two kinds of
// mutation are journaled:
//
//  1. Bound appends. Every TypeVarType the trial appends a bound to (via
//     addLowerBound/addUpperBound) is recorded once, with its bound-list lengths
//     at first touch (record). The length-snapshot rollback is sound only under
//     one precondition: a journaled var's bound lists are extended by APPEND and
//     never replaced or shortened. Initializing a FRESHLY-minted var's bounds by
//     whole-slice assignment (freshenAbove) is exempt and need not be journaled —
//     a fresh var is unreachable after a Discard (every surviving reference to it
//     rides a journaled append that gets truncated), so it self-rolls-back. What
//     must never happen is whole-slice replacement of a var that has already been
//     recorded under the probe; rollback asserts against that.
//  2. Side-table writes. Writes to the carrier's Info / Prov tables (and the
//     checker's accumulated errs) under the probe register an onRollback closure
//     (the carrier owns those, so it registers the closures — see
//     recordType/recordProv and openProbe's errs snapshot). A discard runs them
//     so no stray node→type / type→origin entry or accumulated diagnostic
//     survives a losing trial.
//
// "Leaves no trace" is scoped to those two: bound mutations and the carrier's
// Info/Prov/errs state. The fresh-var counter (Context.varCounter) is
// deliberately NOT rewound — IDs are monotonic and never reused, so a discarded
// trial's vars simply leave a gap. Rewinding it would risk handing a live var an
// ID a discarded var still holds; the only visible effect of the gap is raw
// `t{ID}` debug renders, which is a printer concern, not a soundness one.
//
// Probes nest. A committed child hands its rollback obligation up to its parent
// (Commit), so a later parent discard still undoes the committed child's work; a
// top-level commit makes the mutations permanent and drops the journal. The
// active probe lives on *Context (the engine's bound-mutating core is there); the
// push/pop discipline and side-table registration live on the checker carrier
// (openProbe/closeProbe), which owns Info/Prov/errs. PR6 (overloading) is the
// first consumer: each candidate overload is trialled under a probe and the
// losers rolled back.
//
// M4 D1 adds a SECOND bounded sort, LifetimeVar. The probe stays concrete instead
// of abstracting over "things with bound lists". The plan's discarded `Bounded`
// interface would have been that abstraction, but Go can't fit it because the
// truncate methods would be unexported on soltype types. So the probe carries a
// PARALLEL journal of its own: ltEntries, ltTouched, and recordLt. That journal
// follows the same length-snapshot and truncate-on-discard discipline as the
// *TypeVarType path. The cost is two near-identical discard paths. The gain is no
// speculation-only truncate verb on soltype's public surface, and no cross-package
// abstraction.
type Probe struct {
	entries   []probeEntry                  // one per touched type var, in first-touch order
	touched   set.Set[*soltype.TypeVarType] // dedup: a type var is journaled at most once per probe
	ltEntries []ltProbeEntry                // one per touched lifetime var, in first-touch order
	ltTouched set.Set[*soltype.LifetimeVar] // dedup: a lifetime var is journaled at most once per probe
	cleanups  []func()                      // Info / Prov rollback closures, registration order
	parent    *Probe                        // enclosing probe (nil at top level)
	done      bool                          // Commit/Discard are idempotent and mutually exclusive
}

// probeEntry snapshots one variable's bound-list lengths at the moment the probe
// first touched it. A discard truncates LowerBounds/UpperBounds back to these
// lengths, dropping exactly the bounds the trial appended.
type probeEntry struct {
	v         *soltype.TypeVarType
	prevLower int
	prevUpper int
}

// ltProbeEntry is probeEntry for the lifetime sort: it snapshots a LifetimeVar's
// bound-list lengths at first touch so a discard truncates exactly the outlives
// bounds the trial appended.
type ltProbeEntry struct {
	v         *soltype.LifetimeVar
	prevLower int
	prevUpper int
}

// newProbe returns a probe whose parent is the currently-active probe (or nil).
// touched is lazily created on first use (markTouched), so a probe is usable even
// if built directly as &Probe{} rather than through here.
func newProbe(parent *Probe) *Probe {
	return &Probe{parent: parent}
}

// markTouched adds v to the dedup set, lazily creating the set so a probe built
// as a bare &Probe{} (bypassing newProbe) never writes to a nil map. It returns
// true if v was newly added (the caller should journal it) and false if this
// probe had already seen v.
func (p *Probe) markTouched(v *soltype.TypeVarType) bool {
	if p.touched == nil {
		p.touched = set.NewSet[*soltype.TypeVarType]()
	}
	if p.touched.Contains(v) {
		return false
	}
	p.touched.Add(v)
	return true
}

// record snapshots v's current bound-list lengths the first time this probe sees
// v. It MUST be called before the append that mutates v, so the snapshot captures
// the pre-append lengths; later appends to the same v are no-ops (the lists are
// append-only, so the first snapshot already covers them).
func (p *Probe) record(v *soltype.TypeVarType) {
	if !p.markTouched(v) {
		return
	}
	p.entries = append(p.entries, probeEntry{v: v, prevLower: len(v.LowerBounds), prevUpper: len(v.UpperBounds)})
}

// markLtTouched is markTouched for the lifetime sort: it dedups a LifetimeVar to
// one journal entry per probe, lazily creating the set so a bare &Probe{} never
// writes to a nil map.
func (p *Probe) markLtTouched(v *soltype.LifetimeVar) bool {
	if p.ltTouched == nil {
		p.ltTouched = set.NewSet[*soltype.LifetimeVar]()
	}
	if p.ltTouched.Contains(v) {
		return false
	}
	p.ltTouched.Add(v)
	return true
}

// recordLt is record for the lifetime sort: it snapshots v's bound-list lengths
// the first time this probe sees v, before the append that mutates it.
func (p *Probe) recordLt(v *soltype.LifetimeVar) {
	if !p.markLtTouched(v) {
		return
	}
	p.ltEntries = append(p.ltEntries, ltProbeEntry{v: v, prevLower: len(v.LowerBounds), prevUpper: len(v.UpperBounds)})
}

// onRollback registers a closure to undo a side-table write (Info / Prov) made
// under this probe. A discard runs the closures (see rollback); a commit hands
// them to the parent (or drops them at top level).
func (p *Probe) onRollback(f func()) {
	p.cleanups = append(p.cleanups, f)
}

// rollback reverts every mutation journaled under this probe. Cleanups run before
// bound truncation (side tables first) and in REVERSE registration order (LIFO):
// a single Info/Prov key may be written more than once under the probe, and each
// closure captured that key's prior value at registration time, so only unwinding
// newest-first restores the original value. Entry truncation is reverse for
// symmetry only — touched dedups to one entry per var, so order is moot there.
//
// The truncation asserts the append-only precondition rather than silently
// clamping: a snapshot length above the current list length means a recorded
// var's bound slice was REPLACED or shortened mid-trial (not appended to), which
// would otherwise corrupt the var on rollback. Failing loudly turns that into an
// immediate, located error instead of silent under-constraint.
func (p *Probe) rollback() {
	for i := len(p.cleanups) - 1; i >= 0; i-- {
		p.cleanups[i]()
	}
	for i := len(p.entries) - 1; i >= 0; i-- {
		e := p.entries[i]
		if e.prevLower > len(e.v.LowerBounds) || e.prevUpper > len(e.v.UpperBounds) {
			panic("probe.rollback: a journaled var's bounds were replaced or shortened, not appended — the append-only invariant is violated")
		}
		e.v.LowerBounds = e.v.LowerBounds[:e.prevLower]
		e.v.UpperBounds = e.v.UpperBounds[:e.prevUpper]
	}
	for i := len(p.ltEntries) - 1; i >= 0; i-- {
		e := p.ltEntries[i]
		if e.prevLower > len(e.v.LowerBounds) || e.prevUpper > len(e.v.UpperBounds) {
			panic("probe.rollback: a journaled lifetime var's bounds were replaced or shortened, not appended — the append-only invariant is violated")
		}
		e.v.LowerBounds = e.v.LowerBounds[:e.prevLower]
		e.v.UpperBounds = e.v.UpperBounds[:e.prevUpper]
	}
}

// mutatedBounds reports whether any variable this probe journaled has grown a bound since first
// touch, so a throwaway trial can tell a match that binds a var from one that records nothing.
func (p *Probe) mutatedBounds() bool {
	for _, e := range p.entries {
		if len(e.v.LowerBounds) > e.prevLower || len(e.v.UpperBounds) > e.prevUpper {
			return true
		}
	}
	for _, e := range p.ltEntries {
		if len(e.v.LowerBounds) > e.prevLower || len(e.v.UpperBounds) > e.prevUpper {
			return true
		}
	}
	return false
}

// Discard rolls back every mutation journaled under this probe. Idempotent: a
// second Discard (or a Discard after Commit) is a no-op.
func (p *Probe) Discard() {
	if p.done {
		return
	}
	p.rollback()
	p.done = true
}

// Commit keeps this probe's mutations. At top level the mutations become
// permanent and the journal is dropped. With a parent, the rollback obligation is
// handed up so a later parent Discard still undoes this committed child's work:
// each touched var the parent has NOT yet journaled is inherited with the child's
// snapshot (the var's state before any parent-or-child mutation); a var the parent
// already journaled keeps the parent's earlier (≤) snapshot. Cleanups append after
// the parent's so global registration order — and thus the LIFO rollback — is
// preserved. Idempotent and mutually exclusive with Discard.
func (p *Probe) Commit() {
	if p.done {
		return
	}
	p.done = true
	parent := p.parent
	if parent == nil {
		p.entries = nil
		p.ltEntries = nil
		p.cleanups = nil
		return
	}
	for _, e := range p.entries {
		// Inherit the child's snapshot only for a var the parent hasn't journaled;
		// a var the parent already saw keeps the parent's earlier (≤) snapshot.
		if parent.markTouched(e.v) {
			parent.entries = append(parent.entries, e)
		}
	}
	for _, e := range p.ltEntries {
		// Same handoff for the lifetime sort: a lifetime var the parent hasn't
		// journaled is inherited at the child's snapshot.
		if parent.markLtTouched(e.v) {
			parent.ltEntries = append(parent.ltEntries, e)
		}
	}
	parent.cleanups = append(parent.cleanups, p.cleanups...)
	p.entries = nil
	p.ltEntries = nil
	p.cleanups = nil
}

// openProbe pushes a fresh probe as the active one and returns it. Pair every
// openProbe with a closeProbe so ctx.probe never dangles.
//
// It also journals the checker's diagnostic list: errs is append-only, so a
// snapshot of its length lets a discarded trial drop any diagnostics it
// accumulated through c.constrain / c.report (a committed probe keeps them). This
// completes the "leaves no trace" guarantee alongside the bound + Info/Prov
// journaling, so a speculative trial may safely route failures through the
// error-accumulating walk, not only the error-returning Constrain.
//
// The "leaves no trace" guarantee covers errs, bounds, and the Info/Prov side
// tables. It does NOT cover funcCtx.pendingTransitions, the deferred phase/exclusivity
// conflicts. Those are recorded by the statement walk (checkMutabilityTransition) and
// emitted only after the body walk by resolvePhaseTransitions, so they never pass
// through the errs snapshot above. This is sound only because the transition checks run
// outside any open probe: every openProbe site wraps a type-level Context.Constrain
// trial, not a statement or assignment walk. If a probe is ever opened around a body
// walk that can reach checkMutabilityTransition, a discarded trial would leak a spurious
// phase conflict. Journal len(c.fn.pendingTransitions) here and truncate it on rollback
// at that point, mirroring the errs snapshot.
func (c *checker) openProbe() *Probe {
	p := newProbe(c.ctx.probe)
	c.ctx.probe = p
	errsLen := len(c.errs)
	p.onRollback(func() { c.errs = c.errs[:errsLen] })
	return p
}

// trialAndCommit tries each index in order, running its trial under a fresh child probe.
// The first trial whose body reports no fatal error wins. Its bound mutations commit, and
// the method returns (true, winIdx, winErrs, nil), where winIdx is the winning member's
// index and winErrs holds any warnings the winning trial produced. Every losing trial
// rolls back, so it leaves no bound behind. A warning does not count as failure, so a
// branch that succeeds while emitting one still wins. hasHardError draws that line.
//
// When no trial wins, the method returns false with winIdx -1 and each trial's errors in
// trial order. The caller decides what to do with them. It can promote a shared failure or
// report the last trial's diagnostics. The union-super rule, for example, promotes a
// uniform BorrowEscapeError when every trial reports one.
//
// This is the single path for the speculative member trials the lattice arms run. Both
// constrain's IntersectionType-sub exists rule and its UnionType-super exists rule route
// through it, so the probe push-and-pop discipline lives in one place. Each trial body
// owns its own coinductive seen clone, since only the caller holds the constraint key.
func (c *Context) trialAndCommit(order []int, trial func(idx int) []SolverError) (committed bool, winIdx int, winErrs []SolverError, trialErrs [][]SolverError) {
	for _, idx := range order {
		p := newProbe(c.probe)
		c.probe = p
		errs := trial(idx)
		c.probe = p.parent
		if !hasHardError(errs) {
			p.Commit()
			return true, idx, errs, nil
		}
		p.Discard()
		trialErrs = append(trialErrs, errs)
	}
	return false, -1, nil, trialErrs
}

// trialMutatesBounds trials sub <: super under a throwaway probe, reporting whether it succeeded
// and whether it recorded a bound, so `5 <: T` gives (true, true) and `5 <: number` (true, false).
func (c *Context) trialMutatesBounds(sub, super soltype.Type, seen set.Set[constraintKey], mutCtx bool) (ok, mutated bool) {
	p := newProbe(c.probe)
	c.probe = p
	errs := c.constrain(sub, super, seen.Clone(), mutCtx)
	mutated = p.mutatedBounds()
	c.probe = p.parent
	p.Discard()
	return !hasHardError(errs), mutated
}

// trialUnderProbe trials `sub <: super` under a discard-only probe and returns
// the trial's errors, leaving no bound behind; an empty result means it
// succeeded. The seen-set starts fresh and the mut context at false, so the
// trial is a self-contained covariant read check.
func (c *Context) trialUnderProbe(sub, super soltype.Type) []SolverError {
	return c.trialUnderProbeSeen(sub, super, set.NewSet[constraintKey]())
}

// trialUnderProbeSeen is trialUnderProbe over a caller-supplied cycle-detection set rather than a
// fresh one, so a conditional's `Check <: Extends` branch probe closes a recursive alias through the
// same seen-set the enclosing constraint built up. The caller clones the set when it must keep the
// trial's keys out of its own.
func (c *Context) trialUnderProbeSeen(sub, super soltype.Type, seen set.Set[constraintKey]) []SolverError {
	p := newProbe(c.probe)
	c.probe = p
	errs := c.constrain(sub, super, seen, false)
	c.probe = p.parent
	p.Discard()
	return errs
}

// snapshotMapEntry registers, under the active probe (else a no-op), a closure
// that restores m[k] to its current value — or deletes the key if it is currently
// absent — so a discarded trial leaves m exactly as it was. Shared by the Info
// and Prov side-table writers (recordType, snapshotProv) so both roll back
// identically; a fix to the restore/delete semantics lands in one place.
func snapshotMapEntry[K comparable, V any](c *checker, m map[K]V, k K) {
	if c.ctx.probe == nil {
		return
	}
	prev, had := m[k]
	c.ctx.probe.onRollback(func() {
		if had {
			m[k] = prev
		} else {
			delete(m, k)
		}
	})
}

// closeProbe runs the probe's outcome (Commit when commit is true, else Discard)
// and restores the engine's active-probe pointer to the enclosing probe, so the
// current-probe pointer follows the open/close stack exactly.
func (c *checker) closeProbe(p *Probe, commit bool) {
	if commit {
		p.Commit()
	} else {
		p.Discard()
	}
	c.ctx.probe = p.parent
}

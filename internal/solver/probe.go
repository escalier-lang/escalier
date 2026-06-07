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
// Deviation from the plan's sketch: the plan typed the journal over a `Bounded`
// interface (boundLengths/truncateBounds) to abstract "things with bound lists".
// Go can't add those (unexported) methods to soltype.TypeVarType from this
// package, and M3 has exactly one bounded type, so the journal holds the concrete
// *soltype.TypeVarType and truncates its exported bound fields directly — keeping
// the speculation-only truncate out of soltype's public surface. If a second
// bounded type appears, reintroduce the interface (with exported methods on the
// soltype types) then.
type Probe struct {
	entries  []probeEntry                  // one per touched variable, in first-touch order
	touched  set.Set[*soltype.TypeVarType] // dedup: a var is journaled at most once per probe
	cleanups []func()                      // Info / Prov rollback closures, registration order
	parent   *Probe                        // enclosing probe (nil at top level)
	done     bool                          // Commit/Discard are idempotent and mutually exclusive
}

// probeEntry snapshots one variable's bound-list lengths at the moment the probe
// first touched it. A discard truncates LowerBounds/UpperBounds back to these
// lengths, dropping exactly the bounds the trial appended.
type probeEntry struct {
	v         *soltype.TypeVarType
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
	parent.cleanups = append(parent.cleanups, p.cleanups...)
	p.entries = nil
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
func (c *checker) openProbe() *Probe {
	p := newProbe(c.ctx.probe)
	c.ctx.probe = p
	errsLen := len(c.errs)
	p.onRollback(func() { c.errs = c.errs[:errsLen] })
	return p
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

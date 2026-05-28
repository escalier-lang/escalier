package checker

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// BindJournal records the TypeVar and LifetimeVar field values that
// bind() and its helpers (handleArrayConstraintBinding,
// openClosedObjectForParam, the widening fallback in unifyInner, the
// open-object expansion in expand_type, Prune's path compression, and
// UnifyLifetimes) are about to overwrite. Discard walks the records in
// reverse and restores the saved values; Commit is a no-op since
// mutations were applied tentatively in-place.
//
// Lives on Context so a value-copied ctx propagates the journal pointer
// through every recursive unifyInner call without a save/restore dance —
// the same propagation discipline used by QueryUnify (see unify_mode.go).
//
// Implements type_system.Journal so it can be passed directly to Prune.
type BindJournal struct {
	records         []bindRecord
	lifetimeRecords []lifetimeRecord
	// cleanups holds deferred rollback closures for side effects that
	// don't fit the TypeVar / LifetimeVar record shape — e.g.
	// expr.SetResolvedThrows on the overload-dispatch path. Closures
	// run in reverse order during rollback, mirroring the record
	// slices.
	cleanups []func()
	// depth counts open ProbeScopes sharing this journal. When the
	// last open scope Commits, records become permanent — no outer
	// scope can roll them back — so we can free the backing slices to
	// keep journal memory bounded across many committed probes
	// (otherwise the slice would grow append-only for the lifetime of
	// the journal).
	depth int
}

// bindRecord snapshots the subset of TypeVarType fields that the unifier
// mutates. Discard restores all of them so the post-rollback TypeVar is
// indistinguishable from its pre-probe state.
type bindRecord struct {
	typeVar         *type_system.TypeVarType
	instance        type_system.Type
	constraint      type_system.Type
	isObjectRest    bool
	arrayConstraint *type_system.ArrayConstraint
	instanceChain   []*type_system.TypeVarType
	prov            provenance.Provenance
}

// lifetimeRecord snapshots the Instance of a LifetimeVar mutated by
// UnifyLifetimes. LifetimeVar has fewer mutable fields than TypeVar, so
// the record is much smaller. Lives in a separate slice from bindRecord
// because the two share no state: their rollbacks are independent and
// the order between them doesn't matter.
type lifetimeRecord struct {
	lifetimeVar *type_system.LifetimeVar
	instance    type_system.Lifetime
}

// Snapshot appends a record of tv's current mutable fields. Callers MUST
// invoke this before any mutation when ctx.BindJournal is non-nil. This
// is also the method that satisfies type_system.Journal so Prune can
// record path-compression and recordInstanceChain mutations.
//
// Tolerates a nil receiver so callers can pass ctx.BindJournal
// unconditionally — a typed-nil *BindJournal wrapped in the Journal
// interface looks non-nil to Prune's `j != nil` guard, and this nil
// check is the simpler fix than having every caller compute the
// interface conversion themselves.
//
// Idempotency is not required: multiple snapshots of the same TypeVar
// just stack. Discard restores in reverse order so each snapshot peels
// one mutation off the stack, leaving the TypeVar in its pre-probe shape.
func (j *BindJournal) Snapshot(tv *type_system.TypeVarType) {
	if j == nil {
		return
	}
	j.records = append(j.records, bindRecord{
		typeVar:         tv,
		instance:        tv.Instance,
		constraint:      tv.Constraint,
		isObjectRest:    tv.IsObjectRest,
		arrayConstraint: tv.ArrayConstraint,
		instanceChain:   tv.InstanceChain,
		prov:            tv.Provenance(),
	})
}

// SnapshotLifetime records lt's current Instance for rollback. Callers
// MUST invoke this before any mutation to lt.Instance when
// ctx.BindJournal is non-nil. Tolerates a nil receiver for the same
// reason Snapshot does — see Snapshot's doc.
func (j *BindJournal) SnapshotLifetime(lt *type_system.LifetimeVar) {
	if j == nil {
		return
	}
	j.lifetimeRecords = append(j.lifetimeRecords, lifetimeRecord{
		lifetimeVar: lt,
		instance:    lt.Instance,
	})
}

// AddCleanup registers a closure that runs during rollback in reverse
// order. Use for ad-hoc side effects (e.g. AST mutations like
// SetResolvedThrows) that don't fit the TypeVar / LifetimeVar record
// shape. Tolerates a nil receiver.
func (j *BindJournal) AddCleanup(f func()) {
	if j == nil {
		return
	}
	j.cleanups = append(j.cleanups, f)
}

// rollback restores every record added at or after the given marks, in
// reverse order. Used by Discard and by callers that need probe-scope
// semantics over operations that aren't a single (t1, t2) unification
// (e.g. an overload arm's full handleFuncCall — see infer_expr.go).
func (j *BindJournal) rollback(mark, ltMark, cleanupMark int) {
	records := j.records
	for i := len(records) - 1; i >= mark; i-- {
		r := &records[i]
		r.typeVar.Instance = r.instance
		r.typeVar.Constraint = r.constraint
		r.typeVar.IsObjectRest = r.isObjectRest
		r.typeVar.ArrayConstraint = r.arrayConstraint
		r.typeVar.InstanceChain = r.instanceChain
		r.typeVar.SetProvenance(r.prov)
	}
	j.records = records[:mark]

	ltRecords := j.lifetimeRecords
	for i := len(ltRecords) - 1; i >= ltMark; i-- {
		r := &ltRecords[i]
		r.lifetimeVar.Instance = r.instance
	}
	j.lifetimeRecords = ltRecords[:ltMark]

	cleanups := j.cleanups
	for i := len(cleanups) - 1; i >= cleanupMark; i-- {
		cleanups[i]()
	}
	j.cleanups = cleanups[:cleanupMark]
}

// ProbeScope is the journaled-bind context that beginProbeScope opens.
// Callers run their probed operation between BeginProbeScope and
// scope.Commit() / scope.Discard(). Use Probe instead for the common
// case of probing a single (t1, t2) unification.
//
// Carries both record-slice marks so Discard rolls back exactly the
// TypeVar and LifetimeVar mutations made between begin and discard.
//
// `done` is a heap pointer so the first Commit or Discard call on the
// scope flips it for every value-receiver copy of the scope, making
// subsequent Commit/Discard calls no-ops. Without this, a caller that
// committed mutations could later (e.g. in deferred cleanup) Discard
// the same scope and silently roll them back.
type ProbeScope struct {
	journal      *BindJournal
	mark         int
	ltMark       int
	cleanupMark  int
	done         *bool
}

// beginProbeScope installs a journal on ctx (allocating one if ctx had
// none) so subsequent Prune calls and bind sites that receive
// ctx.BindJournal will snapshot pre-mutation state. The caller is
// responsible for calling Commit or Discard on the returned scope.
//
// Takes *Context so the journal pointer becomes visible to subsequent
// recursive calls that pass ctx by value (each copy keeps the same
// pointer). Callers that don't want their outer ctx mutated should pass
// a local copy.
func (c *Checker) beginProbeScope(ctx *Context) ProbeScope {
	if ctx.BindJournal == nil {
		ctx.BindJournal = &BindJournal{}
	}
	j := ctx.BindJournal
	done := false
	j.depth++
	return ProbeScope{
		journal:     j,
		mark:        len(j.records),
		ltMark:      len(j.lifetimeRecords),
		cleanupMark: len(j.cleanups),
		done:        &done,
	}
}

// Commit keeps every mutation made during the scope. The records stay
// in the journal: if the scope is nested inside an outer probe the
// outer can still roll them back as part of its own Discard. When
// this is the last open scope on the journal (depth reaches 0), the
// records become permanent and the journal trims its backing slices
// to free the memory accumulated by every committed probe so far.
//
// Idempotent: a second Commit or a Discard after Commit is a no-op.
func (s ProbeScope) Commit() {
	if *s.done {
		return
	}
	*s.done = true
	s.journal.depth--
	if s.journal.depth == 0 {
		s.journal.records = s.journal.records[:0]
		s.journal.lifetimeRecords = s.journal.lifetimeRecords[:0]
		s.journal.cleanups = s.journal.cleanups[:0]
	}
}

// Discard restores every TypeVar and LifetimeVar field touched during
// the scope to its pre-scope value. Replays records in reverse so
// stacked mutations to the same variable peel off in the right order.
//
// Idempotent: a second Discard or a Commit after Discard is a no-op.
func (s ProbeScope) Discard() {
	if *s.done {
		return
	}
	*s.done = true
	s.journal.depth--
	s.journal.rollback(s.mark, s.ltMark, s.cleanupMark)
}

// ProbeResult holds the outcome of a Probe call. The caller inspects
// Errors() / Success() and then decides Commit or Discard.
type ProbeResult struct {
	scope  ProbeScope
	errors []Error
}

// Errors returns the errors produced by the probed unification, or an
// empty slice if probing succeeded.
func (p ProbeResult) Errors() []Error { return p.errors }

// Journal returns the BindJournal this probe wrote to. Use it to nest
// a follow-on probe under the same journal — set ctx.BindJournal to
// the returned pointer and call Probe with that ctx. (Probe takes ctx
// by value, so it can't propagate the journal back to the caller's
// scope automatically.)
func (p ProbeResult) Journal() *BindJournal { return p.scope.journal }

// Success is true iff the probed unification produced no errors.
func (p ProbeResult) Success() bool { return len(p.errors) == 0 }

// Commit keeps every mutation made during the probe.
func (p ProbeResult) Commit() { p.scope.Commit() }

// Discard restores every TypeVar field touched during the probe.
func (p ProbeResult) Discard() { p.scope.Discard() }

// Probe runs unifyInner under a BindJournal so the caller can commit or
// discard the resulting TypeVar bindings as a unit. It is the sibling of
// Check (refuses all mutations, returns a pure subtype answer) and Unify
// (commits unconditionally): Probe commits tentatively, lets the unifier
// observe its own bindings during recursion, and lets the caller decide
// after the fact whether to keep them.
//
// Replaces the deepCloneType-then-unify pattern at intersection/union
// sites in unify.go: instead of cloning the inputs and trial-unifying on
// the clones, Probe unifies on the originals while journaling every
// mutation, then either commits (cheap — records are dropped) or
// discards (records are replayed in reverse to restore the pre-probe
// state).
//
// Nested probes share the outer journal; the inner scope's mark records
// the offset so Discard rolls back only the inner probe's records. An
// inner Commit leaves records in the outer journal, so the outer scope
// can still roll them back as part of its own Discard.
//
// Allocates a fresh unifySeen, so callers driving Probe from within an
// already-running unifyInner (the three intersection/union dispatch
// sites in unify.go) should use probeWithSeen instead to inherit the
// outer call's co-inductive cycle-detection history.
func (c *Checker) Probe(ctx Context, t1, t2 type_system.Type) ProbeResult {
	return c.probeWithSeen(ctx, t1, t2, make(unifySeen))
}

// probeWithSeen is the internal Probe variant that inherits an outer
// unifyInner's seen map. Used by the intersection/union dispatch sites
// in unifyMatched so a recursive type alias the outer call already
// marked seen isn't re-expanded inside the probe (which would lose the
// co-inductive cycle-termination guarantee).
func (c *Checker) probeWithSeen(ctx Context, t1, t2 type_system.Type, seen unifySeen) ProbeResult {
	scope := c.beginProbeScope(&ctx)
	errors := c.unifyInner(ctx, t1, t2, seen)
	return ProbeResult{scope: scope, errors: errors}
}

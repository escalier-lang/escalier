package checker

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// BindJournal records the TypeVar field values that bind() and its helpers
// (handleArrayConstraintBinding, openClosedObjectForParam, the widening
// fallback in unifyInner, and Prune's path compression) are about to
// overwrite. Discard walks the records in reverse and restores the saved
// values; Commit is a no-op since mutations were applied tentatively
// in-place.
//
// Lives on Context so a value-copied ctx propagates the journal pointer
// through every recursive unifyInner call without a save/restore dance —
// the same propagation discipline used by QueryUnify (see unify_mode.go).
//
// Implements type_system.Journal so it can be passed directly to Prune.
type BindJournal struct {
	records []bindRecord
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

// rollback restores every record added at or after mark, in reverse
// order. Used by Discard and by callers that need probe-scope semantics
// over operations that aren't a single (t1, t2) unification (e.g. an
// overload arm's full handleFuncCall — see infer_expr.go).
func (j *BindJournal) rollback(mark int) {
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
}

// ProbeScope is the journaled-bind context that beginProbeScope opens.
// Callers run their probed operation between BeginProbeScope and
// scope.Commit() / scope.Discard(). Use Probe instead for the common
// case of probing a single (t1, t2) unification.
type ProbeScope struct {
	journal *BindJournal
	mark    int
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
	return ProbeScope{
		journal: j,
		mark:    len(j.records),
	}
}

// Commit keeps every mutation made during the scope. The records stay
// in the journal: if the scope is nested inside an outer probe the
// outer can still roll them back as part of its own Discard; if not,
// the records are effectively permanent and get GC'd with the journal.
func (s ProbeScope) Commit() {}

// Discard restores every TypeVar field touched during the scope to its
// pre-scope value. Replays records in reverse so stacked mutations to
// the same TypeVar peel off in the right order.
func (s ProbeScope) Discard() {
	s.journal.rollback(s.mark)
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
func (c *Checker) Probe(ctx Context, t1, t2 type_system.Type) ProbeResult {
	scope := c.beginProbeScope(&ctx)
	errors := c.unifyInner(ctx, t1, t2, make(unifySeen))
	return ProbeResult{scope: scope, errors: errors}
}

package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; the
// spike's lifetimeCounter / paramLifetimes / written fields are M4 (lifetimes
// and records/mut), so M1's Context is correspondingly lean.
//
// M3 (PR5) adds the nullable probe pointer. The engine's bound-mutating core
// (constrain/extrude) lives on *Context, so the speculation journal must live
// here too: when probe is non-nil, every bound append snapshots the variable's
// bound-list lengths so a discarded trial can truncate them back. nil is the
// non-speculative path — the common case — and pays only a nil check per append.
// The push/pop discipline (openProbe/closeProbe) lives on the checker carrier,
// which also owns the side-table cleanups (Info/Prov).
//
// Bound lists are extended ONLY through addLowerBound/addUpperBound, which fuse
// the probe snapshot with the append. A bare `v.LowerBounds = append(...)` must
// never appear — routing every append through the two helpers is what makes
// "forgot to journal the mutation" structurally impossible (an un-journaled
// append would silently survive a Discard and corrupt committed state).
type Context struct {
	varCounter int
	probe      *Probe
}

// freshVar allocates a new inference variable at the given level, assigning it
// the next id in sequence.
func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}

// addLowerBound appends t to v's lower bounds, journaling the mutation in the
// active probe first so a discarded trial truncates it away. This (and
// addUpperBound) is the ONLY sanctioned way to extend a bound list.
func (c *Context) addLowerBound(v *soltype.TypeVarType, t soltype.Type) {
	c.recordMutation(v)
	v.LowerBounds = append(v.LowerBounds, t)
}

// addUpperBound is the upper-bound counterpart of addLowerBound.
func (c *Context) addUpperBound(v *soltype.TypeVarType, t soltype.Type) {
	c.recordMutation(v)
	v.UpperBounds = append(v.UpperBounds, t)
}

// recordMutation snapshots v's bound-list lengths in the active probe, if any,
// BEFORE a bound append mutates v. A no-op when no probe is open. record itself
// dedups (only the first touch of v in a probe snapshots), so calling this at
// every append site is cheap and correct: append-only bound lists mean the first
// snapshot already covers every later append to v under the same probe. Reached
// only through addLowerBound/addUpperBound.
func (c *Context) recordMutation(v *soltype.TypeVarType) {
	if c.probe != nil {
		c.probe.record(v)
	}
}

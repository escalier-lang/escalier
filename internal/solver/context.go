package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// Context owns the engine's mutable counters: varCounter for type vars and
// lifetimeCounter for the lifetime sort. The read-after-write cache, the
// `written` map, lives on the checker's per-function context, not here.
//
// The nullable probe pointer drives speculation. The engine's bound-mutating
// core, constrain and extrude, lives on *Context, so the speculation journal
// lives here too. When probe is non-nil, every bound append snapshots the
// variable's bound-list lengths so a discarded trial can truncate them back. nil
// is the non-speculative path, the common case, and pays only a nil check per
// append. The push/pop discipline, openProbe and closeProbe, lives on the
// checker carrier, which also owns the Info and Prov side-table cleanups.
//
// Bound lists are extended ONLY through addLowerBound/addUpperBound, which fuse
// the probe snapshot with the append. A bare `v.LowerBounds = append(...)` must
// never appear — routing every append through the two helpers is what makes
// "forgot to journal the mutation" structurally impossible (an un-journaled
// append would silently survive a Discard and corrupt committed state).
type Context struct {
	varCounter int
	probe      *Probe

	// lifetimeCounter mints the next LifetimeVar id. Lifetimes are a SECOND bounded
	// sort solved by the same machinery as types. A fresh lifetime gets the next id
	// here, its bounds are extended only through addLowerLtBound/addUpperLtBound, and
	// a speculation trial journals it under the same probe discipline as a
	// TypeVarType.
	lifetimeCounter int

	// ltProxyOrigin maps an outer-extruded lifetime proxy to the lifetime it was
	// extruded from. constrainLt consults it through findLtProxy to reuse an existing
	// proxy for a repeated cross-level outlives constraint, so the bound
	// dedup is not defeated by minting a fresh proxy each time. It is metadata only
	// and is never rolled back: a stale entry for a proxy that a discarded trial
	// removed from its bound list is simply never matched, since findLtProxy scans
	// only bounds currently present.
	ltProxyOrigin map[*soltype.LifetimeVar]soltype.Lifetime
}

// freshVar allocates a new inference variable at the given level, assigning it
// the next id in sequence.
func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}

// freshLifetime allocates a new lifetime variable at the given level, assigning it
// the next id in sequence. Lifetimes ride the same let-generalization level
// hierarchy as types. A lifetime minted inside its scheme's generalize-level is
// freshened per instantiation, so two uses of a borrow-passing function never
// share one LifetimeVar.
func (c *Context) freshLifetime(level int) *soltype.LifetimeVar {
	lv := &soltype.LifetimeVar{ID: c.lifetimeCounter, Level: level}
	c.lifetimeCounter++
	return lv
}

// freshJoinLifetime allocates a lifetime variable for a multi-source join site.
// A join site is a return or branch uniting several borrows. It is identical to
// freshLifetime but sets Join, so coalesceLifetime expands it to the
// union of the param lifetimes it reaches rather than naming it as a borrow origin.
func (c *Context) freshJoinLifetime(level int) *soltype.LifetimeVar {
	lv := c.freshLifetime(level)
	lv.Join = true
	return lv
}

// addLowerLtBound appends lt to v's lower bounds, journaling the mutation in the
// active probe first so a discarded trial truncates it away. This (and
// addUpperLtBound) is the ONLY sanctioned way to extend a lifetime bound list —
// the second sort inherits the type sort's "appends only through journaling
// helpers" invariant so an un-journaled append cannot survive a Discard.
func (c *Context) addLowerLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime) {
	c.recordLtMutation(v)
	v.LowerBounds = append(v.LowerBounds, lt)
}

// addUpperLtBound is the upper-bound counterpart of addLowerLtBound.
func (c *Context) addUpperLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime) {
	c.recordLtMutation(v)
	v.UpperBounds = append(v.UpperBounds, lt)
}

// recordLtProxy notes that proxy is an outer-extruded copy of origin, so a later
// repeated outlives constraint can reuse the proxy rather than mint a new one.
// Lazily allocates the map.
func (c *Context) recordLtProxy(proxy *soltype.LifetimeVar, origin soltype.Lifetime) {
	if c.ltProxyOrigin == nil {
		c.ltProxyOrigin = map[*soltype.LifetimeVar]soltype.Lifetime{}
	}
	c.ltProxyOrigin[proxy] = origin
}

// findLtProxy returns a lifetime among bounds that is an outer-extruded proxy of
// origin, or nil if none. Identity-keyed: a proxy matches only when ltProxyOrigin
// records it against the exact origin pointer. Scanning live bounds keeps it
// probe-safe — a proxy a discarded trial removed from its bound list is not found.
func (c *Context) findLtProxy(bounds []soltype.Lifetime, origin soltype.Lifetime) soltype.Lifetime {
	for _, b := range bounds {
		if bv, ok := b.(*soltype.LifetimeVar); ok && c.ltProxyOrigin[bv] == origin {
			return bv
		}
	}
	return nil
}

// recordLtMutation snapshots v's bound-list lengths in the active probe, if any,
// BEFORE a bound append mutates v — the lifetime-sort twin of recordMutation. A
// no-op when no probe is open; recordLt dedups per probe, so the first touch's
// snapshot covers every later append to v under the same probe.
func (c *Context) recordLtMutation(v *soltype.LifetimeVar) {
	if c.probe != nil {
		c.probe.recordLt(v)
	}
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

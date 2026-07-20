package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; M4 D1
// adds lifetimeCounter for the lifetime sort. M4 C3's read-after-write cache,
// the `written` map, lives on the checker's per-function context, not here.
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

	// lifetimeCounter mints the next LifetimeVar id (M4 D1). Lifetimes are a
	// SECOND bounded sort solved by the same machinery as types: a fresh lifetime
	// gets the next id here, its bounds are extended only through
	// addLowerLtBound/addUpperLtBound, and a speculation trial journals it under
	// the same probe discipline as a TypeVarType.
	lifetimeCounter int

	// ltProxyOrigin maps an outer-extruded lifetime proxy to the lifetime it was
	// extruded from (M4 D2.5). constrainLt consults it through findLtProxy to reuse
	// an existing proxy for a repeated cross-level outlives constraint, so the bound
	// dedup is not defeated by minting a fresh proxy each time. It is metadata only
	// and is never rolled back: a stale entry for a proxy that a discarded trial
	// removed from its bound list is simply never matched, since findLtProxy scans
	// only bounds currently present.
	ltProxyOrigin map[*soltype.LifetimeVar]soltype.Lifetime

	// classes is the nominal registry (M5): each class's heavy data — the projected
	// instance body, the resolved supers, and the per-parameter variance — keyed by
	// the class's dep_graph-qualified name, the same string stored in
	// soltype.ClassType.Name. inferClassDecl writes an entry per class decl; member
	// lookup and the nominal constrain rule read it. Every ClassDef comes from a
	// top-level decl and lives for the whole inference run, so an entry is inserted or
	// overwritten but never removed for scope exit.
	classes map[string]*ClassDef

	// aliases is the type-alias registry, the transparent-alias twin of classes. It holds
	// each alias's Body and level, keyed by the alias's dep_graph-qualified name, the same
	// string stored in soltype.AliasType.Name. preBindAlias writes an entry per `type` decl,
	// and expandAlias reads the Body to unfold an alias reference to its structural type at
	// subtyping time. Every AliasDef comes from a top-level decl and lives for the whole
	// inference run.
	aliases map[string]*AliasDef

	// aliasInterns maps an alias reference's canonical identity — its name and the rendered
	// form of its type arguments, e.g. "List<number>" — to one representative AliasType, so
	// two structurally-equal reference nodes share a pointer. constrain's seen-set keys by
	// pointer identity, and expandAlias mints a fresh substituted node each unfold, so a
	// generic recursive alias such as List<number> would produce a new node every lap and
	// never hit the cache. Interning gives the cache a stable canonical key so the cycle
	// closes. The map lives for the whole run and is never rolled back on a discarded probe.
	// A representative is only ever compared by identity for the seen-set and never expanded,
	// so a stale entry cannot make a constraint wrong.
	aliasInterns map[string]*soltype.AliasType

	// unionCommits records each inference var that a union-super exists trial pinned by
	// committing a bare type-variable member. The key is the pinned var and the value is
	// the super union it was chosen from, so `"hi" <: (T | number)` commits the T branch
	// and records T → (T | number). A later constraint that forces an incompatible bound
	// onto the var reads this to point the failure back to the union choice that pinned it,
	// rather than blaming only the later use. The write is journaled under the active probe
	// through tagUnionCommit, so a discarded trial drops the tag alongside the bound it
	// recorded.
	unionCommits map[*soltype.TypeVarType]*soltype.UnionType
}

// tagUnionCommit records that v was pinned by committing v as a bare type-variable member
// of union u, so a later failing constraint on v can name the union that forced it. The
// prior entry is captured and restored on rollback, so a discarded trial leaves the table
// exactly as it was. The tag and the bound the trial recorded on v roll back together.
func (c *Context) tagUnionCommit(v *soltype.TypeVarType, u *soltype.UnionType) {
	if c.unionCommits == nil {
		c.unionCommits = map[*soltype.TypeVarType]*soltype.UnionType{}
	}
	if c.probe != nil {
		prev, had := c.unionCommits[v]
		c.probe.onRollback(func() {
			if had {
				c.unionCommits[v] = prev
			} else {
				delete(c.unionCommits, v)
			}
		})
	}
	c.unionCommits[v] = u
}

// internAlias returns the shared representative for an alias reference's canonical
// identity, minting one on first sight. Two AliasType nodes naming the same alias with
// type arguments that render identically map to one pointer. That pointer is the canonical
// identity formed from the alias and its arguments, the identity constrain's cycle guard
// keys on. A non-generic reference keys on its name alone.
func (c *Context) internAlias(at *soltype.AliasType) *soltype.AliasType {
	// PrintQualified renders the reference under qualified names for every nested alias and
	// class, so two aliases sharing a local name across namespaces never collide on one key,
	// and a nested recursive alias such as List<List<number>> serializes finitely because an
	// argument renders under its own name without expanding.
	k := soltype.PrintQualified(at)
	if c.aliasInterns == nil {
		c.aliasInterns = map[string]*soltype.AliasType{}
	}
	if rep, ok := c.aliasInterns[k]; ok {
		return rep
	}
	c.aliasInterns[k] = at
	return at
}

// aliasDef returns the registered AliasDef for a qualified alias name, or ok=false
// when no alias of that name has been registered on this Context.
func (c *Context) aliasDef(name string) (*AliasDef, bool) {
	def, ok := c.aliases[name]
	return def, ok
}

// registerAlias inserts def under a qualified alias name, allocating the registry
// map on first use.
func (c *Context) registerAlias(name string, def *AliasDef) {
	if c.aliases == nil {
		c.aliases = map[string]*AliasDef{}
	}
	c.aliases[name] = def
}

// classDef returns the registered ClassDef for a qualified class name, or ok=false
// when no class of that name has been registered on this Context.
func (c *Context) classDef(name string) (*ClassDef, bool) {
	def, ok := c.classes[name]
	return def, ok
}

// registerClass inserts def under a qualified class name, allocating the registry
// map on first use.
func (c *Context) registerClass(name string, def *ClassDef) {
	if c.classes == nil {
		c.classes = map[string]*ClassDef{}
	}
	c.classes[name] = def
}

// freshVar allocates a new inference variable at the given level, assigning it
// the next id in sequence.
func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}

// freshSkolem mints a distinct rigid type parameter carrying the given source name. It
// draws from the same counter as freshVar so every skolem has a unique ID, which is what
// keeps two parameters `T` and `U` from unifying.
func (c *Context) freshSkolem(name string) *soltype.SkolemType {
	s := &soltype.SkolemType{ID: c.varCounter, Name: name}
	c.varCounter++
	return s
}

// freshLifetime allocates a new lifetime variable at the given level, assigning it
// the next id in sequence. Lifetimes now ride the same let-generalization level
// hierarchy as types (M4 D2.5): a lifetime minted inside its scheme's
// generalize-level is freshened per instantiation, so two uses of a
// borrow-passing function never share one LifetimeVar.
func (c *Context) freshLifetime(level int) *soltype.LifetimeVar {
	lv := &soltype.LifetimeVar{ID: c.lifetimeCounter, Level: level}
	c.lifetimeCounter++
	return lv
}

// freshJoinLifetime allocates a lifetime variable for a multi-source join site
// (M4 D3). A join site is a return or branch uniting several borrows. It is
// identical to freshLifetime but sets Join, so coalesceLifetime expands it to the
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
// repeated outlives constraint can reuse the proxy rather than mint a new one (M4
// D2.5). Lazily allocates the map.
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

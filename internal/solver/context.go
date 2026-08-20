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

	// uniformKnots memoizes, per alias name, the μ-knot the regular-tree check proves for the
	// instantiations one level into that alias's recursion, or a zero entry when it proves none.
	// Nearly every alias memoizes the zero entry, so a reference pays one map lookup and nothing
	// more. The map lives for the whole run: the check reads only an alias's registered definition,
	// which no constraint and no discarded probe changes. See regular.go.
	uniformKnots map[string]*uniformKnot

	// unfoldingAliases counts, per alias name, how many constraints on the current path are running
	// on that alias's expansion. evalTypeOperator reads it to hand constrain an alias's μ-knot only
	// on the second unfolding, where a cycle can be, rather than on the first. Entries are added and
	// removed by constrainUnfoldingAlias around the constraint each unfolding produces, so the map
	// describes the current path rather than the whole run.
	unfoldingAliases map[string]int

	// knotting is true while a level of some alias's unfolding is being walked for the regular-tree
	// check. That walk reduces the level's residual operators, and a reduction can re-enter
	// constrain, which would ask for a knot again and start a second walk inside the first. The flag
	// makes the inner ask fall back to the plain expansion instead. See regular.go.
	knotting bool

	// muBinderCount numbers the μ-variables the regular-tree check has minted, so two aliases whose
	// knots are composed into one type carry distinct binders. coalesce numbers its own binders per
	// walk instead, since the knots one walk produces are numbered together.
	muBinderCount int

	// unwrapDepth counts the type operators constrain has evaluated along the constraint path it
	// is currently on, so an unwrap that never bottoms out is cut off at maxUnwrapDepth. constrain
	// increments it around each recursive call it makes on an evaluated operator and restores it
	// when that call returns, so the value always names the current path rather than the whole run.
	unwrapDepth int

	// shallowestAssumed is the depth of the shallowest goal that the running constraint derivation
	// has closed a coinductive assumption on. It holds math.MaxInt when the derivation has closed
	// on none. Each constrain frame takes the field over for its own derivation, then folds the
	// result back into the enclosing frame's value on the way out, so what a frame reads covers
	// its own subtree. Comparing that against the frame's own depth is what decides whether the
	// frame's verdict may be memoized — see seenPairs. Outside any derivation the field holds
	// whatever the last one left, which nothing reads.
	//
	// A branch whose failure the caller discards must not inform that caller's comparison. Every
	// arm that rejects a branch therefore restores the field alongside rolling the branch's bounds
	// back. Those arms are the losing trials in trialAndCommit, the throwaway-probe helpers beside
	// it, and constrainNominalWalk's rejected superclass candidates. An accepted branch keeps its
	// contribution, since the caller keeps that derivation. Folding a rejected branch in could
	// only ever suppress a memo entry the caller had earned, never admit one it had not, so the
	// restore buys precision rather than soundness.
	shallowestAssumed int

	// unionCommits maps an inference var that a union-super trial pinned by committing a bare
	// type-variable member to the super union it was chosen from, so `"hi" <: (T | number)`
	// records T → (T | number). A later constraint that forces an incompatible bound onto the
	// var reads this to breadcrumb the failure back to that union choice.
	unionCommits map[*soltype.TypeVarType]*soltype.UnionType

	// fusionRecorder records the interior provenance edge a fusion mints: fused was
	// produced by merging the atoms in from. The engine's fusion sites live on
	// *Context, while the Prov table lives on the checker carrier, so the carrier
	// installs this hook in newChecker to bridge the two. It is nil on a Context that
	// carries no such table, where a fusion records nothing. See recordFusionEdge for
	// what an installed recorder writes.
	fusionRecorder func(fused soltype.Type, from []soltype.Type)
}

// recordFusion records that fused came from merging the atoms in from, when a carrier
// installed a recorder. A fusion helper in normal.go or classes.go calls it at the
// fresh node it allocates, so the interior FromNormalization edge is minted at the
// one place that knows the two source atoms. A no-op when no recorder is installed,
// which is every Context built outside newChecker, such as the bare `&Context{}` the
// engine-level tests use.
func (c *Context) recordFusion(fused soltype.Type, from ...soltype.Type) {
	if c.fusionRecorder != nil {
		c.fusionRecorder(fused, from)
	}
}

// tagUnionCommit records that committing union u pinned v, so a later failure on v can name
// u. The prior entry is restored on rollback, so a discarded trial drops the tag with its bound.
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
	//
	// An argument a phantom parameter receives is erased first, since no instantiation's type
	// depends on it. `type Deep<T> = {a: Deep<{b: T}>}` denotes `{a: {a: …}}` whatever T is, so
	// Deep<number> and Deep<string> both key on "Deep" and share a representative.
	k := soltype.PrintQualified(c.erasePhantomArgs(at))
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

// notProductive reports whether t is a reference to an alias checkProductive rejected. Both the
// evaluator and constrain consult it to leave such a reference alone, since the alias names no type
// to unfold toward and its diagnostic is already reported.
func (c *Context) notProductive(t soltype.Type) bool {
	ref, ok := t.(*soltype.AliasType)
	if !ok {
		return false
	}
	def, registered := c.aliasDef(ref.Name)
	return registered && def.NotProductive
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

// freshMuBinder mints the next μ-variable for a knot the regular-tree check proves, naming it under
// the same X0, X1, … convention coalesce's binders follow.
func (c *Context) freshMuBinder() *soltype.RecursiveVarType {
	v := &soltype.RecursiveVarType{ID: c.muBinderCount, Name: muBinderName(c.muBinderCount)}
	c.muBinderCount++
	return v
}

// freshInferDecl mints the declaration one `infer U` clause introduces, carrying the source name for
// display. It draws from the same counter as freshVar so every declaration has a unique id, which is
// what keeps a nested conditional's `infer U` distinct from the enclosing conditional's clause of the
// same name. The node it returns is the reference form, which resolveCondTypeAnn binds in the scope
// the branches read; the clause itself copies the id onto a binder.
func (c *Context) freshInferDecl(name string) *soltype.InferType {
	t := &soltype.InferType{ID: c.varCounter, Name: name}
	c.varCounter++
	return t
}

// freshMappedKey mints the binding one mapped type's `for K in …` clause introduces, carrying the
// source name for display. It draws from the same counter as freshVar so every binding has a unique
// id, which is what keeps a nested mapped type's key distinct from the enclosing one's when both are
// written K. resolveMappedTypeAnn binds the node it returns in the scope the value, key-remapping,
// and filter positions resolve in, so each reference to K is that one binding.
func (c *Context) freshMappedKey(name string) *soltype.MappedKeyType {
	t := &soltype.MappedKeyType{ID: c.varCounter, Name: name}
	c.varCounter++
	return t
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

// joinLifetimes returns one lifetime that every member of lts outlives, so a value drawn
// from any of them may carry it. Several distinct lifetimes unite under a fresh join
// variable holding each of them as a lower bound, which is the join site freshJoinLifetime
// exists to serve. lts must not be empty.
//
// Where lts names one lifetime already, that lifetime is returned unchanged and the common
// `&'a A | &'a B` mints no join variable.
func (c *Context) joinLifetimes(level int, lts []soltype.Lifetime) soltype.Lifetime {
	shared := true
	for _, lt := range lts[1:] {
		if !soltype.ContainsLifetime(lts[:1], lt) {
			shared = false
			break
		}
	}
	if shared {
		return lts[0]
	}
	joinLt := c.freshJoinLifetime(level)
	for _, lt := range lts {
		c.constrainLt(lt, joinLt)
	}
	return joinLt
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

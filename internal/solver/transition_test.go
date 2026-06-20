package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M4 G1: mutability-transition checking ---
//
// The transition checker is ported from the old checker (check_transitions.go), with
// its two type predicates reimplemented over soltype.
//
// A freshly constructed literal can be given an owned-mutable type, so the
// construction case `val items: mut {x} = {x: 1}` type-checks and the source-level
// Rule 1 and Rule 3 scenarios are reachable. TestMutabilityTransitionsFromSource
// exercises those end to end through inferSource.
//
// The rest stay at the unit level. Rule 2, an immutable→mut transition over a live
// source, is rejected by the type system before the transition pass runs, so it never
// produces a transition error from source. Other old-checker cases need features the
// solver lacks: destructuring, enums and match, binary operators, unions, for-in
// loops, and top-level statements. TestCheckMutabilityTransition exercises the ported
// Rule 1 / Rule 2 / Rule 3 logic directly over a constructed alias/liveness state,
// covering the rules independently of those gaps.

func numT() *soltype.PrimType { return &soltype.PrimType{Prim: soltype.NumPrim} }
func objT() *soltype.ObjectType {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "x", Type: numT()},
	}}
}

// TestIsValueType covers the value-vs-reference predicate that gates alias tracking:
// primitives and literals (and unions of them) have copy semantics, so they are never
// alias-tracked; objects and borrows are reference types.
func TestIsValueType(t *testing.T) {
	require.True(t, isValueType(numT()))
	require.True(t, isValueType(&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}))
	require.True(t, isValueType(&soltype.UnionType{Types: []soltype.Type{
		&soltype.LitType{Lit: &soltype.StrLit{Value: "on"}},
		&soltype.LitType{Lit: &soltype.StrLit{Value: "off"}},
	}}))
	// A union with a non-value member is not a value type.
	require.False(t, isValueType(&soltype.UnionType{Types: []soltype.Type{numT(), objT()}}))
	// An empty union is not a value type (len == 0).
	require.False(t, isValueType(&soltype.UnionType{}))
	require.False(t, isValueType(objT()))
	require.False(t, isValueType(&soltype.RefType{Mut: true, Inner: objT()}))
	// An inference variable falls through to false, conservatively a reference. This is
	// the no-Prune divergence from the old checker; see isValueType's doc.
	require.False(t, isValueType(&soltype.TypeVarType{ID: 0, Level: 0}))
}

// TestIsSourceMutable locks the predicate that reads a source variable's recorded
// mutability from the alias tracker: a seeded mutable value reports true, a seeded
// immutable one reports false, and an unregistered source reports false.
func TestIsSourceMutable(t *testing.T) {
	// Models three names:
	//   val mutVar: mut {x: number} = {x: 1}   // seeded mutable
	//   val immVar: {x: number} = {x: 1}        // seeded immutable
	//   unseeded                                // a name the tracker never saw
	const (
		mutVar   = liveness.VarID(1)
		immVar   = liveness.VarID(2)
		unseeded = liveness.VarID(3)
	)
	a := liveness.NewAliasTracker()
	a.NewValue(mutVar, liveness.AliasMutable)
	a.NewValue(immVar, liveness.AliasImmutable)
	c := transitionFixture(nil, a, set.NewSet[liveness.VarID]())

	require.True(t, c.isSourceMutable(mutVar))
	require.False(t, c.isSourceMutable(immVar))
	require.False(t, c.isSourceMutable(unseeded))
}

// TestIsMutableType covers the predicate that distinguishes a mutable borrow from
// everything else: only a RefType with Mut set is mutable.
func TestIsMutableType(t *testing.T) {
	require.True(t, isMutableType(&soltype.RefType{Mut: true, Inner: objT()}))
	require.False(t, isMutableType(&soltype.RefType{Mut: false, Inner: objT()}))
	require.False(t, isMutableType(objT()))
	require.False(t, isMutableType(numT()))
}

// transitionFixture builds a checker whose enclosing funcCtx carries a constructed
// alias tracker plus a one-block liveness result in which exactly `live` is live after
// the single statement position. assignRef below points at that position.
func transitionFixture(
	varNames map[liveness.VarID]string,
	aliases *liveness.AliasTracker,
	live set.Set[liveness.VarID],
) *checker {
	c := newChecker()
	c.fn = &funcCtx{
		liveness: &liveness.LivenessInfo{
			LiveAfter: [][]set.Set[liveness.VarID]{{live}},
		},
		aliases:    aliases,
		varIDNames: varNames,
		varIDTypes: map[liveness.VarID]soltype.Type{},
		written:    map[fieldKey]soltype.Type{},
	}
	return c
}

var transitionRef = liveness.StmtRef{BlockID: 0, StmtIdx: 0}

// transitionSite is a placeholder blame node; the message under test does not read it.
var transitionSite ast.Node = &ast.IdentExpr{}

// transitionMessages renders every MutabilityTransitionError in errs, failing on any
// other error kind.
func transitionMessages(t *testing.T, errs []SolverError) []string {
	t.Helper()
	var msgs []string
	for _, e := range errs {
		me, ok := e.(*MutabilityTransitionError)
		require.True(t, ok, "unexpected non-transition error: %s", e.Message())
		msgs = append(msgs, me.Message())
	}
	return msgs
}

// TestCheckMutabilityTransition reproduces the old checker's transition cases over the
// ported Rule 1 / Rule 2 / Rule 3 logic.
func TestCheckMutabilityTransition(t *testing.T) {
	const (
		items   = liveness.VarID(1)
		snap    = liveness.VarID(2)
		rAlias  = liveness.VarID(3)
		config  = liveness.VarID(4)
		mutConf = liveness.VarID(5)
	)
	names := map[liveness.VarID]string{
		items: "items", snap: "snapshot", rAlias: "r", config: "config", mutConf: "mutableConfig",
	}

	t.Run("Rule1_MutToImmutable_SourceLive_Error", func(t *testing.T) {
		// Corresponds to:
		//   val items: mut {x: number} = {x: 1}
		//   val snapshot: {x: number} = items   // mut→immutable alias
		//   items.x = 2                         // items still used mutably after
		//   snapshot
		// items and snapshot are both live across the alias, so Rule 1 fires.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items, snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
		}, transitionMessages(t, c.errs))
	})

	t.Run("Rule1_MutToImmutable_TargetDead_OK", func(t *testing.T) {
		// Corresponds to:
		//   val items: mut {x: number} = {x: 1}
		//   val snapshot: {x: number} = items   // snapshot is never read again
		//   items.x = 2
		// snapshot is dead right after the alias, so there is no overlap window.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	t.Run("Rule1_MutToImmutable_SourceDead_OK", func(t *testing.T) {
		// Corresponds to:
		//   val items: mut {x: number} = {x: 1}
		//   val snapshot: {x: number} = items   // items is never used again
		//   snapshot                            // only snapshot is live
		// items is dead after the alias, so the mutable side cannot observe a change.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	t.Run("Rule2_ImmutableToMut_SourceLive_Error", func(t *testing.T) {
		// Corresponds to:
		//   val config: {x: number} = {x: 1}
		//   val mutableConfig: mut {x: number} = config  // immutable→mut alias
		//   config.x                                      // config still read after
		// config and mutableConfig are both live, so Rule 2 fires. This shape is not
		// reachable from source today: the type system rejects the immutable→mut bind
		// before the transition pass runs, so the state is built directly here.
		a := liveness.NewAliasTracker()
		a.NewValue(config, liveness.AliasImmutable)
		a.AddAlias(mutConf, config, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{config, mutConf}))
		c.checkMutabilityTransition(config, mutConf, "config", "mutableConfig", false, true, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'config' to mutable 'mutableConfig': 'config' is still used immutably after this point",
		}, transitionMessages(t, c.errs))
	})

	t.Run("Rule3_MutToMut_NoTransition", func(t *testing.T) {
		// Corresponds to:
		//   val items: mut {x: number} = {x: 1}
		//   val snapshot: mut {x: number} = items   // mut→mut alias
		//   items.x = 2
		// Same mutability is not a transition, so nothing is checked.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items, snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, true, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	t.Run("TransitiveAlias_NamesLiveMutableAlias", func(t *testing.T) {
		// Corresponds to:
		//   val p: mut {x: number} = {x: 1}
		//   val r: mut {x: number} = p   // live mutable alias of p
		//   val q: {x: number} = p       // mut→immutable; p itself is dead afterward
		//   r.x = 2                      // r keeps mutable access to the shared value
		//   q
		// p is dead after the alias, so the conflict names r, the live mutable alias,
		// not p itself.
		const (
			p = liveness.VarID(1)
			r = liveness.VarID(3)
			q = liveness.VarID(6)
		)
		nm := map[liveness.VarID]string{p: "p", r: "r", q: "q"}
		a := liveness.NewAliasTracker()
		a.NewValue(p, liveness.AliasMutable)
		a.AddAlias(r, p, liveness.AliasMutable)
		a.AddAlias(q, p, liveness.AliasImmutable)
		// p itself is dead after the transition; r and q are live.
		c := transitionFixture(nm, a, set.FromSlice([]liveness.VarID{r, q}))
		c.checkMutabilityTransition(p, q, "p", "q", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'q': 'r' still has mutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	t.Run("Conditional_SourceInMultipleSets_NoDuplicateConflicting", func(t *testing.T) {
		// Corresponds to:
		//   val a: mut {x: number} = {x: 1}
		//   val b: mut {x: number} = {x: 2}
		//   val c: mut {x: number} = if cond { a } else { b }  // c joins two alias sets
		//   val frozen: {x: number} = c                         // mut→immutable
		//   c.x = 3                                              // c still used mutably
		//   frozen
		// c is a live mutable alias in both sets, so it is reported once, not per set.
		const (
			a1 = liveness.VarID(1)
			b1 = liveness.VarID(2)
			cv = liveness.VarID(3)
			fr = liveness.VarID(4)
		)
		nm := map[liveness.VarID]string{a1: "a", b1: "b", cv: "c", fr: "frozen"}
		at := liveness.NewAliasTracker()
		at.NewValue(a1, liveness.AliasMutable)
		at.NewValue(b1, liveness.AliasMutable)
		// c is mut and aliases both a and b (conditional), so it sits in two sets.
		at.AddAlias(cv, a1, liveness.AliasMutable)
		at.AddAlias(cv, b1, liveness.AliasMutable)
		at.AddAlias(fr, cv, liveness.AliasImmutable)
		c := transitionFixture(nm, at, set.FromSlice([]liveness.VarID{cv, fr}))
		c.checkMutabilityTransition(cv, fr, "c", "frozen", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'c' to immutable 'frozen': 'c' is still used mutably after this point",
		}, transitionMessages(t, c.errs))
	})
}

// staticBorrow builds a mut/immutable borrow whose lifetime is forced to 'static,
// the shape borrowEscapedToStatic recognizes as a permanent outside alias (G2). The
// 'static upper bound is what D3's constrainEscape adds when a borrow escapes.
func staticBorrow(mut bool) *soltype.RefType {
	lt := &soltype.LifetimeVar{ID: 1, UpperBounds: []soltype.Lifetime{soltype.Static}}
	return &soltype.RefType{Mut: mut, Lt: lt, Inner: objT()}
}

// TestStaticEscapeTransition covers G2's lifetime-sort replacement for the dropped
// HasStatic{Mut,Imm}Alias bits: a source whose borrow escaped to 'static is a
// permanent outside alias, so a transition conflicts even when the source is locally
// dead after the transition point. Without the escape the same state is conflict-free.
func TestStaticEscapeTransition(t *testing.T) {
	const (
		src = liveness.VarID(1)
		tgt = liveness.VarID(2)
		mid = liveness.VarID(3)
	)
	names := map[liveness.VarID]string{src: "p", tgt: "snap", mid: "z"}

	// Corresponds to:
	//   var sink = {x: 0}
	//   fn cache(p: mut {x: number}) {
	//     sink = p                    // p's borrow escapes to 'static, mutably
	//     val snap: {x: number} = p   // mut→immutable; p is dead afterward
	//   }
	// p is locally dead, but its escaped mutable alias outside the function is
	// permanent, so the mut→immutable transition still conflicts. Only snap is live.
	t.Run("Rule1_MutEscape_SourceDead_Error", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(tgt, src, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = staticBorrow(true)
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'snap': a `'static` escape still has mutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	// The Rule 2 mirror of the case above: an immutable borrow that escaped to
	// 'static conflicts with an immutable→mut transition. The escaped immutable alias
	// outside is permanent, so it conflicts even with the source dead. The source form
	// is not reachable today, so the state is built directly here.
	t.Run("Rule2_ImmEscape_SourceDead_Error", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasImmutable)
		a.AddAlias(tgt, src, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = staticBorrow(false)
		c.checkMutabilityTransition(src, tgt, "p", "snap", false, true, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to mutable 'snap': a `'static` escape still has immutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	// A MUTABLE escape does NOT conflict with a Rule 2 immutable→mut transition: the
	// escaped mutability must match the rule's direction, mirroring the two independent
	// bits it replaced. Same shape as the case above but the source escaped mutably.
	t.Run("MutEscape_DoesNotTriggerRule2", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasImmutable)
		a.AddAlias(tgt, src, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = staticBorrow(true)
		c.checkMutabilityTransition(src, tgt, "p", "snap", false, true, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	// Corresponds to:
	//   fn f(p: mut {x: number}) {
	//     val snap: {x: number} = p   // p never escapes; p is dead afterward
	//   }
	// A borrow whose lifetime is NOT forced to 'static is an ordinary local borrow, so
	// a dead source produces no conflict.
	t.Run("UnforcedLifetime_SourceDead_OK", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(tgt, src, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = &soltype.RefType{
			Mut:   true,
			Lt:    &soltype.LifetimeVar{ID: 1},
			Inner: objT(),
		}
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	// Corresponds to:
	//   var sink = {x: 0}
	//   fn f(p: mut {x: number}) {
	//     val z: mut {x: number} = p   // z aliases p, the same value
	//     sink = z                      // z's borrow escapes to 'static, mutably
	//     val snap: {x: number} = p     // mut→immutable on p; p and z dead afterward
	//   }
	// The escape is carried by z, a TRANSITIVE alias, not by p itself. z's permanent
	// mutable alias of the shared value conflicts with the mut→immutable transition on
	// p. This exercises the per-member loop reaching past the source: the old
	// HasStaticMutAlias bit was set-level, so the replacement must find the escape on
	// any member of the source's set, not only on the source's own recorded type.
	t.Run("Rule1_TransitiveAliasEscape_Error", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(mid, src, liveness.AliasMutable)   // z aliases p, same set
		a.AddAlias(tgt, src, liveness.AliasImmutable) // snap aliases p, same set
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[mid] = staticBorrow(true) // z escaped, p did not
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'snap': a `'static` escape still has mutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	// Would correspond to:
	//   fn f(p: mut 'static {x: number}) {   // p is an explicit 'static borrow
	//     val snap: {x: number} = p          // mut→immutable; p dead afterward
	//   }
	// An explicit StaticLifetime on the member, not a LifetimeVar forced to 'static,
	// drives the same conflict. This covers borrowEscapedToStatic's *StaticLifetime
	// branch through the full transition path. The source-level form above is not
	// constructible yet: a lifetime annotation attaches only to a type reference, which
	// needs M7's TypeRef resolution, so this stays at the unit level until then.
	t.Run("Rule1_ExplicitStaticLifetime_Error", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(tgt, src, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = &soltype.RefType{Mut: true, Lt: soltype.Static, Inner: objT()}
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'snap': a `'static` escape still has mutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	// Corresponds to:
	//   var sink = {x: 0}
	//   fn f(p: mut {x: number}) {
	//     sink = p                    // p escapes to 'static
	//     val snap: {x: number} = p   // snap is never read, so it is dead
	//     p.x = 5                     // only p stays live
	//   }
	// An escaped source with a DEAD target reports nothing. The target-dead early
	// return precedes the member loop, so when no live window exists the escape never
	// produces a phantom conflict. snap is absent from the live set here.
	t.Run("MutEscape_TargetDead_OK", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(tgt, src, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{src}))
		c.fn.varIDTypes[src] = staticBorrow(true)
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})
}

// TestStaticEscapeTransitionFromSource is the end-to-end counterpart: a `mut` borrow
// stored into module-level `sink` escapes to 'static (D3), creating a permanent mutable
// alias outside the function. Aliasing that borrow into the immutable `snap` is then a
// mut→immutable transition that conflicts with the escape, even though `p` is dead after
// the alias. The query over `p`'s 'static-forced lifetime is what reports it in G2.
//
// Before G2 the dropped HasStaticMutAlias bit was never set, so this case was silently
// accepted as a false negative. A second error rides along: binding the borrow into the
// owned slot `snap` is a borrow escape, the same known divergence from internal/checker
// that TestTransitionWiringReportsRule1Error pins. G3 removes it by reborrowing the
// initializer. So both messages are asserted.
func TestStaticEscapeTransitionFromSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		var sink = {x: 0}
		fn cache(p: mut {x: number}) {
			sink = p
			val snap: {x: number} = p
			snap
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.ElementsMatch(t, []string{
		"borrowed value mut object does not live long enough to satisfy object",
		"cannot assign 'p' to immutable 'snap': a `'static` escape still has mutable access to 'p' after this point",
	}, msgs)
}

// TestBorrowEscapedToStatic locks the lifetime-sort query G2 uses in place of the
// dropped escape bits: a borrow forced to 'static is recognized with its mutability, an
// owned value or an unforced borrow is not.
func TestBorrowEscapedToStatic(t *testing.T) {
	c := transitionFixture(nil, liveness.NewAliasTracker(), set.NewSet[liveness.VarID]())

	// `mut {x}` whose lifetime a global write forced to 'static, e.g. a `mut` param
	// after `sink = p`. Escaped, mutably.
	c.fn.varIDTypes[1] = staticBorrow(true)
	mut, escaped := c.borrowEscapedToStatic(1)
	require.True(t, escaped)
	require.True(t, mut)

	// The immutable analogue: a `{x}` borrow forced to 'static. Escaped, immutably.
	c.fn.varIDTypes[2] = staticBorrow(false)
	mut, escaped = c.borrowEscapedToStatic(2)
	require.True(t, escaped)
	require.False(t, mut)

	// An explicit annotation `mut 'static {x}` escapes too.
	c.fn.varIDTypes[3] = &soltype.RefType{Mut: true, Lt: soltype.Static, Inner: objT()}
	_, escaped = c.borrowEscapedToStatic(3)
	require.True(t, escaped)

	// An owned value such as `val v = {x: 0}` never escapes.
	c.fn.varIDTypes[4] = objT()
	_, escaped = c.borrowEscapedToStatic(4)
	require.False(t, escaped)

	// An unrecorded variable does not escape.
	_, escaped = c.borrowEscapedToStatic(99)
	require.False(t, escaped)

	// 'static in the LOWER bounds is not an escape. The escape constraint `v <:
	// 'static` adds an UPPER bound, so a lower-bound 'static, which can arise from a
	// join member, must not be read as an escape. forcedToStatic would over-report it.
	c.fn.varIDTypes[5] = &soltype.RefType{
		Mut:   true,
		Lt:    &soltype.LifetimeVar{ID: 5, LowerBounds: []soltype.Lifetime{soltype.Static}},
		Inner: objT(),
	}
	_, escaped = c.borrowEscapedToStatic(5)
	require.False(t, escaped)
}

// TestTransitionReassignNestedRHS guards the currentStmt fix: a reassignment whose
// RHS contains statements (`b = if cond { … } else { … }`) re-enters inferStmt while
// walking the RHS, which overwrites c.fn.currentStmt. The reassignment transition path
// must use the statement captured before the RHS walk, not the clobbered field, so it
// resolves the correct CFG StmtRef. The body type-checks with no spurious error.
func TestTransitionReassignNestedRHS(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test(cond: boolean) {
			var b = 0
			b = if cond { 1 } else { 2 }
			b
		}
	`)
	require.Empty(t, errs)
}

// TestTransitionWiringNoSpuriousErrors confirms the liveness pre-pass is wired into
// function-body inference and that the alias-tracking paths run over real bodies
// without inventing a transition error. Each case type-checks cleanly, so any
// MutabilityTransitionError would be a wiring bug. The cases exercise the decl-alias
// branches and parameter seeding end to end, which the constructed-state unit tests
// above do not.
func TestTransitionWiringNoSpuriousErrors(t *testing.T) {
	tests := map[string]string{
		// Immutable owned objects aliased down a chain. No mutability, no transition.
		"immutable_chain": `
			fn test() {
				val a = {x: 1}
				val b = a
				val c = b
				c
			}
		`,
		// Single-source decl alias: an immutable param aliased to a val exercises the
		// AliasSourceVariable branch of trackAliasesForIdentPat.
		"single_source_alias": `
			fn test(q: {y: number}) {
				val r = q
				r
			}
		`,
		// Multi-source decl alias: an if/else over two params makes the binding alias
		// both, exercising the AliasSourceMultiple branch.
		"multi_source_alias": `
			fn test(cond: boolean, a: {x: number}, b: {x: number}) {
				val c = if cond { a } else { b }
				c
			}
		`,
		// A mut and an immutable parameter are seeded into the alias tracker, and a
		// field write through the mut param walks the prop-assignment path.
		"params_seeded": `
			fn test(p: mut {x: number}, q: {y: number}) {
				p.x = 5
				q.y
			}
		`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Empty(t, errs)
		})
	}
}

// TestTransitionWiringReportsRule1Error is the error counterpart to
// TestTransitionWiringNoSpuriousErrors: it proves the wired pre-pass reports a real
// mut→immutable (Rule 1) transition error from source, not just that it stays silent on
// benign bodies.
//
// Before split-5 the only owned-mutable value is a `mut` parameter, so p (mut) is aliased
// into immutable q and then mutated, leaving both live across the alias. Rule 1 fires.
//
// A second error rides along: binding the `mut` borrow into the owned slot `q` is a borrow
// escape, so "does not live long enough" is reported too. That escape is a known divergence
// from internal/checker, which accepts this binding — G3 removes it by reborrowing the
// initializer instead of treating q as an owned slot. The clean dead-source variant, where
// only the transition rule is in play, is covered without the escape by
// TestMutabilityTransitionsFromSource (Rule1_SourceDead_OK) once split-5 supplies an owned
// mutable value, and by TestCheckMutabilityTransition over constructed state. So this test
// asserts only the live-source case and pins both messages, escape included.
func TestTransitionWiringReportsRule1Error(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test(p: mut {x: number}) {
			val q: {x: number} = p
			p.x = 5
			q.x
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.ElementsMatch(t, []string{
		"borrowed value mut object does not live long enough to satisfy object",
		"cannot assign 'p' to immutable 'q': 'p' is still used mutably after this point",
	}, msgs)
}

// TestCollectOuterBindingsPreludeCache covers the outer-binding collection that feeds
// the rename pass: every reachable value name maps to a distinct negative id, the
// prelude's operator names are included, and the prelude cache makes repeated calls
// return the same result.
func TestCollectOuterBindingsPreludeCache(t *testing.T) {
	c := newChecker()
	scope := sharedPrelude().Child()
	scope.defineValue("myLocal", ValueBinding{})

	first := c.collectOuterBindings(scope)

	require.Contains(t, first, "myLocal")
	require.Contains(t, first, "+") // a prelude operator name
	for name, id := range first {
		require.Negative(t, int(id), "outer binding %q must have a negative id", name)
	}
	require.Same(t, scope.parent, c.preludeNamesRoot) // prelude root was cached

	// A second call returns an equal mapping, so the cached prelude names do not corrupt
	// the result.
	require.Equal(t, first, c.collectOuterBindings(scope))
}

// TestMutabilityTransitionsFromSource reproduces the old checker's transition cases at
// the source level, now reachable because a fresh literal can be constructed into an
// owned-mutable binding. Each case mints its mutable value with `val x: mut {…} = {…}`,
// aliases it, and asserts the transition verdict. want is empty for the safe cases.
func TestMutabilityTransitionsFromSource(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Rule 1 (mut→immutable): error when the mutable source is live after the alias.
		"Rule1_SourceLive_Error": {
			src: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					val snapshot: {x: number} = items
					items.x = 2
					snapshot
				}
			`,
			want: []string{
				"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
			},
		},
		// Rule 1: safe when the mutable source is dead after the alias.
		"Rule1_SourceDead_OK": {
			src: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					items.x = 2
					val snapshot: {x: number} = items
					snapshot
				}
			`,
		},
		// Rule 3: two mutable aliases of the same value are always allowed.
		"Rule3_MultipleMutableAliases_OK": {
			src: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					b.x = 2
					a.x
				}
			`,
		},
		// Chain aliasing through a mutable intermediate: the conflict names the live
		// mutable alias, not the source itself.
		"ChainAlias_TargetLive_Error": {
			src: `
				fn test() {
					val a: mut {x: number} = {x: 1}
					val b: mut {x: number} = a
					val c: {x: number} = b
					a.x = 2
					c
				}
			`,
			want: []string{
				"cannot assign 'b' to immutable 'c': 'a' still has mutable access to 'b' after this point",
			},
		},
		// Conditional aliasing: c aliases both branches; a is live after the transition.
		"Conditional_IfElse_Error": {
			src: `
				fn test(cond: boolean) {
					val a: mut {x: number} = {x: 0}
					val b: mut {x: number} = {x: 1}
					val c: {x: number} = if cond { a } else { b }
					a.x = 5
					c
				}
			`,
			want: []string{
				"cannot assign 'a' to immutable 'c': 'a' is still used mutably after this point",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, transitionMessages(t, errs))
		})
	}
}

// TestMutabilityTransitionReassignFromSource exercises the reassignment transition path
// (inferAssign). TestMutabilityTransitionsFromSource only aliases through declarations, so
// it never walks the `x = e` reassignment branch. Reassigning a live mutable owned value
// into an immutable binding is a Rule 1 transition; the fresh-literal upgrade is what mints
// the owned-mutable source these cases reassign from.
func TestMutabilityTransitionReassignFromSource(t *testing.T) {
	t.Run("source_live_error", func(t *testing.T) {
		// items is reassigned into immutable snap, then mutated, so both are live across
		// the reassignment.
		_, _, errs := inferSource(t, `
			fn f() {
				var snap: {x: number} = {x: 0}
				val items: mut {x: number} = {x: 1}
				snap = items
				items.x = 2
				snap
			}
		`)
		require.Equal(t, []string{
			"cannot assign 'items' to immutable 'snap': 'items' is still used mutably after this point",
		}, transitionMessages(t, errs))
	})

	t.Run("source_dead_ok", func(t *testing.T) {
		// items is mutated before the reassignment and never again, so it is dead after
		// snap = items and Rule 1 stays silent.
		_, _, errs := inferSource(t, `
			fn f() {
				var snap: {x: number} = {x: 0}
				val items: mut {x: number} = {x: 1}
				items.x = 2
				snap = items
				snap
			}
		`)
		require.Empty(t, transitionMessages(t, errs))
	})
}

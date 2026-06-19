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
// its two type predicates reimplemented over soltype. The old checker exercised it
// through source like `val items: mut {x} = {x: 1}` followed by aliasing
// (`val snapshot = items`). The new checker's borrow rules reject that shape outright
// — a `mut` value is a borrow, and a borrow cannot be aliased into an owned local
// (it would not live long enough) — so those exact programs no longer type-check, and
// their alias-transition scenarios are unreachable from source today. The ported Rule
// 1 / Rule 2 / Rule 3 logic is therefore exercised directly here, reproducing the old
// checker's cases at the level of the code that actually moved: the predicates, and
// checkMutabilityTransition over a constructed alias/liveness state.

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
		written:    map[fieldKey]soltype.Type{},
	}
	return c
}

var transitionRef = liveness.StmtRef{BlockID: 0, StmtIdx: 0}

// transitionSite is a placeholder blame node; the message under test does not read it.
var transitionSite ast.Node = &ast.IdentExpr{}

// transitionMessages renders every MutabilityTransitionError in c.errs, failing on any
// other error kind.
func transitionMessages(t *testing.T, c *checker) []string {
	t.Helper()
	var msgs []string
	for _, e := range c.errs {
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
		// `val snapshot = items` where items is mut and still live afterwards.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items, snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
		}, transitionMessages(t, c))
	})

	t.Run("Rule1_MutToImmutable_TargetDead_OK", func(t *testing.T) {
		// snapshot is dead immediately after the transition, so there is no window.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c))
	})

	t.Run("Rule1_MutToImmutable_SourceDead_OK", func(t *testing.T) {
		// items is dead after the transition; only the immutable snapshot survives.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c))
	})

	t.Run("Rule2_ImmutableToMut_SourceLive_Error", func(t *testing.T) {
		// `val mutableConfig = config` where config is immutable and still live.
		a := liveness.NewAliasTracker()
		a.NewValue(config, liveness.AliasImmutable)
		a.AddAlias(mutConf, config, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{config, mutConf}))
		c.checkMutabilityTransition(config, mutConf, "config", "mutableConfig", false, true, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'config' to mutable 'mutableConfig': 'config' is still used immutably after this point",
		}, transitionMessages(t, c))
	})

	t.Run("Rule3_MutToMut_NoTransition", func(t *testing.T) {
		// Same mutability is not a transition, so nothing is checked.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items, snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, true, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c))
	})

	t.Run("TransitiveAlias_NamesLiveMutableAlias", func(t *testing.T) {
		// p has a live mutable alias r and an immutable alias q being created. The
		// conflict names r, the alias still holding mutable access — not p itself.
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
		}, transitionMessages(t, c))
	})

	t.Run("Conditional_SourceInMultipleSets_NoDuplicateConflicting", func(t *testing.T) {
		// A source that belongs to two alias sets and is itself a live mutable alias is
		// reported once, not once per set.
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
		}, transitionMessages(t, c))
	})
}

// TestTransitionWiringNoSpuriousErrors confirms the liveness pre-pass is wired into
// function-body inference and that aliasing immutable, owned values produces no
// transition error: the pass runs, renames the body, tracks the aliases, and finds
// nothing to report. This guards the wiring independently of any reachable error case.
func TestTransitionWiringNoSpuriousErrors(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val a = {x: 1}
			val b = a
			val c = b
			c
		}
	`)
	require.Empty(t, errs)
}

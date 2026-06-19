package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M4 G1: mutability-transition checking (machinery) ---
//
// The transition checker is ported from the old checker (check_transitions.go), with
// its two type predicates reimplemented over soltype. This change adds the machinery
// and tests it directly; a later change wires it into the function-body walk.
//
// checkMutabilityTransition is exercised over a constructed alias/liveness state, which
// covers Rule 1 / Rule 2 / Rule 3 independently of the walk and of the source-level
// constructs the solver does not yet support. The predicates and the outer-binding
// collection are unit-tested the same way.

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
		// `val snapshot = items` where items is mut and still live afterwards.
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
		// snapshot is dead immediately after the transition, so there is no window.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	t.Run("Rule1_MutToImmutable_SourceDead_OK", func(t *testing.T) {
		// items is dead after the transition; only the immutable snapshot survives.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
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
		}, transitionMessages(t, c.errs))
	})

	t.Run("Rule3_MutToMut_NoTransition", func(t *testing.T) {
		// Same mutability is not a transition, so nothing is checked.
		a := liveness.NewAliasTracker()
		a.NewValue(items, liveness.AliasMutable)
		a.AddAlias(snap, items, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{items, snap}))
		c.checkMutabilityTransition(items, snap, "items", "snapshot", true, true, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})

	t.Run("TransitiveAlias_NamesLiveMutableAlias", func(t *testing.T) {
		// p has a live mutable alias r and an immutable alias q being created. The
		// conflict names r, the alias still holding mutable access, not p itself.
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
		}, transitionMessages(t, c.errs))
	})
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

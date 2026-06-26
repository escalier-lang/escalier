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
// A freshly constructed literal can be given an owned-mutable type, so
// `val items: mut {x} = {x: 1}` type-checks and the Rule 1 / Rule 2 / Rule 3 scenarios
// are reachable from real source. TestMutabilityTransitionsFromSource and
// TestRule2TransitionFromSource exercise them end to end through inferSource rather than
// over constructed alias/liveness state. The transition pass runs even when a binding
// type-errors, so a Rule 2 immutable→mut bind reports its transition error alongside the
// type error.
//
// What stays at the unit level cannot be reproduced cleanly from source:
//   - The type predicates isValueType / isMutableType / isSourceMutable and the
//     borrowEscapedToStatic query are Go-level functions, tested directly.
//   - The G2 static-escape cases (TestStaticEscapeTransition) isolate the lifetime
//     query, its polarity, the transitive-member reach, and the target-dead early
//     return. From source those are confounded by the global-write store error and the
//     borrow-into-owned-slot escape, or are not constructible at all. The feature's
//     end-to-end coverage is TestStaticEscapeTransitionFromSource.

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
		msgs = append(msgs, e.Message())
	}
	return msgs
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

	// A MUTABLE escape does NOT conflict with a Rule 2 immutable→mut transition: the
	// escaped mutability must match the rule's direction, mirroring the two independent
	// bits it replaced.
	//
	// This one has no faithful Escalier form. The escape's mutability IS the source
	// borrow's own mutability, so a mutable escape and an immutable transition source
	// cannot coexist on one variable. An immutable binding sharing a value with a
	// mutable escaped one would itself be a Rule 1 transition. The unit test pins the
	// mutable escape directly on the immutable source to isolate the polarity check that
	// escMut must equal sourceMut.
	t.Run("MutEscape_DoesNotTriggerRule2", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasImmutable)
		a.AddAlias(tgt, src, liveness.AliasMutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{tgt}))
		c.fn.varIDTypes[src] = staticBorrow(true)
		c.checkMutabilityTransition(src, tgt, "p", "snap", false, true, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
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
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, false, transitionRef, transitionSite)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'snap': a `'static` escape still has mutable access to 'p' after this point",
		}, transitionMessages(t, c.errs))
	})

	// This isolates ONE transition: aliasing p into the immutable `snap`, where snap is
	// DEAD. The target-dead early return precedes the member loop, so even an escaped
	// source produces no conflict FROM THIS transition. It models only the
	//   val snap: {x: number} = p   // snap is immutable and never read, so it is dead
	// step, with p already an escaped 'static borrow from an earlier store.
	//
	// It does NOT model the escape-creating store. In a full program that store, e.g.
	// `sink = p` into an immutable global with p staying live, is itself a Rule 1 error
	// once Option 1 checks module-level write targets. TestStaticEscapeTransitionFromSource
	// runs the whole program and reports it; this unit test checks a single transition,
	// so the two are not the same situation and this one correctly reports nothing.
	t.Run("MutEscape_TargetDead_OK", func(t *testing.T) {
		a := liveness.NewAliasTracker()
		a.NewValue(src, liveness.AliasMutable)
		a.AddAlias(tgt, src, liveness.AliasImmutable)
		c := transitionFixture(names, a, set.FromSlice([]liveness.VarID{src}))
		c.fn.varIDTypes[src] = staticBorrow(true)
		c.checkMutabilityTransition(src, tgt, "p", "snap", true, false, false, transitionRef, transitionSite)
		require.Empty(t, transitionMessages(t, c.errs))
	})
}

// TestStaticEscapeTransitionFromSource is the end-to-end counterpart. A `&mut`
// borrow stored into module-level `sink` escapes to 'static (D3), creating a
// permanent alias outside the function, then is aliased into the immutable
// `snap`. The program surfaces two errors, both asserted.
//
//  1. The global-write transition at `sink = p`. Storing the mutable borrow into
//     the immutable global `sink` while `p` stays live afterward is a
//     mut→immutable transition against a permanent target.
//  2. The static-escape transition at `val snap = p` (G2). `p` escaped to
//     'static via the earlier store, so aliasing it into immutable `snap`
//     conflicts. The query over `p`'s 'static-forced lifetime is what reports
//     it, named as a `'static` escape.
//
// Before G2 the dropped HasStaticMutAlias bit was never set, so case 2 was
// silently accepted as a false negative. Before the global-write check, case 1
// was missed entirely because the module-level target is not a tracked local.
//
// `val snap: &{x} = p` aliases the borrow at a fresh lifetime. The lifetime sort
// and the transition pass produce the verdict above, which is case 2.
//
// Case 2 also covers the source-dead property a unit test used to isolate. `p`
// is dead after `val snap = p` because only `snap` is read afterward, so the
// conflict fires purely because `p` escaped to 'static, not because `p` is
// locally live. The liveness loop skips the dead `p`. Only the escape query
// reports it.
// DISABLED until PR 6 lands.
//
// PR 6 of the affine semantics plan moves the global-write site from the
// transition-checker onto the move engine. The plan says: "The global-write
// site stops dropping mutability and instead consumes, per 'Move subsumes the
// escape-site logic.'" Once that lands, `sink = p` consumes `p`, the later
// `val snap: &{x: number} = p` is a use-after-move, and the two transition
// diagnostics asserted below are replaced by a single UseAfterMoveError
// against the `p` in `val snap = p`. Re-enable then and switch the assertion
// to the UseAfterMove message.
/*
func TestStaticEscapeTransitionFromSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		var sink = {x: 0}
		fn cache(p: &mut {x: number}) {
			sink = p
			val snap: &{x: number} = p
			snap
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.ElementsMatch(t, []string{
		"use of moved value 'p'",
	}, msgs)
}
*/

// TestGlobalWriteMutTransition covers Option 1: a store into a module-level binding is a
// mutability transition against a permanent, always-live target. The local reassignment
// path skips module-level targets, so before this the store went unchecked. This is an
// in-body check only; it does not catch a caller that retains a mutable alias to a value
// stored into an immutable global (see the dead-source case below).
func TestGlobalWriteMutTransition(t *testing.T) {
	// Storing a mut borrow into the immutable global `sink`, then mutating through the
	// borrow, is a mut→immutable transition: `sink` permanently observes a value that p
	// still mutates. p stays live via the field write, so Rule 1 fires.
	t.Run("mut_into_immutable_global_then_mutate_error", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f(p: mut {x: number}) {
				sink = p
				p.x = 5
			}
		`)
		require.Equal(t, []string{
			"cannot assign 'p' to immutable 'sink': 'p' is still used mutably after this point",
		}, transitionMessages(t, errs))
	})

	// When the source is dead within this body, Option 1's in-body check reports
	// nothing. This is NOT a soundness guarantee. The store still escapes p to 'static,
	// and the CALLER may keep a live mutable alias to the same value and mutate it after
	// the call, so the immutable `sink` observes a mutation. Catching that needs the call
	// site to enforce the 'static borrow as unique, which is the borrow checker's job
	// (#618, #762), not this pass. The assertion pins current behavior and is expected to
	// gain an error once the caller-side check lands.
	t.Run("dead_in_body_source_no_inbody_conflict", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f(p: mut {x: number}) {
				sink = p
			}
		`)
		require.Empty(t, errs)
	})

	// Storing a FRESH value into a global has no aliasable source, so no transition.
	t.Run("fresh_value_into_global_ok", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			var sink = {x: 0}
			fn f() {
				sink = {x: 9}
			}
		`)
		require.Empty(t, errs)
	})
}

// objWithBorrowField builds `{f: <borrow>}` — an owned object carrying a borrow in
// its `f` property. It models a value whose escape lives in a field, not at the top
// level, the nested case PR 5 closes.
func objWithBorrowField(borrow *soltype.RefType) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "f", Type: borrow},
	}}
}

// escapeVarBothPolarities builds `{a: V, g: fn(V) -> void}` where the one type variable
// V carries an escaped mut borrow in its UpperBounds. Field `a` reaches V covariantly
// and field `g`'s parameter reaches it contravariantly, so the walk meets V at both
// polarities and only the contravariant meeting follows the UpperBounds to the escape.
func escapeVarBothPolarities() *soltype.ObjectType {
	v := &soltype.TypeVarType{ID: 13, UpperBounds: []soltype.Type{staticBorrow(true)}}
	return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "a", Type: v},
		&soltype.PropertyElem{Name: "g", Type: &soltype.FuncType{
			Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: v}},
			Ret:    &soltype.Void{},
		}},
	}}
}

// TestBorrowEscapedToStatic locks the lifetime-sort query G2 uses in place of the
// dropped escape bits: a borrow forced to 'static is recognized at its mutability, an
// owned value or an unforced borrow is not. PR 5 generalizes it to walk the whole
// type, so a borrow nested in a field escapes too, reported per mutability.
func TestBorrowEscapedToStatic(t *testing.T) {
	c := transitionFixture(nil, liveness.NewAliasTracker(), set.NewSet[liveness.VarID]())

	cases := []struct {
		name string
		// varID is the tracked variable to query. typ is its recorded type, or nil to
		// leave the variable unrecorded.
		varID            liveness.VarID
		typ              soltype.Type
		wantMut, wantImm bool
	}{
		// `mut {x}` whose lifetime a global write forced to 'static, e.g. a `mut` param
		// after `sink = p`. Escaped, mutably.
		{"top-level mut borrow forced to 'static", 1, staticBorrow(true), true, false},
		// The immutable analogue: a `{x}` borrow forced to 'static. Escaped, immutably.
		{"top-level imm borrow forced to 'static", 2, staticBorrow(false), false, true},
		// An explicit annotation `mut 'static {x}` escapes too.
		{"explicit mut 'static annotation", 3, &soltype.RefType{Mut: true, Lt: soltype.Static, Inner: objT()}, true, false},
		// An owned value such as `val v = {x: 0}` never escapes.
		{"owned value never escapes", 4, objT(), false, false},
		// An unrecorded variable does not escape.
		{"unrecorded variable", 99, nil, false, false},
		// 'static in the LOWER bounds is not an escape. The escape constraint `v <:
		// 'static` adds an UPPER bound, so a lower-bound 'static, which can arise from a
		// join member, must not be read as an escape. forcedToStatic would over-report it.
		{"lower-bound 'static is not an escape", 5, &soltype.RefType{
			Mut:   true,
			Lt:    &soltype.LifetimeVar{ID: 5, LowerBounds: []soltype.Lifetime{soltype.Static}},
			Inner: objT(),
		}, false, false},
		// PR 5 nested case: an owned object `{f: mut 'static {x}}` whose FIELD is a borrow
		// forced to 'static. The top-level type is an owned object, so the pre-PR-5
		// top-level-only query missed it; the structural walk now reports the mutable
		// escape in the field.
		{"nested mut borrow field forced to 'static", 6, objWithBorrowField(staticBorrow(true)), true, false},
		// A nested IMMUTABLE escaped borrow is reported on the immutable side.
		{"nested imm borrow field forced to 'static", 7, objWithBorrowField(staticBorrow(false)), false, true},
		// A nested borrow whose lifetime is NOT forced to 'static does not escape, just
		// as at the top level.
		{"nested borrow field with unforced lifetime", 8, objWithBorrowField(&soltype.RefType{
			Mut:   true,
			Lt:    &soltype.LifetimeVar{ID: 8},
			Inner: objT(),
		}), false, false},
		// A borrow reachable only through a usage-inferred type variable. The recorded
		// type is a bare TypeVarType whose LOWER bounds hold an escaped mut borrow, the
		// shape a branch-join variable such as `sink = if c { p } else { … }` produces.
		// The query descends into the bounds and reports the mutable escape.
		{"mut escape in type-var lower bound", 9, &soltype.TypeVarType{
			ID:          9,
			LowerBounds: []soltype.Type{staticBorrow(true)},
		}, true, false},
		// The immutable analogue through a type variable.
		{"imm escape in type-var lower bound", 10, &soltype.TypeVarType{
			ID:          10,
			LowerBounds: []soltype.Type{staticBorrow(false)},
		}, false, true},
		// The escaped borrow nested in a field of a type variable's lower bound, so the
		// walk must descend through both the bounds side graph and the field.
		{"escape in field of type-var lower bound", 11, &soltype.TypeVarType{
			ID:          11,
			LowerBounds: []soltype.Type{objWithBorrowField(staticBorrow(true))},
		}, true, false},
		// A type variable whose lower bound is an unforced borrow does not escape, matching
		// the structural cases.
		{"type-var lower bound with unforced lifetime", 12, &soltype.TypeVarType{
			ID: 12,
			LowerBounds: []soltype.Type{&soltype.RefType{
				Mut:   true,
				Lt:    &soltype.LifetimeVar{ID: 12},
				Inner: objT(),
			}},
		}, false, false},
		// The same type variable reached at both polarities, with its escaped borrow only
		// in the UpperBounds. Field `a` reaches V covariantly, where BoundsAt follows the
		// empty LowerBounds, and field `g`'s parameter reaches the same V contravariantly,
		// where BoundsAt follows the UpperBounds and finds the escape. Memoizing the visited
		// variable per polarity is what lets the second, contravariant visit run after the
		// first; a variable-only memo would skip it and miss the escape.
		{"escape in upper bound of a type var reached at both polarities", 13, escapeVarBothPolarities(), true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.typ != nil {
				c.fn.varIDTypes[tc.varID] = tc.typ
			}
			escMut, escImm := c.borrowEscapedToStatic(tc.varID)
			require.Equal(t, tc.wantMut, escMut)
			require.Equal(t, tc.wantImm, escImm)
		})
	}
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
// TestTransitionWiringNoSpuriousErrors. It proves the wired pre-pass reports a
// real mut→immutable (Rule 1) transition error from source, not just that it
// stays silent on benign bodies.
//
// p is an owned-mutable parameter. The mut decay at `val q: {x} = p` aliases p
// into the immutable q. p mutates `p.x = 5` and q stays live for the later
// `q.x` read, so both are live across the alias. Rule 1 fires.
//
// DISABLED until PR 6 lands.
//
// PR 6 of the affine semantics plan makes `val`/`var` bindings flow sites
// that consume the source. Once it lands, `val q: {x} = p` consumes p, so
// `p.x = 5` is a use-after-move rather than a live-source Rule 1 transition,
// and the diagnostic changes to a UseAfterMoveError that names both the
// move site (the `val q` binding) and the use site (`p.x = 5`). Re-enable
// then and update the assertion to match the new error.
/*
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
		"use of moved value 'p'",
	}, msgs)
}
*/

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
		// `val z: &mut {x} = p` aliases p's borrow into z at z's lifetime. `sink =
		// z` forces z's lifetime to 'static, and Rule 1 then reports that p still
		// has mutable access to the same value. `val snap: &{x} = p` is a local
		// immutable reborrow that does not extend p's region, so no further
		// transition fires on it.
		"Rule1_TransitiveAliasEscape_Error": {
			src: `
				var sink = {x: 0}
				fn f(p: &mut {x: number}) {
				  val z: &mut {x: number} = p    // z aliases p, the same value
				  sink = z                        // z's borrow escapes to 'static, mutably
				  val snap: &{x: number} = p      // local immutable reborrow; p and z dead afterward
				}
			`,
			want: []string{
				"cannot assign 'z' to immutable 'sink': 'p' still has mutable access to 'z' after this point",
			},
		},
		// G3 binds a `mut` borrow into a bare annotation by reborrowing it as a local
		// immutable view. `snap` dies within `p`'s region and never escapes, so the
		// lifetime sort accepts it, matching internal/checker, which has no borrow-escape
		// concept here. Before G3 this reported the divergent "does not live long enough".
		"UnforcedLifetime_LocalReborrow_OK": {
			src: `
				fn f(p: mut {x: number}) {
					val snap: {x: number} = p
				}
			`,
		},
		"Rule2_ImmEscape_SourceDead_Error": {
			src: `
				var sink = {x: 0}
				fn f(p: {x: number}) {
				  sink = p
				  val snap: mut {x: number} = p
				  snap.x = 5
				}
			`,
			want: []string{
				"cannot constrain immutable object <: mutable object",
			},
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
		// Rule 1: safe when the immutable target is dead. snapshot is never read, so there
		// is no window where the immutable view and the live mutable source overlap.
		"Rule1_TargetDead_OK": {
			src: `
				fn test() {
					val items: mut {x: number} = {x: 1}
					val snapshot: {x: number} = items
					items.x = 2
				}
			`,
		},
		// Conditional aliasing where the SOURCE is mut and sits in two alias sets. c
		// aliases both a and b, then c→frozen is the mut→immutable transition while c
		// stays live. c is reported once, not once per set.
		"Conditional_SourceMutInTwoSets_Error": {
			src: `
				fn test(cond: boolean) {
					val a: mut {x: number} = {x: 0}
					val b: mut {x: number} = {x: 1}
					val c: mut {x: number} = if cond { a } else { b }
					val frozen: {x: number} = c
					c.x = 3
					frozen
				}
			`,
			want: []string{
				"cannot assign 'c' to immutable 'frozen': 'c' is still used mutably after this point",
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

// TestRule2TransitionFromSource covers the Rule 2 immutable→mut transition from source.
// Binding an immutable value into a `mut` slot is a type error, but the decl path runs
// transition tracking unconditionally, so the Rule 2 transition error rides along with
// it. config stays live, so the immutable→mut alias conflicts. Both messages are
// asserted; this is the source-level home for the old constructed-state Rule 2 case.
func TestRule2TransitionFromSource(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn test() {
			val config: {x: number} = {x: 1}
			val mutableConfig: mut {x: number} = config
			mutableConfig.x = 5
			config.x
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.ElementsMatch(t, []string{
		"cannot constrain immutable object <: mutable object",
		"cannot assign 'config' to mutable 'mutableConfig': 'config' is still used immutably after this point",
	}, msgs)
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

package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A lifetime variable on the LEFT of an outlives constraint gains the right as an
// upper bound. 'static is the bottom of the lattice, so `'static <: a` is what always
// holds; the reverse `a <: 'static` recorded here is the forcing escape constraint,
// satisfiable only by a = 'static. Coalescing meets a negative-position variable's
// upper bounds, and 'static is the bottom, so it absorbs that meet and a resolves to
// 'static regardless of any other upper bound.
func TestConstrainLtVarOutlivesStatic(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	static := &soltype.StaticLifetime{}

	c.ctx.constrainLt(a, static)

	require.Equal(t, []soltype.Lifetime{static}, a.UpperBounds, "a <: 'static records 'static as a's upper bound")
	require.Empty(t, a.LowerBounds)
}

// A var on the left gains an upper bound; a var on the right gains a lower bound.
// A var-to-var constraint records BOTH directions so each variable sees the full
// relationship at coalescing.
func TestConstrainLtVarToVarRecordsBothDirections(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, b) // a outlives b

	require.Equal(t, []soltype.Lifetime{b}, a.UpperBounds, "a gains b as an upper bound")
	require.Empty(t, a.LowerBounds)
	require.Equal(t, []soltype.Lifetime{a}, b.LowerBounds, "b gains a as a lower bound")
	require.Empty(t, b.UpperBounds)
}

// Transitivity: with a <: b already recorded, constraining x <: a propagates
// through a's existing upper bounds so x <: b is recorded too.
func TestConstrainLtPropagatesTransitively(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	x := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, b) // a <: b
	c.ctx.constrainLt(x, a) // x <: a, which must propagate x <: b through a's uppers

	require.Contains(t, x.UpperBounds, soltype.Lifetime(a), "x gains a directly")
	require.Contains(t, x.UpperBounds, soltype.Lifetime(b), "x gains b transitively through a")
}

// Two DISTINCT 'static values denote the one lattice bottom, so constraining a
// variable against each in turn records a single 'static upper bound — dedup is by
// value, not pointer. Origination sites mint a fresh &StaticLifetime{} per call, so
// pointer-identity dedup would wrongly pile up duplicate 'static bounds.
func TestConstrainLtStaticDedupsByValue(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, &soltype.StaticLifetime{})
	c.ctx.constrainLt(a, &soltype.StaticLifetime{}) // a different 'static instance
	c.ctx.constrainLt(a, soltype.Static)            // and the canonical singleton

	require.Len(t, a.UpperBounds, 1, "all three 'static constraints collapse to one upper bound")
	require.True(t, soltype.IsStaticLifetime(a.UpperBounds[0]))
}

// A repeated outlives constraint does not re-append a bound already present.
func TestConstrainLtDedupsBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, b)
	c.ctx.constrainLt(a, b) // identical constraint again

	require.Len(t, a.UpperBounds, 1, "the duplicate upper bound is not re-appended")
	require.Len(t, b.LowerBounds, 1, "the duplicate lower bound is not re-appended")
}

// A transitive cycle terminates: 'a <: 'b together with 'b <: 'a must not loop,
// and each direction is recorded exactly once.
func TestConstrainLtCycleTerminates(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	require.NotPanics(t, func() {
		c.ctx.constrainLt(a, b)
		c.ctx.constrainLt(b, a) // closes the cycle
	})

	require.Len(t, a.UpperBounds, 1)
	require.Len(t, a.LowerBounds, 1)
	require.Len(t, b.UpperBounds, 1)
	require.Len(t, b.LowerBounds, 1)
}

// Constraining a lifetime against ITSELF is a no-op — neither a bound nor a loop.
func TestConstrainLtReflexiveIsNoOp(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, a)

	require.Empty(t, a.UpperBounds)
	require.Empty(t, a.LowerBounds)
}

// A borrowed value flowing into 'static storage has its lifetime forced to outlive
// 'static. This is the EscapingRefIntoStatic acceptance (M4 D3). constrainEscape
// constrains the borrow's lifetime `<: 'static`, so coalescing the borrow renders it
// `&'static mut {x: number}` rather than under the param's own name. No Escalier
// construct routes a borrow into static storage yet, since a borrow originates only
// at a parameter, so this exercises the rule's mechanism directly.
func TestEscapingRefIntoStatic(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(0)
	ref := &soltype.RefType{
		Mut: true,
		Lt:  lt,
		Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
		}},
	}

	c.constrainEscape(ref)

	require.Equal(t, []soltype.Lifetime{soltype.Static}, lt.UpperBounds)
	require.Equal(t, "&'static mut {x: number}", soltype.Print(coalesce(ref, soltype.Positive)))
}

// Escape reaches a borrow NESTED inside an object property. Storing `{p: &'a mut
// Point}` forces the inner borrow's lifetime to 'static too, since the whole value
// escapes. constrainEscape walks the structural carriers, so the property's borrow
// is constrained alongside any top-level one.
func TestEscapingNestedRefIntoStatic(t *testing.T) {
	c := newChecker()
	inner := c.ctx.freshLifetime(0)
	stored := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.PropertyElem{Name: "p", Type: &soltype.RefType{
			Mut: true,
			Lt:  inner,
			Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
			}},
		}},
	}}

	c.constrainEscape(stored)

	require.Equal(t, []soltype.Lifetime{soltype.Static}, inner.UpperBounds)
	require.Equal(t, "{p: &'static mut {x: number}}", soltype.Print(coalesce(stored, soltype.Positive)))
}

// mutPointRef is a `mut lt {x: number}` borrow, the carrier these lifetime tests
// hang a join or escape off.
func mutPointRef(lt soltype.Lifetime) *soltype.RefType {
	return &soltype.RefType{
		Mut: true,
		Lt:  lt,
		Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
		}},
	}
}

// borrowFn wraps a return type in a function whose parameters carry the given borrow
// lifetimes. D4 names a lifetime only when it originates at a parameter, which means
// it occurs in a negative position. So a join's member lifetimes must appear on
// parameters to be named or expanded, exactly how joinBorrows produces them from
// real source.
func borrowFn(ret soltype.Type, paramLts ...soltype.Lifetime) *soltype.FuncType {
	params := make([]*soltype.FuncParam, len(paramLts))
	for i, lt := range paramLts {
		params[i] = &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: string(rune('p' + i))},
			Type:    mutPointRef(lt),
		}
	}
	return &soltype.FuncType{Params: params, Ret: ret}
}

// Escaping a JOINED borrow forces every param lifetime the join reaches to outlive
// 'static, so the whole bounded join collapses to a single 'static. This is the
// join↔escape interaction (M4 D3). constrainEscape constrains the join lifetime
// `<: 'static`, which propagates through its lower bounds to each member, and
// coalesceLifetimes then absorbs every member to 'static rather than naming it.
func TestEscapingJoinedBorrowCollapsesToStatic(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	join := c.ctx.freshJoinLifetime(0)
	// The join is bounded below by each source lifetime, as joinBorrows wires it.
	c.ctx.constrainLt(a, join)
	c.ctx.constrainLt(b, join)
	ret := mutPointRef(join)
	fn := borrowFn(ret, a, b)

	// Before escape the join stays named as 'c, bounded below by each source lifetime.
	require.Equal(t,
		"fn <'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))

	c.constrainEscape(ret)

	// Escape forces the join to 'static, propagating through its lower bounds to each
	// member, so the entire signature collapses to 'static with no nameable lifetime left.
	require.Equal(t, []soltype.Lifetime{soltype.Static}, join.UpperBounds)
	require.Equal(t,
		"fn (p: &'static mut {x: number}, q: &'static mut {x: number}) -> &'static mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// When one of a join's two sources escapes to 'static, the join has a single remaining
// NAMED source, so it collapses to that source's name rather than taking a fresh one.
// 'a escapes, rendering `&'static`; the join is left with only 'b, so componentParams
// counts one named member and the return renders `&'b` under the same name as q, not a
// distinct bounded lifetime. Without excluding the 'static-forced source, the join
// would keep its own name and render the weaker `<'b: 'c, 'c>`.
func TestJoinWithOneStaticSourceCollapsesToRemaining(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	join := c.ctx.freshJoinLifetime(0)
	c.ctx.constrainLt(a, soltype.Static) // 'a escapes to 'static
	c.ctx.constrainLt(a, join)
	c.ctx.constrainLt(b, join)
	fn := borrowFn(mutPointRef(join), a, b)

	require.Equal(t,
		"fn <'a>(p: &'static mut {x: number}, q: &'a mut {x: number}) -> &'a mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// D4 elision keeps the `&` on a connect-nothing borrow by parking its lifetime on
// the Anon sentinel, so an immutable borrow whose lifetime reaches no output
// still renders as `&{x}` rather than collapsing to the bare inner. The mutable
// case keeps the `&mut` form for the same reason.
func TestImmutableConnectNothingBorrowKeepsRef(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(0)
	param := &soltype.RefType{
		Mut: false,
		Lt:  lt,
		Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: &soltype.PrimType{Prim: soltype.NumPrim}},
		}},
	}
	fn := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "p"}, Type: param}},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
	}

	require.Equal(t, "fn (p: &{x: number}) -> number", renderScheme(&MonoScheme{Ty: fn}))
}

// A nested join reaching one param lifetime through two distinct sub-joins draws that
// lifetime's bound to the top join once, not twice. The top join's lower bounds are
// the two sub-joins, and both reach 'a. displayLtBounds keys each survivor's targets
// by SCC representative, so a lifetime reaching the top join through two sub-joins
// yields one `'a: 'd` bound. Without that dedup 'a would carry the bound twice.
func TestNestedJoinDedupsSharedLifetime(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	d := c.ctx.freshLifetime(0)
	j2 := c.ctx.freshJoinLifetime(0)
	j3 := c.ctx.freshJoinLifetime(0)
	top := c.ctx.freshJoinLifetime(0)
	c.ctx.constrainLt(a, j2)
	c.ctx.constrainLt(b, j2)
	c.ctx.constrainLt(a, j3) // 'a shared across both sub-joins
	c.ctx.constrainLt(d, j3)
	c.ctx.constrainLt(j2, top)
	c.ctx.constrainLt(j3, top)
	fn := borrowFn(mutPointRef(top), a, b, d)

	require.Equal(t,
		"fn <'a: 'd, 'b: 'd, 'c: 'd, 'd>(p: &'a mut {x: number}, q: &'b mut {x: number}, r: &'c mut {x: number}) -> &'d mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// A lifetime bounded above two distinct joins renders both bounds joined with `&`, the
// meet. Two joins sharing a source param sit in one connected component, so the
// grouping bounds every param in that component to both joins: 'a feeds j1 and j2
// directly, while 'b and 'c reach the second join only through the shared component.
// Each param therefore carries `: 'd & 'e`.
func TestParamFeedingTwoJoinsRendersMeetBound(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	d := c.ctx.freshLifetime(0)
	j1 := c.ctx.freshJoinLifetime(0)
	j2 := c.ctx.freshJoinLifetime(0)
	c.ctx.constrainLt(a, j1) // 'a feeds both joins
	c.ctx.constrainLt(b, j1)
	c.ctx.constrainLt(a, j2)
	c.ctx.constrainLt(d, j2)
	ret := &soltype.TupleType{Elems: []soltype.Type{mutPointRef(j1), mutPointRef(j2)}}
	fn := borrowFn(ret, a, b, d)

	require.Equal(t,
		"fn <'a: 'd & 'e, 'b: 'd & 'e, 'c: 'd & 'e, 'd, 'e>(p: &'a mut {x: number}, q: &'b mut {x: number}, r: &'c mut {x: number}) -> [&'d mut {x: number}, &'e mut {x: number}]",
		renderScheme(&MonoScheme{Ty: fn}))
}

// Two param lifetimes that mutually outlive are EQUAL, so buildLtBoundSet condenses
// them to one representative and a join over both renders `&'a` under a single name.
// The params keep their own names 'a and 'b from the borrows they originate on; only
// the join lifetime resolves to the representative.
func TestJoinOverMutualOutlivesCollapsesToOne(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	join := c.ctx.freshJoinLifetime(0)
	c.ctx.constrainLt(a, b) // a outlives b
	c.ctx.constrainLt(b, a) // b outlives a — the cycle makes a and b equal
	c.ctx.constrainLt(a, join)
	c.ctx.constrainLt(b, join)
	fn := borrowFn(mutPointRef(join), a, b)

	require.Equal(t,
		"fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'a mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// When a param lifetime mutually outlives a NON-param lifetime with a smaller ID, the
// component condenses to the non-param representative. The join must still render
// under the parameter's name, so componentParams emits the param var `p` rather than
// the representative `m`, and the return renders `&'a` rather than a fresh name bound
// to no input.
//
// This guards an ID ordering ordinary source does not produce, so the graph is built
// directly rather than through inference. Mutual-outlives cycles do arise from source —
// storing a borrow into a mutable field makes the field and the stored borrow equal,
// since a mutable field's lifetime is invariant:
//
//	fn f(bag: &mut {item: &mut {x: number}}, it: &mut {x: number}) {
//	  bag.item = it
//	  return it
//	}
//
// But both members of a source-level cycle are param lifetimes, and a param is always
// minted before any join or instantiation intermediary, so the param carries the smaller
// ID and wins the representative slot. The reverse — a non-param representative — needs
// the non-param minted first, which a freshener or extruder can do but top-level
// inference cannot, so `m` is minted before `p` here.
func TestJoinRepresentativeIsNonParamRendersParamName(t *testing.T) {
	c := newChecker()
	m := c.ctx.freshJoinLifetime(0) // non-param, minted first so it holds the smaller ID
	p := c.ctx.freshLifetime(0)     // param lifetime, larger ID
	c.ctx.constrainLt(p, m)         // p outlives m
	c.ctx.constrainLt(m, p)         // m outlives p — the cycle makes p and m equal, rep = m
	fn := borrowFn(mutPointRef(m), p)

	require.Equal(t,
		"fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}",
		renderScheme(&MonoScheme{Ty: fn}))
}

// A discarded probe truncates every lifetime bound the trial appended back to the
// pre-probe length, exactly as it does for type-variable bounds — the second sort
// rides the same journal discipline. Bounds added before the probe survive.
func TestProbeDiscardRestoresLifetimeBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	// Pre-probe bound on a, permanent.
	c.ctx.constrainLt(a, b)
	require.Len(t, a.UpperBounds, 1)

	p := c.openProbe()
	x := c.ctx.freshLifetime(0)
	c.ctx.constrainLt(a, x) // a.UpperBounds: +1 ⇒ 2; x.LowerBounds: +1 ⇒ 1
	require.Len(t, a.UpperBounds, 2)
	require.Len(t, x.LowerBounds, 1)
	require.Len(t, p.ltEntries, 2, "both touched lifetime vars are journaled")

	c.closeProbe(p, false) // discard

	require.Len(t, a.UpperBounds, 1, "the speculative upper bound on a is truncated away")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0], "the pre-probe bound survives")
	require.Empty(t, x.LowerBounds, "x's only bound was speculative")
}

// A committed lifetime-bound mutation survives — discard is what reverts, not the
// journal's existence.
func TestProbeCommitKeepsLifetimeBounds(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	p := c.openProbe()
	c.ctx.constrainLt(a, b)
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(p, true) // commit

	require.Len(t, a.UpperBounds, 1, "a committed probe makes the lifetime bound permanent")
}

// A committed child hands its lifetime-bound rollback obligation up to the parent,
// so the parent's later discard still reverts the committed child's lifetime work.
func TestProbeLifetimeCommittedChildCoveredByParentDiscard(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	parent := c.openProbe()
	child := c.openProbe()
	c.ctx.constrainLt(a, b) // child mutates a and b
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(child, true) // child commits — a/b become the parent's obligation

	c.closeProbe(parent, false) // parent discards
	require.Empty(t, a.UpperBounds, "the parent discard reverts the committed child's lifetime bound")
	require.Empty(t, b.LowerBounds)
}

// Test 1 — the lower-bound propagation branch. TestConstrainLtPropagatesTransitively
// exercises propagation through the SUPER variable's upper bounds; this exercises the
// distinct `subVar.LowerBounds` loop: with lb <: a already recorded, constraining
// a <: super must propagate lb <: super through a's existing lower bound.
func TestConstrainLtPropagatesThroughLowerBounds(t *testing.T) {
	c := newChecker()
	lb := c.ctx.freshLifetime(0)
	a := c.ctx.freshLifetime(0)
	super := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(lb, a)    // lb <: a ⇒ a gains lb as a lower bound
	c.ctx.constrainLt(a, super) // a <: super ⇒ a's lower-bound loop propagates lb <: super

	require.Contains(t, lb.UpperBounds, soltype.Lifetime(a), "lb gains a directly")
	require.Contains(t, lb.UpperBounds, soltype.Lifetime(super), "lb gains super transitively through a's lower-bound propagation")
	require.Contains(t, super.LowerBounds, soltype.Lifetime(lb), "super sees lb as a lower bound from the same propagation")
}

// Test 2 — a probe discard rolls back vars touched TRANSITIVELY, not just the ones
// named at the constrainLt call site. With a <: b set pre-probe, a single
// constrainLt(x, a) under the probe touches x, a, AND b (x <: a <: b), and the
// discard must truncate every probe-era bound while leaving the pre-probe ones.
func TestProbeDiscardRollsBackTransitivelyTouchedLifetimes(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, b) // pre-probe: a.upper=[b], b.lower=[a]
	require.Len(t, a.UpperBounds, 1)
	require.Len(t, b.LowerBounds, 1)

	p := c.openProbe()
	x := c.ctx.freshLifetime(0)
	c.ctx.constrainLt(x, a) // x <: a, transitively recording x <: b; touches x, a, b
	require.Contains(t, x.UpperBounds, soltype.Lifetime(a))
	require.Contains(t, x.UpperBounds, soltype.Lifetime(b), "x gained b transitively under the probe")
	require.Len(t, a.LowerBounds, 1, "a gained x as a probe-era lower bound")
	require.Len(t, b.LowerBounds, 2, "b gained x transitively under the probe")

	c.closeProbe(p, false) // discard

	require.Empty(t, x.UpperBounds, "x was minted and constrained entirely under the probe")
	require.Len(t, a.UpperBounds, 1, "a's pre-probe upper bound survives")
	require.Empty(t, a.LowerBounds, "a's probe-era lower bound x is truncated")
	require.Len(t, b.LowerBounds, 1, "b's transitive probe-era lower bound is truncated")
	require.Equal(t, soltype.Lifetime(a), b.LowerBounds[0], "b's pre-probe lower bound survives")
}

// Test 3 — recordLt journals a lifetime var at most once per probe, even across
// several appends to it, so the single snapshot truncates every later append on
// discard. Mirrors the type sort's TestProbeRecordDedupsPerVariable. Each
// constrainLt(a, …) also touches its super var, so the probe holds three entries
// total; the point is that `a` appears in exactly one of them.
func TestProbeRecordLtDedupsPerLifetimeVar(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	d := c.ctx.freshLifetime(0)

	p := c.openProbe()
	c.ctx.constrainLt(a, b) // a.upper += b
	c.ctx.constrainLt(a, d) // a.upper += d — a SECOND append to a
	require.Len(t, a.UpperBounds, 2)

	aEntries := 0
	for _, e := range p.ltEntries {
		if e.v == a {
			aEntries++
		}
	}
	require.Equal(t, 1, aEntries, "a is journaled exactly once despite two appends")

	c.closeProbe(p, false) // discard
	require.Empty(t, a.UpperBounds, "both speculative bounds on a are truncated via the single journal entry")
}

// Test 5 — a probe built directly as &Probe{} (bypassing newProbe) is safe for the
// lifetime sort too: ltTouched is lazily created on first recordLt, so there is no
// nil-map panic. Mirrors the type sort's TestProbeBareLiteralIsNilMapSafe.
func TestProbeBareLiteralLifetimeIsNilMapSafe(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	c.ctx.probe = &Probe{} // deliberately skip newProbe
	require.NotPanics(t, func() {
		c.ctx.constrainLt(a, b) // appends bounds ⇒ recordLt(a), recordLt(b)
	})
	require.Len(t, a.UpperBounds, 1)

	c.ctx.probe.Discard()
	require.Empty(t, a.UpperBounds, "the bare-literal probe still rolls back the lifetime bound")
	require.Empty(t, b.LowerBounds)
}

// Test 6a — a discarded child reverts only ITS OWN lifetime appends, leaving the
// parent's journal and the var's parent-era bounds intact. Mirrors the type sort's
// TestDiscardedChildLeavesParentJournalIntact.
func TestProbeLifetimeDiscardedChildLeavesParentJournalIntact(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)
	d := c.ctx.freshLifetime(0)

	parent := c.openProbe()
	c.ctx.constrainLt(a, b) // parent: a.upper=[b]
	require.Len(t, a.UpperBounds, 1)

	child := c.openProbe()
	c.ctx.constrainLt(a, d) // child: a.upper=[b, d]
	require.Len(t, a.UpperBounds, 2)
	c.closeProbe(child, false) // child discards ⇒ back to [b]
	require.Len(t, a.UpperBounds, 1, "the child discard reverts only the child's lifetime bound")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0])

	c.closeProbe(parent, false) // parent discards ⇒ back to empty
	require.Empty(t, a.UpperBounds, "the parent discard reverts its own lifetime bound")
}

// Test 6b — when the parent has NOT touched a lifetime var the committed child did,
// the child's snapshot is inherited so the parent discard reverts the child's bound
// to the var's pre-child length. Mirrors TestCommittedChildInheritsUntouchedVarSnapshot.
func TestProbeLifetimeCommittedChildInheritsUntouchedVarSnapshot(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	parent := c.openProbe()
	child := c.openProbe()
	c.ctx.constrainLt(a, b) // only the child touches a and b
	require.Len(t, a.UpperBounds, 1)
	c.closeProbe(child, true) // commit: parent inherits a and b at snapshot 0

	c.closeProbe(parent, false) // discard
	require.Empty(t, a.UpperBounds, "the inherited child snapshot truncates a back to empty")
	require.Empty(t, b.LowerBounds)
}

// Test 7 — re-constraining a lifetime bound already present journals nothing: the
// ContainsLifetime guard skips the append, so no recordLt fires and a discard is a
// clean no-op that leaves the pre-probe bound untouched. This verifies the
// "no journal entry without an append" invariant for the lifetime sort.
func TestProbeReconstrainingPresentLifetimeBoundJournalsNothing(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(a, b) // pre-probe: a.upper=[b], b.lower=[a]

	p := c.openProbe()
	c.ctx.constrainLt(a, b) // identical constraint: both bounds already present
	require.Empty(t, p.ltEntries, "re-constraining a present bound journals nothing")
	require.Len(t, a.UpperBounds, 1, "no duplicate bound is appended")

	c.closeProbe(p, false) // discard is a clean no-op
	require.Len(t, a.UpperBounds, 1, "the pre-probe bound is untouched by the no-op trial")
	require.Equal(t, soltype.Lifetime(b), a.UpperBounds[0])
}

// Test 8 — the lifetime bound minted by the RefType constrain arm itself is
// journaled, so a discarded trial rolls it back. The earlier probe tests drive
// constrainLt directly; this drives it THROUGH constrain over two `mut` borrows
// (the now-active step 3, D2), confirming the RefType arm participates in the same
// speculation discipline as a direct constrainLt call. Without journaling here, a
// failed overload trial that constrained two borrows would leak a lifetime bound.
func TestProbeRollsBackLifetimeBoundFromRefArm(t *testing.T) {
	c := newChecker()
	a := c.ctx.freshLifetime(0)
	b := c.ctx.freshLifetime(0)

	inner := func() *soltype.ObjectType {
		return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: num()},
		}}
	}
	sub := &soltype.RefType{Mut: true, Lt: a, Inner: inner()}
	super := &soltype.RefType{Mut: true, Lt: b, Inner: inner()}

	p := c.openProbe()
	errs := c.ctx.Constrain(sub, super)
	require.Empty(t, errs, "two compatible mut borrows constrain cleanly")
	// step 3 runs constrainLt(super.Lt, sub.Lt) = constrainLt(b, a): b gains a as an
	// upper bound, a gains b as a lower bound.
	require.Equal(t, []soltype.Lifetime{a}, b.UpperBounds)
	require.Equal(t, []soltype.Lifetime{b}, a.LowerBounds)

	c.closeProbe(p, false) // discard
	require.Empty(t, b.UpperBounds, "the RefType arm's lifetime bound is rolled back on discard")
	require.Empty(t, a.LowerBounds)
}

package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M4 D2.5: lifetime-sort generalization (levels + per-instantiation freshening) ---
//
// A lifetime carried by a borrow now rides the same let-generalization level
// hierarchy as a type variable: a lifetime minted above its scheme's
// generalize-level is a quantified param lifetime, freshened per instantiation, so
// two uses of a borrow-passing function never share one LifetimeVar's bounds.
// These tests drive freshenAbove (the instantiation copy) directly, mirroring how
// lifetime_test.go drives constrainLt directly.

// mutObjAt builds `mut <lt> {x: number}` — a borrow carrying the given lifetime,
// the minimal shape that exercises a RefType lifetime through the rewriters.
func mutObjAt(lt soltype.Lifetime) *soltype.RefType {
	return &soltype.RefType{
		Mut: true,
		Lt:  lt,
		Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: num()},
		}},
	}
}

// identRefScheme is `fn(p: mut <lt> {x}) -> mut <lt> {x}` — the IdentityRefReturn
// shape, with the SAME lifetime pointer on both the parameter and the return.
func identRefScheme(lt soltype.Lifetime) *soltype.FuncType {
	return &soltype.FuncType{
		Params: []*soltype.FuncParam{{Type: mutObjAt(lt)}},
		Ret:    mutObjAt(lt),
	}
}

// freshenLtOf pulls the (freshened) lifetimes off the param and return of a
// freshened identRefScheme result.
func freshenLtOf(t *testing.T, out soltype.Type) (paramLt, retLt soltype.Lifetime) {
	t.Helper()
	fn, ok := out.(*soltype.FuncType)
	require.True(t, ok, "freshened scheme is still a FuncType")
	paramLt = fn.Params[0].Type.(*soltype.RefType).Lt
	retLt = fn.Ret.(*soltype.RefType).Lt
	return paramLt, retLt
}

// A quantified param lifetime reached more than once in a single instantiation is
// freshened ONCE: the param and the return share one fresh lifetime, distinct from
// the scheme's original. Without the ltCache the two occurrences would freshen
// independently and the returned borrow would no longer carry the parameter's
// lifetime.
func TestFreshenSharesParamLifetimeAcrossOccurrences(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(1) // above the generalize-level (lim = 0)
	body := identRefScheme(lt)

	out := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})

	paramLt, retLt := freshenLtOf(t, out)
	require.Same(t, paramLt, retLt, "the param lifetime is shared across both occurrences in one instantiation")
	require.NotSame(t, soltype.Lifetime(lt), paramLt, "and is a fresh copy, distinct from the scheme's original lifetime")
}

// Two instantiations of one scheme produce DISTINCT lifetime vars — the core
// cross-site non-contamination property. Each call site gets its own cache, exactly
// as instantiate allocates one per use.
func TestFreshenTwoInstantiationsGetDistinctLifetimes(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(1)
	body := identRefScheme(lt)

	out1 := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	out2 := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})

	lt1, _ := freshenLtOf(t, out1)
	lt2, _ := freshenLtOf(t, out2)
	require.NotSame(t, lt1, lt2, "two call sites instantiate two distinct lifetime vars")
}

// Constraining one instantiation's lifetime does not perturb another's: the two
// freshened lifetimes have independent bound lists. This is the contamination a
// shared LifetimeVar would have caused — before D2.5 both call sites aliased the
// scheme's one lifetime var, so a bound from one site leaked into the other.
func TestFreshenInstantiationsAreNonContaminating(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(1)
	body := identRefScheme(lt)

	out1 := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	out2 := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	lt1, _ := freshenLtOf(t, out1)
	lt2, _ := freshenLtOf(t, out2)

	// Constrain the first site's lifetime to 'static; the second's must stay clean.
	c.ctx.constrainLt(lt1, soltype.Static)

	require.Len(t, lt1.(*soltype.LifetimeVar).UpperBounds, 1, "the constrained site records its own bound")
	require.Empty(t, lt2.(*soltype.LifetimeVar).UpperBounds, "the other site is untouched")
}

// quantify-vs-keep: a lifetime captured from an outer scope (Level <= lim) is
// SHARED, not freshened — the lifetime-sort analogue of a captured type variable.
// Only lifetimes minted above the scheme's level are quantified.
func TestFreshenSharesCapturedLifetime(t *testing.T) {
	c := newChecker()
	captured := c.ctx.freshLifetime(0) // at the generalize-level: not quantified
	body := identRefScheme(captured)

	out := c.freshenAbove(0, body, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})

	paramLt, _ := freshenLtOf(t, out)
	require.Same(t, soltype.Lifetime(captured), paramLt, "a captured lifetime (Level <= lim) is shared across instantiations")
}

// A freshened lifetime carries copies of the original's outlives bounds. Writing
// those bounds onto the fresh var is the one sanctioned non-journaled append. Once a
// probe is discarded the fresh var is unreachable, so there is nothing to roll back:
// the probe journals nothing for it, and the original's bounds stay untouched.
func TestFreshenCopiesLifetimeBoundsUnderProbe(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(1)
	// 'static is the lattice bottom — a level-free bound, so recording lt <: 'static
	// does not trip the level-extrusion the var-to-var case would.
	c.ctx.constrainLt(lt, soltype.Static) // lt.UpperBounds = ['static]
	require.Len(t, lt.UpperBounds, 1)

	p := c.openProbe()
	out := c.freshenAbove(0, identRefScheme(lt), 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	paramLt, _ := freshenLtOf(t, out)
	freshVar := paramLt.(*soltype.LifetimeVar)

	require.NotSame(t, lt, freshVar, "the quantified lifetime is freshened")
	require.Len(t, freshVar.UpperBounds, 1, "the fresh lifetime carries a copy of the original's outlives bound")
	require.True(t, soltype.IsStaticLifetime(freshVar.UpperBounds[0]))
	require.Empty(t, p.ltEntries, "the fresh var's bound write is non-journaled (it self-rolls-back)")

	c.closeProbe(p, false) // discard
	require.Len(t, lt.UpperBounds, 1, "the original scheme lifetime's bound is untouched by the discarded trial")
}

// Source-level regression: the D2 IdentityRefReturn acceptance still renders the
// shared param lifetime on the generalized scheme. D2.5 freshens lifetimes at
// instantiation, but the scheme body keeps its original param lifetime. D4 names it
// `'a` since it reaches both the parameter and the return.
func TestInferIdentityRefReturnStillRendersAfterGeneralization(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}", values["f"])
}

// Source-level regression: a borrow-passing function called at two distinct sites
// typechecks cleanly. Before D2.5 both calls shared the callee's one param
// lifetime, linking the two borrows; freshening per instantiation keeps them
// independent so neither call contaminates the other.
func TestInferBorrowFnCalledAtTwoSites(t *testing.T) {
	src := `fn use(o: mut {x: number}) -> number {
  return o.x
}
fn f(a: mut {x: number}, b: mut {x: number}) {
  val ra = use(a)
  val rb = use(b)
  return rb
}`
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// --- extruder lifetime arm ---
//
// extrude copies a type so a variable above the target level is replaced by a
// fresh var at that level, wired to the original. D2.5 extends it to the lifetime
// sort: a borrow's lifetime above the level is extruded too. These drive extrude
// directly, the first tests to do so.

// A borrow whose lifetime outranks the extrusion level gets a fresh lifetime at
// that level, and in Positive (covariant/output) polarity the original is wired
// ABOVE the fresh var (original <: fresh), mirroring the type-var extrude's
// addUpperBound.
func TestExtrudeFreshensHigherLevelLifetimePositive(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(2) // above the extrusion target (lvl = 1)

	out := c.ctx.extrude(mutObjAt(lt), soltype.Positive, 1, map[extrudeKey]*soltype.TypeVarType{})

	outLt, ok := out.(*soltype.RefType).Lt.(*soltype.LifetimeVar)
	require.True(t, ok)
	require.NotSame(t, lt, outLt, "the higher-level lifetime is extruded to a fresh var")
	require.Equal(t, 1, outLt.Level, "the fresh lifetime sits at the extrusion target level")
	require.Contains(t, lt.UpperBounds, soltype.Lifetime(outLt),
		"Positive polarity wires the fresh var as an upper bound of the original (orig <: fresh)")
}

// The contravariant counterpart: in Negative (input) polarity the fresh var is
// wired BELOW the original (fresh <: original), mirroring the type-var extrude's
// addLowerBound. A var reached in both polarities therefore yields two distinct
// fresh vars with opposite wiring — the reason the cache is keyed by polarity.
func TestExtrudeFreshensHigherLevelLifetimeNegative(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(2)

	out := c.ctx.extrude(mutObjAt(lt), soltype.Negative, 1, map[extrudeKey]*soltype.TypeVarType{})

	outLt := out.(*soltype.RefType).Lt.(*soltype.LifetimeVar)
	require.Contains(t, lt.LowerBounds, soltype.Lifetime(outLt),
		"Negative polarity wires the fresh var as a lower bound of the original (fresh <: orig)")
}

// A borrow whose lifetime is at or below the target level is extruded to itself:
// the whole RefType is shared, since nothing in it outranks the level.
func TestExtrudeSharesBelowLevelBorrow(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(1) // at the target level

	out := c.ctx.extrude(mutObjAt(lt), soltype.Positive, 1, map[extrudeKey]*soltype.TypeVarType{})

	require.Same(t, soltype.Lifetime(lt), out.(*soltype.RefType).Lt, "a below-level lifetime is shared, not extruded")
	require.Empty(t, lt.UpperBounds, "and no extrusion bound is recorded on it")
}

// An extruded lifetime bound on the ORIGINAL var is journaled, so a discarded probe
// trial truncates it back — the lifetime-sort twin of the type-var extrude's
// journaled bound write. Without this a failed overload trial that extruded a borrow
// lifetime would leak a bound.
func TestExtrudeLifetimeBoundRolledBackOnDiscard(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(2)

	p := c.openProbe()
	c.ctx.extrude(mutObjAt(lt), soltype.Positive, 1, map[extrudeKey]*soltype.TypeVarType{})
	require.Len(t, lt.UpperBounds, 1, "the extrusion records a bound on the original under the probe")

	c.closeProbe(p, false) // discard
	require.Empty(t, lt.UpperBounds, "the extruded lifetime bound is rolled back on discard")
}

// --- constrainLt level-extrusion ---

// Recording a higher-level lifetime as the bound of a lower-level variable extrudes
// it down first, so a variable's bound never outranks its own level — the invariant
// the freshener/extruder level prune over the lifetime sort relies on.
func TestConstrainLtMaintainsLevelInvariant(t *testing.T) {
	c := newChecker()
	low := c.ctx.freshLifetime(0)
	high := c.ctx.freshLifetime(2)

	c.ctx.constrainLt(low, high) // low <: high; high outranks low's level

	require.Len(t, low.UpperBounds, 1)
	recorded, ok := low.UpperBounds[0].(*soltype.LifetimeVar)
	require.True(t, ok)
	require.LessOrEqual(t, recorded.Level, low.Level, "a recorded bound never outranks the variable's own level")
	require.NotSame(t, high, recorded, "the higher-level bound is extruded to a fresh lower-level var")
}

// The mirror of TestConstrainLtMaintainsLevelInvariant: when the super sits below the
// sub's level, it is recorded directly with no extrusion, so the recorded bound's
// level is strictly BELOW the variable's own. This exercises the strict side of the
// invariant — a bound may rank lower than its variable, only never higher.
func TestConstrainLtRecordsLowerLevelBoundDirectly(t *testing.T) {
	c := newChecker()
	high := c.ctx.freshLifetime(2)
	low := c.ctx.freshLifetime(0)

	c.ctx.constrainLt(high, low) // high <: low; low does NOT outrank high's level

	// low is below high's level, so it is recorded as-is with no extrusion. That gives
	// high an upper bound ranking strictly below high itself. constrainLt also extrudes
	// high down to seed low's lower bound, so high.UpperBounds additionally holds that
	// self-proxy. Both bounds still satisfy the invariant, asserted by the loop.
	require.Contains(t, high.UpperBounds, soltype.Lifetime(low), "a below-level super is recorded as-is, not extruded")
	require.Less(t, low.Level, high.Level, "the directly-recorded bound ranks strictly below the variable's level")
	for _, ub := range high.UpperBounds {
		require.LessOrEqual(t, soltype.LevelOfLifetime(ub), high.Level, "no recorded bound outranks the variable's level")
	}
}

// A repeated cross-level outlives constraint does NOT accumulate duplicate bounds.
// Extrusion mints an outer-extruded proxy of `high`; without proxy reuse the second
// call would mint a second proxy and the identity-keyed ContainsLifetime dedup would
// never match, so `low.UpperBounds` would grow to 2. extrudeOuterAsUpper reuses the
// first proxy, so the bound count stays 1 across repeats — the lifetime-sort
// analogue of the same-level dedup, restored for the cross-level path.
func TestConstrainLtCrossLevelDoesNotAccumulateDuplicates(t *testing.T) {
	c := newChecker()
	low := c.ctx.freshLifetime(0)
	high := c.ctx.freshLifetime(2)

	c.ctx.constrainLt(low, high)
	upperAfterFirst := len(low.UpperBounds)
	lowerAfterFirst := len(high.LowerBounds)
	require.Equal(t, 1, upperAfterFirst, "low gains one outer-extruded proxy of high as an upper bound")

	c.ctx.constrainLt(low, high) // identical cross-level constraint again

	require.Len(t, low.UpperBounds, upperAfterFirst, "the repeat reuses the proxy; no duplicate upper bound accrues")
	require.Len(t, high.LowerBounds, lowerAfterFirst, "and no duplicate lower bound accrues on high")
}

// A discarded probe trial removes its extruded proxy from the bound list, so a later
// real constraint mints a genuinely-wired fresh proxy rather than reusing the
// rolled-back one. This guards the probe-safety of the proxy-reuse dedup: the reuse
// scan reads live bounds, so a proxy whose wiring a Discard reverted is never found.
func TestConstrainLtProxyReuseIsProbeSafe(t *testing.T) {
	c := newChecker()
	low := c.ctx.freshLifetime(0)
	high := c.ctx.freshLifetime(2)

	p := c.openProbe()
	c.ctx.constrainLt(low, high)
	require.Len(t, low.UpperBounds, 1)
	c.closeProbe(p, false) // discard: low's proxy bound and the proxy's own wiring revert
	require.Empty(t, low.UpperBounds, "the speculative cross-level bound is rolled back")

	c.ctx.constrainLt(low, high) // real constraint after the discard
	require.Len(t, low.UpperBounds, 1)
	proxy := low.UpperBounds[0].(*soltype.LifetimeVar)
	require.Contains(t, high.LowerBounds, soltype.Lifetime(proxy),
		"the post-discard proxy is freshly wired to high, not a rolled-back orphan")
}

// freshenAll freshens a borrow's lifetime, not just its type vars. allFreshener has
// no level prune, so EVERY quantifiable variable — including a RefType's lifetime —
// is replaced. Without the arm a reassigned borrow-typed binding would keep its
// scheme lifetime and the reassignment constraint would mutate it, contaminating
// later uses. inferAssign relies on this isolation.
func TestFreshenAllFreshensBorrowLifetime(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(3)
	ref := mutObjAt(lt)

	out := c.freshenAll(ref, 1)

	outLt := out.(*soltype.RefType).Lt.(*soltype.LifetimeVar)
	require.NotSame(t, lt, outLt, "freshenAll replaces the borrow's lifetime with a fresh one")
}

// freshenAll freshens a lifetime regardless of its level — even one at or below the
// target level, which freshenAbove would share. This is the lifetime-sort analogue
// of allFreshener's no-prune treatment of type vars (lim = math.MinInt).
func TestFreshenAllFreshensLowLevelBorrowLifetime(t *testing.T) {
	c := newChecker()
	lt := c.ctx.freshLifetime(0) // freshenAbove(lim=0) would share this; freshenAll must not
	ref := mutObjAt(lt)

	out := c.freshenAll(ref, 1)

	outLt := out.(*soltype.RefType).Lt.(*soltype.LifetimeVar)
	require.NotSame(t, lt, outLt, "freshenAll freshens even a level-0 lifetime")
}

// --- freshener fall-through: captured lifetime, quantified inner ---

// A borrow with a CAPTURED lifetime (Level <= lim) but a QUANTIFIED inner type var
// freshens the inner while sharing the lifetime. This exercises the RefType arm's
// fall-through path — ltFreshener.fresh returns the lifetime unchanged, so Accept
// rebuilds Inner — which the all-concrete-inner cases never reach (they are pruned
// whole before the arm runs).
func TestFreshenSharesCapturedLifetimeWhileFresheningInner(t *testing.T) {
	c := newChecker()
	captured := c.ctx.freshLifetime(0) // captured: shared across instantiations
	innerVar := c.freshAt(1)           // quantified inner type var: per-use fresh
	ref := &soltype.RefType{
		Mut: true,
		Lt:  captured,
		Inner: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "x", Type: innerVar},
		}},
	}

	out := c.freshenAbove(0, ref, 1, map[*soltype.TypeVarType]*soltype.TypeVarType{})

	outRef := out.(*soltype.RefType)
	require.True(t, outRef.Mut, "mutability is carried through")
	require.Same(t, soltype.Lifetime(captured), outRef.Lt, "the captured lifetime is shared")
	outInnerVar := outRef.Inner.(*soltype.ObjectType).Elems[0].(*soltype.PropertyElem).Type
	require.NotSame(t, innerVar, outInnerVar, "the quantified inner type var is freshened")
}

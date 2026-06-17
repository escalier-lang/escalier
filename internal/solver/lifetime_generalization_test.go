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

// A freshened lifetime carries copies of the original's outlives bounds, and those
// fresh-var bound writes are the sanctioned non-journaled append: a probe discard
// rolls them back for free because the fresh var is unreachable afterward, so the
// probe journals nothing for it and the original's bounds are untouched.
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
// instantiation, but the scheme body keeps its original param lifetime, so its
// display is unchanged.
func TestInferIdentityRefReturnStillRendersAfterGeneralization(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: mut {x: number}) { return p }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut 'l0 {x: number}) -> mut 'l0 {x: number}", values["f"])
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

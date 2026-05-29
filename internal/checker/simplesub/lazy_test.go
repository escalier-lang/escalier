package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// num/str primitives for lazy types.
func lnum() *LazyPrim { return &LazyPrim{Name: "number"} }
func lstr() *LazyPrim { return &LazyPrim{Name: "string"} }

// defineList registers `type List<T> = {head: T, tail: List<T> | Null}` on a.
func defineList(a *LazyAliases, name string) {
	a.Define(name, []string{"T"}, &LazyObj{Fields: map[string]LazyType{
		"head": LazyVar("T"),
		"tail": &LazyUnion{Members: []LazyType{a.Ref(name, LazyVar("T")), &LazyNull{}}},
	}})
}

// TestLazy_RegularSelfSubtypeNoBudget: List<number> <: List<number> is decided
// by the coinductive seen-set, terminating WITHOUT hitting the depth budget —
// the regular-recursion payoff (no budget, no CheckRegular needed).
func TestLazy_RegularSelfSubtypeNoBudget(t *testing.T) {
	a := NewLazyAliases()
	defineList(a, "List")

	ok, budgetHit := a.Subtypes(a.Ref("List", lnum()), a.Ref("List", lnum()))
	require.True(t, ok, "List<number> should be a subtype of itself")
	require.False(t, budgetHit, "regular recursion must close via the seen-set, not the budget")
}

// TestLazy_RegularCrossSubtypeNoBudget: two structurally-identical regular
// recursive aliases (List and Stream, same shape) are mutual subtypes, decided
// coinductively with no budget hit.
func TestLazy_RegularCrossSubtypeNoBudget(t *testing.T) {
	a := NewLazyAliases()
	defineList(a, "List")
	defineList(a, "Stream") // identical body

	ok1, hit1 := a.Subtypes(a.Ref("List", lnum()), a.Ref("Stream", lnum()))
	ok2, hit2 := a.Subtypes(a.Ref("Stream", lnum()), a.Ref("List", lnum()))
	require.True(t, ok1)
	require.True(t, ok2)
	require.False(t, hit1)
	require.False(t, hit2)
}

// TestLazy_RegularNegativeTerminates: List<number> </: List<string> — the field
// mismatch (number </: string) is found, and the check still terminates via the
// seen-set without the budget.
func TestLazy_RegularNegativeTerminates(t *testing.T) {
	a := NewLazyAliases()
	defineList(a, "List")

	ok, budgetHit := a.Subtypes(a.Ref("List", lnum()), a.Ref("List", lstr()))
	require.False(t, ok, "List<number> should not be a subtype of List<string>")
	require.False(t, budgetHit, "the mismatch terminates structurally, not via the budget")
}

// TestLazy_NonRegularNeedsBudget is the relocated-limit case: Grow<number>, a
// non-regular recursive alias, unfolds to infinitely many distinct
// instantiations, so the coinductive seen-set NEVER closes the loop — only the
// depth budget terminates the query. This is the precise sense in which laziness
// relocates the decidability limit rather than removing it.
//
//	type Grow<T> = Grow<Array<T>>
func TestLazy_NonRegularNeedsBudget(t *testing.T) {
	a := NewLazyAliases()
	a.Define("Grow", []string{"T"}, a.Ref("Grow", &LazyCtor{Name: "Array", Args: []LazyType{LazyVar("T")}}))

	_, budgetHit := a.Subtypes(a.Ref("Grow", lnum()), a.Ref("Grow", lnum()))
	require.True(t, budgetHit,
		"non-regular recursion cannot close via the seen-set; only the budget terminates it")
}

// TestLazy_ForcedOnDemand: a lazy alias is only expanded when the subtype check
// needs to see through it. Comparing List<number> against a plain object that
// matches its first unfolding succeeds — the ref is forced exactly as far as the
// comparison demands, then the recursive tail closes coinductively.
func TestLazy_ForcedOnDemand(t *testing.T) {
	a := NewLazyAliases()
	defineList(a, "List")

	// {head: number, tail: List<number> | Null} <: List<number>
	oneLevel := &LazyObj{Fields: map[string]LazyType{
		"head": lnum(),
		"tail": &LazyUnion{Members: []LazyType{a.Ref("List", lnum()), &LazyNull{}}},
	}}
	ok, budgetHit := a.Subtypes(oneLevel, a.Ref("List", lnum()))
	require.True(t, ok)
	require.False(t, budgetHit)
}

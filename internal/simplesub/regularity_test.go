package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func tindex(target TyExpr, key string) *TyIndex {
	return &TyIndex{Target: target, Index: tstr(key)}
}

// TestRegular_ListAccepted: a parameter passed through a recursive call
// unchanged is regular (finitely many instantiation states), so it is accepted.
//
//	type List<T> = {head: T, tail: List<T> | Null}
func TestRegular_ListAccepted(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("List", []string{"T"},
		trec("head", tref("T"), "tail", tunion(tref("List", tref("T")), tref("Null"))))
	require.Empty(t, e.CheckRegular())
}

// TestRegular_GrowRejected: a parameter wrapped in a constructor each lap grows
// without bound, so the alias is rejected at definition time with a precise
// diagnostic (rather than only tripping the runtime budget).
//
//	type Grow<T> = Grow<Array<T>>
func TestRegular_GrowRejected(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Grow", []string{"T"}, tref("Grow", tarray(tref("T"))))

	errs := e.CheckRegular()
	require.Len(t, errs, 1)
	require.Equal(t,
		"type alias \"Grow\" is not regular: parameter \"T\" grows under a type "+
			"constructor in a recursive call, so its expansion is unbounded; introduce "+
			"a nominal type to break the recursion or remove the growing wrapper",
		errs[0].Error())
}

// TestRegular_LastAccepted: a recursive conditional that recurses on an `infer`
// binding (not the formal parameter) is regular — the parameter does not appear
// in the recursive call argument at all.
//
//	type Last<T> = if T : Array<infer U> { Last<U> } else { T }
func TestRegular_LastAccepted(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Last", []string{"T"},
		tcond(tref("T"), tarray(&TyInfer{Name: "U"}), tref("Last", tref("U")), tref("T")))
	require.Empty(t, e.CheckRegular())
}

// TestRegular_JsonAccepted: a parameterless recursive alias (TS's Json) has no
// parameter to grow, so it is regular.
//
//	type Json = string | number | boolean | Null | Array<Json> | {data: Json}
func TestRegular_JsonAccepted(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Json", nil, tunion(
		tprim("string"), tprim("number"), tprim("boolean"), tref("Null"),
		tarray(tref("Json")), trec("data", tref("Json")),
	))
	require.Empty(t, e.CheckRegular())
}

// TestRegular_DeepPartialAccepted: recursing on a structurally-smaller component
// (an indexed access into the parameter) is regular — the parameter is not
// enlarged. Models TS's DeepPartial<T> recursing on T[P].
//
//	type DeepPartial<T> = {value: DeepPartial<T["v"]>}
func TestRegular_DeepPartialAccepted(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("DeepPartial", []string{"T"},
		trec("value", tref("DeepPartial", tindex(tref("T"), "v"))))
	require.Empty(t, e.CheckRegular())
}

// TestRegular_NonRecursiveAccepted: a non-recursive alias is trivially regular.
func TestRegular_NonRecursiveAccepted(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Pair", []string{"A", "B"}, trec("first", tref("A"), "second", tref("B")))
	require.Empty(t, e.CheckRegular())
}

// TestRegular_MutualExpandingRejected: expanding recursion spread across a
// mutual-recursion cycle (A -> B -> A, growing each lap) is still caught, via
// the SCC-based "recursive call" detection.
//
//	type Ping<T> = Pong<Array<T>>
//	type Pong<T> = Ping<T>
func TestRegular_MutualExpandingRejected(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Ping", []string{"T"}, tref("Pong", tarray(tref("T"))))
	e.Define("Pong", []string{"T"}, tref("Ping", tref("T")))

	errs := e.CheckRegular()
	require.Len(t, errs, 1)
	require.EqualError(t, errs[0],
		"type alias \"Ping\" is not regular: parameter \"T\" grows under a type "+
			"constructor in a recursive call, so its expansion is unbounded; introduce "+
			"a nominal type to break the recursion or remove the growing wrapper")
}

// TestRegular_GrowTerminatesViaBudgetWhenUnchecked confirms the static check and
// the runtime budget are complementary: even without CheckRegular, evaluating
// Grow still terminates (budget), so the check is an early, precise diagnostic
// layered on top of a safe-by-default evaluator.
func TestRegular_GrowTerminatesViaBudgetWhenUnchecked(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Grow", []string{"T"}, tref("Grow", tarray(tref("T"))))
	// Not calling CheckRegular here — just confirming evaluation still halts.
	got := e.Render(tref("Grow", tprim("number")))
	require.Contains(t, got, "Grow<")
}

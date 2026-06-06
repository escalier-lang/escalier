package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// PR1 let-generalization, Category-A acceptance against real source. M2 froze
// every top-level binding to a coalesced monotype; PR1 generalizes at the SCC
// boundary so a polymorphic binding is instantiated fresh per use.
//
// Where a render is compact already in PR1 (the type parameter is literally the
// same variable across positions, e.g. identity) the test pins the rendered
// signature, exercising the printer's <T0, …> prefix. Where the compact form
// needs simplification (PR2's co-occurrence merging), the test asserts the
// two-types-of-use BEHAVIOR instead — per the plan, "assert two-types-of-use
// behavior rather than the printed signature where simplification is
// load-bearing."

// A top-level identity generalizes to fn <T0>(x: T0) -> T0. The two-types-of-use
// behavior is the real proof of polymorphism: a monomorphic id would constrain
// its one param var with BOTH 5 and "hi", rendering a: 5 | "hi"; generalization
// instantiates id fresh per call, so a: 5 and b: "hi" stay distinct.
func TestInferModuleTopLevelLetPolymorphism(t *testing.T) {
	t.Run("render", func(t *testing.T) {
		// Identity's param and return are the same variable, so its render is
		// compact even before PR2 — this pins the <T0> quantifier prefix.
		values, _, errs := inferSource(t, `fn id(x) { x }`)
		require.Empty(t, errs)
		require.Equal(t, map[string]string{"id": "fn <T0>(x: T0) -> T0"}, values)
	})

	t.Run("two types of use", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			val id = fn (x) { x }
			val a = id(5)
			val b = id("hi")
		`)
		require.Empty(t, errs)
		require.Equal(t, map[string]string{
			"id": "fn <T0>(x: T0) -> T0",
			"a":  "5", // not 5 | "hi" — id is instantiated fresh per call
			"b":  `"hi"`,
		}, values)
	})
}

// Applying a polymorphic identity at two different argument types in one
// expression yields each argument's own type (not their union), so the tuple is
// ["hello", 5]. This is the headline Category-A render — compact in PR1 because
// every remaining variable coalesces to a concrete literal.
func TestInferModuleIdentityPolymorphism(t *testing.T) {
	values, _, errs := inferSource(t, `
		val identity = fn (x) { x }
		val pair = fn () { [identity("hello"), identity(5)] }
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{
		"identity": "fn <T0>(x: T0) -> T0",
		"pair":     `fn () -> ["hello", 5]`,
	}, values)
}

// A body-level inner function that captures an outer parameter keeps the capture
// through generalization (M2's eager body-level coalescing froze it to `never`).
// PR1's render is non-compact — `fn <T0, T1>(y: T0 & T1) -> [T0, T1]`, which PR2's
// co-occurrence merging collapses to `fn <T0>(y: T0) -> [T0, T0]` — so the
// contract here is BEHAVIOR: applying outer to 5 yields [5, 5], proving y reaches
// both result positions through the captured inner function.
func TestInferModuleInnerCapturesOuterParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		val outer = fn (y) {
			val getY = fn () { y }
			[getY(), getY()]
		}
		val r = outer(5)
	`)
	require.Empty(t, errs)
	require.Equal(t, "[5, 5]", values["r"], "the captured outer param reaches both result positions")
	// outer is generalized (not frozen): its signature carries a quantifier prefix.
	require.Contains(t, values["outer"], "fn <", "outer should generalize over its captured param")
}

// Let-polymorphism extends to body-level `val`s: an inner polymorphic function
// used at two types within the same body resolves each use independently. A
// monomorphic body-level binding (M2) would render [5 | "hi", 5 | "hi"].
func TestInferModuleBodyLevelLetPolymorphism(t *testing.T) {
	values, _, errs := inferSource(t, `
		val f = fn () {
			val id = fn (x) { x }
			[id(5), id("hi")]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{"f": `fn () -> [5, "hi"]`}, values)
}

// A polymorphic function with a parameter-only variable: the second param is
// never used, so its variable occurs only negatively and coalesces to `unknown`
// (single-polarity elimination), while the first param stays a type parameter.
// Per-call instantiation keeps k(1, "z") and k("s", true) at distinct types.
func TestInferModulePolymorphicWithParameterOnlyVar(t *testing.T) {
	values, _, errs := inferSource(t, `
		val k = fn (x, y) { x }
		val a = k(1, "z")
		val b = k("s", true)
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{
		"k": "fn <T0>(x: T0, y: unknown) -> T0",
		"a": "1",
		"b": `"s"`,
	}, values)
}

// A recursive group generalizes without looping (the coalesce seen-guard, shipped
// in M2 PR-5, keeps the cyclic var↔var bound graph total under generalization too).
// The ungrounded mutual recursion bottoms out: each param is unused (parameter-only
// ⇒ unknown) and each return is an ungrounded recursive position (⇒ never). The
// real contract is that inference TERMINATES rather than hangs.
func TestInferModuleRecursiveGroupGeneralizesWithoutLooping(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn ping(n) { pong(n) }
		fn pong(n) { ping(n) }
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{
		"ping": "fn (n: unknown) -> never",
		"pong": "fn (n: unknown) -> never",
	}, values)
}

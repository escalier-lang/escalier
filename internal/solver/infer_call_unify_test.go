package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// #1518. A call through a one-arm overload set must behave exactly like the same call
// through a plain callee of that signature. Each pair below writes the same signature twice:
// once alone, and once as the first arm of a set whose second arm has nothing to do with the
// call. Adding that arm must not change the answer.
//
// Three divergences have been found by inspection rather than by a test — the missing
// argument moves #1508 fixed, the missing owned-mutable upgrade #1519 fixed, and the missing
// rest expansion below. These cases pin the shared behavior so a fourth cannot pass silently.

// A tuple-typed rest parameter expands to one positional parameter per element, so each
// argument is checked against its own element. The overload path did not expand it: the arity
// gate read the tuple's length and passed, then the rest branch found no element type on a
// tuple slot and checked nothing at all.
func TestInferOverloadArmExpandsTupleRest(t *testing.T) {
	const arm2 = "declare fn f(a: boolean) -> string\n"
	t.Run("a matching call accepts either way", func(t *testing.T) {
		alone, _, errs := inferSource(t, "declare fn f(...xs: [number, string]) -> number\nval r = f(1, \"a\")")
		require.Empty(t, errs)
		set, _, errs := inferSource(t, "declare fn f(...xs: [number, string]) -> number\n"+arm2+"val r = f(1, \"a\")")
		require.Empty(t, errs)
		require.Equal(t, alone["r"], set["r"])
	})
	t.Run("a mismatched element is rejected either way", func(t *testing.T) {
		_, _, errs := inferSource(t, "declare fn f(...xs: [number, string]) -> number\nval r = f(1, 2)")
		require.Equal(t, []string{"2:14-2:15: cannot constrain 2 <: string"}, messagesWithSpan(t, errs))

		_, _, errs = inferSource(t, "declare fn f(...xs: [number, string]) -> number\n"+arm2+"val r = f(1, 2)")
		require.Equal(t, []string{
			"3:9-3:16: No matching overload for this call\n" +
				"  fn (...xs: [number, string]) -> number\n" +
				"  fn (a: boolean) -> string",
		}, messagesWithSpan(t, errs))
	})
	t.Run("a union rest distributes on an arm too", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn f(...v: [] | [number]) -> number
			declare fn f(a: boolean, b: boolean) -> string
			val r = f("z")
		`)
		require.Equal(t, []string{
			"4:12-4:18: No matching overload for this call\n" +
				"  fn (...v: [] | [number]) -> number\n" +
				"  fn (a: boolean, b: boolean) -> string",
		}, messagesWithSpan(t, errs))
	})
}

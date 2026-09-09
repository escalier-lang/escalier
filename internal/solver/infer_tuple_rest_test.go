package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// #1514. A rest parameter typed as a tuple is checked element by element, for three tuple
// shapes the expansion used to decline. A declined slot reached the fixed-position walk,
// which paired argument 0 with the whole slot, so the correct argument and the wrong one
// drew the same message and nothing about the element was checked.

// A rest slot typed as a UNION of tuples admits a call when some member admits it. `[] | [T]`
// is how TypeScript spells an optional argument in a rest position, and it is what the
// committed tree writes for the iteration protocol on Iterator, AsyncIterator, Generator and
// AsyncGenerator.
func TestInferUnionTupleRest(t *testing.T) {
	const cls = `
		declare class It<T> {
			next(self, ...value: [] | [T]) -> number,
		}
	`
	call := func(body string) string {
		return cls + "fn g(i: It<string>) -> number { return " + body + " }"
	}
	t.Run("the empty member accepts a call with no argument", func(t *testing.T) {
		_, _, errs := inferSource(t, call("i.next()"))
		require.Empty(t, errs)
	})
	t.Run("the one-element member accepts a matching argument", func(t *testing.T) {
		_, _, errs := inferSource(t, call(`i.next("a")`))
		require.Empty(t, errs)
	})
	t.Run("an argument of the wrong element type is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, call("i.next(1)"))
		require.Equal(t, []string{
			"5:41-5:50: No matching overload for this call\n" +
				"  fn () -> number\n" +
				"  fn (value: string) -> number",
		}, messagesWithSpan(t, errs))
	})
	t.Run("more arguments than any member takes is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, call(`i.next("a", "b")`))
		require.Equal(t, []string{
			"5:41-5:57: No matching overload for this call\n" +
				"  fn () -> number\n" +
				"  fn (value: string) -> number",
		}, messagesWithSpan(t, errs))
	})
	t.Run("a plain function distributes the same way", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare fn f(...v: [] | [number]) -> string
			val a = f()
			val b = f(1)
		`)
		require.Empty(t, errs)
		require.Equal(t, "string", values["a"])
		require.Equal(t, "string", values["b"])
	})
}

// A rest slot typed as a tuple carrying `...P` SPREADS grounds before it expands, so each
// argument is checked against the element the spreads place at that position rather than
// against a spread. `Function.bind` writes `...args: [...A, ...B]` twice in the committed tree.
func TestInferSpreadTupleRest(t *testing.T) {
	const decl = `
		type A = [number]
		type B = [string]
		declare fn f(...args: [...A, ...B]) -> number
	`
	t.Run("arguments matching the spliced elements accept", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`val r = f(1, "a")`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["r"])
	})
	t.Run("an argument is blamed against its own element", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f(1, 2)`)
		require.Equal(t, []string{"5:15-5:16: cannot constrain 2 <: string"},
			messagesWithSpan(t, errs))
	})
	t.Run("arity counts the spliced elements", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f(1)`)
		require.Equal(t,
			[]string{"5:10-5:14: Not enough arguments: expected at least 2, but got 1"},
			messagesWithSpan(t, errs))
	})
	t.Run("a spread that never grounds leaves the slot arity-only", func(t *testing.T) {
		// `...T` over a type parameter names no positions to splice, so the slot stays a
		// residual: the call is checked for arity and its arguments are left alone. Checking
		// them against the slot would ask for `1 <: [...T, string]`, which is neither true nor
		// what the slot means.
		_, _, errs := inferSource(t, `
			declare fn f<T>(...args: [...T, string]) -> number
			val r = f(1, "a")
		`)
		require.Empty(t, errs)
	})
}

// A rest slot typed as an INEXACT tuple expands its fixed prefix into positions and leaves the
// tail unbounded. `[A, ...]` means "at least an A, then any number more".
func TestInferInexactTupleRest(t *testing.T) {
	const decl = "declare fn f(...xs: [number, ...]) -> number\n"
	t.Run("the prefix alone accepts", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f(1)`)
		require.Empty(t, errs)
	})
	t.Run("any tail is admitted", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f(1, "a", true)`)
		require.Empty(t, errs)
	})
	t.Run("the prefix is checked against its element", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f("z")`)
		require.Equal(t, []string{"2:11-2:14: cannot constrain \"z\" <: number"},
			messagesWithSpan(t, errs))
	})
	t.Run("the prefix is still required", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`val r = f()`)
		require.Equal(t,
			[]string{"2:9-2:12: Not enough arguments: expected at least 1, but got 0"},
			messagesWithSpan(t, errs))
	})
}

// The exact-tuple rest that already worked keeps working, and an `Array<E>` rest still
// scatters over the arguments it absorbs. Both share expandTupleRest and the scatter rule with
// the shapes above.
func TestInferGroundTupleRestUnchanged(t *testing.T) {
	t.Run("an exact tuple checks each element", func(t *testing.T) {
		_, _, errs := inferSource(t, "declare fn f(...xs: [number, string]) -> number\nval r = f(1, 2)")
		require.Equal(t, []string{"2:14-2:15: cannot constrain 2 <: string"},
			messagesWithSpan(t, errs))
	})
	t.Run("an Array rest scatters over its element", func(t *testing.T) {
		_, _, errs := inferSource(t, "declare fn f(...xs: Array<number>) -> number\nval r = f(1, \"a\")")
		require.Equal(t, []string{"2:14-2:17: cannot constrain \"a\" <: number"},
			messagesWithSpan(t, errs))
	})
}

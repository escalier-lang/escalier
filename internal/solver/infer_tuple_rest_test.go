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
		// them against the slot would ask for `1 <: [...T, Array<number>]`, which is neither
		// true nor what the slot means.
		//
		// The argument at the trailing `string` position is deliberately a NUMBER. Passing a
		// string there would pass whether or not that position is being enforced, so it could
		// not tell arity-only apart from a slot that checks its fixed suffix.
		_, _, errs := inferSource(t, ungroundableRestDecl+"val r = f(1, 2)")
		require.Empty(t, errs)
	})
	t.Run("an unresolved spread stands for any number of positions", func(t *testing.T) {
		// The floor counts the FIXED elements alone and there is no ceiling, since `...T`
		// stands for an unknown number of positions: `[...T, string]` binds one argument when
		// T is empty and any larger number when it is not. Counting the spread as one element
		// would put the range at [2, 2] and reject both ends.
		for _, call := range []string{`f("a")`, "f(1, 2)", `f(1, "a", true)`} {
			_, _, errs := inferSource(t, ungroundableRestDecl+"val r = "+call)
			require.Empty(t, errs, "%s must be accepted on arity", call)
		}
		_, _, errs := inferSource(t, ungroundableRestDecl+"val r = f()")
		require.Equal(t,
			[]string{"2:9-2:12: Not enough arguments: expected at least 1, but got 0"},
			messagesWithSpan(t, errs), "the fixed element is still required")
	})
}

// ungroundableRestDecl is a rest slot whose spread operand can never ground: `T` is a type
// PARAMETER, so it names no element list to splice however it is instantiated.
//
// The `Array<number>` bound is what makes the spread well-formed rather than merely
// ungroundable. A spread splices a positional list, so its operand has to name one — a tuple,
// an array, or a parameter constrained to either. An unconstrained `T` does not, and the
// committed tree writes the constrained form throughout, as `Function.bind` does with
// `bind<A: mut Array<any>, B: mut Array<any>, R>(this: fn (...args: [...A, ...B]) -> R, …)`.
// Nothing enforces that today, which is #1533; writing it correctly here keeps these cases
// resting on a program that issue would still accept.
const ungroundableRestDecl = "declare fn f<T: Array<number>>(...args: [...T, string]) -> number\n"

// A rest slot typed as an INEXACT tuple expands its fixed prefix into positions and marks the
// function inexact, which is the representation `fn (x: A, ...)` already has. The two spellings
// say the same thing, so every case below runs against both and requires the same answer.
//
// `[A, ...]` and a trailing `...` on the parameter list both mean "at least an A, then any
// number more the signature says nothing about". The `...` is a width marker for SUBTYPING: it
// widens the accept-set to [required, ∞), which is what a callee filling the slot must match. It
// is not permission for a caller who can see the signature to pass arguments the callee ignores,
// so the too-many-arguments lint fires for both — #677 §4.2.3 rejects extras "for exact and
// inexact callees alike".
func TestInferInexactTupleRest(t *testing.T) {
	const tupleRest = "declare fn f(...xs: [number, ...]) -> number\n"
	const inexactFn = "declare fn f(x: number, ...) -> number\n"
	tests := []struct {
		name string
		call string
		want []string
	}{
		{
			name: "the prefix alone accepts",
			call: "f(1)",
		},
		{
			name: "the prefix is checked against its element",
			call: `f("z")`,
			want: []string{`cannot constrain "z" <: number`},
		},
		{
			name: "the prefix is still required",
			call: "f()",
			want: []string{"Not enough arguments: expected at least 1, but got 0"},
		},
		{
			name: "an extra argument is linted at a direct call",
			call: `f(1, "a")`,
			want: []string{"Too many arguments: expected at most 1, but got 2"},
		},
		{
			name: "so is a longer tail",
			call: `f(1, "a", true)`,
			want: []string{"Too many arguments: expected at most 1, but got 3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, viaTuple := inferSource(t, tupleRest+"val r = "+tt.call)
			_, _, viaMarker := inferSource(t, inexactFn+"val r = "+tt.call)
			// Compared without spans, since the two declarations differ in length and the
			// point here is that the two spellings agree on what they report. The spans for
			// these diagnostics are pinned by the ground-tuple cases above.
			require.Equal(t, tt.want, Messages(viaTuple), "through the inexact tuple rest")
			require.Equal(t, tt.want, Messages(viaMarker), "through the inexact function")
		})
	}
}

// The width the `...` does buy shows in SUBTYPING, and there the two spellings agree as well. An
// unbounded ceiling demands a callee that itself accepts unboundedly many, so a fixed-arity
// function cannot fill either slot — where the EXACT tuple rest `[number]` takes the one-argument
// function, since its ceiling is 1.
func TestInferInexactTupleRestSubtyping(t *testing.T) {
	const fill = " = fn (a: number) { return 1 }"
	t.Run("an exact tuple rest takes a matching fixed-arity function", func(t *testing.T) {
		_, _, errs := inferSource(t, "val s: fn(...xs: [number]) -> number"+fill)
		require.Empty(t, errs)
	})
	t.Run("neither inexact spelling does", func(t *testing.T) {
		const want = "cannot constrain function of arity 1 <: function of arity 1 or more"
		_, _, viaTuple := inferSource(t, "val s: fn(...xs: [number, ...]) -> number"+fill)
		_, _, viaMarker := inferSource(t, "val s: fn(x: number, ...) -> number"+fill)
		require.Equal(t, []string{want}, Messages(viaTuple), "through the inexact tuple rest")
		require.Equal(t, []string{want}, Messages(viaMarker), "through the inexact function")
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

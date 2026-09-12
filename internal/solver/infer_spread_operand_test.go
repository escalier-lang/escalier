package solver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A `...P` element of a tuple ANNOTATION splices a positional list, so its operand
// has to name one. The tuple-literal form already reported through
// SpreadNotTupleError; these are the shapes the annotation form used to accept in
// silence.
func TestInferTupleAnnotationSpreadRejectsANonList(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a primitive",
			src:  "type Bad = [...number, string]",
			want: "1:13-1:22: cannot spread number into a tuple",
		},
		{
			name: "a literal",
			src:  "type Also = [...true]",
			want: "1:14-1:21: cannot spread true into a tuple",
		},
		{
			name: "an object",
			src:  "type Obj = [...{x: number}]",
			want: "1:13-1:27: cannot spread object into a tuple",
		},
		{
			name: "an unconstrained type parameter",
			src:  "declare fn f<T>(...args: [...T, string]) -> number",
			want: "1:27-1:31: cannot spread T into a tuple",
		},
		{
			name: "a type parameter bounded by a non-list",
			src:  "declare fn g<T: number>(...args: [...T, string]) -> number",
			want: "1:35-1:39: cannot spread T into a tuple",
		},
		{
			// `unknown` says no more about T than no bound at all, so the two report alike.
			// A bound written `any` resolves to `unknown` and lands on this row.
			name: "a type parameter bounded by unknown",
			src:  "declare fn g<T: unknown>(...args: [...T, string]) -> number",
			want: "1:36-1:40: cannot spread T into a tuple",
		},
		{
			name: "an alias naming a primitive",
			src: `type N = number
type Bad = [...N, string]`,
			want: "2:13-2:17: cannot spread N into a tuple",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, []string{tt.want}, messagesWithSpan(t, errs))
		})
	}
}

// The operands a spread can splice. A tuple and an `Array<E>` are the two ground
// forms; a type parameter bounded by either is how the stdlib tree writes one, and
// an alias is followed to its body.
func TestInferTupleAnnotationSpreadAcceptsAList(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "a tuple", src: "type Ok = [...[number, string], boolean]"},
		{name: "an array", src: "type Arr = [...Array<number>, string]"},
		{
			name: "an alias naming a tuple",
			src: `type Pair = [number, string]
type Ok = [...Pair, boolean]`,
		},
		{
			name: "a type parameter bounded by an array",
			src:  "declare fn k<T: Array<number>>(...args: [...T, string]) -> number",
		},
		{
			name: "a type parameter bounded by a tuple",
			src:  "declare fn k<T: [number, string]>(x: [...T, boolean]) -> number",
		},
		{
			// Two of these spread side by side is the shape `Function.bind` is declared
			// with, and its `[...A, ...B]` is the only tuple-annotation spread the
			// committed tree writes. The tree spells the element `any`, which resolves to
			// the `unknown` written here.
			name: "a type parameter bounded by an owned-mutable array",
			src:  "declare fn h<A: mut Array<unknown>, B: mut Array<unknown>>(x: [...A, ...B]) -> number",
		},
		{
			// A union of tuples is how an optional argument is spelled in a rest
			// position, and the rest rules distribute it into per-member candidates.
			name: "a union of tuples",
			src:  "declare fn u<T: [] | [number]>(x: [...T, boolean]) -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
		})
	}
}

// An iterable is rejected alongside the types with no positions at all, which #1552
// covers. A class declaring `[Symbol.iterator]` is the same unknown-length sequence an
// `Array` is, so the line is drawn at one class rather than at the property that
// matters. Nothing downstream reads such a spread yet, so the rejection costs no legal
// program today. Re-point these cases at acceptance when #1552 lands.
func TestInferTupleAnnotationSpreadRejectsAnIterable(t *testing.T) {
	// Declared rather than defined, so the cases turn on the shape alone. A body
	// returning `self` reports `cannot constrain object <: class Seq` today, which
	// #1564 covers and which has nothing to do with the spread rule under test.
	const seq = `
		declare class Seq {
			next(self) -> number,
			[Symbol.iterator](self) -> Seq,
		}
	`
	t.Run("the class itself", func(t *testing.T) {
		_, _, errs := inferSource(t, seq+`
		type Use = [...Seq, string]
	`)
		require.Equal(t,
			[]string{"7:15-7:21: cannot spread Seq into a tuple"},
			messagesWithSpan(t, errs))
	})
	t.Run("a type parameter bounded by it", func(t *testing.T) {
		_, _, errs := inferSource(t, seq+`
		declare fn f<T: Seq>(x: [...T, string]) -> number
	`)
		require.Equal(t,
			[]string{"7:28-7:32: cannot spread T into a tuple"},
			messagesWithSpan(t, errs))
	})
}

// An operand the check cannot decide is accepted, since it may still reduce to a
// tuple and rejecting it would turn a legal program into a diagnostic.
func TestInferTupleAnnotationSpreadAcceptsAnUndecidableOperand(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a conditional",
			src: `type Pick<T> = if T : number { [number] } else { [string] }
declare fn f<T: number>(x: [...Pick<T>, boolean]) -> number`,
		},
		{
			name: "an indexed access",
			src: `type Holder = {items: [number, string]}
type Ok = [...Holder["items"], boolean]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
		})
	}
}

// An alias declared later in the module is still followed, since dep_graph orders a
// component by dependency and fills the body Uses names before Uses resolves.
func TestInferTupleAnnotationSpreadFollowsAForwardAlias(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Uses = [...Pair, boolean]
		type Pair = [number, string]
	`)
	require.Empty(t, messagesWithSpan(t, errs))
}

// The check reads a type parameter's DECLARED bound. Deciding it after inference
// would read whatever constraint solving had since recorded on the parameter's var,
// so an unrelated line in the body could silence a diagnostic about the signature.
func TestInferTupleAnnotationSpreadIgnoresABoundFromTheBody(t *testing.T) {
	const want = "2:15-2:19: cannot spread T into a tuple"
	t.Run("a body that says nothing about T", func(t *testing.T) {
		_, _, errs := inferSource(t, `
		fn f<T>(x: [...T, string]) -> number { return 1 }`)
		require.Equal(t, []string{want}, messagesWithSpan(t, errs))
	})
	t.Run("a body that constrains T to an array", func(t *testing.T) {
		_, _, errs := inferSource(t, `
		fn f<T>(x: [...T, string]) -> number {
			val y: Array<number> = x
			return 1
		}`)
		require.Contains(t, messagesWithSpan(t, errs), want)
	})
}

// Resolving another annotation in the module must not swallow the diagnostic. The
// run loads `std:prelude` for its `Array`, and that nested walk runs the same
// inference driver with the outer module's diagnostics swapped out.
func TestInferTupleAnnotationSpreadSurvivesAPackageLoad(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Bad = [...number, string]
		declare fn f(x: Array<number>) -> number
	`)
	require.Equal(t,
		[]string{"2:15-2:24: cannot spread number into a tuple"},
		messagesWithSpan(t, errs))
}

// The walk follows an alias chain to its end however long it is. A depth budget would
// have to accept at its cutoff, since a deep operand may still be a list, so a chain
// longer than the cutoff would pass whatever it ends in.
func TestInferTupleAnnotationSpreadFollowsALongAliasChain(t *testing.T) {
	// chain builds `type A1 = A2 … type A{n} = <last>` plus a spread of A1.
	chain := func(n int, last string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "type A%d = %s\n", n, last)
		for i := n - 1; i >= 1; i-- {
			fmt.Fprintf(&b, "type A%d = A%d\n", i, i+1)
		}
		b.WriteString("type Spread = [...A1]\n")
		return b.String()
	}
	t.Run("a chain ending in a non-list still reports", func(t *testing.T) {
		_, _, errs := inferSource(t, chain(40, "number"))
		require.Equal(t,
			[]string{"41:16-41:21: cannot spread A1 into a tuple"},
			messagesWithSpan(t, errs))
	})
	t.Run("a chain ending in a tuple is accepted", func(t *testing.T) {
		_, _, errs := inferSource(t, chain(40, "[number, string]"))
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// An alias that reaches itself never settles, so the walk stops there rather than
// following it forever. checkProductive is what reports such an alias.
func TestInferTupleAnnotationSpreadStopsAtARecursiveAlias(t *testing.T) {
	_, _, errs := inferSource(t, "type R = [...R]")
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message(), "recursive type alias `R` reaches itself")
}

// An overload arm's signature is resolved twice, by the pre-bind and again by the
// phase that checks its body, and a queue entry outlives the pre-bind's probe. One
// written mistake still draws one diagnostic.
func TestInferTupleAnnotationSpreadReportsOnce(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a single declaration",
			src:  "fn g(x: [...number]) -> number { return 1 }",
		},
		{
			name: "an overload set",
			src: `fn g(x: [...number]) -> number { return 1 }
fn g(a: string, b: string) -> string { return a }`,
		},
		{
			name: "a declared overload set",
			src: `declare fn g(x: [...number]) -> number
declare fn g(a: string, b: string) -> string`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, "cannot spread number into a tuple", errs[0].Message())
		})
	}
}

// An operand that failed to resolve already reported, so it draws no second
// diagnostic from this check.
func TestInferTupleAnnotationSpreadSkipsAnUnresolvedOperand(t *testing.T) {
	_, _, errs := inferSource(t, "type Bad = [...NoSuchType, string]")
	require.Equal(t,
		[]string{"1:16-1:26: cannot find type `NoSuchType`"},
		messagesWithSpan(t, errs))
}

// The annotation form and the value form report the same message for the same
// mistake, so one rule reads the same however it is written.
func TestInferTupleSpreadMessageMatchesTheValueForm(t *testing.T) {
	_, _, annErrs := inferSource(t, "type Bad = [...number, string]")
	require.Len(t, annErrs, 1)

	_, _, valErrs := inferSource(t, "fn f(n: number) { return [...n, 1] }")
	require.Len(t, valErrs, 1)

	require.Equal(t, "cannot spread number into a tuple", annErrs[0].Message())
	require.Equal(t, valErrs[0].Message(), annErrs[0].Message())
}

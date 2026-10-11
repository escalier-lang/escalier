package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVariadicTupleSubtyping covers checking a value against a tuple with a variadic tail
// and checking a variadic tuple against other targets. A row with wantErrs asserts those
// diagnostics, and one without asserts the declaration checks.
func TestVariadicTupleSubtyping(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "FixedTupleFillsThePrefixAndTheMiddle",
			src:  `val a: [number, ...Array<string>] = [1, "x", "y"]`,
		},
		{
			name: "FixedTupleOfJustThePrefix",
			src:  `val a: [number, ...Array<string>] = [1]`,
		},
		{
			name:     "EmptyTupleMissesThePrefix",
			src:      `val a: [number, ...Array<number>] = []`,
			wantErrs: []string{"1:37-1:39: cannot constrain tuple <: [number, ...Array<number>]"},
		},
		{
			name:     "MiddleValueOfTheWrongType",
			src:      `val a: [number, ...Array<string>] = [1, 2]`,
			wantErrs: []string{"1:41-1:42: cannot constrain 2 <: string"},
		},
		{
			name: "FixedTupleFillsThePrefixAndTheSuffix",
			src:  `val a: [number, ...Array<string>, boolean] = [1, true]`,
		},
		{
			name: "FixedTupleFillsAMiddleBetweenThem",
			src:  `val a: [number, ...Array<string>, boolean] = [1, "a", "b", true]`,
		},
		{
			name: "VariadicIntoAShorterPrefix",
			src: `
				declare val v: [number, string, ...Array<string>]
				val a: [number, ...Array<string>] = v
			`,
		},
		{
			name: "VariadicIntoALongerPrefix",
			src: `
				declare val v: [number, ...Array<string>]
				val a: [number, string, ...Array<string>] = v
			`,
			wantErrs: []string{"3:49-3:50: cannot constrain [number, ...Array<string>] <: [number, string, ...Array<string>]"},
		},
		{
			name: "VariadicIntoAnArray",
			src: `
				declare val v: [number, ...Array<number>]
				val a: Array<number> = v
			`,
		},
		{
			name: "ArrayIntoAllMiddle",
			src: `
				declare val xs: Array<number>
				val a: [...Array<number>] = xs
			`,
		},
		{
			name: "ArrayMissesAFixedElement",
			src: `
				declare val xs: Array<number>
				val a: [number, ...Array<number>] = xs
			`,
			wantErrs: []string{"3:41-3:43: cannot constrain Array<number> <: [number, ...Array<number>]"},
		},
		{
			name: "VariadicIntoAFixedTuple",
			src: `
				declare val v: [number, ...Array<number>]
				val a: [number] = v
			`,
			wantErrs: []string{"3:23-3:24: cannot constrain [number, ...Array<number>] <: tuple"},
		},
		{
			name: "ArrayIntoAnInexactAllMiddle",
			src: `
				declare val xs: Array<number>
				val a: [...Array<number>, ...] = xs
			`,
		},
		{
			name: "MutableFixedTupleIntoMutableVariadic",
			src: `
				declare val f: mut [number, number]
				val a: mut [number, ...Array<number>] = f
			`,
			wantErrs: []string{"3:45-3:46: cannot constrain tuple <: [number, ...Array<number>]"},
		},
		{
			name: "MutableVariadicIntoAShorterMutablePrefix",
			src: `
				declare val v: mut [number, ...Array<number>]
				val a: mut [...Array<number>] = v
			`,
			wantErrs: []string{"3:37-3:38: cannot constrain [number, ...Array<number>] <: [...Array<number>]"},
		},
		{
			name: "MutableVariadicIntoTheSameShape",
			src: `
				declare val v: mut [number, ...Array<number>]
				val a: mut [number, ...Array<number>] = v
			`,
		},
		{
			name: "VariadicIntoAnInexactTuple",
			src: `
				declare val v: [number, string, ...Array<boolean>]
				val a: [number, ...] = v
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
				return
			}
			require.Empty(t, errs)
		})
	}
}

// TestVariadicTupleInference covers the variadic tuple an array literal infers and how a
// variadic tuple is read back by iterating, spreading and destructuring it.
func TestVariadicTupleInference(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "FixedElementBeforeASpread",
			src:  `fn f(xs: Array<number>) { return [0, ...xs] }`,
			want: "fn (xs: Array<number>) -> [0, ...Array<number>]",
		},
		{
			name: "SpreadAlone",
			src:  `fn f(xs: Array<number>) { return [...xs] }`,
			want: "fn (xs: Array<number>) -> Array<number>",
		},
		{
			name: "FixedElementAfterASpread",
			src:  `fn f(xs: Array<number>) { return [...xs, "z"] }`,
			want: `fn (xs: Array<number>) -> [...Array<number>, "z"]`,
		},
		{
			name: "TwoSpreadsShareOneMiddle",
			src:  `fn f(xs: Array<number>, ys: Array<string>) { return [0, ...xs, 1, ...ys, true] }`,
			want: "fn (xs: Array<number>, ys: Array<string>) -> [0, ...Array<number | string>, true]",
		},
		{
			name: "SpreadAVariadicTuple",
			src:  `fn f(t: [number, ...Array<string>]) { return [...t, true] }`,
			want: "fn (t: [number, ...Array<string>]) -> [number, ...Array<string>, true]",
		},
		{
			name: "IterateAVariadicTuple",
			src: `fn f(t: [number, ...Array<string>]) {
				var r: number | string = 0
				for x in t { r = x }
				return r
			}`,
			want: "fn (t: [number, ...Array<string>]) -> number | string",
		},
		{
			name: "RestAfterThePrefix",
			src: `fn f(t: [number, ...Array<string>, boolean]) {
				val [a, ...rest] = t
				return [a, rest]
			}`,
			want: "fn (t: [number, ...Array<string>, boolean]) -> [number, [...Array<string>, boolean]]",
		},
		{
			name: "LengthReadsOffArray",
			src: `fn f(xs: Array<number>) {
				val ys = [0, ...xs]
				return ys.length
			}`,
			want: "fn (xs: Array<number>) -> number",
		},
		{
			name: "IndexInsideThePrefix",
			src: `fn f(xs: Array<number>) {
				val ys = ["a", ...xs]
				return ys[0]
			}`,
			want: `fn (xs: Array<number>) -> "a"`,
		},
		{
			name: "IndexPastThePrefix",
			src: `fn f(xs: Array<number>) {
				val ys = ["a", ...xs]
				return ys[1]
			}`,
			want: `fn (xs: Array<number>) -> number | "a"`,
		},
		{
			name: "MemberOfAUnionOfVariadicTuples",
			src: `fn f(xs: Array<number>, c: boolean) {
				val r = if c { [0, ...xs] } else { ["a", ...xs] }
				return r.length
			}`,
			want: "fn (xs: Array<number>, c: boolean) -> number",
		},
		{
			name: "SpreadAnInexactVariadicTupleLast",
			src:  `fn f(t: [number, ...Array<string>, ...]) { return [...t] }`,
			want: "fn (t: [number, ...Array<string>, ...]) -> [number, ...Array<string>, ...]",
		},
		{
			name: "SpreadAnInexactVariadicTupleBeforeAnIterable",
			src:  `fn f(t: [number, ...Array<string>, ...], xs: Array<boolean>) { return [...t, ...xs] }`,
			want: "fn (t: [number, ...Array<string>, ...], xs: Array<boolean>) -> [number, ...Array<unknown>]",
		},
		{
			name: "RestInAMatchArm",
			src:  `fn f(t: [number, ...Array<string>]) { return match t { [_, ...rest] => rest } }`,
			want: "fn (t: [number, ...Array<string>]) -> Array<string>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TestVariadicTupleInexactSpreadNotLast asserts that an inexact variadic tuple spread before
// another element is rejected, since its open tail leaves no position for what follows.
func TestVariadicTupleInexactSpreadNotLast(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(t: [number, ...Array<string>, ...]) { return [...t, 1] }`)
	require.Equal(t, []string{
		"1:52-1:56: cannot spread an inexact tuple except as the last element",
	}, messagesWithSpan(t, errs))
}

// TestVariadicTupleFixedPatternRejected asserts that a pattern of fixed length is rejected
// against a variadic tuple, since the tuple may hold fewer or more values than it names.
func TestVariadicTupleFixedPatternRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(t: [number, ...Array<string>]) {
			val [a, b] = t
			return b
		}
	`)
	require.Equal(t, []string{
		"3:8-3:14: cannot constrain [number, ...Array<string>] <: tuple",
	}, messagesWithSpan(t, errs))
}

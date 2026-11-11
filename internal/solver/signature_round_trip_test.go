package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// roundTripCase declares a function `f` twice. inferred writes it without annotations and
// annotated writes it with the signature sig that inference gives inferred. callers calls
// the annotated `f`, and values holds the type each named binding of callers takes.
type roundTripCase struct {
	name      string
	inferred  string
	annotated string
	sig       string
	callers   string
	values    map[string]string
}

var roundTripCases = []roundTripCase{
	{
		name:      "FieldRead",
		inferred:  `fn f(p) { return p.x }`,
		annotated: `fn f<T0, 'a>(p: &'a {x: T0}) -> &'a T0 { return p.x }`,
		sig:       `fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0`,
		callers: `
				val b = {x: 2}
				val n = f(&b)
				val c = {x: {y: 2}}
				val m = f(&c)
				fn sum() -> number {
					val d = {x: 1}
					val k = f(&d)
					return d.x + k
				}
			`,
		values: map[string]string{
			"n":   "2",
			"m":   "&{y: 2}",
			"sum": "fn () -> number",
		},
	},
	{
		name:      "WrittenBorrowOfField",
		inferred:  `fn f(p) { return &p.x }`,
		annotated: `fn f<T0, 'a>(p: &'a {x: T0}) -> &'a T0 { return &p.x }`,
		sig:       `fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0`,
		callers: `
				val c = {x: {y: 2}}
				val m = f(&c)
			`,
		values: map[string]string{"m": "&{y: 2}"},
	},
	{
		name:      "NestedFieldRead",
		inferred:  `fn f(p) { return p.x.y }`,
		annotated: `fn f<T0, 'a>(p: &'a {x: {y: T0}}) -> &'a T0 { return p.x.y }`,
		sig:       `fn <T0, 'a>(p: &'a {x: {y: T0}}) -> &'a T0`,
		callers: `
				val c = {x: {y: {z: 2}}}
				val m = f(&c)
			`,
		values: map[string]string{"m": "&{z: 2}"},
	},
	{
		// Inference rejects an index read off an unannotated parameter, so a tuple
		// element reaches a signature through a destructuring pattern instead.
		name: "TupleDestructure",
		inferred: `fn f(p) {
				val [a, b] = p
				return a
			}`,
		annotated: `fn f<T0, 'a>(p: &'a [T0, unknown]) -> &'a T0 {
				val [a, b] = p
				return a
			}`,
		sig:     `fn <T0, 'a>(p: &'a [T0, unknown]) -> &'a T0`,
		callers: `val m = f([1, "s"])`,
		values:  map[string]string{"m": "1"},
	},
	{
		name: "WrittenBorrowOfDestructuredElement",
		inferred: `fn f(p) {
				val [a, b] = p
				return &a
			}`,
		annotated: `fn f<T0, 'a>(p: &'a [T0, unknown]) -> &'a T0 {
				val [a, b] = p
				return &a
			}`,
		sig:     `fn <T0, 'a>(p: &'a [T0, unknown]) -> &'a T0`,
		callers: `val m = f([{y: 1}, "s"])`,
		values:  map[string]string{"m": "&{y: 1}"},
	},
	{
		name: "ObjectDestructure",
		inferred: `fn f(p) {
				val {x} = p
				return x
			}`,
		annotated: `fn f<T0, 'a>(p: &'a {x: T0}) -> &'a T0 {
				val {x} = p
				return x
			}`,
		sig:     `fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0`,
		callers: `val m = f({x: 2})`,
		values:  map[string]string{"m": "2"},
	},
	{
		name:      "Identity",
		inferred:  `fn f(x) { return x }`,
		annotated: `fn f<T0>(x: T0) -> T0 { return x }`,
		sig:       `fn <T0>(x: T0) -> T0`,
		callers:   `val m = f("hi")`,
		values:    map[string]string{"m": `"hi"`},
	},
	{
		name: "OwnedStore",
		inferred: `
				var sink = {x: 0}
				fn f(p) { sink = p }
			`,
		annotated: `
				var sink = {x: 0}
				fn f(p: {x: number}) -> undefined { sink = p }
			`,
		sig:     `fn (p: {x: number}) -> undefined`,
		callers: `val m = f({x: 1})`,
		values:  map[string]string{"m": "undefined"},
	},
	{
		name:      "FieldWrite",
		inferred:  `fn f(mut p, v) { p.x = v }`,
		annotated: `fn f<T0>(mut p: {x: T0}, v: T0) -> undefined { p.x = v }`,
		sig:       `fn <T0>(p: {x: T0}, v: T0) -> undefined`,
	},
}

// TestInferredSignatureRoundTrips covers functions whose inferred signature, written back
// as an annotation, checks clean and renders as the same signature. Each case checks the
// unannotated `f` and the annotated `f`, each in a source of its own.
func TestInferredSignatureRoundTrips(t *testing.T) {
	for _, tt := range roundTripCases {
		t.Run(tt.name, func(t *testing.T) {
			for _, src := range []string{tt.inferred, tt.annotated} {
				values, errs := inferAgainstTreeWithSpans(t, src)
				require.Empty(t, errs, src)
				require.Equal(t, tt.sig, values["f"], src)
			}
		})
	}
}

// TestCallsToRoundTrippedSignature covers calls to the annotated `f` of each roundTripCases
// entry that has callers, asserting the type each named binding of the callers takes.
func TestCallsToRoundTrippedSignature(t *testing.T) {
	for _, tt := range roundTripCases {
		if tt.callers == "" {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.annotated+"\n"+tt.callers)
			require.Empty(t, errs)
			for name, want := range tt.values {
				require.Equal(t, want, values[name], "binding %q", name)
			}
		})
	}
}

// TestBorrowOfTypeParamIntoOwned covers a borrow of a type parameter flowing into an owned
// slot. A borrow of a rigid parameter can't fill an unrelated type parameter. It can't fill
// the same parameter either, since a caller could then write through an object it only
// lent. A concrete type such as `&{x: number}` into `{x: number}` is rejected the same way.
func TestBorrowOfTypeParamIntoOwned(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "into an unrelated type parameter",
			src:  `fn f<T, U>(p: &T) -> U { return p }`,
			want: []string{"1:33-1:34: cannot constrain T <: U"},
		},
		{
			name: "into the same type parameter",
			src:  `fn steal<T>(p: &T) -> T { return p }`,
			want: []string{"1:16-1:18: cannot use borrowed &T as owned T"},
		},
		{
			name: "into the same type parameter bounded by a primitive",
			src:  `fn f<T: number>(p: &T) -> T { return p }`,
			want: nil,
		},
		{
			name: "into the same type parameter bounded by a union of primitives",
			src:  `fn f<T: number | string>(p: &T) -> T { return p }`,
			want: nil,
		},
		{
			name: "field read through a local into the same type parameter bounded by a primitive",
			src: `fn f<T: number>(p: &{x: T}) -> T {
				val y = p.x
				return y
			}`,
			want: nil,
		},
		{
			name: "into the same type parameter bounded by an object",
			src:  `fn f<T: {x: number}>(p: &T) -> T { return p }`,
			want: []string{"1:25-1:27: cannot use borrowed &T as owned T"},
		},
		{
			name: "mutable borrow into the same type parameter",
			src:  `fn steal<T>(p: &mut T) -> T { return p }`,
			want: []string{"1:16-1:22: cannot use borrowed &mut T as owned T"},
		},
		{
			name: "into a union naming the same type parameter",
			src:  `fn steal<T>(p: &T) -> T | number { return p }`,
			want: []string{"1:16-1:18: cannot use borrowed &T as owned T | number"},
		},
		{
			name: "field read into an unrelated type parameter",
			src:  `fn f<T, U>(p: &{x: T}) -> U { return p.x }`,
			want: []string{"1:38-1:41: cannot constrain T <: U"},
		},
		{
			name: "destructured element into an unrelated type parameter",
			src: `fn f<T, U>(p: &[T, number]) -> U {
				val [a, b] = p
				return a
			}`,
			want: []string{"3:12-3:13: cannot constrain T <: U"},
		},
		{
			// Each use of `y` instantiates a copy of the read's result variable, and the
			// copy still reads as a field read.
			name: "field read through a local into an unrelated type parameter",
			src: `fn f<T, U>(p: &{x: T}) -> U {
				val y = p.x
				return y
			}`,
			want: []string{"3:12-3:13: cannot constrain T <: U"},
		},
		{
			name: "destructured element through a local into an unrelated type parameter",
			src: `fn f<T, U>(p: &[T, number]) -> U {
				val [a, b] = p
				val c = a
				return c
			}`,
			want: []string{"4:12-4:13: cannot constrain T <: U"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

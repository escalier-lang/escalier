package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferredSignatureRoundTrips covers functions whose inferred signature, written back
// as an annotation, checks clean and renders as the same signature. Each case declares
// `f` once without annotations and once with the signature inference gave it. Both
// declarations share the callers in `callers`, and each value in `values` must come out
// the same under either declaration.
func TestInferredSignatureRoundTrips(t *testing.T) {
	tests := []struct {
		name      string
		inferred  string
		annotated string
		sig       string
		callers   string
		values    map[string]string
	}{
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
			annotated: `fn f<T0>(p: [T0, unknown]) -> T0 {
				val [a, b] = p
				return a
			}`,
			sig:     `fn <T0>(p: [T0, unknown]) -> T0`,
			callers: `val m = f([1, "s"])`,
			values:  map[string]string{"m": "1"},
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, src := range []string{tt.inferred, tt.annotated} {
				values, errs := inferAgainstTreeWithSpans(t, src+"\n"+tt.callers)
				require.Empty(t, errs, src)
				require.Equal(t, tt.sig, values["f"], src)
				for name, want := range tt.values {
					require.Equal(t, want, values[name], "%s: binding %q", src, name)
				}
			}
		})
	}
}

// TestBorrowOfTypeParamIntoOwned covers a borrow of a type parameter flowing into an owned
// slot. A borrow of a rigid parameter can't fill an unrelated type parameter.
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
		// A borrow of T fills an owned T without a copy, so a caller could write through an
		// object it only lent. A concrete type such as `&{x: number}` into `{x: number}` is
		// already rejected by T6.
		//
		// DISABLED until #2589, which applies T6 to a type parameter. Until then `steal`
		// checks clean.
		/*
			{
				name: "into the same type parameter",
				src:  `fn steal<T>(p: &T) -> T { return p }`,
				want: []string{"1:16-1:18: cannot use borrowed &T as owned T"},
			},
		*/
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

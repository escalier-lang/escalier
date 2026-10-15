package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferOptionalChain covers the optional member, index, and call links, written `?.`,
// `?[`, and `?(`. A link whose
// receiver may be `null` or `undefined` adds `undefined` to the chain's value, and a
// receiver that is neither reads the way the plain link does.
func TestInferOptionalChain(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "MemberOffAnOptionalField",
			src:  "declare val o: {e?: {f: string}}\nval x = o.e?.f",
			want: "string | undefined",
		},
		{
			name: "IndexOffAnOptionalField",
			src:  "declare val o: {e?: {f: string}}\nval x = o.e?[\"f\"]",
			want: "string | undefined",
		},
		{
			name: "MemberOffANullReceiver",
			src:  "declare val o: {f: string} | null\nval x = o?.f",
			want: "string | undefined",
		},
		{
			name: "DynamicIndexOffAnOptionalArray",
			src:  "declare val xs: Array<number> | undefined\ndeclare val i: number\nval x = xs?[i]",
			want: "number | undefined",
		},
		{
			name: "CallOfANullableFunction",
			src:  "declare val f: (fn () -> number) | undefined\nval x = f?()",
			want: "number | undefined",
		},
		{
			name: "CallOfAnOptionalMethod",
			src:  "declare val o: {m?: fn () -> number}\nval x = o.m?()",
			want: "number | undefined",
		},
		{
			// The call is a link of the chain, so it reads `m` off the defined receiver
			// and is skipped along with the read when the receiver is `undefined`.
			name: "MethodCallThroughAnOptionalLink",
			src:  "declare val o: {m: fn () -> number} | undefined\nval x = o?.m()",
			want: "number | undefined",
		},
		{
			// `a?.b.c` reads `.c` only when `a` is defined, so `.c` reads off `b` itself.
			name: "MemberShortCircuitsTheChain",
			src:  "declare val a: {b: {c: number}} | undefined\nval x = a?.b.c",
			want: "number | undefined",
		},
		{
			name: "IndexShortCircuitsTheChain",
			src:  "declare val a: {b: {c: number}} | undefined\nval x = a?.b[\"c\"]",
			want: "number | undefined",
		},
		{
			name: "CallShortCircuitsTheChain",
			src:  "declare val a: {b: {m: fn () -> string}} | undefined\nval x = a?.b.m()",
			want: "string | undefined",
		},
		{
			name: "MemberAfterACallShortCircuitsTheChain",
			src:  "declare val a: {m: fn () -> {c: number}} | undefined\nval x = a?.m().c",
			want: "number | undefined",
		},
		{
			name: "NestedOptionalLinks",
			src:  "declare val a: {b?: {c?: {d: number}}}\nval x = a.b?.c?.d",
			want: "number | undefined",
		},
		{
			name: "MemberOffANonNullableReceiver",
			src:  "declare val a: {b: number}\nval x = a?.b",
			want: "number",
		},
		{
			name: "IndexOffANonNullableReceiver",
			src:  "declare val a: {b: number}\nval x = a?[\"b\"]",
			want: "number",
		},
		{
			name: "CallOfANonNullableFunction",
			src:  "declare val f: fn () -> number\nval x = f?()",
			want: "number",
		},
		{
			// A receiver that is always `undefined` always short-circuits.
			name: "MemberOffAnUndefinedReceiver",
			src:  "declare val a: undefined\nval x = a?.b",
			want: "undefined",
		},
		{
			name: "DynamicIndexOffAnUndefinedReceiver",
			src:  "declare val a: undefined\ndeclare val i: number\nval x = a?[i]",
			want: "undefined",
		},
		{
			name: "MemberOffABorrowOfANullableUnion",
			src:  "fn read(p: &({b: number} | undefined)) { return p?.b }\nval x = read",
			want: "fn (p: &({b: number} | undefined)) -> number | undefined",
		},
		{
			name: "MemberOffAnOptionalParam",
			src:  "fn read(p: {b: number} | undefined) { return p?.b }\nval x = read(undefined)",
			want: "number | undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["x"])
		})
	}
}

// TestInferOptionalChainErrors covers the reads a `?.` does not excuse. A short-circuit
// skips only the links after the `?.`, so a later link still reads off each member its own
// receiver may hold.
func TestInferOptionalChainErrors(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			// `b` may be `undefined` even when `a` is defined, so `.c` may read off
			// `undefined`.
			name:     "PlainLinkOffAnOptionalField",
			src:      "declare val a: {b?: {c: number}} | undefined\nval x = a?.b.c",
			wantErrs: []string{"2:9-2:15: cannot constrain undefined <: object"},
		},
		{
			name:     "MissingPropertyBehindAnOptionalLink",
			src:      "declare val a: {b: number} | undefined\nval x = a?.c",
			wantErrs: []string{"2:12-2:13: object is missing property: c"},
		},
		{
			name:     "CallOfANonFunctionBehindAnOptionalLink",
			src:      "declare val f: number | undefined\nval x = f?()",
			wantErrs: []string{"2:9-2:13: cannot constrain number <: function"},
		},
		{
			// An argument is not a link of the chain, so `a?.b` passes its own
			// `number | undefined` to `f`.
			name:     "AnArgumentEndsItsOwnChain",
			src:      "declare val a: {b: number} | undefined\ndeclare fn f(n: number) -> number\nval x = f(a?.b)",
			wantErrs: []string{"3:11-3:15: cannot constrain undefined <: number"},
		},
		{
			// JavaScript rejects an optional chain as an assignment target, even when the
			// `?.` is not the last link.
			name:     "AssignmentThroughAnOptionalLink",
			src:      "fn write(a: mut {b: {c: number}}) { a?.b.c = 5 }",
			wantErrs: []string{"1:37-1:43: cannot assign through an optional chain, since `?.` may short-circuit and leave no place to write"},
		},
		{
			name:     "AssignmentThroughAnOptionalIndex",
			src:      `fn write(a: mut {b: number}) { a?["b"] = 5 }`,
			wantErrs: []string{"1:32-1:39: cannot assign through an optional chain, since `?.` may short-circuit and leave no place to write"},
		},
		{
			name:     "BorrowOfAnOptionalChain",
			src:      "declare val a: {b: {c: number}} | undefined\nval x = &a?.b",
			wantErrs: []string{"2:9-2:14: Unsupported: borrow of an optional chain"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
		})
	}
}

// TestInferOptionalObjectFieldRead covers a plain read of an optional field whose type is
// an object. The read yields the field or `undefined`, and a borrowed receiver still lends
// the field at its own lifetime.
func TestInferOptionalObjectFieldRead(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "OwnedReceiver",
			src:  "fn read(a: {b?: {c: number}}) { return a.b }",
			want: "fn (a: {b?: {c: number}}) -> {c: number} | undefined",
		},
		{
			name: "BorrowedReceiver",
			src:  "fn read(a: &{b?: {c: number}}) { return a.b }",
			want: "fn <'a>(a: &'a {b?: {c: number}}) -> &'a {c: number} | undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["read"])
		})
	}
}

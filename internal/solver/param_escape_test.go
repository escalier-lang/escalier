package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferUnannotatedParamOwnership covers which unannotated parameters infer as a borrow. A
// parameter the body only reads through infers as `&`. Every other use in the table leaves it
// owned.
func TestInferUnannotatedParamOwnership(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		errs []string
	}{
		{
			name: "MemberRead",
			src:  `fn f(p) { return p.x }`,
			want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0",
		},
		{
			name: "NestedMemberRead",
			src:  `fn f(p) { return p.x.y }`,
			want: "fn <T0, 'a>(p: &'a {x: {y: T0}}) -> &'a T0",
		},
		{
			name: "MemberReadIntoArithmetic",
			src:  `fn f(p) { return p.x + p.y }`,
			want: "fn (p: &{x: number, y: number}) -> number",
		},
		{
			name: "MemberReadInCondition",
			src:  `fn f(p) { return if p.ok { 1 } else { 2 } }`,
			want: "fn (p: &{ok: boolean}) -> 1 | 2",
		},
		{
			name: "MemberReadBorrowed",
			src:  `fn f(p) { return &p.x }`,
			want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0",
		},
		{
			name: "MemberReadStoredInModuleState",
			src: `
				var sink = {y: 0}
				fn f(p) { sink = p.x }
			`,
			want: "fn (p: {x: {y: number}}) -> undefined",
		},
		{
			name: "MemberReadPassedToCall",
			src: `
				declare fn own(q: {y: number}) -> number
				fn f(p) { return own(p.x) }
			`,
			want: "fn (p: {x: {y: number}}) -> number",
		},
		{
			name: "MemberReadBound",
			src: `fn f(p) {
				val q = p.x
				return q
			}`,
			want: "fn <T0>(p: {x: T0}) -> T0",
		},
		{
			name: "MemberReadAsBlockValue",
			src: `
				var sink = {y: 0}
				fn f(p) { sink = do { p.x } }
			`,
			want: "fn (p: {x: {y: number}}) -> undefined",
		},
		{
			name: "MemberReadDiscardedInNestedBlock",
			src: `fn f(p) {
				if p.ok {
					p.x
					return 1
				}
				return 2
			}`,
			want: "fn (p: &{ok: boolean, x: unknown}) -> 1 | 2",
		},
		{
			name: "MemberReadInLogicalOperand",
			src:  `fn f(p) { return p.a || p.b }`,
			want: "fn (p: &{a: boolean, b: boolean}) -> boolean",
		},
		{
			name: "MemberReadInTemplate",
			src:  "fn f(p) { return `${p.x}` }",
			want: "fn (p: &{x: string | number | boolean | bigint}) -> string",
		},
		{
			name: "MutBindingOnlyRead",
			src:  `fn f(mut p) { return p.x }`,
			want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0",
		},
		{
			name: "Returned",
			src:  `fn f(p) { return p }`,
			want: "fn <T0>(p: T0) -> T0",
		},
		{
			name: "StoredInModuleState",
			src: `
				var sink = {x: 0}
				fn f(p) { sink = p }
			`,
			want: "fn (p: {x: number}) -> undefined",
		},
		{
			name: "ValueTypeOperand",
			src:  `fn f(a) { return a + 1 }`,
			want: "fn (a: number) -> number",
		},
		{
			name: "ShorthandProperty",
			src:  `fn f(p) { return {p} }`,
			want: "fn <T0>(p: T0) -> {p: T0}",
		},
		{
			name: "MethodCall",
			src:  `fn f(o) { return o.m() }`,
			want: "fn <T0>(o: {m: fn () -> T0}) -> T0",
		},
		{
			name: "WriteThroughMember",
			src:  `fn f(mut p) { p.x = 1 }`,
			want: "fn (p: {x: number}) -> undefined",
		},
		{
			name: "CapturedByClosure",
			src:  `fn f(p) { return fn () { return p.x } }`,
			want: "fn <T0>(p: {x: T0}) -> fn () -> T0",
		},
		{
			// The nested function reads p through a shorthand property, which counts as a use of p.
			name: "CapturedByShorthandInClosure",
			src: `fn f(p) {
				val g = fn () { return {p} }
				return p.x
			}`,
			want: "fn <T0>(p: {x: T0}) -> T0",
		},
		{
			name: "NameRebound",
			src: `fn f(p) {
				val q = p.x
				val p = {x: q}
				return p.x
			}`,
			want: "fn <T0>(p: {x: T0}) -> T0",
		},
		{
			name: "NameReboundByShorthandPattern",
			src: `fn f(p) {
				val {p} = {p: {x: 1}}
				return p.x
			}`,
			want: "fn <T0>(p: T0) -> 1",
		},
		{
			name: "Unused",
			src:  `fn f(p) { return 1 }`,
			want: "fn <T0>(p: T0) -> 1",
		},
		{
			name: "Destructured",
			src:  `fn f({x}) { return x }`,
			want: "fn <T0>({x}: {x: T0}) -> T0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.errs, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TestInferCallbackParamTakesCalleeType asserts that an unannotated parameter of a function
// passed as a call argument takes its type from the callee's signature, even when the body
// only reads through it.
func TestInferCallbackParamTakesCalleeType(t *testing.T) {
	values, errs := inferAgainstTreeWithSpans(t, `
		declare fn measure(f: fn (s: {n: number}) -> number) -> number
		val g = fn (s) { return s.n }
		val r = measure(fn (s) { return s.n })
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0, 'a>(s: &'a {n: T0}) -> &'a T0", values["g"])
	require.Equal(t, "number", values["r"])
}

// TestCallToBorrowParamNeedsWrittenBorrow asserts that a call to a function whose parameter
// infers as a borrow takes the borrow only through a written `&`, as F4 requires.
func TestCallToBorrowParamNeedsWrittenBorrow(t *testing.T) {
	tests := []struct {
		name string
		call string
		errs []string
	}{
		{
			name: "WrittenBorrow",
			call: "read(&b)",
		},
		{
			name: "UnwrittenBorrow",
			call: "read(b)",
			errs: []string{"5:19-5:20: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, `
				fn read(p) { return p.x }
				fn go() -> number {
					val b = {x: 2}
					val n = `+tt.call+`
					return b.x + n
				}
			`)
			require.Equal(t, tt.errs, errs)
		})
	}
}

// TestReadThroughBorrowParamKeepsBorrow covers the value a field read through an inferred
// borrow parameter returns. The read keeps the parameter's borrow, so the caller cannot write
// through it, and a value-type field copies out of it.
func TestReadThroughBorrowParamKeepsBorrow(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		errs []string
	}{
		{
			name: "WriteThroughReturnedField",
			body: `
					val b = {x: {y: 1}}
					var mut r = read(&b)
					r.y = 5
					return b
			`,
			want: "fn () -> {x: {y: 1}}",
			errs: []string{"6:6-6:13: cannot constrain immutable object <: mutable object"},
		},
		{
			name: "ValueFieldCopiesOut",
			body: `
					val b = {x: 1}
					val n = read(&b)
					return n + 1
			`,
			want: "fn () -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, `
				fn read(p) { return p.x }
				fn h() {`+tt.body+`}
			`)
			require.Equal(t, tt.errs, errs)
			require.Equal(t, tt.want, values["h"])
		})
	}
}

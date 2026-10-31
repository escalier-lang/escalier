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
			want: "fn (p: unknown) -> 1",
		},
		{
			name: "Unused",
			src:  `fn f(p) { return 1 }`,
			want: "fn (p: unknown) -> 1",
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
		src  string
		want string
		errs []string
	}{
		{
			name: "WriteThroughReturnedField",
			src: `fn read(p) { return p.x }
				fn h() {
					val b = {x: {y: 1}}
					var mut r = read(&b)
					r.y = 5
					return b
				}`,
			want: "fn () -> {x: {y: 1}}",
			errs: []string{"5:6-5:13: cannot constrain immutable object <: mutable object"},
		},
		{
			name: "ValueFieldCopiesOut",
			src: `fn read(p) { return p.x }
				fn h() {
					val b = {x: 1}
					val n = read(&b)
					return n + 1
				}`,
			want: "fn () -> number",
		},
		{
			name: "UndefinedFieldCopiesOut",
			src: `fn read(p) { return p.x }
				fn h() {
					val b = {x: undefined}
					val s: undefined = read(&b)
					return s
				}`,
			want: "fn () -> undefined",
		},
		{
			name: "FunctionFieldCopiesOut",
			src: `fn read(p) { return p.x }
				fn h() {
					val b = {x: fn () { return 1 }}
					return read(&b)()
				}`,
			want: "fn () -> 1",
		},
		{
			name: "ObjectFieldIntoOwnedDestination",
			src: `fn read(p) { return p.x }
				fn h() {
					val b = {x: {y: 1}}
					val s: {y: number} = read(&b)
					return s
				}`,
			want: "fn () -> {y: number}",
			errs: []string{"4:13-4:24: cannot use borrowed &object as owned object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.errs, errs)
			require.Equal(t, tt.want, values["h"])
		})
	}
}

// TestInferOpenEscapeParam covers D9's rule for an unannotated parameter stored where the
// destination's type leaves ownership open. Each case declares `remember` and a caller `go`.
// A parameter the body never writes infers as a `'static` borrow, and one the body writes, or
// one the destination's type makes owned, stays owned and moves the caller's argument.
func TestInferOpenEscapeParam(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is remember's rendered type, checked when it is not empty.
		want string
		// seen is the rendered type of the module-level map, checked when it is not empty.
		seen string
		errs []string
	}{
		{
			// remember's rendering waits on #2352, so TestInferOpenEscapeParamRendering holds it.
			name: "ReadOnlyIsStaticBorrow",
			src: `
				val mut seen = Map()
				fn remember(value) { seen.set("last", value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(&opts)
					return opts.retries
				}
			`,
			seen: "Map<string, &'static {retries: 3}>",
		},
		{
			// remember's rendering waits on #2352, so TestInferOpenEscapeParamRendering holds it.
			name: "ReadOnlyNeedsWrittenBorrow",
			src: `
				val mut seen = Map()
				fn remember(value) { seen.set("last", value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			seen: "Map<string, &'static {retries: 3}>",
			errs: []string{"6:15-6:19: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
		{
			name: "WrittenIsOwned",
			src: `
				val mut seen = Map()
				fn remember(mut value) {
					value.retries = 0
					seen.set("last", value)
				}
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: {retries: number}) -> undefined",
			errs: []string{"10:13-10:25: use of moved value 'opts'"},
		},
		{
			name: "OwnedDestinationIsOwned",
			src: `
				val mut seen: Map<string, {retries: number}> = Map()
				fn remember(value) { seen.set("last", value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: {retries: number}) -> undefined",
			errs: []string{"7:13-7:25: use of moved value 'opts'"},
		},
		{
			name: "AnnotationOverrides",
			src: `
				val mut seen = Map()
				fn remember(value: {retries: number}) { seen.set("last", value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: {retries: number}) -> undefined",
			errs: []string{"7:13-7:25: use of moved value 'opts'"},
		},
		{
			name: "AlsoReturnedIsOwned",
			src: `
				val mut seen = Map()
				fn remember(value) {
					seen.set("last", value)
					return value
				}
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn <T0>(value: T0) -> T0",
			errs: []string{"10:13-10:25: use of moved value 'opts'"},
		},
		{
			name: "LocalReceiverDoesNotEscape",
			src: `
				fn remember(value) {
					val mut seen = Map()
					seen.set("last", value)
				}
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: unknown) -> undefined",
		},
		{
			// id's parameter is a fresh variable for each call, which m's own type does not
			// reach, so the call stores nothing in m.
			name: "GenericMethodDoesNotEscape",
			src: `
				val m = {id: fn <T>(x: T) -> T { return x }}
				fn remember(value) { m.id(value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: unknown) -> undefined",
		},
		{
			// A callback's parameter takes its type from the callee, so the callee decides its
			// ownership and storing it in seen leaves it owned.
			name: "CallbackArgumentKeepsTheCalleeType",
			src: `
				val mut seen = Map()
				declare fn each<T>(x: T, f: fn (v: T) -> undefined) -> undefined
				fn go() -> number {
					val opts = {retries: 3}
					each(opts, fn (v) { seen.set("last", v) })
					return opts.retries
				}
			`,
			seen: "Map<string, {retries: number}>",
		},
		{
			// A value type has no storage to borrow, so it copies into the borrow without a
			// written `&`. The key escapes into seen too, so it is a `'static` borrow as well.
			// A `&'static` of a value type stands for the value type, so seen holds the strings
			// and numbers. remember's rendering waits on #2352, so
			// TestInferOpenEscapeParamRendering holds it.
			name: "ValueTypeArgumentsCopy",
			src: `
				val mut seen = Map()
				fn remember(key, value) { seen.set(key, value) }
				fn go() -> number {
					val k = "a"
					remember(k, 3)
					remember("b", 4)
					return 1
				}
			`,
			seen: `Map<&'static ("a" | "b"), &'static (3 | 4)>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.errs, errs)
			if tt.want != "" {
				require.Equal(t, tt.want, values["remember"])
			}
			if tt.seen != "" {
				require.Equal(t, tt.seen, values["seen"])
			}
		})
	}
}

// DISABLED until #2352, which keeps a function monomorphic when its parameter flows into a weak
// binding. Today remember renders each such parameter as `unknown`, since seen's weak variable
// holds the type the parameter's borrow points to and remember is coalesced as if it were generic.
/*
func TestInferOpenEscapeParamRendering(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ReadOnlyIsStaticBorrow",
			src: `
				val mut seen = Map()
				fn remember(value) { seen.set("last", value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(&opts)
					return opts.retries
				}
			`,
			want: "fn (value: &'static {retries: 3}) -> undefined",
		},
		{
			name: "ValueTypeArgumentsCopy",
			src: `
				val mut seen = Map()
				fn remember(key, value) { seen.set(key, value) }
				fn go() -> number {
					val k = "a"
					remember(k, 3)
					remember("b", 4)
					return 1
				}
			`,
			want: `fn (key: &'static ("a" | "b"), value: &'static (3 | 4)) -> undefined`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["remember"])
		})
	}
}
*/

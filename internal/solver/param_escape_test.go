package solver

import (
	"strconv"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferUnannotatedParamOwnership covers which unannotated parameters infer as a borrow. A
// parameter the body only reads through infers as `&`. Every other use in the table leaves it
// owned. A case with several variants is a group that reads the same parts of the parameter.
// Each variant's want is the same signature, apart from the parameter's own pattern, which the
// signature shows as the variant writes it.
//
// TestInferUnannotatedDestructuredParam holds the groups that return or read through a leaf of
// the parameter, since it also asserts which parameters each variant moves.
func TestInferUnannotatedParamOwnership(t *testing.T) {
	tests := []struct {
		name     string
		variants []sigVariant
		errs     []string
	}{
		{
			name: "MemberReadIntoArithmetic",
			variants: []sigVariant{
				{src: `fn f(p) { return p.x + p.y }`, want: "fn (p: &{x: number, y: number}) -> number"},
			},
		},
		{
			name: "LeafIntoArithmetic",
			variants: []sigVariant{
				{src: `fn f(p) { return p.x + 1 }`, want: "fn (p: &{x: number}) -> number"},
				{
					src: `fn f(p) {
					val q = p.x
					return q + 1
				}`,
					want: "fn (p: &{x: number}) -> number",
				},
				{src: `fn f({x}) { return x + 1 }`, want: "fn ({x}: &{x: number}) -> number"},
			},
		},
		{
			name: "MemberReadInCondition",
			variants: []sigVariant{
				{src: `fn f(p) { return if p.ok { 1 } else { 2 } }`, want: "fn (p: &{ok: boolean}) -> 1 | 2"},
			},
		},
		{
			name: "MemberReadBorrowed",
			variants: []sigVariant{
				{src: `fn f(p) { return &p.x }`, want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0"},
			},
		},
		{
			name: "MemberReadStoredInModuleState",
			variants: []sigVariant{
				{
					src: `
				var sink = {y: 0}
				fn f(p) { sink = p.x }
			`,
					want: "fn (p: {x: {y: number}}) -> undefined",
				},
			},
		},
		{
			name: "MemberReadPassedToCall",
			variants: []sigVariant{
				{
					src: `
				declare fn own(q: {y: number}) -> number
				fn f(p) { return own(p.x) }
			`,
					want: "fn (p: {x: {y: number}}) -> number",
				},
			},
		},
		{
			name: "BoundReadStoredInModuleState",
			variants: []sigVariant{
				{
					src: `
				var sink = {y: 0}
				fn f(p) {
					val q = p.x
					sink = q
				}
			`,
					want: "fn (p: {x: {y: number}}) -> undefined",
				},
			},
		},
		{
			name: "BoundReadPassedToCall",
			variants: []sigVariant{
				{
					src: `
				declare fn own(q: {y: number}) -> number
				fn f(p) {
					val q = p.x
					return own(q)
				}
			`,
					want: "fn (p: {x: {y: number}}) -> number",
				},
			},
		},
		// A closure that reads the parameter through a capture borrows it for as long as the
		// closure lives, so the closure can be called more than once.
		//
		// DISABLED until #2565. Today the capture counts as an escape, so the parameter stays
		// owned and each variant infers `fn <T0>(p: {x: T0}) -> fn () -> T0`. That closure
		// returns its owned capture on every call. Section 1 of #2565 ties a returned closure's
		// lifetime to its captures, which lets the capture read through the borrow.
		/*
			{
				name: "ReadCapturedByClosure",
				variants: []sigVariant{
					{
						src: `fn f(p) {
						val q = p.x
						return fn () { return q }
					}`,
						want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a fn () -> &'a T0",
					},
					{src: `fn f(p) { return fn () { return p.x } }`, want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a fn () -> &'a T0"},
				},
			},
		*/
		{
			name: "BoundReadInVar",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				var q = p.x
				return q
			}`,
					want: "fn <T0>(p: {x: T0}) -> T0",
				},
			},
		},
		{
			name: "BoundReadNameRebound",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val q = p.x
				val q = {y: 1}
				return q
			}`,
					want: "fn (p: {x: unknown}) -> {y: 1}",
				},
			},
		},
		{
			// The second `val` binds q again while reading through it, so a later use of q may
			// name either binding.
			name: "BoundReadNameReboundByAlias",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val q = p.x
				val {q} = q
				return q
			}`,
					want: "fn <T0>(p: {x: {q: T0}}) -> T0",
				},
			},
		},
		{
			// A literal pattern binds no alias, so the read in its initializer goes nowhere the
			// scanner accepts.
			name: "MemberReadIntoLiteralPattern",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val 1 = p.x
				return 1
			}`,
					want: "fn (p: {x: unknown}) -> 1",
				},
			},
		},
		{
			// A defaulted parameter stays owned, since the default fills it when the caller
			// passes nothing.
			name: "DefaultedParam",
			variants: []sigVariant{
				{src: `fn f(p = {x: 1}) { return p.x }`, want: "fn <T0>(p?: {x: T0}) -> T0 | 1"},
			},
		},
		{
			name: "MemberReadAsBlockValue",
			variants: []sigVariant{
				{
					src: `
				var sink = {y: 0}
				fn f(p) { sink = do { p.x } }
			`,
					want: "fn (p: {x: {y: number}}) -> undefined",
				},
			},
		},
		{
			name: "MemberReadDiscardedInNestedBlock",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				if p.ok {
					p.x
					return 1
				}
				return 2
			}`,
					want: "fn (p: &{ok: boolean, x: unknown}) -> 1 | 2",
				},
			},
		},
		{
			name: "MemberReadInLogicalOperand",
			variants: []sigVariant{
				{src: `fn f(p) { return p.a || p.b }`, want: "fn (p: &{a: boolean, b: boolean}) -> boolean"},
			},
		},
		{
			name: "MemberReadInTemplate",
			variants: []sigVariant{
				{src: "fn f(p) { return `${p.x}` }", want: "fn (p: &{x: string | number | boolean | bigint}) -> string"},
			},
		},
		{
			name: "MutBindingOnlyRead",
			variants: []sigVariant{
				{src: `fn f(mut p) { return p.x }`, want: "fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0"},
			},
		},
		{
			name: "Returned",
			variants: []sigVariant{
				{src: `fn f(p) { return p }`, want: "fn <T0>(p: T0) -> T0"},
			},
		},
		{
			name: "StoredInModuleState",
			variants: []sigVariant{
				{
					src: `
				var sink = {x: 0}
				fn f(p) { sink = p }
			`,
					want: "fn (p: {x: number}) -> undefined",
				},
			},
		},
		{
			name: "ValueTypeOperand",
			variants: []sigVariant{
				{src: `fn f(a) { return a + 1 }`, want: "fn (a: number) -> number"},
			},
		},
		{
			name: "ShorthandProperty",
			variants: []sigVariant{
				{src: `fn f(p) { return {p} }`, want: "fn <T0>(p: T0) -> {p: T0}"},
			},
		},
		{
			name: "MethodCall",
			variants: []sigVariant{
				{src: `fn f(o) { return o.m() }`, want: "fn <T0>(o: {m: fn () -> T0}) -> T0"},
			},
		},
		{
			name: "WriteThroughMember",
			variants: []sigVariant{
				{src: `fn f(mut p) { p.x = 1 }`, want: "fn (p: {x: number}) -> undefined"},
			},
		},
		{
			// The nested function reads p through a shorthand property, which counts as a use of p.
			name: "CapturedByShorthandInClosure",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val g = fn () { return {p} }
				return p.x
			}`,
					want: "fn <T0>(p: {x: T0}) -> T0",
				},
			},
		},
		{
			name: "NameRebound",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val q = p.x
				val p = {x: q}
				return p.x
			}`,
					want: "fn <T0>(p: {x: T0}) -> T0",
				},
			},
		},
		{
			name: "NameReboundByShorthandPattern",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val {p} = {p: {x: 1}}
				return p.x
			}`,
					want: "fn (p: unknown) -> 1",
				},
			},
		},
		{
			name: "Unused",
			variants: []sigVariant{
				{src: `fn f(p) { return 1 }`, want: "fn (p: unknown) -> 1"},
			},
		},
		{
			name: "PatternLeafStoredInModuleState",
			variants: []sigVariant{
				{
					src: `
				var sink = {y: 0}
				fn f({x}) { sink = x }
			`,
					want: "fn ({x}: {x: {y: number}}) -> undefined",
				},
			},
		},
		{
			// A read stored into a `&'static` sink requires the field itself to hold a
			// `'static` borrow, so the parameter stays owned around it.
			name: "LeafStoredInStaticBorrow",
			variants: []sigVariant{
				{
					src: `declare var sink: &'static {y: number}
				fn f(p) { sink = p.x }`,
					want: "fn (p: {x: &'static {y: number}}) -> undefined",
				},
				{
					src: `declare var sink: &'static {y: number}
				fn f(p) {
					val q = p.x
					sink = q
				}`,
					want: "fn (p: {x: &'static {y: number}}) -> undefined",
				},
				{
					src: `declare var sink: &'static {y: number}
				fn f(p) { sink = do { p.x } }`,
					want: "fn (p: {x: &'static {y: number}}) -> undefined",
				},
				{
					src: `declare var sink: &'static {y: number}
				fn f({x}) { sink = x }`,
					want: "fn ({x}: {x: &'static {y: number}}) -> undefined",
				},
			},
		},
		{
			name: "MemberBorrowStoredInStaticBorrow",
			variants: []sigVariant{
				{
					src: `
				declare var sink: &'static {y: number}
				fn f(p) { sink = &p.x }
			`,
					want: "fn (p: &'static {x: &'static {y: number}}) -> undefined",
				},
			},
		},
		{
			name: "StoredInStaticBorrow",
			variants: []sigVariant{
				{
					src: `
				declare var sink: &'static {x: number}
				fn f(p) { sink = p }
			`,
					want: "fn (p: &'static {x: number}) -> undefined",
				},
			},
		},
		{
			name: "MutPatternLeaf",
			variants: []sigVariant{
				{src: `fn f({mut x}) { return x }`, want: "fn <T0>({x}: {x: T0}) -> T0"},
			},
		},
		{
			name: "BoundReadRestInBody",
			variants: []sigVariant{
				{
					src: `fn f(p) {
				val {a, ...r} = p.x
				return r
			}`,
					want: "fn <'a>(p: &'a {x: {a: unknown}}) -> &'a {...}",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, variant := range tt.variants {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					sig, _, errs := inferSignature(t, variant.src)
					require.Equal(t, tt.errs, errs)
					require.Equal(t, variant.want, sig)
				})
			}
		})
	}
}

// sigVariant is one source of a group of functions that read the same parts of a parameter,
// and the signature its `f` infers.
type sigVariant struct {
	src  string
	want string
}

// inferSignature infers src against the standard library and returns three things about the
// function the module binds to `f`:
//   - its rendered signature
//   - the positions of the parameters whose FuncParam.Moved is set, or nil when none is
//   - the inference errors, rendered with their spans
func inferSignature(t *testing.T, src string) (string, []int, []string) {
	t.Helper()
	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree, Options{})
	b, ok := res.Scope.values["f"]
	require.True(t, ok, "the source binds no `f`")
	require.Len(t, b.Schemes, 1)

	var moved []int
	if fn, isFunc := schemeType(b.Schemes[0]).(*soltype.FuncType); isFunc {
		for i, p := range fn.Params {
			if p.Moved {
				moved = append(moved, i)
			}
		}
	}
	return res.checker.renderValueBinding(b.Schemes[0]), moved, messagesWithSpan(t, res.Errors)
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
// one the destination's type makes owned, stays owned and moves the caller's argument. A
// parameter stored in the weak `val mut seen = Map()` also reports that seen needs a written
// type, and remember's calls then leave seen's types undecided.
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
			seen: "Map<string, never>",
			errs: []string{rememberIntoSeen("3:26-3:30", "string")},
		},
		{
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
			seen: "Map<string, never>",
			errs: []string{
				rememberIntoSeen("3:26-3:30", "string"),
				"6:15-6:19: this argument is borrowed by the callee, so write the borrow: `&`",
			},
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
			errs: []string{
				rememberIntoSeen("5:6-5:10", "string"),
				"10:13-10:25: use of moved value 'opts'",
			},
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
			errs: []string{
				rememberIntoSeen("4:6-4:10", "string"),
				"10:13-10:25: use of moved value 'opts'",
			},
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
			// reach, so the call stores nothing in m. The argument is written as a borrow,
			// since id's parameter is typed by a type parameter and an owned argument would
			// move. GenericMethodMovesParameter covers the owned argument.
			name: "GenericMethodDoesNotEscape",
			src: `
				val m = {id: fn <T>(x: T) -> T { return x }}
				fn remember(value) { m.id(&value) }
				fn go() -> number {
					val opts = {retries: 3}
					remember(opts)
					return opts.retries
				}
			`,
			want: "fn (value: unknown) -> undefined",
		},
		{
			// Passing value to id's type-parameter parameter moves it, so remember moves the
			// caller's argument too.
			name: "GenericMethodMovesParameter",
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
			errs: []string{"7:13-7:25: use of moved value 'opts'"},
		},
		{
			// A callback's parameter takes its type from the callee, so the callee decides its
			// ownership and storing it in seen leaves it owned. each's `x` is typed by a type
			// parameter, so the call moves opts.
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
			errs: []string{"7:13-7:25: use of moved value 'opts'"},
		},
		{
			// A value type has no storage to borrow, so it copies into the borrow without a
			// written `&`, and go reports nothing. Both parameters flow into seen's types, so
			// the checker asks for a written type on seen, and remember's calls do not decide
			// them.
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
			seen: "Map<never, never>",
			errs: []string{rememberIntoSeen("3:31-3:35", "number")},
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

// TestInferOpenEscapeParamRendering covers how remember renders when its parameters flow
// into the weak `seen`. D18 reports that seen needs a written type, and remember's calls
// then decide nothing about its parameters, so each renders as `unknown`.
func TestInferOpenEscapeParamRendering(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		errs []string
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
			want: "fn (value: unknown) -> undefined",
			errs: []string{rememberIntoSeen("3:26-3:30", "string")},
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
			want: "fn (key: unknown, value: unknown) -> undefined",
			errs: []string{rememberIntoSeen("3:31-3:35", "number")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.errs, errs)
			require.Equal(t, tt.want, values["remember"])
		})
	}
}

// rememberIntoSeen returns the error that remember's parameter flowing into `seen` reports at
// span. key is the key type the error's example annotation shows.
func rememberIntoSeen(span, key string) string {
	return span + ": a parameter of 'remember' flows into 'seen', whose type is not known yet. " +
		"Write the type of 'seen', as in `val mut seen: Map<" + key + ", number> = Map()`."
}

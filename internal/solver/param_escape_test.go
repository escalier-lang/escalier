package solver

import (
	"strconv"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferUnannotatedParamOwnership covers which unannotated parameters infer as a borrow. A
// parameter the body only reads through infers as `&`. Every other use in the table leaves it
// owned. A case with several sources is a group of variants that read the same parts of the
// parameter, and every variant has to infer want. want is written with positional parameter
// names, as positionalSignature renders it.
//
// TestInferUnannotatedDestructuredParam holds the groups that return or read through a leaf of
// the parameter, since it also asserts which parameters each variant moves.
func TestInferUnannotatedParamOwnership(t *testing.T) {
	tests := []struct {
		name string
		srcs []string
		want string
		errs []string
	}{
		{
			name: "MemberReadIntoArithmetic",
			srcs: []string{`fn f(p) { return p.x + p.y }`},
			want: "fn (arg0: &{x: number, y: number}) -> number",
		},
		{
			name: "LeafIntoArithmetic",
			srcs: []string{
				`fn f(p) { return p.x + 1 }`,
				`fn f(p) {
					val q = p.x
					return q + 1
				}`,
				`fn f({x}) { return x + 1 }`,
			},
			want: "fn (arg0: &{x: number}) -> number",
		},
		{
			name: "MemberReadInCondition",
			srcs: []string{`fn f(p) { return if p.ok { 1 } else { 2 } }`},
			want: "fn (arg0: &{ok: boolean}) -> 1 | 2",
		},
		{
			name: "MemberReadBorrowed",
			srcs: []string{`fn f(p) { return &p.x }`},
			want: "fn <T0, 'a>(arg0: &'a {x: T0}) -> &'a T0",
		},
		{
			name: "MemberReadStoredInModuleState",
			srcs: []string{`
				var sink = {y: 0}
				fn f(p) { sink = p.x }
			`},
			want: "fn (arg0: {x: {y: number}}) -> undefined",
		},
		{
			name: "MemberReadPassedToCall",
			srcs: []string{`
				declare fn own(q: {y: number}) -> number
				fn f(p) { return own(p.x) }
			`},
			want: "fn (arg0: {x: {y: number}}) -> number",
		},
		{
			name: "BoundReadStoredInModuleState",
			srcs: []string{`
				var sink = {y: 0}
				fn f(p) {
					val q = p.x
					sink = q
				}
			`},
			want: "fn (arg0: {x: {y: number}}) -> undefined",
		},
		{
			name: "BoundReadPassedToCall",
			srcs: []string{`
				declare fn own(q: {y: number}) -> number
				fn f(p) {
					val q = p.x
					return own(q)
				}
			`},
			want: "fn (arg0: {x: {y: number}}) -> number",
		},
		{
			name: "BoundReadCapturedByClosure",
			srcs: []string{`fn f(p) {
				val q = p.x
				return fn () { return q }
			}`},
			want: "fn <T0>(arg0: {x: T0}) -> fn () -> T0",
		},
		{
			name: "BoundReadInVar",
			srcs: []string{`fn f(p) {
				var q = p.x
				return q
			}`},
			want: "fn <T0>(arg0: {x: T0}) -> T0",
		},
		{
			name: "BoundReadNameRebound",
			srcs: []string{`fn f(p) {
				val q = p.x
				val q = {y: 1}
				return q
			}`},
			want: "fn (arg0: {x: unknown}) -> {y: 1}",
		},
		{
			name: "MemberReadAsBlockValue",
			srcs: []string{`
				var sink = {y: 0}
				fn f(p) { sink = do { p.x } }
			`},
			want: "fn (arg0: {x: {y: number}}) -> undefined",
		},
		{
			name: "MemberReadDiscardedInNestedBlock",
			srcs: []string{`fn f(p) {
				if p.ok {
					p.x
					return 1
				}
				return 2
			}`},
			want: "fn (arg0: &{ok: boolean, x: unknown}) -> 1 | 2",
		},
		{
			name: "MemberReadInLogicalOperand",
			srcs: []string{`fn f(p) { return p.a || p.b }`},
			want: "fn (arg0: &{a: boolean, b: boolean}) -> boolean",
		},
		{
			name: "MemberReadInTemplate",
			srcs: []string{"fn f(p) { return `${p.x}` }"},
			want: "fn (arg0: &{x: string | number | boolean | bigint}) -> string",
		},
		{
			name: "MutBindingOnlyRead",
			srcs: []string{`fn f(mut p) { return p.x }`},
			want: "fn <T0, 'a>(arg0: &'a {x: T0}) -> &'a T0",
		},
		{
			name: "Returned",
			srcs: []string{`fn f(p) { return p }`},
			want: "fn <T0>(arg0: T0) -> T0",
		},
		{
			name: "StoredInModuleState",
			srcs: []string{`
				var sink = {x: 0}
				fn f(p) { sink = p }
			`},
			want: "fn (arg0: {x: number}) -> undefined",
		},
		{
			name: "ValueTypeOperand",
			srcs: []string{`fn f(a) { return a + 1 }`},
			want: "fn (arg0: number) -> number",
		},
		{
			name: "ShorthandProperty",
			srcs: []string{`fn f(p) { return {p} }`},
			want: "fn <T0>(arg0: T0) -> {p: T0}",
		},
		{
			name: "MethodCall",
			srcs: []string{`fn f(o) { return o.m() }`},
			want: "fn <T0>(arg0: {m: fn () -> T0}) -> T0",
		},
		{
			name: "WriteThroughMember",
			srcs: []string{`fn f(mut p) { p.x = 1 }`},
			want: "fn (arg0: {x: number}) -> undefined",
		},
		{
			name: "CapturedByClosure",
			srcs: []string{`fn f(p) { return fn () { return p.x } }`},
			want: "fn <T0>(arg0: {x: T0}) -> fn () -> T0",
		},
		{
			// The nested function reads p through a shorthand property, which counts as a use of p.
			name: "CapturedByShorthandInClosure",
			srcs: []string{`fn f(p) {
				val g = fn () { return {p} }
				return p.x
			}`},
			want: "fn <T0>(arg0: {x: T0}) -> T0",
		},
		{
			name: "NameRebound",
			srcs: []string{`fn f(p) {
				val q = p.x
				val p = {x: q}
				return p.x
			}`},
			want: "fn <T0>(arg0: {x: T0}) -> T0",
		},
		{
			name: "NameReboundByShorthandPattern",
			srcs: []string{`fn f(p) {
				val {p} = {p: {x: 1}}
				return p.x
			}`},
			want: "fn (arg0: unknown) -> 1",
		},
		{
			name: "Unused",
			srcs: []string{`fn f(p) { return 1 }`},
			want: "fn (arg0: unknown) -> 1",
		},
		{
			name: "PatternLeafStoredInModuleState",
			srcs: []string{`
				var sink = {y: 0}
				fn f({x}) { sink = x }
			`},
			want: "fn (arg0: {x: {y: number}}) -> undefined",
		},
		{
			name: "MutPatternLeaf",
			srcs: []string{`fn f({mut x}) { return x }`},
			want: "fn <T0>(arg0: {x: T0}) -> T0",
		},
		{
			name: "BoundReadRestInBody",
			srcs: []string{`fn f(p) {
				val {a, ...r} = p.x
				return r
			}`},
			want: "fn <'a>(arg0: &'a {x: {a: unknown}}) -> &'a {...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, src := range tt.srcs {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					sig, _, errs := positionalSignature(t, src)
					require.Equal(t, tt.errs, errs)
					require.Equal(t, tt.want, sig)
				})
			}
		})
	}
}

// positionalSignature infers src against the standard library and returns three things about
// the function the module binds to `f`:
//   - its signature, rendered with each parameter's pattern replaced by its positional name
//   - the positions of the parameters whose FuncParam.Moved is set, or nil when none is
//   - the inference errors, rendered with their spans
//
// The positional names let two functions whose parameters are named or destructured
// differently render the same, so `fn f({x}) { return x }` and `fn f(p) { return p.x }` both
// render `fn <T0, 'a>(arg0: &'a {x: T0}) -> &'a T0`. The names are replaced on the type
// rather than on the rendered string. The helper coalesces the scheme to its display type,
// copies that FuncType with each parameter's pattern set to nil, and renders the copy. The
// printer names a parameter with a nil pattern by its position. The copy is made after
// coalescing because a scheme body can be a binding variable whose bound holds the FuncType,
// so the body itself has no parameters to rewrite.
func positionalSignature(t *testing.T, src string) (string, []int, []string) {
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

	var positional TypeScheme
	switch sc := b.Schemes[0].(type) {
	case *PolyScheme:
		cp := *sc
		cp.coalesced = withoutParamPatterns(sc.display())
		positional = &cp
	case *MonoScheme:
		positional = &MonoScheme{Ty: withoutParamPatterns(coalesce(sc.Ty, soltype.Positive))}
	}
	return res.checker.renderValueBinding(positional), moved, messagesWithSpan(t, res.Errors)
}

// withoutParamPatterns returns a copy of t with each parameter's pattern set to nil when t is a
// FuncType, and t itself otherwise. The copy shares every parameter type with t.
func withoutParamPatterns(t soltype.Type) soltype.Type {
	fn, ok := t.(*soltype.FuncType)
	if !ok {
		return t
	}
	cp := *fn
	cp.Params = make([]*soltype.FuncParam, len(fn.Params))
	for i, p := range fn.Params {
		param := *p
		param.Pattern = nil
		cp.Params[i] = &param
	}
	return &cp
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
			seen: "Map<string, &'static {retries: number}>",
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
			seen: "Map<string, &'static {retries: number}>",
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
			// written `&`. The key escapes into seen too, so it is a `'static` borrow as well.
			// A `&'static` of a value type stands for the value type, and seen's values are
			// writable slots that widen it, so seen holds strings and numbers. remember's
			// rendering waits on #2352, so TestInferOpenEscapeParamRendering holds it.
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
			seen: "Map<string, number>",
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

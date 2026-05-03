package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInferLifetimeTypes type-checks a program and asserts the printed
// type of each named binding, so that we can pin down both the inferred
// lifetime parameters on functions and the lifetime annotations on the
// parameter/return types.
func TestInferLifetimeTypes(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"IdentityRefReturn": {
			input: `
				fn identity(p: mut {x: number}) -> mut {x: number} { return p }
			`,
			expectedTypes: map[string]string{
				"identity": "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}",
			},
		},
		"FreshObjectReturn": {
			input: `
				fn clone(p: {x: number}) -> mut {x: number} { return {x: p.x} }
			`,
			expectedTypes: map[string]string{
				"clone": "fn (p: {x: number}) -> mut {x: number}",
			},
		},
		"PrimitiveReturnNoLifetime": {
			input: `
				fn getX(p: {x: number}) -> number { return p.x }
			`,
			expectedTypes: map[string]string{
				"getX": "fn (p: {x: number}) -> number",
			},
		},
		"FirstOfTwoRefParams": {
			input: `
				fn first(a: mut {x: number}, b: mut {x: number}) -> mut {x: number} {
					return a
				}
			`,
			expectedTypes: map[string]string{
				"first": "fn <'a>(a: mut 'a {x: number}, b: mut {x: number}) -> mut 'a {x: number}",
			},
		},
		"ConditionalUnionReturn": {
			input: `
				fn pick(a: mut {x: number}, b: mut {x: number}, cond: boolean) -> mut {x: number} {
					if cond { return a } else { return b }
				}
			`,
			expectedTypes: map[string]string{
				"pick": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}, cond: boolean) -> mut ('a | 'b) {x: number}",
			},
		},
		"EscapingRefIntoModuleLevelVar": {
			// Phase 8.4: storing a parameter into a module-level mutable
			// variable forces the parameter to outlive the program — its
			// lifetime is 'static. The return is a primitive so no return
			// lifetime is involved.
			input: `
				var cache: mut {x: number} = {x: 0}
				fn cacheItem(item: mut {x: number}) -> number {
					cache = item
					return item.x
				}
			`,
			expectedTypes: map[string]string{
				"cacheItem": "fn (item: mut 'static {x: number}) -> number",
			},
		},
		"EscapingRefViaPropertyAssignment": {
			// Phase 8.4: assigning into a property of a module-level
			// object also escapes — the root of the lvalue chain is a
			// non-local binding.
			input: `
				var cache: mut {item: mut {x: number}} = {item: {x: 0}}
				fn cacheItem(item: mut {x: number}) -> number {
					cache.item = item
					return item.x
				}
			`,
			expectedTypes: map[string]string{
				"cacheItem": "fn (item: mut 'static {x: number}) -> number",
			},
		},
		"NoEscapeWhenAssigningToLocal": {
			// Sanity check: assigning a param into a local variable does
			// NOT trigger 'static. The return is a primitive, so no
			// lifetime is inferred at all.
			input: `
				fn copyItem(item: mut {x: number}) -> number {
					var local: mut {x: number} = item
					return local.x
				}
			`,
			expectedTypes: map[string]string{
				"copyItem": "fn (item: mut {x: number}) -> number",
			},
		},
		"TupleDestructuredParamFirstReturned": {
			// Phase 8.3: tuple-destructured param. Only the leaf actually
			// returned (`a`) gets a lifetime; `b` is unconstrained, just
			// like a non-destructured param that isn't returned. The
			// printer renders the leaf's lifetime inline at the
			// destructured position.
			input: `
				fn pickFirst([a, b]: [mut {x: number}, mut {x: number}]) -> mut {x: number} {
					return a
				}
			`,
			expectedTypes: map[string]string{
				"pickFirst": "fn <'a>([a: mut 'a {x: number}, b: mut {x: number}]) -> mut 'a {x: number}",
			},
		},
		"MutuallyRecursiveBaseAndRecursive": {
			// Phase 8.7 (best-effort fixed-point): two mutually-recursive
			// functions where one has a base-case `return p` and the
			// other only forwards via the recursive call. After the
			// re-run pass, the forwarding function picks up the lifetime
			// from its peer.
			//
			// (The processing order within an SCC is not guaranteed, so
			// this test is robust against either order.)
			input: `
				fn even(p: mut {x: number}, n: number) -> mut {x: number} {
					if n == 0 { return p }
					return odd(p, n - 1)
				}
				fn odd(p: mut {x: number}, n: number) -> mut {x: number} {
					return even(p, n)
				}
			`,
			expectedTypes: map[string]string{
				"even": "fn <'a>(p: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
				"odd":  "fn <'a>(p: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
			},
		},
		"MutuallyRecursiveThreeCycle": {
			// Phase 8.7 (fixed-point): three mutually-recursive functions
			// where only one has a base-case `return p`. The two
			// forwarding functions can only acquire the lifetime once
			// their peer has it, so worst-case ordering needs more than
			// one re-run pass — exercising the fixed-point loop.
			input: `
				fn a(p: mut {x: number}, n: number) -> mut {x: number} {
					if n == 0 { return p }
					return b(p, n - 1)
				}
				fn b(p: mut {x: number}, n: number) -> mut {x: number} {
					return c(p, n)
				}
				fn c(p: mut {x: number}, n: number) -> mut {x: number} {
					return a(p, n)
				}
			`,
			expectedTypes: map[string]string{
				"a": "fn <'a>(p: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
				"b": "fn <'a>(p: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
				"c": "fn <'a>(p: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
			},
		},
		"GeneratorYieldsAliasParam": {
			// Phase 8.3: a generator that yields a parameter should
			// propagate the parameter's lifetime to the yield type T
			// inside Generator<T, _, _> (rather than to the Generator
			// container itself). Each yielded value carries the lifetime.
			input: `
				fn iter(p: mut {x: number}) {
					yield p
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a>(p: mut 'a {x: number}) -> Generator<mut 'a {x: number}, void, never>",
			},
		},
		"GeneratorReturnAliasParam": {
			// Phase 8.3: a generator's TReturn slot inherits the lifetime
			// of any parameter aliased by an explicit `return value`
			// path. The yield type T is unconstrained when no yields
			// alias parameters; the lifetime attaches only to TReturn.
			input: `
				fn iter(p: mut {x: number}) -> Generator<number, mut {x: number}, never> {
					yield 1
					return p
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a>(p: mut 'a {x: number}) -> Generator<number, mut 'a {x: number}, never>",
			},
		},
		"GeneratorYieldAndReturnAliasDistinctParams": {
			// Phase 8.3: yield T and TReturn are inferred independently.
			// A generator that yields one parameter and returns another
			// gets distinct lifetime variables on the two result
			// positions, each propagated to its own param.
			input: `
				fn iter(p: mut {x: number}, q: mut {x: number}) -> Generator<mut {x: number}, mut {x: number}, never> {
					yield p
					return q
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}) -> Generator<mut 'a {x: number}, mut 'b {x: number}, never>",
			},
		},
		"GeneratorYieldAndReturnAliasSameParam": {
			// Phase 8.3: when yield T and TReturn alias the SAME
			// parameter, the lifetime is allocated once and reused on
			// both result positions — `existingLV` reuse in
			// attachLifetimeToResult preserves pointer identity so the
			// signature shows a single 'a flowing to all three positions.
			input: `
				fn iter(p: mut {x: number}) -> Generator<mut {x: number}, mut {x: number}, never> {
					yield p
					return p
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a>(p: mut 'a {x: number}) -> Generator<mut 'a {x: number}, mut 'a {x: number}, never>",
			},
		},
		"GeneratorYieldFromAliasParam": {
			// Phase 8.3: `yield from iter` propagates the iterator's
			// lifetime to each delegated yield. When iter is a
			// parameter, the relay generator's yield T inherits the
			// parameter's lifetime — every value yielded through the
			// delegate borrows from iter, so its lifetime is bounded
			// by iter's.
			input: `
				fn relay(g: Generator<mut {x: number}, void, never>) {
					yield from g
				}
			`,
			expectedTypes: map[string]string{
				"relay": "fn <'a>(g: 'a Generator<mut 'a {x: number}, void, never>) -> Generator<mut 'a {x: number}, void, never>",
			},
		},
		"AsyncGeneratorYieldsAliasParam": {
			// Phase 8.3: an async generator (async fn containing yield)
			// should propagate the parameter's lifetime to the yield
			// type T inside AsyncGenerator<T, _, _>. The AsyncGenerator
			// container is freshly assembled per call so it has no
			// caller-provided lifetime — only its inner T does.
			input: `
				async fn iter(p: mut {x: number}) {
					yield p
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a>(p: mut 'a {x: number}) -> AsyncGenerator<mut 'a {x: number}, void, never>",
			},
		},
		"GeneratorYieldEscapingReturnAliasing": {
			// Phase 8.3 + 8.4 cross-cutting: a generator where one param
			// escapes into module-level state (forcing 'static) AND
			// another param is aliased on the TReturn path. The escape
			// detection runs once before either attachLifetimeToResult
			// call, so 'static is set on `p` up front; the yields call
			// then sees `returnHasStatic` and writes 'static onto yield T,
			// while the returns call independently allocates 'a for `q`
			// and writes it onto TReturn. Confirms the helper is safe to
			// invoke twice when one of the two paths escapes.
			input: `
				var cache: mut {x: number} = {x: 0}
				fn iter(p: mut {x: number}, q: mut {x: number}) -> Generator<mut {x: number}, mut {x: number}, never> {
					cache = p
					yield p
					return q
				}
			`,
			expectedTypes: map[string]string{
				"iter": "fn <'a>(p: mut 'static {x: number}, q: mut 'a {x: number}) -> Generator<mut 'static {x: number}, mut 'a {x: number}, never>",
			},
		},
		"RestParamReturnsElement": {
			// Phase 8.3: rest param `...args: Array<T>` — the lifetime-
			// bearing position is the *element* type T, not the array
			// container (the container is freshly assembled per call).
			// Returning args[0] must inherit that element-level lifetime.
			input: `
				fn first(...args: Array<mut {x: number}>) -> mut {x: number} {
					return args[0]
				}
			`,
			expectedTypes: map[string]string{
				"first": "fn <'a>(...args: Array<mut 'a {x: number}>) -> mut 'a {x: number}",
			},
		},
		"ObjectDestructuredParamFirstReturned": {
			// Phase 8.6: object-destructured param using shorthand
			// patterns. Each leaf's lifetime is resolved against the
			// corresponding property's type position; only the leaf
			// actually returned (`head`) gets a lifetime. Destructured
			// shorthand prints as `{key: <type>}` rather than carrying
			// the binding name through.
			input: `
				fn pickHead(
					{head, tail}: {head: mut {x: number}, tail: mut {x: number}},
				) -> mut {x: number} {
					return head
				}
			`,
			expectedTypes: map[string]string{
				"pickHead": "fn <'a>({head: mut 'a {x: number}, tail: mut {x: number}}) -> mut 'a {x: number}",
			},
		},
		"ObjectDestructuredParamWithRest": {
			// Regression test for the forEachLeafBinding refactor:
			// object-destructured params using ObjRestPat
			// (`{x, ...rest}`) must seed an alias entry for `rest` and
			// a unique VarID via the rename pass, even though
			// walkPatternForLeaves itself skips ObjRestPat for lifetime
			// attachment (a freshly-assembled rest object has no
			// caller-provided lifetime). Returning `x` still yields a
			// lifetime; the rest binding is type-inferred but doesn't
			// contribute one.
			input: `
				fn pickX(
					{x, ...rest}: {x: mut {a: number}, y: mut {a: number}},
				) -> mut {a: number} {
					return x
				}
			`,
			expectedTypes: map[string]string{
				"pickX": "fn <'a>({x: mut 'a {a: number}, ...rest}) -> mut 'a {a: number}",
			},
		},
		"ObjectDestructuredParamConditional": {
			// Phase 8.6: conditional return from an object-destructured
			// param produces a LifetimeUnion combining both leaves —
			// matching the per-position lifetime story of `Pair<'a, 'b>`
			// from Phase 8.6 but expressed through destructuring.
			input: `
				fn pickEither(
					{head, tail}: {head: mut {x: number}, tail: mut {x: number}},
					cond: boolean,
				) -> mut {x: number} {
					if cond { return head } else { return tail }
				}
			`,
			expectedTypes: map[string]string{
				"pickEither": "fn <'a, 'b>({head: mut 'a {x: number}, tail: mut 'b {x: number}}, cond: boolean) -> mut ('a | 'b) {x: number}",
			},
		},
		"TupleDestructuredParamConditional": {
			// Phase 8.3: conditional return from a tuple-destructured
			// param produces a LifetimeUnion combining both leaves.
			input: `
				fn pickEither([a, b]: [mut {x: number}, mut {x: number}], cond: boolean) -> mut {x: number} {
					if cond { return a } else { return b }
				}
			`,
			expectedTypes: map[string]string{
				"pickEither": "fn <'a, 'b>([a: mut 'a {x: number}, b: mut 'b {x: number}], cond: boolean) -> mut ('a | 'b) {x: number}",
			},
		},
		"EscapingRefViaCallResult": {
			// Bug B3: when a parameter flows through a function call into
			// module-level state (`cache = wrap(p)`), the escape-detection
			// pass must use the checker-aware alias source so the call's
			// return-aliases-its-arg lifetime info propagates. Without
			// this, `p` would not be marked as escaping and would not get
			// 'static, leaving the program unsound.
			input: `
				fn wrap(q: mut {x: number}) -> mut {x: number} { return q }
				var cache: mut {x: number} = {x: 0}
				fn store(p: mut {x: number}) -> number {
					cache = wrap(p)
					return p.x
				}
			`,
			expectedTypes: map[string]string{
				"store": "fn (p: mut 'static {x: number}) -> number",
			},
		},
		"AsyncReturnsParam": {
			// Design D6: by analogy with Generator handling, an async
			// function whose return aliases a parameter should attach
			// the lifetime to the inner T (the resolved value type)
			// rather than to the Promise container, which is freshly
			// allocated per call. The Promise container itself has no
			// caller-provided lifetime.
			input: `
				async fn passthrough(p: mut {x: number}) -> mut {x: number} { return p }
			`,
			expectedTypes: map[string]string{
				"passthrough": "fn <'a>(p: mut 'a {x: number}) -> Promise<mut 'a {x: number}, never>",
			},
		},
		"NonAsyncReturnsParamPromise": {
			// A non-async function whose parameter and return type are
			// both Promise<T> — the function passes the param through
			// without unwrapping. The Promise container itself IS the
			// parameter (not freshly assembled), so the lifetime should
			// attach to the Promise<T> reference at both the param and
			// return positions, NOT to the inner T (which would be the
			// case for an async function whose Promise is built from
			// the resolved T).
			input: `
				fn forward(p: Promise<mut {x: number}, never>) -> Promise<mut {x: number}, never> {
					return p
				}
			`,
			expectedTypes: map[string]string{
				"forward": "fn <'a>(p: 'a Promise<mut {x: number}, never>) -> 'a Promise<mut {x: number}, never>",
			},
		},
		"NonGeneratorReturnsParamGenerator": {
			// A plain function (no `yield`) whose parameter and return
			// type are both Generator<T,_,_> — the param is forwarded
			// directly. The Generator container IS the parameter, not
			// freshly assembled, so the lifetime should attach to the
			// Generator<T,_,_> reference at both positions, NOT to the
			// inner yield type T.
			input: `
				fn forwardIter(g: Generator<mut {x: number}, void, never>) -> Generator<mut {x: number}, void, never> {
					return g
				}
			`,
			expectedTypes: map[string]string{
				"forwardIter": "fn <'a>(g: 'a Generator<mut {x: number}, void, never>) -> 'a Generator<mut {x: number}, void, never>",
			},
		},
		"TupleRestParamReturnsRestElement": {
			// Tuple-destructured rest pattern: `[head, ...tail]` paired
			// with a tuple type that has a rest spread `[T, ...Array<T>]`.
			// Returning `tail[0]` must give the rest binding `tail` a
			// lifetime — the destructured tuple-rest's element is a
			// caller-provided position, not freshly assembled.
			input: `
				fn pickRest([head, ...tail]: [mut {x: number}, ...Array<mut {x: number}>]) -> mut {x: number} {
					return tail[0]
				}
			`,
			expectedTypes: map[string]string{
				"pickRest": "fn <'a>([head: mut {x: number}, ...tail: Array<mut 'a {x: number}>]) -> mut 'a {x: number}",
			},
		},
		"MutuallyRecursivePartialFirstPass": {
			// Bug B4: in a true mutual-recursion cycle, the function with
			// the base case (`even` here, via `return p`) infers a non-
			// empty LifetimeParams on its first pass — but only for the
			// directly-returned param. The forwarding return path
			// (`return odd(q, n - 1)`) cannot detect that `q` is also
			// aliased through the call until `odd`'s signature has a
			// lifetime, which only happens AFTER even is first processed.
			// The re-run must re-examine `even` and add `q`'s lifetime;
			// the existing early-return guard incorrectly skips it.
			input: `
				fn even(p: mut {x: number}, q: mut {x: number}, n: number) -> mut {x: number} {
					if n == 0 { return p }
					return odd(q, n - 1)
				}
				fn odd(q: mut {x: number}, n: number) -> mut {x: number} {
					return even(q, q, n - 1)
				}
			`,
			expectedTypes: map[string]string{
				"even": "fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}, n: number) -> mut ('a | 'b) {x: number}",
				"odd":  "fn <'a>(q: mut 'a {x: number}, n: number) -> mut 'a {x: number}",
			},
		},
		"TupleOfTwoParams_ElementLevelUnion": {
			// Phase 8.9: a fresh tuple literal `[a, b]` typed as a
			// homogeneous Array<T> at the slot — both element-level
			// lifetimes collapse onto the same Array<T>'s element type
			// and union there.
			input: `
				fn pair(a: mut {x: number}, b: mut {x: number}) -> mut Array<mut {x: number}> {
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"pair": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> mut Array<mut ('a | 'b) {x: number}>",
			},
		},
		"ObjectLiteral_PropertyLevelDistinctLifetimes": {
			// Phase 8.9: object literal with distinct slots produces
			// distinct property-level lifetimes on the result.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
			`,
			expectedTypes: map[string]string{
				"wrap": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {head: mut 'a {x: number}, tail: mut 'b {x: number}}",
			},
		},
		"TupleOfTwoParams_PerSlotDistinctLifetimes": {
			// Tuple-typed return (not Array) preserves per-index
			// lifetimes — `'a` and `'b` stay separate at slots 0 and
			// 1 rather than collapsing into a union.
			input: `
				fn pair(a: mut {x: number}, b: mut {x: number}) -> [mut {x: number}, mut {x: number}] {
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"pair": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> [mut 'a {x: number}, mut 'b {x: number}]",
			},
		},
		"PassThroughCallWithEmbeddedReturnLifetimes": {
			// Fix from determineCheckerAliasSource: when the callee's
			// return type embeds lifetimes inside slots (rather than
			// at the top), the call result surfaces those slots as
			// fresh-rooted leaves with per-slot paths. The caller's
			// signature inference uses those leaves to drive per-slot
			// lifetime attachment in its own return type.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
				fn passThrough(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return wrap(a, b)
				}
			`,
			expectedTypes: map[string]string{
				"passThrough": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {head: mut 'a {x: number}, tail: mut 'b {x: number}}",
			},
		},
		"PassThroughCallWithNumericKeyedEmbeddedLifetimes": {
			// Regression for the walkReturnLifetimeSlots fix: when the
			// callee's return type embeds lifetimes inside numeric-keyed
			// slots (`{0: 'a P, 1: 'b P}`), walkReturnLifetimeSlots must
			// emit PropertyOf steps with FormatNumKey-encoded keys so
			// that embeddedLifetimeAliasSource can match them back to
			// the corresponding arg leaves. Before the fix the numeric
			// keys were skipped, the call's slots were lost, and the
			// caller's signature collapsed to no inferred lifetimes.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {0: mut {x: number}, 1: mut {x: number}} {
					return {0: a, 1: b}
				}
				fn passThrough(a: mut {x: number}, b: mut {x: number}) -> {0: mut {x: number}, 1: mut {x: number}} {
					return wrap(a, b)
				}
			`,
			expectedTypes: map[string]string{
				"passThrough": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {0: mut 'a {x: number}, 1: mut 'b {x: number}}",
			},
		},
		"PropertyAccess_ProjectsPerSlotLifetime": {
			// Caller-side aliasing precision: accessing a single slot
			// of a per-property-typed result projects only that slot's
			// lifetime — `wrap(a, b).head` carries `'a`, not the union,
			// so `pickHead`'s signature ties its return to `a` only.
			// Implemented via bidirectional path semantics in
			// liveness.ProjectStep: fresh-rooted leaves with per-slot
			// paths get filtered by the matching front step on
			// MemberExpr/IndexExpr descent.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
				fn pickHead(a: mut {x: number}, b: mut {x: number}) -> mut {x: number} {
					return wrap(a, b).head
				}
			`,
			expectedTypes: map[string]string{
				"pickHead": "fn <'a>(a: mut 'a {x: number}, b: mut {x: number}) -> mut 'a {x: number}",
			},
		},
		"PropertyAccess_CastedProjection": {
			// Exercises the TypeCastExpr branch in
			// determineCheckerAliasSource: a cast on the call result
			// must pass through the per-slot leaves so the subsequent
			// `.head` projection still narrows to `'a` only.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
				fn pickHeadCast(a: mut {x: number}, b: mut {x: number}) -> mut {x: number} {
					return (wrap(a, b): {head: mut {x: number}, tail: mut {x: number}}).head
				}
			`,
			expectedTypes: map[string]string{
				"pickHeadCast": "fn <'a>(a: mut 'a {x: number}, b: mut {x: number}) -> mut 'a {x: number}",
			},
		},
		"IndexAccess_ProjectsPerSlotLifetime": {
			// Tuple-slot counterpart to PropertyAccess_ProjectsPerSlotLifetime:
			// `pair(a, b)[0]` carries only `'a`, so `pickFirst` ties
			// its return to `a` only.
			input: `
				fn pair(a: mut {x: number}, b: mut {x: number}) -> [mut {x: number}, mut {x: number}] {
					return [a, b]
				}
				fn pickFirst(a: mut {x: number}, b: mut {x: number}) -> mut {x: number} {
					return pair(a, b)[0]
				}
			`,
			expectedTypes: map[string]string{
				"pickFirst": "fn <'a>(a: mut 'a {x: number}, b: mut {x: number}) -> mut 'a {x: number}",
			},
		},
		"PerSlotEscape_OnlyEscapingPropertyGetsStatic": {
			// Benefit: fewer false-positive escape errors at the
			// property level. The single object-destructured param
			// `{a, b}` has two leaves at distinct property slots;
			// escaping only `a` should pin `'static` onto the `a`
			// slot of the param's type while leaving the `b` slot
			// with its own lifetime variable. Without per-property
			// granularity the whole param object would widen to
			// `'static`, dragging `b` along with it.
			input: `
				var cache: mut {slot: mut {x: number}} = {slot: {x: 0}}
				fn stash(
					{a, b}: {a: mut {x: number}, b: mut {x: number}},
				) -> mut {x: number} {
					cache.slot = a
					return b
				}
			`,
			expectedTypes: map[string]string{
				"stash": "fn <'a>({a: mut 'static {x: number}, b: mut 'a {x: number}}) -> mut 'a {x: number}",
			},
		},
		"NumericKeyedObjectCapturesParam": {
			// Regression for the stepIntoSlot fix: when a fresh object
			// literal uses a numeric key (`{0: p}`), the producer emits
			// PropertyOf{Key: "0"} on the leaf path. The consumer must
			// match that string against the corresponding NumObjTypeKeyKind
			// property on the return type. Before the fix, only
			// StrObjTypeKeyKind properties matched, so the leaf was
			// dropped and no lifetime was attached to the `0` slot.
			input: `
				fn wrap(p: mut {x: number}) -> {0: mut {x: number}} {
					return {0: p}
				}
			`,
			expectedTypes: map[string]string{
				"wrap": "fn <'a>(p: mut 'a {x: number}) -> {0: mut 'a {x: number}}",
			},
		},
		"MixedNumericAndStringKeysCapturesDifferentParams": {
			// Two params land in two slots of the fresh object, one keyed
			// numerically and one keyed by name. Each gets its own
			// lifetime, exercising both the NumObjTypeKeyKind and the
			// StrObjTypeKeyKind arms of stepIntoSlot.PropertyOf in the
			// same return type.
			input: `
				fn pair(a: mut {x: number}, b: mut {x: number}) -> {0: mut {x: number}, name: mut {x: number}} {
					return {0: a, name: b}
				}
			`,
			expectedTypes: map[string]string{
				"pair": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {0: mut 'a {x: number}, name: mut 'b {x: number}}",
			},
		},
		"AsyncFn_IdentityCarriesLifetimeIntoPromise": {
			// An async fn that returns its mutable param should infer
			// a lifetime on that param and propagate it into the
			// Promise<T> wrapper around the return type. The unwrap
			// of Promise<T> for inference happens in inferLifetimesCore
			// based on async mode.
			input: `
				async fn identity(p: mut {x: number}) -> mut {x: number} {
					return p
				}
			`,
			expectedTypes: map[string]string{
				"identity": "fn <'a>(p: mut 'a {x: number}) -> Promise<mut 'a {x: number}, never>",
			},
		},
		"AsyncFn_ProjectsPerSlotLifetimeIntoPromise": {
			// Composite-return async fn: the per-slot leaves produced
			// by the object literal should drive per-property lifetime
			// attachment inside the Promise<T> wrapper.
			input: `
				async fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
			`,
			expectedTypes: map[string]string{
				"wrap": "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> Promise<{head: mut 'a {x: number}, tail: mut 'b {x: number}}, never>",
			},
		},
		"AsyncFn_AwaitedReturnPropagatesLifetime_KnownGap": {
			// An async caller that returns `await callee(p)` where
			// callee is `async fn(p: mut T) -> mut T` should — once
			// Promise<T> descent is implemented in
			// collectReturnLifetimeSlots — tie the caller's return to
			// `p`'s lifetime. Today the call's Promise<T> return type
			// isn't descended into, so no per-slot leaves are produced
			// and the caller's signature collapses to no inferred
			// lifetimes. Pinning current behavior; tracked in #544.
			//
			// The AwaitExpr branch in determineCheckerAliasSource is
			// already in place, so once #544 lands and the call
			// produces leaves, ProjectStep will consume the AwaitOf
			// step from each leaf and surface the underlying root.
			input: `
				async fn identity(p: mut {x: number}) -> mut {x: number} {
					return p
				}
				async fn caller(p: mut {x: number}) -> mut {x: number} {
					return await identity(p)
				}
			`,
			expectedTypes: map[string]string{
				// Target (post-#544): "fn <'a>(p: mut 'a {x: number}) -> Promise<mut 'a {x: number}, never>"
				"caller": "fn (p: mut {x: number}) -> Promise<mut 'a {x: number}, never>",
			},
		},
		"NestedObjectInArray_DescendsTwoSteps": {
			// Phase 8.9: a leaf with path [IndexOf 0, PropertyOf "inner"]
			// drives lifetime attachment to the inner property of the
			// array's element type.
			input: `
				fn box(p: mut {x: number}) -> Array<{inner: mut {x: number}}> {
					return [{inner: p}]
				}
			`,
			expectedTypes: map[string]string{
				"box": "fn <'a>(p: mut 'a {x: number}) -> Array<{inner: mut 'a {x: number}}>",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			for varName, want := range test.expectedTypes {
				got, ok := actual[varName]
				require.Truef(t, ok, "binding %q not found", varName)
				assert.Equalf(t, want, got,
					"unexpected type for %q", varName)
			}
		})
	}
}

// TestInferConstructorLifetimeTypes asserts the printed constructor type
// for classes, including any lifetime parameters introduced by stored
// reference fields.
func TestInferConstructorLifetimeTypes(t *testing.T) {
	tests := map[string]struct {
		input                     string
		expectedTypes             map[string]string
		expectedInstanceType      map[string]string
		expectedInstanceLifetimes map[string]int
	}{
		"ContainerStoresMutRef": {
			input: `
				class Container { item: mut {x: number} }
			`,
			expectedTypes: map[string]string{
				"Container": "{new fn <'a>(item: mut 'a {x: number}) -> Container<'a>}",
			},
		},
		"PointPrimitivesNoLifetime": {
			input: `
				class Point { x: number, y: number }
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> Point}",
			},
		},
		"PairOfRefs": {
			input: `
				class Pair { first: mut {x: number}, second: mut {x: number} }
			`,
			expectedTypes: map[string]string{
				"Pair": "{new fn <'a, 'b>(first: mut 'a {x: number}, second: mut 'b {x: number}) -> Pair<'a, 'b>}",
			},
		},
		"GuardOnlyParamNotCaptured": {
			// A param read by a guard but never stored into self should
			// NOT pin a lifetime onto the constructor — there's no
			// reachable state that aliases it after the constructor
			// returns. (#531)
			input: `
				class C {
					n: number,
					constructor(mut self, p: mut {x: number}) {
						self.n = 0
						if p.x < 0 {
							self.n = 1
						}
					}
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn (p: mut {x: number}) -> C}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 0,
			},
		},
		"DerivedValueParamNotCaptured": {
			// A param used purely to derive a fresh value and store
			// THAT fresh value should not pin a lifetime onto the param
			// itself. (#531)
			input: `
				class C {
					n: number,
					constructor(mut self, p: {x: number}) {
						self.n = p.x + 1
					}
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn (p: {x: number}) -> C}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 0,
			},
		},
		"CtorStoresTupleOfParams_FieldSlotsCarryLifetimes": {
			// Phase 8.9 (e) deeper goal: when params escape into a
			// composite field, the field's per-slot types in the
			// instance should reflect the per-slot lifetimes — e.g.
			// `items[0]: mut 'a {...}`, `items[1]: mut 'b {...}` after
			// `self.items = [a, b]`. With per-slot fresh-var tuple
			// inference (option 1), the constructor's `'a`/`'b` flow
			// through the `[a, b]` literal's slot vars into the field
			// type, so each instance's `p.items` carries per-slot
			// lifetimes from its constructor args.
			input: `
				class Pair {
					items: [mut {x: number}, mut {x: number}],
					constructor(mut self, a, b) {
						self.items = [a, b]
					}
				}
				val p = Pair({x: 1}, {x: 2})
				val items = p.items
			`,
			expectedTypes: map[string]string{
				"items": "[mut 'a {x: number}, mut 'b {x: number}]",
			},
		},
		"CtorStoresTupleOfParams_EscapesBoth": {
			// Phase 8.9: `self.items = [a, b]` — the RHS is a fresh
			// tuple whose leaves both escape into self. Both params
			// must pin lifetimes onto the ctor; today's escape
			// detector consults src.Kind() which returns Fresh for
			// fresh-rooted sources, so without path-based escape
			// recognition only the alias-rooted RHS (e.g. `self.x = a`)
			// is detected. This case pins down the new behavior.
			// Constructor params are unannotated; their types and
			// lifetimes are inferred from the field they store into.
			input: `
				class Pair {
					items: [mut {x: number}, mut {x: number}],
					constructor(mut self, a, b) {
						self.items = [a, b]
					}
				}
			`,
			expectedTypes: map[string]string{
				"Pair": "{new fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> Pair<'a, 'b>}",
			},
			expectedInstanceLifetimes: map[string]int{
				"Pair": 2,
			},
		},
		"CtorStoresObjectOfParams_EscapesBoth": {
			// Sister case to CtorStoresTupleOfParams: object-literal
			// RHS leaves are also fresh-rooted with PropertyOf paths.
			// Both params should pin distinct lifetimes onto the ctor.
			// Constructor params are unannotated; their types and
			// lifetimes are inferred from the field they store into.
			input: `
				class Wrap {
					pair: {head: mut {x: number}, tail: mut {x: number}},
					constructor(mut self, a, b) {
						self.pair = {head: a, tail: b}
					}
				}
			`,
			expectedTypes: map[string]string{
				"Wrap": "{new fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> Wrap<'a, 'b>}",
			},
			expectedInstanceLifetimes: map[string]int{
				"Wrap": 2,
			},
		},
		"CtorStoresTupleOfAnnotatedParams_FieldSlotsCarryLifetimes": {
			// (#539) When ctor params are explicitly annotated, the param
			// types and the field-slot types are distinct pointers, so
			// `setLifetimeOnType` on the param leaf does not surface to
			// the field. A separate per-slot pass walks `self.<f> = expr`
			// and attaches lifetimes to the matching slots in the field
			// type.
			input: `
				class Pair {
					items: [mut {x: number}, mut {x: number}],
					constructor(mut self, a: mut {x: number}, b: mut {x: number}) {
						self.items = [a, b]
					}
				}
				val p = Pair({x: 1}, {x: 2})
				val items = p.items
			`,
			expectedTypes: map[string]string{
				"items": "[mut 'a {x: number}, mut 'b {x: number}]",
			},
		},
		"CtorStoresObjectOfAnnotatedParams_FieldSlotsCarryLifetimes": {
			// (#539) Sister case for object-literal RHS with explicitly
			// annotated params.
			input: `
				class Wrap {
					pair: {head: mut {x: number}, tail: mut {x: number}},
					constructor(mut self, a: mut {x: number}, b: mut {x: number}) {
						self.pair = {head: a, tail: b}
					}
				}
				val w = Wrap({x: 1}, {x: 2})
				val pair = w.pair
			`,
			expectedTypes: map[string]string{
				"pair": "{head: mut 'a {x: number}, tail: mut 'b {x: number}}",
			},
		},
		"CtorStoresTupleOfAnnotatedParams_CallResult_FieldSlotsCarryLifetimes": {
			// (#539) Like CtorStoresTupleOfAnnotatedParams_FieldSlotsCarryLifetimes,
			// but the RHS of `self.items = …` is a call result rather than
			// a tuple literal. Exercises the determineCheckerAliasSource
			// path in attachFieldSlotLifetimes — the call's per-slot leaves
			// must drive per-slot lifetime attachment on the field type.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> [mut {x: number}, mut {x: number}] {
					return [a, b]
				}
				class Pair {
					items: [mut {x: number}, mut {x: number}],
					constructor(mut self, a: mut {x: number}, b: mut {x: number}) {
						self.items = wrap(a, b)
					}
				}
				val p = Pair({x: 1}, {x: 2})
				val items = p.items
			`,
			expectedTypes: map[string]string{
				"items": "[mut 'a {x: number}, mut 'b {x: number}]",
			},
		},
		"CtorStoresObjectOfAnnotatedParams_CallResult_FieldSlotsCarryLifetimes": {
			// (#539) Sister case to the tuple call-result variant: the RHS
			// of `self.pair = …` is a call returning a per-property typed
			// object, and the call's embedded slot lifetimes must surface
			// at the corresponding field slots.
			input: `
				fn wrap(a: mut {x: number}, b: mut {x: number}) -> {head: mut {x: number}, tail: mut {x: number}} {
					return {head: a, tail: b}
				}
				class Wrap {
					pair: {head: mut {x: number}, tail: mut {x: number}},
					constructor(mut self, a: mut {x: number}, b: mut {x: number}) {
						self.pair = wrap(a, b)
					}
				}
				val w = Wrap({x: 1}, {x: 2})
				val pair = w.pair
			`,
			expectedTypes: map[string]string{
				"pair": "{head: mut 'a {x: number}, tail: mut 'b {x: number}}",
			},
		},
		"DestructuredTupleCtorParamCapture": {
			// A destructured tuple ctor param whose leaves are stored
			// into self should still pin lifetimes onto the param. (#531)
			input: `
				class C {
					a: mut {x: number},
					constructor(mut self, [a, b]: [mut {x: number}, mut {x: number}]) {
						self.a = a
					}
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn <'a>([a: mut 'a {x: number}, b: mut {x: number}]) -> C<'a>}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 1,
			},
		},
		"GetterCapturesConstructorParam": {
			// A getter whose body references a constructor param by name
			// (made visible via the synthesized `self.p = p` body) forces
			// a lifetime onto the constructor param.
			input: `
				class C {
					p: mut {x: number},
					get q(self) -> mut {x: number} { return self.p },
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn <'a>(p: mut 'a {x: number}) -> C<'a>}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 1,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			for varName, want := range test.expectedTypes {
				got, ok := actual[varName]
				require.Truef(t, ok, "binding %q not found", varName)
				assert.Equalf(t, want, got,
					"unexpected type for %q", varName)
			}
			for typeName, want := range test.expectedInstanceType {
				alias, ok := ns.Types[typeName]
				require.Truef(t, ok, "type alias %q not found", typeName)
				assert.Equalf(t, want, alias.Type.String(),
					"unexpected instance type for %q", typeName)
			}
			for typeName, want := range test.expectedInstanceLifetimes {
				alias, ok := ns.Types[typeName]
				require.Truef(t, ok, "type alias %q not found", typeName)
				assert.Lenf(t, alias.LifetimeParams, want,
					"unexpected lifetime-param count on type alias %q",
					typeName)
			}
		})
	}
}

// TestCtorCapturesViaWrappingCall: `self.f = wrap(p)` pins p's lifetime
// onto the ctor because wrap's return aliases its argument. Works because
// InferConstructorLifetimes runs in the body phase (after wrap's signature
// has been resolved), letting determineCheckerAliasSource trace through
// the call. (#531)
func TestCtorCapturesViaWrappingCall(t *testing.T) {
	t.Parallel()
	ns := mustInferAsModule(t, `
		fn wrap(x: mut {y: number}) -> mut {y: number} { return x }
		class C {
			f: mut {y: number},
			constructor(mut self, p: mut {y: number}) {
				self.f = wrap(p)
			}
		}
	`)
	actual := collectBindingTypes(ns)
	got, ok := actual["C"]
	require.True(t, ok, "binding C not found")
	assert.Equal(t, "{new fn <'a>(p: mut 'a {y: number}) -> C<'a>}", got)
}

// TestCallSiteAliasFromLifetimeUnion exercises the LifetimeUnion call-site
// path: a function whose return aliases either of two parameters
// (`('a | 'b)`) is called, the result variable joins the alias sets of
// BOTH arguments, and a later immutable read of either arg while the
// mutable result is live produces a transition error.
func TestCallSiteAliasFromLifetimeUnion(t *testing.T) {
	t.Parallel()
	mutErrors := mustInferScriptMutErrors(t, `
		fn pick(a: mut {x: number}, b: mut {x: number}, cond: boolean) -> mut {x: number} {
			if cond { return a } else { return b }
		}
		fn test() {
			val p: mut {x: number} = {x: 0}
			val q: mut {x: number} = {x: 1}
			val r: mut {x: number} = pick(p, q, true)
			val frozenP: {x: number} = p
			r.x = 5
			frozenP
		}
	`)
	require.Len(t, mutErrors, 1)
	assert.Contains(t, mutErrors[0], "cannot assign 'p' to immutable 'frozenP'")
}

// TestCallSiteAliasFromInferredLifetime exercises the end-to-end path:
// a function whose return aliases its parameter (inferred lifetime) is
// called, the result variable joins the parameter's alias set, and a
// later mutation through the result while the parameter is read as
// immutable produces a transition error.
func TestCallSiteAliasFromInferredLifetime(t *testing.T) {
	t.Parallel()
	mutErrors := mustInferScriptMutErrors(t, `
		fn identity(p: mut {x: number}) -> mut {x: number} { return p }
		fn test() {
			val p: mut {x: number} = {x: 0}
			val r: mut {x: number} = identity(p)
			val q: {x: number} = p
			r.x = 5
			q
		}
	`)
	require.Len(t, mutErrors, 1)
	assert.Contains(t, mutErrors[0], "cannot assign 'p' to immutable 'q'")
}

// TestDestructuredParamLeafSeededInAliasTracker verifies that when a
// function parameter is a destructured pattern (object or tuple), each
// leaf binding is seeded into the alias tracker so that subsequent
// aliases of the leaf participate in mutability transition checks.
//
// Previously the prepass only seeded top-level IdentPat params, leaving
// destructured leaves (e.g. `head` in `{head, tail}: ...`) without an
// alias set. AddAlias against a leaf without a set is a silent no-op, so
// transitions involving the leaf were not detected.
func TestDestructuredParamLeafSeededInAliasTracker(t *testing.T) {
	t.Parallel()
	mutErrors := mustInferScriptMutErrors(t, `
		fn identity(p: mut {x: number}) -> mut {x: number} { return p }
		fn test({head, tail}: {head: mut {x: number}, tail: mut {x: number}}) {
			val r: mut {x: number} = identity(head)
			val q: {x: number} = head
			r.x = 5
			q
		}
	`)
	require.Len(t, mutErrors, 1)
	assert.Contains(t, mutErrors[0], "cannot assign 'head' to immutable 'q'")
}

// TestCallSiteStaticEscapePropagation exercises Phase 8.5 step 4: when a
// callee has a `'static` parameter, the call records a permanent escape
// on the corresponding argument's alias sets. Subsequent mut↔immut
// transitions on the argument must observe the escape as an always-live
// alias of the escaped mutability.
func TestCallSiteStaticEscapePropagation(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		// A `mut 'static` escape on the argument blocks a later attempt
		// to freeze it as immutable: the permanent external mutable
		// reference could mutate the value while the immutable view
		// assumes it is unchanged.
		"MutStaticBlocksImmutableAlias": {
			input: `
				var cache: mut {x: number} = {x: 0}
				fn cacheItem(item: mut {x: number}) -> number {
					cache = item
					return item.x
				}
				fn test() {
					val p: mut {x: number} = {x: 0}
					cacheItem(p)
					val frozen: {x: number} = p
					frozen
				}
			`,
			expectedErrors: []string{
				"cannot assign 'p' to immutable 'frozen': a `'static` escape still has mutable access to 'p' after this point",
			},
		},
		// Symmetric immutable case: an immutable `'static` escape on the
		// argument blocks a later attempt to upgrade it to a mutable
		// view. Closes the symmetric gap to MutStaticBlocksImmutableAlias.
		"ImmStaticBlocksMutableAlias": {
			input: `
				var cache: {x: number} = {x: 0}
				fn cacheItem(item: {x: number}) -> number {
					cache = item
					return item.x
				}
				fn test() {
					val p: {x: number} = {x: 0}
					cacheItem(p)
					val mutp: mut {x: number} = p
					mutp.x = 5
					mutp
				}
			`,
			expectedErrors: []string{
				"cannot assign 'p' to mutable 'mutp': a `'static` escape still has immutable access to 'p' after this point",
			},
		},
		// The caller-side escape marking does NOT block legitimate
		// mutable use of the argument after the call — the value is
		// still mutable for the caller, just permanently aliased.
		"AllowsMutableUseAfterCall": {
			input: `
				var cache: mut {x: number} = {x: 0}
				fn cacheItem(item: mut {x: number}) -> number {
					cache = item
					return item.x
				}
				fn test() {
					val p: mut {x: number} = {x: 0}
					cacheItem(p)
					p.x = 5
					p
				}
			`,
		},
		// When a callee has a rest parameter whose elements escape to
		// `'static`, *every* variadic argument (not just the first)
		// must be marked. Otherwise later args silently violate the
		// mut→immut transition rule.
		"RestParamMarksAllArgs": {
			input: `
				var cache: mut Array<mut {x: number}> = []
				fn cacheAll(...items: Array<mut {x: number}>) -> number {
					cache = items
					return 0
				}
				fn test() {
					val a: mut {x: number} = {x: 0}
					val b: mut {x: number} = {x: 1}
					cacheAll(a, b)
					val frozenB: {x: number} = b
					frozenB
				}
			`,
			expectedErrors: []string{
				"cannot assign 'b' to immutable 'frozenB': a `'static` escape still has mutable access to 'b' after this point",
			},
		},
		// Caller-side propagation considers per-leaf `'static` lifetimes
		// on a tuple-destructured parameter, not just the top-level
		// tuple lifetime. Without leaf-level walking, a callee that
		// escapes a single tuple element silently drops the marking.
		"TupleDestructuredLeafEscape": {
			input: `
				var cache: mut {x: number} = {x: 0}
				fn cacheFirst([a, b]: [mut {x: number}, mut {x: number}]) -> number {
					cache = a
					return 0
				}
				fn test() {
					val pair: mut [mut {x: number}, mut {x: number}] = [{x: 0}, {x: 1}]
					cacheFirst(pair)
					val frozen: [{x: number}, {x: number}] = pair
					frozen
				}
			`,
			expectedErrors: []string{
				"cannot assign 'pair' to immutable 'frozen': a `'static` escape still has mutable access to 'pair' after this point",
			},
		},
		// When a static-escape parameter is filled by a nested call
		// whose return aliases the inner argument, propagation still
		// records the escape on that underlying argument. Without
		// checker-aware alias-source resolution, the inner call's
		// return looks "fresh" and the escape silently disappears.
		"PropagatesThroughIdentityCall": {
			input: `
				var cache: mut {x: number} = {x: 0}
				fn cacheItem(item: mut {x: number}) -> number {
					cache = item
					return item.x
				}
				fn id(p: mut {x: number}) -> mut {x: number} { return p }
				fn test() {
					val p: mut {x: number} = {x: 0}
					cacheItem(id(p))
					val frozen: {x: number} = p
					frozen
				}
			`,
			expectedErrors: []string{
				"cannot assign 'p' to immutable 'frozen': a `'static` escape still has mutable access to 'p' after this point",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mutErrors := mustInferScriptMutErrors(t, tc.input)
			assert.Equal(t, tc.expectedErrors, mutErrors)
		})
	}
}

// TestCallSiteNoAliasForFreshReturn verifies that a function returning a
// fresh value does NOT cause its argument and the result to share an
// alias set.
func TestCallSiteNoAliasForFreshReturn(t *testing.T) {
	t.Parallel()
	mutErrors := mustInferScriptMutErrors(t, `
		fn clone(p: {x: number}) -> mut {x: number} { return {x: p.x} }
		fn test() {
			val p: mut {x: number} = {x: 0}
			val r: mut {x: number} = clone(p)
			val q: {x: number} = p
			r.x = 5
			q
		}
	`)
	assert.Empty(t, mutErrors,
		"clone returns a fresh value, so r should not alias p")
}

// mustInferAsModule parses and type-checks the given input as a module
// (so class declarations are accepted), failing the test on any non-
// mutability error. Returns the top-level namespace.
func mustInferAsModule(t *testing.T, input string) *type_system.Namespace {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	inferErrors := c.InferModule(inferCtx, module)

	for _, err := range inferErrors {
		if _, ok := err.(*MutabilityTransitionError); ok {
			continue
		}
		t.Fatalf("unexpected non-mutability error: %s", err.Message())
	}
	return inferCtx.Scope.Namespace
}

// mustInferScriptMutErrors parses and type-checks the given input as a
// script, returning the formatted MutabilityTransitionError messages.
// Other errors fail the test.
func mustInferScriptMutErrors(t *testing.T, input string) []string {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrors := c.InferScript(inferCtx, script)

	var mutErrors []string
	for _, err := range inferErrors {
		if mutErr, ok := err.(*MutabilityTransitionError); ok {
			mutErrors = append(mutErrors, mutErr.Message())
			continue
		}
		t.Fatalf("unexpected non-mutability error: %s", err.Message())
	}
	return mutErrors
}

// collectBindingTypes returns a map from binding name to the printed
// type string for every value in the namespace.
func collectBindingTypes(ns *type_system.Namespace) map[string]string {
	out := make(map[string]string)
	for name, binding := range ns.Values {
		out[name] = binding.Type.String()
	}
	return out
}

// assertExpectedErrors asserts that actualErrors contains exactly one entry
// per substring in expected, in order, with each substring appearing in the
// corresponding error's message. An empty (or nil) expected asserts no
// errors were produced.
func assertExpectedErrors(t *testing.T, expected []string, actualErrors []Error) {
	t.Helper()
	if len(expected) == 0 {
		if len(actualErrors) > 0 {
			for i, err := range actualErrors {
				t.Logf("Unexpected Error[%d]: %s", i, err.Message())
			}
		}
		assert.Empty(t, actualErrors, "expected no inference errors")
		return
	}
	if !assert.Equalf(t, len(expected), len(actualErrors),
		"expected %d errors, got %d", len(expected), len(actualErrors)) {
		for i, err := range actualErrors {
			t.Logf("Error[%d]: %s", i, err.Message())
		}
		return
	}
	for i, want := range expected {
		assert.Containsf(t, actualErrors[i].Message(), want,
			"error[%d] message %q should contain %q",
			i, actualErrors[i].Message(), want)
	}
}

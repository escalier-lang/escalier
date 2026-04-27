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
				class Container(item: mut {x: number}) { item, }
			`,
			expectedTypes: map[string]string{
				"Container": "{new fn <'a>(item: mut 'a {x: number}) -> Container<'a>}",
			},
		},
		"PointPrimitivesNoLifetime": {
			input: `
				class Point(x: number, y: number) { x, y, }
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> Point}",
			},
		},
		"PairOfRefs": {
			input: `
				class Pair(first: mut {x: number}, second: mut {x: number}) {
					first, second,
				}
			`,
			expectedTypes: map[string]string{
				"Pair": "{new fn <'a, 'b>(first: mut 'a {x: number}, second: mut 'b {x: number}) -> Pair<'a, 'b>}",
			},
		},
		"ShorthandWithDefaultStoresParam": {
			input: `
				class Container(item: mut {x: number}) {
					item = {x: 0},
				}
			`,
			expectedTypes: map[string]string{
				"Container": "{new fn <'a>(item: mut 'a {x: number}) -> Container<'a>}",
			},
		},
		"FieldWithMemberAccessOfParam": {
			// Phase 8.6 (#6): non-identity initializer that reaches into a
			// param via a property access still captures the param.
			input: `
				class Wrap(p: mut {x: {y: number}}) {
					inner: p.x,
				}
			`,
			expectedTypes: map[string]string{
				"Wrap": "{new fn <'a>(p: mut 'a {x: {y: number}}) -> Wrap<'a>}",
			},
		},
		"FieldWithObjectLiteralCapturingParam": {
			// Phase 8.6 (#5): nested object literal in a field initializer.
			input: `
				class Wrap(p: mut {x: number}) {
					inner: {nested: p},
				}
			`,
			expectedTypes: map[string]string{
				"Wrap": "{new fn <'a>(p: mut 'a {x: number}) -> Wrap<'a>}",
			},
		},
		"FieldWithTupleLiteralCapturingParam": {
			// Phase 8.6 (#5): tuple/array literal in a field initializer.
			input: `
				class Wrap(p: mut {x: number}, q: mut {x: number}) {
					pair: [p, q],
				}
			`,
			expectedTypes: map[string]string{
				"Wrap": "{new fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}) -> Wrap<'a, 'b>}",
			},
		},
		"MethodBodyShadowedParamNotCaptured": {
			// Phase 8.6 (#4): a method with its own param named `p` must
			// not be treated as capturing the constructor's `p` — the
			// inner param shadows the outer name within the method body.
			input: `
				class C(p: mut {x: number}) {
					foo(self, p: mut {x: number}) -> mut {x: number} { return p }
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn (p: mut {x: number}) -> C}",
			},
			expectedInstanceType: map[string]string{
				// `foo`'s own `p` parameter independently gets a
				// fresh 'a (the method threads its arg to its
				// return). The class type alias itself has zero
				// lifetime params, confirming no capture from the
				// constructor.
				"C": "{foo<'a>(self, p: mut 'a {x: number}) -> mut 'a {x: number}}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 0,
			},
		},
		"GetterCapturesConstructorParam": {
			// Bug B5: getters and setters were not scanned for
			// constructor-param captures (only FieldElem / MethodElem
			// were). A getter whose body references a constructor
			// param by name should force a lifetime onto the
			// constructor param, just like a method body.
			input: `
				class C(p: mut {x: number}) {
					get q -> mut {x: number} { return p }
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn <'a>(p: mut 'a {x: number}) -> C<'a>}",
			},
			expectedInstanceType: map[string]string{
				"C": "{get q(self) -> mut {x: number}}",
			},
			expectedInstanceLifetimes: map[string]int{
				"C": 1,
			},
		},
		"SetterCapturesConstructorParam": {
			// Bug B5: same as the getter case but for setters.
			input: `
				class C(p: mut {x: number}) {
					set q(mut self, v: number) { p.x = v }
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn <'a>(p: mut 'a {x: number}) -> C<'a>}",
			},
		},
		"NestedFuncParamDefaultCapturesConstructorParam": {
			// A nested function's parameter default expression evaluates
			// in its enclosing scope (here, the setter body, where the
			// constructor's `p` is visible). The capture visitor must
			// visit nested-function param defaults BEFORE pushing the
			// nested-function's own scope, otherwise the reference is
			// silently dropped and the constructor fails to receive a
			// lifetime. The setter body itself does not directly mention
			// `p` — only the nested function's param default does.
			input: `
				class C(p: mut {x: number}) {
					set q(mut self, v: number) {
						val nested = fn(w = p) -> number { return 0 }
					}
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn <'a>(p: mut 'a {x: number}) -> C<'a>}",
			},
		},
		"StaticMethodDoesNotCapture": {
			// Phase 8.6 (#4): static methods can't access instance state,
			// so they should never trigger constructor-param capture even
			// if their bodies happen to mention the param name.
			input: `
				class C(p: mut {x: number}) {
					static make() -> number { return 0 }
				}
			`,
			expectedTypes: map[string]string{
				"C": "{new fn (p: mut {x: number}) -> C, make() -> number}",
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

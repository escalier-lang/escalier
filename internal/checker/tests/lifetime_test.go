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
		input         string
		expectedTypes map[string]string
	}{
		"ContainerStoresMutRef": {
			input: `
				class Container(item: mut {x: number}) { item, }
			`,
			expectedTypes: map[string]string{
				"Container": "{new fn <'a>(item: mut 'a {x: number}) -> mut? Container<'a>}",
			},
		},
		"PointPrimitivesNoLifetime": {
			input: `
				class Point(x: number, y: number) { x, y, }
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> mut? Point}",
			},
		},
		"PairOfRefs": {
			input: `
				class Pair(first: mut {x: number}, second: mut {x: number}) {
					first, second,
				}
			`,
			expectedTypes: map[string]string{
				"Pair": "{new fn <'a, 'b>(first: mut 'a {x: number}, second: mut 'b {x: number}) -> mut? Pair<'a, 'b>}",
			},
		},
		"ShorthandWithDefaultStoresParam": {
			input: `
				class Container(item: mut {x: number}) {
					item = {x: 0},
				}
			`,
			expectedTypes: map[string]string{
				"Container": "{new fn <'a>(item: mut 'a {x: number}) -> mut? Container<'a>}",
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
				"Wrap": "{new fn <'a>(p: mut 'a {x: {y: number}}) -> mut? Wrap<'a>}",
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
				"Wrap": "{new fn <'a>(p: mut 'a {x: number}) -> mut? Wrap<'a>}",
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
				"Wrap": "{new fn <'a, 'b>(p: mut 'a {x: number}, q: mut 'b {x: number}) -> mut? Wrap<'a, 'b>}",
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
				"C": "{new fn (p: mut {x: number}) -> mut? C}",
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
				"C": "{new fn (p: mut {x: number}) -> mut? C, make() -> number}",
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


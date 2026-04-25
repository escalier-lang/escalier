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


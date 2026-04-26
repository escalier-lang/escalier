package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMutPrefixBindingTypes confirms the inferred type of bindings whose
// initializer uses (or omits) the `mut` prefix on a call expression.
func TestMutPrefixBindingTypes(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"BareConstructorCall_Immutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val p = Point(5, 10)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"MutConstructorCall_Mutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val p = mut Point(5, 10)
			`,
			bindingName:  "p",
			expectedType: "mut Point",
		},
		"MutConstructorCall_OnClassWithMutSelf": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { return self.count }
				}
				val c = mut Counter(0)
			`,
			bindingName:  "c",
			expectedType: "mut Counter",
		},
		"BareFunctionCall_Immutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn makePoint(x: number, y: number) -> Point { return Point(x, y) }
				val p = makePoint(5, 10)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"MutFunctionCall_Mutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn makePoint(x: number, y: number) -> Point { return Point(x, y) }
				val p = mut makePoint(5, 10)
			`,
			bindingName:  "p",
			expectedType: "mut Point",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedType, got,
				"unexpected type for %q", test.bindingName)
		})
	}
}

// TestMutPrefixMutationBehavior exercises the runtime-relevant consequences
// of the mut prefix: mutating an immutable instance is rejected, and the
// `mut` prefix unlocks mutation as well as `mut self` method calls.
//
// Class declarations require module context; the mutation statements live
// inside a function body so they are accepted by ParseLibFiles.
func TestMutPrefixMutationBehavior(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		"ImmutableConstructor_CannotAssignField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn test() {
					val p = Point(5, 10)
					p.x = 99
				}
			`,
			expectErrors: true,
		},
		"MutConstructor_CanAssignField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn test() {
					val p = mut Point(5, 10)
					p.x = 99
				}
			`,
			expectErrors: false,
		},
		// NOTE: a follow-up should also reject calling a mut-self method
		// on an immutable receiver. The checker does not yet enforce this
		// at the call site (only direct field writes are checked), so we
		// only test the positive case here.
		"MutInstance_CanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn test() {
					val c = mut Counter(0)
					c.tick()
				}
			`,
			expectErrors: false,
		},
		"MutFunctionCall_CanAssignField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn makePoint(x: number, y: number) -> Point { return Point(x, y) }
				fn test() {
					val p = mut makePoint(5, 10)
					p.x = 99
				}
			`,
			expectErrors: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			inferErrors := c.InferModule(inferCtx, module)

			if test.expectErrors {
				assert.NotEmpty(t, inferErrors, "expected inference errors for %s", name)
				for i, err := range inferErrors {
					t.Logf("Error[%d]: %s", i, err.Message())
				}
			} else {
				if len(inferErrors) > 0 {
					for i, err := range inferErrors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				assert.Empty(t, inferErrors, "expected no inference errors for %s", name)
			}
		})
	}
}

// TestMutPrefixOnNonCallRejected ensures the type checker rejects `mut`
// applied to anything other than a call expression.
func TestMutPrefixOnNonCallRejected(t *testing.T) {
	tests := map[string]string{
		"OnLiteral":  `val x = mut 42`,
		"OnIdent":    `val a = 1 val b = mut a`,
		"OnArrayLit": `val x = mut [1, 2, 3]`,
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			_, inferErrors := c.InferScript(inferCtx, script)

			found := false
			for _, err := range inferErrors {
				if _, ok := err.(*MutPrefixOnNonCallError); ok {
					found = true
					break
				}
			}
			assert.Truef(t, found, "expected MutPrefixOnNonCallError, got %v", inferErrors)
		})
	}
}

// TestMutPrefixWithBuiltinCollections confirms that the prelude merge of
// Map/ReadonlyMap and Set/ReadonlySet correctly classifies methods so that
// read methods work on immutable values while mutating methods require mut.
func TestMutPrefixWithBuiltinCollections(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		"ImmutableMap_CanReadHas": {
			input: `
				declare val m: Map<string, number>
				val x = m.has("hello")
			`,
			expectErrors: false,
		},
		"MutMap_CanClear": {
			input: `
				declare val m: mut Map<string, number>
				m.clear()
			`,
			expectErrors: false,
		},
		"ImmutableSet_CanReadHas": {
			input: `
				declare val s: Set<number>
				val x = s.has(1)
			`,
			expectErrors: false,
		},
		"MutSet_CanAdd": {
			input: `
				declare val s: mut Set<number>
				s.add(1)
			`,
			expectErrors: false,
		},
		// NOTE: rejecting mut-self method calls on immutable receivers
		// is a follow-up — the checker only enforces direct field writes
		// today, so we only assert the positive cases for Map and Set.
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			_, inferErrors := c.InferScript(inferCtx, script)

			if test.expectErrors {
				assert.NotEmpty(t, inferErrors, "expected inference errors for %s", name)
				for i, err := range inferErrors {
					t.Logf("Error[%d]: %s", i, err.Message())
				}
			} else {
				if len(inferErrors) > 0 {
					for i, err := range inferErrors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				assert.Empty(t, inferErrors, "expected no inference errors for %s", name)
			}
		})
	}
}

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMutPrefixBindingTypes confirms that pattern-level `mut` flows
// through a factory-function initializer (not just a constructor call).
// The constructor-call and plain-binding cases live in
// TestPatternLevelMut_BindingTypes.
func TestMutPrefixBindingTypes(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"MutFunctionCall_Mutable": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn makePoint(x: number, y: number) -> Point { return Point(x, y) }
				val mut p = makePoint(5, 10)
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

// TestMutPrefixMutationBehavior covers mut-receiver behaviors that aren't
// in TestPatternLevelMut_MutationBehavior: binding a mut-self method
// reference, the call-rejection path on an immutable receiver, factory
// functions returning mut, and type-variable / alias receivers.
//
// Class declarations require module context; the mutation statements live
// inside a function body so they are accepted by ParseLibFiles.
//
// expectedErrors is a list of substrings; each substring must match the
// corresponding inference error in order. An empty list asserts no errors.
func TestMutPrefixMutationBehavior(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"MutInstance_CanBindMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn test() {
					val mut c = Counter(0)
					val t = c.tick
				}
			`,
		},
		"ImmutableInstance_CannotCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn test() {
					val c = Counter(0)
					c.tick()
				}
			`,
			expectedErrors: []string{"Callee is not callable"},
		},
		"MutFunctionCall_CanAssignField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn makePoint(x: number, y: number) -> Point { return Point(x, y) }
				fn test() {
					val mut p = makePoint(5, 10)
					p.x = 99
				}
			`,
		},
		"TypeVarReceiver_ImmutableConstraint_CannotCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn callTick<T: Counter>(t: T) -> number {
					return t.tick()
				}
			`,
			expectedErrors: []string{"Callee is not callable"},
		},
		"TypeVarReceiver_MutConstraint_CanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn callTick<T: mut Counter>(t: T) -> number {
					return t.tick()
				}
			`,
		},
		"AliasReceiver_MutAliasNonGeneric_CanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				type MutCounter = mut Counter
				declare val c: MutCounter
				fn test() -> number { return c.tick() }
			`,
		},
		"AliasReceiver_MutAliasGeneric_CanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				type MutT<T> = mut T
				declare val c: MutT<Counter>
				fn test() -> number { return c.tick() }
			`,
		},
		"AliasReceiver_ImmutableAlias_CannotCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				type Alias<T> = T
				declare val c: Alias<Counter>
				fn test() -> number { return c.tick() }
			`,
			expectedErrors: []string{"Callee is not callable"},
		},
		"AliasReceiver_IdentityAliasOfMut_CanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				type Identity<T> = T
				declare val c: Identity<mut Counter>
				fn test() -> number { return c.tick() }
			`,
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

			assertExpectedErrors(t, test.expectedErrors, inferErrors)
		})
	}
}

// TestExpressionLevelMutRejected ensures the parser rejects `mut` in any
// expression position, and that subsequent inference of the recovered AST
// (the bare expression without `mut`) does not panic. Pattern-level `mut`
// (`val mut x = …`, `IdentPat.Mutable` / `ObjShorthandPat.Mutable`) is the
// only sanctioned form for binding-side mutability — see `TestPatternLevelMut*`.
func TestExpressionLevelMutRejected(t *testing.T) {
	tests := map[string]string{
		"ExprMutOnCall":     `val x = mut foo()`,
		"ExprMutOnLiteral":  `val x = mut 42`,
		"ExprMutOnIdent":    `val a = 1 val b = mut a`,
		"ExprMutOnArrayLit": `val x = mut [1, 2, 3]`,
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			found := false
			for _, err := range parseErrors {
				if strings.Contains(err.Message, "'mut' is not allowed in expression position") {
					found = true
					break
				}
			}
			assert.Truef(t, found,
				"expected parse error about expression-level 'mut', got %v", parseErrors)

			// Recovered AST (the bare expression without `mut`) must still
			// infer without panicking.
			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			require.NotPanics(t, func() {
				c.InferScript(inferCtx, script)
			})
		})
	}
}

// TestMutPrefixWithBuiltinCollections confirms that the prelude merge of
// Map/ReadonlyMap and Set/ReadonlySet correctly classifies methods so that
// read methods work on immutable values while mutating methods require mut.
func TestMutPrefixWithBuiltinCollections(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"ImmutableMap_CanReadHas": {
			input: `
				declare val m: Map<string, number>
				val x = m.has("hello")
			`,
		},
		"MutMap_CanClear": {
			input: `
				declare val m: mut Map<string, number>
				m.clear()
			`,
		},
		"ImmutableSet_CanReadHas": {
			input: `
				declare val s: Set<number>
				val x = s.has(1)
			`,
		},
		"MutSet_CanAdd": {
			input: `
				declare val s: mut Set<number>
				s.add(1)
			`,
		},
		"ImmutableMap_CannotClear": {
			input: `
				declare val m: Map<string, number>
				m.clear()
			`,
			expectedErrors: []string{"Callee is not callable"},
		},
		"ImmutableSet_CannotAdd": {
			input: `
				declare val s: Set<number>
				s.add(1)
			`,
			expectedErrors: []string{"Callee is not callable"},
		},
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

			assertExpectedErrors(t, test.expectedErrors, inferErrors)
		})
	}
}


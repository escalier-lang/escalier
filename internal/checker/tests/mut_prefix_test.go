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
		// Regression: `mut` must be recognized as an expression starter so
		// it works in spread positions (and other contexts that use
		// canStartExpr to gate further parsing).
		"MutInArraySpread_Parses": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val arr = [...[mut Point(1, 2)]]
			`,
			bindingName:  "arr",
			expectedType: "[mut Point]",
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
		"MutInstance_CanBindMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn test() {
					val c = mut Counter(0)
					val t = c.tick
				}
			`,
			expectErrors: false,
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
			expectErrors: true,
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
			expectErrors: true,
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
			expectErrors: false,
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
			expectErrors: false,
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
			expectErrors: false,
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
			expectErrors: true,
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

// TestExpressionLevelMutRejectedOnNonCall ensures the parser rejects
// expression-level `mut` applied to anything other than a call expression.
// The constraint is syntactic (the Mutable flag lives on CallExpr itself),
// so it surfaces at parse time rather than inference time. Pattern-level
// `mut` (`val mut x = …`, `IdentPat.Mutable` / `ObjShorthandPat.Mutable`)
// is the sanctioned form for binding-side mutability — see
// `TestPatternLevelMut*` for those positives.
func TestExpressionLevelMutRejectedOnNonCall(t *testing.T) {
	tests := map[string]string{
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
			_, parseErrors := p.ParseScript()

			found := false
			for _, err := range parseErrors {
				if strings.Contains(err.Message, "'mut' prefix can only be applied to a call expression") {
					found = true
					break
				}
			}
			assert.Truef(t, found,
				"expected parse error about 'mut' prefix, got %v", parseErrors)
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
		"ImmutableMap_CannotClear": {
			input: `
				declare val m: Map<string, number>
				m.clear()
			`,
			expectErrors: true,
		},
		"ImmutableSet_CannotAdd": {
			input: `
				declare val s: Set<number>
				s.add(1)
			`,
			expectErrors: true,
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

// TestMutPrefixOnNonCall_InferenceRecovers ensures that after the parser
// rejects `mut <non-call>`, the recovered AST (the bare expression without
// `mut`) still infers cleanly — no panics, no crashes.
func TestMutPrefixOnNonCall_InferenceRecovers(t *testing.T) {
	inputs := map[string]string{
		"OnIdent":    `val a = 1 val b = mut a`,
		"OnArrayLit": `val x = mut [1, 2, 3]`,
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			require.NotEmpty(t, parseErrors, "expected parse error")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			require.NotPanics(t, func() {
				c.InferScript(inferCtx, script)
			})
		})
	}
}

// TestMutSuffixBinding documents how `mut` interacts with suffix expressions:
//   - `mut foo().bar()` is accepted; `mut` applies to the outer call (.bar()).
//   - `mut foo().bar` is rejected — the user must write `(mut foo()).bar`.
//   - `mut foo()(arg)` is accepted; `mut` applies to the outer call.
//   - parenthesized forms always parse cleanly.
func TestMutSuffixBinding(t *testing.T) {
	tests := map[string]struct {
		input       string
		expectError bool
	}{
		"MutThenMemberOnly_Rejected":   {`val x = mut foo().bar`, true},
		"MutThenMemberThenCall_Accept": {`val x = mut foo().bar()`, false},
		"MutThenChainedCall_Accept":    {`val x = mut foo()(arg)`, false},
		"ParenMutThenMember_Accept":    {`val x = (mut foo()).bar`, false},
		"ParenMutThenCall_Accept":      {`val x = (mut foo()).bar()`, false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			_, errs := p.ParseScript()
			if test.expectError {
				assert.NotEmpty(t, errs, "expected parse error for %q", test.input)
			} else {
				assert.Empty(t, errs, "expected clean parse for %q", test.input)
			}
		})
	}
}

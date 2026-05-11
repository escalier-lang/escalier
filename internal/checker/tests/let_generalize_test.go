package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

// TestBodyLetGeneralization exercises let-generalization for functions
// declared inside other function bodies — both `fn name(...)` (FuncDecl) and
// `val name = fn(...)` / destructured-FuncExpr (VarDecl) forms. The outer
// function's inferred type is checked; a polymorphic inner function lets the
// outer body use it at multiple incompatible types without unification errors,
// and the resulting tuple's element types reflect each call's argument type.
func TestBodyLetGeneralization(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"BodyVarDecl_IdentPat_FuncExpr": {
			input: `
				fn outer() {
					val id = fn (x) { return x }
					val a = id("hello")
					val b = id(5)
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5]",
			},
		},
		"BodyFuncDecl_Polymorphic": {
			input: `
				fn outer() {
					fn id(x) { return x }
					val a = id("hello")
					val b = id(5)
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5]",
			},
		},
		"BodyVarDecl_TupleDestructure_FuncExprs": {
			input: `
				fn outer() {
					val [f, g] = [fn (x) { return x }, fn (y) { return y }]
					val a = f("hello")
					val b = f(5)
					val c = g(true)
					val d = g(1.5)
					return [a, b, c, d]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5, true, 1.5]",
			},
		},
		"BodyVarDecl_ObjectDestructure_FuncExprs": {
			input: `
				fn outer() {
					val {f, g} = {f: fn (x) { return x }, g: fn (y) { return y }}
					val a = f("hello")
					val b = f(5)
					val c = g(true)
					val d = g(1.5)
					return [a, b, c, d]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5, true, 1.5]",
			},
		},
		"BodyVarDecl_NestedDestructure_FuncExprs": {
			input: `
				fn outer() {
					val {a: [f, g]} = {a: [fn (x) { return x }, fn (y) { return y }]}
					val p = f("hello")
					val q = f(5)
					val r = g(true)
					val s = g(1.5)
					return [p, q, r, s]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5, true, 1.5]",
			},
		},
		"BodyVarDecl_DeeplyNested": {
			input: `
				fn outer() {
					fn middle() {
						fn inner() {
							val id = fn (x) { return x }
							val a = id("hello")
							val b = id(5)
							return [a, b]
						}
						return inner()
					}
					return middle()
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5]",
			},
		},
		"BodyVarDecl_SiblingInnerDecls": {
			input: `
				fn outer() {
					val f = fn (x) { return x }
					val g = fn (y) { return y }
					val a = f("hello")
					val b = g(5)
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5]",
			},
		},
		"BodyFuncDecl_SiblingInnerDecls": {
			input: `
				fn outer() {
					fn f(x) { return x }
					fn g(y) { return y }
					val a = f("hello")
					val b = g(5)
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn () -> [\"hello\", 5]",
			},
		},
		// Inner function captures an outer-scope param `y`. The env-FTV
		// filter must keep `y`'s TV unresolved on `inner`'s signature, so
		// `inner` reads as `fn<T>(x: T) -> <outer_y_tv>` rather than
		// generalizing the captured TV into a fresh TypeParam. The outer
		// function then owns that TV and generalizes it itself, producing
		// `fn<T0, T1>(y: T0) -> [T0, T0]`. Without the filter, `inner`
		// would over-generalize and `outer`'s return would lose the tie to
		// `y`'s type — the two `[T0, T0]` slots would diverge or collapse
		// to void.
		"BodyVarDecl_InnerCapturesOuterParam": {
			input: `
				fn outer(y) {
					val inner = fn (x) { return y }
					val a = inner(1)
					val b = inner("a")
					return [a, b]
				}
			`,
			expectedTypes: map[string]string{
				"outer": "fn <T0>(y: T0) -> [T0, T0]",
			},
		},
		"TopLevelLetPolymorphismUnchanged": {
			input: `
				val id = fn (x) { return x }
			`,
			expectedTypes: map[string]string{
				"id": "fn <T0>(x: T0) -> T0",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
				assert.Equal(t, inferErrors, []*Error{})
			}

			actualTypes := make(map[string]string)
			for name, binding := range scope.Values {
				assert.NotNil(t, binding)
				actualTypes[name] = binding.Type.String()
			}

			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				assert.True(t, exists, "Expected variable %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
				}
			}
		})
	}
}

// TestBodyLetGeneralizationNegatives ensures generalization does NOT happen in
// positions where the FuncExpr's value does not flow into a named binding.
// In each case the inner FuncExpr's parameter type must be unified across
// uses, so calling it at two incompatible types reports a unification error.
func TestBodyLetGeneralizationNegatives(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		// FuncExpr appears inside an object literal at an IdentPat-bound
		// slot. Generalizing the field would require first-class
		// polymorphism on object fields, which is out of scope. `o.f`
		// must be monomorphic, so calling it at two incompatible
		// literal types errors.
		"ObjectFieldFuncExpr_NotGeneralized": {
			input: `
				fn outer() {
					val o = {f: fn (x) { return x }}
					val a = o.f("hello")
					val b = o.f(5)
					return [a, b]
				}
			`,
			expectedErrors: []string{`5 cannot be assigned to "hello"`},
		},
		// IdentPat + TupleExpr is a pattern/init shape mismatch: the
		// VarDecl binds a single name to the tuple value, so the inner
		// FuncExprs are not in destructurable positions. They must stay
		// monomorphic; destructuring the tuple later into per-slot names
		// does not re-trigger generalization (the FuncTypes were already
		// inferred without the let-generalize signal). Calling the
		// destructured `a` at two incompatible literal types must error.
		"IdentPatTupleInit_ShapeMismatch_NotGeneralized": {
			input: `
				fn outer() {
					val pair = [fn (x) { return x }, fn (y) { return y }]
					val [a, b] = pair
					val r1 = a("hello")
					val r2 = a(5)
					return [r1, r2]
				}
			`,
			expectedErrors: []string{`5 cannot be assigned to "hello"`},
		},
		// FuncExpr passed as a call argument. The CallExpr branch in
		// inferExpr does not re-arm the let-generalize signal, so the
		// inner FuncExpr is inferred monomorphically. The returned
		// FuncType is bound to a single param TV; calling it at two
		// incompatible literal types must error.
		"FuncExprAsCallArg_NotGeneralized": {
			input: `
				fn outer() {
					fn apply(f) { return f }
					val g = apply(fn (x) { return x })
					val a = g("hello")
					val b = g(5)
					return [a, b]
				}
			`,
			expectedErrors: []string{`5 cannot be assigned to "hello"`},
		},
		// `val id = fn(x) { return id(x) }` — recursive self-reference
		// via `val` binding. The init is inferred before `id`'s binding
		// is finalized, so `id` inside its own body resolves to `never`
		// rather than the polymorphic outer binding. This pins the
		// current limitation; if the language later supports recursive
		// `val` for function values, this test should move to the
		// positive suite.
		"BodyVarDecl_RecursiveSelfReference": {
			input: `
				fn outer() {
					val id = fn (x) { return id(x) }
					val a = id("hello")
					val b = id(5)
					return [a, b]
				}
			`,
			expectedErrors: []string{"Callee is not callable: never"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, errors, 0)

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)
			actualMessages := make([]string, len(inferErrors))
			for i, e := range inferErrors {
				actualMessages[i] = e.Message()
			}
			assert.Equal(t, test.expectedErrors, actualMessages)
		})
	}
}

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

// TestLifetimeElision_DeclareFn exercises Phase 11 elision rules
// against body-less `declare fn` declarations. Each scenario covers
// one classification: single ref param + ref return, no ref params,
// ref return only, primitive return, void return, ambiguous (multi
// ref param + ref return), and the user-explicit no-elision opt-out.
func TestLifetimeElision_DeclareFn(t *testing.T) {
	tests := map[string]struct {
		input        string
		fnName       string
		expectedType string
	}{
		"SingleRefParamRefReturn": {
			input: `
				declare fn first(p: mut {x: number}) -> mut {x: number}
			`,
			fnName:       "first",
			expectedType: "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}",
		},
		"SingleRefParamRefReturnImmut": {
			input: `
				declare fn read(p: {x: number}) -> {x: number}
			`,
			fnName:       "read",
			expectedType: "fn <'a>(p: 'a {x: number}) -> 'a {x: number}",
		},
		"PrimitiveReturnNoElision": {
			input: `
				declare fn getX(p: mut {x: number}) -> number
			`,
			fnName:       "getX",
			expectedType: "fn (p: mut {x: number}) -> number",
		},
		"VoidReturnNoElision": {
			input: `
				declare fn touch(p: mut {x: number}) -> undefined
			`,
			fnName:       "touch",
			expectedType: "fn (p: mut {x: number}) -> undefined",
		},
		"NoRefParamsRefReturnFresh": {
			input: `
				declare fn spawn() -> mut {x: number}
			`,
			fnName:       "spawn",
			expectedType: "fn () -> mut {x: number}",
		},
		"PrimitiveParamRefReturnFresh": {
			input: `
				declare fn make(seed: number) -> mut {x: number}
			`,
			fnName:       "make",
			expectedType: "fn (seed: number) -> mut {x: number}",
		},
		"MultipleRefParamsRefReturnLeftUnannotated": {
			// Phase 11 lenient policy: instead of erroring, the
			// signature is left without lifetime annotations. The
			// return is treated as fresh w.r.t. the parameters.
			input: `
				declare fn pick(a: mut {x: number}, b: mut {x: number}) -> mut {x: number}
			`,
			fnName:       "pick",
			expectedType: "fn (a: mut {x: number}, b: mut {x: number}) -> mut {x: number}",
		},
		"AlreadyAnnotatedNotElided": {
			// User wrote an explicit `<'a>` clause. Elision must not
			// override it (in particular, must not attach `'a` to b).
			// Lifetime annotations require a named type reference at
			// parse time, so we use a type alias to introduce `Point`.
			input: `
				type Point = {x: number}
				declare fn keep<'a>(a: mut 'a Point, b: mut Point) -> mut 'a Point
			`,
			fnName:       "keep",
			expectedType: "fn <'a>(a: mut 'a Point, b: mut Point) -> mut 'a Point",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: tc.input}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			scope, inferErrors := c.InferScript(inferCtx, script)

			for i, err := range inferErrors {
				if !err.IsWarning() {
					t.Errorf("unexpected error[%d]: %s", i, err.Message())
				}
			}

			binding := scope.GetValue(tc.fnName)
			require.NotNil(t, binding, "binding %q not found", tc.fnName)
			assert.Equal(t, tc.expectedType, binding.Type.String())
		})
	}
}

// TestIsReferenceType pins down the classification used by elision —
// objects, tuples, and function types are reference types; primitives,
// void, and never are not.
func TestIsReferenceType(t *testing.T) {
	tests := []struct {
		name     string
		t        type_system.Type
		expected bool
	}{
		{"NumPrim", type_system.NewNumPrimType(nil), false},
		{"StrPrim", type_system.NewStrPrimType(nil), false},
		{"BoolPrim", type_system.NewBoolPrimType(nil), false},
		{"Void", type_system.NewVoidType(nil), false},
		{"Never", type_system.NewNeverType(nil), false},
		{"Object", type_system.NewObjectType(nil, nil), true},
		{"Tuple", type_system.NewTupleType(nil, type_system.NewNumPrimType(nil)), true},
		{"Func", type_system.NewFuncType(nil, nil, nil, type_system.NewVoidType(nil), type_system.NewNeverType(nil)), true},
		{"MutObject", type_system.NewMutType(nil, type_system.NewObjectType(nil, nil)), true},
		{"MutPrim", type_system.NewMutType(nil, type_system.NewNumPrimType(nil)), false},
		{"UnionWithRef", type_system.NewUnionType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewObjectType(nil, nil),
		), true},
		{"UnionAllPrim", type_system.NewUnionType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil),
		), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsReferenceType(tt.t))
		})
	}
}

// TestVerifyLifetimeCompatibility exercises the interface/implementation
// lifetime-compatibility checker. The parser does not yet support an
// `implements` clause, so we drive the check directly with FuncTypes
// constructed in code.
func TestVerifyLifetimeCompatibility(t *testing.T) {
	t.Parallel()
	c := NewChecker(context.Background())

	// Helpers to build small FuncTypes with a single param.
	mkFunc := func(paramLT type_system.Lifetime, retLT type_system.Lifetime) *type_system.FuncType {
		paramObj := type_system.NewObjectType(nil, nil)
		paramObj.Lifetime = paramLT
		retObj := type_system.NewObjectType(nil, nil)
		retObj.Lifetime = retLT

		ident := &type_system.IdentPat{Name: "p"}
		ft := type_system.NewFuncType(
			nil, nil,
			[]*type_system.FuncParam{{Pattern: ident, Type: paramObj}},
			retObj,
			type_system.NewNeverType(nil),
		)
		return ft
	}

	// Case 1: interface ties param to return; impl matches. OK.
	t.Run("ImplMatchesIfaceAlias", func(t *testing.T) {
		ifaceLT := &type_system.LifetimeVar{ID: 1, Name: "a"}
		implLT := &type_system.LifetimeVar{ID: 2, Name: "a"}
		iface := mkFunc(ifaceLT, ifaceLT)
		impl := mkFunc(implLT, implLT)
		errs := c.VerifyLifetimeCompatibility(iface, impl, ast.Span{})
		assert.Empty(t, errs)
	})

	// Case 2: interface ties param to return; impl returns a fresh
	// value (no lifetime). More conservative — OK.
	t.Run("ImplFreshReturnIsConservative", func(t *testing.T) {
		ifaceLT := &type_system.LifetimeVar{ID: 1, Name: "a"}
		implLT := &type_system.LifetimeVar{ID: 2, Name: "a"}
		iface := mkFunc(ifaceLT, ifaceLT)
		impl := mkFunc(implLT, nil) // return has no lifetime
		errs := c.VerifyLifetimeCompatibility(iface, impl, ast.Span{})
		assert.Empty(t, errs)
	})

	// Case 3: interface promises a fresh return; impl aliases the
	// param. Less conservative — error.
	t.Run("ImplAliasesWhenIfaceFresh", func(t *testing.T) {
		ifaceLT := &type_system.LifetimeVar{ID: 1, Name: "a"}
		implLT := &type_system.LifetimeVar{ID: 2, Name: "a"}
		iface := mkFunc(ifaceLT, nil) // fresh return
		impl := mkFunc(implLT, implLT)
		errs := c.VerifyLifetimeCompatibility(iface, impl, ast.Span{})
		assert.NotEmpty(t, errs)
	})

	// Case 4: parameter count mismatch — error.
	t.Run("ParamCountMismatch", func(t *testing.T) {
		ifaceLT := &type_system.LifetimeVar{ID: 1, Name: "a"}
		implLT := &type_system.LifetimeVar{ID: 2, Name: "a"}
		iface := mkFunc(ifaceLT, ifaceLT)

		paramObj := type_system.NewObjectType(nil, nil)
		paramObj.Lifetime = implLT
		retObj := type_system.NewObjectType(nil, nil)
		retObj.Lifetime = implLT
		impl := type_system.NewFuncType(
			nil, nil,
			[]*type_system.FuncParam{
				{Pattern: &type_system.IdentPat{Name: "a"}, Type: paramObj},
				{Pattern: &type_system.IdentPat{Name: "b"}, Type: paramObj},
			},
			retObj,
			type_system.NewNeverType(nil),
		)
		errs := c.VerifyLifetimeCompatibility(iface, impl, ast.Span{})
		assert.NotEmpty(t, errs)
	})
}


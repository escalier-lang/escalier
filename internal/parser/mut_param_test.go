package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestParseMutParam covers the parameter forms that accept a `mut` binding
// marker. want maps each name a parameter binds to whether that binding is
// written `mut`.
func TestParseMutParam(t *testing.T) {
	tests := map[string]struct {
		src  string
		want map[string]bool
	}{
		"Ident":          {"fn f(mut p: number) {}", map[string]bool{"p": true}},
		"Unannotated":    {"fn f(mut p) {}", map[string]bool{"p": true}},
		"Optional":       {"fn f(mut p?: number) {}", map[string]bool{"p": true}},
		"Default":        {"fn f(mut p: number = 1) {}", map[string]bool{"p": true}},
		"Open":           {"fn f(open mut p) {}", map[string]bool{"p": true}},
		"MixedWithPlain": {"fn f(mut p: number, q: number) {}", map[string]bool{"p": true, "q": false}},
		"Rest":           {"fn f(...mut rest: Array<number>) {}", map[string]bool{"rest": true}},
		// A `mut` followed by `:` is the rest parameter's name, the way
		// `...from` names one.
		"RestNamedMut":      {"fn f(...mut: Array<number>) {}", map[string]bool{"mut": false}},
		"ObjectShorthand":   {"fn f({mut a, b}: {a: number, b: number}) {}", map[string]bool{"a": true, "b": false}},
		"ObjectKeyValue":    {"fn f({x: mut a, y: b}: {x: number, y: number}) {}", map[string]bool{"a": true, "b": false}},
		"Tuple":             {"fn f([mut a, b]: [number, number]) {}", map[string]bool{"a": true, "b": false}},
		"TupleRest":         {"fn f([a, ...mut rest]: Array<number>) {}", map[string]bool{"a": false, "rest": true}},
		"KeywordNamedParam": {"fn f(mut: number) {}", map[string]bool{"mut": false}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.src}
			decls, errs := ParseDecls(context.Background(), source)
			require.Empty(t, errs)
			require.Len(t, decls, 1)
			fd, ok := decls[0].(*ast.FuncDecl)
			require.True(t, ok)

			got := map[string]bool{}
			for _, param := range fd.Params {
				ast.ForEachLeafBinding(param.Pattern, func(name string, leaf ast.Node) {
					switch l := leaf.(type) {
					case *ast.IdentPat:
						got[name] = l.Mutable
					case *ast.ObjShorthandPat:
						got[name] = l.Mutable
					}
				})
			}
			require.Equal(t, test.want, got)
		})
	}
}

// TestParseMutParamRejected covers the parameter forms that reject `mut`. A
// parameter takes `mut` only on an identifier, the same as a `val` binding.
// The parser does not recover inside the parameter list, so the rest of the
// declaration reports follow-on errors. want is the first error, which names
// the problem.
func TestParseMutParamRejected(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"OnTuple": {
			"fn f(mut [a, b]: [number, number]) {}",
			"'mut' cannot be applied to a tuple pattern; use per-leaf mut, e.g. [mut a, b]",
		},
		"OnObject": {
			"fn f(mut {a, b}: {a: number, b: number}) {}",
			"'mut' cannot be applied to an object pattern; use per-leaf mut, e.g. {mut a, b}",
		},
		"BeforeRest": {
			"fn f(mut ...rest: Array<number>) {}",
			"'mut' in pattern position must be followed by an identifier",
		},
		"OnWildcard": {
			"fn f(mut _: number) {}",
			"'mut' in pattern position must be followed by an identifier",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.src}
			_, errs := ParseDecls(context.Background(), source)
			require.NotEmpty(t, errs)
			require.Equal(t, test.want, errs[0].Message)
		})
	}
}

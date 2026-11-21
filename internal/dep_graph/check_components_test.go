package dep_graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestCheckComponents covers the order CheckComponents gives a module's bindings. A
// function with a complete signature depends only on what its signature names, and its
// body key depends on what its body reads, so two such functions that call each other
// land in separate components. A function whose signature leaves something to inference
// keeps its body's dependencies.
func TestCheckComponents(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected string
	}{
		{
			name: "MutuallyRecursiveSignatureBoundFunctions",
			src: `
				fn even(n: number) -> boolean { return if n == 0 { true } else { odd(n - 1) } }
				fn odd(n: number) -> boolean { return if n == 0 { false } else { even(n - 1) } }
			`,
			expected: "[[value:even] [value:odd] [body:even] [body:odd]]",
		},
		{
			name: "MutuallyRecursiveInferredFunctions",
			src: `
				fn even(n: number) { return if n == 0 { true } else { odd(n - 1) } }
				fn odd(n: number) { return if n == 0 { false } else { even(n - 1) } }
			`,
			expected: "[[value:odd value:even]]",
		},
		{
			name: "MutuallyRecursiveInferredErrorTypes",
			src: `
				fn a(n: number) -> number throws _ { if n > 0 { return b(n - 1) } throw "a" }
				fn b(n: number) -> number throws _ { if n > 0 { return a(n - 1) } throw "b" }
			`,
			expected: "[[value:b value:a]]",
		},
		{
			name: "SignatureNamesAType",
			src: `
				fn area(s: Shape) -> number { return s.w * s.h }
				type Shape = {w: number, h: number}
			`,
			expected: "[[type:Shape] [value:area] [body:area]]",
		},
		{
			name: "DeclaredFunctionHasNoBodyKey",
			src: `
				declare fn ext(n: number) -> number
				val x = ext(1)
			`,
			expected: "[[value:ext] [value:x]]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{
				{ID: 0, Path: "test.esc", Contents: tt.src},
			})
			require.Empty(t, errs)
			graph := BuildDepGraph(module)
			require.Equal(t, tt.expected, fmt.Sprint(graph.CheckComponents))
		})
	}
}

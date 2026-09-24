package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/stretchr/testify/require"
)

// bindingKeys returns every binding key g holds a declaration for, in the btree's
// sorted order.
func bindingKeys(g *dep_graph.DepGraph) []string {
	keys := []string{}
	g.Decls.Scan(func(key dep_graph.BindingKey, _ []ast.Decl) bool {
		keys = append(keys, key.String())
		return true
	})
	return keys
}

// TestModuleResultDepGraph checks that a run hands back the dep graph it walked.
// Codegen takes a graph, and a caller reading this field emits against the one
// inference ordered its declarations by rather than a second build of its own.
func TestModuleResultDepGraph(t *testing.T) {
	module := parseModule(t, `
		val x = 5
		fn double(n: number) -> number { return n }
		type Count = number
	`)

	res := InferModuleWithSource(module, testStdlibSource())

	require.Empty(t, res.Errors)
	require.NotNil(t, res.DepGraph)
	require.Equal(t, []string{"type:Count", "value:double", "value:x"}, bindingKeys(res.DepGraph))
	require.Equal(t, bindingKeys(dep_graph.BuildDepGraph(module)), bindingKeys(res.DepGraph))
}

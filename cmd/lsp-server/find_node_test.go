package main

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestFindNodeInScriptReachesAnImport asserts that the lookups over a script visit its
// imports, which the parser keeps apart from its statements.
func TestFindNodeInScriptReachesAnImport(t *testing.T) {
	source := "import \"std:console\"\nconsole.log(\"hi\")"
	script, _, _ := parseAndInferAllowErrors(t, source)
	loc := cursorAt(source, 1, 5)

	node := findNodeInScript(script, loc)
	require.IsType(t, &ast.ImportStmt{}, node)

	node, _ = findNodeAndParent(script, loc)
	require.IsType(t, &ast.ImportStmt{}, node)
}

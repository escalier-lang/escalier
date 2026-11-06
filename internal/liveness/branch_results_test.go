package liveness

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// parseScript parses a source string into a Script AST.
// Fails the test if parsing produces errors.
func parseScript(t *testing.T, input string) *ast.Script {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "test.esc", Contents: input}
	p := parser.NewParser(context.Background(), source)
	script, errs := p.ParseScript()
	require.Empty(t, errs, "parse errors for: %s", input)
	return script
}

// parseExpr parses a source string containing a single expression statement
// and returns the expression.
func parseExpr(t *testing.T, input string) ast.Expr {
	t.Helper()
	script := parseScript(t, input)
	require.Len(t, script.Stmts, 1, "expected exactly one statement")
	exprStmt, ok := script.Stmts[0].(*ast.ExprStmt)
	require.True(t, ok, "expected ExprStmt, got %T", script.Stmts[0])
	return exprStmt.Expr
}

// TestBranchResults covers the branch result expressions BranchResults returns for each
// branching expression, and that it reports false for one that does not branch.
func TestBranchResults(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		branches bool
		want     []string
	}{
		{name: "a conditional", src: "if c { a } else { b }", branches: true, want: []string{"a", "b"}},
		{name: "a conditional without an else", src: "if c { a }", branches: true, want: []string{"a"}},
		{name: "an if val", src: "if val x = o { a } else { b }", branches: true, want: []string{"a", "b"}},
		{name: "a match", src: "match o { 1 => a, _ => b }", branches: true, want: []string{"a", "b"}},
		{name: "a do", src: "do { a }", branches: true, want: []string{"a"}},
		{name: "a try", src: "try { a } catch { e => b }", branches: true, want: []string{"a", "b"}},
		{name: "a nested conditional", src: "if c { if d { a } else { b } } else { b }", branches: true, want: []string{"if d {\n    a\n} else {\n    b\n}", "b"}},
		{name: "a block ending in a declaration", src: "do { val y = a }", branches: true, want: []string{"<nil>"}},
		{name: "a name", src: "a", branches: false, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, branches := BranchResults(parseExpr(t, tt.src))
			require.Equal(t, tt.branches, branches)
			var got []string
			for _, r := range results {
				if r == nil {
					got = append(got, "<nil>")
					continue
				}
				text, err := printer.Print(r, printer.DefaultOptions())
				require.NoError(t, err)
				got = append(got, text)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

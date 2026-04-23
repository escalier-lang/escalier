package liveness

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseFuncExpr parses a script and extracts the first FuncExpr from the
// first variable declaration's initializer. The function body must go through
// a rename pass for VarIDs to be set, so we run Rename on both the outer
// scope and the inner function.
func parseFuncExpr(t *testing.T, input string) *ast.FuncExpr {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "test.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "Expected no parse errors")

	// Run rename on the outer scope first (to assign VarIDs to outer vars).
	outerBindings := make(map[string]VarID)
	Rename(nil, ast.Block{Stmts: script.Stmts}, outerBindings)

	// Find the FuncExpr.
	for _, stmt := range script.Stmts {
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				if funcExpr, ok := varDecl.Init.(*ast.FuncExpr); ok {
					// Run rename on the inner function body to set VarIDs.
					innerOuter := make(map[string]VarID)
					// Add outer variable names as outer bindings with negative IDs.
					nextID := VarID(-1)
					for _, s := range script.Stmts {
						if ds, ok := s.(*ast.DeclStmt); ok {
							if vd, ok := ds.Decl.(*ast.VarDecl); ok {
								if ip, ok := vd.Pattern.(*ast.IdentPat); ok {
									innerOuter[ip.Name] = nextID
									nextID--
								}
							}
						}
					}
					Rename(funcExpr.FuncSig.Params, *funcExpr.Body, innerOuter)
					return funcExpr
				}
			}
		}
	}
	t.Fatal("No FuncExpr found in input")
	return nil
}

func TestAnalyzeCaptures_ReadOnly(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val items = [1, 2, 3]
		val f = fn() { items }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "items", captures[0].Name)
	assert.False(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_MutableDirectAssign(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		var count = 0
		val f = fn() { count = count + 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "count", captures[0].Name)
	assert.True(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_MutablePropertyAssign(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val obj = {x: 0}
		val f = fn() { obj.x = 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 1)
	assert.Equal(t, "obj", captures[0].Name)
	assert.True(t, captures[0].IsMutable)
}

func TestAnalyzeCaptures_NoCaptures(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val f = fn(x: number) { x + 1 }
	`)

	captures := AnalyzeCaptures(funcExpr)
	assert.Empty(t, captures)
}

func TestAnalyzeCaptures_EmptyBody(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val f = fn() {}
	`)

	captures := AnalyzeCaptures(funcExpr)
	assert.Empty(t, captures)
}

func TestAnalyzeCaptures_MultipleCaptures_SortedByName(t *testing.T) {
	funcExpr := parseFuncExpr(t, `
		val zebra = 1
		val alpha = 2
		val middle = 3
		val f = fn() { zebra + alpha + middle }
	`)

	captures := AnalyzeCaptures(funcExpr)
	require.Len(t, captures, 3)
	// Output should be sorted by name for deterministic results.
	assert.Equal(t, "alpha", captures[0].Name)
	assert.Equal(t, "middle", captures[1].Name)
	assert.Equal(t, "zebra", captures[2].Name)
}

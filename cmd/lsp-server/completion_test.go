package main

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseAndInfer parses source code and returns the script and its scope.
func parseAndInfer(t *testing.T, source string) (*ast.Script, *checker.Scope) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, &ast.Source{
		Path:     "test.esc",
		Contents: source,
	})
	script, _ := p.ParseScript()

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scope, _ := c.InferScript(inferCtx, script)
	return script, scope
}

// getCompletionLabels extracts sorted labels from completion items.
func getCompletionLabels(items []protocol.CompletionItem) []string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
	}
	sort.Strings(labels)
	return labels
}

func TestMemberCompletionOnObject(t *testing.T) {
	source := `val obj: {a: number, b: string} = {a: 1, b: "hello"}
obj.`
	script, scope := parseAndInfer(t, source)

	// Cursor at end of "obj." — line 2, after the dot
	loc := ast.Location{Line: 2, Column: 5}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	assert.Equal(t, "", memberExpr.Prop.Name)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	items := completionsFromType(objType, scope)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"a", "b"}, labels)
}

func TestMemberCompletionFiltered(t *testing.T) {
	source := `val obj: {alpha: number, beta: string, able: boolean} = {alpha: 1, beta: "hi", able: true}
obj.al`
	script, scope := parseAndInfer(t, source)

	// Cursor at "al" after dot — line 2, col 7
	// The parser produces a MemberExpr with Prop.Name = "al"
	loc := ast.Location{Line: 2, Column: 7}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	assert.Equal(t, "al", memberExpr.Prop.Name)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	items := completionsFromType(objType, scope)
	items = filterByPrefix(items, memberExpr.Prop.Name)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"alpha"}, labels)
}

func TestMemberCompletionOnErrorType(t *testing.T) {
	source := `val obj: {a: number} = {a: 1}
val x = obj.
x.`
	script, scope := parseAndInfer(t, source)

	// Cursor at end of "x." — line 3, after the dot
	loc := ast.Location{Line: 3, Column: 3}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	items := completionsFromType(objType, scope)
	assert.Empty(t, items)
}

func TestScopeCompletionBasic(t *testing.T) {
	source := `val x: number = 42
val y: string = "hello"
x`
	script, scope := parseAndInfer(t, source)

	// Cursor at "x" on line 3
	loc := ast.Location{Line: 3, Column: 1}
	items := completionsFromScope(script, scope, loc)
	items = filterByPrefix(items, "x")
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "x")
	assert.NotContains(t, labels, "y")
}

func TestScopeCompletionIncludesFunctions(t *testing.T) {
	source := `fn greet(name: string) -> string { return name }
gre`
	script, scope := parseAndInfer(t, source)

	// Cursor at "gre" — line 2, col 3
	loc := ast.Location{Line: 2, Column: 3}
	items := completionsFromScope(script, scope, loc)
	items = filterByPrefix(items, "gre")
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"greet"}, labels)
}

func TestScopeCompletionPositionDependent(t *testing.T) {
	source := `val a: number = 1
val b: number = 2
a`
	script, scope := parseAndInfer(t, source)

	// Cursor at "a" on line 3 — both a and b should be visible
	loc := ast.Location{Line: 3, Column: 1}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["a"], "a should be in scope")
	assert.True(t, seen["b"], "b should be in scope")
}

func TestScopeCompletionExcludesFutureDecls(t *testing.T) {
	source := `val a: number = 1
a
val b: number = 2`
	script, scope := parseAndInfer(t, source)

	// Cursor at "a" on line 2 — only a should be visible, not b
	loc := ast.Location{Line: 2, Column: 1}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["a"], "a should be in scope")
	assert.False(t, seen["b"], "b should not be in scope yet")
}

func TestCompletionResultLimiting(t *testing.T) {
	items := make([]protocol.CompletionItem, 150)
	for i := range items {
		label := "item" + string(rune('A'+i%26))
		kind := protocol.CompletionItemKindVariable
		items[i] = protocol.CompletionItem{
			Label: label,
			Kind:  &kind,
		}
	}

	result := sortAndLimit(items)
	assert.Len(t, result, maxCompletionItems)
}

func TestFilterByPrefix(t *testing.T) {
	kind := protocol.CompletionItemKindProperty
	items := []protocol.CompletionItem{
		{Label: "toString", Kind: &kind},
		{Label: "toFixed", Kind: &kind},
		{Label: "valueOf", Kind: &kind},
	}

	filtered := filterByPrefix(items, "to")
	labels := getCompletionLabels(filtered)
	assert.Equal(t, []string{"toFixed", "toString"}, labels)
}

func TestFilterByPrefixCaseInsensitive(t *testing.T) {
	kind := protocol.CompletionItemKindProperty
	items := []protocol.CompletionItem{
		{Label: "ToString", Kind: &kind},
		{Label: "valueOf", Kind: &kind},
	}

	filtered := filterByPrefix(items, "to")
	assert.Len(t, filtered, 1)
	assert.Equal(t, "ToString", filtered[0].Label)
}

func TestStripNullUndefined(t *testing.T) {
	source := `val obj: {a: number} | null = {a: 1}
obj?.`
	script, scope := parseAndInfer(t, source)

	// Cursor after "?." — line 2, col 6
	loc := ast.Location{Line: 2, Column: 6}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	stripped := stripNullUndefined(objType)
	items := completionsFromType(stripped, scope)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"a"}, labels)
}

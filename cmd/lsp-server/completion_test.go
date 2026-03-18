package main

import (
	"context"
	"fmt"
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

// parseAndInferAllowErrors parses source code and returns the script and its scope,
// tolerating parse and type errors (for tests with intentionally malformed code).
func parseAndInferAllowErrors(t *testing.T, source string) (*ast.Script, *checker.Scope) {
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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
	script, scope := parseAndInferAllowErrors(t, source)

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
		label := fmt.Sprintf("item%03d", i)
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

func TestScopeCompletionInsideFuncBody(t *testing.T) {
	source := `fn add(a: number, b: number) -> number {
	val sum = a + b
	sum
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "sum" on line 3, inside the function body
	loc := ast.Location{Line: 3, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["a"], "param a should be in scope")
	assert.True(t, seen["b"], "param b should be in scope")
	assert.True(t, seen["sum"], "local var sum should be in scope")
	assert.True(t, seen["add"], "function add should be in scope (hoisted at script level)")
}

func TestScopeCompletionInsideFuncSeesOuterScope(t *testing.T) {
	source := `val outer: number = 10
fn foo() -> number {
	outer
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "outer" on line 3, inside foo's body
	loc := ast.Location{Line: 3, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["outer"], "outer var should be visible inside function")
	assert.True(t, seen["foo"], "function foo should be visible (hoisted)")
}

func TestScopeCompletionInsideNestedBlocks(t *testing.T) {
	source := `fn foo(x: number) -> number {
	val a = 1
	if (true) {
		val b = 2
		b
	} else {
		val c = 3
		a
	}
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "b" on line 5, inside the if-block
	loc := ast.Location{Line: 5, Column: 3}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "param x should be visible")
	assert.True(t, seen["a"], "outer var a should be visible")
	assert.True(t, seen["b"], "if-block var b should be visible")
	assert.False(t, seen["c"], "else-block var c should NOT be visible in if-block")

	// Cursor at "a" on line 8, inside the else-block
	loc2 := ast.Location{Line: 8, Column: 3}
	items2 := completionsFromScope(script, scope, loc2)

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["x"], "param x should be visible in else-block")
	assert.True(t, seen2["a"], "outer var a should be visible in else-block")
	assert.True(t, seen2["c"], "else-block var c should be visible")
	assert.False(t, seen2["b"], "if-block var b should NOT be visible in else-block")
}

func TestScopeCompletionInsideMatchCase(t *testing.T) {
	// Match with destructuring pattern: {myField} binds "myField" from the object
	source := `fn foo(obj: {myField: string}) -> string {
  match obj {
    {myField} => myField,
  }
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Line 3: "    {myField} => myField,"
	//          123456789012345678901
	// "myField" after => starts at col 19
	loc := ast.Location{Line: 3, Column: 19}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["obj"], "param obj should be visible")
	assert.True(t, seen["foo"], "function foo should be visible (hoisted)")
	assert.True(t, seen["myField"], "match case binding myField should be visible")
}

func TestScopeCompletionMatchBindingNotVisibleOutside(t *testing.T) {
	source := `fn foo(obj: {myField: string}) -> string {
  val x = match obj {
    {myField} => myField,
  }
  x
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "x" on line 5, outside the match expression
	loc := ast.Location{Line: 5, Column: 3}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["obj"], "param obj should be visible")
	assert.True(t, seen["x"], "local var x should be visible")
	assert.True(t, seen["foo"], "function foo should be visible (hoisted)")
	assert.False(t, seen["myField"], "match case binding myField should NOT be visible outside match")
}

func TestScopeCompletionInsideForIn(t *testing.T) {
	source := `val items: number[] = [1, 2, 3]
for item in items {
  item
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "item" on line 3, col 3
	loc := ast.Location{Line: 3, Column: 3}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["item"], "loop variable should be visible")
	assert.True(t, seen["items"], "outer var items should be visible")
}

func TestScopeCompletionForInBindingNotVisibleOutside(t *testing.T) {
	source := `val items: number[] = [1, 2, 3]
for item in items {
  item
}
items`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "items" on line 5, after the for-in loop
	loc := ast.Location{Line: 5, Column: 1}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["items"], "outer var items should be visible")
	assert.False(t, seen["item"], "loop variable should NOT be visible outside for-in")
}

func TestScopeCompletionInsideFuncExpr(t *testing.T) {
	source := `val greet = fn (name: string) -> string {
	name
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "name" on line 2, inside the function expression body
	loc := ast.Location{Line: 2, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["name"], "param name should be visible")
	assert.True(t, seen["greet"], "outer var greet should be visible")
}

func TestScopeCompletionFuncParamsNotVisibleOutside(t *testing.T) {
	source := `fn add(a: number, b: number) -> number {
	val sum = a + b
	sum
}
add`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "add" on line 5, outside the function
	loc := ast.Location{Line: 5, Column: 1}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["add"], "function add should be visible (hoisted)")
	assert.False(t, seen["a"], "param a should NOT be visible outside function")
	assert.False(t, seen["b"], "param b should NOT be visible outside function")
	assert.False(t, seen["sum"], "local var sum should NOT be visible outside function")
}

func TestScopeCompletionInsideTryCatch(t *testing.T) {
	source := `val x: number = 1
try {
	val a = 2
	a
} catch {
	error => {
		val b = 3
		b
	},
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor inside try block at "a" on line 4
	loc := ast.Location{Line: 4, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "outer var x should be visible in try block")
	assert.True(t, seen["a"], "try-block var a should be visible")
	assert.False(t, seen["error"], "catch pattern binding should NOT be visible in try block")
	assert.False(t, seen["b"], "catch-block var b should NOT be visible in try block")

	// Cursor inside catch block at "b" on line 8
	loc2 := ast.Location{Line: 8, Column: 3}
	items2 := completionsFromScope(script, scope, loc2)

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["x"], "outer var x should be visible in catch block")
	assert.True(t, seen2["error"], "catch pattern binding should be visible")
	assert.True(t, seen2["b"], "catch-block var b should be visible")
	assert.False(t, seen2["a"], "try-block var a should NOT be visible in catch block")
}

func TestScopeCompletionInsideIfLet(t *testing.T) {
	source := `val target: [number, string] | undefined = [1, "hi"]
if let [a, b] = target {
	a
} else {
	val c = 0
	c
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor inside consequent at "a" on line 3
	loc := ast.Location{Line: 3, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["target"], "outer var target should be visible")
	assert.True(t, seen["a"], "if-let binding a should be visible")
	assert.True(t, seen["b"], "if-let binding b should be visible")
	assert.False(t, seen["c"], "else-block var c should NOT be visible in consequent")

	// Cursor inside else at "c" on line 6
	loc2 := ast.Location{Line: 6, Column: 2}
	items2 := completionsFromScope(script, scope, loc2)

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["target"], "outer var target should be visible in else")
	assert.True(t, seen2["c"], "else-block var c should be visible")
	assert.False(t, seen2["a"], "if-let binding a should NOT be visible in else")
	assert.False(t, seen2["b"], "if-let binding b should NOT be visible in else")
}

func TestScopeCompletionInsideDoExpr(t *testing.T) {
	source := `val outer: number = 10
val result = do {
	val inner = 20
	inner
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "inner" on line 4
	loc := ast.Location{Line: 4, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["outer"], "outer var should be visible inside do block")
	assert.True(t, seen["inner"], "do-block var inner should be visible")
}

func TestScopeCompletionInsideIfElse(t *testing.T) {
	source := `val x: number = 1
if (true) {
	val a = 2
	a
} else {
	val b = 3
	b
}`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor inside consequent at "a" on line 4
	loc := ast.Location{Line: 4, Column: 2}
	items := completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "outer var x should be visible in if-block")
	assert.True(t, seen["a"], "if-block var a should be visible")
	assert.False(t, seen["b"], "else-block var b should NOT be visible in if-block")

	// Cursor inside else at "b" on line 7
	loc2 := ast.Location{Line: 7, Column: 2}
	items2 := completionsFromScope(script, scope, loc2)

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["x"], "outer var x should be visible in else-block")
	assert.True(t, seen2["b"], "else-block var b should be visible")
	assert.False(t, seen2["a"], "if-block var a should NOT be visible in else-block")
}

func TestUnionCompletionIncludesAllProperties(t *testing.T) {
	source := `val obj: {a: number, b: string} | {a: number, c: boolean} = {a: 1, b: "hi"}
obj.`
	script, scope := parseAndInferAllowErrors(t, source)

	loc := ast.Location{Line: 2, Column: 5}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	items := completionsFromType(objType, scope)
	labels := getCompletionLabels(items)
	// All properties from any variant should be included
	assert.Equal(t, []string{"a", "b", "c"}, labels)

	// Properties not on all variants should have "| undefined" in detail
	detailByLabel := map[string]string{}
	for _, item := range items {
		if item.Detail != nil {
			detailByLabel[item.Label] = *item.Detail
		}
	}
	assert.Equal(t, "number", detailByLabel["a"], "a is on both variants")
	assert.Equal(t, "string | undefined", detailByLabel["b"], "b is only on one variant")
	assert.Equal(t, "boolean | undefined", detailByLabel["c"], "c is only on one variant")
}

func TestIntersectionCompletionOnlyCommonKeys(t *testing.T) {
	source := `val obj: {a: number, b: string} & {a: number, c: boolean} = {a: 1, b: "hi", c: true}
obj.`
	script, scope := parseAndInferAllowErrors(t, source)

	loc := ast.Location{Line: 2, Column: 5}
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	items := completionsFromType(objType, scope)
	labels := getCompletionLabels(items)
	// Only keys present in ALL parts should be included
	assert.Equal(t, []string{"a"}, labels)
}

func TestStripNullUndefined(t *testing.T) {
	source := `val obj: {a: number} | null = {a: 1}
obj?.`
	script, scope := parseAndInferAllowErrors(t, source)

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

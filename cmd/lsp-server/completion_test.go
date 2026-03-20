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
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/tliron/glsp"
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

// testServer returns a minimal *Server for use in completion tests.
func testServer() *Server {
	return NewServer()
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

// scriptCompletions exercises the full textDocumentCompletion handler for a
// script file, returning the completion items.
func scriptCompletions(t *testing.T, source string, loc ast.Location) []protocol.CompletionItem {
	t.Helper()
	uri := protocol.DocumentUri("file:///test.esc")
	script, scope := parseAndInferAllowErrors(t, source)

	s := testServer()
	version := protocol.Integer(1)
	s.documents[uri] = protocol.TextDocumentItem{
		URI:        uri,
		LanguageID: "escalier",
		Version:    version,
		Text:       source,
	}
	s.astCache[uri] = script
	s.scopeCache[uri] = scope
	s.validatedVersion[uri] = version

	// LSP positions are 0-based; loc is already 1-based from the test.
	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      protocol.UInteger(loc.Line - 1),
				Character: protocol.UInteger(loc.Column - 1),
			},
		},
	}
	result, err := s.textDocumentCompletion(&glsp.Context{}, params)
	require.NoError(t, err)
	if result == nil {
		return nil
	}
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok, "expected *CompletionList, got %T", result)
	return list.Items
}

func TestNoCompletionsOnIdentPat(t *testing.T) {
	// Cursor at "p" — line 1, col 5
	items := scriptCompletions(t, `val p`, ast.Location{Line: 1, Column: 5})
	assert.Empty(t, items, "should not provide completions when cursor is on IdentPat")
}

func TestNoCompletionsOnIdentPatInCompleteDecl(t *testing.T) {
	source := "type Point = {x: number, y: number}\nval p = 10"
	// Cursor right after "p" in a complete declaration — still in the pattern.
	items := scriptCompletions(t, source, ast.Location{Line: 2, Column: 6})
	assert.Empty(t, items, "should not provide completions on IdentPat in complete val decl")
}

func TestNoCompletionsOnIdentPatInIncompleteDecl(t *testing.T) {
	source := "type Point = {x: number, y: number}\nval p"
	// Cursor right after "p" in an incomplete declaration — still in the pattern.
	items := scriptCompletions(t, source, ast.Location{Line: 2, Column: 6})
	assert.Empty(t, items, "should not provide completions on IdentPat in incomplete val decl")
}

func TestCompletionsOnIdentExpr(t *testing.T) {
	source := "type Point = {x: number, y: number}\np"
	// Cursor at "p" on line 2 — this is an IdentExpr, not IdentPat.
	items := scriptCompletions(t, source, ast.Location{Line: 2, Column: 1})
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "Point", "should provide completions when cursor is on IdentExpr")
}

func TestScopeCompletionBasic(t *testing.T) {
	source := `val x: number = 42
val y: string = "hello"
x`
	script, scope := parseAndInferAllowErrors(t, source)

	// Cursor at "x" on line 3
	loc := ast.Location{Line: 3, Column: 1}
	items := testServer().completionsFromScope(script, scope, loc)
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
	items := testServer().completionsFromScope(script, scope, loc)
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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items2 := testServer().completionsFromScope(script, scope, loc2)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items2 := testServer().completionsFromScope(script, scope, loc2)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items2 := testServer().completionsFromScope(script, scope, loc2)

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
	items := testServer().completionsFromScope(script, scope, loc)

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
	items := testServer().completionsFromScope(script, scope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "outer var x should be visible in if-block")
	assert.True(t, seen["a"], "if-block var a should be visible")
	assert.False(t, seen["b"], "else-block var b should NOT be visible in if-block")

	// Cursor inside else at "b" on line 7
	loc2 := ast.Location{Line: 7, Column: 2}
	items2 := testServer().completionsFromScope(script, scope, loc2)

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

// --- Module completion tests ---

// parseModuleAndInfer parses multiple sources as a module and returns the
// module, module scope, and file scopes.
func parseModuleAndInfer(t *testing.T, sources []*ast.Source) (*ast.Module, *checker.Scope, map[int]*checker.Scope) {
	t.Helper()
	return parseModuleAndInferWithPackages(t, sources, nil)
}

// parseModuleAndInferWithPackages parses multiple sources as a module, registers
// mock packages, and returns the module, module scope, and file scopes.
func parseModuleAndInferWithPackages(
	t *testing.T,
	sources []*ast.Source,
	packages map[string]*type_system.Namespace,
) (*ast.Module, *checker.Scope, map[int]*checker.Scope) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	for _, err := range parseErrors {
		t.Logf("parse error: %s", err.Message)
	}

	c := checker.NewChecker()
	for name, ns := range packages {
		err := c.PackageRegistry.Register(name, ns)
		require.NoError(t, err, "registering mock package %q", name)
	}
	inferCtx := checker.Context{
		Scope:      checker.Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	typeErrors := c.InferModule(inferCtx, module)
	for _, err := range typeErrors {
		t.Logf("type error: %s", err.Message())
	}

	return module, inferCtx.Scope, c.FileScopes
}

func TestModuleCrossFileDeclarationsVisible(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/types.esc", Contents: `type UserId = number`},
		{ID: 1, Path: "lib/utils.esc", Contents: `
fn getUser(id: UserId) -> UserId { id }
val defaultId: UserId = 0
`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at start of a new line in utils.esc (after the declarations)
	// Line 3, col 1 (inside the file, after defaultId declaration)
	loc := ast.Location{Line: 4, Column: 1}
	fileScope := fileScopes[1]

	items := testServer().completionsFromModuleScope(module, 1, fileScope, moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	// Declarations from other files should be visible
	assert.True(t, seen["getUser"], "getUser from same file should be visible")
	assert.True(t, seen["defaultId"], "defaultId from same file should be visible")
	// Type from other file should be visible (collected from scope)
	assert.True(t, seen["UserId"], "UserId type from types.esc should be visible")
}

func TestModulePositionDependentSameFile(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `val a: number = 1
val b: number = 2
val c: number = 3`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at line 2, col 1 — between a and b declarations
	// All top-level declarations are visible anywhere in the file because
	// the DepGraph reorders them before type checking and code generation.
	loc := ast.Location{Line: 2, Column: 1}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	assert.True(t, seen["a"], "a should be visible (before cursor)")
	assert.True(t, seen["b"], "b should be visible (same line as cursor)")
	assert.True(t, seen["c"], "c should be visible (DepGraph reorders all declarations)")
}

func TestModuleOtherFileDeclsAlwaysVisible(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/file1.esc", Contents: `val x: number = 1`},
		{ID: 1, Path: "lib/file2.esc", Contents: `val a: number = 10
val b: number = 20`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at line 1 col 1 in file1 — all declarations from file2 should be visible
	// even though the cursor is "before" them (they're in a different file)
	loc := ast.Location{Line: 1, Column: 1}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	assert.True(t, seen["x"], "x from same file should be visible")
	assert.True(t, seen["a"], "a from file2 should be visible (cross-file)")
	assert.True(t, seen["b"], "b from file2 should be visible (cross-file)")
}

func TestModuleFuncDeclsAreAlwaysVisible(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `val x: number = 1
fn laterFunc() -> number { 42 }`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at line 1 col 1 — before laterFunc declaration
	// Function declarations are always visible in modules just like all other
	// declarations
	loc := ast.Location{Line: 1, Column: 1}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	assert.True(t, seen["x"], "x should be visible")
	assert.True(t, seen["laterFunc"], "laterFunc should be visible (hoisted)")
}

func TestModuleMemberCompletionOnCrossFileType(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/types.esc", Contents: `type Point = {x: number, y: number}`},
		{ID: 1, Path: "lib/main.esc", Contents: `fn usePoint(p: Point) -> number {
	p.
}`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor after "p." inside the function body — line 2, col 3
	loc := ast.Location{Line: 2, Column: 4}
	node, _ := findNodeAndParentInFile(module, 1, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	// Use the prelude scope for type lookups (wrapper type aliases)
	lookupScope := moduleScope
	if lookupScope.Parent != nil {
		lookupScope = lookupScope.Parent
	}
	_ = fileScopes // not needed for member completions

	items := completionsFromType(objType, lookupScope)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"x", "y"}, labels)
}

func TestModuleCompletionInsideFuncBody(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `val outer: number = 10
fn foo(a: number) -> number {
	val inner = a + 1
	val later = 5
	inner + later
}`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at "inner" on line 5, inside foo's body (after inner and later declarations)
	loc := ast.Location{Line: 5, Column: 2}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	assert.True(t, seen["a"], "param a should be visible")
	assert.True(t, seen["inner"], "local var inner should be visible")
	assert.True(t, seen["later"], "local var later should be visible (declared before cursor)")
	assert.True(t, seen["outer"], "module-level outer should be visible")
	assert.True(t, seen["foo"], "function foo should be visible (hoisted)")

	// Cursor at line 3, col 15 (after "val inner" declaration but before "later" declaration)
	// Variables declared after this point should not be visible
	locEarly := ast.Location{Line: 3, Column: 15}
	itemsEarly := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, locEarly)
	seenEarly := map[string]bool{}
	for _, item := range itemsEarly {
		seenEarly[item.Label] = true
	}

	assert.True(t, seenEarly["a"], "param a should be visible")
	assert.True(t, seenEarly["inner"], "local var inner should be visible (just declared)")
	assert.False(t, seenEarly["later"], "local var later should NOT be visible (declared after cursor)")
	assert.True(t, seenEarly["outer"], "module-level outer should be visible")
	assert.True(t, seenEarly["foo"], "function foo should be visible (hoisted)")
}

func TestModuleImportVisibleInImportingFile(t *testing.T) {
	// file1 imports "test-utils" as utils; file2 does not.
	// The "utils" namespace should appear in file1's completions but not file2's.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/file1.esc", Contents: `import * as utils from "test-utils"
declare val x: number`},
		{ID: 1, Path: "lib/file2.esc", Contents: `declare val y: number`},
	}

	mockPkg := type_system.NewNamespace()
	mockPkg.Values["helper"] = &type_system.Binding{
		Type:     type_system.NewNumPrimType(nil),
		Mutable:  false,
		Exported: true,
	}

	packages := map[string]*type_system.Namespace{
		"test-utils": mockPkg,
	}

	module, moduleScope, fileScopes := parseModuleAndInferWithPackages(t, sources, packages)

	// Completions from file1 (which has the import)
	loc := ast.Location{Line: 2, Column: 1}
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}

	assert.True(t, seen1["utils"], "utils namespace should be visible in importing file")
	assert.True(t, seen1["x"], "x from same file should be visible")
	assert.True(t, seen1["y"], "y from other file should be visible (cross-file)")

	// Completions from file2 (which does NOT have the import)
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, loc)
	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}

	assert.False(t, seen2["utils"], "utils namespace should NOT be visible in non-importing file")
	assert.True(t, seen2["y"], "y from same file should be visible")
	assert.True(t, seen2["x"], "x from other file should be visible (cross-file)")
}

func TestModuleImportValuesVisibleOnlyInImportingFile(t *testing.T) {
	// file1 imports named values from "test-pkg"; file2 does not.
	// The imported value bindings should appear only in file1's completions.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/file1.esc", Contents: `import * as pkg from "test-pkg"
fn usePkg() -> number { 1 }`},
		{ID: 1, Path: "lib/file2.esc", Contents: `fn other() -> number { 2 }`},
	}

	mockPkg := type_system.NewNamespace()
	mockPkg.Values["add"] = &type_system.Binding{
		Type: type_system.NewFuncType(nil, nil,
			[]*type_system.FuncParam{
				type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewNumPrimType(nil)),
				type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewNumPrimType(nil)),
			},
			type_system.NewNumPrimType(nil),
			type_system.NewNeverType(nil),
		),
		Mutable:  false,
		Exported: true,
	}
	mockPkg.Types["MyType"] = &type_system.TypeAlias{
		Type:       type_system.NewStrPrimType(nil),
		TypeParams: nil,
		Exported:   true,
	}

	packages := map[string]*type_system.Namespace{
		"test-pkg": mockPkg,
	}

	module, moduleScope, fileScopes := parseModuleAndInferWithPackages(t, sources, packages)

	loc := ast.Location{Line: 2, Column: 1}

	// file1 completions: pkg namespace should be present
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}
	assert.True(t, seen1["pkg"], "pkg namespace should be visible in file1 (imports it)")
	assert.True(t, seen1["usePkg"], "usePkg from same file should be visible")
	assert.True(t, seen1["other"], "other from file2 should be visible (cross-file)")

	// file2 completions: pkg namespace should NOT be present
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, loc)
	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.False(t, seen2["pkg"], "pkg namespace should NOT be visible in file2 (no import)")
	assert.True(t, seen2["other"], "other from same file should be visible")
	assert.True(t, seen2["usePkg"], "usePkg from file1 should be visible (cross-file)")
}

func TestModuleNamespaceVisibleFromRootFile(t *testing.T) {
	// Files in lib/math/ create a "math" namespace.
	// A file in lib/ (root namespace) should see "math" as a namespace completion.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `val x: number = 1`},
		{ID: 1, Path: "lib/math/add.esc", Contents: `fn add(a: number, b: number) -> number { a + b }`},
		{ID: 2, Path: "lib/math/sub.esc", Contents: `fn sub(a: number, b: number) -> number { a - b }`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Completions from main.esc (root namespace)
	loc := ast.Location{Line: 1, Column: 1}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)

	seen := map[string]bool{}
	kindByLabel := map[string]protocol.CompletionItemKind{}
	for _, item := range items {
		seen[item.Label] = true
		if item.Kind != nil {
			kindByLabel[item.Label] = *item.Kind
		}
	}

	assert.True(t, seen["x"], "x from same file should be visible")
	assert.True(t, seen["math"], "math namespace should be visible from root file")
	assert.Equal(t, protocol.CompletionItemKindModule, kindByLabel["math"], "math should have Module completion kind")
}

func TestModuleNamespaceDeclsVisibleWithinSameNamespace(t *testing.T) {
	// Files in lib/math/ share the "math" namespace.
	// Declarations from add.esc should be visible in sub.esc and vice versa.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/math/add.esc", Contents: `fn add(a: number, b: number) -> number { a + b }`},
		{ID: 1, Path: "lib/math/sub.esc", Contents: `fn sub(a: number, b: number) -> number { a - b }`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	loc := ast.Location{Line: 1, Column: 1}

	// Completions from add.esc
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}

	assert.True(t, seen1["add"], "add from same file should be visible")
	assert.True(t, seen1["sub"], "sub from other file in same namespace should be visible")

	// Completions from sub.esc
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, loc)
	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}

	assert.True(t, seen2["sub"], "sub from same file should be visible")
	assert.True(t, seen2["add"], "add from other file in same namespace should be visible")
}

func TestModuleMultipleNamespacesVisible(t *testing.T) {
	// Multiple subdirectories create multiple namespaces.
	// A root file should see all namespace names as completions.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `val x: number = 1`},
		{ID: 1, Path: "lib/math/add.esc", Contents: `fn add(a: number, b: number) -> number { a + b }`},
		{ID: 2, Path: "lib/strings/concat.esc", Contents: `fn concat(a: string, b: string) -> string { a ++ b }`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	loc := ast.Location{Line: 1, Column: 1}
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, loc)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	assert.True(t, seen["math"], "math namespace should be visible")
	assert.True(t, seen["strings"], "strings namespace should be visible")
	assert.True(t, seen["x"], "x from same file should be visible")
	// Declarations inside namespaces should NOT appear directly in root scope
	assert.False(t, seen["add"], "add should not be directly visible from root (it's inside math namespace)")
	assert.False(t, seen["concat"], "concat should not be directly visible from root (it's inside strings namespace)")
}

func TestModuleNamespaceMemberCompletion(t *testing.T) {
	// Accessing math.add should provide member completions from the math namespace.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `fn useAdd() -> number {
	math.
}`},
		{ID: 1, Path: "lib/math/add.esc", Contents: `fn add(a: number, b: number) -> number { a + b }`},
		{ID: 2, Path: "lib/math/sub.esc", Contents: `fn sub(a: number, b: number) -> number { a - b }`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor after "math." — line 2, col 7
	loc := ast.Location{Line: 2, Column: 7}
	node, _ := findNodeAndParentInFile(module, 0, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	lookupScope := moduleScope
	if lookupScope.Parent != nil {
		lookupScope = lookupScope.Parent
	}
	_ = fileScopes

	items := completionsFromType(objType, lookupScope)
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "add")
	assert.Contains(t, labels, "sub")
}

func TestModuleSubdirectoryFileSeesParentNamespaceDecls(t *testing.T) {
	// Root file has declarations that should be visible in subdirectory files
	// (declarations in the parent namespace)
	sources := []*ast.Source{
		{ID: 0, Path: "main.esc", Contents: `val rootDecl: number = 1
fn rootFunc() -> string { "hello" }`},
		{ID: 1, Path: "lib/helper.esc", Contents: `fn helperFunc() -> number {
	rootDecl + 1
}`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor at "rootDecl" on line 2 inside helperFunc in the lib/helper.esc file
	loc := ast.Location{Line: 2, Column: 2}
	items := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, loc)
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}

	// Declarations from the parent namespace (root file) should be visible
	assert.True(t, seen["rootDecl"], "rootDecl from parent namespace should be visible")
	assert.True(t, seen["rootFunc"], "rootFunc from parent namespace should be visible")
	assert.True(t, seen["helperFunc"], "helperFunc from same file should be visible")
}

func TestModuleScopeCompletionFilteredByPrefix(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "main.esc", Contents: `val rootDecl: number = 1
fn rootFunc() -> string { "hello" }
val other: number = 2`},
		{ID: 1, Path: "lib/helper.esc", Contents: `fn helperFunc() -> number {
	roo
}`},
	}
	module, moduleScope, fileScopes := parseModuleAndInfer(t, sources)

	// Cursor on the partial identifier "roo" in helper.esc.
	loc := ast.Location{Line: 2, Column: 4}
	node, _ := findNodeAndParentInFile(module, 1, loc)
	require.NotNil(t, node)
	identExpr, ok := node.(*ast.IdentExpr)
	require.True(t, ok, "expected IdentExpr, got %T", node)
	require.Equal(t, "roo", identExpr.Name)

	items := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, loc)
	filtered := filterByPrefix(items, identExpr.Name)
	labels := getCompletionLabels(filtered)

	assert.Equal(t, []string{"rootDecl", "rootFunc"}, labels)
}

func TestModuleNamespaceMemberCompletionFilteredByPrefix(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/main.esc", Contents: `fn useMath() -> number {
	math.s
}`},
		{ID: 1, Path: "lib/math/add.esc", Contents: `fn add(a: number, b: number) -> number { a + b }`},
		{ID: 2, Path: "lib/math/sub.esc", Contents: `fn sub(a: number, b: number) -> number { a - b }`},
	}
	module, moduleScope, _ := parseModuleAndInfer(t, sources)

	// Cursor on the partial member "s" in "math.s".
	loc := ast.Location{Line: 2, Column: 8}
	node, _ := findNodeAndParentInFile(module, 0, loc)
	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	require.Equal(t, "s", memberExpr.Prop.Name)

	objType := memberExpr.Object.InferredType()
	require.NotNil(t, objType)

	lookupScope := moduleScope
	if lookupScope.Parent != nil {
		lookupScope = lookupScope.Parent
	}

	items := completionsFromType(objType, lookupScope)
	filtered := filterByPrefix(items, memberExpr.Prop.Name)
	labels := getCompletionLabels(filtered)

	assert.Equal(t, []string{"sub"}, labels)
}

package main

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkScript parses source as a bin/ script with no library and checks it, tolerating
// parse and type errors so a test can hold intentionally malformed code.
func checkScript(t *testing.T, source string) compiler.BinScriptOutput {
	t.Helper()
	out := compiler.CheckBinScript(context.Background(), nil, &ast.Source{
		Path:     "test.esc",
		Contents: source,
	}, compiler.PackageConfig{})
	require.NotNil(t, out.Info)
	return out
}

// parseAndInferAllowErrors is checkScript returning the script, a view of its scope,
// and the per-node records the check made.
func parseAndInferAllowErrors(t *testing.T, source string) (*ast.Script, scopeView, *solver.Info) {
	t.Helper()
	out := checkScript(t, source)
	return out.Script, newSolverScopeView(out.Scope, out.Info), out.Info
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

// offsetToPos converts a Location back into the position an LSP client would
// have sent for it, so a test can drive a handler through its real entry point.
func offsetToPos(src string, loc ast.Location) protocol.Position {
	line, column := ast.NewLineMap(src).Position(loc.Offset, ast.UTF16Columns)
	return protocol.Position{
		Line:      protocol.UInteger(line - 1),
		Character: protocol.UInteger(column - 1),
	}
}

// cursorAt returns the position a 1-based line and column names in src. Tests
// write a cursor the way an editor shows one, and a Location holds a byte
// offset, so the two are converted here rather than at each call.
func cursorAt(src string, line, column int) ast.Location {
	return ast.Location{Offset: ast.NewLineMap(src).Offset(line, column, ast.CodePointColumns)}
}

func TestMemberCompletionHidesMutSelfOnImmutableReceiver(t *testing.T) {
	source := `declare val m: Map<string, number>
m.`
	script, _, info := parseAndInferAllowErrors(t, source)

	// Cursor at end of "m." — line 2, after the dot
	loc := cursorAt(source, 2, 3)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "has", "read-only method should be visible on immutable receiver")
	assert.Contains(t, labels, "get", "read-only method should be visible on immutable receiver")
	assert.NotContains(t, labels, "clear", "mut self method should be hidden on immutable receiver")
	assert.NotContains(t, labels, "set", "mut self method should be hidden on immutable receiver")
	assert.NotContains(t, labels, "delete", "mut self method should be hidden on immutable receiver")
}

func TestMemberCompletionShowsMutSelfOnMutableReceiver(t *testing.T) {
	source := `declare val m: mut Map<string, number>
m.`
	script, _, info := parseAndInferAllowErrors(t, source)

	loc := cursorAt(source, 2, 3)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "has")
	assert.Contains(t, labels, "get")
	assert.Contains(t, labels, "clear", "mut self method should be visible on mut receiver")
	assert.Contains(t, labels, "set", "mut self method should be visible on mut receiver")
	assert.Contains(t, labels, "delete", "mut self method should be visible on mut receiver")
}

func TestMemberCompletionGetterVisibleSetterHiddenOnImmutableReceiver(t *testing.T) {
	source := `declare val obj: {
    get bar() -> boolean,
    set bar(value: boolean) -> undefined
}
obj.`
	script, _, info := parseAndInferAllowErrors(t, source)

	loc := cursorAt(source, 5, 5)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)

	var barItems []protocol.CompletionItem
	for _, item := range items {
		if item.Label == "bar" {
			barItems = append(barItems, item)
		}
	}
	require.Len(t, barItems, 1, "getter should produce exactly one bar completion on immutable receiver; setter should be hidden")
	require.NotNil(t, barItems[0].Detail)
	assert.NotEqual(t, "setter", *barItems[0].Detail, "the surviving bar item should be the getter, not the setter")
}

func TestMemberCompletionGetterAndSetterBothVisibleOnMutableReceiver(t *testing.T) {
	source := `declare val obj: mut {
    get bar() -> boolean,
    set bar(value: boolean) -> undefined
}
obj.`
	script, _, info := parseAndInferAllowErrors(t, source)

	loc := cursorAt(source, 5, 5)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)

	var barItems []protocol.CompletionItem
	for _, item := range items {
		if item.Label == "bar" {
			barItems = append(barItems, item)
		}
	}
	require.Len(t, barItems, 2, "both getter and setter should produce bar completions on mutable receiver")
}

func TestMemberCompletionOnObject(t *testing.T) {
	source := `val obj: {a: number, b: string} = {a: 1, b: "hello"}
obj.`
	script, _, info := parseAndInferAllowErrors(t, source)

	// Cursor at end of "obj." — line 2, after the dot
	loc := cursorAt(source, 2, 5)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	assert.Equal(t, "", memberExpr.Prop.Name)

	items := testServer().memberCompletions(memberExpr, info, nil)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"a", "b"}, labels)
}

func TestMemberCompletionFiltered(t *testing.T) {
	source := `val obj: {alpha: number, beta: string, able: boolean} = {alpha: 1, beta: "hi", able: true}
obj.al`
	script, _, info := parseAndInferAllowErrors(t, source)

	// Cursor at "al" after dot — line 2, col 7
	// The parser produces a MemberExpr with Prop.Name = "al"
	loc := cursorAt(source, 2, 7)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	assert.Equal(t, "al", memberExpr.Prop.Name)

	items := testServer().memberCompletions(memberExpr, info, nil)
	items = filterByPrefix(items, memberExpr.Prop.Name)
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"alpha"}, labels)
}

func TestMemberCompletionOnErrorType(t *testing.T) {
	source := `val obj: {a: number} = {a: 1}
val x = obj.
x.`
	script, _, info := parseAndInferAllowErrors(t, source)

	// Cursor at end of "x." — line 3, after the dot
	loc := cursorAt(source, 3, 3)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
	assert.Empty(t, items)
}

// scriptCompletions exercises the full textDocumentCompletion handler for a
// script file, returning the completion items.
func scriptCompletions(t *testing.T, source string, loc ast.Location) []protocol.CompletionItem {
	t.Helper()
	uri := protocol.DocumentUri("file:///test.esc")
	out := checkScript(t, source)

	s := testServer()
	s.rootURI = "file:///"
	sourceID := stableSourceID("test.esc")
	version := protocol.Integer(1)
	s.documents[uri] = protocol.TextDocumentItem{
		URI:        uri,
		LanguageID: "escalier",
		Version:    version,
		Text:       source,
	}
	s.checkOutput = &compiler.CheckOutput{
		Scripts:      map[int]*ast.Script{sourceID: out.Script},
		ScriptInfos:  map[int]*solver.Info{sourceID: out.Info},
		ScriptScopes: map[int]*solver.Scope{sourceID: out.Scope},
	}
	s.validatedVersion[uri] = version

	// The handler takes an LSP position, so the offset goes back through the
	// same conversion the server applies to a real request.
	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     offsetToPos(source, loc),
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
	items := scriptCompletions(t, "val p", cursorAt("val p", 1, 5))
	assert.Empty(t, items, "should not provide completions when cursor is on IdentPat")
}

func TestNoCompletionsOnIdentPatInCompleteDecl(t *testing.T) {
	source := "type Point = {x: number, y: number}\nval p = 10"
	// Cursor right after "p" in a complete declaration — still in the pattern.
	items := scriptCompletions(t, source, cursorAt(source, 2, 6))
	assert.Empty(t, items, "should not provide completions on IdentPat in complete val decl")
}

func TestNoCompletionsOnIdentPatInIncompleteDecl(t *testing.T) {
	source := "type Point = {x: number, y: number}\nval p"
	// Cursor right after "p" in an incomplete declaration — still in the pattern.
	items := scriptCompletions(t, source, cursorAt(source, 2, 6))
	assert.Empty(t, items, "should not provide completions on IdentPat in incomplete val decl")
}

func TestCompletionsOnTypeAnnotationInVarDecl(t *testing.T) {
	source := "type Point = {x: number, y: number}\nval p: P"

	// Cursor on "P" in the type annotation (col 8) and right after "P" (col 9).
	// In `val p: P`, P starts at column 8.
	for _, col := range []int{8, 9} {
		items := scriptCompletions(t, source, cursorAt(source, 2, col))
		labels := getCompletionLabels(items)
		assert.Contains(t, labels, "Point",
			"col %d: should provide type completions in type annotation position", col)
	}
}

func TestCompletionsOnIdentExpr(t *testing.T) {
	source := "type Point = {x: number, y: number}\np"
	// Cursor at "p" on line 2 — this is an IdentExpr, not IdentPat.
	items := scriptCompletions(t, source, cursorAt(source, 2, 1))
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "Point", "should provide completions when cursor is on IdentExpr")
}

func TestScopeCompletionBasic(t *testing.T) {
	source := `val x: number = 42
val y: string = "hello"
x`
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "x" on line 3
	loc := cursorAt(source, 3, 1)
	items := testServer().completionsFromScope(script, scope, info, loc, "")
	items = filterByPrefix(items, "x")
	labels := getCompletionLabels(items)
	assert.Contains(t, labels, "x")
	assert.NotContains(t, labels, "y")
}

func TestScopeCompletionIncludesFunctions(t *testing.T) {
	source := `fn greet(name: string) -> string { return name }
gre`
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "gre" — line 2, col 3
	loc := cursorAt(source, 2, 3)
	items := testServer().completionsFromScope(script, scope, info, loc, "")
	items = filterByPrefix(items, "gre")
	labels := getCompletionLabels(items)
	assert.Equal(t, []string{"greet"}, labels)
}

func TestScopeCompletionPositionDependent(t *testing.T) {
	source := `val a: number = 1
val b: number = 2
a`
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "a" on line 3 — both a and b should be visible
	loc := cursorAt(source, 3, 1)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "a" on line 2 — only a should be visible, not b
	loc := cursorAt(source, 2, 1)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "sum" on line 3, inside the function body
	loc := cursorAt(source, 3, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "outer" on line 3, inside foo's body
	loc := cursorAt(source, 3, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "b" on line 5, inside the if-block
	loc := cursorAt(source, 5, 3)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "param x should be visible")
	assert.True(t, seen["a"], "outer var a should be visible")
	assert.True(t, seen["b"], "if-block var b should be visible")
	assert.False(t, seen["c"], "else-block var c should NOT be visible in if-block")

	// Cursor at "a" on line 8, inside the else-block
	loc2 := cursorAt(source, 8, 3)
	items2 := testServer().completionsFromScope(script, scope, info, loc2, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Line 3: "    {myField} => myField,"
	//          123456789012345678901
	// "myField" after => starts at col 19
	loc := cursorAt(source, 3, 19)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "x" on line 5, outside the match expression
	loc := cursorAt(source, 5, 3)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "item" on line 3, col 3
	loc := cursorAt(source, 3, 3)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "items" on line 5, after the for-in loop
	loc := cursorAt(source, 5, 1)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "name" on line 2, inside the function expression body
	loc := cursorAt(source, 2, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "add" on line 5, outside the function
	loc := cursorAt(source, 5, 1)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor inside try block at "a" on line 4
	loc := cursorAt(source, 4, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "outer var x should be visible in try block")
	assert.True(t, seen["a"], "try-block var a should be visible")
	assert.False(t, seen["error"], "catch pattern binding should NOT be visible in try block")
	assert.False(t, seen["b"], "catch-block var b should NOT be visible in try block")

	// Cursor inside catch block at "b" on line 8
	loc2 := cursorAt(source, 8, 3)
	items2 := testServer().completionsFromScope(script, scope, info, loc2, "")

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["x"], "outer var x should be visible in catch block")
	assert.True(t, seen2["error"], "catch pattern binding should be visible")
	assert.True(t, seen2["b"], "catch-block var b should be visible")
	assert.False(t, seen2["a"], "try-block var a should NOT be visible in catch block")
}

func TestScopeCompletionInsideIfVal(t *testing.T) {
	source := `val target: [number, string] | undefined = [1, "hi"]
if val [a, b] = target {
	a
} else {
	val c = 0
	c
}`
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor inside consequent at "a" on line 3
	loc := cursorAt(source, 3, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["target"], "outer var target should be visible")
	assert.True(t, seen["a"], "if-val binding a should be visible")
	assert.True(t, seen["b"], "if-val binding b should be visible")
	assert.False(t, seen["c"], "else-block var c should NOT be visible in consequent")

	// Cursor inside else at "c" on line 6
	loc2 := cursorAt(source, 6, 2)
	items2 := testServer().completionsFromScope(script, scope, info, loc2, "")

	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.True(t, seen2["target"], "outer var target should be visible in else")
	assert.True(t, seen2["c"], "else-block var c should be visible")
	assert.False(t, seen2["a"], "if-val binding a should NOT be visible in else")
	assert.False(t, seen2["b"], "if-val binding b should NOT be visible in else")
}

func TestScopeCompletionInsideDoExpr(t *testing.T) {
	source := `val outer: number = 10
val result = do {
	val inner = 20
	inner
}`
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor at "inner" on line 4
	loc := cursorAt(source, 4, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

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
	script, scope, info := parseAndInferAllowErrors(t, source)

	// Cursor inside consequent at "a" on line 4
	loc := cursorAt(source, 4, 2)
	items := testServer().completionsFromScope(script, scope, info, loc, "")

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Label] = true
	}
	assert.True(t, seen["x"], "outer var x should be visible in if-block")
	assert.True(t, seen["a"], "if-block var a should be visible")
	assert.False(t, seen["b"], "else-block var b should NOT be visible in if-block")

	// Cursor inside else at "b" on line 7
	loc2 := cursorAt(source, 7, 2)
	items2 := testServer().completionsFromScope(script, scope, info, loc2, "")

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
	script, _, info := parseAndInferAllowErrors(t, source)

	loc := cursorAt(source, 2, 5)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
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
	script, _, info := parseAndInferAllowErrors(t, source)

	loc := cursorAt(source, 2, 5)
	node, _ := findNodeAndParent(script, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
	labels := getCompletionLabels(items)
	// Only keys present in ALL parts should be included
	assert.Equal(t, []string{"a"}, labels)
}

// --- Module completion tests ---

// parseModuleAndInfer parses multiple sources as a module and returns the
// module, module scope, and file scopes.
func parseModuleAndInfer(t *testing.T, sources []*ast.Source) (*ast.Module, scopeView, map[int]scopeView, *solver.Info) {
	t.Helper()
	out := checkModule(t, sources)
	fileScopes := make(map[int]scopeView, len(out.FileScopes))
	for sourceID, scope := range out.FileScopes {
		fileScopes[sourceID] = newSolverScopeView(scope, out.Info)
	}
	return out.Module, newSolverScopeView(out.ModuleScope, out.Info), fileScopes, out.Info
}

// checkModule parses sources as one lib/ module and checks it, logging the parse and
// type errors rather than failing on them.
func checkModule(t *testing.T, sources []*ast.Source) compiler.CheckLibOutput {
	t.Helper()
	out := compiler.CheckLib(context.Background(), sources, compiler.PackageConfig{})
	for _, err := range out.ParseErrors {
		t.Logf("parse error: %s", err.Message)
	}
	for _, err := range out.TypeErrors {
		t.Logf("type error: %s", err.Message())
	}
	require.NotNil(t, out.Info)
	return out
}

func TestModuleCrossFileDeclarationsVisible(t *testing.T) {
	sources := []*ast.Source{
		{ID: 0, Path: "lib/types.esc", Contents: `type UserId = number`},
		{ID: 1, Path: "lib/utils.esc", Contents: `
fn getUser(id: UserId) -> UserId { id }
val defaultId: UserId = 0
`},
	}
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at start of a new line in utils.esc (after the declarations)
	// Line 3, col 1 (inside the file, after defaultId declaration)
	loc := cursorAt(sources[1].Contents, 4, 1)
	fileScope := fileScopes[1]

	items := testServer().completionsFromModuleScope(module, 1, fileScope, moduleScope, info, loc)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at line 2, col 1 — between a and b declarations
	// All top-level declarations are visible anywhere in the file because
	// the DepGraph reorders them before type checking and code generation.
	loc := cursorAt(sources[0].Contents, 2, 1)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at line 1 col 1 in file1 — all declarations from file2 should be visible
	// even though the cursor is "before" them (they're in a different file)
	loc := cursorAt(sources[0].Contents, 1, 1)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at line 1 col 1 — before laterFunc declaration
	// Function declarations are always visible in modules just like all other
	// declarations
	loc := cursorAt(sources[0].Contents, 1, 1)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)
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
	module, _, _, info := parseModuleAndInfer(t, sources)

	// Cursor after "p." inside the function body — line 2, col 3
	loc := cursorAt(sources[1].Contents, 2, 4)
	node, _ := findNodeAndParentInFile(module, 1, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, nil)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at "inner" on line 5, inside foo's body (after inner and later declarations)
	loc := cursorAt(sources[0].Contents, 5, 2)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)
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
	locEarly := cursorAt(sources[0].Contents, 3, 15)
	itemsEarly := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, locEarly)
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
	// file1 imports "std:math"; file2 does not. The namespace an import binds is
	// the last segment of its specifier, so it should appear in file1's
	// completions but not file2's.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/file1.esc", Contents: `import "std:math"
declare val x: number`},
		{ID: 1, Path: "lib/file2.esc", Contents: `declare val y: number`},
	}

	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Completions from file1 (which has the import)
	loc0 := cursorAt(sources[0].Contents, 2, 1)
	loc1 := cursorAt(sources[1].Contents, 2, 1)
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc0)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}

	assert.True(t, seen1["math"], "math namespace should be visible in importing file")
	assert.True(t, seen1["x"], "x from same file should be visible")
	assert.True(t, seen1["y"], "y from other file should be visible (cross-file)")

	// Completions from file2 (which does NOT have the import)
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, info, loc1)
	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}

	assert.False(t, seen2["math"], "math namespace should NOT be visible in non-importing file")
	assert.True(t, seen2["y"], "y from same file should be visible")
	assert.True(t, seen2["x"], "x from other file should be visible (cross-file)")
}

func TestModuleImportValuesVisibleOnlyInImportingFile(t *testing.T) {
	// file1 imports "std:math"; file2 does not.
	// The namespace the import binds should appear only in file1's completions.
	sources := []*ast.Source{
		{ID: 0, Path: "lib/file1.esc", Contents: `import "std:math"
fn usePkg() -> number { 1 }`},
		{ID: 1, Path: "lib/file2.esc", Contents: `fn other() -> number { 2 }`},
	}

	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	loc0 := cursorAt(sources[0].Contents, 2, 1)
	loc1 := cursorAt(sources[1].Contents, 2, 1)

	// file1 completions: math namespace should be present
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc0)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}
	assert.True(t, seen1["math"], "math namespace should be visible in file1 (imports it)")
	assert.True(t, seen1["usePkg"], "usePkg from same file should be visible")
	assert.True(t, seen1["other"], "other from file2 should be visible (cross-file)")

	// file2 completions: math namespace should NOT be present
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, info, loc1)
	seen2 := map[string]bool{}
	for _, item := range items2 {
		seen2[item.Label] = true
	}
	assert.False(t, seen2["math"], "math namespace should NOT be visible in file2 (no import)")
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Completions from main.esc (root namespace)
	loc := cursorAt(sources[0].Contents, 1, 1)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)

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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	loc0 := cursorAt(sources[0].Contents, 1, 1)
	loc1 := cursorAt(sources[1].Contents, 1, 1)

	// Completions from add.esc
	items1 := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc0)
	seen1 := map[string]bool{}
	for _, item := range items1 {
		seen1[item.Label] = true
	}

	assert.True(t, seen1["add"], "add from same file should be visible")
	assert.True(t, seen1["sub"], "sub from other file in same namespace should be visible")

	// Completions from sub.esc
	items2 := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, info, loc1)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	loc := cursorAt(sources[0].Contents, 1, 1)
	items := testServer().completionsFromModuleScope(module, 0, fileScopes[0], moduleScope, info, loc)

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
	module, _, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor after "math." — line 2, col 7
	loc := cursorAt(sources[0].Contents, 2, 7)
	node, _ := findNodeAndParentInFile(module, 0, loc)

	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)

	items := testServer().memberCompletions(memberExpr, info, fileScopes[0])
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor at "rootDecl" on line 2 inside helperFunc in the lib/helper.esc file
	loc := cursorAt(sources[1].Contents, 2, 2)
	items := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, info, loc)
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
	module, moduleScope, fileScopes, info := parseModuleAndInfer(t, sources)

	// Cursor on the partial identifier "roo" in helper.esc.
	loc := cursorAt(sources[1].Contents, 2, 4)
	node, _ := findNodeAndParentInFile(module, 1, loc)
	require.NotNil(t, node)
	identExpr, ok := node.(*ast.IdentExpr)
	require.True(t, ok, "expected IdentExpr, got %T", node)
	require.Equal(t, "roo", identExpr.Name)

	items := testServer().completionsFromModuleScope(module, 1, fileScopes[1], moduleScope, info, loc)
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
	module, moduleScope, _, info := parseModuleAndInfer(t, sources)

	// Cursor on the partial member "s" in "math.s".
	loc := cursorAt(sources[0].Contents, 2, 8)
	node, _ := findNodeAndParentInFile(module, 0, loc)
	require.NotNil(t, node)
	memberExpr, ok := node.(*ast.MemberExpr)
	require.True(t, ok, "expected MemberExpr, got %T", node)
	require.Equal(t, "s", memberExpr.Prop.Name)

	items := testServer().memberCompletions(memberExpr, info, moduleScope)
	filtered := filterByPrefix(items, memberExpr.Prop.Name)
	labels := getCompletionLabels(filtered)

	assert.Equal(t, []string{"sub"}, labels)
}

// --- wordAtCursor tests ---

func TestWordAtCursorBasic(t *testing.T) {
	text := "val foo = bar"
	// Cursor right after "bar" (line 1, col 14).
	result := wordAtCursor(text, cursorAt(text, 1, 14))
	assert.Equal(t, "bar", result)
}

func TestWordAtCursorMiddleOfWord(t *testing.T) {
	text := "val foo = bar"
	// The cursor sits after "ba" in "bar", at 1-based column 13. That is byte
	// offset 12, and the word behind it starts at offset 10.
	result := wordAtCursor(text, cursorAt(text, 1, 13))
	assert.Equal(t, "ba", result)
}

func TestWordAtCursorAtStart(t *testing.T) {
	text := "val foo = bar"
	// Cursor at beginning of line — no word behind.
	result := wordAtCursor(text, cursorAt(text, 1, 1))
	assert.Equal(t, "", result)
}

func TestWordAtCursorOnSpace(t *testing.T) {
	text := "val foo = bar"
	// Cursor on space before "bar" (col 11).
	result := wordAtCursor(text, cursorAt(text, 1, 11))
	assert.Equal(t, "", result)
}

func TestWordAtCursorMultiLine(t *testing.T) {
	text := "val x = 1\nval y = 2"
	// Cursor after "y" on line 2 (col 6).
	result := wordAtCursor(text, cursorAt(text, 2, 6))
	assert.Equal(t, "y", result)
}

func TestWordAtCursorUnderscoreAndDigits(t *testing.T) {
	text := "val my_var2 = 1"
	// Cursor after "my_var2" (col 12).
	result := wordAtCursor(text, cursorAt(text, 1, 12))
	assert.Equal(t, "my_var2", result)
}

func TestWordAtCursorMultibyteRunes(t *testing.T) {
	// `é` takes two bytes, so a cursor after `café` sits at byte offset 9
	// while a reader counts it as column 9 of 8 characters. Both the offset
	// conversion and the walk back over runes have to agree for the whole
	// name to come back.
	text := "val café = 1"
	result := wordAtCursor(text, cursorAt(text, 1, 9))
	assert.Equal(t, "café", result)
}

func TestWordAtCursorPartWayThroughAWord(t *testing.T) {
	text := "val café = 1"
	// Cursor between `caf` and `é`.
	assert.Equal(t, "caf", wordAtCursor(text, cursorAt(text, 1, 8)))
}

func TestWordAtCursorOutOfBounds(t *testing.T) {
	text := "hello"
	// An offset outside the document yields nothing. A line or column past
	// the end of the file clamps to the last position in it, so the offsets
	// here are built directly rather than through cursorAt.
	assert.Equal(t, "", wordAtCursor(text, ast.Location{Offset: -1}))
	assert.Equal(t, "", wordAtCursor(text, ast.Location{Offset: len(text) + 1}))
}

// --- filterTypeItems tests ---

func TestFilterTypeItemsKeepsTypes(t *testing.T) {
	classKind := protocol.CompletionItemKindClass
	interfaceKind := protocol.CompletionItemKindInterface
	enumKind := protocol.CompletionItemKindEnum
	moduleKind := protocol.CompletionItemKindModule
	funcKind := protocol.CompletionItemKindFunction
	varKind := protocol.CompletionItemKindVariable

	items := []protocol.CompletionItem{
		{Label: "MyClass", Kind: &classKind},
		{Label: "MyInterface", Kind: &interfaceKind},
		{Label: "MyEnum", Kind: &enumKind},
		{Label: "MyModule", Kind: &moduleKind},
		{Label: "myFunc", Kind: &funcKind},
		{Label: "myVar", Kind: &varKind},
		{Label: "noKind"},
	}

	filtered := filterTypeItems(items)
	labels := getCompletionLabels(filtered)

	assert.Contains(t, labels, "MyClass")
	assert.Contains(t, labels, "MyInterface")
	assert.Contains(t, labels, "MyEnum")
	assert.Contains(t, labels, "MyModule")
	assert.NotContains(t, labels, "myFunc", "functions should be filtered out")
	assert.NotContains(t, labels, "myVar", "variables should be filtered out")
	assert.NotContains(t, labels, "noKind", "items with no Kind should be filtered out")
}

func TestFilterTypeItemsEmpty(t *testing.T) {
	filtered := filterTypeItems(nil)
	assert.Empty(t, filtered)
}

// --- Module suppression tests ---

// moduleCompletions exercises the full textDocumentCompletion handler for a
// module file, returning the completion items.
func moduleCompletions(
	t *testing.T,
	sources []*ast.Source,
	targetPath string,
	loc ast.Location,
) []protocol.CompletionItem {
	t.Helper()

	out := checkModule(t, sources)

	s := testServer()
	s.rootURI = "file:///workspace"

	// Register each source as an open document.
	for _, src := range sources {
		fileURI := protocol.DocumentUri(fmt.Sprintf("file:///workspace/%s", src.Path))
		version := protocol.Integer(1)
		s.documents[fileURI] = protocol.TextDocumentItem{
			URI:        fileURI,
			LanguageID: "escalier",
			Version:    version,
			Text:       src.Contents,
		}
		s.validatedVersion[fileURI] = version
	}

	s.checkOutput = &compiler.CheckOutput{
		Module:      out.Module,
		ModuleInfo:  out.Info,
		ModuleScope: out.ModuleScope,
		FileScopes:  out.FileScopes,
		Scripts:     map[int]*ast.Script{},
	}

	targetURI := protocol.DocumentUri(fmt.Sprintf("file:///workspace/%s", targetPath))

	var targetContents string
	for _, src := range sources {
		if src.Path == targetPath {
			targetContents = src.Contents
			break
		}
	}

	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: targetURI},
			Position:     offsetToPos(targetContents, loc),
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

func TestModuleNoCompletionsOnIdentPat(t *testing.T) {
	sources := []*ast.Source{
		{ID: stableSourceID("lib/main.esc"), Path: "lib/main.esc", Contents: "val p"},
	}
	// Cursor at "p" — line 1, col 5.
	items := moduleCompletions(t, sources, "lib/main.esc", cursorAt(sources[0].Contents, 1, 5))
	assert.Empty(t, items, "should not provide completions when cursor is on IdentPat in a module file")
}

func TestModuleNoCompletionsOnIdentPatComplete(t *testing.T) {
	sources := []*ast.Source{
		{ID: stableSourceID("lib/main.esc"), Path: "lib/main.esc", Contents: "val p = 10"},
	}
	// Cursor right after "p" (col 6) — still in the pattern.
	items := moduleCompletions(t, sources, "lib/main.esc", cursorAt(sources[0].Contents, 1, 6))
	assert.Empty(t, items, "should not provide completions on IdentPat in complete module val decl")
}

// --- Module TypeRefTypeAnn with file-scoped imports ---

func TestModuleTypeAnnotationIncludesImportedNamespace(t *testing.T) {
	// file1 imports a namespace from a package. In a type annotation position,
	// the namespace should be visible for qualified type references like
	// `math.SomeType`.
	sources := []*ast.Source{
		{ID: stableSourceID("lib/file1.esc"), Path: "lib/file1.esc", Contents: `import "std:math"
val x: ma`},
	}

	out := checkModule(t, sources)

	s := testServer()
	s.rootURI = "file:///workspace"
	uri := protocol.DocumentUri("file:///workspace/lib/file1.esc")
	version := protocol.Integer(1)
	s.documents[uri] = protocol.TextDocumentItem{
		URI:        uri,
		LanguageID: "escalier",
		Version:    version,
		Text:       sources[0].Contents,
	}
	s.validatedVersion[uri] = version
	s.checkOutput = &compiler.CheckOutput{
		Module:      out.Module,
		ModuleInfo:  out.Info,
		ModuleScope: out.ModuleScope,
		FileScopes:  out.FileScopes,
		Scripts:     map[int]*ast.Script{},
	}

	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      1, // 0-based: line 2
				Character: 9, // 0-based: after "ma" in "val x: ma"
			},
		},
	}
	result, err := s.textDocumentCompletion(&glsp.Context{}, params)
	require.NoError(t, err)
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok, "expected *CompletionList, got %T", result)

	labels := getCompletionLabels(list.Items)
	assert.Contains(t, labels, "math", "imported namespace should appear in type annotation completions")
}

func TestModuleTypeAnnotationExcludesValues(t *testing.T) {
	// file1 imports a namespace that contains a value binding. The namespace
	// itself should be visible (Module kind) but value-only bindings from it
	// should not appear as bare completions in a type annotation position.
	sources := []*ast.Source{
		{ID: stableSourceID("lib/file1.esc"), Path: "lib/file1.esc", Contents: `import "std:math"
fn usePkg() -> number { math.abs(1) }
val x: a`},
	}

	out := checkModule(t, sources)

	s := testServer()
	s.rootURI = "file:///workspace"
	uri := protocol.DocumentUri("file:///workspace/lib/file1.esc")
	version := protocol.Integer(1)
	s.documents[uri] = protocol.TextDocumentItem{
		URI:        uri,
		LanguageID: "escalier",
		Version:    version,
		Text:       sources[0].Contents,
	}
	s.validatedVersion[uri] = version
	s.checkOutput = &compiler.CheckOutput{
		Module:      out.Module,
		ModuleInfo:  out.Info,
		ModuleScope: out.ModuleScope,
		FileScopes:  out.FileScopes,
		Scripts:     map[int]*ast.Script{},
	}

	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      2, // 0-based: line 3
				Character: 8, // 0-based: after "a"
			},
		},
	}
	result, err := s.textDocumentCompletion(&glsp.Context{}, params)
	require.NoError(t, err)
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok, "expected *CompletionList, got %T", result)

	labels := getCompletionLabels(list.Items)
	// "abs" is a value (function kind), not a type — should be excluded.
	assert.NotContains(t, labels, "abs", "imported value should not appear in type annotation completions")
	// "usePkg" is also a value (function) — should be excluded.
	assert.NotContains(t, labels, "usePkg", "function declarations should not appear in type annotation completions")
}

// --- Member completions from internal/solver ---

// solverMemberCompletions checks files on internal/solver, opens open, and returns the
// completions at line and col, both 1-based, rendered as "label: detail".
func solverMemberCompletions(t *testing.T, files map[string]string, open string, line, col int) []string {
	t.Helper()
	s := integrationServer(t, files)
	uri := openFile(t, s, open, files[open])
	result, err := s.textDocumentCompletion(&glsp.Context{}, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      protocol.UInteger(line - 1),
				Character: protocol.UInteger(col - 1),
			},
		},
	})
	require.NoError(t, err)
	if result == nil {
		return nil
	}
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok)
	rendered := make([]string, len(list.Items))
	for i, item := range list.Items {
		detail := ""
		if item.Detail != nil {
			detail = *item.Detail
		}
		rendered[i] = item.Label + ": " + detail
	}
	return rendered
}

func TestMemberCompletionFromSolver(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// line and col are the 1-based cursor position in src.
		line, col int
		expected  []string
	}{
		{
			name:     "AnnotatedObject",
			src:      "val obj: {a: number, b: string} = {a: 1, b: \"hello\"}\nobj.",
			line:     2,
			col:      5,
			expected: []string{"a: number", "b: string"},
		},
		{
			name:     "FilteredByPrefix",
			src:      "val obj: {alpha: number, beta: string, able: boolean} = {alpha: 1, beta: \"hi\", able: true}\nobj.al",
			line:     2,
			col:      7,
			expected: []string{"alpha: number"},
		},
		{
			name:     "UnionOffersEveryVariantsMembers",
			src:      "val obj: {a: number, b: string} | {a: number, c: boolean} = {a: 1, b: \"hi\"}\nobj.",
			line:     2,
			col:      5,
			expected: []string{"a: number", "b: string | undefined", "c: boolean | undefined"},
		},
		{
			name:     "IntersectionOffersCommonMembers",
			src:      "declare val obj: {a: number, b: string} & {a: number, c: boolean}\nobj.",
			line:     2,
			col:      5,
			expected: []string{"a: number"},
		},
		{
			name:     "OptionalChainSkipsNullish",
			src:      "declare val obj: {a: number} | undefined\nobj?.",
			line:     2,
			col:      6,
			expected: []string{"a: number"},
		},
		{
			name:     "GetterWithoutSetterOnImmutableReceiver",
			src:      "declare val obj: {\n    get bar(self) -> boolean,\n    set bar(mut self, value: boolean) -> undefined\n}\nobj.",
			line:     5,
			col:      5,
			expected: []string{"bar: boolean"},
		},
		{
			name: "ClassInstanceHidesMutSelfOnImmutableBinding",
			src: `class Counter {
	n: number,
	constructor(&mut self) { self.n = 0 },
	get(&self) -> number { return self.n },
	bump(&mut self) -> number { self.n = self.n + 1
		return self.n },
}
val c = Counter()
c.`,
			line:     9,
			col:      3,
			expected: []string{"get: fn () -> number", "n: number"},
		},
		{
			name: "ClassInstanceShowsMutSelfOnMutBinding",
			src: `class Counter {
	n: number,
	constructor(&mut self) { self.n = 0 },
	get(&self) -> number { return self.n },
	bump(&mut self) -> number { self.n = self.n + 1
		return self.n },
}
val mut c = Counter()
c.`,
			line:     9,
			col:      3,
			expected: []string{"bump: fn () -> number", "get: fn () -> number", "n: number"},
		},
		{
			name:     "UnionBehindAlias",
			src:      "type Shape = {a: number, b: string} | {a: number, c: boolean}\ndeclare val s: Shape\ns.",
			line:     3,
			col:      3,
			expected: []string{"a: number", "b: string | undefined", "c: boolean | undefined"},
		},
		{
			name: "MutSelfGetterOnImmutableBinding",
			src: `class Lazy {
	m: string,
	constructor(&mut self) { self.m = "" },
	get n(&mut self) -> number { return 1 },
}
val l = Lazy()
l.`,
			line:     7,
			col:      3,
			expected: []string{"m: string"},
		},
		{
			name:     "GetterKeepsTypeInUnionOnMutReceiver",
			src:      "declare val obj: mut ({get bar(self) -> boolean, set bar(mut self, value: boolean) -> undefined} | {bar: boolean})\nobj.",
			line:     2,
			col:      5,
			expected: []string{"bar: boolean"},
		},
		{
			name:     "ErrorTypeOffersNothing",
			src:      "val obj: {a: number} = {a: 1}\nval x = obj.\nx.",
			line:     3,
			col:      3,
			expected: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"bin/main.esc": tt.src}
			require.Equal(t, tt.expected, solverMemberCompletions(t, files, "bin/main.esc", tt.line, tt.col))
		})
	}
}

// TestMemberCompletionFromSolverMembership covers receivers whose member lists are too
// long to assert whole, such as the standard library's classes, by naming members the
// list must and must not hold.
func TestMemberCompletionFromSolverMembership(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the cursor is in. line and col are 1-based.
		open      string
		line, col int
		contains  []string
		excludes  []string
	}{
		{
			name:     "StringLiteral",
			files:    map[string]string{"bin/main.esc": "val s = \"abc\"\ns.toUp"},
			open:     "bin/main.esc",
			line:     2,
			col:      7,
			contains: []string{"toUpperCase: fn () -> string"},
		},
		{
			name:     "ImmutableMapHidesMutSelf",
			files:    map[string]string{"bin/main.esc": "declare val m: Map<string, number>\nm."},
			open:     "bin/main.esc",
			line:     2,
			col:      3,
			contains: []string{"has: fn <B>(key: B) -> boolean where string: B"},
			excludes: []string{"clear: fn () -> undefined"},
		},
		{
			name:     "MutMapShowsMutSelf",
			files:    map[string]string{"bin/main.esc": "declare val m: mut Map<string, number>\nm."},
			open:     "bin/main.esc",
			line:     2,
			col:      3,
			contains: []string{"has: fn <B>(key: B) -> boolean where string: B", "clear: fn () -> undefined"},
		},
		{
			name:     "AmbientNamespace",
			files:    map[string]string{"bin/main.esc": "val n = Math.flo"},
			open:     "bin/main.esc",
			line:     1,
			col:      17,
			contains: []string{"floor: fn (x: number) -> number"},
		},
		{
			name: "LibModuleFile",
			files: map[string]string{
				"lib/point.esc": "type Point = {x: number, y: number}\ndeclare val p: Point\nval q = p.",
			},
			open:     "lib/point.esc",
			line:     3,
			col:      11,
			contains: []string{"x: number", "y: number"},
		},
		{
			name: "BinReadsLibClass",
			files: map[string]string{
				"lib/box.esc":  "export class Box {\n\tvalue: number,\n\tconstructor(&mut self) { self.value = 0 },\n}",
				"bin/main.esc": "val b = Box()\nb.",
			},
			open:     "bin/main.esc",
			line:     2,
			col:      3,
			contains: []string{"value: number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := solverMemberCompletions(t, tt.files, tt.open, tt.line, tt.col)
			for _, item := range tt.contains {
				require.Contains(t, items, item)
			}
			for _, item := range tt.excludes {
				require.NotContains(t, items, item)
			}
		})
	}
}

// --- Scope completions from internal/solver ---

// solverCompletionItems checks files on internal/solver, opens open, and returns the
// completion items at line and col, both 1-based, with each item's detail resolved.
func solverCompletionItems(t *testing.T, files map[string]string, open string, line, col int) map[string]string {
	t.Helper()
	s := integrationServer(t, files)
	uri := openFile(t, s, open, files[open])
	result, err := s.textDocumentCompletion(&glsp.Context{}, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      protocol.UInteger(line - 1),
				Character: protocol.UInteger(col - 1),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok)
	details := make(map[string]string, len(list.Items))
	for _, item := range list.Items {
		resolved, err := s.completionItemResolve(&glsp.Context{}, &item)
		require.NoError(t, err)
		detail := ""
		if resolved.Detail != nil {
			detail = *resolved.Detail
		}
		details[item.Label] = detail
	}
	return details
}

func TestScopeCompletionFromSolver(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the cursor is in. line and col are 1-based.
		open      string
		line, col int
		// expected maps each label the list must hold to its resolved detail.
		expected map[string]string
		// excludes are labels the list must not hold.
		excludes []string
	}{
		{
			name:     "ScriptLocalsBeforeCursor",
			files:    map[string]string{"bin/main.esc": "val x = 5\nval y = x\nval z = 1"},
			open:     "bin/main.esc",
			line:     2,
			col:      10,
			expected: map[string]string{"x": "5"},
			excludes: []string{"z"},
		},
		{
			name:     "AmbientBuiltinsInScript",
			files:    map[string]string{"bin/main.esc": "val n = pars"},
			open:     "bin/main.esc",
			line:     1,
			col:      13,
			expected: map[string]string{"parseInt": "fn (string: string, radix?: number) -> number"},
		},
		{
			name:     "OperatorBindingsLeftOut",
			files:    map[string]string{"bin/main.esc": "val n = un"},
			open:     "bin/main.esc",
			line:     1,
			col:      11,
			expected: map[string]string{},
			excludes: []string{"unary +", "unary -"},
		},
		{
			name:     "AmbientNamespaceInScript",
			files:    map[string]string{"bin/main.esc": "val n = Mat"},
			open:     "bin/main.esc",
			line:     1,
			col:      12,
			expected: map[string]string{"Math": "namespace"},
		},
		{
			name: "BinSeesLibExport",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { return a + b }",
				"bin/main.esc": "val x = ad",
			},
			open:     "bin/main.esc",
			line:     1,
			col:      11,
			expected: map[string]string{"add": "fn (a: number, b: number) -> number"},
		},
		{
			name:     "ScriptTypeAnnotation",
			files:    map[string]string{"bin/main.esc": "type Point = {x: number}\nval p: Po"},
			open:     "bin/main.esc",
			line:     2,
			col:      10,
			expected: map[string]string{"Point": "{x: number}"},
		},
		{
			name: "ModuleDeclsAcrossFiles",
			files: map[string]string{
				"lib/a.esc": "export fn add(a: number, b: number) -> number { return a + b }",
				"lib/b.esc": "export val total = ad",
			},
			open:     "lib/b.esc",
			line:     1,
			col:      22,
			expected: map[string]string{"add": "fn (a: number, b: number) -> number"},
		},
		{
			name: "NamespacedLibFile",
			files: map[string]string{
				"lib/geo/point.esc": "export fn origin() -> number { return 0 }\nexport val o = ori",
			},
			open:     "lib/geo/point.esc",
			line:     2,
			col:      19,
			expected: map[string]string{"origin": "fn () -> number"},
		},
		{
			name:     "LibFileImport",
			files:    map[string]string{"lib/a.esc": "import \"std:math\"\nexport val n = mat"},
			open:     "lib/a.esc",
			line:     2,
			col:      18,
			expected: map[string]string{"math": ""},
		},
		{
			name:     "AmbientBuiltinsInModule",
			files:    map[string]string{"lib/a.esc": "export val n = pars"},
			open:     "lib/a.esc",
			line:     1,
			col:      20,
			expected: map[string]string{"parseInt": "fn (string: string, radix?: number) -> number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := solverCompletionItems(t, tt.files, tt.open, tt.line, tt.col)
			for label, detail := range tt.expected {
				require.Contains(t, details, label)
				require.Equal(t, detail, details[label], "detail of %s", label)
			}
			for _, label := range tt.excludes {
				require.NotContains(t, details, label)
			}
		})
	}
}

// TestScopeCompletionFromSolverAfterIncrementalCheck asserts that a bin/ file re-checked
// alone against the cached library completes from the scope that re-check produced.
func TestScopeCompletionFromSolverAfterIncrementalCheck(t *testing.T) {
	files := map[string]string{
		"lib/math.esc": "export fn add(a: number, b: number) -> number { return a + b }",
		"bin/main.esc": "val x = add(1, 2)",
	}
	s := integrationServer(t, files)
	openFile(t, s, "lib/math.esc", files["lib/math.esc"])
	binURI := openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	newText := "val x = add(1, 2)\nval later = 1\nval y = la"
	require.NoError(t, s.textDocumentDidChange(&glsp.Context{}, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: binURI},
			Version:                2,
		},
		ContentChanges: []any{
			protocol.TextDocumentContentChangeEventWhole{Text: newText},
		},
	}))
	// sync.NewCond(s.mu.RLocker()) built s.validated, so the wait runs under RLock.
	s.mu.RLock()
	for s.validatedVersion[binURI] < 2 {
		s.validated.Wait()
	}
	s.mu.RUnlock()

	require.Equal(t, []string{"later"}, getCompletionLabelsAt(t, s, binURI, 3, 11))
}

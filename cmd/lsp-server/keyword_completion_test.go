package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// cursorMark stands for the cursor in a test source. `|` is also Escalier's union
// operator, so a source written with a union type can't use this mark.
const cursorMark = "|"

// keywordTestPaths are the files each keyword case runs in, so a case covers both the
// script and the module completion handlers.
var keywordTestPaths = []string{"bin/main.esc", "lib/index.esc"}

// completionItemsAtMark opens src at path with the cursor where cursorMark is written,
// and returns the completion items there. snippets sets whether the client accepts
// snippet syntax.
func completionItemsAtMark(t *testing.T, path, src string, snippets bool) []protocol.CompletionItem {
	t.Helper()
	require.Equal(t, 1, strings.Count(src, cursorMark), "source must hold exactly one cursor mark")
	offset := strings.Index(src, cursorMark)
	text := src[:offset] + src[offset+len(cursorMark):]

	s := integrationServer(t, map[string]string{path: text})
	s.snippetSupport = snippets
	uri := openFile(t, s, path, text)
	lines := strings.Split(text[:offset], "\n")
	result, err := s.textDocumentCompletion(&glsp.Context{}, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line: protocol.UInteger(len(lines) - 1),
				// The sources are ASCII, so a byte count is the UTF-16 column.
				Character: protocol.UInteger(len(lines[len(lines)-1])),
			},
		},
	})
	require.NoError(t, err)
	if result == nil {
		return nil
	}
	list, ok := result.(*protocol.CompletionList)
	require.True(t, ok, "expected *CompletionList, got %T", result)
	return list.Items
}

// TestThrowsCompletionOffered covers each signature form whose return type the parser
// lets a `throws` clause follow. At each position the list holds `throws` alone.
func TestThrowsCompletionOffered(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "FuncDeclNothingTyped", src: "fn f(x: number) -> number | {\n    return x\n}\n"},
		{name: "FuncDeclPartial", src: "fn f() -> number th| {\n    return 1\n}\n"},
		{name: "FuncDeclPartialNoBody", src: "fn f() -> number th|\n"},
		{name: "FuncDeclKeywordTyped", src: "fn f() -> number throws| {\n    return 1\n}\n"},
		{name: "FuncDeclGenericReturn", src: "fn f() -> Array<number> th| {\n    return []\n}\n"},
		{name: "FuncExpr", src: "val g = fn () -> number th| {\n    return 1\n}\n"},
		{name: "DeclareFn", src: "declare fn f() -> number th|\nval y = 1\n"},
		{name: "Method", src: "class C {\n    m(self) -> number th| {\n        return 1\n    }\n}\n"},
		{name: "StaticMethod", src: "class C {\n    static m() -> number th| {\n        return 1\n    }\n}\n"},
		{name: "Getter", src: "class C {\n    get x(self) -> number th| {\n        return 1\n    }\n}\n"},
		{name: "FuncTypeAnn", src: "type F = fn () -> number th|\nval y = 1\n"},
		{name: "ParamFuncTypeAnn", src: "fn f(cb: fn () -> number th|) {}\n"},
		{name: "ObjectTypeMethod", src: "type O = {m(self) -> number th|}\n"},
		{name: "ObjectTypeGetter", src: "type O = {get x(self) -> number th|}\n"},
		{name: "InterfaceMethod", src: "interface I {\n    m(self) -> number th|\n}\n"},
	}
	for _, tt := range tests {
		for _, path := range keywordTestPaths {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				items := completionItemsAtMark(t, path, tt.src, false)
				require.Equal(t, []string{"throws"}, getCompletionLabels(items))
			})
		}
	}
}

// TestThrowsCompletionNotOffered covers positions near a signature where a `throws`
// clause can't be written.
func TestThrowsCompletionNotOffered(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "InBody", src: "fn f() -> number {\n    th|\n}\n"},
		{name: "BeforeArrow", src: "fn f() | -> number {\n    return 1\n}\n"},
		{name: "InParams", src: "fn f(x: num|) -> number {\n    return 1\n}\n"},
		{name: "OnReturnType", src: "fn f() -> number| {\n    return 1\n}\n"},
		{name: "AfterThrowsClause", src: "fn f() -> number throws string | {\n    return 1\n}\n"},
		{name: "NextLineAfterDeclareFn", src: "declare fn f() -> number\nth|\n"},
		{name: "Expression", src: "val x = 1\nval y = th|\n"},
		{name: "MemberAccess", src: "val p = {thing: 1}\nval q = p.th|\n"},
		{name: "String", src: "val s = \"fn f() -> number th|\"\n"},
		{name: "Comment", src: "// fn f() -> number th|\nval y = 1\n"},
		{name: "InsideWord", src: "fn f() -> number th|rows string {\n    return 1\n}\n"},
	}
	for _, tt := range tests {
		for _, path := range keywordTestPaths {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				items := completionItemsAtMark(t, path, tt.src, false)
				require.NotContains(t, getCompletionLabels(items), "throws")
			})
		}
	}
}

// TestThrowsCompletionInsertText covers what accepting `throws` inserts. It inserts
// `throws _` with the `_` as a snippet placeholder when the client accepts snippets, and
// as plain text when it doesn't. A name already after the cursor is the clause's type,
// so the item inserts its label alone there.
func TestThrowsCompletionInsertText(t *testing.T) {
	const bodyFollows = "fn f() -> number th| {\n    return 1\n}\n"
	tests := []struct {
		name     string
		src      string
		snippets bool
		// text and format are the item's InsertText and InsertTextFormat, and are
		// empty when the item has neither.
		text   string
		format protocol.InsertTextFormat
	}{
		{name: "Snippet", src: bodyFollows, snippets: true, text: "throws ${1:_}", format: protocol.InsertTextFormatSnippet},
		{name: "PlainText", src: bodyFollows, snippets: false, text: "throws _", format: protocol.InsertTextFormatPlainText},
		{name: "TypeFollows", src: "fn f() -> number thr| string {\n    return 1\n}\n", snippets: true},
	}
	for _, tt := range tests {
		for _, path := range keywordTestPaths {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				items := completionItemsAtMark(t, path, tt.src, tt.snippets)
				require.Len(t, items, 1)
				item := items[0]
				require.Equal(t, "throws", item.Label)
				require.NotNil(t, item.Kind)
				require.Equal(t, protocol.CompletionItemKindKeyword, *item.Kind)
				if tt.text == "" {
					require.Nil(t, item.InsertText)
					require.Nil(t, item.InsertTextFormat)
					return
				}
				require.NotNil(t, item.InsertText)
				require.Equal(t, tt.text, *item.InsertText)
				require.NotNil(t, item.InsertTextFormat)
				require.Equal(t, tt.format, *item.InsertTextFormat)
			})
		}
	}
}

// TestClientSupportsSnippets covers how the snippet flag is read from the capabilities
// a client sends in `initialize`.
func TestClientSupportsSnippets(t *testing.T) {
	tests := []struct {
		name     string
		caps     string
		expected bool
	}{
		{name: "NoTextDocument", caps: `{}`, expected: false},
		{name: "NoCompletion", caps: `{"textDocument": {}}`, expected: false},
		{name: "Unset", caps: `{"textDocument": {"completion": {"completionItem": {}}}}`, expected: false},
		{name: "False", caps: `{"textDocument": {"completion": {"completionItem": {"snippetSupport": false}}}}`, expected: false},
		{name: "True", caps: `{"textDocument": {"completion": {"completionItem": {"snippetSupport": true}}}}`, expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var caps protocol.ClientCapabilities
			require.NoError(t, json.Unmarshal([]byte(tt.caps), &caps))
			require.Equal(t, tt.expected, clientSupportsSnippets(caps))
		})
	}
}

// TestPositionalKeywordCompletion covers which keywords the list holds at each kind of
// position.
func TestPositionalKeywordCompletion(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// paths are the files the case runs in, and default to keywordTestPaths.
		paths    []string
		includes []string
		excludes []string
	}{
		{name: "StatementInBody", src: "fn f() {\n    va|\n}\n", includes: []string{"val", "var"}},
		{name: "BlankLineInBody", src: "fn f() {\n    val a = 1\n    |\n}\n", includes: []string{"if", "return", "val"}},
		{name: "ReturnInBody", src: "fn f() {\n    re|\n}\n", includes: []string{"return"}},
		{name: "StatementAfterStatement", src: "fn f() {\n    val a = 1\n    i|\n}\n", includes: []string{"if", "interface"}, excludes: []string{"import"}},
		{name: "StatementInIfBlock", src: "fn f(c: boolean) {\n    if c {\n        fo|\n    }\n}\n", includes: []string{"for"}},
		{name: "StatementInMethod", src: "class C {\n    m(self) -> number {\n        re|\n    }\n}\n", includes: []string{"return"}},
		{name: "TopLevelScript", src: "val a = 1\ni|\n", paths: []string{"bin/main.esc"}, includes: []string{"if", "import", "interface"}, excludes: []string{"export"}},
		{name: "TopLevelDeclareScript", src: "de|\n", paths: []string{"bin/main.esc"}, includes: []string{"declare"}},
		{name: "NoReturnAtTopLevel", src: "val a = 1\nre|\n", paths: []string{"bin/main.esc"}, excludes: []string{"return"}},
		{name: "TopLevelLib", src: "val a = 1\ni|\n", paths: []string{"lib/index.esc"}, includes: []string{"import", "interface"}, excludes: []string{"if"}},
		{name: "ExportInLib", src: "ex|\n", paths: []string{"lib/index.esc"}, includes: []string{"export"}},
		{name: "NoExportInScript", src: "ex|\n", paths: []string{"bin/main.esc"}, excludes: []string{"export"}},
		{name: "Expression", src: "fn f() {\n    val x = tr|\n}\n", includes: []string{"true", "try"}},
		{name: "NoStatementInExpression", src: "fn f() {\n    val x = va|\n}\n", excludes: []string{"val", "var"}},
		{name: "NoReturnInExpression", src: "fn f() {\n    val x = re|\n}\n", excludes: []string{"return"}},
		{name: "ExpressionOnContinuationLine", src: "fn g(x: boolean) {}\nfn f() {\n    g(\n        va|\n    )\n}\n", excludes: []string{"val", "var"}},
		{name: "AwaitInAsync", src: "async fn f() {\n    val x = aw|\n}\n", includes: []string{"await"}},
		{name: "NoAwaitOutsideAsync", src: "fn f() {\n    val x = aw|\n}\n", excludes: []string{"await"}},
		{name: "NoAwaitInNestedFunc", src: "async fn f() {\n    val g = fn () {\n        val x = aw|\n    }\n}\n", excludes: []string{"await"}},
		{name: "YieldInGen", src: "gen fn f() {\n    yi|\n}\n", includes: []string{"yield"}},
		{name: "NoYieldOutsideGen", src: "fn f() {\n    yi|\n}\n", excludes: []string{"yield"}},
		{name: "ElseAfterIf", src: "fn f(c: boolean) {\n    if c {\n    } el|\n}\n", includes: []string{"else"}},
		{name: "ElseOnNextLine", src: "fn f(c: boolean) {\n    if c {\n    }\n    el|\n}\n", includes: []string{"else"}},
		{name: "NoElseAfterElse", src: "fn f(c: boolean) {\n    if c {\n    } else {\n    } el|\n}\n", excludes: []string{"else"}},
		{name: "NoElseWithoutIf", src: "fn f() {\n    el|\n}\n", excludes: []string{"else"}},
		{name: "CatchAfterTry", src: "fn f() {\n    try {\n    } ca|\n}\n", includes: []string{"catch"}},
		{name: "FieldInitializerOnOwnLine", src: "val foo = 1\nclass C {\n    static x: number =\n        fo|\n}\n", includes: []string{"foo"}, excludes: []string{"static"}},
		{name: "TypeKeyword", src: "val p: num| = 1\n", includes: []string{"number"}},
		{name: "TypeKeywordInAlias", src: "type T = ke|\n", includes: []string{"keyof"}},
		{name: "NoTypeKeywordInExpression", src: "fn f() {\n    val x = num|\n}\n", excludes: []string{"number"}},
		{name: "NoKeywordAfterDot", src: "val p = {value: 1}\nfn f() {\n    val q = p.va|\n}\n", excludes: []string{"val", "var"}},
		{name: "NoKeywordInComment", src: "fn f() {\n    // va|\n}\n", excludes: []string{"val", "var"}},
		{name: "NoKeywordInTopLevelComment", src: "// va|\nval a = 1\n", excludes: []string{"val", "var"}},
		{name: "NoKeywordInString", src: "val s = \"va|\"\n", excludes: []string{"val", "var"}},
	}
	for _, tt := range tests {
		paths := tt.paths
		if paths == nil {
			paths = keywordTestPaths
		}
		for _, path := range paths {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				labels := getCompletionLabels(completionItemsAtMark(t, path, tt.src, false))
				for _, label := range tt.includes {
					require.Contains(t, labels, label)
				}
				for _, label := range tt.excludes {
					require.NotContains(t, labels, label)
				}
			})
		}
	}
}

// TestClassBodyKeywordCompletion asserts that a class body lists the member modifiers
// alone, since a member's name is new and no name in scope fits there.
func TestClassBodyKeywordCompletion(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{name: "BlankLine", src: "class C {\n    x: number,\n    |\n}\n", expected: []string{"async", "constructor", "gen", "get", "private", "readonly", "set", "static"}},
		{name: "Partial", src: "class C {\n    st|\n}\n", expected: []string{"static"}},
		{name: "PartialAfterObjectTypeInHeader", src: "class B<T> {}\nclass C extends B<{a: number}> {\n    st|\n}\n", expected: []string{"static"}},
		{name: "PartialGetOrGen", src: "class C {\n    ge|\n}\n", expected: []string{"gen", "get"}},
	}
	for _, tt := range tests {
		for _, path := range keywordTestPaths {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				items := completionItemsAtMark(t, path, tt.src, false)
				require.Equal(t, tt.expected, getCompletionLabels(items))
			})
		}
	}
}

// TestClassHeaderKeywordCompletion asserts that a line inside a multi-line class header
// gets no member modifiers, even when an object type in the header holds a `{`.
func TestClassHeaderKeywordCompletion(t *testing.T) {
	src := "class C<\n    T: {a: number},\n    |\n> {\n}\n"
	for _, path := range keywordTestPaths {
		t.Run(path, func(t *testing.T) {
			labels := getCompletionLabels(completionItemsAtMark(t, path, src, false))
			require.NotContains(t, labels, "static")
		})
	}
}

// TestSortAndLimitKeepsKeywords asserts that cutting a list to maxCompletionItems
// takes only from the items that aren't keywords.
func TestSortAndLimitKeepsKeywords(t *testing.T) {
	variable := protocol.CompletionItemKindVariable
	var items []protocol.CompletionItem
	for i := range 150 {
		items = append(items, protocol.CompletionItem{Label: fmt.Sprintf("item%03d", i), Kind: &variable})
	}
	items = append(items, keywordCompletion("val", "", "", false))

	labels := getCompletionLabels(sortAndLimit(items))
	require.Len(t, labels, maxCompletionItems)
	require.Contains(t, labels, "val")
	require.Contains(t, labels, "item098")
	require.NotContains(t, labels, "item099")
}

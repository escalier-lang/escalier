package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// cursorMark stands for the cursor in a test source. `|` is a union in Escalier, so a
// character the language doesn't use marks the spot.
const cursorMark = "‸"

// keywordTestPaths are the files each keyword case runs in, so a case covers both the
// script and the module completion handlers.
var keywordTestPaths = []string{"bin/main.esc", "lib/index.esc"}

// completionItemsAtMark opens src at path with the cursor where cursorMark is written,
// and returns the completion items there. snippets sets whether the client accepts
// snippet syntax.
func completionItemsAtMark(t *testing.T, path, src string, snippets bool) []protocol.CompletionItem {
	t.Helper()
	offset := strings.Index(src, cursorMark)
	require.GreaterOrEqual(t, offset, 0, "source has no cursor mark")
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
				// The sources are ASCII apart from the mark, so a byte count is the
				// UTF-16 column.
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
		{name: "FuncDeclNothingTyped", src: "fn f(x: number) -> number ‸ {\n    return x\n}\n"},
		{name: "FuncDeclPartial", src: "fn f() -> number th‸ {\n    return 1\n}\n"},
		{name: "FuncDeclPartialNoBody", src: "fn f() -> number th‸\n"},
		{name: "FuncDeclKeywordTyped", src: "fn f() -> number throws‸ {\n    return 1\n}\n"},
		{name: "FuncDeclGenericReturn", src: "fn f() -> Array<number> th‸ {\n    return []\n}\n"},
		{name: "FuncExpr", src: "val g = fn () -> number th‸ {\n    return 1\n}\n"},
		{name: "DeclareFn", src: "declare fn f() -> number th‸\nval y = 1\n"},
		{name: "Method", src: "class C {\n    m(self) -> number th‸ {\n        return 1\n    }\n}\n"},
		{name: "StaticMethod", src: "class C {\n    static m() -> number th‸ {\n        return 1\n    }\n}\n"},
		{name: "Getter", src: "class C {\n    get x(self) -> number th‸ {\n        return 1\n    }\n}\n"},
		{name: "FuncTypeAnn", src: "type F = fn () -> number th‸\nval y = 1\n"},
		{name: "ParamFuncTypeAnn", src: "fn f(cb: fn () -> number th‸) {}\n"},
		{name: "ObjectTypeMethod", src: "type O = {m(self) -> number th‸}\n"},
		{name: "ObjectTypeGetter", src: "type O = {get x(self) -> number th‸}\n"},
		{name: "InterfaceMethod", src: "interface I {\n    m(self) -> number th‸\n}\n"},
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
		{name: "InBody", src: "fn f() -> number {\n    th‸\n}\n"},
		{name: "BeforeArrow", src: "fn f() ‸ -> number {\n    return 1\n}\n"},
		{name: "InParams", src: "fn f(x: num‸) -> number {\n    return 1\n}\n"},
		{name: "OnReturnType", src: "fn f() -> number‸ {\n    return 1\n}\n"},
		{name: "AfterThrowsClause", src: "fn f() -> number throws string ‸ {\n    return 1\n}\n"},
		{name: "NextLineAfterDeclareFn", src: "declare fn f() -> number\nth‸\n"},
		{name: "Expression", src: "val x = 1\nval y = th‸\n"},
		{name: "MemberAccess", src: "val p = {thing: 1}\nval q = p.th‸\n"},
		{name: "String", src: "val s = \"fn f() -> number th‸\"\n"},
		{name: "Comment", src: "// fn f() -> number th‸\nval y = 1\n"},
		{name: "InsideWord", src: "fn f() -> number th‸rows string {\n    return 1\n}\n"},
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
	const bodyFollows = "fn f() -> number th‸ {\n    return 1\n}\n"
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
		{name: "TypeFollows", src: "fn f() -> number thr‸ string {\n    return 1\n}\n", snippets: true},
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

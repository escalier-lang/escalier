package main

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// renderedSignature is one signature of a response, with each parameter label rendered
// as the text its range covers.
type renderedSignature struct {
	label  string
	params []string
	active protocol.UInteger
}

// signatureHelpAt opens the file open from files and requests signature help where its
// text holds `|`, which is removed before the file is written.
func signatureHelpAt(t *testing.T, files map[string]string, open string) *protocol.SignatureHelp {
	t.Helper()
	marked := files[open]
	cursor := strings.Index(marked, "|")
	require.GreaterOrEqual(t, cursor, 0, "the opened file marks the cursor with |")
	text := marked[:cursor] + marked[cursor+1:]
	written := map[string]string{}
	for path, contents := range files {
		written[path] = contents
	}
	written[open] = text

	s := integrationServer(t, written)
	uri := openFile(t, s, open, text)
	before := text[:cursor]
	line := strings.Count(before, "\n")
	col := len(utf16.Encode([]rune(before[strings.LastIndex(before, "\n")+1:])))
	help, err := s.textDocumentSignatureHelp(&glsp.Context{}, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(col)},
		},
	})
	require.NoError(t, err)
	return help
}

// renderSignatures returns the signatures of help with each parameter label rendered as
// the text its UTF-16 range covers in the signature label.
func renderSignatures(t *testing.T, help *protocol.SignatureHelp) []renderedSignature {
	t.Helper()
	sigs := make([]renderedSignature, len(help.Signatures))
	for i, sig := range help.Signatures {
		label := utf16.Encode([]rune(sig.Label))
		var params []string
		for _, p := range sig.Parameters {
			r, ok := p.Label.([]protocol.UInteger)
			require.True(t, ok, "a parameter label is a range")
			require.Len(t, r, 2)
			params = append(params, string(utf16.Decode(label[r[0]:r[1]])))
		}
		require.NotNil(t, sig.ActiveParameter)
		sigs[i] = renderedSignature{label: sig.Label, params: params, active: *sig.ActiveParameter}
	}
	return sigs
}

const addLib = "export fn add(a: number, b: number) -> number { return a + b }"

func TestSignatureHelp(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the request names. Its text marks the cursor with `|`.
		open            string
		expected        []renderedSignature
		activeSignature protocol.UInteger
		activeParameter protocol.UInteger
	}{
		{
			name:  "FunctionCall",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(|)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 0},
			},
		},
		{
			name:  "SecondArgument",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1, |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			name:  "CursorBeforeComma",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1|, 2)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 0},
			},
		},
		{
			name:  "UnclosedCall",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1, |"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			name:  "CommaInBlockComment",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1 /* a, b */ |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 0},
			},
		},
		{
			name:  "CommaInLineComment",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1 // a, b\n    |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 0},
			},
		},
		{
			name:  "CommaAfterBlockComment",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(1 /* a, b */, |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			// A comma inside the first argument separates the inner call's arguments, not
			// the outer call's.
			name:  "OuterCallAfterNestedCall",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = add(add(1, 2), |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			name: "InnerCall",
			files: map[string]string{
				"lib/math.esc": addLib + "\nexport fn neg(n: number) -> number { return -n }",
				"bin/main.esc": "val x = add(neg(|), 2)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "neg(n: number) -> number", params: []string{"n: number"}, active: 0},
			},
		},
		{
			name: "CallInLibFile",
			files: map[string]string{
				"lib/math.esc": addLib + "\nexport fn twice(n: number) -> number { return add(n, |) }",
			},
			open: "lib/math.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			name: "GenericFunction",
			files: map[string]string{
				"lib/id.esc":   "export fn id<T>(x: T) -> T { return x }",
				"bin/main.esc": "val x = id(|5)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "id<T>(x: T) -> T", params: []string{"x: T"}, active: 0},
			},
		},
		{
			name: "Constructor",
			files: map[string]string{
				"lib/point.esc": "export class Point {\n    x: number,\n    y: number,\n" +
					"    constructor(&mut self, x: number, y: number) { self.x = x  self.y = y },\n}",
				"bin/main.esc": "val p = Point(1, |)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "Point(x: number, y: number) -> Point", params: []string{"x: number", "y: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			// `intl.Collator` declares two constructors, and a class constructs when called.
			name:  "StdlibConstructor",
			files: map[string]string{"bin/main.esc": "import \"std:intl\"\nval c = intl.Collator(|)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{
					label:  "Collator(locales?: LocalesArgument, options?: CollatorOptions) -> Collator",
					params: []string{"locales?: LocalesArgument", "options?: CollatorOptions"},
				},
				{
					label:  "Collator(locales?: string | Array<string>, options?: CollatorOptions) -> Collator",
					params: []string{"locales?: string | Array<string>", "options?: CollatorOptions"},
				},
			},
		},
		{
			name: "OverloadSet",
			files: map[string]string{
				"lib/dup.esc": "export fn dup(value: number) -> number { return 2 * value }\n" +
					"export fn dup(value: string, times: number) -> string { return value }",
				"bin/main.esc": "val d = dup(\"a\", |)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "dup(value: number) -> number", params: []string{"value: number"}, active: 1},
				{label: "dup(value: string, times: number) -> string", params: []string{"value: string", "times: number"}, active: 1},
			},
			// The first arm has no second parameter, so the second arm is active.
			activeSignature: 1,
			activeParameter: 1,
		},
		{
			// A class that declares both answers a call with either, so both are listed.
			name: "ConstructorAndCallSignature",
			files: map[string]string{
				"lib/tag.esc": "export declare class Tag {\n    constructor(&mut self, id: number),\n" +
					"    (name: string) -> Tag,\n}",
				"bin/main.esc": "val t = Tag(|)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "Tag(id: number) -> Tag", params: []string{"id: number"}, active: 0},
				{label: "Tag(name: string) -> Tag", params: []string{"name: string"}, active: 0},
			},
		},
		{
			name: "NamespaceOverloadSet",
			files: map[string]string{
				"lib/text/dup.esc": "export fn dup(value: number) -> number { return 2 * value }\n" +
					"export fn dup(value: string) -> string { return value }",
				"bin/main.esc": "val d = text.dup(\"a\"|)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "dup(value: number) -> number", params: []string{"value: number"}, active: 0},
				{label: "dup(value: string) -> string", params: []string{"value: string"}, active: 0},
			},
		},
		{
			name:  "ParenthesizedCallee",
			files: map[string]string{"lib/math.esc": addLib, "bin/main.esc": "val x = (add)(1, |)"},
			open:  "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			// The outer call has no `)`, so its span ends with the `)` of `neg(2)`.
			name: "UnclosedCallAfterNestedCall",
			files: map[string]string{
				"lib/math.esc": addLib + "\nexport fn neg(n: number) -> number { return -n }",
				"bin/main.esc": "val x = add(1, neg(2)|",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "add(a: number, b: number) -> number", params: []string{"a: number", "b: number"}, active: 1},
			},
			activeParameter: 1,
		},
		{
			name: "RestParameter",
			files: map[string]string{
				"lib/sum.esc":  "export declare fn sum(first: number, ...rest: Array<number>) -> number",
				"bin/main.esc": "val s = sum(1, 2, 3, |)",
			},
			open: "bin/main.esc",
			expected: []renderedSignature{
				{label: "sum(first: number, ...rest: Array<number>) -> number", params: []string{"first: number", "...rest: Array<number>"}, active: 1},
			},
			activeParameter: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			help := signatureHelpAt(t, tt.files, tt.open)
			require.NotNil(t, help)
			require.Equal(t, tt.expected, renderSignatures(t, help))
			require.Equal(t, tt.activeSignature, *help.ActiveSignature)
			require.Equal(t, tt.activeParameter, *help.ActiveParameter)
		})
	}
}

func TestSignatureHelpOutsideArgumentList(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "OnCallee", src: "val x = a|dd(1, 2)"},
		{name: "BeforeParen", src: "val x = add|(1, 2)"},
		{name: "AfterCloseParen", src: "val x = add(1, 2)|"},
		{name: "NoCall", src: "val x = |1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			help := signatureHelpAt(t, map[string]string{"lib/math.esc": addLib, "bin/main.esc": tt.src}, "bin/main.esc")
			require.Nil(t, help)
		})
	}
}

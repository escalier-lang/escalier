package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestSignatureAction covers the code action that writes a function's inferred signature.
// Each case requests actions at the start of cursor's first occurrence in src, and expected
// is src with the action's edits applied, or "" when the action is not offered. warning is
// the MissingSignatureError src reports, or "" when it reports none.
func TestSignatureAction(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		cursor   string
		expected string
		warning  string
	}{
		{
			name:     "UnannotatedParam",
			src:      "fn read(p) { return p.x }",
			cursor:   "read",
			expected: "fn read<T0>(p: &{x: T0}) -> &T0 { return p.x }",
			warning:  "0:3-0:7 'read' is declared at a script's top level, so parameter 'p' needs a type and 'read' needs a return type",
		},
		{
			name:     "MissingReturnType",
			src:      "fn count(xs: &Array<number>) { return xs.length }",
			cursor:   "xs:",
			expected: "fn count(xs: &Array<number>) -> number { return xs.length }",
			warning:  "0:3-0:8 'count' is declared at a script's top level, so it needs a return type; write `-> number`",
		},
		{
			name:     "ReturnsUndefined",
			src:      "fn noop(x: number) {}",
			cursor:   "noop",
			expected: "fn noop(x: number) -> undefined {}",
			warning:  "0:3-0:7 'noop' is declared at a script's top level, so it needs a return type; write `-> undefined`",
		},
		{
			name:     "NoParams",
			src:      "fn one() { return 1 }",
			cursor:   "fn",
			expected: "fn one() -> 1 { return 1 }",
			warning:  "0:3-0:6 'one' is declared at a script's top level, so it needs a return type; write `-> 1`",
		},
		{
			name:     "WrittenTypeParam",
			src:      "fn id<T>(x: T) { return x }",
			cursor:   "id",
			expected: "fn id<T>(x: T) -> T { return x }",
			warning:  "0:3-0:5 'id' is declared at a script's top level, so it needs a return type; write `-> T`",
		},
		{
			name:     "ReturnedBorrowOfOneOfTwoParams",
			src:      "type Flat = {x: number}\nfn pick(a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:   "pick",
			expected: "type Flat = {x: number}\nfn pick<'a>(a: &'a Flat, b: &Flat) -> &'a Flat { return a }",
		},
		{
			name:     "Method",
			src:      "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tget(&self) { return self.inner.x },\n}",
			cursor:   "get",
			expected: "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tget(&self) -> number { return self.inner.x },\n}",
			warning:  "3:1-3:4 'get' is a method, so it needs a return type; write `-> number`",
		},
		{
			name:     "MethodReturnsReceiverBorrow",
			src:      "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) { return &self.inner },\n}",
			cursor:   "peek",
			expected: "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) -> &Flat { return &self.inner },\n}",
			warning:  "3:1-3:5 'peek' is a method, so it needs a return type; write `-> &Flat`",
		},
		{
			name:     "MethodOfGenericClass",
			src:      "class Cell<T> {\n\tvalue: T,\n\tget(&self) { return self.value },\n}",
			cursor:   "get",
			expected: "class Cell<T> {\n\tvalue: T,\n\tget(&self) -> &T { return self.value },\n}",
			warning:  "2:1-2:4 'get' is a method, so it needs a return type; write `-> &T`",
		},
		{
			name:     "MethodTypeParamHidesClassTypeParam",
			src:      "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) { return x },\n}",
			cursor:   "pass",
			expected: "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) -> T { return x },\n}",
			warning:  "2:1-2:5 'pass' is a method, so it needs a return type; write `-> T`",
		},
		{
			name:     "BinderListEndsInComma",
			src:      "fn id<T,>(x: T) { return x }",
			cursor:   "id",
			expected: "fn id<T,>(x: T) -> T { return x }",
			warning:  "0:3-0:5 'id' is declared at a script's top level, so it needs a return type; write `-> T`",
		},
		{
			name:     "NoParamsAfterBinderList",
			src:      "fn one<T>() { return 1 }",
			cursor:   "one",
			expected: "fn one<T>() -> 1 { return 1 }",
			warning:  "0:3-0:6 'one' is declared at a script's top level, so it needs a return type; write `-> 1`",
		},
		{
			// Writing the binder list would repeat the `where` clause's bound on `T`.
			name:     "WhereClauseWithAddedBinder",
			src:      "fn f<T>(x: T, y) where T: number { return [x, y] }",
			cursor:   "f<",
			expected: "",
			warning:  "0:3-0:4 'f' is declared at a script's top level, so parameter 'y' needs a type and 'f' needs a return type",
		},
		{
			name:     "AddsLifetimeToWrittenBinders",
			src:      "type Flat = {x: number}\nfn pick<T, U>(t: T, u: U, a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:   "pick",
			expected: "type Flat = {x: number}\nfn pick<T, U, 'a>(t: T, u: U, a: &'a Flat, b: &Flat) -> &'a Flat { return a }",
		},
		{
			// Elision assigns the source's 'q, so a rewritten binder list would drop it while
			// `a: &'q Flat` still names it.
			name:     "RewriteWouldDropWrittenLifetime",
			src:      "type Flat = {x: number}\nfn pick<'q>(q: &'q Flat, a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:   "pick",
			expected: "",
		},
		{
			name:     "CommentsInParamList",
			src:      "fn f(\n\tx: number, // the input\n\ty: number /* scale */\n) { return x * y }",
			cursor:   "f(",
			expected: "fn f(\n\tx: number, // the input\n\ty: number /* scale */\n) -> number { return x * y }",
			warning:  "0:3-0:4 'f' is declared at a script's top level, so it needs a return type; write `-> number`",
		},
		{
			name:     "FullSignature",
			src:      "fn full(a: number) -> number { return a }",
			cursor:   "full",
			expected: "",
		},
		{
			name:     "CursorInBody",
			src:      "fn noop(x: number) { x }",
			cursor:   "x }",
			expected: "",
			warning:  "0:3-0:7 'noop' is declared at a script's top level, so it needs a return type; write `-> undefined`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, map[string]string{"bin/main.esc": tt.src})
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, "bin/main.esc", tt.src)
			if tt.warning == "" {
				require.Empty(t, awaitDiagnostics(t, published, uri, 1))
			} else {
				require.Equal(t, []string{tt.warning}, awaitDiagnostics(t, published, uri, 1))
			}

			offset := strings.Index(tt.src, tt.cursor)
			require.GreaterOrEqual(t, offset, 0)
			before := tt.src[:offset]
			line := strings.Count(before, "\n")
			col := len(before) - strings.LastIndex(before, "\n") - 1
			pos := protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(col)}
			result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri},
				Range:        protocol.Range{Start: pos, End: pos},
				Context:      protocol.CodeActionContext{Only: []protocol.CodeActionKind{protocol.CodeActionKindRefactor}},
			})
			require.NoError(t, err)
			actions, ok := result.([]protocol.CodeAction)
			require.True(t, ok)
			if tt.expected == "" {
				require.Empty(t, actions)
				return
			}
			require.Len(t, actions, 1)
			action := actions[0]
			require.Equal(t, "Write the inferred signature", action.Title)
			require.Equal(t, addr(protocol.CodeActionKindRefactorRewrite), action.Kind)
			edited := applyEdits(tt.src, action.Edit.Changes[uri])
			require.Equal(t, tt.expected, edited)

			// The edited source checks clean.
			require.NoError(t, s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                2,
				},
				ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: edited}},
			}))
			require.Empty(t, awaitDiagnostics(t, published, uri, 2))
		})
	}
}

// TestSignatureActionStaleDocument asserts that the action is not offered while the
// document holds an edit the last check has not read, since its edits are offsets into the
// text that check read.
func TestSignatureActionStaleDocument(t *testing.T) {
	src := "fn noop(x: number) {}"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	require.Equal(t,
		[]string{"0:3-0:7 'noop' is declared at a script's top level, so it needs a return type; write `-> undefined`"},
		awaitDiagnostics(t, published, uri, 1))
	// Recording a newer version without checking it leaves the check output stale.
	s.mu.Lock()
	doc := s.documents[uri]
	doc.Text = "\n" + src
	doc.Version = 2
	s.documents[uri] = doc
	s.mu.Unlock()
	pos := protocol.Position{Line: 1, Character: 3}
	result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        protocol.Range{Start: pos, End: pos},
		Context:      protocol.CodeActionContext{Only: []protocol.CodeActionKind{signatureActionKind}},
	})
	require.NoError(t, err)
	require.Empty(t, result)
}

// TestSignatureActionInLib asserts that the action is offered on a function declared in a
// lib/ file, whose declarations the check output holds in the module rather than a script.
func TestSignatureActionInLib(t *testing.T) {
	src := "export fn count(xs: &Array<number>) { return xs.length }"
	s := integrationServer(t, map[string]string{"lib/index.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "lib/index.esc", src)
	require.Equal(t,
		[]string{"0:10-0:15 'count' is declared at module level, so it needs a return type; write `-> number`"},
		awaitDiagnostics(t, published, uri, 1))
	pos := protocol.Position{Line: 0, Character: 10}
	result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        protocol.Range{Start: pos, End: pos},
		Context:      protocol.CodeActionContext{Only: []protocol.CodeActionKind{signatureActionKind}},
	})
	require.NoError(t, err)
	actions, ok := result.([]protocol.CodeAction)
	require.True(t, ok)
	require.Len(t, actions, 1)
	require.Equal(t,
		"export fn count(xs: &Array<number>) -> number { return xs.length }",
		applyEdits(src, actions[0].Edit.Changes[uri]))
}

func TestSkipPast(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     byte
		marks    string
		// expected is the offset skipPast returns, or -1 when it fails.
		expected int
	}{
		{name: "Immediate", contents: ")", want: ')', expected: 1},
		{name: "Whitespace", contents: " \t\n)", want: ')', expected: 4},
		{name: "Marks", contents: ", ...)", want: ')', marks: ",.", expected: 6},
		{name: "LineComment", contents: "// c\n)", want: ')', expected: 6},
		{name: "BlockComment", contents: "/* ) */)", want: ')', expected: 8},
		{name: "UnclosedBlockComment", contents: "/* )", want: ')', expected: -1},
		{name: "UnterminatedLineComment", contents: "// )", want: ')', expected: -1},
		{name: "OtherByte", contents: "x)", want: ')', expected: -1},
		{name: "End", contents: "  ", want: ')', expected: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := skipPast(tt.contents, 0, tt.want, tt.marks)
			if tt.expected < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestMissingSignatureSeverity asserts that MissingSignatureError is published as a warning.
func TestMissingSignatureSeverity(t *testing.T) {
	src := "fn noop(x: number) {}"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	diags := awaitPublished(t, published, uri, 1)
	require.Len(t, diags, 1)
	require.Equal(t, addr(protocol.DiagnosticSeverityWarning), diags[0].Severity)
}

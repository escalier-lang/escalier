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
// is src with the action's edits applied, or "" when the action is not offered.
func TestSignatureAction(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		cursor   string
		expected string
	}{
		{
			name:     "UnannotatedParam",
			src:      "fn read(p) { return p.x }",
			cursor:   "read",
			expected: "fn read<T0>(p: &{x: T0}) -> &T0 { return p.x }",
		},
		{
			name:     "MissingReturnType",
			src:      "fn count(xs: &Array<number>) { return xs.length }",
			cursor:   "xs:",
			expected: "fn count(xs: &Array<number>) -> number { return xs.length }",
		},
		{
			name:     "ReturnsUndefined",
			src:      "fn noop(x: number) {}",
			cursor:   "noop",
			expected: "fn noop(x: number) -> undefined {}",
		},
		{
			name:     "NoParams",
			src:      "fn one() { return 1 }",
			cursor:   "fn",
			expected: "fn one() -> 1 { return 1 }",
		},
		{
			name:     "WrittenTypeParam",
			src:      "fn id<T>(x: T) { return x }",
			cursor:   "id",
			expected: "fn id<T>(x: T) -> T { return x }",
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
		},
		{
			name:     "MethodReturnsReceiverBorrow",
			src:      "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) { return &self.inner },\n}",
			cursor:   "peek",
			expected: "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) -> &Flat { return &self.inner },\n}",
		},
		{
			name:     "MethodOfGenericClass",
			src:      "class Cell<T> {\n\tvalue: T,\n\tget(&self) { return self.value },\n}",
			cursor:   "get",
			expected: "class Cell<T> {\n\tvalue: T,\n\tget(&self) -> &T { return self.value },\n}",
		},
		{
			name:     "MethodTypeParamHidesClassTypeParam",
			src:      "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) { return x },\n}",
			cursor:   "pass",
			expected: "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) -> T { return x },\n}",
		},
		{
			name:     "BinderListEndsInComma",
			src:      "fn id<T,>(x: T) { return x }",
			cursor:   "id",
			expected: "fn id<T,>(x: T) -> T { return x }",
		},
		{
			name:     "NoParamsAfterBinderList",
			src:      "fn one<T>() { return 1 }",
			cursor:   "one",
			expected: "fn one<T>() -> 1 { return 1 }",
		},
		{
			// Writing the binder list would repeat the `where` clause's bound on `T`.
			name:     "WhereClauseWithAddedBinder",
			src:      "fn f<T>(x: T, y) where T: number { return [x, y] }",
			cursor:   "f<",
			expected: "",
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
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, map[string]string{"bin/main.esc": tt.src})
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, "bin/main.esc", tt.src)
			require.Empty(t, awaitDiagnostics(t, published, uri, 1))

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
	require.Empty(t, awaitDiagnostics(t, published, uri, 1))
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
	require.Empty(t, awaitDiagnostics(t, published, uri, 1))
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

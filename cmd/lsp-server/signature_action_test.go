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
// is src with the action's edits applied, or "" when the action is not offered. diagnostics
// are the diagnostics src reports, in any order.
func TestSignatureAction(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		cursor      string
		expected    string
		diagnostics []string
	}{
		{
			// The ambiguity warning in the body leaves the inferred return type `never`, so
			// the action does not write it.
			name:     "NeverReturnFromABodyWithAWarning",
			src:      "fn h<T>(t: T, k: fn <A>(x: A | T | number) -> A) { return k(5) }",
			cursor:   "h<",
			expected: "",
			diagnostics: []string{
				"0:60-0:61 ambiguous match against t5 | T | number: committed number, but t5 would also match; annotate to disambiguate",
				"0:3-0:4 'h' is declared at a script's top level, so it needs a return type; write `-> never`",
			},
		},
		{
			name:        "ClosureBoundAtTopLevel",
			src:         "val f = fn (x: number) { return x }",
			cursor:      "fn",
			expected:    "val f = fn (x: number) -> number { return x }",
			diagnostics: []string{"0:4-0:5 'f' is a closure at a script's top level, so it needs a return type; write `-> number` on the closure, or annotate 'f'"},
		},
		{
			// A function expression's binder list goes after its `fn` keyword.
			name:     "ClosureGetsBinderList",
			src:      "val g = fn (x) { return x }",
			cursor:   "val",
			expected: "val g = fn <T0>(x: T0) -> T0 { return x }",
			diagnostics: []string{
				"0:12-0:13 parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
				"0:4-0:5 'g' is a closure at a script's top level, so it needs a return type",
			},
		},
		{
			name:        "UnannotatedParam",
			src:         "fn read(p) { return p.x }",
			cursor:      "read",
			expected:    "fn read<T0>(p: &{x: T0}) -> &T0 { return p.x }",
			diagnostics: []string{"0:3-0:7 'read' is declared at a script's top level, so parameter 'p' needs a type and 'read' needs a return type"},
		},
		{
			name:        "MissingReturnType",
			src:         "fn count(xs: &Array<number>) { return xs.length }",
			cursor:      "xs:",
			expected:    "fn count(xs: &Array<number>) -> number { return xs.length }",
			diagnostics: []string{"0:3-0:8 'count' is declared at a script's top level, so it needs a return type; write `-> number`"},
		},
		{
			name:        "ReturnsUndefined",
			src:         "fn noop(x: number) {}",
			cursor:      "noop",
			expected:    "fn noop(x: number) -> undefined {}",
			diagnostics: []string{"0:3-0:7 'noop' is declared at a script's top level, so it needs a return type; write `-> undefined`"},
		},
		{
			name:        "NoParams",
			src:         "fn one() { return 1 }",
			cursor:      "fn",
			expected:    "fn one() -> 1 { return 1 }",
			diagnostics: []string{"0:3-0:6 'one' is declared at a script's top level, so it needs a return type; write `-> 1`"},
		},
		{
			name:        "WrittenTypeParam",
			src:         "fn id<T>(x: T) { return x }",
			cursor:      "id",
			expected:    "fn id<T>(x: T) -> T { return x }",
			diagnostics: []string{"0:3-0:5 'id' is declared at a script's top level, so it needs a return type; write `-> T`"},
		},
		{
			name:        "ReturnedBorrowOfOneOfTwoParams",
			src:         "type Flat = {x: number}\nfn pick(a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:      "pick",
			expected:    "type Flat = {x: number}\nfn pick<'a>(a: &'a Flat, b: &Flat) -> &'a Flat { return a }",
			diagnostics: []string{"1:31-1:36 the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, as in `fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat`"},
		},
		{
			name:        "Method",
			src:         "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tget(&self) { return self.inner.x },\n}",
			cursor:      "get",
			expected:    "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tget(&self) -> number { return self.inner.x },\n}",
			diagnostics: []string{"3:1-3:4 'get' is a method, so it needs a return type; write `-> number`"},
		},
		{
			name:        "MethodReturnsReceiverBorrow",
			src:         "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) { return &self.inner },\n}",
			cursor:      "peek",
			expected:    "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpeek(&self, other: &Flat) -> &Flat { return &self.inner },\n}",
			diagnostics: []string{"3:1-3:5 'peek' is a method, so it needs a return type; write `-> &Flat`"},
		},
		{
			name:        "MethodOfGenericClass",
			src:         "class Cell<T> {\n\tvalue: T,\n\tget(&self) { return self.value },\n}",
			cursor:      "get",
			expected:    "class Cell<T> {\n\tvalue: T,\n\tget(&self) -> &T { return self.value },\n}",
			diagnostics: []string{"2:1-2:4 'get' is a method, so it needs a return type; write `-> &T`"},
		},
		{
			name:        "MethodTypeParamHidesClassTypeParam",
			src:         "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) { return x },\n}",
			cursor:      "pass",
			expected:    "class Cell<T> {\n\tvalue: T,\n\tpass<T>(&self, x: T) -> T { return x },\n}",
			diagnostics: []string{"2:1-2:5 'pass' is a method, so it needs a return type; write `-> T`"},
		},
		{
			name:        "BinderListEndsInComma",
			src:         "fn id<T,>(x: T) { return x }",
			cursor:      "id",
			expected:    "fn id<T,>(x: T) -> T { return x }",
			diagnostics: []string{"0:3-0:5 'id' is declared at a script's top level, so it needs a return type; write `-> T`"},
		},
		{
			name:        "NoParamsAfterBinderList",
			src:         "fn one<T>() { return 1 }",
			cursor:      "one",
			expected:    "fn one<T>() -> 1 { return 1 }",
			diagnostics: []string{"0:3-0:6 'one' is declared at a script's top level, so it needs a return type; write `-> 1`"},
		},
		{
			// Writing the binder list would repeat the `where` clause's bound on `T`.
			name:        "WhereClauseWithAddedBinder",
			src:         "fn f<T>(x: T, y) where T: number { return [x, y] }",
			cursor:      "f<",
			expected:    "",
			diagnostics: []string{"0:3-0:4 'f' is declared at a script's top level, so parameter 'y' needs a type and 'f' needs a return type"},
		},
		{
			name:        "AddsLifetimeToWrittenBinders",
			src:         "type Flat = {x: number}\nfn pick<T, U>(t: T, u: U, a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:      "pick",
			expected:    "type Flat = {x: number}\nfn pick<T, U, 'a>(t: T, u: U, a: &'a Flat, b: &Flat) -> &'a Flat { return a }",
			diagnostics: []string{"1:49-1:54 the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, as in `fn pick<'x, T, U>(t: T, u: U, a: &'x Flat, b: &Flat) -> &'x Flat`"},
		},
		{
			// Elision assigns the source's 'q, so a rewritten binder list would drop it while
			// `a: &'q Flat` still names it.
			name:        "RewriteWouldDropWrittenLifetime",
			src:         "type Flat = {x: number}\nfn pick<'q>(q: &'q Flat, a: &Flat, b: &Flat) -> &Flat { return a }",
			cursor:      "pick",
			expected:    "",
			diagnostics: []string{"1:48-1:53 the return type borrows from 'q', 'a' or 'b', and elision cannot tell which; write a lifetime, as in `fn pick<'x, 'q>(q: &'q Flat, a: &'x Flat, b: &Flat) -> &'x Flat`"},
		},
		{
			// The type's span ends inside its parentheses, so the return type goes after the
			// `)` that closes the parameter list, not the type's own.
			name:        "ParenthesizedParamType",
			src:         "fn f(p: &({a: number} | {b: number})) { return p }",
			cursor:      "f(",
			expected:    "fn f(p: &({a: number} | {b: number})) -> &({a: number} | {b: number}) { return p }",
			diagnostics: []string{"0:3-0:4 'f' is declared at a script's top level, so it needs a return type; write `-> &({a: number} | {b: number})`"},
		},
		{
			name:        "CommentsInParamList",
			src:         "fn f(\n\tx: number, // the input\n\ty: number /* scale */\n) { return x * y }",
			cursor:      "f(",
			expected:    "fn f(\n\tx: number, // the input\n\ty: number /* scale */\n) -> number { return x * y }",
			diagnostics: []string{"0:3-0:4 'f' is declared at a script's top level, so it needs a return type; write `-> number`"},
		},
		{
			// The checker renders this method with a binder the source does not write. Every
			// part of its signature is written, so the action adds no binder for it.
			name:     "FullSignatureGenericMethod",
			src:      "class Foo {\n\tvalue: number,\n\tget<T>(&self, d: T) -> number | T {\n\t\tif self.value != 0 { return self.value } else { return d }\n\t},\n}",
			cursor:   "get",
			expected: "",
		},
		{
			name:     "FullSignature",
			src:      "fn full(a: number) -> number { return a }",
			cursor:   "full",
			expected: "",
		},
		{
			name:        "OptionalParamWithoutType",
			src:         "fn f(x?) { return x }",
			cursor:      "f(",
			expected:    "fn f<T0>(x?: T0) -> T0 { return x }",
			diagnostics: []string{"0:3-0:4 'f' is declared at a script's top level, so parameter 'x' needs a type and 'f' needs a return type"},
		},
		{
			name:        "MethodReceiverGetsLifetime",
			src:         "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpick(&self, a: &Flat, c: boolean) { if c { return &self.inner } else { return a } },\n}",
			cursor:      "pick",
			expected:    "type Flat = {x: number}\nclass Box {\n\tinner: Flat,\n\tpick<'a>(&self, a: &'a Flat, c: boolean) -> &Flat | &'a Flat { if c { return &self.inner } else { return a } },\n}",
			diagnostics: []string{"3:1-3:5 'pick' is a method, so it needs a return type; write `pick<'a>(&self, a: &'a Flat, c: boolean) -> &Flat | &'a Flat`"},
		},
		{
			// The method names 's, and the body makes 's outlive the result as well as other's
			// lifetime, so the written signature declares the bound on 's too.
			name:        "NamedLifetimeKeepsTheBoundTheBodyNeeds",
			src:         "type Flat = {x: number}\ndeclare fn either<'r>(a: &'r Flat, b: &'r Flat) -> &'r Flat\nclass Box {\n\tinner: Flat,\n\tpick<'s>(&'s self, other: &Flat) { return either(&self.inner, other) },\n}",
			cursor:      "pick",
			expected:    "type Flat = {x: number}\ndeclare fn either<'r>(a: &'r Flat, b: &'r Flat) -> &'r Flat\nclass Box {\n\tinner: Flat,\n\tpick<'s: 'b, 'a: 'b, 'b>(&'s self, other: &'a Flat) -> &'b Flat { return either(&self.inner, other) },\n}",
			diagnostics: []string{"4:1-4:5 'pick' is a method, so it needs a return type; write `pick<'s: 'b, 'a: 'b, 'b>(&'s self, other: &'a Flat) -> &'b Flat`"},
		},
		{
			// Elision cannot place a thrown borrow of two inputs, so the declaration has an error
			// beside the missing signature and the action is not offered.
			name:   "ThrownBorrowOfTwoInputsOffersNoAction",
			src:    "type Flat = {x: number}\nfn f(a: &Flat, b: &Flat, c: boolean) throws &Flat { if c { throw a } else { throw b } }",
			cursor: "f(",
			diagnostics: []string{
				"1:44-1:49 the thrown type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, " +
					"as in `fn f<'x>(a: &'x Flat, b: &Flat, c: boolean) throws &'x Flat`",
				"1:3-1:4 'f' is declared at a script's top level, so it needs a return type; write `-> never`",
			},
		},
		{
			name:        "ThrownBorrowOfOneInputElides",
			src:         "type Flat = {x: number}\nfn f(a: &Flat, c: boolean) throws &Flat { throw a }",
			cursor:      "f(",
			expected:    "type Flat = {x: number}\nfn f(a: &Flat, c: boolean) -> never throws &Flat { throw a }",
			diagnostics: []string{"1:3-1:4 'f' is declared at a script's top level, so it needs a return type; write `-> never`"},
		},
		{
			// The inferred signature adds the binder `T0` and elides the source's 'q, so a
			// rewritten binder list would drop 'q while `q: &'q Flat` still names it.
			name:        "AddedTypeParamWouldDropWrittenLifetime",
			src:         "type Flat = {x: number}\nfn f<T, 'q>(t: T, q: &'q Flat, y) { return [t, y] }",
			cursor:      "f<",
			expected:    "",
			diagnostics: []string{"1:3-1:4 'f' is declared at a script's top level, so parameter 'y' needs a type and 'f' needs a return type"},
		},
		{
			name:        "AfterDeclaredFn",
			src:         "declare fn g(x: number) -> number\nfn one() { return 1 }",
			cursor:      "one",
			expected:    "declare fn g(x: number) -> number\nfn one() -> 1 { return 1 }",
			diagnostics: []string{"1:3-1:6 'one' is declared at a script's top level, so it needs a return type; write `-> 1`"},
		},
		{
			name:        "CursorInBody",
			src:         "fn noop(x: number) { x }",
			cursor:      "x }",
			expected:    "",
			diagnostics: []string{"0:3-0:7 'noop' is declared at a script's top level, so it needs a return type; write `-> undefined`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, map[string]string{"bin/main.esc": tt.src})
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, "bin/main.esc", tt.src)
			got := awaitDiagnostics(t, published, uri, 1)
			if len(tt.diagnostics) == 0 {
				require.Empty(t, got)
			} else {
				require.ElementsMatch(t, tt.diagnostics, got)
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

// TestWrittenNamedLifetimeSignatureChecks asserts that the signature the code action writes
// for a method that names a lifetime checks without a diagnostic.
func TestWrittenNamedLifetimeSignatureChecks(t *testing.T) {
	src := "type Flat = {x: number}\ndeclare fn either<'r>(a: &'r Flat, b: &'r Flat) -> &'r Flat\nclass Box {\n\tinner: Flat,\n\tpick<'s: 'b, 'a: 'b, 'b>(&'s self, other: &'a Flat) -> &'b Flat { return either(&self.inner, other) },\n}"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	require.Empty(t, awaitDiagnostics(t, published, uri, 1))
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

// TestMissingSignatureSeverity asserts that MissingSignatureError is published as an error.
func TestMissingSignatureSeverity(t *testing.T) {
	src := "fn noop(x: number) {}"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	diags := awaitPublished(t, published, uri, 1)
	require.Len(t, diags, 1)
	require.Equal(t, addr(protocol.DiagnosticSeverityError), diags[0].Severity)
}

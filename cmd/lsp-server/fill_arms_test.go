package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestFillArmsAction covers the code action that adds the arms a `match` or a `catch` is
// missing. Each case opens path from files, which reports the one diagnostic in diagnostic.
// It requests the action as a quick fix for that diagnostic, and as a refactor at the start
// of cursor's first occurrence in the file. Both must produce expected, the file with the
// action's edits applied. A case whose diagnostic is "" reports nothing, so only the
// refactor is requested. title is the action's title, or "" for the `match` title. after is
// the diagnostics the edited file reports.
func TestFillArmsAction(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		path       string
		cursor     string
		diagnostic string
		title      string
		expected   string
		after      []string
	}{
		{
			name: "EnumUnitAndFieldVariants",
			files: map[string]string{"bin/main.esc": "enum Shape {\n" +
				"\tCircle(radius: number),\n" +
				"\tRect(width: number, height: number),\n" +
				"\tEmpty,\n" +
				"}\n" +
				"declare val s: Shape\n" +
				"val area = match s {\n" +
				"\tShape.Circle(radius) => radius,\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "6:11-8:1 match is not exhaustive; add branches for `Shape.Rect`, `Shape.Empty`",
			expected: "enum Shape {\n" +
				"\tCircle(radius: number),\n" +
				"\tRect(width: number, height: number),\n" +
				"\tEmpty,\n" +
				"}\n" +
				"declare val s: Shape\n" +
				"val area = match s {\n" +
				"\tShape.Circle(radius) => radius,\n" +
				"\tShape.Rect(width, height) => throw \"todo\",\n" +
				"\tShape.Empty => throw \"todo\",\n" +
				"}\n",
		},
		{
			name: "StringLiteralsOnOneLine",
			files: map[string]string{"bin/main.esc": "declare val method: \"get\" | \"post\" | \"put\"\n" +
				"val code = match method { \"get\" => 1 }\n"},
			path:       "bin/main.esc",
			cursor:     "method {",
			diagnostic: "1:11-1:38 match is not exhaustive; add branches for `\"post\"`, `\"put\"`",
			expected: "declare val method: \"get\" | \"post\" | \"put\"\n" +
				"val code = match method { \"get\" => 1, \"post\" => throw \"todo\", \"put\" => throw \"todo\" }\n",
		},
		{
			name: "StringLiteralsOnOneLineWithTrailingComma",
			files: map[string]string{"bin/main.esc": "declare val method: \"get\" | \"post\"\n" +
				"val code = match method { \"get\" => 1, }\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "1:11-1:39 match is not exhaustive; add a branch for `\"post\"`",
			expected: "declare val method: \"get\" | \"post\"\n" +
				"val code = match method { \"get\" => 1, \"post\" => throw \"todo\", }\n",
		},
		{
			name: "ClassesWithoutTrailingComma",
			files: map[string]string{"bin/main.esc": "class HttpError { status: number }\n" +
				"class TimeoutError { ms: number }\n" +
				"class ParseError { text: string }\n" +
				"declare val err: HttpError | TimeoutError | ParseError\n" +
				"val msg = match err {\n" +
				"    HttpError{status} => \"http\" // the common case\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "HttpError{status}",
			diagnostic: "4:10-6:1 match is not exhaustive; add branches for `TimeoutError`, `ParseError`",
			expected: "class HttpError { status: number }\n" +
				"class TimeoutError { ms: number }\n" +
				"class ParseError { text: string }\n" +
				"declare val err: HttpError | TimeoutError | ParseError\n" +
				"val msg = match err {\n" +
				"    HttpError{status} => \"http\", // the common case\n" +
				"    TimeoutError{} => throw \"todo\",\n" +
				"    ParseError{} => throw \"todo\"\n" +
				"}\n",
		},
		{
			name: "InexactObjectNeedsCatchAll",
			files: map[string]string{"bin/main.esc": "declare val p: {x: number, ...}\n" +
				"val r = match p {\n" +
				"\t{x: 0} => \"origin\",\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "1:8-3:1 match is not exhaustive; `{x: number, ...}` admits values no pattern names, so add a catch-all branch",
			expected: "declare val p: {x: number, ...}\n" +
				"val r = match p {\n" +
				"\t{x: 0} => \"origin\",\n" +
				"\t_ => throw \"todo\",\n" +
				"}\n",
		},
		{
			name: "PrimitiveMemberAndCatchAll",
			files: map[string]string{"bin/main.esc": "declare val n: number\n" +
				"val r = match n { 0 => \"zero\" }\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "1:8-1:31 match is not exhaustive; `number` admits values no pattern names, so add a catch-all branch",
			expected: "declare val n: number\n" +
				"val r = match n { 0 => \"zero\", _ => throw \"todo\" }\n",
		},
		{
			name:       "EmptyMatch",
			files:      map[string]string{"bin/main.esc": "declare val b: boolean | null\nval r = match b {}\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "1:8-1:18 match is not exhaustive; add branches for `boolean`, `null`",
			expected: "declare val b: boolean | null\n" +
				"val r = match b {\n" +
				"    true => throw \"todo\",\n" +
				"    false => throw \"todo\",\n" +
				"    null => throw \"todo\",\n" +
				"}\n",
		},
		{
			name: "ObjectAndTupleShapes",
			files: map[string]string{"bin/main.esc": "declare val v: [number, string] | {a: number, b: string} | string | undefined\n" +
				"val r = match v {\n" +
				"\tundefined => 0,\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "undefined =>",
			diagnostic: "1:8-3:1 match is not exhaustive; add branches for `[number, string]`, `{a: number, b: string}`, `string`",
			expected: "declare val v: [number, string] | {a: number, b: string} | string | undefined\n" +
				"val r = match v {\n" +
				"\tundefined => 0,\n" +
				"\t[_, _] => throw \"todo\",\n" +
				"\t{a, b} => throw \"todo\",\n" +
				"\tvalue: string => throw \"todo\",\n" +
				"}\n",
		},
		{
			name: "LibModule",
			files: map[string]string{"lib/index.esc": "export enum Result {\n" +
				"\tOk(value: number),\n" +
				"\tErr(message: string),\n" +
				"}\n" +
				"export fn show(r: Result) -> string {\n" +
				"\treturn match r {\n" +
				"\t\tResult.Ok(value) => `${value}`,\n" +
				"\t}\n" +
				"}\n"},
			path:       "lib/index.esc",
			cursor:     "match",
			diagnostic: "5:8-7:2 match is not exhaustive; add a branch for `Result.Err`",
			expected: "export enum Result {\n" +
				"\tOk(value: number),\n" +
				"\tErr(message: string),\n" +
				"}\n" +
				"export fn show(r: Result) -> string {\n" +
				"\treturn match r {\n" +
				"\t\tResult.Ok(value) => `${value}`,\n" +
				"\t\tResult.Err(message) => throw \"todo\",\n" +
				"\t}\n" +
				"}\n",
			// A function with no `throws` clause raises nothing, so the thrown placeholder is
			// reported until the arm is written.
			after: []string{"7:31-7:37 `show` can throw `\"todo\"`, but it doesn't declare it; add `throws string` to `show`'s signature"},
		},
		{
			name: "ClassInLibNamespace",
			files: map[string]string{
				"lib/geo/shapes.esc": "export class Circle { radius: number }\nexport class Square { side: number }\n",
				"lib/index.esc": "export fn name(s: geo.Circle | geo.Square) -> string throws _ {\n" +
					"\treturn match s {\n" +
					"\t\tgeo.Circle{} => \"circle\",\n" +
					"\t}\n" +
					"}\n",
			},
			path:       "lib/index.esc",
			cursor:     "match",
			diagnostic: "1:8-3:2 match is not exhaustive; add a branch for `Square`",
			expected: "export fn name(s: geo.Circle | geo.Square) -> string throws _ {\n" +
				"\treturn match s {\n" +
				"\t\tgeo.Circle{} => \"circle\",\n" +
				"\t\tgeo.Square{} => throw \"todo\",\n" +
				"\t}\n" +
				"}\n",
		},
		{
			name: "EnumInsideItsLibNamespace",
			files: map[string]string{
				"lib/geo/shape.esc": "export enum Shape {\n" +
					"\tCircle(radius: number),\n" +
					"\tSquare(side: number),\n" +
					"}\n" +
					"export fn size(s: Shape) -> number throws _ {\n" +
					"\treturn match s {\n" +
					"\t\tShape.Circle(radius) => radius,\n" +
					"\t}\n" +
					"}\n",
				"lib/index.esc": "export val one = 1\n",
			},
			path:       "lib/geo/shape.esc",
			cursor:     "match",
			diagnostic: "5:8-7:2 match is not exhaustive; add a branch for `Shape.Square`",
			expected: "export enum Shape {\n" +
				"\tCircle(radius: number),\n" +
				"\tSquare(side: number),\n" +
				"}\n" +
				"export fn size(s: Shape) -> number throws _ {\n" +
				"\treturn match s {\n" +
				"\t\tShape.Circle(radius) => radius,\n" +
				"\t\tShape.Square(side) => throw \"todo\",\n" +
				"\t}\n" +
				"}\n",
		},
		{
			name: "EnumFromAnotherLibNamespace",
			files: map[string]string{
				"lib/geo/shape.esc": "export enum Shape {\n\tCircle(radius: number),\n\tSquare(side: number),\n}\n",
				"lib/index.esc": "export fn size(s: geo.Shape) -> number throws _ {\n" +
					"\treturn match s {\n" +
					"\t}\n" +
					"}\n",
			},
			path:       "lib/index.esc",
			cursor:     "match",
			diagnostic: "1:8-2:2 match is not exhaustive; add branches for `Shape.Circle`, `Shape.Square`",
			expected: "export fn size(s: geo.Shape) -> number throws _ {\n" +
				"\treturn match s {\n" +
				"\t\tgeo.Shape.Circle(radius) => throw \"todo\",\n" +
				"\t\tgeo.Shape.Square(side) => throw \"todo\",\n" +
				"\t}\n" +
				"}\n",
		},
		{
			// The guarded arm's value is left for the user to cover.
			name: "GuardedWitnessLeftAlone",
			files: map[string]string{"bin/main.esc": "declare val b: boolean\n" +
				"declare val strict: boolean\n" +
				"val r = match b { true if strict => 1 }\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "2:8-2:39 match is not exhaustive; add a branch for `false`; `true` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it",
			expected: "declare val b: boolean\n" +
				"declare val strict: boolean\n" +
				"val r = match b { true if strict => 1, false => throw \"todo\" }\n",
			after: []string{"2:8-2:62 match is not exhaustive; `true` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it"},
		},
		{
			name: "ParenthesizedBody",
			files: map[string]string{"bin/main.esc": "declare val m: \"a\" | \"b\"\n" +
				"declare val x: number\n" +
				"val r = match m {\n" +
				"\t\"a\" => (x + 1)\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "2:8-4:1 match is not exhaustive; add a branch for `\"b\"`",
			expected: "declare val m: \"a\" | \"b\"\n" +
				"declare val x: number\n" +
				"val r = match m {\n" +
				"\t\"a\" => (x + 1),\n" +
				"\t\"b\" => throw \"todo\"\n" +
				"}\n",
		},
		{
			name: "CatchRethrowingWhatTheClauseRejects",
			files: map[string]string{"bin/main.esc": "class A { a: number }\n" +
				"class B { b: number }\n" +
				"class C { c: number }\n" +
				"declare fn g() -> number throws A | B | C\n" +
				"fn f() -> number {\n" +
				"\treturn try {\n" +
				"\t\tg()\n" +
				"\t} catch {\n" +
				"\t\tA{} => 1,\n" +
				"\t}\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "catch",
			diagnostic: "5:8-8:10 the catch arms leave B | C uncovered, so it is rethrown, and the enclosing `throws never` does not admit it. Cover it with a catch arm, or widen the enclosing clause",
			title:      fillCatchArmsTitle,
			expected: "class A { a: number }\n" +
				"class B { b: number }\n" +
				"class C { c: number }\n" +
				"declare fn g() -> number throws A | B | C\n" +
				"fn f() -> number {\n" +
				"\treturn try {\n" +
				"\t\tg()\n" +
				"\t} catch {\n" +
				"\t\tA{} => 1,\n" +
				"\t\tB{} => throw \"todo\",\n" +
				"\t\tC{} => throw \"todo\",\n" +
				"\t}\n" +
				"}\n",
			after: []string{
				"9:15-9:21 `f` can throw `\"todo\"`, but it doesn't declare it; add `throws string` to `f`'s signature",
				"10:15-10:21 `f` can throw `\"todo\"`, but it doesn't declare it; add `throws string` to `f`'s signature",
			},
		},
		{
			// What the arms leave reaches the inferred clause, so the check reports nothing
			// and the action is offered as a refactor only.
			name: "CatchOfImportedClassInLib",
			files: map[string]string{"lib/index.esc": "import \"std:error\"\n" +
				"class A { a: number }\n" +
				"declare fn g() -> number throws A | error.Error\n" +
				"export fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch { A{} => 1 }\n" +
				"}\n"},
			path:   "lib/index.esc",
			cursor: "A{}",
			title:  fillCatchArmsTitle,
			expected: "import \"std:error\"\n" +
				"class A { a: number }\n" +
				"declare fn g() -> number throws A | error.Error\n" +
				"export fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch { A{} => 1, error.Error{} => throw \"todo\" }\n" +
				"}\n",
		},
		{
			// The cursor sits in a `match` inside the try block, and the `match` is innermost.
			name: "MatchInsideTryIsInnermost",
			files: map[string]string{"bin/main.esc": "class A { a: number }\n" +
				"declare fn g() -> \"x\" | \"y\" throws A | string\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { match g() { \"x\" => 1 } } catch { A{} => 1 }\n" +
				"}\n"},
			path:       "bin/main.esc",
			cursor:     "match",
			diagnostic: "3:14-3:36 match is not exhaustive; add a branch for `\"y\"`",
			expected: "class A { a: number }\n" +
				"declare fn g() -> \"x\" | \"y\" throws A | string\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { match g() { \"x\" => 1, \"y\" => throw \"todo\" } } catch { A{} => 1 }\n" +
				"}\n",
		},
		{
			name: "CatchWithParenthesizedLastBody",
			files: map[string]string{"bin/main.esc": "class A { a: number }\n" +
				"class B { b: number }\n" +
				"declare fn g() -> number throws A | B\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch { A{} => (1) }\n" +
				"}\n"},
			path:   "bin/main.esc",
			cursor: "}\n}",
			title:  fillCatchArmsTitle,
			expected: "class A { a: number }\n" +
				"class B { b: number }\n" +
				"declare fn g() -> number throws A | B\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch { A{} => (1), B{} => throw \"todo\" }\n" +
				"}\n",
		},
		{
			// The comma goes before the comment, and the new arm after it.
			name: "CatchWithCommentAfterLastArm",
			files: map[string]string{"bin/main.esc": "class A { a: number }\n" +
				"class B { b: number }\n" +
				"declare fn g() -> number throws A | B\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch {\n" +
				"\t\tA{} => 1 // the common case\n" +
				"\t}\n" +
				"}\n"},
			path:   "bin/main.esc",
			cursor: "catch",
			title:  fillCatchArmsTitle,
			expected: "class A { a: number }\n" +
				"class B { b: number }\n" +
				"declare fn g() -> number throws A | B\n" +
				"fn f() -> number throws _ {\n" +
				"\treturn try { g() } catch {\n" +
				"\t\tA{} => 1, // the common case\n" +
				"\t\tB{} => throw \"todo\"\n" +
				"\t}\n" +
				"}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.files[tt.path]
			s := integrationServer(t, tt.files)
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, tt.path, src)
			title := tt.title
			if title == "" {
				title = fillMatchArmsTitle
			}
			diags := awaitPublished(t, published, uri, 1)
			if tt.diagnostic == "" {
				require.Empty(t, diags)
			} else {
				require.Equal(t, []string{tt.diagnostic}, renderDiagnostics(diags))
				quickFix := requestCodeActions(t, s, uri, diags[0].Range.Start, diags,
					[]protocol.CodeActionKind{protocol.CodeActionKindQuickFix})
				require.Len(t, quickFix, 1)
				require.Equal(t, title, quickFix[0].Title)
				require.Equal(t, addr(protocol.CodeActionKindQuickFix), quickFix[0].Kind)
				require.Equal(t, diags, quickFix[0].Diagnostics)
				require.Equal(t, tt.expected, applyEdits(src, quickFix[0].Edit.Changes[uri]))
			}

			refactor := requestCodeActions(t, s, uri, cursorPosition(t, src, tt.cursor), nil,
				[]protocol.CodeActionKind{protocol.CodeActionKindRefactor})
			require.Len(t, refactor, 1)
			require.Equal(t, title, refactor[0].Title)
			require.Equal(t, addr(fillArmsRefactorKind), refactor[0].Kind)
			edited := applyEdits(src, refactor[0].Edit.Changes[uri])
			require.Equal(t, tt.expected, edited)

			require.NoError(t, s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                2,
				},
				ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: edited}},
			}))
			after := awaitDiagnostics(t, published, uri, 2)
			if tt.after == nil {
				require.Empty(t, after)
			} else {
				require.Equal(t, tt.after, after)
			}
		})
	}
}

// TestFillArmsActionOnlyOnce asserts that a request asking for every kind, with the
// diagnostic in its context, receives the quick fix and no refactor duplicating it.
func TestFillArmsActionOnlyOnce(t *testing.T) {
	src := "declare val b: boolean\nval r = match b { true => 1 }\n"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	diags := awaitPublished(t, published, uri, 1)
	require.Len(t, diags, 1)
	actions := requestCodeActions(t, s, uri, diags[0].Range.Start, diags,
		[]protocol.CodeActionKind{protocol.CodeActionKindQuickFix, protocol.CodeActionKindRefactor})
	require.Len(t, actions, 1)
	require.Equal(t, addr(protocol.CodeActionKindQuickFix), actions[0].Kind)
}

// TestFillArmsActionOutsideMatch asserts that the refactor is not offered at a cursor
// outside every non-exhaustive `match`.
func TestFillArmsActionOutsideMatch(t *testing.T) {
	src := "declare val b: boolean\nval r = match b { true => 1 }\n"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	require.Len(t, awaitPublished(t, published, uri, 1), 1)
	actions := requestCodeActions(t, s, uri, protocol.Position{Line: 0, Character: 2}, nil,
		[]protocol.CodeActionKind{protocol.CodeActionKindRefactor})
	require.Empty(t, actions)
}

// requestCodeActions requests the code actions of the kinds in only at pos in the document
// uri, with diags as the request's diagnostics.
func requestCodeActions(
	t *testing.T,
	s *Server,
	uri protocol.DocumentUri,
	pos protocol.Position,
	diags []protocol.Diagnostic,
	only []protocol.CodeActionKind,
) []protocol.CodeAction {
	t.Helper()
	result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        protocol.Range{Start: pos, End: pos},
		Context:      protocol.CodeActionContext{Diagnostics: diags, Only: only},
	})
	require.NoError(t, err)
	actions, ok := result.([]protocol.CodeAction)
	require.True(t, ok)
	return actions
}

// cursorPosition returns the position of the first occurrence of cursor in src.
func cursorPosition(t *testing.T, src, cursor string) protocol.Position {
	t.Helper()
	offset := strings.Index(src, cursor)
	require.GreaterOrEqual(t, offset, 0)
	before := src[:offset]
	line := strings.Count(before, "\n")
	col := len(before) - strings.LastIndex(before, "\n") - 1
	return protocol.Position{Line: protocol.UInteger(line), Character: protocol.UInteger(col)}
}

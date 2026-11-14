package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// integrationServer creates a Server backed by real files on disk.
// It writes the given files to a temp directory and configures the server
// with the correct rootURI and libFilesCache.
func integrationServer(t *testing.T, files map[string]string) *Server {
	t.Helper()
	root := t.TempDir()
	for relPath, contents := range files {
		absPath := filepath.Join(root, relPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(absPath), 0o755))
		require.NoError(t, os.WriteFile(absPath, []byte(contents), 0o644))
	}
	s := NewServer()
	s.rootURI = pathToURI(root)
	// Populate file caches so collectSources can find lib/ and bin/ files.
	if err := s.refreshLibFilesCache(); err != nil {
		t.Fatalf("refreshLibFilesCache: %v", err)
	}
	if err := s.refreshBinFilesCache(); err != nil {
		t.Fatalf("refreshBinFilesCache: %v", err)
	}
	return s
}

// openFile simulates opening a file in the editor, triggering synchronous validation.
func openFile(t *testing.T, s *Server, relPath string, contents string) protocol.DocumentUri {
	t.Helper()
	return openFileIn(t, s, &glsp.Context{}, relPath, contents)
}

// openFileIn is openFile with the server's notifications sent to ctx.
func openFileIn(t *testing.T, s *Server, ctx *glsp.Context, relPath string, contents string) protocol.DocumentUri {
	t.Helper()
	rootPath := uriToPath(s.rootURI)
	absPath := filepath.Join(rootPath, relPath)
	uri := protocol.DocumentUri(pathToURI(absPath))
	err := s.textDocumentDidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: "escalier",
			Version:    1,
			Text:       contents,
		},
	})
	require.NoError(t, err)
	return uri
}

// getCompletionLabelsAt calls textDocumentCompletion and returns the labels.
// The server's sortAndLimit sorts items alphabetically, so labels arrive sorted.
func getCompletionLabelsAt(t *testing.T, s *Server, uri protocol.DocumentUri, line, col int) []string {
	t.Helper()
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
	labels := make([]string, len(list.Items))
	for i, item := range list.Items {
		labels[i] = item.Label
	}
	return labels
}

// --- Integration tests: completions through full validation pipeline ---

func TestIntegration_ScriptCompletionsThroughValidation(t *testing.T) {
	files := map[string]string{
		"bin/main.esc": "val x = 5\nval y = x",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	// Cursor at end of line 2 — should see 'x' and 'y' in scope completions.
	labels := getCompletionLabelsAt(t, s, uri, 2, 10)
	assert.Contains(t, labels, "x")
}

func TestIntegration_LibCompletionsThroughValidation(t *testing.T) {
	files := map[string]string{
		"lib/math.esc": "fn add(a: number, b: number) -> number { a + b }\nval z = add",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "lib/math.esc", files["lib/math.esc"])

	// Cursor at end of line 2 — should see 'add' in completions.
	labels := getCompletionLabelsAt(t, s, uri, 2, 12)
	assert.Contains(t, labels, "add")
}

func TestIntegration_BinScriptSeesLibExports(t *testing.T) {
	files := map[string]string{
		"lib/math.esc": "export fn add(a: number, b: number) -> number { a + b }",
		"bin/main.esc": "val sum = add(1, 2)\nval x = ad",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	// Cursor at end of "val x = ad" — prefix "ad" filters to 'add' from lib.
	labels := getCompletionLabelsAt(t, s, uri, 2, 11)
	assert.Contains(t, labels, "add", "bin/ script should see lib/ exports in completions")
}

// --- Integration tests: go-to-definition through validation ---

func TestIntegration_DefinitionInScript(t *testing.T) {
	// "val x = 5\nval y = x" — go-to-definition on 'x' in line 2 should
	// point to 'x' declaration in line 1.
	files := map[string]string{
		"bin/main.esc": "val x = 5\nval y = x",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	// Position on 'x' in "val y = x" (line 2, col 9)
	result, err := s.textDocumentDefinition(&glsp.Context{}, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      1, // 0-based: line 2
				Character: 8, // 0-based: 'x'
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	loc, ok := result.(protocol.Location)
	require.True(t, ok, "expected protocol.Location, got %T", result)
	assert.Equal(t, uri, loc.URI)
	// 'x' in "val x" is at line 1 (0-based: 0), col 5 (0-based: 4)
	assert.Equal(t, protocol.UInteger(0), loc.Range.Start.Line)
	assert.Equal(t, protocol.UInteger(4), loc.Range.Start.Character)
}

// --- Integration tests: hover through validation ---

func TestIntegration_HoverShowsType(t *testing.T) {
	files := map[string]string{
		"bin/main.esc": "val x: number = 5",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	// Hover on 'x' (line 1, col 5 — 0-based: 0, 4)
	hover, err := s.textDocumentHover(&glsp.Context{}, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      0,
				Character: 4,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, hover)

	content, ok := hover.Contents.(protocol.MarkupContent)
	require.True(t, ok)
	assert.Equal(t, escalierCode("x: number"), content.Value)
}

// --- Integration test: diagnostics ---

func TestIntegration_DiagnosticsPublished(t *testing.T) {
	// Validate a script with a type error and verify checkOutput has errors.
	files := map[string]string{
		"bin/main.esc": "val x: number = \"hello\"",
	}
	s := integrationServer(t, files)
	_ = openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	s.mu.RLock()
	co := s.checkOutput
	s.mu.RUnlock()
	require.NotNil(t, co)

	// There should be type errors from assigning a string to a number.
	hasErrors := len(co.ParseErrors) > 0 || len(co.TypeErrors) > 0
	assert.True(t, hasErrors, "expected parse or type errors for type mismatch")
}

func TestIntegration_DiagnosticsNeverNil(t *testing.T) {
	// When a file has no errors, the published diagnostics must be an empty
	// slice (not nil). A nil slice serializes as JSON null which crashes
	// clients that call diagnostics.map(...).
	files := map[string]string{
		"lib/math.esc": `export fn add(a: number, b: number) -> number {
	return a + b
}`,
		"bin/main.esc": "val x = 5",
	}
	s := integrationServer(t, files)
	_ = openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	s.mu.RLock()
	co := s.checkOutput
	s.mu.RUnlock()
	require.NotNil(t, co)

	// No errors expected for valid code.
	assert.Empty(t, co.ParseErrors)
	assert.Empty(t, co.TypeErrors)

	// Simulate what the diagnostics publishing code does and verify
	// emptyIfNil produces a non-nil slice.
	diagsBySourceID := make(map[int][]protocol.Diagnostic)
	// Intentionally don't populate any entries — this mirrors the case
	// where a file has zero errors.
	missingID := 999
	diags := emptyIfNil(diagsBySourceID[missingID])
	assert.NotNil(t, diags, "diagnostics must be non-nil empty slice, not nil")
	assert.Len(t, diags, 0)
}

// --- Integration test: definition in lib/ module ---

func TestIntegration_DefinitionInModule(t *testing.T) {
	files := map[string]string{
		"lib/math.esc": "fn add(a: number, b: number) -> number { a + b }\nval sum = add(1, 2)",
	}
	s := integrationServer(t, files)
	uri := openFile(t, s, "lib/math.esc", files["lib/math.esc"])

	// Position on 'add' in "val sum = add(1, 2)" (line 2, col 11-13)
	result, err := s.textDocumentDefinition(&glsp.Context{}, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      1, // 0-based: line 2
				Character: 10,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	loc, ok := result.(protocol.Location)
	require.True(t, ok, "expected protocol.Location, got %T", result)
	assert.Equal(t, uri, loc.URI)
	// 'add' is defined on line 1 (0-based: line 0).
	assert.Equal(t, protocol.UInteger(0), loc.Range.Start.Line)
}

// --- Issues 1+2: bin/ scripts must not leak bindings between each other ---

func TestIntegration_BinScriptsDoNotLeakBindings(t *testing.T) {
	// Two bin/ scripts: alpha.esc defines 'secret', beta.esc should NOT see it.
	files := map[string]string{
		"bin/alpha.esc": "val secret = 42",
		"bin/beta.esc":  "val y = 1\nval z = y",
	}
	s := integrationServer(t, files)

	// Open alpha first so it gets checked.
	openFile(t, s, "bin/alpha.esc", files["bin/alpha.esc"])
	// Open beta — completions should NOT include 'secret' from alpha.
	betaURI := openFile(t, s, "bin/beta.esc", files["bin/beta.esc"])

	labels := getCompletionLabelsAt(t, s, betaURI, 2, 10)
	assert.Contains(t, labels, "y", "beta should see its own binding 'y'")
	assert.NotContains(t, labels, "secret", "beta must NOT see alpha's binding 'secret'")
}

// --- Issue 5: incremental bin-only validation reuses lib output ---

func TestIntegration_IncrementalBinValidation(t *testing.T) {
	files := map[string]string{
		"lib/math.esc": "export fn add(a: number, b: number) -> number { a + b }",
		"bin/main.esc": "val x = add(1, 2)",
	}
	s := integrationServer(t, files)

	// Open both files to trigger a full validation.
	openFile(t, s, "lib/math.esc", files["lib/math.esc"])
	openFile(t, s, "bin/main.esc", files["bin/main.esc"])

	s.mu.RLock()
	co1 := s.checkOutput
	mod1 := co1.Module
	s.mu.RUnlock()
	require.NotNil(t, mod1, "lib module should be populated after full validation")

	// Simulate editing the bin/ file (didChange). This should take the
	// incremental fast path and NOT re-parse lib/ files.
	binURI := protocol.DocumentUri(pathToURI(filepath.Join(uriToPath(s.rootURI), "bin/main.esc")))
	newText := "val x = add(1, 2)\nval y = x"
	err := s.textDocumentDidChange(&glsp.Context{}, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: binURI},
			Version:                2,
		},
		ContentChanges: []any{
			protocol.TextDocumentContentChangeEventWhole{Text: newText},
		},
	})
	require.NoError(t, err)

	// Wait for the async validation goroutine to complete.
	// RLock is correct here because s.validated was created with
	// sync.NewCond(s.mu.RLocker()) — see NewServer in main.go.
	s.mu.RLock()
	for s.validatedVersion[binURI] < 2 {
		s.validated.Wait()
	}
	co2 := s.checkOutput
	s.mu.RUnlock()

	// The lib Module pointer should be the exact same object (not re-parsed).
	assert.Same(t, mod1, co2.Module, "lib module should be reused, not re-parsed")

	// The bin script should have been updated with the new source.
	srcID := s.sourceIDForURI(binURI)
	assert.NotNil(t, co2.Scripts[srcID], "bin script should be updated")

	// Completions should still work — 'add' from lib and 'x'/'y' from script.
	// Cursor on "add" (line 1, col 9) to check lib export is visible.
	labels := getCompletionLabelsAt(t, s, binURI, 1, 9)
	assert.Contains(t, labels, "add", "should still see lib export after incremental check")
	// Cursor on "x" (line 2, col 10) to check script bindings are visible.
	labels2 := getCompletionLabelsAt(t, s, binURI, 2, 10)
	assert.Contains(t, labels2, "x", "should see script binding after incremental check")
}

// --- Issue 8: workspaceExecuteCommand rejects closed documents ---

func TestWorkspaceExecuteCommand_RejectsClosedDocument(t *testing.T) {
	files := map[string]string{
		"bin/main.esc": "val x = 5",
	}
	s := integrationServer(t, files)
	// Do NOT open the file — it exists on disk but is not in s.documents.
	rootPath := uriToPath(s.rootURI)
	uri := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, "bin/main.esc")))

	_, err := s.workspaceExecuteCommand(&glsp.Context{}, &protocol.ExecuteCommandParams{
		Command:   "compile",
		Arguments: []any{uri},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "document not open")
}

// --- Diagnostics published from internal/solver ---

// publishedDiagnostics returns a context whose Notify forwards each
// publishDiagnostics notification to the returned channel. The server sends each
// notification from a goroutine of its own, so a test reads them from the channel
// rather than from a slice.
func publishedDiagnostics() (*glsp.Context, <-chan *protocol.PublishDiagnosticsParams) {
	published := make(chan *protocol.PublishDiagnosticsParams, 64)
	ctx := &glsp.Context{
		Notify: func(method string, params any) {
			if method != protocol.ServerTextDocumentPublishDiagnostics {
				return
			}
			published <- params.(*protocol.PublishDiagnosticsParams)
		},
	}
	return ctx, published
}

// awaitDiagnostics reads published notifications until one names uri at version,
// and returns its diagnostics rendered as "line:col-line:col message". Lines and
// columns are the LSP's, counted from 0.
func awaitDiagnostics(
	t *testing.T,
	published <-chan *protocol.PublishDiagnosticsParams,
	uri protocol.DocumentUri,
	version protocol.UInteger,
) []string {
	t.Helper()
	diags := awaitPublished(t, published, uri, version)
	rendered := make([]string, len(diags))
	for i, d := range diags {
		rendered[i] = fmt.Sprintf(
			"%d:%d-%d:%d %s",
			d.Range.Start.Line, d.Range.Start.Character,
			d.Range.End.Line, d.Range.End.Character,
			d.Message,
		)
	}
	return rendered
}

// awaitPublished reads published notifications until one names uri at version, and
// returns its diagnostics.
func awaitPublished(
	t *testing.T,
	published <-chan *protocol.PublishDiagnosticsParams,
	uri protocol.DocumentUri,
	version protocol.UInteger,
) []protocol.Diagnostic {
	t.Helper()
	// The first solver check in the process loads the standard library from disk,
	// which takes several seconds under the race detector.
	timeout := time.After(60 * time.Second)
	for {
		select {
		case params := <-published:
			if params.URI != uri || params.Version == nil || *params.Version != version {
				continue
			}
			return params.Diagnostics
		case <-timeout:
			t.Fatalf("no diagnostics published for %s at version %d", uri, version)
			return nil
		}
	}
}

func TestDiagnosticsPublishedFromSolver(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the test opens, which triggers a full package check.
		open string
		// change, when set, replaces the text of open after the full check. A bin/
		// file changed alone is re-checked against the cached library surface.
		change string
		// expected are the diagnostics published for open once the last check ends.
		expected []string
	}{
		{
			name: "BinTypeErrorFromFullCheck",
			files: map[string]string{
				"bin/main.esc": `val x: number = "hello"`,
			},
			open:     "bin/main.esc",
			expected: []string{`0:16-0:23 cannot constrain "hello" <: number`},
		},
		{
			// A pattern still being typed after its dot reports only parse errors. The
			// checker reads the arm as a catch-all, so it reports no unknown variant and
			// no missing branch.
			name: "PatternEndingInDot",
			files: map[string]string{
				"lib/index.esc": "export enum Lang {\n    English,\n    French,\n}\nexport fn f(l: Lang) -> string {\n    return match l {\n        Lang.English => \"en\",\n        Lang.\n    }\n}\n",
			},
			open: "lib/index.esc",
			expected: []string{
				"7:12-7:13 expected an identifier after .",
				"8:4-8:5 Expected => but got }",
				"9:0-9:1 Expected an expression",
			},
		},
		{
			// A half-typed arm before the others reports only its parse error.
			name: "PatternEndingInDotBeforeOtherArms",
			files: map[string]string{
				"lib/index.esc": "export enum Lang {\n    English,\n    French,\n}\nexport fn f(l: Lang) -> string {\n    return match l {\n        Lang. => \"x\",\n        Lang.English => \"en\",\n        Lang.French => \"fr\",\n    }\n}\n",
			},
			open:     "lib/index.esc",
			expected: []string{"6:12-6:13 expected an identifier after ."},
		},
		{
			name: "IfValPatternEndingInDot",
			files: map[string]string{
				"lib/index.esc": "export enum Lang {\n    English,\n    French,\n}\nexport fn f(l: Lang) -> boolean {\n    return if val Lang. = l { true } else { false }\n}\n",
			},
			open:     "lib/index.esc",
			expected: []string{"5:22-5:23 expected an identifier after ."},
		},
		{
			name: "LibTypeErrorFromFullCheck",
			files: map[string]string{
				"lib/math.esc": `export val x: number = "hello"`,
			},
			open:     "lib/math.esc",
			expected: []string{`0:23-0:30 cannot constrain "hello" <: number`},
		},
		{
			name: "BinTypeErrorFromIncrementalCheck",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { a + b }",
				"bin/main.esc": "val x = add(1, 2)",
			},
			open:     "bin/main.esc",
			change:   `val x: string = add(1, 2)`,
			expected: []string{`0:16-0:25 cannot constrain number <: string`},
		},
		{
			name: "NoErrorsPublishesEmptyList",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { a + b }",
				"bin/main.esc": "val x = add(1, 2)",
			},
			open:     "bin/main.esc",
			expected: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, tt.files)
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, tt.open, tt.files[tt.open])
			var version protocol.UInteger = 1
			var module *ast.Module
			if tt.change != "" {
				// The files a change case starts from are valid, and reading this
				// notification leaves the change's as the next one for this file.
				require.Empty(t, awaitDiagnostics(t, published, uri, 1))
				s.mu.RLock()
				module = s.checkOutput.Module
				s.mu.RUnlock()
				version = 2
				require.NoError(t, s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
					TextDocument: protocol.VersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
						Version:                protocol.Integer(version),
					},
					ContentChanges: []any{
						protocol.TextDocumentContentChangeEventWhole{Text: tt.change},
					},
				}))
			}

			require.Equal(t, tt.expected, awaitDiagnostics(t, published, uri, version))
			if tt.change != "" {
				// The re-check of a bin/ file alone keeps the parsed library, where a
				// full package check parses it again.
				s.mu.RLock()
				defer s.mu.RUnlock()
				require.Same(t, module, s.checkOutput.Module)
			}
		})
	}
}

// --- Hover and go-to-definition from internal/solver ---

func TestDefinitionFromSolver(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the request names. line and col are 0-based.
		open      string
		line, col protocol.UInteger
		// expected is the location returned, rendered as "path line:col-line:col", or
		// the error when the request fails.
		expected string
	}{
		{
			name: "BinReadsLibExport",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { return a + b }",
				"bin/main.esc": "val x = add(1, 2)",
			},
			open: "bin/main.esc", line: 0, col: 9,
			expected: "lib/math.esc 0:10-0:13",
		},
		{
			name: "Param",
			files: map[string]string{
				"bin/main.esc": "fn double(n: number) -> number { return n * 2 }",
			},
			open: "bin/main.esc", line: 0, col: 40,
			expected: "bin/main.esc 0:10-0:11",
		},
		{
			name: "DestructuredLeaf",
			files: map[string]string{
				"bin/main.esc": "val {a, b} = {a: 1, b: 2}\nval c = b",
			},
			open: "bin/main.esc", line: 1, col: 8,
			expected: "bin/main.esc 0:8-0:9",
		},
		{
			name: "MutVal",
			files: map[string]string{
				"bin/main.esc": "val mut x: number = 5\nval y = x",
			},
			open: "bin/main.esc", line: 1, col: 8,
			expected: "bin/main.esc 0:8-0:9",
		},
		{
			name: "ShorthandLeafWithDefault",
			files: map[string]string{
				"bin/main.esc": "val {a, b = 2}: {a: number, b?: number} = {a: 1}\nval c = b",
			},
			open: "bin/main.esc", line: 1, col: 8,
			expected: "bin/main.esc 0:8-0:9",
		},
		{
			name: "AssignmentTarget",
			files: map[string]string{
				"bin/main.esc": "var x = 1\nx = 2",
			},
			open: "bin/main.esc", line: 1, col: 0,
			expected: "bin/main.esc 0:4-0:5",
		},
		{
			name: "OverloadedCallee",
			files: map[string]string{
				"lib/f.esc": "export declare fn f(a: number) -> number\nexport declare fn f(a: string) -> string\nval r = f(1)",
			},
			open: "lib/f.esc", line: 2, col: 8,
			expected: "lib/f.esc 0:18-0:19",
		},
		{
			name: "OverloadedCalleeSecondArm",
			files: map[string]string{
				"lib/f.esc": "export declare fn f(a: number) -> number\nexport declare fn f(a: string) -> string\nval r = f(\"hello\")",
			},
			open: "lib/f.esc", line: 2, col: 8,
			expected: "lib/f.esc 1:18-1:19",
		},
		{
			name: "StdlibName",
			files: map[string]string{
				"bin/main.esc": `val n = parseInt("1")`,
			},
			open: "bin/main.esc", line: 0, col: 9,
			expected: "textDocument/definition: no declaration in the package for parseInt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, tt.files)
			uri := openFile(t, s, tt.open, tt.files[tt.open])

			result, err := s.textDocumentDefinition(&glsp.Context{}, &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri},
					Position:     protocol.Position{Line: tt.line, Character: tt.col},
				},
			})
			if err != nil {
				require.Equal(t, tt.expected, err.Error())
				return
			}
			loc, ok := result.(protocol.Location)
			require.True(t, ok, "expected protocol.Location, got %T", result)
			rel, err := filepath.Rel(uriToPath(s.rootURI), uriToPath(string(loc.URI)))
			require.NoError(t, err)
			require.Equal(t, tt.expected, fmt.Sprintf(
				"%s %d:%d-%d:%d",
				filepath.ToSlash(rel),
				loc.Range.Start.Line, loc.Range.Start.Character,
				loc.Range.End.Line, loc.Range.End.Character,
			))
		})
	}
}

// escalierCode returns the hover text that shows code as an Escalier code block.
func escalierCode(code string) string {
	return "```escalier\n" + code + "\n```"
}

func TestHoverFromSolver(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the request names. line and col are 0-based.
		open      string
		line, col protocol.UInteger
		// expected is the hover text, or "" when the request returns no hover.
		expected string
	}{
		{
			name:  "InferredVal",
			files: map[string]string{"bin/main.esc": "val x = 5"},
			open:  "bin/main.esc", line: 0, col: 4,
			expected: escalierCode("x: 5"),
		},
		{
			name:  "ValMutBinding",
			files: map[string]string{"bin/main.esc": "val mut p = {x: 1}"},
			open:  "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("mut p: {x: number}"),
		},
		{
			name:  "ValMutRead",
			files: map[string]string{"bin/main.esc": "val mut p = {x: 1}\nval n = p.x"},
			open:  "bin/main.esc", line: 1, col: 8,
			expected: escalierCode("mut p: {x: number}"),
		},
		{
			name:  "DestructuredMutShorthandReadInFunction",
			files: map[string]string{"lib/a.esc": "val {mut a, b} = {a: {x: 1}, b: {x: 2}}\nexport fn f() -> number { return a.x + b.x }"},
			open:  "lib/a.esc", line: 1, col: 33,
			expected: escalierCode("mut a: {x: number}"),
		},
		{
			name:  "DestructuredShorthandReadInFunction",
			files: map[string]string{"lib/a.esc": "val {mut a, b} = {a: {x: 1}, b: {x: 2}}\nexport fn f() -> number { return a.x + b.x }"},
			open:  "lib/a.esc", line: 1, col: 39,
			expected: escalierCode("b: {x: 2}"),
		},
		{
			name:  "DestructuredMutKeyValueReadInFunction",
			files: map[string]string{"lib/a.esc": "val {a: mut c} = {a: {x: 1}}\nexport fn f() -> number { return c.x }"},
			open:  "lib/a.esc", line: 1, col: 33,
			expected: escalierCode("mut c: {x: number}"),
		},
		{
			name:  "AnnotatedValMut",
			files: map[string]string{"bin/main.esc": "val mut p: {x: number} = {x: 1}"},
			open:  "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("mut p: {x: number}"),
		},
		{
			name: "MutParam",
			files: map[string]string{
				"lib/point.esc": "export fn f(mut p: {x: number}) -> number { return p.x }",
			},
			open: "lib/point.esc", line: 0, col: 16,
			expected: escalierCode("mut p: {x: number}"),
		},
		{
			name:  "DestructuredMutRead",
			files: map[string]string{"bin/main.esc": "val {mut a} = {a: {x: 1}}\nval n = a.x"},
			open:  "bin/main.esc", line: 1, col: 8,
			expected: escalierCode("mut a: {x: number}"),
		},
		{
			// A call moves its argument only into a concrete owned shape, so the `mut` on a
			// union parameter leaves the binding immutable, and the hover shows no `mut`.
			name: "MutUnionParam",
			files: map[string]string{
				"lib/point.esc": "export fn f(mut p: {x: number} | {y: number}) -> number { return 1 }",
			},
			open: "lib/point.esc", line: 0, col: 16,
			expected: escalierCode("p: {x: number} | {y: number}"),
		},
		{
			name: "MutParamRead",
			files: map[string]string{
				"lib/point.esc": "export fn f(mut p: {x: number}) -> number { return p.x }",
			},
			open: "lib/point.esc", line: 0, col: 51,
			expected: escalierCode("mut p: {x: number}"),
		},
		{
			name: "LibFuncReadFromBin",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { return a + b }",
				"bin/main.esc": "val x = add(1, 2)",
			},
			open: "bin/main.esc", line: 0, col: 9,
			expected: escalierCode("fn add(a: number, b: number) -> number"),
		},
		// The checker binds `self` with no declaration, so the hover shows its type alone.
		{
			name: "SelfBorrowRead",
			files: map[string]string{
				"lib/c.esc": "export class C { n: number, peek(&self) -> number { return self.n } }",
			},
			open: "lib/c.esc", line: 0, col: 59,
			expected: escalierCode("self: &C"),
		},
		{
			name: "LibParam",
			files: map[string]string{
				"lib/math.esc": "export fn add(a: number, b: number) -> number { return a + b }",
			},
			open: "lib/math.esc", line: 0, col: 55,
			expected: escalierCode("a: number"),
		},
		{
			name:  "NumberLiteral",
			files: map[string]string{"bin/main.esc": "val x = 5"},
			open:  "bin/main.esc", line: 0, col: 8,
		},
		{
			name:  "StringLiteralArgument",
			files: map[string]string{"bin/main.esc": "declare fn f(s: string) -> number\nval n = f(\"title\")"},
			open:  "bin/main.esc", line: 1, col: 11,
		},
		{
			name:  "TemplateString",
			files: map[string]string{"bin/main.esc": "val n = 1\nval s = `n = ${n}`"},
			open:  "bin/main.esc", line: 1, col: 9,
		},
		{
			name:  "LiteralPattern",
			files: map[string]string{"bin/main.esc": "val n: number = 1\nval s = match n { 0 => \"zero\", _ => \"other\" }"},
			open:  "bin/main.esc", line: 1, col: 18,
		},
		{
			name: "TypeAliasDecl",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}",
			},
			open: "lib/a.esc", line: 0, col: 13,
			expected: escalierCode("type Point = { x: number, y?: number }"),
		},
		{
			name: "TypeAliasKeyword",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}",
			},
			open: "lib/a.esc", line: 0, col: 8,
		},
		{
			name: "TypeAliasUseFromBin",
			files: map[string]string{
				"lib/a.esc":    "export type Point = {x: number, y?: number}",
				"bin/main.esc": "val p: Point = {x: 1}",
			},
			open: "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("type Point = { x: number, y?: number }"),
		},
		{
			name: "TypeAliasInIndexedAccess",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}\nexport type Y = Point[\"y\"]",
			},
			open: "lib/a.esc", line: 1, col: 17,
			expected: escalierCode("type Point = { x: number, y?: number }"),
		},
		{
			name: "IndexedAccessAliasDecl",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}\nexport type Y = Point[\"y\"]",
			},
			open: "lib/a.esc", line: 1, col: 12,
			expected: escalierCode("type Y = Point[\"y\"]"),
		},
		{
			name: "GenericAliasDecl",
			files: map[string]string{
				"lib/a.esc": "export type Unwrap<T> = if T : Promise<infer V> { Unwrap<V> } else { T }",
			},
			open: "lib/a.esc", line: 0, col: 13,
			expected: escalierCode("type Unwrap<T> = if T : Promise<infer V> { Unwrap<V> } else { T }"),
		},
		{
			name: "GenericAliasUse",
			files: map[string]string{
				"lib/a.esc": "export type Box<T> = {value: T}\nexport val b: Box<string> = {value: \"a\"}",
			},
			open: "lib/a.esc", line: 1, col: 14,
			expected: escalierCode("type Box<T> = { value: T }"),
		},
		{
			name: "BuiltinAlias",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}\nexport val p: Partial<Point> = {}",
			},
			open: "lib/a.esc", line: 1, col: 15,
			expected: escalierCode("type Partial<T> = { [P]+?: T[P] for P in keyof T }"),
		},
		{
			name: "AliasTypeParamDecl",
			files: map[string]string{
				"lib/a.esc": "export type Box<T> = {value: T}",
			},
			open: "lib/a.esc", line: 0, col: 16,
			expected: escalierCode("(type parameter) T"),
		},
		{
			name: "AliasTypeParamRef",
			files: map[string]string{
				"lib/a.esc": "export type Box<T> = {value: T}",
			},
			open: "lib/a.esc", line: 0, col: 29,
			expected: escalierCode("(type parameter) T"),
		},
		{
			name: "FuncTypeParamDecl",
			files: map[string]string{
				"lib/a.esc": "export fn id<T: number>(x: T) -> T { return x }",
			},
			open: "lib/a.esc", line: 0, col: 13,
			expected: escalierCode("(type parameter) T: number"),
		},
		{
			name: "FuncTypeParamRef",
			files: map[string]string{
				"lib/a.esc": "export fn id<T: number>(x: T) -> T { return x }",
			},
			open: "lib/a.esc", line: 0, col: 27,
			expected: escalierCode("(type parameter) T: number"),
		},
		{
			name: "TypeParamDefault",
			files: map[string]string{
				"lib/a.esc": "export type Box<T = string> = {value: T}",
			},
			open: "lib/a.esc", line: 0, col: 38,
			expected: escalierCode("(type parameter) T = string"),
		},
		{
			name: "InferDecl",
			files: map[string]string{
				"lib/a.esc": "export type Unwrap<T> = if T : Promise<infer V> { Unwrap<V> } else { T }",
			},
			open: "lib/a.esc", line: 0, col: 46,
			expected: escalierCode("(type parameter) V"),
		},
		{
			name: "InferRef",
			files: map[string]string{
				"lib/a.esc": "export type Unwrap<T> = if T : Promise<infer V> { Unwrap<V> } else { T }",
			},
			open: "lib/a.esc", line: 0, col: 58,
			expected: escalierCode("(type parameter) V"),
		},
		{
			name: "MappedTypeKey",
			files: map[string]string{
				"lib/a.esc": "export type Patch<T> = {[K]+?: T[K] for K in keyof T}",
			},
			open: "lib/a.esc", line: 0, col: 33,
			expected: escalierCode("(type parameter) K in keyof T"),
		},
		{
			name: "FuncDeclName",
			files: map[string]string{
				"lib/a.esc": "export fn add(a: number, b: number) -> number { return a + b }",
			},
			open: "lib/a.esc", line: 0, col: 10,
			expected: escalierCode("fn add(a: number, b: number) -> number"),
		},
		{
			name: "GenericFuncDeclName",
			files: map[string]string{
				"lib/a.esc": "export fn id<T: number>(x: T) -> T { return x }",
			},
			open: "lib/a.esc", line: 0, col: 10,
			expected: escalierCode("fn id<T: number>(x: T) -> T"),
		},
		{
			name: "OverloadedFuncReadFromBin",
			files: map[string]string{
				"lib/a.esc":    "export fn f(n: number) -> string { return \"n\" }\nexport fn f(s: string) -> string { return s }",
				"bin/main.esc": "val s = f(1)",
			},
			open: "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("fn f(n: number) -> string (+1 overload)"),
		},
		{
			name: "ClassDeclName",
			files: map[string]string{
				"lib/a.esc": "export class Dog { name: string, bark(self) -> string { return self.name } }",
			},
			open: "lib/a.esc", line: 0, col: 13,
			expected: escalierCode("class Dog"),
		},
		{
			name: "GenericClassDeclName",
			files: map[string]string{
				"lib/a.esc": "export class Box<T> { value: T }",
			},
			open: "lib/a.esc", line: 0, col: 13,
			expected: escalierCode("class Box<T>"),
		},
		{
			name: "ClassConstructorRead",
			files: map[string]string{
				"lib/a.esc":    "export class HttpError { status: number }",
				"bin/main.esc": "val e = HttpError(404)",
			},
			open: "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("constructor HttpError(status: number)"),
		},
		{
			name: "ClassRefInType",
			files: map[string]string{
				"lib/a.esc": "export class Dog { name: string, bark(self) -> string { return self.name } }\nexport fn f(d: Dog) -> string { return d.bark() }",
			},
			open: "lib/a.esc", line: 1, col: 17,
			expected: escalierCode("class Dog"),
		},
		{
			name: "EnumDeclName",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Neg(operand: Expr) }",
			},
			open: "lib/a.esc", line: 0, col: 12,
			expected: escalierCode("enum Expr"),
		},
		{
			name: "GenericEnumDeclName",
			files: map[string]string{
				"lib/a.esc": "export enum Result<T, E> { Ok(value: T), Err(error: E) }",
			},
			open: "lib/a.esc", line: 0, col: 12,
			expected: escalierCode("enum Result<T, E>"),
		},
		{
			name: "EnumRefInType",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Neg(operand: Expr) }\nexport fn show(e: Expr) -> number { return 1 }",
			},
			open: "lib/a.esc", line: 1, col: 18,
			expected: escalierCode("enum Expr"),
		},
		{
			name: "InterfaceDeclName",
			files: map[string]string{
				"lib/a.esc": "export interface Shape { area(self) -> number, name: string }",
			},
			open: "lib/a.esc", line: 0, col: 17,
			expected: escalierCode("interface Shape { area(self) -> number, name: string }"),
		},
		{
			name: "ConstructorWithFuncParam",
			files: map[string]string{
				"lib/a.esc":    "export class Timer { constructor(cb: fn () -> undefined) { } }",
				"bin/main.esc": "val t = Timer(fn () { return undefined })",
			},
			open: "bin/main.esc", line: 0, col: 8,
			expected: escalierCode("constructor Timer(cb: fn () -> undefined)"),
		},
		{
			name: "SelfMutBorrowRead",
			files: map[string]string{
				"lib/c.esc": "export class C { n: number, bump(&mut self) { self.n = self.n + 1 } }",
			},
			open: "lib/c.esc", line: 0, col: 46,
			expected: escalierCode("self: &mut C"),
		},
		{
			name: "PropertyReadOnClass",
			files: map[string]string{
				"lib/a.esc": "export class HttpError { status: number }\nexport fn f(e: &HttpError) -> number { return e.status }",
			},
			open: "lib/a.esc", line: 1, col: 48,
			expected: escalierCode("(property) HttpError.status: number"),
		},
		{
			name: "MethodReadOnClass",
			files: map[string]string{
				"lib/a.esc": "export class C { n: number, peek(self) -> number { return self.n } }\nexport fn g(c: &C) -> number { return c.peek() }",
			},
			open: "lib/a.esc", line: 1, col: 40,
			expected: escalierCode("(method) C.peek() -> number"),
		},
		{
			name: "GetterRead",
			files: map[string]string{
				"lib/a.esc": "export class Temp { celsius: number, get fahrenheit(self) -> number { return self.celsius } }\nexport fn u(t: &Temp) -> number { return t.fahrenheit }",
			},
			open: "lib/a.esc", line: 1, col: 43,
			expected: escalierCode("(getter) Temp.fahrenheit: number"),
		},
		{
			name: "PropertyReadOnObjectLiteral",
			files: map[string]string{
				"lib/a.esc": "export fn f(p: &{x: number}) -> number { return p.x }",
			},
			open: "lib/a.esc", line: 0, col: 50,
			expected: escalierCode("(property) x: number"),
		},
		{
			name: "ObjectLiteralPropertyKey",
			files: map[string]string{
				"lib/a.esc": "export val p = {volume: 7}",
			},
			open: "lib/a.esc", line: 0, col: 16,
			expected: escalierCode("(property) volume: 7"),
		},
		{
			name: "NamespaceRead",
			files: map[string]string{
				"lib/a.esc": "export fn f() -> number { return Math.floor(1.5) }",
			},
			open: "lib/a.esc", line: 0, col: 33,
			expected: escalierCode("namespace Math"),
		},
		{
			name: "EnumAsNamespaceRead",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Neg(operand: Expr) }\nexport val add = Expr.Num",
			},
			open: "lib/a.esc", line: 1, col: 17,
			expected: escalierCode("namespace Expr"),
		},
		{
			name: "EnumVariantRead",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Neg(operand: Expr) }\nexport val add = Expr.Num",
			},
			open: "lib/a.esc", line: 1, col: 22,
			expected: escalierCode("(variant) Expr.Num(value: number)"),
		},
		{
			name: "GenericEnumVariantRead",
			files: map[string]string{
				"lib/a.esc": "export enum Result<T, E> { Ok(value: T), Err(error: E) }\nexport val mk = Result.Ok",
			},
			open: "lib/a.esc", line: 1, col: 23,
			expected: escalierCode("(variant) Result.Ok(value: T)"),
		},
		{
			name: "ClassFieldDecl",
			files: map[string]string{
				"lib/a.esc": "export class Dog { name: string, bark(&self) -> string { return self.name } }",
			},
			open: "lib/a.esc", line: 0, col: 19,
			expected: escalierCode("(property) Dog.name: string"),
		},
		{
			name: "ClassMethodDecl",
			files: map[string]string{
				"lib/a.esc": "export class Dog { name: string, bark(&self) -> string { return self.name } }",
			},
			open: "lib/a.esc", line: 0, col: 33,
			expected: escalierCode("(method) Dog.bark() -> string"),
		},
		{
			name: "ClassGetterDecl",
			files: map[string]string{
				"lib/a.esc": "export class Temp { celsius: number, get fahrenheit(&self) -> number { return self.celsius } }",
			},
			open: "lib/a.esc", line: 0, col: 41,
			expected: escalierCode("(getter) Temp.fahrenheit: number"),
		},
		{
			name: "ObjectTypeProperty",
			files: map[string]string{
				"lib/a.esc": "export type Pt = {lat: number, lng: number}",
			},
			open: "lib/a.esc", line: 0, col: 18,
			expected: escalierCode("(property) lat: number"),
		},
		{
			// A static field's initializer sits directly under the class, so a read in it
			// shows the value it reads rather than a member of the class.
			name: "StaticFieldInitializerRead",
			files: map[string]string{
				"lib/a.esc": "val count = 1\nexport class C { count: number, static total: number = count }",
			},
			open: "lib/a.esc", line: 1, col: 55,
			expected: escalierCode("count: 1"),
		},
		{
			// A getter and a setter share a name, so the declaration resolves by the kind of
			// member written as well as its name.
			name: "ClassSetterDecl",
			files: map[string]string{
				"lib/a.esc": "export class T { n: number, get x(&self) -> number { return self.n }, set x(&mut self, v: number) { self.n = v } }",
			},
			open: "lib/a.esc", line: 0, col: 74,
			expected: escalierCode("(setter) T.x: number"),
		},
		{
			name: "InferRefInExtends",
			files: map[string]string{
				"lib/a.esc": "export type Same<T> = if T : [infer A, A] { A } else { never }",
			},
			open: "lib/a.esc", line: 0, col: 39,
			expected: escalierCode("(type parameter) A"),
		},
		{
			name: "RegexCaptureRef",
			files: map[string]string{
				"lib/a.esc": "export type Strip<T> = if T : /^_*(?<Ident>[a-zA-Z]+)_*$/ { Ident } else { T }",
			},
			open: "lib/a.esc", line: 0, col: 60,
			expected: escalierCode("(type parameter) Ident"),
		},
		{
			name: "ReadonlyPropertyReadOnClass",
			files: map[string]string{
				"lib/a.esc": "export class Ticket { readonly id: number }\nexport fn f(t: &Ticket) -> number { return t.id }",
			},
			open: "lib/a.esc", line: 1, col: 45,
			expected: escalierCode("(property) readonly Ticket.id: number"),
		},
		{
			name: "ReadonlyPropertyReadOnObjectType",
			files: map[string]string{
				"lib/a.esc": "export fn f(t: &{readonly id: number}) -> number { return t.id }",
			},
			open: "lib/a.esc", line: 0, col: 60,
			expected: escalierCode("(property) readonly id: number"),
		},
		{
			name: "OptionalPropertyRead",
			files: map[string]string{
				"lib/a.esc": "export fn f(p: &{x: number, y?: number}) -> number | undefined { return p.y }",
			},
			open: "lib/a.esc", line: 0, col: 74,
			expected: escalierCode("(property) y?: number"),
		},
		{
			name: "ReadonlyClassFieldDecl",
			files: map[string]string{
				"lib/a.esc": "export class Ticket { readonly id: number }",
			},
			open: "lib/a.esc", line: 0, col: 31,
			expected: escalierCode("(property) readonly Ticket.id: number"),
		},
		{
			name: "ReadonlyObjectTypeProperty",
			files: map[string]string{
				"lib/a.esc": "export type Task = {readonly id: number, title: string}",
			},
			open: "lib/a.esc", line: 0, col: 29,
			expected: escalierCode("(property) readonly id: number"),
		},
		{
			name: "OptionalObjectTypeProperty",
			files: map[string]string{
				"lib/a.esc": "export type Point = {x: number, y?: number}",
			},
			open: "lib/a.esc", line: 0, col: 32,
			expected: escalierCode("(property) y?: number"),
		},
		{
			name: "StaticFieldRead",
			files: map[string]string{
				"lib/a.esc": "export class C { static max: number = 5, static make() -> number { return 1 } }\nexport val m = C.max",
			},
			open: "lib/a.esc", line: 1, col: 17,
			expected: escalierCode("(property) C.max: number"),
		},
		{
			name: "StaticMethodRead",
			files: map[string]string{
				"lib/a.esc": "export class C { static max: number = 5, static make() -> number { return 1 } }\nexport val k = C.make()",
			},
			open: "lib/a.esc", line: 1, col: 17,
			expected: escalierCode("(method) C.make() -> number"),
		},
		{
			name: "OptionalMethodRead",
			files: map[string]string{
				"lib/a.esc": "export fn f(s: &{describe?(self) -> string}) -> number { val d = s.describe\n return 1 }",
			},
			open: "lib/a.esc", line: 0, col: 67,
			expected: escalierCode("(method) describe?() -> string"),
		},
		{
			name: "EnumVariantDecl",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Add(left: Expr, right: Expr) }",
			},
			open: "lib/a.esc", line: 0, col: 19,
			expected: escalierCode("(variant) Expr.Num(value: number)"),
		},
		{
			name: "EnumVariantDeclTwoParams",
			files: map[string]string{
				"lib/a.esc": "export enum Expr { Num(value: number), Add(left: Expr, right: Expr) }",
			},
			open: "lib/a.esc", line: 0, col: 39,
			expected: escalierCode("(variant) Expr.Add(left: Expr, right: Expr)"),
		},
		{
			name: "GenericEnumVariantDecl",
			files: map[string]string{
				"lib/a.esc": "export enum Result<T, E> { Ok(value: T), Err(error: E) }",
			},
			open: "lib/a.esc", line: 0, col: 41,
			expected: escalierCode("(variant) Result.Err(error: E)"),
		},
		{
			name: "StaticFieldDecl",
			files: map[string]string{
				"lib/a.esc": "export class C { static max: number = 5, static make() -> number { return 1 } }",
			},
			open: "lib/a.esc", line: 0, col: 24,
			expected: escalierCode("(property) C.max: number"),
		},
		{
			name: "StaticMethodDecl",
			files: map[string]string{
				"lib/a.esc": "export class C { static max: number = 5, static make() -> number { return 1 } }",
			},
			open: "lib/a.esc", line: 0, col: 48,
			expected: escalierCode("(method) C.make() -> number"),
		},
		{
			name: "InterfaceMethodDecl",
			files: map[string]string{
				"lib/a.esc": "export interface Shape { area(self) -> number, describe?(self) -> string, get label(self) -> string }",
			},
			open: "lib/a.esc", line: 0, col: 25,
			expected: escalierCode("(method) area() -> number"),
		},
		{
			name: "InterfaceOptionalMethodDecl",
			files: map[string]string{
				"lib/a.esc": "export interface Shape { area(self) -> number, describe?(self) -> string, get label(self) -> string }",
			},
			open: "lib/a.esc", line: 0, col: 47,
			expected: escalierCode("(method) describe?() -> string"),
		},
		{
			name: "InterfaceGetterDecl",
			files: map[string]string{
				"lib/a.esc": "export interface Shape { area(self) -> number, describe?(self) -> string, get label(self) -> string }",
			},
			open: "lib/a.esc", line: 0, col: 78,
			expected: escalierCode("(getter) label: string"),
		},
		{
			name: "ObjectTypeMethodDecl",
			files: map[string]string{
				"lib/a.esc": "export type S = {area(self) -> number}",
			},
			open: "lib/a.esc", line: 0, col: 17,
			expected: escalierCode("(method) area() -> number"),
		},
		{
			name:  "Keyword",
			files: map[string]string{"bin/main.esc": "val x = 5"},
			open:  "bin/main.esc", line: 0, col: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, tt.files)
			uri := openFile(t, s, tt.open, tt.files[tt.open])

			hover, err := s.textDocumentHover(&glsp.Context{}, &protocol.HoverParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri},
					Position:     protocol.Position{Line: tt.line, Character: tt.col},
				},
			})
			require.NoError(t, err)
			if tt.expected == "" {
				require.Nil(t, hover)
				return
			}
			require.NotNil(t, hover)
			content, ok := hover.Contents.(protocol.MarkupContent)
			require.True(t, ok)
			require.Equal(t, protocol.MarkupKindMarkdown, content.Kind)
			require.Equal(t, tt.expected, content.Value)
		})
	}
}

// --- Quick fix that writes a missing borrow ---

// insertAt returns an edit that inserts text at a 0-based line and column.
func insertAt(line, col protocol.UInteger, text string) protocol.TextEdit {
	pos := protocol.Position{Line: line, Character: col}
	return protocol.TextEdit{Range: protocol.Range{Start: pos, End: pos}, NewText: text}
}

// applyEdits returns src with edits applied. The edits must not overlap.
func applyEdits(src string, edits []protocol.TextEdit) string {
	lineMap := ast.NewLineMap(src)
	sorted := slices.Clone(edits)
	// Applying the last edit first keeps the offsets of the earlier ones valid.
	slices.SortFunc(sorted, func(a, b protocol.TextEdit) int {
		return posToLoc(lineMap, b.Range.Start).Offset - posToLoc(lineMap, a.Range.Start).Offset
	})
	for _, edit := range sorted {
		start := posToLoc(lineMap, edit.Range.Start).Offset
		end := posToLoc(lineMap, edit.Range.End).Offset
		src = src[:start] + edit.NewText + src[end:]
	}
	return src
}

func TestBorrowQuickFix(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// message is the message of the one diagnostic published for src.
		message string
		// data is the data the diagnostic is published with.
		data borrowFixData
		// title and edits describe the quick fix offered for that diagnostic.
		title string
		edits []protocol.TextEdit
	}{
		{
			name:    "ImmutableBorrowOfAFieldRead",
			message: "this value is borrowed by its destination, so write the borrow: `&`",
			src:     "fn g(h: &{f: {x: number}}) -> &{x: number} {\n\treturn h.f\n}",
			data: borrowFixData{
				Borrow: "&",
				Range: protocol.Range{
					Start: protocol.Position{Line: 1, Character: 8},
					End:   protocol.Position{Line: 1, Character: 11},
				},
			},
			title: "Write the borrow &",
			edits: []protocol.TextEdit{insertAt(1, 8, "&")},
		},
		{
			name:    "MutableBorrowOfAnArgument",
			message: "this argument is borrowed by the callee, so write the borrow: `&mut`",
			src:     "fn write(p: &mut {v: number}) -> undefined {}\nfn g() -> undefined {\n\tval mut x = {v: 1}\n\twrite(x)\n}",
			data: borrowFixData{
				Borrow: "&mut",
				Range: protocol.Range{
					Start: protocol.Position{Line: 3, Character: 7},
					End:   protocol.Position{Line: 3, Character: 8},
				},
			},
			title: "Write the borrow &mut",
			edits: []protocol.TextEdit{insertAt(3, 7, "&mut ")},
		},
		{
			name:    "LiteralIsParenthesized",
			message: "this argument is borrowed by the callee, so write the borrow: `&`",
			src:     "fn read(p: &{v: number}) -> undefined {}\nfn g() -> undefined {\n\tread({v: 1})\n}",
			data: borrowFixData{
				Borrow: "&",
				Range: protocol.Range{
					Start: protocol.Position{Line: 2, Character: 6},
					End:   protocol.Position{Line: 2, Character: 12},
				},
				Parenthesize: true,
			},
			title: "Write the borrow &",
			edits: []protocol.TextEdit{insertAt(2, 6, "&("), insertAt(2, 12, ")")},
		},
		{
			// The span of `(h).f` starts inside the parentheses, and the borrow goes before them.
			name:    "ParenthesizedReceiver",
			message: "this argument is borrowed by the callee, so write the borrow: `&`",
			src:     "fn read(p: &{v: number}) -> undefined {}\nfn g(h: &{f: {v: number}}) -> undefined {\n\tread((h).f)\n}",
			data: borrowFixData{
				Borrow: "&",
				Range: protocol.Range{
					Start: protocol.Position{Line: 2, Character: 6},
					End:   protocol.Position{Line: 2, Character: 11},
				},
			},
			title: "Write the borrow &",
			edits: []protocol.TextEdit{insertAt(2, 6, "&")},
		},
		{
			name:    "ShorthandProperty",
			message: "this value is borrowed by its destination, so write the borrow: `&`",
			src:     "fn g(f: {x: number}) -> undefined {\n\tval o: {f: &{x: number}} = {f}\n}",
			data: borrowFixData{
				Borrow: "&",
				Range: protocol.Range{
					Start: protocol.Position{Line: 1, Character: 29},
					End:   protocol.Position{Line: 1, Character: 30},
				},
				Key: "f",
			},
			title: "Write the borrow &",
			edits: []protocol.TextEdit{insertAt(1, 29, "f: &")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, map[string]string{"bin/main.esc": tt.src})
			ctx, published := publishedDiagnostics()
			uri := openFileIn(t, s, ctx, "bin/main.esc", tt.src)
			diags := awaitPublished(t, published, uri, 1)
			require.Len(t, diags, 1)
			require.Equal(t, tt.message, diags[0].Message)
			require.Equal(t, tt.data, diags[0].Data)

			// A client sends the diagnostics back as JSON, which decodes the data into a map.
			raw, err := json.Marshal(diags)
			require.NoError(t, err)
			var sent []protocol.Diagnostic
			require.NoError(t, json.Unmarshal(raw, &sent))

			result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri},
				Range:        tt.data.Range,
				Context: protocol.CodeActionContext{
					Diagnostics: sent,
					Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
				},
			})
			require.NoError(t, err)
			actions, ok := result.([]protocol.CodeAction)
			require.True(t, ok)
			require.Len(t, actions, 1)
			fix := actions[0]
			require.Equal(t, tt.title, fix.Title)
			require.Equal(t, addr(protocol.CodeActionKindQuickFix), fix.Kind)
			require.Equal(t, addr(true), fix.IsPreferred)
			require.Equal(t, sent, fix.Diagnostics)
			require.Equal(t, &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{uri: tt.edits},
			}, fix.Edit)

			// The fixed source checks clean.
			require.NoError(t, s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					Version:                2,
				},
				ContentChanges: []any{
					protocol.TextDocumentContentChangeEventWhole{Text: applyEdits(tt.src, tt.edits)},
				},
			}))
			require.Empty(t, awaitDiagnostics(t, published, uri, 2))
		})
	}
}

// TestBorrowQuickFixSkipsOptionalChain asserts that an optional chain filling a borrow
// destination gets no quick fix, because `&h?.f` is not a borrow the checker accepts.
func TestBorrowQuickFixSkipsOptionalChain(t *testing.T) {
	src := "fn g(h: &{f: {x: number}} | undefined) -> undefined {\n\tval d = h?.f\n}"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published := publishedDiagnostics()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	diags := awaitPublished(t, published, uri, 1)
	require.Len(t, diags, 1)
	require.Equal(t,
		"this optional chain is borrowed by its destination, and an optional chain cannot be borrowed, "+
			"so bind the receiver with `if val` and borrow the field through it",
		diags[0].Message)
	require.Nil(t, diags[0].Data)

	result, err := s.textDocumentCodeAction(&glsp.Context{}, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        diags[0].Range,
		Context:      protocol.CodeActionContext{Diagnostics: diags},
	})
	require.NoError(t, err)
	actions, ok := result.([]protocol.CodeAction)
	require.True(t, ok)
	require.Len(t, actions, 1)
	require.Equal(t, "Compile", actions[0].Title)
}

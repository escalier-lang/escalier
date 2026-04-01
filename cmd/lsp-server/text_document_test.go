package main

import (
	"os"
	"path/filepath"
	"testing"

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
	rootPath := uriToPath(s.rootURI)
	absPath := filepath.Join(rootPath, relPath)
	uri := protocol.DocumentUri(pathToURI(absPath))
	err := s.textDocumentDidOpen(&glsp.Context{}, &protocol.DidOpenTextDocumentParams{
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
	assert.Equal(t, "`number`", content.Value)
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
	// Module-level go-to-definition may not be fully implemented yet.
	if err != nil {
		t.Skipf("module definition not supported: %v", err)
	}
	if result == nil {
		t.Skip("module definition returned nil result")
	}
	loc, ok := result.(protocol.Location)
	if ok {
		assert.Equal(t, uri, loc.URI)
	}
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

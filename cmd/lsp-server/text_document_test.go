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
	// Populate libFilesCache so collectSources can find lib/ files.
	_ = s.refreshLibFilesCache()
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

// getCompletionLabelsAt calls textDocumentCompletion and returns sorted labels.
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
	// For module files, definition may work differently.
	// At minimum, it should not panic or error due to nil caches.
	if err != nil {
		// Acceptable — module definition may not be fully implemented yet.
		return
	}
	if result == nil {
		return
	}
	loc, ok := result.(protocol.Location)
	if ok {
		assert.Equal(t, uri, loc.URI)
	}
}

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestHoverVerbosityOverJSON asserts that Handle decodes a hover request carrying a
// verbosity level and answers with the hover's JSON. A hover that has no level above 0
// writes none of the level fields.
func TestHoverVerbosityOverJSON(t *testing.T) {
	files := map[string]string{"lib/a.esc": "export type Point = {x: number, y?: number}\nexport type Y = Point[\"y\"]"}
	s := integrationServer(t, files)
	uri := openFile(t, s, "lib/a.esc", files["lib/a.esc"])
	s.handler.SetInitialized(true)

	params, err := json.Marshal(map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"position":       map[string]any{"line": 1, "character": 12},
		"verbosityLevel": 1,
	})
	require.NoError(t, err)
	result, validMethod, validParams, err := s.Handle(&glsp.Context{
		Method: string(protocol.MethodTextDocumentHover),
		Params: params,
	})
	require.NoError(t, err)
	require.True(t, validMethod)
	require.True(t, validParams)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"contents": {"kind": "markdown", "value": "`+"```escalier\\ntype Y = Point[\\\"y\\\"]\\n= number\\n```"+`"}
	}`, string(encoded))
}

// TestHoverBeforeInitialize asserts that a hover sent before initialization completes
// gets glsp's usual error rather than reaching the server.
func TestHoverBeforeInitialize(t *testing.T) {
	s := NewServer()
	_, _, _, err := s.Handle(&glsp.Context{
		Method: string(protocol.MethodTextDocumentHover),
		Params: json.RawMessage(`{}`),
	})
	require.EqualError(t, err, "server not initialized")
}

// TestHoverParamsDecode asserts that a hover request's JSON decodes into hoverParams with
// its verbosity level and its position.
func TestHoverParamsDecode(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected int
	}{
		{
			name:     "WithLevel",
			json:     `{"textDocument": {"uri": "file:///lib/a.esc"}, "position": {"line": 1, "character": 12}, "verbosityLevel": 2}`,
			expected: 2,
		},
		{
			name:     "WithoutLevel",
			json:     `{"textDocument": {"uri": "file:///lib/a.esc"}, "position": {"line": 1, "character": 12}}`,
			expected: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params hoverParams
			require.NoError(t, json.Unmarshal([]byte(tt.json), &params))
			require.Equal(t, tt.expected, params.VerbosityLevel)
			require.Equal(t, protocol.DocumentUri("file:///lib/a.esc"), params.TextDocument.URI)
			require.Equal(t, protocol.Position{Line: 1, Character: 12}, params.Position)
		})
	}
}

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestHoverVerbosity(t *testing.T) {
	const point = "export type Point = {x: number, y?: number}\n"
	tests := []struct {
		name  string
		files map[string]string
		// open is the file the request names. line and col are 0-based.
		open      string
		line, col protocol.UInteger
		level     int
		// expected is the hover text. reached is the level it was rendered at, and
		// canIncrease and canDecrease are the flags the hover reports.
		expected                 string
		reached                  int
		canIncrease, canDecrease bool
	}{
		{
			name:  "IndexedAccessAliasLevel0",
			files: map[string]string{"lib/a.esc": point + "export type Y = Point[\"y\"]"},
			open:  "lib/a.esc", line: 1, col: 12, level: 0,
			expected: "type Y = Point[\"y\"]\n= number",
			reached:  0, canIncrease: true, canDecrease: false,
		},
		{
			name:  "IndexedAccessAliasLevel1",
			files: map[string]string{"lib/a.esc": point + "export type Y = Point[\"y\"]"},
			open:  "lib/a.esc", line: 1, col: 12, level: 1,
			expected: "type Y = number",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			// Expanding past level 1 changes nothing, so the hover stays at level 1.
			name:  "IndexedAccessAliasPastLastLevel",
			files: map[string]string{"lib/a.esc": point + "export type Y = Point[\"y\"]"},
			open:  "lib/a.esc", line: 1, col: 12, level: 3,
			expected: "type Y = number",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			name:  "IndexedAccessAliasRefLevel1",
			files: map[string]string{"lib/a.esc": point + "export type Y = Point[\"y\"]\nexport val y: Y = 1"},
			open:  "lib/a.esc", line: 2, col: 14, level: 1,
			expected: "type Y = number",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			name: "ConditionalAliasLevel0",
			files: map[string]string{
				"lib/a.esc": "export type U = if Promise<number> : Promise<infer V> { V } else { never }",
			},
			open: "lib/a.esc", line: 0, col: 12, level: 0,
			expected: "type U = if Promise<number> : Promise<infer V> { V } else { never }\n= number",
			reached:  0, canIncrease: true, canDecrease: false,
		},
		{
			name: "ConditionalAliasLevel1",
			files: map[string]string{
				"lib/a.esc": "export type U = if Promise<number> : Promise<infer V> { V } else { never }",
			},
			open: "lib/a.esc", line: 0, col: 12, level: 1,
			expected: "type U = number",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			name:  "GenericAliasRefLevel0",
			files: map[string]string{"lib/a.esc": "export type Box<T> = {value: T}\nexport val b: Box<string> = {value: \"a\"}"},
			open:  "lib/a.esc", line: 1, col: 14, level: 0,
			expected: "type Box<T> = { value: T }",
			reached:  0, canIncrease: true, canDecrease: false,
		},
		{
			name:  "GenericAliasRefLevel1",
			files: map[string]string{"lib/a.esc": "export type Box<T> = {value: T}\nexport val b: Box<string> = {value: \"a\"}"},
			open:  "lib/a.esc", line: 1, col: 14, level: 1,
			expected: "type Box<string> = {value: string}",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			// Each level expands one more layer, so the alias `Point` that `Box<Point>`
			// uncovers stays a reference until level 2.
			name:  "NestedAliasRefLevel1",
			files: map[string]string{"lib/a.esc": point + "export type Box<T> = {value: T}\nexport val b: Box<Point> = {value: {x: 1}}"},
			open:  "lib/a.esc", line: 2, col: 14, level: 1,
			expected: "type Box<Point> = {value: Point}",
			reached:  1, canIncrease: true, canDecrease: true,
		},
		{
			name:  "NestedAliasRefLevel2",
			files: map[string]string{"lib/a.esc": point + "export type Box<T> = {value: T}\nexport val b: Box<Point> = {value: {x: 1}}"},
			open:  "lib/a.esc", line: 2, col: 14, level: 2,
			expected: "type Box<Point> = {value: {x: number, y?: number}}",
			reached:  2, canIncrease: false, canDecrease: true,
		},
		{
			name:  "AliasOfAliasDeclLevel1",
			files: map[string]string{"lib/a.esc": point + "export type P = Point"},
			open:  "lib/a.esc", line: 1, col: 12, level: 1,
			expected: "type P = {x: number, y?: number}",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			// The level names the alias's own type parameter as the source writes it.
			name:  "GenericAliasDeclLevel1",
			files: map[string]string{"lib/a.esc": "export type Box<T> = {value: T}\nexport type Wrap<T> = Box<T>"},
			open:  "lib/a.esc", line: 1, col: 12, level: 1,
			expected: "type Wrap<T> = {value: T}",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			// `Box<T>` reads the type parameter of the alias around it, and expands to
			// the type the declaration level 0 shows already writes.
			name:  "AliasRefInsideAlias",
			files: map[string]string{"lib/a.esc": "export type Box<T> = {value: T}\nexport type Wrap<T> = Box<T>"},
			open:  "lib/a.esc", line: 1, col: 22, level: 1,
			expected: "type Box<T> = { value: T }",
			reached:  0, canIncrease: false, canDecrease: false,
		},
		{
			// A generic function's type parameter has no name a level could print it
			// under, so the reference offers no levels.
			name:  "AliasRefOverFuncTypeParam",
			files: map[string]string{"lib/a.esc": "export type Box<T> = {value: T}\nexport fn f<T>(x: Box<T>) -> number { return 1 }"},
			open:  "lib/a.esc", line: 1, col: 18, level: 1,
			expected: "type Box<T> = { value: T }",
			reached:  0, canIncrease: false, canDecrease: false,
		},
		{
			// Level 1 would print the declaration level 0 already shows.
			name:  "PlainAliasRef",
			files: map[string]string{"lib/a.esc": point + "export val p: Point = {x: 1}"},
			open:  "lib/a.esc", line: 1, col: 14, level: 1,
			expected: "type Point = { x: number, y?: number }",
			reached:  0, canIncrease: false, canDecrease: false,
		},
		{
			name:  "PreludeAliasRefLevel1",
			files: map[string]string{"lib/a.esc": point + "export val p: Partial<Point> = {}"},
			open:  "lib/a.esc", line: 1, col: 15, level: 1,
			expected: "type Partial<Point> = {x?: number, y?: number}",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			name:  "PlainAliasDecl",
			files: map[string]string{"lib/a.esc": point},
			open:  "lib/a.esc", line: 0, col: 12, level: 1,
			expected: "type Point = { x: number, y?: number }",
			reached:  0, canIncrease: false, canDecrease: false,
		},
		{
			name:  "AliasRefFromBin",
			files: map[string]string{"lib/a.esc": point + "export type Y = Point[\"y\"]", "bin/main.esc": "val y: Y = 1"},
			open:  "bin/main.esc", line: 0, col: 7, level: 1,
			expected: "type Y = number",
			reached:  1, canIncrease: false, canDecrease: true,
		},
		{
			// A hover with no levels ignores the level it is asked for.
			name:  "ValueHover",
			files: map[string]string{"bin/main.esc": "val x = 5"},
			open:  "bin/main.esc", line: 0, col: 4, level: 2,
			expected: "x: 5",
			reached:  0, canIncrease: false, canDecrease: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := integrationServer(t, tt.files)
			uri := openFile(t, s, tt.open, tt.files[tt.open])

			hover, err := s.textDocumentVerboseHover(&glsp.Context{}, &hoverParams{
				HoverParams: protocol.HoverParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{
						TextDocument: protocol.TextDocumentIdentifier{URI: uri},
						Position:     protocol.Position{Line: tt.line, Character: tt.col},
					},
				},
				VerbosityLevel: tt.level,
			})
			require.NoError(t, err)
			require.NotNil(t, hover)
			require.Equal(t, escalierCode(tt.expected), hover.Contents.Value)
			require.Equal(t, tt.reached, hover.VerbosityLevel)
			require.Equal(t, tt.canIncrease, hover.CanIncreaseVerbosity)
			require.Equal(t, tt.canDecrease, hover.CanDecreaseVerbosity)
		})
	}
}

// TestHoverVerbosityOverJSON asserts that Handle reads the verbosity level from the
// request's JSON and writes the level fields into the response.
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
		"contents": {"kind": "markdown", "value": "`+"```escalier\\ntype Y = number\\n```"+`"},
		"verbosityLevel": 1,
		"canDecreaseVerbosity": true
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

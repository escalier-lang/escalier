package printer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrintDeclDoc_RoundTrip parses a declaration carrying a JSDoc block and
// prints it back, asserting the doc survives the trip. A doc that reaches the
// AST but not the printed output would be silently dropped by every caller
// that reprints a declaration.
func TestPrintDeclDoc_RoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
	}{
		{"VarDecl", "/** the answer */\nval x = 42"},
		{"FuncDecl", "/** greets */\ndeclare fn greet() -> string"},
		{"TypeDecl", "/** an alias */\ntype Answer = number"},
		{"InterfaceDecl", "/** a point */\ninterface Point {\n    x: number\n}"},
		{"ClassDecl", "/** a point */\ndeclare class Point {\n    x: number\n}"},
		{
			name: "multi-line doc",
			src:  "/**\n * the answer\n *\n * @returns nothing\n */\nval x = 42",
		},
		{
			name: "doc on an exported decl",
			src:  "/** exported */\nexport val x = 42",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decl := parseOneDecl(t, tc.src)
			out, err := Print(decl, DefaultOptions())
			require.NoError(t, err)
			require.Equal(t, tc.src, out)
		})
	}
}

// TestPrintDeclDoc_ReindentsContinuationLines checks that a doc written at one
// indent level is re-emitted at the printer's own. The parser keeps the
// comment verbatim, so the continuation lines arrive carrying the source's
// leading whitespace.
func TestPrintDeclDoc_ReindentsContinuationLines(t *testing.T) {
	t.Parallel()
	decl := parseOneDecl(t, "        /**\n"+
		"         * the answer\n"+
		"         */\n"+
		"        val x = 42")
	out, err := Print(decl, DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "/**\n * the answer\n */\nval x = 42", out)
}

// TestPrintDeclDoc_CompactModeOmitsTheDoc checks that a one-line rendering
// leaves the doc out. A `//`-free doc still ends with `*/`, but emitting it
// compact would put the declaration inside the comment on some inputs, and
// compact output is meant to be read rather than reparsed.
func TestPrintDeclDoc_CompactModeOmitsTheDoc(t *testing.T) {
	t.Parallel()
	decl := parseOneDecl(t, "/** the answer */\nval x = 42")
	out, err := Print(decl, CompactOptions())
	require.NoError(t, err)
	require.Equal(t, "val x = 42", out)
}

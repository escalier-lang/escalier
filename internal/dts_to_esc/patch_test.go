package dts_to_esc

import (
	"slices"
	"strings"
	"testing"

	"github.com/aymanbagabas/go-udiff"
	"github.com/stretchr/testify/require"
)

// twelveDecls is a committed file long enough that two changes at
// opposite ends fall outside each other's context window.
const twelveDecls = `declare fn one() -> number
declare fn two() -> number
declare fn three() -> number
declare fn four() -> number
declare fn five() -> number
declare fn six() -> number
declare fn seven() -> number
declare fn eight() -> number
declare fn nine() -> number
declare fn ten() -> number
declare fn eleven() -> number
declare fn twelve() -> number
`

// after returns the offset just past needle in contents, which is where
// the write pass puts a member it appends to the line needle ends.
func after(t *testing.T, contents, needle string) int {
	t.Helper()
	i := strings.Index(contents, needle)
	require.NotEqual(t, -1, i, "%q is not in the test input", needle)
	return i + len(needle)
}

// diffCase is one committed file, the insertions a re-run would make
// into it, and the patch those render as.
type diffCase struct {
	name     string
	exists   bool
	contents string
	edits    []textEdit
	want     string
}

// diffCases are the shapes an additive re-run produces: a file that is
// not on disk yet, a member spliced into a body, a declaration appended
// past the last line, and two edits far enough apart to take a hunk
// each.
func diffCases(t *testing.T) []diffCase {
	t.Helper()

	const oneClass = "declare class Array {\n    length: number\n}\n"
	const commaClass = "declare class Array {\n    length: number,\n}\n"
	const oneDecl = "declare fn one() -> number"

	return []diffCase{
		{
			name:     "no edits",
			exists:   true,
			contents: twelveDecls,
			want:     "",
		},
		{
			name:   "a file that is not on disk yet",
			exists: false,
			edits:  []textEdit{{at: 0, text: oneDecl + "\n"}},
			want: `--- /dev/null
+++ b/std/array.esc
@@ -0,0 +1 @@
+declare fn one() -> number
`,
		},
		{
			name:     "a member spliced into a body",
			exists:   true,
			contents: oneClass,
			edits: []textEdit{{
				at:   after(t, oneClass, "    length: number"),
				text: ",\n    at(self, index: number) -> number",
			}},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -1,3 +1,4 @@
 declare class Array {
-    length: number
+    length: number,
+    at(self, index: number) -> number
 }
`,
		},
		{
			name:     "a member spliced in after a line the edit leaves whole",
			exists:   true,
			contents: commaClass,
			edits: []textEdit{{
				at:   after(t, commaClass, "    length: number,"),
				text: "\n    at(self, index: number) -> number,",
			}},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -1,3 +1,4 @@
 declare class Array {
     length: number,
+    at(self, index: number) -> number,
 }
`,
		},
		{
			name:     "a declaration appended past the last line",
			exists:   true,
			contents: twelveDecls,
			edits: []textEdit{{
				at:   len(twelveDecls),
				text: "\ndeclare fn thirteen() -> number\n",
			}},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -10,3 +10,5 @@
 declare fn ten() -> number
 declare fn eleven() -> number
 declare fn twelve() -> number
+
+declare fn thirteen() -> number
`,
		},
		{
			name:     "an append to a file with no trailing newline",
			exists:   true,
			contents: oneDecl,
			edits: []textEdit{{
				at:   len(oneDecl),
				text: "\n\ndeclare fn two() -> number\n",
			}},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -1 +1,3 @@
-declare fn one() -> number
\ No newline at end of file
+declare fn one() -> number
+
+declare fn two() -> number
`,
		},
		{
			name:     "two edits far enough apart to take a hunk each",
			exists:   true,
			contents: twelveDecls,
			edits: []textEdit{
				{at: after(t, twelveDecls, "declare fn one() -> number"), text: " // first"},
				{at: after(t, twelveDecls, "declare fn twelve() -> number"), text: " // last"},
			},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -1,4 +1,4 @@
-declare fn one() -> number
+declare fn one() -> number // first
 declare fn two() -> number
 declare fn three() -> number
 declare fn four() -> number
@@ -9,4 +9,4 @@
 declare fn nine() -> number
 declare fn ten() -> number
 declare fn eleven() -> number
-declare fn twelve() -> number
+declare fn twelve() -> number // last
`,
		},
		{
			name:     "two edits close enough to share a hunk",
			exists:   true,
			contents: twelveDecls,
			edits: []textEdit{
				{at: after(t, twelveDecls, "declare fn one() -> number"), text: " // first"},
				{at: after(t, twelveDecls, "declare fn three() -> number"), text: " // third"},
			},
			want: `--- a/std/array.esc
+++ b/std/array.esc
@@ -1,6 +1,6 @@
-declare fn one() -> number
+declare fn one() -> number // first
 declare fn two() -> number
-declare fn three() -> number
+declare fn three() -> number // third
 declare fn four() -> number
 declare fn five() -> number
 declare fn six() -> number
`,
		},
	}
}

// TestUnifiedDiff pins the rendered patch for every shape in diffCases,
// and pins it against the edits in either order. The write pass hands
// its edits over grouped by owner, which need not run down the file, so
// the order they arrive in must not reach the output.
func TestUnifiedDiff(t *testing.T) {
	t.Parallel()
	for _, tc := range diffCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := unifiedDiff("std/array.esc", tc.exists, tc.contents, tc.edits)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			backwards := slices.Clone(tc.edits)
			slices.Reverse(backwards)
			got, err = unifiedDiff("std/array.esc", tc.exists, tc.contents, backwards)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestUnifiedDiff_ReproducesTheWrite checks that the edits handed to
// the renderer still produce the file the write pass leaves on disk.
// lineAligned rotates an insertion onto the following line boundary to
// keep it reading as a plain addition, and that rotation has to be
// byte-for-byte neutral. A contributor applying the `check` output
// would otherwise land somewhere `regenerate` never would.
func TestUnifiedDiff_ReproducesTheWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range diffCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rendered, err := udiff.Apply(tc.contents, udiffEdits(tc.contents, tc.edits))
			require.NoError(t, err)
			require.Equal(t, applyEdits(tc.contents, tc.edits), rendered)
		})
	}
}

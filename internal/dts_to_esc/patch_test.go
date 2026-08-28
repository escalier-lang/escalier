package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
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

// diffCase is one committed file plus the insertions a re-run would
// make into it, and the patch that renders.
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
@@ -0,0 +1,1 @@
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
@@ -1,1 +1,3 @@
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

// TestUnifiedDiff pins the rendered patch for every shape in
// diffCases.
func TestUnifiedDiff(t *testing.T) {
	t.Parallel()
	for _, tc := range diffCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want,
				unifiedDiff("std/array.esc", tc.exists, tc.contents, tc.edits))
		})
	}
}

// TestUnifiedDiff_SortsEditsByOffset pins that the renderer orders the
// edits itself. The write pass applies them from the end backwards, so
// the slice it hands over runs the other way from the diff.
func TestUnifiedDiff_SortsEditsByOffset(t *testing.T) {
	t.Parallel()
	const contents = "declare fn one() -> number\ndeclare fn two() -> number\n"
	patch := unifiedDiff("std/array.esc", true, contents, []textEdit{
		{at: len(contents), text: "declare fn three() -> number\n"},
		{at: after(t, contents, "declare fn one() -> number"), text: " // first"},
	})
	snaps.MatchInlineSnapshot(t, patch, snaps.Inline(`--- a/std/array.esc
+++ b/std/array.esc
@@ -1,2 +1,3 @@
-declare fn one() -> number
+declare fn one() -> number // first
 declare fn two() -> number
+declare fn three() -> number
`))
}

// applyEdits inserts every edit into contents the way the write pass
// does, from the end backwards so each offset still indexes into the
// text it was measured against.
func applyEdits(contents string, edits []textEdit) string {
	sorted := make([]textEdit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].at > sorted[j].at })
	out := contents
	for _, e := range sorted {
		out = out[:e.at] + e.text + out[e.at:]
	}
	return out
}

// applyUnifiedDiff reconstructs the new file from a patch and the text
// it was rendered against. It reads only what unifiedDiff emits: a two
// line header, then hunks in committed-line order.
func applyUnifiedDiff(t *testing.T, contents, patch string) string {
	t.Helper()
	committed := splitLinesKeepEnds(contents)
	var out []string
	at := 0
	prev := byte(0)

	body := splitLinesKeepEnds(patch)
	require.GreaterOrEqual(t, len(body), 2, "a patch opens with its two header lines")
	for _, raw := range body[2:] {
		line := strings.TrimSuffix(raw, "\n")
		switch {
		case strings.HasPrefix(line, "@@ "):
			// Copy the committed lines the hunk skipped over.
			start, count := parseHunkStart(t, line)
			first := start
			if count > 0 {
				first = start - 1
			}
			require.LessOrEqual(t, at, first, "hunks run in committed-line order")
			out = append(out, committed[at:first]...)
			at = first
		case line == `\ No newline at end of file`:
			// The line before it was written with a newline this file
			// does not have. A removed line contributed nothing to
			// strip.
			if prev != '-' {
				out[len(out)-1] = strings.TrimSuffix(out[len(out)-1], "\n")
			}
		case strings.HasPrefix(line, " "):
			require.Less(t, at, len(committed), "a context line names a committed line")
			out = append(out, committed[at])
			at++
		case strings.HasPrefix(line, "-"):
			require.Less(t, at, len(committed), "a removed line names a committed line")
			at++
		case strings.HasPrefix(line, "+"):
			out = append(out, line[1:]+"\n")
		default:
			require.Fail(t, "unexpected patch line", "%q", line)
		}
		if line != `\ No newline at end of file` && line != "" {
			prev = line[0]
		}
	}
	out = append(out, committed[at:]...)
	return strings.Join(out, "")
}

// parseHunkStart reads the committed-side start and count off a hunk
// header such as `@@ -4,5 +4,10 @@`.
func parseHunkStart(t *testing.T, header string) (start, count int) {
	t.Helper()
	fields := strings.Fields(header)
	require.Len(t, fields, 4, "a hunk header has four fields")
	_, err := fmt.Sscanf(fields[1], "-%d,%d", &start, &count)
	require.NoError(t, err, "parsing %q", fields[1])
	return start, count
}

// TestUnifiedDiff_ReproducesTheWrite applies each rendered patch back to
// the committed text and checks the result against what the write pass
// would leave on disk. Both are built from one set of edits, so a patch
// that does not reproduce the write is a rendering bug — and a
// contributor who applies the `check` output by hand would land
// somewhere `regenerate` never would.
func TestUnifiedDiff_ReproducesTheWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range diffCases(t) {
		if len(tc.edits) == 0 {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			patch := unifiedDiff("std/array.esc", tc.exists, tc.contents, tc.edits)
			require.Equal(t, applyEdits(tc.contents, tc.edits),
				applyUnifiedDiff(t, tc.contents, patch))
		})
	}
}

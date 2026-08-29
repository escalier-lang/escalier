package dts_to_esc

import (
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// This file renders what an additive re-run would write as a unified
// diff, the form `dts_to_esc check` reports per §6.6 of
// planning/builtins/implementation_plan.md.
//
// go-udiff does the rendering. It re-exports the diff package gopls
// uses, and its entry point takes byte-offset edits rather than two
// whole texts. That is what the write pass already produces, so the
// check mode renders a package without assembling the file the write
// mode would.

// textEdit is one insertion the write pass makes into a committed
// `.esc` file. `text` goes in at byte offset `at` and nothing is
// removed.
type textEdit struct {
	at   int
	text string
}

// diffContext is how many unchanged lines a hunk carries on each side
// of a change, matching the unified-diff default.
const diffContext = 3

// unifiedDiff renders edits against contents as a unified diff.
//
// path is the committed file's path relative to the `.esc` tree root,
// and exists reports whether that file was on disk. An absent file
// diffs against /dev/null, so seeding an empty tree reads as a set of
// file creations. The result is "" when edits is empty.
func unifiedDiff(path string, exists bool, contents string, edits []textEdit) (string, error) {
	from := "/dev/null"
	if exists {
		from = "a/" + path
	}
	return udiff.ToUnified(from, "b/"+path, contents, udiffEdits(contents, edits), diffContext)
}

// udiffEdits converts the write pass's insertions into the edits
// go-udiff takes, each one a replacement of the empty byte range at its
// offset.
func udiffEdits(contents string, edits []textEdit) []udiff.Edit {
	out := make([]udiff.Edit, len(edits))
	for i, e := range edits {
		at, text := lineAligned(contents, e.at, e.text)
		out[i] = udiff.Edit{Start: at, End: at, New: text}
	}
	return out
}

// lineAligned moves an insertion sitting at the end of a line to the
// start of the next one, rotating the newline from the front of the text
// to the back. Both forms produce the same bytes, but the renderer widens
// every edit to the lines it touches, so only the whole-line form keeps
// the line it followed off both sides of the hunk. An insertion opening
// with anything else does change that line, and stays put.
func lineAligned(contents string, at int, text string) (int, string) {
	if !strings.HasPrefix(text, "\n") || at >= len(contents) || contents[at] != '\n' {
		return at, text
	}
	return at + 1, text[1:] + "\n"
}

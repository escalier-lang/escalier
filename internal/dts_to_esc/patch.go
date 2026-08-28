package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"
)

// This file renders what an additive re-run would write as a unified
// diff, the form `dts_to_esc check` reports per §6.6 of
// planning/builtins/implementation_plan.md. The write pass only ever
// inserts, so the diff is built from the insertion points themselves
// rather than by matching the two texts line by line.

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
func unifiedDiff(path string, exists bool, contents string, edits []textEdit) string {
	if len(edits) == 0 {
		return ""
	}
	lines := splitLinesKeepEnds(contents)
	changes := lineChanges(contents, lines, edits)
	if len(changes) == 0 {
		return ""
	}

	var sb strings.Builder
	if exists {
		fmt.Fprintf(&sb, "--- a/%s\n", path)
	} else {
		sb.WriteString("--- /dev/null\n")
	}
	fmt.Fprintf(&sb, "+++ b/%s\n", path)
	writeHunks(&sb, lines, changes)
	return sb.String()
}

// lineChange is one committed line and what the write pass leaves in
// its place. The line at index len(lines) is the position past the end
// of the file, where appended declarations land; a change there has no
// old line.
type lineChange struct {
	line int
	old  []string
	new  []string
}

// lineChanges groups the edits by the committed line they fall on and
// rebuilds each of those lines with its insertions applied. Edits
// arrive in any order and are sorted here, since the offsets they carry
// are what decides which line they belong to.
func lineChanges(contents string, lines []string, edits []textEdit) []lineChange {
	sorted := make([]textEdit, len(edits))
	copy(sorted, edits)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].at < sorted[j].at })

	starts := lineStarts(lines)
	owner := lineIndexes(lines, starts, sorted)

	var out []lineChange
	for i := 0; i < len(sorted); {
		j := i
		for j < len(sorted) && owner[j] == owner[i] {
			j++
		}
		line := owner[i]
		out = append(out, insertionOnly(lineChange{
			line: line,
			old:  oldLineAt(lines, line),
			new:  splitLinesKeepEnds(rebuildLine(contents, lines, starts, line, sorted[i:j])),
		}))
		i = j
	}
	return out
}

// lineIndexes returns the index of the committed line each edit falls
// on, given edits sorted by offset. An offset past the last newline
// belongs to no line and gets len(lines), the position past the end
// where appended declarations go. The exception is a file whose last
// line has no trailing newline: an offset at its end still lands on
// that line, and extends it.
func lineIndexes(lines []string, starts []int, edits []textEdit) []int {
	pastEnd := len(lines)
	if pastEnd > 0 && !strings.HasSuffix(lines[pastEnd-1], "\n") {
		pastEnd--
	}
	out := make([]int, len(edits))
	line := 0
	for i, e := range edits {
		for line < len(lines) && e.at >= starts[line]+len(lines[line]) {
			line++
		}
		out[i] = min(line, pastEnd)
	}
	return out
}

// insertionOnly turns a change whose rebuilt text opens with a copy of
// the committed line into an insertion after that line. A member
// spliced in at the end of a line leaves that line's own bytes alone,
// so reporting it as a rewrite of the line to itself would put an
// unchanged line on both sides of the diff.
func insertionOnly(c lineChange) lineChange {
	if len(c.old) == 1 && len(c.new) > 0 && c.new[0] == c.old[0] {
		c.line++
		c.old = nil
		c.new = c.new[1:]
	}
	return c
}

// oldLineAt returns the committed line a change replaces, or nil for a
// change past the end of the file.
func oldLineAt(lines []string, line int) []string {
	if line >= len(lines) {
		return nil
	}
	return []string{lines[line]}
}

// rebuildLine returns the text that replaces one committed line: the
// line with every edit that falls on it spliced in. For the position
// past the end of the file it returns the inserted text alone.
func rebuildLine(contents string, lines []string, starts []int, line int, edits []textEdit) string {
	var sb strings.Builder
	at := len(contents)
	end := len(contents)
	if line < len(lines) {
		at = starts[line]
		end = starts[line] + len(lines[line])
	}
	for _, e := range edits {
		sb.WriteString(contents[at:e.at])
		sb.WriteString(e.text)
		at = e.at
	}
	sb.WriteString(contents[at:end])
	return sb.String()
}

// writeHunks renders the changes as unified-diff hunks. Changes close
// enough that their context windows would overlap share one hunk.
func writeHunks(sb *strings.Builder, lines []string, changes []lineChange) {
	// delta tracks how far the new side has drifted from the old one
	// across the hunks already written, which is what turns an old line
	// number into the new line number beside it.
	delta := 0
	for i := 0; i < len(changes); {
		j := i + 1
		for j < len(changes) && changes[j].line-endOf(changes[j-1]) <= 2*diffContext {
			j++
		}
		delta += writeHunk(sb, lines, changes[i:j], delta)
		i = j
	}
}

// endOf returns the first committed line past the one a change
// replaces.
func endOf(c lineChange) int {
	return c.line + len(c.old)
}

// writeHunk renders one hunk and returns how many lines it added to the
// new side. delta is the drift the hunks before it accumulated.
func writeHunk(sb *strings.Builder, lines []string, changes []lineChange, delta int) int {
	oldStart := max(0, changes[0].line-diffContext)
	oldEnd := min(len(lines), endOf(changes[len(changes)-1])+diffContext)

	grew := 0
	for _, c := range changes {
		grew += len(c.new) - len(c.old)
	}
	oldCount := oldEnd - oldStart
	fmt.Fprintf(sb, "@@ -%s +%s @@\n",
		hunkRange(oldStart, oldCount),
		hunkRange(oldStart+delta, oldCount+grew))

	next := 0
	for line := oldStart; line < oldEnd; line++ {
		// A change with no old line inserts before this one and leaves
		// it to be written as context.
		replaced := false
		for ; next < len(changes) && changes[next].line == line; next++ {
			writeDiffLines(sb, "-", changes[next].old)
			writeDiffLines(sb, "+", changes[next].new)
			replaced = replaced || len(changes[next].old) > 0
		}
		if !replaced {
			writeDiffLines(sb, " ", []string{lines[line]})
		}
	}
	for ; next < len(changes); next++ {
		writeDiffLines(sb, "-", changes[next].old)
		writeDiffLines(sb, "+", changes[next].new)
	}
	return grew
}

// hunkRange renders one side of a hunk header. Line numbers are
// 1-based, except that an empty range is numbered by the line it
// follows, which is what makes a hunk against an empty file read
// `-0,0`.
func hunkRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// writeDiffLines writes each line under the given marker. A line with
// no trailing newline is the last line of a file that ends without one,
// and takes the marker diff uses to say so.
func writeDiffLines(sb *strings.Builder, marker string, lines []string) {
	for _, line := range lines {
		sb.WriteString(marker)
		sb.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			sb.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

// splitLinesKeepEnds splits text into lines, each keeping its trailing
// newline. The final line has no newline when the text does not end
// with one. Empty text yields no lines.
func splitLinesKeepEnds(text string) []string {
	var out []string
	for start := 0; start < len(text); {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			out = append(out, text[start:])
			break
		}
		out = append(out, text[start:start+i+1])
		start += i + 1
	}
	return out
}

// lineStarts returns the byte offset each line begins at.
func lineStarts(lines []string) []int {
	starts := make([]int, len(lines))
	at := 0
	for i, line := range lines {
		starts[i] = at
		at += len(line)
	}
	return starts
}

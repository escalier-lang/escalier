package ast

import (
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// ColumnEncoding names the unit a column counts. A byte offset is the same
// number whoever reads it, but a column is not, so every conversion has to say
// which unit it wants.
type ColumnEncoding int

const (
	// CodePointColumns counts runes. Diagnostics a person reads use this,
	// since it matches what a caret under the offending text has to line up
	// with in a fixed-width terminal.
	CodePointColumns ColumnEncoding = iota
	// UTF16Columns counts UTF-16 code units. LSP positions use this unit
	// unless the client negotiates another one.
	UTF16Columns
	// ByteColumns counts bytes.
	ByteColumns
)

// LineMap converts between byte offsets and 1-based line and column positions
// in one source file. Build it once per file and reuse it. Source.LineMap
// memoizes one per Source.
type LineMap struct {
	contents string
	// lineStarts[i] is the byte offset where line i+1 begins. The first entry
	// is 0, and every newline in the file appends the offset just past it, so
	// the length is the number of lines.
	lineStarts []int
}

// NewLineMap scans contents for line breaks and returns a map over it. The
// scan is linear in the length of contents and happens once.
func NewLineMap(contents string) *LineMap {
	lineStarts := []int{0}
	for i := range len(contents) {
		if contents[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	return &LineMap{contents: contents, lineStarts: lineStarts}
}

// Position converts a byte offset into a 1-based line and column. An offset
// before the start or past the end of the file is clamped to the nearest
// position in it.
//
// An offset that lands inside a multi-byte character reports the column of
// the character containing it. Only a caller that built the offset by hand
// can produce one, since every offset the lexer records sits on a boundary.
func (m *LineMap) Position(offset int, enc ColumnEncoding) (line, column int) {
	offset = m.clamp(offset)
	line = m.lineAt(offset)
	return line, 1 + countColumns(m.contents[m.lineStarts[line-1]:offset], enc)
}

// Offset converts a 1-based line and column into a byte offset. A line or
// column outside the file is clamped to the nearest offset in it, and a
// column past the end of its line lands on the line's last position.
func (m *LineMap) Offset(line, column int, enc ColumnEncoding) int {
	if line < 1 {
		return 0
	}
	if line > len(m.lineStarts) {
		return len(m.contents)
	}
	start := m.lineStarts[line-1]
	end := len(m.contents)
	if line < len(m.lineStarts) {
		end = m.lineStarts[line]
	}
	if column < 1 {
		return start
	}
	remaining := column - 1
	for at := start; at < end; {
		if remaining == 0 {
			return at
		}
		r, width := utf8.DecodeRuneInString(m.contents[at:])
		remaining -= columnsForRune(r, enc)
		at += width
		// A column landing mid-character overshoots. Report the position just
		// past the character it fell inside rather than a boundary the caller
		// did not ask for.
		if remaining < 0 {
			return at
		}
	}
	return end
}

// Line returns the 1-based line the byte offset falls on.
func (m *LineMap) Line(offset int) int {
	return m.lineAt(m.clamp(offset))
}

// SameLine reports whether two byte offsets fall on the same line. The parser
// consults it where a line break separates two constructs that would
// otherwise read as one, such as a call argument list opening on the line
// after its callee.
func (m *LineMap) SameLine(a, b int) bool {
	return m.Line(a) == m.Line(b)
}

// LineText returns the source text of a 1-based line without its trailing
// newline. It returns "" for a line number outside the file.
func (m *LineMap) LineText(line int) string {
	if line < 1 || line > len(m.lineStarts) {
		return ""
	}
	start := m.lineStarts[line-1]
	end := len(m.contents)
	if line < len(m.lineStarts) {
		end = m.lineStarts[line] - 1
	}
	if end > start && m.contents[end-1] == '\r' {
		end--
	}
	return m.contents[start:end]
}

// LineCount returns the number of lines in the file. A file ending in a
// newline has a final empty line, matching how an editor numbers it.
func (m *LineMap) LineCount() int {
	return len(m.lineStarts)
}

func (m *LineMap) clamp(offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(m.contents) {
		return len(m.contents)
	}
	return offset
}

// lineAt returns the 1-based line containing an already-clamped offset. It
// finds the first line starting past the offset, so the line before that one
// is the line the offset falls on.
func (m *LineMap) lineAt(offset int) int {
	return sort.Search(len(m.lineStarts), func(i int) bool {
		return m.lineStarts[i] > offset
	})
}

// countColumns returns how many columns of the given encoding s spans.
func countColumns(s string, enc ColumnEncoding) int {
	switch enc {
	case ByteColumns:
		return len(s)
	case CodePointColumns:
		return utf8.RuneCountInString(s)
	case UTF16Columns:
		count := 0
		for _, r := range s {
			count += columnsForRune(r, UTF16Columns)
		}
		return count
	}
	return utf8.RuneCountInString(s)
}

// columnsForRune returns how many columns of the given encoding one rune
// occupies. Only a rune outside the basic multilingual plane differs between
// the encodings, and only for UTF16Columns, where it takes a surrogate pair.
func columnsForRune(r rune, enc ColumnEncoding) int {
	switch enc {
	case ByteColumns:
		return utf8.RuneLen(r)
	case CodePointColumns:
		return 1
	case UTF16Columns:
		if n := utf16.RuneLen(r); n > 0 {
			return n
		}
		// RuneLen rejects a surrogate half and any value outside Unicode.
		// Neither survives decoding, which yields RuneError instead, so this
		// guards against a rune a caller passed in directly.
		return 1
	}
	return 1
}

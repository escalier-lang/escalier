package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// contents holds one multi-byte character per encoding that behaves
// differently: `é` takes two bytes and one UTF-16 code unit, and `𝄞` takes
// four bytes and a surrogate pair.
const mixedContents = "val x = 1\nval café = 2\nval 𝄞 = 3\n"

func TestLineMapPosition(t *testing.T) {
	t.Parallel()
	lineMap := NewLineMap(mixedContents)

	tests := []struct {
		name       string
		offset     int
		enc        ColumnEncoding
		line       int
		column     int
		coversText string
	}{
		{"start of file", 0, CodePointColumns, 1, 1, "val"},
		{"middle of the first line", 4, CodePointColumns, 1, 5, "x"},
		{"first byte after a newline", 10, CodePointColumns, 2, 1, "val"},
		{
			name: "past a two-byte character", offset: 19, enc: CodePointColumns,
			line: 2, column: 9, coversText: " = 2",
		},
		{
			name: "past a two-byte character in bytes", offset: 19, enc: ByteColumns,
			line: 2, column: 10, coversText: " = 2",
		},
		{
			name: "past a two-byte character in UTF-16", offset: 19, enc: UTF16Columns,
			line: 2, column: 9, coversText: " = 2",
		},
		{
			name: "past a four-byte character", offset: 32, enc: CodePointColumns,
			line: 3, column: 6, coversText: " = 3",
		},
		{
			name: "past a four-byte character in UTF-16", offset: 32, enc: UTF16Columns,
			line: 3, column: 7, coversText: " = 3",
		},
		{
			name: "past a four-byte character in bytes", offset: 32, enc: ByteColumns,
			line: 3, column: 9, coversText: " = 3",
		},
		{"end of file", len(mixedContents), CodePointColumns, 4, 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, column := lineMap.Position(tc.offset, tc.enc)
			require.Equal(t, tc.line, line)
			require.Equal(t, tc.column, column)
			// The offset is what actually indexes the file, so pin the text it
			// lands on rather than trusting the line and column alone.
			require.Equal(t, tc.coversText, mixedContents[tc.offset:tc.offset+len(tc.coversText)])
		})
	}
}

func TestLineMapPositionClampsOutsideTheFile(t *testing.T) {
	t.Parallel()
	lineMap := NewLineMap("abc\ndef\n")

	line, column := lineMap.Position(-5, CodePointColumns)
	require.Equal(t, 1, line)
	require.Equal(t, 1, column)

	line, column = lineMap.Position(1000, CodePointColumns)
	require.Equal(t, 3, line)
	require.Equal(t, 1, column)
}

func TestLineMapOffsetRoundTrips(t *testing.T) {
	t.Parallel()
	lineMap := NewLineMap(mixedContents)
	encodings := []ColumnEncoding{CodePointColumns, UTF16Columns, ByteColumns}

	// Every position in the file converts to a line and column and back. A
	// mid-character offset is skipped, since only a hand-built offset can land
	// there and it has no line and column of its own.
	for offset := range len(mixedContents) + 1 {
		if offset < len(mixedContents) && mixedContents[offset]&0xC0 == 0x80 {
			continue
		}
		for _, enc := range encodings {
			line, column := lineMap.Position(offset, enc)
			require.Equalf(t, offset, lineMap.Offset(line, column, enc),
				"offset %d did not survive the trip through %d:%d", offset, line, column)
		}
	}
}

func TestLineMapOffsetClampsOutsideTheFile(t *testing.T) {
	t.Parallel()
	contents := "abc\ndef\n"
	lineMap := NewLineMap(contents)

	tests := []struct {
		name         string
		line, column int
		want         int
	}{
		{"line below the first", 0, 1, 0},
		{"column below the first", 1, 0, 0},
		{"column past the end of its line", 1, 99, 3},
		{"line past the last", 99, 1, len(contents)},
		{"the empty line a trailing newline leaves", 3, 1, len(contents)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, lineMap.Offset(tc.line, tc.column, CodePointColumns))
		})
	}
}

// A column landing inside a multi-byte character cannot be honoured exactly.
// Reporting the position just past the character keeps the result on a
// character boundary.
func TestLineMapOffsetInsideAMultiByteCharacter(t *testing.T) {
	t.Parallel()
	contents := "café"
	lineMap := NewLineMap(contents)
	// `é` starts at byte 3, so byte column 5 lands on its second byte.
	require.Equal(t, len(contents), lineMap.Offset(1, 5, ByteColumns))
}

func TestLineMapLineAndSameLine(t *testing.T) {
	t.Parallel()
	lineMap := NewLineMap("abc\ndef\nghi")

	require.Equal(t, 1, lineMap.Line(0))
	require.Equal(t, 1, lineMap.Line(3))
	require.Equal(t, 2, lineMap.Line(4))
	require.Equal(t, 3, lineMap.Line(10))

	require.True(t, lineMap.SameLine(0, 3))
	require.False(t, lineMap.SameLine(3, 4))
	// Order does not matter.
	require.False(t, lineMap.SameLine(4, 3))
}

func TestLineMapLineText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		contents string
		line     int
		want     string
	}{
		{"first line", "abc\ndef\n", 1, "abc"},
		{"last line before a trailing newline", "abc\ndef\n", 2, "def"},
		{"the empty line a trailing newline leaves", "abc\ndef\n", 3, ""},
		{"a file with no trailing newline", "abc\ndef", 2, "def"},
		{"a carriage return is not part of the line", "abc\r\ndef\n", 1, "abc"},
		{"line zero", "abc\n", 0, ""},
		{"line past the end", "abc\n", 99, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, NewLineMap(tc.contents).LineText(tc.line))
		})
	}
}

func TestLineMapLineCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		contents string
		want     int
	}{
		{"empty file", "", 1},
		{"one line with no newline", "abc", 1},
		{"one line with a newline", "abc\n", 2},
		{"two lines", "abc\ndef", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, NewLineMap(tc.contents).LineCount())
		})
	}
}

func TestSourceLineMapIsReused(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // The memoized line map is built on first use.
	source := &Source{ID: 0, Path: "input.esc", Contents: "abc\ndef\n"}
	require.Same(t, source.LineMap(), source.LineMap())
}

func TestSpanString(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // The memoized line map is built on first use.
	source := &Source{ID: 0, Path: "input.esc", Contents: "val x = 1\nval y = 2\n"}
	span := NewSpan(Location{Offset: 10}, Location{Offset: 13}, 0)

	require.Equal(t, "2:1-2:4", SpanString(source, span))
	// Without a source there is no line to name, so the offsets stand in.
	require.Equal(t, "10-13", SpanString(nil, span))
	// A span reaching past the end of the file did not come from it.
	past := NewSpan(Location{Offset: 10}, Location{Offset: 500}, 0)
	require.Equal(t, "10-500", SpanString(source, past))
}

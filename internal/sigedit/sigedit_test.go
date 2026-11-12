package sigedit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkipPast(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     byte
		marks    string
		// expected is the offset skipPast returns, or -1 when it fails.
		expected int
	}{
		{name: "Immediate", contents: ")", want: ')', expected: 1},
		{name: "Whitespace", contents: " \t\n)", want: ')', expected: 4},
		{name: "Marks", contents: ", ...)", want: ')', marks: ",.", expected: 6},
		{name: "LineComment", contents: "// c\n)", want: ')', expected: 6},
		{name: "BlockComment", contents: "/* ) */)", want: ')', expected: 8},
		{name: "UnclosedBlockComment", contents: "/* )", want: ')', expected: -1},
		{name: "UnterminatedLineComment", contents: "// )", want: ')', expected: -1},
		{name: "OtherByte", contents: "x)", want: ')', expected: -1},
		{name: "End", contents: "  ", want: ')', expected: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := skipPast(tt.contents, 0, tt.want, tt.marks)
			if tt.expected < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name     string
		edits    []Edit
		expected string
	}{
		{name: "Replace", edits: []Edit{{Start: 0, End: 1, Text: "x"}}, expected: "xbc"},
		{name: "OutOfOrder", edits: []Edit{{Start: 2, End: 2, Text: "2"}, {Start: 0, End: 0, Text: "0"}}, expected: "0ab2c"},
		{name: "InsertionsAtOneOffsetKeepTheirOrder", edits: []Edit{{Start: 1, End: 1, Text: "x"}, {Start: 1, End: 1, Text: "y"}}, expected: "axybc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, Apply("abc", tt.edits))
		})
	}
}

package dts_parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseReferenceDirectives covers the directive headers ParseReferenceDirectives reads.
func TestParseReferenceDirectives(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     []ReferenceDirective
	}{
		{
			name:     "each kind in source order",
			contents: "/// <reference path=\"global.d.ts\" />\n/// <reference types=\"node\" />\n/// <reference lib=\"es2015\" />\n",
			want: []ReferenceDirective{
				{Kind: ReferencePath, Value: "global.d.ts"},
				{Kind: ReferenceTypes, Value: "node"},
				{Kind: ReferenceLib, Value: "es2015"},
			},
		},
		{
			name:     "a directive after a line comment and a block comment",
			contents: "// Type definitions\n/*\n * A header.\n */\n\n/// <reference path='global.d.ts'/>\n",
			want:     []ReferenceDirective{{Kind: ReferencePath, Value: "global.d.ts"}},
		},
		{
			name:     "a directive after the first statement",
			contents: "/// <reference path=\"a.d.ts\" />\nexport declare const x: number;\n/// <reference path=\"b.d.ts\" />\n",
			want:     []ReferenceDirective{{Kind: ReferencePath, Value: "a.d.ts"}},
		},
		{
			name:     "a directive after a byte-order mark",
			contents: "\uFEFF/// <reference path=\"global.d.ts\" />\n",
			want:     []ReferenceDirective{{Kind: ReferencePath, Value: "global.d.ts"}},
		},
		{
			name:     "a directive after code that follows a one-line block comment",
			contents: "/* c */ export declare const x: number;\n/// <reference path=\"b.d.ts\" />\n",
		},
		{
			name:     "a directive after two block comments on one line",
			contents: "/* a */ /* b\n */\n/// <reference path=\"b.d.ts\" />\n",
			want:     []ReferenceDirective{{Kind: ReferencePath, Value: "b.d.ts"}},
		},
		{
			name:     "an attribute that names no kind",
			contents: "/// <reference no-default-lib=\"true\" />\n/// <amd-module name=\"x\" />\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, ParseReferenceDirectives(test.contents))
		})
	}
}

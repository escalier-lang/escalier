package parser

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParsePatternNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringLiteral": {
			input: "\"hello\"",
		},
		"NumberLiteral": {
			input: "5",
		},
		"BooleanLiteralTrue": {
			input: "true",
		},
		"BooleanLiteralFalse": {
			input: "false",
		},
		"NullLiteral": {
			input: "null",
		},
		"UndefinedLiteral": {
			input: "undefined",
		},
		"Identifier": {
			input: "x",
		},
		"Wildcard": {
			input: "_",
		},
		"TuplePatternWithRest": {
			input: "[a, b, ...rest]",
		},
		"ObjectPatternWithRest": {
			input: "{a, b: c, ...rest}",
		},
		"ExtractPattern": {
			input: "Foo(a, b)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.parsePattern()

			snaps.MatchSnapshot(t, expr)
			assert.Equal(t, parser.Errors, []*Error{})
		})
	}
}

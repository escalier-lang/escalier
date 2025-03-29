package parser

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseJSXNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SelfClosingNoAttrs": {
			input: "<Foo />",
		},
		"SelfClosingAttrs": {
			input: "<Foo bar={5} baz=\"hello\" />",
		},
		"MultipleLines": {
			input: "<Foo\n  bar={5}\n  baz=\"hello\"\n/>",
		},
		"NoAttrsNoChildren": {
			input: "<Foo></Foo>",
		},
		"AttrsNoChildren": {
			input: "<Foo bar={5} baz=\"hello\"></Foo>",
		},
		"ChildElements": {
			input: "<div><span>hello</span>world</div>",
		},
		"ChildExpr": {
			input: "<div>hello, {msg}</div>",
		},
		"Nesting": {
			input: "<div>{<span>{foo}</span>}</div>",
		},
		"Fragment": {
			input: "<><Foo /><Bar /></>",
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
			jsx := parser.parseJSXElement()

			snaps.MatchSnapshot(t, jsx)
			assert.Len(t, parser.Errors, 0)
		})
	}
}

func TestParseJSXErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"MissingEqualsInExprAttr": {
			input: "<Foo bar {5} />",
		},
		"MissingEqualsInStringAttr": {
			input: "<Foo bar \"hello\" />",
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
			jsx := parser.parseJSXElement()

			snaps.MatchSnapshot(t, jsx)
			assert.Greater(t, len(parser.Errors), 0)
			snaps.MatchSnapshot(t, parser.Errors)
		})
	}
}

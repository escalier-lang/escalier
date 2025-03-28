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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.parseJSXElement()

			snaps.MatchSnapshot(t, expr)
			assert.Len(t, parser.Errors, 0)
		})
	}
}

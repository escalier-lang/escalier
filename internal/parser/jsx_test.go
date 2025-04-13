package parser

import (
	"context"
	"testing"
	"time"

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

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			jsx, errors := parser.parseJSXElement()

			snaps.MatchSnapshot(t, jsx)
			assert.Len(t, errors, 0)
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

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			jsx, errors := parser.parseJSXElement()

			snaps.MatchSnapshot(t, jsx)
			assert.Greater(t, len(errors), 0)
			snaps.MatchSnapshot(t, errors)
		})
	}
}

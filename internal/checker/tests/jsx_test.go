package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestJSXElementBasic(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SelfClosingElement": {
			input: `val elem = <div />`,
		},
		"ElementWithClosingTag": {
			input: `val elem = <div></div>`,
		},
		"ElementWithStringProp": {
			input: `val elem = <div className="foo" />`,
		},
		"ElementWithMultipleProps": {
			input: `val elem = <div className="foo" id="bar" />`,
		},
		"ElementWithExpressionProp": {
			input: `
				val name = "foo"
				val elem = <div className={name} />
			`,
		},
		"ElementWithBooleanShorthand": {
			input: `val elem = <input disabled />`,
		},
		"ElementWithSpreadProps": {
			input: `
				val props = {className: "foo", id: "bar"}
				val elem = <div {...props} />
			`,
		},
		"ElementWithSpreadAndRegularProps": {
			input: `
				val props = {className: "foo"}
				val elem = <div {...props} id="bar" />
			`,
		},
		"ElementWithTextChild": {
			input: `val elem = <div>Hello</div>`,
		},
		"ElementWithExpressionChild": {
			input: `
				val name = "World"
				val elem = <div>Hello {name}</div>
			`,
		},
		"NestedElements": {
			input: `val elem = <div><span>Hello</span></div>`,
		},
		"DeeplyNestedElements": {
			input: `val elem = <div><span><a>Link</a></span></div>`,
		},
		"MultipleChildren": {
			input: `val elem = <div><span>One</span><span>Two</span></div>`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			if len(parseErrors) > 0 {
				for _, err := range parseErrors {
					t.Logf("ParseError: %v", err)
				}
			}
			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors")
		})
	}
}

func TestJSXFragmentBasic(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"EmptyFragment": {
			input: `val elem = <></>`,
		},
		"FragmentWithChildren": {
			input: `val elem = <><div /><span /></>`,
		},
		"FragmentWithTextAndElements": {
			input: `val elem = <>Hello<div />World</>`,
		},
		"NestedFragments": {
			input: `val elem = <><><div /></></>`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			if len(parseErrors) > 0 {
				for _, err := range parseErrors {
					t.Logf("ParseError: %v", err)
				}
			}
			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors")
		})
	}
}

func TestJSXComponent(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ComponentWithoutProps": {
			input: `
				fn MyComponent() {
					return <div />
				}
				val elem = <MyComponent />
			`,
		},
		"ComponentWithProps": {
			input: `
				fn Button(props: {label: string}) {
					return <button>{props.label}</button>
				}
				val elem = <Button label="Click me" />
			`,
		},
		"NestedComponents": {
			input: `
				fn Child() {
					return <span>Child</span>
				}
				fn Parent() {
					return <div><Child /></div>
				}
				val elem = <Parent />
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()

			if len(parseErrors) > 0 {
				for _, err := range parseErrors {
					t.Logf("ParseError: %v", err)
				}
			}
			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors")
		})
	}
}

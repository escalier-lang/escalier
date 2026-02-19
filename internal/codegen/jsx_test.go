package codegen

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

func TestJSXTransformBasic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:  "SelfClosingElement",
			input: `val elem = <div />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {});
`,
		},
		{
			name:  "ElementWithClosingTag",
			input: `val elem = <div></div>`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {});
`,
		},
		{
			name:  "ElementWithStringProp",
			input: `val elem = <div className="foo" />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {className: "foo"});
`,
		},
		{
			name:  "ElementWithMultipleProps",
			input: `val elem = <div className="foo" id="bar" />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {className: "foo", id: "bar"});
`,
		},
		{
			name: "ElementWithExpressionProp",
			input: `
val name = "foo"
val elem = <div className={name} />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const name = "foo";
const elem = _jsx("div", {className: name});
`,
		},
		{
			name:  "ElementWithBooleanShorthand",
			input: `val elem = <input disabled />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("input", {disabled: true});
`,
		},
		{
			name:  "ElementWithTextChild",
			input: `val elem = <div>Hello</div>`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {children: "Hello"});
`,
		},
		{
			name: "ElementWithExpressionChild",
			input: `
val name = "World"
val elem = <div>Hello {name}</div>`,
			expected: `import { jsxs as _jsxs } from "react/jsx-runtime";
const name = "World";
const elem = _jsxs("div", {children: ["Hello", name]});
`,
		},
		{
			name:  "NestedElements",
			input: `val elem = <div><span>Hello</span></div>`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {children: _jsx("span", {children: "Hello"})});
`,
		},
		{
			name:  "DeeplyNestedElements",
			input: `val elem = <div><span><a>Link</a></span></div>`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx("div", {children: _jsx("span", {children: _jsx("a", {children: "Link"})})});
`,
		},
		{
			name:  "MultipleChildren",
			input: `val elem = <div><span>One</span><span>Two</span></div>`,
			expected: `import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
const elem = _jsxs("div", {children: [_jsx("span", {children: "One"}), _jsx("span", {children: "Two"})]});
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
				t.FailNow()
			}

			builder := &Builder{
				tempId:   0,
				depGraph: nil,
			}
			module := builder.BuildScript(script)

			printer := NewPrinter()
			printer.PrintModule(module)

			if printer.Output != test.expected {
				t.Errorf("Output mismatch:\nexpected:\n%s\ngot:\n%s", test.expected, printer.Output)
			}
		})
	}
}

func TestJSXTransformFragment(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:  "EmptyFragment",
			input: `val elem = <></>`,
			expected: `import { jsx as _jsx, Fragment as _Fragment } from "react/jsx-runtime";
const elem = _jsx(_Fragment, {});
`,
		},
		{
			name:  "FragmentWithChildren",
			input: `val elem = <><div /><span /></>`,
			expected: `import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
const elem = _jsxs(_Fragment, {children: [_jsx("div", {}), _jsx("span", {})]});
`,
		},
		{
			name:  "FragmentWithTextAndElements",
			input: `val elem = <>Hello<div />World</>`,
			expected: `import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
const elem = _jsxs(_Fragment, {children: ["Hello", _jsx("div", {}), "World"]});
`,
		},
		{
			name:  "NestedFragments",
			input: `val elem = <><><div /></></>`,
			expected: `import { jsx as _jsx, Fragment as _Fragment } from "react/jsx-runtime";
const elem = _jsx(_Fragment, {children: _jsx(_Fragment, {children: _jsx("div", {})})});
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
				t.FailNow()
			}

			builder := &Builder{
				tempId:   0,
				depGraph: nil,
			}
			module := builder.BuildScript(script)

			printer := NewPrinter()
			printer.PrintModule(module)

			if printer.Output != test.expected {
				t.Errorf("Output mismatch:\nexpected:\n%s\ngot:\n%s", test.expected, printer.Output)
			}
		})
	}
}

func TestJSXTransformComponent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "ComponentWithoutProps",
			input: `
fn MyComponent() {
	return <div />
}
val elem = <MyComponent />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
function MyComponent() {
  return _jsx("div", {});
}
const elem = _jsx(MyComponent, {});
`,
		},
		{
			name: "ComponentWithProps",
			input: `
fn Button(props) {
	return <button>{props.label}</button>
}
val elem = <Button label="Click me" />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
function Button(temp1) {
  const props = temp1;
  return _jsx("button", {children: props.label});
}
const elem = _jsx(Button, {label: "Click me"});
`,
		},
		{
			name: "NestedComponents",
			input: `
fn Child() {
	return <span>Child</span>
}
fn Parent() {
	return <div><Child /></div>
}
val elem = <Parent />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
function Child() {
  return _jsx("span", {children: "Child"});
}
function Parent() {
  return _jsx("div", {children: _jsx(Child, {})});
}
const elem = _jsx(Parent, {});
`,
		},
		{
			name:  "MemberExpressionComponent",
			input: `val elem = <Icons.Star size={24} />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const elem = _jsx(Icons.Star, {size: 24});
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
				t.FailNow()
			}

			builder := &Builder{
				tempId:   0,
				depGraph: nil,
			}
			module := builder.BuildScript(script)

			printer := NewPrinter()
			printer.PrintModule(module)

			if printer.Output != test.expected {
				t.Errorf("Output mismatch:\nexpected:\n%s\ngot:\n%s", test.expected, printer.Output)
			}
		})
	}
}

func TestJSXTransformSpread(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "SpreadPropsOnly",
			input: `
val props = {className: "foo", id: "bar"}
val elem = <div {...props} />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const props = {className: "foo", id: "bar"};
const elem = _jsx("div", {...props});
`,
		},
		{
			name: "SpreadWithRegularProps",
			input: `
val props = {className: "foo"}
val elem = <div {...props} id="bar" />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const props = {className: "foo"};
const elem = _jsx("div", {...props, id: "bar"});
`,
		},
		{
			name: "MultipleSpreadProps",
			input: `
val props1 = {className: "foo"}
val props2 = {id: "bar"}
val elem = <div {...props1} {...props2} />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const props1 = {className: "foo"};
const props2 = {id: "bar"};
const elem = _jsx("div", {...props1, ...props2});
`,
		},
		{
			name: "SpreadWithBooleanShorthand",
			input: `
val props = {className: "foo"}
val elem = <input {...props} disabled />`,
			expected: `import { jsx as _jsx } from "react/jsx-runtime";
const props = {className: "foo"};
const elem = _jsx("input", {...props, disabled: true});
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
				t.FailNow()
			}

			builder := &Builder{
				tempId:   0,
				depGraph: nil,
			}
			module := builder.BuildScript(script)

			printer := NewPrinter()
			printer.PrintModule(module)

			if printer.Output != test.expected {
				t.Errorf("Output mismatch:\nexpected:\n%s\ngot:\n%s", test.expected, printer.Output)
			}
		})
	}
}

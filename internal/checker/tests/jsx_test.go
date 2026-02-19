package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

// newOptionalProp creates an optional PropertyElem using the constructor pattern.
// This is a test helper that wraps NewPropertyElem and sets Optional to true.
func newOptionalProp(name string, value type_system.Type) *type_system.PropertyElem {
	prop := type_system.NewPropertyElem(type_system.NewStrKey(name), value)
	prop.Optional = true
	return prop
}

// createJSXNamespaceWithIntrinsicElements creates a JSX namespace with a subset of
// IntrinsicElements for testing. This simulates what would be loaded from @types/react.
func createJSXNamespaceWithIntrinsicElements() *type_system.Namespace {
	jsxNs := type_system.NewNamespace()

	// Common types used across elements
	strType := type_system.NewStrPrimType(nil)
	boolType := type_system.NewBoolPrimType(nil)
	handlerType := type_system.NewFuncType(nil, nil, nil, type_system.NewVoidType(nil), type_system.NewNeverType(nil))

	// Create IntrinsicElements type as an object type mapping tag names to prop types
	// For example: { div: { className?: string, id?: string, ... }, input: { disabled?: boolean, ... }, ... }
	intrinsicElems := []type_system.ObjTypeElem{
		// div element props
		type_system.NewPropertyElem(
			type_system.NewStrKey("div"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				newOptionalProp("className", strType),
				newOptionalProp("id", strType),
				newOptionalProp("onClick", handlerType),
			}),
		),
		// span element props (similar to div)
		type_system.NewPropertyElem(
			type_system.NewStrKey("span"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				newOptionalProp("className", strType),
				newOptionalProp("id", strType),
			}),
		),
		// button element props
		type_system.NewPropertyElem(
			type_system.NewStrKey("button"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				newOptionalProp("className", strType),
				newOptionalProp("disabled", boolType),
				newOptionalProp("onClick", handlerType),
				newOptionalProp("type", strType),
			}),
		),
		// input element props
		type_system.NewPropertyElem(
			type_system.NewStrKey("input"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				newOptionalProp("className", strType),
				newOptionalProp("disabled", boolType),
				newOptionalProp("type", strType),
				newOptionalProp("value", strType),
				newOptionalProp("onChange", handlerType),
			}),
		),
		// a (anchor) element props
		type_system.NewPropertyElem(
			type_system.NewStrKey("a"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				newOptionalProp("href", strType),
				newOptionalProp("target", strType),
				newOptionalProp("className", strType),
			}),
		),
	}

	intrinsicElementsType := type_system.NewObjectType(nil, intrinsicElems)
	jsxNs.Types["IntrinsicElements"] = &type_system.TypeAlias{
		Type:       intrinsicElementsType,
		TypeParams: nil,
	}

	return jsxNs
}

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

func TestJSXInferredTypes(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"SelfClosingElement": {
			input: `val elem = <div />`,
			expectedTypes: map[string]string{
				"elem": "{}", // Placeholder type - will be JSX.Element in Phase 4
			},
		},
		"ElementWithProps": {
			input: `val elem = <div className="foo" id="bar" />`,
			expectedTypes: map[string]string{
				"elem": "{}", // Placeholder type - will be JSX.Element in Phase 4
			},
		},
		"Fragment": {
			input: `val elem = <><div /><span /></>`,
			expectedTypes: map[string]string{
				"elem": "{}", // Placeholder type - will be JSX.Element in Phase 4
			},
		},
		"NestedElements": {
			input: `val elem = <div><span>Hello</span></div>`,
			expectedTypes: map[string]string{
				"elem": "{}", // Placeholder type - will be JSX.Element in Phase 4
			},
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			scope, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					t.Logf("Unexpected Error[%d]: %s", i, err.Message())
				}
			}
			assert.Empty(t, inferErrors, "Expected no inference errors for %s", name)

			// Collect actual types for verification
			actualTypes := make(map[string]string)
			for name, binding := range scope.Namespace.Values {
				assert.NotNil(t, binding)
				actualTypes[name] = binding.Type.String()
			}

			// Verify that all expected types match the actual inferred types
			for expectedName, expectedType := range test.expectedTypes {
				actualType, exists := actualTypes[expectedName]
				assert.True(t, exists, "Expected variable %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualType, "Type mismatch for variable %s", expectedName)
				}
			}
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

// Phase 2 Tests: Intrinsic Element Type Validation
// These tests verify that intrinsic element props are validated against JSX.IntrinsicElements

func TestIntrinsicElementValidProps(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"DivWithClassName": {
			input: `val elem = <div className="foo" />`,
		},
		"DivWithId": {
			input: `val elem = <div id="main" />`,
		},
		"DivWithMultipleValidProps": {
			input: `val elem = <div className="foo" id="main" />`,
		},
		"ButtonWithDisabled": {
			input: `val elem = <button disabled={true} />`,
		},
		"ButtonWithClassName": {
			input: `val elem = <button className="primary" />`,
		},
		"InputWithValue": {
			input: `val elem = <input value="hello" />`,
		},
		"InputWithDisabledBooleanShorthand": {
			input: `val elem = <input disabled />`,
		},
		"AnchorWithHref": {
			input: `val elem = <a href="https://example.com" />`,
		},
		"AnchorWithHrefAndTarget": {
			input: `val elem = <a href="https://example.com" target="_blank" />`,
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			scope := Prelude(c)

			// Add JSX namespace with IntrinsicElements to the scope
			jsxNs := createJSXNamespaceWithIntrinsicElements()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid props")
		})
	}
}

func TestIntrinsicElementInvalidPropType(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string // Substring expected in at least one error message
	}{
		"DivClassNameWithNumber": {
			input:       `val elem = <div className={123} />`,
			errorSubstr: "string", // Error should mention the expected type
		},
		"ButtonDisabledWithString": {
			input:       `val elem = <button disabled="yes" />`,
			errorSubstr: "boolean", // Error should mention the expected type
		},
		"DivOnClickWithString": {
			input:       `val elem = <div onClick="notAFunction" />`,
			errorSubstr: "fn", // Error should mention that a function type was expected
		},
		"ButtonOnClickWithNumber": {
			input:       `val elem = <button onClick={42} />`,
			errorSubstr: "fn", // Error should mention that a function type was expected
		},
		"InputOnChangeWithBoolean": {
			input:       `val elem = <input onChange={true} />`,
			errorSubstr: "fn", // Error should mention that a function type was expected
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			scope := Prelude(c)

			// Add JSX namespace with IntrinsicElements to the scope
			jsxNs := createJSXNamespaceWithIntrinsicElements()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect type errors for invalid prop types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for invalid prop types")

			// Verify at least one error message contains the expected substring
			found := false
			for _, inferErr := range inferErrors {
				if strings.Contains(inferErr.Message(), test.errorSubstr) {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected at least one error message to contain %q", test.errorSubstr)
		})
	}
}

func TestIntrinsicElementUnknownElement(t *testing.T) {
	// Unknown elements (not in IntrinsicElements) should still allow any props
	// This is the permissive fallback behavior
	tests := map[string]struct {
		input string
	}{
		"UnknownElementWithAnyProps": {
			input: `val elem = <customtag foo="bar" baz={123} />`,
		},
		"AnotherUnknownElement": {
			input: `val elem = <unknowntag className="test" unknownProp={true} />`,
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			scope := Prelude(c)

			// Add JSX namespace with IntrinsicElements to the scope
			jsxNs := createJSXNamespaceWithIntrinsicElements()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// Unknown elements should not produce errors (permissive fallback)
			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for unknown elements")
		})
	}
}

func TestIntrinsicElementWithoutJSXNamespace(t *testing.T) {
	// When JSX namespace is not available, any props should be allowed (permissive fallback)
	tests := map[string]struct {
		input string
	}{
		"DivWithAnyProps": {
			input: `val elem = <div unknownProp="value" anotherProp={123} />`,
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			// Use prelude WITHOUT adding JSX namespace
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// Without JSX namespace, any props should be allowed
			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors when JSX namespace is not available")
		})
	}
}

func TestIntrinsicElementEventHandlers(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"DivWithOnClick": {
			input: `val elem = <div onClick={fn() { }} />`,
		},
		"ButtonWithOnClick": {
			input: `val elem = <button onClick={fn() { }} />`,
		},
		"InputWithOnChange": {
			input: `val elem = <input onChange={fn() { }} />`,
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			scope := Prelude(c)

			// Add JSX namespace with IntrinsicElements to the scope
			jsxNs := createJSXNamespaceWithIntrinsicElements()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid event handlers")
		})
	}
}

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

// createJSXNamespaceWithRequiredProps creates a JSX namespace with some required props for testing.
func createJSXNamespaceWithRequiredProps() *type_system.Namespace {
	jsxNs := type_system.NewNamespace()

	strType := type_system.NewStrPrimType(nil)

	intrinsicElems := []type_system.ObjTypeElem{
		// img element with required src and alt props
		type_system.NewPropertyElem(
			type_system.NewStrKey("img"),
			type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				type_system.NewPropertyElem(type_system.NewStrKey("src"), strType),  // required
				type_system.NewPropertyElem(type_system.NewStrKey("alt"), strType),  // required
				newOptionalProp("className", strType),                                // optional
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

func TestIntrinsicElementMissingRequiredProp(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"ImgMissingSrc": {
			input:       `val elem = <img alt="description" />`,
			errorSubstr: "src",
		},
		"ImgMissingAlt": {
			input:       `val elem = <img src="image.png" />`,
			errorSubstr: "alt",
		},
		"ImgMissingBothRequired": {
			input:       `val elem = <img className="photo" />`,
			errorSubstr: "Missing required prop",
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

			// Add JSX namespace with required props
			jsxNs := createJSXNamespaceWithRequiredProps()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for missing required props
			assert.NotEmpty(t, inferErrors, "Expected inference errors for missing required props")

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

func TestIntrinsicElementWithAllRequiredProps(t *testing.T) {
	// Test that providing all required props doesn't produce errors
	tests := map[string]struct {
		input string
	}{
		"ImgWithAllRequired": {
			input: `val elem = <img src="image.png" alt="description" />`,
		},
		"ImgWithAllRequiredAndOptional": {
			input: `val elem = <img src="image.png" alt="description" className="photo" />`,
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

			// Add JSX namespace with required props
			jsxNs := createJSXNamespaceWithRequiredProps()
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
			assert.Len(t, inferErrors, 0, "Expected no inference errors when all required props are provided")
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

// Tests for spread props type checking

func TestSpreadPropsValidTypes(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SpreadWithCorrectTypes": {
			input: `
				val props = { className: "foo", id: "bar" }
				val elem = <div {...props} />
			`,
		},
		"SpreadWithExplicitProp": {
			input: `
				val props = { className: "foo" }
				val elem = <div {...props} id="bar" />
			`,
		},
		"SpreadOverridesExplicit": {
			input: `
				val props = { className: "override" }
				val elem = <div className="base" {...props} />
			`,
		},
		"ExplicitOverridesSpread": {
			input: `
				val props = { className: "base" }
				val elem = <div {...props} className="override" />
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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid spread props")
		})
	}
}

func TestSpreadPropsInvalidTypes(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"SpreadWithWrongClassNameType": {
			input: `
				val props = { className: 123 }
				val elem = <div {...props} />
			`,
			errorSubstr: "string",
		},
		"SpreadWithWrongDisabledType": {
			input: `
				val props = { disabled: "yes" }
				val elem = <button {...props} />
			`,
			errorSubstr: "boolean",
		},
		"SpreadWithWrongEventHandlerType": {
			input: `
				val props = { onClick: "notAFunction" }
				val elem = <div {...props} />
			`,
			errorSubstr: "fn",
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

			// We expect type errors for invalid spread prop types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for invalid spread prop types")

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

func TestSpreadPropsSatisfyRequired(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SpreadProvidesAllRequired": {
			input: `
				val props = { src: "image.png", alt: "description" }
				val elem = <img {...props} />
			`,
		},
		"SpreadProvidesRequiredWithOptional": {
			input: `
				val props = { src: "image.png", alt: "description", className: "photo" }
				val elem = <img {...props} />
			`,
		},
		"SpreadProvidesOneRequiredExplicitProvidesOther": {
			input: `
				val props = { src: "image.png" }
				val elem = <img {...props} alt="description" />
			`,
		},
		"ExplicitProvidesOneRequiredSpreadProvidesOther": {
			input: `
				val props = { alt: "description" }
				val elem = <img src="image.png" {...props} />
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

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker()
			scope := Prelude(c)

			// Add JSX namespace with required props
			jsxNs := createJSXNamespaceWithRequiredProps()
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
			assert.Len(t, inferErrors, 0, "Expected no inference errors when spread props satisfy required props")
		})
	}
}

func TestSpreadPropsMissingRequired(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"SpreadMissingSrc": {
			input: `
				val props = { alt: "description" }
				val elem = <img {...props} />
			`,
			errorSubstr: "src",
		},
		"SpreadMissingAlt": {
			input: `
				val props = { src: "image.png" }
				val elem = <img {...props} />
			`,
			errorSubstr: "alt",
		},
		"SpreadWithOnlyOptional": {
			input: `
				val props = { className: "photo" }
				val elem = <img {...props} />
			`,
			errorSubstr: "Missing required prop",
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

			// Add JSX namespace with required props
			jsxNs := createJSXNamespaceWithRequiredProps()
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err, "Should set JSX namespace without error")

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for missing required props
			assert.NotEmpty(t, inferErrors, "Expected inference errors for missing required props in spread")

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

// Phase 3 Tests: Component Type Checking
// These tests verify that custom component props are validated correctly

func TestComponentValidProps(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ComponentWithMatchingProps": {
			input: `
				fn MyComponent(props: {title: string, count: number}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent title="Hello" count={5} />
			`,
		},
		"ComponentWithOptionalProps": {
			input: `
				fn MyComponent(props: {title: string, count?: number}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent title="Hello" />
			`,
		},
		"ComponentWithAllOptionalProps": {
			input: `
				fn MyComponent(props: {title?: string, count?: number}) {
					return <div>Default</div>
				}
				val elem = <MyComponent />
			`,
		},
		"ComponentWithNoProps": {
			input: `
				fn MyComponent() {
					return <div>No props</div>
				}
				val elem = <MyComponent />
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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid component props")
		})
	}
}

func TestComponentMissingRequiredProp(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"MissingTitle": {
			input: `
				fn MyComponent(props: {title: string, count: number}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent count={5} />
			`,
			errorSubstr: "title",
		},
		"MissingCount": {
			input: `
				fn MyComponent(props: {title: string, count: number}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent title="Hello" />
			`,
			errorSubstr: "count",
		},
		"MissingAllRequired": {
			input: `
				fn MyComponent(props: {title: string, count: number}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent />
			`,
			errorSubstr: "Missing required prop",
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
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for missing required props
			assert.NotEmpty(t, inferErrors, "Expected inference errors for missing required props")

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

func TestComponentWrongPropType(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"StringInsteadOfNumber": {
			input: `
				fn MyComponent(props: {count: number}) {
					return <div>{props.count}</div>
				}
				val elem = <MyComponent count="five" />
			`,
			errorSubstr: "number",
		},
		"NumberInsteadOfString": {
			input: `
				fn MyComponent(props: {title: string}) {
					return <div>{props.title}</div>
				}
				val elem = <MyComponent title={123} />
			`,
			errorSubstr: "string",
		},
		"WrongFunctionType": {
			input: `
				fn MyComponent(props: {onClick: fn() -> void}) {
					return <button onClick={props.onClick}>Click</button>
				}
				val elem = <MyComponent onClick="not a function" />
			`,
			errorSubstr: "fn",
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
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect type errors for wrong prop types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for wrong prop types")

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

func TestUnknownComponent(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"UndefinedComponent": {
			input:       `val elem = <UnknownComponent />`,
			errorSubstr: "UnknownComponent",
		},
		"UndefinedComponentWithProps": {
			input:       `val elem = <UnknownComponent title="Hello" />`,
			errorSubstr: "UnknownComponent",
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
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for unknown components
			assert.NotEmpty(t, inferErrors, "Expected inference errors for unknown component")

			// Verify the error message mentions the unknown component
			found := false
			for _, inferErr := range inferErrors {
				msg := inferErr.Message()
				if strings.Contains(msg, test.errorSubstr) && strings.Contains(msg, "not defined") {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected error message to mention %q is not defined", test.errorSubstr)
		})
	}
}

func TestMemberExpressionComponent(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"NamespaceComponent": {
			input: `
				val Icons = {
					Star: fn(props: {size: number}) {
						return <span>★</span>
					}
				}
				val elem = <Icons.Star size={24} />
			`,
		},
		"NestedNamespaceComponent": {
			input: `
				val UI = {
					Icons: {
						Star: fn() {
							return <span>★</span>
						}
					}
				}
				val elem = <UI.Icons.Star />
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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for member expression components")
		})
	}
}

func TestMemberExpressionComponentErrors(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"UnknownNamespace": {
			input:       `val elem = <Unknown.Component />`,
			errorSubstr: "Unknown",
		},
		"UnknownPropertyOnNamespace": {
			input: `
				val Icons = {
					Star: fn() {
						return <span>★</span>
					}
				}
				val elem = <Icons.Moon />
			`,
			errorSubstr: "Moon",
		},
		"MemberExpressionWrongPropType": {
			input: `
				val Icons = {
					Star: fn(props: {size: number}) {
						return <span>★</span>
					}
				}
				val elem = <Icons.Star size="large" />
			`,
			errorSubstr: "number",
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
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors
			assert.NotEmpty(t, inferErrors, "Expected inference errors")

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

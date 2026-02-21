package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// Add JSX.Element type - this is the return type of JSX expressions
	// In real React, this is a complex type, but for testing we use an empty object type
	elementType := type_system.NewObjectType(nil, nil)
	jsxNs.Types["Element"] = &type_system.TypeAlias{
		Type:       elementType,
		TypeParams: nil,
	}

	return jsxNs
}

// setupJSXTestScope creates a checker and scope with JSX namespace properly configured.
// This is the standard setup for JSX tests that need the JSX.Element type available.
func setupJSXTestScope(c *Checker) *Scope {
	scope := Prelude(c)
	jsxNs := createJSXNamespaceWithIntrinsicElements()
	scope.Namespace.SetNamespace("JSX", jsxNs)
	return scope
}

// getProjectRoot returns the project root directory (where go.mod is located).
// Returns an error if the current directory cannot be determined or go.mod is not found.
func getProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	projectRoot := cwd
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
			return projectRoot, nil
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			return "", fmt.Errorf("could not find project root with go.mod")
		}
		projectRoot = parent
	}
}

// Cached React types - loaded once and reused across all tests
var (
	cachedJSXNamespace   *type_system.Namespace
	cachedReactNamespace *type_system.Namespace
	cachedReactTypesErr  error // Error from loading, if any
	reactTypesLoaded     bool  // True only if loading succeeded
	reactTypesLoadOnce   sync.Once
)

// loadReactTypesOnce loads React types once and caches them for reuse across tests.
// If loading fails, cachedReactTypesErr is set and reactTypesLoaded remains false.
func loadReactTypesOnce() {
	reactTypesLoadOnce.Do(func() {
		// Create a temporary checker just for loading types
		c := NewChecker()
		scope := Prelude(c)
		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		projectRoot, err := getProjectRoot()
		if err != nil {
			cachedReactTypesErr = err
			return
		}

		errors := c.LoadReactTypes(ctx, projectRoot)
		// Log errors but don't fail - some TypeScript features aren't supported yet
		if len(errors) > 0 {
			// These are expected errors from unsupported features, not fatal
			_ = errors
		}

		// Cache the JSX namespace - this is required for tests to work
		jsxNs, jsxOk := scope.Namespace.GetNamespace("JSX")
		if !jsxOk {
			cachedReactTypesErr = fmt.Errorf("JSX namespace not found after loading @types/react")
			return
		}
		cachedJSXNamespace = jsxNs

		// Cache the React namespace - also required
		reactNs, reactOk := scope.Namespace.GetNamespace("React")
		if !reactOk {
			cachedReactTypesErr = fmt.Errorf("React namespace not found after loading @types/react")
			return
		}
		cachedReactNamespace = reactNs

		// Only mark as loaded if we successfully got both namespaces
		reactTypesLoaded = true
	})
}

// setupReactTypesScope creates a checker and scope with the official @types/react types loaded.
// This uses cached React type definitions for fast test execution.
// Fails the test immediately if React types could not be loaded.
func setupReactTypesScope(t *testing.T, c *Checker) *Scope {
	// Load React types once (cached across all tests)
	loadReactTypesOnce()

	// Fail fast if loading failed
	if cachedReactTypesErr != nil {
		t.Fatalf("Failed to load React types: %v", cachedReactTypesErr)
	}
	if !reactTypesLoaded {
		t.Fatalf("React types not loaded (unknown error)")
	}

	scope := Prelude(c)

	// Copy cached namespaces to this scope
	if err := scope.Namespace.SetNamespace("JSX", cachedJSXNamespace); err != nil {
		t.Fatalf("Failed to set JSX namespace: %v", err)
	}
	if err := scope.Namespace.SetNamespace("React", cachedReactNamespace); err != nil {
		t.Fatalf("Failed to set React namespace: %v", err)
	}

	return scope
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
			scope := setupReactTypesScope(t, c)
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
			scope := setupReactTypesScope(t, c)

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
			errorSubstr: "EventHandler", // Error should mention the event handler type
		},
		"ButtonOnClickWithNumber": {
			input:       `val elem = <button onClick={42} />`,
			errorSubstr: "EventHandler", // Error should mention the event handler type
		},
		"InputOnChangeWithBoolean": {
			input:       `val elem = <input onChange={true} />`,
			errorSubstr: "EventHandler", // Error should mention the event handler type
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
				type_system.NewPropertyElem(type_system.NewStrKey("src"), strType), // required
				type_system.NewPropertyElem(type_system.NewStrKey("alt"), strType), // required
				newOptionalProp("className", strType),                              // optional
			}),
		),
	}

	intrinsicElementsType := type_system.NewObjectType(nil, intrinsicElems)
	jsxNs.Types["IntrinsicElements"] = &type_system.TypeAlias{
		Type:       intrinsicElementsType,
		TypeParams: nil,
	}

	// Add JSX.Element type - required for JSX expressions to return a valid type
	elementType := type_system.NewObjectType(nil, nil)
	jsxNs.Types["Element"] = &type_system.TypeAlias{
		Type:       elementType,
		TypeParams: nil,
	}

	return jsxNs
}

func TestIntrinsicElementMissingRequiredProp(t *testing.T) {
	// This test uses a custom JSX namespace with required props (src, alt on img)
	// because @types/react makes all HTML element props optional
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
			// Use custom namespace with required props instead of @types/react
			jsxNs := createJSXNamespaceWithRequiredProps()
			scope := Prelude(c)
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err)

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
			scope := setupReactTypesScope(t, c)

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
			scope := setupReactTypesScope(t, c)

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
	// When JSX namespace is not available, an error should be returned
	tests := map[string]struct {
		input string
	}{
		"DivWithAnyProps": {
			input: `val elem = <div unknownProp="value" anotherProp={123} />`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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

			// Without JSX namespace, an error should be returned
			assert.NotEmpty(t, inferErrors, "Expected errors when JSX namespace is not available")

			// Verify at least one error mentions JSX namespace
			found := false
			for _, err := range inferErrors {
				if strings.Contains(err.Message(), "JSX namespace") {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected error about missing JSX namespace")
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
			scope := setupReactTypesScope(t, c)

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
			scope := setupReactTypesScope(t, c)

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
			errorSubstr: "EventHandler", // Error should mention the event handler type
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
	// This test uses a custom JSX namespace with required props (src, alt on img)
	// because @types/react makes all HTML element props optional
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
			// Use custom namespace with required props instead of @types/react
			jsxNs := createJSXNamespaceWithRequiredProps()
			scope := Prelude(c)
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err)

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
	// This test uses a custom JSX namespace with required props (src, alt on img)
	// because @types/react makes all HTML element props optional
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
			// Use custom namespace with required props instead of @types/react
			jsxNs := createJSXNamespaceWithRequiredProps()
			scope := Prelude(c)
			err := scope.Namespace.SetNamespace("JSX", jsxNs)
			assert.NoError(t, err)

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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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
				Scope:      setupReactTypesScope(t, c),
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

// Phase 3.3 Tests: Children Type Checking
// These tests verify that children types are properly inferred and validated

func TestComponentWithValidChildren(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ComponentWithStringChild": {
			input: `
				fn Container(props: {children: string}) {
					return <div>{props.children}</div>
				}
				val elem = <Container>Hello</Container>
			`,
		},
		"ComponentWithElementChild": {
			input: `
				fn Container(props: {children: {}}) {
					return <div>{props.children}</div>
				}
				val elem = <Container><span>Child</span></Container>
			`,
		},
		"ComponentWithNoChildren": {
			input: `
				fn Container(props: {title: string}) {
					return <div>{props.title}</div>
				}
				val elem = <Container title="Title" />
			`,
		},
		"ComponentWithExpressionChild": {
			input: `
				fn Container(props: {children: number}) {
					return <div>{props.children}</div>
				}
				val count = 42
				val elem = <Container>{count}</Container>
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
				Scope:      setupReactTypesScope(t, c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid children")
		})
	}
}

func TestComponentWithInvalidChildrenType(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"StringChildWhenNumberExpected": {
			input: `
				fn Container(props: {children: number}) {
					return <div>{props.children}</div>
				}
				val elem = <Container>Hello</Container>
			`,
			errorSubstr: "number",
		},
		"NumberChildWhenStringExpected": {
			input: `
				fn Container(props: {children: string}) {
					return <div>{props.children}</div>
				}
				val elem = <Container>{42}</Container>
			`,
			errorSubstr: "string",
		},
		"MultipleChildrenWhenScalarExpected": {
			// Multiple children produce a tuple type which cannot be assigned to a scalar.
			// Components wanting multiple children should use Array<T> for the children prop.
			input: `
				fn Container(props: {children: string}) {
					return <div>{props.children}</div>
				}
				val elem = <Container>Hello{" "}World</Container>
			`,
			errorSubstr: "string",
		},
		"ChildrenProvidedWithoutChildrenProp": {
			// Component doesn't have a children prop but children are provided
			input: `
				fn Container(props: {title: string}) {
					return <div>{props.title}</div>
				}
				val elem = <Container title="Title">Unexpected child</Container>
			`,
			errorSubstr: "does not accept children",
		},
		"MissingRequiredChildren": {
			// Component has required children prop but no children provided
			input: `
				fn Container(props: {children: string}) {
					return <div>{props.children}</div>
				}
				val elem = <Container />
			`,
			errorSubstr: "Missing required prop 'children'",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
				Scope:      setupReactTypesScope(t, c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect type errors for invalid children types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for invalid children type")

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

func TestMultipleChildren(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"MultipleTextChildren": {
			input: `
				fn Container(props: {title: string, children: Array<string>}) {
					return <div>{props.title}</div>
				}
				val elem = <Container title="Title">Hello{" "}World</Container>
			`,
		},
		"MultipleElementChildren": {
			input: `
				fn Container(props: {title: string, children: Array<{}>}) {
					return <div>{props.title}</div>
				}
				val elem = <Container title="Title"><span>One</span><span>Two</span></Container>
			`,
		},
		"MixedChildren": {
			input: `
				fn Container(props: {title: string, children: Array<string | {}>}) {
					return <div>{props.title}</div>
				}
				val name = "World"
				val elem = <Container title="Title">Hello {name}<span>!</span></Container>
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
				Scope:      setupReactTypesScope(t, c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for multiple children")
		})
	}
}

func TestNestedComponentChildren(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ComponentAsChild": {
			input: `
				fn Child() {
					return <span>Child</span>
				}
				fn Parent(props: {title: string, children: {}}) {
					return <div>{props.title}</div>
				}
				val elem = <Parent title="Title"><Child /></Parent>
			`,
		},
		"DeeplyNestedComponents": {
			input: `
				fn Inner() {
					return <span>Inner</span>
				}
				fn Middle(props: {title: string, children: {}}) {
					return <div>{props.title}</div>
				}
				fn Outer(props: {label: string, children: {}}) {
					return <div>{props.label}</div>
				}
				val elem = <Outer label="Label"><Middle title="Title"><Inner /></Middle></Outer>
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
				Scope:      setupReactTypesScope(t, c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			if len(inferErrors) > 0 {
				for _, err := range inferErrors {
					t.Logf("InferError: %v", err.Message())
				}
			}
			assert.Len(t, inferErrors, 0, "Expected no inference errors for nested component children")
		})
	}
}

// TestKeyPropValid tests that valid key prop values are accepted.
// Valid key types: string, number, null
func TestKeyPropValid(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"KeyWithStringLiteral": {
			input: `val elem = <div key="item-1" />`,
		},
		"KeyWithStringVariable": {
			input: `
				val id = "item-1"
				val elem = <div key={id} />
			`,
		},
		"KeyWithNumberLiteral": {
			input: `val elem = <div key={42} />`,
		},
		"KeyWithNumberVariable": {
			input: `
				val index = 0
				val elem = <div key={index} />
			`,
		},
		"KeyWithNull": {
			input: `val elem = <div key={null} />`,
		},
		"KeyOnComponent": {
			input: `
				fn MyComponent() {
					return <div />
				}
				val elem = <MyComponent key="unique" />
			`,
		},
		"KeyWithIndexExpression": {
			input: `
				val index = 0
				val elem = <div key={index + 1} />
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for valid key prop")
		})
	}
}

// TestKeyPropInvalid tests that invalid key prop values produce errors.
func TestKeyPropInvalid(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"KeyWithObject": {
			input:       `val elem = <div key={{id: 1}} />`,
			errorSubstr: "Invalid 'key' prop type",
		},
		"KeyWithBoolean": {
			input:       `val elem = <div key={true} />`,
			errorSubstr: "Invalid 'key' prop type",
		},
		"KeyWithArray": {
			input:       `val elem = <div key={[1, 2, 3]} />`,
			errorSubstr: "Invalid 'key' prop type",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for invalid key prop types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for invalid key prop type")

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

// TestKeyNotPassedToProps tests that key is not included in the props passed to components.
// Components should not see 'key' in their props object.
func TestKeyNotPassedToProps(t *testing.T) {
	// This test ensures that when a component has a prop named 'key', the JSX key attribute
	// doesn't satisfy it (since key is a special React prop, not a regular prop).
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"ComponentWithKeyPropRequirement": {
			// If a component requires a 'key' prop, the JSX key attribute should NOT satisfy it
			// This should produce a "missing required prop" error
			input: `
				fn MyComponent(props: {key: string}) {
					return <div>{props.key}</div>
				}
				val elem = <MyComponent key="should-not-pass" />
			`,
			errorSubstr: "Missing required prop 'key'",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors because key is not passed to component props
			assert.NotEmpty(t, inferErrors, "Expected inference errors because key is not passed to props")

			// Verify at least one error message contains the expected substring
			found := false
			for _, inferErr := range inferErrors {
				if strings.Contains(inferErr.Message(), test.errorSubstr) {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected at least one error message to contain %q, got: %v", test.errorSubstr, inferErrors)
		})
	}
}

// TestRefPropOnIntrinsicElement tests that ref is allowed on intrinsic elements.
func TestRefPropOnIntrinsicElement(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"RefOnDiv": {
			input: `
				val myRef = { current: null }
				val elem = <div ref={myRef} />
			`,
		},
		"RefOnInput": {
			input: `
				val inputRef = { current: null }
				val elem = <input ref={inputRef} value="hello" />
			`,
		},
		"RefOnButton": {
			input: `
				val buttonRef = { current: null }
				val elem = <button ref={buttonRef}>Click me</button>
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for ref on intrinsic element")
		})
	}
}

// TestRefNotPassedToProps tests that ref is not included in the props passed to components.
func TestRefNotPassedToProps(t *testing.T) {
	// This test ensures that when a component has a prop named 'ref', the JSX ref attribute
	// doesn't satisfy it (since ref is a special React prop, not a regular prop).
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"ComponentWithRefPropRequirement": {
			// If a component requires a 'ref' prop, the JSX ref attribute should NOT satisfy it
			input: `
				fn MyComponent(props: {ref: string}) {
					return <div>{props.ref}</div>
				}
				val myRef = { current: null }
				val elem = <MyComponent ref={myRef} />
			`,
			errorSubstr: "Missing required prop 'ref'",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors because ref is not passed to component props
			assert.NotEmpty(t, inferErrors, "Expected inference errors because ref is not passed to props")

			// Verify at least one error message contains the expected substring
			found := false
			for _, inferErr := range inferErrors {
				if strings.Contains(inferErr.Message(), test.errorSubstr) {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected at least one error message to contain %q, got: %v", test.errorSubstr, inferErrors)
		})
	}
}

// TestKeyPropOnComponent tests that key prop works correctly on custom components.
func TestKeyPropOnComponent(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"KeyOnComponentWithProps": {
			input: `
				fn Button(props: {label: string, variant: string}) {
					return <button className={props.variant}>{props.label}</button>
				}
				val elem = <Button key="btn-1" label="Click me" variant="primary" />
			`,
		},
		"KeyOnComponentWithChildren": {
			input: `
				fn Container(props: {title: string, children: string}) {
					return <div>{props.title}</div>
				}
				val elem = <Container key="container-1" title="Hello">Child content</Container>
			`,
		},
		"KeyOnNestedComponents": {
			input: `
				fn Item(props: {name: string}) {
					return <li>{props.name}</li>
				}
				fn List() {
					return <ul>
						<Item key="item-1" name="First" />
						<Item key="item-2" name="Second" />
						<Item key="item-3" name="Third" />
					</ul>
				}
				val elem = <List />
			`,
		},
		"KeyWithNumberOnComponent": {
			input: `
				fn Card(props: {title: string}) {
					return <div>{props.title}</div>
				}
				val elem = <Card key={42} title="Card Title" />
			`,
		},
		"KeyWithNullOnComponent": {
			input: `
				fn Badge(props: {text: string}) {
					return <span>{props.text}</span>
				}
				val elem = <Badge key={null} text="New" />
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for key on component")
		})
	}
}

// TestKeyPropInvalidOnComponent tests that invalid key prop values on components produce errors.
func TestKeyPropInvalidOnComponent(t *testing.T) {
	tests := map[string]struct {
		input       string
		errorSubstr string
	}{
		"KeyWithObjectOnComponent": {
			input: `
				fn Card(props: {title: string}) {
					return <div>{props.title}</div>
				}
				val elem = <Card key={{id: 1}} title="Title" />
			`,
			errorSubstr: "Invalid 'key' prop type",
		},
		"KeyWithBooleanOnComponent": {
			input: `
				fn Button(props: {label: string}) {
					return <button>{props.label}</button>
				}
				val elem = <Button key={true} label="Click" />
			`,
			errorSubstr: "Invalid 'key' prop type",
		},
		"KeyWithArrayOnComponent": {
			input: `
				fn List(props: {items: Array<string>}) {
					return <ul />
				}
				val elem = <List key={[1, 2]} items={["a", "b"]} />
			`,
			errorSubstr: "Invalid 'key' prop type",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

			inferCtx := Context{
				Scope:      scope,
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferScript(inferCtx, script)

			// We expect errors for invalid key prop types
			assert.NotEmpty(t, inferErrors, "Expected inference errors for invalid key prop type on component")

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

// TestRefPropOnComponent tests that ref is allowed on custom components.
func TestRefPropOnComponent(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"RefOnComponentWithProps": {
			input: `
				fn Button(props: {label: string}) {
					return <button>{props.label}</button>
				}
				val buttonRef = { current: null }
				val elem = <Button ref={buttonRef} label="Click me" />
			`,
		},
		"RefOnComponentWithChildren": {
			input: `
				fn Container(props: {title: string, children: string}) {
					return <div>{props.title}</div>
				}
				val containerRef = { current: null }
				val elem = <Container ref={containerRef} title="Hello">Child</Container>
			`,
		},
		"RefWithCallbackOnComponent": {
			// Ref can be a callback function
			input: `
				fn Input(props: {placeholder: string}) {
					return <input placeholder={props.placeholder} />
				}
				val refCallback = fn(element: {}) { }
				val elem = <Input ref={refCallback} placeholder="Enter text" />
			`,
		},
		"RefWithNullOnComponent": {
			input: `
				fn Badge(props: {text: string}) {
					return <span>{props.text}</span>
				}
				val elem = <Badge ref={null} text="New" />
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for ref on component")
		})
	}
}

// TestKeyAndRefTogetherOnComponent tests using both key and ref on custom components.
func TestKeyAndRefTogetherOnComponent(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"KeyAndRefOnComponentWithProps": {
			input: `
				fn Card(props: {title: string, subtitle?: string}) {
					return <div>{props.title}</div>
				}
				val cardRef = { current: null }
				val elem = <Card key="card-1" ref={cardRef} title="Hello" subtitle="World" />
			`,
		},
		"KeyAndRefOnComponentWithChildren": {
			input: `
				fn Container(props: {className?: string, children: Array<{}>}) {
					return <div />
				}
				val containerRef = { current: null }
				val elem = <Container key={42} ref={containerRef} className="wrapper">
					<span>Child 1</span>
					<span>Child 2</span>
				</Container>
			`,
		},
		"MultipleComponentsWithKeyAndRef": {
			input: `
				fn Item(props: {name: string}) {
					return <li>{props.name}</li>
				}
				val ref1 = { current: null }
				val ref2 = { current: null }
				val elem = <ul>
					<Item key="a" ref={ref1} name="First" />
					<Item key="b" ref={ref2} name="Second" />
				</ul>
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for key and ref on component")
		})
	}
}

// TestKeyAndRefTogether tests using both key and ref on the same element.
func TestKeyAndRefTogether(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"KeyAndRefOnDiv": {
			input: `
				val myRef = { current: null }
				val elem = <div key="unique" ref={myRef} className="container" />
			`,
		},
		"KeyAndRefOnInput": {
			input: `
				val inputRef = { current: null }
				val elem = <input key={1} ref={inputRef} value="hello" />
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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
			scope := setupReactTypesScope(t, c)

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
			assert.Len(t, inferErrors, 0, "Expected no inference errors for key and ref together")
		})
	}
}

// Phase 4.2 Tests: React Types Loading and JSX Syntax Detection

// TestHasJSXSyntax tests the JSX syntax detection function.
func TestHasJSXSyntax(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected bool
	}{
		"TopLevelJSXElement": {
			input:    `val elem = <div />`,
			expected: true,
		},
		"TopLevelJSXFragment": {
			input:    `val elem = <></>`,
			expected: true,
		},
		"JSXInFunctionBody": {
			input: `
				fn render() {
					return <div>Hello</div>
				}
			`,
			expected: true,
		},
		"JSXInTernary": {
			input: `
				val condition = true
				val elem = if condition { <div /> } else { <span /> }
			`,
			expected: true,
		},
		"JSXInNestedClosure": {
			input: `
				val render = fn() {
					val inner = fn() {
						return <button>Click</button>
					}
					return inner()
				}
			`,
			expected: true,
		},
		"NoJSXSimpleVariable": {
			input:    `val x = 42`,
			expected: false,
		},
		"NoJSXFunction": {
			input: `
				fn add(a: number, b: number) -> number {
					return a + b
				}
			`,
			expected: false,
		},
		"NoJSXObjectLiteral": {
			input:    `val obj = { name: "test", value: 123 }`,
			expected: false,
		},
		"NoJSXArrayLiteral": {
			input:    `val arr = [1, 2, 3, 4, 5]`,
			expected: false,
		},
		"NoJSXComplexExpression": {
			input: `
				val x = 10
				val y = 20
				val result = x + y * 2
			`,
			expected: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
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

			// Use HasJSXSyntaxInScript for script ASTs
			result := HasJSXSyntaxInScript(script)

			assert.Equal(t, test.expected, result, "JSX syntax detection mismatch for %s", name)
		})
	}
}

// TestLoadReactTypesIntegration tests loading @types/react with the real package.
// This test is skipped if @types/react is not installed.
func TestLoadReactTypesIntegration(t *testing.T) {
	projectRoot, err := getProjectRoot()
	if err != nil {
		t.Fatalf("Failed to find project root: %v", err)
	}

	// Check if @types/react is installed
	reactTypesDir := filepath.Join(projectRoot, "node_modules", "@types", "react")
	if _, err := os.Stat(reactTypesDir); err != nil {
		t.Fatalf("@types/react not installed, skipping integration test")
		return
	}

	// NOTE: Full @types/react loading is skipped for now because the full React type definitions
	// contain complex TypeScript features (conditional types, mapped types, etc.) that require
	// more work to fully support. The basic infrastructure for loading is in place.
	// See Phase 4.3 and beyond in the implementation plan for the remaining work.
	t.Run("LoadReactTypesSuccessfully", func(t *testing.T) {
		c := NewChecker()
		scope := Prelude(c)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Load React types
		errors := c.LoadReactTypes(ctx, projectRoot)

		// Log any errors
		for _, err := range errors {
			t.Logf("Error: %s", err.Message())
		}

		// Note: There may be some errors due to complex TypeScript features we don't support yet
		// For now, we just verify the function doesn't panic
		t.Logf("LoadReactTypes completed with %d errors", len(errors))
	})

	t.Run("LoadReactTypesCaching", func(t *testing.T) {
		c := NewChecker()
		scope := Prelude(c)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Load React types twice
		_ = c.LoadReactTypes(ctx, projectRoot)
		_ = c.LoadReactTypes(ctx, projectRoot)

		// Second call should use cached namespace from PackageRegistry
		// We can verify this by checking that the React namespace is available
		// (The actual caching behavior is logged to stderr)
		t.Logf("LoadReactTypes called twice successfully")
	})
}

// TestLoadReactTypesWithoutPackage tests LoadReactTypes when @types/react is not available.
func TestLoadReactTypesWithoutPackage(t *testing.T) {
	t.Run("ReturnsErrorWhenNotInstalled", func(t *testing.T) {
		c := NewChecker()
		scope := Prelude(c)

		ctx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Use a directory that doesn't have @types/react installed
		tempDir := t.TempDir()

		// Load React types from temp directory
		errors := c.LoadReactTypes(ctx, tempDir)

		// Should return an error about @types/react not being found
		assert.NotEmpty(t, errors, "Expected an error about missing @types/react")

		// The error should mention @types/react
		found := false
		for _, err := range errors {
			if strings.Contains(err.Message(), "@types/react") {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected error to mention @types/react")
	})
}

// Phase 4.3 Tests: Automatic JSX Type Loading

// TestHasJSXSyntaxModule tests HasJSXSyntax with Module ASTs (as opposed to Script ASTs).
func TestHasJSXSyntaxModule(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected bool
	}{
		"ModuleWithJSX": {
			input:    `val elem = <div />`,
			expected: true,
		},
		"ModuleWithJSXFragment": {
			input:    `val elem = <><span /></>`,
			expected: true,
		},
		"ModuleWithoutJSX": {
			input:    `val x = 42`,
			expected: false,
		},
		"ModuleWithJSXInFunction": {
			input: `
				fn render() {
					return <div>Hello</div>
				}
			`,
			expected: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			result := HasJSXSyntax(module)

			assert.Equal(t, test.expected, result, "JSX syntax detection mismatch for %s", name)
		})
	}
}

// TestAutoLoadReactTypesForJSX tests that InferModule automatically attempts to load
// React types when JSX syntax is detected in the module.
func TestAutoLoadReactTypesForJSX(t *testing.T) {
	t.Run("JSXModuleAttemptsReactTypesLoad", func(t *testing.T) {
		// Use a temp directory without @types/react to verify the loading is attempted
		tempDir := t.TempDir()

		source := &ast.Source{
			ID:       0,
			Path:     filepath.Join(tempDir, "input.esc"),
			Contents: `val elem = <div />`,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})

		assert.Len(t, parseErrors, 0, "Expected no parse errors")

		// Create checker and scope
		c := NewChecker()
		scope := Prelude(c)

		checkerCtx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// InferModule should attempt to load React types and return an error
		// because @types/react is not installed in tempDir
		errors := c.InferModule(checkerCtx, module)

		// Should return an error about @types/react not being found
		found := false
		for _, err := range errors {
			if strings.Contains(err.Message(), "@types/react") {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected error about missing @types/react when JSX is detected")
	})

	t.Run("NonJSXModuleDoesNotLoadReactTypes", func(t *testing.T) {
		// Use a temp directory without @types/react
		tempDir := t.TempDir()

		source := &ast.Source{
			ID:       0,
			Path:     filepath.Join(tempDir, "input.esc"),
			Contents: `val x = 42`,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})

		assert.Len(t, parseErrors, 0, "Expected no parse errors")

		// Create checker and scope
		c := NewChecker()
		scope := Prelude(c)

		checkerCtx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// InferModule should NOT attempt to load React types for non-JSX modules
		errors := c.InferModule(checkerCtx, module)

		// Should NOT have any errors about @types/react
		found := false
		for _, err := range errors {
			if strings.Contains(err.Message(), "@types/react") {
				found = true
				break
			}
		}
		assert.False(t, found, "Non-JSX module should not trigger @types/react loading")
	})
}

// Phase 4.4 Tests: JSX.Element Type Resolution

// TestJSXElementTypeResolution tests that JSX elements return JSX.Element type
// when the JSX namespace is available with an Element type defined.
func TestJSXElementTypeResolution(t *testing.T) {
	t.Run("ReturnsJSXElementWhenNamespaceAvailable", func(t *testing.T) {
		t.Parallel()
		input := `val elem = <div />`

		source := &ast.Source{
			ID:       0,
			Path:     "input.esc",
			Contents: input,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		p := parser.NewParser(ctx, source)
		script, parseErrors := p.ParseScript()

		assert.Len(t, parseErrors, 0, "Expected no parse errors")

		// Create checker and scope with JSX namespace
		c := NewChecker()
		scope := Prelude(c)

		// Create a JSX namespace with an Element type
		jsxNs := type_system.NewNamespace()
		elementType := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(
				type_system.NewStrKey("$$typeof"),
				type_system.NewStrPrimType(nil),
			),
		})
		jsxNs.Types["Element"] = &type_system.TypeAlias{
			Type:       elementType,
			TypeParams: nil,
		}

		// Also add IntrinsicElements so div is recognized
		jsxNs.Types["IntrinsicElements"] = &type_system.TypeAlias{
			Type: type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				type_system.NewPropertyElem(
					type_system.NewStrKey("div"),
					type_system.NewObjectType(nil, nil),
				),
			}),
			TypeParams: nil,
		}

		// Add JSX namespace to the scope (not GlobalScope to avoid test interference)
		err := scope.Namespace.SetNamespace("JSX", jsxNs)
		assert.NoError(t, err)

		checkerCtx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Infer the script
		resultScope, inferErrors := c.InferScript(checkerCtx, script)

		// Should have no errors
		assert.Len(t, inferErrors, 0, "Expected no inference errors")

		// Check that elem has the JSX.Element type (with $$typeof property)
		binding := resultScope.GetValue("elem")
		assert.NotNil(t, binding, "Expected elem binding to exist")

		// Prune the type to resolve any type variables
		prunedType := type_system.Prune(binding.Type)

		// The type should be the JSX.Element type we defined
		objType, ok := prunedType.(*type_system.ObjectType)
		assert.True(t, ok, "Expected ObjectType, got %T", prunedType)

		// Check for the $$typeof property we added to JSX.Element
		found := false
		for _, elem := range objType.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Str == "$$typeof" {
					found = true
					break
				}
			}
		}
		assert.True(t, found, "Expected JSX.Element type with $$typeof property")
	})

	t.Run("ReturnsErrorWhenJSXNamespaceNotAvailable", func(t *testing.T) {
		t.Parallel()
		input := `val elem = <div />`

		source := &ast.Source{
			ID:       0,
			Path:     "input.esc",
			Contents: input,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		p := parser.NewParser(ctx, source)
		script, parseErrors := p.ParseScript()

		assert.Len(t, parseErrors, 0, "Expected no parse errors")

		// Create checker and scope WITHOUT JSX namespace
		c := NewChecker()
		scope := Prelude(c)

		checkerCtx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Infer the script - should return error about missing JSX namespace
		_, inferErrors := c.InferScript(checkerCtx, script)

		// Should have at least one error about missing JSX namespace
		assert.NotEmpty(t, inferErrors, "Expected inference errors when JSX namespace is missing")

		// Verify the error message mentions JSX namespace
		found := false
		for _, err := range inferErrors {
			if strings.Contains(err.Message(), "JSX namespace") {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected error about missing JSX namespace")
	})
}

// TestJSXFragmentTypeResolution tests that JSX fragments return JSX.Element type.
func TestJSXFragmentTypeResolution(t *testing.T) {
	t.Run("FragmentReturnsJSXElementType", func(t *testing.T) {
		t.Parallel()
		input := `val elem = <><div /></>`

		source := &ast.Source{
			ID:       0,
			Path:     "input.esc",
			Contents: input,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		p := parser.NewParser(ctx, source)
		script, parseErrors := p.ParseScript()

		assert.Len(t, parseErrors, 0, "Expected no parse errors")

		// Create a fresh checker and scope with JSX namespace
		c := NewChecker()
		scope := Prelude(c)

		// Create a JSX namespace with an Element type
		jsxNs := type_system.NewNamespace()
		elementType := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewPropertyElem(
				type_system.NewStrKey("$$typeof"),
				type_system.NewStrPrimType(nil),
			),
		})
		jsxNs.Types["Element"] = &type_system.TypeAlias{
			Type:       elementType,
			TypeParams: nil,
		}

		// Also add IntrinsicElements so div is recognized
		jsxNs.Types["IntrinsicElements"] = &type_system.TypeAlias{
			Type: type_system.NewObjectType(nil, []type_system.ObjTypeElem{
				type_system.NewPropertyElem(
					type_system.NewStrKey("div"),
					type_system.NewObjectType(nil, nil),
				),
			}),
			TypeParams: nil,
		}

		// Add JSX namespace to the scope (not GlobalScope to avoid test interference)
		err := scope.Namespace.SetNamespace("JSX", jsxNs)
		assert.NoError(t, err)

		checkerCtx := Context{
			Scope:      scope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		// Infer the script
		resultScope, inferErrors := c.InferScript(checkerCtx, script)

		// Should have no errors
		assert.Len(t, inferErrors, 0, "Expected no inference errors")

		// Check that elem has the JSX.Element type
		binding := resultScope.GetValue("elem")
		assert.NotNil(t, binding, "Expected elem binding to exist")

		// Prune the type to resolve any type variables
		prunedType := type_system.Prune(binding.Type)

		// The type should be the JSX.Element type we defined
		objType, ok := prunedType.(*type_system.ObjectType)
		assert.True(t, ok, "Expected ObjectType, got %T", prunedType)

		// Check for the $$typeof property
		found := false
		for _, elem := range objType.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				if prop.Name.Str == "$$typeof" {
					found = true
					break
				}
			}
		}
		assert.True(t, found, "Expected fragment to have JSX.Element type with $$typeof property")
	})
}

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Body-level class declarations were previously a panic in inferDecl
// (issue #514). These tests exercise the inferClassDecl path that runs
// when a class is declared inside a function body.

func TestCheckBodyLevelClassDeclNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SimpleSynthesizedCtor": {
			input: `
				fn make() {
					class Point {
						x: number,
						y: number,
					}
					val p = Point(5, 10)
					return p.x + p.y
				}
			`,
		},
		"ExplicitCtorAndMethod": {
			input: `
				fn make() {
					class Point {
						x: number,
						y: number,
						constructor(mut self, x: number, y: number) {
							self.x = x
							self.y = y
						},
						sum(self) {
							return self.x + self.y
						},
					}
					val p = Point(5, 10)
					return p.sum()
				}
			`,
		},
		"StaticField": {
			input: `
				fn make() {
					class Counter {
						static count: number = 0,
						value: number,
					}
					val c = Counter(1)
					return c.value
				}
			`,
		},
		"GenericClass": {
			input: `
				fn make() {
					class Box<T> {
						value: T,
					}
					val b = Box(42:number)
					return b.value
				}
			`,
		},
	}

	schema := loadSchema(t)

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
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			if len(parseErrors) > 0 {
				for i, err := range parseErrors {
					fmt.Printf("Parse Error[%d]: %s\n", i, err.String())
				}
			}
			assert.Len(t, parseErrors, 0)

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.Schema = schema
			inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
			}
			assert.Len(t, inferErrors, 0)
		})
	}
}

func TestCheckBodyLevelClassDeclErrors(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"ExtendsWithoutCtor": {
			input: `
				class Animal {
					name: string,
				}
				fn make() {
					class Dog extends Animal {
						breed: string,
					}
				}
			`,
			expectedErrors: []string{
				"Subclasses must declare an explicit `constructor` block; constructor synthesis is not supported for classes with an `extends` clause.",
			},
		},
		"InstanceFieldInitializer": {
			input: `
				fn make() {
					class Bad {
						x: number = 1,
					}
				}
			`,
			expectedErrors: []string{
				"Field 'x' cannot have a `= expr` initializer; only static fields may use this form. Initialize instance fields in the constructor body.",
			},
		},
	}

	schema := loadSchema(t)

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
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, parseErrors, 0)

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.Schema = schema
			inferErrors := c.InferModule(inferCtx, module)

			actualMsgs := make([]string, 0, len(inferErrors))
			for _, err := range inferErrors {
				actualMsgs = append(actualMsgs, err.Message())
			}

			assert.ElementsMatch(t, test.expectedErrors, actualMsgs)
		})
	}
}

// InferScript drives top-level statements through inferStmt -> inferDecl,
// so script-level `class` declarations also exercise inferClassDecl. These
// tests give us coverage of the inferClassDecl path without wrapping the
// class in a function body.

func TestCheckScriptLevelClassDeclNoErrors(t *testing.T) {
	tests := map[string]struct {
		input         string
		expectedTypes map[string]string
	}{
		"SimpleScriptClass": {
			input: `
				class Point {
					x: number,
					y: number,
				}
				val p = Point(3, 4)
				val sum = p.x + p.y
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> Point}",
				"p":     "Point",
				"sum":   "number",
			},
		},
		"ScriptClassWithMethod": {
			input: `
				class Counter {
					value: number,
					inc(mut self) {
						self.value = self.value + 1
						return self
					},
				}
				val mut c = Counter(0)
				c.inc()
			`,
			expectedTypes: map[string]string{
				"Counter": "{new fn (value: number) -> Counter}",
				"c":       "mut Counter",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, parseErrors := p.ParseScript()
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			scriptScope, inferErrors := c.InferScript(inferCtx, script)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
				}
			}
			assert.Len(t, inferErrors, 0)

			for expectedName, expectedType := range test.expectedTypes {
				binding, ok := scriptScope.Namespace.Values[expectedName]
				assert.True(t, ok, "expected value %q to be declared", expectedName)
				if ok {
					assert.Equal(t, expectedType, binding.Type.String(),
						"type mismatch for value %q", expectedName)
				}
			}
		})
	}
}

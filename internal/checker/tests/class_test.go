package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassImplements(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"SingleInterface": {
			input: `
				interface Greeter {
					greet(self) -> string,
				}
				class Hello implements Greeter {
					greet(self) -> string { return "hi" }
				}
				val h = Hello()
			`,
			bindingName:  "h",
			expectedType: "Hello",
		},
		"ExtendsAndImplements": {
			input: `
				class Animal {
					name: string,
				}
				interface Runnable {
					run(self) -> string,
				}
				interface Barker {
					bark(self) -> string,
				}
				class Dog extends Animal implements Runnable, Barker {
					constructor(mut self, name: string) { self.name = name },
					run(self) -> string { return "running" },
					bark(self) -> string { return "woof" },
				}
				val d = Dog("Rex")
			`,
			bindingName:  "d",
			expectedType: "Dog",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedType, got,
				"unexpected type for %q", test.bindingName)
		})
	}
}

// TestClassImplementsConformance pins the gap left by #534: a class can
// currently claim `implements I` while omitting members of `I` and the
// checker does not complain. When conformance verification lands (#558),
// remove the t.Skip and this test will start asserting that the
// missing-member case produces an error.
func TestClassImplementsConformance(t *testing.T) {
	t.Skip("conformance check not yet implemented — see #558")

	input := `
		interface Greeter {
			greet(self) -> string,
		}
		class Hello implements Greeter {}
		val h = Hello()
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	inferErrors := c.InferModule(inferCtx, module)
	require.NotEmpty(t, inferErrors,
		"expected a conformance error: Hello does not implement Greeter.greet")
}

// TestDefaultMutabilityFromClass instantiates each class and asserts the
// printed type of the resulting binding. Per #499, a bare constructor call
// always produces an immutable instance — regardless of `mut self` methods
// or the `data` modifier — and the user opts in to mutability at the
// binding pattern (e.g., `val mut c = …`).
func TestDefaultMutabilityFromClass(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"NoMutSelf_DefaultsImmutable": {
			input: `
				class Point {
					x: number,
					y: number,
				}
				val p = Point(5, 10)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"HasMutSelf_DefaultsImmutable": {
			input: `
				class Counter {
					count: number,
					increment(mut self) -> number { return self.count }
				}
				val c = Counter(0)
			`,
			bindingName:  "c",
			expectedType: "Counter",
		},
		"HasMutSelf_MutPatternYieldsMutable": {
			input: `
				class Counter {
					count: number,
					increment(mut self) -> number { return self.count }
				}
				val mut c = Counter(0)
			`,
			bindingName:  "c",
			expectedType: "mut Counter",
		},
		"DataModifier_DefaultsImmutable": {
			input: `
				class Config {
					host: string,
					setHost(mut self, h: string) -> void {}
				}
				val cfg = Config("localhost")
			`,
			bindingName:  "cfg",
			expectedType: "Config",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedType, got,
				"unexpected type for %q", test.bindingName)
		})
	}
}

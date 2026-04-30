package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inferModuleErrors parses and type-checks the given input as a module and
// returns every inference error. Phase 2 tests use this to assert on error
// presence/messages rather than swallowing errors.
func inferModuleErrors(t *testing.T, input string) []Error {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	return c.InferModule(inferCtx, module)
}

// TestInBodyConstructorBasic checks that a class with a single in-body
// constructor type-checks and is callable with the expected arity.
func TestInBodyConstructorBasic(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"NoExtraParams": {
			input: `
				class Foo {
					constructor(mut self) {}
				}
				val f = Foo()
			`,
			bindingName:  "f",
			expectedType: "Foo",
		},
		"WithFieldsAssignedInBody": {
			input: `
				class Point {
					x:: number,
					y:: number,

					constructor(mut self, x: number, y: number) {
						self.x = x
						self.y = y
					}
				}
				val p = Point(1, 2)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"MutBindingYieldsMutInstance": {
			input: `
				class Counter {
					count:: number,

					constructor(mut self, count: number) {
						self.count = count
					},

					increment(mut self) -> number { return self.count }
				}
				val mut c = Counter(0)
			`,
			bindingName:  "c",
			expectedType: "mut Counter",
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

// TestSynthesizedConstructor verifies the §2.7 synthesizer behavior for
// classes with no in-body `constructor` and no primary-ctor head: the
// constructor is derived from the instance fields.
func TestSynthesizedConstructor(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"FieldsInDeclarationOrder": {
			input: `
				class Point {
					x:: number,
					y:: number,
				}
				val p = Point(1, 2)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
		"ReversedDeclarationOrder": {
			input: `
				class P {
					y:: number,
					x:: number,
				}
				val p = P(10, 20)
			`,
			bindingName:  "p",
			expectedType: "P",
		},
		"NoFields": {
			input: `
				class Empty {}
				val e = Empty()
			`,
			bindingName:  "e",
			expectedType: "Empty",
		},
		"FieldWithDefaultSkippedFromParams": {
			input: `
				class WithDefault {
					x:: number,
					y:: number = 99,
				}
				val w = WithDefault(1)
			`,
			bindingName:  "w",
			expectedType: "WithDefault",
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

// TestConstructorErrors covers Phase 2's new diagnostics: mixed forms,
// multiple in-body constructors, field-level defaults when an in-body
// constructor is declared, private constructors, and the
// computed-key-required-field case for synthesis.
func TestConstructorErrors(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected []string
	}{
		"MixedConstructorForms": {
			input: `
				class Foo(x: number) {
					x,
					constructor(mut self, x: number) {
						self.x = x
					}
				}
			`,
			expected: []string{"Cannot mix primary-constructor"},
		},
		"MultipleInBodyConstructors": {
			input: `
				class Foo {
					x:: number,
					constructor(mut self, x: number) {
						self.x = x
					},
					constructor(mut self) {
						self.x = 0
					}
				}
			`,
			expected: []string{"Multiple constructors"},
		},
		"FieldDefaultRejectedWithInBodyCtor": {
			input: `
				class Foo {
					x:: number = 0,
					constructor(mut self) {
						self.x = 1
					}
				}
			`,
			expected: []string{"Field 'x' cannot have a default"},
		},
		"ComputedKeyRequiredFieldRejectsSynthesis": {
			input: `
				val k = "name"
				class Foo {
					[k]:: number,
				}
			`,
			expected: []string{"computed-key field"},
		},
		"PrivateConstructorRejected": {
			input: `
				class Foo {
					x:: number,
					private constructor(mut self, x: number) {
						self.x = x
					}
				}
			`,
			expected: []string{"Private constructors are not yet supported"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, test.input)
			// Filter to only the substrings we care about — the body checker
			// may generate downstream errors (e.g. unknown field types) that
			// we don't want to assert on here.
			var matched []Error
			for _, e := range errs {
				for _, want := range test.expected {
					if strings.Contains(e.Message(), want) {
						matched = append(matched, e)
						break
					}
				}
			}
			require.Equalf(t, len(test.expected), len(matched),
				"expected %d matching errors; got %d (all errors: %v)",
				len(test.expected), len(matched), formatErrs(errs))
		})
	}
}

// TestConstructorOwnTypeParamsInScope verifies that a constructor's own
// type parameters are visible inside the constructor body. Regression: the
// Context returned by inferConstructorSig (which carries ctor-local type
// params) was previously discarded so body checking used the class scope
// only.
func TestConstructorOwnTypeParamsInScope(t *testing.T) {
	t.Parallel()
	input := `
		class Foo<T> {
			x:: T,
			constructor<U>(mut self, x: T, y: U) {
				val z: U = y
				self.x = x
			}
		}
	`
	errs := inferModuleErrors(t, input)
	for _, e := range errs {
		if strings.Contains(e.Message(), "U") || strings.Contains(e.Message(), "type") {
			t.Logf("error: %s", e.Message())
		}
	}
	require.Empty(t, errs, "expected no errors; got: %v", formatErrs(errs))
}

// TestConstructorParamsDoNotLeakIntoMethods verifies that an in-body
// constructor's parameters are scoped to the constructor body and are
// NOT visible inside other methods/getters/setters. Methods see fields
// via `self.<field>` only.
func TestConstructorParamsDoNotLeakIntoMethods(t *testing.T) {
	t.Parallel()
	input := `
		class Foo {
			x:: number,
			constructor(mut self, secret: number) {
				self.x = secret
			},
			leak(self) -> number {
				return secret
			}
		}
	`
	errs := inferModuleErrors(t, input)
	// Expect an UnknownIdentifierError naming 'secret' inside leak().
	found := false
	for _, e := range errs {
		uie, ok := e.(*UnknownIdentifierError)
		if ok && uie.Ident != nil && uie.Ident.Name == "secret" {
			found = true
			break
		}
	}
	require.Truef(t, found,
		"expected an UnknownIdentifierError for 'secret' inside method; got: %v",
		formatErrs(errs))
}

// TestConstructorInferredTypes is a table-based suite that pins the
// rendered type of every relevant value binding for a few constructor
// scenarios:
//
//   - ThrowingConstructor: a class whose constructor declares a
//     `throws` clause, and a wrapper `fn ... throws _` that calls it.
//     Pins both the class's own (constructor-bearing) type and the
//     wrapper's inferred signature. Documents whether caller-side
//     throws propagation through `Foo(x)` is in place.
//   - SynthesizedGenericClass: a generic class with no in-body
//     constructor, exercising the synthesizer + generic instantiation
//     at the call site.
func TestConstructorInferredTypes(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected map[string]string
	}{
		"ThrowingConstructor": {
			input: `
				class Foo {
					x:: number,
					constructor(mut self, x: number) throws string {
						if x < 0 {
							throw "negative"
						}
						self.x = x
					}
				}
				val make = fn (x: number) -> _ throws _ {
					return Foo(x)
				}
			`,
			expected: map[string]string{
				// `Foo`'s class binding renders as a constructor-bearing
				// object type — the `throws string` clause is attached to
				// the callable signature.
				"Foo": "{new fn (x: number) -> Foo throws string}",
				// Caller-side throws inference propagates the
				// constructor's declared throws into `make`'s
				// inferred signature.
				"make": "fn (x: number) -> Foo throws string",
			},
		},
		"SynthesizedGenericClass": {
			input: `
				class Box<T> {
					value:: T,
				}
				val b = Box(42)
				val s = Box("hi")
			`,
			expected: map[string]string{
				"Box": "{new fn <T>(value: T) -> Box<T>}",
				"b":   "Box<42>",
				"s":   "Box<\"hi\">",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, test.input)
			require.Empty(t, errs, "expected no errors; got: %v", formatErrs(errs))

			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			for binding, want := range test.expected {
				got, ok := actual[binding]
				require.Truef(t, ok, "binding %q not found", binding)
				assert.Equalf(t, want, got,
					"unexpected inferred type for %q", binding)
			}
		})
	}
}

// Note: optional-field syntax (`x?: T`) is in the §2.4 plan but isn't
// yet plumbed through the AST, so the "only optional fields → Foo()"
// case described in the plan can't be exercised at the source level
// today. The closest current shape — an empty class — is already
// covered by `TestSynthesizedConstructor/NoFields`.

// Note on `mut self` / explicit-return-type validation: the surface
// parser already rejects `constructor(self, ...)` /
// `constructor(x: number)` / `constructor(...) -> T { ... }` at parse
// time (see `internal/parser/decl.go`), so user-typed code never
// reaches `validateConstructorSelf`. The checker-side validation
// remains as defense-in-depth for synthesizers and other AST-building
// code paths that bypass the parser. Exercising those paths requires
// hand-built ASTs and is left to direct unit tests in the `checker`
// package.

// TestSubclassSynthesisIsNotAllowed verifies that a subclass without an
// explicit constructor does NOT silently get a synthesized constructor
// (which would skip the required super(...) call).
func TestSubclassSynthesisIsNotAllowed(t *testing.T) {
	t.Parallel()
	input := `
		class Base {
			x:: number,
		}
		class Derived extends Base {
			y:: number,
		}
	`
	errs := inferModuleErrors(t, input)
	// We expect SOME error indicating subclass synthesis isn't supported,
	// rather than silent success or a confusing downstream type error.
	require.NotEmpty(t, errs,
		"expected a diagnostic for subclass without an explicit constructor; got none")
}

func formatErrs(errs []Error) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message()
	}
	return out
}

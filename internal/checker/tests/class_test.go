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

// TestClassImplementsConformance verifies that a class declaring
// `implements I` is checked structurally against `I` (#558). Each
// sub-case feeds source through InferModule and asserts on the resulting
// diagnostics.
func TestClassImplementsConformance(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"MissingMember": {
			input: `
				interface Greeter {
					greet(self) -> string,
				}
				class Hello implements Greeter {}
				val h = Hello()
			`,
			expectedErrors: []string{
				"Class 'Hello' does not implement interface 'Greeter': missing member 'greet'",
			},
		},
		"AllMembersSatisfied": {
			input: `
				interface Greeter {
					greet(self) -> string,
				}
				class Hello implements Greeter {
					greet(self) -> string { return "hi" }
				}
				val h = Hello()
			`,
		},
		"InheritedMemberSatisfies": {
			input: `
				interface Runnable {
					run(self) -> string,
				}
				class Animal {
					run(self) -> string { return "moving" }
				}
				class Dog extends Animal implements Runnable {
					constructor(mut self) {}
				}
				val d = Dog()
			`,
		},
		"ReturnTypeMismatch": {
			input: `
				interface Greeter {
					greet(self) -> string,
				}
				class Hello implements Greeter {
					greet(self) -> number { return 42 }
				}
				val h = Hello()
			`,
			expectedErrors: []string{
				"Class 'Hello' does not implement interface 'Greeter': member 'greet' signature does not match",
			},
		},
		"ParamTypeMismatch": {
			input: `
				interface Adder {
					add(self, x: number) -> number,
				}
				class Bad implements Adder {
					add(self, x: string) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Adder': member 'add' signature does not match",
			},
		},
		"PropertySatisfied": {
			input: `
				interface HasName {
					name: string,
				}
				class Person implements HasName {
					name: string,
				}
				val p = Person("Alice")
			`,
		},
		"SelfReturnType": {
			input: `
				interface Cloneable {
					clone(self) -> Self,
				}
				class Box implements Cloneable {
					value: number,
					clone(self) -> Box { return Box(self.value) }
				}
				val b = Box(1)
			`,
		},
		"MutSelfRequiredButClassUsesSelf": {
			input: `
				interface Counter {
					increment(mut self) -> number,
				}
				class Bad implements Counter {
					increment(self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Counter': member 'increment' self receiver does not match",
			},
		},
		"SelfRequiredButClassUsesMutSelf": {
			input: `
				interface Reader {
					read(self) -> number,
				}
				class Bad implements Reader {
					read(mut self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Reader': member 'read' self receiver does not match",
			},
		},
		"MutSelfMatches": {
			input: `
				interface Counter {
					increment(mut self) -> number,
				}
				class Good implements Counter {
					increment(mut self) -> number { return 0 }
				}
				val g = Good()
			`,
		},
		"PropertyRequiredButClassDeclaresMethod": {
			input: `
				interface HasName {
					name: string,
				}
				class Bad implements HasName {
					name(self) -> string { return "x" }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'HasName': member 'name' member is not a property",
			},
		},
		"InterfaceSetterWithMatchingReceiver": {
			input: `
				interface HasValue {
					set value(mut self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(mut self, x: number) { self._value = x },
				}
				val b = Box(0)
			`,
		},
		"SetterMutSelfMismatch": {
			// Iface promises the setter can be called on an immutable
			// receiver; class needs `mut self`. The class does not
			// satisfy the iface contract.
			input: `
				interface HasValue {
					set value(self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(mut self, x: number) { self._value = x },
				}
				val b = Box(0)
			`,
			expectedErrors: []string{
				"Class 'Box' does not implement interface 'HasValue': member 'value' self receiver does not match",
			},
		},
		"GetterMutSelfMatches": {
			// A getter that mutates a cache on the instance needs
			// `mut self`. The interface declares the same shape, so
			// this is valid.
			input: `
				interface CachedSize {
					get size(mut self) -> number,
				}
				class Container implements CachedSize {
					_cache: number,
					get size(mut self) -> number {
						self._cache = 1
						return self._cache
					},
				}
				val c = Container(0)
			`,
		},
		"GetterMutSelfMismatch": {
			// Iface promises a non-mutating getter; class declares
			// `mut self`. A caller holding the iface ref expects no
			// mutation from a read, so this is rejected.
			input: `
				interface ReadSize {
					get size(self) -> number,
				}
				class Container implements ReadSize {
					_cache: number,
					get size(mut self) -> number { return self._cache },
				}
				val c = Container(0)
			`,
			expectedErrors: []string{
				"Class 'Container' does not implement interface 'ReadSize': member 'size' self receiver does not match",
			},
		},
		"GenericClassImplementsGenericInterface": {
			input: `
				interface Container<T> {
					value: T,
				}
				class Box<T> implements Container<T> {
					value: T,
				}
				val b = Box(1)
			`,
		},
		"NarrowerClassReturnSatisfiesIface": {
			// The class method's return type is a subtype of the
			// interface's return type, so the class is substitutable
			// for the interface. This must be accepted.
			input: `
				interface Producer {
					produce(self) -> number | string,
				}
				class IntProducer implements Producer {
					produce(self) -> number { return 1 }
				}
				val p = IntProducer()
			`,
		},
		"WiderClassReturnRejected": {
			// The class returns a supertype of what the interface
			// promises. A caller holding a Producer expects only
			// `number` back, but the class might return a string.
			input: `
				interface Producer {
					produce(self) -> number,
				}
				class Bad implements Producer {
					produce(self) -> number | string { return 1 }
				}
				val p = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Producer': member 'produce' signature does not match",
			},
		},
		"WiderClassParamSatisfiesIface": {
			// Class accepts a wider parameter type than the interface
			// promises callers can pass. This is contravariantly safe.
			input: `
				interface Sink {
					accept(self, x: number) -> undefined,
				}
				class Lenient implements Sink {
					accept(self, x: number | string) -> undefined { return undefined }
				}
				val s = Lenient()
			`,
		},
		"SetterArgTypeMismatch": {
			input: `
				interface HasValue {
					set value(self, x: number) -> undefined,
				}
				class Bad implements HasValue {
					_value: string,
					set value(mut self, x: string) { self._value = x },
				}
				val b = Bad("")
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'HasValue': member 'value' self receiver does not match",
			},
		},
		"GetterReturnTypeMismatch": {
			input: `
				interface HasName {
					get name(self) -> string,
				}
				class Bad implements HasName {
					get name(self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'HasName': member 'name' getter return type does not match",
			},
		},
		"MultipleImplementsOneMissing": {
			input: `
				interface A {
					a(self) -> number,
				}
				interface B {
					b(self) -> number,
				}
				class Partial implements A, B {
					a(self) -> number { return 1 }
				}
				val p = Partial()
			`,
			expectedErrors: []string{
				"Class 'Partial' does not implement interface 'B': missing member 'b'",
			},
		},
		"OptionalClassPropertyDoesNotSatisfyRequiredInterfaceProperty": {
			input: `
				interface HasName {
					name: string,
				}
				class Person implements HasName {
					name?: string,
				}
				val p = Person()
			`,
			expectedErrors: []string{
				"Class 'Person' does not implement interface 'HasName': member 'name' property is optional but interface requires it",
			},
		},
		"OptionalClassPropertyDoesNotSatisfyInterfaceGetter": {
			input: `
				interface HasName {
					get name(self) -> string,
				}
				class Person implements HasName {
					name?: string,
				}
				val p = Person()
			`,
			expectedErrors: []string{
				"Class 'Person' does not implement interface 'HasName': member 'name' property is optional but interface requires it",
			},
		},
		"OptionalClassPropertyDoesNotSatisfyInterfaceSetter": {
			input: `
				interface HasValue {
					set value(self, x: number) -> undefined,
				}
				class Box implements HasValue {
					value?: number,
				}
				val b = Box()
			`,
			expectedErrors: []string{
				"Class 'Box' does not implement interface 'HasValue': member 'value' property is optional but interface requires it",
			},
		},
		"OptionalPropertyAbsentOnClassIsAllowed": {
			input: `
				interface HasOptional {
					nickname?: string,
				}
				class Person implements HasOptional {
					name: string,
				}
				val p = Person("Alice")
			`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			inferErrors := c.InferModule(inferCtx, module)

			conformanceErrs := filterConformanceErrors(inferErrors)
			otherErrs := otherInferErrors(inferErrors)
			if len(otherErrs) > 0 {
				msgs := make([]string, len(otherErrs))
				for i, e := range otherErrs {
					msgs[i] = e.Message()
				}
				t.Fatalf("unexpected non-conformance inference errors: %v", msgs)
			}
			actualMsgs := make([]string, len(conformanceErrs))
			for i, e := range conformanceErrs {
				actualMsgs[i] = e.Message()
			}
			if test.expectedErrors == nil {
				assert.Empty(t, actualMsgs,
					"expected no conformance errors")
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
}

func filterConformanceErrors(errs []Error) []Error {
	var out []Error
	for _, e := range errs {
		if _, ok := e.(*ClassDoesNotImplementInterfaceError); ok {
			out = append(out, e)
		}
	}
	return out
}

func otherInferErrors(errs []Error) []Error {
	var out []Error
	for _, e := range errs {
		if _, ok := e.(*ClassDoesNotImplementInterfaceError); !ok {
			out = append(out, e)
		}
	}
	return out
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

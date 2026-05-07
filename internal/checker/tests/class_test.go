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

// TestClassImplementsLifetimeConformance pins the wiring of
// VerifyLifetimeCompatibility into the implements check. Each case
// declares an interface method with explicit lifetime parameters and
// a class method that either matches, is more conservative, or
// violates the relationship.
func TestClassImplementsLifetimeConformance(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		// Interface ties param to return; class method ties them
		// the same way. OK.
		"MatchingAlias": {
			input: `
				type Point = {x: number}
				interface Borrower {
					borrow<'a>(self, p: 'a Point) -> 'a Point,
				}
				class Forwarder implements Borrower {
					borrow<'a>(self, p: 'a Point) -> 'a Point { return p }
				}
				val f = Forwarder()
			`,
		},
		// Lifetime declared at the interface level (not on the method)
		// flows into a field and a method signature.
		"InterfaceLevelLifetimeOnField": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
					peek(self) -> 'a Point,
				}
			`,
		},
		// Class-level lifetime parameter on a field.
		"ClassLevelLifetimeOnField": {
			input: `
				type Point = {x: number}
				class Container<'a> {
					p: 'a Point,
				}
			`,
		},
		// Interface promises a fresh (independent) return; the impl
		// aliases its parameter into the return. Less conservative —
		// must error.
		"ImplAliasesWhenIfaceFresh": {
			input: `
				type Point = {x: number}
				interface Borrower {
					borrow<'a>(self, p: 'a Point) -> Point,
				}
				class AliasingImpl implements Borrower {
					borrow<'a>(self, p: 'a Point) -> 'a Point { return p }
				}
				val a = AliasingImpl()
			`,
			expectedErrors: []string{
				"interface implementation lifetime mismatch: implementation aliases parameter 'p' but interface declares the return value is independent",
			},
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

			var lifetimeErrs []Error
			var otherErrs []Error
			for _, e := range inferErrors {
				switch e.(type) {
				case InterfaceLifetimeMismatchError:
					lifetimeErrs = append(lifetimeErrs, e)
				default:
					otherErrs = append(otherErrs, e)
				}
			}
			if len(otherErrs) > 0 {
				msgs := make([]string, len(otherErrs))
				for i, e := range otherErrs {
					msgs[i] = e.Message()
				}
				t.Fatalf("unexpected non-lifetime inference errors: %v", msgs)
			}
			actualMsgs := make([]string, len(lifetimeErrs))
			for i, e := range lifetimeErrs {
				actualMsgs[i] = e.Message()
			}
			if test.expectedErrors == nil {
				assert.Empty(t, actualMsgs,
					"expected no lifetime-conformance errors")
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
}

// TestLifetimeArgArityMismatch verifies that a type reference whose
// number of lifetime arguments disagrees with the type alias's declared
// lifetime parameters produces a diagnostic at the resolution site.
// Without this check, mismatched arities can be silently propagated
// through the type ref and only surface much later (or not at all).
func TestLifetimeArgArityMismatch(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"TooManyLifetimeArgs": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				fn use<'a, 'b>(v: View<'a, 'b>) {}
			`,
			expectedErrors: []string{
				"type 'View' expects 1 lifetime argument(s) but got 2",
			},
		},
		"TooFewLifetimeArgs": {
			input: `
				type Point = {x: number}
				interface Pair<'a, 'b> {
					left: 'a Point,
					right: 'b Point,
				}
				fn use<'a>(p: Pair<'a>) {}
			`,
			expectedErrors: []string{
				"type 'Pair' expects 2 lifetime argument(s) but got 1",
			},
		},
		"MatchingArity": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				fn use<'a>(v: View<'a>) {}
			`,
		},
		// VarDecl initializers run with AllowUndefinedTypeRefs and
		// register unresolved refs in TypeRefsToUpdate. The arity check
		// must also fire when the alias is resolved on that deferred
		// path, otherwise forward refs through a var binding silently
		// bypass the check.
		"DeferredForwardRef": {
			input: `
				type Point = {x: number}
				declare val v: View<'a, 'b>
				interface View<'a> {
					value: 'a Point,
				}
			`,
			expectedErrors: []string{
				"type 'View' expects 1 lifetime argument(s) but got 2",
			},
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

			var arityErrs []Error
			var otherErrs []Error
			for _, e := range inferErrors {
				if _, ok := e.(*LifetimeArgCountMismatchError); ok {
					arityErrs = append(arityErrs, e)
				} else if _, ok := e.(UndeclaredLifetimeError); !ok {
					// UndeclaredLifetimeError is expected because the
					// `declare val` in DeferredForwardRef references
					// 'a/'b without an enclosing `<>` clause; it's
					// unrelated to the arity check under test.
					otherErrs = append(otherErrs, e)
				}
			}
			actualMsgs := make([]string, len(arityErrs))
			for i, e := range arityErrs {
				actualMsgs[i] = e.Message()
			}
			otherMsgs := make([]string, len(otherErrs))
			for i, e := range otherErrs {
				otherMsgs[i] = e.Message()
			}
			assert.Empty(t, otherMsgs, "unexpected non-arity errors")
			if test.expectedErrors == nil {
				assert.Empty(t, actualMsgs)
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
}

// TestInterfaceMergeLifetimeParamMismatch verifies that duplicate
// interface declarations whose `<'a, ...>` lifetime clauses disagree
// produce a diagnostic when merging, mirroring how mismatched type
// parameters are reported.
func TestInterfaceMergeLifetimeParamMismatch(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"DifferentArity": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				interface View<'a, 'b> {
					other: 'b Point,
				}
			`,
			expectedErrors: []string{
				"Interface 'View' has 2 lifetime parameter(s) but was previously declared with 1 lifetime parameter(s)",
			},
		},
		"DifferentNames": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				interface View<'b> {
					other: 'b Point,
				}
			`,
			expectedErrors: []string{
				"Lifetime parameter at position 0 has name 'b' but was previously declared with name 'a' in interface 'View'",
			},
		},
		"OneDeclWithLifetimesOneWithout": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				interface View {
					tag: number,
				}
			`,
			expectedErrors: []string{
				"Interface 'View' has 0 lifetime parameter(s) but was previously declared with 1 lifetime parameter(s)",
			},
		},
		"MatchingLifetimeParams": {
			input: `
				type Point = {x: number}
				interface View<'a> {
					value: 'a Point,
				}
				interface View<'a> {
					tag: number,
				}
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

			var paramErrs []Error
			var otherErrs []Error
			for _, e := range inferErrors {
				if _, ok := e.(*TypeParamMismatchError); ok {
					paramErrs = append(paramErrs, e)
				} else {
					otherErrs = append(otherErrs, e)
				}
			}
			otherMsgs := make([]string, len(otherErrs))
			for i, e := range otherErrs {
				otherMsgs[i] = e.Message()
			}
			assert.Empty(t, otherMsgs, "unexpected non-param-mismatch errors")
			actualMsgs := make([]string, len(paramErrs))
			for i, e := range paramErrs {
				actualMsgs[i] = e.Message()
			}
			if test.expectedErrors == nil {
				assert.Empty(t, actualMsgs)
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
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

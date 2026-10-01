package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
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
					greet(&self) -> string,
				}
				class Hello implements Greeter {
					greet(&self) -> string { return "hi" }
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
					run(&self) -> string,
				}
				interface Barker {
					bark(&self) -> string,
				}
				class Dog extends Animal implements Runnable, Barker {
					constructor(&mut self, name: string) { self.name = name },
					run(&self) -> string { return "running" },
					bark(&self) -> string { return "woof" },
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

// TestClassInheritedMemberAccess verifies that a member declared on a superclass is
// accessible through a subclass instance. `getObjectAccess` walks the instance object's
// `Extends` chain when the member is not found directly, so an inherited field read and an
// inherited method call both resolve to the member's declared type on the subclass value.
func TestClassInheritedMemberAccess(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"InheritedProperty": {
			input: `
				class Animal {
					name: string,
				}
				class Dog extends Animal {
					constructor(&mut self) {}
				}
				val d = Dog()
				val n = d.name
			`,
			bindingName:  "n",
			expectedType: "string",
		},
		"InheritedMethod": {
			input: `
				class Animal {
					name: string,
					speak(&self) -> string { return "..." },
				}
				class Dog extends Animal {
					constructor(&mut self) {}
				}
				val d = Dog()
				val s = d.speak()
			`,
			bindingName:  "s",
			expectedType: "string",
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
					greet(&self) -> string,
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
					greet(&self) -> string,
				}
				class Hello implements Greeter {
					greet(&self) -> string { return "hi" }
				}
				val h = Hello()
			`,
		},
		"InheritedMemberSatisfies": {
			input: `
				interface Runnable {
					run(&self) -> string,
				}
				class Animal {
					run(&self) -> string { return "moving" }
				}
				class Dog extends Animal implements Runnable {
					constructor(&mut self) {}
				}
				val d = Dog()
			`,
		},
		"ReturnTypeMismatch": {
			input: `
				interface Greeter {
					greet(&self) -> string,
				}
				class Hello implements Greeter {
					greet(&self) -> number { return 42 }
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
					add(&self, x: number) -> number,
				}
				class Bad implements Adder {
					add(&self, x: string) -> number { return 0 }
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
					clone(&self) -> Self,
				}
				class Box implements Cloneable {
					value: number,
					clone(&self) -> Box { return Box(self.value) }
				}
				val b = Box(1)
			`,
		},
		"MutSelfRequiredButClassUsesSelf": {
			input: `
				interface Counter {
					increment(&mut self) -> number,
				}
				class Bad implements Counter {
					increment(&self) -> number { return 0 }
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
					read(&self) -> number,
				}
				class Bad implements Reader {
					read(&mut self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Reader': member 'read' self receiver does not match",
			},
		},
		"ConsumingClassMethodAgainstBorrowingInterface": {
			input: `
				interface Reader {
					read(&self) -> number,
				}
				class Bad implements Reader {
					read(self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Reader': member 'read' self receiver does not match",
			},
		},
		"BorrowingClassMethodAgainstConsumingInterface": {
			input: `
				interface Closer {
					close(mut self) -> number,
				}
				class Bad implements Closer {
					close(&mut self) -> number { return 0 }
				}
				val b = Bad()
			`,
			expectedErrors: []string{
				"Class 'Bad' does not implement interface 'Closer': member 'close' self receiver does not match",
			},
		},
		"MutSelfMatches": {
			input: `
				interface Counter {
					increment(&mut self) -> number,
				}
				class Good implements Counter {
					increment(&mut self) -> number { return 0 }
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
					name(&self) -> string { return "x" }
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
					set value(&mut self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(&mut self, x: number) { self._value = x },
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
					set value(&self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(&mut self, x: number) { self._value = x },
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
					get size(&mut self) -> number,
				}
				class Container implements CachedSize {
					_cache: number,
					get size(&mut self) -> number {
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
					get size(&self) -> number,
				}
				class Container implements ReadSize {
					_cache: number,
					get size(&mut self) -> number { return self._cache },
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
					produce(&self) -> number | string,
				}
				class IntProducer implements Producer {
					produce(&self) -> number { return 1 }
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
					produce(&self) -> number,
				}
				class Bad implements Producer {
					produce(&self) -> number | string { return 1 }
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
					accept(&self, x: number) -> undefined,
				}
				class Lenient implements Sink {
					accept(&self, x: number | string) -> undefined { return undefined }
				}
				val s = Lenient()
			`,
		},
		"SetterArgTypeMismatch": {
			input: `
				interface HasValue {
					set value(&self, x: number) -> undefined,
				}
				class Bad implements HasValue {
					_value: string,
					set value(&mut self, x: string) { self._value = x },
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
					get name(&self) -> string,
				}
				class Bad implements HasName {
					get name(&self) -> number { return 0 }
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
					a(&self) -> number,
				}
				interface B {
					b(&self) -> number,
				}
				class Partial implements A, B {
					a(&self) -> number { return 1 }
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
					get name(&self) -> string,
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
					set value(&self, x: number) -> undefined,
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
			_, inferErrors := c.InferModule(inferCtx, module)

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
					borrow<'a>(&self, p: 'a Point) -> 'a Point,
				}
				class Forwarder implements Borrower {
					borrow<'a>(&self, p: 'a Point) -> 'a Point { return p }
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
					peek(&self) -> 'a Point,
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
		// Receiver lifetime: interface ties self to return; impl
		// matches. OK.
		"MatchingReceiverLifetime": {
			input: `
				type Point = {x: number}
				interface Viewer {
					peek<'a>(&'a self) -> 'a Point,
				}
				class V implements Viewer {
					p: Point,
					peek<'a>(&'a self) -> 'a Point { return self.p }
				}
				val v = V({x: 0})
			`,
		},
		// Receiver lifetime: interface promises a fresh (independent)
		// return; the impl aliases self into the return. Less
		// conservative — must error.
		"ImplAliasesSelfWhenIfaceFresh": {
			input: `
				type Point = {x: number}
				interface Viewer {
					peek<'a>(&'a self) -> Point,
				}
				class V implements Viewer {
					p: Point,
					peek<'a>(&'a self) -> 'a Point { return self.p }
				}
				val v = V({x: 0})
			`,
			expectedErrors: []string{
				"interface implementation lifetime mismatch: implementation aliases `self` but interface declares the return value is independent",
			},
		},
		// Interface promises a fresh (independent) return; the impl
		// aliases its parameter into the return. Less conservative —
		// must error.
		"ImplAliasesWhenIfaceFresh": {
			input: `
				type Point = {x: number}
				interface Borrower {
					borrow<'a>(&self, p: 'a Point) -> Point,
				}
				class AliasingImpl implements Borrower {
					borrow<'a>(&self, p: 'a Point) -> 'a Point { return p }
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
			_, inferErrors := c.InferModule(inferCtx, module)

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

// TestClassMethodSelfParamPopulated pins that class method/getter/setter
// inference populates FuncType.SelfParam with a receiver whose type is
// the class instance ref (wrapped in MutType for `mut self`). This is
// the type-system-level invariant the checker relies on once
// receiver-lifetime work goes downstream of method-call typing.
func TestClassMethodSelfParamPopulated(t *testing.T) {
	tests := map[string]struct {
		input        string
		className    string
		methodName   string
		expectMut    bool
		expectStatic bool
	}{
		"ImmutableSelf": {
			input: `
				class Box {
					value: number,
					read(&self) -> number { return self.value }
				}
			`,
			className:  "Box",
			methodName: "read",
		},
		"MutSelf": {
			input: `
				class Counter {
					n: number,
					constructor(&mut self) { self.n = 0 },
					bump(&mut self) -> number {
						self.n = self.n + 1
						return self.n
					}
				}
			`,
			className:  "Counter",
			methodName: "bump",
			expectMut:  true,
		},
		"StaticHasNoSelfParam": {
			// Static methods must NOT carry a SelfParam — they have no
			// receiver to bind. The method elem's MutSelf is nil.
			input: `
				class Boxer {
					static make() -> number { return 0 }
				}
			`,
			className:    "Boxer",
			methodName:   "make",
			expectStatic: true,
		},
		"GetterImmutableSelf": {
			input: `
				class Reader {
					_value: number,
					get value(&self) -> number { return self._value }
				}
			`,
			className:  "Reader",
			methodName: "value",
		},
		"SetterMutSelf": {
			input: `
				class Writer {
					_value: number,
					constructor(&mut self) { self._value = 0 },
					set value(&mut self, x: number) { self._value = x }
				}
			`,
			className:  "Writer",
			methodName: "value",
			expectMut:  true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)

			classAlias := ns.Types[test.className]
			require.NotNilf(t, classAlias, "class type alias %q not found", test.className)

			searchObj := type_system.Prune(classAlias.Type).(*type_system.ObjectType)
			if test.expectStatic {
				// Static methods live on the class object's value
				// binding, not on the instance type. Look up the
				// constructor type alias for the class to inspect them.
				ctorBinding := ns.Values[test.className]
				require.NotNil(t, ctorBinding, "constructor binding not found")
				if obj, ok := type_system.Prune(ctorBinding.Type).(*type_system.ObjectType); ok {
					searchObj = obj
				}
			}

			var methodFn *type_system.FuncType
			matchKey := func(k type_system.ObjTypeKey) bool {
				return k.Kind == type_system.StrObjTypeKeyKind && k.Str == test.methodName
			}
			for _, elem := range searchObj.Elems {
				switch e := elem.(type) {
				case *type_system.MethodElem:
					if matchKey(e.Name) {
						methodFn = e.SingleSig()
					}
				case *type_system.GetterElem:
					if matchKey(e.Name) {
						methodFn = e.Fn
					}
				case *type_system.SetterElem:
					if matchKey(e.Name) {
						methodFn = e.Fn
					}
				}
				if methodFn != nil {
					break
				}
			}
			require.NotNilf(t, methodFn, "method/accessor %q not found", test.methodName)

			if test.expectStatic {
				assert.Nil(t, methodFn.SelfParam,
					"static methods must not carry SelfParam")
				return
			}

			require.NotNil(t, methodFn.SelfParam,
				"instance methods must carry SelfParam")

			_, isMut := methodFn.SelfParam.Type.(*type_system.MutType)
			assert.Equalf(t, test.expectMut, isMut,
				"SelfParam.Type wrap-in-MutType should reflect mutability")
		})
	}
}

// TestClassMethodSelfLifetime pins that an explicit `'a self` /
// `mut 'a self` annotation produces a `SelfParam.Type` carrying the
// resolved LifetimeVar — and that two methods declaring their own
// `<'a>` get independent receiver TypeRefType clones (regression
// against accidentally sharing `classSelfRef`).
func TestClassMethodSelfLifetime(t *testing.T) {
	src := `
		type Point = {x: number}
		class Container {
			p: Point,
			peek<'a>(&'a self) -> 'a Point { return self.p },
			swap<'b>(&'b mut self, q: mut 'b Point) -> mut 'b Point { return q }
		}
	`
	ns := mustInferAsModule(t, src)
	classAlias := ns.Types["Container"]
	require.NotNil(t, classAlias)
	objType := type_system.Prune(classAlias.Type).(*type_system.ObjectType)

	findMethod := func(name string) *type_system.FuncType {
		for _, e := range objType.Elems {
			if m, ok := e.(*type_system.MethodElem); ok &&
				m.Name.Kind == type_system.StrObjTypeKeyKind && m.Name.Str == name {
				return m.SingleSig()
			}
		}
		return nil
	}

	peek := findMethod("peek")
	require.NotNil(t, peek, "peek method should exist")
	require.NotNil(t, peek.SelfParam, "peek must carry a SelfParam")
	peekRef, ok := peek.SelfParam.Type.(*type_system.TypeRefType)
	require.True(t, ok, "peek SelfParam.Type must be a bare TypeRefType (immutable self)")
	require.NotNil(t, peekRef.Lifetime, "peek receiver must carry the 'a lifetime")
	require.Len(t, peek.LifetimeParams, 1)
	assert.Equal(t, peek.LifetimeParams[0], peekRef.Lifetime,
		"peek receiver lifetime must be the same LifetimeVar as the method's <'a>")

	swap := findMethod("swap")
	require.NotNil(t, swap)
	require.NotNil(t, swap.SelfParam)
	swapMut, ok := swap.SelfParam.Type.(*type_system.MutType)
	require.True(t, ok, "swap SelfParam.Type must be a MutType wrapping the receiver")
	swapRef, ok := swapMut.Type.(*type_system.TypeRefType)
	require.True(t, ok)
	require.NotNil(t, swapRef.Lifetime)
	require.Len(t, swap.LifetimeParams, 1)
	assert.Equal(t, swap.LifetimeParams[0], swapRef.Lifetime,
		"swap receiver lifetime must be the method's <'b>")

	// Per-method clone regression: peek's and swap's receiver TypeRefTypes
	// must be distinct pointers — sharing one would mean writing into one
	// method's lifetime poisoned the other.
	assert.NotSame(t, peekRef, swapRef,
		"each method must own a distinct receiver TypeRefType clone")
	assert.NotEqual(t, peekRef.Lifetime, swapRef.Lifetime,
		"each method's receiver carries its own LifetimeVar")
}

// TestConstructorRejectsSelfLifetime pins that a constructor with a
// lifetime on `self` produces the dedicated diagnostic.
func TestConstructorRejectsSelfLifetime(t *testing.T) {
	src := `
		class C {
			n: number,
			constructor(&'a mut self) { self.n = 0 }
		}
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrors := c.InferModule(inferCtx, module)

	count := 0
	for _, e := range inferErrors {
		if me, ok := e.(MissingMutSelfParameterError); ok && me.Reason == MutSelfHasLifetime {
			if count == 0 {
				assert.Equal(t, "Constructors cannot have a lifetime on `self`.", me.Message())
			}
			count++
		}
	}
	assert.Equal(t, 1, count,
		"expected exactly one MutSelfHasLifetime diagnostic; got %v", inferErrors)

	// The parser must NOT also report its own lifetime-on-self error:
	// having both fire produces a duplicate diagnostic for one mistake.
	for _, pe := range parseErrors {
		assert.NotContains(t, pe.Message,
			"constructors cannot have a lifetime on `self`",
			"parser should not duplicate the checker's MutSelfHasLifetime diagnostic")
	}
}

// TestConstructorRejectsConsumingSelf pins that a constructor declaring a
// consuming receiver gets ConstructorConsumesSelfError rather than the
// mutability diagnostic a `&self` constructor gets.
func TestConstructorRejectsConsumingSelf(t *testing.T) {
	tests := map[string]string{
		"consuming":         "self",
		"mutable consuming": "mut self",
	}
	for name, receiver := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := `
				class C {
					n: number,
					constructor(` + receiver + `) { self.n = 0 }
				}
			`
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			// The parser reports the same mistake. The checker still sees the
			// receiver it parsed, which is what this test pins.
			module, _ := parser.ParseLibFiles(ctx, []*ast.Source{source})

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			_, inferErrors := c.InferModule(inferCtx, module)

			var msgs []string
			for _, e := range inferErrors {
				switch e.(type) {
				case ConstructorConsumesSelfError, MissingMutSelfParameterError:
					msgs = append(msgs, e.Message())
				}
			}
			require.Equal(t, []string{
				"A constructor returns the instance it fills in, so it must borrow `self` as `&mut self` rather than consume it.",
			}, msgs)
		})
	}
}

// TestInstanceMethodMissingSelfReceiver pins that a non-static class
// method, getter, or setter that omits its `self` receiver produces a
// MissingSelfReceiverError. The parser accepts the shape (so we still
// produce a usable AST), but the checker rejects it.
func TestInstanceMethodMissingSelfReceiver(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"Method": {
			input: `
				class Foo {
					bar(x: number) -> number { return x },
				}
			`,
		},
		"Getter": {
			input: `
				class Foo {
					get bar() -> number { return 0 },
				}
			`,
		},
		"Setter": {
			input: `
				class Foo {
					set bar(x: number) {},
				}
			`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, _ := parser.ParseLibFiles(ctx, []*ast.Source{source})

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			_, inferErrors := c.InferModule(inferCtx, module)

			count := 0
			for _, e := range inferErrors {
				if me, ok := e.(MissingSelfReceiverError); ok {
					if count == 0 {
						assert.Equal(t,
							"Instance methods, getters, and setters must declare a `self` receiver as their first parameter.",
							me.Message())
					}
					count++
				}
			}
			assert.Equal(t, 1, count,
				"expected exactly one MissingSelfReceiverError; got %v", inferErrors)
		})
	}
}

// TestObjectTypeAnnRejectsReceiverLifetime pins that `'a self` written
// inside a structural object-type annotation (no class/interface
// receiver to attach the lifetime to) produces a dedicated diagnostic
// rather than being silently dropped.
func TestObjectTypeAnnRejectsReceiverLifetime(t *testing.T) {
	src := `
		type Point = {x: number}
		type Viewer = {
			peek<'a>(&'a self) -> 'a Point,
		}
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, _ := parser.ParseLibFiles(ctx, []*ast.Source{source})

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrors := c.InferModule(inferCtx, module)

	count := 0
	for _, e := range inferErrors {
		if me, ok := e.(ReceiverLifetimeOutsideMemberError); ok {
			if count == 0 {
				assert.Equal(t,
					"A lifetime on `self` is only valid on class or interface members.",
					me.Message())
			}
			count++
		}
	}
	assert.Equal(t, 1, count,
		"expected exactly one ReceiverLifetimeOutsideMemberError; got %v", inferErrors)
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
			_, inferErrors := c.InferModule(inferCtx, module)

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
			_, inferErrors := c.InferModule(inferCtx, module)

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
					increment(&mut self) -> number { return self.count }
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
					increment(&mut self) -> number { return self.count }
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
					setHost(&mut self, h: string) -> undefined {}
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

// TestDeclareClassImplementsContributesMembers covers the `declare` half of
// the `implements` split, where the clause contributes each interface's
// members instead of asserting the class restates them. The dts_to_esc
// converter relies on it: a member declared on a mixin such as `ParentNode`
// has to be reachable on the class that implements it.
func TestDeclareClassImplementsContributesMembers(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"MemberFromImplementedInterface": {
			input: `
				interface ParentNode {
					querySelector(&self, selectors: string) -> string,
				}
				declare class Node {
					nodeName: string,
				}
				declare class Element extends Node implements ParentNode {}
				declare fn makeElement() -> Element
				val found = makeElement().querySelector(".x")
			`,
			bindingName:  "found",
			expectedType: "string",
		},
		"MemberFromTheSecondImplementedInterface": {
			input: `
				interface ARIAMixin {
					ariaLabel: string,
				}
				interface Slottable {
					assignedSlot: number,
				}
				declare class Element implements ARIAMixin, Slottable {}
				declare fn makeElement() -> Element
				val slot = makeElement().assignedSlot
			`,
			bindingName:  "slot",
			expectedType: "number",
		},
		// An overloaded method reaches the class the same way a
		// single-signature one does. Each arm returns a different type, so
		// the inferred type names the arm the call picked. The interop tree
		// gets here through interfaces such as `CanvasDrawImage`, whose
		// `drawImage` declares three arms.
		"OverloadedMemberFromImplementedInterface": {
			input: `
				interface CanvasDrawImage {
					drawImage(&self, dx: number, dy: number) -> string,
					drawImage(&self, dx: number, dy: number, dw: number) -> boolean,
				}
				declare class CanvasRenderingContext2D implements CanvasDrawImage {}
				declare fn makeContext() -> CanvasRenderingContext2D
				val drawn = makeContext().drawImage(1, 2)
			`,
			bindingName:  "drawn",
			expectedType: "string",
		},
		"TheSecondOverloadArmFromImplementedInterface": {
			input: `
				interface CanvasDrawImage {
					drawImage(&self, dx: number, dy: number) -> string,
					drawImage(&self, dx: number, dy: number, dw: number) -> boolean,
				}
				declare class CanvasRenderingContext2D implements CanvasDrawImage {}
				declare fn makeContext() -> CanvasRenderingContext2D
				val drawn = makeContext().drawImage(1, 2, 3)
			`,
			bindingName:  "drawn",
			expectedType: "boolean",
		},
		"MemberFromTheSuperclassOfAnImplementedInterface": {
			input: `
				interface Animatable {
					animate(&self) -> boolean,
				}
				interface ChildNode extends Animatable {
					remove(&self) -> undefined,
				}
				declare class Element implements ChildNode {}
				declare fn makeElement() -> Element
				val animated = makeElement().animate()
			`,
			bindingName:  "animated",
			expectedType: "boolean",
		},
		// A restated readonly member narrows the interface's, and the
		// class's declaration is what lookup returns. `MessagePort` retypes
		// one member of `MessageEventTarget` this way.
		"RestatedReadonlyMemberNarrows": {
			input: `
				interface MessageEventTarget {
					readonly onmessage: string | undefined,
				}
				declare class MessagePort implements MessageEventTarget {
					readonly onmessage: string,
				}
				declare fn makePort() -> MessagePort
				val handler = makePort().onmessage
			`,
			bindingName:  "handler",
			expectedType: "string",
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

// TestDeclareClassImplementsConformance pins which `implements` failures a
// `declare` class still reports. A missing member is inherited rather than
// an error; a restated one has to be assignable to the interface's.
func TestDeclareClassImplementsConformance(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"MissingMemberIsInherited": {
			input: `
				interface ParentNode {
					querySelector(&self, selectors: string) -> string,
				}
				declare class Element implements ParentNode {}
			`,
		},
		"WideningRestatementIsRejected": {
			input: `
				interface MessageEventTarget {
					onmessage: string,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string | number,
				}
			`,
			expectedErrors: []string{
				"Class 'MessagePort' does not implement interface 'MessageEventTarget': member 'onmessage' property type does not match",
			},
		},
		"UnrelatedRestatementIsRejected": {
			input: `
				interface MessageEventTarget {
					onmessage: string,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: number,
				}
			`,
			expectedErrors: []string{
				"Class 'MessagePort' does not implement interface 'MessageEventTarget': member 'onmessage' property type does not match",
			},
		},
		// A class restating an overloaded member is left unchecked, since
		// comparing an overload arm by arm is deferred to #651. This is
		// the shape `SharedWorker` has in the interop tree, where the
		// class restates `addEventListener`.
		"RestatedOverloadIsNotCompared": {
			input: `
				interface AbstractWorker {
					addEventListener(&self, name: string) -> undefined,
					addEventListener(&self, name: string, once: boolean) -> undefined,
				}
				declare class SharedWorker implements AbstractWorker {
					addEventListener(&self, name: string) -> undefined,
					addEventListener(&self, name: string, once: boolean) -> undefined,
				}
			`,
		},
		// Only a readonly member may narrow. Reading is covariant, so a
		// class promising less than the interface declares is safe.
		"NarrowingAReadonlyRestatementIsAccepted": {
			input: `
				interface MessageEventTarget {
					readonly onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					readonly onmessage: string,
				}
			`,
		},
		// A mutable property is written through as well as read, which makes
		// it invariant. TypeScript accepts this narrowing; Escalier does not,
		// because a write of `number` satisfies the interface's type and not
		// the class's.
		"NarrowingAMutableRestatementIsRejected": {
			input: `
				interface MessageEventTarget {
					onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string,
				}
			`,
			expectedErrors: []string{
				"Class 'MessagePort' does not implement interface 'MessageEventTarget': member 'onmessage' is a mutable property, so its type has to match the interface's exactly",
			},
		},
		// Restating a mutable member at the interface's own type is fine.
		"RestatingAMutableMemberExactlyIsAccepted": {
			input: `
				interface MessageEventTarget {
					onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string | number,
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
			_, inferErrors := c.InferModule(inferCtx, module)

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
				assert.Empty(t, actualMsgs, "expected no conformance errors")
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
}

// TestDeclareClassImplementsConflicts covers two implemented interfaces
// declaring the same member name on a `declare` class. Both contribute it, so
// types that disagree must be settled by the class rather than by lookup
// order.
func TestDeclareClassImplementsConflicts(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"AgreeingMembersAreAccepted": {
			input: `
				interface ChildNode {
					remove(&self) -> undefined,
					nodeName: string,
				}
				interface ParentNode {
					nodeName: string,
				}
				declare class Element implements ChildNode, ParentNode {}
			`,
		},
		"ConflictingMembersAreRejected": {
			input: `
				interface ChildNode {
					nodeName: string,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Element implements ChildNode, ParentNode {}
			`,
			expectedErrors: []string{
				"Class 'Element' implements 'ChildNode' and 'ParentNode', which declare member 'nodeName' with conflicting types",
			},
		},
		// Lookup walks the superclass first, so its member settles the name.
		// The members are readonly, so reading is covariant and `number`
		// satisfies what each interface declares.
		"ASuperclassMemberResolvesTheConflict": {
			input: `
				interface ChildNode {
					readonly nodeName: string | number,
				}
				interface ParentNode {
					readonly nodeName: number,
				}
				declare class Node {
					readonly nodeName: number,
				}
				declare class Element extends Node implements ChildNode, ParentNode {}
			`,
		},
		// The same shape with mutable members. A mutable property is
		// invariant, so no single type satisfies two interfaces that declare
		// the name differently, and the superclass settles nothing.
		"AMutableSuperclassMemberCannotResolveTheConflict": {
			input: `
				interface ChildNode {
					nodeName: string | number,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Node {
					nodeName: number,
				}
				declare class Element extends Node implements ChildNode, ParentNode {}
			`,
			expectedErrors: []string{
				"Class 'Element' does not implement interface 'ChildNode': member 'nodeName' is a mutable property, so its type has to match the interface's exactly",
			},
		},
		// The superclass settles the name, so the interfaces no longer
		// conflict. What it settles on contradicts `ParentNode`, which is
		// reported against that interface rather than as a conflict.
		"ASuperclassMemberContradictingAnInterfaceIsRejected": {
			input: `
				interface ChildNode {
					nodeName: string,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Node {
					nodeName: string,
				}
				declare class Element extends Node implements ChildNode, ParentNode {}
			`,
			expectedErrors: []string{
				"Class 'Element' does not implement interface 'ParentNode': member 'nodeName' property type does not match",
			},
		},
		// Same type, but one declares it optional, so reading the name gives
		// `string | undefined` through one and `string` through the other.
		"AnOptionalAndARequiredMemberConflict": {
			input: `
				interface ChildNode {
					nodeName?: string,
				}
				interface ParentNode {
					nodeName: string,
				}
				declare class Element implements ChildNode, ParentNode {}
			`,
			expectedErrors: []string{
				"Class 'Element' implements 'ChildNode' and 'ParentNode', which declare member 'nodeName' with conflicting types",
			},
		},
		"AConflictInheritedByAnInterfaceIsRejected": {
			input: `
				interface Animatable {
					nodeName: string,
				}
				interface ChildNode extends Animatable {
					remove(&self) -> undefined,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Element implements ChildNode, ParentNode {}
			`,
			expectedErrors: []string{
				"Class 'Element' implements 'ChildNode' and 'ParentNode', which declare member 'nodeName' with conflicting types",
			},
		},
		// A restated readonly member settles the name, and reading is
		// covariant, so `number` satisfies both interfaces.
		"ARestatedReadonlyMemberResolvesTheConflict": {
			input: `
				interface ChildNode {
					readonly nodeName: string | number,
				}
				interface ParentNode {
					readonly nodeName: number,
				}
				declare class Element implements ChildNode, ParentNode {
					readonly nodeName: number,
				}
			`,
		},
		// TypeScript accepts the mutable form, where the class narrows one
		// interface's member to satisfy the other. Escalier rejects it: a
		// mutable property is invariant, so `number` does not satisfy
		// `ChildNode`.
		"ARestatedMutableMemberCannotResolveTheConflict": {
			input: `
				interface ChildNode {
					nodeName: string | number,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Element implements ChildNode, ParentNode {
					nodeName: number,
				}
			`,
			expectedErrors: []string{
				"Class 'Element' does not implement interface 'ChildNode': member 'nodeName' is a mutable property, so its type has to match the interface's exactly",
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
			_, inferErrors := c.InferModule(inferCtx, module)

			actualMsgs := make([]string, len(inferErrors))
			for i, e := range inferErrors {
				actualMsgs[i] = e.Message()
			}
			if test.expectedErrors == nil {
				assert.Empty(t, actualMsgs, "expected no inference errors")
			} else {
				assert.Equal(t, test.expectedErrors, actualMsgs)
			}
		})
	}
}

// A class with a body is checked against its `implements` interfaces but
// takes no members from them, which is what keeps Implements and Mixins
// apart on the object type. Only a `declare` class fills both.
func TestNonDeclareClassTakesNoMembersFromImplements(t *testing.T) {
	t.Parallel()
	input := `
		interface Greeter {
			greet(&self) -> string,
		}
		class Hello implements Greeter {
			greet(&self) -> string { return "hi" },
		}
		interface Extra {
			bonus(&self) -> string,
		}
		class Partial implements Extra {
			bonus(&self) -> string { return "b" },
		}
		val h = Hello()
		val g = h.greet()
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors)

	c := NewChecker(ctx)
	_, inferErrors := c.InferModule(Context{Scope: Prelude(c)}, module)
	msgs := make([]string, len(inferErrors))
	for i, e := range inferErrors {
		msgs[i] = e.Message()
	}
	assert.Empty(t, msgs)
}

// A `declare` class that leaves a member to its clause still reports the
// member as missing once the class is not `declare`, which is the same
// source checked both ways.
func TestImplementsMeansConformanceWithoutDeclare(t *testing.T) {
	t.Parallel()
	body := `
		interface ParentNode {
			querySelector(&self, selectors: string) -> string,
		}
		%s class Element implements ParentNode {}
	`
	tests := map[string]struct {
		modifier string
		want     []string
	}{
		"Declare": {modifier: "declare"},
		"NotDeclare": {
			modifier: "",
			want: []string{
				"Class 'Element' does not implement interface 'ParentNode': missing member 'querySelector'",
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf(body, test.modifier)
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, parseErrors)

			c := NewChecker(ctx)
			_, inferErrors := c.InferModule(Context{Scope: Prelude(c)}, module)
			msgs := make([]string, len(inferErrors))
			for i, e := range inferErrors {
				msgs[i] = e.Message()
			}
			if test.want == nil {
				assert.Empty(t, msgs)
			} else {
				assert.Equal(t, test.want, msgs)
			}
		})
	}
}

// `Self` names the class's own instance type inside its body, the way it does
// inside an interface. The dts converter emits it on a fused class's methods,
// as in `add(&mut self, value: T) -> Self` on `Set`, and it accounted for 228
// of `web:dom`'s diagnostics (#1725).
func TestSelfInAClassBody(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"DeclareClassMethodReturningSelf": {
			input: `
				declare class Node {
					cloneNode(&self) -> Self,
				}
				declare fn makeNode() -> Node
				val cloned = makeNode().cloneNode()
			`,
			bindingName:  "cloned",
			expectedType: "Node",
		},
		"ClassWithABodyToo": {
			input: `
				class Builder {
					count: number,
					constructor(&mut self) { self.count = 0 },
					self_(&self) -> Self { return self },
				}
				val b = Builder().self_()
			`,
			bindingName:  "b",
			expectedType: "Builder",
		},
		// `Self` carries the class's own type arguments, so it is the
		// instantiated type rather than the bare name.
		"SelfCarriesTypeArguments": {
			input: `
				declare class Box<T> {
					value: T,
					clone(&self) -> Self,
				}
				declare fn makeBox() -> Box<number>
				val copied = makeBox().clone()
				val inner = copied.value
			`,
			bindingName:  "inner",
			expectedType: "number",
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

// `Self` is bound by the class body and nowhere else.
func TestSelfIsNotVisibleOutsideAClass(t *testing.T) {
	t.Parallel()
	errs := inferModuleErrors(t, `
		declare class Node {
			cloneNode(&self) -> Self,
		}
		declare fn stray() -> Self
	`)
	require.NotEmpty(t, errorsContaining(errs, "Unknown type: Self"),
		"expected Self to be unbound outside the class; got: %v", formatErrs(errs))
}

// A bare call signature describes calling the class value, as `Boolean(x)`
// does, so it belongs on the class object beside the statics. Class inference
// had no case for it and reported `Unimplemented` on sight, which was 39 of
// `web:dom`'s diagnostics (#1723).
func TestClassCallSignature(t *testing.T) {
	t.Parallel()
	t.Run("LandsOnTheClassValueType", func(t *testing.T) {
		t.Parallel()
		input := `
			declare class Err {
				constructor(&mut self, message?: string),
				(message?: string) -> Err,
				(message?: string, options?: number) -> Err,
			}
		`
		source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
		require.Empty(t, parseErrors)

		c := NewChecker(ctx)
		_, errs := c.InferModule(Context{Scope: Prelude(c)}, module)
		require.Empty(t, errorMessages(errs))

		var got string
		for _, scope := range c.FileScopes {
			if b := scope.GetValue("Err"); b != nil {
				got = b.Type.String()
			}
		}
		// Both arms are recorded, beside the constructor rather than
		// replacing it.
		require.Equal(t,
			"{new (message?: string) -> Err, (message?: string) -> Err, "+
				"(message?: string, options?: number) -> Err}", got)
	})

	// A class with a body has nowhere to put the implementation, so the
	// signature is rejected rather than silently recorded.
	t.Run("AClassWithABodyIsRejected", func(t *testing.T) {
		t.Parallel()
		errs := inferModuleErrors(t, `
			class Foo {
				constructor(&mut self) {},
				(x: number) -> string,
			}
		`)
		require.NotEmpty(t, errorsContaining(errs,
			"Only a `declare` class can have a call signature, but class "+
				"'Foo' has a body"),
			"got: %v", formatErrs(errs))
	})
}

// A class value carries a `ConstructorElem` only when the class can be
// constructed. Escalier has no `new` expression, so a plain call resolves
// against the first constructor or call signature on the class value. A
// synthesised zero-arg constructor sits ahead of the signature, so a class
// whose only callable surface is a call signature must not carry one.
func TestCallOnlyClassHasNoConstructor(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		// The call signature is the whole callable surface, so the class
		// value holds it alone.
		"ACallSignatureAloneLeavesNoConstructor": {
			input: `
				declare class Sym {
					(desc?: string) -> symbol,
				}
			`,
			bindingName:  "Sym",
			expectedType: "{(desc?: string) -> symbol}",
		},
		"CallingSuchAClassReturnsTheSignaturesReturn": {
			input: `
				declare class Sym {
					(desc?: string) -> symbol,
				}
				val s = Sym("x")
			`,
			bindingName:  "s",
			expectedType: "symbol",
		},
		// A declared constructor is kept, and comes first.
		"BothKeepsTheConstructorFirst": {
			input: `
				declare class Wrapper {
					constructor(&mut self, value: number),
					(value: number) -> string,
				}
			`,
			bindingName:  "Wrapper",
			expectedType: "{new (value: number) -> Wrapper, (value: number) -> string}",
		},
		// So a plain call on it constructs rather than converting.
		"BothConstructsOnAPlainCall": {
			input: `
				declare class Wrapper {
					constructor(&mut self, value: number),
					(value: number) -> string,
				}
				val w = Wrapper(1)
			`,
			bindingName:  "w",
			expectedType: "Wrapper",
		},
		// A `declare` class with neither still gets the zero-arg
		// placeholder, which downstream phases rely on being there.
		"NeitherStillGetsThePlaceholder": {
			input: `
				declare class Plain {
					static readonly tag: string,
				}
			`,
			bindingName:  "Plain",
			expectedType: "{new () -> Plain, readonly tag: string}",
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
			_, errs := c.InferModule(Context{Scope: Prelude(c)}, module)
			require.Empty(t, errorMessages(errs))

			var got string
			for _, scope := range c.FileScopes {
				if b := scope.GetValue(test.bindingName); b != nil {
					got = b.Type.String()
				}
			}
			require.Equal(t, test.expectedType, got)
		})
	}
}

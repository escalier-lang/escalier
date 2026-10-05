package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// implementsCase is one source and every diagnostic it is expected to produce.
type implementsCase struct {
	name string
	src  string
	want []string
}

func runImplementsCases(t *testing.T, tests []implementsCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, errorMessagesOrNil(errs))
		})
	}
}

// errorMessagesOrNil returns each error's message, or nil when there are none, so a case
// expecting no diagnostics can leave want unset.
func errorMessagesOrNil(errs []SolverError) []string {
	if len(errs) == 0 {
		return nil
	}
	return errorMessagesOf(errs)
}

// TestImplementsConformance covers the check that a class with a body provides every member
// its `implements` clause names, in a form each entry accepts.
func TestImplementsConformance(t *testing.T) {
	runImplementsCases(t, []implementsCase{
		{
			name: "MissingProperty",
			src: `
				interface I { v: string }
				class Missing implements I { constructor(&mut self) {} }
			`,
			want: []string{"Class 'Missing' does not implement interface 'I': missing member 'v'"},
		},
		{
			name: "MistypedProperty",
			src: `
				interface I { v: string }
				class Mistyped implements I { v: number, constructor(&mut self) { self.v = 1 } }
			`,
			want: []string{"Class 'Mistyped' does not implement interface 'I': member 'v' property type does not match"},
		},
		{
			name: "MissingMemberOfAClass",
			src: `
				class Shape { v: string, constructor(&mut self) { self.v = "" } }
				class Missing implements Shape { constructor(&mut self) {} }
			`,
			want: []string{"Class 'Missing' does not implement interface 'Shape': missing member 'v'"},
		},
		{
			name: "MistypedMemberOfAClass",
			src: `
				class Shape { v: string, constructor(&mut self) { self.v = "" } }
				class Mistyped implements Shape { v: number, constructor(&mut self) { self.v = 1 } }
			`,
			want: []string{"Class 'Mistyped' does not implement interface 'Shape': member 'v' property type does not match"},
		},
		{
			name: "MemberOfAClassSatisfied",
			src: `
				class Shape { v: string, constructor(&mut self) { self.v = "" } }
				class Square implements Shape { v: string, constructor(&mut self) { self.v = "" } }
			`,
		},
		{
			name: "MissingMethod",
			src: `
				interface Greeter {
					greet(&self) -> string,
				}
				class Hello implements Greeter {}
			`,
			want: []string{"Class 'Hello' does not implement interface 'Greeter': missing member 'greet'"},
		},
		{
			name: "AllMembersSatisfied",
			src: `
				interface Greeter {
					greet(&self) -> string,
				}
				class Hello implements Greeter {
					greet(&self) -> string { return "hi" }
				}
			`,
		},
		{
			name: "InheritedMemberSatisfies",
			src: `
				interface Runnable {
					run(&self) -> string,
				}
				class Animal {
					run(&self) -> string { return "moving" }
				}
				class Dog extends Animal implements Runnable {
					constructor(&mut self) { super() }
				}
			`,
		},
		{
			name: "MemberOfAnExtendedInterfaceIsRequired",
			src: `
				interface Named { name: string }
				interface Person extends Named { age: number }
				class Bob implements Person {
					age: number,
					constructor(&mut self) { self.age = 1 }
				}
			`,
			want: []string{"Class 'Bob' does not implement interface 'Person': missing member 'name'"},
		},
		{
			name: "ReturnTypeMismatch",
			src: `
				interface Greeter {
					greet(&self) -> string,
				}
				class Hello implements Greeter {
					greet(&self) -> number { return 42 }
				}
			`,
			want: []string{"Class 'Hello' does not implement interface 'Greeter': member 'greet' signature does not match"},
		},
		{
			name: "ParamTypeMismatch",
			src: `
				interface Adder {
					add(&self, x: number) -> number,
				}
				class Bad implements Adder {
					add(&self, x: string) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Adder': member 'add' signature does not match"},
		},
		{
			name: "PropertySatisfied",
			src: `
				interface HasName {
					name: string,
				}
				class Person implements HasName {
					name: string,
				}
			`,
		},
		{
			name: "SelfReturnType",
			src: `
				interface Cloneable {
					clone(&self) -> Self,
				}
				class Box implements Cloneable {
					value: number,
					clone(&self) -> Box { return Box(self.value) }
				}
			`,
		},
		{
			name: "SelfReturnTypeOfAnotherClassIsRejected",
			src: `
				interface Cloneable {
					clone(&self) -> Self,
				}
				class Other { constructor(&mut self) {} }
				class Box implements Cloneable {
					value: number,
					clone(&self) -> Other { return Other() }
				}
			`,
			want: []string{"Class 'Box' does not implement interface 'Cloneable': member 'clone' signature does not match"},
		},
		{
			// A writable member matches exactly, which `Self` read as the interface would not.
			name: "SelfInAWritablePropertyReadsAsTheClass",
			src: `
				interface Linked {
					next: Self,
				}
				class Node implements Linked {
					next: Node,
				}
			`,
		},
		{
			// The interface names itself rather than writing `Self`, so a field typed as the
			// interface fits.
			name: "ReferenceToTheInterfaceByName",
			src: `
				interface Linked {
					next: Linked,
				}
				class Node implements Linked {
					next: Linked,
				}
			`,
		},
		{
			name: "MutSelfRequiredButClassUsesSelf",
			src: `
				interface Counter {
					increment(&mut self) -> number,
				}
				class Bad implements Counter {
					increment(&self) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Counter': member 'increment' self receiver does not match"},
		},
		{
			name: "SelfRequiredButClassUsesMutSelf",
			src: `
				interface Reader {
					read(&self) -> number,
				}
				class Bad implements Reader {
					read(&mut self) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Reader': member 'read' self receiver does not match"},
		},
		{
			name: "ConsumingClassMethodAgainstBorrowingInterface",
			src: `
				interface Reader {
					read(&self) -> number,
				}
				class Bad implements Reader {
					read(self) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Reader': member 'read' self receiver does not match"},
		},
		{
			name: "BorrowingClassMethodAgainstConsumingInterface",
			src: `
				interface Closer {
					close(mut self) -> number,
				}
				class Bad implements Closer {
					close(&mut self) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Closer': member 'close' self receiver does not match"},
		},
		{
			name: "MutSelfMatches",
			src: `
				interface Counter {
					increment(&mut self) -> number,
				}
				class Good implements Counter {
					increment(&mut self) -> number { return 0 }
				}
			`,
		},
		{
			name: "PropertyRequiredButClassDeclaresMethod",
			src: `
				interface HasName {
					name: string,
				}
				class Bad implements HasName {
					name(&self) -> string { return "x" }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'HasName': member 'name' member is not a property"},
		},
		{
			name: "OptionalPropertyDoesNotSatisfyMethod",
			src: `
				interface I {
					m(&self) -> number,
				}
				class C implements I {
					m?: fn () -> number,
				}
			`,
			want: []string{"Class 'C' does not implement interface 'I': member 'm' property is optional but interface requires it"},
		},
		{
			// A read through the interface reaches the getter and a write reaches the setter.
			name: "AccessorPairSatisfiesWritableProperty",
			src: `
				interface HasX {
					x: number,
				}
				class C implements HasX {
					_x: number,
					get x(&self) -> number { return self._x },
					set x(&mut self, v: number) { self._x = v },
				}
			`,
		},
		{
			name: "GetterSatisfiesReadonlyProperty",
			src: `
				interface HasX {
					readonly x: number,
				}
				class C implements HasX {
					get x(&self) -> number { return 1 },
				}
			`,
		},
		{
			name: "GetterAloneDoesNotSatisfyWritableProperty",
			src: `
				interface HasX {
					x: number,
				}
				class C implements HasX {
					get x(&self) -> number { return 1 },
				}
			`,
			want: []string{"Class 'C' does not implement interface 'HasX': member 'x' is readonly but interface lets it be written"},
		},
		{
			name: "AccessorPairWithANarrowerSetterIsRejected",
			src: `
				interface HasX {
					x: number | string,
				}
				class C implements HasX {
					_x: number,
					get x(&self) -> number { return self._x },
					set x(&mut self, v: number) { self._x = v },
				}
			`,
			want: []string{"Class 'C' does not implement interface 'HasX': member 'x' setter argument type does not match"},
		},
		{
			name: "ReadonlyFieldDoesNotSatisfyWritableProperty",
			src: `
				interface HasX {
					x: number,
				}
				class C implements HasX {
					readonly x: number,
				}
			`,
			want: []string{"Class 'C' does not implement interface 'HasX': member 'x' is readonly but interface lets it be written"},
		},
		{
			name: "ReadonlyFieldDoesNotSatisfySetter",
			src: `
				interface HasX {
					set x(&mut self, v: number) -> undefined,
				}
				class C implements HasX {
					readonly x: number,
				}
			`,
			want: []string{"Class 'C' does not implement interface 'HasX': member 'x' is readonly but interface lets it be written"},
		},
		{
			name: "InterfaceSetterWithMatchingReceiver",
			src: `
				interface HasValue {
					set value(&mut self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(&mut self, x: number) { self._value = x },
				}
			`,
		},
		{
			// The interface promises the setter can be called on an immutable receiver, and
			// the class needs a mutable one.
			name: "SetterMutSelfMismatch",
			src: `
				interface HasValue {
					set value(&self, x: number) -> undefined,
				}
				class Box implements HasValue {
					_value: number,
					set value(&mut self, x: number) { self._value = x },
				}
			`,
			want: []string{"Class 'Box' does not implement interface 'HasValue': member 'value' self receiver does not match"},
		},
		{
			name: "SetterArgTypeMismatch",
			src: `
				interface HasValue {
					set value(&mut self, x: number) -> undefined,
				}
				class Bad implements HasValue {
					_value: string,
					set value(&mut self, x: string) { self._value = x },
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'HasValue': member 'value' setter argument type does not match"},
		},
		{
			name: "GetterMutSelfMatches",
			src: `
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
			`,
		},
		{
			// A caller holding the interface expects a read not to mutate.
			name: "GetterMutSelfMismatch",
			src: `
				interface ReadSize {
					get size(&self) -> number,
				}
				class Container implements ReadSize {
					_cache: number,
					get size(&mut self) -> number { return self._cache },
				}
			`,
			want: []string{"Class 'Container' does not implement interface 'ReadSize': member 'size' self receiver does not match"},
		},
		{
			name: "GetterReturnTypeMismatch",
			src: `
				interface HasName {
					get name(&self) -> string,
				}
				class Bad implements HasName {
					get name(&self) -> number { return 0 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'HasName': member 'name' getter return type does not match"},
		},
		{
			name: "GenericClassImplementsGenericInterface",
			src: `
				interface Container<T> {
					value: T,
				}
				class Box<T> implements Container<T> {
					value: T,
				}
			`,
		},
		{
			// T stands for every type, so a field typed `T` does not provide the `string` the
			// interface asks for.
			name: "GenericClassFieldDoesNotSatisfyAConcreteMember",
			src: `
				interface Container<T> {
					readonly value: T,
				}
				class Box<T> implements Container<string> {
					value: T,
				}
			`,
			want: []string{"Class 'Box' does not implement interface 'Container': member 'value' property type does not match"},
		},
		{
			// The class method's return type is a subtype of the interface's, so the class
			// can stand in for the interface.
			name: "NarrowerClassReturnSatisfiesIface",
			src: `
				interface Producer {
					produce(&self) -> number | string,
				}
				class IntProducer implements Producer {
					produce(&self) -> number { return 1 }
				}
			`,
		},
		{
			// A caller holding a Producer expects only a `number` back.
			name: "WiderClassReturnRejected",
			src: `
				interface Producer {
					produce(&self) -> number,
				}
				class Bad implements Producer {
					produce(&self) -> number | string { return 1 }
				}
			`,
			want: []string{"Class 'Bad' does not implement interface 'Producer': member 'produce' signature does not match"},
		},
		{
			// A parameter wider than the interface's accepts everything a caller may pass.
			name: "WiderClassParamSatisfiesIface",
			src: `
				interface Sink {
					accept(&self, x: number) -> undefined,
				}
				class Lenient implements Sink {
					accept(&self, x: number | string) -> undefined { return undefined }
				}
			`,
		},
		{
			name: "MultipleImplementsOneMissing",
			src: `
				interface A {
					a(&self) -> number,
				}
				interface B {
					b(&self) -> number,
				}
				class Partial implements A, B {
					a(&self) -> number { return 1 }
				}
			`,
			want: []string{"Class 'Partial' does not implement interface 'B': missing member 'b'"},
		},
		{
			name: "OptionalClassPropertyDoesNotSatisfyRequiredInterfaceProperty",
			src: `
				interface HasName {
					name: string,
				}
				class Person implements HasName {
					name?: string,
				}
			`,
			want: []string{"Class 'Person' does not implement interface 'HasName': member 'name' property is optional but interface requires it"},
		},
		{
			name: "OptionalClassPropertyDoesNotSatisfyInterfaceGetter",
			src: `
				interface HasName {
					get name(&self) -> string,
				}
				class Person implements HasName {
					name?: string,
				}
			`,
			want: []string{"Class 'Person' does not implement interface 'HasName': member 'name' property is optional but interface requires it"},
		},
		{
			name: "OptionalClassPropertyDoesNotSatisfyInterfaceSetter",
			src: `
				interface HasValue {
					set value(&mut self, x: number) -> undefined,
				}
				class Box implements HasValue {
					value?: number,
				}
			`,
			want: []string{"Class 'Box' does not implement interface 'HasValue': member 'value' property is optional but interface requires it"},
		},
		{
			name: "OptionalPropertyAbsentOnClassIsAllowed",
			src: `
				interface HasOptional {
					nickname?: string,
				}
				class Person implements HasOptional {
					name: string,
				}
			`,
		},
		{
			// The same clause on a `declare` class takes the member instead. See
			// TestDeclareImplementsConformance/MissingMemberIsInherited.
			name: "NonDeclareClassWithoutTheMember",
			src: `
				interface ParentNode {
					querySelector(&self, selectors: string) -> string,
				}
				class Element implements ParentNode {}
			`,
			want: []string{"Class 'Element' does not implement interface 'ParentNode': missing member 'querySelector'"},
		},
	})
}

// TestDeclareImplementsConformance covers the `implements` check on a `declare` class. A
// member the class does not declare is taken from the entry rather than missing. A member it
// does declare overrides the entry's and still has to fit it.
func TestDeclareImplementsConformance(t *testing.T) {
	runImplementsCases(t, []implementsCase{
		{
			name: "MissingMemberIsInherited",
			src: `
				interface ParentNode {
					querySelector(&self, selectors: string) -> string,
				}
				declare class Element implements ParentNode {}
			`,
		},
		{
			name: "MissingMemberOfAClassIsNotReported",
			src: `
				declare class Shape { v: string }
				declare class Square implements Shape {}
			`,
		},
		{
			name: "UnrelatedRestatementIsRejected",
			src: `
				interface I { v: string }
				declare class C implements I { v: number }
			`,
			want: []string{"Class 'C' does not implement interface 'I': member 'v' property type does not match"},
		},
		{
			name: "WideningRestatementIsRejected",
			src: `
				interface MessageEventTarget {
					onmessage: string,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string | number,
				}
			`,
			want: []string{"Class 'MessagePort' does not implement interface 'MessageEventTarget': member 'onmessage' property type does not match"},
		},
		{
			// Comparing overload sets arm by arm is deferred to #651, so a restated overload
			// is left unchecked.
			name: "RestatedOverloadIsNotCompared",
			src: `
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
		{
			// Reading is covariant, so a readonly member may narrow.
			name: "NarrowingAReadonlyRestatementIsAccepted",
			src: `
				interface MessageEventTarget {
					readonly onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					readonly onmessage: string,
				}
			`,
		},
		{
			// A writable member is written through as well as read, so a write of `number`
			// satisfies the interface's type and not the class's.
			name: "NarrowingAMutableRestatementIsRejected",
			src: `
				interface MessageEventTarget {
					onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string,
				}
			`,
			want: []string{"Class 'MessagePort' does not implement interface 'MessageEventTarget': member 'onmessage' is a mutable property, so its type has to match the interface's exactly"},
		},
		{
			// TypeScript accepts this, and the generated lib's `ByteLengthQueuingStrategy`
			// restates `QueuingStrategy`'s `highWaterMark` this way. A class with a body is
			// rejected, as ReadonlyFieldDoesNotSatisfyWritableProperty covers.
			name: "ReadonlyRestatementOfAWritableMemberIsAccepted",
			src: `
				interface QueuingStrategy {
					highWaterMark?: number,
				}
				declare class ByteLengthQueuingStrategy implements QueuingStrategy {
					readonly highWaterMark: number,
				}
			`,
		},
		{
			name: "RestatingAMutableMemberExactlyIsAccepted",
			src: `
				interface MessageEventTarget {
					onmessage: string | number,
				}
				declare class MessagePort implements MessageEventTarget {
					onmessage: string | number,
				}
			`,
		},
		{
			// The superclass member settles the name, and reading is covariant, so `number`
			// satisfies both interfaces.
			name: "AReadonlySuperclassMemberResolvesTheConflict",
			src: `
				interface ChildNode {
					readonly nodeName: string | number,
				}
				interface ParentNode {
					readonly nodeName: number,
				}
				declare class Node {
					readonly nodeName: number,
				}
				declare class Element extends Node implements ChildNode, ParentNode { constructor(&mut self) }
			`,
		},
		{
			// A writable member matches exactly, so no single type satisfies two interfaces
			// declaring the name differently.
			name: "AMutableSuperclassMemberCannotResolveTheConflict",
			src: `
				interface ChildNode {
					nodeName: string | number,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Node {
					nodeName: number,
				}
				declare class Element extends Node implements ChildNode, ParentNode { constructor(&mut self) }
			`,
			want: []string{"Class 'Element' does not implement interface 'ChildNode': member 'nodeName' is a mutable property, so its type has to match the interface's exactly"},
		},
		{
			name: "ASuperclassMemberContradictingAnInterfaceIsRejected",
			src: `
				interface ChildNode {
					nodeName: string,
				}
				interface ParentNode {
					nodeName: number,
				}
				declare class Node {
					nodeName: string,
				}
				declare class Element extends Node implements ChildNode, ParentNode { constructor(&mut self) }
			`,
			want: []string{"Class 'Element' does not implement interface 'ParentNode': member 'nodeName' property type does not match"},
		},
		{
			name: "ARestatedReadonlyMemberResolvesTheConflict",
			src: `
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
		{
			name: "ARestatedMutableMemberCannotResolveTheConflict",
			src: `
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
			want: []string{"Class 'Element' does not implement interface 'ChildNode': member 'nodeName' is a mutable property, so its type has to match the interface's exactly"},
		},
	})
}

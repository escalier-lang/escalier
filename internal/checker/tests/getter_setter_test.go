package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestGetterSetterAccess(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrors []string
	}{
		"ReadGetterOnlyProperty": {
			input: `
				class Circle {
					radius: number,
					get area(self) -> number {
						return 3.14 * self.radius * self.radius
					},
				}
				val c = Circle(5)
				val area = c.area
			`,
			expectedErrors: nil,
		},
		"WriteGetterOnlyPropertyShouldError": {
			input: `
				class Circle {
					radius: number,
					get area(self) -> number {
						return 3.14 * self.radius * self.radius
					},
				}
				val c: mut Circle = Circle(5)
				fn main() {
					c.area = 100
				}
			`,
			expectedErrors: []string{
				"Unknown property 'area' in object type {radius: number, get area(self) -> number}",
				"100 cannot be assigned to undefined",
			},
		},
		"WriteSetterOnlyProperty": {
			input: `
				class Temperature {
					celsius: number,
					set fahrenheit(mut self, value: number) {
						self.celsius = (value - 32) * 5 / 9
					},
				}
				val temp: mut Temperature = Temperature(25)
				fn main() {
					temp.fahrenheit = 86
				}
			`,
			expectedErrors: nil,
		},
		"ReadSetterOnlyPropertyShouldError": {
			input: `
				class Temperature {
					celsius: number,
					set fahrenheit(mut self, value: number) {
						self.celsius = (value - 32) * 5 / 9
					},
				}
				val temp = Temperature(25)
				val f = temp.fahrenheit
			`,
			expectedErrors: []string{
				"Unknown property 'fahrenheit' in object type {celsius: number, set fahrenheit(mut self, value: number) -> undefined}",
			},
		},
		"ReadAndWriteWithBothGetterAndSetter": {
			input: `
				declare fn split(s: string, delimiter: string) -> Array<string>
				class Person {
					firstName: string,
					lastName: string,
					get fullName(self) -> string {
						return self.firstName ++ " " ++ self.lastName
					},
					set fullName(mut self, value: string) {
						val parts = split(value, " ")
						self.firstName = parts[0]
						self.lastName = parts[1]
					},
				}
				val person: mut Person = Person("John", "Doe")
				val name = person.fullName
				fn main() {
					person.fullName = "Jane Smith"
				}
			`,
			expectedErrors: nil,
		},
		"WriteSetterViaSpreadSource": {
			input: `
				class Base {
					_v: number,
					set value(mut self, v: number) {
						self._v = v
					},
				}
				val b: mut Base = Base(1)
				fn main() {
					b.value = 42
				}
			`,
			expectedErrors: nil,
		},
		"ReadGetterViaSpreadSource": {
			input: `
				class Base {
					_v: number,
					get value(self) -> number {
						return self._v
					},
				}
				val obj = {_v: 0, ...Base(1)}
				val v = obj.value
			`,
			expectedErrors: nil,
		},
		"ReadSetterViaSpreadSourceShouldError": {
			input: `
				class Base {
					_v: number,
					set value(mut self, v: number) {
						self._v = v
					},
				}
				val obj = {_v: 0, ...Base(1)}
				val v = obj.value
			`,
			expectedErrors: []string{
				"Unknown property 'value' in object type {_v: 0, ...Base}",
			},
		},
		// Union rest path - setter-only keys on union members
		// should not cause nil values in the rest object.
		"UnionDestructureWithRestSkipsSetterOnlyFields": {
			input: `
				class A {
					x: number,
					set s(mut self, v: number) {},
				}
				class B {
					x: string,
					set s(mut self, v: string) {},
				}
				fn foo(u: A | B) {
					val {x, s, ...rest} = u
				}
			`,
			expectedErrors: []string{
				"Unknown property 's' in object type A | B",
			},
		},
		// Verify that the per-member lazy substitution cache (#461) separates
		// read (getter) and write (setter) results. Without AccessMode in the
		// cache key, reading a getter-only property could pollute the cache so
		// that a subsequent write incorrectly succeeds (or vice versa).
		"GenericClassGetterThenSetterCacheIsolation": {
			input: `
				class Box<T> {
					value: T,
					get contents(self) -> T {
						return self.value
					},
					set contents(mut self, v: T) {
						self.value = v
					},
				}
				val b: mut Box<number> = Box(1)
				val c = b.contents
				fn main() {
					b.contents = 42
				}
			`,
			expectedErrors: nil,
		},
		// A `mut self` getter mutates a cached field on `self` and
		// returns it. Reading from a `mut` binding must succeed: type
		// checking accepts the mutation inside the body, and the
		// computed return type matches the declared one.
		"MutSelfGetterMutatesCacheOnMutBinding": {
			input: `
				class Counter {
					_seen: number,
					get next(mut self) -> number {
						self._seen = self._seen + 1
						return self._seen
					},
				}
				val mut c: mut Counter = Counter(0)
				val n = c.next
			`,
			expectedErrors: nil,
		},
		// A `mut self` getter must be hidden on a non-mutable receiver,
		// mirroring the rule for `mut self` methods. Reading `c.next`
		// would silently mutate state behind a `val` binding's back.
		"MutSelfGetterOnNonMutBindingFails": {
			input: `
				class Counter {
					_seen: number,
					get next(mut self) -> number {
						self._seen = self._seen + 1
						return self._seen
					},
				}
				val c = Counter(0)
				val n = c.next
			`,
			expectedErrors: []string{
				"Unknown property 'next' in object type {_seen: number, get next(mut self) -> number}",
			},
		},
		// A `mut self` setter must remain visible during write lookup
		// on a non-mutable receiver so the assignment-site mutability
		// check fires with a clear CannotMutateImmutable error rather
		// than falling through to UnknownProperty + a follow-on
		// "cannot be assigned to undefined" mismatch.
		"WriteMutSelfSetterOnNonMutBindingFails": {
			input: `
				class Counter {
					_v: number,
					set value(mut self, v: number) {
						self._v = v
					},
				}
				val c = Counter(0)
				fn main() {
					c.value = 1
				}
			`,
			expectedErrors: []string{
				"Cannot mutate immutable type: Counter",
			},
		},
		// The reverse order: write first, then read.
		"GenericClassSetterThenGetterCacheIsolation": {
			input: `
				class Box<T> {
					value: T,
					get contents(self) -> T {
						return self.value
					},
					set contents(mut self, v: T) {
						self.value = v
					},
				}
				val b: mut Box<string> = Box("hello")
				fn main() {
					b.contents = "world"
				}
				val c: string = b.contents
			`,
			expectedErrors: nil,
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
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			assert.Len(t, parseErrors, 0, "Expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			_, inferErrors := c.InferModule(inferCtx, module)

			var errorMessages []string
			for _, err := range inferErrors {
				errorMessages = append(errorMessages, err.Message())
			}
			assert.Equal(t, test.expectedErrors, errorMessages)
		})
	}
}

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
				class Circle(radius: number) {
					radius,
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
				class Circle(radius: number) {
					radius,
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
				class Temperature(celsius: number) {
					celsius,
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
				class Temperature(celsius: number) {
					celsius,
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
				class Person(firstName: string, lastName: string) {
					firstName,
					lastName,
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
		"ObjectLiteralReadGetter": {
			input: `
				val value: number = 5
				val obj = {
					get value(self) {
						return self._value
					},
					_value: value,
				}
				val v = obj.value
			`,
			expectedErrors: nil,
		},
		"ObjectLiteralWriteSetterOnly": {
			input: `
				val obj = {
					_value: 0:number,
					set value(mut self, v: number) {
						self._value = v
					},
				}
				val mobj: mut typeof obj = obj
				fn main() {
					mobj.value = 42
				}
			`,
			expectedErrors: nil,
		},
		"WriteSetterViaSpreadSource": {
			input: `
				class Base(_v: number) {
					_v,
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
				class Base(_v: number) {
					_v,
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
				class Base(_v: number) {
					_v,
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
				class A(x: number) {
					x,
					set s(mut self, v: number) {},
				}
				class B(x: string) {
					x,
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

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)

			var errorMessages []string
			for _, err := range inferErrors {
				errorMessages = append(errorMessages, err.Message())
			}
			assert.Equal(t, test.expectedErrors, errorMessages)
		})
	}
}

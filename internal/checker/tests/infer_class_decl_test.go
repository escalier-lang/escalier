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
)

func TestCheckClassDeclNoErrors(t *testing.T) {
	tests := map[string]struct {
		input               string
		expectedTypes       map[string]string
		expectedTypeAliases map[string]string
	}{
		"SimpleDecl": {
			input: `
				class Point(x: number, y: number) {
					x,
					y: y,
					z: 0:number,
				}

				val p = Point(5, 10)
				val {x, y, z} = p
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> mut? Point throws never}",
				"p":     "Point",
				"x":     "number",
				"y":     "number",
				"z":     "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, z: number}",
			},
		},
		"SimpleDeclWithMethods": {
			input: `
				declare fn sqrt(x: number) -> number
				class Point(x: number, y: number) {
					x,
					y,
					length(self) {
						return sqrt(self.x * self.x + self.y * self.y)
					},
					add(self, other: Point) {
						return Point(self.x + other.x, self.y + other.y)
					},
				}

				val p = Point(5, 10)
				val len = p.length()
				val q = p.add(Point(1, 2))
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> mut? Point throws never}",
				"p":     "Point",
				"q":     "Point",
				"len":   "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, length(self) -> number throws never, add(self, other: Point) -> Point throws never}",
			},
		},
		"ClassWithFluentMutatingMethods": {
			input: `
				declare fn sqrt(x: number) -> number
				class Point(x: number, y: number) {
					x,
					y,
					scale(mut self, factor: number) {
						self.x = self.x * factor
						self.y = self.y * factor
						return self
					},
					translate(mut self, dx: number, dy: number) {
						self.x = self.x + dx
						self.y = self.y + dy
						return self
					},
				}

				val p = Point(5, 10)
				val q = p.scale(2).translate(1, -1)
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> mut? Point throws never}",
				"p":     "Point",
				"q":     "mut Point",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, scale(mut self, factor: number) -> mut Point throws never, translate(mut self, dx: number, dy: number) -> mut Point throws never}",
			},
		},
		"SimpleDeclWithComputedMembers": {
			input: `
				val bar = "bar"
				val baz = "baz"
				class Foo() {
					[bar]: 42:number,
					[baz](self) {
						return self[bar]
					}
				}

				val foo = Foo()
				val fooBar = foo[bar]
				val fooBaz = foo[baz]()
			`,
			expectedTypes: map[string]string{
				"Foo":    "{new fn () -> mut? Foo throws never}",
				"fooBar": "number",
				"fooBaz": "number",
			},
			expectedTypeAliases: map[string]string{
				"Foo": "{bar: number, baz(self) -> number throws never}",
			},
		},
		"ClassWithStaticMethod": {
			input: `
				class MyMath() {
					static add(a: number, b: number) {
						return a + b
					},
				}

				val m = MyMath()
				val result = MyMath.add(5, 3)
			`,
			expectedTypes: map[string]string{
				"MyMath": "{new fn () -> mut? MyMath throws never, add(a: number, b: number) -> number throws never}",
				"m":      "MyMath",
				"result": "number",
			},
			expectedTypeAliases: map[string]string{
				"MyMath": "{}",
			},
		},
		"ClassWithStaticAndInstanceMethods": {
			input: `
				class Point(x: number, y: number) {
					x,
					y,
					static origin() {
						return Point(0, 0)
					},
					length(self) {
						return self.x + self.y
					},
				}

				val p = Point(3, 4)
				val origin = Point.origin()
				val len = p.length()
			`,
			expectedTypes: map[string]string{
				"Point":  "{new fn (x: number, y: number) -> mut? Point throws never, origin() -> Point throws never}",
				"p":      "Point",
				"origin": "Point",
				"len":    "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, length(self) -> number throws never}",
			},
		},
		"ClassWithInstanceGetter": {
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
			expectedTypes: map[string]string{
				"Circle": "{new fn (radius: number) -> mut? Circle throws never}",
				"c":      "Circle",
				"area":   "number",
			},
			expectedTypeAliases: map[string]string{
				"Circle": "{radius: number, get area(self) -> number throws never}",
			},
		},
		"ClassWithInstanceSetter": {
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
			expectedTypes: map[string]string{
				"Temperature": "{new fn (celsius: number) -> mut? Temperature throws never}",
				"temp":        "mut Temperature",
			},
			expectedTypeAliases: map[string]string{
				"Temperature": "{celsius: number, set fahrenheit(mut self, value: number) -> undefined throws never}",
			},
		},
		"ClassWithGetterAndSetter": {
			input: `
				declare fn split(s: string, delimiter: string) -> Array<string>
				class Person(firstName: string, lastName: string) {
					firstName,
					lastName,
					get fullName(self) -> string {
						return self.firstName ++ " " ++ self.lastName
					},
					set fullName(self, value: string) {
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
			expectedTypes: map[string]string{
				"Person": "{new fn (firstName: string, lastName: string) -> mut? Person throws never}",
				"person": "mut Person",
				"name":   "string",
			},
			expectedTypeAliases: map[string]string{
				"Person": "{firstName: string, lastName: string, get fullName(self) -> string throws never, set fullName(mut self, value: string) -> undefined throws never}",
			},
		},
		"ClassWithStaticGetter": {
			input: `
				class Config() {
					static get version() -> string {
						return "1.0.0"
					},
				}

				val config = Config()
				val version = Config.version
			`,
			expectedTypes: map[string]string{
				"Config":  "{new fn () -> mut? Config throws never, get version(self) -> string throws never}",
				"config":  "Config",
				"version": "string",
			},
			expectedTypeAliases: map[string]string{
				"Config": "{}",
			},
		},
		"ClassWithStaticAndInstanceFields": {
			input: `
				class Counter(initialValue: number) {
					value: initialValue,
					static totalInstances: 0:number,
					static defaultValue: 100,
				}

				val counter1 = Counter(5)
				val value1 = counter1.value
				val totalInstances = Counter.totalInstances
				val defaultVal = Counter.defaultValue
			`,
			expectedTypes: map[string]string{
				"Counter":        "{new fn (initialValue: number) -> mut? Counter throws never, totalInstances: number, defaultValue: 100}",
				"counter1":       "Counter",
				"value1":         "number",
				"totalInstances": "number",
				"defaultVal":     "100",
			},
			expectedTypeAliases: map[string]string{
				"Counter": "{value: number}",
			},
		},
		// TODO: figure out how we want to handle static setters
		// "ClassWithStaticSetter": {
		// 	input: `
		// 		class GlobalState() {
		// 			static set debugMode(value: boolean) {
		// 				// Implementation would set global debug state
		// 				return
		// 			},
		// 		}

		// 		val state = GlobalState()
		// 		fn main() {
		// 			GlobalState.debugMode = true
		// 		}
		// 	`,
		// 	expectedTypes: map[string]string{
		// 		"GlobalState": "{new fn () -> GlobalState throws never, set debugMode(mut self, value: boolean) -> undefined throws never}",
		// 		"state":       "mut GlobalState",
		// 	},
		// 	expectedTypeAliases: map[string]string{
		// 		"GlobalState": "{}",
		// 	},
		// },
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
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %s\n", i, err.String())
				}
			}
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.Schema = schema
			scope, inferErrors := c.InferModule(inferCtx, module)
			if len(inferErrors) > 0 {
				for i, err := range inferErrors {
					fmt.Printf("Infer Error[%d]: %s\n", i, err.Message())
					fmt.Printf("Infer Error[%d]: %#v\n", i, err)
				}
				assert.Equal(t, inferErrors, []*Error{})
			}

			// Collect actual types for verification
			actualTypes := make(map[string]string)
			for name, binding := range scope.Values {
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

			for expectedName, expectedType := range test.expectedTypeAliases {
				actualTypeAlias, exists := scope.Types[expectedName]
				assert.True(t, exists, "Expected type alias %s to be declared", expectedName)
				if exists {
					assert.Equal(t, expectedType, actualTypeAlias.Type.String(), "Type mismatch for type alias %s", expectedName)
				}
			}

			// Note: We don't check for unexpected variables since the scope includes
			// prelude functions and operators that are implementation details
		})
	}
}

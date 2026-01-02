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
		"GenericClass": {
			input: `
				class Box<T>(value: T) {
					value,
				}

				val box = Box(42:number)
				val boxValue = box.value
			`,
			expectedTypes: map[string]string{
				"Box":      "{new fn <T>(value: T) -> mut? Box<T> throws never}",
				"box":      "Box<number>",
				"boxValue": "number",
			},
		},
		"ClassWithExtends": {
			input: `
				class Animal(name: string) {
					name,
					speak(self) -> string {
						return "Animal speaks"
					},
				}

				class Dog extends Animal(name: string, breed: string) {
					breed,
					speak(self) -> string {
						return "Woof!"
					},
				}

				val animal = Animal("Generic")
				val dog = Dog("Buddy", "Golden Retriever")
				val dogName = dog.name
				val dogBreed = dog.breed
				val animalSound = animal.speak()
				val dogSound = dog.speak()
			`,
			expectedTypes: map[string]string{
				"Animal":      "{new fn (name: string) -> mut? Animal throws never}",
				"Dog":         "{new fn (name: string, breed: string) -> mut? Dog throws never}",
				"animal":      "Animal",
				"dog":         "Dog",
				"dogName":     "string",
				"dogBreed":    "string",
				"animalSound": "string",
				"dogSound":    "string",
			},
			expectedTypeAliases: map[string]string{
				"Animal": "{name: string, speak(self) -> string throws never}",
				"Dog":    "{breed: string, speak(self) -> string throws never}",
			},
		},
		"ClassWithExtendsAccessingParentMethods": {
			input: `
				class Vehicle(make: string, model: string) {
					make,
					model,
					getInfo(self) -> string {
						return self.make ++ " " ++ self.model
					},
				}

				class Car extends Vehicle(make: string, model: string, doors: number) {
					doors,
					getFullInfo(self) -> string {
						return self.getInfo() ++ " (" ++ "doors" ++ ")"
					},
				}

				val car = Car("Toyota", "Camry", 4)
				val info = car.getInfo()
				val fullInfo = car.getFullInfo()
				val carMake = car.make
				val carDoors = car.doors
			`,
			expectedTypes: map[string]string{
				"Vehicle":  "{new fn (make: string, model: string) -> mut? Vehicle throws never}",
				"Car":      "{new fn (make: string, model: string, doors: number) -> mut? Car throws never}",
				"car":      "Car",
				"info":     "string",
				"fullInfo": "string",
				"carMake":  "string",
				"carDoors": "number",
			},
			expectedTypeAliases: map[string]string{
				"Vehicle": "{make: string, model: string, getInfo(self) -> string throws never}",
				"Car":     "{doors: number, getFullInfo(self) -> string throws never}",
			},
		},
		"ClassWithExtendsMultipleFields": {
			input: `
				class Base(a: number, b: string) {
					a,
					b,
				}

				class Extended extends Base(a: number, b: string, c: boolean, d: number) {
					c,
					d,
				}

				val ext = Extended(1, "test", true, 42)
				val extA = ext.a
				val extB = ext.b
				val extC = ext.c
				val extD = ext.d
			`,
			expectedTypes: map[string]string{
				"Base":     "{new fn (a: number, b: string) -> mut? Base throws never}",
				"Extended": "{new fn (a: number, b: string, c: boolean, d: number) -> mut? Extended throws never}",
				"ext":      "Extended",
				"extA":     "number",
				"extB":     "string",
				"extC":     "boolean",
				"extD":     "number",
			},
			expectedTypeAliases: map[string]string{
				"Base":     "{a: number, b: string}",
				"Extended": "{c: boolean, d: number}",
			},
		},
		"ClassWithExtendsAndGetter": {
			input: `
				class Shape(color: string) {
					color,
				}

				class Circle extends Shape(color: string, radius: number) {
					radius,
					get area(self) -> number {
						return 3.14 * self.radius * self.radius
					},
				}

				val circle = Circle("red", 5)
				val circleColor = circle.color
				val circleArea = circle.area
			`,
			expectedTypes: map[string]string{
				"Shape":       "{new fn (color: string) -> mut? Shape throws never}",
				"Circle":      "{new fn (color: string, radius: number) -> mut? Circle throws never}",
				"circle":      "Circle",
				"circleColor": "string",
				"circleArea":  "number",
			},
			expectedTypeAliases: map[string]string{
				"Shape":  "{color: string}",
				"Circle": "{radius: number, get area(self) -> number throws never}",
			},
		},
		"ClassWithExtendsIndexAccess": {
			input: `
				class Container(size: number) {
					size,
				}

				class Box extends Container(size: number, contents: string) {
					contents,
				}

				val box = Box(10, "items")
				val boxSize = box["size"]
				val boxContents = box["contents"]
			`,
			expectedTypes: map[string]string{
				"Box":         "{new fn (size: number, contents: string) -> mut? Box throws never}",
				"box":         "Box",
				"boxSize":     "number",
				"boxContents": "string",
			},
			expectedTypeAliases: map[string]string{
				"Container": "{size: number}",
				"Box":       "{contents: string}",
			},
		},
		"MultiLevelInheritance": {
			input: `
				class GrandParent(id: number) {
					id,
				}

				class Parent extends GrandParent(id: number, name: string) {
					name,
				}

				class Child extends Parent(id: number, name: string, age: number) {
					age,
				}

				val child = Child(1, "Alice", 10)
				val childId = child.id
				val childName = child.name
				val childAge = child.age
			`,
			expectedTypes: map[string]string{
				"GrandParent": "{new fn (id: number) -> mut? GrandParent throws never}",
				"Parent":      "{new fn (id: number, name: string) -> mut? Parent throws never}",
				"Child":       "{new fn (id: number, name: string, age: number) -> mut? Child throws never}",
				"child":       "Child",
				"childId":     "number",
				"childName":   "string",
				"childAge":    "number",
			},
			expectedTypeAliases: map[string]string{
				"GrandParent": "{id: number}",
				"Parent":      "{name: string}",
				"Child":       "{age: number}",
			},
		},
		"ClassWithExtendsAndMutatingMethod": {
			input: `
				class Counter(value: number) {
					value,
				}

				class ExtendedCounter extends Counter(value: number, step: number) {
					step,
					increment(mut self) {
						self.value = self.value + self.step
						return self
					},
				}

				val counter = ExtendedCounter(0, 5)
				val incremented = counter.increment()
			`,
			expectedTypes: map[string]string{
				"Counter":         "{new fn (value: number) -> mut? Counter throws never}",
				"ExtendedCounter": "{new fn (value: number, step: number) -> mut? ExtendedCounter throws never}",
				"counter":         "ExtendedCounter",
				"incremented":     "mut ExtendedCounter",
			},
			expectedTypeAliases: map[string]string{
				"Counter":         "{value: number}",
				"ExtendedCounter": "{step: number, increment(mut self) -> mut ExtendedCounter throws never}",
			},
		},
		// TODO: Generic class inheritance requires type parameter substitution when accessing parent members
		"ClassWithExtendsOverridingMethod": {
			input: `
				class Animal(name: string) {
					name,
					makeSound(self) -> string {
						return "Some sound"
					},
				}

				class Cat extends Animal(name: string, lives: number) {
					lives,
					makeSound(self) -> string {
						return "Meow"
					},
				}

				val cat = Cat("Whiskers", 9)
				val sound = cat.makeSound()
				val catName = cat.name
			`,
			expectedTypes: map[string]string{
				"Animal":  "{new fn (name: string) -> mut? Animal throws never}",
				"Cat":     "{new fn (name: string, lives: number) -> mut? Cat throws never}",
				"cat":     "Cat",
				"sound":   "string",
				"catName": "string",
			},
			expectedTypeAliases: map[string]string{
				"Animal": "{name: string, makeSound(self) -> string throws never}",
				"Cat":    "{lives: number, makeSound(self) -> string throws never}",
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
			inferErrors := c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace
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

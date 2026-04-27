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
				"Point": "{new fn (x: number, y: number) -> Point}",
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
				"Point": "{new fn (x: number, y: number) -> Point}",
				"p":     "Point",
				"q":     "Point",
				"len":   "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, length(self) -> number, add(self, other: Point) -> Point}",
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

				val mut p = Point(5, 10)
				val q = p.scale(2).translate(1, -1)
			`,
			expectedTypes: map[string]string{
				"Point": "{new fn (x: number, y: number) -> Point}",
				"p":     "mut Point",
				"q":     "mut Point",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, scale(mut self, factor: number) -> mut Point, translate(mut self, dx: number, dy: number) -> mut Point}",
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
				"Foo":    "{new fn () -> Foo}",
				"fooBar": "number",
				"fooBaz": "number",
			},
			expectedTypeAliases: map[string]string{
				"Foo": "{bar: number, baz(self) -> number}",
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
				"MyMath": "{new fn () -> MyMath, add(a: number, b: number) -> number}",
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
				"Point":  "{new fn (x: number, y: number) -> Point, origin() -> Point}",
				"p":      "Point",
				"origin": "Point",
				"len":    "number",
			},
			expectedTypeAliases: map[string]string{
				"Point": "{x: number, y: number, length(self) -> number}",
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
				"Circle": "{new fn (radius: number) -> Circle}",
				"c":      "Circle",
				"area":   "number",
			},
			expectedTypeAliases: map[string]string{
				"Circle": "{radius: number, get area(self) -> number}",
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
				"Temperature": "{new fn (celsius: number) -> Temperature}",
				"temp":        "mut Temperature",
			},
			expectedTypeAliases: map[string]string{
				"Temperature": "{celsius: number, set fahrenheit(mut self, value: number) -> undefined}",
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
				"Person": "{new fn (firstName: string, lastName: string) -> Person}",
				"person": "mut Person",
				"name":   "string",
			},
			expectedTypeAliases: map[string]string{
				"Person": "{firstName: string, lastName: string, get fullName(self) -> string, set fullName(mut self, value: string) -> undefined}",
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
				"Config":  "{new fn () -> Config, get version(self) -> string}",
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
				"Counter":        "{new fn (initialValue: number) -> Counter, totalInstances: number, defaultValue: 100}",
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
				"Box":      "{new fn <T>(value: T) -> Box<T>}",
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

				class Dog(name: string, breed: string) extends Animal {
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
				"Animal":      "{new fn (name: string) -> Animal}",
				"Dog":         "{new fn (name: string, breed: string) -> Dog}",
				"animal":      "Animal",
				"dog":         "Dog",
				"dogName":     "string",
				"dogBreed":    "string",
				"animalSound": "string",
				"dogSound":    "string",
			},
			expectedTypeAliases: map[string]string{
				"Animal": "{name: string, speak(self) -> string}",
				"Dog":    "{breed: string, speak(self) -> string}",
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

				class Car(make: string, model: string, doors: number) extends Vehicle {
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
				"Vehicle":  "{new fn (make: string, model: string) -> Vehicle}",
				"Car":      "{new fn (make: string, model: string, doors: number) -> Car}",
				"car":      "Car",
				"info":     "string",
				"fullInfo": "string",
				"carMake":  "string",
				"carDoors": "number",
			},
			expectedTypeAliases: map[string]string{
				"Vehicle": "{make: string, model: string, getInfo(self) -> string}",
				"Car":     "{doors: number, getFullInfo(self) -> string}",
			},
		},
		"ClassWithExtendsMultipleFields": {
			input: `
				class Base(a: number, b: string) {
					a,
					b,
				}

				class Extended(a: number, b: string, c: boolean, d: number) extends Base {
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
				"Base":     "{new fn (a: number, b: string) -> Base}",
				"Extended": "{new fn (a: number, b: string, c: boolean, d: number) -> Extended}",
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

				class Circle(color: string, radius: number) extends Shape {
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
				"Shape":       "{new fn (color: string) -> Shape}",
				"Circle":      "{new fn (color: string, radius: number) -> Circle}",
				"circle":      "Circle",
				"circleColor": "string",
				"circleArea":  "number",
			},
			expectedTypeAliases: map[string]string{
				"Shape":  "{color: string}",
				"Circle": "{radius: number, get area(self) -> number}",
			},
		},
		"ClassWithExtendsIndexAccess": {
			input: `
				class Container(size: number) {
					size,
				}

				class Box(size: number, contents: string) extends Container {
					contents,
				}

				val box = Box(10, "items")
				val boxSize = box["size"]
				val boxContents = box["contents"]
			`,
			expectedTypes: map[string]string{
				"Box":         "{new fn (size: number, contents: string) -> Box}",
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

				class Parent(id: number, name: string) extends GrandParent {
					name,
				}

				class Child(id: number, name: string, age: number) extends Parent {
					age,
				}

				val child = Child(1, "Alice", 10)
				val childId = child.id
				val childName = child.name
				val childAge = child.age
			`,
			expectedTypes: map[string]string{
				"GrandParent": "{new fn (id: number) -> GrandParent}",
				"Parent":      "{new fn (id: number, name: string) -> Parent}",
				"Child":       "{new fn (id: number, name: string, age: number) -> Child}",
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

				class ExtendedCounter(value: number, step: number) extends Counter {
					step,
					increment(mut self) {
						self.value = self.value + self.step
						return self
					},
				}

				val mut counter = ExtendedCounter(0, 5)
				val incremented = counter.increment()
			`,
			expectedTypes: map[string]string{
				"Counter":         "{new fn (value: number) -> Counter}",
				"ExtendedCounter": "{new fn (value: number, step: number) -> ExtendedCounter}",
				"counter":         "mut ExtendedCounter",
				"incremented":     "mut ExtendedCounter",
			},
			expectedTypeAliases: map[string]string{
				"Counter":         "{value: number}",
				"ExtendedCounter": "{step: number, increment(mut self) -> mut ExtendedCounter}",
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

				class Cat(name: string, lives: number) extends Animal {
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
				"Animal":  "{new fn (name: string) -> Animal}",
				"Cat":     "{new fn (name: string, lives: number) -> Cat}",
				"cat":     "Cat",
				"sound":   "string",
				"catName": "string",
			},
			expectedTypeAliases: map[string]string{
				"Animal": "{name: string, makeSound(self) -> string}",
				"Cat":    "{lives: number, makeSound(self) -> string}",
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
		// 		"GlobalState": "{new fn () -> GlobalState, set debugMode(mut self, value: boolean) -> undefined}",
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

			c := NewChecker(ctx)
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

// TestNominalClassUnificationTerminates verifies that unifying two different
// nominal classes terminates and produces an error. For nominal TypeRefTypes,
// ExpandType returns nil (the visitor checks Nominal and bails), so the
// last-resort branch in unifyPruned is a no-op and execution falls through to
// CannotUnifyTypesError. A self-referential class (Node with a "next" field of
// its own type) is included to verify that the expansion loop doesn't hang.
func TestNominalClassUnificationTerminates(t *testing.T) {
	source := &ast.Source{
		ID:   0,
		Path: "input.esc",
		Contents: `
			class Node(value: number, next: Node | null) {
				value,
				next,
			}
			class Leaf(value: number) {
				value,
			}
			val n = Node(1, null)
			val l: Leaf = n
		`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	assert.Len(t, parseErrors, 0)

	c := NewChecker(ctx)
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	inferErrors := c.InferModule(inferCtx, module)
	assert.Len(t, inferErrors, 1)
	assert.Equal(t, "Node cannot be assigned to Leaf", inferErrors[0].Message())
}

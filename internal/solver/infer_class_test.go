package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// classValues is the value/type-binding assertion shared by the class tests: it
// infers src, requires no errors, and checks each expected value and type binding.
func classValues(t *testing.T, src string, wantValues, wantTypes map[string]string) {
	t.Helper()
	values, types, errs := inferSource(t, src)
	require.Empty(t, errs)
	for name, want := range wantValues {
		require.Equal(t, want, values[name], "value binding %q", name)
	}
	for name, want := range wantTypes {
		require.Equal(t, want, types[name], "type binding %q", name)
	}
}

// TestInferClassBasic covers a non-generic class end to end: the type binding
// renders under the class name, the value binding is the constructor signature,
// construction yields an instance, and fields and methods resolve through the
// projected body.
func TestInferClassBasic(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name: "SynthesizedConstructor",
			src: `
				class Point {
					x: number,
					y: number,
				}
			`,
			wantValues: map[string]string{"Point": "fn (x: number, y: number) -> Point"},
			wantTypes:  map[string]string{"Point": "Point"},
		},
		{
			name: "ConstructAndReadField",
			src: `
				class Point {
					x: number,
					y: number,
				}
				val p = Point(1, 2)
				val px = p.x
			`,
			wantValues: map[string]string{
				"Point": "fn (x: number, y: number) -> Point",
				"p":     "Point",
				"px":    "number",
			},
		},
		{
			name: "MethodCall",
			src: `
				class Point {
					x: number,
					y: number,
					getX(self) -> number { return self.x },
				}
				val p = Point(1, 2)
				val d = p.getX()
			`,
			wantValues: map[string]string{"p": "Point", "d": "number"},
		},
		{
			name: "ExplicitConstructor",
			src: `
				class Point {
					x: number,
					y: number,
					constructor(mut self, x: number, y: number) {
						self.x = x
						self.y = y
					},
				}
				val p = Point(1, 2)
				val px = p.x
			`,
			wantValues: map[string]string{
				"Point": "fn (x: number, y: number) -> Point",
				"p":     "Point",
				"px":    "number",
			},
		},
		{
			name: "MutSelfMethod",
			src: `
				class Counter {
					count: number,
					constructor(mut self, count: number) { self.count = count },
					increment(mut self) -> number { return self.count },
				}
				val c = Counter(0)
				val n = c.increment()
			`,
			wantValues: map[string]string{"c": "Counter", "n": "number"},
		},
		{
			name: "Getter",
			src: `
				class Box {
					v: number,
					get value(self) -> number { return self.v },
				}
				val b = Box(3)
				val x = b.value
			`,
			wantValues: map[string]string{"b": "Box", "x": "number"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, test.wantTypes)
		})
	}
}

// TestInferClassGeneric covers a generic class: the constructor is generalized over
// its type parameters, construction infers the type arguments, member access
// projects the instance's argument into a field typed by a parameter, and a
// declared constraint is enforced as the parameter's bound.
func TestInferClassGeneric(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			name: "GenericFieldProjection",
			src: `
				class Box<T> { value: T }
				val b = Box(5)
				val v = b.value
			`,
			wantValues: map[string]string{
				"Box": "fn <T0>(value: T0) -> Box<T0>",
				"b":   "Box<5>",
				"v":   "5",
			},
		},
		{
			name: "TwoTypeParams",
			src: `
				class Pair<A, B> { first: A, second: B }
				val p = Pair(1, "x")
				val a = p.first
				val b = p.second
			`,
			wantValues: map[string]string{
				"p": `Pair<1, "x">`,
				"a": "1",
				"b": `"x"`,
			},
		},
		{
			name: "ConstraintSatisfied",
			src: `
				class Box<T: number> { value: T }
				val b = Box(5)
				val v = b.value
			`,
			wantValues: map[string]string{"b": "Box<5>", "v": "5"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, nil)
		})
	}
}

// TestInferClassCrossParamBounds covers B4: a type-parameter bound or default may
// reference any sibling parameter — forward, mutual, or through a generic class. Each case
// resolves its cross-parameter references without reporting `Unsupported: TypeRefTypeAnn`,
// because the shared resolveTypeParams declares every parameter in scope before resolving
// any bound, so a bound reading a later-declared sibling finds it already in scope.
func TestInferClassCrossParamBounds(t *testing.T) {
	srcs := map[string]string{
		"ForwardConstraint": `class C<T: U, U> { value: T }`,
		"MutualConstraint":  `class C<T: U, U: T> { value: T }`,
		"ForwardDefault":    `class C<T = U, U> { value: T }`,
		"MutualDefault":     `class C<T = U, U = T> { value: T }`,
		// The referenced class Cmp is declared before Foo so its instance type is in scope
		// when Foo's bounds resolve. Resolving `Cmp<U>` / `Cmp<T>` combines the two-pass
		// sibling visibility with generic-class-reference resolution. Robust ordering
		// regardless of declaration order rides the B2 recursive-class SCC path
		// (planning/simple_sub/m5-implementation-plan.md).
		"MutualFBound": `
			class Cmp<X> { value: X }
			class Foo<T: Cmp<U>, U: Cmp<T>> { value: T }
		`,
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Empty(t, errs)
		})
	}
}

// TestInferClassForwardBoundEnforced shows a forward reference resolves to a real bound,
// not just a parsed placeholder. `<T: U, U: number>` chains T's bound through the
// later-declared U to number, so a construction whose argument violates it is rejected and
// one that satisfies it checks clean and infers the argument at both positions.
func TestInferClassForwardBoundEnforced(t *testing.T) {
	t.Run("Violated", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T: U, U: number> { value: T }
			val b = Box("hi")
		`)
		require.Len(t, errs, 1)
		require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
	})
	t.Run("Satisfied", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box<T: U, U: number> { value: T }
			val b = Box(5)
		`)
		require.Empty(t, errs)
		require.Equal(t, "Box<5, 5>", values["b"])
	})
}

// A join of the same class at two different type arguments — the value of
// `if c { Box(5) } else { Box("hello") }` — leaves the binding a union of both
// instantiations, Box<5> | Box<"hello">. That union is the shape classCarrier sees as two
// distinct class instances on one variable's lower bounds, so a member access on it cannot
// take the fast projected-member path and rides the nominal-vs-structural rule instead.
func TestInferClassJoinDistinctArgs(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> { value: T }
		val c = true
		val b = if c { Box(5) } else { Box("hello") }
	`)
	require.Empty(t, errs)
	require.Equal(t, `Box<5> | Box<"hello">`, values["b"])
}

// Reading a member off that union — `b.value` for b : Box<5> | Box<"hello"> — distributes
// over the union and joins each arm's `value` field into 5 | "hello".
//
// DISABLED until C1 lands. classCarrier resolves only an unambiguous class, so a variable
// whose lower bounds carry two different instantiations falls to the structural
// field-requirement path. That path has no class-vs-object rule yet, so each Box<…> arm
// fails `cannot constrain ? <: object` and v coalesces to never. C1 adds the
// nominal-vs-structural rule; re-enable then and assert v : 5 | "hello" with no errors.
/*
func TestInferClassJoinMemberAccess(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> { value: T }
		val c = true
		val b = if c { Box(5) } else { Box("hello") }
		val v = b.value
	`)
	require.Empty(t, errs)
	require.Equal(t, `5 | "hello"`, values["v"])
}
*/

// TestInferClassMutualRecursion covers classes that reference each other, or
// themselves, through the SCC path (M5 B2). The dep graph condenses a mutually
// recursive group into one type-key component ordered before every class's value key,
// so pre-binding each class's nominal identity there lets a sibling defined later in the
// group resolve a forward reference. Each constructor and type binding renders the peer's
// class name with no placeholder leak.
func TestInferClassMutualRecursion(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			// A forward reference: A's field is typed by B, declared later.
			name: "MutualFields",
			src: `
				class A { b: B }
				class B { a: A }
			`,
			wantValues: map[string]string{
				"A": "fn (b: B) -> A",
				"B": "fn (a: A) -> B",
			},
			wantTypes: map[string]string{"A": "A", "B": "B"},
		},
		{
			// A self-referential field resolves to the class being declared.
			name:       "SelfReferentialField",
			src:        `class Node { next: Node }`,
			wantValues: map[string]string{"Node": "fn (next: Node) -> Node"},
			wantTypes:  map[string]string{"Node": "Node"},
		},
		{
			// A method on A returns B and a method on B returns A, each inferred from a
			// field read, so the return type is the sibling class.
			name: "MutualMethodReturns",
			src: `
				class A {
					b: B,
					getB(self) { return self.b },
				}
				class B {
					a: A,
					getA(self) { return self.a },
				}
			`,
			wantValues: map[string]string{
				"A": "fn (b: B) -> A",
				"B": "fn (a: A) -> B",
			},
			wantTypes: map[string]string{"A": "A", "B": "B"},
		},
		{
			// A three-class cycle A -> B -> C -> A lands in one type-key component; every
			// forward reference still resolves.
			name: "ThreeClassCycle",
			src: `
				class A { b: B }
				class B { c: C }
				class C { a: A }
			`,
			wantValues: map[string]string{
				"A": "fn (b: B) -> A",
				"B": "fn (c: C) -> B",
				"C": "fn (a: A) -> C",
			},
			wantTypes: map[string]string{"A": "A", "B": "B", "C": "C"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, test.wantTypes)
		})
	}
}

// TestInferClassErrors asserts the full diagnostic for each rejected class shape.
func TestInferClassErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "MissingSelfReceiver",
			src:  `class C { foo() -> number { return 1 } }`,
			want: "Instance member 'foo' must declare a `self` receiver as its first parameter.",
		},
		{
			name: "WriteOnlySetterRead",
			src: `
				class C {
					v: number,
					set value(mut self, x: number) { self.v = x },
				}
				val c = C(0)
				val r = c.value
			`,
			want: "Property 'value' is write-only; it has a setter but no getter or field to read.",
		},
		{
			name: "SetterNoParams",
			src: `
				class C {
					v: number,
					set value(mut self) { },
				}
			`,
			want: "Setter 'value' must declare exactly one value parameter; found 0.",
		},
		{
			name: "SetterTooManyParams",
			src: `
				class C {
					v: number,
					set value(mut self, a: number, b: number) { },
				}
			`,
			want: "Setter 'value' must declare exactly one value parameter; found 2.",
		},
		{
			name: "MultipleConstructors",
			src: `class C {
				constructor(mut self) {},
				constructor(mut self) {},
			}`,
			want: "Multiple constructors per class are not yet supported.",
		},
		{
			name: "FieldInitializerNotAllowed",
			src:  `class C { x: number = 5 }`,
			want: "Field 'x' cannot have a `= expr` initializer; only static fields may use this form. Initialize instance fields in the constructor body.",
		},
		{
			name: "SubclassConstructorRequired",
			src: `
				class A { x: number }
				class B extends A { }
			`,
			want: "Subclasses must declare an explicit `constructor` block; constructor synthesis is not supported for classes with an `extends` clause.",
		},
		{
			name: "ConstraintViolated",
			src: `
				class Box<T: number> { value: T }
				val b = Box("hi")
			`,
			want: `cannot constrain "hi" <: number`,
		},
		{
			name: "StaticFieldInitializerMismatch",
			src:  `class C { static x: number = "hi" }`,
			want: `cannot constrain "hi" <: number`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Len(t, errs, 1)
			require.Equal(t, test.want, errs[0].Message())
		})
	}
}

package solver

import (
	"fmt"
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
			wantValues: map[string]string{"Point": "{new (x: number, y: number) -> Point}"},
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
				"Point": "{new (x: number, y: number) -> Point}",
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
				"Point": "{new (x: number, y: number) -> Point}",
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

// TestInferClassStatic covers the callable class value with static members. The statics sit
// alongside the construct signature in the same object, so construction still resolves through
// the signature, a static field reads its declared type, and a static method reads and calls
// through the same value.
func TestInferClassStatic(t *testing.T) {
	src := `
		class Vec {
			x: number,
			y: number,
			static dim: number = 2,
			static unit(f: number) -> number { return f },
		}
		val v = Vec(1, 2)
		val d = Vec.dim
		val u = Vec.unit
		val r = Vec.unit(3)
	`
	classValues(t, src, map[string]string{
		"Vec": "{new (x: number, y: number) -> Vec, dim: number, unit(f: number) -> number}",
		"v":   "Vec",
		"d":   "number",
		"u":   "fn (f: number) -> number",
		"r":   "number",
	}, map[string]string{"Vec": "Vec"})
}

// A class with no static members binds an object carrying its construct signature alone, the
// same shape a class with statics binds. One shape for every class value is what lets a
// `{new (…) -> R, ...}` target accept any of them through ordinary object subtyping.
// Construction and field access read through the signature either way.
func TestInferClassNoStaticsBindsSignatureOnlyObject(t *testing.T) {
	src := `
		class Point {
			x: number,
			y: number,
		}
		val p = Point(1, 2)
	`
	classValues(t, src, map[string]string{
		"Point": "{new (x: number, y: number) -> Point}",
		"p":     "Point",
	}, nil)
}

// Binding a class value with statics to an un-annotated `var` widens the binding, which
// walks the value object's members. A constructor and static methods carry no literal to
// widen, so the walk passes them through rather than treating every element as a property.
func TestInferClassValueVarWiden(t *testing.T) {
	src := `
		class Vec {
			x: number,
			static dim: number = 2,
		}
		var X = Vec
	`
	classValues(t, src, map[string]string{
		"X": "{new (x: number) -> Vec, dim: number}",
	}, nil)
}

// A read of a static member the class does not declare reports a MissingPropertyError,
// blaming the property, so the class-value member path defers a genuine miss to the same
// diagnostic an ordinary object read produces.
func TestInferClassStaticMissing(t *testing.T) {
	src := `
		class Vec {
			x: number,
			static dim: number = 2,
		}
		val bad = Vec.nope
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: nope", errs[0].Message())
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
				"Box": "<T0> {new (value: T0) -> Box<T0>}",
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

// TestInferClassCrossParamBounds covers B4: a type-parameter bound may reference any sibling
// parameter — forward, mutual, or through a generic class. Each case resolves its
// cross-parameter references without reporting `Unsupported: TypeRefTypeAnn`, because the
// shared resolveTypeParams declares every parameter in scope before resolving any bound, so a
// bound reading a later-declared sibling finds it already in scope. A default is restricted
// where a bound is not; TestTypeParamDefaultForwardRef covers that.
func TestInferClassCrossParamBounds(t *testing.T) {
	srcs := map[string]string{
		"ForwardConstraint": `class C<T: U, U> { value: T }`,
		"MutualConstraint":  `class C<T: U, U: T> { value: T }`,
		"EarlierDefault":    `class C<T, U = T> { value: U }`,
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
// over the union and joins each arm's `value` field into 5 | "hello". Each Box<…> arm
// rides C1's class-vs-object rule: the field read constrains the arm against an inexact
// `{value: _, ...}` requirement, which projects the class body and binds the requirement's
// var to that arm's field type.
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

// TestInferClassNominalSubtype covers C1's nominal constrain rule reached from source
// through a type-parameter bound, the one class-typed constraint target source can
// produce before M7's general TypeRef resolution. `class Box<T: A>` makes a construction
// `Box(arg)` constrain `arg <: A`, so the argument exercises each leg of the rule.
func TestInferClassNominalSubtype(t *testing.T) {
	// base declares A, a subclass B, an unrelated Other, and a bounded Box<T: A>.
	const base = `
		class A { x: number, constructor(mut self) { self.x = 0 } }
		class B extends A { constructor(mut self) {} }
		class Other { y: number, constructor(mut self) { self.y = 0 } }
		class Box<T: A> { value: T }
	`
	t.Run("same class satisfies the bound", func(t *testing.T) {
		values, _, errs := inferSource(t, base+`val b = Box(A())`)
		require.Empty(t, errs)
		require.Equal(t, "Box<A>", values["b"])
	})
	t.Run("subclass satisfies the bound through the graph", func(t *testing.T) {
		values, _, errs := inferSource(t, base+`val b = Box(B())`)
		require.Empty(t, errs)
		require.Equal(t, "Box<B>", values["b"])
	})
	t.Run("unrelated class rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, base+`val b = Box(Other())`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain Other <: A", errs[0].Message())
	})
	t.Run("structural object rejects against a class bound", func(t *testing.T) {
		_, _, errs := inferSource(t, base+`val b = Box({x: 0})`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain object <: class A", errs[0].Message())
	})
}

// TestInferClassIntoObject covers C1's target-dispatched class-vs-object rule reached
// from source through an object type annotation, which resolves today even though a
// bare class name in annotation position does not. A class instance flows into an inexact
// object target by projecting its body, and into an exact object target it is rejected,
// since a non-final class may have subclasses that add members.
func TestInferClassIntoObject(t *testing.T) {
	const point = `
		class Point { x: number, y: number }
		val p = Point(1, 2)
	`
	t.Run("into inexact object succeeds", func(t *testing.T) {
		_, _, errs := inferSource(t, point+`val foo: {x: number, y: number, ...} = p`)
		require.Empty(t, errs)
	})
	t.Run("into exact object rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, point+`val foo: {x: number, y: number} = p`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain class Point <: exact object", errs[0].Message())
	})
}

// TestInferClassFinal covers `final ⇒ exact instance` (exact-types §2.6): a final class
// has no subclasses, so its instance projects an exact body and is checked structurally
// against an exact object target rather than rejected outright the way a non-final
// instance is. The `final` modifier is parsed by the class-declaration grammar.
func TestInferClassFinal(t *testing.T) {
	const finalPoint = `
		final class Point { x: number, y: number }
		val p = Point(1, 2)
	`
	t.Run("into a matching exact object succeeds", func(t *testing.T) {
		_, _, errs := inferSource(t, finalPoint+`val foo: {x: number, y: number} = p`)
		require.Empty(t, errs)
	})
	t.Run("into an exact object missing one of its members rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, finalPoint+`val foo: {x: number} = p`)
		require.Len(t, errs, 1)
		require.Equal(t, "object has extra property: y", errs[0].Message())
	})
	t.Run("into an inexact object still succeeds", func(t *testing.T) {
		_, _, errs := inferSource(t, finalPoint+`val foo: {x: number, y: number, ...} = p`)
		require.Empty(t, errs)
	})
}

// TestInferClassObjectDestructure covers destructuring a class instance with an object
// pattern — `val {x, y} = p`. This is NOT the nominal InstancePat constructor pattern
// `Point({x, y})`, which D1 adds; it is a plain object pattern, which lowers to the
// constraint `p <: {x: _, y: _, ...}` — an inexact object requirement — and so rides
// C1's class-vs-object projection rule. Each named field binds at the projected member
// type, and a field the class lacks reports the object-requirement miss. Before C1 the
// requirement had no class-vs-object rule and failed with `cannot constrain ? <: object`.
func TestInferClassObjectDestructure(t *testing.T) {
	t.Run("binds fields at their member types", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Point { x: number, y: number }
			val p = Point(1, 2)
			val {x, y} = p
		`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["x"])
		require.Equal(t, "number", values["y"])
	})
	t.Run("projects a generic instance's argument into the bound field", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val b = Box(5)
			val {value} = b
		`)
		require.Empty(t, errs)
		require.Equal(t, "5", values["value"])
	})
	t.Run("a field the class lacks is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Point { x: number, y: number }
			val p = Point(1, 2)
			val {z} = p
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "object is missing property: z", errs[0].Message())
	})
}

// TestInferClassNonClassSuper covers the C1 diagnostic for an `extends` or `implements`
// clause naming something that is not a class. A class type parameter resolves to a
// binding that is not a ClassType, so using it as a super reports NonClassSuperError
// rather than silently dropping the edge.
func TestInferClassNonClassSuper(t *testing.T) {
	t.Run("extends a type parameter", func(t *testing.T) {
		_, _, errs := inferSource(t, `class B<T> extends T { constructor(mut self) {} }`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("implements a type parameter", func(t *testing.T) {
		_, _, errs := inferSource(t, `class C<T> implements T {}`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("extends a type parameter applied to arguments", func(t *testing.T) {
		// A type parameter carries no type arguments, so `T<X>` is doubly ill-formed. The
		// extends clause still requires a class, so the non-class binding is reported here
		// rather than dropped silently.
		_, _, errs := inferSource(t, `class B<T, X> extends T<X> { constructor(mut self) {} }`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
}

// TestInferClassExtendFinal covers the rule that a final class cannot be a superclass:
// a final class has no subclasses (exact-types §2.6), so an `extends` clause naming one
// reports CannotExtendFinalClassError. A non-final superclass is unaffected.
func TestInferClassExtendFinal(t *testing.T) {
	t.Run("extending a final class is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			final class A { x: number, constructor(mut self) { self.x = 0 } }
			class B extends A { constructor(mut self) {} }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "Cannot extend `A`; it is a final class and has no subclasses.", errs[0].Message())
	})
	t.Run("extending a non-final class is allowed", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class A { x: number, constructor(mut self) { self.x = 0 } }
			class B extends A { constructor(mut self) {} }
		`)
		require.Empty(t, errs)
	})
}

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
				"A": "{new (b: B) -> A}",
				"B": "{new (a: A) -> B}",
			},
			wantTypes: map[string]string{"A": "A", "B": "B"},
		},
		{
			// A self-referential field resolves to the class being declared.
			name:       "SelfReferentialField",
			src:        `class Node { next: Node }`,
			wantValues: map[string]string{"Node": "{new (next: Node) -> Node}"},
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
				"A": "{new (b: B) -> A}",
				"B": "{new (a: A) -> B}",
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
				"A": "{new (b: B) -> A}",
				"B": "{new (c: C) -> B}",
				"C": "{new (a: A) -> C}",
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

// TestInferClassMethodRecursion covers the two-phase member walk (B3): a method body
// resolves a call to another method of the same class through the pre-declared sibling
// signature, whether self-recursive, mutually recursive, a forward call to a member
// declared later, or a call from a constructor. It also confirms a getter read and a
// `mut self` receiver work alongside the new `self.method()` path.
func TestInferClassMethodRecursion(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			// double() calls getN() twice through self; both resolve to the sibling's
			// number-returning signature.
			name: "SelfMethodCall",
			src: `
				class Counter {
					n: number,
					getN(self) -> number { return self.n },
					double(self) -> number { return self.getN() },
				}
				val c = Counter(5)
				val d = c.double()
			`,
			wantValues: map[string]string{"d": "number"},
		},
		{
			// a() calls b(), which is declared later in the class body; the forward call
			// resolves because every signature exists before any body is walked.
			name: "ForwardSiblingCall",
			src: `
				class C {
					n: number,
					a(self) -> number { return self.b() },
					b(self) -> number { return self.n },
				}
				val c = C(1)
				val r = c.a()
			`,
			wantValues: map[string]string{"r": "number"},
		},
		{
			// A self-recursive method with an annotated return type-checks; the recursive
			// call resolves against the method's own pre-declared signature.
			name: "SelfRecursionAnnotated",
			src: `
				class C {
					n: number,
					loop(self, k: number) -> number { return self.loop(k) },
				}
			`,
			wantValues: map[string]string{"C": "{new (n: number) -> C}"},
		},
		{
			// A mutually recursive pair with annotated returns type-checks; each call
			// resolves against the sibling's annotated signature.
			name: "MutualRecursionAnnotated",
			src: `
				class C {
					n: number,
					ping(self, k: number) -> number { return self.pong(k) },
					pong(self, k: number) -> number { return self.ping(k) },
				}
			`,
			wantValues: map[string]string{"C": "{new (n: number) -> C}"},
		},
		{
			// A method reads a getter of the same class through self; the getter's value
			// resolves through member lookup, not the structural field path.
			name: "MethodReadsGetter",
			src: `
				class Box {
					v: number,
					get value(self) -> number { return self.v },
					twice(self) -> number { return self.value },
				}
				val b = Box(3)
				val x = b.twice()
			`,
			wantValues: map[string]string{"x": "number"},
		},
		{
			// A `mut self` method calls an immutable-self sibling; the call resolves and
			// the field write path still type-checks alongside it.
			name: "MutSelfCallsMethod",
			src: `
				class C {
					n: number,
					helper(self) -> number { return self.n },
					update(mut self) -> number { return self.helper() },
				}
			`,
			wantValues: map[string]string{"C": "{new (n: number) -> C}"},
		},
		{
			// A destructuring method parameter is handled: the stub carries arity only, so
			// the body pass installs the real signature that binds the pattern.
			name: "DestructuredParam",
			src: `
				class C {
					n: number,
					f(self, {a, b}: {a: number, b: number}) -> number { return self.g(a) },
					g(self, x: number) -> number { return x },
				}
				val c = C(1)
				val r = c.f({a: 1, b: 2})
			`,
			wantValues: map[string]string{"r": "number"},
		},
		{
			// A constructor body calls a method of the class; self binds to the full body
			// in the constructor too, so the call resolves.
			name: "ConstructorCallsMethod",
			src: `
				class C {
					n: number,
					constructor(mut self, x: number) {
						self.n = x
					},
					getN(self) -> number { return self.n },
				}
				val c = C(4)
				val g = c.getN()
			`,
			wantValues: map[string]string{"g": "number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, nil)
		})
	}
}

// TestInferClassMutualRecursionRequiresAnnotation pins the annotation gate: a pair of
// mutually recursive methods with no annotated return anywhere in the cycle cannot ground
// its own return types, so every member of the cycle is reported. Annotating either
// member breaks the cycle, which the positive test above covers.
func TestInferClassMutualRecursionRequiresAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			ping(self, k: number) { return self.pong(k) },
			pong(self, k: number) { return self.ping(k) },
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	require.ElementsMatch(t, []string{
		"Mutually recursive method 'ping' must declare a return type; the cycle ping, pong has no annotated return to ground it.",
		"Mutually recursive method 'pong' must declare a return type; the cycle ping, pong has no annotated return to ground it.",
	}, msgs)
}

// TestInferClassMutMethodFromImmutMethod asserts that an immutable-`self` method calling a
// `mut self` method of the same class is rejected: the mutable method needs a mutable
// receiver, but `self` is immutable in the caller. A plain-`self` body holds only a shared
// borrow of its receiver, so it has no mutable access to lend `bump`, and the receiver
// check renders the mismatch against the class name on both sides.
func TestInferClassMutMethodFromImmutMethod(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			bump(mut self) -> number { return self.n },
			peek(self) -> number { return self.bump() },
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain immutable C <: mutable C", errs[0].Message())
}

// TestInferClassMutMethodFromMutMethod is the passing companion: a `mut self` method may
// call another `mut self` method, since its own mutable borrow of the receiver satisfies
// the callee's `mut self`. A plain-`self` method called from a `mut self` body also
// type-checks, downgrading the mutable receiver to a shared read.
func TestInferClassMutMethodFromMutMethod(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			bump(mut self) -> number { return self.n },
			read(self) -> number { return self.n },
			run(mut self) -> number { return self.bump() },
			also(mut self) -> number { return self.read() },
		}
	`)
	require.Empty(t, errs)
}

// TestInferClassMutGetterFromImmutMethod pins the receiver check for a getter member: a
// `mut self` getter read from a plain-`self` method is rejected for the same reason a
// `mut self` method call is — the caller holds only a shared borrow. The getter carries its
// receiver on GetterElem.SelfParam rather than a FuncType, so this exercises a distinct
// member kind from the method case.
func TestInferClassMutGetterFromImmutMethod(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			v: number,
			get doubled(mut self) -> number { return self.v },
			peek(self) -> number { return self.doubled },
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain immutable C <: mutable C", errs[0].Message())
}

// TestInferClassGenericMethodReturnsTypeParam checks that a method whose return flows from a
// class type parameter projects to the instance's argument — both through a `self.method()`
// call and external access. `read` returns the field typed `T`, so `b.read()` for `b : Box<5>`
// projects `T` to `5`, and `alias` calling `self.read()` reaches the same value.
//
// B8 (planning/simple_sub/m5-implementation-plan.md §"Phase B") lands this. A generic class
// body is coalesced at decl time in a mode that keeps the class's own type-parameter vars
// symbolic, so a method whose return is an unannotated field read reads as `T` — recovered
// through the kept-flow map, since the read's var-var edge is stored on `T`'s upper bounds —
// and class-argument projection then rewrites `T`. The fix lives in the shared member-access
// path, so both classBodyMember (self access) and projectedMember (external access) benefit.
func TestInferClassGenericMethodReturnsTypeParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			read(self) { return self.v },
			alias(self) { return self.read() },
		}
		val b = Box(5)
		val x = b.read()
		val y = b.alias()
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.Equal(t, "5", values["y"])
}

// TestInferClassGenericGetterReturnsTypeParam checks the getter analogue of the method case:
// a getter whose return flows from a class type parameter projects to the instance's argument.
// `get first(self) { return self.v }` on `class Box<T>` reads `T`, so `b.first` for `b : Box<5>`
// projects to `5`. The kept-flow coalesce keeps `T` symbolic through the getter's return var
// the same way it does for a method return.
func TestInferClassGenericGetterReturnsTypeParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			get first(self) { return self.v },
		}
		val b = Box("hi")
		val x = b.first
	`)
	require.Empty(t, errs)
	require.Equal(t, `"hi"`, values["x"])
}

// TestInferClassGenericMixedMembers checks that keeping the class's type-parameter var symbolic
// does not disturb a concrete member. `label` returns a plain `string` regardless of the type
// argument, while `read` returns the parameter `T`, so `b.label()` is `string` and `b.read()`
// is the argument. This confirms the generic-body coalesce still collapses a member with a
// concrete value the way a non-generic class's freeze does, coalescing only the type-parameter
// flow symbolically.
func TestInferClassGenericMixedMembers(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			read(self) { return self.v },
			label(self) -> string { return "box" },
		}
		val b = Box(5)
		val r = b.read()
		val l = b.label()
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["r"])
	require.Equal(t, "string", values["l"])
}

// TestInferClassInheritedMemberAccess checks that a member declared on a superclass is
// reachable through a subclass instance: reading an inherited field and calling an
// inherited method both resolve to the member's declared type. projectedMember walks the
// `extends` chain, so `class Dog extends Animal` lets `d.name` project to `string` and
// `d.speak()` return `string`.
func TestInferClassInheritedMemberAccess(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Animal {
			name: string,
			speak(self) -> string { return "..." },
		}
		class Dog extends Animal {
			constructor(mut self) {}
		}
		val d = Dog()
		val n = d.name
		val s = d.speak()
	`)
	require.Empty(t, errs)
	require.Equal(t, "string", values["n"])
	require.Equal(t, "string", values["s"])
}

// TestInferClassInheritedMemberAccessMultiLevel checks that the extends walk reaches a
// member declared two levels up. `class Leaf extends Mid extends Base` reads `base`,
// declared on Base, through a Leaf instance. The binding names avoid the member names so a
// separate dep-graph bug — a class field whose name matches a top-level `val` gets a
// spurious dependency on it — does not scramble the inference order.
func TestInferClassInheritedMemberAccessMultiLevel(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Base {
			base: number,
		}
		class Mid extends Base {
			constructor(mut self) {}
		}
		class Leaf extends Mid {
			constructor(mut self) {}
		}
		val leaf = Leaf()
		val got = leaf.base
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["got"])
}

// TestInferClassFieldNameMatchingTopLevelVal checks that a class field whose name matches
// a top-level `val` binding does not scramble the inference order. The dep graph must not
// record a dependency from the class to the colliding `val x`, so `A` is inferred before
// the vals that construct and read from it, and `a.x` projects to `number`.
func TestInferClassFieldNameMatchingTopLevelVal(t *testing.T) {
	values, types, errs := inferSource(t, `
		class A {
			x: number,
		}
		val a = A(1)
		val x = a.x
	`)
	require.Empty(t, errs)
	require.Equal(t, "A", values["a"])
	require.Equal(t, "number", values["x"])
	require.Equal(t, "A", types["A"])
}

// TestInferClassInheritedMemberAccessCollidingVal checks that inherited member access works
// when the inherited field name matches a top-level `val`. Reading `c.x` through a two-level
// `class C extends B extends A` hierarchy resolves to `number` even though `val x` shares the
// field's name. Without the dep-graph fix, the collision pulled `type:A`, `type:B`, `value:C`,
// `value:c`, and `value:x` into one strongly-connected component, so `C` was inferred before
// `B`, its `extends` edge was dropped, and `c.x` reported a missing property.
func TestInferClassInheritedMemberAccessCollidingVal(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A {
			x: number,
		}
		class B extends A {
			constructor(mut self) {}
		}
		class C extends B {
			constructor(mut self) {}
		}
		val c = C()
		val x = c.x
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["x"])
}

// TestInferClassGenericSubGenericSuper covers a generic subclass that extends a generic
// superclass at its own type parameter. Two things must line up. The `extends
// Animal<D>` edge threads Dog's `D` into the super arguments, so a Dog instance projects
// the inherited field `food: A` at Dog's argument. And Dog's constructor parameter `tag: D`
// resolves the class parameter `D` through the general resolveTypeAnn path, so Dog infers
// generic in `D` and `Dog("bone")` recovers `Dog<"bone">` rather than a non-generic
// `Dog<never>`. The inherited field read projects through the edge to the same argument.
//
// The constructor writes Dog's own field, not the inherited one: a subclass constructor
// that initializes an inherited field needs `super(...)` forwarding, which is deferred.
func TestInferClassGenericSubGenericSuper(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Animal<A> {
			food: A,
		}
		class Dog<D> extends Animal<D> {
			tag: D,
			constructor(mut self, tag: D) { self.tag = tag }
		}
		val d = Dog("bone")
		val f = d.food
		val g = d.tag
	`)
	require.Empty(t, errs)
	require.Equal(t, "<T0> {new (tag: T0) -> Dog<T0>}", values["Dog"])
	require.Equal(t, `Dog<"bone">`, values["d"])
	require.Equal(t, `"bone"`, values["f"])
	require.Equal(t, `"bone"`, values["g"])
}

// TestInferClassNonGenericSubGenericSuper covers a non-generic subclass extending a generic
// superclass at a concrete argument. `class Dog extends Animal<string>` threads the literal
// `string` into the edge, so the inherited field `food: A` projects to `string` through a
// Dog instance.
func TestInferClassNonGenericSubGenericSuper(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Animal<A> {
			food: A,
		}
		class Dog extends Animal<string> {
			constructor(mut self) {}
		}
		val d = Dog()
		val f = d.food
	`)
	require.Empty(t, errs)
	require.Equal(t, "Dog", values["d"])
	require.Equal(t, "string", values["f"])
}

// TestInferClassGenericMemberParam covers a generic class whose constructor and method both
// take a parameter typed by the class type parameter. Resolving `v: T` and `next: T`
// routes through the general resolveTypeAnn path, which now consults the class type scope,
// so neither reports `Unsupported: TypeRefTypeAnn` and the class infers generic in `T`.
func TestInferClassGenericMemberParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			constructor(mut self, v: T) { self.v = v },
			replace(mut self, next: T) { self.v = next },
		}
		val b = Box(5)
	`)
	require.Empty(t, errs)
	require.Equal(t, "<T0> {new (v: T0) -> Box<T0>}", values["Box"])
	require.Equal(t, "Box<5>", values["b"])
}

// TestInferClassNameInAnnotation checks that a class name resolves in a type annotation
// outside a class body — a top-level `val` type and a function parameter. resolveTypeAnn
// consults the enclosing scope wherever it runs, so a bare `Point` or a generic instance
// `Box<number>` resolves through the same path a class body uses, rather than reporting
// `Unsupported: TypeRefTypeAnn`.
func TestInferClassNameInAnnotation(t *testing.T) {
	t.Run("bare class name in a val annotation", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Point { x: number, y: number }
			val p: Point = Point(1, 2)
		`)
		require.Empty(t, errs)
		require.Equal(t, "Point", values["p"])
	})
	t.Run("generic class instance in a val annotation", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val b: Box<number> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("class name in a function parameter annotation", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Point { x: number, y: number }
			fn getX(p: Point) -> number { return p.x }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: Point) -> number", values["getX"])
	})
}

// TestInferClassVariance covers C2 end to end: a class parameter's variance is inferred
// from its body and drives the nominal constrain rule. A covariant Box widens through an
// annotation, so `Box<5> <: Box<number | string>` — a check C1's conservative invariant
// rejected — now succeeds; a class using its parameter in a method value parameter is
// contravariant, so the same widening is rejected.
func TestInferClassVariance(t *testing.T) {
	t.Run("covariant field parameter widens", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val b: Box<number | string> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("covariant instance widens into a wider instance", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val narrow: Box<number> = Box(5)
			val wide: Box<number | string> = narrow
		`)
		require.Empty(t, errs)
	})
	t.Run("contravariant method parameter rejects a widening", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			val narrow: Consumer<number> = Consumer()
			val wide: Consumer<number | string> = narrow
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
}

// TestInferClassCovariance demonstrates C2 covariance in the shapes that produce an
// output-position occurrence — a field, a method return, a getter, and each parameter of a
// multi-parameter class — so a narrower instance flows into a wider one with no error. It
// also shows covariance reached through a function argument and that a field read off the
// widened instance yields the wider type. Every case is expected clean: covariance never
// rejects a widening.
func TestInferClassCovariance(t *testing.T) {
	t.Run("field occurrence widens", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val wide: Box<number | string> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("method return occurrence widens", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> {
				value: T,
				read(self) -> T { return self.value },
			}
			val wide: Box<number | string> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("getter occurrence widens", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> {
				value: T,
				get item(self) -> T { return self.value },
			}
			val wide: Box<number | string> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("each parameter of a multi-parameter class is covariant", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Pair<A, B> { first: A, second: B }
			val p: Pair<number | string, number | boolean> = Pair(5, true)
		`)
		require.Empty(t, errs)
		require.Equal(t, "Pair<number | string, number | boolean>", values["p"])
	})
	t.Run("widening flows through a function argument", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box<T> { value: T }
			fn widen(b: Box<number | string>) -> Box<number | string> { return b }
			val r = widen(Box(5))
		`)
		require.Empty(t, errs)
		require.Equal(t, "Box<number | string>", values["r"])
	})
	t.Run("a field read off the widened instance yields the wider type", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box<T> { value: T }
			val wide: Box<number | string> = Box(5)
			val v = wide.value
		`)
		require.Empty(t, errs)
		require.Equal(t, "number | string", values["v"])
	})
}

// TestInferClassContravariance demonstrates C2 contravariance in the shapes that produce
// an input-position occurrence — a method value parameter and a setter — so a wider
// instance flows into a narrower one with no error. This is the reverse of covariance: a
// Consumer<number | string> is a Consumer<number>, because a consumer that accepts the
// wider type accepts the narrower. It also shows the narrowing reached through a function
// argument and across each parameter of a multi-parameter class. Every case is expected
// clean: contravariance accepts a narrowing.
func TestInferClassContravariance(t *testing.T) {
	t.Run("method parameter occurrence narrows", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			val wide: Consumer<number | string> = Consumer()
			val narrow: Consumer<number> = wide
		`)
		require.Empty(t, errs)
		require.Equal(t, "Consumer<number>", values["narrow"])
	})
	t.Run("setter occurrence narrows", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Sink<T> {
				set item(mut self, x: T) { },
			}
			val wide: Sink<number | string> = Sink()
			val narrow: Sink<number> = wide
		`)
		require.Empty(t, errs)
		require.Equal(t, "Sink<number>", values["narrow"])
	})
	t.Run("each parameter of a multi-parameter class is contravariant", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Sink2<A, B> {
				a(self, x: A) { },
				b(self, y: B) { },
			}
			val wide: Sink2<number | string, number | boolean> = Sink2()
			val narrow: Sink2<number, boolean> = wide
		`)
		require.Empty(t, errs)
		require.Equal(t, "Sink2<number, boolean>", values["narrow"])
	})
	t.Run("narrowing flows through a function argument", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			fn feed(c: Consumer<number>) { }
			val wide: Consumer<number | string> = Consumer()
			val r = feed(wide)
		`)
		require.Empty(t, errs)
	})
}

// TestInferClassMutVariance covers the mutable-view variance vector end to end
// (escalier-lang/escalier#870). A `mut` reference to a class instance tightens only the
// parameters a write through that reference can reach, rather than pinning every type
// argument.
//
// A non-`readonly` field is writable through a `mut` reference and readable through any
// reference, so a parameter reaching one is covariant immutably and invariant mutably. A
// method value parameter and a method return are input and output positions whatever the
// reference's mutability, so those parameters keep their variance under `mut`.
func TestInferClassMutVariance(t *testing.T) {
	t.Run("mut field parameter rejects a widening", func(t *testing.T) {
		// `wide` may run `b.value = "s"`, so accepting this would let a `string` land in a
		// field the instance declares as `number`. A writable field is an input position
		// under `mut`, which is what rejects it.
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			fn wide(b: mut Box<number | string>) { }
			fn narrow(b: mut Box<number>) { wide(b) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("mut field parameter rejects a narrowing", func(t *testing.T) {
		// The other direction fails too, which is what invariance means. Here the danger
		// is on the read side: `narrow` may bind `b.value` as a `number` while the
		// instance holds a `string`.
		_, _, errs := inferSource(t, `
			class Box<T> { value: T }
			fn narrow(b: mut Box<number>) { }
			fn wide(b: mut Box<number | string>) { narrow(b) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("mut method parameter accepts a narrowing", func(t *testing.T) {
		// `T` reaches no storage, only `accept`'s parameter, so a `mut` reference buys no
		// capability an immutable one lacks. Everything `narrow` can do is call
		// `accept(<number>)`, and the instance behind it accepts a `number | string`.
		// Rejecting this was the imprecision escalier-lang/escalier#870 reported.
		_, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			fn narrow(c: mut Consumer<number>) { }
			fn wide(c: mut Consumer<number | string>) { narrow(c) }
		`)
		require.Empty(t, errs)
	})
	t.Run("mut method parameter rejects a widening", func(t *testing.T) {
		// Contravariance is a direction, not a licence. `wide` may call
		// `c.accept("s")`, which the instance's `number`-only `accept` cannot handle, so
		// the reverse of the case above stays rejected under `mut` just as it is
		// immutably.
		_, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			fn wide(c: mut Consumer<number | string>) { }
			fn narrow(c: mut Consumer<number>) { wide(c) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("mut readonly field accepts a widening", func(t *testing.T) {
		// `readonly` rejects `f.value = …` through any reference, so the field is an
		// output position even under `mut` and T stays covariant. `wide` can only read,
		// and a read yields the `number` the instance holds.
		_, _, errs := inferSource(t, `
			class Frozen<T> { readonly value: T }
			fn wide(f: mut Frozen<number | string>) { }
			fn narrow(f: mut Frozen<number>) { wide(f) }
		`)
		require.Empty(t, errs)
	})
	t.Run("mut readonly field rejects a narrowing", func(t *testing.T) {
		// Covariance under `mut` is still covariance. Reading `f.value` as a `number` off
		// an instance that may hold a `string` is the unsound direction, so it is
		// rejected here as it would be immutably.
		_, _, errs := inferSource(t, `
			class Frozen<T> { readonly value: T }
			fn narrow(f: mut Frozen<number>) { }
			fn wide(f: mut Frozen<number | string>) { narrow(f) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("mut method return accepts a widening", func(t *testing.T) {
		// Both of Reader's occurrences of T are output positions, and neither becomes
		// writable under `mut`. So `wide` can only obtain a T, never supply one, and every
		// T it obtains is the `number` the instance stores.
		_, _, errs := inferSource(t, `
			class Reader<T> {
				readonly value: T,
				read(self) -> T { return self.value },
			}
			fn wide(r: mut Reader<number | string>) { }
			fn narrow(r: mut Reader<number>) { wide(r) }
		`)
		require.Empty(t, errs)
	})
	t.Run("a writable field pins a parameter a method also returns", func(t *testing.T) {
		// T is covariant immutably — both the field read and the return are output
		// positions — but the field write makes it invariant mutably, so the widening the
		// immutable check below accepts is rejected under `mut`.
		_, _, errs := inferSource(t, `
			class Box<T> {
				value: T,
				read(self) -> T { return self.value },
			}
			fn wide(b: mut Box<number | string>) { }
			fn narrow(b: mut Box<number>) { wide(b) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a mut subclass reference does not reach its superclass", func(t *testing.T) {
		// A cross-name pair stays pinned by the RefType arm's residual write-back, so this
		// widening is rejected even though the immutable one below is accepted. The
		// nominal walk decides `Dog <: Animal` on the declared extends edge alone, and
		// nothing checks that Dog's redeclared `pos` keeps the type Animal declares. Were
		// the widening accepted, `reset` would store a `{x: number}` into a field Dog
		// declares as `{x: number, y: number}`. Re-examine this once
		// escalier-lang/escalier#985 checks an inherited member's type.
		_, _, errs := inferSource(t, `
			class Animal {
				pos: {x: number},
				constructor(mut self, pos: {x: number}) { self.pos = pos },
			}
			class Dog extends Animal {
				pos: {x: number, y: number},
				constructor(mut self, pos: {x: number, y: number}) { self.pos = pos },
			}
			fn reset(a: mut Animal) { a.pos = {x: 0} }
			fn resetDog(d: mut Dog) { reset(d) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain Animal <: Dog", errs[0].Message())
	})
	t.Run("an immutable subclass reference reaches its superclass", func(t *testing.T) {
		// The immutable widening is the one the extends edge does license. `describe` can
		// only read through `a`, and every member Animal declares is one Dog carries, so
		// no read can miss. The redeclared-member gap that blocks the `mut` case above
		// cannot bite here, since reaching it needs a write.
		_, _, errs := inferSource(t, `
			class Animal {
				name: string,
				constructor(mut self, name: string) { self.name = name },
			}
			class Dog extends Animal {
				breed: string,
				constructor(mut self, name: string, breed: string) { self.breed = breed },
			}
			fn describe(a: Animal) -> string { return a.name }
			fn describeDog(d: Dog) -> string { return describe(d) }
		`)
		require.Empty(t, errs)
	})
	t.Run("a readonly field holding a mut borrow is invariant in both views", func(t *testing.T) {
		// `readonly` rejects `h.inner = …`, but the borrow the field holds still admits
		// `h.inner.value = …`, so T is reachable for writing through either kind of
		// reference to a Holder. Both widenings must therefore be rejected. Were the
		// first accepted, `poke` would store a `number` into a `Box<5>.value`.
		src := `
			class Box<T> { value: T }
			class Holder<T> { readonly inner: mut Box<T> }
			fn poke(h: %s Holder<number>) { h.inner.value = 42 }
			fn narrow(h: %s Holder<5>) { poke(h) }
		`
		for _, mutability := range []string{"mut", ""} {
			_, _, errs := inferSource(t, fmt.Sprintf(src, mutability, mutability))
			require.Len(t, errs, 1, "mutability %q", mutability)
			require.Equal(t, "cannot constrain number <: 5", errs[0].Message())
		}
	})
	t.Run("a class spelled through an alias behaves as the class it names", func(t *testing.T) {
		// An alias is transparent, so `mut CNum` must permit exactly what `mut
		// Consumer<number>` permits — no more, which would be unsound, and no less, which
		// would reject a sound program. The residual write-back dispatches on the
		// pointee's kind, so it peels the alias first. Without that, `mut CNum` would miss
		// the class arm and take the whole reverse constraint, rejecting the narrowing the
		// bare spelling accepts a few cases above.
		_, _, errs := inferSource(t, `
			class Consumer<T> {
				accept(self, x: T) { },
			}
			type CNum = Consumer<number>
			fn narrow(c: mut CNum) { }
			fn wide(c: mut Consumer<number | string>) { narrow(c) }
		`)
		require.Empty(t, errs)
	})
	t.Run("the same widening is accepted through an immutable reference", func(t *testing.T) {
		// The immutable counterpart of the writable-field case above, and the pair is the
		// point: dropping `mut` from both signatures leaves `wide` unable to write, so the
		// field is an output position again and the widening becomes sound. This is the
		// precision the mutable-view vector buys — the two views genuinely differ rather
		// than one being conservatively pinned to the other.
		_, _, errs := inferSource(t, `
			class Box<T> {
				value: T,
				read(self) -> T { return self.value },
			}
			fn wide(b: Box<number | string>) { }
			fn narrow(b: Box<number>) { wide(b) }
		`)
		require.Empty(t, errs)
	})
}

// TestInferClassVarianceModifiers covers the declaration-site `in`/`out`/`in out`
// modifiers (C2): a modifier that matches the inferred variance checks silently, and one
// that disagrees reports VarianceMismatchError. The measured variance still governs
// subtyping — the modifier is checked, not trusted.
func TestInferClassVarianceModifiers(t *testing.T) {
	t.Run("matching out modifier on a covariant parameter checks", func(t *testing.T) {
		_, _, errs := inferSource(t, `class Box<out T> { value: T }`)
		require.Empty(t, errs)
	})
	t.Run("in modifier on a covariant parameter is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `class Box<in T> { value: T }`)
		require.Len(t, errs, 1)
		require.Equal(t, "type parameter `T` is declared contravariant but is actually covariant", errs[0].Message())
	})
	t.Run("in out modifier on a covariant parameter is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `class Box<in out T> { value: T }`)
		require.Len(t, errs, 1)
		require.Equal(t, "type parameter `T` is declared invariant but is actually covariant", errs[0].Message())
	})
	t.Run("matching in modifier on a contravariant parameter checks", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Consumer<in T> {
				accept(self, x: T) { },
			}
		`)
		require.Empty(t, errs)
	})
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
			name: "SetterPlainSelfReceiver",
			src: `
				class C {
					v: number,
					set value(self, x: number) { },
				}
			`,
			want: "Setter 'value' must declare a `mut self` receiver; writing through it mutates the instance.",
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
		{
			// An undefined type argument in the `extends` clause reports the general
			// resolveTypeAnn recovery for an unresolved reference. The edge still recovers
			// its arity to a fresh var, so no cascade follows. M7's scope-driven TypeRef
			// resolution replaces this with a proper undefined-type diagnostic.
			name: "SuperTypeArgUndefined",
			src: `
				class Animal<A> { food: A }
				class Dog extends Animal<Bogus> {
					constructor(mut self) {}
				}
			`,
			want: "Unsupported: TypeRefTypeAnn",
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

// TestConstructorInitClean covers constructors whose definite-assignment analysis is
// satisfied: every required field is assigned on every path, an optional field may be
// left unassigned, and both branches of an `if` that each assign a field count.
func TestConstructorInitClean(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "AllFieldsAssigned",
			src: `
				class Point {
					x: number,
					y: number,
					constructor(mut self, x: number, y: number) {
						self.x = x
						self.y = y
					},
				}
			`,
		},
		{
			name: "OptionalFieldUnassigned",
			src: `
				class C {
					x: number,
					y?: number,
					constructor(mut self, x: number) {
						self.x = x
					},
				}
			`,
		},
		{
			name: "BothBranchesAssign",
			src: `
				class C {
					x: number,
					constructor(mut self, cond: boolean) {
						if cond {
							self.x = 1
						} else {
							self.x = 2
						}
					},
				}
			`,
		},
		{
			name: "AssignThenRead",
			src: `
				class C {
					x: number,
					y: number,
					constructor(mut self, x: number) {
						self.x = x
						self.y = self.x
					},
				}
			`,
		},
		{
			// A method may be called once every required field is assigned.
			name: "MethodCallAfterInit",
			src: `
				class C {
					x: number,
					log(self) -> number { return self.x },
					constructor(mut self, x: number) {
						self.x = x
						val r = self.log()
					},
				}
			`,
		},
		{
			// Referencing a method member of self before init reads no field, so it is not
			// a read-before-init.
			name: "MethodReferenceBeforeInit",
			src: `
				class C {
					x: number,
					log(self) -> number { return self.x },
					constructor(mut self, x: number) {
						val f = self.log
						self.x = x
					},
				}
			`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
		})
	}
}

// TestConstructorInitErrors covers the definite-assignment diagnostics: a required
// field left unassigned on some path is a FieldNotInitializedError, a `self.f` read
// before its assignment is a ReadBeforeInitError, and a `self.method(...)` call before
// every required field is assigned is a MethodCallBeforeInitError.
func TestConstructorInitErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "FieldNeverAssigned",
			src: `
				class Point {
					x: number,
					y: number,
					constructor(mut self, x: number) {
						self.x = x
					},
				}
			`,
			want: "Field 'y' is not initialized on every path through the constructor.",
		},
		{
			name: "MultipleFieldsUnassigned",
			src: `
				class Point {
					x: number,
					y: number,
					constructor(mut self) { },
				}
			`,
			want: "Fields 'x', 'y' are not initialized on every path through the constructor.",
		},
		{
			name: "AssignedOnOnlyOnePath",
			src: `
				class C {
					x: number,
					constructor(mut self, cond: boolean) {
						if cond {
							self.x = 1
						}
					},
				}
			`,
			want: "Field 'x' is not initialized on every path through the constructor.",
		},
		{
			name: "ReadBeforeInit",
			src: `
				class C {
					x: number,
					y: number,
					constructor(mut self, x: number) {
						self.y = self.x
						self.x = x
					},
				}
			`,
			want: "Field 'self.x' is read before it has been initialized.",
		},
		{
			name: "MethodCallBeforeInit",
			src: `
				class C {
					x: number,
					log(self) -> number { return self.x },
					constructor(mut self, x: number) {
						val r = self.log()
						self.x = x
					},
				}
			`,
			want: "Cannot call a method on `self` before all required fields are initialized; missing 'x'.",
		},
		{
			name: "MethodCallMissingMultiple",
			src: `
				class C {
					x: number,
					y: number,
					log(self) -> number { return self.x },
					constructor(mut self) {
						val r = self.log()
						self.x = 1
						self.y = 2
					},
				}
			`,
			want: "Cannot call a method on `self` before all required fields are initialized; missing 'x', 'y'.",
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

// TestInferClassNamespaceQualified covers the namespace-qualified class registry.
// A class is keyed in the nominal registry, in its ClassType handle, and in its scope
// type binding by its dep_graph-qualified name, e.g. "geometry.Point". So two sibling
// `class Point` declarations under different directory-derived namespaces stay distinct:
// a bare "Point" key would collide and merge their bodies, while the qualified keys keep
// each class's body its own. A class-body type reference resolves against its own
// namespace first, so a bare sibling reference still resolves and a qualified
// cross-namespace reference resolves too. Every class still renders under its bare name,
// since the printer strips the namespace prefix.
//
// The instance-construction and member-access forms — `val p = Point(1, 2); p.x` — are
// exercised at the root namespace by TestInferClassBasic. A namespaced instance is not
// constructed here because a bare value reference does not yet resolve across a
// namespace boundary in the solver; that value-side resolution rides the broader
// namespace-in-scope work. The per-class constructor signature, synthesized from the
// class's own body, stands in as the observable proof that each class resolves its own
// members with no cross-namespace collision.
func TestInferClassNamespaceQualified(t *testing.T) {
	tests := []struct {
		name       string
		srcs       map[string]string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name: "SiblingSameNameDistinctNamespaces",
			srcs: map[string]string{
				"geometry/point.esc": `
					class Point {
						x: number,
					}
				`,
				"shape/point.esc": `
					class Point {
						label: string,
					}
				`,
			},
			// Each constructor is synthesized from its OWN class body, so the two bodies
			// never merged on a shared "Point" registry entry. Both render bare.
			wantValues: map[string]string{
				"geometry.Point": "{new (x: number) -> Point}",
				"shape.Point":    "{new (label: string) -> Point}",
			},
			wantTypes: map[string]string{
				"geometry.Point": "Point",
				"shape.Point":    "Point",
			},
		},
		{
			name: "IntraNamespaceSiblingReference",
			srcs: map[string]string{
				"geometry/shapes.esc": `
					class Line {
						start: Point,
					}
					class Point {
						x: number,
					}
				`,
			},
			// The bare `Point` in Line's field resolves to the sibling geometry.Point
			// through the class's own namespace, not to any other namespace's Point.
			wantValues: map[string]string{
				"geometry.Line":  "{new (start: Point) -> Line}",
				"geometry.Point": "{new (x: number) -> Point}",
			},
			wantTypes: map[string]string{
				"geometry.Line":  "Line",
				"geometry.Point": "Point",
			},
		},
		{
			name: "CrossNamespaceReference",
			srcs: map[string]string{
				"geometry/point.esc": `
					class Point {
						x: number,
					}
				`,
				"shape/line.esc": `
					class Line {
						start: geometry.Point,
					}
				`,
			},
			// The qualified `geometry.Point` reference from the shape namespace resolves
			// to the geometry class's registered type binding.
			wantValues: map[string]string{
				"shape.Line":     "{new (start: Point) -> Line}",
				"geometry.Point": "{new (x: number) -> Point}",
			},
			wantTypes: map[string]string{
				"shape.Line":     "Line",
				"geometry.Point": "Point",
			},
		},
		{
			name: "RootNamespaceUnchanged",
			srcs: map[string]string{
				"input.esc": `
					class Point {
						x: number,
					}
				`,
			},
			// A root-namespace class keeps its bare name as the qualified key, so nothing
			// changes for the common single-namespace case.
			wantValues: map[string]string{"Point": "{new (x: number) -> Point}"},
			wantTypes:  map[string]string{"Point": "Point"},
		},
		{
			name: "MutuallyRecursiveAcrossNamespaces",
			srcs: map[string]string{
				"foo/a.esc": `
					class A {
						peer: bar.B,
					}
				`,
				"bar/b.esc": `
					class B {
						peer: foo.A,
					}
				`,
			},
			// A cycle whose two classes live in different namespaces. The dep graph
			// condenses it into one type-key component, and the SCC pre-pass registers
			// both shells under their qualified names before either body is walked, so
			// each cross-namespace forward reference — `foo.A` naming `bar.B` and back —
			// resolves through the shared handle with no placeholder leak. Each field
			// renders the peer under its bare name.
			wantValues: map[string]string{
				"foo.A": "{new (peer: B) -> A}",
				"bar.B": "{new (peer: A) -> B}",
			},
			wantTypes: map[string]string{
				"foo.A": "A",
				"bar.B": "B",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, types, errs := inferSources(t, test.srcs)
			require.Empty(t, errs)
			// Compare the whole maps, not just the expected keys, so a stale bare `Point`
			// binding left over from a namespace collision would surface as an unexpected
			// extra entry rather than passing unnoticed.
			require.Equal(t, test.wantValues, values)
			require.Equal(t, test.wantTypes, types)
		})
	}
}

// TestInferMethodOverloadResolvesByArg pins method overload resolution (E1): a method
// declared with two signatures resolves the arm whose parameter matches the argument, the
// method analogue of a direct overloaded-function call. c.f(1) picks the number arm and
// c.f("hi") the string arm, so the two reads take the two arms' distinct return types.
func TestInferMethodOverloadResolvesByArg(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			f(self, x: number) -> number { return x },
			f(self, x: string) -> string { return x },
		}
		val c = C(0)
		val a = c.f(1)
		val b = c.f("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["a"])
	require.Equal(t, "string", values["b"])
}

// TestInferMethodOverloadObjectArgFieldSubsumption is the #723 case through a method: an
// overloaded method with object parameters ranks a wider-field argument to the wider arm.
// c.g({x, y}) picks the {x, y} arm over the earlier {x} arm, so method resolution ranks
// most-specific-first rather than by declaration order.
func TestInferMethodOverloadObjectArgFieldSubsumption(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			g(self, p: {x: number}) -> number { return p.x },
			g(self, p: {x: number, y: number}) -> string { return "wide" },
		}
		val c = C(0)
		val r = c.g({x: 1, y: 2})
	`)
	require.Empty(t, errs)
	require.Equal(t, "string", values["r"],
		"the wider-field arm outranks the earlier narrow arm for a superset argument")
}

// TestInferMethodOverloadNoMatch reports a NoMatchingOverloadError when no arm accepts the
// argument, listing the arms tried. The candidate signatures render with their `self`
// receiver stripped, since a call site supplies only the value arguments.
func TestInferMethodOverloadNoMatch(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			f(self, x: number) -> number { return x },
			f(self, x: string) -> string { return x },
		}
		val c = C(0)
		val r = c.f(true)
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"No matching overload for this call\n  fn (x: number) -> number\n  fn (x: string) -> string",
		errs[0].Message())
}

// TestInferMethodOverloadDeferredFallsBackToFirstMatch confirms the declaration-order
// fallback for an unconstrained argument reaches method overloads too: inside a method
// whose parameter x is unannotated, a self-call self.f(x) resolves to the first arm and
// pins x, since an unconstrained argument cannot rank the arms. run's parameter x is
// therefore number, the first arm's type.
func TestInferMethodOverloadDeferredFallsBackToFirstMatch(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			f(self, x: number) -> number { return x },
			f(self, x: string) -> string { return x },
			run(self, x) -> number { return self.f(x) },
		}
		val c = C(0)
		val r = c.run(1)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"],
		"an unconstrained argument defers to declaration-order first-match, pinning x to the first arm")
}

// TestInferMethodOverloadMixedReceiverRejected pins the receiver-mutability uniformity rule
// for overloaded methods: arms that disagree on their `self` receiver are rejected at
// declaration. Overload resolution dispatches on the value arguments, and the
// receiver-mutability check reads only the first arm, so a `mut self` arm reached from a
// plain-`self` body would otherwise dispatch to a mutable receiver unchecked.
func TestInferMethodOverloadMixedReceiverRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			f(self, x: number) -> number { return self.n },
			f(mut self, x: string) -> string { return x },
			peek(self) -> string { return self.f("hi") },
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"Overloaded method 'f' must use the same `self` receiver mutability in every arm.",
		errs[0].Message())
}

// TestInferMethodOverloadUniformMutReceiverAccepted is the passing companion: an overload
// set whose arms all declare `mut self` is well-formed, resolves by argument, and calls
// through a mutable receiver.
func TestInferMethodOverloadUniformMutReceiverAccepted(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			f(mut self, x: number) -> number { return x },
			f(mut self, x: string) -> string { return x },
			run(mut self) -> string { return self.f("hi") },
		}
		val mut c = C(0)
		val r = c.f(1)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
}

// TestInferClassMethodTypeParamsGated pins the gate on a method's own `<T>` binder.
// inferMemberFunc calls inferFunc with allowTypeParams=false, because a member's type
// parameter needs a per-instance projection the class-body freeze does not apply, so
// resolving it would collapse two calls onto one shared var. The binder is reported as an
// unsupported feature and the member infers monomorphically, which leaves each `T` in the
// signature an unbound name.
//
// A declared bound rides on the binder, so gating the binder gates the bound with it: the
// call below passes 1 to a parameter written `T: string` and no mismatch is reported. The
// class's own `<U: number>` binder is a separate mechanism and still enforces, which the
// last case shows. Lifting the gate, tracked by issue #957, should flip this test and
// enable TestInferClassMethodTypeParamBounds below.
func TestInferClassMethodTypeParamsGated(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "InstanceMethodBinderIsGated",
			src: `
				class C {
					pick<T: string>(self, x: T) -> T { return x },
				}
				val c = C()
				val r = c.pick(1)
			`,
			want: []string{
				"Unsupported: TypeParam",
				"Unsupported: TypeRefTypeAnn",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "StaticMethodBinderIsGated",
			src: `
				class C {
					static pick<T: string>(x: T) -> T { return x },
				}
				val r = C.pick(1)
			`,
			want: []string{
				"Unsupported: TypeParam",
				"Unsupported: TypeRefTypeAnn",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "ClassBinderStillEnforcesItsBound",
			src: `
				class C<U: number> {
					v: U,
				}
				val c = C("z")
			`,
			want: []string{`cannot constrain "z" <: number`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}

// DISABLED until issue #957 lands support for a method's own type parameters — the
// per-instance projection inferMemberFunc names, which lets two calls to one generic method
// instantiate independently. Today the binder is reported as unsupported and the member
// infers monomorphically, which TestInferClassMethodTypeParamsGated pins.
//
// Once the gate lifts, a method's `<T: string>` should enforce its bound at the call the
// same way a generic function's does, since both route their binder through
// resolveTypeParams. These cases carry that intended behavior: a satisfying argument is
// accepted, a violating one reports the mismatch, an unbounded binder accepts anything, a
// bound naming a sibling parameter resolves, and two calls to one method instantiate
// independently rather than sharing a var. Re-enable by removing the comment wrapper.
func TestInferClassMethodTypeParamBounds(t *testing.T) {
	/*
		tests := []struct {
			name string
			src  string
			want []string
		}{
			{
				name: "ArgumentInsideBound",
				src: `
					class C {
						pick<T: string>(self, x: T) -> T { return x },
					}
					val c = C()
					val r = c.pick("a")
				`,
			},
			{
				name: "ArgumentOutsideBound",
				src: `
					class C {
						pick<T: string>(self, x: T) -> T { return x },
					}
					val c = C()
					val r = c.pick(1)
				`,
				want: []string{"cannot constrain 1 <: string"},
			},
			{
				name: "UnboundedBinderAcceptsAnyArgument",
				src: `
					class C {
						pick<T>(self, x: T) -> T { return x },
					}
					val c = C()
					val r = c.pick(1)
				`,
			},
			{
				name: "SiblingBoundViolated",
				src: `
					class C {
						pair<A, B: A>(self, a: A, b: B) -> A { return a },
					}
					val c = C()
					val r = c.pair(1, "y")
				`,
				want: []string{`cannot constrain "y" <: 1`},
			},
			{
				name: "TwoCallsInstantiateIndependently",
				src: `
					class C {
						pick<T: string>(self, x: T) -> T { return x },
					}
					val c = C()
					val a = c.pick("a")
					val b = c.pick("b")
				`,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, _, errs := inferSource(t, tt.src)
				var msgs []string
				for _, e := range errs {
					msgs = append(msgs, e.Message())
				}
				require.Equal(t, tt.want, msgs)
			})
		}
	*/
}

// TestInferClassTypeArgArity covers the use-site type-argument count a class reference must
// supply. A trailing parameter with a default is optional, so the accepted range runs from the
// count of parameters with no default up to the total. A count outside that range reports a
// ClassArityMismatchError, whose message states a range when a default makes a parameter
// optional and a single count when every parameter is required.
func TestInferClassTypeArgArity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TooManyAllRequired",
			src: `
				class Box<T> { value: T }
				val b: Box<number, string> = Box(1)
			`,
			want: "class `Box` expects 1 type arguments but got 2",
		},
		{
			name: "TooFewAllRequired",
			src: `
				class Pair<T, U> { first: T, second: U }
				val p: Pair<number> = Pair(1, 2)
			`,
			want: "class `Pair` expects 2 type arguments but got 1",
		},
		{
			name: "BareReferenceToGenericClass",
			src: `
				class Box<T> { value: T }
				val b: Box = Box(1)
			`,
			want: "class `Box` expects 1 type arguments but got 0",
		},
		{
			name: "TooManyWithDefault",
			src: `
				class Pair<T, U = T> { first: T, second: U }
				val p: Pair<number, number, number> = Pair(1, 2)
			`,
			want: "class `Pair` expects between 1 and 2 type arguments but got 3",
		},
		{
			name: "TooFewWithDefault",
			src: `
				class Pair<T, U = T> { first: T, second: U }
				val p: Pair = Pair(1, 2)
			`,
			want: "class `Pair` expects between 1 and 2 type arguments but got 0",
		},
		{
			name: "ArgumentsOnNonGenericClass",
			src: `
				class Point { x: number }
				val p: Point<number> = Point(1)
			`,
			want: "class `Point` expects 0 type arguments but got 1",
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

// TestInferClassTypeArgDefaults covers a reference that omits a trailing parameter carrying a
// default. The omitted argument is filled from the default, and a default naming an earlier
// parameter reads that parameter's supplied argument, so `class Pair<K, V = K>` resolves
// `Pair<string>` to `Pair<string, string>`. A reference supplying every argument is the
// regression guard that the fill only applies to omitted positions.
func TestInferClassTypeArgDefaults(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "OmittedFilledFromDefault",
			src: `
				class Box<T = number> { value: T }
				val p: Box = Box(1)
			`,
			want: "Box<number>",
		},
		{
			name: "DefaultNamesEarlierParam",
			src: `
				class Pair<K, V = K> { first: K, second: V }
				val p: Pair<string> = Pair("a", "b")
			`,
			want: "Pair<string, string>",
		},
		{
			name: "AllArgumentsSupplied",
			src: `
				class Pair<K, V = K> { first: K, second: V }
				val p: Pair<string, number> = Pair("a", 1)
			`,
			want: "Pair<string, number>",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values["p"])
		})
	}
}

// TestInferClassTypeArgArityAcrossRefForms confirms one shared check covers every form a class
// reference takes, since they all resolve through buildClassInstance. Each source names the
// generic class `Box<T>` with no argument in a different position — an `extends` edge, an
// `implements` edge, a field annotation, a type-parameter bound, a constructor parameter, and a
// method return — and each reports the same too-few diagnostic.
func TestInferClassTypeArgArityAcrossRefForms(t *testing.T) {
	srcs := map[string]string{
		"Extends": `
			class Box<T> { value: T }
			class Wrapper extends Box {
				constructor(mut self) {},
			}
		`,
		"Implements": `
			class Box<T> { value: T }
			class Wrapper implements Box { value: number }
		`,
		"FieldAnnotation": `
			class Box<T> { value: T }
			class Holder { boxed: Box }
		`,
		"TypeParamBound": `
			class Box<T> { value: T }
			class Holder<U: Box> { value: U }
		`,
		"ConstructorParam": `
			class Box<T> { value: T }
			class Holder {
				boxed: Box<number>,
				constructor(mut self, boxed: Box) { self.boxed = boxed },
			}
		`,
		"MethodReturn": `
			class Box<T> { value: T }
			class Holder {
				boxed: Box<number>,
				unwrap(self) -> Box { return self.boxed },
			}
		`,
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Len(t, errs, 1)
			require.Equal(t, "class `Box` expects 1 type arguments but got 0", errs[0].Message())
		})
	}
}

// TestInferMutuallyRecursiveGenericClasses guards the arity check against the SCC pre-pass. A
// class is pre-bound to an empty ClassDef so a sibling can name it, and preBindClassTypeParams
// fills in its type parameters afterwards, during the module SCC type-key pass and before any
// class body is inferred. Each class below names the other with a full argument list, so a check
// that ran while a parameter list was still empty would report a spurious mismatch.
func TestInferMutuallyRecursiveGenericClasses(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Node<T> { value: T, tail: Tail<T> }
		class Tail<T> { node: Node<T> }
	`)
	require.Empty(t, errs)
	require.Equal(t, "<T0> {new (value: T0, tail: Tail<T0>) -> Node<T0>}", values["Node"])
}

// TestInferClassTypeArgArityIsOrderIndependent checks that the arity check reads a final
// parameter list no matter which class is declared first. A class body resolves at its value
// key, while a reference to another class depends only on that class's type key, so resolving
// type parameters at the value key would leave the referenced class's list empty whenever the
// referring class happened to be inferred first. The type-key pre-pass resolves every class's
// parameters before any body runs, so both orderings report.
func TestInferClassTypeArgArityIsOrderIndependent(t *testing.T) {
	t.Run("GenericClassDeclaredFirst", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Alpha<T> { v: T }
			class Zeta { t: Alpha }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "class `Alpha` expects 1 type arguments but got 0", errs[0].Message())
		require.Equal(t, "{new (t: Alpha<unknown>) -> Zeta}", values["Zeta"])
	})
	t.Run("GenericClassDeclaredSecond", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Zeta<T> { v: T }
			class Alpha { t: Zeta }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "class `Zeta` expects 1 type arguments but got 0", errs[0].Message())
		// The recovery is a fresh var, so the omitted argument stays local to the reference.
		// Reusing the referenced class's own parameter var would instead give the non-generic
		// Alpha a phantom type parameter, `fn <T0>(t: Zeta<T0>) -> Alpha`.
		require.Equal(t, "{new (t: Zeta<unknown>) -> Alpha}", values["Alpha"])
	})
	t.Run("MutuallyRecursiveGroup", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Node<T> { tail: Tail }
			class Tail<T> { node: Node<T> }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "class `Tail` expects 1 type arguments but got 0", errs[0].Message())
	})
	t.Run("BoundNamesLaterSiblingClass", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Holder<U: Later> { v: U }
			class Later<T> { v: T }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "class `Later` expects 1 type arguments but got 0", errs[0].Message())
	})
}

// TestInferClassRequiredTypeParamAfterDefault covers a required parameter written after a
// defaulted one. Filling T from its default would leave U with no argument, so the accepted
// count runs up to and past the last parameter with no default and `Pair<string>` is rejected.
func TestInferClassRequiredTypeParamAfterDefault(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Pair<T = number, U> { first: T, second: U }
		val p: Pair<string> = Pair("a", 1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "class `Pair` expects 2 type arguments but got 1", errs[0].Message())
}

// TestInferClassSurplusTypeArgStillResolved checks that a type argument past the last declared
// parameter is still resolved even though it is dropped from the instance, so an error inside
// it is reported rather than swallowed by the arity diagnostic.
func TestInferClassSurplusTypeArgStillResolved(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "NonGenericClass",
			src: `
				class Point { x: number }
				val p: Point<DoesNotExist> = Point(1)
			`,
			want: []string{
				"class `Point` expects 0 type arguments but got 1",
				"Unsupported: TypeRefTypeAnn",
			},
		},
		{
			name: "GenericClass",
			src: `
				class Box<T> { v: T }
				val b: Box<number, DoesNotExist> = Box(1)
			`,
			want: []string{
				"class `Box` expects 1 type arguments but got 2",
				"Unsupported: TypeRefTypeAnn",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, test.want, msgs)
		})
	}
}


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
					getX(&self) -> number { return self.x },
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
					constructor(&mut self, x: number, y: number) {
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
			// `val mut` on a constructor call builds an owned-mutable instance, which is
			// what a `mut self` method needs. A plain `val` binding cannot reach one, which
			// TestMutSelfMethodNeedsAMutableReceiver asserts.
			name: "MutSelfMethod",
			src: `
				class Counter {
					count: number,
					constructor(&mut self, count: number) { self.count = count },
					increment(&mut self) -> number { return self.count },
				}
				val mut c = Counter(0)
				val n = c.increment()
			`,
			wantValues: map[string]string{"c": "mut Counter", "n": "number"},
		},
		{
			name: "Getter",
			src: `
				class Box {
					v: number,
					get value(&self) -> number { return self.v },
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
				"Box": "<T> {new (value: T) -> Box<T>}",
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
// parameter — forward, or through a generic class. Each case resolves its
// cross-parameter references without reporting `an unknown-type error`, because the
// shared resolveTypeParams declares every parameter in scope before resolving any bound, so a
// bound reading a later-declared sibling finds it already in scope. A default is restricted
// where a bound is not; TestTypeParamDefaultForwardRef covers that.
func TestInferClassCrossParamBounds(t *testing.T) {
	srcs := map[string]string{
		"ForwardConstraint": `class C<T: U, U> { value: T }`,
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
		class A { x: number, constructor(&mut self) { self.x = 0 } }
		class B extends A { constructor(&mut self) { super() } }
		class Other { y: number, constructor(&mut self) { self.y = 0 } }
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
// rather than silently dropping the edge. An `implements` clause may name an interface,
// so an alias there is resolved instead, and its body and type arguments are checked.
func TestInferClassNonClassSuper(t *testing.T) {
	t.Run("extends a type parameter", func(t *testing.T) {
		_, _, errs := inferSource(t, `class B<T> extends T { constructor(&mut self) {} }`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("extends an interface", func(t *testing.T) {
		_, _, errs := inferSource(t, "interface I { x: number }\nclass C extends I { constructor(&mut self) {} }")
		require.Len(t, errs, 1)
		require.Equal(t, "`I` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("implements a type parameter", func(t *testing.T) {
		_, _, errs := inferSource(t, `class C<T> implements T {}`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("implements an alias of a primitive", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type N = number
			class C implements N { constructor(&mut self) {} }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "`N` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
	t.Run("implements an interface with too many type arguments", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			interface Box<T> { value: T }
			class C implements Box<number, string> { value: number, constructor(&mut self) { self.value = 0 } }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "type alias `Box` expects 1 type argument but got 2", errs[0].Message())
	})
	t.Run("extends a type parameter applied to arguments", func(t *testing.T) {
		// A type parameter carries no type arguments, so `T<X>` is doubly ill-formed. The
		// extends clause still requires a class, so the non-class binding is reported here
		// rather than dropped silently.
		_, _, errs := inferSource(t, `class B<T, X> extends T<X> { constructor(&mut self) {} }`)
		require.Len(t, errs, 1)
		require.Equal(t, "`T` does not name a class and cannot be extended or implemented.", errs[0].Message())
	})
}

// TestInferDeclareClassImplementsInterface covers an `implements` clause naming an interface. On
// a `declare` class the interface supplies each member the class does not have through its own
// body or its `extends` chain. A class with a body takes nothing from the clause, and neither
// form reports the interface as a non-class.
func TestInferDeclareClassImplementsInterface(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "ContributesAMember",
			src: `
				interface Named { readonly name: string }
				declare class Person implements Named { readonly age: number }
				fn f(p: Person) { return p.name }
			`,
			want: map[string]string{"f": "fn (p: Person) -> string"},
		},
		{
			name: "KeepsTheClassOwnMember",
			src: `
				interface HasId { readonly id: string | number }
				declare class Row implements HasId { readonly id: number }
				fn f(r: Row) { return r.id }
			`,
			want: map[string]string{"f": "fn (r: Row) -> number"},
		},
		{
			name: "KeepsTheInheritedMember",
			src: `
				interface HasId { readonly id: string | number }
				declare class Base { readonly id: number }
				declare class Row extends Base implements HasId { constructor(&mut self) }
				fn f(r: Row) { return r.id }
			`,
			want: map[string]string{"f": "fn (r: Row) -> number"},
		},
		{
			name: "SubstitutesTheTypeArguments",
			src: `
				interface Box<T> { readonly value: T }
				declare class NumBox implements Box<number> {}
				fn f(b: NumBox) { return b.value }
			`,
			want: map[string]string{"f": "fn (b: NumBox) -> number"},
		},
		{
			name: "ReadsAnExtendedInterface",
			src: `
				interface A { readonly x: number | string, readonly y: boolean }
				interface B extends A { readonly x: number }
				declare class C implements B {}
				fn f(c: C) { return [c.x, c.y] }
			`,
			want: map[string]string{"f": "fn (c: C) -> [number, boolean]"},
		},
		{
			name: "TwoInterfacesDeclareASharedNameIdentically",
			src: `
				interface First { readonly v: number }
				interface Second { readonly v: number }
				declare class C implements First, Second {}
				fn f(c: C) { return c.v }
			`,
			want: map[string]string{"f": "fn (c: C) -> number"},
		},
		{
			// The two methods differ only in their type parameter's name, so they declare the
			// same member.
			name: "TwoInterfacesDeclareAGenericMethodIdentically",
			src: `
				interface First { id<T>(self, x: T) -> T }
				interface Second { id<U>(self, x: U) -> U }
				declare class C implements First, Second {}
				fn f(c: C) { return c.id(1) }
			`,
			want: map[string]string{"f": "fn (c: C) -> 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}

	t.Run("AClassWithABodyTakesNothing", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			interface Named { readonly name: string }
			class Person implements Named {
				age: number,
				constructor(&mut self) { self.age = 0 }
			}
			fn f(p: Person) { return p.name }
		`)
		require.Equal(t, []string{
			"object is missing property: name",
			"Class 'Person' does not implement interface 'Named': missing member 'name'",
		}, errorMessagesOf(errs))
	})
}

// Two interfaces in a `declare` class's `implements` clause that declare one name differently
// are reported when the class does not declare or inherit that name. TypeScript rejects the same
// conflict in an interface that extends both, which is how the generated lib writes it. The first
// interface still supplies the member, so a read of it keeps a type.
func TestInferDeclareClassImplementsConflictingInterfaces(t *testing.T) {
	values, _, errs := inferSource(t, `
		interface First { readonly v: number }
		interface Second { readonly v: string }
		declare class C implements First, Second {}
		fn f(c: C) { return c.v }
	`)
	require.Equal(t, []string{
		"class `C` implements `First` and `Second`, which declare `v` differently: `{readonly v: number}` and `{readonly v: string}`",
	}, errorMessagesOf(errs))
	require.Equal(t, "fn (c: C) -> number", values["f"])
}

// TestInferClassExtendFinal covers the rule that a final class cannot be a superclass:
// a final class has no subclasses (exact-types §2.6), so an `extends` clause naming one
// reports CannotExtendFinalClassError. A non-final superclass is unaffected.
func TestInferClassExtendFinal(t *testing.T) {
	t.Run("extending a final class is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			final class A { x: number, constructor(&mut self) { self.x = 0 } }
			class B extends A { constructor(&mut self) {} }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "Cannot extend `A`; it is a final class and has no subclasses.", errs[0].Message())
	})
	t.Run("extending a non-final class is allowed", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class A { x: number, constructor(&mut self) { self.x = 0 } }
			class B extends A { constructor(&mut self) { super() } }
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
					getB(&self) { return self.b },
				}
				class B {
					a: A,
					getA(&self) { return self.a },
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
					getN(&self) -> number { return self.n },
					double(&self) -> number { return self.getN() },
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
					a(&self) -> number { return self.b() },
					b(&self) -> number { return self.n },
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
					loop(&self, k: number) -> number { return self.loop(k) },
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
					ping(&self, k: number) -> number { return self.pong(k) },
					pong(&self, k: number) -> number { return self.ping(k) },
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
					get value(&self) -> number { return self.v },
					twice(&self) -> number { return self.value },
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
					helper(&self) -> number { return self.n },
					update(&mut self) -> number { return self.helper() },
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
					f(&self, {a, b}: {a: number, b: number}) -> number { return self.g(a) },
					g(&self, x: number) -> number { return x },
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
					constructor(&mut self, x: number) {
						self.n = x
					},
					getN(&self) -> number { return self.n },
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

// TestInferClassReturnsSelf covers a member that hands back `self` rather than reading
// through it. The bare `self` is an instance of the class, so a method returning it
// infers the class as its return type, with or without an annotation.
func TestInferClassReturnsSelf(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			name: "InferredReturn",
			src: `
				class P {
					x: number,
					id(self) { return self },
				}
				val p = P(1)
				val m = p.id
				val q = p.id()
			`,
			wantValues: map[string]string{"m": "fn () -> P", "q": "P"},
		},
		{
			name: "AnnotatedReturn",
			src: `
				class P {
					x: number,
					id(self) -> Self { return self },
				}
				val p = P(1)
				val q = p.id()
			`,
			wantValues: map[string]string{"q": "P"},
		},
		{
			name: "DeclaredReturn",
			src: `
				declare class P {
					id(self) -> Self,
				}
				declare val p: P
				val q = p.id()
			`,
			wantValues: map[string]string{"q": "P"},
		},
		{
			// A read through `self` still sees the structural body.
			name: "FieldRead",
			src: `
				class P {
					x: number,
					getX(self) { return self.x },
				}
				val p = P(1)
				val x = p.getX()
			`,
			wantValues: map[string]string{"x": "number"},
		},
		{
			// A borrowed receiver hands back the borrow, so the chain stays on one instance.
			name: "FluentChain",
			src: `
				class Point {
					x: number,
					y: number,
					scale(&mut self, factor: number) {
						self.x = self.x * factor
						self.y = self.y * factor
						return self
					},
					translate(&mut self, dx: number, dy: number) {
						self.x = self.x + dx
						self.y = self.y + dy
						return self
					},
				}
				val mut p = Point(5, 10)
				val q = p.scale(2).translate(1, 1)
				val x = q.x
			`,
			wantValues: map[string]string{"x": "number"},
		},
		{
			name: "PassedAsArgument",
			src: `
				declare fn take(p: P) -> number
				class P {
					x: number,
					send(self) { return take(self) },
				}
				val p = P(1)
				val n = p.send()
			`,
			wantValues: map[string]string{"n": "number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, nil)
		})
	}
}

// TestInferClassMutualRecursionRequiresAnnotation asserts the annotation gate: a pair of
// mutually recursive methods with no annotated return anywhere in the cycle cannot ground
// its own return types, so every member of the cycle is reported. Annotating either
// member breaks the cycle, which the positive test above covers.
func TestInferClassMutualRecursionRequiresAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			ping(&self, k: number) { return self.pong(k) },
			pong(&self, k: number) { return self.ping(k) },
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
			bump(&mut self) -> number { return self.n },
			peek(&self) -> number { return self.bump() },
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
			bump(&mut self) -> number { return self.n },
			read(&self) -> number { return self.n },
			run(&mut self) -> number { return self.bump() },
			also(&mut self) -> number { return self.read() },
		}
	`)
	require.Empty(t, errs)
}

// TestInferClassMutGetterFromImmutMethod asserts the receiver check for a getter member: a
// `mut self` getter read from a plain-`self` method is rejected for the same reason a
// `mut self` method call is — the caller holds only a shared borrow. The getter carries its
// receiver on GetterElem.SelfParam rather than a FuncType, so this exercises a distinct
// member kind from the method case.
func TestInferClassMutGetterFromImmutMethod(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			v: number,
			get doubled(&mut self) -> number { return self.v },
			peek(&self) -> number { return self.doubled },
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
			read(&self) { return self.v },
			alias(&self) { return self.read() },
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
// `get first(&self) { return self.v }` on `class Box<T>` reads `T`, so `b.first` for `b : Box<5>`
// projects to `5`. The kept-flow coalesce keeps `T` symbolic through the getter's return var
// the same way it does for a method return.
func TestInferClassGenericGetterReturnsTypeParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			get first(&self) { return self.v },
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
			read(&self) { return self.v },
			label(&self) -> string { return "box" },
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
			speak(&self) -> string { return "..." },
		}
		class Dog extends Animal {
			constructor(&mut self) { super("rex") }
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
			constructor(&mut self) { super(0) }
		}
		class Leaf extends Mid {
			constructor(&mut self) { super() }
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
			constructor(&mut self) { super(0) }
		}
		class C extends B {
			constructor(&mut self) { super() }
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
// The constructor writes Dog's own field. It could assign the inherited one too, since
// `self` carries the whole chain, but definite assignment covers only a class's own fields,
// so leaving it out is not an error.
func TestInferClassGenericSubGenericSuper(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Animal<A> {
			food: A,
		}
		class Dog<D> extends Animal<D> {
			tag: D,
			constructor(&mut self, tag: D) {
				super(tag)
				self.tag = tag
			}
		}
		val d = Dog("bone")
		val f = d.food
		val g = d.tag
	`)
	require.Empty(t, errs)
	require.Equal(t, "<D> {new (tag: D) -> Dog<D>}", values["Dog"])
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
			constructor(&mut self) { super("bone") }
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
// so neither reports `an unknown-type error` and the class infers generic in `T`.
func TestInferClassGenericMemberParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> {
			v: T,
			constructor(&mut self, v: T) { self.v = v },
			replace(&mut self, next: T) { self.v = next },
		}
		val b = Box(5)
	`)
	require.Empty(t, errs)
	require.Equal(t, "<T> {new (v: T) -> Box<T>}", values["Box"])
	require.Equal(t, "Box<5>", values["b"])
}

// TestInferClassNameInAnnotation checks that a class name resolves in a type annotation
// outside a class body — a top-level `val` type and a function parameter. resolveTypeAnn
// consults the enclosing scope wherever it runs, so a bare `Point` or a generic instance
// `Box<number>` resolves through the same path a class body uses, rather than reporting
// `an unknown-type error`.
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
				accept(&self, x: T) { },
			}
			val narrow: Consumer<number> = Consumer()
			val wide: Consumer<number | string> = narrow
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a mut self parameter does not shape the immutable view", func(t *testing.T) {
		// No immutable reference reaches `push`, so its parameter says nothing about how
		// two `Sink` instances relate while shared. `T` is bivariant here: it reaches no
		// member such a reference can use, which is what leaves both directions open.
		//
		// Nothing observable is widened. A parameter reachable only under a mutable
		// receiver means the class declares no `T`-typed storage, so there is no read to
		// come back at the wrong type. The case below adds one and the widening stops.
		_, _, errs := inferSource(t, `
			class Sink<T> {
				push(&mut self, item: T) -> undefined { return undefined },
			}
			fn widen(s: Sink<number>) -> Sink<number | string> { return s }
			fn narrow(s: Sink<number | string>) -> Sink<number> { return s }
		`)
		require.Empty(t, errs)
	})
	t.Run("a field keeps the parameter covariant alongside a mut self reader", func(t *testing.T) {
		// `take` demands a mutable receiver, but `slot` does not, and a field read is an
		// output position the immutable view has. So `T` is covariant rather than
		// bivariant, and the narrowing this asks for is rejected. A `mut self` method can
		// only hand back a `T` if the class holds one, and holding one is what puts `T` in
		// the immutable view.
		_, _, errs := inferSource(t, `
			class Cell<T> {
				slot: T,
				take(&mut self) -> T { return self.slot },
			}
			fn narrow(c: Cell<number | string>) -> Cell<number> { return c }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a self reader beside a mut self mutator stays covariant", func(t *testing.T) {
		// The shape `Array<T>` has. `read` is an output position both views reach and
		// `write` an input position only a mutable reference reaches, so the immutable
		// view measures `T` covariant and the widening holds.
		_, _, errs := inferSource(t, `
			class Slot<T> {
				value: T,
				read(&self) -> T { return self.value },
				write(&mut self, v: T) -> undefined { return undefined },
			}
			fn widen(s: Slot<number>) -> Slot<number | string> { return s }
		`)
		require.Empty(t, errs)
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
				read(&self) -> T { return self.value },
			}
			val wide: Box<number | string> = Box(5)
		`)
		require.Empty(t, errs)
	})
	t.Run("getter occurrence widens", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class Box<T> {
				value: T,
				get item(&self) -> T { return self.value },
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
				accept(&self, x: T) { },
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
				set item(&mut self, x: T) { },
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
				a(&self, x: A) { },
				b(&self, y: B) { },
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
				accept(&self, x: T) { },
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
				accept(&self, x: T) { },
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
				accept(&self, x: T) { },
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
				read(&self) -> T { return self.value },
			}
			fn wide(r: mut Reader<number | string>) { }
			fn narrow(r: mut Reader<number>) { wide(r) }
		`)
		require.Empty(t, errs)
	})
	t.Run("mut self method parameter is contravariant under mut", func(t *testing.T) {
		// `push` is unreachable immutably, so it shapes the mutable view alone, where it is
		// an input position like any method parameter. `narrow` can only call
		// `push(<number>)`, and the instance behind it takes a `number | string`.
		_, _, errs := inferSource(t, `
			class Sink<T> {
				push(&mut self, item: T) -> undefined { return undefined },
			}
			fn narrow(s: mut Sink<number>) { }
			fn wide(s: mut Sink<number | string>) { narrow(s) }
		`)
		require.Empty(t, errs)
	})
	t.Run("mut self method parameter rejects a widening", func(t *testing.T) {
		// The other direction, which contravariance rejects: `wide` may call
		// `push("s")`, and the instance's `number`-only `push` cannot take it.
		_, _, errs := inferSource(t, `
			class Sink<T> {
				push(&mut self, item: T) -> undefined { return undefined },
			}
			fn wide(s: mut Sink<number | string>) { }
			fn narrow(s: mut Sink<number>) { wide(s) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a mut self mutator pins a parameter a self reader returns", func(t *testing.T) {
		// The shape `Array<T>` has, and the reason it is covariant shared and invariant
		// mutable. `read` is an output position both views have. `write` is an input
		// position only the mutable view has, and it is what rejects the widening the
		// immutable check accepts.
		_, _, errs := inferSource(t, `
			class Slot<T> {
				value: T,
				read(&self) -> T { return self.value },
				write(&mut self, v: T) -> undefined { return undefined },
			}
			fn wide(s: mut Slot<number | string>) { }
			fn narrow(s: mut Slot<number>) { wide(s) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a setter pins a parameter its getter returns", func(t *testing.T) {
		// A setter is a write, so no immutable reference reaches it and it lands in the
		// mutable view alone. The field is `readonly` and the getter is an output position,
		// so the setter is the only input position here and the only thing that can reject
		// this. The immutable case below is the same class, accepted.
		const src = `
			class Prop<T> {
				readonly v: T,
				get item(&self) -> T { return self.v },
				set item(&mut self, x: T) { },
			}
		`
		_, _, errs := inferSource(t, src+`
			fn wide(p: mut Prop<number | string>) { }
			fn narrow(p: mut Prop<number>) { wide(p) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())

		_, _, errs = inferSource(t, src+`
			fn widen(p: Prop<number>) -> Prop<number | string> { return p }
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
				read(&self) -> T { return self.value },
			}
			fn wide(b: mut Box<number | string>) { }
			fn narrow(b: mut Box<number>) { wide(b) }
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain string <: number", errs[0].Message())
	})
	t.Run("a mut subclass reference reaches its superclass", func(t *testing.T) {
		// The write `reset` performs lands on `pos`, and checkInheritedMembers makes a
		// member Dog redeclares invariant against Animal's declaration whenever Animal's is
		// writable. Every member reachable through `mut Animal` therefore holds the type
		// Animal declares, so the widening is licensed. Dog's extra `breed` is unreachable
		// through the Animal view, so adding it changes nothing.
		_, _, errs := inferSource(t, `
			class Animal {
				pos: {x: number},
				constructor(&mut self, pos: {x: number}) { self.pos = pos },
			}
			class Dog extends Animal {
				breed: string,
				constructor(&mut self, breed: string) {
					super({x: 0})
					self.breed = breed
				},
			}
			fn reset(a: mut Animal) { a.pos = {x: 0} }
			fn resetDog(d: mut Dog) { reset(d) }
		`)
		require.Empty(t, errs)
	})
	t.Run("an immutable subclass reference reaches its superclass", func(t *testing.T) {
		// The immutable widening is licensed for the same reason with less to check.
		// `describe` can only read through `a`, and every member Animal declares is one Dog
		// carries, so no read can miss.
		_, _, errs := inferSource(t, `
			class Animal {
				name: string,
				constructor(&mut self, name: string) { self.name = name },
			}
			class Dog extends Animal {
				breed: string,
				constructor(&mut self, name: string, breed: string) {
					super(name)
					self.breed = breed
				},
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
				accept(&self, x: T) { },
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
				read(&self) -> T { return self.value },
			}
			fn wide(b: Box<number | string>) { }
			fn narrow(b: Box<number>) { wide(b) }
		`)
		require.Empty(t, errs)
	})
}

// TestInferClassSelfMethodInputVariance covers a parameter that a `&self` method takes as
// input and that the class also gives an output position. The method cannot store what it
// takes into the instance, so the output decides and the immutable view is covariant. A
// field write still makes the mutable view invariant, and a parameter with no output
// position stays contravariant.
//
// Keeping such a class covariant lets `Bag<number>` widen to `Bag<number | string>`, so
// a subclass instance can be reached through a view wider than the one it extends. An
// override of the method is therefore checked against the ancestor's method at the widest
// instance the subclass can be read as, not at the arguments its `extends` clause writes.
// Each covariant argument there is its parameter's bound, or `unknown` for an unbounded
// parameter. That is why the expected messages below name `fn (x: unknown) -> boolean`
// where the `extends` clause says `Bag<number>`. Making the parameter invariant instead
// would keep the override check at `Bag<number>`, but it would also stop the widening.
func TestInferClassSelfMethodInputVariance(t *testing.T) {
	t.Parallel()

	const bag = `
		class Bag<T> {
			items: Array<T>,
			contains(&self, x: T) -> boolean { return false },
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an immutable instance widens",
			src: bag + `
				fn widen(b: &Bag<number>) -> &Bag<number | string> { return b }
			`,
		},
		{
			name: "an immutable instance does not narrow",
			src: bag + `
				fn narrow(b: &Bag<number | string>) -> &Bag<number> { return b }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "a method taking and returning the parameter widens",
			src: `
				class Echo<T> {
					echo(&self, x: T) -> T { return x },
				}
				fn widen(e: &Echo<number>) -> &Echo<number | string> { return e }
			`,
		},
		{
			// `NumBag` extends `Bag<number>`, but it can be read as `Bag<unknown>`. Through
			// that view `contains("s")` would run this override, which compares a string
			// with `>`. The override is checked against `Bag<unknown>`'s `contains`.
			name: "an override narrowing a covariant input is rejected",
			src: bag + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean { return x > 1 },
				}
			`,
			want: []string{
				"class `NumBag` redeclares inherited member `contains` with type " +
					"`fn (x: number) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// The override matches `Bag<unknown>`'s `contains`, the widest view of `AnyBag`.
			name: "an override accepting any input is allowed",
			src: bag + `
				class AnyBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: unknown) -> boolean { return false },
				}
			`,
		},
		{
			// `U` is unbounded and nothing in `Bag2` consumes it, so the override is as
			// generic in `U` as `Bag`'s method is in `T`. A wider view passes a wider `U`,
			// and the override is checked at `Bag<U>` rather than `Bag<unknown>`.
			name: "a generic override keeps its parameter",
			src: bag + `
				class Bag2<U> extends Bag<U> {
					constructor(&mut self, items: Array<U>) { super(items) },
					contains(&self, x: U) -> boolean { return false },
				}
			`,
		},
		{
			// `U: number` lets the override use `x` as a `number`, so it is not generic
			// in the way the widening needs.
			name: "a bounded generic override narrowing a covariant input is rejected",
			src: bag + `
				class NumBag2<U: number> extends Bag<U> {
					constructor(&mut self, items: Array<U>) { super(items) },
					contains(&self, x: U) -> boolean { return x > 1 },
				}
			`,
			want: []string{
				"class `NumBag2` redeclares inherited member `contains` with type " +
					"`fn (x: U) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// `Bag2<U>` measures `U` invariant, since it reaches the `extends` clause, so
			// `Bag2<number>` does not widen. `Bag<number>` still does, and that view
			// reaches the override below.
			name: "an override is checked against every ancestor that widens",
			src: bag + `
				class Bag2<U> extends Bag<U> {
					constructor(&mut self, items: Array<U>) { super(items) },
					contains(&self, x: U) -> boolean { return false },
				}
				class NumBag3 extends Bag2<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean { return x > 1 },
				}
			`,
			want: []string{
				"class `NumBag3` redeclares inherited member `contains` with type " +
					"`fn (x: number) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// `Keyed<string>` is a `Bag<string>` through its `extends` clause, and `Bag` is
			// covariant, so `Keyed<string>` is also a `Bag<number | string>`. That is how
			// `go` hands `k` to `probe`. `Keyed<string>` is not a `Keyed<number | string>`,
			// since the `key` field makes `Keyed` invariant in `U`, but the `Bag` view is
			// enough. Through it `contains(42)` runs this override, which passes `42` to
			// `self.key`, a function that takes only a `string`. Every subtype relation
			// here holds, so the checker rejects the override, the one place that assumes
			// `x` is a `U`.
			name: "a generic override holding a consumer of its parameter is rejected",
			src: bag + `
				class Keyed<U> extends Bag<U> {
					key: fn (x: U) -> number,
					constructor(&mut self, key: fn (x: U) -> number) {
						super([])
						self.key = key
					},
					contains(&self, x: U) -> boolean { return self.key(x) > 0 },
				}
				fn probe(b: &Bag<number | string>) -> boolean { return b.contains(42) }
				fn go(k: &Keyed<string>) -> boolean { return probe(k) }
			`,
			want: []string{
				"class `Keyed` redeclares inherited member `contains` with type " +
					"`fn (x: U) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// Writing `x` into `sink` makes `Logged` invariant in `U`. The override is
			// no longer generic in the way the widening needs, so it is checked at
			// `Bag<unknown>`.
			name: "a generic override storing its parameter in a mut field is rejected",
			src: bag + `
				class Logged<'a, U> extends Bag<U> {
					sink: &'a mut Array<U>,
					constructor(&mut self, sink: &'a mut Array<U>) {
						super([])
						self.sink = sink
					},
					contains(&self, x: U) -> boolean {
						self.sink.push(x)
						return false
					},
				}
			`,
			want: []string{
				"class `Logged` redeclares inherited member `contains` with type " +
					"`fn (x: U) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// `NBag<1>` widens no further than `NBag<number>`, so the override has to accept
			// a `number` and nothing wider.
			name: "an override is widened to the parameter's bound",
			src: `
				class NBag<T: number> {
					readonly v: T,
					has(&self, x: T) -> boolean { return false },
				}
				class One extends NBag<1> {
					constructor(&mut self) { super(1) },
					has(&self, x: number) -> boolean { return x > 0 },
				}
			`,
		},
		{
			// The widest view of `One` is `NBag<number>`, so the override has to accept
			// every `number`, not just `1`.
			name: "an override narrower than the parameter's bound is rejected",
			src: `
				class NBag<T: number> {
					readonly v: T,
					has(&self, x: T) -> boolean { return false },
				}
				class One extends NBag<1> {
					constructor(&mut self) { super(1) },
					has(&self, x: 1) -> boolean { return true },
				}
			`,
			want: []string{
				"class `One` redeclares inherited member `has` with type " +
					"`fn (x: 1) -> boolean`, which is not compatible with " +
					"`fn (x: number) -> boolean` declared by `NBag`",
			},
		},
		{
			// `Keyed` holds the consumer and `K2` reaches it through an inherited field.
			name: "a generic override reaching an inherited consumer of its parameter is rejected",
			src: bag + `
				class Keyed<U> extends Bag<U> {
					key: fn (x: U) -> number,
					constructor(&mut self, key: fn (x: U) -> number) {
						super([])
						self.key = key
					},
				}
				class K2<V> extends Keyed<V> {
					constructor(&mut self, key: fn (x: V) -> number) { super(key) },
					contains(&self, x: V) -> boolean { return self.key(x) > 0 },
				}
			`,
			want: []string{
				"class `K2` redeclares inherited member `contains` with type " +
					"`fn (x: V) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// `T` is covariant, so `D` reads as `C<unknown, unknown>` and `U`'s bound widens
			// with it.
			name: "a bound naming a covariant parameter widens with it",
			src: `
				class C<T, U: T> {
					readonly t: T,
					readonly u: U,
					has(&self, x: U) -> boolean { return false },
				}
				class D extends C<number, number> {
					constructor(&mut self) { super(1, 1) },
					has(&self, x: number) -> boolean { return x > 0 },
				}
			`,
			want: []string{
				"class `D` redeclares inherited member `has` with type " +
					"`fn (x: number) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `C`",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// TestInferClassNestedClassVariance covers a class instance held in a member. Each of its
// arguments is passed on at the variance the nested class measures for it, and a mutable
// reference to the holder reaches the nested instance at its mutable-view variance.
func TestInferClassNestedClassVariance(t *testing.T) {
	t.Parallel()

	const sink = `
		class Sink<T> {
			readonly f: fn (x: T) -> undefined,
			feed(&self, x: T) -> undefined { return self.f(x) },
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// `W<number>` read as `W<number | string>` would let `feed("s")` reach a
			// `Sink<number>` whose `f` takes only a `number`.
			name: "a contravariant nested class rejects a widening",
			src: sink + `
				class W<T> {
					readonly sink: Sink<T>,
					feed(&self, x: T) -> undefined { return self.sink.feed(x) },
				}
				fn widen(w: &W<number>) -> &W<number | string> { return w }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "a contravariant nested class allows a narrowing",
			src: sink + `
				class W<T> {
					readonly sink: Sink<T>,
				}
				fn narrow(w: &W<number | string>) -> &W<number> { return w }
			`,
		},
		{
			name: "a covariant nested class widens",
			src: `
				class Bag<T> { readonly items: Array<T> }
				fn widen(b: &Bag<number>) -> &Bag<number | string> { return b }
			`,
		},
		{
			// A `mut Bag<number>` read as `mut Bag<number | string>` could run
			// `b.items.push("s")`, since a mutable reference reaches into the field.
			name: "a nested class in a readonly field is invariant through mut",
			src: `
				class Bag<T> { readonly items: Array<T> }
				fn wide(b: &mut Bag<number | string>) { }
				fn narrow(b: &mut Bag<number>) { wide(b) }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// `A` is measured after `B` whichever is declared first.
			name: "a nested class declared later widens",
			src: `
				class A<T> { readonly b: B<T> }
				class B<T> { readonly v: T }
				fn widen(a: &A<number>) -> &A<number | string> { return a }
			`,
		},
		{
			// `widen(h).c("s")` would call a function that takes only a `number`.
			name: "an alias over a consumer rejects a widening",
			src: `
				type Consumer<T> = fn (x: T) -> undefined
				class H<T> { readonly c: Consumer<T> }
				fn widen(h: &H<number>) -> &H<number | string> { return h }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "an alias over a holder widens",
			src: `
				type Holder<T> = {readonly v: T}
				class H<T> { readonly c: Holder<T> }
				fn widen(h: &H<number>) -> &H<number | string> { return h }
			`,
		},
		{
			// Nothing writes through `&'a Box<T>`, even through a `mut H`.
			name: "an immutable borrow in a field stays covariant through mut",
			src: `
				class Box<T> { value: T }
				class H<'a, T> { readonly r: &'a Box<T> }
				fn wide(h: &mut H<'static, number>) { }
				fn narrow(h: &mut H<'static, 1>) { wide(h) }
			`,
		},
		{
			// A write such as `h.r.value = 2` goes through `&'a mut Box<T>`, so `T` is
			// invariant. Even an immutable `&H` reaches the mutable borrow the field holds
			// and cannot widen.
			name: "a mutable borrow in a field is invariant",
			src: `
				class Box<T> { value: T }
				class H<'a, T> { readonly r: &'a mut Box<T> }
				fn wide(h: &mut H<'static, number>) { }
				fn narrow(h: &mut H<'static, 1>) { wide(h) }
				fn widen(h: &H<'static, 1>) -> &H<'static, number> { return h }
			`,
			want: []string{"cannot constrain number <: 1", "cannot constrain number <: 1"},
		},
		{
			name: "classes holding each other widen together",
			src: `
				class A<T> { readonly v: T, readonly b: B<T> | null }
				class B<T> { readonly w: T, readonly a: A<T> | null }
				fn widen(a: &A<number>) -> &A<number | string> { return a }
			`,
		},
		{
			// `B` holds a consumer of `T`, and `A` passes `T` on to `B`, so the group is
			// contravariant. `A` narrows but does not widen.
			name: "classes holding each other share a consumer's variance",
			src: `
				class A<T> { readonly b: B<T> | null }
				class B<T> { readonly f: fn (x: T) -> undefined, readonly a: A<T> | null }
				fn widen(a: &A<number>) -> &A<number | string> { return a }
				fn narrow(a: &A<number | string>) -> &A<number> { return a }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// A superclass naming its subclass keeps the subclass inferred after it, so
			// the subclass reads the method it inherits.
			name: "a superclass may name its subclass",
			src: `
				class Z {
					asB(&self) -> B | null { return null },
					greet(&self) -> string { return "hi" },
				}
				class B extends A {
					constructor(&mut self) { super() },
					hello(&self) -> string { return self.greet() },
				}
				class A extends Z {
					constructor(&mut self) { super() },
				}
			`,
		},
		{
			// The inner `Holder` is measured too, so the consumer it wraps makes `H`
			// contravariant. Skipping it as already seen would leave `T` unconstrained.
			name: "an alias nested in itself reads its inner argument",
			src: `
				type Holder<T> = {readonly v: T}
				class H<T> { readonly c: Holder<Holder<fn (x: T) -> undefined>> }
				fn widen(h: &H<number>) -> &H<number | string> { return h }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "a recursive alias widens",
			src: `
				type List<T> = {readonly head: T, readonly tail: List<T> | null}
				class H<T> { readonly c: List<T> }
				fn widen(h: &H<number>) -> &H<number | string> { return h }
			`,
		},
		{
			// `Cell` declares `in out T` and is read at it inside the group, so `Holder`
			// cannot widen and write a string into a `Cell<number>`.
			name: "a group reads a member at its declared modifier",
			src: `
				declare class Cell<in out T> {
					get(&self) -> T,
					set(&self, v: T) -> undefined,
					readonly h: Holder<T> | null,
				}
				class Holder<T> { readonly c: Cell<T> }
				fn widen(h: &Holder<number>) -> &Holder<number | string> { return h }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// The group's vectors swap at every pass and never settle, so both classes
			// stay invariant. `A` neither widens nor narrows.
			name: "a group that never settles stays invariant",
			src: `
				class A<T> { m(&self, x: T) -> undefined { return undefined }, readonly b: B<T> | null }
				class B<T> { readonly f: fn (x: A<T>) -> undefined }
				fn widen(a: &A<number>) -> &A<number | string> { return a }
				fn narrow(a: &A<number | string>) -> &A<number> { return a }
			`,
			want: []string{"cannot constrain string <: number", "cannot constrain string <: number"},
		},
		{
			name: "a class holding itself widens",
			src: `
				class List<T> {
					readonly head: T,
					readonly tail: List<T> | undefined,
				}
				fn widen(l: &List<number>) -> &List<number | string> { return l }
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// TestInferClassVarianceAcrossNamespaces asserts that two aliases sharing a short name in
// different namespaces are walked as two aliases. `bar.H` holds a consumer of `T`, so `C`
// does not widen.
func TestInferClassVarianceAcrossNamespaces(t *testing.T) {
	t.Parallel()

	_, _, errs := inferModule(parseModuleFiles(t, map[string]string{
		"foo/h.esc": `export type H<T> = {readonly v: T}`,
		"bar/h.esc": `export type H<T> = {readonly f: fn (x: T) -> undefined}`,
		"input.esc": `
			class C<T> { readonly a: foo.H<T>, readonly b: bar.H<T> }
			fn widen(c: &C<number>) -> &C<number | string> { return c }
		`,
	}))
	require.Equal(t, []string{"cannot constrain string <: number"}, errorMessagesOf(errs))
}

// TestInferClassVarianceModifiers covers the declaration-site `in`/`out`/`in out`
// modifiers (C2). A modifier as strict as the measured variance or stricter checks, and the
// declared variance is what subtyping then reads. A modifier looser than the measured
// variance reports VarianceMismatchError.
func TestInferClassVarianceModifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "matching out modifier on a covariant parameter checks",
			src:  `class Box<out T> { value: T }`,
		},
		{
			name: "in modifier on a covariant parameter is rejected",
			src:  `class Box<in T> { value: T }`,
			want: []string{"type parameter `T` is declared contravariant but is actually covariant"},
		},
		{
			name: "out modifier on an invariant parameter is rejected",
			src: `class Cell<out T> {
				readonly value: T,
				readonly sink: fn (x: T) -> undefined,
			}`,
			want: []string{"type parameter `T` is declared covariant but is actually invariant"},
		},
		{
			name: "matching in modifier on a contravariant parameter checks",
			src: `class Consumer<in T> {
				accept(&self, x: T) { },
			}`,
		},
		{
			// The modifier is stricter than the body, and the declared variance is what a
			// widening is checked against.
			name: "in out modifier rejects a widening the body allows",
			src: `class C<in out T> {
				readonly v: T,
				has(&self, x: T) -> boolean { return false },
			}
			fn widen(c: &C<number>) -> &C<number | string> { return c }`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// `in out T` keeps `C<number>` from widening, so nothing passes `has` a value its
			// override cannot take.
			name: "in out modifier allows an override at a fixed argument",
			src: `class C<in out T> {
				readonly v: T,
				has(&self, x: T) -> boolean { return false },
			}
			class D extends C<number> {
				constructor(&mut self) { super(1) },
				has(&self, x: number) -> boolean { return x > 0 },
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
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
					set value(&mut self, x: number) { self.v = x },
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
					set value(&mut self) { },
				}
			`,
			want: "Setter 'value' must declare exactly one value parameter; found 0.",
		},
		{
			name: "SetterTooManyParams",
			src: `
				class C {
					v: number,
					set value(&mut self, a: number, b: number) { },
				}
			`,
			want: "Setter 'value' must declare exactly one value parameter; found 2.",
		},
		{
			name: "SetterPlainSelfReceiver",
			src: `
				class C {
					v: number,
					set value(&self, x: number) { },
				}
			`,
			want: "Setter 'value' must declare a `&mut self` receiver; writing through it mutates the instance.",
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
					constructor(&mut self) { super(5) }
				}
			`,
			want: "cannot find type `Bogus`",
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
					constructor(&mut self, x: number, y: number) {
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
					constructor(&mut self, x: number) {
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
					constructor(&mut self, cond: boolean) {
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
					constructor(&mut self, x: number) {
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
					log(&self) -> number { return self.x },
					constructor(&mut self, x: number) {
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
					log(&self) -> number { return self.x },
					constructor(&mut self, x: number) {
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
					constructor(&mut self, x: number) {
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
					constructor(&mut self) { },
				}
			`,
			want: "Fields 'x', 'y' are not initialized on every path through the constructor.",
		},
		{
			name: "AssignedOnOnlyOnePath",
			src: `
				class C {
					x: number,
					constructor(&mut self, cond: boolean) {
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
					constructor(&mut self, x: number) {
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
					log(&self) -> number { return self.x },
					constructor(&mut self, x: number) {
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
					log(&self) -> number { return self.x },
					constructor(&mut self) {
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

// TestInferMethodOverloadResolvesByArg asserts method overload resolution (E1): a method
// declared with two signatures resolves the arm whose parameter matches the argument, the
// method analogue of a direct overloaded-function call. c.f(1) picks the number arm and
// c.f("hi") the string arm, so the two reads take the two arms' distinct return types.
func TestInferMethodOverloadResolvesByArg(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			f(&self, x: number) -> number { return x },
			f(&self, x: string) -> string { return x },
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
			g(&self, p: {x: number}) -> number { return p.x },
			g(&self, p: {x: number, y: number}) -> string { return "wide" },
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
			f(&self, x: number) -> number { return x },
			f(&self, x: string) -> string { return x },
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
			f(&self, x: number) -> number { return x },
			f(&self, x: string) -> string { return x },
			run(&self, x) -> number { return self.f(x) },
		}
		val c = C(0)
		val r = c.run(1)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"],
		"an unconstrained argument defers to declaration-order first-match, pinning x to the first arm")
}

// TestInferMethodOverloadMixedReceiverRejected asserts the receiver-mutability uniformity rule
// for overloaded methods: arms that disagree on their `self` receiver are rejected at
// declaration. Overload resolution dispatches on the value arguments, and the
// receiver-mutability check reads only the first arm, so a `mut self` arm reached from a
// plain-`self` body would otherwise dispatch to a mutable receiver unchecked.
func TestInferMethodOverloadMixedReceiverRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			n: number,
			f(&self, x: number) -> number { return self.n },
			f(&mut self, x: string) -> string { return x },
			peek(&self) -> string { return self.f("hi") },
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"Overloaded method 'f' must use the same `self` receiver in every arm.",
		errs[0].Message())
}

// TestInferMethodOverloadUniformMutReceiverAccepted is the passing companion: an overload
// set whose arms all declare `mut self` is well-formed, resolves by argument, and calls
// through a mutable receiver.
func TestInferMethodOverloadUniformMutReceiverAccepted(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			n: number,
			f(&mut self, x: number) -> number { return x },
			f(&mut self, x: string) -> string { return x },
			run(&mut self) -> string { return self.f("hi") },
		}
		val mut c = C(0)
		val r = c.f(1)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
}

// A method's own `<T>` binder resolves through resolveTypeParams, the path a generic
// function declaration and a generic function annotation already take, and is
// instantiated per call so two calls do not share a var.
//
// The class's own binder is a separate mechanism and enforces independently, which the
// last case shows.
func TestInferClassMethodTypeParams(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "InstanceMethodBinderEnforcesItsBound",
			src: `
				class C {
					pick<T: string>(&self, x: T) -> T { return x },
				}
				val c = C()
				val r = c.pick(1)
			`,
			want: []string{`cannot constrain 1 <: string`},
		},
		{
			name: "StaticMethodBinderEnforcesItsBound",
			src: `
				class C {
					static pick<T: string>(x: T) -> T { return x },
				}
				val r = C.pick(1)
			`,
			want: []string{`cannot constrain 1 <: string`},
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

// A method's `<T: string>` enforces its bound at the call the same way a generic
// function's does, since both route their binder through resolveTypeParams.
func TestInferClassMethodTypeParamBounds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ArgumentInsideBound",
			src: `
				class C {
					pick<T: string>(&self, x: T) -> T { return x },
				}
				val c = C()
				val r = c.pick("a")
			`,
		},
		{
			name: "ArgumentOutsideBound",
			src: `
				class C {
					pick<T: string>(&self, x: T) -> T { return x },
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
					pick<T>(&self, x: T) -> T { return x },
				}
				val c = C()
				val r = c.pick(1)
			`,
		},
		{
			// A bound naming the class's own parameter resolves, since the method's scope
			// sits under the class's. Enforcing it at the instance's argument is a further
			// step: a call reads the bound off the binder's variable, which substitution
			// cannot rewrite, so `T: U` on a `C<number>` admits any argument. #1547.
			name: "ClassParameterBoundResolves",
			src: `
				class C<U> {
					v: U,
					pick<T: U>(&self, x: T) -> T { return x },
				}
				val c: C<number> = C(1)
				val r = c.pick(2)
			`,
		},
		{
			// A method binder may shadow the class's parameter of the same name. The
			// inner binder wins inside the signature, which is what its child scope
			// gives, and the class's own parameter is untouched outside it.
			name: "MethodBinderShadowsAClassParameter",
			src: `
				class C<T> {
					v: T,
					pick<T>(&self, x: T) -> T { return x },
				}
				val c: C<number> = C(1)
				val r = c.pick("a")
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
}

// Two calls to one generic method instantiate independently rather than sharing a var,
// which is what makes the binder worth resolving at all.
func TestInferClassMethodTypeParamsInstantiatePerCall(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			pick<T: string>(&self, x: T) -> T { return x },
		}
		val c = C()
		val a = c.pick("a")
		val b = c.pick("b")
	`)
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t, `"a"`, values["a"])
	require.Equal(t, `"b"`, values["b"])
}

// A body that narrows its own type parameter is reported in the body, for a method as for a
// generic function. Calling `f` inside `g` freshens `f`'s binder to a variable carrying
// `string`, and `U` is unbounded, so `f(u)` reports. The call `g(1)` is checked against
// the declared bound alone and reports nothing more.
func TestInferMethodBodyInferredBoundMatchesTheFunctionForm(t *testing.T) {
	const callee = "fn f<A: string>(a: A) -> A { return a }\n"
	const want = "cannot constrain U <: string"

	_, _, fnErrs := inferSource(t, callee+`
		fn g<U>(u: U) -> U { return f(u) }
		val r = g(1)
	`)
	_, _, methodErrs := inferSource(t, callee+`
		class C {
			g<U>(&self, u: U) -> U { return f(u) },
		}
		val c = C()
		val r = c.g(1)
	`)
	require.Equal(t, []string{want}, errorMessagesOf(fnErrs))
	require.Equal(t, []string{want}, errorMessagesOf(methodErrs))
}

// A bound naming a SIBLING binder is recorded and propagated, and still admits the call
// below, for a method exactly as for a generic function. `B: A` becomes `B <: A` on the
// binder's variable, so constraining `"y" <: B` propagates to `"y" <: A`. `A` is itself
// an inference variable with no upper bound, so the argument joins the `1` that `a`
// contributed and `A` solves to `1 | "y"`. The bound has nothing to fail against.
// Bounding `A` in turn, as `<A: number, B: A>`, gives that propagation something to
// reach. The second pair below writes it that way and the argument is rejected.
//
// The two forms are written side by side here so the parity is what the test asserts
// rather than the behavior of either alone.
func TestInferMethodSiblingBoundMatchesTheFunctionForm(t *testing.T) {
	_, _, fnErrs := inferSource(t, `
		fn pair<A, B: A>(a: A, b: B) -> A { return a }
		val r = pair(1, "y")
	`)
	_, _, methodErrs := inferSource(t, `
		class C {
			pair<A, B: A>(&self, a: A, b: B) -> A { return a },
		}
		val c = C()
		val r = c.pair(1, "y")
	`)
	require.Empty(t, messagesWithSpan(t, fnErrs))
	require.Equal(t, messagesWithSpan(t, fnErrs), messagesWithSpan(t, methodErrs))

	_, _, boundedFnErrs := inferSource(t, `
		fn pair<A: number, B: A>(a: A, b: B) -> A { return a }
		val r = pair(1, "y")
	`)
	_, _, boundedMethodErrs := inferSource(t, `
		class C {
			pair<A: number, B: A>(&self, a: A, b: B) -> A { return a },
		}
		val c = C()
		val r = c.pair(1, "y")
	`)
	want := []string{`cannot constrain "y" <: number`}
	require.Equal(t, want, errorMessagesOf(boundedFnErrs))
	require.Equal(t, want, errorMessagesOf(boundedMethodErrs))
}

// The signature a caller reads carries the binder under its written name and the bound
// the source declared. linkMemberSig records `T <: stubReturn` for a method returning
// `T`, so the binder's variable also collects the stub's return variable; the freeze
// drops it, which is what keeps `<T>` from rendering as `<T: T0>` and stops that stray
// variable from being quantified onto the class value as a phantom parameter.
func TestInferClassMethodTypeParamRendering(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name: "an instance method's bound",
			src: `
				class C { pick<T: string>(&self, x: T) -> T { return x }, }
				fn probe(c: C) { return c.pick }`,
			binding: "probe",
			want:    "fn (c: C) -> fn <T: string>(x: T) -> T",
		},
		{
			name: "an unbounded binder",
			src: `
				class C { pick<T>(&self, x: T) -> T { return x }, }
				fn probe(c: C) { return c.pick }`,
			binding: "probe",
			want:    "fn (c: C) -> fn <T>(x: T) -> T",
		},
		{
			name: "a bound naming the class's parameter",
			src: `
				class C<U> { v: U, pick<T: U>(&self, x: T) -> T { return x }, }
				fn probe(c: C<number>) { return c.pick }`,
			binding: "probe",
			want:    "fn (c: C<number>) -> fn <T: number>(x: T) -> T",
		},
		{
			name: "a signature mixing both binders",
			src: `
				class C<U> { v: U, map<T>(&self, x: T) -> [T, U] { return [x, self.v] }, }
				fn probe(c: C<number>) { return c.map }`,
			binding: "probe",
			want:    "fn (c: C<number>) -> fn <T>(x: T) -> [T, number]",
		},
		{
			// The class value carries the static member, and no phantom binder beside it.
			name:    "a static method on the class value",
			src:     `class C { static pick<T: string>(x: T) -> T { return x }, }`,
			binding: "C",
			want:    "{new () -> C, pick<T: string>(x: T) -> T}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// An overloaded method's arms each carry their own binder, and each is instantiated per
// call. Wrapping an arm in a MonoScheme without instantiating leaves the binder's variable
// shared, so the first call binds it and every later one is weighed against that binding.
func TestInferOverloadedGenericMethod(t *testing.T) {
	t.Run("one generic arm beside a plain one", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class C {
				pick<T: string>(&self, x: T) -> T { return x },
				pick(&self, a: number, b: number) -> number { return a },
			}
			val c = C()
			val a = c.pick("s")
			val b = c.pick(1, 2)
		`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, `"s"`, values["a"])
		require.Equal(t, "number", values["b"])
	})
	t.Run("two generic arms", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class C {
				pick<T: string>(&self, x: T) -> T { return x },
				pick<T: number>(&self, x: T, y: T) -> T { return x },
			}
			val c = C()
			val a = c.pick("s")
			val b = c.pick(1, 2)
		`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, `"s"`, values["a"])
		require.Equal(t, "1 | 2", values["b"])
	})
	t.Run("a call matching no arm", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				pick<T: string>(&self, x: T) -> T { return x },
				pick(&self, a: number, b: number) -> number { return a },
			}
			val c = C()
			val r = c.pick(1)
		`)
		require.Equal(t,
			[]string{"7:12-7:21: No matching overload for this call\n" +
				"  fn <T: string>(x: T) -> T\n" +
				"  fn (a: number, b: number) -> number"},
			messagesWithSpan(t, errs))
	})
}

// A binder nested inside a member's type is kept through the freeze as well as the
// member's own. A parameter typed `fn <V>(x: V) -> V` is a rank-2 callback, and
// coalescing `V` to a non-variable trips the guard in acceptTypeParamVar, which is a
// panic rather than a diagnostic.
func TestInferClassKeepsANestedCallbackBinder(t *testing.T) {
	values, _, errs := inferSource(t, `class C {
	apply(&self, g: fn <V>(x: V) -> V, y: number) -> number { return g(y) },
}
fn probe(c: C) { return c.apply }`)
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t,
		"fn (c: C) -> fn (g: fn <V>(x: V) -> V, y: number) -> number",
		values["probe"])
}

// A sibling call reads the member's signature stub, which is built before any body is
// inferred. A generic member is linked into its stub through one instantiation of its
// binder, so the declared bound reaches a sibling whichever order the two are written
// in. Linking the binder itself would instead record the stub's variables on it.
func TestInferGenericMethodBoundReachesASiblingCall(t *testing.T) {
	const want = "cannot constrain 1 <: string"
	t.Run("the callee is declared after the caller", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				caller(&self) -> number { return self.pick(1) },
				pick<T: string>(&self, x: T) -> T { return x },
			}
		`)
		require.Contains(t, errorMessagesOf(errs), want)
	})
	t.Run("the callee is declared before the caller", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				pick<T: string>(&self, x: T) -> T { return x },
				caller(&self) -> number { return self.pick(1) },
			}
		`)
		require.Contains(t, errorMessagesOf(errs), want)
	})
	t.Run("a sibling call inside the bound is accepted", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				caller(&self) -> string { return self.pick("a") },
				pick<T: string>(&self, x: T) -> T { return x },
			}
		`)
		require.Empty(t, messagesWithSpan(t, errs))
	})
}

// A getter and a setter cannot quantify a parameter of their own. A getter takes no
// argument to infer one from, and a setter's single argument is the property's own type,
// which the class fixes. Both report rather than accepting a binder nothing instantiates.
func TestInferAccessorTypeParamsGated(t *testing.T) {
	t.Run("a getter", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				get value<T>(&self) -> number { return 1 },
			}
		`)
		require.Equal(t, []string{"Unsupported: TypeParam"}, errorMessagesOf(errs))
	})
	t.Run("a setter", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				x: number,
				set value<T>(&mut self, v: T) { self.x = 1 },
			}
		`)
		require.Equal(t,
			[]string{"Unsupported: TypeParam", "cannot find type `T`"},
			errorMessagesOf(errs))
	})
}

// A constructor's own binder stays gated. The class's parameters are what a
// constructor's arguments infer, so a binder of its own has nothing to quantify, and
// TypeScript rejects one for the same reason.
func TestInferClassConstructorTypeParamsGated(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			x: number,
			constructor<T>(&mut self, x: number) { self.x = x },
		}
	`)
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Message())
	}
	require.Equal(t, []string{"Unsupported: TypeParam"}, msgs)
}

// A class may declare more than one constructor. Every arm binds under the one
// ConstructorElem the class value carries, and a call resolves against the set the way a call
// to an overloaded method does. `Array` is the case that needs it: it declares both the
// length form and the element-list form, which is why `Array(3)` and `Array(1, 2, 3)` mean
// different things.
func TestInferClassConstructorOverloads(t *testing.T) {
	const decl = "declare class Box {\n" +
		"  constructor(&mut self, n: number),\n" +
		"  constructor(&mut self, a: string, b: string),\n" +
		"}\n"
	const noMatch = "No matching overload for this call\n" +
		"  fn (n: number) -> Box\n" +
		"  fn (a: string, b: string) -> Box"
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "SelectsTheFirstArm",
			src:  decl + `val v = Box(1)`,
		},
		{
			name: "SelectsTheSecondArm",
			src:  decl + `val v = Box("x", "y")`,
		},
		{
			// Neither arm accepts the argument, so the report names the whole set rather than
			// one arm's parameter.
			name: "RejectsAgainstTheWholeSet",
			src:  decl + `val v = Box(true)`,
			want: noMatch,
		},
		{
			name: "RejectsAnArityNoArmAccepts",
			src:  decl + `val v = Box(1, 2, 3)`,
			want: noMatch,
		},
		{
			// The arity matches the second arm and the element does not, which is still a
			// no-match rather than a per-argument mismatch against that arm.
			name: "RejectsAWrongElementAtAMatchingArity",
			src:  decl + `val v = Box("x", 2)`,
			want: noMatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.want == "" {
				require.Empty(t, errs)
				require.Equal(t, "Box", values["v"])
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// A class value renders every constructor it declares, joined the way an overloaded method's
// arms are, so the set is visible at a hover rather than collapsed to one arm.
func TestInferClassConstructorOverloadsRender(t *testing.T) {
	src := "declare class Box {\n" +
		"  constructor(&mut self, n: number),\n" +
		"  constructor(&mut self, a: string, b: string),\n" +
		"}"
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{new (n: number) -> Box; new (a: string, b: string) -> Box}", values["Box"])
}

// A class declaring one constructor keeps the single-signature shape, so the arity lints and
// the per-argument blame a direct call gives are unaffected by the overload path above.
func TestInferClassSingleConstructorUnaffected(t *testing.T) {
	src := "declare class Box {\n  constructor(&mut self, n: number),\n}\nval v = Box(\"x\")"
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "x" <: number`, errs[0].Message())
}

// An overload arm whose last parameter is an `Array<E>` rest slot checks every argument the
// slot gathers against E. Resolution reads the arms one at a time rather than through the
// callee <: callShape constraint, so the scatter rule constrain applies to a single-signature
// callee is not what runs here and the element check has to be made per arm.
func TestInferClassConstructorOverloadWithARestArm(t *testing.T) {
	const decl = "declare class Arr {\n" +
		"  constructor(&mut self, arrayLength: number),\n" +
		"  constructor(&mut self, ...items: mut Array<string>),\n" +
		"}\n"
	const noMatch = "No matching overload for this call\n" +
		"  fn (arrayLength: number) -> Arr\n" +
		"  fn (...items: mut Array<string>) -> Arr"
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "SelectsTheLengthArm",
			src:  decl + `val v = Arr(3)`,
		},
		{
			name: "SelectsTheElementArm",
			src:  decl + `val v = Arr("x", "y")`,
		},
		{
			// Zero arguments fill the slot, so the element arm still accepts.
			name: "AcceptsNoArguments",
			src:  decl + `val v = Arr()`,
		},
		{
			// The gap this case asserts. An arm accepted with its element unchecked takes any
			// argument at all past the first, so the call resolves clean.
			name: "RejectsAWrongElement",
			src:  decl + `val v = Arr("x", true)`,
			want: noMatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.want == "" {
				require.Empty(t, errs)
				require.Equal(t, "Arr", values["v"])
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// `Self` inside a class body names the class's own instance type, which is what a
// builder-style return needs: a method handing back the receiver's own type writes `-> Self`
// rather than repeating the class name and its arguments. `std/prelude.esc` writes
// `fill(&mut self, value: T, start?: number, end?: number) -> Self`.
func TestInferClassSelfType(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		which string
		want  string
	}{
		{
			// The handle carries the class's own type-parameter vars as its arguments, so
			// `Self` inside `class Box<T>` is `Box<T>` and an instance's argument substitutes
			// for `T` the way it does through any other reference to the class.
			name:  "MethodReturnOnAGenericClass",
			src:   "declare class Box<T> {\n  fill(&mut self, value: T) -> Self,\n}\nfn g(b: mut Box<number>) -> Box<number> { return b.fill(1) }",
			which: "g",
			want:  "fn (b: mut Box<number>) -> Box<number>",
		},
		{
			name:  "MethodReturnOnANonGenericClass",
			src:   "declare class Box {\n  copy(&self) -> Self,\n}\nfn g(b: Box) -> Box { return b.copy() }",
			which: "g",
			want:  "fn (b: Box) -> Box",
		},
		{
			// A `Self` nested inside a CALLBACK parameter is contravariant twice, so it is
			// covariant overall and stays legal. This is the position 176 of the tree's 242
			// occurrences take, and it resolves at the receiver's class the way a return does.
			name: "CallbackParameter",
			src: "declare class Box {\n  each(&self, cb: fn (arr: Self) -> boolean) -> number,\n}\n" +
				"fn g(b: Box) { return b.each }",
			which: "g",
			want:  "fn (b: Box) -> fn (cb: fn (arr: Box) -> boolean) -> number",
		},
		{
			// The binding covers the fields as well as the member signatures, so a
			// self-referential field reaches it.
			name:  "FieldAnnotation",
			src:   "declare class Node {\n  next: Self,\n}",
			which: "Node",
			want:  "{new (next: Node) -> Node}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values[tt.which])
		})
	}
}

// A DIRECT parameter is the one position `Self` may not take. It is contravariant, so the
// polymorphic reading breaks `B <: A`: an A-typed holder may call `a.eq(someA)` on a value that
// is really a B, whose `eq` demands a B. TypeScript has this hole and method bivariance hides
// it. The committed tree writes no such `Self`, so rejecting costs nothing.
//
// The member keeps its shape after the report, so `a.eq(b)` draws no second diagnostic
// cascading from this one.
func TestInferSelfTypeInAParameterRejected(t *testing.T) {
	const msg = "2:20-2:24: \"Self\" cannot be a method parameter's type. It means the receiver's class, " +
		"so a subclass would accept fewer arguments than its superclass. Write the class by name instead."
	t.Run("a direct parameter is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t,
			"declare class Box {\n  eq(&self, other: Self) -> boolean,\n}\n"+
				"fn g(a: Box, b: Box) -> boolean { return a.eq(b) }")
		require.Equal(t, []string{msg}, messagesWithSpan(t, errs))
	})
	t.Run("writing the class by name is the way to say it", func(t *testing.T) {
		values, _, errs := inferSource(t,
			"declare class Box {\n  eq(&self, other: Box) -> boolean,\n}\n"+
				"fn g(a: Box, b: Box) -> boolean { return a.eq(b) }")
		require.Empty(t, errs)
		require.Equal(t, "fn (a: Box, b: Box) -> boolean", values["g"])
	})
	t.Run("a callback parameter is not a parameter position for this rule", func(t *testing.T) {
		_, _, errs := inferSource(t,
			"declare class Box {\n  each(&self, cb: fn (arr: Self) -> boolean) -> number,\n}")
		require.Empty(t, errs)
	})
}

// TestInferBorrowedReceiverInACallback covers a callback parameter that borrows the receiver,
// written `&Self` or with the class by name. An unannotated callback parameter takes the borrow
// from the signature, so the callback reads through it and cannot keep it. `&Self` reads at
// the receiver's own class, a subclass for a subclass instance.
func TestInferBorrowedReceiverInACallback(t *testing.T) {
	t.Parallel()

	const coll = `
		declare class Coll<T> {
			each(&self, cb: fn (v: T, arr: &Self) -> unknown) -> unknown,
			push(&mut self, v: T) -> number,
			readonly length: number,
		}
	`
	const named = `
		declare class Named<T> {
			each(&self, cb: fn (v: T, arr: &Named<T>) -> unknown) -> unknown,
			push(&mut self, v: T) -> number,
			readonly length: number,
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a callback reads through a borrowed self",
			src: coll + `
				fn f(xs: &Coll<number>) { xs.each(fn (v, arr) { return arr.length }) }
			`,
		},
		{
			name: "a borrowed self reads at the subclass",
			src: coll + `
				declare class Sub extends Coll<number> {
					constructor(&mut self),
					readonly extra: number,
				}
				fn f(s: &Sub) { s.each(fn (v, arr) { return arr.extra }) }
			`,
		},
		{
			name: "a callback cannot keep a borrowed self",
			src: coll + `
				fn f(s: &Coll<number>) -> Coll<number> | undefined {
					var keep: Coll<number> | undefined = undefined
					s.each(fn (v, arr) {
						keep = arr
						return 0
					})
					return keep
				}
			`,
			want: []string{
				"cannot use borrowed &Coll<number> as owned undefined | Coll<number>",
			},
		},
		{
			// `x.inner` reads as a borrow bounded by `x`, so it cannot be kept either.
			name: "a callback cannot keep a field it reads through a borrow",
			src: `
				class Box { v: number }
				class C {
					readonly inner: mut Box,
					each(&self, cb: fn (c: &C) -> undefined) -> undefined { return undefined },
				}
				fn f(c: &C, keep: &mut Array<mut Box>) {
					c.each(fn (x) {
						keep.push(x.inner)
						return undefined
					})
				}
			`,
			want: []string{"cannot use borrowed &mut Box as owned mut Box"},
		},
		{
			name: "a callback reads a field's field through a borrow",
			src: `
				class Box { v: number }
				class C {
					readonly inner: mut Box,
					readonly n: number,
					each(&self, cb: fn (c: &C) -> undefined) -> undefined { return undefined },
				}
				fn f(c: &C) -> number {
					var total = 0
					c.each(fn (x) {
						total = x.n + x.inner.v
						return undefined
					})
					return total
				}
			`,
		},
		{
			name: "a callback reads a field through a borrowed union",
			src: `
				class A { readonly n: number }
				class B { readonly n: number }
				declare fn each(cb: fn (x: &(A | B)) -> unknown) -> unknown
				fn f() { each(fn (x) { return x.n }) }
			`,
		},
		{
			name: "a callback cannot keep a nullable field it reads through a borrow",
			src: `
				class Box { v: number }
				class C {
					readonly inner: mut Box | null,
					each(&self, cb: fn (c: &C) -> undefined) -> undefined { return undefined },
				}
				fn f(c: &C, keep: &mut Array<mut Box | null>) {
					c.each(fn (x) {
						keep.push(x.inner)
						return undefined
					})
				}
			`,
			want: []string{"cannot constrain mut Box <: mut Box | null"},
		},
		{
			// `T` is still a variable when `x.inner` is read, but `c` has already given it
			// the lower bound `C`. The field is read off `C` as a borrow bounded by `x`.
			name: "a callback cannot keep a field it reads through a borrowed variable",
			src: `
				class Box { v: number }
				class C { readonly inner: mut Box }
				declare fn each<T>(v: &T, cb: fn (x: &T) -> undefined) -> undefined
				fn f(c: &C, keep: &mut Array<mut Box>) {
					each(c, fn (x) {
						keep.push(x.inner)
						return undefined
					})
				}
			`,
			want: []string{"cannot use borrowed &mut Box as owned mut Box"},
		},
		{
			name: "a callback reads a field's field through a borrowed variable",
			src: `
				class Box { v: number }
				class C { readonly inner: mut Box }
				declare fn each<T>(v: &T, cb: fn (x: &T) -> undefined) -> undefined
				fn f(c: &C) -> number {
					var total = 0
					each(c, fn (x) {
						total = x.inner.v
						return undefined
					})
					return total
				}
			`,
		},
		{
			// The callback is inferred before `c` reaches `T`. The read waits for that lower
			// bound and then reads off it.
			name: "a callback reads through a borrowed variable bound after it",
			src: `
				class Box { v: number }
				class C { readonly inner: mut Box }
				declare fn each<T>(cb: fn (x: &T) -> undefined, v: &T) -> undefined
				fn f(c: &C) -> number {
					var total = 0
					each(fn (x) {
						total = x.inner.v
						return undefined
					}, c)
					return total
				}
			`,
		},
		{
			name: "a callback cannot keep a field it reads through a borrowed variable bound after it",
			src: `
				class Box { v: number }
				class C { readonly inner: mut Box }
				declare fn each<T>(cb: fn (x: &T) -> undefined, v: &T) -> undefined
				fn f(c: &C, keep: &mut Array<mut Box>) {
					each(fn (x) {
						keep.push(x.inner)
						return undefined
					}, c)
				}
			`,
			want: []string{"cannot use borrowed &mut Box as owned mut Box"},
		},
		{
			// `a` and `b` each give `T` a lower bound, so `x.n` reads as `number | string`
			// and does not fit `total`.
			name: "a read through a borrowed variable joins every lower bound",
			src: `
				class A { readonly n: number }
				class B { readonly n: string }
				declare fn each<T>(a: &T, cb: fn (x: &T) -> undefined, b: &T) -> undefined
				fn f(a: &A, b: &B) -> number {
					var total = 0
					each(a, fn (x) {
						total = x.n
						return undefined
					}, b)
					return total
				}
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// `T`'s lower bound is the borrow `&C`, which is read through its own lifetime.
			name: "a read through a borrowed variable whose lower bound is a borrow",
			src: `
				class C { readonly n: number }
				declare fn each<T>(v: T, cb: fn (x: &T) -> undefined) -> undefined
				fn f(c: &C) {
					each(c, fn (x) {
						val b: boolean = x.n
						return undefined
					})
				}
			`,
			want: []string{"cannot constrain number <: boolean"},
		},
		{
			name: "a read through a borrowed variable whose lower bound may be null",
			src: `
				class C { readonly n: number }
				declare fn each<T>(v: &T, cb: fn (x: &T) -> undefined) -> undefined
				fn f(c: &(C | null)) {
					each(c, fn (x) {
						val b: number = x.n
						return undefined
					})
				}
			`,
			want: []string{"cannot constrain null <: object"},
		},
		{
			// The missing property is reported once, by the shape `T` is checked against.
			name: "a callback reads a missing field through a borrowed variable",
			src: `
				class C { readonly n: number }
				declare fn each<T>(v: &T, cb: fn (x: &T) -> undefined) -> undefined
				fn f(c: &C) {
					each(c, fn (x) {
						val m = x.missing
						return undefined
					})
				}
			`,
			want: []string{"object is missing property: missing"},
		},
		{
			// An immutable borrow of `C` cannot lend the write its `&'a mut` field holds.
			name: "a callback cannot write through a mutable borrow it reads through an immutable one",
			src: `
				class Box { v: number }
				class C<'a> { readonly m: &'a mut Box }
				declare fn each<'a>(c: &C<'a>, cb: fn (x: &C<'a>) -> undefined) -> undefined
				fn f(c: &C<'static>) {
					each(c, fn (x) {
						val p = x.m
						p.v = 5
						return undefined
					})
				}
			`,
			want: []string{"cannot constrain immutable Box <: mutable object"},
		},
		{
			name: "a direct parameter cannot borrow Self",
			src:  `declare class Box { eq(&self, other: &Self) -> boolean }`,
			want: []string{
				"\"Self\" cannot be a method parameter's type. It means the receiver's class, so a " +
					"subclass would accept fewer arguments than its superclass. Write the class by name instead.",
			},
		},
		{
			name: "a callback reads through a borrow written by name",
			src: named + `
				fn f(xs: &Named<number>) { xs.each(fn (v, arr) { return arr.length }) }
			`,
		},
		{
			name: "a callback cannot call a mutating method through a borrow",
			src: named + `
				fn f(xs: &Named<number>) { xs.each(fn (v, arr) { return arr.push(1) }) }
			`,
			want: []string{"object is missing property: push"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// The override check reads an inherited member as the declaring class offers it TO the class
// redeclaring it, so a `-> Self` member is compared at that class however many levels up it was
// declared. The walk climbs one superclass at a time, and reading `Self` at the class the walk
// currently sits on rather than the one it started from would compare against an intermediate:
// for `C extends B extends A`, against B instead of C.
//
// That is what makes the widening case below the load-bearing one. `-> B` on C is compared
// against `-> C` and rejected; under the intermediate reading it would be compared against
// `-> B` and pass.
func TestInferSelfTypeInAnOverrideCheck(t *testing.T) {
	chain := func(redeclared string) string {
		return "declare class A {\n  m(&self) -> Self,\n}\n" +
			"declare class B extends A {\n  constructor(&mut self),\n}\n" +
			"declare class C extends B {\n  constructor(&mut self),\n  m(&self) -> " + redeclared + ",\n}"
	}
	t.Run("redeclaring at the receiving class is accepted", func(t *testing.T) {
		_, _, errs := inferSource(t, chain("C"))
		require.Empty(t, errs)
	})
	t.Run("redeclaring at an intermediate class is a widening", func(t *testing.T) {
		_, _, errs := inferSource(t, chain("B"))
		require.Equal(t, []string{
			"9:3-9:16: class `C` redeclares inherited member `m` with type `fn () -> B`, " +
				"which is not compatible with `fn () -> C` declared by `A`",
		}, messagesWithSpan(t, errs))
	})
	t.Run("one level behaves the same way", func(t *testing.T) {
		_, _, errs := inferSource(t,
			"declare class A {\n  m(&self) -> Self,\n}\n"+
				"declare class B extends A {\n  constructor(&mut self),\n  m(&self) -> A,\n}")
		require.Equal(t, []string{
			"6:3-6:16: class `B` redeclares inherited member `m` with type `fn () -> A`, " +
				"which is not compatible with `fn () -> B` declared by `A`",
		}, messagesWithSpan(t, errs))
	})
}

// `Self` is bound by a class body and nowhere else, so a plain function writing it finds the
// name unbound.
func TestInferSelfTypeOutsideAClassBody(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(x: Self) -> number { return 1 }`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot find type `Self`", errs[0].Message())
}

// A `-> Self` return is an output position, so the immutable view measures the class's type
// parameter covariant and `Box<5>` widens to `Box<number>`. The receiver holds the same
// handle and stays excluded from the variance walk, which is what keeps the parameter from
// collapsing to invariant.
func TestInferSelfTypeVariance(t *testing.T) {
	src := "declare class Box<T> {\n  fill(&mut self, value: T) -> Self,\n}\nfn g(b: Box<5>) -> Box<number> { return b }"
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// `Self` in an INHERITED member reads at the class that declared it, not at the subclass
// reaching it. `me` is declared on A returning `Self`, so `b.me()` on a `B` yields an `A`.
//
// #1520 carries the polymorphic reading, where it resolves at the receiving subclass the way
// TypeScript's `this` type does. That needs `Self` kept distinct in the stored signature and
// substituted by the receiver's class at member lookup, which no rule does today.
//
// Both halves are needed to observe the answer. An annotated `-> A` return would hold whether
// the call yields `A` or `B`, since `B <: A` nominally, so the unannotated case is what reads
// the type back. The annotated `-> B` case is what fails once the substitution lands, which is
// the signal that this comment is stale.
func TestInferSelfTypeInAnInheritedMember(t *testing.T) {
	const decl = "declare class A {\n  me(&self) -> Self,\n}\n" +
		"declare class B extends A {\n  constructor(&mut self),\n  extra(&self) -> number,\n}\n"

	t.Run("InfersTheReceivingClass", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+"fn g(b: B) { return b.me() }")
		require.Empty(t, errs)
		require.Equal(t, "fn (b: B) -> B", values["g"])
	})

	t.Run("AcceptsTheSubclassReturn", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+"fn g(b: B) -> B { return b.me() }")
		require.Empty(t, errs)
	})

	t.Run("TheDeclaringClassStillReadsAsItself", func(t *testing.T) {
		// A member declared `-> A` literally is NOT polymorphic, which is the distinction
		// `Self` exists to draw. Reaching it through a B still yields A.
		const literal = "declare class A {\n  me(&self) -> A,\n}\n" +
			"declare class B extends A {\n  constructor(&mut self),\n}\n"
		values, _, errs := inferSource(t, literal+"fn g(b: B) { return b.me() }")
		require.Empty(t, errs)
		require.Equal(t, "fn (b: B) -> A", values["g"])
	})

	t.Run("ReadingItOnTheDeclaringClassYieldsThatClass", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+"fn g(a: A) { return a.me() }")
		require.Empty(t, errs)
		require.Equal(t, "fn (a: A) -> A", values["g"])
	})

	t.Run("ResolvesAtTheReceiverTwoLevelsDown", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+
			"declare class C extends B {\n  constructor(&mut self),\n}\nfn g(c: C) { return c.me() }")
		require.Empty(t, errs)
		require.Equal(t, "fn (c: C) -> C", values["g"])
	})

	t.Run("KeepsTheSubclassTypeArguments", func(t *testing.T) {
		values, _, errs := inferSource(t,
			"declare class Box<T> {\n  dup(&self) -> Self,\n}\n"+
				"declare class Pair<T> extends Box<T> {\n  constructor(&mut self),\n}\n"+
				"fn g(p: Pair<string>) { return p.dup() }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: Pair<string>) -> Pair<string>", values["g"])
	})

	t.Run("ABuilderChainSurvivesInheritance", func(t *testing.T) {
		// This is what polymorphic `Self` buys. Each inherited step yields the subclass, so the
		// chain can end on a member only the subclass declares. Under the declaring-class
		// reading `q.a()` would be a Q and `.r()` would not resolve.
		_, _, errs := inferSource(t,
			"declare class Q {\n  a(&self) -> Self,\n  b(&self) -> Self,\n}\n"+
				"declare class R extends Q {\n  constructor(&mut self),\n  r(&self) -> number,\n}\n"+
				"fn g(r: R) -> number { return r.a().b().r() }")
		require.Empty(t, errs)
	})
}

// No type declaration of any kind may be named `Self`. Every class body binds that name to its
// own instance type, so a declaration of that name is unreachable from inside any class body and
// reads as the shorthand everywhere else.
//
// The declaration is still bound after the report, which StillBoundAfterTheReport shows: the
// class registers, and a reference to it resolves and draws its own arity diagnostic rather than
// cascading from the name. That recovery is why resolveScopedTypeRef still gates its `Self`
// shortcut on the enclosing class's own handle instead of on the name.
func TestInferTypeNamedSelf(t *testing.T) {
	const tail = " cannot be named \"Self\"; the name is bound in every class body to " +
		"that class's own instance type"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Class",
			src:  "declare class Self {\n  x: number,\n}",
			want: []string{"a class" + tail},
		},
		{
			name: "Interface",
			src:  "declare interface Self {\n  x: number,\n}",
			want: []string{"an interface" + tail},
		},
		{
			// Interface declarations of one name merge into a single binding, so the report is
			// against the first rather than once per declaration.
			name: "MergedInterfaces",
			src:  "declare interface Self {\n  x: number,\n}\ndeclare interface Self {\n  y: number,\n}",
			want: []string{"an interface" + tail},
		},
		{
			name: "TypeAlias",
			src:  "type Self = number",
			want: []string{"a type alias" + tail},
		},
		{
			name: "Enum",
			src:  "enum Self { A, B }",
			want: []string{"an enum" + tail},
		},
		{
			name: "InANamespace",
			src:  "namespace Geo {\n  declare class Self {\n    x: number,\n  }\n}",
			want: []string{"a class" + tail},
		},
		{
			name: "StillBoundAfterTheReport",
			src:  "declare class Self<T> {\n  v: T,\n}\nfn f(p: Self) -> number { return 1 }",
			want: []string{"a class" + tail, "class `Self` expects 1 type argument but got 0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, len(tt.want))
			for i, want := range tt.want {
				require.Equal(t, want, errs[i].Message())
			}
		})
	}
}

// A lifetime argument written on `Self` is counted against what the class declares, the same
// as one written on a reference through the class's own name. The shorthand resolves to the
// enclosing handle directly, so without the guard it would accept any `<'…>` list and drop it.
func TestInferSelfTypeRejectsLifetimeArgs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "NoneDeclared",
			src:  "declare class Box<T> {\n  m(&self) -> Self<'a>,\n}",
			want: "class `Self` expects 0 lifetime arguments but got 1",
		},
		{
			name: "MoreThanDeclared",
			src:  "declare class Box<'a, T> {\n  m(&self) -> Self<'a, 'a>,\n}",
			want: "class `Self` expects 1 lifetime arguments but got 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// TestInferClassReadsLaterClassMember covers a method body reading a member of a class
// declared later in the file. A reference to the later class depends only on its type
// key, so the members it declares have to be readable before its own bodies are walked.
func TestInferClassReadsLaterClassMember(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "FieldRead",
			src: `
				class A { readonly b: B, get(&self) -> number { return self.b.n } }
				class B { readonly n: number }
			`,
		},
		{
			name: "MethodCall",
			src: `
				class A { readonly b: B, get(&self) -> number { return self.b.m() } }
				class B { m(&self) -> number { return 1 } }
			`,
		},
		{
			name: "GetterRead",
			src: `
				class A { readonly b: B, get(&self) -> number { return self.b.n } }
				class B { get n(&self) -> number { return 1 } }
			`,
		},
		{
			name: "ParamRead",
			src: `
				class A { get(&self, b: B) -> number { return b.n } }
				class B { readonly n: number }
			`,
		},
		{
			name: "MutualReads",
			src: `
				class A { readonly b: B, n: number, get(&self) -> number { return self.b.a.n } }
				class B { readonly a: A, m(&self) -> number { return self.a.b.m() } }
			`,
		},
		{
			name: "InferredReturn",
			src: `
				class A { readonly b: B, get(&self) { return self.b.m() } }
				class B { m(&self) -> number { return 1 } }
				val x: number = A(B()).get()
			`,
		},
		{
			name: "InferredReturnFromInferredReturn",
			src: `
				class A { readonly b: B, get(&self) { return self.b.m() } }
				class B { m(&self) { return 1 } }
				val x: string = A(B()).get()
			`,
			wantErrs: []string{"cannot constrain 1 <: string"},
		},
		{
			name: "InferredReturnMismatch",
			src: `
				class A { readonly b: B, get(&self) { return self.b.m() } }
				class B { m(&self) -> number { return 1 } }
				val x: string = A(B()).get()
			`,
			wantErrs: []string{"cannot constrain number <: string"},
		},
		{
			// A and B each read the other, so neither body can be walked first. A's
			// unannotated get reads B's m before B's bodies are walked, and sees m's
			// annotated return.
			name: "CycleInferredReturnMismatch",
			src: `
				class A {
					readonly b: B,
					get(&self) { return self.b.m() },
					k(&self) -> number { return 1 },
				}
				class B {
					readonly a: A,
					m(&self) -> number { return 1 },
					j(&self) { return self.a.k() },
				}
				fn f(a: A) -> string { return a.get() }
			`,
			wantErrs: []string{"cannot constrain number <: string"},
		},
		{
			// f and B depend on each other, so f reads m's stub before B's bodies are
			// walked. The mismatch is reported once.
			name: "FuncReadsStubInCycle",
			src: `
				fn f(b: B) { return b.m() }
				class B { m(&self) -> number { return 1 }, n(&self, b: B) { return f(b) } }
				fn g(b: B) -> string { return f(b) }
			`,
			wantErrs: []string{"cannot constrain number <: string"},
		},
		{
			name: "MissingMember",
			src: `
				class A { readonly b: B, get(&self) -> number { return self.b.z } }
				class B { readonly n: number }
			`,
			wantErrs: []string{"object is missing property: z"},
		},
		{
			name: "MismatchedReturn",
			src: `
				class A { readonly b: B, get(&self) -> string { return self.b.m() } }
				class B { m(&self) -> number { return 1 } }
			`,
			wantErrs: []string{"cannot constrain number <: string"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			msgs := make([]string, len(errs))
			for i, err := range errs {
				msgs[i] = err.Message()
			}
			if test.wantErrs == nil {
				require.Empty(t, msgs)
				return
			}
			require.Equal(t, test.wantErrs, msgs)
		})
	}
}

// TestInferReaderOfLaterClassMemberType covers a function reading a member of a class it
// forms a cycle with. f and g are inferred with B's value key, so the type each is
// generalized at includes what B's method body returns.
func TestInferReaderOfLaterClassMemberType(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			name: "ReaderChainBeforeMember",
			src: `
				fn f(b: B) { return b.m() }
				fn g(b: B) { return f(b) }
				class B { m(&self) { return 1 }, n(&self, b: B) { return g(b) } }
			`,
			wantValues: map[string]string{
				"f": "fn (b: B) -> 1",
				"g": "fn (b: B) -> 1",
			},
		},
		{
			name: "ReaderChainBeforeGetter",
			src: `
				fn f(b: B) { return b.v }
				fn g(b: B) { return f(b) }
				class B { get v(&self) { return "s" }, n(&self, b: B) { return g(b) } }
			`,
			wantValues: map[string]string{
				"f": `fn (b: B) -> "s"`,
				"g": `fn (b: B) -> "s"`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classValues(t, test.src, test.wantValues, nil)
		})
	}
}

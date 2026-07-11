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
			wantValues: map[string]string{"C": "fn (n: number) -> C"},
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
			wantValues: map[string]string{"C": "fn (n: number) -> C"},
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
			wantValues: map[string]string{"C": "fn (n: number) -> C"},
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
// receiver, but `self` is immutable in the caller.
//
// DISABLED until E1. B3 resolves the call — `self.bump()` finds bump's signature — but does
// not yet constrain the receiver against the method's SelfParam, so today the call
// type-checks with no error. That `receiver <: SelfParam` check lands with E1's method-call
// path through valueProp (receiver-dependent dispatch), and the same gap holds for an
// external `mut` call on an immutable binding. The asserted message is the expected form
// from the mutability machinery, which today reports `cannot constrain immutable object <:
// mutable object` for the analogous immutable-value-into-`mut`-parameter case; E1 finalizes
// the exact rendering for a class receiver.
/*
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
*/

// TestInferClassGenericMethodReturnsTypeParam pins the gap where a method whose return is a
// class type parameter round-trips as `never` through a call — both a `self.method()` call
// and external access. `read` returns the field typed `T`, so `b.read()` for `b : Box<5>`
// should project `T` to `5`, and `alias` calling `self.read()` should reach the same value.
//
// DISABLED until B8 (planning/simple_sub/m5-implementation-plan.md §"Phase B"). B1's
// §"Member access" owned this — wire a resolved method through the projected ClassDef.Body,
// wrapping the method's own TypeParams in a PolyScheme and substituting the class arguments —
// but the shipped B1 slice descoped the generic case. Today member access returns the
// method's raw FuncType, and an unannotated field read mints an intermediate var that
// class-argument projection cannot rewrite, so the return collapses to `never`. Neither
// classBodyMember (self access) nor projectedMember (external access) needs its own fix — the
// selective generic-body coalesce and the wrap belong in the shared member-access path. B8
// lands it; re-enable then.
/*
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
*/

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

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Object rest over non-property members and over class instances ---

// An object rest builds a FRESH object at run time by reading each leftover name off the
// scrutinee and storing the result, so the leftover's members are data properties rather
// than the accessors the scrutinee declared. That is JavaScript's own rest semantics, and it
// is where the leftover parts company with `Omit<T, K>`, which keeps a getter a getter.
func TestInferObjectRestConvertsAccessors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The getter is read once and its result stored, so the leftover holds a plain
			// property at the getter's own type.
			name: "GetterBecomesAProperty",
			src: `
				fn f(p: {x: number, get y(&self) -> string}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, get y() -> string}) -> {y: string}",
		},
		{
			// Reading a name that has only a setter yields `undefined`, and that is what
			// the copy stores. The name survives rather than being dropped.
			name: "SetterOnlyBecomesUndefined",
			src: `
				fn f(p: {x: number, set y(&mut self, v: string)}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, set y(value: string)}) -> {y: undefined}",
		},
		{
			// A getter and setter sharing a name are one readable name, so the pair
			// collapses to the single property the getter's type gives.
			name: "GetterAndSetterPairCollapse",
			src: `
				fn f(p: {x: number, get y(&self) -> string, set y(&mut self, v: string)}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, get y() -> string, set y(value: string)}) -> {y: string}",
		},
		{
			// A `throws` on the getter is raised by the destructuring itself, so the stored
			// property does not carry it.
			name: "GetterThrowsIsNotStored",
			src: `
				fn f(p: {x: number, get y(&self) -> string throws boolean}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, get y() -> string throws boolean}) -> {y: string}",
		},
		{
			// A method is a value the copy stores as-is, so it stays callable through the
			// leftover rather than flattening.
			name: "MethodCarriesThrough",
			src: `
				fn f(p: {x: number, m(&self, a: number) -> string}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, m(a: number) -> string}) -> {m(a: number) -> string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// The leftover's members stay usable through the bound name, so the conversion produces a
// real object rather than a rendering of one.
func TestInferObjectRestMembersStayUsable(t *testing.T) {
	t.Run("a converted getter reads at its type", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get doubled(&self) -> number { return self.v },
			}
			fn f(b: Box) {
				val {v, ...rest} = b
				return rest.doubled
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> number", values["f"])
	})

	t.Run("a converted setter-only name reads undefined", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f(p: {x: number, set y(&mut self, v: string)}) {
				val {x, ...rest} = p
				return rest.y
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: number, set y(value: string)}) -> undefined", values["f"])
	})

	t.Run("a class's converted setter-only name reads undefined too", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				set value(&mut self, x: number) { self.v = x },
			}
			fn f(b: Box) {
				val {v, ...rest} = b
				return rest.value
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> undefined", values["f"])
	})

	t.Run("a class's converted accessor pair reads at the getter's type", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get value(&self) -> number { return self.v },
				set value(&mut self, x: number) { self.v = x },
			}
			fn f(b: Box) {
				val {v, ...rest} = b
				return rest.value
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> number", values["f"])
	})

	t.Run("a converted getter is writable through a mut rest", func(t *testing.T) {
		// The copy is a fresh object, so a write lands on it and never reaches the
		// getter. The converted property is therefore not readonly.
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get doubled(&self) -> number { return self.v },
			}
			fn f(b: Box) {
				val {v, ...mut rest} = b
				rest.doubled = 5
				return rest
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> {doubled: number, ...}", values["f"])
	})

	t.Run("a method in the leftover calls", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				getV(&self) -> number { return self.v },
			}
			fn f(b: Box) {
				val {v, ...rest} = b
				return rest.getV()
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> number", values["f"])
	})
}

// An object pattern reads a getter the way member access does. A getter that borrows its
// receiver immutably fills the plain property requirement propReq mints, so `{y}` binds the
// getter's type. A setter-only name has nothing to read, so naming it as a field is still
// reported missing.
//
// The rest is unaffected either way. The named key is excluded from the leftover whether or
// not its own binding checked, so the recovery does not additionally hand the field to the
// rest.
func TestInferObjectPatternAccessorField(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     string
		wantErrs []string
	}{
		{
			name: "a getter field on an object type binds",
			src: `
				fn f(p: {x: number, get y(&self) -> string}) {
					val {y} = p
					return y
				}
			`,
			want: "fn (p: {x: number, get y() -> string}) -> string",
		},
		{
			name: "member access on the same shape succeeds",
			src: `
				class Box {
					v: number,
					get doubled(&self) -> number { return self.v },
				}
				fn f(b: Box) { return b.doubled }
			`,
			want: "fn (b: Box) -> number",
		},
		{
			name: "a getter field on a class instance is withheld from the rest",
			src: `
				class Box {
					v: number,
					get doubled(&self) -> number { return self.v },
				}
				fn f(b: Box) {
					val {doubled, ...rest} = b
					return rest
				}
			`,
			want: "fn (b: Box) -> {v: number, ...}",
		},
		{
			name: "a setter-only field on a class instance is reported missing",
			src: `
				class Box {
					v: number,
					set value(&mut self, x: number) { self.v = x },
				}
				fn f(b: Box) {
					val {value, ...rest} = b
					return rest
				}
			`,
			want:     "fn (b: Box) -> {v: number, ...}",
			wantErrs: []string{"7:11-7:16: object is missing property: value"},
		},
		{
			name: "an accessor pair named as a field binds its getter",
			src: `
				class Box {
					v: number,
					get value(&self) -> number { return self.v },
					set value(&mut self, x: number) { self.v = x },
				}
				fn f(b: Box) {
					val {value, ...rest} = b
					return [value, rest]
				}
			`,
			want: "fn (b: Box) -> [number, {v: number, ...}]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errs)
			} else {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			}
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A class instance grounds to its projected body before the leftover is read, so an object
// pattern destructures an instance the same way it destructures an object type. The body is
// inexact, so every leftover here keeps an open `...` tail even when the pattern names every
// field the class declares.
func TestInferObjectRestOnClassInstance(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "InstanceFields",
			src: `
				class Pt { x: number, y: string }
				fn f(p: Pt) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: Pt) -> {y: string, ...}",
		},
		{
			// Naming every declared field still leaves the body's open tail, so the
			// leftover is the empty inexact object rather than the empty exact one.
			name: "InstanceEveryFieldNamed",
			src: `
				class Pt { x: number, y: string }
				fn f(p: Pt) {
					val {x, y, ...rest} = p
					return rest
				}`,
			want: "fn (p: Pt) -> {...}",
		},
		{
			// The instance's own type argument reaches the leftover, so the remaining
			// field reads at `number` rather than at the class's parameter.
			name: "GenericInstanceReadsAtItsArgument",
			src: `
				class Box<T> { value: T, tag: string }
				fn f(b: Box<number>) {
					val {tag, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box<number>) -> {value: number, ...}",
		},
		{
			// A static member belongs to the class value, not the instance body, so it is
			// never part of an instance's leftover.
			name: "StaticsAreNotInstanceMembers",
			src: `
				class Box {
					v: number,
					static make() -> number { return 1 },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {...}",
		},
		{
			// An accessor declared on the class converts the same way one written on an
			// object type does, receiver and all, so its `mut self` does not survive into
			// the copied property. The class-side twin of
			// TestInferObjectRestConvertsAccessors.
			name: "InstanceGetterConverts",
			src: `
				class Box {
					v: number,
					get doubled(&mut self) -> number { return self.v },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {doubled: number, ...}",
		},
		{
			// A `match` arm binds an instance through the same walk a `val` does.
			name: "InstanceMatchArm",
			src: `
				class Pt { x: number, y: string }
				fn f(p: Pt) {
					return match p {
						{x, ...rest} => rest
					}
				}`,
			want: "fn (p: Pt) -> {y: string, ...}",
		},
		{
			// An instance pattern narrows to the class first, then destructures its body,
			// so its rest reads the same leftover the bare object pattern does.
			name: "InstancePatternArm",
			src: `
				class Pt { x: number, y: string }
				fn f(p: Pt) {
					return match p {
						Pt {x, ...rest} => rest
					}
				}`,
			want: "fn (p: Pt) -> {y: string, ...}",
		},
		{
			// A class setter with no getter converts the way an object type's does: the
			// name has nothing readable, so the copy stores `undefined` under it.
			name: "InstanceSetterOnlyBecomesUndefined",
			src: `
				class Box {
					v: number,
					set value(&mut self, x: number) { self.v = x },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {value: undefined, ...}",
		},
		{
			// A getter and setter declared under one name on a class collapse to the one
			// property the getter's type gives.
			name: "InstanceAccessorPairCollapses",
			src: `
				class Box {
					v: number,
					get value(&self) -> number { return self.v },
					set value(&mut self, x: number) { self.v = x },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {value: number, ...}",
		},
		{
			// The pair's two halves may differ, since a setter may accept more than its
			// getter returns. A rest READS, so the property takes the getter's type and
			// the setter's wider parameter does not reach it.
			name: "InstanceAccessorPairTakesTheGetterType",
			src: `
				class Box {
					v: number,
					get value(&self) -> number { return self.v },
					set value(&mut self, x: number | string) { },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {value: number, ...}",
		},
		{
			// A static setter belongs to the class value, so it is no more part of an
			// instance's leftover than a static method is.
			name: "StaticSetterIsNotAnInstanceMember",
			src: `
				class Box {
					v: number,
					static set cfg(x: number) { },
				}
				fn f(b: Box) {
					val {v, ...rest} = b
					return rest
				}`,
			want: "fn (b: Box) -> {...}",
		},
		{
			// A `match` arm converts an instance's setter the same way a `val` does.
			name: "InstanceSetterInMatchArm",
			src: `
				class Box {
					v: number,
					set value(&mut self, x: number) { self.v = x },
				}
				fn f(b: Box) {
					return match b {
						{v, ...rest} => rest
					}
				}`,
			want: "fn (b: Box) -> {value: undefined, ...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A borrowed class instance projects the borrow onto its leftover, so the rest is a `&mut`
// view of the remaining members bounded by the scrutinee's lifetime. The return annotation
// is what pins it: an owned or shared rest would fail that constraint.
func TestInferObjectRestOnBorrowedClassInstance(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Pt { x: {a: number}, y: {b: number} }
		fn f(p: &mut Pt) -> &mut {y: {b: number}, ...} {
			val {x, ...rest} = p
			return rest
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: &'a mut Pt) -> &'a mut {y: {b: number}, ...}", values["f"])
}

// A borrowed instance carrying a setter projects the borrow over the CONVERTED leftover, so
// the `undefined` the setter's name reads as sits under the `&mut` just as a field would.
func TestInferObjectRestOnBorrowedInstanceWithSetter(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box {
			v: {a: number},
			set value(&mut self, x: {a: number}) { self.v = x },
		}
		fn f(b: &mut Box) -> &mut {value: undefined, ...} {
			val {v, ...rest} = b
			return rest
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(b: &'a mut Box) -> &'a mut {value: undefined, ...}", values["f"])
}

// TestInferObjectRestOverUnion covers an object rest whose scrutinee is a union. The rest
// distributes over the members, so each member contributes the fields the pattern leaves
// and the rest binds the union of those leftovers. That is what TypeScript infers for the
// same destructuring.
func TestInferObjectRestOverUnion(t *testing.T) {
	const fb = `type FB = {kind: "foo", id: number, foo: string} | {kind: "bar", id: number, bar: boolean}`
	tests := []struct {
		name     string
		src      string
		binding  string
		want     string
		wantErrs []string
	}{
		{
			name: "a non-union object keeps its leftover",
			src: `
				declare val p: {kind: "foo", id: number, foo: string}
				val {kind: k, id: i, ...rest} = p
			`,
			binding: "rest",
			want:    "{foo: string}",
		},
		{
			name: "a union reached through an alias",
			src: fb + `
				declare val fb: FB
				val {kind: k, id: i, ...rest} = fb
			`,
			binding: "rest",
			want:    "{foo: string} | {bar: boolean}",
		},
		{
			name: "a union member that is itself an alias of a union",
			src: `
				type AB = {kind: "a", a: number} | {kind: "b", b: string}
				declare val v: AB | {kind: "c", c: boolean}
				val {kind, ...rest} = v
			`,
			binding: "rest",
			want:    "{a: number} | {b: string} | {c: boolean}",
		},
		{
			name: "a union written inline",
			src: `
				declare val fb: {kind: "foo", foo: string} | {kind: "bar", bar: boolean}
				val {kind, ...rest} = fb
			`,
			binding: "rest",
			want:    "{foo: string} | {bar: boolean}",
		},
		{
			name: "a rest inside a function parameter pattern",
			src: fb + `
				fn f({kind, id, ...rest}: FB) { return rest }
			`,
			binding: "f",
			want:    "fn ({kind, id, ...rest}: FB) -> {foo: string} | {bar: boolean}",
		},
		{
			name: "a rest in a match arm pattern",
			src: fb + `
				fn f(p: FB) {
					return match p {
						{kind, id, ...rest} => rest,
					}
				}
			`,
			binding: "f",
			want:    "fn (p: FB) -> {foo: string} | {bar: boolean}",
		},
		// A plain read of a key that an exact member lacks reads `undefined` from that member.
		{
			name: "a plain read of a key an exact member lacks",
			src: `
				fn f(p: {kind: "a", b: string} | {kind: "c", c: number}) {
					return p.b
				}
			`,
			binding: "f",
			want:    `fn (p: {kind: "a", b: string} | {kind: "c", c: number}) -> string | undefined`,
		},
		// A member written with `...` may carry a key it does not declare, at any type, so a
		// plain read of that key reads `unknown`. The next case destructures the same key and
		// must agree with it.
		{
			name: "a plain read of a key one member lacks",
			src: `
				fn f(p: {kind: "a", b: string} | {kind: "c", c: number, ...}) {
					return p.b
				}
			`,
			binding: "f",
			want:    `fn (p: {kind: "a", b: string} | {kind: "c", c: number, ...}) -> unknown`,
		},
		{
			name: "a destructured key one member lacks reads as the plain read does",
			src: `
				fn f(p: {kind: "a", b: string} | {kind: "c", c: number, ...}) {
					val {b, ...rest} = p
					return [b, rest]
				}
			`,
			binding: "f",
			want: `fn (p: {kind: "a", b: string} | {kind: "c", c: number, ...}) -> ` +
				`[unknown, {kind: "a"} | {kind: "c", c: number, ...}]`,
		},
		// An open member does not make a key that no member declares readable.
		{
			name: "a plain read of a key no member declares",
			src: `
				fn f(p: {kind: "a", b: string} | {kind: "c", c: number, ...}) {
					return p.z
				}
			`,
			binding:  "f",
			want:     `fn (p: {kind: "a", b: string} | {kind: "c", c: number, ...}) -> never`,
			wantErrs: []string{"3:15-3:16: object is missing property: z"},
		},
		// A member that is not an object fails a plain read. The next case destructures
		// the same key and must report the same error.
		{
			name: "a plain read through a non-object member",
			src: `
				fn f(p: {kind: "a", b: string} | number) {
					return p.b
				}
			`,
			binding:  "f",
			want:     `fn (p: {kind: "a", b: string} | number) -> string`,
			wantErrs: []string{"3:13-3:16: cannot constrain number <: object"},
		},
		{
			name: "a destructured key through a non-object member reports the plain read's error",
			src: `
				fn f(p: {kind: "a", b: string} | number) {
					val {b, ...rest} = p
					return rest
				}
			`,
			binding:  "f",
			want:     `fn (p: {kind: "a", b: string} | number) -> {...}`,
			wantErrs: []string{"3:11-3:12: cannot constrain number <: object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs == nil {
				require.Empty(t, errs)
			} else {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			}
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

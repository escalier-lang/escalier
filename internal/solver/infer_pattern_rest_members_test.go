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
				fn f(p: {x: number, get y(self) -> string}) {
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
				fn f(p: {x: number, set y(mut self, v: string)}) {
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
				fn f(p: {x: number, get y(self) -> string, set y(mut self, v: string)}) {
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
				fn f(p: {x: number, get y(self) -> string throws boolean}) {
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
				fn f(p: {x: number, m(self, a: number) -> string}) {
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
				get doubled(self) -> number { return self.v },
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
			fn f(p: {x: number, set y(mut self, v: string)}) {
				val {x, ...rest} = p
				return rest.y
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: number, set y(value: string)}) -> undefined", values["f"])
	})

	t.Run("a converted getter is writable through a mut rest", func(t *testing.T) {
		// The copy is a fresh object, so a write lands on it and never reaches the
		// getter. The converted property is therefore not readonly.
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get doubled(self) -> number { return self.v },
			}
			fn f(b: Box) {
				val {v, ...mut rest} = b
				rest.doubled = 5
				return rest
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> mut {doubled: number, ...}", values["f"])
	})

	t.Run("a method in the leftover calls", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				getV(self) -> number { return self.v },
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

// KNOWN GAP. Naming an accessor as a pattern FIELD fails, on an object type and on a class
// instance alike, because propReq mints a plain property requirement and an object whose
// member is a getter or setter does not satisfy one. Member access has no such trouble:
// `b.doubled` reads `number` off the same class. Destructuring and member access disagree
// here, and the pattern path is the one that is wrong.
//
// The rest itself is unaffected, which is why these live alongside the passing cases above.
// The named key is excluded from the leftover whether or not its own binding checked, so
// the recovery does not additionally hand the field to the rest.
func TestInferObjectPatternAccessorFieldGap(t *testing.T) {
	t.Run("a getter field on an object type is reported missing", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(p: {x: number, get y(self) -> string}) {
				val {y} = p
				return y
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "2:12-2:46: object is missing property: y", msgWithSpan(errs[0]))
	})

	t.Run("member access on the same shape succeeds", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get doubled(self) -> number { return self.v },
			}
			fn f(b: Box) { return b.doubled }
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (b: Box) -> number", values["f"])
	})

	t.Run("a reported accessor field is still withheld from the rest", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			class Box {
				v: number,
				get doubled(self) -> number { return self.v },
			}
			fn f(b: Box) {
				val {doubled, ...rest} = b
				return rest
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "7:10-7:17: object is missing property: doubled", msgWithSpan(errs[0]))
		require.Equal(t, "fn (b: Box) -> {v: number, ...}", values["f"])
	})
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
					get doubled(mut self) -> number { return self.v },
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

package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferClassOverrideCompat covers the rule that a member a subclass redeclares must
// stay compatible with the same-named member its superclass chain declares. The nominal
// subtype rule decides `Dog <: Animal` on the declared `extends` edge alone, so without
// this check a subclass could contradict its own edge (escalier-lang/escalier#985).
func TestInferClassOverrideCompat(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "FieldRedeclaredAtAWiderObject",
			src: `
				class Animal {
					pos: {x: number},
					constructor(mut self, pos: {x: number}) { self.pos = pos },
				}
				class Dog extends Animal {
					pos: {x: number, y: number},
					constructor(mut self, pos: {x: number, y: number}) { self.pos = pos },
				}
			`,
			want: []string{
				"class `Dog` redeclares inherited member `pos` with type `{x: number, y: number}`, " +
					"which is not compatible with `{x: number}` declared by `Animal`",
			},
		},
		{
			name: "FieldRedeclaredAtASubtype",
			src: `
				class Animal {
					legs: number | undefined,
					constructor(mut self, legs: number | undefined) { self.legs = legs },
				}
				class Dog extends Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
			`,
			// A writable field is invariant, since the `Animal` view admits `a.legs =
			// undefined` and `undefined` does not fit `Dog`'s `number`.
			want: []string{
				"class `Dog` redeclares inherited member `legs` with type `number`, " +
					"which is not compatible with `number | undefined` declared by `Animal`",
			},
		},
		{
			name: "FieldRedeclaredAtTheInheritedType",
			src: `
				class Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
				class Dog extends Animal {
					legs: number,
					constructor(mut self) { self.legs = 4 },
				}
			`,
			want: nil,
		},
		{
			name: "MemberTheSuperclassDoesNotDeclare",
			src: `
				class Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
				class Dog extends Animal {
					legs: number,
					name: string,
					constructor(mut self) {
						self.legs = 4
						self.name = "Rex"
					},
				}
			`,
			want: nil,
		},
		{
			name: "MethodReturnNotCovariant",
			src: `
				class Animal {
					constructor(mut self) {},
					speak(self) -> string { return "..." },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					speak(self) -> number { return 5 },
				}
			`,
			// The two returns are unrelated rather than one widening the other, which
			// MethodWidenedReturn covers.
			want: []string{
				"class `Dog` redeclares inherited member `speak` with type `fn () -> number`, " +
					"which is not compatible with `fn () -> string` declared by `Animal`",
			},
		},
		{
			name: "MethodParamNotContravariant",
			src: `
				class Animal {
					constructor(mut self) {},
					eat(self, food: string | number) -> undefined { return undefined },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					eat(self, food: string) -> undefined { return undefined },
				}
			`,
			// Dog's parameter is a subtype of the one it inherits, so an `Animal` reference
			// could pass a `number` the Dog does not accept.
			want: []string{
				"class `Dog` redeclares inherited member `eat` with type `fn (food: string) -> undefined`, " +
					"which is not compatible with `fn (food: number | string) -> undefined` declared by `Animal`",
			},
		},
		{
			name: "MethodWidenedParamIsAllowed",
			src: `
				class Animal {
					constructor(mut self) {},
					eat(self, food: string) -> undefined { return undefined },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					eat(self, food: string | number) -> undefined { return undefined },
				}
			`,
			// A parameter is contravariant, so a subclass may accept more than the inherited
			// signature promises. Every call an `Animal` reference makes still fits.
			want: nil,
		},
		{
			name: "MethodWidenedReturn",
			src: `
				class Animal {
					constructor(mut self) {},
					speak(self) -> number { return 5 },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					speak(self) -> number | string { return 5 },
				}
			`,
			// A return is covariant, so widening it breaks the inherited promise: a caller
			// holding an `Animal` reads the result as `number` and may get a `string`.
			want: []string{
				"class `Dog` redeclares inherited member `speak` with type `fn () -> number | string`, " +
					"which is not compatible with `fn () -> number` declared by `Animal`",
			},
		},
		{
			name: "MethodNarrowedReturnIsAllowed",
			src: `
				class Animal {
					constructor(mut self) {},
					speak(self) -> number | string { return 5 },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					speak(self) -> number { return 5 },
				}
			`,
			// Narrowing is the direction a return admits. A caller holding an `Animal` reads
			// the result as `number | string`, which every `number` a Dog returns fits.
			want: nil,
		},
		{
			name: "FieldOverriddenByAMethod",
			src: `
				class Animal {
					speak: string,
					constructor(mut self) { self.speak = "..." },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					speak(self) -> string { return "woof" },
				}
			`,
			want: []string{
				"class `Dog` redeclares inherited member `speak` as a method, " +
					"but `Animal` declares it as a writable field",
			},
		},
		{
			name: "InheritedThroughTwoEdges",
			src: `
				class Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
				class Pet extends Animal {
					constructor(mut self) {},
				}
				class Dog extends Pet {
					legs: string,
					constructor(mut self) { self.legs = "four" },
				}
			`,
			want: []string{
				"class `Dog` redeclares inherited member `legs` with type `string`, " +
					"which is not compatible with `number` declared by `Animal`",
			},
		},
		{
			name: "GetterOverriddenAtANarrowerType",
			src: `
				class Animal {
					constructor(mut self) {},
					get p(self) -> number | string { return 4 },
					set p(mut self, v: number | string) {},
				}
				class Dog extends Animal {
					constructor(mut self) {},
					get p(self) -> number { return 4 },
					set p(mut self, v: number | string) {},
				}
			`,
			// A read through `Animal` yields `number | string`, which Dog's getter narrows.
			// The narrowing is the direction a read admits, so nothing is reported.
			want: nil,
		},
		{
			name: "OverridingOnlyTheGetterHalfDropsTheSetter",
			src: `
				class Animal {
					constructor(mut self) {},
					get p(self) -> number { return 4 },
					set p(mut self, v: number) {},
				}
				class Dog extends Animal {
					constructor(mut self) {},
					get p(self) -> number { return 4 },
				}
			`,
			// Dog's getter shadows the whole pair rather than merging with it, so an
			// `Animal`-typed reference to a Dog could write a member the Dog does not offer.
			want: []string{
				"class `Dog` redeclares inherited member `p` as a getter, " +
					"but `Animal` declares it as a setter",
			},
		},
		{
			name: "AddingTheMissingAccessorHalfDropsTheInheritedOne",
			src: `
				class Animal {
					constructor(mut self) {},
					get legs(self) -> number { return 4 },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					set legs(mut self, v: number) {},
				}
			`,
			// Reading `legs` off a Dog already reports that the property is write-only,
			// because a subclass body shadows the inherited accessor rather than merging
			// with it. This says the same thing at the declaration. Revisit once
			// escalier-lang/escalier#872 folds inherited members into a subclass's body.
			want: []string{
				"class `Dog` redeclares inherited member `legs` as a setter, " +
					"but `Animal` declares it as a getter",
			},
		},
		{
			name: "GetterOverriddenWithAThrows",
			src: `
				class Animal {
					flag: boolean,
					constructor(mut self, flag: boolean) { self.flag = flag },
					get p(self) -> number { return 4 },
				}
				class Dog extends Animal {
					flag: boolean,
					constructor(mut self, flag: boolean) { self.flag = flag },
					get p(self) -> number throws string {
						if self.flag {
							throw "x"
						}
						return 4
					},
				}
			`,
			// Reading `p` through `Animal` raises nothing, so a subclass cannot make the read
			// throw.
			want: []string{
				"class `Dog` redeclares inherited member `p` with throws type `string`, " +
					"which is not compatible with `never` declared by `Animal`",
			},
		},
		{
			name: "OverloadSetInADifferentOrder",
			src: `
				class Animal {
					constructor(mut self) {},
					f(self, x: number) -> number { return x },
					f(self, x: string) -> string { return x },
				}
				class Dog extends Animal {
					constructor(mut self) {},
					f(self, x: string) -> string { return x },
					f(self, x: number) -> number { return x },
				}
			`,
			// An overload set reads as the intersection of its arms, so the two classes
			// declare the same member and the order the arms are written in does not matter.
			want: nil,
		},
		{
			name: "MethodOverriddenWithAMutReceiver",
			src: `
				class Animal {
					x: number,
					constructor(mut self) { self.x = 0 },
					bump(self) -> undefined { return undefined },
				}
				class Dog extends Animal {
					x: number,
					constructor(mut self) { self.x = 0 },
					bump(mut self) -> undefined { self.x = 1 },
				}
			`,
			// An immutable `Animal` reference can call `bump`, so a subclass cannot make the
			// call need a mutable one.
			want: []string{
				"class `Dog` redeclares inherited member `bump` as a method taking `mut self`, " +
					"but `Animal` declares it as a method",
			},
		},
		{
			name: "GetterOverriddenByAnOptionalField",
			src: `
				class Animal {
					constructor(mut self) {},
					get p(self) -> number { return 4 },
				}
				class Dog extends Animal {
					p?: number,
					constructor(mut self) {},
				}
			`,
			// The `Animal` view reads `p` as always present, so a subclass cannot make it a
			// field that may be absent.
			want: []string{
				"class `Dog` redeclares inherited member `p` as an optional writable field, " +
					"but `Animal` declares it as a getter",
			},
		},
		{
			name: "GenericSuperclassCheckedAtTheSubclassArgument",
			src: `
				class Box<T> {
					value: T,
					constructor(mut self, value: T) { self.value = value },
				}
				class StrBox<T> extends Box<T> {
					value: string,
					constructor(mut self, value: string) { self.value = value },
				}
			`,
			// `Box<T>`'s `value` projects to `StrBox<T>`'s own `T`, and `string` is not a
			// subtype of an arbitrary `T`.
			want: []string{
				"class `StrBox` redeclares inherited member `value` with type `string`, " +
					"which is not compatible with `T` declared by `Box`",
			},
		},
		{
			name: "WritableFieldRedeclaredReadonly",
			src: `
				class Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
				class Dog extends Animal {
					readonly legs: number,
					constructor(mut self) {},
				}
			`,
			// The `Animal` view still admits `a.legs = 5`, so a subclass cannot take the
			// write away. The initialization diagnostic rides along because a `readonly`
			// instance field has no way to be assigned, which is a separate gap.
			want: []string{
				"Field 'legs' is not initialized on every path through the constructor.",
				"class `Dog` redeclares inherited member `legs` as a readonly field, " +
					"but `Animal` declares it as a writable field",
			},
		},
		{
			name: "RequiredFieldRedeclaredOptional",
			src: `
				class Animal {
					legs: number,
					constructor(mut self, legs: number) { self.legs = legs },
				}
				class Dog extends Animal {
					legs?: number,
					constructor(mut self) {},
				}
			`,
			// The `Animal` view still reads `a.legs` as present, so a subclass cannot make
			// it optional.
			want: []string{
				"class `Dog` redeclares inherited member `legs` as an optional writable field, " +
					"but `Animal` declares it as a writable field",
			},
		},
		{
			name: "GenericSuperclassAtTheSameParameter",
			src: `
				class Box<T> {
					value: T,
					constructor(mut self, value: T) { self.value = value },
				}
				class LabelledBox<T> extends Box<T> {
					value: T,
					label: string,
					constructor(mut self, value: T) {
						self.value = value
						self.label = "box"
					},
				}
			`,
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, Messages(errs))
		})
	}
}

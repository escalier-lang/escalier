package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferClassInheritedWholeBody covers the whole-body views of a class instance seeing
// the members it inherits, not just the ones it declares. A single member read already
// walked the `extends` chain; `self` and projectClassBody now walk it too
// (escalier-lang/escalier#872).
func TestInferClassInheritedWholeBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ConstructorAssignsAnInheritedField",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					breed: string,
					constructor(mut self, name: string, breed: string) {
						self.name = name
						self.breed = breed
					},
				}
			`,
			want: nil,
		},
		{
			name: "MethodReadsAndWritesAnInheritedField",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					constructor(mut self, name: string) { self.name = name },
					rename(mut self, name: string) -> undefined { self.name = name },
					read(self) -> string { return self.name },
				}
			`,
			want: nil,
		},
		{
			name: "SubclassPatternBindsAnInheritedField",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					breed: string,
					constructor(mut self, breed: string) { self.breed = breed },
				}
				fn f(a: Animal) {
					return match a {
						Dog { name, breed } => [name, breed]
					}
				}
			`,
			want: nil,
		},
		{
			name: "ClassIntoObjectCarriesAnInheritedMember",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					breed: string,
					constructor(mut self, breed: string) { self.breed = breed },
				}
				fn g(d: Dog) -> {name: string, ...} { return d }
			`,
			want: nil,
		},
		{
			name: "InheritedThroughTwoEdges",
			src: `
				class Base {
					base: number,
					constructor(mut self) { self.base = 0 },
				}
				class Mid extends Base {
					constructor(mut self) {},
				}
				class Leaf extends Mid {
					constructor(mut self) { self.base = 1 },
				}
				fn h(l: Leaf) -> {base: number, ...} { return l }
			`,
			want: nil,
		},
		{
			name: "ConstructorAssignsAFieldInheritedThroughTwoEdges",
			src: `
				class Base {
					base: number,
					constructor(mut self) { self.base = 0 },
				}
				class Mid extends Base {
					constructor(mut self) {},
				}
				class Leaf extends Mid {
					constructor(mut self) { self.base = 1 },
				}
			`,
			// Reaching Base from Leaf needs Mid's own edge and body recorded before Leaf's
			// body runs, which is what the dependency from a subclass's value key to its
			// superclass's orders.
			want: nil,
		},
		{
			name: "InheritedFieldSurvivesValueKeyPressure",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
					m(self) -> number { return n },
				}
				class Dog extends Animal {
					constructor(mut self, name: string) { self.name = name },
				}
				fn g(x: Dog) -> number { return 1 }
				val d = Dog("rex")
				val n = g(d)
			`,
			// The surrounding declarations pull Dog's value key ahead of Animal's unless the
			// `extends` clause depends on the superclass's value key, which is where the
			// inherited members land. Without that edge `self.name` reports a missing
			// property here, while the same two classes on their own check clean.
			want: nil,
		},
		{
			name: "GenericSuperclassProjectedAtTheSubclassArgument",
			src: `
				class Animal<A> {
					food: A,
					constructor(mut self, food: A) { self.food = food },
				}
				class Dog extends Animal<string> {
					constructor(mut self) { self.food = "bone" },
				}
			`,
			// `Animal<A>`'s `food` reaches Dog through `extends Animal<string>`, so the
			// assignment has to fit `string` rather than an unresolved `A`.
			want: nil,
		},
		{
			name: "GenericSuperclassRejectsTheWrongArgument",
			src: `
				class Animal<A> {
					food: A,
					constructor(mut self, food: A) { self.food = food },
				}
				class Dog extends Animal<string> {
					constructor(mut self) { self.food = 5 },
				}
			`,
			// A `mut self` field write is invariant, so a rejected one reports both
			// directions. That is what an own field of the wrong type reports too, so the
			// inherited field behaves exactly like a declared one.
			want: []string{
				"cannot constrain string <: number",
				"cannot constrain number <: string",
			},
		},
		{
			name: "RedeclaredMemberIsNotDuplicated",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				fn g(d: Dog) -> {name: string, ...} { return d }
			`,
			// Dog's own `name` shadows the inherited one, so the projected body carries a
			// single `name` rather than two competing members.
			want: nil,
		},
		{
			name: "DefiniteAssignmentSkipsInheritedFields",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					breed: string,
					constructor(mut self, breed: string) { self.breed = breed },
				}
			`,
			// A subclass may leave an inherited field unassigned. Requiring it needs
			// `super(…)` to delegate to, which is escalier-lang/escalier#1094.
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

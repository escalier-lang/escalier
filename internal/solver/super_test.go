package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferSuperCall covers the rules a `super(…)` call has to satisfy and the checking of
// its arguments. A subclass constructor runs its superclass's exactly once, as a statement of
// its own body, before it mentions `self`. The arguments are checked against the superclass
// constructor at the type arguments the `extends` clause names.
func TestInferSuperCall(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "DelegatesWithNoArguments",
			src: `
				class Animal {
					legs: number,
					constructor(mut self) { self.legs = 4 },
				}
				class Dog extends Animal {
					constructor(mut self) { super() },
				}
			`,
			want: nil,
		},
		{
			name: "PassesTheSuperclassArgument",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					constructor(mut self, name: string) { super(name) },
				}
			`,
			want: nil,
		},
		{
			name: "MissingCall",
			src: `
				class Animal {
					constructor(mut self) {},
				}
				class Dog extends Animal {
					constructor(mut self) {},
				}
			`,
			want: []string{
				"A subclass constructor must call `super(…)`; " +
					"the members inherited from `Animal` do not exist until it does.",
			},
		},
		{
			name: "CalledTwice",
			src: `
				class Animal {
					constructor(mut self) {},
				}
				class Dog extends Animal {
					constructor(mut self) {
						super()
						super()
					},
				}
			`,
			want: []string{"`super(…)` may only be called once per constructor."},
		},
		{
			name: "NestedInAConditional",
			src: `
				class Animal {
					constructor(mut self) {},
				}
				class Dog extends Animal {
					constructor(mut self, early: boolean) {
						if early {
							super()
						}
					},
				}
			`,
			// One call written inside an `if` reaches only some paths, so requiring the top
			// level is what makes one written call mean one call on every path.
			want: []string{
				"`super(…)` must be a statement of the constructor body, " +
					"so that it runs on every path through it.",
			},
		},
		{
			name: "CalledAfterSelfIsUsed",
			src: `
				class Animal {
					legs: number,
					constructor(mut self) { self.legs = 4 },
				}
				class Dog extends Animal {
					breed: string,
					constructor(mut self, breed: string) {
						self.breed = breed
						super()
					},
				}
			`,
			want: []string{
				"`super(…)` must run before `self` is used, " +
					"since the inherited members do not exist until it does.",
			},
		},
		{
			name: "ClassWithoutASuperclass",
			src: `
				class Animal {
					constructor(mut self) { super() },
				}
			`,
			want: []string{
				"`super(…)` needs a superclass to call; this class declares no `extends` clause.",
			},
		},
		{
			name: "OutsideAConstructor",
			src: `
				class Animal {
					constructor(mut self) {},
					speak(self) -> undefined { super() },
				}
			`,
			want: []string{"`super(…)` may only be called inside a constructor."},
		},
		{
			// A wrong argument count is caught by the constraint between the superclass
			// constructor and the call shape, so it reports the arity of the two signatures.
			// A direct call to `Animal()` reports "Not enough arguments: expected at least 1,
			// but got 0" instead, since the too-few lint fires only on a direct call.
			name: "TooFewArguments",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					constructor(mut self) { super() },
				}
			`,
			want: []string{"cannot constrain function of arity 1 <: function of arity 0"},
		},
		{
			name: "ArgumentOfTheWrongType",
			src: `
				class Animal {
					name: string,
					constructor(mut self, name: string) { self.name = name },
				}
				class Dog extends Animal {
					constructor(mut self) { super(5) },
				}
			`,
			want: []string{"cannot constrain 5 <: string"},
		},
		{
			// The superclass constructor is read at the type arguments the `extends` clause
			// names, not at fresh variables, so `A` is `string` here rather than resolving to
			// whatever the call passes.
			name: "ArgumentCheckedAtTheEdgeTypeArgument",
			src: `
				class Animal<A> {
					food: A,
					constructor(mut self, food: A) { self.food = food },
				}
				class Dog extends Animal<string> {
					constructor(mut self) { super(5) },
				}
			`,
			want: []string{"cannot constrain 5 <: string"},
		},
		{
			name: "ArgumentThreadedThroughTheSubclassParameter",
			src: `
				class Animal<A> {
					food: A,
					constructor(mut self, food: A) { self.food = food },
				}
				class Dog<D> extends Animal<D> {
					constructor(mut self, food: D) { super(food) },
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

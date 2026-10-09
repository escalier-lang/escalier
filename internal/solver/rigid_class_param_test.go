package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMemberBodyIsCheckedForEveryClassArgument covers a member or constructor body using its
// class's type parameter in a way only some instances allow. The class's parameters are rigid
// while the bodies are inferred, so such a use is reported where it is written. A method's own binder
// that meets the class's parameter is compared through its own bounds.
func TestAMemberBodyIsCheckedForEveryClassArgument(t *testing.T) {
	// bag declares a method generic in its own binder, which an override must keep generic.
	const bag = `class Bag<T> {
	items: Array<T>,
	contains<B>(&self, x: B) -> number { return 0 },
}
`
	// keyed extends bag and holds a function that takes only the class's own parameter.
	const keyed = `class Keyed<U> extends Bag<U> {
	key: fn (x: U) -> number,
	constructor(&mut self, items: Array<U>, key: fn (x: U) -> number) {
		super(items)
		self.key = key
	},
`
	tests := []struct {
		name string
		src  string
		// binding names a value whose rendered type the case asserts. An empty binding
		// asserts no type.
		binding string
		want    string
		errs    []string
	}{
		{
			// Bag's contains promises to take any B, so an override passing x on to a function
			// that takes only a U is rejected in its body. The override then has no forced
			// bound left to leak into the method's value type.
			name: "AnOverrideNarrowingItsBinderToTheClassParameter",
			src: bag + keyed + `	contains<B>(&self, x: B) -> number { return (self.key)(x) },
}
fn probe(b: &Bag<string>) -> number { return b.contains(42) }
val k = Keyed(["a"], fn (s: string) -> number { return 1 })
val r = probe(&k)
val mm = k.contains`,
			binding: "mm",
			want:    "fn <B>(x: B) -> number",
			errs:    []string{"11:47-11:59: cannot constrain B <: U"},
		},
		{
			// Leaf reaches key through Keyed, at Leaf's own V.
			name: "AnOverrideTwoLevelsDown",
			src: bag + keyed + `}
class Leaf<V> extends Keyed<V> {
	constructor(&mut self, items: Array<V>, key: fn (x: V) -> number) {
		super(items, key)
	},
	contains<B>(&self, x: B) -> number { return (self.key)(x) },
}`,
			errs: []string{"16:47-16:59: cannot constrain B <: V"},
		},
		{
			// A binder bounded by the class's parameter may be passed on. The override check
			// still rejects the narrower bound against Bag's unbounded B.
			name: "AnOverrideBoundingItsBinderByTheClassParameter",
			src: bag + keyed + `	contains<B: U>(&self, x: B) -> number { return (self.key)(x) },
}`,
			errs: []string{
				"11:2-11:64: class `Keyed` redeclares inherited member `contains` with type " +
					"`fn <B: U>(x: B) -> number`, which is not compatible with `fn <B>(x: B) -> number` " +
					"declared by `Bag`",
			},
		},
		{
			name: "ABinderBoundedByTheClassParameter",
			src: `class Keyed<U> {
	key: fn (x: U) -> number,
	apply<B: U>(&self, x: B) -> number { return (self.key)(x) },
}`,
		},
		{
			name: "AnOperatorNarrowingTheClassParameter",
			src: `class C<U> {
	v: U,
	m(&self, x: U) -> boolean { return x > 1 },
}`,
			errs: []string{"3:37-3:38: cannot constrain U <: number"},
		},
		{
			// a passes x to b before b's body is inferred, so the call is recorded against b's
			// signature stub. The `number` b takes reaches U when b's signature is linked to
			// that stub, which is where the error points.
			name: "AForwardCallToASibling",
			src: `class C<U> {
	v: U,
	a(&self, x: U) -> number { return self.b(x) },
	b(&self, y: number) -> number { return y },
}`,
			errs: []string{"4:2-4:44: cannot constrain U <: number"},
		},
		{
			name: "AConstructorBody",
			src: `class C<U> {
	v: U,
	constructor(&mut self, v: U) {
		self.v = v
		val n: number = v
	},
}`,
			errs: []string{"5:19-5:20: cannot constrain U <: number"},
		},
		{
			// A body that only moves the class's parameter around works for every instance.
			name: "AMethodMovingTheClassParameter",
			src: `class Box<T> {
	v: T,
	pair<U>(&self, u: U) -> [T, U] { return [self.v, u] },
}
val p = Box(1).pair("s")`,
			binding: "p",
			want:    `[1, "s"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.errs, messagesWithSpan(t, errs))
			if tt.binding != "" {
				require.Equal(t, tt.want, values[tt.binding])
			}
		})
	}
}

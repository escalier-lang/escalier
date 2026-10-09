package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMemberBodyIsCheckedForEveryClassArgument covers a member or constructor body using its
// class's type parameter in a way only some instances allow. The class's parameters are rigid
// while the bodies are inferred, so such a use is reported where it is written. A method's own
// binder that meets the class's parameter is compared through its own bounds.
func TestAMemberBodyIsCheckedForEveryClassArgument(t *testing.T) {
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
			// Bag's contains promises to take any B, and Keyed holds a key that takes only a U.
			// An override passing x on to key is rejected in its body. The override then has
			// no forced bound left to leak into the method's value type.
			name: "AnOverrideNarrowingItsBinderToTheClassParameter",
			src: `class Bag<T> {
	items: Array<T>,
	contains<B>(&self, x: B) -> number { return 0 },
}
class Keyed<U> extends Bag<U> {
	key: fn (x: U) -> number,
	constructor(&mut self, items: Array<U>, key: fn (x: U) -> number) {
		super(items)
		self.key = key
	},
	contains<B>(&self, x: B) -> number { return self.key(x) },
}
fn probe(b: &Bag<string>) -> number { return b.contains(42) }
val k = Keyed(["a"], fn (s: string) -> number { return 1 })
val r = probe(&k)
val mm = k.contains`,
			binding: "mm",
			want:    "fn <B>(x: B) -> number",
			errs:    []string{"11:46-11:57: cannot constrain B <: U"},
		},
		{
			// Leaf reaches key through Keyed, at Leaf's own V.
			name: "AnOverrideTwoLevelsDown",
			src: `class Bag<T> {
	items: Array<T>,
	contains<B>(&self, x: B) -> number { return 0 },
}
class Keyed<U> extends Bag<U> {
	key: fn (x: U) -> number,
	constructor(&mut self, items: Array<U>, key: fn (x: U) -> number) {
		super(items)
		self.key = key
	},
}
class Leaf<V> extends Keyed<V> {
	constructor(&mut self, items: Array<V>, key: fn (x: V) -> number) {
		super(items, key)
	},
	contains<B>(&self, x: B) -> number { return self.key(x) },
}`,
			errs: []string{"16:46-16:57: cannot constrain B <: V"},
		},
		{
			// A binder bounded by the class's parameter may be passed on. The override check
			// still rejects the narrower bound against Bag's unbounded B.
			name: "AnOverrideBoundingItsBinderByTheClassParameter",
			src: `class Bag<T> {
	items: Array<T>,
	contains<B>(&self, x: B) -> number { return 0 },
}
class Keyed<U> extends Bag<U> {
	key: fn (x: U) -> number,
	constructor(&mut self, items: Array<U>, key: fn (x: U) -> number) {
		super(items)
		self.key = key
	},
	contains<B <: U>(&self, x: B) -> number { return self.key(x) },
}`,
			errs: []string{
				"11:2-11:64: class `Keyed` redeclares inherited member `contains` with type " +
					"`fn <B <: U>(x: B) -> number`, which is not compatible with `fn <B>(x: B) -> number` " +
					"declared by `Bag`",
			},
		},
		{
			name: "ABinderBoundedByTheClassParameter",
			src: `class Keyed<U> {
	key: fn (x: U) -> number,
	apply<B <: U>(&self, x: B) -> number { return self.key(x) },
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
			// a calls b before b's body is inferred, so the call is checked against b's
			// signature stub. The stub already takes the `number` b's annotation names, so the
			// error points at the call.
			name: "AForwardCallToASibling",
			src: `class C<U> {
	v: U,
	a(&self, x: U) -> number { return self.b(x) },
	b(&self, y: number) -> number { return y },
}`,
			errs: []string{"3:36-3:45: cannot constrain U <: number"},
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
			name: "AReturnForcingAValueIntoTheClassParameter",
			src: `class C<U> {
	v: U,
	m(&self) -> U { return 5 },
}`,
			errs: []string{"3:25-3:26: cannot constrain 5 <: U"},
		},
		{
			name: "AConstructorWritingAValueIntoTheClassParameter",
			src: `class C<U> {
	v: U,
	constructor(&mut self) { self.v = 5 },
}`,
			errs: []string{"3:36-3:37: cannot constrain 5 <: U"},
		},
		{
			// p is inferred, so it may still be inferred to U.
			name: "AnInferredParameterFlowingIntoTheClassParameter",
			src: `class C<U> {
	v: U,
	put(&mut self, p) { self.v = p },
}`,
			binding: "C",
			want:    "<U> {new (v: U) -> C<U>}",
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

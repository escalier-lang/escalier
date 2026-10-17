package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSubclassImplicitConstructor covers the constructor a subclass that declares none gets.
// It takes its superclass constructor's parameters, read at the arguments the `extends`
// clause writes, followed by one parameter per required field the subclass declares.
func TestSubclassImplicitConstructor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "SynthesizedBase",
			src: `class Base { x: number }
class Derived extends Base { y: number }`,
			want: map[string]string{"Derived": "{new (x: number, y: number) -> Derived}"},
		},
		{
			name: "ExplicitBase",
			src: `class Base {
    x: number,
    constructor(&mut self, seed: string) { self.x = 1 },
}
class Derived extends Base { y: number }`,
			want: map[string]string{"Derived": "{new (seed: string, y: number) -> Derived}"},
		},
		{
			name: "ChainOfSubclasses",
			src: `class Base { x: number }
class Mid extends Base { y: number }
class Derived extends Mid { z: string }`,
			want: map[string]string{
				"Mid":     "{new (x: number, y: number) -> Mid}",
				"Derived": "{new (x: number, y: number, z: string) -> Derived}",
			},
		},
		{
			name: "FieldlessSubclass",
			src: `class Base { x: number }
class Derived extends Base {}`,
			want: map[string]string{"Derived": "{new (x: number) -> Derived}"},
		},
		{
			name: "OptionalAndStaticFieldsTakeNoParameter",
			src: `class Base { x: number }
class Derived extends Base { y?: number, static z: number = 1, w: string }`,
			want: map[string]string{"Derived": "{new (x: number, w: string) -> Derived, z: number}"},
		},
		{
			name: "GenericBaseAtTheSubclassParameter",
			src: `class Base<T> { v: T }
class Derived<T> extends Base<T> { w: number }`,
			want: map[string]string{"Derived": "{new <T>(v: T, w: number) -> Derived<T>}"},
		},
		{
			name: "GenericBaseAtAConcreteArgument",
			src: `class Base<T> { v: T }
class Derived extends Base<string> { w: number }`,
			want: map[string]string{"Derived": "{new (v: string, w: number) -> Derived}"},
		},
		{
			name: "OptionalBaseParameterWithNoOwnFields",
			src: `class Base {
    x: number,
    constructor(&mut self, seed?: number) { self.x = 1 },
}
class Derived extends Base {}`,
			want: map[string]string{"Derived": "{new (seed?: number) -> Derived}"},
		},
		{
			name: "ConstructingTheSubclass",
			src: `class Base { x: number }
class Derived extends Base { y: string }
val d = Derived(1, "a")
val x = d.x
val y = d.y`,
			want: map[string]string{"x": "number", "y": "string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestSubclassImplicitConstructorErrors covers a subclass whose constructor cannot be
// synthesized, which has to declare one.
func TestSubclassImplicitConstructorErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "NameCollision",
			src: `class Base {
    n: number,
    constructor(&mut self, x: number) { self.n = x },
}
class Derived extends Base { x: string }`,
			want: []string{"5:7-5:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"a field of `Derived` and a parameter of the constructor of `Base` are both named `x`"},
		},
		{
			name: "OverloadedBaseConstructor",
			src: `class Base {
    n: number,
    constructor(&mut self, x: number) { self.n = x },
    constructor(&mut self, s: string) { self.n = 1 },
}
class Derived extends Base { y: number }`,
			want: []string{"6:7-6:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"`Base` declares more than one constructor"},
		},
		{
			name: "UnstableComputedKey",
			src: `declare val keys: {readonly k: unique symbol}
class Base { x: number }
class Derived extends Base { [keys.k]: number }`,
			want: []string{"3:7-3:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"a field's computed key is neither a variable nor a property of `Symbol`"},
		},
		{
			name: "OptionalBaseParameterBeforeOwnFields",
			src: `class Base {
    n: number,
    constructor(&mut self, seed?: number) { self.n = 1 },
}
class Derived extends Base { y: number }`,
			want: []string{"5:7-5:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"parameter `seed` of the constructor of `Base` may be left out, and the fields `Derived` adds would follow it"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

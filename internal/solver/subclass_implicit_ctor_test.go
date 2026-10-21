package solver

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestSubclassImplicitConstructor covers the constructor a subclass that declares none gets
// when its superclass's constructor was synthesized too. It takes the superclass's
// parameters, read at the arguments the `extends` clause writes, followed by one parameter
// per required field the subclass adds. A field the subclass redeclares takes its value
// through the superclass's parameter for it.
func TestSubclassImplicitConstructor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "AddedField",
			src: `class Base { foo: string }
class Derived extends Base { bar: number }`,
			want: map[string]string{"Derived": "{new (foo: string, bar: number) -> Derived}"},
		},
		{
			name: "RedeclaredField",
			src: `class Base { foo: string }
class Derived extends Base { foo: string, bar: number }`,
			want: map[string]string{"Derived": "{new (foo: string, bar: number) -> Derived}"},
		},
		{
			// The subclass's narrower type is the one its constructor accepts. Only a readonly
			// field may narrow, since a write through the superclass still has to fit.
			name: "NarrowingRedeclaration",
			src: `class Base { readonly foo: number | string }
class Derived extends Base { readonly foo: string, bar: number }`,
			want: map[string]string{"Derived": "{new (foo: string, bar: number) -> Derived}"},
		},
		{
			// An optional field takes no parameter in the superclass, so redeclaring it
			// required gives the subclass a parameter of its own.
			name: "OptionalFieldRedeclaredRequired",
			src: `class Base { foo?: string, n: number }
class Derived extends Base { foo: string }`,
			want: map[string]string{"Derived": "{new (n: number, foo: string) -> Derived}"},
		},
		{
			name: "ChainOfSubclasses",
			src: `class Base { x: number }
class Mid extends Base { y: number }
class Leaf extends Mid { z: string }`,
			want: map[string]string{
				"Mid":  "{new (x: number, y: number) -> Mid}",
				"Leaf": "{new (x: number, y: number, z: string) -> Leaf}",
			},
		},
		{
			name: "FieldlessSubclass",
			src: `class Base { x: number }
class Derived extends Base {}`,
			want: map[string]string{"Derived": "{new (x: number) -> Derived}"},
		},
		{
			name: "FieldlessBase",
			src: `class Base {}
class Derived extends Base { y: number }`,
			want: map[string]string{"Derived": "{new (y: number) -> Derived}"},
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
			name: "NamespacedBase",
			src: `namespace geo { class Base { x: number } }
class Derived extends geo.Base { y: number }`,
			want: map[string]string{"Derived": "{new (x: number, y: number) -> Derived}"},
		},
		{
			// The superclass constructs the subclass, so the two share a component.
			name: "BaseInTheSameComponent",
			src: `class Base { x: number, make(&self) { return Derived(1, 2) } }
class Derived extends Base { y: number }`,
			want: map[string]string{"Derived": "{new (x: number, y: number) -> Derived}"},
		},
		{
			// A computed key is evaluated once beside the class, so any key shape gets a
			// constructor, and a subclass can build on it.
			name: "BaseWithAComputedKey",
			src: `declare val keys: {readonly k: unique symbol}
class Base { [keys.k]: number }
class Derived extends Base { y: number }`,
			want: map[string]string{"Derived": "{new (_field1: number, y: number) -> Derived}"},
		},
		{
			name: "SubclassWithAComputedKey",
			src: `declare val keys: {readonly k: unique symbol}
class Base { x: number }
class Derived extends Base { [keys.k]: number }`,
			want: map[string]string{"Derived": "{new (x: number, _field1: number) -> Derived}"},
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

// TestSubclassImplicitConstructorOverImportedBase covers a subclass whose superclass another
// package exports, which the subclass names through the package's namespace.
func TestSubclassImplicitConstructorOverImportedBase(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "geometry"
			class Point3 extends geometry.Point { z: number }
			val p = Point3(1, 2, 3)
		`),
		sourceOf(t, map[string]string{
			"geometry": `
				export class Point {
					x: number,
					y: number,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "{new (x: number, y: number, z: number) -> Point3}",
		soltype.Print(inferredValueType(t, res.Scope, "Point3")))
}

// TestSubclassImplicitConstructorInsideAPackageNamespace asserts that a subclass in a
// package namespace synthesizes its constructor over the sibling superclass in that
// namespace. The package also declares a root-level `Base` whose constructor takes a
// `string`, which `extends Base` inside `geo` does not name.
func TestSubclassImplicitConstructorInsideAPackageNamespace(t *testing.T) {
	t.Parallel()

	source := preludeFallback(t, func(uri string) (*ast.Module, string, error) {
		if uri != "shapes" {
			return nil, "", fmt.Errorf("no such package")
		}
		return parseModuleFiles(t, map[string]string{
			"lib/index.esc": `
				export class Base {
					tag: string,
					constructor(&mut self, tag: string) { self.tag = tag },
				}
			`,
			"lib/geo/shapes.esc": `
				export class Base {
					sides: number,
				}
				export class Square extends Base {
					label: string,
				}
			`,
		}), "lib/index.esc", nil
	})

	res := InferModuleWithSource(parseModule(t, `
		import "shapes"
		val ok = shapes.geo.Square(4, "square")
		val bad = shapes.geo.Square("four", "square")
	`), source)

	require.Equal(t, []string{`cannot constrain "four" <: number`}, errorMessagesOf(res.Errors))
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
			name: "ExplicitBaseConstructor",
			src: `class Base {
    n: number,
    constructor(&mut self, seed: string) { self.n = 1 },
}
class Derived extends Base { y: number }`,
			want: []string{"5:7-5:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"the constructor of `Base` is not synthesized"},
		},
		{
			// The superclass's own constructor could not be synthesized either.
			name: "BaseConstructorNotSynthesized",
			src: `class Base {
    n: number,
    constructor(&mut self) { self.n = 1 },
}
class Mid extends Base { y: number }
class Leaf extends Mid { z: number }`,
			want: []string{
				"5:7-5:10: class `Mid` must declare a constructor, since none can be synthesized: " +
					"the constructor of `Base` is not synthesized",
				"6:7-6:11: class `Leaf` must declare a constructor, since none can be synthesized: " +
					"the constructor of `Mid` is not synthesized",
			},
		},
		{
			name: "BaseWithOnlyACallSignature",
			src: `declare class Sym { (d: string) -> Sym }
class Derived extends Sym { y: number }`,
			want: []string{"2:7-2:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"the constructor of `Sym` is not synthesized"},
		},
		{
			// A declared class has no constructor emission builds, so a subclass cannot
			// forward to one.
			name: "DeclaredBase",
			src: `declare class Base { x: number }
class Derived extends Base { y: number }`,
			want: []string{"2:7-2:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"the constructor of `Base` is not synthesized"},
		},
		{
			name: "ExtendsNamesNoClass",
			src:  `class Derived extends Nope { y: number }`,
			want: []string{"1:7-1:14: class `Derived` must declare a constructor, since none can be synthesized: " +
				"`Nope` in its `extends` clause names no class"},
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

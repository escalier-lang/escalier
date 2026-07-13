package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferEnumBasic covers a non-generic enum end to end (M5 D-Enum): the enum name
// binds as a nominal type, each variant constructor resolves under the enum namespace,
// and a constructor call yields the enum type — `Color.Hex(...)` is `Color`.
func TestInferEnumBasic(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name: "VariantConstructorAndEnumType",
			src: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				val rgb = Color.RGB
				val red: Color = Color.RGB(255, 0, 0)
				val blue = Color.Hex("#0000FF")
			`,
			wantValues: map[string]string{
				"rgb":  "fn (r: number, g: number, b: number) -> Color",
				"red":  "Color",
				"blue": "Color",
			},
			wantTypes: map[string]string{"Color": "Color"},
		},
		{
			name: "NullaryVariant",
			src: `
				enum Signal {
					Stop,
					Go,
				}
				val stop = Signal.Stop()
				val s: Signal = Signal.Go()
			`,
			wantValues: map[string]string{"stop": "Signal", "s": "Signal"},
			wantTypes:  map[string]string{"Signal": "Signal"},
		},
		{
			name: "OutOfOrderReference",
			src: `
				val red: Color = Color.RGB(255, 0, 0)
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
			`,
			wantValues: map[string]string{"red": "Color"},
			wantTypes:  map[string]string{"Color": "Color"},
		},
		{
			name: "RecursiveEnum",
			src: `
				enum Tree {
					Leaf,
					Node(left: Tree, right: Tree),
				}
				val leaf: Tree = Tree.Leaf()
				val node = Tree.Node(Tree.Leaf(), Tree.Leaf())
			`,
			wantValues: map[string]string{"leaf": "Tree", "node": "Tree"},
			wantTypes:  map[string]string{"Tree": "Tree"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, tt.wantTypes)
		})
	}
}

// TestInferEnumNominalDistinctness checks that two enums are distinct nominal types
// even when they declare a variant of the same name: a value of one enum never
// satisfies the other's annotation.
func TestInferEnumNominalDistinctness(t *testing.T) {
	_, _, errs := inferSource(t, `
		enum Color { RGB(r: number) }
		enum Palette { RGB(r: number) }
		val bad: Color = Palette.RGB(1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain Palette <: Color", errs[0].Message())
}

// TestInferEnumSharedVariantName checks that two enums declaring a variant of the same
// name do not collide: each `RGB` constructor resolves under its own enum namespace with
// its own signature and produces its own enum type. The two constructors differ in arity
// (three params vs two), which they could not if they shared one registry entry.
func TestInferEnumSharedVariantName(t *testing.T) {
	classValues(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		enum Pixel {
			RGB(x: number, y: number),
			Empty,
		}
		val colorRGB = Color.RGB
		val pixelRGB = Pixel.RGB
		val c = Color.RGB(1, 2, 3)
		val p = Pixel.RGB(4, 5)
	`, map[string]string{
		"colorRGB": "fn (r: number, g: number, b: number) -> Color",
		"pixelRGB": "fn (x: number, y: number) -> Pixel",
		"c":        "Color",
		"p":        "Pixel",
	}, map[string]string{
		"Color": "Color",
		"Pixel": "Pixel",
	})
}

// TestEnumVariantDisplay locks in the qualified rendering of an enum variant token: a
// variant prints as `Enum.Variant` — its enum plus its own name — so two enums sharing
// a variant name stay distinct wherever a variant surfaces, such as a narrowed `match`
// arm (D2). A plain class token still prints under its bare name.
func TestEnumVariantDisplay(t *testing.T) {
	variant := &soltype.ClassType{Name: "Geometry.Color.RGB", Final: true, Variant: true}
	require.Equal(t, "Color.RGB", soltype.Print(variant))

	class := &soltype.ClassType{Name: "Geometry.Point"}
	require.Equal(t, "Point", soltype.Print(class))
}

// TestInferEnumGeneric checks that a generic enum's constructor is generalized like a
// plain generic value, so each construction freshens the enum's type parameter and the
// argument's type flows into the enum instance.
func TestInferEnumGeneric(t *testing.T) {
	classValues(t, `
		enum MyOption<T> {
			Some(value: T),
			None,
		}
		val some = MyOption.Some(5)
		val str = MyOption.Some("hi")
	`, map[string]string{
		"some": "MyOption<5>",
		"str":  `MyOption<"hi">`,
	}, nil)
}

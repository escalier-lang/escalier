package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferEnumBasic covers a non-generic enum end to end (M5 D-Enum): the enum name
// binds as a nominal type, each variant constructor resolves under the enum namespace
// and constructs a value of the enum-qualified variant type, and a variant value flows
// into the enum type through the nominal subtype edge.
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
				"rgb":  "fn (r: number, g: number, b: number) -> Color.RGB",
				"red":  "Color",
				"blue": "Color.Hex",
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
			wantValues: map[string]string{"stop": "Signal.Stop", "s": "Signal"},
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
			wantValues: map[string]string{"leaf": "Tree", "node": "Tree.Node"},
			wantTypes:  map[string]string{"Tree": "Tree"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, tt.wantTypes)
		})
	}
}

// TestInferEnumSubtyping checks the nominal subtype edge each variant carries: a value
// of a variant type is a subtype of its enum, and a variant of a different enum — even
// one sharing a variant name — is not.
func TestInferEnumSubtyping(t *testing.T) {
	t.Run("VariantIsSubtypeOfEnum", func(t *testing.T) {
		// The annotated binding constrains the variant value against the enum, so a clean
		// inference proves Color.Hex <: Color.
		classValues(t, `
			enum Color {
				RGB(r: number, g: number, b: number),
				Hex(code: string),
			}
			val c: Color = Color.Hex("#fff")
		`, map[string]string{"c": "Color"}, nil)
	})

	t.Run("VariantOfOtherEnumRejected", func(t *testing.T) {
		// Both enums declare an `RGB` variant; qualifying by enum keeps them distinct, so
		// a Palette.RGB never satisfies a Color annotation.
		_, _, errs := inferSource(t, `
			enum Color { RGB(r: number) }
			enum Palette { RGB(r: number) }
			val bad: Color = Palette.RGB(1)
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain Palette.RGB <: Color", errs[0].Message())
	})
}

// TestInferEnumGeneric checks that a generic enum's constructor is generalized like a
// plain generic value, so each construction freshens the enum's type parameter and the
// argument's type flows into the variant instance.
func TestInferEnumGeneric(t *testing.T) {
	classValues(t, `
		enum MyOption<T> {
			Some(value: T),
			None,
		}
		val some = MyOption.Some(5)
		val str = MyOption.Some("hi")
	`, map[string]string{
		"some": "MyOption.Some<5>",
		"str":  `MyOption.Some<"hi">`,
	}, nil)
}

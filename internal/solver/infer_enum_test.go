package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferEnumBasic covers a non-generic enum end to end (M5 D-Enum): the enum name
// binds as a type to the union of its variants, each variant constructor resolves and
// constructs a value of the variant type, and a variant value flows into the enum type.
func TestInferEnumBasic(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name: "VariantConstructorAndUnionType",
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
				"rgb":  "fn (r: number, g: number, b: number) -> RGB",
				"red":  "RGB | Hex",
				"blue": "Hex",
			},
			wantTypes: map[string]string{"Color": "RGB | Hex"},
		},
		{
			name: "NullaryVariant",
			src: `
				enum Signal {
					Stop,
					Go,
				}
				val stop = Signal.Stop()
			`,
			wantValues: map[string]string{"stop": "Stop"},
			wantTypes:  map[string]string{"Signal": "Stop | Go"},
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
			wantValues: map[string]string{"red": "RGB | Hex"},
			wantTypes:  map[string]string{"Color": "RGB | Hex"},
		},
		{
			name: "RecursiveEnum",
			src: `
				enum Tree {
					Leaf,
					Node(left: Tree, right: Tree),
				}
				val leaf: Tree = Tree.Leaf()
			`,
			wantValues: map[string]string{"leaf": "Leaf | Node"},
			wantTypes:  map[string]string{"Tree": "Leaf | Node"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, tt.wantTypes)
		})
	}
}

// TestInferEnumSubtyping checks the nominal relationship the enum union rides on: a
// value of a variant type is a subtype of its own enum, and a variant of a different
// enum is not.
func TestInferEnumSubtyping(t *testing.T) {
	t.Run("VariantIsSubtypeOfEnum", func(t *testing.T) {
		// The annotated binding constrains the variant value against the enum union, so a
		// clean inference proves RGB <: (RGB | Hex).
		classValues(t, `
			enum Color {
				RGB(r: number, g: number, b: number),
				Hex(code: string),
			}
			val c: Color = Color.Hex("#fff")
		`, map[string]string{"c": "RGB | Hex"}, nil)
	})

	t.Run("VariantOfOtherEnumRejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			enum A { X, Y }
			enum B { Z }
			val bad: B = A.X()
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "cannot constrain X <: Z", errs[0].Message())
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
		"some": "Some<5>",
		"str":  `Some<"hi">`,
	}, nil)
}

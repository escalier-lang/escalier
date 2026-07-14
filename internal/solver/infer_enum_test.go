package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferEnumBasic covers a non-generic enum end to end (M5 D-Enum): the enum name
// binds as a namespace and its type binds to the union of its variants, each variant
// constructor resolves under the namespace, and a constructor call yields the enum
// union — `Color.Hex(...)` is `Color.RGB | Color.Hex`.
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
				"rgb":  "fn (r: number, g: number, b: number) -> Color.RGB | Color.Hex",
				"red":  "Color.RGB | Color.Hex",
				"blue": "Color.RGB | Color.Hex",
			},
			wantTypes: map[string]string{"Color": "Color.RGB | Color.Hex"},
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
			wantValues: map[string]string{
				"stop": "Signal.Stop | Signal.Go",
				"s":    "Signal.Stop | Signal.Go",
			},
			wantTypes: map[string]string{"Signal": "Signal.Stop | Signal.Go"},
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
			wantValues: map[string]string{"red": "Color.RGB | Color.Hex"},
			wantTypes:  map[string]string{"Color": "Color.RGB | Color.Hex"},
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
			wantValues: map[string]string{
				"leaf": "Tree.Leaf | Tree.Node",
				"node": "Tree.Leaf | Tree.Node",
			},
			wantTypes: map[string]string{"Tree": "Tree.Leaf | Tree.Node"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, tt.wantTypes)
		})
	}
}

// TestInferEnumMutuallyRecursive checks that two enums whose variants reference each
// other are inferred correctly: each enum's union type is bound before either enum's
// variant parameters are resolved, so `A`'s `ANode(next: B)` and `B`'s `BNode(next: A)`
// both resolve the sibling enum. A single-pass walk would report the later enum's name
// as an unknown type.
func TestInferEnumMutuallyRecursive(t *testing.T) {
	classValues(t, `
		enum A {
			AEnd,
			ANode(next: B),
		}
		enum B {
			BEnd,
			BNode(next: A),
		}
		val a: A = A.AEnd()
		val b = B.BNode(A.AEnd())
		val ctorA = A.ANode
	`, map[string]string{
		"a":     "A.AEnd | A.ANode",
		"b":     "B.BEnd | B.BNode",
		"ctorA": "fn (next: B.BEnd | B.BNode) -> A.AEnd | A.ANode",
	}, map[string]string{
		"A": "A.AEnd | A.ANode",
		"B": "B.BEnd | B.BNode",
	})
}

// TestInferEnumClassMutuallyRecursive checks that an enum and a class that reference each
// other are inferred correctly: the enum variant `Branch(node: Node)` names the class and
// the class field `left: Tree` names the enum, forming one recursive group. Every nominal
// identity — the enum union and the class token — is pre-bound before any body resolves a
// reference, so both directions type-check.
func TestInferEnumClassMutuallyRecursive(t *testing.T) {
	classValues(t, `
		enum Tree {
			Leaf(value: number),
			Branch(node: Node),
		}
		class Node {
			left: Tree,
			right: Tree,
		}
		val node = Node(Tree.Leaf(1), Tree.Leaf(2))
		val branch: Tree = Tree.Branch(node)
		val treeCtor = Tree.Branch
		val kids = node.left
	`, map[string]string{
		"Node":     "fn (left: Tree.Leaf | Tree.Branch, right: Tree.Leaf | Tree.Branch) -> Node",
		"node":     "Node",
		"branch":   "Tree.Leaf | Tree.Branch",
		"treeCtor": "fn (node: Node) -> Tree.Leaf | Tree.Branch",
		"kids":     "Tree.Leaf | Tree.Branch",
	}, map[string]string{
		"Tree": "Tree.Leaf | Tree.Branch",
		"Node": "Node",
	})
}

// TestInferEnumSharedVariantName checks that two enums declaring a variant of the same
// name do not collide: each `RGB` constructor resolves under its own enum namespace with
// its own signature and produces its own enum union. The two constructors differ in
// arity (three params vs two), which they could not if they shared one registry entry.
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
		"colorRGB": "fn (r: number, g: number, b: number) -> Color.RGB | Color.Hex",
		"pixelRGB": "fn (x: number, y: number) -> Pixel.RGB | Pixel.Empty",
		"c":        "Color.RGB | Color.Hex",
		"p":        "Pixel.RGB | Pixel.Empty",
	}, map[string]string{
		"Color": "Color.RGB | Color.Hex",
		"Pixel": "Pixel.RGB | Pixel.Empty",
	})
}

// TestInferEnumNominalDistinctness checks that two enums are distinct types even when
// they declare a variant of the same name: a value of one enum never satisfies the
// other's annotation, and the qualified variant name in the error keeps the two `RGB`s
// apart.
func TestInferEnumNominalDistinctness(t *testing.T) {
	_, _, errs := inferSource(t, `
		enum Color { RGB(r: number), Hex(code: string) }
		enum Palette { RGB(r: number) }
		val bad: Color = Palette.RGB(1)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain Palette.RGB <: Color.RGB | Color.Hex", errs[0].Message())
}

// TestInferEnumUnnamedParams checks that a variant with several parameters that carry no
// single name — here two wildcards — gives each constructor parameter a distinct
// positional name rather than colliding on one shared name.
func TestInferEnumUnnamedParams(t *testing.T) {
	classValues(t, `
		enum E {
			Pair(_: number, _: string),
		}
		val ctor = E.Pair
	`, map[string]string{
		"ctor": "fn (arg0: number, arg1: string) -> E.Pair",
	}, nil)
}

// TestInferEnumInFunctionBodyRejected checks that a local enum inside a function body is
// rejected, the same as a local class: enum declarations are supported at module top
// level and script top level, not inside a function body.
func TestInferEnumInFunctionBodyRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f() {
			enum Local { A, B }
			return 0
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Declaration not allowed in function body: EnumDecl", errs[0].Message())
}

// TestEnumVariantDisplay locks in the qualified rendering of an enum variant token: a
// variant prints as `Enum.Variant` — its enum plus its own name — so two enums sharing
// a variant name stay distinct wherever a variant surfaces, such as a union member or a
// narrowed `match` arm. A plain class token still prints under its bare name.
func TestEnumVariantDisplay(t *testing.T) {
	variant := &soltype.ClassType{Name: "Geometry.Color.RGB", Final: true, Variant: true}
	require.Equal(t, "Color.RGB", soltype.Print(variant))

	class := &soltype.ClassType{Name: "Geometry.Point"}
	require.Equal(t, "Point", soltype.Print(class))
}

// TestInferEnumGeneric checks that a generic enum's constructor is generalized like a
// plain generic value, so each construction freshens the enum's type parameter and the
// argument's type flows into the enum union. Without type aliases the enum name expands
// to its union at each use, so the parameter also reaches the sibling variant — `None`
// prints parameterized here; naming the union to hide that waits on M7's aliases.
func TestInferEnumGeneric(t *testing.T) {
	classValues(t, `
		enum MyOption<T> {
			Some(value: T),
			None,
		}
		val some = MyOption.Some(5)
		val str = MyOption.Some("hi")
	`, map[string]string{
		"some": "MyOption.Some<5> | MyOption.None<5>",
		"str":  `MyOption.Some<"hi"> | MyOption.None<"hi">`,
	}, nil)
}

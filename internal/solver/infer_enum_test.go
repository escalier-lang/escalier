package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferEnumBasic covers a non-generic enum end to end (M5 D-Enum): the enum name
// binds as a namespace and its type binds to an alias whose body is the union of its
// variants, each variant constructor resolves under the namespace, and a constructor call
// yields the enum's alias type — `Color.Hex(...)` is `Color`.
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
				"rgb":  "{new (r: number, g: number, b: number) -> Color, [Symbol.customMatcher](subject: Color.RGB) -> [number, number, number]}",
				"red":  "Color",
				"blue": "Color",
			},
			// The test harness renders a type-alias binding as its body, so the enum's alias
			// shows the variant union it stands for. A value of the enum renders as `Color`.
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
				"stop": "Signal",
				"s":    "Signal",
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
			wantValues: map[string]string{"red": "Color"},
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
				"leaf": "Tree",
				"node": "Tree",
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
		"a":     "A",
		"b":     "B",
		"ctorA": "{new (next: B) -> A, [Symbol.customMatcher](subject: A.ANode) -> [B]}",
	}, map[string]string{
		"A": "A.AEnd | A.ANode",
		"B": "B.BEnd | B.BNode",
	})
}

// TestInferEnumClassMutuallyRecursive checks that an enum and a class that reference each
// other are inferred correctly: the enum variant `Branch(node: Node)` names the class and
// the class field `left: Tree` names the enum, forming one recursive group. Every nominal
// identity — the enum union and the class handle — is pre-bound before any body resolves a
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
		"Node":     "{new (left: Tree, right: Tree) -> Node}",
		"node":     "Node",
		"branch":   "Tree",
		"treeCtor": "{new (node: Node) -> Tree, [Symbol.customMatcher](subject: Tree.Branch) -> [Node]}",
		"kids":     "Tree",
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
		"colorRGB": "{new (r: number, g: number, b: number) -> Color, [Symbol.customMatcher](subject: Color.RGB) -> [number, number, number]}",
		"pixelRGB": "{new (x: number, y: number) -> Pixel, [Symbol.customMatcher](subject: Pixel.RGB) -> [number, number]}",
		"c":        "Color",
		"p":        "Pixel",
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
		"ctor": "{new (arg0: number, arg1: string) -> E, [Symbol.customMatcher](subject: E.Pair) -> [number, string]}",
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

// TestEnumVariantDisplay locks in the qualified rendering of an enum variant handle: a
// variant prints as `Enum.Variant` — its enum plus its own name — so two enums sharing
// a variant name stay distinct wherever a variant surfaces, such as a union member or a
// narrowed `match` arm. A plain class handle still prints under its bare name.
func TestEnumVariantDisplay(t *testing.T) {
	variant := &soltype.ClassType{Name: "Geometry.Color.RGB", Final: true, Variant: true}
	require.Equal(t, "Color.RGB", soltype.Print(variant))

	class := &soltype.ClassType{Name: "Geometry.Point"}
	require.Equal(t, "Point", soltype.Print(class))
}

// TestInferEnumGeneric checks that each construction of a generic enum's variant freshens
// the enum's type parameter, which the constructor binds as its own, and that the
// argument's type flows into the enum's alias instance. The enum name binds to an alias
// carrying the enum's type-parameter var, so a construction renders under the enum name
// with the argument's type — `MyOption.Some(5)` is `MyOption<5>`, the union hidden behind
// the alias.
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

// TestInferEnumVariantValue covers the value a variant's name binds to: a constructor beside
// a static `[Symbol.customMatcher]`, both bound to the enum's own type parameters, and the
// payload an extractor pattern recovers through the matcher.
func TestInferEnumVariantValue(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			name: "GenericVariantBindsTheParameterOnEachSignature",
			src: `
				enum MyOption<T> {
					Some(value: T),
					None(),
				}
				val some = MyOption.Some
				val none = MyOption.None
			`,
			wantValues: map[string]string{
				"some": "{new <T>(value: T) -> MyOption<T>, [Symbol.customMatcher]<T>(subject: MyOption.Some<T>) -> [T]}",
				"none": "{new <T>() -> MyOption<T>, [Symbol.customMatcher]<T>(subject: MyOption.None<T>) -> []}",
			},
		},
		{
			name: "MatchReadsAGenericPayloadAtTheScrutineesArgument",
			src: `
				enum MyOption<T> {
					Some(value: T),
					None(),
				}
				declare val option: MyOption<number>
				val result = match option {
					MyOption.Some(value) => value,
					MyOption.None() => 0,
				}
			`,
			wantValues: map[string]string{"result": "number"},
		},
		{
			name: "MatchReadsEachVariantsPayload",
			src: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				declare val color: Color
				val result = match color {
					Color.RGB(r, g, b) => r + g + b,
					Color.Hex(code) => code,
				}
			`,
			wantValues: map[string]string{"result": "number | string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, nil)
		})
	}
}

// TestExtractorOverGenericClass covers extractor patterns over a generic class. Each reads
// the field at the scrutinee's argument, and two arms of one match naming the class each
// bind their own argument rather than sharing one variable.
func TestExtractorOverGenericClass(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
	}{
		{
			name: "DeclaredScrutinee",
			src: `
				class Box<T> { value: T }
				declare val b: Box<string>
				val first = match b { Box(v) => v }
				val second = match b { Box(v) => v }
			`,
			wantValues: map[string]string{"first": "string", "second": "string"},
		},
		{
			name: "TwoArmsOverAConstructedScrutinee",
			src: `
				class Box<T> { value: T }
				val b = Box("s")
				val r = match b {
					Box(v) if v == "a" => 1,
					Box(w) => w,
				}
			`,
			wantValues: map[string]string{"r": `1 | "s"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classValues(t, tt.src, tt.wantValues, nil)
		})
	}
}

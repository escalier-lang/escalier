package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// knot builds `μ<name>.<body>` where body is produced from the binder, so a test reads as the μ
// form it asserts. The callback receives the binder's reference node, which is the same node the
// body must name for the knot to be recursive.
func knot(id int, name string, body func(ref *RecursiveVarType) Type) *RecursiveType {
	binder := &RecursiveVarType{ID: id, Name: name}
	return &RecursiveType{Binder: binder, Body: body(binder)}
}

// TestPrintRecursive covers the μ form's rendering: the binder's name, the reference inside the
// body, the unnamed binder's debug fallback, and the parens a knot needs inside a union or
// intersection. It needs them because its body is greedy the way a function's return type is.
func TestPrintRecursive(t *testing.T) {
	selfNext := knot(0, "X0", func(ref *RecursiveVarType) Type {
		return &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "next", Type: ref}}}
	})
	tests := []struct {
		name string
		in   Type
		want string
	}{
		{
			name: "self-referential object",
			in:   selfNext,
			want: "μX0.{next: X0}",
		},
		{
			name: "unnamed binder falls back to the r{ID} debug form",
			in: knot(7, "", func(ref *RecursiveVarType) Type {
				return &TupleType{Elems: []Type{ref}}
			}),
			want: "μr7.[r7]",
		},
		{
			name: "nested knots each name their own binder",
			in: knot(0, "X0", func(outer *RecursiveVarType) Type {
				return &ObjectType{Elems: []ObjTypeElem{
					&PropertyElem{Name: "up", Type: outer},
					&PropertyElem{Name: "down", Type: knot(1, "X1", func(inner *RecursiveVarType) Type {
						return &TupleType{Elems: []Type{outer, inner}}
					})},
				}}
			}),
			want: "μX0.{up: X0, down: μX1.[X0, X1]}",
		},
		{
			// The knot's body would swallow the `| number` tail, so a union member parenthesizes it.
			name: "knot inside a union is parenthesized",
			in:   &UnionType{Types: []Type{selfNext, numP()}},
			want: "(μX0.{next: X0}) | number",
		},
		{
			name: "knot inside an intersection is parenthesized",
			in:   &IntersectionType{Types: []Type{selfNext, numP()}},
			want: "(μX0.{next: X0}) & number",
		},
		{
			// A knot nested in a property is delimited by the braces, so it needs no parens.
			name: "knot in a property needs no parens",
			in:   &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "head", Type: selfNext}}},
			want: "{head: μX0.{next: X0}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// TestLevelOfRecursive pins the split that lets a knot cross a level boundary. The body's variables
// lift the level so the freshener and extruder prune descends into them, while the binder and its
// references contribute nothing.
func TestLevelOfRecursive(t *testing.T) {
	inner := &TypeVarType{ID: 0, Level: 3}
	tests := []struct {
		name string
		ty   Type
		want int
	}{
		{
			name: "bare binder reference is level 0",
			ty:   &RecursiveVarType{ID: 0, Name: "X0"},
			want: 0,
		},
		{
			name: "knot over a variable-free body is level 0",
			ty: knot(0, "X0", func(ref *RecursiveVarType) Type {
				return &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "next", Type: ref}}}
			}),
			want: 0,
		},
		{
			name: "knot takes its body's level",
			ty: knot(0, "X0", func(ref *RecursiveVarType) Type {
				return &ObjectType{Elems: []ObjTypeElem{
					&PropertyElem{Name: "next", Type: ref},
					&PropertyElem{Name: "value", Type: inner},
				}}
			}),
			want: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, LevelOf(tt.ty))
		})
	}
}

// TestAcceptRecursive pins the visitor arm: the body is rewritten, the binder carries through
// unchanged, and an unchanged body keeps the knot's pointer so identity-keyed caches stay valid.
func TestAcceptRecursive(t *testing.T) {
	original := knot(0, "X0", func(ref *RecursiveVarType) Type {
		return &ObjectType{Elems: []ObjTypeElem{
			&PropertyElem{Name: "next", Type: ref},
			&PropertyElem{Name: "value", Type: numP()},
		}}
	})

	t.Run("body is rewritten and the binder is preserved", func(t *testing.T) {
		out := original.Accept(&primSwapper{}, Positive)
		rewritten, ok := out.(*RecursiveType)
		require.True(t, ok, "the visit must rebuild a knot, got %T", out)
		require.Same(t, original.Binder, rewritten.Binder)
		require.Equal(t, "μX0.{next: X0, value: string}", Print(rewritten))
	})

	t.Run("an unchanged body keeps the knot's pointer", func(t *testing.T) {
		require.Same(t, original, original.Accept(identityVisitor{}, Positive))
	})
}

// primSwapper rewrites every number to a string, so a visit that reaches a knot's body is visible
// in the rendered result.
type primSwapper struct{}

func (s *primSwapper) EnterType(t Type, _ Polarity) EnterResult { return EnterResult{} }

func (s *primSwapper) ExitType(t Type, _ Polarity) Type {
	if p, ok := t.(*PrimType); ok && p.Prim == NumPrim {
		return strP()
	}
	return t
}

package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLevelOf(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	v2 := &TypeVarType{ID: 0, Level: 2}
	v5 := &TypeVarType{ID: 1, Level: 5}

	tests := []struct {
		name string
		ty   Type
		want int
	}{
		{
			name: "primitive leaf is level 0",
			ty:   num,
			want: 0,
		},
		{
			name: "literal leaf is level 0",
			ty:   &LitType{Lit: &NumLit{Value: 5}},
			want: 0,
		},
		{
			name: "void leaf is level 0",
			ty:   &Void{},
			want: 0,
		},
		{
			name: "bare variable is its own level",
			ty:   v5,
			want: 5,
		},
		{
			name: "function: max over params and return",
			ty: &FuncType{
				Params: []*FuncParam{
					{Pattern: &IdentPat{Name: "x"}, Type: v2},
					{Pattern: &IdentPat{Name: "y"}, Type: num},
				},
				Ret: v5,
			},
			want: 5,
		},
		{
			name: "function with concrete-only signature is level 0",
			ty: &FuncType{
				Params: []*FuncParam{{Pattern: &IdentPat{Name: "x"}, Type: num}},
				Ret:    num,
			},
			want: 0,
		},
		{
			name: "tuple: max over elements",
			ty:   &TupleType{Elems: []Type{num, v2, v5}},
			want: 5,
		},
		{
			name: "nested function inside tuple",
			ty: &TupleType{Elems: []Type{
				&FuncType{
					Params: []*FuncParam{{Pattern: &IdentPat{Name: "x"}, Type: v5}},
					Ret:    num,
				},
				v2,
			}},
			want: 5,
		},
		{
			// LevelOf must descend into ObjectType property types — its arm drives
			// freshenAbove's level prune, and a wrong 0 would let a Level>0 object be
			// shared and aliased across instantiations (the IntersectionType comment
			// in LevelOf explains the same hazard).
			name: "object: max over property types",
			ty: &ObjectType{Elems: []ObjTypeElem{
				&PropertyElem{Name: "a", Type: num},
				&PropertyElem{Name: "b", Type: v2},
				&PropertyElem{Name: "c", Type: v5},
			}},
			want: 5,
		},
		{
			name: "object with concrete-only properties is level 0",
			ty: &ObjectType{Elems: []ObjTypeElem{
				&PropertyElem{Name: "a", Type: num},
			}},
			want: 0,
		},
		{
			name: "nested function inside object",
			ty: &ObjectType{Elems: []ObjTypeElem{
				&PropertyElem{Name: "f", Type: &FuncType{
					Params: []*FuncParam{{Pattern: &IdentPat{Name: "x"}, Type: v5}},
					Ret:    num,
				}},
			}},
			want: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, LevelOf(tt.ty))
		})
	}
}

func TestTypeVarTypeBoundsAt(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	v := &TypeVarType{
		ID:          0,
		Level:       0,
		LowerBounds: []Type{num},
		UpperBounds: []Type{str},
	}

	require.Equal(t, v.LowerBounds, v.BoundsAt(Positive))
	require.Equal(t, v.UpperBounds, v.BoundsAt(Negative))
}

func TestLitTypeEqual(t *testing.T) {
	tests := []struct {
		name string
		a    *LitType
		b    *LitType
		want bool
	}{
		{
			name: "equal numbers",
			a:    &LitType{Lit: &NumLit{Value: 5}},
			b:    &LitType{Lit: &NumLit{Value: 5}},
			want: true,
		},
		{
			name: "different numbers",
			a:    &LitType{Lit: &NumLit{Value: 5}},
			b:    &LitType{Lit: &NumLit{Value: 6}},
			want: false,
		},
		{
			name: "equal strings",
			a:    &LitType{Lit: &StrLit{Value: "hello"}},
			b:    &LitType{Lit: &StrLit{Value: "hello"}},
			want: true,
		},
		{
			name: "different strings",
			a:    &LitType{Lit: &StrLit{Value: "hello"}},
			b:    &LitType{Lit: &StrLit{Value: "world"}},
			want: false,
		},
		{
			name: "equal bools",
			a:    &LitType{Lit: &BoolLit{Value: true}},
			b:    &LitType{Lit: &BoolLit{Value: true}},
			want: true,
		},
		{
			name: "different bools",
			a:    &LitType{Lit: &BoolLit{Value: true}},
			b:    &LitType{Lit: &BoolLit{Value: false}},
			want: false,
		},
		{
			name: "different kinds: num vs str",
			a:    &LitType{Lit: &NumLit{Value: 0}},
			b:    &LitType{Lit: &StrLit{Value: ""}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.a.Equal(tt.b))
		})
	}
}

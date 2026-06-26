package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// NewRef collapses the degenerate immutable-no-lifetime cell to the bare inner, and
// keeps the wrapper for every meaningful borrow. Lt is always nil today, so the
// only meaningful borrow constructible here is the owned-mutable one.
func TestNewRef(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: &PrimType{Prim: NumPrim}}}}

	t.Run("immutable no-lifetime collapses to bare inner", func(t *testing.T) {
		require.Same(t, obj, NewRef(false, nil, obj))
	})

	t.Run("owned-mutable keeps the wrapper", func(t *testing.T) {
		got := NewRef(true, nil, obj)
		r, ok := got.(*RefType)
		require.True(t, ok, "a mutable borrow stays a *RefType")
		require.True(t, r.Mut)
		require.Nil(t, r.Lt)
		require.Same(t, obj, r.Inner)
	})
}

func TestUnwrapRef(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: &PrimType{Prim: NumPrim}}}}

	t.Run("peels a borrow into inner, mut, lt", func(t *testing.T) {
		inner, mut, lt := UnwrapRef(&RefType{Mut: true, Inner: obj})
		require.Same(t, obj, inner)
		require.True(t, mut)
		require.Nil(t, lt)
	})

	t.Run("a non-borrow returns itself, owned and immutable", func(t *testing.T) {
		inner, mut, lt := UnwrapRef(obj)
		require.Same(t, obj, inner)
		require.False(t, mut)
		require.Nil(t, lt)
	})
}

func TestCarrierOf(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: &PrimType{Prim: NumPrim}}}}

	require.Same(t, obj, CarrierOf(&RefType{Mut: true, Inner: obj}), "peels a borrow to its carrier")
	require.Same(t, obj, CarrierOf(obj), "a non-borrow is its own carrier")
}

func TestBorrowableType(t *testing.T) {
	tests := []struct {
		name string
		ty   Type
		want bool
	}{
		{"object is borrowable", &ObjectType{}, true},
		{"tuple is borrowable", &TupleType{}, true},
		{"type variable is borrowable", &TypeVarType{ID: 1}, true},
		{"primitive is not borrowable", &PrimType{Prim: NumPrim}, false},
		{"literal is not borrowable", &LitType{Lit: &NumLit{Value: 5}}, false},
		{"function is not borrowable", &FuncType{Ret: &PrimType{Prim: NumPrim}}, false},
		{"promise is not borrowable", &PromiseType{Inner: &PrimType{Prim: NumPrim}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, BorrowableType(tt.ty))
		})
	}
}

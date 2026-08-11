package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// NewRef collapses the degenerate immutable-no-lifetime cell to the bare inner, and
// keeps the wrapper for every meaningful borrow. Lt is always nil in C1, so the
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

// UnwrapRefs answers for a whole set at once, so every rejection is a case where one
// borrow cannot stand in for all of them. Only a borrow carrying a lifetime counts, which
// is what separates a real borrow from an owned-mutable cell.
func TestUnwrapRefs(t *testing.T) {
	objX := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: &PrimType{Prim: NumPrim}}}}
	objY := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "y", Type: &PrimType{Prim: StrPrim}}}}
	ltA, ltB := &LifetimeVar{ID: 1}, &LifetimeVar{ID: 2}
	borrow := func(mut bool, lt Lifetime, inner RefInner) Type {
		return &RefType{Mut: mut, Lt: lt, Inner: inner}
	}

	tests := []struct {
		name   string
		types  []Type
		want   []Type
		wantLt []Lifetime
		allMut bool
		ok     bool
	}{
		{
			name:   "immutable borrows peel to their carriers",
			types:  []Type{borrow(false, ltA, objX), borrow(false, ltB, objY)},
			want:   []Type{objX, objY},
			wantLt: []Lifetime{ltA, ltB},
		},
		{
			name:   "every member mutable reports allMut",
			types:  []Type{borrow(true, ltA, objX), borrow(true, ltB, objY)},
			want:   []Type{objX, objY},
			wantLt: []Lifetime{ltA, ltB},
			allMut: true,
		},
		{
			// One immutable member is enough to make the set immutable, since a leaf reached
			// through that member cannot be written.
			name:   "one immutable member clears allMut",
			types:  []Type{borrow(true, ltA, objX), borrow(false, ltB, objY)},
			want:   []Type{objX, objY},
			wantLt: []Lifetime{ltA, ltB},
		},
		{
			name:  "a plain value is not a borrow",
			types: []Type{borrow(false, ltA, objX), objY},
		},
		{
			// An owned-mutable `mut {…}` cell carries no lifetime, so it is a value.
			name:  "an owned-mutable cell is not a borrow",
			types: []Type{borrow(false, ltA, objX), borrow(true, nil, objY)},
		},
		{
			name:  "an empty set names no borrow",
			types: []Type{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inners, lts, allMut, ok := UnwrapRefs(tt.types)
			if tt.want == nil {
				require.False(t, ok)
				require.Nil(t, inners)
				require.Nil(t, lts)
				require.False(t, allMut)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.want, inners)
			require.Equal(t, tt.wantLt, lts)
			require.Equal(t, tt.allMut, allMut)
		})
	}
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

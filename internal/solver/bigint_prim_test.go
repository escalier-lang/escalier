package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// `bigint` is the type of every bigint. It resolves, renders under its own name, and is
// disjoint from every other primitive, so a value of one is not a value of the other.
func TestBigIntPrimitive(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ItResolvesAndRenders",
			src:  `declare fn f(n: bigint) -> bigint`,
			want: "fn (n: bigint) -> bigint",
		},
		{
			name: "ItJoinsIntoAUnion",
			src:  `type T = bigint | string`,
			want: "string | bigint",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			got, isValue := values["f"]
			if !isValue {
				got = types["T"]
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// A value of another type does not satisfy `bigint`. The report names it under the
// surface spelling rather than a placeholder.
func TestBigIntPrimitiveRejectsAnotherValue(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare fn take(n: bigint) -> number
		val n = take(1)
	`)
	require.Equal(t, []string{"cannot constrain 1 <: bigint"}, errorMessagesOf(errs))
}

// A bigint draws its values from a family of its own, so an atom from another family is
// disjoint from it and their meet is `never`. Without the arm a bigint would read as
// `notValueAtom`, which decides nothing and would leave `bigint & number` inhabited.
func TestBigIntIsItsOwnValueFamily(t *testing.T) {
	c := &Context{}
	bigint := &soltype.PrimType{Prim: soltype.BigIntPrim}

	require.Equal(t, bigintFamily, c.valueFamilyOf(bigint))
	for _, other := range []soltype.Type{
		&soltype.PrimType{Prim: soltype.StrPrim},
		&soltype.PrimType{Prim: soltype.NumPrim},
		&soltype.PrimType{Prim: soltype.BoolPrim},
		&soltype.PrimType{Prim: soltype.SymPrim},
		&soltype.NullType{},
		&soltype.UndefinedType{},
	} {
		t.Run(soltype.Print(other), func(t *testing.T) {
			require.NotEqual(t, bigintFamily, c.valueFamilyOf(other))

			met, fused := c.meetValueAtoms(bigint, other)
			require.True(t, fused, "two atoms from different families fuse")
			require.Equal(t, "never", soltype.Print(met))
		})
	}
}

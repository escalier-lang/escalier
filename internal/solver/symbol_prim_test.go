package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// `symbol` is the type of every symbol. It resolves, renders under its own name, and is
// disjoint from every other primitive, so a value of one is not a value of the other.
func TestSymbolPrimitive(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ItResolvesAndRenders",
			src:  `declare fn f(s: symbol) -> symbol`,
			want: "fn (s: symbol) -> symbol",
		},
		{
			name: "ItJoinsIntoAUnion",
			src:  `type T = symbol | string`,
			want: "string | symbol",
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

// A value of another type does not satisfy `symbol`. The report names it under the
// surface spelling rather than a placeholder.
func TestSymbolPrimitiveRejectsAnotherValue(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare fn take(s: symbol) -> number
		val n = take("x")
	`)
	require.Equal(t, []string{`cannot constrain "x" <: symbol`}, errorMessagesOf(errs))
}

// A symbol draws its values from a family of its own, so an atom from another family is
// disjoint from it and their meet is `never`. Without the arm a symbol would read as
// notValueAtom, which decides nothing and would leave `symbol & string` inhabited.
func TestSymbolIsItsOwnValueFamily(t *testing.T) {
	c := &Context{}
	symbol := &soltype.PrimType{Prim: soltype.SymPrim}

	require.Equal(t, symbolFamily, c.valueFamilyOf(symbol))
	for _, other := range []soltype.Type{
		&soltype.PrimType{Prim: soltype.StrPrim},
		&soltype.PrimType{Prim: soltype.NumPrim},
		&soltype.PrimType{Prim: soltype.BoolPrim},
		&soltype.NullType{},
		&soltype.UndefinedType{},
	} {
		t.Run(soltype.Print(other), func(t *testing.T) {
			require.NotEqual(t, symbolFamily, c.valueFamilyOf(other))

			met, fused := c.meetValueAtoms(symbol, other)
			require.True(t, fused, "two atoms from different families fuse")
			require.Equal(t, "never", soltype.Print(met))
		})
	}
}

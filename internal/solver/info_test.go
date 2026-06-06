package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

func TestInfoSetTypeAndTypeOf(t *testing.T) {
	info := NewInfo()

	a := ast.NewIdentifier("a", ast.Span{})
	b := ast.NewIdentifier("b", ast.Span{})

	numT := &soltype.PrimType{Prim: soltype.NumPrim}
	strT := &soltype.PrimType{Prim: soltype.StrPrim}

	info.setType(a, numT)
	info.setType(b, strT)

	// Each node round-trips to the type it was recorded with.
	require.Same(t, numT, info.TypeOf(a))
	require.Same(t, strT, info.TypeOf(b))

	// Overwriting an entry replaces the prior type.
	info.setType(a, strT)
	require.Same(t, strT, info.TypeOf(a))
}

func TestInfoTypeOfAbsentNodeIsNil(t *testing.T) {
	info := NewInfo()

	n := ast.NewIdentifier("missing", ast.Span{})

	// A node that was never recorded returns the zero value (nil).
	require.Nil(t, info.TypeOf(n))
}

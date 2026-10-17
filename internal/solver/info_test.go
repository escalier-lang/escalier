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

func TestInfoDeclOf(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// read is the name whose last read in src is looked up.
		read string
		// expected is the source text of the node DeclOf returns for that read.
		expected string
		// expectedErrs is the message of each error InferScript reports.
		expectedErrs []string
	}{
		{
			name:     "ScriptVal",
			src:      "val x = 5\nval y = x",
			read:     "x",
			expected: "val x = 5",
		},
		{
			name:     "ScriptFunc",
			src:      "fn add(a: number, b: number) -> number { return a + b }\nval s = add(1, 2)",
			read:     "add",
			expected: "fn add(a: number, b: number) -> number { return a + b }",
		},
		{
			name:     "Param",
			src:      "fn double(n: number) -> number { return n * 2 }",
			read:     "n",
			expected: "n",
		},
		{
			name:     "DestructuredLeaf",
			src:      "val {a, b} = {a: 1, b: 2}\nval c = b",
			read:     "b",
			expected: "b",
		},
		{
			name:     "AssignmentTarget",
			src:      "var x = 1\nx = 2",
			read:     "x",
			expected: "var x = 1",
		},
		{
			name:         "RejectedAssignmentTarget",
			src:          "val x = 1\nx = 2",
			read:         "x",
			expected:     "val x = 1",
			expectedErrs: []string{"Cannot assign to immutable binding: x"},
		},
		{
			name:     "LocalShadowsOuter",
			src:      "val x = 1\nfn f() -> number {\n\tval x = 2\n\treturn x\n}",
			read:     "x",
			expected: "val x = 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, info, errs := InferScript(parseScript(t, tt.src), testStdlibSource())
			var msgs []string
			for _, err := range errs {
				msgs = append(msgs, err.Message())
			}
			require.Equal(t, tt.expectedErrs, msgs)

			var read *ast.IdentExpr
			for ident := range info.decls {
				if ident.Name != tt.read {
					continue
				}
				if read == nil || ident.Span().Start.Offset > read.Span().Start.Offset {
					read = ident
				}
			}
			require.NotNil(t, read)

			decl, ok := info.DeclOf(read)
			require.True(t, ok)
			span := decl.Span()
			require.Equal(t, tt.expected, tt.src[span.Start.Offset:span.End.Offset])
		})
	}
}

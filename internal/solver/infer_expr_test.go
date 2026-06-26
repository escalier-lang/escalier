package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// testSpan is the fixed, non-zero placeholder span these unit tests stamp on the
// AST nodes they feed directly into the walk. It delegates to the shared
// builderSpan() (astbuild.go) so a node from a builder and a node built inline in
// a test carry the same span, keeping error-span assertions consistent.
func testSpan() ast.Span {
	return builderSpan()
}

func TestInferLiteral(t *testing.T) {
	tests := []struct {
		name string
		lit  ast.Lit
		want string
	}{
		{"number", ast.NewNumber(5, testSpan()), "5"},
		{"string", ast.NewString("hello", testSpan()), `"hello"`},
		{"boolean", ast.NewBoolean(true, testSpan()), "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			e := ast.NewLitExpr(tt.lit)
			got := c.inferExpr(NewScope(), 0, e)
			require.Empty(t, c.errs)
			require.Equal(t, tt.want, soltype.Print(got))
			// The same type is recorded in the Info side table.
			require.Equal(t, got, c.info.TypeOf(e))
		})
	}
}

// A literal kind outside the soltype.Lit set (regex, bigint, null, undefined)
// is an unsupported-subset miss, not a crash.
func TestInferLiteralUnsupported(t *testing.T) {
	c := newChecker()
	e := ast.NewLitExpr(ast.NewNull(testSpan()))
	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got) // report's recovery placeholder
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: NullLit", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferIdentResolvesBinding(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("x", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.PrimType{Prim: soltype.NumPrim})}})

	e := ast.NewIdent("x", testSpan())
	got := c.inferExpr(scope, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", soltype.Print(got))
	require.Equal(t, got, c.info.TypeOf(e))
}

func TestInferIdentResolvesThroughParent(t *testing.T) {
	c := newChecker()
	parent := NewScope()
	parent.defineValue("y", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.LitType{Lit: &soltype.StrLit{Value: "hi"}})}})
	child := parent.Child()

	e := ast.NewIdent("y", testSpan())
	got := c.inferExpr(child, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, `"hi"`, soltype.Print(got))
}

func TestInferIdentUnknown(t *testing.T) {
	c := newChecker()
	e := ast.NewIdent("nope", testSpan())
	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got) // report's recovery placeholder
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unknown identifier: nope", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferIdentNamespaceUsedAsValue(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{Name: "Foo"})

	e := ast.NewIdent("Foo", testSpan())
	got := c.inferExpr(scope, 0, e)
	require.IsType(t, &soltype.ErrorType{}, got) // report's recovery placeholder
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace used as a value: Foo", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

func TestInferExprUnsupportedNode(t *testing.T) {
	c := newChecker()
	left := ast.NewLitExpr(ast.NewNumber(1, testSpan()))
	right := ast.NewLitExpr(ast.NewNumber(2, testSpan()))
	e := ast.NewBinary(left, right, ast.Plus, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got) // report's recovery placeholder
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: BinaryExpr", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

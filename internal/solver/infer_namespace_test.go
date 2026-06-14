package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// F1: namespace member lookup (resolvePath). Namespaces never enter scope via real
// source yet (namespace declarations are unsupported in M3), so these tests
// hand-build the Namespace structure and feed path expressions straight into the
// walk — the same construction as TestInferIdentNamespaceUsedAsValue.

func numScheme() []TypeScheme {
	return []TypeScheme{monoScheme(&soltype.PrimType{Prim: soltype.NumPrim})}
}

func strScheme() []TypeScheme {
	return []TypeScheme{monoScheme(&soltype.PrimType{Prim: soltype.StrPrim})}
}

// Foo.bar resolves to the member's type.
func TestInferNamespaceMember(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{
		Name:   "Foo",
		Values: map[string]ValueBinding{"bar": {Schemes: numScheme()}},
	})

	e := memberExpr(identExpr("Foo"), "bar")
	got := c.inferExpr(scope, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", soltype.Print(got))
	require.Equal(t, got, c.info.TypeOf(e))
}

// Foo["bar"] resolves a constant-keyed member — the bracket form of Foo.bar.
func TestInferNamespaceConstantIndex(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{
		Name:   "Foo",
		Values: map[string]ValueBinding{"bar": {Schemes: strScheme()}},
	})

	e := ast.NewIndex(identExpr("Foo"), strExpr("bar"), false, testSpan())
	got := c.inferExpr(scope, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "string", soltype.Print(got))
	require.Equal(t, got, c.info.TypeOf(e))
}

// A.B.c walks through a nested namespace to the member's type.
func TestInferNestedNamespaceMember(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	inner := &Namespace{
		Name:   "A.B",
		Values: map[string]ValueBinding{"c": {Schemes: numScheme()}},
	}
	scope.defineNamespace("A", &Namespace{
		Name:   "A",
		Nested: map[string]*Namespace{"B": inner},
	})

	e := memberExpr(memberExpr(identExpr("A"), "B"), "c")
	got := c.inferExpr(scope, 0, e)
	require.Empty(t, c.errs)
	require.Equal(t, "number", soltype.Print(got))
}

// f(Foo) — a bare namespace name in value position is rejected.
func TestInferNamespaceAsValue(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{Name: "Foo"})

	got := c.inferExpr(scope, 0, identExpr("Foo"))
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace used as a value: Foo", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// f(A.B) — a partial chain stopping at a nested namespace is rejected once, with
// the nested namespace's qualified name.
func TestInferNestedNamespaceAsValue(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	inner := &Namespace{Name: "A.B"}
	scope.defineNamespace("A", &Namespace{
		Name:   "A",
		Nested: map[string]*Namespace{"B": inner},
	})

	e := memberExpr(identExpr("A"), "B")
	got := c.inferExpr(scope, 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace used as a value: A.B", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// Foo.nope — an absent member is an UnknownNamespaceMemberError.
func TestInferNamespaceUnknownMember(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineNamespace("Foo", &Namespace{
		Name:   "Foo",
		Values: map[string]ValueBinding{"bar": {Schemes: numScheme()}},
	})

	e := memberExpr(identExpr("Foo"), "nope")
	got := c.inferExpr(scope, 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace Foo has no member: nope", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// Foo[k] — a dynamic (non-constant) index into a namespace is rejected.
func TestInferNamespaceDynamicIndex(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("k", ValueBinding{Schemes: strScheme()})
	scope.defineNamespace("Foo", &Namespace{
		Name:   "Foo",
		Values: map[string]ValueBinding{"bar": {Schemes: numScheme()}},
	})

	e := ast.NewIndex(identExpr("Foo"), identExpr("k"), false, testSpan())
	got := c.inferExpr(scope, 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Namespace Foo can only be indexed by a constant string", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span())
}

// An index into a value (array element / index-signature read) is M7, still
// outside the supported subset — the namespace path doesn't accidentally accept it.
func TestInferValueIndexUnsupported(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("o", ValueBinding{Schemes: numScheme()})

	e := ast.NewIndex(identExpr("o"), numExpr(0), false, testSpan())
	got := c.inferExpr(scope, 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported in M2: IndexExpr", c.errs[0].Message())
}

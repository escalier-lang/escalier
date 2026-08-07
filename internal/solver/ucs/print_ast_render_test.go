package ucs

import (
	"math/big"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// The tests here walk each renderer's arms. A snapshot in a later IR test is only as
// trustworthy as the fragment rendering underneath it, so every form this file
// spells out gets a case, and every form it does not gets a `<NodeKind>` case that
// pins the fallback.

func TestLitString(t *testing.T) {
	tests := []struct {
		name string
		in   ast.Lit
		want string
	}{
		{"true", ast.NewBoolean(true, ast.Span{}), "true"},
		{"false", ast.NewBoolean(false, ast.Span{}), "false"},
		{"integral number", ast.NewNumber(1, ast.Span{}), "1"},
		{"fractional number", ast.NewNumber(1.5, ast.Span{}), "1.5"},
		{"string", ast.NewString("hi", ast.Span{}), `"hi"`},
		{
			// A quoted string keeps its escapes, so two literals that differ only in
			// escaping stay distinguishable.
			"string with a quote",
			ast.NewString(`a"b`, ast.Span{}),
			`"a\"b"`,
		},
		{"regex", ast.NewRegex("/ab+/", ast.Span{}), "/ab+/"},
		{"bigint", ast.NewBigInt(*big.NewInt(42), ast.Span{}), "42n"},
		{"null", ast.NewNull(ast.Span{}), "null"},
		{"undefined", ast.NewUndefined(ast.Span{}), "undefined"},
		{"nil", nil, "<nil>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, litString(test.in))
		})
	}
}

func TestUnaryOpString(t *testing.T) {
	require.Equal(t, "+", unaryOpString(ast.UnaryPlus))
	require.Equal(t, "-", unaryOpString(ast.UnaryMinus))
	require.Equal(t, "!", unaryOpString(ast.LogicalNot))
	// An operator the renderer does not know still prints something.
	require.Equal(t, "?", unaryOpString(ast.UnaryOp(99)))
}

func TestExprStringRendersEachForm(t *testing.T) {
	a := ident("a")

	tests := []struct {
		name string
		in   ast.Expr
		want string
	}{
		{"identifier", a, "a"},
		{"literal", num(2), "2"},
		{"member", ast.NewMember(a, ast.NewIdentifier("b", ast.Span{}), false, ast.Span{}), "a.b"},
		{"index", ast.NewIndex(a, num(0), false, ast.Span{}), "a[0]"},
		{"call with no args", ast.NewCall(a, nil, false, ast.Span{}), "a()"},
		{"call with args", ast.NewCall(a, []ast.Expr{num(1), num(2)}, false, ast.Span{}), "a(1, 2)"},
		{"empty tuple", ast.NewArray(nil, ast.Span{}), "[]"},
		{"tuple", ast.NewArray([]ast.Expr{num(1), a}, ast.Span{}), "[1, a]"},
		{"nil", nil, "<nil>"},
		{"unrendered form", ast.NewDo(ast.Block{}, ast.Span{}), "<DoExpr>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, exprString(test.in))
		})
	}
}

func TestPatStringRendersEachForm(t *testing.T) {
	qual := func(name string) ast.QualIdent { return ast.NewIdentifier(name, ast.Span{}) }

	tests := []struct {
		name string
		in   ast.Pat
		want string
	}{
		{"identifier", identPat("a"), "a"},
		{"mutable identifier", ast.NewIdentPat("a", true, nil, nil, ast.Span{}), "mut a"},
		{"wildcard", wildcardPat(), "_"},
		{"literal", numPat(1), "1"},
		{"empty tuple", ast.NewTuplePat(nil, ast.Span{}), "[]"},
		{"tuple", ast.NewTuplePat([]ast.Pat{identPat("a"), wildcardPat()}, ast.Span{}), "[a, _]"},
		{"tuple with a rest", ast.NewTuplePat([]ast.Pat{
			identPat("first"), ast.NewRestPat(identPat("rest"), ast.Span{}),
		}, ast.Span{}), "[first, ...rest]"},
		{"empty object", ast.NewObjectPat(nil, ast.Span{}), "{}"},
		{"object shorthand", objPat("x", "y"), "{x, y}"},
		{"object key-value", ast.NewObjectPat([]ast.ObjPatElem{
			keyValueElem("x", identPat("a")),
		}, ast.Span{}), "{x: a}"},
		{"object rest", ast.NewObjectPat([]ast.ObjPatElem{
			shorthandElem("x"), objRestElem("rest"),
		}, ast.Span{}), "{x, ...rest}"},
		{
			"extractor",
			ast.NewExtractorPat(qual("Ok"), []ast.Pat{identPat("v")}, ast.Span{}),
			"Ok(v)",
		},
		{
			"nullary extractor",
			ast.NewExtractorPat(qual("None"), nil, ast.Span{}),
			"None()",
		},
		{
			"instance",
			ast.NewInstancePat(qual("Point"), objPat("x", "y"), ast.Span{}),
			"Point {x, y}",
		},
		{"nil", nil, "<nil>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, patString(test.in))
		})
	}
}

// A nil *ast.ObjectPat renders `{}` rather than panicking, which keeps an instance
// pattern printable when its object half is missing.
func TestObjPatStringToleratesANilPattern(t *testing.T) {
	require.Equal(t, "{}", objPatString(nil))
}

func TestTypeAnnStringRendersEachForm(t *testing.T) {
	number := &ast.NumberTypeAnn{}
	str := &ast.StringTypeAnn{}

	tests := []struct {
		name string
		in   ast.TypeAnn
		want string
	}{
		{"number", number, "number"},
		{"string", str, "string"},
		{"boolean", &ast.BooleanTypeAnn{}, "boolean"},
		{"bigint", &ast.BigintTypeAnn{}, "bigint"},
		{"symbol", &ast.SymbolTypeAnn{}, "symbol"},
		{"any", &ast.AnyTypeAnn{}, "any"},
		{"unknown", &ast.UnknownTypeAnn{}, "unknown"},
		{"never", &ast.NeverTypeAnn{}, "never"},
		{"wildcard", &ast.WildcardTypeAnn{}, "_"},
		{"literal", ast.NewLitTypeAnn(ast.NewNumber(1, ast.Span{}), ast.Span{}), "1"},
		{
			"type reference",
			&ast.TypeRefTypeAnn{Name: ast.NewIdentifier("Point", ast.Span{})},
			"Point",
		},
		{
			"generic type reference",
			&ast.TypeRefTypeAnn{
				Name:     ast.NewIdentifier("Map", ast.Span{}),
				TypeArgs: []ast.TypeAnn{str, number},
			},
			"Map<string, number>",
		},
		{"empty tuple", &ast.TupleTypeAnn{}, "[]"},
		{"tuple", &ast.TupleTypeAnn{Elems: []ast.TypeAnn{number, str}}, "[number, string]"},
		{
			"inexact tuple",
			&ast.TupleTypeAnn{Elems: []ast.TypeAnn{number}, Inexact: true},
			"[number, ...]",
		},
		{"union", &ast.UnionTypeAnn{Types: []ast.TypeAnn{number, str}}, "number | string"},
		{
			"inexact union",
			&ast.UnionTypeAnn{Types: []ast.TypeAnn{number}, Inexact: true},
			"number | ...",
		},
		{
			"intersection",
			&ast.IntersectionTypeAnn{Types: []ast.TypeAnn{number, str}},
			"number & string",
		},
		{"nil", nil, "<nil>"},
		{"unrendered form", &ast.KeyOfTypeAnn{}, "<KeyOfTypeAnn>"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, typeAnnString(test.in))
		})
	}
}

func TestBodyStringAndStmtString(t *testing.T) {
	tests := []struct {
		name string
		in   ast.BlockOrExpr
		want string
	}{
		{"bare expression", exprBody(num(1)), "1"},
		{"block with one statement", blockBody(num(1)), "{ 1 }"},
		{
			"block with several statements",
			ast.BlockOrExpr{Block: &ast.Block{Stmts: []ast.Stmt{
				ast.NewExprStmt(num(1), ast.Span{}),
				ast.NewReturnStmt(num(2), ast.Span{}),
			}}},
			"{ 1; return 2 }",
		},
		{
			"bare return",
			ast.BlockOrExpr{Block: &ast.Block{Stmts: []ast.Stmt{
				ast.NewReturnStmt(nil, ast.Span{}),
			}}},
			"{ return }",
		},
		{"empty block", ast.BlockOrExpr{Block: &ast.Block{}}, "{  }"},
		{
			// Neither half set is what an arm with no body looks like, which the
			// renderer names rather than crashing on.
			"neither expression nor block",
			ast.BlockOrExpr{},
			"<empty>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, bodyString(test.in))
		})
	}
}

// A statement form the renderer does not spell out prints as its node kind, and a nil
// statement prints `<nil>` rather than panicking.
func TestStmtStringFallbacks(t *testing.T) {
	require.Equal(t, "<nil>", stmtString(nil))
	require.Equal(t, "<DeclStmt>", stmtString(&ast.DeclStmt{}))
}

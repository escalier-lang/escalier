package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// An operand that is itself an operator expression is parenthesized. Without that,
// `!(x > y)` and `(!x) > y` both render `!x > y`, so two guards that test different
// things would be indistinguishable in a snapshot.
func TestExprStringParenthesizesOperatorOperands(t *testing.T) {
	x, y := ident("x"), ident("y")
	notGreater := ast.NewUnary(ast.LogicalNot, ast.NewBinary(x, y, ast.GreaterThan, ast.Span{}), ast.Span{})
	greaterOfNot := ast.NewBinary(ast.NewUnary(ast.LogicalNot, x, ast.Span{}), y, ast.GreaterThan, ast.Span{})

	tests := []struct {
		name string
		in   ast.Expr
		want string
	}{
		{"negated comparison", notGreater, "!(x > y)"},
		{"comparison of a negation", greaterOfNot, "(!x) > y"},
		{
			"right-nested conjunction",
			ast.NewBinary(x, ast.NewBinary(y, ident("z"), ast.LogicalAnd, ast.Span{}), ast.LogicalOr, ast.Span{}),
			"x || (y && z)",
		},
		{
			"left-nested disjunction",
			ast.NewBinary(ast.NewBinary(x, y, ast.LogicalOr, ast.Span{}), ident("z"), ast.LogicalAnd, ast.Span{}),
			"(x || y) && z",
		},
		{
			// A plain operand needs no parentheses, so the common guard stays readable.
			"simple operands",
			ast.NewBinary(x, y, ast.GreaterThan, ast.Span{}),
			"x > y",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, exprString(test.in))
		})
	}
}

// A binding leaf renders its `mut` prefix, its type annotation, and its default. The
// default is what makes a field optional, so dropping it would let `{x = 0}` and
// `{x}` render alike even though they match different values.
func TestPatStringRendersLeafExtras(t *testing.T) {
	numberAnn := &ast.NumberTypeAnn{}
	stringAnn := &ast.StringTypeAnn{}

	tests := []struct {
		name string
		in   ast.Pat
		want string
	}{
		{"plain shorthand", objPat("x"), "{x}"},
		{
			"shorthand with a default",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), false, nil, num(0), ast.Span{}),
			}, ast.Span{}),
			"{x = 0}",
		},
		{
			"shorthand with an annotation",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), false, numberAnn, nil, ast.Span{}),
			}, ast.Span{}),
			"{x: number}",
		},
		{
			"mutable shorthand with both",
			ast.NewObjectPat([]ast.ObjPatElem{
				ast.NewObjShorthandPat(ast.NewIdentifier("x", ast.Span{}), true, numberAnn, num(0), ast.Span{}),
			}, ast.Span{}),
			"{mut x: number = 0}",
		},
		{
			"ident leaf with both",
			ast.NewIdentPat("a", false, stringAnn, str("hi"), ast.Span{}),
			`a: string = "hi"`,
		},
		{
			"tuple element with a default",
			ast.NewTuplePat([]ast.Pat{ast.NewIdentPat("a", false, nil, num(1), ast.Span{})}, ast.Span{}),
			"[a = 1]",
		},
		{
			// An annotation this renderer does not spell out still shows its presence.
			"unrendered annotation",
			ast.NewIdentPat("a", false, &ast.KeyOfTypeAnn{}, nil, ast.Span{}),
			"a: <KeyOfTypeAnn>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, patString(test.in))
		})
	}
}

// A form the compact renderer does not spell out prints as its node kind, so an
// unrecognized body still leaves the surrounding IR readable.
func TestBodyStringFallsBackToTheNodeKind(t *testing.T) {
	require.Equal(t, "<DoExpr>", bodyString(ast.BlockOrExpr{Expr: ast.NewDo(ast.Block{}, ast.Span{})}))
}

// A receiver that is an operator expression keeps its parentheses. Without them
// `(a + b).c` flattens to `a + b.c`, which names a different expression, and
// `(-a).c` flattens to `-a.c`, which negates the field rather than the object.
func TestExprStringParenthesizesOperatorReceivers(t *testing.T) {
	a, b, c := ident("a"), ident("b"), ident("c")
	sum := ast.NewBinary(a, b, ast.Plus, ast.Span{})
	negated := ast.NewUnary(ast.UnaryMinus, a, ast.Span{})
	prop := ast.NewIdentifier("c", ast.Span{})

	tests := []struct {
		name string
		in   ast.Expr
		want string
	}{
		{
			"member of a sum",
			ast.NewMember(sum, prop, false, ast.Span{}),
			"(a + b).c",
		},
		{
			"member of a negation",
			ast.NewMember(negated, prop, false, ast.Span{}),
			"(-a).c",
		},
		{
			"index of a sum",
			ast.NewIndex(sum, c, false, ast.Span{}),
			"(a + b)[c]",
		},
		{
			"call of a sum",
			ast.NewCall(sum, []ast.Expr{c}, false, ast.Span{}),
			"(a + b)(c)",
		},
		{
			// A plain receiver needs no parentheses, so the common member access
			// stays readable.
			"member of an identifier",
			ast.NewMember(a, prop, false, ast.Span{}),
			"a.c",
		},
		{
			// Brackets already delimit an argument, an index, and a tuple element, so
			// an operator in those positions stays bare.
			"delimited positions stay bare",
			ast.NewCall(a, []ast.Expr{sum}, false, ast.Span{}),
			"a(a + b)",
		},
		{
			"index expression stays bare",
			ast.NewIndex(a, sum, false, ast.Span{}),
			"a[a + b]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, exprString(test.in))
		})
	}
}

// A union inside an intersection keeps its parentheses. `&` binds tighter than `|`,
// so without them the two nestings below both render `number | string & boolean` and
// an annotation on a pattern leaf would not distinguish them.
func TestTypeAnnStringParenthesizesANestedUnion(t *testing.T) {
	number := &ast.NumberTypeAnn{}
	str := &ast.StringTypeAnn{}
	boolean := &ast.BooleanTypeAnn{}
	union := func(types ...ast.TypeAnn) ast.TypeAnn { return &ast.UnionTypeAnn{Types: types} }
	isect := func(types ...ast.TypeAnn) ast.TypeAnn { return &ast.IntersectionTypeAnn{Types: types} }

	tests := []struct {
		name string
		in   ast.TypeAnn
		want string
	}{
		{
			"union inside an intersection",
			isect(union(number, str), boolean),
			"(number | string) & boolean",
		},
		{
			// The tighter operator already groups the way the tree does.
			"intersection inside a union",
			union(number, isect(str, boolean)),
			"number | string & boolean",
		},
		{"plain intersection", isect(number, str), "number & string"},
		{"plain union", union(number, str), "number | string"},
		{
			"inexact union inside an intersection",
			isect(&ast.UnionTypeAnn{Types: []ast.TypeAnn{number}, Inexact: true}, boolean),
			"(number | ...) & boolean",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, typeAnnString(test.in))
		})
	}
}

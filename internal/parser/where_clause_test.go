package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// TestParseWhereClause covers the `where` clause on every declaration and signature form
// that takes type parameters, the desugaring of its relations onto the binders, and the
// errors a malformed clause reports. Each case parses, prints, and compares the printed form
// against want, or against src when want is empty.
func TestParseWhereClause(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		errs []string
	}{
		{name: "ALowerBoundOnADeclaredFunction", src: "declare fn f<B>(x: B) -> B where number: B"},
		{
			name: "UpperBoundsOnAFunction",
			src: `fn fstNum<A, B>(a: A, b: B) -> A where A: number, B: number {
    return a
}`,
		},
		{
			name: "AClauseAfterAThrowsClause",
			src:  "declare fn f<B>(x: B) -> B throws string where number: B",
		},
		{
			name: "AClauseOnAFunctionWithoutAReturnType",
			src: `fn f<A>(a: A) where A: number {
    return a
}`,
		},
		{
			name: "AClauseOnTheNextLine",
			src: `fn f<A, B>(a: A, b: B) -> A
where A: number, B: number {
    return a
}`,
			want: `fn f<A, B>(a: A, b: B) -> A where A: number, B: number {
    return a
}`,
		},
		{
			name: "ALowerBoundOnAMethod",
			src: `class Bag<T> {
    contains<B>(&self, x: B) -> boolean where T: B {
        return false
    }
}`,
		},
		{
			name: "AClauseOnAClassHeader",
			src: `class Box<T> where T: {
    x: number
} {
    v: T
}`,
		},
		{name: "AClauseAfterExtends", src: "class Sub<T> extends Base<T> where T: Shape {}"},
		{
			name: "AClauseOnAnInterface",
			src: `interface Box<T> extends Base where number: T {
    m<B>(&self, x: B) -> boolean where T: B
}`,
		},
		{name: "AClauseOnAnAlias", src: "type Widen<B> where string: B = B"},
		{
			name: "AClauseOnAnEnum",
			src: `enum E<T> where T: number {
    A(T)
}`,
		},
		{name: "AClauseOnAFunctionType", src: "declare val f: fn<B> (x: B) -> boolean where number: B"},
		{
			name: "AClauseOnAnObjectTypeMethod",
			src: `type O = {
    m<B>(&self, x: B) -> boolean where number: B
}`,
		},
		{
			name: "AClauseOnAFunctionExpression",
			src: `val f = fn <B>(x: B) -> B where number: B {
    return x
}`,
		},
		{name: "ACompoundTypeOnTheLeft", src: "type A<T> where Box<U>: T = T"},
		{
			name: "AnObjectTypeOnTheLeft",
			src: `type A<T> where {
    x: number
}: T = T`,
		},
		// Several relations on one parameter merge into one bound: upper bounds
		// intersect and lower bounds union.
		{name: "TwoUpperBoundsIntersect", src: "type A<T> where T: X, T: Y = T", want: "type A<T> where T: X & Y = T"},
		{name: "ThreeUpperBoundsStayFlat", src: "type A<T> where T: X, T: Y, T: Z = T", want: "type A<T> where T: X & Y & Z = T"},
		{name: "TwoLowerBoundsUnion", src: "type A<T> where X: T, Y: T = T", want: "type A<T> where X | Y: T = T"},
		{
			// A bound the binder wrote inline joins the clause's bound, and the merged bound
			// prints in the clause.
			name: "AnInlineBoundJoinsTheClause",
			src:  "type A<T: X> where T: Y = T",
			want: "type A<T> where T: X & Y = T",
		},
		{
			// The sub side is tried first, so a relation between two parameters is an upper
			// bound on the left one.
			name: "TwoParametersBoundTheLeftOne",
			src:  "type A<P, Q> where P: Q = P",
		},
		{name: "AnUpperAndALowerBoundOnOneParameter", src: "type A<B> where B: Base, T: B = B"},
		{name: "AClauseBesideAnInlineBoundAndADefault", src: "type A<B: Base = D> where T: B = B"},
		// A union or intersection the source wrote is a single bound, so a second relation
		// on the parameter groups it rather than extending it.
		{name: "AWrittenUnionIsOneUpperBound", src: "type A<T> where T: X | Y, T: Z = T", want: "type A<T> where T: (X | Y) & Z = T"},
		{name: "AWrittenIntersectionIsOneLowerBound", src: "type A<T> where X & Y: T, Z: T = T", want: "type A<T> where X & Y | Z: T = T"},
		{name: "AParenthesizedLeftSide", src: "type A<T> where (X | Y): T = T", want: "type A<T> where X | Y: T = T"},
		{
			// A function type with no parameters of its own leaves the clause to the
			// signature it is the return of, and the printer parenthesizes that return.
			name: "AClauseAfterAFunctionTypedReturn",
			src:  "declare fn f<B>(x: B) -> fn (y: B) -> boolean where number: B",
			want: "declare fn f<B>(x: B) -> (fn (y: B) -> boolean) where number: B",
		},
		{
			// A generic function type takes a clause that follows it, so the enclosing
			// signature parenthesizes the return to keep its own.
			name: "AParenthesizedGenericReturnKeepsTheOuterClause",
			src:  "declare fn f<B>(x: B) -> (fn<V> (v: V) -> V) where number: B",
		},
		{name: "AGenericReturnTakesTheClause", src: "declare val f: fn () -> fn<V> (v: V) -> V where number: V"},
		{
			name: "ALifetimeRelation",
			src:  "declare fn f<'a, T>(x: &'a T) -> &'a T where 'a: 'static, number: T",
			want: "declare fn f<'a, T>(x: &'a T) -> &'a T where number: T",
			errs: []string{"lifetime bounds are written on the lifetime binder, as <'a: 'b>"},
		},
		{name: "ALifetimeBoundKeepsItsColon", src: "declare fn f<'a, 'b: 'a>(x: &'a number) -> &'b number"},
		{name: "WhereAsAParameterName", src: "declare fn f(where: number) -> number"},
		{
			// A relation naming no parameter of the declaration has no binder to land on.
			name: "ARelationNamingNoParameter",
			src:  "type A<T> where X: Y = T",
			want: "type A<T> = T",
			errs: []string{"a where clause relation must name a type parameter of this declaration on one side"},
		},
		{
			// A `where` is a clause only when a type and a `:` follow it, so one with no
			// relation is an unexpected identifier where the declaration wanted `=`.
			name: "AWhereWithoutARelationIsNotAClause",
			src:  "type A<T> where T = T",
			want: "type A<T> = T",
			errs: []string{"Expected = but got identifier", "Unexpected token", "Unexpected token", "Unexpected token"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := parseAndPrint(t, tt.src)
			require.Equal(t, tt.errs, errs)
			want := tt.want
			if want == "" {
				want = tt.src
			}
			require.Equal(t, want, got)
		})
	}
}

// TestAWhereRelationIsCarriedOnTheTypeParam asserts that `where T: B` fills B's LowerBound
// and leaves its inline upper bound where the binder wrote it.
func TestAWhereRelationIsCarriedOnTheTypeParam(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "type A<B: Base> where T: B = B"})
	require.Empty(t, errs)
	require.Len(t, decls, 1)
	decl, ok := decls[0].(*ast.TypeDecl)
	require.True(t, ok)
	require.Len(t, decl.TypeParams, 1)
	tp := decl.TypeParams[0]
	lower, err := printer.Print(tp.LowerBound, printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "T", lower)
	upper, err := printer.Print(tp.UpperBound, printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "Base", upper)
	require.False(t, tp.UpperBoundInWhere)
}

// TestALowerBoundNamingALaterSiblingSortsAfterIt asserts that SortTypeParamsTopologically
// reads a lower bound, so `B` in `<B, T> where Box<T>: B` is ordered after the `T` its
// lower bound names.
func TestALowerBoundNamingALaterSiblingSortsAfterIt(t *testing.T) {
	decls, errs := ParseDecls(context.Background(), &ast.Source{ID: 0, Path: "t.esc", Contents: "type A<B, T> where Box<T>: B = B"})
	require.Empty(t, errs)
	require.Len(t, decls, 1)
	decl, ok := decls[0].(*ast.TypeDecl)
	require.True(t, ok)
	var names []string
	for _, tp := range ast.SortTypeParamsTopologically(decl.TypeParams) {
		names = append(names, tp.Name)
	}
	require.Equal(t, []string{"T", "B"}, names)
}

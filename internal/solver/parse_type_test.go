package solver

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// parseType parses an Escalier type-annotation string into a soltype.Type so
// tests can author cases like parseType(t, "number | string") rather than
// hand-building each AST node. It is test-only.
//
// The converter handles the surface forms the lattice tests author: the prim
// keywords number / string / boolean; the atomic keywords never / unknown /
// void / null; literal types such as `5`, `"x"`, `true`; objects, tuples,
// owned-mutable `mut T`, unions, and intersections. It does not handle
// generic references, function annotations, borrows with named lifetimes, or
// type variables. Tests that need those continue to build the soltype value
// directly.
//
// A union or intersection is built through newUnion / newIntersection with no
// Context, so the result is normalized exactly as the production combine path
// would produce it. ast.UnionTypeAnn carries no Inexact flag today (that
// arrives with M6 PR4), so parseType always produces an exact union. A test
// that needs an inexact union mints one through newUnion(..., true).
func parseType(t *testing.T, s string) soltype.Type {
	t.Helper()
	ta, errs := parser.ParseTypeAnn(context.Background(), s)
	require.Empty(t, errs, "parser errors for %q", s)
	require.NotNil(t, ta, "parser returned nil TypeAnn for %q", s)
	return toSoltype(t, ta)
}

// parseTypes is the slice-input variant. It threads each string through
// parseType and returns the resulting members in input order, so a test can
// write `newUnion(nil, parseTypes(t, "number", "string"), false)`.
func parseTypes(t *testing.T, parts ...string) []soltype.Type {
	t.Helper()
	out := make([]soltype.Type, len(parts))
	for i, p := range parts {
		out[i] = parseType(t, p)
	}
	return out
}

// toSoltype walks one ast.TypeAnn node into a soltype.Type. Unsupported
// nodes fail the test rather than degrading silently, so a typo in a test
// string surfaces immediately.
func toSoltype(t *testing.T, ta ast.TypeAnn) soltype.Type {
	t.Helper()
	switch ta := ta.(type) {
	case *ast.NumberTypeAnn:
		return num()
	case *ast.StringTypeAnn:
		return str()
	case *ast.BooleanTypeAnn:
		return boolT()
	case *ast.NeverTypeAnn:
		return &soltype.NeverType{}
	case *ast.UnknownTypeAnn:
		return &soltype.UnknownType{}
	case *ast.VoidTypeAnn:
		return &soltype.Void{}
	case *ast.LitTypeAnn:
		return litToSoltype(t, ta.Lit)
	case *ast.UnionTypeAnn:
		members := make([]soltype.Type, len(ta.Types))
		for i, m := range ta.Types {
			members[i] = toSoltype(t, m)
		}
		return newUnion(nil, members, false)
	case *ast.IntersectionTypeAnn:
		members := make([]soltype.Type, len(ta.Types))
		for i, m := range ta.Types {
			members[i] = toSoltype(t, m)
		}
		return newIntersection(nil, members)
	case *ast.ObjectTypeAnn:
		return objectToSoltype(t, ta)
	case *ast.TupleTypeAnn:
		elems := make([]soltype.Type, len(ta.Elems))
		for i, e := range ta.Elems {
			elems[i] = toSoltype(t, e)
		}
		return &soltype.TupleType{Elems: elems, Inexact: ta.Inexact}
	case *ast.MutableTypeAnn:
		inner := toSoltype(t, ta.Target)
		ri, ok := inner.(soltype.RefInner)
		require.True(t, ok, "parseType: `mut` over a non-borrowable type %T", inner)
		return &soltype.RefType{Mut: true, Lt: nil, Inner: ri}
	}
	t.Fatalf("parseType: unsupported type annotation %T", ta)
	return nil
}

// litToSoltype maps an ast.Lit inside a LitTypeAnn to its soltype value.
// NullLit maps to NullType, since null is a distinct atomic kind rather than
// a literal.
func litToSoltype(t *testing.T, lit ast.Lit) soltype.Type {
	t.Helper()
	switch l := lit.(type) {
	case *ast.NumLit:
		return &soltype.LitType{Lit: &soltype.NumLit{Value: l.Value}}
	case *ast.StrLit:
		return &soltype.LitType{Lit: &soltype.StrLit{Value: l.Value}}
	case *ast.BoolLit:
		return &soltype.LitType{Lit: &soltype.BoolLit{Value: l.Value}}
	case *ast.NullLit:
		return &soltype.NullType{}
	}
	t.Fatalf("parseType: unsupported literal %T", lit)
	return nil
}

// objectToSoltype lowers an ObjectTypeAnn into a soltype.ObjectType. Only
// property elements (`name: T`, `name?: T`) are accepted, mirroring the
// production resolveObjectTypeAnn arm. Method, getter, setter, index, and
// spread elements fail the test since the lattice tests do not need them.
func objectToSoltype(t *testing.T, ta *ast.ObjectTypeAnn) *soltype.ObjectType {
	t.Helper()
	elems := make([]soltype.ObjTypeElem, 0, len(ta.Elems))
	for _, e := range ta.Elems {
		prop, ok := e.(*ast.PropertyTypeAnn)
		require.True(t, ok, "parseType: unsupported object element %T", e)
		name, ok := objKeyName(prop.Name)
		require.True(t, ok, "parseType: unsupported object key %T", prop.Name)
		var ft soltype.Type
		if prop.Value != nil {
			ft = toSoltype(t, prop.Value)
		} else {
			ft = &soltype.UnknownType{}
		}
		elems = append(elems, &soltype.PropertyElem{Name: name, Type: ft, Optional: prop.Optional})
	}
	return &soltype.ObjectType{Elems: elems, Inexact: ta.Inexact}
}

// TestParseTypeHelperSmoke confirms parseType round-trips a variety of
// surface forms to the same soltype value the tests would otherwise build
// by hand.
func TestParseTypeHelperSmoke(t *testing.T) {
	tests := []struct {
		in   string
		want soltype.Type
	}{
		{"number", num()},
		{"string", str()},
		{"boolean", boolT()},
		{"never", &soltype.NeverType{}},
		{"unknown", &soltype.UnknownType{}},
		{"void", &soltype.Void{}},
		{"null", &soltype.NullType{}},
		{"5", numLit(5)},
		{`"x"`, strLit("x")},
		{"true", &soltype.LitType{Lit: &soltype.BoolLit{Value: true}}},
		{"number | string", &soltype.UnionType{Types: []soltype.Type{num(), str()}}},
		{"number & string", &soltype.IntersectionType{Types: []soltype.Type{num(), str()}}},
		{"{x: number}", exactObj(propElem("x", num()))},
		{"{x: number, ...}", inexactObj(propElem("x", num()))},
		{"[number, string]", &soltype.TupleType{Elems: []soltype.Type{num(), str()}}},
		{"[number, ...]", &soltype.TupleType{Elems: []soltype.Type{num()}, Inexact: true}},
		{"mut {x: number}", mutRef(exactObj(propElem("x", num())))},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := parseType(t, tt.in)
			require.True(t, equalType(tt.want, got), "parseType(%q): want %s, got %s", tt.in, soltype.Print(tt.want), soltype.Print(got))
		})
	}
}

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
// null / undefined; literal types such as `5`, `"x"`, `true`; objects, tuples,
// owned-mutable `mut T`, unions, intersections, and `fn (x: T) -> U`
// signatures. It does not handle a generic reference such as `Array<T>`,
// borrows with named lifetimes, or type parameters on a signature. Tests that
// need those continue to build the soltype value directly.
//
// A bare name such as `T` resolves through the environment parseTypeIn takes,
// which is how a test writes a free type variable into a source string.
//
// A union or intersection is built through newUnion / newIntersection with no
// Context, so the result is normalized exactly as the production combine path
// would produce it. ast.UnionTypeAnn carries no Inexact flag today (that
// arrives with M6 PR4), so parseType always produces an exact union. A test
// that needs an inexact union mints one through newUnion(..., true).
func parseType(t *testing.T, s string) soltype.Type {
	t.Helper()
	return parseTypeIn(t, nil, s)
}

// parseTypeIn is parseType over an environment that binds bare type names to
// types the caller already built. A test mints a type variable and binds it
// under `T`, then authors `fn (x: number) -> T` as source instead of assembling
// the FuncType by hand. Both operands of a subtyping case parse under the same
// environment, so the `T` in each names one variable. A name the environment
// does not bind fails the test, the same as any other unsupported form.
func parseTypeIn(t *testing.T, env map[string]soltype.Type, s string) soltype.Type {
	t.Helper()
	ta, errs := parser.ParseTypeAnn(context.Background(), s)
	require.Empty(t, errs, "parser errors for %q", s)
	require.NotNil(t, ta, "parser returned nil TypeAnn for %q", s)
	return toSoltype(t, env, ta)
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
// string surfaces immediately. env binds the bare names the annotation may
// reference; see parseTypeIn.
func toSoltype(t *testing.T, env map[string]soltype.Type, ta ast.TypeAnn) soltype.Type {
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
	case *ast.LitTypeAnn:
		return litToSoltype(t, ta.Lit)
	case *ast.UnionTypeAnn:
		members := make([]soltype.Type, len(ta.Types))
		for i, m := range ta.Types {
			members[i] = toSoltype(t, env, m)
		}
		return newUnion(nil, members, false)
	case *ast.IntersectionTypeAnn:
		members := make([]soltype.Type, len(ta.Types))
		for i, m := range ta.Types {
			members[i] = toSoltype(t, env, m)
		}
		return newIntersection(nil, members)
	case *ast.ObjectTypeAnn:
		return objectToSoltype(t, env, ta)
	case *ast.TupleTypeAnn:
		elems := make([]soltype.Type, len(ta.Elems))
		for i, e := range ta.Elems {
			elems[i] = toSoltype(t, env, e)
		}
		return &soltype.TupleType{Elems: elems, Inexact: ta.Inexact}
	case *ast.MutableTypeAnn:
		inner := toSoltype(t, env, ta.Target)
		ri, ok := inner.(soltype.RefInner)
		require.True(t, ok, "parseType: `mut` over a non-borrowable type %T", inner)
		return &soltype.RefType{Mut: true, Lt: nil, Inner: ri}
	case *ast.FuncTypeAnn:
		return funcToSoltype(t, env, ta)
	case *ast.TypeRefTypeAnn:
		return envLookup(t, env, ta)
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
	case *ast.UndefinedLit:
		return &soltype.UndefinedType{}
	}
	t.Fatalf("parseType: unsupported literal %T", lit)
	return nil
}

// funcToSoltype lowers a `fn (x: T) -> U throws E` annotation into a
// soltype.FuncType. It accepts positional parameters written as `name: T`, an
// optional `?` marker, the trailing `...` inexactness marker, and a `throws`
// clause. A destructuring or rest pattern, a missing parameter or return
// annotation, and the type-parameter and lifetime clauses all fail the test,
// mirroring how the rest of this converter refuses what it cannot lower
// faithfully.
//
// An omitted `throws` leaves Throws nil, the shorthand for `never` that
// FuncType.ThrowsOrNever resolves.
//
// An `open p` parameter fails for the same reason. soltype.FuncParam has no
// slot for the marker, so lowering one would drop it and hand back a signature
// narrower than the string says.
func funcToSoltype(t *testing.T, env map[string]soltype.Type, ta *ast.FuncTypeAnn) *soltype.FuncType {
	t.Helper()
	require.Empty(t, ta.TypeParams, "parseType: type parameters on a fn annotation")
	require.Empty(t, ta.LifetimeParams, "parseType: lifetime parameters on a fn annotation")
	require.NotNil(t, ta.Return, "parseType: fn annotation without a return type")
	params := make([]*soltype.FuncParam, len(ta.Params))
	for i, p := range ta.Params {
		pat, ok := p.Pattern.(*ast.IdentPat)
		require.True(t, ok, "parseType: unsupported parameter pattern %T", p.Pattern)
		require.False(t, p.Open, "parseType: open parameter %s", pat.Name)
		require.NotNil(t, p.TypeAnn, "parseType: parameter %s has no type", pat.Name)
		params[i] = &soltype.FuncParam{
			Pattern:  &soltype.IdentPat{Name: pat.Name},
			Type:     toSoltype(t, env, p.TypeAnn),
			Optional: p.Optional,
		}
	}
	var throws soltype.Type
	if ta.Throws != nil {
		throws = toSoltype(t, env, ta.Throws)
	}
	return &soltype.FuncType{
		Params:  params,
		Ret:     toSoltype(t, env, ta.Return),
		Throws:  throws,
		Inexact: ta.Inexact,
	}
}

// envLookup resolves a bare type name against the environment parseTypeIn was
// given. An environment entry is one already-built type rather than a generic
// the converter could apply, so type arguments, lifetime arguments, and a
// lifetime qualifier all fail the test.
func envLookup(t *testing.T, env map[string]soltype.Type, ta *ast.TypeRefTypeAnn) soltype.Type {
	t.Helper()
	require.Empty(t, ta.TypeArgs, "parseType: type arguments on a bare name")
	require.Empty(t, ta.LifetimeArgs, "parseType: lifetime arguments on a bare name")
	require.Nil(t, ta.Lifetime, "parseType: lifetime on a bare name")
	name, ok := ta.Name.(*ast.Ident)
	require.True(t, ok, "parseType: qualified name %s", ast.QualIdentToString(ta.Name))
	bound, ok := env[name.Name]
	require.True(t, ok, "parseType: unbound type name %s", name.Name)
	return bound
}

// objectToSoltype lowers an ObjectTypeAnn into a soltype.ObjectType. Only
// property elements (`name: T`, `name?: T`) are accepted, mirroring the
// production resolveObjectTypeAnn arm. Method, getter, setter, index, and
// spread elements fail the test since the lattice tests do not need them.
func objectToSoltype(t *testing.T, env map[string]soltype.Type, ta *ast.ObjectTypeAnn) *soltype.ObjectType {
	t.Helper()
	elems := make([]soltype.ObjTypeElem, 0, len(ta.Elems))
	for _, e := range ta.Elems {
		prop, ok := e.(*ast.PropertyTypeAnn)
		require.True(t, ok, "parseType: unsupported object element %T", e)
		name, ok := objKeyName(prop.Name)
		require.True(t, ok, "parseType: unsupported object key %T", prop.Name)
		var ft soltype.Type
		if prop.Value != nil {
			ft = toSoltype(t, env, prop.Value)
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
		{"undefined", &soltype.UndefinedType{}},
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
		{"fn (x: number) -> string", exactFn(str(), identParam("x", num()))},
		{"fn (x: number, ...) -> string", inexactFn(str(), identParam("x", num()))},
		{"fn (x?: number) -> string", exactFn(str(), optParam("x", num()))},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := parseType(t, tt.in)
			require.True(t, equalType(tt.want, got), "parseType(%q): want %s, got %s", tt.in, soltype.Print(tt.want), soltype.Print(got))
		})
	}
}

// TestParseTypeInEnvBindsOneVariable confirms a bare name resolves to the type
// the environment binds it to, and that every occurrence of the name across a
// string resolves to that same variable rather than to a fresh one per site.
func TestParseTypeInEnvBindsOneVariable(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	got := parseTypeIn(t, map[string]soltype.Type{"T": v}, "fn (x: T) -> T")
	fn, ok := got.(*soltype.FuncType)
	require.True(t, ok, "expected a function type, got %T", got)
	require.Len(t, fn.Params, 1)
	require.Same(t, v, fn.Params[0].Type)
	require.Same(t, v, fn.Ret)
}

package solver

import (
	"context"
	"sync/atomic"
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

// objectToSoltype lowers an ObjectTypeAnn into a soltype.ObjectType. It accepts
// property, method, getter, setter, constructor, and mapped members, mirroring the
// production resolveObjectTypeAnn arm for each. A method written more than once
// under one name folds into a single MethodElem holding an overload set, the way
// the production overload merge collapses repeated signatures. A spread and a
// callable element fail the test, since the lattice tests do not author them.
func objectToSoltype(t *testing.T, env map[string]soltype.Type, ta *ast.ObjectTypeAnn) *soltype.ObjectType {
	t.Helper()
	elems := make([]soltype.ObjTypeElem, 0, len(ta.Elems))
	for _, e := range ta.Elems {
		switch e := e.(type) {
		case *ast.PropertyTypeAnn:
			name, ok := objKeyName(e.Name)
			require.True(t, ok, "parseType: unsupported object key %T", e.Name)
			var ft soltype.Type
			if e.Value != nil {
				ft = toSoltype(t, env, e.Value)
			} else {
				ft = &soltype.UnknownType{}
			}
			elems = append(elems, &soltype.PropertyElem{Name: name, Type: ft, Optional: e.Optional, Readonly: e.Readonly})
		case *ast.MethodTypeAnn:
			fn := methodFuncToSoltype(t, env, e.Fn, e.Receiver)
			elems = append(elems, &soltype.MethodElem{Name: objKeyNameReq(t, e.Name), Signatures: []*soltype.FuncType{fn}})
		case *ast.GetterTypeAnn:
			// A getter's Fn is `(self) -> T throws E`, so its return and throws are the
			// value read and what reading raises.
			fn := methodFuncToSoltype(t, env, e.Fn, e.Receiver)
			elems = append(elems, &soltype.GetterElem{Name: objKeyNameReq(t, e.Name), SelfParam: fn.SelfParam, Type: fn.Ret, Throws: fn.Throws})
		case *ast.SetterTypeAnn:
			// A setter's Fn is `(self, value: T) -> undefined throws E`, so its one value
			// parameter is what the setter accepts and its throws is what writing raises.
			fn := methodFuncToSoltype(t, env, e.Fn, e.Receiver)
			require.Len(t, fn.Params, 1, "parseType: a setter takes one value parameter")
			elems = append(elems, &soltype.SetterElem{Name: objKeyNameReq(t, e.Name), SelfParam: fn.SelfParam, Param: fn.Params[0].Type, Throws: fn.Throws})
		case *ast.ConstructorTypeAnn:
			elems = append(elems, &soltype.ConstructorElem{Fn: funcToSoltype(t, env, e.Fn)})
		case *ast.MappedTypeAnn:
			elems = append(elems, mappedToSoltype(t, env, e))
		default:
			t.Fatalf("parseType: unsupported object element %T", e)
		}
	}
	return &soltype.ObjectType{Elems: mergeMethodOverloads(elems), Inexact: ta.Inexact}
}

// mappedKeyCounter mints the ids mappedToSoltype gives each mapped key, so a nested
// mapped type's key stays distinct from the enclosing one's when both are written K,
// the way freshMappedKey draws from the Context's counter in production.
var mappedKeyCounter atomic.Int64

// mappedToSoltype lowers a `[K: Keys]: V` mapped member to a soltype.MappedElem,
// mirroring the production resolveMappedElem. The `for K in Keys` clause binds K, so
// the value and the optional key-remapping and filter operands resolve in an
// environment where K names the minted key. The `?` and `readonly` markers carry
// through mappedModifier, which is what decides whether an index signature is
// settled.
func mappedToSoltype(t *testing.T, env map[string]soltype.Type, m *ast.MappedTypeAnn) *soltype.MappedElem {
	t.Helper()
	var keys soltype.Type = &soltype.UnknownType{}
	if m.TypeParam.Constraint != nil {
		keys = toSoltype(t, env, m.TypeParam.Constraint)
	}
	key := &soltype.MappedKeyType{ID: int(mappedKeyCounter.Add(1)), Name: m.TypeParam.Name}
	inner := env
	if m.TypeParam.Name != "" {
		inner = make(map[string]soltype.Type, len(env)+1)
		for name, ty := range env {
			inner[name] = ty
		}
		inner[m.TypeParam.Name] = key
	}
	var name, check, extends soltype.Type
	if m.Name != nil {
		name = toSoltype(t, inner, m.Name)
	}
	// The parser fills Check and Extends together or leaves both nil, so a filter is
	// resolved only when both halves are present.
	if m.Check != nil && m.Extends != nil {
		check = toSoltype(t, inner, m.Check)
		extends = toSoltype(t, inner, m.Extends)
	}
	return &soltype.MappedElem{
		Key:      key,
		Keys:     keys,
		Value:    toSoltype(t, inner, m.Value),
		Name:     name,
		Check:    check,
		Extends:  extends,
		Optional: mappedModifier(m.Optional),
		Readonly: mappedModifier(m.ReadOnly),
	}
}

// objKeyNameReq lowers an object key to its name, failing the test on a computed or
// otherwise unsupported key. It is the must-succeed form of objKeyName, used where
// only a named member is expected.
func objKeyNameReq(t *testing.T, key ast.ObjKey) string {
	t.Helper()
	name, ok := objKeyName(key)
	require.True(t, ok, "parseType: unsupported object key %T", key)
	return name
}

// methodFuncToSoltype lowers a method, getter, or setter signature, attaching the
// `self` receiver the FuncTypeAnn does not carry. The receiver's type is the same
// marker on every member, so two members compare equal on their receiver the way
// two instance members of one class body do.
func methodFuncToSoltype(t *testing.T, env map[string]soltype.Type, fnAnn *ast.FuncTypeAnn, recv *ast.MethodReceiver) *soltype.FuncType {
	t.Helper()
	fn := funcToSoltype(t, env, fnAnn)
	if recv != nil {
		require.False(t, recv.Mut, "parseType: mut receiver")
		require.Nil(t, recv.Lifetime, "parseType: receiver lifetime")
		fn.SelfParam = &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: &soltype.ClassType{Name: "Self"}}
	}
	return fn
}

// mergeMethodOverloads folds methods that repeat one name into a single MethodElem
// whose Signatures slice carries every arm in source order, mirroring the production
// overload merge. Members of other kinds pass through untouched. Two methods merge
// only when their Static markers agree, since a static and an instance member are
// distinct.
func mergeMethodOverloads(elems []soltype.ObjTypeElem) []soltype.ObjTypeElem {
	out := make([]soltype.ObjTypeElem, 0, len(elems))
	byName := map[string]*soltype.MethodElem{}
	for _, e := range elems {
		m, ok := e.(*soltype.MethodElem)
		if !ok {
			out = append(out, e)
			continue
		}
		if prev, seen := byName[m.Name]; seen && prev.Static == m.Static {
			prev.Signatures = append(prev.Signatures, m.Signatures...)
			continue
		}
		byName[m.Name] = m
		out = append(out, m)
	}
	return out
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

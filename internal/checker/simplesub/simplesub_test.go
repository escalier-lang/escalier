package simplesub

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// --- SimpleType helpers for direct constrain tests ---
func num() *Primitive     { return &Primitive{name: "number"} }
func str() *Primitive     { return &Primitive{name: "string"} }
func boolean() *Primitive { return &Primitive{name: "boolean"} }

func fn1(param, ret SimpleType) *Function {
	return &Function{params: []SimpleType{param}, ret: ret}
}
func fn2(p1, p2, ret SimpleType) *Function {
	return &Function{params: []SimpleType{p1, p2}, ret: ret}
}

// --- IR helpers ---
func lam(param string, body Term) *Lam { return &Lam{Params: []string{param}, Body: body} }
func vr(name string) *Var              { return &Var{Name: name} }
func litStr(s string) *Lit             { return &Lit{Kind: "str", Str: s} }
func litNum(n float64) *Lit            { return &Lit{Kind: "num", Num: n} }
func sel(recv Term, name string) *Sel  { return &Sel{Receiver: recv, Name: name} }

// rec builds a *Record from name, type pairs: rec("a", num(), "b", str()).
func rec(pairs ...any) *Record {
	fields := map[string]SimpleType{}
	for i := 0; i < len(pairs); i += 2 {
		fields[pairs[i].(string)] = pairs[i+1].(SimpleType)
	}
	return &Record{fields: fields}
}

func mut(inner SimpleType) *Mut  { return &Mut{inner: inner} }
func litNumT(n float64) *Literal { return &Literal{kind: "num", num: n} }

// renderWith infers and renders using a caller-supplied Inferer, so a test can
// pre-create variables (sharing the id counter) for use in annotations.
func renderWith(in *Inferer, term Term) (string, []error) {
	ty, errs := inferWith(in, term)
	return type_system.PrintType(ty, type_system.PrintConfig{}), errs
}

// TestInferIdentity is the identity case (also TopLevelLetPolymorphism):
// fn (x){return x}  ==>  fn <T0>(x: T0) -> T0.
func TestInferIdentity(t *testing.T) {
	got, errs := Render(lam("x", vr("x")))
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(x: T0) -> T0", got)
}

// TestIdentityPolymorphism: a let-bound identity applied at two different types
// must be generalized, so the results keep their literal types.
//
//	fn outer() {
//	  val id = fn (x) { return x }
//	  val a = id("hello")
//	  val b = id(5)
//	  return [a, b]
//	}  ==>  fn () -> ["hello", 5]
func TestIdentityPolymorphism(t *testing.T) {
	outer := &Lam{Params: nil, Body: &Let{
		Name: "id", Rhs: lam("x", vr("x")),
		Body: &Let{
			Name: "a", Rhs: &App{Fn: vr("id"), Arg: litStr("hello")},
			Body: &Let{
				Name: "b", Rhs: &App{Fn: vr("id"), Arg: litNum(5)},
				Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}},
			},
		},
	}}
	got, errs := Render(outer)
	require.Empty(t, errs)
	require.Equal(t, `fn () -> ["hello", 5]`, got)
}

// TestApplyIdentitySimplifies shows the M1 simplification pass: applying the
// identity to a literal yields that literal (the result variable is
// single-polarity, so it collapses to its lower bound rather than `T0 | 5`).
func TestApplyIdentitySimplifies(t *testing.T) {
	got, errs := Render(&App{Fn: lam("x", vr("x")), Arg: litNum(5)})
	require.Empty(t, errs)
	require.Equal(t, "5", got)
}

// TestInnerCapturesOuterParam exercises co-occurrence variable merging: the
// inner function captures the outer parameter y, so both results alias y and
// must collapse to a single type variable.
func TestInnerCapturesOuterParam(t *testing.T) {
	// fn outer(y) {
	//   val inner = fn (x) { return y }
	//   val a = inner(1)
	//   val b = inner("a")
	//   return [a, b]
	// }  ==>  fn <T0>(y: T0) -> [T0, T0]
	outer := &Lam{Params: []string{"y"}, Body: &Let{
		Name: "inner", Rhs: lam("x", vr("y")),
		Body: &Let{
			Name: "a", Rhs: &App{Fn: vr("inner"), Arg: litNum(1)},
			Body: &Let{
				Name: "b", Rhs: &App{Fn: vr("inner"), Arg: litStr("a")},
				Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}},
			},
		},
	}}
	got, errs := Render(outer)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(y: T0) -> [T0, T0]", got)
}

// TestPropertyAccess: reading obj.bar infers the receiver's required shape from
// usage. The receiver's variable accumulates {bar: <fresh>} as an upper bound,
// which coalesces (negative position) to the record {bar: T0}.
//
//	fn foo(obj) { return obj.bar }  ==>  fn <T0>(obj: {bar: T0}) -> T0
func TestPropertyAccess(t *testing.T) {
	foo := &Lam{Params: []string{"obj"}, Body: sel(vr("obj"), "bar")}
	got, errs := Render(foo)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(obj: {bar: T0}) -> T0", got)
}

// TestMultipleReads: two field reads accumulate two record upper bounds on the
// receiver, which merge into a single record at coalescing.
//
//	fn foo(obj) { return [obj.bar, obj.baz] }
//	  ==>  fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]
func TestMultipleReads(t *testing.T) {
	foo := &Lam{Params: []string{"obj"}, Body: &TupleExpr{Elems: []Term{
		sel(vr("obj"), "bar"),
		sel(vr("obj"), "baz"),
	}}}
	got, errs := Render(foo)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]", got)
}

// TestConstrainRecords exercises record width + depth subtyping directly.
func TestConstrainRecords(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		// width: a record with more fields is a subtype of one with fewer.
		{"more fields subtype of fewer", rec("a", num(), "b", str()), rec("a", num()), false},
		// ...but a record missing a required field is not.
		{"missing field", rec("a", num()), rec("a", num(), "b", str()), true},
		{"depth covariant ok", rec("a", num()), rec("a", num()), false},
		{"depth covariant fail", rec("a", num()), rec("a", str()), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestMutInvariance is the M3 gate: mutable references are invariant, encoded
// via the read/write decomposition. The decisive cases contrast a step that the
// immutable type allows (and that the mutable type must reject):
//
//   - width:  {x,y} <: {x}  ok      but  mut {x,y} <: mut {x}  FAIL
//   - depth:  {x: 5} <: {x: number} ok but  mut {x: 5} <: mut {x: number} FAIL
//
// If invariance could not be encoded, these mut cases would wrongly succeed and
// the migration would be in serious doubt.
func TestMutInvariance(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		{"mut equal ok", mut(num()), mut(num()), false},
		{"mut prim mismatch", mut(num()), mut(str()), true},

		// width subtyping: allowed immutably, rejected under mut.
		{"immutable width ok", rec("x", num(), "y", num()), rec("x", num()), false},
		{"mut width rejected", mut(rec("x", num(), "y", num())), mut(rec("x", num())), true},

		// depth (literal vs primitive): allowed immutably, rejected under mut.
		{"immutable depth ok", rec("x", litNumT(5)), rec("x", num()), false},
		{"mut depth rejected", mut(rec("x", litNumT(5))), mut(rec("x", num())), true},

		// a mutable reference can be read where an immutable value is expected.
		{"mut read coercion ok", mut(rec("x", num())), rec("x", num()), false},
		// but an immutable value is not a mutable reference.
		{"immutable is not mut", rec("x", num()), mut(rec("x", num())), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestMutParamFlows checks mut flows through inference and renders correctly: an
// identity over a concretely-annotated mut parameter keeps the mut on the
// result. (The lifetime annotations of the production checker are M4.)
//
//	fn identity(p: mut {x: number}) { return p }
//	  ==>  fn (p: mut {x: number}) -> mut {x: number}
func TestMutParamFlows(t *testing.T) {
	identity := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mut(rec("x", num()))},
		Body:       vr("p"),
	}
	got, errs := Render(identity)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> mut {x: number}", got)
}

// TestMutFieldIsInvariantTypeParam: reading the field of a mut parameter with a
// polymorphic content type keeps that content as a single (invariant) type
// parameter, readable as the result.
//
//	fn get(p: mut {x: <fresh>}) { return p.x }
//	  ==>  fn <T0>(p: mut {x: T0}) -> T0
func TestMutFieldIsInvariantTypeParam(t *testing.T) {
	in := NewInferer()
	alpha := in.freshVar(1)
	get := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mut(&Record{fields: map[string]SimpleType{"x": alpha}})},
		Body:       sel(vr("p"), "x"),
	}
	got, errs := renderWith(in, get)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(p: mut {x: T0}) -> T0", got)
}

// TestConstrain exercises the constrain primitive directly.
func TestConstrain(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		{"prim equal", boolean(), boolean(), false},
		{"prim mismatch", boolean(), num(), true},
		{"func equal", fn1(num(), num()), fn1(num(), num()), false},
		{"func param contravariant fail", fn1(num(), num()), fn1(str(), num()), true},
		{"func return covariant fail", fn1(num(), num()), fn1(num(), str()), true},
		{"fewer params subtype of more", fn1(num(), num()), fn2(num(), num(), num()), false},
		{"more params not subtype of fewer", fn2(num(), num(), num()), fn1(num(), num()), true},
		{"fewer params but overlap contravariant fail", fn1(str(), num()), fn2(num(), num(), num()), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestConstrainVariablePropagation: once v <: number, asserting boolean <: v
// must fail via boolean <: number.
func TestConstrainVariablePropagation(t *testing.T) {
	in := NewInferer()
	v := in.freshVar(0)
	require.Empty(t, in.Constrain(v, num()))
	require.NotEmpty(t, in.Constrain(boolean(), v))
}

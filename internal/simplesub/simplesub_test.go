package simplesub

import (
	"strings"
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
		name       string
		lhs, rhs   SimpleType
		wantErrMsg string // "" means success expected; otherwise the joined error text
	}{
		// width: a record with more fields is a subtype of one with fewer.
		{"more fields subtype of fewer", rec("a", num(), "b", str()), rec("a", num()), ""},
		// ...but a record missing a required field is not.
		{"missing field", rec("a", num()), rec("a", num(), "b", str()), `record is missing field "b"`},
		{"depth covariant ok", rec("a", num()), rec("a", num()), ""},
		{"depth covariant fail", rec("a", num()), rec("a", str()), "cannot constrain number <: string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			requireErrMsg(t, errs, tt.wantErrMsg)
		})
	}
}

// requireErrMsg asserts the joined text of errs equals wantErrMsg (empty means
// no errors expected). Joining handles cases that legitimately produce more than
// one error (e.g. mut invariance emits both the read and write directions).
func requireErrMsg(t *testing.T, errs []error, wantErrMsg string) {
	t.Helper()
	if wantErrMsg == "" {
		require.Empty(t, errs)
		return
	}
	require.NotEmpty(t, errs)
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	require.Equal(t, wantErrMsg, strings.Join(parts, "; "))
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
		name       string
		lhs, rhs   SimpleType
		wantErrMsg string // "" means success expected; otherwise the joined error text
	}{
		{"mut equal ok", mut(num()), mut(num()), ""},
		// mut is invariant: a prim mismatch fails in both the read and write
		// directions of the read/write decomposition.
		{"mut prim mismatch", mut(num()), mut(str()),
			"cannot constrain number <: string; cannot constrain string <: number"},

		// width subtyping: allowed immutably, rejected under mut.
		{"immutable width ok", rec("x", num(), "y", num()), rec("x", num()), ""},
		{"mut width rejected", mut(rec("x", num(), "y", num())), mut(rec("x", num())),
			`record is missing field "y"`},

		// depth (literal vs primitive): allowed immutably, rejected under mut.
		{"immutable depth ok", rec("x", litNumT(5)), rec("x", num()), ""},
		{"mut depth rejected", mut(rec("x", litNumT(5))), mut(rec("x", num())),
			"cannot constrain number <: 5"},

		// a mutable reference can be read where an immutable value is expected.
		{"mut read coercion ok", mut(rec("x", num())), rec("x", num()), ""},
		// but an immutable value is not a mutable reference.
		{"immutable is not mut", rec("x", num()), mut(rec("x", num())),
			"cannot constrain record <: mut record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			requireErrMsg(t, errs, tt.wantErrMsg)
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
	// With M4, returning the mut borrow infers a lifetime connecting param and
	// result (this is exactly IdentityRefReturn).
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}", got)
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

// assign builds a field-assignment statement: obj.name = value.
func assign(recv Term, name string, value Term) *Assign {
	return &Assign{Receiver: recv, Name: name, Value: value}
}

// TestInferMutFromWrites is the M3-extension headline: writing to a parameter's
// fields infers a mutable record parameter, with literals widened to their
// primitives and multiple writes merged into one record.
//
//	fn foo(obj) { obj.x = 5; obj.y = 10 }
//	  ==>  fn (obj: mut {x: number, y: number}) -> void
func TestInferMutFromWrites(t *testing.T) {
	foo := &Lam{Params: []string{"obj"}, Body: &Block{Exprs: []Term{
		assign(vr("obj"), "x", litNum(5)),
		assign(vr("obj"), "y", litNum(10)),
	}}}
	got, errs := Render(foo)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number, y: number}) -> void", got)
}

// TestWriteWidensLiteral isolates the widening rule: a single write of a literal
// infers the field at the literal's primitive type, not the literal.
//
//	fn foo(obj) { obj.x = 5 }  ==>  fn (obj: mut {x: number}) -> void
func TestWriteWidensLiteral(t *testing.T) {
	foo := &Lam{Params: []string{"obj"}, Body: &Block{Exprs: []Term{
		assign(vr("obj"), "x", litNum(5)),
	}}}
	got, errs := Render(foo)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> void", got)
}

// TestReadAndWriteSameField: reading and writing the same field of a parameter
// collapses to the written (widened) type. The write records the field's type
// per receiver variable, so the later read returns it directly rather than
// emitting an independent read requirement.
//
//	fn foo(obj) { obj.x = 5; return obj.x }  ==>  fn (obj: mut {x: number}) -> number
func TestReadAndWriteSameField(t *testing.T) {
	foo := &Lam{Params: []string{"obj"}, Body: &Block{Exprs: []Term{
		assign(vr("obj"), "x", litNum(5)),
		sel(vr("obj"), "x"),
	}}}
	got, errs := Render(foo)
	require.Empty(t, errs)
	require.Equal(t, "fn (obj: mut {x: number}) -> number", got)
}

// --- M4: lifetimes as a second sort ---

// mutRec builds a mut record parameter type from field pairs.
func mutRec(pairs ...any) *Mut { return mut(rec(pairs...)) }

// alias builds a named type alias over a body (e.g. type Point = {x: number}).
func alias(name string, body SimpleType) *Alias { return &Alias{name: name, body: body} }

// TestIdentityRefReturn: returning a mut parameter shares its lifetime by value
// identity, so the same lifetime variable appears on parameter and result.
//
//	fn identity(p: mut {x: number}) { return p }
//	  ==>  fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}
func TestIdentityRefReturn(t *testing.T) {
	identity := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body:       vr("p"),
	}
	got, errs := Render(identity)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}", got)
}

// TestFreshObjectReturn: returning a freshly-allocated record borrows nothing,
// so the result carries no lifetime. The parameter's lifetime is also elided —
// the borrow is never returned or stored, so it connects nothing (lifetime
// elision, the lifetime-sort analogue of single-polarity variable elimination).
// This is the key contrast with IdentityRefReturn, where the borrow IS returned.
//
//	fn clone(p: mut {x: number}) { return {x: 0} }  ==>  fn (p: mut {x: number}) -> {x: 0}
//
// (The result is {x: 0}, not {x: number}: record literals aren't widened — only
// values written through a mut reference are, per the M3 extension.)
func TestFreshObjectReturn(t *testing.T) {
	clone := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body:       &RecordExpr{Fields: map[string]Term{"x": litNum(0)}},
	}
	got, errs := Render(clone)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number}) -> {x: 0}", got)
}

// TestConditionalUnionReturn: returning one of two borrowed parameters unions
// their lifetimes on the result.
//
//	fn pick(a: mut {x: number}, b: mut {x: number}, cond: boolean) {
//	  if cond { a } else { b }
//	}  ==>  fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}, cond: boolean) -> mut ('a | 'b) {x: number}
//
// Returning one of two borrowed parameters unions their lifetimes on the result
// — the M4 thesis in action: a fresh join lifetime variable bounded below by
// both branches' lifetimes coalesces, in positive (return) position, to the
// union of its param-lifetime members.
func TestConditionalUnionReturn(t *testing.T) {
	pick := &Lam{
		Params:     []string{"a", "b", "cond"},
		ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num()), boolean()},
		Body:       &IfExpr{Cond: vr("cond"), Then: vr("a"), Else: vr("b")},
	}
	got, errs := Render(pick)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}, cond: boolean) -> mut ('a | 'b) {x: number}",
		got)
}

// TestConditionalMutDifferingFieldsNoInventedFields guards against the mut-join
// unsoundly unioning *differing* field sets into one mut record (which would
// invent a writable field absent from one branch). When the branches' field
// sets differ, joinBranches must fall back to the generic union path, preserving
// each branch's true shape rather than synthesizing a merged mut record.
//
//	fn pick(a: mut {x}, b: mut {x, y}, cond) { if cond { a } else { b } }
//	  ==>  result is the union of the two mut records, NOT mut {x, y}
func TestConditionalMutDifferingFieldsNoInventedFields(t *testing.T) {
	pick := &Lam{
		Params:     []string{"a", "b", "cond"},
		ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num(), "y", num()), boolean()},
		Body:       &IfExpr{Cond: vr("cond"), Then: vr("a"), Else: vr("b")},
	}
	got, errs := Render(pick)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number, y: number}, cond: boolean) -> mut 'a {x: number} | mut 'b {x: number, y: number}",
		got)
}

// TestEscapingRefIntoStatic: a parameter that escapes into static storage has
// its lifetime constrained to 'static, which coalesces away the lifetime
// variable in favor of 'static.
//
//	fn cache(item: mut {x: number}) { escape(item) }
//	  ==>  fn (item: mut 'static {x: number}) -> void
func TestEscapingRefIntoStatic(t *testing.T) {
	cache := &Lam{
		Params:     []string{"item"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body:       &Block{Exprs: []Term{&Escape{Value: vr("item")}}},
	}
	got, errs := Render(cache)
	require.Empty(t, errs)
	require.Equal(t, "fn (item: mut 'static {x: number}) -> void", got)
}

// --- Recursive functions (LetRec / LetRecGroup) ---
//
// The point of these tests is that "fresh var + constrain" handles cyclic
// declarations without the placeholder/patch dance the unification checker
// needs in InferComponent: each binding gets a fresh type variable that is
// visible during its own (and its siblings') inference, references resolve
// through the variable, and generalization happens once for the whole group
// at the end. There is no separate "create placeholder, infer, then unify
// back to patch the placeholder" step.

// TestLetRec_Loop is the minimal recursion: `letrec loop = fn(x){ loop(x) }`.
// With no constraint pinning the result, both the parameter and return type
// generalize independently — exactly what the principal type asks for.
//
//	letrec loop = fn(x) { loop(x) } in loop  ==>  fn <T0, T1>(x: T0) -> T1
func TestLetRec_Loop(t *testing.T) {
	prog := &LetRec{
		Name: "loop",
		Rhs:  lam("x", &App{Fn: vr("loop"), Arg: vr("x")}),
		Body: vr("loop"),
	}
	got, errs := Render(prog)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0, T1>(x: T0) -> T1", got)
}

// TestLetRec_Factorial: a recursive factorial-shaped function. To avoid pulling
// arithmetic into the spike IR, we model the body with curried builtins
// (`pred: number->number`, `mul: number->number->number`) injected as parameters
// of an outer lambda. The recursive structure itself is what we want to verify:
// `fact` refers to itself inside its own RHS, and the type
// `fact: fn(number) -> number` falls out of constrain propagation through the
// builtins, with no placeholder type needed.
//
//	fn(pred, mul, base) {
//	  letrec fact = fn(n) { mul(n)(fact(pred(n))) } in fact
//	}  ==>  fn (pred: number->number, mul: number->number->number, base: number)
//	         -> fn (n: number) -> number
func TestLetRec_Factorial(t *testing.T) {
	prog := &Lam{
		Params: []string{"pred", "mul", "base"},
		ParamTypes: []SimpleType{
			fn1(num(), num()),             // pred: number -> number
			fn1(num(), fn1(num(), num())), // mul: number -> number -> number (curried)
			num(),                         // base: number
		},
		Body: &LetRec{
			Name: "fact",
			Rhs: lam("n", &App{
				Fn:  &App{Fn: vr("mul"), Arg: vr("n")},
				Arg: &App{Fn: vr("fact"), Arg: &App{Fn: vr("pred"), Arg: vr("n")}},
			}),
			Body: vr("fact"),
		},
	}
	got, errs := Render(prog)
	require.Empty(t, errs)
	require.Equal(t,
		"fn (pred: fn (x0: number) -> number, "+
			"mul: fn (x0: number) -> fn (x0: number) -> number, "+
			"base: number) -> fn (n: number) -> number",
		got)
}

// TestLetRecGroup_MutualEvenOdd: mutually recursive `isEven`/`isOdd`. Both
// names are in scope inside *both* RHSs (and the body), and each RHS's inferred
// type is constrained `<:` its corresponding fresh variable in one shot. With no
// base case in the IR, the return type generalizes to a fresh parameter — the
// recursion is sound regardless, which is the point.
//
//	fn(pred) {
//	  letrec
//	    isEven = fn(n) { isOdd(pred(n)) }
//	    isOdd  = fn(n) { isEven(pred(n)) }
//	  in isEven
//	}  ==>  fn <T0>(pred: number->number) -> fn (n: number) -> T0
func TestLetRecGroup_MutualEvenOdd(t *testing.T) {
	prog := &Lam{
		Params:     []string{"pred"},
		ParamTypes: []SimpleType{fn1(num(), num())},
		Body: &LetRecGroup{
			Bindings: []recBinding{
				{Name: "isEven", Rhs: lam("n", &App{Fn: vr("isOdd"), Arg: &App{Fn: vr("pred"), Arg: vr("n")}})},
				{Name: "isOdd", Rhs: lam("n", &App{Fn: vr("isEven"), Arg: &App{Fn: vr("pred"), Arg: vr("n")}})},
			},
			Body: vr("isEven"),
		},
	}
	got, errs := Render(prog)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <T0>(pred: fn (x0: number) -> number) -> fn (n: number) -> T0",
		got)
}

// TestAliasRefReturn: a lifetime attaches to a type alias just as it does to a
// record. Returning a `mut` borrow of an alias-typed parameter shares the
// borrow's lifetime, which renders before the alias name.
//
//	type Point = {x: number}
//	fn identity(p: mut Point) { return p }
//	  ==>  fn <'a>(p: mut 'a Point) -> mut 'a Point
//
// This mirrors lifetime_test.go's ConstrainedTypeParam_AliasBound shape, where
// a lifetime-bearing alias makes the borrow lifetime-annotated.
func TestAliasRefReturn(t *testing.T) {
	identity := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mut(alias("Point", rec("x", num())))},
		Body:       vr("p"),
	}
	got, errs := Render(identity)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a Point) -> mut 'a Point", got)
}

// TestAliasByValueNoLifetime: a by-value alias parameter borrows nothing, so no
// lifetime is attached (the alias renders bare).
//
//	type Point = {x: number}
//	fn get(p: Point) { return p.x }  ==>  fn (p: Point) -> number
func TestAliasByValueNoLifetime(t *testing.T) {
	get := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{alias("Point", rec("x", num()))},
		Body:       sel(vr("p"), "x"),
	}
	got, errs := Render(get)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> number", got)
}

// TestAliasStructuralSubtyping: an alias is structurally its body, so a value of
// the alias's shape satisfies the alias and vice versa.
func TestAliasStructuralSubtyping(t *testing.T) {
	in := NewInferer()
	point := alias("Point", rec("x", num()))
	// {x: number, y: number} <: Point   (width subtyping through the alias body)
	require.Empty(t, in.Constrain(rec("x", num(), "y", num()), point))
	// Point <: {x: number}
	require.Empty(t, in.Constrain(point, rec("x", num())))
	// Point </: {x: string}
	require.NotEmpty(t, in.Constrain(point, rec("x", str())))
}

// TestLifetimePassesThroughIdentity verifies that a borrow's lifetime flows
// THROUGH the polymorphic identity, even though identity is typed as
// fn <T0>(x: T0) -> T0 with no lifetime on its signature. Lifetimes ride on
// values, not on the type scheme: instantiating id at the call site lets the
// argument's mut-record (carrying lifetime 'a) flow into the result.
//
//	fn wrapper(p: mut {x: number}) {
//	  val id = fn (x) { return x }   // id : fn <T0>(x: T0) -> T0
//	  return id(p)
//	}  ==>  fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}
func TestLifetimePassesThroughIdentity(t *testing.T) {
	// Precondition: the identity on its own carries no lifetime.
	idTy, idErrs := Render(lam("x", vr("x")))
	require.Empty(t, idErrs)
	require.Equal(t, "fn <T0>(x: T0) -> T0", idTy)

	// Applying that same polymorphic identity to a mut borrow passes the
	// borrow's lifetime through to the result.
	wrapper := &Lam{
		Params:     []string{"p"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body: &Let{
			Name: "id", Rhs: lam("x", vr("x")),
			Body: &App{Fn: vr("id"), Arg: vr("p")},
		},
	}
	got, errs := Render(wrapper)
	require.Empty(t, errs)
	require.Equal(t, "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}", got)
}

// recExpr builds a record-literal term from name, term pairs.
func recExpr(pairs ...any) *RecordExpr {
	fields := map[string]Term{}
	for i := 0; i < len(pairs); i += 2 {
		fields[pairs[i].(string)] = pairs[i+1].(Term)
	}
	return &RecordExpr{Fields: fields}
}

// TestPropertyLevelLifetimes: a record can carry distinct lifetimes per
// property, because each property's value is itself a borrow with its own
// lifetime. Storing two differently-borrowed params into a fresh record yields
// per-property lifetimes on the result — no lifetime sits on the outer (freshly
// allocated) record.
//
//	fn wrap(a: mut {x: number}, b: mut {x: number}) { return {head: a, tail: b} }
//	  ==>  fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number})
//	         -> {head: mut 'a {x: number}, tail: mut 'b {x: number}}
//
// This matches the production checker's ObjectLiteral_PropertyLevelDistinctLifetimes.
// It falls out of the value-based lifetime model for free: each field is the
// corresponding parameter value, carrying its own lifetime, and coalescing
// renders each independently.
func TestPropertyLevelLifetimes(t *testing.T) {
	wrap := &Lam{
		Params:     []string{"a", "b"},
		ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num())},
		Body:       recExpr("head", vr("a"), "tail", vr("b")),
	}
	got, errs := Render(wrap)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {head: mut 'a {x: number}, tail: mut 'b {x: number}}",
		got)
}

// TestPropertyLevelLifetimes_SharedSource: storing the SAME borrow into two
// properties gives both the same lifetime (one parameter, one lifetime).
//
//	fn dup(a: mut {x: number}) { return {head: a, tail: a} }
//	  ==>  fn <'a>(a: mut 'a {x: number}) -> {head: mut 'a {x: number}, tail: mut 'a {x: number}}
func TestPropertyLevelLifetimes_SharedSource(t *testing.T) {
	dup := &Lam{
		Params:     []string{"a"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body:       recExpr("head", vr("a"), "tail", vr("a")),
	}
	got, errs := Render(dup)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(a: mut 'a {x: number}) -> {head: mut 'a {x: number}, tail: mut 'a {x: number}}",
		got)
}

// TestTuplePerSlotLifetimes: a tuple preserves a distinct lifetime per slot —
// each element is its own borrow with its own lifetime, and (unlike Array<T>)
// the slots stay separate rather than collapsing into a union.
//
//	fn pair(a: mut {x: number}, b: mut {x: number}) { return [a, b] }
//	  ==>  fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number})
//	         -> [mut 'a {x: number}, mut 'b {x: number}]
//
// This matches the production checker's TupleOfTwoParams_PerSlotDistinctLifetimes
// and, like property-level lifetimes, falls out of the value-based model for
// free: each tuple element is the corresponding parameter value, carrying its
// own lifetime, and coalescing renders each slot independently.
func TestTuplePerSlotLifetimes(t *testing.T) {
	pair := &Lam{
		Params:     []string{"a", "b"},
		ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num())},
		Body:       &TupleExpr{Elems: []Term{vr("a"), vr("b")}},
	}
	got, errs := Render(pair)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> [mut 'a {x: number}, mut 'b {x: number}]",
		got)
}

// TestTuplePerSlotLifetimes_SharedSource: the same borrow in two slots gives
// both the same lifetime.
//
//	fn dup(a: mut {x: number}) { return [a, a] }
//	  ==>  fn <'a>(a: mut 'a {x: number}) -> [mut 'a {x: number}, mut 'a {x: number}]
func TestTuplePerSlotLifetimes_SharedSource(t *testing.T) {
	dup := &Lam{
		Params:     []string{"a"},
		ParamTypes: []SimpleType{mutRec("x", num())},
		Body:       &TupleExpr{Elems: []Term{vr("a"), vr("a")}},
	}
	got, errs := Render(dup)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <'a>(a: mut 'a {x: number}) -> [mut 'a {x: number}, mut 'a {x: number}]",
		got)
}

// TestConstrain exercises the constrain primitive directly.
func TestConstrain(t *testing.T) {
	tests := []struct {
		name       string
		lhs, rhs   SimpleType
		wantErrMsg string // "" means success expected
	}{
		{"prim equal", boolean(), boolean(), ""},
		{"prim mismatch", boolean(), num(), "cannot constrain boolean <: number"},
		{"func equal", fn1(num(), num()), fn1(num(), num()), ""},
		{"func param contravariant fail", fn1(num(), num()), fn1(str(), num()),
			"cannot constrain string <: number"},
		{"func return covariant fail", fn1(num(), num()), fn1(num(), str()),
			"cannot constrain number <: string"},
		{"fewer params subtype of more", fn1(num(), num()), fn2(num(), num(), num()), ""},
		{"more params not subtype of fewer", fn2(num(), num(), num()), fn1(num(), num()),
			"cannot constrain function of arity 2 <: function of arity 1"},
		{"fewer params but overlap contravariant fail", fn1(str(), num()), fn2(num(), num(), num()),
			"cannot constrain number <: string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			requireErrMsg(t, errs, tt.wantErrMsg)
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

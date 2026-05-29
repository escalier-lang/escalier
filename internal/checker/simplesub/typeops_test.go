package simplesub

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- TyExpr helpers ---
func tref(name string, args ...TyExpr) *TyRef { return &TyRef{Name: name, Args: args} }
func tprim(name string) *TyPrim               { return &TyPrim{Name: name} }
func tstr(v string) *TyStrLit                 { return &TyStrLit{Value: v} }
func tbool(v bool) *TyBoolLit                 { return &TyBoolLit{Value: v} }
func tunion(ms ...TyExpr) *TyUnion            { return &TyUnion{Members: ms} }
func tarray(elem TyExpr) *TyArray             { return &TyArray{Elem: elem} }
func tcond(check, ext, then, els TyExpr) *TyCond {
	return &TyCond{Check: check, Extends: ext, Then: then, Else: els}
}
func trec(pairs ...any) *TyRecord {
	fields := map[string]TyExpr{}
	for i := 0; i < len(pairs); i += 2 {
		fields[pairs[i].(string)] = pairs[i+1].(TyExpr)
	}
	return &TyRecord{Fields: fields}
}

// TestBasicConditionalTypeAlias: a conditional reduces when its operands are
// ground.
//
//	type IsString<T> = if T : string { true } else { false }
//	IsString<string> ==> true ; IsString<number> ==> false
func TestBasicConditionalTypeAlias(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("IsString", []string{"T"},
		tcond(tref("T"), tprim("string"), tbool(true), tbool(false)))

	require.Equal(t, "true", e.Render(tref("IsString", tprim("string"))))
	require.Equal(t, "false", e.Render(tref("IsString", tprim("number"))))
}

// TestConditionalTypeWithInfer: `infer U` binds the matched portion and is
// usable in the Then branch.
//
//	type GetElement<T> = if T : Array<infer U> { U } else { never }
//	GetElement<Array<number>> ==> number ; GetElement<string> ==> never
func TestConditionalTypeWithInfer(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("GetElement", []string{"T"},
		tcond(tref("T"), tarray(&TyInfer{Name: "U"}), tref("U"), &TyNever{}))

	require.Equal(t, "number", e.Render(tref("GetElement", tarray(tprim("number")))))
	require.Equal(t, "never", e.Render(tref("GetElement", tprim("string"))))
}

// TestDistributiveConditionalTypes: a conditional over a naked type parameter
// distributes across a union argument.
//
//	type ToArray<T> = if T : any { Array<T> } else { never }
//	ToArray<string | number> ==> Array<string> | Array<number>
func TestDistributiveConditionalTypes(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("ToArray", []string{"T"},
		tcond(tref("T"), &TyAny{}, tarray(tref("T")), &TyNever{}))

	require.Equal(t, "Array<string> | Array<number>",
		e.Render(tref("ToArray", tunion(tprim("string"), tprim("number")))))
}

// TestKeyofGround: keyof over a ground object reduces to the union of its keys.
//
//	keyof {a: number, b: string} ==> "a" | "b"
func TestKeyofGround(t *testing.T) {
	e := NewTypeEvaluator()
	got := e.Render(&TyKeyof{Target: trec("a", tprim("number"), "b", tprim("string"))})
	require.Equal(t, "\"a\" | \"b\"", got)
}

// TestIndexedAccessGround: T[K] over a ground object and literal key reduces to
// the property's type.
//
//	{a: number, b: string}["b"] ==> string
func TestIndexedAccessGround(t *testing.T) {
	e := NewTypeEvaluator()
	got := e.Render(&TyIndex{
		Target: trec("a", tprim("number"), "b", tprim("string")),
		Index:  tstr("b"),
	})
	require.Equal(t, "string", got)
}

// TestGenericAliasAppliedToConcrete: a conditional inside a generic alias
// applied to concrete arguments reduces fully (the common Baseline-D case).
//
//	type MyExclude<T, U> = if T : U { never } else { T }
//	MyExclude<"a" | "b" | "c", "a"> ==> "b" | "c"
func TestGenericAliasAppliedToConcrete(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("MyExclude", []string{"T", "U"},
		tcond(tref("T"), tref("U"), &TyNever{}, tref("T")))

	require.Equal(t, "\"b\" | \"c\"",
		e.Render(tref("MyExclude", tunion(tstr("a"), tstr("b"), tstr("c")), tstr("a"))))
}

// TestSymbolicWhenOperandNotGround documents Baseline D: an operator whose
// operand is not a known concrete type stays symbolic rather than reducing.
// `keyof Foo`, where Foo is an unknown (nominal) reference, does not reduce.
func TestSymbolicWhenOperandNotGround(t *testing.T) {
	e := NewTypeEvaluator()
	require.Equal(t, "keyof Foo", e.Render(&TyKeyof{Target: tref("Foo")}))
	require.Equal(t, "Foo[\"x\"]",
		e.Render(&TyIndex{Target: tref("Foo"), Index: tstr("x")}))
}

// --- Recursive types ---

// TestRecursiveAliasFiniteKnot: a recursive alias reduces to a finite type with
// a symbolic back-reference (the "knot") at the recursion point, rather than
// expanding forever. This is the cycle-cache case: the bound is analytic (the
// number of distinct instantiation states), not the budget.
//
//	type List<T> = {head: T, tail: List<T> | Null}
//	List<number> ==> {head: number, tail: List<number> | Null}
func TestRecursiveAliasFiniteKnot(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("List", []string{"T"},
		trec("head", tref("T"), "tail", tunion(tref("List", tref("T")), tref("Null"))))
	require.Equal(t, "{head: number, tail: List<number> | Null}",
		e.Render(tref("List", tprim("number"))))
}

// TestResidualKeyofOverRecursiveType: a keyof residual reduces over a recursive
// type — the operand expands to its finite knot, and keyof reads its key set.
//
//	keyof List<number> ==> "head" | "tail"
func TestResidualKeyofOverRecursiveType(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("List", []string{"T"},
		trec("head", tref("T"), "tail", tunion(tref("List", tref("T")), tref("Null"))))
	require.Equal(t, "\"head\" | \"tail\"",
		e.Render(&TyKeyof{Target: tref("List", tprim("number"))}))
	require.Equal(t, "number",
		e.Render(&TyIndex{Target: tref("List", tprim("number")), Index: tstr("head")}))
}

// TestRecursiveConditionalTerminatesAtBaseCase: a conditional that recurses on a
// shrinking argument terminates at its base case.
//
//	type Last<T> = if T : Array<infer U> { Last<U> } else { T }
//	Last<Array<Array<number>>> ==> number
func TestRecursiveConditionalTerminatesAtBaseCase(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Last", []string{"T"},
		tcond(tref("T"), tarray(&TyInfer{Name: "U"}), tref("Last", tref("U")), tref("T")))
	require.Equal(t, "number", e.Render(tref("Last", tarray(tarray(tprim("number"))))))
}

// TestUnboundedRecursionTerminatesViaBudget documents the Turing-complete
// fragment: an alias whose argument grows without bound has no base case and no
// repeating instantiation state, so the cycle cache never fires. The depth
// budget stops it — the result stays symbolic (a deeply nested Grow<...> head)
// instead of hanging. The point is termination, not the exact result, so we
// assert only that it completes and stays headed by Grow.
func TestUnboundedRecursionTerminatesViaBudget(t *testing.T) {
	e := NewTypeEvaluator()
	e.Define("Grow", []string{"T"}, tref("Grow", tarray(tref("T"))))
	got := e.Render(tref("Grow", tprim("number")))
	require.True(t, strings.HasPrefix(got, "Grow<"),
		"unbounded recursion should terminate with a symbolic Grow<...> head, got %q", got)
}

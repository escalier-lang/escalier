package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	ts "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestGeneralizeFuncType_TypeVarConstraintWithNestedTypeVar(t *testing.T) {
	// Simulates a scenario where a type var's constraint references another
	// unresolved type var: fn(a, b) where a's type var has constraint Array<b's type var>.
	// Both type vars should be collected and generalized.
	tvB := ts.NewTypeVarType(nil, 1) // unresolved
	tvA := ts.NewTypeVarType(nil, 2) // unresolved, constraint references tvB
	arrayAlias := &ts.TypeAlias{Type: ts.NewUnknownType(nil), TypeParams: []*ts.TypeParam{}}
	tvA.Constraint = ts.NewTypeRefType(nil, "Array", arrayAlias, tvB)

	funcType := ts.NewFuncType(
		nil,
		nil,
		[]*ts.FuncParam{
			ts.NewFuncParam(ts.NewIdentPat("a"), tvA),
			ts.NewFuncParam(ts.NewIdentPat("b"), tvB),
		},
		tvA, // return type same as param a
		ts.NewNeverType(nil),
	)

	GeneralizeFuncType(funcType)

	assert.Len(t, funcType.TypeParams, 2)
	assert.Equal(t, "T0", funcType.TypeParams[0].Name)
	assert.Equal(t, "T1", funcType.TypeParams[1].Name)
	// T0's constraint should be Array<T1>
	constraint, ok := funcType.TypeParams[0].Constraint.(*ts.TypeRefType)
	assert.True(t, ok, "constraint should be a TypeRefType")
	assert.Equal(t, "Array", ts.QualIdentToString(constraint.Name))
	assert.Len(t, constraint.TypeArgs, 1)
	// The type arg resolves (via Prune) to a TypeRefType referencing T1
	typeArg, ok := ts.Prune(constraint.TypeArgs[0]).(*ts.TypeRefType)
	assert.True(t, ok, "type arg should resolve to a TypeRefType")
	assert.Equal(t, "T1", ts.QualIdentToString(typeArg.Name))
}

func TestGeneralizeFuncType_TypeVarDefaultWithNestedTypeVar(t *testing.T) {
	// A type var with a default that references another unresolved type var.
	tvInner := ts.NewTypeVarType(nil, 1) // unresolved
	tvOuter := ts.NewTypeVarType(nil, 2) // unresolved, default references tvInner
	tvOuter.Default = tvInner

	funcType := ts.NewFuncType(
		nil,
		nil,
		[]*ts.FuncParam{
			ts.NewFuncParam(ts.NewIdentPat("x"), tvOuter),
		},
		tvOuter,
		ts.NewNeverType(nil),
	)

	GeneralizeFuncType(funcType)

	// Both tvOuter and tvInner should be generalized
	assert.Len(t, funcType.TypeParams, 2)
	assert.Equal(t, "T0", funcType.TypeParams[0].Name)
	assert.Equal(t, "T1", funcType.TypeParams[1].Name)
}

func TestGeneralizeFuncType_FuncTypeParamConstraintWithTypeVar(t *testing.T) {
	// A param is a FuncType whose type param has a constraint containing
	// an unresolved type var: fn(f: fn<U: T>() -> U) where T is unresolved.
	tvT := ts.NewTypeVarType(nil, 1) // unresolved

	innerFuncType := ts.NewFuncType(
		nil,
		[]*ts.TypeParam{{Name: "U", Constraint: tvT}},
		[]*ts.FuncParam{},
		ts.NewUnknownType(nil),
		ts.NewNeverType(nil),
	)

	funcType := ts.NewFuncType(
		nil,
		nil,
		[]*ts.FuncParam{
			ts.NewFuncParam(ts.NewIdentPat("f"), innerFuncType),
		},
		ts.NewUnknownType(nil),
		ts.NewNeverType(nil),
	)

	GeneralizeFuncType(funcType)

	// tvT should be found inside the inner func's type param constraint
	assert.Len(t, funcType.TypeParams, 1)
	assert.Equal(t, "T0", funcType.TypeParams[0].Name)
}

func TestGeneralizeFuncType_AppendsAfterExistingTypeParams(t *testing.T) {
	// Existing explicit type params should come first, generated ones after.
	tvNew := ts.NewTypeVarType(nil, 1) // unresolved, will be generalized

	funcType := ts.NewFuncType(
		nil,
		[]*ts.TypeParam{{Name: "A"}, {Name: "B"}},
		[]*ts.FuncParam{
			ts.NewFuncParam(ts.NewIdentPat("x"), ts.NewUnknownType(nil)),
			ts.NewFuncParam(ts.NewIdentPat("y"), tvNew),
		},
		ts.NewUnknownType(nil),
		ts.NewNeverType(nil),
	)

	GeneralizeFuncType(funcType)

	assert.Len(t, funcType.TypeParams, 3)
	assert.Equal(t, "A", funcType.TypeParams[0].Name)
	assert.Equal(t, "B", funcType.TypeParams[1].Name)
	assert.Equal(t, "T0", funcType.TypeParams[2].Name)
}

// TestDetermineCheckerAliasSource_UnionLifetimeParam exercises the case
// where a callee's parameter has a LifetimeUnion that overlaps the
// return's lifetime. Constructed directly because user-annotated lifetime
// unions don't yet flow through the type-annotation pipeline (inferTypeAnn
// doesn't populate Type.Lifetime), and InferLifetimes only assigns single
// LifetimeVars per param — so this code path has no script-level surface
// today. The fix in determineCheckerAliasSource should still recognize
// the overlap and propagate the matching argument's alias source.
func TestDetermineCheckerAliasSource_UnionLifetimeParam(t *testing.T) {
	// Lifetime variables: 'a, 'b.
	lifetimeA := &ts.LifetimeVar{ID: 1, Name: "a"}
	lifetimeB := &ts.LifetimeVar{ID: 2, Name: "b"}

	// A trivial type alias for `Point` so TypeRefType has somewhere to point.
	pointAlias := &ts.TypeAlias{Type: ts.NewUnknownType(nil)}

	makePointWithLifetime := func(lt ts.Lifetime) *ts.TypeRefType {
		t := ts.NewTypeRefType(nil, "Point", pointAlias)
		t.Lifetime = lt
		return t
	}

	// pick<'a, 'b>(
	//     unioned: ('a | 'b) Point,   // overlap on 'a → propagate
	//     returned: 'a Point,         // overlap on 'a → propagate
	//     other:    'b Point,         // no overlap with return → skip
	// ) -> 'a Point
	funcType := ts.NewFuncType(
		nil,
		nil,
		[]*ts.FuncParam{
			ts.NewFuncParam(
				ts.NewIdentPat("unioned"),
				makePointWithLifetime(&ts.LifetimeUnion{Lifetimes: []ts.Lifetime{lifetimeA, lifetimeB}}),
			),
			ts.NewFuncParam(ts.NewIdentPat("returned"), makePointWithLifetime(lifetimeA)),
			ts.NewFuncParam(ts.NewIdentPat("other"), makePointWithLifetime(lifetimeB)),
		},
		makePointWithLifetime(lifetimeA),
		ts.NewNeverType(nil),
	)
	funcType.LifetimeParams = []*ts.LifetimeVar{lifetimeA, lifetimeB}

	// Callee: an IdentExpr "pick" whose inferred type is the FuncType above.
	callee := ast.NewIdent("pick", ast.Span{})
	callee.SetInferredType(funcType)

	// Args: three IdentExprs with distinct VarIDs. liveness.DetermineAliasSource
	// reads VarID directly from the node.
	argUnioned := ast.NewIdent("u", ast.Span{})
	argUnioned.VarID = 101
	argReturned := ast.NewIdent("r", ast.Span{})
	argReturned.VarID = 102
	argOther := ast.NewIdent("o", ast.Span{})
	argOther.VarID = 103

	call := ast.NewCall(callee, []ast.Expr{argUnioned, argReturned, argOther}, false, ast.Span{})

	src := determineCheckerAliasSource(call)

	// Expect aliasing of args 1 and 2 (overlap on 'a), but not arg 3 ('b only).
	assert.Equal(t, liveness.AliasSourceMultiple, src.Kind,
		"two distinct sources should produce AliasSourceMultiple")
	assert.ElementsMatch(t,
		[]liveness.VarID{liveness.VarID(101), liveness.VarID(102)},
		src.VarIDs,
		"unioned arg (overlap on 'a) and returned arg (exact 'a) should propagate; other arg ('b only) should not")
}

func TestGeneralizeFuncType_ThrowsOnlyTypeVarBecomesNever(t *testing.T) {
	// A type var that only appears in throws should become never, not a type param.
	tvThrows := ts.NewTypeVarType(nil, 1) // unresolved, only in throws

	funcType := ts.NewFuncType(
		nil,
		nil,
		[]*ts.FuncParam{
			ts.NewFuncParam(ts.NewIdentPat("x"), ts.NewNumPrimType(nil)),
		},
		ts.NewNumPrimType(nil),
		tvThrows,
	)

	GeneralizeFuncType(funcType)

	assert.Len(t, funcType.TypeParams, 0)
	// tvThrows should have been resolved to never
	pruned := ts.Prune(tvThrows)
	_, isNever := pruned.(*ts.NeverType)
	assert.True(t, isNever)
}

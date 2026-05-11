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
	assert.Equal(t, liveness.AliasSourceMultiple, src.RootKind(),
		"two distinct sources should produce AliasSourceMultiple")
	assert.ElementsMatch(t,
		[]liveness.VarID{liveness.VarID(101), liveness.VarID(102)},
		src.UniqueVarIDs(),
		"unioned arg (overlap on 'a) and returned arg (exact 'a) should propagate; other arg ('b only) should not")
}

// TestGeneralizeFuncType_CyclicUnionDoesNotStackOverflow exercises issue
// #590: a cyclic UnionType (as formed by mutually recursive two-arm
// functions whose returns reference each other) must not send
// collectUnresolvedTypeVars into infinite recursion.
func TestGeneralizeFuncType_CyclicUnionDoesNotStackOverflow(t *testing.T) {
	// Build foo.Return = U1 = [T, bar.Return.TV] and
	//      bar.Return = U2 = [T, foo.Return.TV]
	// where Prune(foo.Return.TV) = U1 and Prune(bar.Return.TV) = U2,
	// matching the cyclic structure described in the issue.
	tvT := ts.NewTypeVarType(nil, 1)
	fooRetTV := ts.NewTypeVarType(nil, 2)
	barRetTV := ts.NewTypeVarType(nil, 3)

	u1 := ts.NewUnionType(nil, tvT, barRetTV).(*ts.UnionType)
	u2 := ts.NewUnionType(nil, tvT, fooRetTV).(*ts.UnionType)
	fooRetTV.Instance = u1
	barRetTV.Instance = u2

	fooType := ts.NewFuncType(
		nil, nil,
		[]*ts.FuncParam{ts.NewFuncParam(ts.NewIdentPat("x"), tvT)},
		fooRetTV,
		ts.NewNeverType(nil),
	)

	// Must not stack-overflow.
	GeneralizeFuncType(fooType)

	assert.Len(t, fooType.TypeParams, 1)
	assert.Equal(t, "T0", fooType.TypeParams[0].Name)
}

// TestSimplifyRecursiveCycles_ReachesViaFuncTypeParams verifies that the
// cyclic-type simplifier traverses FuncType.TypeParams[i].{Constraint,Default},
// mirroring collectUnresolvedTypeVarsImpl's coverage. Without this traversal,
// a cyclic union reachable only through a pre-existing TypeParam's Constraint
// slips past the simplifier and downstream walkers will loop on it.
func TestSimplifyRecursiveCycles_ReachesViaFuncTypeParams(t *testing.T) {
	selfRefTV := ts.NewTypeVarType(nil, 1)
	tvLeaf := ts.NewTypeVarType(nil, 2)
	cyclic := ts.NewUnionType(nil, tvLeaf, selfRefTV).(*ts.UnionType)
	selfRefTV.Instance = cyclic

	existingTP := &ts.TypeParam{Name: "U", Constraint: cyclic}
	funcType := ts.NewFuncType(
		nil,
		[]*ts.TypeParam{existingTP},
		[]*ts.FuncParam{ts.NewFuncParam(ts.NewIdentPat("x"), ts.NewNeverType(nil))},
		ts.NewNeverType(nil),
		ts.NewNeverType(nil),
	)

	simplifyRecursiveCycles([]*ts.FuncType{funcType})

	if got := len(cyclic.Types); got != 1 {
		t.Fatalf("cyclic union reachable via FuncType.TypeParams[i].Constraint not simplified: got %d elements, want 1", got)
	}
}

// TestSimplifyRecursiveCycles_ReachesViaMappedElemTypeParam verifies coverage
// of MappedElem.TypeParam.Constraint, which collectUnresolvedTypeVarsImpl
// visits but the simplifier's walk previously skipped.
func TestSimplifyRecursiveCycles_ReachesViaMappedElemTypeParam(t *testing.T) {
	selfRefTV := ts.NewTypeVarType(nil, 1)
	tvLeaf := ts.NewTypeVarType(nil, 2)
	cyclic := ts.NewUnionType(nil, tvLeaf, selfRefTV).(*ts.UnionType)
	selfRefTV.Instance = cyclic

	mapped := &ts.MappedElem{
		TypeParam: &ts.IndexParam{Name: "K", Constraint: cyclic},
		Name:      ts.NewNeverType(nil),
		Value:     ts.NewNeverType(nil),
	}
	obj := ts.NewObjectType(nil, []ts.ObjTypeElem{mapped})
	funcType := ts.NewFuncType(
		nil, nil,
		[]*ts.FuncParam{ts.NewFuncParam(ts.NewIdentPat("x"), obj)},
		ts.NewNeverType(nil),
		ts.NewNeverType(nil),
	)

	simplifyRecursiveCycles([]*ts.FuncType{funcType})

	if got := len(cyclic.Types); got != 1 {
		t.Fatalf("cyclic union reachable via MappedElem.TypeParam.Constraint not simplified: got %d elements, want 1", got)
	}
}

// TestSimplifyRecursiveCycles_IntersectionCycle verifies that a cyclic
// IntersectionType is simplified the same way as a cyclic UnionType.
// Intersection is idempotent (T & T = T), so a self-referencing element
// is redundant; absorption (T & (T | I) = T) lets us drop the element
// without changing the type's meaning.
func TestSimplifyRecursiveCycles_IntersectionCycle(t *testing.T) {
	selfRefTV := ts.NewTypeVarType(nil, 1)
	tvLeaf := ts.NewTypeVarType(nil, 2)
	cyclic := ts.NewIntersectionType(nil, tvLeaf, selfRefTV).(*ts.IntersectionType)
	selfRefTV.Instance = cyclic

	funcType := ts.NewFuncType(
		nil, nil,
		[]*ts.FuncParam{ts.NewFuncParam(ts.NewIdentPat("x"), ts.NewNeverType(nil))},
		cyclic,
		ts.NewNeverType(nil),
	)

	simplifyRecursiveCycles([]*ts.FuncType{funcType})

	if got := len(cyclic.Types); got != 1 {
		t.Fatalf("cyclic intersection not simplified: got %d elements, want 1", got)
	}
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

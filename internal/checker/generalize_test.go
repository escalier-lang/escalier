package checker

import (
	"testing"

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
	// T0's constraint should reference T1 (Array<T1>)
	assert.NotNil(t, funcType.TypeParams[0].Constraint)
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

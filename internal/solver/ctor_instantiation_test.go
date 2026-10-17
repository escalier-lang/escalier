package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestACtorCallBindsOneCopyOfEachClassParameter asserts that `Box(5:number)` binds a single
// variable for `T`. The class value's constructor binder, the class type the call returns,
// and the union the copied `getValue` link reaches all name that one variable.
func TestACtorCallBindsOneCopyOfEachClassParameter(t *testing.T) {
	module := parseModule(t, `
		class Box<T> {
			value: T,
			getValue<U>(&self, d: U) -> T | U {
				if self.value != 0 { return self.value } else { return d }
			},
		}
		val box = Box(5:number)
	`)
	c := newTestChecker()
	scope := c.preludeScope().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	require.Empty(t, c.errs)

	ns, _ := module.Namespaces.Get("")
	var call *ast.CallExpr
	for _, d := range ns.Decls {
		if vd, ok := d.(*ast.VarDecl); ok {
			call = vd.Init.(*ast.CallExpr)
		}
	}
	require.NotNil(t, call)

	calleeVar, ok := c.info.TypeOf(call.Callee).(*soltype.TypeVarType)
	require.True(t, ok)
	var param *soltype.TypeVarType
	for _, lb := range calleeVar.LowerBounds {
		if obj, ok := lb.(*soltype.ObjectType); ok {
			for _, elem := range obj.Elems {
				if ctor, ok := elem.(*soltype.ConstructorElem); ok {
					param = ctor.Signatures[0].TypeParams[0].Var
				}
			}
		}
	}
	require.NotNil(t, param)

	resultVar, ok := c.info.TypeOf(call).(*soltype.TypeVarType)
	require.True(t, ok)
	require.NotEmpty(t, resultVar.LowerBounds)
	for _, lb := range resultVar.LowerBounds {
		ct, ok := lb.(*soltype.ClassType)
		require.True(t, ok)
		require.Same(t, param, ct.TypeArgs[0])
	}

	var linked *soltype.UnionType
	for _, ub := range param.UpperBounds {
		if link, ok := ub.(*soltype.TypeVarType); ok {
			for _, linkUb := range link.UpperBounds {
				if u, ok := linkUb.(*soltype.UnionType); ok {
					linked = u
				}
			}
		}
	}
	require.NotNil(t, linked)
	require.Contains(t, linked.Types, soltype.Type(param))
}

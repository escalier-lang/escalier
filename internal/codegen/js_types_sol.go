package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// SolTypes is what one internal/solver run concluded, as JavaScript emission reads
// it. internal/compiler supplies an implementation over the run's side table and its
// registry of named types.
type SolTypes interface {
	// ResolvedTypeOf returns the type recorded for a node with any inference variable
	// resolved to the shape it settled on, or nil when the node carries none. An
	// unresolved variable answers no type switch, so emission reads this rather than
	// what the walk recorded as it passed.
	ResolvedTypeOf(n ast.Node) soltype.Type
	// NamespaceMemberDecl returns the declaration a namespace member read resolved
	// to, and reports whether the node is such a read.
	NamespaceMemberDecl(n ast.Node) (ast.Decl, bool)
	// IsMethodRead reports whether the member read n resolved to a method that runs
	// against the value it was read from.
	IsMethodRead(n ast.Node) bool
	// AbsentKeys returns the keys the object pattern p's runtime test also checks are
	// absent.
	AbsentKeys(p *ast.ObjectPat) []string
	// Expand returns t with each alias on the outside expanded, until none is left.
	Expand(t soltype.Type) soltype.Type
}

// SolverJSTypes answers from one internal/solver run.
type SolverJSTypes struct {
	Types SolTypes
}

// IsNominalTypeRef reports whether the reference resolved to a class. A class is
// nominal in soltype by being a `ClassType` at all, so there is no shape to inspect.
//
// An alias of a class answers false, even though the class it names is nominal. The
// guard this feeds tests the name the annotation wrote, and an alias declares no
// runtime binding to test against. Given `type B = Point`, a true here would emit
// `value instanceof B` and throw a ReferenceError.
func (s SolverJSTypes) IsNominalTypeRef(t *ast.TypeRefTypeAnn) bool {
	_, isClass := s.Types.ResolvedTypeOf(t).(*soltype.ClassType)
	return isClass
}

// CalleeConstructs reports whether the callee's type carries a constructor. A class's
// value is an object holding its constructor signatures beside its statics, so the
// test is for that element rather than for the class type itself, which is the type of
// an instance.
func (s SolverJSTypes) CalleeConstructs(callee ast.Expr) bool {
	objType, ok := s.Types.ResolvedTypeOf(callee).(*soltype.ObjectType)
	if !ok {
		return false
	}
	for _, elem := range objType.Elems {
		if _, isConstructor := elem.(*soltype.ConstructorElem); isConstructor {
			return true
		}
	}
	return false
}

// IsMethodRead reports whether the read resolved to such a method. The run recorded
// it during the walk, because the value a method read yields has its receiver stripped
// and so looks the same as a field holding a plain function.
func (s SolverJSTypes) IsMethodRead(e ast.Expr) bool {
	return s.Types.IsMethodRead(e)
}

// ExpandedTypeOf expands the type the run recorded for the annotation.
func (s SolverJSTypes) ExpandedTypeOf(ta ast.TypeAnn) soltype.Type {
	t := s.Types.ResolvedTypeOf(ta)
	if t == nil {
		return nil
	}
	return s.Types.Expand(t)
}

// Expand expands t against the run's alias registry.
func (s SolverJSTypes) Expand(t soltype.Type) soltype.Type {
	return s.Types.Expand(t)
}

// AbsentKeys reads the keys the run recorded for p.
func (s SolverJSTypes) AbsentKeys(p *ast.ObjectPat) []string {
	return s.Types.AbsentKeys(p)
}

// NullableArms reads the union arms that are `null` and `undefined`. soltype gives
// each its own former rather than holding it as a literal, so the arms are matched by
// type rather than by reading a literal's value.
func (s SolverJSTypes) NullableArms(e ast.Expr) (bool, bool) {
	unionType, ok := s.Types.ResolvedTypeOf(e).(*soltype.UnionType)
	if !ok {
		return false, false
	}
	var hasNull, hasUndefined bool
	for _, arm := range unionType.Types {
		switch soltype.CarrierOf(arm).(type) {
		case *soltype.NullType:
			hasNull = true
		case *soltype.UndefinedType:
			hasUndefined = true
		}
	}
	return hasNull, hasUndefined
}

// MemberJSExpr reads the `@js("…")` argument off the declaration the member resolved
// to through its namespace. The run recorded which declaration that was, because a
// resolved member's type says what the member is rather than where it came from.
func (s SolverJSTypes) MemberJSExpr(m *ast.MemberExpr) (string, bool) {
	decl, ok := s.Types.NamespaceMemberDecl(m)
	if !ok {
		return "", false
	}
	_, arg, ok := ast.FindJsDecorator(decl)
	if !ok {
		return "", false
	}
	return arg, true
}

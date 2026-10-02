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
	// TypeBodyOf returns what a named class or alias stands for, and reports whether
	// the name is registered. A `ClassType` and an `AliasType` carry a name rather
	// than a body, so following one takes a registry lookup.
	TypeBodyOf(name string) (soltype.Type, bool)
}

// SolverJSTypes answers from one internal/solver run.
type SolverJSTypes struct {
	Types SolTypes
}

// IsNominalTypeRef reports whether the reference resolved to a class. A class is
// nominal in soltype by being a ClassType at all, where type_system marks an object
// type nominal with a flag, so there is no shape to inspect. An alias is followed to
// what it stands for, so `type Alias = Point` is as nominal as `Point`.
func (s SolverJSTypes) IsNominalTypeRef(t *ast.TypeRefTypeAnn) bool {
	return s.isNominal(s.Types.ResolvedTypeOf(t))
}

// isNominal follows at most one alias. An alias of an alias is registered under the
// name it ultimately stands for, so one step reaches the class.
func (s SolverJSTypes) isNominal(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.ClassType:
		return true
	case *soltype.AliasType:
		body, ok := s.Types.TypeBodyOf(t.Name)
		if !ok {
			return false
		}
		_, isClass := body.(*soltype.ClassType)
		return isClass
	default:
		return false
	}
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

// IsFunc reports whether the expression resolved to a function type.
func (s SolverJSTypes) IsFunc(e ast.Expr) bool {
	_, ok := s.Types.ResolvedTypeOf(e).(*soltype.FuncType)
	return ok
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

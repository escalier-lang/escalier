package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// JSTypes answers the questions JavaScript emission asks about what inference
// concluded. Everything else in the emitter reads the AST and the dep graph, both of
// which are the same whichever checker ran, so this interface is the whole of
// codegen's dependency on a type representation.
//
// Each checker holds its results in its own form: internal/checker stamps a type onto
// each AST node, and internal/solver records types in a side table keyed by node.
// Neither converts to the other's representation, so each supplies its own
// implementation of this interface.
//
// A Builder with no JSTypes answers every question the way an untyped tree does, so
// emission still runs and produces the plainest form of each construct. That is the
// right answer for a test that builds an AST by hand and never infers it. It is the
// wrong answer for a real compile, where a false here silently drops a `new`, a
// `.bind`, or a null guard. NewBuilder takes an implementation, so a caller outside
// this package cannot reach emission without one.
type JSTypes interface {
	// IsNominalTypeRef reports whether a type reference names a nominal type, which
	// a pattern match tests with `x instanceof C` rather than by shape.
	IsNominalTypeRef(t *ast.TypeRefTypeAnn) bool
	// CalleeConstructs reports whether a call's callee carries a constructor, which
	// emits `new C(…)` rather than `C(…)`.
	CalleeConstructs(callee ast.Expr) bool
	// IsFunc reports whether an expression's type is a function. A member read that
	// is a function and is not itself being called emits `obj.m.bind(obj)`, so the
	// method keeps its receiver when it travels as a value.
	IsFunc(e ast.Expr) bool
	// NullableArms reports whether an expression's type is a union admitting `null`
	// or `undefined`. An `if val` guards its pattern test with the checks these ask
	// for.
	NullableArms(e ast.Expr) (hasNull, hasUndefined bool)
	// MemberJSExpr returns the JavaScript a member read lowers to when it resolves to
	// a declaration carrying an `@js("…")` decorator, and reports whether it does.
	// The receiver vanishes, because the decorator argument is a complete
	// JS-runtime expression: `math.sin` lowers to `Math.sin`.
	MemberJSExpr(m *ast.MemberExpr) (string, bool)
}

// noJSTypes answers as an untyped tree does. A nil JSTypes field reads as this, so
// every question has one answer path rather than a nil check at each call site.
type noJSTypes struct{}

func (noJSTypes) IsNominalTypeRef(*ast.TypeRefTypeAnn) bool   { return false }
func (noJSTypes) CalleeConstructs(ast.Expr) bool              { return false }
func (noJSTypes) IsFunc(ast.Expr) bool                        { return false }
func (noJSTypes) NullableArms(ast.Expr) (bool, bool)          { return false, false }
func (noJSTypes) MemberJSExpr(*ast.MemberExpr) (string, bool) { return "", false }

// types returns the oracle to ask, which is noJSTypes for a Builder that was given
// none.
func (b *Builder) types() JSTypes {
	if b.jsTypes == nil {
		return noJSTypes{}
	}
	return b.jsTypes
}

// CheckerJSTypes answers from internal/checker's results, which it reads off the AST
// through the inferred-type field that checker stamps onto each node.
type CheckerJSTypes struct{}

// IsNominalTypeRef follows a reference to the object type it names, directly or
// through a type alias, and reports whether that object is nominal.
func (CheckerJSTypes) IsNominalTypeRef(t *ast.TypeRefTypeAnn) bool {
	inferred := t.InferredType()
	if inferred == nil {
		return false
	}
	pruned := type_system.Prune(inferred)
	if typeRef, ok := pruned.(*type_system.TypeRefType); ok && typeRef.TypeAlias != nil {
		if obj, ok := type_system.Prune(typeRef.TypeAlias.Type).(*type_system.ObjectType); ok && obj.Nominal {
			return true
		}
	}
	obj, ok := pruned.(*type_system.ObjectType)
	return ok && obj.Nominal
}

// CalleeConstructs reports whether the callee's type is an object carrying a
// constructor element.
func (CheckerJSTypes) CalleeConstructs(callee ast.Expr) bool {
	objType, ok := callee.InferredType().(*type_system.ObjectType)
	if !ok {
		return false
	}
	for _, elem := range objType.Elems {
		if _, isConstructor := elem.(*type_system.ConstructorElem); isConstructor {
			return true
		}
	}
	return false
}

// IsFunc reports whether the expression's type is a function type.
func (CheckerJSTypes) IsFunc(e ast.Expr) bool {
	_, ok := e.InferredType().(*type_system.FuncType)
	return ok
}

// NullableArms reads the union arms that are the `null` and `undefined` literals. A
// type that is not a union admits neither, so it answers false twice.
func (CheckerJSTypes) NullableArms(e ast.Expr) (bool, bool) {
	inferred := e.InferredType()
	if inferred == nil {
		return false, false
	}
	unionType, ok := type_system.Prune(inferred).(*type_system.UnionType)
	if !ok {
		return false, false
	}
	var hasNull, hasUndefined bool
	for _, t := range unionType.Types {
		litType, ok := type_system.Prune(t).(*type_system.LitType)
		if !ok {
			continue
		}
		if _, isNull := litType.Lit.(*type_system.NullLit); isNull {
			hasNull = true
		}
		if _, isUndefined := litType.Lit.(*type_system.UndefinedLit); isUndefined {
			hasUndefined = true
		}
	}
	return hasNull, hasUndefined
}

// MemberJSExpr resolves the property through the namespace the receiver's type names,
// then reads the `@js("…")` argument off the declaration that bound it.
func (CheckerJSTypes) MemberJSExpr(m *ast.MemberExpr) (string, bool) {
	if m.Prop == nil {
		return "", false
	}
	objType := m.Object.InferredType()
	if objType == nil {
		return "", false
	}
	nsType, ok := type_system.Prune(objType).(*type_system.NamespaceType)
	if !ok || nsType.Namespace == nil {
		return "", false
	}
	binding := nsType.Namespace.Values[m.Prop.Name]
	if binding == nil {
		return "", false
	}
	return jsExprFromOwner(binding.Owner)
}

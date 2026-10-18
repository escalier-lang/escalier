package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// JSTypes answers the questions JavaScript emission asks about what inference
// concluded. Everything else in the emitter reads the AST and the dep graph, so this
// interface is the whole of codegen's dependency on a type representation.
// SolverJSTypes implements it over the side table internal/solver records types in.
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
	// IsMethodRead reports whether a member read `obj.m` or an index read `obj["m"]`
	// resolved to a method that runs against the value it was read from. Such a read that
	// is not itself being called emits `obj.m.bind(obj)`, so the method keeps its receiver
	// when it travels as a value. A field holding a plain function reads no receiver and
	// emits no `bind`. Any other expression reports false.
	IsMethodRead(e ast.Expr) bool
	// AbsentKeys returns the keys an object pattern's runtime test also checks are absent,
	// beyond the keys the pattern names. Each emits `!("key" in value)`.
	AbsentKeys(p *ast.ObjectPat) []string
	// ExpandedTypeOf returns the type an annotation resolved to, with each alias on the
	// outside expanded, or nil when it records none. `Digits` under `type Digits = /^\d+$/`
	// returns the regex type `/^\d+$/`.
	ExpandedTypeOf(ta ast.TypeAnn) soltype.Type
	// Expand returns t with each alias on the outside expanded, until none is left. It
	// returns t unchanged when it cannot expand it.
	Expand(t soltype.Type) soltype.Type
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
func (noJSTypes) IsMethodRead(ast.Expr) bool                  { return false }
func (noJSTypes) AbsentKeys(*ast.ObjectPat) []string          { return nil }
func (noJSTypes) ExpandedTypeOf(ast.TypeAnn) soltype.Type     { return nil }
func (noJSTypes) Expand(t soltype.Type) soltype.Type          { return t }
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

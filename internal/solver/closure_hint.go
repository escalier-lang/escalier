package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferExprIn infers e where its value flows into a destination of type dest. When e is a
// function expression and dest is a function type, each parameter e leaves unannotated takes
// the type of dest's parameter at the same position, before e's body is checked.
func (c *checker) inferExprIn(scope *Scope, lvl int, e ast.Expr, dest soltype.Type) soltype.Type {
	if fe, ok := e.(*ast.FuncExpr); ok {
		if ft := contextFuncType(dest); ft != nil {
			c.closureHint, c.closureHintFor = ft, fe
		}
	}
	return c.inferExpr(scope, lvl, e)
}

// contextFuncType returns the function type t, or nil when t is not one. A function type that
// quantifies type or lifetime parameters returns nil too. A closure is checked against one with
// each binder held rigid, which a parameter type copied out of it would not be.
func contextFuncType(t soltype.Type) *soltype.FuncType {
	ft, ok := t.(*soltype.FuncType)
	if !ok || quantified(ft) {
		return nil
	}
	return ft
}

// takeClosureHint returns the function type node flows into, as inferExprIn recorded it, and
// clears the record. It returns nil when node is not the function expression the record names.
func (c *checker) takeClosureHint(node ast.Node) *soltype.FuncType {
	hint := c.closureHint
	if c.closureHintFor == nil || ast.Node(c.closureHintFor) != node {
		hint = nil
	}
	c.closureHint, c.closureHintFor = nil, nil
	return hint
}

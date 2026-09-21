package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferBinary types an operator application `left op right`.
//
// An operator's name is an ordinary value binding. addOperatorBindings seeds `+`
// as `(number, number) -> number`, so the walk resolves that binding, constrains
// each operand against the parameter in its position, and yields the signature's
// return type. `1 + 2` constrains `1 <: number` and `2 <: number`, then yields
// `number`.
//
// Both operands are typed before the operator is resolved, so an operand's own
// diagnostics are reported even when the operator has no binding.
//
// The assignment form `a = expr` writes to a place rather than applying an
// operator, so inferExpr routes it to inferAssign before reaching here.
func (c *checker) inferBinary(scope *Scope, lvl int, e *ast.BinaryExpr) soltype.Type {
	// The parser substitutes ast.NewError for a missing operand, so a nil operand is
	// unreachable from source. A hand-built AST can still carry one. Blame the whole
	// expression rather than dereferencing the operand below.
	if e.Left == nil || e.Right == nil {
		return c.reportUnsupported(e)
	}
	leftT := c.inferExpr(scope, lvl, e.Left)
	rightT := c.inferExpr(scope, lvl, e.Right)

	fn, ok := c.operatorSignature(scope, lvl, string(e.Op), 2)
	if !ok {
		// ast.Modulo and ast.NullishCoalescing have no prelude binding, and the parser
		// emits neither, so source cannot reach this. A hand-built AST can. The node
		// kind is supported and the operator is not, so name the operator rather than
		// the kind.
		return c.reportUnsupportedFeature(e, "operator "+string(e.Op))
	}
	// Each operand is constrained at its OWN node, so a rejected operand blames that
	// operand instead of the whole expression. `"x" * 2` blames `"x"`.
	c.constrain(e.Left, leftT, fn.Params[0].Type)
	c.constrain(e.Right, rightT, fn.Params[1].Type)

	// The return type comes straight off the prelude signature, which every use of
	// the operator shares. Nothing records provenance against it. Prov is keyed by
	// pointer identity, so one entry would file every `+` in the module under
	// whichever expression wrote it last. A later constraint that needs blame falls
	// back to its own site instead.
	c.recordType(e, fn.Ret)
	return fn.Ret
}

// operatorSignature resolves an operator's value binding to the signature an
// application of it checks against. op is the operator's name as
// addOperatorBindings binds it. arity is the number of operands the application
// supplies, so a caller may index Params up to arity once this returns true.
//
// It returns false when the name has no value binding, when the binding is an
// overload set, when it holds something other than a function, or when that
// function declares a different number of parameters. A caller reports the
// operator as unsupported in each of those cases.
func (c *checker) operatorSignature(scope *Scope, lvl int, op string, arity int) (*soltype.FuncType, bool) {
	b, found := scope.GetValue(op)
	if !found || len(b.Schemes) != 1 {
		return nil, false
	}
	fn, isFunc := c.instantiate(b.Schemes[0], lvl).(*soltype.FuncType)
	if !isFunc || len(fn.Params) != arity {
		return nil, false
	}
	return fn, true
}

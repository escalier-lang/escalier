package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (ast.Type, []*Error) {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := ast.NewNeverType()

		opOption := ctx.Scope.getValue(string(expr.Op))
		if opOption.IsNone() {
			return neverType, []*Error{{
				message: "Unknown operator " + string(expr.Op),
			}}
		}
		opType := opOption.Unwrap()

		// TODO: extract this into a unifyCall method
		if fnType, ok := opType.(*ast.FuncType); ok {
			if len(fnType.Params) != 2 {
				return neverType, []*Error{{
					message: "Invalid number of arguments for operator " + string(expr.Op),
				}}
			}

			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)
			errors := append(leftErrors, rightErrors...)

			leftErrors = c.unify(ctx, leftType, fnType.Params[0].Type)
			errors = append(errors, leftErrors...)
			rightErrors = c.unify(ctx, rightType, fnType.Params[1].Type)
			errors = append(errors, rightErrors...)

			if len(errors) > 0 {
				return neverType, errors
			}

			return fnType.Return, nil
		}

		return neverType, []*Error{{
			message: "Operator " + string(expr.Op) + " is not a function",
		}}
	case *ast.UnaryExpr:

		return ast.NewNeverType(), []*Error{}
	case *ast.CallExpr:
		panic("call expression not implemented")
	case *ast.MemberExpr:
		panic("member expression not implemented")
	case *ast.IdentExpr:
		panic("identifier expression not implemented")
	case *ast.LiteralExpr:
		panic("literal expression not implemented")
	case *ast.TupleExpr:
		panic("tuple expression not implemented")
	case *ast.ObjectExpr:
		panic("object expression not implemented")
	default:
		return nil, []*Error{{message: "Unknown expression type"}}
	}
}

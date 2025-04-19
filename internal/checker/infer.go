package checker

import (
	"iter"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/moznion/go-optional"
)

var DUMMY_SPAN = ast.Span{
	Start: ast.Location{
		Line:   0,
		Column: 0,
	},
	End: ast.Location{
		Line:   0,
		Column: 0,
	},
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (Type, []*Error) {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := NewNeverType()

		opOption := ctx.Scope.getValue(string(expr.Op))
		if opOption.IsNone() {
			return neverType, []*Error{{
				message: "Unknown operator " + string(expr.Op),
			}}
		}
		opType := opOption.Unwrap()

		// TODO: extract this into a unifyCall method
		if fnType, ok := opType.(*FuncType); ok {
			if len(fnType.Params) != 2 {
				return neverType, []*Error{{
					message: "Invalid number of arguments for operator " + string(expr.Op),
				}}
			}

			errors := []*Error{}

			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)
			errors = slices.Concat(errors, leftErrors, rightErrors)

			leftErrors = c.unify(ctx, leftType, fnType.Params[0].Type)
			rightErrors = c.unify(ctx, rightType, fnType.Params[1].Type)
			errors = slices.Concat(errors, leftErrors, rightErrors)

			if len(errors) > 0 {
				return neverType, errors
			}

			return fnType.Return, nil
		}

		return neverType, []*Error{{
			message: "Operator " + string(expr.Op) + " is not a function",
		}}
	case *ast.UnaryExpr:

		return NewNeverType(), []*Error{}
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

func (c *Checker) inferDecl(ctx Context, decl ast.Decl) (Type, []*Error) {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return c.inferFuncDecl(ctx, decl)
	// case *ast.VarDecl:
	// 	return c.inferVarDecl(ctx, decl)
	// case *ast.TypeDecl:
	// 	return c.inferTypeDecl(ctx, decl)
	default:
		return nil, []*Error{{message: "Unknown declaration type"}}
	}
}

func (c *Checker) inferFuncDecl(ctx Context, decl *ast.FuncDecl) (Type, []*Error) {
	// create a placeholder function type
	// placeholder := &ast.FuncType{
	// 	Params:     decl.Params,
	// 	Return:     ast.NewNeverType(),
	// 	Throws:     ast.NewNeverType(),
	// 	TypeParams: []*ast.TypeParam[Type]{},
	// 	Self:       optional.None[Type](),
	// }

	panic("TODO: infer function type")
}

func (c *Checker) inferStmt(ctx Context, stmt ast.Stmt) []*Error {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		_, errors := c.inferExpr(ctx, stmt.Expr)
		return errors
	case *ast.DeclStmt:
		_, errors := c.inferDecl(ctx, stmt.Decl)
		return errors
	case *ast.ReturnStmt:
		errors := []*Error{}
		optional.Map(stmt.Expr, func(expr ast.Expr) Type {
			t, exprErrors := c.inferExpr(ctx, expr)
			errors = exprErrors
			return t
		}).TakeOrElse(func() Type {
			return NewLitType(&UndefinedLit{})
		})
		return errors
	default:
		return []*Error{{message: "Unknown statement type"}}
	}
}

func Zip[T, U any](t []T, u []U) iter.Seq2[T, U] {
	return func(yield func(T, U) bool) {
		for i := range min(len(t), len(u)) { // range over int (Go 1.22)
			if !yield(t[i], u[i]) {
				return
			}
		}
	}
}

// func (c *Checker) inferFuncSig(ctx Context, sig *ast.FuncSig) (Type, []*Error) {
// 	typeParams := c.inferTypeParams(ctx, sig.TypeParams)

// 	params := make([]*ast.FuncParam, len(sig.Params))
// 	for i, param := range sig.Params {
// 		paramType := optional.Map(param, func(t Type) Type {
// 			return t
// 		}).TakeOrElse(func() Type {
// 			return c.FreshVar()
// 		})
// 	}

// 	panic("TODO: infer function signature")
// }

func (c *Checker) inferFunc(
	ctx Context,
	placeholder FuncType,
	sig *ast.FuncSig,
	body *ast.Block,
) (Type, []*Error) {
	if sig == nil {
		return nil, []*Error{{message: "Function signature is nil"}}
	}

	if body == nil {
		return nil, []*Error{{message: "Function body is nil"}}
	}

	newCtx := ctx.WithScope(ctx.Scope)
	errors := []*Error{}

	for placeholderParam, sigParam := range Zip(placeholder.Params, sig.Params) {
		patType, bindings, paramErrors := c.inferPattern(ctx, sigParam.Pattern)
		errors = slices.Concat(errors, paramErrors)

		c.unify(ctx, patType, placeholderParam.Type)

		for name, binding := range bindings {
			newCtx.Scope.Values[name] = binding.Type
		}
	}

	for _, stmt := range body.Stmts {
		stmtErrors := c.inferStmt(newCtx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	// TODO: find return types

	returnType := NewLitType(&UndefinedLit{})

	funcType := &FuncType{
		Params:     placeholder.Params,
		Return:     returnType,
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
		Self:       optional.None[Type](),
	}

	return funcType, errors
}

type Binding struct {
	Type    Type
	Mutable bool
}

func (c *Checker) inferLit(lit ast.Lit) (Type, []*Error) {
	switch lit := lit.(type) {
	case *ast.StrLit:
		return NewLitType(&StrLit{Value: lit.Value}), []*Error{}
	case *ast.NumLit:
		return NewLitType(&NumLit{Value: lit.Value}), []*Error{}
	case *ast.BoolLit:
		return NewLitType(&BoolLit{Value: lit.Value}), []*Error{}
	case *ast.BigIntLit:
		return NewLitType(&BigIntLit{Value: lit.Value}), []*Error{}
	case *ast.NullLit:
		return NewLitType(&NullLit{}), []*Error{}
	case *ast.UndefinedLit:
		return NewLitType(&UndefinedLit{}), []*Error{}
	default:
		return NewNeverType(), []*Error{{message: "Unknown literal type"}}
	}
}

// TODO: return a list of bindings for the pattern
func (c *Checker) inferPattern(ctx Context, pattern ast.Pat) (Type, map[string]Binding, []*Error) {

	bindings := map[string]Binding{}
	var inferPatRec func(ast.Pat) (Type, []*Error)

	inferPatRec = func(pat ast.Pat) (Type, []*Error) {
		switch p := pattern.(type) {
		case *ast.IdentPat:
			t := c.FreshVar()
			// TODO: report an error if the name is already bound
			bindings[p.Name] = Binding{
				Type:    t,
				Mutable: false, // TODO
			}
			return t, []*Error{}
		case *ast.LitPat:
			return c.inferLit(p.Lit)
		case *ast.TuplePat:
			elems := make([]Type, len(p.Elems))
			errors := []*Error{}
			for i, elem := range p.Elems {
				elemType, elemErrors := inferPatRec(elem)
				elems[i] = elemType
				errors = append(errors, elemErrors...)
			}
			return NewTupleType(elems...), errors
		case *ast.ObjectPat:
			elems := make([]ObjTypeElem, len(p.Elems))
			errors := []*Error{}
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := &StrObjTypeKey{
						Value: elem.Key,
					}
					elems = append(elems, &PropertyElemType{
						Name:     name,
						Value:    t,
						Optional: false,
						Readonly: false, // TODO: when should this be true?
					})
				case *ast.ObjShorthandPat:
					t := c.FreshVar()
					name := &StrObjTypeKey{
						Value: elem.Key,
					}
					// TODO: report an error if the name is already bound
					bindings[elem.Key] = Binding{
						Type:    t,
						Mutable: false, // TODO
					}
					elems = append(elems, &PropertyElemType{
						Name:     name,
						Value:    t,
						Optional: false,
						Readonly: false, // TODO: when should this be true?
					})
				case *ast.ObjRestPat:
					panic("object pattern not implemented")
				}
			}
			t := NewObjectType(elems)
			return t, errors
		case *ast.ExtractorPat:
			errors := []*Error{}
			t := optional.Map(
				ctx.Scope.getValue(p.Name),
				func(t Type) Type {
					args := make([]Type, len(p.Args))
					for i, arg := range p.Args {
						argType, argErrors := inferPatRec(arg)
						args[i] = argType
						errors = append(errors, argErrors...)
					}
					return NewExtractorType(t, args...)
				},
			).TakeOrElse(func() Type { return NewNeverType() })
			return t, errors
		case *ast.IsPat:
			panic("IGNORE: this will be replaced with an Assertion filed on some patterns")
		case *ast.RestPat:
			return NewRestSpreadType(c.FreshVar()), []*Error{}
		case *ast.WildcardPat:
			return c.FreshVar(), []*Error{}
		}

		panic("unknown pattern type")
	}

	t, errors := inferPatRec(pattern)
	pattern.SetInferredType(t)

	return t, bindings, errors
}

package checker

import (
	"iter"
	"slices"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/moznion/go-optional"
)

// TODO: Return a namespace instead of a type
// A namespace is a mapping of names to types
func (c *Checker) Infer(ctx Context, m *ast.Module) (map[string]Binding, []*Error) {
	errors := []*Error{}
	bindings := map[string]Binding{}

	for _, stmt := range m.Stmts {
		switch stmt := stmt.(type) {
		case *ast.DeclStmt:
			newBindings, declErrors := c.inferDecl(ctx, stmt.Decl)
			// TODO: check for duplicate bindings
			maps.Copy(bindings, newBindings)
			errors = slices.Concat(errors, declErrors)
		case *ast.ExprStmt:
			panic("TODO: infer expression statement")
		case *ast.ReturnStmt:
			panic("TODO: infer return statement")
		}
	}

	return bindings, errors
}

func (c *Checker) inferDecl(ctx Context, decl ast.Decl) (map[string]Binding, []*Error) {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return c.inferFuncDecl(ctx, decl)
	case *ast.VarDecl:
		return c.inferVarDecl(ctx, decl)
	// case *ast.TypeDecl:
	// 	return c.inferTypeDecl(ctx, decl)
	default:
		return nil, []*Error{{message: "Unknown declaration type"}}
	}
}

func (c *Checker) inferVarDecl(ctx Context, decl *ast.VarDecl) (map[string]Binding, []*Error) {
	errors := []*Error{}

	patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
	errors = slices.Concat(errors, patErrors)

	if decl.Init.IsNone() {
		return nil, errors
	}

	initType, initErrors := c.inferExpr(ctx, decl.Init.Unwrap())
	errors = slices.Concat(errors, initErrors)

	c.unify(ctx, patType, initType)

	for name, binding := range bindings {
		ctx.Scope.Values[name] = binding
	}

	return bindings, errors
}

func (c *Checker) inferFuncDecl(ctx Context, decl *ast.FuncDecl) (map[string]Binding, []*Error) {
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
		// TODO: look up the identifier in the context's scope
		// We should be able to determine where an identifier was declared by
		// using the provenance of the identifier's inferred type.
		if ctx.Scope.getValue(expr.Name).IsSome() {
			t := ctx.Scope.getValue(expr.Name).Unwrap()
			expr.SetInferredType(t)
			return t, nil
		} else {
			t := NewNeverType()
			expr.SetInferredType(t)
			return t, []*Error{{
				message: "Unknown identifier " + expr.Name,
			}}
		}
	case *ast.LiteralExpr:
		return c.inferLit(expr.Lit)
	case *ast.TupleExpr:
		panic("tuple expression not implemented")
	case *ast.ObjectExpr:
		panic("object expression not implemented")
	default:
		return nil, []*Error{{message: "Unknown expression type"}}
	}
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
			newCtx.Scope.Values[name] = binding
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

// We want to model both `let x = 5` as well as `fn (x: number) => x`
type Binding struct {
	Source  optional.Option[ast.BindingSource]
	Type    Type
	Mutable bool
}

func (c *Checker) inferLit(lit ast.Lit) (Type, []*Error) {
	var t Type
	errors := []*Error{}
	switch lit := lit.(type) {
	case *ast.StrLit:
		t = NewLitType(&StrLit{Value: lit.Value})
	case *ast.NumLit:
		t = NewLitType(&NumLit{Value: lit.Value})
	case *ast.BoolLit:
		t = NewLitType(&BoolLit{Value: lit.Value})
	case *ast.BigIntLit:
		t = NewLitType(&BigIntLit{Value: lit.Value})
	case *ast.NullLit:
		t = NewLitType(&NullLit{})
	case *ast.UndefinedLit:
		t = NewLitType(&UndefinedLit{})
	default:
		t = NewNeverType()
		errors = []*Error{{message: "Unknown literal type"}}
	}
	t.SetProvenance(&ast.LitProvenance{
		Lit: lit,
	})
	return t, errors
}

func (c *Checker) inferPattern(
	ctx Context,
	pattern ast.Pat,
) (Type, map[string]Binding, []*Error) {

	bindings := map[string]Binding{}
	var inferPatRec func(ast.Pat) (Type, []*Error)

	inferPatRec = func(pat ast.Pat) (Type, []*Error) {
		var t Type
		var errors []*Error

		switch p := pat.(type) {
		case *ast.IdentPat:
			t = c.FreshVar()
			t.SetProvenance(&ast.PatProvenance{
				Pat: p,
			})
			// TODO: report an error if the name is already bound
			bindings[p.Name] = Binding{
				Source:  optional.Some[ast.BindingSource](p),
				Type:    t,
				Mutable: false, // TODO
			}
			errors = []*Error{}
		case *ast.LitPat:
			t, errors = c.inferLit(p.Lit)
		case *ast.TuplePat:
			elems := make([]Type, len(p.Elems))
			for i, elem := range p.Elems {
				elemType, elemErrors := inferPatRec(elem)
				elems[i] = elemType
				errors = append(errors, elemErrors...)
			}
			t = NewTupleType(elems...)
		case *ast.ObjectPat:
			elems := make([]ObjTypeElem, len(p.Elems))
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := &StrObjTypeKey{
						Value: elem.Key.Name,
					}
					elems = append(elems, &PropertyElemType{
						Name:     name,
						Value:    t,
						Optional: false,
						Readonly: false, // TODO: when should this be true?
					})
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					t := c.FreshVar()
					name := &StrObjTypeKey{
						Value: elem.Key.Name,
					}
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = Binding{
						Source:  optional.Some[ast.BindingSource](elem.Key),
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
			t = NewObjectType(elems)
		case *ast.ExtractorPat:
			t = optional.Map(
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
		case *ast.IsPat:
			panic("IGNORE: this will be replaced with an Assertion filed on some patterns")
		case *ast.RestPat:
			t = NewRestSpreadType(c.FreshVar())
			errors = []*Error{}
		case *ast.WildcardPat:
			t = c.FreshVar()
			errors = []*Error{}
		}

		t.SetProvenance(&ast.PatProvenance{
			Pat: pat,
		})
		return t, errors
	}

	t, errors := inferPatRec(pattern)
	t.SetProvenance(&ast.PatProvenance{
		Pat: pattern,
	})
	pattern.SetInferredType(t)

	return t, bindings, errors
}

package checker

import (
	"fmt"
	"iter"
	"slices"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/moznion/go-optional"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []*Error) {
	errors := []*Error{}
	ctx = ctx.WithParentScope()

	for _, stmt := range m.Stmts {
		switch stmt := stmt.(type) {
		case *ast.DeclStmt:
			declErrors := c.inferDecl(ctx, stmt.Decl)
			errors = slices.Concat(errors, declErrors)
		case *ast.ExprStmt:
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = slices.Concat(errors, exprErrors)
		case *ast.ReturnStmt:
			panic("TODO: infer return statement")
		}
	}

	return ctx.Scope, errors
}

func (c *Checker) InferModule(ctx Context, m *ast.Module) (map[string]Binding, []*Error) {
	panic("TODO: infer module")
}

func (c *Checker) inferDecl(ctx Context, decl ast.Decl) []*Error {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		// Handle incomplete function declarations
		if decl.Name.Name == "" {
			return []*Error{}
		}
		return c.inferFuncDecl(ctx, decl)
	case *ast.VarDecl:
		return c.inferVarDecl(ctx, decl)
	case *ast.TypeDecl:
		return c.inferTypeDecl(ctx, decl)
	default:
		return []*Error{{message: "Unknown declaration type"}}
	}
}

func (c *Checker) inferVarDecl(ctx Context, decl *ast.VarDecl) []*Error {
	errors := []*Error{}

	patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
	errors = slices.Concat(errors, patErrors)

	if decl.TypeAnn.IsNone() && decl.Init.IsNone() {
		return errors
	}

	// TODO: infer a structural placeholder based on the expression and then
	// unify it with the pattern type.  Then we can pass in map of the new bindings
	// which will be added to a new scope before inferring function expressions
	// in the expressions.

	decl.TypeAnn.IfSome(func(typeAnn ast.TypeAnn) {
		taType, taErrors := c.inferTypeAnn(ctx, typeAnn)
		errors = slices.Concat(errors, taErrors)

		unifyErrors := c.unify(ctx, taType, patType)
		errors = slices.Concat(errors, unifyErrors)

		decl.Init.IfSome(func(init ast.Expr) {
			initType, initErrors := c.inferExpr(ctx, init)
			errors = slices.Concat(errors, initErrors)

			unifyErrors = c.unify(ctx, initType, taType)
			errors = slices.Concat(errors, unifyErrors)
		})
	})

	decl.TypeAnn.IfNone(func() {
		initType, initErrors := c.inferExpr(ctx, decl.Init.Unwrap())
		errors = slices.Concat(errors, initErrors)

		unifyErrors := c.unify(ctx, patType, initType)
		errors = slices.Concat(errors, unifyErrors)
	})

	maps.Copy(ctx.Scope.Values, bindings)
	return errors
}

func (c *Checker) inferFuncDecl(ctx Context, decl *ast.FuncDecl) []*Error {
	errors := []*Error{}

	funcType, paramBindings, sigErrors := c.inferFuncSig(ctx, &decl.FuncSig)
	errors = slices.Concat(errors, sigErrors)

	decl.Body.IfSome(func(body ast.Block) {
		returnType, bodyErrors := c.inferFuncBody(ctx, paramBindings, &body)
		errors = slices.Concat(errors, bodyErrors)
		unifyErrors := c.unify(ctx, funcType.Return, returnType)
		errors = slices.Concat(errors, unifyErrors)
	})

	binding := Binding{
		Source:  optional.Some[ast.BindingSource](decl.Name),
		Type:    funcType,
		Mutable: false,
	}
	ctx.Scope.setValue(decl.Name.Name, binding)
	return errors
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (Type, []*Error) {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := NewNeverType()

		if expr.Op == ast.Assign {
			// TODO: check if expr.Left is a valid lvalue
			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)

			errors := slices.Concat(leftErrors, rightErrors)
			// RHS must be a subtype of LHS because we're assigning RHS to LHS
			unifyErrors := c.unify(ctx, rightType, leftType)
			errors = slices.Concat(errors, unifyErrors)

			return neverType, errors
		}

		opOption := ctx.Scope.getValue(string(expr.Op))
		if opOption.IsNone() {
			return neverType, []*Error{{
				message: "Unknown operator " + string(expr.Op),
			}}
		}
		opType := opOption.Unwrap()

		// TODO: extract this into a unifyCall method
		// TODO: handle function overloading
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

			return fnType.Return, errors
		}

		return neverType, []*Error{{
			message: "Operator " + string(expr.Op) + " is not a function",
		}}
	case *ast.UnaryExpr:
		if expr.Op == ast.UnaryMinus {
			if lit, ok := expr.Arg.(*ast.LiteralExpr); ok {
				if num, ok := lit.Lit.(*ast.NumLit); ok {
					return NewLitType(&NumLit{Value: num.Value * -1}), []*Error{}
				}
			}
		}
		return NewNeverType(), []*Error{{
			message: "TODO: Handle unary operators",
		}}
	case *ast.CallExpr:
		errors := []*Error{}
		calleeType, calleeErrors := c.inferExpr(ctx, expr.Callee)
		errors = slices.Concat(errors, calleeErrors)

		// TODO: handle calleeType being something other than a function, e.g.
		// TypeRef, ObjType with callable signature, etc.
		// TODO: handle generic functions
		// TODO: extract this into a unifyCall method
		if fnType, ok := calleeType.(*FuncType); ok {
			// TODO: handle rest params and spread args
			if len(fnType.Params) != len(expr.Args) {
				return NewNeverType(), []*Error{{
					message: "Invalid number of arguments for function",
				}}
			}

			for i, arg := range expr.Args {
				argType, argErrors := c.inferExpr(ctx, arg)
				errors = slices.Concat(errors, argErrors)

				paramType := fnType.Params[i].Type
				paramErrors := c.unify(ctx, argType, paramType)
				errors = slices.Concat(errors, paramErrors)
			}

			return fnType.Return, errors
		} else {
			return NewNeverType(), []*Error{{
				message: "Callee is not a function",
			}}
		}
	case *ast.MemberExpr:
		t, objErrors := c.inferExpr(ctx, expr.Object)
		objType, expandErrors := expandObjectType(ctx, Prune(t))
		errors := slices.Concat(objErrors, expandErrors)

		var propType Type = NewNeverType()
		for _, elem := range objType.Elems {
			switch elem := elem.(type) {
			case *PropertyElemType:
				if elem.Name == NewStrKey(expr.Prop.Name) {
					propType = elem.Value
				}
			case *MethodElemType:
				if elem.Name == NewStrKey(expr.Prop.Name) {
					propType = elem.Fn
				}
			case *GetterElemType:
				if elem.Name == NewStrKey(expr.Prop.Name) {
					propType = elem.Fn.Return
				}
			case *SetterElemType:
				if elem.Name == NewStrKey(expr.Prop.Name) {
					propType = elem.Fn.Params[0].Type
				}
			default:
				panic(fmt.Sprintf("Unknown object type element: %#v", elem))
			}
		}

		return propType, errors
	case *ast.IndexExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		indexType, indexErrors := c.inferExpr(ctx, expr.Index)

		errors := slices.Concat(objErrors, indexErrors)

		objType = Prune(objType)
		indexType = Prune(indexType)

		switch objType := objType.(type) {
		case *TypeRefType:
			if objType.Name == "Array" {
				unifyErrors := c.unify(ctx, indexType, NewNumType())
				errors = slices.Concat(errors, unifyErrors)
				return objType.TypeArgs[0], errors
			} else {
				errors = append(errors, &Error{
					message: fmt.Sprintf("Expected Array but got %s", objType),
				})
				return NewNeverType(), errors
			}
		case *TupleType:
			if indexLit, ok := indexType.(*LitType); ok {
				if indexType, ok := indexLit.Lit.(*NumLit); ok {
					index := int(indexType.Value)
					if index < len(objType.Elems) {
						return objType.Elems[index], errors
					} else {
						errors = append(errors, &Error{
							message: fmt.Sprintf("Index %d out of bounds for tuple of length %d", index, len(objType.Elems)),
						})
						return NewNeverType(), errors
					}
				} else {
					errors = append(errors, &Error{
						message: fmt.Sprintf("Expected index to be a number but got %s", indexType),
					})
					return NewNeverType(), errors
				}
			} else {
				errors = append(errors, &Error{
					message: fmt.Sprintf("Expected index to be a number but got %s", indexType),
				})
				return NewNeverType(), errors
			}
		case *ObjectType:
			if indexLit, ok := indexType.(*LitType); ok {
				if indexType, ok := indexLit.Lit.(*StrLit); ok {
					for _, elem := range objType.Elems {
						switch elem := elem.(type) {
						case *PropertyElemType:
							if elem.Name == NewStrKey(indexType.Value) {
								return elem.Value, errors
							}
						default:
							panic(fmt.Sprintf("Unknown object type element: %#v", elem))
						}
					}

					errors = append(errors, &Error{
						message: fmt.Sprintf("No property with key %s", indexType),
					})
					return NewNeverType(), errors
				}
			}
			errors = append(errors, &Error{
				message: fmt.Sprintf("%s is not a valid object key", indexType),
			})
			return NewNeverType(), errors
		default:
			panic(fmt.Sprintf("Unknown object type: %#v", objType))
		}
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
		t, errors := c.inferLit(expr.Lit)
		expr.SetInferredType(t)
		return t, errors
	case *ast.TupleExpr:
		types := make([]Type, len(expr.Elems))
		errors := []*Error{}
		for i, elem := range expr.Elems {
			elemType, elemErrors := c.inferExpr(ctx, elem)
			types[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		tupleType := NewTupleType(types...)
		expr.SetInferredType(tupleType)
		return tupleType, errors
	case *ast.ObjectExpr:
		elems := make([]ObjTypeElem, len(expr.Elems))
		errors := []*Error{}
		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				elem.Value.IfSome(func(value ast.Expr) {
					t, elemErrors := c.inferExpr(ctx, value)
					errors = slices.Concat(errors, elemErrors)
					elems[i] = NewPropertyElemType(astKeyToTypeKey(elem.Name), t)
				})
			default:
				panic(fmt.Sprintf("TODO: handle object expression element: %#v", elem))
			}
		}

		objType := NewObjectType(elems)
		expr.SetInferredType(objType)

		return objType, errors
	case *ast.FuncExpr:
		funcType, bindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig)
		returnType, bodyErrors := c.inferFuncBody(ctx, bindings, &expr.Body)
		unifyErrors := c.unify(ctx, funcType.Return, returnType)
		expr.SetInferredType(funcType)
		return funcType, slices.Concat(sigErrors, bodyErrors, unifyErrors)
	case *ast.IfElseExpr:
		return c.inferIfElse(ctx, expr)
	default:
		return nil, []*Error{{message: "Unknown expression type"}}
	}
}

func expandObjectType(ctx Context, objType Type) (*ObjectType, []*Error) {
	switch t := objType.(type) {
	case *ObjectType:
		return t, nil
	case *TypeRefType:
		typeAlias := ctx.Scope.getTypeAlias(t.Name)
		if typeAlias.IsNone() {
			errors := []*Error{{
				message: fmt.Sprintf("Unknown type %s", t.Name),
			}}
			return nil, errors
		}
		ta := typeAlias.Unwrap()
		// TODO: replace type params with type args
		return expandObjectType(ctx, ta.Type)
	default:
		panic(fmt.Sprintf("Unknown object type: %#v", t))
	}
}

func astKeyToTypeKey(key ast.ObjKey) ObjTypeKey {
	switch key := key.(type) {
	case *ast.IdentExpr:
		return NewStrKey(key.Name)
	case *ast.StrLit:
		return NewStrKey(key.Value)
	case *ast.NumLit:
		return NewNumKey(key.Value)
	case *ast.ComputedKey:
		panic("TODO: handle computed key")
	default:
		panic(fmt.Sprintf("Unknown object key type: %T", key))
	}
}

func (c *Checker) inferIfElse(ctx Context, expr *ast.IfElseExpr) (Type, []*Error) {
	condType, condErrors := c.inferExpr(ctx, expr.Cond)
	unifyErrors := c.unify(ctx, condType, NewBoolType())
	errors := slices.Concat(condErrors, unifyErrors)

	var consType Type = NewNeverType()
	for _, stmt := range expr.Cons.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}
	if len(expr.Cons.Stmts) > 0 {
		lastStmt := expr.Cons.Stmts[len(expr.Cons.Stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			consType = exprStmt.Expr.InferredType()
		}
	}

	var altType Type = NewNeverType()
	expr.Alt.IfSome(func(alt ast.BlockOrExpr) {
		if alt.Block != nil {
			for _, stmt := range alt.Block.Stmts {
				stmtErrors := c.inferStmt(ctx, stmt)
				errors = slices.Concat(errors, stmtErrors)
			}
			if len(alt.Block.Stmts) > 0 {
				lastStmt := alt.Block.Stmts[len(alt.Block.Stmts)-1]
				if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
					altType = exprStmt.Expr.InferredType()
				}
			}
		} else if alt.Expr != nil {
			t, altErrors := c.inferExpr(ctx, alt.Expr)
			errors = slices.Concat(errors, altErrors)
			altType = t
		} else {
			panic("alt must be a block or expression")
		}
	})

	t := NewUnionType(consType, altType)
	expr.SetInferredType(t)

	return t, errors
}

func (c *Checker) inferStmt(ctx Context, stmt ast.Stmt) []*Error {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		_, errors := c.inferExpr(ctx, stmt.Expr)
		return errors
	case *ast.DeclStmt:
		return c.inferDecl(ctx, stmt.Decl)
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

func (c *Checker) inferFuncSig(
	ctx Context,
	sig *ast.FuncSig, // TODO: make FuncSig an interface
) (*FuncType, map[string]Binding, []*Error) {
	// TODO: handle generic functions
	// typeParams := c.inferTypeParams(ctx, sig.TypeParams)
	errors := []*Error{}
	bindings := map[string]Binding{}
	params := make([]*FuncParam, len(sig.Params))

	for i, param := range sig.Params {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)

		errors = slices.Concat(errors, patErrors)

		typeAnn := optional.Map(param.TypeAnn, func(typeAnn ast.TypeAnn) Type {
			typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, typeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
			return typeAnnType
		}).TakeOrElse(func() Type {
			return c.FreshVar()
		})

		// TODO: handle type annotations on parameters
		c.unify(ctx, patType, typeAnn)

		maps.Copy(bindings, patBindings)

		fmt.Printf("typeAnn: %s\n", typeAnn)
		for name, binding := range patBindings {
			fmt.Printf("%s: %s\n", name, binding.Type)
		}

		params[i] = &FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: false, // TODO
		}
	}

	t := &FuncType{
		Params:     params,
		Return:     c.FreshVar(),
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
		Self:       optional.None[Type](),
	}

	return t, bindings, errors
}

type ReturnVisitor struct {
	Returns []*ast.ReturnStmt
}

func (v *ReturnVisitor) VisitStmt(stmt ast.Stmt) bool {
	if returnStmt, ok := stmt.(*ast.ReturnStmt); ok {
		v.Returns = append(v.Returns, returnStmt)
	}

	return true
}
func (v *ReturnVisitor) VisitExpr(expr ast.Expr) bool {
	// Don't visit function expressions since we don't want to include any
	// return statements inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) VisitDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// return statements inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) VisitObjExprElem(elem ast.ObjExprElem) bool {
	// An expression like if/else could have a return statement inside one of
	// its branches.
	return true
}
func (v *ReturnVisitor) VisitPat(pat ast.Pat) bool       { return true }
func (v *ReturnVisitor) VisitTypeAnn(t ast.TypeAnn) bool { return true }
func (v *ReturnVisitor) VisitLit(lit ast.Lit) bool       { return true }

func (c *Checker) inferFuncBody(
	ctx Context,
	bindings map[string]Binding,
	body *ast.Block,
) (Type, []*Error) {

	ctx = ctx.WithParentScope()
	maps.Copy(ctx.Scope.Values, bindings)

	errors := []*Error{}
	for _, stmt := range body.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	visitor := &ReturnVisitor{
		Returns: []*ast.ReturnStmt{},
	}

	for _, stmt := range body.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(visitor)
	}

	returnTypes := []Type{}
	for _, returnStmt := range visitor.Returns {
		returnStmt.Expr.IfSome(func(expr ast.Expr) {
			returnType, returnErrors := c.inferExpr(ctx, expr)
			returnTypes = append(returnTypes, returnType)
			errors = slices.Concat(errors, returnErrors)
		})
	}

	// TODO: We also need to do dead code analysis to account for unreachable
	// code.

	if len(returnTypes) == 1 {
		return returnTypes[0], errors
	}

	if len(returnTypes) > 1 {
		return NewUnionType(returnTypes...), errors
	}

	return NewLitType(&UndefinedLit{}), errors
}

func patToPat(p ast.Pat) Pat {
	switch p := p.(type) {
	case *ast.IdentPat:
		return &IdentPat{Name: p.Name}
	case *ast.LitPat:
		panic("TODO: handle literal pattern")
		// return &LitPat{Lit: p.Lit}
	case *ast.TuplePat:
		elems := make([]Pat, len(p.Elems))
		for i, elem := range p.Elems {
			elems[i] = patToPat(elem)
		}
		return &TuplePat{Elems: elems}
	case *ast.ObjectPat:
		elems := make([]ObjPatElem, len(p.Elems))
		for i, elem := range p.Elems {
			switch elem := elem.(type) {
			case *ast.ObjKeyValuePat:
				elems[i] = &ObjKeyValuePat{
					Key:   elem.Key.Name,
					Value: patToPat(elem.Value),
				}
			case *ast.ObjShorthandPat:
				elems[i] = &ObjShorthandPat{
					Key: elem.Key.Name,
				}
			case *ast.ObjRestPat:
				elems[i] = &ObjRestPat{
					Pattern: patToPat(elem.Pattern),
				}
			default:
				panic("unknown object pattern element type")
			}
		}
		return &ObjectPat{Elems: elems}
	case *ast.ExtractorPat:
		args := make([]Pat, len(p.Args))
		for i, arg := range p.Args {
			args[i] = patToPat(arg)
		}
		return &ExtractorPat{Name: p.Name, Args: args}
	case *ast.RestPat:
		return &RestPat{Pattern: patToPat(p.Pattern)}
	default:
		panic("unknown pattern type: " + fmt.Sprintf("%T", p))
	}
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
			elems := []ObjTypeElem{}
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := NewStrKey(elem.Key.Name)
					elems = append(elems, NewPropertyElemType(name, t))
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					t := c.FreshVar()
					name := NewStrKey(elem.Key.Name)
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = Binding{
						Source:  optional.Some[ast.BindingSource](elem.Key),
						Type:    t,
						Mutable: false, // TODO
					}
					elems = append(elems, NewPropertyElemType(name, t))
				case *ast.ObjRestPat:
					t, restErrors := inferPatRec(elem.Pattern)
					errors = slices.Concat(errors, restErrors)
					elems = append(elems, NewRestSpreadElemType(t))
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
		case *ast.RestPat:
			argType, argErrors := inferPatRec(p.Pattern)
			errors = append(errors, argErrors...)
			t = NewRestSpreadType(argType)
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

func (c *Checker) inferTypeDecl(
	ctx Context,
	decl *ast.TypeDecl,
) []*Error {
	errors := []*Error{}

	typeParams := make([]*TypeParam, len(decl.TypeParams))
	for i, typeParam := range decl.TypeParams {
		constraint := optional.Map(typeParam.Constraint, func(constraint ast.TypeAnn) Type {
			constraintType, constraintErrors := c.inferTypeAnn(ctx, constraint)
			errors = slices.Concat(errors, constraintErrors)
			return constraintType
		})
		default_ := optional.Map(typeParam.Default, func(default_ ast.TypeAnn) Type {
			defaultType, defaultErrors := c.inferTypeAnn(ctx, default_)
			errors = slices.Concat(errors, defaultErrors)
			return defaultType
		})
		typeParams[i] = &TypeParam{
			Name:       typeParam.Name,
			Constraint: constraint,
			Default:    default_,
		}
	}

	t, typeErrors := c.inferTypeAnn(ctx, decl.TypeAnn)
	errors = slices.Concat(errors, typeErrors)

	typeAlias := TypeAlias{
		Type:       t,
		TypeParams: typeParams,
	}

	typeAlias.Type = t
	ctx.Scope.setTypeAlias(decl.Name.Name, typeAlias)

	return errors
}

func (c *Checker) inferFuncTypeAnn(
	ctx Context,
	funcTypeAnn *ast.FuncTypeAnn,
) (*FuncType, []*Error) {
	errors := []*Error{}
	params := make([]*FuncParam, len(funcTypeAnn.Params))
	for i, param := range funcTypeAnn.Params {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)
		errors = slices.Concat(errors, patErrors)

		// TODO: make type annoations required on parameters in function type
		// annotations
		typeAnn := optional.Map(param.TypeAnn, func(typeAnn ast.TypeAnn) Type {
			typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, typeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
			return typeAnnType
		}).TakeOrElse(func() Type {
			return c.FreshVar()
		})

		c.unify(ctx, patType, typeAnn)

		maps.Copy(ctx.Scope.Values, patBindings)

		params[i] = &FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: false,
		}
	}
	returnType, returnErrors := c.inferTypeAnn(ctx, funcTypeAnn.Return)
	errors = slices.Concat(errors, returnErrors)

	funcType := FuncType{
		Params:     params,
		Return:     returnType,
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
		Self:       optional.None[Type](),
	}

	return &funcType, errors
}

func (c *Checker) inferTypeAnn(
	ctx Context,
	typeAnn ast.TypeAnn,
) (Type, []*Error) {
	errors := []*Error{}
	var t Type = NewNeverType()

	switch typeAnn := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		t = optional.Map(ctx.Scope.getTypeAlias(typeAnn.Name), func(typeAlias TypeAlias) Type {
			typeArgs := make([]Type, len(typeAnn.TypeArgs))
			for i, typeArg := range typeAnn.TypeArgs {
				typeArgType, typeArgErrors := c.inferTypeAnn(ctx, typeArg)
				typeArgs[i] = typeArgType
				errors = slices.Concat(errors, typeArgErrors)
			}

			t := NewTypeRefType(typeAnn.Name, optional.Some(typeAlias), typeArgs...)
			return t
		}).TakeOrElse(func() Type {
			errors = append(errors, &Error{
				message: fmt.Sprintf("Unknown type %s", typeAnn.Name),
			})
			t := NewNeverType()
			return t
		})
	case *ast.NumberTypeAnn:
		t = NewNumType()
	case *ast.StringTypeAnn:
		t = NewStrType()
	case *ast.BooleanTypeAnn:
		t = NewBoolType()
	case *ast.LitTypeAnn:
		switch lit := typeAnn.Lit.(type) {
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
			errors = append(errors, &Error{
				message: fmt.Sprintf("Unknown literal type %T", lit),
			})
		}
	case *ast.TupleTypeAnn:
		elems := make([]Type, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			elemType, elemErrors := c.inferTypeAnn(ctx, elem)
			elems[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		t = NewTupleType(elems...)
	case *ast.ObjectTypeAnn:
		elems := make([]ObjTypeElem, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			switch elem := elem.(type) {
			case *ast.CallableTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &CallableElemType{Fn: fn}
			case *ast.ConstructorTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &ConstructorElemType{Fn: fn}
			case *ast.MethodTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &MethodElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.GetterTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &GetterElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.SetterTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &SetterElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.PropertyTypeAnn:
				typeAnn, typeAnnErrors := c.inferTypeAnn(ctx, elem.Value)
				errors = slices.Concat(errors, typeAnnErrors)
				elems[i] = &PropertyElemType{
					Name:     astKeyToTypeKey(elem.Name),
					Optional: elem.Optional,
					Readonly: elem.Readonly,
					Value:    typeAnn,
				}
			case *ast.MappedTypeAnn:
				panic("TODO: handle MappedTypeAnn")
			case *ast.RestSpreadTypeAnn:
				panic("TODO: handle RestSpreadTypeAnn")
			}
		}

		t = NewObjectType(elems)
	default:
		return nil, []*Error{{message: "Unknown type annotation"}}
	}

	t.SetProvenance(ast.NewTypeAnnProvenance(typeAnn))
	typeAnn.SetInferredType(t)

	return t, errors
}

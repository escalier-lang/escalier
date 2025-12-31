package checker

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/graphql"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (type_system.Type, []Error) {
	var resultType type_system.Type
	var errors []Error

	provenance := &ast.NodeProvenance{Node: expr}

	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := type_system.NewNeverType(nil)

		if expr.Op == ast.Assign {
			// TODO: check if expr.Left is a valid lvalue
			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)

			errors = slices.Concat(leftErrors, rightErrors)

			// Check if we're trying to mutate an immutable object
			if memberExpr, ok := expr.Left.(*ast.MemberExpr); ok {
				objType, objErrors := c.inferExpr(ctx, memberExpr.Object)
				errors = slices.Concat(errors, objErrors)

				// Check if the property is readonly (this check takes precedence)
				if c.isPropertyReadonly(ctx, objType, memberExpr.Prop.Name) {
					// Even if the type is mutable, readonly properties cannot be mutated
					errors = append(errors, &CannotMutateReadonlyPropertyError{
						Type:     objType,
						Property: memberExpr.Prop.Name,
						span:     expr.Left.Span(),
					})
				} else {
					// Check if the object type allows mutation
					if _, ok := type_system.Prune(objType).(*type_system.MutabilityType); !ok {
						errors = append(errors, &CannotMutateImmutableError{
							Type: objType,
							span: expr.Left.Span(),
						})
					}
				}
			} else if indexExpr, ok := expr.Left.(*ast.IndexExpr); ok {
				objType, objErrors := c.inferExpr(ctx, indexExpr.Object)
				errors = slices.Concat(errors, objErrors)

				// Check if the property is readonly when using string literal index
				indexType, indexErrors := c.inferExpr(ctx, indexExpr.Index)
				errors = slices.Concat(errors, indexErrors)

				// Unwrap MutabilityType if present
				if mutType, ok := indexType.(*type_system.MutabilityType); ok {
					indexType = mutType.Type
				}

				isReadonly := false
				if litType, ok := indexType.(*type_system.LitType); ok {
					if strLit, ok := litType.Lit.(*type_system.StrLit); ok {
						if c.isPropertyReadonly(ctx, objType, strLit.Value) {
							isReadonly = true
						}
					}
				}

				// Check if property is readonly (this check takes precedence)
				if isReadonly {
					// Even if the type is mutable, readonly properties cannot be mutated
					errors = append(errors, &CannotMutateReadonlyPropertyError{
						Type:     objType,
						Property: indexType.String(),
						span:     expr.Left.Span(),
					})
				} else {
					// Check if the object type allows mutation
					if _, ok := type_system.Prune(objType).(*type_system.MutabilityType); !ok {
						errors = append(errors, &CannotMutateImmutableError{
							Type: objType,
							span: expr.Left.Span(),
						})
					}
				}
			}

			// RHS must be a subtype of LHS because we're assigning RHS to LHS
			unifyErrors := c.Unify(ctx, rightType, leftType)
			errors = slices.Concat(errors, unifyErrors)

			resultType = neverType
		} else {
			opBinding := ctx.Scope.GetValue(string(expr.Op))
			if opBinding == nil {
				resultType = neverType
				errors = []Error{&UnknownOperatorError{
					Operator: string(expr.Op),
				}}
			} else {
				// TODO: extract this into a unifyCall method
				// TODO: handle function overloading
				if fnType, ok := opBinding.Type.(*type_system.FuncType); ok {
					if len(fnType.Params) != 2 {
						resultType = neverType
						errors = []Error{&InvalidNumberOfArgumentsError{
							CallExpr: expr,
							Callee:   fnType,
							Args:     []ast.Expr{expr.Left, expr.Right},
						}}
					} else {
						errors = []Error{}

						leftType, leftErrors := c.inferExpr(ctx, expr.Left)
						rightType, rightErrors := c.inferExpr(ctx, expr.Right)
						errors = slices.Concat(errors, leftErrors, rightErrors)

						leftErrors = c.Unify(ctx, leftType, fnType.Params[0].Type)
						rightErrors = c.Unify(ctx, rightType, fnType.Params[1].Type)
						errors = slices.Concat(errors, leftErrors, rightErrors)

						resultType = fnType.Return
					}
				} else {
					resultType = neverType
					errors = []Error{&UnknownOperatorError{Operator: string(expr.Op)}}
				}
			}
		}
	case *ast.UnaryExpr:
		if expr.Op == ast.UnaryMinus {
			if lit, ok := expr.Arg.(*ast.LiteralExpr); ok {
				if num, ok := lit.Lit.(*ast.NumLit); ok {
					resultType = type_system.NewNumLitType(provenance, -num.Value)
					errors = []Error{}
				} else {
					resultType = type_system.NewNeverType(nil)
					errors = []Error{&UnimplementedError{
						message: "Handle unary operators",
						span:    expr.Span(),
					}}
				}
			} else {
				resultType = type_system.NewNeverType(nil)
				errors = []Error{&UnimplementedError{
					message: "Handle unary operators",
					span:    expr.Span(),
				}}
			}
		} else {
			resultType = type_system.NewNeverType(nil)
			errors = []Error{&UnimplementedError{
				message: "Handle unary operators",
				span:    expr.Span(),
			}}
		}
	case *ast.CallExpr:
		resultType, errors = c.inferCallExpr(ctx, expr)
	case *ast.MemberExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, span: expr.Prop.Span()}
		propType, propErrors := c.getMemberType(ctx, objType, key)

		resultType = propType

		if methodType, ok := propType.(*type_system.FuncType); ok {
			if retType, ok := methodType.Return.(*type_system.TypeRefType); ok && type_system.QualIdentToString(retType.Name) == "Self" {
				t := *methodType   // Create a copy of the struct
				t.Return = objType // Replace `Self` with the object type
				resultType = &t
			}
		}

		errors = slices.Concat(objErrors, propErrors)
	case *ast.IndexExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		indexType, indexErrors := c.inferExpr(ctx, expr.Index)

		errors = slices.Concat(objErrors, indexErrors)

		key := IndexKey{Type: indexType, span: expr.Index.Span()}
		accessType, accessErrors := c.getMemberType(ctx, objType, key)
		resultType = accessType
		errors = slices.Concat(errors, accessErrors)
	case *ast.IdentExpr:
		if binding := ctx.Scope.GetValue(expr.Name); binding != nil {
			// We create a new type and set its provenance to be the identifier
			// instead of the binding source.  This ensures that errors are reported
			// on the identifier itself instead of the binding source.
			t := type_system.Prune(binding.Type)
			resultType = t.Copy()
			resultType.SetProvenance(&ast.NodeProvenance{Node: expr})
			expr.Source = binding.Source
			errors = nil
		} else if namespace := ctx.Scope.getNamespace(expr.Name); namespace != nil {
			t := type_system.NewNamespaceType(provenance, namespace)
			resultType = t
			errors = nil
		} else {
			resultType = type_system.NewNeverType(nil)
			errors = []Error{&UnknownIdentifierError{Ident: expr, span: expr.Span()}}
		}
	case *ast.LiteralExpr:
		resultType, errors = c.inferLit(expr.Lit)
		resultType = &type_system.MutabilityType{
			Type:       resultType,
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.TupleExpr:
		types := make([]type_system.Type, len(expr.Elems))
		errors = []Error{}
		for i, elem := range expr.Elems {
			elemType, elemErrors := c.inferExpr(ctx, elem)
			types[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}

		resultType = &type_system.MutabilityType{
			Type:       type_system.NewTupleType(provenance, types...),
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.ObjectExpr:
		// Create a context for the object so that we can add a `Self` type to it
		objCtx := ctx.WithNewScope()

		typeElems := make([]type_system.ObjTypeElem, len(expr.Elems))
		types := make([]type_system.Type, len(expr.Elems))
		paramBindingsSlice := make([]map[string]*type_system.Binding, len(expr.Elems))

		selfType := c.FreshVar(nil)
		selfTypeAlias := type_system.TypeAlias{Type: selfType, TypeParams: []*type_system.TypeParam{}}
		objCtx.Scope.SetTypeAlias("Self", &selfTypeAlias)

		methodCtxs := make([]Context, len(expr.Elems))

		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					t := c.FreshVar(&ast.NodeProvenance{Node: elem})
					types[i] = t
					typeElems[i] = type_system.NewPropertyElem(*key, t)
				}
			case *ast.MethodExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					methodType, methodCtx, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					methodCtxs[i] = methodCtx
					paramBindingsSlice[i] = paramBindings
					types[i] = methodType
					typeElems[i] = type_system.NewMethodElem(*key, methodType, elem.MutSelf)
				}
			case *ast.GetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, _, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &type_system.GetterElem{Fn: funcType, Name: *key}
				}
			case *ast.SetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, _, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &type_system.SetterElem{Fn: funcType, Name: *key}
				}
			}
		}

		objType := type_system.NewObjectType(provenance, typeElems)
		bindErrors := c.bind(objCtx, selfType, objType)
		errors = slices.Concat(errors, bindErrors)

		i := 0 // indexes into paramBindingsSlice
		for t, exprElem := range Zip(types, expr.Elems) {
			switch elem := exprElem.(type) {
			case *ast.PropertyExpr:
				if elem.Value != nil {
					valueType, valueErrors := c.inferExpr(objCtx, elem.Value)
					unifyErrors := c.Unify(objCtx, valueType, t)

					errors = slices.Concat(errors, valueErrors, unifyErrors)
				} else {
					switch key := elem.Name.(type) {
					case *ast.IdentExpr:
						// TODO: dedupe with *ast.IdentExpr case
						if binding := objCtx.Scope.GetValue(key.Name); binding != nil {
							unifyErrors := c.Unify(objCtx, binding.Type, t)
							errors = slices.Concat(errors, unifyErrors)
						} else {
							unifyErrors := c.Unify(objCtx, type_system.NewNeverType(nil), t)
							errors = slices.Concat(errors, unifyErrors)

							errors = append(
								errors,
								&UnknownIdentifierError{Ident: key, span: key.Span()},
							)
						}
					}
				}
			case *ast.MethodExpr:
				methodType := t.(*type_system.FuncType)
				methodCtx := methodCtxs[i]
				methodExpr := elem
				paramBindings := paramBindingsSlice[i]

				if methodExpr.MutSelf != nil {
					var selfType type_system.Type = type_system.NewTypeRefType(nil, "Self", &selfTypeAlias)
					if *methodExpr.MutSelf {
						selfType = type_system.NewMutableType(nil, selfType)
					}
					paramBindings["self"] = &type_system.Binding{
						Source:  &ast.NodeProvenance{Node: expr},
						Type:    selfType,
						Mutable: false, // `self` cannot be reassigned
					}
				}

				inferErrors := c.inferFuncBodyWithFuncSigType(
					methodCtx, methodType, paramBindings, methodExpr.Fn.Body, methodExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)

			case *ast.GetterExpr:
				funcType := t.(*type_system.FuncType)
				paramBindings := paramBindingsSlice[i]
				paramBindings["self"] = &type_system.Binding{
					Source:  &ast.NodeProvenance{Node: expr},
					Type:    type_system.NewTypeRefType(nil, "Self", &selfTypeAlias),
					Mutable: false, // `self` cannot be reassigned
				}

				getterExpr := elem
				inferErrors := c.inferFuncBodyWithFuncSigType(
					objCtx, funcType, paramBindings, getterExpr.Fn.Body, getterExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)

			case *ast.SetterExpr:
				funcType := t.(*type_system.FuncType)
				paramBindings := paramBindingsSlice[i]
				paramBindings["self"] = &type_system.Binding{
					Source:  &ast.NodeProvenance{Node: expr},
					Type:    type_system.NewMutableType(nil, type_system.NewTypeRefType(nil, "Self", &selfTypeAlias)),
					Mutable: false, // `self` cannot be reassigned
				}

				setterExpr := elem
				inferErrors := c.inferFuncBodyWithFuncSigType(
					objCtx, funcType, paramBindings, setterExpr.Fn.Body, setterExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)
			}

			i++
		}

		resultType = &type_system.MutabilityType{
			Type:       selfType,
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.FuncExpr:
		funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig, expr)
		errors = slices.Concat(errors, sigErrors)

		inferErrors := c.inferFuncBodyWithFuncSigType(funcCtx, funcType, paramBindings, expr.Body, expr.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)

		resultType = funcType
	case *ast.IfElseExpr:
		resultType, errors = c.inferIfElse(ctx, expr)
	case *ast.DoExpr:
		resultType, errors = c.inferDoExpr(ctx, expr)
	case *ast.MatchExpr:
		resultType, errors = c.inferMatchExpr(ctx, expr)
	case *ast.ThrowExpr:
		// Infer the type of the argument being thrown
		_, argErrors := c.inferExpr(ctx, expr.Arg)
		errors = argErrors
		// Throw expressions have type never since they don't return a value
		resultType = type_system.NewNeverType(nil)
	case *ast.AwaitExpr:
		// Await can only be used inside async functions
		if !ctx.IsAsync {
			errors = []Error{
				&UnimplementedError{
					message: "await can only be used inside async functions",
					span:    expr.Span(),
				},
			}
			resultType = type_system.NewNeverType(nil)
		} else {
			// Infer the type of the expression being awaited
			argType, argErrors := c.inferExpr(ctx, expr.Arg)
			errors = argErrors

			// If the argument is a Promise<T, E>, the result type is T
			// and the throws type should be E (stored in expr.Throws for later use)
			if promiseType, ok := argType.(*type_system.TypeRefType); ok && type_system.QualIdentToString(promiseType.Name) == "Promise" {
				if len(promiseType.TypeArgs) >= 2 {
					resultType = promiseType.TypeArgs[0]  // T
					expr.Throws = promiseType.TypeArgs[1] // E (store for throw inference)
				} else {
					resultType = type_system.NewNeverType(nil)
				}
			} else {
				// If not a Promise type, this is an error
				errors = append(errors, &UnimplementedError{
					message: "await expression expects a Promise type",
					span:    expr.Span(),
				})
				resultType = type_system.NewNeverType(nil)
			}
		}
	case *ast.TaggedTemplateLitExpr:
		// Create string literals from the quasis (static parts)
		stringElems := make([]ast.Expr, len(expr.Quasis))
		for i, quasi := range expr.Quasis {
			strLit := ast.NewString(quasi.Value, quasi.Span)
			stringElems[i] = ast.NewLitExpr(strLit)
		}

		// Create array of string literals as first argument
		stringsArray := ast.NewArray(stringElems, expr.Span())

		// Combine the strings array with the interpolated expressions
		args := make([]ast.Expr, 1+len(expr.Exprs))
		args[0] = stringsArray
		copy(args[1:], expr.Exprs)

		// If expr.Tag is the identifier `gql` then do some custom handling
		if tag, ok := expr.Tag.(*ast.IdentExpr); ok && tag.Name == "gql" {
			// TODO: Interpolate Exprs
			str := ""
			for i, quasi := range expr.Quasis {
				str += quasi.Value
				if i < len(expr.Exprs) {
					expr := expr.Exprs[i]
					t, _ := c.inferExpr(ctx, expr)

					switch t := type_system.Prune(t).(type) {
					case *type_system.LitType:
						str += t.Lit.String()
					default:
						// TODO: handle interpolating DocumentNode fragments
						panic("Can only interpolate literal types in gql tagged templates")
					}
				}
			}

			queryDoc := gqlparser.MustLoadQueryWithRules(c.Schema, str, rules.NewDefaultRules())
			result := graphql.InferGraphQLQuery(c.Schema, queryDoc)

			// `TypedDocumentNode<ResultType, VariablesType>`
			// TODO: Look up `TypedDocumentNode` from `@graphql-typed-document-node/core`
			t := type_system.NewTypeRefType(provenance, "TypedDocumentNode", nil, result.ResultType, result.VariablesType)
			return t, nil
		}

		// Create a call expression
		callExpr := ast.NewCall(expr.Tag, args, false, expr.Span())

		// Infer the call expression
		resultType, errors = c.inferCallExpr(ctx, callExpr)
	case *ast.TypeCastExpr:
		// Infer the type of the expression being cast
		exprType, exprErrors := c.inferExpr(ctx, expr.Expr)
		errors = slices.Concat(errors, exprErrors)

		// Infer the type annotation to get the target type
		targetType, typeAnnErrors := c.inferTypeAnn(ctx, expr.TypeAnn)
		errors = slices.Concat(errors, typeAnnErrors)

		// Check that the expression type is a subtype of the target type
		// For type casting, we require that exprType can be unified with targetType
		unifyErrors := c.Unify(ctx, exprType, targetType)
		errors = slices.Concat(errors, unifyErrors)

		// The result type is the target type
		resultType = targetType
	case *ast.TemplateLitExpr:
		// Template literals always produce strings
		// We need to infer all the interpolated expressions for type checking
		errors = []Error{}
		for _, expr := range expr.Exprs {
			_, exprErrors := c.inferExpr(ctx, expr)
			errors = slices.Concat(errors, exprErrors)
		}
		// Template literals always result in a string type
		resultType = type_system.NewStrPrimType(provenance)
	default:
		resultType = type_system.NewNeverType(nil)
		errors = []Error{
			&UnimplementedError{
				message: "Infer expression type: " + fmt.Sprintf("%T", expr),
				span:    expr.Span(),
			},
		}
	}

	// Always set the inferred type on the expression before returning
	expr.SetInferredType(resultType)
	resultType.SetProvenance(provenance)
	return resultType, errors
}

// isPropertyReadonly checks if a specific property in an object type is readonly
func (c *Checker) isPropertyReadonly(ctx Context, objType type_system.Type, propertyName string) bool {
	objType = type_system.Prune(objType)

	// Repeatedly expand objType until it's either an ObjectType or can't be expanded further
	for {
		expandedType, _ := c.ExpandType(ctx, objType, 1)

		// If expansion didn't change the type, we're done expanding
		if expandedType == objType {
			break
		}

		objType = expandedType

		// If we've reached an ObjectType, we can stop expanding
		if _, ok := objType.(*type_system.ObjectType); ok {
			break
		}
	}

	switch t := objType.(type) {
	case *type_system.MutabilityType:
		// For mutable types, check the inner type
		return c.isPropertyReadonly(ctx, t.Type, propertyName)
	case *type_system.TypeRefType:
		// For TypeRefTypes, try to expand the type alias and check recursively
		expandType, _ := c.expandTypeRef(ctx, t)
		return c.isPropertyReadonly(ctx, expandType, propertyName)
	case *type_system.ObjectType:
		// Check if the property exists and is readonly
		for _, elem := range t.Elems {
			if propElem, ok := elem.(*type_system.PropertyElem); ok {
				if propElem.Name == type_system.NewStrKey(propertyName) {
					return propElem.Readonly
				}
			}
		}
	}

	return false
}

func (c *Checker) inferCallExpr(
	ctx Context,
	expr *ast.CallExpr,
) (resultType type_system.Type, errors []Error) {
	errors = []Error{}
	calleeType, calleeErrors := c.inferExpr(ctx, expr.Callee)
	errors = slices.Concat(errors, calleeErrors)
	provneance := &ast.NodeProvenance{Node: expr}

	argTypes := make([]type_system.Type, len(expr.Args))
	for i, arg := range expr.Args {
		argType, argErrors := c.inferExpr(ctx, arg)
		errors = slices.Concat(errors, argErrors)
		argTypes[i] = argType
	}

	// Check if calleeType is a FuncType
	if fnType, ok := calleeType.(*type_system.FuncType); ok {
		return c.handleFuncCall(ctx, fnType, expr, argTypes, provneance, errors)
	} else if typeRefType, ok := calleeType.(*type_system.TypeRefType); ok {
		name := type_system.QualIdentToString(typeRefType.Name)
		typeAlias := ctx.Scope.getTypeAlias(name)

		if objType, ok := type_system.Prune(typeAlias.Type).(*type_system.ObjectType); ok {
			// Check if ObjectType has a constructor or callable element
			var fnType *type_system.FuncType = nil

			for _, elem := range objType.Elems {
				if constructorElem, ok := elem.(*type_system.ConstructorElem); ok {
					fnType = constructorElem.Fn
					break
				} else if callableElem, ok := elem.(*type_system.CallableElem); ok {
					fnType = callableElem.Fn
					break
				}
			}

			if fnType == nil {
				return type_system.NewNeverType(provneance), []Error{
					&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
			}

			return c.handleFuncCall(ctx, fnType, expr, argTypes, provneance, errors)
		} else {
			panic("TODO: try expanding the type alias using ExpandType")
		}
	} else if objType, ok := calleeType.(*type_system.ObjectType); ok {
		// Check if ObjectType has a constructor or callable element
		var fnType *type_system.FuncType = nil

		for _, elem := range objType.Elems {
			if constructorElem, ok := elem.(*type_system.ConstructorElem); ok {
				fnType = constructorElem.Fn
				break
			} else if callableElem, ok := elem.(*type_system.CallableElem); ok {
				fnType = callableElem.Fn
				break
			}
		}

		if fnType == nil {
			return type_system.NewNeverType(provneance), []Error{
				&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
		}

		return c.handleFuncCall(ctx, fnType, expr, argTypes, provneance, errors)

	} else if intersectionType, ok := calleeType.(*type_system.IntersectionType); ok {
		for _, t := range intersectionType.Types {
			attemptErrors := []Error{}
			// TODO: Extract the body of `inferCallExpr` into a function that we can
			// pass the callee and the args to separately.  We need to be able to
			// expand the callee type if necessary here.  This would allow us to lazily
			// expand the callee type.
			if funcType, ok := t.(*type_system.FuncType); ok {
				retType, callErrors := c.handleFuncCall(ctx, funcType, expr, argTypes, provneance, attemptErrors)
				if len(callErrors) == 0 {
					return retType, errors
				}
			}
		}

		return type_system.NewNeverType(provneance), []Error{
			&UnimplementedError{message: "TODO: create an error for when no overload for a function match the provided args"},
		}
	} else {
		return type_system.NewNeverType(provneance), []Error{
			&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
	}
}

func (c *Checker) handleFuncCall(
	ctx Context,
	fnType *type_system.FuncType,
	expr *ast.CallExpr,
	argTypes []type_system.Type,
	provneance *ast.NodeProvenance,
	errors []Error,
) (type_system.Type, []Error) {
	// Handle generic functions by replacing type refs with fresh type variables
	if len(fnType.TypeParams) > 0 {
		// Create a copy of the function type without type params
		fnTypeWithoutParams := type_system.NewFuncType(
			&type_system.TypeProvenance{Type: fnType},
			nil,
			fnType.Params,
			fnType.Return,
			fnType.Throws,
		)

		// Create fresh type variables for each type parameter
		substitutions := make(map[string]type_system.Type)
		for _, typeParam := range fnType.TypeParams {
			t := c.FreshVar(nil)
			if typeParam.Constraint != nil {
				t.Constraint = typeParam.Constraint
			}
			substitutions[typeParam.Name] = t
		}

		// Substitute type refs in the copied function type with fresh type variables
		fnType = SubstituteTypeParams(fnTypeWithoutParams, substitutions)
	}

	// Find if the function has a rest parameter
	var restIndex = -1
	for i, param := range fnType.Params {
		if param.Pattern != nil {
			if _, isRest := param.Pattern.(*type_system.RestPat); isRest {
				restIndex = i
				break
			}
		}
	}

	if restIndex != -1 {
		// Function has rest parameters
		if len(expr.Args) < restIndex {
			return type_system.NewNeverType(provneance), []Error{&InvalidNumberOfArgumentsError{
				CallExpr: expr,
				Callee:   fnType,
				Args:     expr.Args,
			}}
		}

		// Unify fixed parameters (before rest)
		for i := 0; i < restIndex; i++ {
			argType := argTypes[i]
			paramType := fnType.Params[i].Type
			paramErrors := c.Unify(ctx, argType, paramType)
			errors = slices.Concat(errors, paramErrors)
		}

		// Unify rest arguments with rest parameter type
		if len(expr.Args) > restIndex {
			restParam := fnType.Params[restIndex]
			if arrayType, ok := restParam.Type.(*type_system.TypeRefType); ok && type_system.QualIdentToString(arrayType.Name) == "Array" && len(arrayType.TypeArgs) > 0 {
				elementType := arrayType.TypeArgs[0]
				for i := restIndex; i < len(expr.Args); i++ {
					argType := argTypes[i]
					paramErrors := c.Unify(ctx, argType, elementType)
					errors = slices.Concat(errors, paramErrors)
				}
			} else {
				return type_system.NewNeverType(provneance), []Error{&InvalidNumberOfArgumentsError{
					CallExpr: expr,
					Callee:   fnType,
					Args:     expr.Args,
				}}
			}
		}

		returnType := fnType.Return.Copy()
		returnType.SetProvenance(provneance)
		return returnType, errors
	} else {
		// No rest parameters
		// Compute the number of required (non‑optional) parameters.
		requiredCount := 0
		for _, p := range fnType.Params {
			if !p.Optional {
				requiredCount++
			}
		}
		// Ensure the argument count respects optional parameters.
		if len(expr.Args) < requiredCount || len(expr.Args) > len(fnType.Params) {
			return type_system.NewNeverType(provneance), []Error{&InvalidNumberOfArgumentsError{
				CallExpr: expr,
				Callee:   fnType,
				Args:     expr.Args,
			}}
		}
		// Unify each provided argument with its corresponding parameter.
		for i, argType := range argTypes {
			// Since we have already validated the count, i is safe.
			param := fnType.Params[i]
			paramType := param.Type
			paramErrors := c.Unify(ctx, argType, paramType)
			errors = slices.Concat(errors, paramErrors)
		}
		returnType := fnType.Return.Copy()
		returnType.SetProvenance(provneance)
		return returnType, errors
	}
}
func (c *Checker) inferIfElse(ctx Context, expr *ast.IfElseExpr) (type_system.Type, []Error) {
	// Infer the condition and ensure it's a boolean
	condType, condErrors := c.inferExpr(ctx, expr.Cond)
	unifyErrors := c.Unify(ctx, condType, type_system.NewBoolPrimType(nil))
	errors := slices.Concat(condErrors, unifyErrors)

	// Infer the consequent block (the "then" branch)
	consType, consErrors := c.inferBlock(ctx, &expr.Cons, type_system.NewNeverType(nil))
	errors = slices.Concat(errors, consErrors)

	// Infer the alternative (the "else" branch) if present
	var altType type_system.Type = type_system.NewNeverType(nil)
	if expr.Alt != nil {
		alt := expr.Alt
		if alt.Block != nil {
			var altErrors []Error
			altType, altErrors = c.inferBlock(ctx, alt.Block, type_system.NewNeverType(nil))
			errors = slices.Concat(errors, altErrors)
		} else if alt.Expr != nil {
			t, altErrors := c.inferExpr(ctx, alt.Expr)
			errors = slices.Concat(errors, altErrors)
			altType = t
		} else {
			panic("alt must be a block or expression")
		}
	}

	// The overall type of the if/else is the union of the branches
	result := type_system.NewUnionType(nil, consType, altType)
	expr.SetInferredType(result)
	return result, errors
}

func (c *Checker) inferDoExpr(ctx Context, expr *ast.DoExpr) (type_system.Type, []Error) {
	// Infer the body block - default to undefined if no expression at the end
	resultType, errors := c.inferBlock(ctx, &expr.Body, type_system.NewUndefinedType(nil))

	expr.SetInferredType(resultType)
	return resultType, errors
}

func (c *Checker) inferMatchExpr(ctx Context, expr *ast.MatchExpr) (type_system.Type, []Error) {
	errors := []Error{}

	// Infer the type of the target expression
	targetType, targetErrors := c.inferExpr(ctx, expr.Target)
	errors = slices.Concat(errors, targetErrors)

	// Collect the types of all case bodies
	caseTypes := make([]type_system.Type, 0, len(expr.Cases))

	for _, matchCase := range expr.Cases {
		// Create a new scope for this case to handle pattern bindings
		caseCtx := ctx.WithNewScope()

		// Infer the pattern type and get bindings
		patternType, patternBindings, patternErrors := c.inferPattern(caseCtx, matchCase.Pattern)
		errors = slices.Concat(errors, patternErrors)

		// Add pattern bindings to the case scope
		for name, binding := range patternBindings {
			caseCtx.Scope.setValue(name, binding)
		}

		// Unify the pattern type with the target type to ensure they're compatible
		// The pattern type must be a subtype of the target type.
		// This is opposite of what we do when inferring variable declarations.
		unifyErrors := c.Unify(caseCtx, patternType, targetType)
		errors = slices.Concat(errors, unifyErrors)

		// If there's a guard, check that it's a boolean
		if matchCase.Guard != nil {
			guardType, guardErrors := c.inferExpr(caseCtx, matchCase.Guard)
			errors = slices.Concat(errors, guardErrors)

			guardUnifyErrors := c.Unify(caseCtx, guardType, type_system.NewBoolPrimType(nil))
			errors = slices.Concat(errors, guardUnifyErrors)
		}

		// Infer the type of the case body
		var caseType type_system.Type
		if matchCase.Body.Block != nil {
			// Handle block body using the helper function
			var caseErrors []Error
			caseType, caseErrors = c.inferBlock(caseCtx, matchCase.Body.Block, type_system.NewUndefinedType(nil))
			errors = slices.Concat(errors, caseErrors)
		} else if matchCase.Body.Expr != nil {
			// Handle expression body
			var caseErrors []Error
			caseType, caseErrors = c.inferExpr(caseCtx, matchCase.Body.Expr)
			errors = slices.Concat(errors, caseErrors)
		} else {
			// This shouldn't happen with a well-formed AST
			caseType = type_system.NewNeverType(nil)
		}

		caseTypes = append(caseTypes, caseType)
	}

	// The type of the match expression is the union of all case types
	resultType := type_system.NewUnionType(nil, caseTypes...)

	expr.SetInferredType(resultType)
	return resultType, errors
}

// inferBlock infers the types of all statements in a block and returns the type
// of the block. The type of the block is the type of the last statement if it's
// an expression statement, otherwise it returns the provided default type.
func (c *Checker) inferBlock(
	ctx Context,
	block *ast.Block,
	defaultType type_system.Type,
) (type_system.Type, []Error) {
	errors := []Error{}

	// Process all statements in the block
	for _, stmt := range block.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	// The type of the block is the type of the last statement if it's an expression
	resultType := defaultType
	if len(block.Stmts) > 0 {
		lastStmt := block.Stmts[len(block.Stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			if inferredType := exprStmt.Expr.InferredType(); inferredType != nil {
				resultType = inferredType
			}
		}
	}

	return resultType, errors
}

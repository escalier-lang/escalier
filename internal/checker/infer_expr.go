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

type matchCasePatternInfo struct {
	patternType type_system.Type
	bindings    map[string]*type_system.Binding
	errors      []Error
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (type_system.Type, []Error) {
	var exprType type_system.Type
	var errors []Error

	provenance := &ast.NodeProvenance{Node: expr}

	switch expr := expr.(type) {
	case *ast.ErrorExpr:
		exprType = type_system.NewErrorType(nil)
	case *ast.BinaryExpr:
		neverType := type_system.NewNeverType(nil)

		if expr.Op == ast.Assign {
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)
			errors = rightErrors

			var leftType type_system.Type

			if memberExpr, ok := expr.Left.(*ast.MemberExpr); ok {
				objType, objErrors := c.inferExpr(ctx, memberExpr.Object)
				errors = slices.Concat(errors, objErrors)

				key := PropertyKey{Name: memberExpr.Prop.Name, OptChain: memberExpr.OptChain, span: memberExpr.Prop.Span()}
				var leftErrors []Error
				leftType, leftErrors = c.getMemberType(ctx, objType, key, AccessWrite)
				errors = slices.Concat(errors, leftErrors)

				// Check if the property is readonly (this check takes precedence)
				if c.isPropertyReadonly(ctx, objType, memberExpr.Prop.Name) {
					// Even if the type is mutable, readonly properties cannot be mutated
					errors = append(errors, &CannotMutateReadonlyPropertyError{
						Type:     objType,
						Property: memberExpr.Prop.Name,
						span:     expr.Left.Span(),
					})
				} else {
					pruned := type_system.Prune(objType)
					// Open object types start without a MutabilityType wrapper —
					// mark the property as written since we now know mutation occurs.
					if !markPropertyWritten(pruned, memberExpr.Prop.Name) {
						// Not an open object — check if the object type allows mutation
						if _, ok := pruned.(*type_system.MutabilityType); !ok {
							errors = append(errors, &CannotMutateImmutableError{
								Type: objType,
								span: expr.Left.Span(),
							})
						}
					}
				}
			} else if indexExpr, ok := expr.Left.(*ast.IndexExpr); ok {
				objType, objErrors := c.inferExpr(ctx, indexExpr.Object)
				indexType, indexErrors := c.inferExpr(ctx, indexExpr.Index)
				errors = slices.Concat(errors, objErrors, indexErrors)

				key := IndexKey{Type: indexType, span: indexExpr.Index.Span()}
				var leftErrors []Error
				leftType, leftErrors = c.getMemberType(ctx, objType, key, AccessWrite)
				errors = slices.Concat(errors, leftErrors)

				// Unwrap MutabilityType if present
				unwrappedIndexType := indexType
				if mutType, ok := unwrappedIndexType.(*type_system.MutabilityType); ok {
					unwrappedIndexType = mutType.Type
				}

				isReadonly := false
				if litType, ok := unwrappedIndexType.(*type_system.LitType); ok {
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
						Property: unwrappedIndexType.String(),
						span:     expr.Left.Span(),
					})
				} else {
					pruned := type_system.Prune(objType)
					// Check if the base has an ArrayConstraint — mark index assignment
					if tv, ok := pruned.(*type_system.TypeVarType); ok && tv.ArrayConstraint != nil {
						tv.ArrayConstraint.HasIndexAssignment = true
					} else {
						// Open object types start without a MutabilityType wrapper —
						// mark the property as written since we now know mutation occurs.
						marked := false
						if litType, ok := unwrappedIndexType.(*type_system.LitType); ok {
							if strLit, ok := litType.Lit.(*type_system.StrLit); ok {
								marked = markPropertyWritten(pruned, strLit.Value)
							}
						}
						if !marked {
							// Not an open object — check if the object type allows mutation
							if _, ok := pruned.(*type_system.MutabilityType); !ok {
								errors = append(errors, &CannotMutateImmutableError{
									Type: objType,
									span: expr.Left.Span(),
								})
							}
						}
					}
				}
			} else {
				// IdentExpr or other — infer normally (variable assignment, not member access)
				var leftErrors []Error
				leftType, leftErrors = c.inferExpr(ctx, expr.Left)
				errors = slices.Concat(errors, leftErrors)
			}

			// RHS must be a subtype of LHS because we're assigning RHS to LHS
			errorsBefore := len(errors)
			unifyErrors := c.Unify(ctx, rightType, leftType)
			errors = slices.Concat(errors, unifyErrors)

			// Alias tracking for variable reassignment (Phase 6.3)
			// Only run when unification succeeded — incorrect types could cause
			// false positive transition errors. We intentionally check only for
			// new errors from unification (not len(errors) == 0) because prior
			// errors from LHS/RHS inference (e.g. readonly checks, member access
			// errors) don't invalidate the types used for alias tracking.
			if ctx.Aliases != nil && len(errors) == errorsBefore {
				if identExpr, ok := expr.Left.(*ast.IdentExpr); ok && identExpr.VarID > 0 {
					transErrors := c.trackAliasesForAssignment(ctx, identExpr, expr.Right, leftType)
					errors = slices.Concat(errors, transErrors)
				} else {
					// Object property aliasing — when obj.prop = value and value
					// aliases a variable, merge alias sets of obj and value.
					c.trackAliasesForPropAssignment(ctx, expr.Left, expr.Right)
				}
			}

			exprType = neverType
		} else {
			opBinding := ctx.Scope.GetValue(string(expr.Op))
			if opBinding == nil {
				exprType = neverType
				errors = []Error{&UnknownOperatorError{
					Operator: string(expr.Op),
				}}
			} else {
				// TODO: extract this into a unifyCall method
				// TODO: handle function overloading
				if fnType, ok := opBinding.Type.(*type_system.FuncType); ok {
					if len(fnType.Params) != 2 {
						exprType = neverType
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

						// Copy the return type to avoid mutating the shared operator
						// type's provenance when SetProvenance is called below (#371).
						exprType = fnType.Return.Copy()
					}
				} else {
					exprType = neverType
					errors = []Error{&UnknownOperatorError{Operator: string(expr.Op)}}
				}
			}
		}
	case *ast.UnaryExpr:
		if expr.Op == ast.UnaryMinus {
			if lit, ok := expr.Arg.(*ast.LiteralExpr); ok {
				if num, ok := lit.Lit.(*ast.NumLit); ok {
					exprType = type_system.NewNumLitType(provenance, -num.Value)
					errors = []Error{}
				} else {
					exprType = type_system.NewNeverType(nil)
					errors = []Error{&UnimplementedError{
						message: "Handle unary operators",
						span:    expr.Span(),
					}}
				}
			} else {
				exprType = type_system.NewNeverType(nil)
				errors = []Error{&UnimplementedError{
					message: "Handle unary operators",
					span:    expr.Span(),
				}}
			}
		} else {
			exprType = type_system.NewNeverType(nil)
			errors = []Error{&UnimplementedError{
				message: "Handle unary operators",
				span:    expr.Span(),
			}}
		}
	case *ast.CallExpr:
		exprType, errors = c.inferCallExpr(ctx, expr)
	case *ast.MemberExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)

		if expr.Prop.Name == "" {
			// Missing property name (e.g. trailing dot) — infer the object
			// so its type is available for completions, but the overall
			// MemberExpr is an error.
			exprType = type_system.NewErrorType(nil)
			errors = objErrors
		} else if _, ok := objType.(*type_system.ErrorType); ok {
			// Object is ErrorType — propagate without reporting errors.
			exprType = type_system.NewErrorType(nil)
			errors = objErrors
		} else {
			key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, span: expr.Prop.Span()}
			propType, propErrors := c.getMemberType(ctx, objType, key, AccessRead)

			exprType = propType

			if methodType, ok := propType.(*type_system.FuncType); ok {
				if retType, ok := methodType.Return.(*type_system.TypeRefType); ok && type_system.QualIdentToString(retType.Name) == "Self" {
					t := *methodType   // Create a copy of the struct
					t.Return = objType // Replace `Self` with the object type
					exprType = &t
				}
			}

			errors = slices.Concat(objErrors, propErrors)
		}
	case *ast.IndexExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		indexType, indexErrors := c.inferExpr(ctx, expr.Index)

		errors = slices.Concat(objErrors, indexErrors)

		if _, ok := objType.(*type_system.ErrorType); ok {
			exprType = type_system.NewErrorType(nil)
		} else {
			key := IndexKey{Type: indexType, span: expr.Index.Span()}
			accessType, accessErrors := c.getMemberType(ctx, objType, key, AccessRead)
			exprType = accessType
			errors = slices.Concat(errors, accessErrors)
		}
	case *ast.IdentExpr:
		if binding := ctx.Scope.GetValue(expr.Name); binding != nil {
			// We create a new type and set its provenance to be the identifier
			// instead of the binding source.  This ensures that errors are reported
			// on the identifier itself instead of the binding source.
			t := type_system.Prune(binding.Type)
			if _, isTypeVar := t.(*type_system.TypeVarType); isTypeVar {
				// Don't copy TypeVarType — preserving pointer identity is essential
				// so that unification constraints flow back to the function signature.
				exprType = t
			} else if openObj, ok := t.(*type_system.ObjectType); ok && openObj.Open {
				// Don't copy open ObjectTypes — preserving pointer identity is
				// essential so that property additions during inference (e.g.
				// accessing obj.baz after obj.bar) flow back to the original type.
				exprType = t
			} else {
				exprType = t.Copy()
				exprType.SetProvenance(&ast.NodeProvenance{Node: expr})
			}
			expr.Source = binding.Source
			errors = nil
		} else if namespace := ctx.Scope.getNamespace(expr.Name); namespace != nil {
			t := type_system.NewNamespaceType(provenance, namespace)
			exprType = t
			errors = nil
		} else {
			exprType = type_system.NewNeverType(nil)
			errors = []Error{&UnknownIdentifierError{Ident: expr, span: expr.Span()}}
		}
	case *ast.LiteralExpr:
		exprType, errors = c.inferLit(expr.Lit)
		exprType = &type_system.MutabilityType{
			Type:       exprType,
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.TupleExpr:
		elemTypes := []type_system.Type{}
		errors = []Error{}
		for _, elem := range expr.Elems {
			if spread, ok := elem.(*ast.ArraySpreadExpr); ok {
				spreadType, spreadErrors := c.inferExpr(ctx, spread.Value)
				errors = slices.Concat(errors, spreadErrors)

				prunedType := type_system.Prune(spreadType)
				// Unwrap MutabilityType if present
				if mut, ok := prunedType.(*type_system.MutabilityType); ok {
					prunedType = type_system.Prune(mut.Type)
				}
				handled := false
				switch st := prunedType.(type) {
				case *type_system.TupleType:
					// Inline tuple elements directly
					elemTypes = append(elemTypes, st.Elems...)
					handled = true
				case *type_system.TypeVarType:
					// If the type variable originated from an object rest pattern
					// (e.g. {a, ...rest}), it must be an object and is not iterable.
					if st.IsObjectRest {
						err := NewGenericError(
							"Object rest type is not iterable",
							spread.Span(),
						)
						errors = append(errors, err)
						elementType := type_system.NewAnyType(nil)
						elemTypes = append(elemTypes, type_system.NewRestSpreadType(nil, &type_system.TypeRefType{
							Name:     type_system.NewIdent("Array"),
							TypeArgs: []type_system.Type{elementType},
						}))
					} else {
						elemTypes = append(elemTypes, type_system.NewRestSpreadType(nil, st))
					}
					handled = true
				case *type_system.TypeRefType:
					if c.isArrayType(st) {
						// Array<T> - preserve as RestSpreadType
						elemTypes = append(elemTypes, type_system.NewRestSpreadType(nil, st))
						handled = true
					}
				}
				if !handled {
					// Other types (including non-Array TypeRefTypes like Generator) -
					// extract element type via iterability check
					elementType := c.GetIterableElementType(ctx, spreadType)
					if elementType == nil {
						err := NewGenericError(
							fmt.Sprintf("Type '%s' is not iterable", spreadType),
							spread.Span(),
						)
						errors = append(errors, err)
						elementType = type_system.NewAnyType(nil)
					}
					elemTypes = append(elemTypes, type_system.NewRestSpreadType(nil, &type_system.TypeRefType{
						Name:     type_system.NewIdent("Array"),
						TypeArgs: []type_system.Type{elementType},
					}))
				}
			} else {
				elemType, elemErrors := c.inferExpr(ctx, elem)
				elemTypes = append(elemTypes, elemType)
				errors = slices.Concat(errors, elemErrors)
			}
		}

		// Collapse tuples that contain only Array rest spreads into a plain
		// Array type: [...Array<T>] → Array<T>, and
		// [...Array<T1>, ...Array<T2>] → Array<T1 | T2>.
		// Otherwise returns a TupleType.
		exprType = &type_system.MutabilityType{
			Type:       collapseArrayRestSpreads(c, elemTypes),
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.ObjectExpr:
		// Create a context for the object so that we can add a `Self` type to it
		objCtx := ctx.WithNewScope()

		// TODO(#413): typeElems may contain nil entries when astKeyToTypeKey
		// fails (e.g. for unsupported computed key types). These nil entries
		// cause a panic in bind. Filter them out before creating the ObjectType.
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
			// No object-type constraint is enforced on the spread source.
			// This matches JS/TS semantics where spreading non-objects
			// (e.g. {...42}) is valid and produces {}.
			case *ast.ObjSpreadExpr:
				sourceType, spreadErrors := c.inferExpr(ctx, elem.Value)
				errors = append(errors, spreadErrors...)
				typeElems[i] = type_system.NewRestSpreadElem(sourceType)
			}
		}

		objType := type_system.NewObjectType(provenance, typeElems)
		bindErrors := c.bind(objCtx, selfType, objType, make(unifySeen))
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
					methodCtx, methodType, paramBindings, methodExpr.Fn.Params, methodExpr.Fn.Body, methodExpr.Fn.Async)
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
					objCtx, funcType, paramBindings, getterExpr.Fn.Params, getterExpr.Fn.Body, getterExpr.Fn.Async)
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
					objCtx, funcType, paramBindings, setterExpr.Fn.Params, setterExpr.Fn.Body, setterExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)
			case *ast.ObjSpreadExpr:
				// Already handled in the first loop — nothing to do here.
			}

			i++
		}

		exprType = &type_system.MutabilityType{
			Type:       selfType,
			Mutability: type_system.MutabilityUncertain,
		}
	case *ast.FuncExpr:
		funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig, expr)
		errors = slices.Concat(errors, sigErrors)

		// Allocate call-site maps for outermost FuncExprs. Nested FuncExprs
		// inherit the parent's maps via Context copying.
		if !ctx.InFuncBody {
			callSites := make(map[int][]*type_system.FuncType)
			callSiteTypeVars := make(map[int]*type_system.TypeVarType)
			funcCtx.CallSites = &callSites
			funcCtx.CallSiteTypeVars = &callSiteTypeVars
		}

		inferErrors := c.inferFuncBodyWithFuncSigType(funcCtx, funcType, paramBindings, expr.FuncSig.Params, expr.Body, expr.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)

		// Only generalize top-level FuncExprs. Nested FuncExprs (inside function
		// bodies) share type variables with outer functions, so their generalization
		// is deferred to the outermost function.
		if !ctx.InFuncBody {
			c.resolveCallSites(funcCtx)
			GeneralizeFuncType(funcType)
		}

		exprType = funcType
	case *ast.IfElseExpr:
		exprType, errors = c.inferIfElse(ctx, expr)
	case *ast.DoExpr:
		exprType, errors = c.inferDoExpr(ctx, expr)
	case *ast.MatchExpr:
		exprType, errors = c.inferMatchExpr(ctx, expr)
	case *ast.ThrowExpr:
		// Infer the type of the argument being thrown
		_, argErrors := c.inferExpr(ctx, expr.Arg)
		errors = argErrors
		// Throw expressions have type never since they don't return a value
		exprType = type_system.NewNeverType(nil)
	case *ast.AwaitExpr:
		// Await can only be used inside async functions
		if !ctx.IsAsync {
			errors = []Error{
				&UnimplementedError{
					message: "await can only be used inside async functions",
					span:    expr.Span(),
				},
			}
			exprType = type_system.NewNeverType(nil)
		} else {
			// Infer the type of the expression being awaited
			argType, argErrors := c.inferExpr(ctx, expr.Arg)
			errors = argErrors

			// If the argument is a Promise, unwrap its value type.
			// Escalier's Promise type may be declared with one or two type arguments:
			//   Promise<T>               – only a resolved value type
			//   Promise<T, E>            – value type and error (throws) type
			// We treat the first argument as the awaited value type.
			// If a second argument exists, we record it on the AwaitExpr so
			// the function body can incorporate the possible throws.
			// If the argument is a Promise, unwrap its value type.
			if promiseType, ok := argType.(*type_system.TypeRefType); ok && type_system.QualIdentToString(promiseType.Name) == "Promise" {
				if len(promiseType.TypeArgs) >= 1 {
					// Use the first type argument as the awaited value.
					exprType = promiseType.TypeArgs[0]
				} else {
					// No type arguments – fallback to never.
					exprType = type_system.NewNeverType(nil)
				}
				// Record the throws type via context pointer
				if ctx.AwaitThrowTypes != nil {
					if len(promiseType.TypeArgs) >= 2 {
						*ctx.AwaitThrowTypes = append(*ctx.AwaitThrowTypes, promiseType.TypeArgs[1])
					} else {
						*ctx.AwaitThrowTypes = append(*ctx.AwaitThrowTypes, type_system.NewNeverType(nil))
					}
				}
			} else {
				// If not a Promise type, this is an error
				errors = append(errors, &UnimplementedError{
					message: "await expression expects a Promise type",
					span:    expr.Span(),
				})
				exprType = type_system.NewNeverType(nil)
			}
		}
	case *ast.YieldExpr:
		// yield/yield from can only be used inside functions (where ContainsYield is allocated)
		if ctx.ContainsYield == nil {
			keyword := "yield"
			if expr.IsDelegate {
				keyword = "yield from"
			}
			errors = []Error{&UnimplementedError{
				message: fmt.Sprintf("'%s' can only be used inside a function", keyword),
				span:    expr.Span(),
			}}
			exprType = type_system.NewNeverType(provenance)
		} else {
			// Mark this function context as containing yield (makes it a generator)
			*ctx.ContainsYield = true

			if expr.IsDelegate {
				// yield from: the value must be iterable
				if expr.Value == nil {
					errors = []Error{&UnimplementedError{
						message: "'yield from' requires an iterable expression",
						span:    expr.Span(),
					}}
					exprType = type_system.NewNeverType(provenance)
				} else {
					valueType, errs := c.inferExpr(ctx, expr.Value)
					errors = errs

					// In async generators, yield from can delegate to both async and sync iterables
					var elementType type_system.Type
					if ctx.IsAsync {
						elementType = c.GetAsyncIterableElementType(ctx, valueType)
						if elementType == nil {
							elementType = c.GetIterableElementType(ctx, valueType)
						}
					} else {
						elementType = c.GetIterableElementType(ctx, valueType)
					}

					if elementType == nil {
						errors = append(errors, &UnimplementedError{
							message: fmt.Sprintf("Type '%s' is not iterable", valueType),
							span:    expr.Value.Span(),
						})
					} else {
						ctx.AddYieldedType(elementType)
					}

					// The yield from expression evaluates to TReturn of the delegated iterator
					delegatedReturnType := c.GetIteratorReturnType(ctx, valueType)
					if delegatedReturnType == nil {
						exprType = type_system.NewNeverType(provenance)
					} else {
						exprType = delegatedReturnType
					}
				}
			} else {
				// Regular yield
				if expr.Value != nil {
					valueType, errs := c.inferExpr(ctx, expr.Value)
					errors = errs
					ctx.AddYieldedType(valueType)
				} else {
					// Bare `yield` yields undefined
					ctx.AddYieldedType(type_system.NewUndefinedType(provenance))
				}

				// The yield expression evaluates to TNext (value passed to .next()).
				// Currently GeneratorNextType is always nil (see Context definition),
				// so yield always evaluates to never. This is fine because most
				// generators are consumed via for...in, not manual .next(value).
				if ctx.GeneratorNextType != nil {
					exprType = ctx.GeneratorNextType
				} else {
					exprType = type_system.NewNeverType(provenance)
				}
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
		exprType, errors = c.inferCallExpr(ctx, callExpr)
	case *ast.TypeCastExpr:
		// Infer the type of the expression being cast
		castType, exprErrors := c.inferExpr(ctx, expr.Expr)
		errors = slices.Concat(errors, exprErrors)

		// Infer the type annotation to get the target type
		targetType, typeAnnErrors := c.inferTypeAnn(ctx, expr.TypeAnn)
		errors = slices.Concat(errors, typeAnnErrors)

		// Check that the expression type is a subtype of the target type
		// For type casting, we require that exprType can be unified with targetType
		unifyErrors := c.Unify(ctx, castType, targetType)
		errors = slices.Concat(errors, unifyErrors)

		// The result type is the target type
		exprType = targetType
	case *ast.TemplateLitExpr:
		// Template literals always produce strings
		// We need to infer all the interpolated expressions for type checking
		errors = []Error{}
		for _, expr := range expr.Exprs {
			_, exprErrors := c.inferExpr(ctx, expr)
			errors = slices.Concat(errors, exprErrors)
		}
		// Template literals always result in a string type
		exprType = type_system.NewStrPrimType(provenance)
	case *ast.IfLetExpr:
		// Infer the type of the target expression
		targetType, targetErrors := c.inferExpr(ctx, expr.Target)
		errors = slices.Concat(errors, targetErrors)

		// Infer the pattern and get the bindings it creates
		patternType, bindings, patternErrors := c.inferPattern(ctx, expr.Pattern)
		errors = slices.Concat(errors, patternErrors)

		// For if-let expressions, we need to narrow the target type by removing null/undefined
		// The pattern should match the non-nullable part of the target type
		narrowedTargetType := targetType
		if unionType, ok := type_system.Prune(targetType).(*type_system.UnionType); ok {
			definedElems := c.getDefinedElems(unionType)
			if len(definedElems) > 0 {
				narrowedTargetType = type_system.NewUnionType(nil, definedElems...)
			}
		}

		// Unify the pattern type with the narrowed target type
		unifyErrors := c.Unify(ctx, patternType, narrowedTargetType)
		errors = slices.Concat(errors, unifyErrors)

		// Create a new scope and context with the pattern bindings
		newNamespace := &type_system.Namespace{
			Values:     map[string]*type_system.Binding{},
			Types:      map[string]*type_system.TypeAlias{},
			Namespaces: map[string]*type_system.Namespace{},
		}
		// Add the pattern bindings to the new namespace
		for name, binding := range bindings {
			newNamespace.Values[name] = binding
		}
		newScope := ctx.Scope.WithNewScopeAndNamespace(newNamespace)
		newCtx := ctx.WithScope(newScope)

		// Infer the type of the consequent block with the new context
		consType, consErrors := c.inferBlock(newCtx, &expr.Cons, type_system.NewNeverType(nil))
		errors = slices.Concat(errors, consErrors)

		// Infer the type of the alternative (if present)
		// If there's no else clause, the if-let expression returns undefined when the pattern doesn't match
		var altType type_system.Type = type_system.NewUndefinedType(nil)
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

		// The overall type of the if let is the union of the consequent and alternative types
		exprType = type_system.NewUnionType(provenance, consType, altType)
	case *ast.TryCatchExpr:
		errors = []Error{}

		// TODO:
		// - find any expressions that can throw inside the try block, this includes all:
		//   - `throw` expression
		//   - function calls that can throw
		//   - await expressions where the Promise can reject

		// Infer the type of the try block
		tryType, tryErrors := c.inferBlock(ctx, &expr.Try, type_system.NewUndefinedType(nil))
		errors = slices.Concat(errors, tryErrors)

		// Now that we've inferred the try block, find all the throw types within it
		throwTypes, throwErrors := c.findThrowTypes(ctx, &expr.Try)
		errors = slices.Concat(errors, throwErrors)

		// Create a union of all throw types to use as the target type for catch patterns
		var throwTargetType type_system.Type
		if len(throwTypes) == 0 {
			// If no throw types were found, use never type
			throwTargetType = type_system.NewNeverType(nil)
		} else if len(throwTypes) == 1 {
			throwTargetType = throwTypes[0]
		} else {
			throwTargetType = type_system.NewUnionType(nil, throwTypes...)
		}

		// Collect the types of all catch case bodies
		catchTypes := make([]type_system.Type, 0, len(expr.Catch))

		for _, matchCase := range expr.Catch {
			// Create a new scope for this catch case to handle pattern bindings
			caseCtx := ctx.WithNewScope()

			// Infer the pattern type and get bindings
			patternType, patternBindings, patternErrors := c.inferPattern(caseCtx, matchCase.Pattern)
			errors = slices.Concat(errors, patternErrors)

			// Add pattern bindings to the case scope
			for name, binding := range patternBindings {
				caseCtx.Scope.setValue(name, binding)
			}

			// Unify the pattern type with the throw target type
			unifyErrors := c.Unify(caseCtx, patternType, throwTargetType)
			errors = slices.Concat(errors, unifyErrors)

			// If there's a guard, check that it's a boolean
			if matchCase.Guard != nil {
				guardType, guardErrors := c.inferExpr(caseCtx, matchCase.Guard)
				errors = slices.Concat(errors, guardErrors)

				// Unify the guard type with boolean
				guardUnifyErrors := c.Unify(caseCtx, guardType, type_system.NewBoolPrimType(nil))
				errors = slices.Concat(errors, guardUnifyErrors)
			}

			// Infer the type of the case body
			var caseType type_system.Type
			if matchCase.Body.Block != nil {
				var bodyErrors []Error
				caseType, bodyErrors = c.inferBlock(caseCtx, matchCase.Body.Block, type_system.NewUndefinedType(nil))
				errors = slices.Concat(errors, bodyErrors)
			} else if matchCase.Body.Expr != nil {
				var exprErrors []Error
				caseType, exprErrors = c.inferExpr(caseCtx, matchCase.Body.Expr)
				errors = slices.Concat(errors, exprErrors)
			} else {
				// Empty case body defaults to never
				caseType = type_system.NewNeverType(nil)
			}

			catchTypes = append(catchTypes, caseType)
		}

		// The type of the try-catch expression is the union of the try type
		// and all catch case types
		if len(catchTypes) > 0 {
			allTypes := append([]type_system.Type{tryType}, catchTypes...)
			exprType = type_system.NewUnionType(provenance, allTypes...)
		} else {
			// No catch clauses, just the try type
			exprType = tryType
		}
	case *ast.JSXElementExpr:
		exprType, errors = c.inferJSXElement(ctx, expr)
	case *ast.JSXFragmentExpr:
		exprType, errors = c.inferJSXFragment(ctx, expr)
	default:
		exprType = type_system.NewNeverType(nil)
		errors = []Error{
			&UnimplementedError{
				message: "Infer expression type: " + fmt.Sprintf("%T", expr),
				span:    expr.Span(),
			},
		}
	}

	// Always set the inferred type on the expression before returning
	expr.SetInferredType(exprType)
	exprType.SetProvenance(provenance)
	return exprType, errors
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

// collapseArrayRestSpreads builds the result type for a tuple expression.
// If all elements are Array rest spreads, it collapses them into a single
// Array<T1 | T2 | ...> (e.g. [...Array<T>] → Array<T>). Otherwise it
// returns a TupleType with the elements as-is.
//
// Note: this function does not Prune elements before checking because it is
// called during inference before call-site specialization. TypeVarType-based
// rests (e.g. [...T0]) are still unbound at this point and correctly fall
// through to the TupleType path.
func collapseArrayRestSpreads(c *Checker, elems []type_system.Type) type_system.Type {
	var unionMembers []type_system.Type
	for _, elem := range elems {
		rest, ok := elem.(*type_system.RestSpreadType)
		if !ok {
			return type_system.NewTupleType(nil, elems...)
		}
		inner := type_system.Prune(rest.Type)
		ref, ok := inner.(*type_system.TypeRefType)
		if !ok || !c.isArrayType(ref) || len(ref.TypeArgs) == 0 {
			return type_system.NewTupleType(nil, elems...)
		}
		unionMembers = append(unionMembers, ref.TypeArgs[0])
	}
	if len(unionMembers) == 0 {
		return type_system.NewTupleType(nil, elems...)
	}
	return &type_system.TypeRefType{
		Name:     type_system.NewIdent("Array"),
		TypeArgs: []type_system.Type{type_system.NewUnionType(nil, unionMembers...)},
	}
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

	switch t := calleeType.(type) {
	case *type_system.ErrorType:
		return type_system.NewErrorType(provneance), errors
	case *type_system.FuncType:
		return c.handleFuncCall(ctx, t, expr, argTypes, provneance, errors)

	case *type_system.TypeRefType:
		name := type_system.QualIdentToString(t.Name)
		typeAlias := ctx.Scope.GetTypeAlias(name)
		if typeAlias == nil {
			return type_system.NewNeverType(provneance), []Error{
				&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
		}

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

	case *type_system.ObjectType:
		// Check if ObjectType has a constructor or callable element
		var fnType *type_system.FuncType = nil

		for _, elem := range t.Elems {
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

	case *type_system.IntersectionType:
		// Try each function type in the intersection as a potential overload
		attemptedErrors := [][]Error{}

		for _, funcType := range t.Types {
			if funcType, ok := funcType.(*type_system.FuncType); ok {
				// Try this overload
				retType, callErrors := c.handleFuncCall(ctx, funcType, expr, argTypes, provneance, []Error{})

				// If this overload succeeds (no errors), use it
				if len(callErrors) == 0 {
					return retType, errors
				}

				// Otherwise, record the errors for this overload attempt
				attemptedErrors = append(attemptedErrors, callErrors)
			}
		}

		// No overload matched - create a comprehensive error
		overloadErr := &NoMatchingOverloadError{
			CallExpr:         expr,
			IntersectionType: t,
			AttemptedErrors:  attemptedErrors,
		}
		return type_system.NewNeverType(provneance), append(errors, overloadErr)

	case *type_system.TypeVarType:
		// The callee is an unresolved type variable (e.g., a function parameter
		// with no type annotation being called). Create a synthetic FuncType
		// matching the call site, collect it for deferred resolution, and
		// delegate to handleFuncCall for argument unification.
		//
		// Strip uncertain mutability (mut?) from argument types so the
		// inferred function signature is clean (e.g. fn(42) not fn(mut? 42)).
		// The mut? wrapper tracks whether a value will be mutated and is
		// resolved during generalization — it shouldn't leak into inferred
		// param types.
		unwrappedArgTypes := make([]type_system.Type, len(argTypes))
		for i, at := range argTypes {
			unwrappedArgTypes[i] = unwrapMutability(at)
		}

		params := make([]*type_system.FuncParam, len(unwrappedArgTypes))
		for i := range unwrappedArgTypes {
			params[i] = &type_system.FuncParam{
				Pattern: type_system.NewIdentPat(fmt.Sprintf("arg%d", i)),
				Type:    c.FreshVar(nil),
			}
		}

		// Collect the call site — don't bind the TypeVar yet so that
		// multiple calls with different arg types can produce an intersection.
		// Note: ctx.CallSites is always non-nil here because a TypeVarType
		// callee requires a function param binding, which only exists inside
		// function bodies where the caller allocates CallSites.

		retType := c.FreshVar(nil)
		synthFuncType := type_system.NewFuncType(nil, nil, params, retType, type_system.NewNeverType(nil))

		(*ctx.CallSites)[t.ID] = append((*ctx.CallSites)[t.ID], synthFuncType)
		(*ctx.CallSiteTypeVars)[t.ID] = t

		return c.handleFuncCall(ctx, synthFuncType, expr, unwrappedArgTypes, provneance, errors)

	default:
		return type_system.NewNeverType(provneance), []Error{
			&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
	}
}

// instantiateGenericFunc creates a fresh instance of a generic function type by
// replacing all type parameters with fresh type variables. This implements HM
// "instantiation at use" — each reference to a polymorphic binding gets its
// own fresh type variables.
func (c *Checker) instantiateGenericFunc(fnType *type_system.FuncType) *type_system.FuncType {
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
		substitutions[typeParam.Name] = t
	}

	// After all type parameters are in the substitution map,
	// substitute any type parameter references in the constraints
	for _, typeParam := range fnType.TypeParams {
		if typeParam.Constraint != nil {
			substitutedConstraint := SubstituteTypeParams(typeParam.Constraint, substitutions)
			if freshVar, ok := substitutions[typeParam.Name].(*type_system.TypeVarType); ok {
				freshVar.Constraint = substitutedConstraint
			}
		}
	}

	// Substitute type refs in the copied function type with fresh type variables
	return SubstituteTypeParams(fnTypeWithoutParams, substitutions)
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
		fnType = c.instantiateGenericFunc(fnType)
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
			// Instantiate generic function arguments at the call site.
			if ft, ok := argType.(*type_system.FuncType); ok && len(ft.TypeParams) > 0 {
				argType = c.instantiateGenericFunc(ft)
			}
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
					if ft, ok := argType.(*type_system.FuncType); ok && len(ft.TypeParams) > 0 {
						argType = c.instantiateGenericFunc(ft)
					}
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

		returnType := fnType.Return
		// Don't copy TypeVarType — preserving pointer identity is essential
		// so that unification constraints flow back to the caller.
		if _, isTypeVar := type_system.Prune(returnType).(*type_system.TypeVarType); !isTypeVar {
			returnType = returnType.Copy()
			returnType.SetProvenance(provneance)
		}
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
			// Instantiate generic function arguments at the call site.
			if ft, ok := argType.(*type_system.FuncType); ok && len(ft.TypeParams) > 0 {
				argType = c.instantiateGenericFunc(ft)
			}
			// Since we have already validated the count, i is safe.
			param := fnType.Params[i]
			paramType := param.Type
			paramErrors := c.Unify(ctx, argType, paramType)
			errors = slices.Concat(errors, paramErrors)
		}
		returnType := fnType.Return
		// Don't copy TypeVarType — preserving pointer identity is essential
		// so that unification constraints flow back to the caller.
		if _, isTypeVar := type_system.Prune(returnType).(*type_system.TypeVarType); !isTypeVar {
			returnType = returnType.Copy()
			returnType.SetProvenance(provneance)
		}
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

	// matchErrors tracks errors from target inference, pattern inference,
	// and unification. If any of these fail, exhaustiveness checking is
	// skipped to avoid misleading secondary diagnostics.
	matchErrors := []Error{}

	// Infer the type of the target expression
	targetType, targetErrors := c.inferExpr(ctx, expr.Target)
	matchErrors = slices.Concat(matchErrors, targetErrors)

	// Check if target type is a constructor when patterns expect instances
	targetObjType, isObj := type_system.Prune(targetType).(*type_system.ObjectType)
	if isObj {
		hasCallableOrConstructor := false
		for _, elem := range targetObjType.Elems {
			switch elem.(type) {
			case *type_system.CallableElem, *type_system.ConstructorElem:
				hasCallableOrConstructor = true
			}
		}
		if hasCallableOrConstructor {
			for _, matchCase := range expr.Cases {
				if _, ok := matchCase.Pattern.(*ast.ExtractorPat); ok {
					matchErrors = append(matchErrors, &ConstructorUsedAsMatchTargetError{
						TargetType: targetType,
						span:       matchCase.Pattern.Span(),
					})
					break
				}
			}
		}
	}

	// Phase 1: Infer all pattern types and collect bindings up front.
	// This allows Phase 2 to inspect every pattern before any unification
	// with the target type has occurred.
	//
	// Note: the temporary scopes created here are discarded — Phase 3 creates
	// fresh scopes and re-adds the bindings. This is safe because inferPattern
	// has no side effects on the scope beyond the returned bindings map.
	patternInfos := make([]matchCasePatternInfo, len(expr.Cases))
	for i, matchCase := range expr.Cases {
		caseCtx := ctx.WithNewScope()
		patType, bindings, patErrors := c.inferPattern(caseCtx, matchCase.Pattern)
		patternInfos[i] = matchCasePatternInfo{patType, bindings, patErrors}
	}

	// Phase 2: If the target is an unresolved TypeVar (e.g. an unannotated
	// function parameter), infer its type from the match patterns before
	// processing individual cases.
	if _, isTypeVar := type_system.Prune(targetType).(*type_system.TypeVarType); isTypeVar {
		if inferredType := c.inferTargetTypeFromPatterns(ctx, expr.Cases, patternInfos); inferredType != nil {
			unifyErrors := c.Unify(ctx, targetType, inferredType)
			matchErrors = slices.Concat(matchErrors, unifyErrors)
		}
	}

	// Phase 3: Process each case — unify pattern with target, check guards,
	// and infer body types.
	caseTypes := make([]type_system.Type, 0, len(expr.Cases))

	for i, matchCase := range expr.Cases {
		// Create a new scope for this case to handle pattern bindings
		caseCtx := ctx.WithNewScope()

		patternType := patternInfos[i].patternType
		matchErrors = slices.Concat(matchErrors, patternInfos[i].errors)

		// Add pattern bindings to the case scope
		for name, binding := range patternInfos[i].bindings {
			caseCtx.Scope.setValue(name, binding)
		}

		// Unify the pattern type with the target type to ensure they're compatible
		// The pattern type must be a subtype of the target type.
		// This is opposite of what we do when inferring variable declarations.
		patMatchCtx := caseCtx
		patMatchCtx.IsPatMatch = true
		unifyErrors := c.Unify(patMatchCtx, patternType, targetType)
		matchErrors = slices.Concat(matchErrors, unifyErrors)

		// If there's a guard, check that it's a boolean
		if matchCase.Guard != nil {
			guardType, guardErrors := c.inferExpr(caseCtx, matchCase.Guard)
			matchErrors = slices.Concat(matchErrors, guardErrors)

			guardUnifyErrors := c.Unify(caseCtx, guardType, type_system.NewBoolPrimType(nil))
			matchErrors = slices.Concat(matchErrors, guardUnifyErrors)
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

	// Always include match errors in the final error list.
	errors = slices.Concat(errors, matchErrors)

	// Only check exhaustiveness if type inference/unification succeeded.
	// When prior errors exist, inferred types and MatchedUnionMembers may
	// be in an inconsistent state and would produce misleading diagnostics.
	if len(matchErrors) == 0 {
		result := c.checkExhaustiveness(expr, targetType)

		if !result.IsExhaustive {
			// UncoveredTypes is non-empty whenever the match is non-exhaustive
			// at the top level — including non-finite types (number, string,
			// etc.) where analyzeCoverageExhaustiveness sets UncoveredTypes
			// to [targetType]. The only way to reach !IsExhaustive with
			// empty UncoveredTypes is via PartialCoverages (handled below).
			if len(result.UncoveredTypes) > 0 {
				errors = append(errors, &NonExhaustiveMatchError{
					UncoveredTypes: result.UncoveredTypes,
					IsNonFinite:    result.IsNonFinite,
					span:           expr.Span(),
				})
			}
			for _, pc := range result.PartialCoverages {
				// TODO(#433): This flattens nested partial coverages into a
				// single InnerUncovered list, losing depth information beyond
				// one level. Update to walk PartialCoverages recursively so
				// error messages can describe the full nesting path.
				var innerUncovered []type_system.Type
				innerUncovered = append(innerUncovered, pc.InnerResult.UncoveredTypes...)
				for _, innerPC := range pc.InnerResult.PartialCoverages {
					innerUncovered = append(innerUncovered, innerPC.Member)
				}

				isNonFinite := pc.InnerResult.IsNonFinite
				if len(pc.InnerResult.PartialCoverages) > 0 && len(pc.InnerResult.UncoveredTypes) == 0 {
					isNonFinite = false
				}

				errors = append(errors, &InnerNonExhaustiveMatchError{
					MemberType:     pc.Member,
					InnerUncovered: innerUncovered,
					IsNonFinite:    isNonFinite,
					span:           expr.Span(),
				})
			}
		}

		for _, redundant := range result.RedundantCases {
			errors = append(errors, &RedundantMatchCaseWarning{
				span: redundant.Span,
			})
		}
	}

	// The type of the match expression is the union of all case types
	resultType := type_system.NewUnionType(nil, caseTypes...)

	expr.SetInferredType(resultType)
	return resultType, errors
}

// inferTargetTypeFromPatterns examines the match case patterns to determine what
// type the match target should have. This is used when the target is an unresolved
// TypeVar (e.g. an unannotated function parameter). Each pattern type contributes
// differently:
//   - ExtractorPat: the parent enum type (from the constructor's return type)
//   - InstancePat: the class type (from the type alias)
//   - LitPat, ObjectPat, TuplePat: the pattern's inferred type directly
//   - IdentPat, WildcardPat: no contribution (IdentPat contributes indirectly
//     through body usage via the alias chain created during unification)
//
// When multiple patterns contribute types, a union is built.
func (c *Checker) inferTargetTypeFromPatterns(
	ctx Context,
	cases []*ast.MatchCase,
	patternInfos []matchCasePatternInfo,
) type_system.Type {
	var targetTypes []type_system.Type
	seenEnums := make(map[string]bool)

	for i, matchCase := range cases {
		switch p := matchCase.Pattern.(type) {
		case *ast.ExtractorPat:
			// Multiple extractors from the same enum produce the same enum
			// type, so we deduplicate by enum name. Extractors from different
			// enums each contribute their own type.
			if enumType := c.getEnumTypeFromExtractor(ctx, p); enumType != nil {
				if ref, ok := enumType.(*type_system.TypeRefType); ok {
					key := type_system.QualIdentToString(ref.Name)
					if !seenEnums[key] {
						seenEnums[key] = true
						targetTypes = append(targetTypes, enumType)
					}
				}
			}
		case *ast.InstancePat:
			typeAlias := resolveQualifiedTypeAlias(ctx, convertQualIdent(p.ClassName))
			if typeAlias != nil {
				var typeArgs []type_system.Type
				for range typeAlias.TypeParams {
					typeArgs = append(typeArgs, c.FreshVar(nil))
				}
				targetTypes = append(targetTypes, type_system.NewTypeRefTypeFromQualIdent(
					nil, convertQualIdent(p.ClassName), typeAlias, typeArgs...,
				))
			}
		case *ast.LitPat:
			targetTypes = append(targetTypes, patternInfos[i].patternType)
		case *ast.ObjectPat:
			targetTypes = append(targetTypes, patternInfos[i].patternType)
		case *ast.TuplePat:
			targetTypes = append(targetTypes, patternInfos[i].patternType)
			// IdentPat and WildcardPat contribute no type info during pre-scan.
			// IdentPat bindings are aliased with the target TypeVar in Phase 3,
			// so body expressions can still constrain the target indirectly.
		}
	}

	if len(targetTypes) == 0 {
		return nil
	}
	if len(targetTypes) == 1 {
		return targetTypes[0]
	}
	return type_system.NewUnionType(nil, targetTypes...)
}

// getEnumTypeFromExtractor extracts the parent enum type from an extractor
// pattern's constructor. For example, given `Option.Some(value)`, it returns
// the `Option<T>` TypeRefType with type params instantiated as fresh TypeVars.
func (c *Checker) getEnumTypeFromExtractor(ctx Context, p *ast.ExtractorPat) type_system.Type {
	binding := resolveQualifiedValue(ctx, convertQualIdent(p.Name))
	if binding == nil {
		return nil
	}
	objType, ok := type_system.Prune(binding.Type).(*type_system.ObjectType)
	if !ok {
		return nil
	}
	for _, elem := range objType.Elems {
		if ctor, ok := elem.(*type_system.ConstructorElem); ok {
			returnType := ctor.Fn.Return
			if len(ctor.Fn.TypeParams) > 0 {
				// Instantiate type params with fresh TypeVars, following the
				// same pattern as instantiateGenericFunc.
				substitutions := make(map[string]type_system.Type)
				for _, tp := range ctor.Fn.TypeParams {
					substitutions[tp.Name] = c.FreshVar(nil)
				}
				returnType = SubstituteTypeParams(returnType, substitutions)
			}
			return returnType
		}
	}
	return nil
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

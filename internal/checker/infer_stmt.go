package checker

import (
	"fmt"
	"slices"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferStmt(ctx Context, stmt ast.Stmt) []Error {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		_, errors := c.inferExpr(ctx, stmt.Expr)
		return errors
	case *ast.DeclStmt:
		return c.inferDecl(ctx, stmt.Decl)
	case *ast.ReturnStmt:
		errors := []Error{}
		if stmt.Expr != nil {
			// The inferred type is ignored here, but inferExpr still attaches
			// the inferred type to the expression.  This is used later on this
			// file, search for `ReturnVisitor` to see how it is used.
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = exprErrors
		}
		return errors
	case *ast.ImportStmt:
		errors := c.inferImport(ctx, stmt)
		return errors
	default:
		panic(fmt.Sprintf("Unknown statement type: %T", stmt))
	}
}

func (c *Checker) inferDecl(ctx Context, decl ast.Decl) []Error {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		// Handle incomplete function declarations
		if decl.Name.Name == "" {
			return []Error{}
		}
		return c.inferFuncDecl(ctx, decl)
	case *ast.VarDecl:
		bindings, errors := c.inferVarDecl(ctx, decl)
		maps.Copy(ctx.Scope.Namespace.Values, bindings)
		return errors
	case *ast.TypeDecl:
		typeAlias, errors := c.inferTypeDecl(ctx, decl)
		ctx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
		return errors
	case *ast.InterfaceDecl:
		typeAlias, errors := c.inferInterface(ctx, decl)
		// For interfaces, we directly set the type alias in the namespace
		// because interface merging is handled in inferInterface
		ctx.Scope.Namespace.Types[decl.Name.Name] = typeAlias
		return errors
	case *ast.ClassDecl:
		panic("TODO: infer class declaration")
	case *ast.EnumDecl:
		panic("TODO: infer enum declaration")
	default:
		panic(fmt.Sprintf("Unknown declaration type: %T", decl))
	}
}

// TODO: refactor this to return the binding map instead of copying them over
// immediately
func (c *Checker) inferVarDecl(
	ctx Context,
	decl *ast.VarDecl,
) (map[string]*type_system.Binding, []Error) {
	errors := []Error{}

	patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
	errors = slices.Concat(errors, patErrors)

	if decl.TypeAnn == nil && decl.Init == nil {
		return nil, errors
	}

	// TODO: infer a structural placeholder based on the expression and then
	// unify it with the pattern type.  Then we can pass in map of the new bindings
	// which will be added to a new scope before inferring function expressions
	// in the expressions.

	if decl.TypeAnn != nil {
		taType, taErrors := c.inferTypeAnn(ctx, decl.TypeAnn)
		errors = slices.Concat(errors, taErrors)

		unifyErrors := c.Unify(ctx, taType, patType)
		errors = slices.Concat(errors, unifyErrors)

		if decl.Init != nil {
			initType, initErrors := c.inferExpr(ctx, decl.Init)
			errors = slices.Concat(errors, initErrors)

			unifyErrors = c.Unify(ctx, initType, taType)
			errors = slices.Concat(errors, unifyErrors)
		}
	} else {
		if decl.Init == nil {
			// TODO: report an error, but set initType to be `unknown`
			panic("Expected either a type annotation or an initializer expression")
		}
		initType, initErrors := c.inferExpr(ctx, decl.Init)
		errors = slices.Concat(errors, initErrors)

		unifyErrors := c.Unify(ctx, initType, patType)
		errors = slices.Concat(errors, unifyErrors)
	}

	return bindings, errors
}

func (c *Checker) inferFuncDecl(ctx Context, decl *ast.FuncDecl) []Error {
	errors := []Error{}

	funcType, _, paramBindings, sigErrors := c.inferFuncSig(ctx, &decl.FuncSig, decl)
	errors = slices.Concat(errors, sigErrors)

	// For declared functions, we don't have a body to infer from
	if decl.Declare() && (decl.Body == nil || len(decl.Body.Stmts) == 0) {
		// For declared async functions, validate that the return type is a Promise
		if decl.FuncSig.Async {
			if promiseType, ok := funcType.Return.(*type_system.TypeRefType); ok &&
				type_system.QualIdentToString(promiseType.Name) == "Promise" {
				// Good, it's a Promise type. Ensure it has the right structure.
				if len(promiseType.TypeArgs) == 1 {
					// Promise<T> should become Promise<T, never>
					promiseAlias := ctx.Scope.GetTypeAlias("Promise")
					if promiseAlias != nil {
						// Update the function type to have Promise<T, never>
						newPromiseType := type_system.NewTypeRefType(
							nil, "Promise", promiseAlias, promiseType.TypeArgs[0], type_system.NewNeverType(nil))
						funcType.Return = newPromiseType
					}
				} else if len(promiseType.TypeArgs) >= 2 {
					// Promise<T, E> is already correct
				} else {
					// Promise with no args, this shouldn't happen but let's handle it
					errors = append(errors, &UnimplementedError{
						message: "Promise type must have at least one type argument",
						span:    decl.Span(),
					})
				}
			} else {
				// Declared async function must return a Promise type
				errors = append(errors, &UnimplementedError{
					message: "Declared async functions must return a Promise type",
					span:    decl.Span(),
				})
			}
		}
	} else if decl.Body != nil {
		inferErrors := c.inferFuncBodyWithFuncSigType(
			ctx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)
	}

	binding := type_system.Binding{
		Source:  &ast.NodeProvenance{Node: decl},
		Type:    funcType,
		Mutable: false,
	}
	ctx.Scope.setValue(decl.Name.Name, &binding)
	return errors
}

func (c *Checker) inferTypeDecl(
	ctx Context,
	decl *ast.TypeDecl,
) (*type_system.TypeAlias, []Error) {
	errors := []Error{}

	// Sort type parameters topologically so dependencies come first
	sortedTypeParams := ast.SortTypeParamsTopologically(decl.TypeParams)

	typeParams := make([]*type_system.TypeParam, len(sortedTypeParams))

	// Create a context that accumulates type parameters as we process them
	// This allows later type parameters to reference earlier ones in their constraints
	paramCtx := ctx
	paramScope := ctx.Scope.WithNewScope()

	for i, typeParam := range sortedTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			var constraintErrors []Error
			// Use paramCtx which includes previously defined type parameters
			constraintType, constraintErrors = c.inferTypeAnn(paramCtx, typeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if typeParam.Default != nil {
			var defaultErrors []Error
			// Use paramCtx which includes previously defined type parameters
			defaultType, defaultErrors = c.inferTypeAnn(paramCtx, typeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
		typeParams[i] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}

		// Add this type parameter to the scope for subsequent parameters
		typeParamTypeRef := type_system.NewTypeRefType(nil, typeParam.Name, nil)
		typeParamAlias := &type_system.TypeAlias{
			Type:       typeParamTypeRef,
			TypeParams: []*type_system.TypeParam{},
		}
		paramScope.SetTypeAlias(typeParam.Name, typeParamAlias)

		// Update the context to include this type parameter
		paramCtx = Context{
			Scope:      paramScope,
			IsAsync:    ctx.IsAsync,
			IsPatMatch: ctx.IsPatMatch,
		}
	}

	// Use the context with all type parameters for inferring the type annotation
	typeCtx := paramCtx

	t, typeErrors := c.inferTypeAnn(typeCtx, decl.TypeAnn)
	errors = slices.Concat(errors, typeErrors)

	typeAlias := type_system.TypeAlias{
		Type:       t,
		TypeParams: typeParams,
	}

	return &typeAlias, errors
}

func (c *Checker) inferInterface(
	ctx Context,
	decl *ast.InterfaceDecl,
) (*type_system.TypeAlias, []Error) {
	errors := []Error{}

	// Sort type parameters topologically so dependencies come first
	sortedTypeParams := ast.SortTypeParamsTopologically(decl.TypeParams)

	typeParams := make([]*type_system.TypeParam, len(sortedTypeParams))

	// Create a context that accumulates type parameters as we process them
	// This allows later type parameters to reference earlier ones in their constraints
	typeCtx := ctx.WithNewScope()

	typeArgs := make([]type_system.Type, len(sortedTypeParams))
	for i, typeParam := range sortedTypeParams {
		typeArgs[i] = type_system.NewTypeRefType(nil, typeParam.Name, nil)
	}
	selfType := type_system.NewTypeRefType(nil, decl.Name.Name, nil, typeArgs...)
	selfTypeAlias := type_system.TypeAlias{Type: selfType, TypeParams: []*type_system.TypeParam{}}
	typeCtx.Scope.SetTypeAlias("Self", &selfTypeAlias)

	for i, typeParam := range sortedTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			var constraintErrors []Error
			// Use paramCtx which includes previously defined type parameters
			constraintType, constraintErrors = c.inferTypeAnn(typeCtx, typeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if typeParam.Default != nil {
			var defaultErrors []Error
			// Use paramCtx which includes previously defined type parameters
			defaultType, defaultErrors = c.inferTypeAnn(typeCtx, typeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
		typeParams[i] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}

		// Add this type parameter to the scope for subsequent parameters
		typeParamTypeRef := type_system.NewTypeRefType(nil, typeParam.Name, nil)
		typeParamAlias := &type_system.TypeAlias{
			Type:       typeParamTypeRef,
			TypeParams: []*type_system.TypeParam{},
		}
		typeCtx.Scope.SetTypeAlias(typeParam.Name, typeParamAlias)
	}

	objType, typeErrors := c.inferObjectTypeAnn(typeCtx, decl.TypeAnn)
	errors = slices.Concat(errors, typeErrors)

	// Infer the Extends clause if present
	if decl.Extends != nil {
		extendsTypes := make([]*type_system.TypeRefType, len(decl.Extends))
		for i, extends := range decl.Extends {
			extendsType, extendsErrors := c.inferTypeAnn(typeCtx, extends)
			errors = slices.Concat(errors, extendsErrors)

			if extendsType == nil {
				continue
			}

			// The extends type should be a TypeRefType
			if typeRef, ok := extendsType.(*type_system.TypeRefType); ok {
				extendsTypes[i] = typeRef
			} else {
				// If it's not a TypeRefType, we still set it but wrap it if needed
				// This handles cases where the type might be pruned or indirect
				prunedType := type_system.Prune(extendsType)
				if typeRef, ok := prunedType.(*type_system.TypeRefType); ok {
					extendsTypes[i] = typeRef
				}
			}
		}
		objType.Extends = extendsTypes
	}

	objType.Interface = true
	objType.Nominal = true

	// Check if an interface with this name already exists
	existingAlias := ctx.Scope.GetTypeAlias(decl.Name.Name)
	if existingAlias != nil {
		// Validate that type parameters match
		validateErrors := c.validateTypeParams(
			ctx,
			existingAlias.TypeParams,
			typeParams,
			decl.Name.Name,
			decl.Name.Span(),
		)
		errors = slices.Concat(errors, validateErrors)

		// If it exists, merge the elements
		if existingObjType, ok := type_system.Prune(existingAlias.Type).(*type_system.ObjectType); ok &&
			existingObjType.Interface {
			// Validate that duplicate properties have compatible types
			mergeErrors := c.validateInterfaceMerge(ctx, existingObjType, objType, decl)
			errors = slices.Concat(errors, mergeErrors)

			// Merge the elements from the new interface into the existing one
			mergedElems := append(existingObjType.Elems, objType.Elems...)
			objType.Elems = mergedElems
			objType.ID = existingObjType.ID
			// Preserve other flags
			objType.Exact = existingObjType.Exact
			objType.Immutable = existingObjType.Immutable
			objType.Mutable = existingObjType.Mutable
			objType.Nominal = existingObjType.Nominal
			objType.Interface = true // Ensure interface flag is set
		}
	}

	typeAlias := type_system.TypeAlias{
		Type:       objType,
		TypeParams: typeParams,
	}

	return &typeAlias, errors
}

package checker

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferFuncParams(
	ctx Context,
	funcParams []*ast.Param,
) ([]*type_system.FuncParam, map[string]*type_system.Binding, []Error) {
	errors := []Error{}
	bindings := map[string]*type_system.Binding{}
	params := make([]*type_system.FuncParam, len(funcParams))

	for i, param := range funcParams {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)

		errors = slices.Concat(errors, patErrors)

		var typeAnn type_system.Type
		if param.TypeAnn == nil {
			freshVar := c.FreshVar(nil)
			// Only mark simple identifier parameters as IsParam so that
			// bind() keeps the type open. Destructuring patterns infer their
			// shape from the pattern structure itself, so they don't need the
			// open-type treatment.
			if _, isIdent := param.Pattern.(*ast.IdentPat); isIdent {
				freshVar.IsParam = true
			}
			typeAnn = freshVar
		} else {
			var typeAnnErrors []Error
			typeAnn, typeAnnErrors = c.inferTypeAnn(ctx, param.TypeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
		}

		// TODO: handle type annotations on parameters
		c.Unify(ctx, patType, typeAnn)

		maps.Copy(bindings, patBindings)

		params[i] = &type_system.FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: param.Optional,
		}
	}

	return params, bindings, errors
}

// inferFuncTypeParams infers type parameters for functions and function expressions.
// Unlike inferTypeParams, this version:
// - Uses inferTypeAnn instead of FreshVar for constraints and defaults
// - Sets provenance on constraint and default types
// - Adds the type parameters to the function context scope
// Returns the list of type parameters and any errors encountered.
func (c *Checker) inferFuncTypeParams(
	ctx Context,
	funcCtx Context,
	astTypeParams []*ast.TypeParam,
) ([]*type_system.TypeParam, []Error) {
	errors := []Error{}

	// Sort type parameters topologically so dependencies come first
	sortedTypeParams := ast.SortTypeParamsTopologically(astTypeParams)

	typeParams := make([]*type_system.TypeParam, len(sortedTypeParams))

	for i, tp := range sortedTypeParams {
		var defaultType type_system.Type
		var constraintType type_system.Type
		if tp.Default != nil {
			var defaultErrors []Error
			defaultType, defaultErrors = c.inferTypeAnn(funcCtx, tp.Default)
			defaultType.SetProvenance(&ast.NodeProvenance{Node: tp.Default})
			errors = slices.Concat(errors, defaultErrors)
		}
		if tp.Constraint != nil {
			var constraintErrors []Error
			constraintType, constraintErrors = c.inferTypeAnn(funcCtx, tp.Constraint)
			constraintType.SetProvenance(&ast.NodeProvenance{Node: tp.Constraint})
			errors = slices.Concat(errors, constraintErrors)
		}
		typeParam := &type_system.TypeParam{
			Name:       tp.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
		typeParams[i] = typeParam

		var t type_system.Type = type_system.NewUnknownType(nil)
		if typeParam.Constraint != nil {
			t = typeParam.Constraint
		}
		funcCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
			Type:       t,
			TypeParams: []*type_system.TypeParam{},
		})
	}

	return typeParams, errors
}

// NOTE: A new context should be created before calling this function in order
// to contain any type parameters in scope.
// Returns:
// - the inferred function type
// - the new context with type parameters in scope
// - a map of parameter bindings
// - any errors encountered during inference
// TODO: Accept an ast.Node parameter so that we can set provenance on the
// inferred type.
func (c *Checker) inferFuncSig(
	ctx Context,
	sig *ast.FuncSig, // TODO: make FuncSig an interface
	node ast.Node,
) (*type_system.FuncType, Context, map[string]*type_system.Binding, []Error) {
	errors := []Error{}

	// Create a new context with type parameters in scope
	funcCtx := ctx.WithNewScope()

	// Handle generic functions by creating type parameters
	typeParams, typeParamErrors := c.inferFuncTypeParams(ctx, funcCtx, sig.TypeParams)
	errors = slices.Concat(errors, typeParamErrors)

	params, bindings, paramErrors := c.inferFuncParams(funcCtx, sig.Params)
	errors = slices.Concat(errors, paramErrors)

	var returnType type_system.Type
	if sig.Return == nil {
		tvar := c.FreshVar(nil)
		tvar.FromBinding = true
		returnType = tvar
	} else {
		var returnErrors []Error
		returnType, returnErrors = c.inferTypeAnn(funcCtx, sig.Return)
		errors = slices.Concat(errors, returnErrors)
	}

	var throwsType type_system.Type
	if sig.Throws == nil {
		// No throws clause means the function doesn't throw.
		throwsType = type_system.NewNeverType(nil)
	} else {
		// throws _ infers from the body; throws T checks against T.
		var throwsErrors []Error
		throwsType, throwsErrors = c.inferTypeAnn(funcCtx, sig.Throws)
		errors = slices.Concat(errors, throwsErrors)
	}

	t := type_system.NewFuncType(
		&ast.NodeProvenance{Node: node},
		typeParams,
		params,
		returnType,
		throwsType,
	)

	return t, funcCtx, bindings, errors
}

// NOTE: This function updates `funcSigType`
func (c *Checker) inferFuncBodyWithFuncSigType(
	ctx Context,
	funcSigType *type_system.FuncType,
	paramBindings map[string]*type_system.Binding,
	body *ast.Block,
	isAsync bool,
) []Error {
	errors := []Error{}

	// Allocate fresh pointers for generator tracking — this function gets its own
	// tracking independent of any enclosing function
	containsYield := false
	yieldedTypes := []type_system.Type{}

	// Create context for the function body
	bodyCtx := ctx.WithNewScope()
	bodyCtx.InFuncBody = true
	bodyCtx.IsAsync = isAsync
	bodyCtx.ContainsYield = &containsYield
	bodyCtx.YieldedTypes = &yieldedTypes

	// Allocate fresh slice for collecting await throw types during inference
	if isAsync {
		awaitThrowTypes := []type_system.Type{}
		bodyCtx.AwaitThrowTypes = &awaitThrowTypes
	}

	returnType, inferredThrowType, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, body)
	errors = slices.Concat(errors, bodyErrors)

	// Check if this function is a generator (contains yield)
	if containsYield {
		var yieldType type_system.Type
		if len(yieldedTypes) == 1 {
			yieldType = yieldedTypes[0]
		} else if len(yieldedTypes) > 1 {
			yieldType = type_system.NewUnionType(nil, yieldedTypes...)
		} else {
			yieldType = type_system.NewNeverType(nil)
		}
		// TNext is always never for now — see GeneratorNextType comment in Context.
		nextType := type_system.NewNeverType(nil)

		var inferredGenType type_system.Type
		if isAsync {
			asyncGenAlias := ctx.Scope.GetTypeAlias("AsyncGenerator")
			inferredGenType = type_system.NewTypeRefType(nil, "AsyncGenerator", asyncGenAlias, yieldType, returnType, nextType)
		} else {
			genAlias := ctx.Scope.GetTypeAlias("Generator")
			inferredGenType = type_system.NewTypeRefType(nil, "Generator", genAlias, yieldType, returnType, nextType)
		}

		unifyErrors := c.Unify(ctx, inferredGenType, funcSigType.Return)
		errors = slices.Concat(errors, unifyErrors)

		// Only overwrite the return type when there's no explicit annotation
		// (i.e., it's a fresh type variable). When annotated, unification
		// already checked compatibility.
		if _, isTypeVar := funcSigType.Return.(*type_system.TypeVarType); isTypeVar {
			funcSigType.Return = inferredGenType
		}
		funcSigType.Throws = type_system.NewNeverType(nil)
		closeOpenParams(funcSigType)
		return errors
	}

	// For async functions, we construct a Promise<T, E> from the inferred
	// return and throws types, then unify with the function signature.
	if isAsync {
		// Create a Promise<T, E> type using the inferred components.
		promiseAlias := ctx.Scope.GetTypeAlias("Promise")
		if promiseAlias != nil {
			promiseRef := type_system.NewTypeRefType(nil, "Promise", promiseAlias, returnType, inferredThrowType)
			// Update the function signature's return type to this Promise.
			funcSigType.Return = promiseRef
			// Async functions do not throw directly; set throws to never.
			funcSigType.Throws = type_system.NewNeverType(nil)
		}
	} else {
		// For non-async functions, use the original logic
		unifyReturnErrors := c.Unify(ctx, returnType, funcSigType.Return)
		unifyThrowsErrors := c.Unify(ctx, inferredThrowType, funcSigType.Throws)
		errors = slices.Concat(errors, unifyReturnErrors, unifyThrowsErrors)
	}

	closeOpenParams(funcSigType)
	return errors
}

// Infer throws type - handles throws clause inference
// NOTE: This function updates `funcSigType`
func (c *Checker) inferFuncBody(
	ctx Context,
	bindings map[string]*type_system.Binding,
	body *ast.Block,
) (type_system.Type, type_system.Type, []Error) {

	ctx = ctx.WithNewScope()
	maps.Copy(ctx.Scope.Namespace.Values, bindings)

	errors := []Error{}
	for _, stmt := range body.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	returnVisitor := &ReturnVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Returns:        []*ast.ReturnStmt{},
	}

	for _, stmt := range body.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(returnVisitor)
	}

	returnTypes := []type_system.Type{}
	for _, returnStmt := range returnVisitor.Returns {
		if returnStmt.Expr != nil {
			returnType := returnStmt.Expr.InferredType()
			returnTypes = append(returnTypes, returnType)
		}
	}

	throwTypes, throwErrors := c.findThrowTypes(ctx, body)
	errors = slices.Concat(errors, throwErrors)

	// TODO: We also need to do dead code analysis to account for unreachable
	// code.

	var returnType type_system.Type
	if len(returnTypes) == 1 {
		returnType = returnTypes[0]
	} else if len(returnTypes) > 1 {
		returnType = type_system.NewUnionType(nil, returnTypes...)
	} else {
		returnType = type_system.NewVoidType(nil)
	}

	throwType := type_system.NewUnionType(nil, throwTypes...)

	return returnType, throwType, errors
}

type ReturnVisitor struct {
	ast.DefaultVisitor
	Returns []*ast.ReturnStmt
}

func (v *ReturnVisitor) EnterStmt(stmt ast.Stmt) bool {
	if returnStmt, ok := stmt.(*ast.ReturnStmt); ok {
		v.Returns = append(v.Returns, returnStmt)
	}

	return true
}
func (v *ReturnVisitor) EnterExpr(expr ast.Expr) bool {
	// Don't visit function expressions since we don't want to include any
	// return statements inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// return statements inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	// An expression like if/else could have a return statement inside one of
	// its branches.
	return true
}

type ThrowVisitor struct {
	ast.DefaultVisitor
	Throws []*ast.ThrowExpr
}

func (v *ThrowVisitor) EnterExpr(expr ast.Expr) bool {
	if throwExpr, ok := expr.(*ast.ThrowExpr); ok {
		v.Throws = append(v.Throws, throwExpr)
	}

	// Don't visit function expressions since we don't want to include any
	// throw expressions inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}

	// Don't visit try-catch expressions with a catch clause since throws
	// inside them are caught locally.
	if tryCatchExpr, ok := expr.(*ast.TryCatchExpr); ok {
		if tryCatchExpr.Catch != nil {
			return false
		}
	}

	return true
}

func (v *ThrowVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// throw expressions inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

func (v *ThrowVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	// An expression like if/else could have a throw expression inside one of
	// its branches.
	return true
}


func (c *Checker) findThrowTypes(ctx Context, block *ast.Block) ([]type_system.Type, []Error) {
	errors := []Error{}

	throwVisitor := &ThrowVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Throws:         []*ast.ThrowExpr{},
	}

	for _, stmt := range block.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(throwVisitor)
	}

	throwTypes := []type_system.Type{}
	for _, throwExpr := range throwVisitor.Throws {
		argType := throwExpr.Arg.InferredType()
		if argType != nil {
			throwTypes = append(throwTypes, argType)
		}
	}

	// Collect throw types from await expressions (collected during inference)
	if ctx.AwaitThrowTypes != nil {
		throwTypes = append(throwTypes, *ctx.AwaitThrowTypes...)
	}

	return throwTypes, errors
}

// closeOpenParams closes all open object types on function parameters after
// body inference is complete. It removes RestSpreadElems whose row variables
// don't appear in the return type and resolves mutability.
func closeOpenParams(funcSigType *type_system.FuncType) {
	// Collect unresolved type vars in the return type to determine which
	// row variables escape. We intentionally do NOT collect from
	// funcSigType.Throws: GeneralizeFuncType defaults throws-only type vars
	// to `never` (not generalized), so row variables that only appear in
	// throws should be removed, not preserved as type parameters. Preserving
	// them would create throws-polymorphic signatures (e.g.
	// fn <R>(obj: {x: number, ...R}) throws {x: number, ...R} -> void)
	// which adds noise for minimal benefit — thrown values are almost never
	// pass-through data whose extra properties callers need to recover.
	returnVars := map[int]*type_system.TypeVarType{}
	returnOrder := []int{}
	collectUnresolvedTypeVars(funcSigType.Return, returnVars, &returnOrder)

	for _, param := range funcSigType.Params {
		closeOpenObjectsInType(param.Type, returnVars)
	}
}

// closeOpenObjectsInType walks a type tree and closes any open ObjectTypes
// found within it. When the input type is a TypeVarType whose pruned value
// is an open ObjectType (possibly wrapped in MutabilityType), it finalizes
// mutability and closes the object. Otherwise, it recurses into type
// constructors (TypeRefType args, TupleType elements, UnionType options, etc.)
// to find and close nested open objects.
func closeOpenObjectsInType(t type_system.Type, returnVars map[int]*type_system.TypeVarType) {
	pruned := type_system.Prune(t)

	// If t is a TypeVar resolving to an open object, finalize and close it.
	// Setting tv.Instance is the standard union-find mechanism: Prune()
	// follows Instance chains, so all references that resolve through this
	// TypeVar (including the return type, if unified) see the updated value.
	// This is the same pattern used by GeneralizeFuncType in generalize.go.
	if tv, ok := t.(*type_system.TypeVarType); ok {
		unwrapped := pruned
		if mut, ok := unwrapped.(*type_system.MutabilityType); ok {
			unwrapped = mut.Type
		}
		if objType, ok := unwrapped.(*type_system.ObjectType); ok && objType.Open {
			if finalizeOpenObject(objType) {
				tv.Instance = &type_system.MutabilityType{
					Type:       objType,
					Mutability: type_system.MutabilityMutable,
				}
			} else {
				tv.Instance = objType
			}
			closeObjectType(objType, returnVars)
			return
		}
	}

	// Recurse into type constructors to find nested open objects.
	switch p := pruned.(type) {
	case *type_system.MutabilityType:
		closeOpenObjectsInType(p.Type, returnVars)
	case *type_system.ObjectType:
		for _, elem := range p.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				closeOpenObjectsInType(prop.Value, returnVars)
			}
		}
	case *type_system.TypeRefType:
		for _, arg := range p.TypeArgs {
			closeOpenObjectsInType(arg, returnVars)
		}
	case *type_system.TupleType:
		for _, elem := range p.Elems {
			closeOpenObjectsInType(elem, returnVars)
		}
	case *type_system.UnionType:
		for _, opt := range p.Types {
			closeOpenObjectsInType(opt, returnVars)
		}
	case *type_system.IntersectionType:
		for _, opt := range p.Types {
			closeOpenObjectsInType(opt, returnVars)
		}
	case *type_system.FuncType:
		for _, param := range p.Params {
			closeOpenObjectsInType(param.Type, returnVars)
		}
		closeOpenObjectsInType(p.Return, returnVars)
		closeOpenObjectsInType(p.Throws, returnVars)
	}
}

// closeObjectType closes an open ObjectType, recursively closes nested open
// objects found in property values, and removes RestSpreadElems whose row
// variables don't appear in returnVars.
func closeObjectType(objType *type_system.ObjectType, returnVars map[int]*type_system.TypeVarType) {
	objType.Open = false

	// Recurse into property values to close any nested open objects
	for _, elem := range objType.Elems {
		if prop, ok := elem.(*type_system.PropertyElem); ok {
			closeOpenObjectsInType(prop.Value, returnVars)
		}
	}

	// Remove RestSpreadElems whose row vars don't appear in return type
	filtered := make([]type_system.ObjTypeElem, 0, len(objType.Elems))
	for _, elem := range objType.Elems {
		if rest, ok := elem.(*type_system.RestSpreadElem); ok {
			if rowVar, ok := type_system.Prune(rest.Value).(*type_system.TypeVarType); ok {
				if _, found := returnVars[rowVar.ID]; !found {
					continue // remove this RestSpreadElem
				}
			}
		}
		filtered = append(filtered, elem)
	}
	objType.Elems = filtered
}

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
			typeAnn = c.FreshVar(nil)
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
	sortedTypeParams := sortTypeParamsTopologically(astTypeParams)

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
		// If no throws clause is specified, we use a fresh type variable which
		// will be unified later if any throw expressions are found in the
		// function body.
		tvar := c.FreshVar(nil)
		tvar.FromBinding = true
		throwsType = tvar
	} else {
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

	// Create async context if this is an async function
	bodyCtx := ctx.WithNewScope()
	bodyCtx.IsAsync = isAsync

	returnType, inferredThrowType, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, body)
	errors = slices.Concat(errors, bodyErrors)

	// For async functions, we need to handle Promise return types differently
	// For async functions, we construct a Promise<T, E> from the inferred
	// return and throws types, then unify with the function signature.
	if isAsync {
		// Create a Promise<T, E> type using the inferred components.
		promiseAlias := ctx.Scope.getTypeAlias("Promise")
		if promiseAlias != nil {
			promiseRef := type_system.NewTypeRefType(nil, "Promise", promiseAlias, returnType, inferredThrowType)
			// Update the function signature's return type to this Promise.
			funcSigType.Return = promiseRef
			// Async functions do not throw directly; set throws to never.
			funcSigType.Throws = type_system.NewNeverType(nil)
		}
		// Now unify the (possibly updated) return type with itself – no additional work needed.
	} else {
		// For non-async functions, use the original logic
		unifyReturnErrors := c.Unify(ctx, returnType, funcSigType.Return)
		unifyThrowsErrors := c.Unify(ctx, inferredThrowType, funcSigType.Throws)
		errors = slices.Concat(errors, unifyReturnErrors, unifyThrowsErrors)
	}

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

	visitor := &ReturnVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Returns:        []*ast.ReturnStmt{},
	}

	throwVisitor := &ThrowVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Throws:         []*ast.ThrowExpr{},
	}

	awaitVisitor := &AwaitVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Awaits:         []*ast.AwaitExpr{},
	}

	for _, stmt := range body.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(visitor)
		stmt.Accept(throwVisitor)
		stmt.Accept(awaitVisitor)
	}

	returnTypes := []type_system.Type{}
	for _, returnStmt := range visitor.Returns {
		if returnStmt.Expr != nil {
			returnType := returnStmt.Expr.InferredType()
			returnTypes = append(returnTypes, returnType)
		}
	}

	throwTypes := []type_system.Type{}
	for _, throwExpr := range throwVisitor.Throws {
		throwType, throwErrors := c.inferExpr(ctx, throwExpr.Arg)
		throwTypes = append(throwTypes, throwType)
		errors = slices.Concat(errors, throwErrors)
	}

	// Collect throw types from await expressions (Promise rejection types)
	for _, awaitExpr := range awaitVisitor.Awaits {
		if awaitExpr.Throws != nil {
			throwTypes = append(throwTypes, awaitExpr.Throws)
		}
	}

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

type AwaitVisitor struct {
	ast.DefaultVisitor
	Awaits []*ast.AwaitExpr
}

func (v *AwaitVisitor) EnterExpr(expr ast.Expr) bool {
	if awaitExpr, ok := expr.(*ast.AwaitExpr); ok {
		v.Awaits = append(v.Awaits, awaitExpr)
	}

	// Don't visit function expressions since we don't want to include any
	// await expressions inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *AwaitVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// await expressions inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

func (v *AwaitVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	return true
}

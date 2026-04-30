package checker

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// nestedMutInParamErrors walks a function parameter's pattern and reports
// any non-top-level `mut` flag (IdentPat.Mutable inside a destructure, or
// ObjShorthandPat.Mutable). These forms let the function body mutate the
// caller's value through a leaf alias while the parameter's printed type
// does not reflect the mutation — see TestPatternLevelMut_NoLeakIntoParentContainer
// for the structural side, and the matching positive test
// FuncParamMutCanMutateField for the legitimate top-level form.
//
// The top-level IdentPat.Mutable case is the sanctioned form (`fn f(mut p: T)`),
// so it is not visited here.
func nestedMutInParamErrors(pat ast.Pat) []Error {
	var errs []Error
	var walk func(p ast.Pat, atTopLevel bool)
	walk = func(p ast.Pat, atTopLevel bool) {
		switch p := p.(type) {
		case *ast.IdentPat:
			if p.Mutable && !atTopLevel {
				errs = append(errs, NestedMutInParamError{span: p.Span()})
			}
		case *ast.ObjectPat:
			for _, elem := range p.Elems {
				switch e := elem.(type) {
				case *ast.ObjShorthandPat:
					if e.Mutable {
						errs = append(errs, NestedMutInParamError{span: e.Span()})
					}
				case *ast.ObjKeyValuePat:
					walk(e.Value, false)
				case *ast.ObjRestPat:
					walk(e.Pattern, false)
				}
			}
		case *ast.TuplePat:
			for _, elem := range p.Elems {
				walk(elem, false)
			}
		case *ast.RestPat:
			walk(p.Pattern, false)
		}
	}
	walk(pat, true)
	return errs
}

func (c *Checker) inferFuncParams(
	ctx Context,
	funcParams []*ast.Param,
) ([]*type_system.FuncParam, map[string]*type_system.Binding, []Error) {
	errors := []Error{}
	bindings := map[string]*type_system.Binding{}
	params := make([]*type_system.FuncParam, len(funcParams))

	for i, param := range funcParams {
		errors = slices.Concat(errors, nestedMutInParamErrors(param.Pattern))

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

		// `fn f(mut p: Point)` — wrap the param's stored type in MutType
		// so the receiver-mutability filter and transition checker see
		// `p` as a mutable place. The pattern's binding already carries
		// the wrap from inferPattern; here we mirror it onto the
		// FuncParam.Type that flows into the generalized signature.
		//
		// Only the top-level IdentPat case is wrapped. Nested `mut` on a
		// destructured leaf is rejected up-front by nestedMutInParamErrors
		// above — the printed parameter type would not reflect the
		// mutation, breaking the call-site contract.
		if identPat, ok := param.Pattern.(*ast.IdentPat); ok && identPat.Mutable {
			if _, alreadyMut := typeAnn.(*type_system.MutType); !alreadyMut {
				typeAnn = type_system.NewMutType(
					&ast.NodeProvenance{Node: param.Pattern}, typeAnn)
			}
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
			Type:        t,
			TypeParams:  []*type_system.TypeParam{},
			IsTypeParam: true,
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
	astParams []*ast.Param,
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

	returnType, inferredThrowType, divergent, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, astParams, body)
	errors = slices.Concat(errors, bodyErrors)

	// If the body diverges (every reachable path exits via `throw` /
	// other exit form, with no `return` statements at all) and the
	// user wrote a non-`never` return annotation, that annotation is
	// misleading: callers see the type but the function will never
	// produce a value of it. Require `-> never` instead. This check is
	// limited to ordinary (non-generator, non-async) functions —
	// async/generator wrappers wrap the return in a Promise/Generator
	// where `never` semantics differ.
	if divergent && !containsYield && !isAsync {
		annotated := type_system.Prune(funcSigType.Return)
		if _, isTypeVar := annotated.(*type_system.TypeVarType); !isTypeVar {
			if _, isNever := annotated.(*type_system.NeverType); !isNever {
				errors = append(errors, DivergingBodyNonNeverReturnError{
					DeclaredReturn: annotated.String(),
					span:           body.Span,
				})
			}
		}
	}

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
		c.closeOpenParams(funcSigType)

		// Phase 8.3: infer lifetimes for generator yields. Yields aliasing
		// parameters propagate the lifetime to T inside Generator<T, ...>.
		c.InferLifetimes(astParams, body, funcSigType, isAsync)

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

	c.closeOpenParams(funcSigType)

	// Infer lifetime parameters from the body. This must run after returnType
	// has been unified into funcSigType.Return so that the lifetime is attached
	// to the same type the caller will see.
	c.InferLifetimes(astParams, body, funcSigType, isAsync)

	return errors
}

// Infer throws type - handles throws clause inference
// NOTE: This function updates `funcSigType`. Returns:
//   - returnType: the inferred fall-through type of the body (may be
//     `never` when the body always exits via `throw` / etc.).
//   - throwType: the union of `throw`-arg types and call-throws.
//   - divergent: true iff the body has zero top-level `return`
//     statements AND every reachable path exits via `throw` (or
//     another diverging form). Callers use this to report
//     `DivergingBodyNonNeverReturnError` when the user declared a
//     non-`never` return type.
func (c *Checker) inferFuncBody(
	ctx Context,
	bindings map[string]*type_system.Binding,
	astParams []*ast.Param,
	body *ast.Block,
) (type_system.Type, type_system.Type, bool, []Error) {

	ctx = ctx.WithNewScope()
	maps.Copy(ctx.Scope.Namespace.Values, bindings)

	// Liveness pre-pass: resolve names, build CFG, compute liveness,
	// and initialize alias tracker for mutability transition checking.
	c.runLivenessPrePass(&ctx, astParams, bindings, body)

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
	divergent := false
	if len(returnTypes) == 1 {
		returnType = returnTypes[0]
	} else if len(returnTypes) > 1 {
		returnType = type_system.NewUnionType(nil, returnTypes...)
	} else if blockAlwaysExits(body) {
		// Body has no `return` and every reachable path exits via
		// `throw` (or another diverging form). The function never
		// falls off the end normally, so the fall-through type is
		// `never`, not `void`. This lets a body like `{ throw "x" }`
		// satisfy a declared `-> number` return without spuriously
		// reporting `void cannot be assigned to number`. The caller
		// uses `divergent` to optionally reject misleading non-never
		// return annotations.
		returnType = type_system.NewNeverType(nil)
		divergent = true
	} else {
		returnType = type_system.NewVoidType(nil)
	}

	throwType := type_system.NewUnionType(nil, throwTypes...)

	return returnType, throwType, divergent, errors
}

// blockAlwaysExits reports whether every reachable path through `block`
// exits via `throw` (or another diverging form like `return`) rather
// than falling off the end. Used to decide whether a function body's
// fall-through type should be `never` (unreachable) instead of `void`.
//
// This is a syntactic, conservative reachability check — it doesn't
// reason about loop conditions, only about statement-level control
// flow. False negatives are fine (we just default to `void`); false
// positives would be unsound.
func blockAlwaysExits(block *ast.Block) bool {
	if block == nil || len(block.Stmts) == 0 {
		return false
	}
	return stmtAlwaysExits(block.Stmts[len(block.Stmts)-1])
}

func stmtAlwaysExits(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		return exprAlwaysExits(s.Expr)
	default:
		return false
	}
}

func exprAlwaysExits(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.ThrowExpr:
		return true
	case *ast.IfElseExpr:
		// Without an `else`, fall-through is reachable when the
		// condition is false.
		if e.Alt == nil {
			return false
		}
		return blockAlwaysExits(&e.Cons) && blockOrExprAlwaysExits(*e.Alt)
	case *ast.MatchExpr:
		// A match exits unconditionally only if every arm does. (We
		// don't check exhaustiveness here — a non-exhaustive match
		// would already be an error elsewhere; treating it as
		// possibly falling through is the safe default.)
		if len(e.Cases) == 0 {
			return false
		}
		for _, arm := range e.Cases {
			if !blockOrExprAlwaysExits(arm.Body) {
				return false
			}
		}
		return true
	case *ast.DoExpr:
		return blockAlwaysExits(&e.Body)
	default:
		return false
	}
}

func blockOrExprAlwaysExits(b ast.BlockOrExpr) bool {
	if b.Block != nil {
		return blockAlwaysExits(b.Block)
	}
	if b.Expr != nil {
		return exprAlwaysExits(b.Expr)
	}
	return false
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
	// Calls collects every reachable call expression so the enclosing
	// function inherits each callee's declared `throws` type (call
	// throws propagate to the caller exactly the same way `throw expr`
	// statements do — both bubble up to the nearest catch).
	Calls []*ast.CallExpr
}

func (v *ThrowVisitor) EnterExpr(expr ast.Expr) bool {
	if throwExpr, ok := expr.(*ast.ThrowExpr); ok {
		v.Throws = append(v.Throws, throwExpr)
	}
	if callExpr, ok := expr.(*ast.CallExpr); ok {
		v.Calls = append(v.Calls, callExpr)
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
		Calls:          []*ast.CallExpr{},
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

	// Collect throws declared by each reachable callee. A `throws T`
	// clause on the callee surfaces in the caller exactly like a
	// `throw …` statement: both propagate to the nearest catch (or to
	// the caller's signature when uncaught). Try/catch scoping is
	// already handled by the visitor's EnterExpr filter above.
	for _, callExpr := range throwVisitor.Calls {
		if t := calleeThrowsType(callExpr); t != nil {
			throwTypes = append(throwTypes, t)
		}
	}

	// Collect throw types from await expressions (collected during inference)
	if ctx.AwaitThrowTypes != nil {
		throwTypes = append(throwTypes, *ctx.AwaitThrowTypes...)
	}

	return throwTypes, errors
}

// calleeThrowsType returns the `Throws` type declared on the callee of
// `callExpr`, walking through the type-system shapes a callee can take:
//
//   - FuncType: the throws clause is right there.
//   - ObjectType: a class instance / constructor object — the throws
//     lives on the embedded ConstructorElem or CallableElem.
//   - TypeRefType: alias to one of the above; resolve via the
//     attached `TypeAlias`.
//   - IntersectionType (overload set): we can't pick a specific
//     overload here without re-running resolution, so we return the
//     union of every arm's throws as a sound upper bound. A caller
//     might dispatch to any arm, so callers must be prepared to handle
//     any arm's throws.
//
// Returns nil when no throws can be attributed (callee has no
// throws clause, or its throws type is `never`).
func calleeThrowsType(callExpr *ast.CallExpr) type_system.Type {
	if callExpr.Callee == nil {
		return nil
	}
	calleeType := callExpr.Callee.InferredType()
	if calleeType == nil {
		return nil
	}
	fns := collectCalleeFuncTypes(type_system.Prune(calleeType))
	throws := []type_system.Type{}
	for _, fn := range fns {
		if fn.Throws == nil {
			continue
		}
		if _, isNever := type_system.Prune(fn.Throws).(*type_system.NeverType); isNever {
			continue
		}
		throws = append(throws, fn.Throws)
	}
	switch len(throws) {
	case 0:
		return nil
	case 1:
		return throws[0]
	default:
		return type_system.NewUnionType(nil, throws...)
	}
}

// collectCalleeFuncTypes returns every `*FuncType` arm a call expression
// can be invoked against. For a single-signature callee this is a slice
// of one; for an intersection of overloads it returns one entry per
// arm. Returns an empty slice when the type isn't directly callable
// (e.g. an unresolved type variable, or non-callable object type).
func collectCalleeFuncTypes(t type_system.Type) []*type_system.FuncType {
	switch tt := t.(type) {
	case *type_system.FuncType:
		return []*type_system.FuncType{tt}
	case *type_system.ObjectType:
		var out []*type_system.FuncType
		for _, elem := range tt.Elems {
			// An ObjectType may carry a ConstructorElem (for `new`) and
			// one or more CallableElems (for plain calls). We collect
			// every callable shape so overload sets attached to object
			// types behave like IntersectionType arms.
			switch e := elem.(type) {
			case *type_system.ConstructorElem:
				out = append(out, e.Fn)
			case *type_system.CallableElem:
				out = append(out, e.Fn)
			}
		}
		return out
	case *type_system.TypeRefType:
		if tt.TypeAlias == nil {
			return nil
		}
		return collectCalleeFuncTypes(type_system.Prune(tt.TypeAlias.Type))
	case *type_system.IntersectionType:
		var out []*type_system.FuncType
		for _, arm := range tt.Types {
			out = append(out, collectCalleeFuncTypes(type_system.Prune(arm))...)
		}
		return out
	default:
		return nil
	}
}

// closeOpenParams closes all open object types on function parameters after
// body inference is complete. It removes RestSpreadElems whose row variables
// don't appear in the return type, resolves mutability, and resolves
// ArrayConstraints to concrete tuple or array types.
func (c *Checker) closeOpenParams(funcSigType *type_system.FuncType) {
	// Resolve ArrayConstraints first so that inferred tuple types (including
	// rest type variables) are visible when we collect return type vars.
	// Without this ordering, rest type variables created during resolution
	// wouldn't appear in returnVars and would always be removed.
	for _, param := range funcSigType.Params {
		c.resolveArrayConstraintsInType(param.Type)
	}

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

// resolveArrayConstraintsInType walks a type tree and resolves any
// ArrayConstraints on TypeVarTypes to concrete tuple or array types.
//
// We only need to check for ArrayConstraint on the incoming TypeVar (before
// pruning), not on its representative. ArrayConstraints are always set on
// the Prune-representative of a TypeVar because getMemberType prunes before
// reaching the TypeVarType case where getOrCreateArrayConstraint is called.
// The representative either IS the incoming TypeVar (the common case, and
// the pre-prune check catches it), or the incoming TypeVar's representative
// is a different param TypeVar that will be processed in its own iteration
// of closeOpenParams's loop over all params.
func (c *Checker) resolveArrayConstraintsInType(t type_system.Type) {
	// Check for ArrayConstraint before pruning: a param TypeVar with an
	// ArrayConstraint may have had its Instance set during return-type
	// unification (e.g., `return items` unifies the param TypeVar with the
	// return TypeVar, setting Instance on one of them). In that case,
	// Prune() follows Instance and misses the constraint.
	if tv, ok := t.(*type_system.TypeVarType); ok && tv.ArrayConstraint != nil {
		resolved := c.resolveArrayConstraint(tv.ArrayConstraint)
		tv.ArrayConstraint = nil
		// Bind the resolved type to the representative of this TypeVar's
		// equivalence class. If Instance is set (param was unified with the
		// return TypeVar), the representative is the other end of the chain;
		// otherwise the param TypeVar is its own representative.
		rep := type_system.Prune(tv)
		if repTV, ok := rep.(*type_system.TypeVarType); ok {
			repTV.Instance = resolved
		} else {
			tv.Instance = resolved
		}
		// Recurse into the resolved type to handle nested ArrayConstraints
		// (e.g. items[0][1] where the element itself is used as a tuple/array).
		c.resolveArrayConstraintsInType(resolved)
		return
	}

	t = type_system.Prune(t)

	// Recurse into type constructors that can appear in inferred parameter types.
	// We only need to cover shapes produced by inference on unannotated params:
	// - IntersectionType, FuncType.Throws, and ObjectType methods/getters/setters
	//   are omitted because ArrayConstraints are only created on unannotated
	//   parameter TypeVars during body inference, and those types come from
	//   annotations or type definitions, not from the inference path.
	// - ObjectType only checks PropertyElem because open objects created during
	//   inference (via newOpenObjectWithProperty) only contain PropertyElems.
	switch p := t.(type) {
	case *type_system.TypeRefType:
		for _, arg := range p.TypeArgs {
			c.resolveArrayConstraintsInType(arg)
		}
	case *type_system.TupleType:
		for _, elem := range p.Elems {
			c.resolveArrayConstraintsInType(elem)
		}
	case *type_system.UnionType:
		for _, opt := range p.Types {
			c.resolveArrayConstraintsInType(opt)
		}
	case *type_system.FuncType:
		for _, param := range p.Params {
			c.resolveArrayConstraintsInType(param.Type)
		}
		c.resolveArrayConstraintsInType(p.Return)
	case *type_system.ObjectType:
		for _, elem := range p.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				c.resolveArrayConstraintsInType(prop.Value)
			}
		}
	case *type_system.MutType:
		c.resolveArrayConstraintsInType(p.Type)
	}
}

// resolveArrayConstraint resolves an ArrayConstraint to a concrete type.
// Mutating methods (.push, .pop, etc.) or non-literal indexes force Array<T>.
// Index assignment (items[0] = v) forces mutability but keeps tuple shape.
func (c *Checker) resolveArrayConstraint(constraint *type_system.ArrayConstraint) type_system.Type {
	ctx := Context{
		Scope:      c.GlobalScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Unify all per-method-call element type vars with ElemTypeVar.
	// Each freshElem was bound to the argument type during handleFuncCall;
	// unifying them all with ElemTypeVar accumulates a union.
	for _, freshElem := range constraint.MethodElemVars {
		c.Unify(ctx, freshElem, constraint.ElemTypeVar)
	}

	// Mutating methods, non-literal indexes, or read-only methods without any
	// literal indexes force resolution to Array<T>. Read-only methods like
	// .map() operate on the whole collection, so without positional information
	// from literal indexes a tuple would be meaningless.
	forceArray := constraint.HasMutatingMethod || constraint.HasNonLiteralIndex ||
		(constraint.HasReadOnlyMethod && len(constraint.LiteralIndexes) == 0)
	if forceArray {
		// Unify all literal index type vars with ElemTypeVar
		for _, elemTV := range constraint.LiteralIndexes {
			c.Unify(ctx, elemTV, constraint.ElemTypeVar)
		}
		arrayAlias := c.GlobalScope.Namespace.Types["Array"]
		arrayType := type_system.NewTypeRefType(nil, "Array", arrayAlias, constraint.ElemTypeVar)
		if constraint.HasMutatingMethod || constraint.HasIndexAssignment {
			return &type_system.MutType{
				Type: arrayType,
			}
		}
		return arrayType
	}

	// Resolve to tuple
	isMut := constraint.HasIndexAssignment
	if len(constraint.LiteralIndexes) == 0 {
		restTV := c.FreshVar(nil)
		tupleType := type_system.NewTupleType(nil, type_system.NewRestSpreadType(nil, restTV))
		if isMut {
			return &type_system.MutType{
				Type: tupleType,
			}
		}
		return tupleType
	}
	maxIndex := 0
	for idx := range constraint.LiteralIndexes {
		if idx > maxIndex {
			maxIndex = idx
		}
	}
	elems := make([]type_system.Type, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		if tv, ok := constraint.LiteralIndexes[i]; ok {
			elems[i] = tv
		} else {
			elems[i] = c.FreshVar(nil) // gap — unresolved type variable
		}
	}
	// Append a rest type variable to capture extra caller-supplied elements.
	// This is the tuple analogue of the row variable added to open objects.
	// The rest variable will be removed during closing if it doesn't appear
	// in the function's return type.
	restTV := c.FreshVar(nil)
	elems = append(elems, type_system.NewRestSpreadType(nil, restTV))
	tupleType := type_system.NewTupleType(nil, elems...)
	if isMut {
		return &type_system.MutType{
			Type: tupleType,
		}
	}
	return tupleType
}

// closeOpenObjectsInType walks a type tree and closes any open ObjectTypes
// found within it. When the input type is a TypeVarType whose pruned value
// is an open ObjectType (possibly wrapped in MutType), it finalizes
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
		if mut, ok := unwrapped.(*type_system.MutType); ok {
			unwrapped = mut.Type
		}
		if objType, ok := unwrapped.(*type_system.ObjectType); ok && objType.Open {
			if finalizeOpenObject(objType) {
				tv.Instance = &type_system.MutType{
					Type: objType,
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
	case *type_system.MutType:
		closeOpenObjectsInType(p.Type, returnVars)
	case *type_system.ObjectType:
		if p.Open {
			closeObjectType(p, returnVars)
		} else {
			for _, elem := range p.Elems {
				if prop, ok := elem.(*type_system.PropertyElem); ok {
					closeOpenObjectsInType(prop.Value, returnVars)
				}
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
		closeTupleType(p, returnVars)
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

// closeTupleType removes a trailing RestSpreadType whose type variable doesn't
// appear in returnVars. This is the tuple analogue of closeObjectType removing
// RestSpreadElems for objects. If the rest variable appears in the
// return type, it is kept and GeneralizeFuncType will promote it to a type
// parameter.
func closeTupleType(tupleType *type_system.TupleType, returnVars map[int]*type_system.TypeVarType) {
	if len(tupleType.Elems) == 0 {
		return
	}
	last := tupleType.Elems[len(tupleType.Elems)-1]
	if rest, ok := last.(*type_system.RestSpreadType); ok {
		if tv, ok := type_system.Prune(rest.Type).(*type_system.TypeVarType); ok {
			// Preserve rest variables from explicit destructuring patterns.
			// For example, in `fn foo([first, ...rest]) { return first }`,
			// the rest type variable should be kept even though it doesn't
			// appear in the return type — the user explicitly wrote `...rest`
			// to accept variadic arguments. Pattern-originated rest bindings
			// have FromBinding=true (set by inferPattern for IdentPat),
			// while inferred rest variables from resolveArrayConstraint do
			// not.
			if tv.FromBinding {
				return
			}
			if _, found := returnVars[tv.ID]; !found {
				// Rest var not in return type — remove it
				tupleType.Elems = tupleType.Elems[:len(tupleType.Elems)-1]
			}
		}
	}
}

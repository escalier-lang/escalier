package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// InferLifetimes analyzes a function body to determine which parameters
// are aliased by the return value, attaches a fresh LifetimeVar to each
// such parameter and to the return type, and records the lifetime
// parameters on the FuncType.
//
// This is the foundational case of Phase 8.3. The algorithm only handles:
//   - Returns of a parameter (or property/index access of a parameter):
//     produces a container-level lifetime on the parameter and return type.
//   - Multiple returns aliasing different parameters: emits a
//     LifetimeUnion on the return type.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Element-level lifetimes (Array<'a T>) for fresh containers whose
//     elements alias a parameter.
//   - Generator yields (yield treated as fresh).
//   - Escaping references to module-level state ('static).
//   - Propagating lifetimes through nested calls into other functions.
//
// astParams is the list of parameter ASTs (used to map their VarIDs to
// parameter indices). funcType is mutated in place.
func (c *Checker) InferLifetimes(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
) {
	if body == nil || funcType == nil {
		return
	}
	// If the user already declared explicit lifetime parameters on the
	// signature, don't second-guess them. (Resolution of those annotated
	// lifetimes during type-checking is a separate concern handled by
	// the type-annotation inference path.)
	if len(funcType.LifetimeParams) > 0 {
		return
	}

	// Build VarID → param index map for the simple identifier-pattern case.
	// Destructuring-pattern params don't get lifetimes in this pass.
	paramIndex := make(map[liveness.VarID]int)
	for i, p := range astParams {
		if identPat, ok := p.Pattern.(*ast.IdentPat); ok && identPat.VarID > 0 {
			paramIndex[liveness.VarID(identPat.VarID)] = i
		}
	}
	if len(paramIndex) == 0 {
		return
	}

	// Walk the body to collect every return statement's expression.
	v := &returnExprVisitor{}
	for _, stmt := range body.Stmts {
		stmt.Accept(v)
	}
	if len(v.exprs) == 0 {
		return
	}

	// Determine which parameters are aliased across all return expressions.
	// Use insertion order of parameter indices so that the resulting
	// LifetimeParams list is deterministic.
	seen := make(map[int]bool)
	var orderedParams []int
	for _, expr := range v.exprs {
		src := liveness.DetermineAliasSource(expr)
		switch src.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, vid := range src.VarIDs {
				idx, ok := paramIndex[vid]
				if !ok {
					continue
				}
				if !seen[idx] {
					seen[idx] = true
					orderedParams = append(orderedParams, idx)
				}
			}
		}
	}

	if len(orderedParams) == 0 {
		return
	}

	// Skip lifetime inference entirely when the return type cannot carry
	// a lifetime (e.g. it's a TypeVar, primitive, or union/intersection
	// without a common lifetime-bearing structure). Without a place to
	// attach the lifetime on the return side, the lifetime parameter
	// would be unused noise in the signature.
	if !typeCarriesLifetime(funcType.Return) {
		return
	}

	// Allocate one fresh LifetimeVar per aliased parameter whose param
	// type can also carry a lifetime — same reasoning as above for the
	// param side.
	lifetimeParams := make([]*type_system.LifetimeVar, 0, len(orderedParams))
	for _, idx := range orderedParams {
		if idx >= len(funcType.Params) {
			continue
		}
		paramType := funcType.Params[idx].Type
		if !typeCarriesLifetime(paramType) {
			continue
		}
		// paramIndex (above) was populated only from IdentPat params, so
		// astParams[idx].Pattern is guaranteed to be *ast.IdentPat.
		identPat := astParams[idx].Pattern.(*ast.IdentPat)
		lv := c.FreshLifetimeVar(identPat.Name)
		lifetimeParams = append(lifetimeParams, lv)

		// Attach the lifetime to the parameter's type.
		setLifetimeOnType(paramType, lv)
	}

	if len(lifetimeParams) == 0 {
		return
	}

	// Construct the lifetime to attach to the return type. Only include
	// parameters that actually got lifetime variables (i.e. param types
	// that could carry one).
	var returnLifetime type_system.Lifetime
	if len(lifetimeParams) == 1 {
		returnLifetime = lifetimeParams[0]
	} else {
		members := make([]type_system.Lifetime, len(lifetimeParams))
		for i, lv := range lifetimeParams {
			members[i] = lv
		}
		returnLifetime = &type_system.LifetimeUnion{Lifetimes: members}
	}
	setLifetimeOnType(funcType.Return, returnLifetime)

	funcType.LifetimeParams = lifetimeParams
}

// setLifetimeOnType attaches a lifetime to a type's Lifetime field, walking
// past wrapper types (mutability) to reach the underlying reference type.
// Has no effect on types that don't carry a lifetime field (primitives,
// void, never, etc.).
func setLifetimeOnType(t type_system.Type, lt type_system.Lifetime) {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		ty.Lifetime = lt
	case *type_system.ObjectType:
		ty.Lifetime = lt
	case *type_system.TupleType:
		ty.Lifetime = lt
	case *type_system.MutabilityType:
		setLifetimeOnType(ty.Type, lt)
	}
}

// InferConstructorLifetimes analyzes a class declaration to determine
// which constructor parameters are stored as fields, attaches a fresh
// LifetimeVar to each such parameter and to the constructor's return
// type, and records the lifetime parameters and default mutability on
// the class TypeAlias.
//
// Detected storage patterns (Phase 8.6 foundational subset):
//   - Shorthand field referring to a constructor parameter by name:
//     `class C(p: mut Point) { p, }`
//   - Explicit field whose value is an IdentExpr to a constructor
//     parameter: `class C(p: mut Point) { p: p, }`
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Storage through nested expressions (`x: f(p)`).
//   - Storage into nested object/array literals.
//   - Lifetime inference for explicit field-initializer expressions
//     that depend on parameters in non-identity ways.
func (c *Checker) InferConstructorLifetimes(
	classDecl *ast.ClassDecl,
	typeAlias *type_system.TypeAlias,
	ctorFn *type_system.FuncType,
) {
	if classDecl == nil || ctorFn == nil || typeAlias == nil {
		return
	}

	// Default mutability per Phase 8.6 algorithm step 5:
	//   - immutable modifier  → immutable
	//   - any mut self method → mutable
	//   - else                → immutable
	mutable := false
	if !classDecl.Immutable {
		for _, elem := range classDecl.Body {
			if methodElem, ok := elem.(*ast.MethodElem); ok {
				if methodElem.MutSelf != nil && *methodElem.MutSelf {
					mutable = true
					break
				}
			}
		}
	}
	typeAlias.DefaultMutableSet = true
	typeAlias.DefaultMutable = mutable

	// Honor explicit lifetime params if the user already wrote them.
	if len(typeAlias.LifetimeParams) > 0 {
		return
	}

	// For each constructor param that is a reference type AND is stored
	// as a field, allocate a fresh LifetimeVar.
	paramNameToIndex := make(map[string]int)
	for i, p := range classDecl.Params {
		if identPat, ok := p.Pattern.(*ast.IdentPat); ok {
			paramNameToIndex[identPat.Name] = i
		}
	}

	storedParams := make(map[int]bool)
	for _, elem := range classDecl.Body {
		fieldElem, ok := elem.(*ast.FieldElem)
		if !ok {
			continue
		}
		// Field name must be a plain identifier for our patterns.
		fieldNameIdent, ok := fieldElem.Name.(*ast.IdentExpr)
		if !ok {
			continue
		}

		// Shorthand: name only, no value, no default.
		if fieldElem.Value == nil && fieldElem.Default == nil {
			if idx, ok := paramNameToIndex[fieldNameIdent.Name]; ok {
				storedParams[idx] = true
			}
			continue
		}

		// Explicit: `name: <ident>` where ident is a constructor param.
		if valIdent, ok := fieldElem.Value.(*ast.IdentExpr); ok {
			if idx, ok := paramNameToIndex[valIdent.Name]; ok {
				storedParams[idx] = true
			}
		}
	}

	if len(storedParams) == 0 {
		return
	}

	// Allocate lifetimes in parameter order for determinism.
	var lifetimeParams []*type_system.LifetimeVar
	for i, p := range classDecl.Params {
		if !storedParams[i] {
			continue
		}
		// Skip params whose declared type is not a reference type — the
		// lifetime would have nowhere to attach.
		if i >= len(ctorFn.Params) {
			continue
		}
		paramType := ctorFn.Params[i].Type
		if !typeCarriesLifetime(paramType) {
			continue
		}

		// storedParams (above) was populated only from params whose name
		// is in paramNameToIndex, which only contains IdentPat params, so
		// p.Pattern is guaranteed to be *ast.IdentPat.
		identPat := p.Pattern.(*ast.IdentPat)
		lv := c.FreshLifetimeVar(identPat.Name)
		lifetimeParams = append(lifetimeParams, lv)
		setLifetimeOnType(paramType, lv)
	}

	if len(lifetimeParams) == 0 {
		return
	}

	typeAlias.LifetimeParams = lifetimeParams
	ctorFn.LifetimeParams = lifetimeParams

	// Attach LifetimeArgs to the constructor's return type so callers can
	// see which lifetime corresponds to which parameter.
	lifetimeArgs := make([]type_system.Lifetime, len(lifetimeParams))
	for i, lv := range lifetimeParams {
		lifetimeArgs[i] = lv
	}
	setLifetimeArgsOnType(ctorFn.Return, lifetimeArgs)
}

// typeCarriesLifetime reports whether the given type can carry a lifetime.
// Walks past mutability wrappers. Type parameters (TypeRefType pointing
// at a TypeAlias with IsTypeParam=true) are excluded, since the parameter
// might be instantiated to a primitive at the call site, in which case
// the lifetime would have nowhere to live.
func typeCarriesLifetime(t type_system.Type) bool {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		if ty.TypeAlias != nil && ty.TypeAlias.IsTypeParam {
			return false
		}
		return true
	case *type_system.ObjectType, *type_system.TupleType:
		_ = ty
		return true
	case *type_system.MutabilityType:
		return typeCarriesLifetime(ty.Type)
	}
	return false
}

// setLifetimeArgsOnType attaches a list of lifetime arguments to a
// TypeRefType (e.g. Container<'a, 'b>), walking past mutability wrappers.
func setLifetimeArgsOnType(t type_system.Type, args []type_system.Lifetime) {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		ty.LifetimeArgs = args
	case *type_system.MutabilityType:
		setLifetimeArgsOnType(ty.Type, args)
	}
}

// determineCheckerAliasSource is a checker-aware wrapper around
// liveness.DetermineAliasSource that handles call expressions whose callee
// has lifetime parameters: the result is added to the alias set of each
// argument whose corresponding parameter shares a lifetime with the
// return type.
//
// For non-call expressions and calls whose callee has no lifetime
// information, this falls through to liveness.DetermineAliasSource.
func determineCheckerAliasSource(expr ast.Expr) liveness.AliasSource {
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return liveness.DetermineAliasSource(expr)
	}

	calleeType := callExpr.Callee.InferredType()
	fnType := extractFuncType(calleeType)
	if fnType == nil || len(fnType.LifetimeParams) == 0 {
		return liveness.DetermineAliasSource(expr)
	}

	retLifetime := type_system.PruneLifetime(type_system.GetLifetime(fnType.Return))
	if retLifetime == nil {
		return liveness.DetermineAliasSource(expr)
	}

	// Build the set of LifetimeVar IDs that the return type carries.
	// PruneLifetime already followed the Instance pointers, so members are
	// either resolved LifetimeValues or unresolved LifetimeVars; we only
	// need the LifetimeVar IDs because that's what the parameter types
	// will (also) carry pre-resolution.
	retVarIDs := lifetimeVarIDs(retLifetime)
	if len(retVarIDs) == 0 {
		return liveness.DetermineAliasSource(expr)
	}

	// For each parameter whose lifetime is in retVarIDs, propagate the
	// argument's alias source into the result's alias source.
	var aggregated []liveness.VarID
	seen := make(map[liveness.VarID]bool)
	for i, p := range fnType.Params {
		if i >= len(callExpr.Args) {
			break
		}
		paramLifetime := type_system.PruneLifetime(type_system.GetLifetime(p.Type))
		paramVar, ok := paramLifetime.(*type_system.LifetimeVar)
		if !ok {
			continue
		}
		if !retVarIDs[paramVar.ID] {
			continue
		}
		argSource := determineCheckerAliasSource(callExpr.Args[i])
		switch argSource.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, id := range argSource.VarIDs {
				if !seen[id] {
					seen[id] = true
					aggregated = append(aggregated, id)
				}
			}
		}
	}

	switch len(aggregated) {
	case 0:
		// None of the matching arguments could be tracked back to a local
		// variable — fall back to the default treatment.
		return liveness.DetermineAliasSource(expr)
	case 1:
		return liveness.AliasSource{Kind: liveness.AliasSourceVariable, VarIDs: aggregated}
	default:
		return liveness.AliasSource{Kind: liveness.AliasSourceMultiple, VarIDs: aggregated}
	}
}

// extractFuncType reaches into the (possibly wrapped) callee type to find
// the underlying FuncType. Mirrors the dispatch in inferCallExpr.
func extractFuncType(t type_system.Type) *type_system.FuncType {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.FuncType:
		return ty
	case *type_system.TypeRefType:
		if ty.TypeAlias != nil {
			if obj, ok := type_system.Prune(ty.TypeAlias.Type).(*type_system.ObjectType); ok {
				return funcFromObjectType(obj)
			}
		}
	case *type_system.ObjectType:
		return funcFromObjectType(ty)
	}
	return nil
}

func funcFromObjectType(obj *type_system.ObjectType) *type_system.FuncType {
	for _, elem := range obj.Elems {
		switch e := elem.(type) {
		case *type_system.ConstructorElem:
			return e.Fn
		case *type_system.CallableElem:
			return e.Fn
		}
	}
	return nil
}

// lifetimeVarIDs returns the set of LifetimeVar IDs referenced by the
// given (pruned) lifetime. Returns a non-nil empty map when the lifetime
// is a resolved LifetimeValue with no associated variables.
func lifetimeVarIDs(lt type_system.Lifetime) map[int]bool {
	ids := make(map[int]bool)
	switch v := lt.(type) {
	case *type_system.LifetimeVar:
		ids[v.ID] = true
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			for id := range lifetimeVarIDs(type_system.PruneLifetime(m)) {
				ids[id] = true
			}
		}
	}
	return ids
}

// returnExprVisitor walks a function body collecting return-statement
// expressions. It does not descend into nested functions (their returns
// belong to the inner scope).
type returnExprVisitor struct {
	ast.DefaultVisitor
	exprs []ast.Expr
}

func (v *returnExprVisitor) EnterStmt(stmt ast.Stmt) bool {
	if r, ok := stmt.(*ast.ReturnStmt); ok && r.Expr != nil {
		v.exprs = append(v.exprs, r.Expr)
	}
	return true
}

func (v *returnExprVisitor) EnterExpr(expr ast.Expr) bool {
	// Skip nested function bodies — their returns are not ours.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *returnExprVisitor) EnterDecl(decl ast.Decl) bool {
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

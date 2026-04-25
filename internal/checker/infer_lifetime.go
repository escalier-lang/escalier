package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// lifetimeParamName returns the inferred lifetime parameter name for the
// i-th allocated lifetime in a signature: 'a, 'b, ..., 'z, 'aa, 'ab, etc.
// Using letters (rather than the underlying param name) makes printed
// signatures easier to read since lifetimes are visually distinct from
// the params they constrain.
func lifetimeParamName(i int) string {
	const base = 26
	name := ""
	for {
		name = string(rune('a'+(i%base))) + name
		i = i/base - 1
		if i < 0 {
			break
		}
	}
	return name
}

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
		switch pat := p.Pattern.(type) {
		case *ast.IdentPat:
			if pat.VarID > 0 {
				paramIndex[liveness.VarID(pat.VarID)] = i
			}
		case *ast.RestPat:
			// Rest params are skipped here: the lifetime-bearing position
			// for `...args: T[]` is the *element* type, not the container
			// variable. Attaching a container-level lifetime to `args`
			// would be incorrect (the array is freshly assembled per
			// call), and element-level lifetimes are deferred — see the
			// file-level docstring's "Element-level lifetimes" bullet.
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
	seen := set.NewSet[int]()
	var orderedParams []int
	for _, re := range v.exprs {
		src := liveness.DetermineAliasSource(re)
		switch src.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, vid := range src.VarIDs {
				idx, ok := paramIndex[vid]
				if !ok {
					continue
				}
				if !seen.Contains(idx) {
					seen.Add(idx)
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
		_ = astParams[idx].Pattern.(*ast.IdentPat)
		lv := c.FreshLifetimeVar(lifetimeParamName(len(lifetimeParams)))
		lifetimeParams = append(lifetimeParams, lv)

		// Attach the lifetime to the parameter's type.
		setLifetimeOnType(paramType, lv)
	}

	if len(lifetimeParams) == 0 {
		return
	}

	// Construct the lifetime to attach to the return type. Only include
	// parameters that actually got lifetime variables (i.e. param types
	// that could carry one). By construction of orderedParams above,
	// every entry in lifetimeParams is guaranteed to be a possible source
	// for the return value on at least one code path — DetermineAliasSource
	// in the body walk only added a param to orderedParams if some return
	// expression aliased it. The union therefore expresses "the result
	// could have come from any of these params, depending on which branch
	// ran"; the caller treats the result as bounded by the shortest of
	// the listed lifetimes.
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
// which constructor parameters are stored as fields (or implicitly
// captured by method bodies), attaches a fresh LifetimeVar to each such
// parameter and to the constructor's return type, and records the
// lifetime parameters and default mutability on the class TypeAlias.
//
// Detected storage / capture patterns (Phase 8.6):
//   - Shorthand field referring to a constructor parameter by name,
//     with or without a default:
//     `class C(p: mut Point) { p, }`
//     `class C(p: mut Point) { p = fallback, }`
//   - Explicit field whose value references a constructor parameter,
//     directly or through composite/projection expressions:
//     `class C(p: mut Point) { x: p, }`
//     `class C(p: mut Point) { x: p.inner, }`
//     `class C(p: mut Point) { x: {inner: p}, }`
//     `class C(p: mut Point) { x: [p, p], }`
//   - Implicit capture: a method body references a constructor parameter
//     by name (Escalier allows methods to see constructor params without
//     going through `self`):
//     `class C(p: mut Point) { foo(self) { return p } }`
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Inference of method-side return-type lifetimes when a method
//     captures a constructor param (the constructor gets the lifetime,
//     but the method's return type does not yet inherit it).
//   - Storage through function-call results (`x: f(p)`) — calls are
//     conservatively treated as fresh by the field walker.
func (c *Checker) InferConstructorLifetimes(
	classDecl *ast.ClassDecl,
	typeAlias *type_system.TypeAlias,
	ctorFn *type_system.FuncType,
) {
	if classDecl == nil || ctorFn == nil || typeAlias == nil {
		return
	}

	// Default mutability per Phase 8.6 algorithm step 5:
	//   - data modifier       → immutable (regardless of methods)
	//   - any mut self method → mutable
	//   - else                → immutable
	mutable := false
	if !classDecl.Data {
		for _, elem := range classDecl.Body {
			if methodElem, ok := elem.(*ast.MethodElem); ok {
				if methodElem.MutSelf != nil && *methodElem.MutSelf {
					mutable = true
					break
				}
			}
		}
	}
	typeAlias.DefaultMutable = &mutable

	// Honor explicit lifetime params if the user already wrote them.
	if len(typeAlias.LifetimeParams) > 0 {
		return
	}

	// For each constructor param that is a reference type AND is stored
	// as a field (or captured by a method), allocate a fresh LifetimeVar.
	//
	// Note: matching is by *name* rather than VarID because
	// InferConstructorLifetimes runs during the namespace placeholder phase,
	// before the rename pass has populated VarIDs on identifiers in field
	// initializers or method bodies.
	paramNameToIndex := make(map[string]int)
	for i, p := range classDecl.Params {
		if identPat, ok := p.Pattern.(*ast.IdentPat); ok {
			paramNameToIndex[identPat.Name] = i
		}
	}

	storedParams := set.NewSet[int]()

	for _, elem := range classDecl.Body {
		switch elem := elem.(type) {
		case *ast.FieldElem:
			collectFieldStorageParams(elem, paramNameToIndex, storedParams)
		case *ast.MethodElem:
			// Static methods can't access instance state implicitly.
			if elem.Static || elem.Fn == nil || elem.Fn.Body == nil {
				continue
			}
			collectMethodBodyCaptures(elem.Fn, paramNameToIndex, storedParams)
		}
	}

	if storedParams.Len() == 0 {
		return
	}

	// Allocate lifetimes in parameter order for determinism.
	var lifetimeParams []*type_system.LifetimeVar
	for i, p := range classDecl.Params {
		if !storedParams.Contains(i) {
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
		_ = p.Pattern.(*ast.IdentPat)
		lv := c.FreshLifetimeVar(lifetimeParamName(len(lifetimeParams)))
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

// collectFieldStorageParams inspects a single class field-element and adds
// the indices of any constructor parameters whose value is captured by the
// field's initializer to storedParams.
//
// The cases handled here mirror the storage shapes documented on
// InferConstructorLifetimes: shorthand fields, identity initializers, and
// composite or projection initializers (object/tuple literals, member or
// index access into a param). Function-call initializers are not analyzed —
// liveness.DetermineAliasSource treats calls as fresh, and a checker-aware
// alternative would need lifetime info from callees that may not yet be
// resolved at this point in inference.
func collectFieldStorageParams(
	fieldElem *ast.FieldElem,
	paramNameToIndex map[string]int,
	storedParams set.Set[int],
) {
	// Field name must be a plain identifier for the shorthand pattern.
	fieldNameIdent, ok := fieldElem.Name.(*ast.IdentExpr)

	// Shorthand: `{ p, }` or `{ p = fallback, }`. A default does not change
	// the conclusion: when the caller passes `p`, the field stores the
	// param, so the param's lifetime matters.
	if ok && fieldElem.Value == nil {
		if idx, ok := paramNameToIndex[fieldNameIdent.Name]; ok {
			storedParams.Add(idx)
		}
		return
	}

	if fieldElem.Value == nil {
		return
	}

	// Explicit `name: <expr>` — walk the initializer for any captured params.
	for _, idx := range findCapturedParamsInExpr(fieldElem.Value, paramNameToIndex) {
		storedParams.Add(idx)
	}
}

// findCapturedParamsInExpr walks a field-initializer expression looking
// for references to constructor parameters by name. Returns the parameter
// indices in first-seen order. Recurses into composite literals
// (object/tuple) and into spread elements; projection expressions
// (member/index access, type cast, await) fall through to
// liveness.DetermineAliasSource which already walks past those.
//
// Function calls and other complex expressions whose result might capture
// arguments are NOT analyzed — they are treated as fresh.
func findCapturedParamsInExpr(
	expr ast.Expr,
	paramNameToIndex map[string]int,
) []int {
	seen := set.NewSet[int]()
	var result []int

	addByName := func(name string) {
		if idx, ok := paramNameToIndex[name]; ok && !seen.Contains(idx) {
			seen.Add(idx)
			result = append(result, idx)
		}
	}

	var visit func(e ast.Expr)
	visit = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch ex := e.(type) {
		case *ast.ObjectExpr:
			for _, elem := range ex.Elems {
				switch el := elem.(type) {
				case *ast.PropertyExpr:
					if el.Value != nil {
						visit(el.Value)
						continue
					}
					// Object shorthand: `{ p }` — the property name doubles
					// as a value reference.
					if name, ok := el.Name.(*ast.IdentExpr); ok {
						addByName(name.Name)
					}
				case *ast.ObjSpreadExpr:
					visit(el.Value)
				}
			}
		case *ast.TupleExpr:
			for _, sub := range ex.Elems {
				visit(sub)
			}
		case *ast.ArraySpreadExpr:
			visit(ex.Value)
		case *ast.IdentExpr:
			addByName(ex.Name)
		case *ast.MemberExpr:
			visit(ex.Object)
		case *ast.IndexExpr:
			visit(ex.Object)
		case *ast.TypeCastExpr:
			visit(ex.Expr)
		case *ast.AwaitExpr:
			visit(ex.Arg)
		case *ast.IfElseExpr:
			// Conditional that yields a value: both branches may
			// contribute captures. Use the existing helper to find each
			// branch's result expression.
			cons := blockResultExpr(ex.Cons)
			if cons != nil {
				visit(cons)
			}
			if ex.Alt != nil {
				if ex.Alt.Expr != nil {
					visit(ex.Alt.Expr)
				} else if ex.Alt.Block != nil {
					if alt := blockResultExpr(*ex.Alt.Block); alt != nil {
						visit(alt)
					}
				}
			}
		}
	}
	visit(expr)
	return result
}

// blockResultExpr returns the result expression of a block (the last
// statement if it's an ExprStmt), or nil if the block is empty or ends
// with a non-expression statement. Mirrors the helper of the same name in
// the liveness package, duplicated here to avoid an import cycle.
func blockResultExpr(b ast.Block) ast.Expr {
	if len(b.Stmts) == 0 {
		return nil
	}
	if exprStmt, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok {
		return exprStmt.Expr
	}
	return nil
}

// collectMethodBodyCaptures walks a method body looking for IdentExpr
// references whose name matches a constructor parameter, and adds the
// matching parameter indices to storedParams. Tracks shadowing introduced
// by inner FuncExpr params so that names rebound by nested functions are
// not counted as captures.
//
// This closes the soundness gap where a method body references a
// constructor param by name (Escalier allows this) without any
// corresponding `self.field` projection — see the example in
// implementation_plan.md Phase 8.6 implicit-captures discussion.
func collectMethodBodyCaptures(
	fn *ast.FuncExpr,
	paramNameToIndex map[string]int,
	storedParams set.Set[int],
) {
	// Names shadowed in the current scope (and all enclosing scopes within
	// the method) are tracked as a stack: each entry is the set of names
	// bound by a single FuncExpr's parameters.
	v := &methodCaptureVisitor{
		paramNameToIndex: paramNameToIndex,
		storedParams:     storedParams,
	}
	// The method's own params shadow constructor params with the same name.
	v.pushScope(fn.Params)
	if fn.Body != nil {
		fn.Body.Accept(v)
	}
	v.popScope()
}

type methodCaptureVisitor struct {
	ast.DefaultVisitor
	paramNameToIndex map[string]int
	storedParams     set.Set[int]
	shadowed         []set.Set[string]
}

func (v *methodCaptureVisitor) pushScope(params []*ast.Param) {
	scope := set.NewSet[string]()
	for _, p := range params {
		collectPatternBindingNames(p.Pattern, scope)
	}
	v.shadowed = append(v.shadowed, scope)
}

func (v *methodCaptureVisitor) popScope() {
	v.shadowed = v.shadowed[:len(v.shadowed)-1]
}

func (v *methodCaptureVisitor) isShadowed(name string) bool {
	for _, s := range v.shadowed {
		if s.Contains(name) {
			return true
		}
	}
	return false
}

func (v *methodCaptureVisitor) EnterExpr(e ast.Expr) bool {
	switch ex := e.(type) {
	case *ast.IdentExpr:
		if v.isShadowed(ex.Name) {
			return true
		}
		if idx, ok := v.paramNameToIndex[ex.Name]; ok {
			v.storedParams.Add(idx)
		}
	case *ast.FuncExpr:
		// Nested function: its params introduce new shadows for the
		// duration of its body. Manually descend so we control scope.
		v.pushScope(ex.Params)
		if ex.Body != nil {
			ex.Body.Accept(v)
		}
		v.popScope()
		return false
	}
	return true
}

func (v *methodCaptureVisitor) EnterDecl(d ast.Decl) bool {
	switch dd := d.(type) {
	case *ast.VarDecl:
		// `let p = ...` (or `var p = ...`) shadows the constructor's `p`
		// from this point onward in the enclosing block. Record the
		// pattern's bound names in the current scope.
		if len(v.shadowed) > 0 {
			collectPatternBindingNames(dd.Pattern, v.shadowed[len(v.shadowed)-1])
		}
	case *ast.FuncDecl:
		// Nested function declaration: similar to FuncExpr, its params
		// shadow outer names within its body.
		if dd.Body == nil {
			return false
		}
		v.pushScope(dd.Params)
		dd.Body.Accept(v)
		v.popScope()
		return false
	case *ast.ClassDecl:
		// Skip nested classes — their constructor param names introduce
		// a fresh shadow scope that's outside the analysis we care about.
		return false
	}
	return true
}

// collectPatternBindingNames adds every identifier name introduced by a
// pattern (recursively) to the provided set.
func collectPatternBindingNames(p ast.Pat, into set.Set[string]) {
	if p == nil {
		return
	}
	switch pp := p.(type) {
	case *ast.IdentPat:
		into.Add(pp.Name)
	case *ast.TuplePat:
		for _, sub := range pp.Elems {
			collectPatternBindingNames(sub, into)
		}
	case *ast.ObjectPat:
		for _, elem := range pp.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				collectPatternBindingNames(e.Value, into)
			case *ast.ObjShorthandPat:
				if e.Key != nil {
					into.Add(e.Key.Name)
				}
			case *ast.ObjRestPat:
				collectPatternBindingNames(e.Pattern, into)
			}
		}
	case *ast.RestPat:
		collectPatternBindingNames(pp.Pattern, into)
	}
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
	seen := set.NewSet[liveness.VarID]()
	for i, p := range fnType.Params {
		if i >= len(callExpr.Args) {
			break
		}
		paramLifetime := type_system.PruneLifetime(type_system.GetLifetime(p.Type))
		// Symmetric with the return-side handling above: use lifetimeVarIDs
		// so we correctly recognize a param whose lifetime is a
		// LifetimeUnion containing one or more vars also referenced by the
		// return. Today inferred params only ever get a single LifetimeVar
		// (see setLifetimeOnType call sites in InferLifetimes /
		// InferConstructorLifetimes), and user-annotated lifetimes aren't
		// yet propagated into Type.Lifetime by inferTypeAnn — so this
		// branch is currently only hit when the helper happens to see a
		// single var. The overlap check is defensive: once Phase 9
		// unification produces union bindings on params, or the type-ann
		// pipeline starts honoring user-written ('a | 'b) annotations,
		// this code path becomes reachable and the symmetric treatment
		// avoids silently dropping union-param aliasing.
		paramVarIDs := lifetimeVarIDs(paramLifetime)
		overlap := false
		for id := range paramVarIDs {
			if retVarIDs.Contains(id) {
				overlap = true
				break
			}
		}
		if !overlap {
			continue
		}
		argSource := determineCheckerAliasSource(callExpr.Args[i])
		switch argSource.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, id := range argSource.VarIDs {
				if !seen.Contains(id) {
					seen.Add(id)
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
// given (pruned) lifetime. Returns a non-nil empty set when the lifetime
// is a resolved LifetimeValue with no associated variables.
func lifetimeVarIDs(lt type_system.Lifetime) set.Set[int] {
	ids := set.NewSet[int]()
	switch v := lt.(type) {
	case *type_system.LifetimeVar:
		ids.Add(v.ID)
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			for id := range lifetimeVarIDs(type_system.PruneLifetime(m)) {
				ids.Add(id)
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

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
// are aliased by the return value (or yielded value, for generators), or
// escape into module-level state, and attaches the appropriate lifetime
// to each such parameter and to the return / yield type, recording
// lifetime parameters on the FuncType.
//
// This is the foundational case of Phase 8.3 + 8.4. The algorithm
// handles:
//   - Returns of a parameter (or property/index access of a parameter):
//     produces a container-level lifetime on the parameter and return type.
//   - Multiple returns aliasing different parameters: emits a
//     LifetimeUnion on the return type.
//   - Parameters stored into module-level (non-local) state: assigned
//     'static directly. See DetectEscapingRefs.
//   - Yields aliasing parameters (generators): the lifetime is attached
//     to the yield type T inside Generator<T, TReturn, TNext> rather than
//     to the Generator container itself, so each yielded value carries
//     the lifetime.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Element-level lifetimes (Array<'a T>) for fresh containers whose
//     elements alias a parameter.
//   - `yield from` (delegate yield) propagation from the inner iterator's
//     element type.
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

	// Build an ordered list of param "leaves": for IdentPat params, the
	// leaf is the param itself; for tuple-destructured params (TuplePat),
	// each leaf IdentPat becomes its own leaf with a Type position
	// pointing at the corresponding sub-position of the param's type;
	// for RestPat (`...args: T[]`), the inner pattern's leaf points at
	// the *element* type Array<T>'s TypeArgs[0], not the array container
	// — the container is freshly assembled per call and has no
	// caller-provided lifetime.
	//
	// Object-destructured params are not yet walked (Phase 8.6 deferred).
	leaves := collectParamLeaves(astParams, funcType.Params)
	if len(leaves) == 0 {
		return
	}
	leafIndex := make(map[liveness.VarID]int)
	for i, l := range leaves {
		leafIndex[l.varID] = i
	}

	// Phase 8.4: detect parameters that escape into module-level state and
	// assign them 'static directly. This must run before the return-alias
	// pass so that an escaping param gets 'static rather than a fresh 'a
	// even when it is also returned by some path.
	escapingLeaves := detectEscapingLeafIndices(body, leafIndex)
	for idx := range escapingLeaves {
		leaf := leaves[idx]
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		setLifetimeOnType(leaf.leafType, &type_system.LifetimeValue{
			Name:     "static",
			IsStatic: true,
		})
	}

	// Determine the *result type* — the position to which the
	// per-source lifetime is attached. For generators, the result is
	// the yield type T inside Generator<T, TReturn, TNext>; for plain
	// functions, it's the return type itself.
	resultType := funcType.Return
	yieldTargetType := generatorYieldType(funcType.Return)
	if yieldTargetType != nil {
		resultType = yieldTargetType
	}

	// Walk the body to collect alias-source expressions: every return
	// statement's expression for plain functions, plus every yield
	// expression's value for generators (the yielded value is what
	// callers see via Iterator.next()).
	v := &returnExprVisitor{}
	for _, stmt := range body.Stmts {
		stmt.Accept(v)
	}
	yields := collectYieldExprs(body)
	if len(v.exprs) == 0 && len(yields) == 0 {
		return
	}

	// Determine which leaves are aliased across all alias-source
	// expressions. Use insertion order so the resulting LifetimeParams
	// list is deterministic.
	sourceExprs := make([]ast.Expr, 0, len(v.exprs)+len(yields))
	if yieldTargetType != nil {
		// Generator: yields are the lifetime-bearing source.
		// The function's `return` (if any) sets TReturn — that lifetime
		// position is not yet inferred (deferred).
		sourceExprs = append(sourceExprs, yields...)
	} else {
		sourceExprs = append(sourceExprs, v.exprs...)
	}
	seen := set.NewSet[int]()
	var orderedLeaves []int
	for _, re := range sourceExprs {
		// Use the checker-aware variant so call expressions whose callee
		// has inferred lifetime parameters propagate the relevant
		// argument's alias source through to the result. This is what
		// makes Phase 8.7's mutual-recursion fixed-point pass actually
		// converge: on the second pass, peers' lifetimes are visible
		// here.
		src := determineCheckerAliasSource(re)
		switch src.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, vid := range src.VarIDs {
				idx, ok := leafIndex[vid]
				if !ok {
					continue
				}
				if !seen.Contains(idx) {
					seen.Add(idx)
					orderedLeaves = append(orderedLeaves, idx)
				}
			}
		}
	}

	if len(orderedLeaves) == 0 {
		return
	}

	// Skip lifetime inference entirely when the result type cannot carry
	// a lifetime (e.g. it's a TypeVar, primitive, or union/intersection
	// without a common lifetime-bearing structure). Without a place to
	// attach the lifetime on the result side, the lifetime parameter
	// would be unused noise in the signature.
	if !typeCarriesLifetime(resultType) {
		return
	}

	// Allocate one fresh LifetimeVar per aliased leaf whose leaf type
	// can also carry a lifetime — same reasoning as above for the param
	// side. Escaping leaves are handled separately: their leaf type
	// already carries 'static, and the return type (if it aliases them)
	// inherits 'static too.
	lifetimeParams := make([]*type_system.LifetimeVar, 0, len(orderedLeaves))
	returnHasStatic := false
	var returnLifetimeMembers []type_system.Lifetime
	for _, idx := range orderedLeaves {
		leaf := leaves[idx]
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		if escapingLeaves.Contains(idx) {
			// Leaf already carries 'static. Record that the return
			// shares this 'static lifetime, but don't allocate a 'a for
			// it (since the leaf's lifetime is a concrete value, not a
			// variable).
			returnHasStatic = true
			continue
		}
		lv := c.FreshLifetimeVar(lifetimeParamName(len(lifetimeParams)))
		lifetimeParams = append(lifetimeParams, lv)
		returnLifetimeMembers = append(returnLifetimeMembers, lv)

		// Attach the lifetime to the leaf's type position.
		setLifetimeOnType(leaf.leafType, lv)
	}

	if len(returnLifetimeMembers) == 0 && !returnHasStatic {
		return
	}

	// Construct the lifetime to attach to the return type. The members
	// include any fresh LifetimeVars allocated above plus a 'static value
	// when the return aliases an escaping param. The union expresses
	// "the result could have come from any of these sources, depending on
	// which branch ran"; the caller treats the result as bounded by the
	// shortest of the listed lifetimes (and 'static is unbounded).
	if returnHasStatic {
		returnLifetimeMembers = append(returnLifetimeMembers, &type_system.LifetimeValue{
			Name:     "static",
			IsStatic: true,
		})
	}
	var returnLifetime type_system.Lifetime
	if len(returnLifetimeMembers) == 1 {
		returnLifetime = returnLifetimeMembers[0]
	} else {
		returnLifetime = &type_system.LifetimeUnion{Lifetimes: returnLifetimeMembers}
	}
	setLifetimeOnType(resultType, returnLifetime)

	if len(lifetimeParams) > 0 {
		funcType.LifetimeParams = lifetimeParams
	}
}

// paramLeaf represents a leaf binding within a function-parameter pattern,
// paired with the Type position to which a lifetime should be attached.
// For a simple identifier param `p: T`, leafType is the param's full type;
// for a tuple-destructured param `[a, b]: [T, U]`, each leaf points at the
// corresponding tuple element type; for a rest param `...args: T[]`, the
// leaf points at the *element* type T (the array container is freshly
// assembled per call and has no caller-provided lifetime).
type paramLeaf struct {
	varID    liveness.VarID
	leafType type_system.Type
}

// collectParamLeaves walks each function parameter's pattern in lockstep
// with the parameter's inferred type, producing an ordered list of
// (VarID, leafType) pairs for every leaf binding that has a positive
// VarID set by the rename pass.
//
// Object-destructured params are not yet walked — see Phase 8.6 deferred
// items in implementation_plan.md.
func collectParamLeaves(
	astParams []*ast.Param,
	funcParams []*type_system.FuncParam,
) []paramLeaf {
	var leaves []paramLeaf
	for i, p := range astParams {
		if i >= len(funcParams) {
			continue
		}
		walkPatternForLeaves(p.Pattern, funcParams[i].Type, &leaves)
	}
	return leaves
}

func walkPatternForLeaves(pat ast.Pat, t type_system.Type, into *[]paramLeaf) {
	if pat == nil || t == nil {
		return
	}
	pt := stripMutabilityWrapper(type_system.Prune(t))
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p.VarID > 0 {
			*into = append(*into, paramLeaf{
				varID:    liveness.VarID(p.VarID),
				leafType: t,
			})
		}
	case *ast.TuplePat:
		tt, ok := pt.(*type_system.TupleType)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if i >= len(tt.Elems) {
				break
			}
			walkPatternForLeaves(elem, tt.Elems[i], into)
		}
	case *ast.RestPat:
		// `...args: T[]` — the lifetime-bearing position is the
		// *element* type, not the array container. Descend into the
		// inner pattern with the element type.
		elem := arrayElemType(t)
		if elem == nil {
			return
		}
		walkPatternForLeaves(p.Pattern, elem, into)
	}
}

// stripMutabilityWrapper strips a MutabilityType wrapper (any kind:
// explicit `mut`, immutable, or uncertain `mut?`) so callers can match
// the underlying structural type. Returns the input unchanged if not
// wrapped. Distinct from the more selective `unwrapMutability` in
// unify.go which only strips uncertain wrappers — here we want the
// underlying structural type regardless of mutability annotation.
func stripMutabilityWrapper(t type_system.Type) type_system.Type {
	if mt, ok := t.(*type_system.MutabilityType); ok {
		return type_system.Prune(mt.Type)
	}
	return t
}

// arrayElemType returns the element type T of an Array<T> reference,
// walking past mutability wrappers. Returns nil if t is not an Array.
func arrayElemType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if type_system.QualIdentToString(tref.Name) != "Array" || len(tref.TypeArgs) != 1 {
		return nil
	}
	return tref.TypeArgs[0]
}

// DetectEscapingRefs walks a function body looking for assignments that
// store one of the function's parameters into a non-local location
// (module-level variable, prelude binding, or any other binding looked
// up through the function's enclosing scope chain). Returns the set of
// parameter indices whose value escapes.
//
// Detection is by VarID: the rename pass assigns positive VarIDs to
// locals and negative VarIDs to outer-scope references. An assignment
// whose lvalue root is a non-local identifier (VarID <= 0) is treated
// as a store into outer-lived state. Stores into locals don't escape
// because the local's lifetime is bounded by the function body.
//
// Limitations:
//   - Closures over a *nested* function's local: inner functions whose
//     body assigns to an outer function's local will mark that param as
//     'static, which is more conservative than necessary. The
//     borrow-checker tolerates this (it's sound, just imprecise).
//   - Stores via property assignment whose root is a local but is
//     itself stored elsewhere: not tracked here.
func DetectEscapingRefs(
	body *ast.Block,
	paramIndex map[liveness.VarID]int,
) set.Set[int] {
	return detectEscapingLeafIndices(body, paramIndex)
}

// detectEscapingLeafIndices is the leaf-aware version of
// DetectEscapingRefs: leafIndex maps a leaf binding's VarID to its
// position in the leaves list, and the returned set contains the
// indices of leaves whose value escapes.
func detectEscapingLeafIndices(
	body *ast.Block,
	leafIndex map[liveness.VarID]int,
) set.Set[int] {
	escaped := set.NewSet[int]()
	if body == nil || len(leafIndex) == 0 {
		return escaped
	}
	v := &escapingRefsVisitor{
		leafIndex: leafIndex,
		escaped:   escaped,
	}
	body.Accept(v)
	return escaped
}

type escapingRefsVisitor struct {
	ast.DefaultVisitor
	leafIndex map[liveness.VarID]int
	escaped   set.Set[int]
}

func (v *escapingRefsVisitor) EnterExpr(e ast.Expr) bool {
	if be, ok := e.(*ast.BinaryExpr); ok && be.Op == ast.Assign {
		if isNonLocalLValue(be.Left) {
			src := liveness.DetermineAliasSource(be.Right)
			switch src.Kind {
			case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
				for _, vid := range src.VarIDs {
					if idx, ok := v.leafIndex[vid]; ok {
						v.escaped.Add(idx)
					}
				}
			}
		}
	}
	// Don't descend into nested function bodies — their assignments
	// concern their own params, not ours.
	if _, ok := e.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *escapingRefsVisitor) EnterDecl(d ast.Decl) bool {
	switch d.(type) {
	case *ast.FuncDecl, *ast.ClassDecl:
		return false
	}
	return true
}

// isNonLocalLValue returns true if the given assignment-target expression's
// root identifier resolves to a non-local binding (VarID <= 0). Walks
// through MemberExpr/IndexExpr chains to find the root identifier.
func isNonLocalLValue(expr ast.Expr) bool {
	for {
		switch e := expr.(type) {
		case *ast.IdentExpr:
			return e.VarID <= 0
		case *ast.MemberExpr:
			expr = e.Object
		case *ast.IndexExpr:
			expr = e.Object
		default:
			return false
		}
	}
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
	if fnType == nil {
		return liveness.DetermineAliasSource(expr)
	}
	// Don't gate on fnType.LifetimeParams here: by the time the call is
	// type-checked, callee-side instantiation may have cleared
	// LifetimeParams while leaving the LifetimeVars themselves attached
	// to the param/return type positions. The presence of a lifetime on
	// the return type is the authoritative signal that this call carries
	// alias information.

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

// generatorYieldType returns the yield type T of a Generator<T, TReturn,
// TNext> or AsyncGenerator<T, TReturn, TNext> reference, walking past
// mutability wrappers. Returns nil if t is not a generator reference or
// has no type args.
func generatorYieldType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	name := type_system.QualIdentToString(tref.Name)
	if name != "Generator" && name != "AsyncGenerator" {
		return nil
	}
	if len(tref.TypeArgs) == 0 {
		return nil
	}
	return tref.TypeArgs[0]
}

// collectYieldExprs walks a function body collecting the value
// expressions of every (non-delegate) yield expression. Delegate yields
// (`yield from iter`) are skipped — propagating lifetimes from the
// inner iterator's element type is deferred. Bare `yield` (with no
// value) is also skipped because there is no value to alias.
func collectYieldExprs(body *ast.Block) []ast.Expr {
	v := &yieldExprVisitor{}
	for _, stmt := range body.Stmts {
		stmt.Accept(v)
	}
	return v.exprs
}

type yieldExprVisitor struct {
	ast.DefaultVisitor
	exprs []ast.Expr
}

func (v *yieldExprVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.YieldExpr:
		if e.Value != nil && !e.IsDelegate {
			v.exprs = append(v.exprs, e.Value)
		}
		return true
	case *ast.FuncExpr:
		// Skip nested function bodies — their yields belong to the
		// inner generator (if any), not ours.
		return false
	}
	return true
}

func (v *yieldExprVisitor) EnterDecl(decl ast.Decl) bool {
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

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
// This is the entry point for the body-side of Phase 8.3 + 8.4 + 8.7.
// The algorithm handles:
//   - Returns of a parameter (or property/index access of a parameter):
//     produces a container-level lifetime on the parameter and return type.
//   - Multiple returns aliasing different parameters: emits a
//     LifetimeUnion on the return type.
//   - Parameters stored into module-level (non-local) state: assigned
//     'static directly. See detectEscapingLeafIndices.
//   - Yields and returns aliasing parameters (generators): yields drive
//     the yield T slot inside Generator<T, TReturn, TNext>; explicit
//     `return expr` paths drive the TReturn slot. Each is inferred
//     independently, so a generator may carry distinct lifetimes on
//     T and TReturn. Lifetimes attach to the inner slots rather than
//     the Generator container, since the container is freshly assembled
//     per call. See "Generator-specific behavior" below.
//   - Per-leaf lifetimes for tuple-, object-, and rest-destructured
//     parameters: walkPatternForLeaves walks each parameter's pattern in
//     lockstep with its inferred type, producing one (VarID, leafType)
//     entry per leaf binding so that each destructured leaf can receive
//     its own lifetime variable.
//
// For SCC re-runs in mutual recursion (Phase 8.7), see ReinferLifetimes,
// which forwards to inferLifetimesCore with reinfer=true so that
// previously-inferred LifetimeParams can be extended once peers' signatures
// become resolvable on the second pass.
//
// Generator-specific behavior (Phase 8.3):
//   - Yields (regular and delegate) drive the yield T position;
//     `return expr` paths drive the TReturn position; the two are
//     inferred independently via `attachLifetimeToResult`.
//   - For `yield from iter`, the iterator expression is the alias
//     source — each yielded element borrows from iter, so iter's
//     lifetime propagates to the relay generator's yield T.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Element-level lifetimes (Array<'a T>) for fresh containers whose
//     elements alias a parameter.
//   - Propagating lifetimes through nested calls into other functions.
//   - TODO(#507): Shared-instance overspecification for unioned yield
//     types and `yield from`: when the result position shares its
//     underlying Type instance with a parameter, attaching a lifetime
//     to the result also writes through to the parameter, producing
//     tighter output than necessary. Fix would require shallow-cloning
//     the yield T / TReturn before lifetime attachment.
//
// astParams is the list of parameter ASTs (used to map their VarIDs to
// parameter indices). funcType is mutated in place. isAsync reports
// whether the function was declared `async` — together with the
// presence of `yield` in the body it determines whether the return
// type should be unwrapped to its inner T (Promise<T,_>'s value or
// Generator<T,_,_>'s yield) or treated as the direct result.
func (c *Checker) InferLifetimes(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	isAsync bool,
) {
	c.inferLifetimesCore(astParams, body, funcType, isAsync, false)
}

// ReinferLifetimes re-runs lifetime inference on a function whose first
// pass has already completed. Intended for the SCC re-run in
// InferComponent: peers in the same SCC may have gained inferred
// lifetimes after this function's first pass, in which case
// `determineCheckerAliasSource` can now reach through their call
// expressions and reveal additional aliased leaves. Unlike
// InferLifetimes, this entry point bypasses the user-explicit guard,
// so callers must ensure they don't invoke it on a function whose
// LifetimeParams were declared explicitly by the user.
func (c *Checker) ReinferLifetimes(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	isAsync bool,
) {
	c.inferLifetimesCore(astParams, body, funcType, isAsync, true)
}

// inferLifetimesCore is the shared body of InferLifetimes and
// ReinferLifetimes. The reinfer flag controls whether the function may
// extend an already-inferred set of lifetime params: when false, the
// function bails out as soon as it sees existing LifetimeParams (the
// historical behavior protecting user-explicit lifetimes); when true,
// it walks the leaves and *appends* new lifetime params for any leaves
// that became visible via newly-resolved peer signatures.
func (c *Checker) inferLifetimesCore(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	isAsync bool,
	reinfer bool,
) {
	if body == nil || funcType == nil {
		return
	}
	// If the user already declared explicit lifetime parameters on the
	// signature, don't second-guess them. (Resolution of those annotated
	// lifetimes during type-checking is a separate concern handled by
	// the type-annotation inference path.) The reinfer path skips this
	// guard so the SCC re-run can extend a previously-inferred result.
	if !reinfer && len(funcType.LifetimeParams) > 0 {
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
	// even when it is also returned by some path. Already-set 'static
	// (from a prior pass) is a no-op.
	escapingLeaves := detectEscapingLeafIndices(body, leafIndex, nil)
	for idx := range escapingLeaves {
		leaf := leaves[idx]
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		if existing, ok := type_system.PruneLifetime(type_system.GetLifetime(leaf.leafType)).(*type_system.LifetimeValue); ok && existing.IsStatic {
			continue
		}
		setLifetimeOnType(leaf.leafType, &type_system.LifetimeValue{
			Name:     "static",
			IsStatic: true,
		})
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

	// A function is a generator when its body actually contains `yield`,
	// not merely when its return type happens to be Generator<...>. A
	// plain function that forwards a Generator<T> parameter (e.g.
	// `return g`) is NOT a generator and the return type carries the
	// lifetime as a whole, not just its inner yield T.
	isGenerator := len(yields) > 0

	// Generators have two lifetime-bearing result positions: the yield
	// type T (driven by `yield expr` paths) and TReturn (driven by
	// `return expr` paths). They are inferred independently — a generator
	// might yield from one parameter and return another, in which case
	// each receives its own lifetime variable. For non-generators there
	// is a single result position: the return type for plain functions,
	// or the inner T inside Promise<T, E> for async functions (the
	// Promise container itself is freshly assembled per call, with no
	// caller-provided lifetime).
	if isGenerator {
		c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, yields, generatorYieldType(funcType.Return))
		c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, v.exprs, generatorReturnType(funcType.Return))
		return
	}
	resultType := funcType.Return
	if isAsync {
		if t := promiseValueType(funcType.Return); t != nil {
			resultType = t
		}
	}
	c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, v.exprs, resultType)
}

// attachLifetimeToResult is the per-result-position core of lifetime
// inference: given a list of alias-source expressions (e.g. `return e`
// or `yield e`) and a single result-type position, walk the expressions
// to collect aliased leaves, allocate / reuse a LifetimeVar per leaf,
// attach those lifetimes to the leaves AND to the result, and append
// any fresh LifetimeVars to funcType.LifetimeParams.
//
// resultType may be nil (e.g. a Generator<_, void, _>'s TReturn slot is
// `void` and carries no lifetime); in that case the function bails
// without allocating any lifetime params even if returns happen to
// alias parameters, since there is no result-side position to attach.
//
// This is invoked once per result position. Generators call it twice
// (yields → yield T, returns → TReturn); plain and async functions
// call it once. When called twice, fresh LifetimeVars allocated by the
// first call are appended to funcType.LifetimeParams before the second
// call begins, so the second call's `nameIdx` (which reads
// len(funcType.LifetimeParams)) correctly continues the 'a, 'b, ...
// sequence.
//
// escapingLeaves names leaves whose lifetime was already overwritten to
// 'static by inferLifetimesCore prior to this call. Reading those leaves
// here is read-only — the helper records returnHasStatic but does not
// re-mutate the leaf's lifetime — so it is safe to invoke twice.
func (c *Checker) attachLifetimeToResult(
	funcType *type_system.FuncType,
	leaves []paramLeaf,
	leafIndex map[liveness.VarID]int,
	escapingLeaves set.Set[int],
	sourceExprs []ast.Expr,
	resultType type_system.Type,
) {
	if resultType == nil || !typeCarriesLifetime(resultType) {
		return
	}
	if len(sourceExprs) == 0 {
		return
	}

	// Determine which leaves are aliased across all alias-source
	// expressions. Use insertion order so the resulting LifetimeParams
	// list is deterministic.
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

	// Walk the aliased leaves. For each, either:
	//   (a) Reuse the leaf's existing lifetime (set by a prior pass —
	//       the LifetimeVar is pointer-shared, already in
	//       funcType.LifetimeParams).
	//   (b) Treat as escaping — contribute 'static to the return.
	//   (c) Allocate a fresh LifetimeVar — append it to LifetimeParams.
	// returnLifetimeMembers accumulates ALL lifetimes contributed to
	// the return, so that on a re-run we can rebuild the union over
	// both pre-existing and newly-discovered leaves.
	var newLifetimeParams []*type_system.LifetimeVar
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
		// Reuse an already-inferred LifetimeVar on the leaf so the
		// signature stays stable across re-runs and pointer identity
		// is preserved with anything that already references it. This
		// also lets the second call (e.g. for generator TReturn) reuse
		// a lifetime allocated by the first (yield T) call when the
		// same parameter is aliased on both sides.
		if existingLV, ok := type_system.PruneLifetime(type_system.GetLifetime(leaf.leafType)).(*type_system.LifetimeVar); ok && existingLV != nil {
			returnLifetimeMembers = append(returnLifetimeMembers, existingLV)
			continue
		}
		nameIdx := len(funcType.LifetimeParams) + len(newLifetimeParams)
		newLV := c.FreshLifetimeVar(lifetimeParamName(nameIdx))
		newLifetimeParams = append(newLifetimeParams, newLV)
		returnLifetimeMembers = append(returnLifetimeMembers, newLV)

		// Attach the lifetime to the leaf's type position.
		setLifetimeOnType(leaf.leafType, newLV)
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
	// TODO(#507): if resultType is pointer-shared with a param leaf
	// (which happens for unioned yield types and for `yield from`),
	// this write bleeds through to the param. Shallow-clone resultType
	// here and substitute the clone back into funcType.Return.TypeArgs.
	setLifetimeOnType(resultType, returnLifetime)

	if len(newLifetimeParams) > 0 {
		funcType.LifetimeParams = append(funcType.LifetimeParams, newLifetimeParams...)
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
// VarID set by the rename pass. Tuple-, object-, and rest-destructuring
// patterns are supported; for object patterns, leaf positions are
// resolved by string-key lookup against the corresponding ObjectType's
// PropertyElems.
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

// walkPatternForLeaves recurses through a destructuring pattern in
// lockstep with its inferred type, appending a paramLeaf for every
// leaf binding (IdentPat or ObjShorthandPat) with a positive VarID.
//
// We don't use the ast.Visitor interface here because the visitor only
// carries pattern context — it has no notion of a parallel type tree.
// Each container pattern (TuplePat, ObjectPat, RestPat) needs to pick
// the matching sub-type (tuple element, property value, array element)
// before descending, which would force a visitor implementation to
// maintain a side stack of types pushed/popped on EnterPat/ExitPat and
// to repeat the same type-shape switching done here. With one caller
// and one purpose, a self-contained recursive walk is simpler.
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
			elemType := tt.Elems[i]
			// Tuple rest spreads (`[T, ...Array<U>]`) carry a
			// RestSpreadType in the tuple's Elems slot. The pattern's
			// matching RestPat expects to receive the *array* type so
			// its inner pattern can be walked against the element type.
			// Unwrap the spread here so RestPat sees Array<U> directly.
			if rest, ok := elemType.(*type_system.RestSpreadType); ok {
				elemType = rest.Type
			}
			walkPatternForLeaves(elem, elemType, into)
		}
	case *ast.ObjectPat:
		ot, ok := pt.(*type_system.ObjectType)
		if !ok {
			return
		}
		// Build a key→Type map for the object type's properties so each
		// pattern element can resolve its corresponding sub-position.
		propTypes := make(map[string]type_system.Type)
		for _, elem := range ot.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok &&
				prop.Name.Kind == type_system.StrObjTypeKeyKind {
				propTypes[prop.Name.Str] = prop.Value
			}
		}
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				// `{ key: value-pat }` — recurse into value-pat with
				// the property's type. The property name is the lookup
				// key.
				if e.Key == nil {
					continue
				}
				if propType, exists := propTypes[e.Key.Name]; exists {
					walkPatternForLeaves(e.Value, propType, into)
				}
			case *ast.ObjShorthandPat:
				// `{ key }` (or `{ key: TypeAnn }`, `{ key = default }`)
				// — the leaf binding has the same name as the property.
				if e.Key == nil || e.VarID <= 0 {
					continue
				}
				propType, exists := propTypes[e.Key.Name]
				if !exists {
					continue
				}
				*into = append(*into, paramLeaf{
					varID:    liveness.VarID(e.VarID),
					leafType: propType,
				})
			case *ast.ObjRestPat:
				// `{ ...rest }` — the rest pattern collects remaining
				// properties into a fresh object. Like a function rest
				// param's container, this is freshly assembled per
				// call, so attaching a container-level lifetime would
				// be wrong. Per-property element lifetimes for object
				// rest are deferred (would require synthesizing a
				// per-call object type).
			}
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

// stripMutabilityWrapper strips a MutType wrapper so callers can
// match the underlying structural type. Returns the input unchanged if
// not wrapped.
func stripMutabilityWrapper(t type_system.Type) type_system.Type {
	if mt, ok := t.(*type_system.MutType); ok {
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

// detectEscapingLeafIndices walks a function body looking for
// assignments that store one of the function's parameter leaves into a
// non-local location (module-level variable, prelude binding, or any
// other binding looked up through the function's enclosing scope
// chain). leafIndex maps a leaf binding's VarID to its position in the
// leaves list; the returned set contains the indices of leaves whose
// value escapes.
//
// Detection is by VarID: the rename pass assigns positive VarIDs to
// locals and negative VarIDs to outer-scope references. An assignment
// whose lvalue root is a non-local identifier (VarID <= 0) is treated
// as a store into storage whose lifetime outlives the callee's stack
// frame. Stores into locals don't escape because the local's lifetime
// is bounded by the function body.
//
// extraEscapeRoots, if non-nil, names additional positive-VarID roots
// whose lvalue assignments should ALSO be treated as escaping. Used for
// constructor bodies, where `self` is a local parameter (positive VarID)
// but `self.<field> = expr` should escape because the receiver outlives
// the constructor's stack frame. Pass nil for ordinary function bodies.
//
// Limitations:
//   - Closures over a *nested* function's local: inner functions whose
//     body assigns to an outer function's local will mark that param as
//     'static, which is more conservative than necessary. The
//     borrow-checker tolerates this (it's sound, just imprecise).
//   - Stores via property assignment whose root is a local but is
//     itself stored elsewhere: not tracked here.
func detectEscapingLeafIndices(
	body *ast.Block,
	leafIndex map[liveness.VarID]int,
	extraEscapeRoots set.Set[liveness.VarID],
) set.Set[int] {
	escaped := set.NewSet[int]()
	if body == nil || len(leafIndex) == 0 {
		return escaped
	}
	v := &escapingRefsVisitor{
		leafIndex:        leafIndex,
		extraEscapeRoots: extraEscapeRoots,
		escaped:          escaped,
	}
	body.Accept(v)
	return escaped
}

type escapingRefsVisitor struct {
	ast.DefaultVisitor
	leafIndex        map[liveness.VarID]int
	extraEscapeRoots set.Set[liveness.VarID] // may be nil
	escaped          set.Set[int]
}

func (v *escapingRefsVisitor) EnterExpr(e ast.Expr) bool {
	if be, ok := e.(*ast.BinaryExpr); ok && be.Op == ast.Assign {
		if v.isEscapingLValue(be.Left) {
			// Use the checker-aware variant so a parameter that flows
			// through a call whose callee returns its argument
			// (`cache = wrap(p)`) is still detected as escaping.
			src := determineCheckerAliasSource(be.Right)
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

// isEscapingLValue returns true if the given assignment-target expression's
// root identifier (walking through MemberExpr/IndexExpr chains) is an
// escape root: either a non-local binding (rootObjectVarID returns 0,
// meaning module-level / prelude / outer-scope) or a positive VarID that
// the caller declared as a forced escape root (e.g. `self` in a
// constructor body, where `self.<field> = expr` outlives the local frame
// even though `self` itself is a local parameter).
func (v *escapingRefsVisitor) isEscapingLValue(expr ast.Expr) bool {
	root := rootObjectVarID(expr)
	if root == 0 {
		return true
	}
	if v.extraEscapeRoots != nil && v.extraEscapeRoots.Contains(root) {
		return true
	}
	return false
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
	case *type_system.MutType:
		setLifetimeOnType(ty.Type, lt)
	}
}

// propagateCalleeStaticLifetimes is the caller-side counterpart of Phase
// 8.4's escape detection. After a call is type-checked, walk the
// callee's parameters: any param whose lifetime resolves to `'static`
// represents a value that the callee stored into storage whose lifetime
// outlives the callee's stack frame. For each such param, mark the
// corresponding argument variable's alias sets via AliasTracker.MarkStatic
// with the param's mutability — this records that a permanent reference
// to the argument has escaped, so the transition checker can flag later
// mut↔immut transitions on the argument as unsafe.
//
// Argument resolution goes through determineCheckerAliasSource, so the
// escape propagates through identity-like nested calls whose return
// type aliases an argument (e.g. `cacheItem(id(p))` marks `p`). Has no
// effect when no underlying VarID can be resolved — e.g. property
// projections like `f(p.x)`, which would need projection-path tracking,
// deferred to a later phase.
func (c *Checker) propagateCalleeStaticLifetimes(
	ctx Context,
	fnType *type_system.FuncType,
	args []ast.Expr,
) {
	if ctx.Aliases == nil || fnType == nil {
		return
	}
	for i, p := range fnType.Params {
		// For a rest parameter (`...items: Array<T>`), the lifetime-
		// bearing position is the *element* type T, not the Array
		// container. Every variadic argument from this index onward is
		// passed as an element, so we mark each one — not just args[i].
		if rp, isRest := p.Pattern.(*type_system.RestPat); isRest {
			elem := arrayElemType(p.Type)
			if elem == nil {
				continue
			}
			// Walk the rest's inner pattern × element type to collect
			// every leaf-position mutability whose lifetime resolved to
			// `'static`. This catches both `...args: T[]` (a single
			// IdentPat leaf) and the destructured form `...[a, b]: ...[]`
			// where individual elements may escape independently.
			muts := collectStaticEscapeMutabilities(rp.Pattern, elem)
			if len(muts) == 0 {
				continue
			}
			for j := i; j < len(args); j++ {
				src := determineCheckerAliasSource(args[j])
				switch src.Kind {
				case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
					for _, mut := range muts {
						for _, vid := range src.VarIDs {
							ctx.Aliases.MarkStatic(vid, mut)
						}
					}
				}
			}
			return
		}

		if i >= len(args) {
			break
		}
		// Walk the param's pattern × type in lockstep (same shape as
		// collectParamLeaves) so that destructured leaves carrying
		// `'static` are detected even when the *outer* param type has no
		// `'static` annotation. Phase 8.4's setLifetimeOnType attaches
		// `'static` at the leaf type position only — a tuple- or object-
		// destructured param with one escaping leaf has no top-level
		// static lifetime, so a single `GetLifetime(p.Type)` check would
		// silently miss it.
		muts := collectStaticEscapeMutabilities(p.Pattern, p.Type)
		if len(muts) == 0 {
			continue
		}
		// Use the checker-aware alias-source helper so a nested call whose
		// return aliases its argument (e.g. an identity-like wrapper)
		// propagates the escape to the underlying variable. The plain
		// liveness helper treats every CallExpr as fresh, hiding such
		// transitive aliases from the static-escape check.
		src := determineCheckerAliasSource(args[i])
		switch src.Kind {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, mut := range muts {
				for _, vid := range src.VarIDs {
					ctx.Aliases.MarkStatic(vid, mut)
				}
			}
		}
	}
}

// collectStaticEscapeMutabilities walks a callee's parameter pattern in
// lockstep with its inferred type and returns the alias mutability of
// every leaf whose pruned lifetime contains `'static`. Pattern shapes
// match collectParamLeaves / walkPatternForLeaves: IdentPat is a leaf
// at the param's full type, TuplePat / ObjectPat / RestPat descend into
// the matching sub-type, ObjShorthandPat is a leaf at the property
// type, and ObjRestPat is skipped (its container is freshly assembled
// per call and can't carry a caller-provided lifetime). The mutability
// is determined per-leaf via isMutableType so a destructured param can
// emit a mix of mut/immut escapes.
func collectStaticEscapeMutabilities(pat type_system.Pat, t type_system.Type) []liveness.AliasMutability {
	var out []liveness.AliasMutability
	walkPatternForStaticLeaves(pat, t, &out)
	return out
}

func walkPatternForStaticLeaves(pat type_system.Pat, t type_system.Type, into *[]liveness.AliasMutability) {
	if pat == nil || t == nil {
		return
	}
	pt := stripMutabilityWrapper(type_system.Prune(t))
	switch p := pat.(type) {
	case *type_system.IdentPat:
		lt := type_system.PruneLifetime(type_system.GetLifetime(t))
		if !lifetimeContainsStatic(lt) {
			return
		}
		mut := liveness.AliasImmutable
		if isMutableType(t) {
			mut = liveness.AliasMutable
		}
		*into = append(*into, mut)
	case *type_system.TuplePat:
		tt, ok := pt.(*type_system.TupleType)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if i >= len(tt.Elems) {
				break
			}
			elemType := tt.Elems[i]
			if rest, ok := elemType.(*type_system.RestSpreadType); ok {
				elemType = rest.Type
			}
			walkPatternForStaticLeaves(elem, elemType, into)
		}
	case *type_system.ObjectPat:
		ot, ok := pt.(*type_system.ObjectType)
		if !ok {
			return
		}
		propTypes := make(map[string]type_system.Type)
		for _, elem := range ot.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok &&
				prop.Name.Kind == type_system.StrObjTypeKeyKind {
				propTypes[prop.Name.Str] = prop.Value
			}
		}
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *type_system.ObjKeyValuePat:
				if propType, exists := propTypes[e.Key]; exists {
					walkPatternForStaticLeaves(e.Value, propType, into)
				}
			case *type_system.ObjShorthandPat:
				propType, exists := propTypes[e.Key]
				if !exists {
					continue
				}
				lt := type_system.PruneLifetime(type_system.GetLifetime(propType))
				if !lifetimeContainsStatic(lt) {
					continue
				}
				mut := liveness.AliasImmutable
				if isMutableType(propType) {
					mut = liveness.AliasMutable
				}
				*into = append(*into, mut)
			}
		}
	case *type_system.RestPat:
		elem := arrayElemType(t)
		if elem == nil {
			return
		}
		walkPatternForStaticLeaves(p.Pattern, elem, into)
	}
}

// lifetimeContainsStatic reports whether the given (already pruned)
// lifetime is `'static` or a LifetimeUnion that contains `'static`. A
// LifetimeUnion containing `'static` means at least one branch escaped,
// which is enough to treat the argument as permanently aliased.
func lifetimeContainsStatic(lt type_system.Lifetime) bool {
	switch v := lt.(type) {
	case *type_system.LifetimeValue:
		return v != nil && v.IsStatic
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			if lifetimeContainsStatic(type_system.PruneLifetime(m)) {
				return true
			}
		}
	}
	return false
}

// InferConstructorLifetimes analyzes a class declaration to determine
// which constructor parameters are stored into `self` (as fields or
// through transitive aliases), attaches a fresh LifetimeVar to each such
// parameter and to the constructor's return type, and records the
// lifetime parameters on the class TypeAlias.
//
// Capture detection reuses the *escape-detection* primitives from
// `inferLifetimesCore` — `escapingRefsVisitor`,
// `determineCheckerAliasSource`, `collectParamLeaves` — to walk the
// constructor body for assignments whose lvalue root is `self` (e.g.
// `self.x = p`, `self.list[0] = p`) and trace the RHS back to the
// underlying parameter VarID(s). This is precise: guards like
// `if x < 0 { throw … }`, side-effect calls like `validate(x)`, and
// transient computation like `var tmp = x * 2` are correctly identified
// as NOT capturing `x`.
//
// To produce VarIDs in the body and on parameter pattern leaves, this
// function runs its own `liveness.Rename` pass on the constructor body.
// VarIDs written here will be overwritten by the body phase's
// `runLivenessPrePass` when the constructor body is later type-checked,
// which is fine — we only need a consistent VarID space for the
// duration of this call.
//
// Why not call `inferLifetimesCore` directly? Three reasons it isn't a
// drop-in fit, even after sharing the escape-detection primitives:
//
//  1. Escape root differs. `inferLifetimesCore` calls
//     `detectEscapingLeafIndices` with a nil extra-roots set, which
//     only flags lvalues whose root has `VarID <= 0` (non-local). In a
//     constructor, `self` is a local positive-VarID parameter, so
//     `self.x = p` wouldn't be detected. The constructor path passes
//     `self`'s VarID as an extra escape root to the same function.
//
//  2. No return-alias pass needed. `inferLifetimesCore` does most of
//     its work in `attachLifetimeToResult` — collecting return/yield
//     exprs and unioning lifetimes onto the result type. Constructor
//     bodies have no `return expr` (the return is the implicit
//     `self`), and the return type already gets `LifetimeArgs`
//     attached separately below. Running the result-side machinery
//     would be a no-op at best.
//
//  3. Output shape differs. `inferLifetimesCore` writes lifetimes
//     onto `funcType.LifetimeParams` only. Constructors also write
//     to `typeAlias.LifetimeParams` and call `setLifetimeArgsOnType`
//     on the return so callers see `C<'a>`. That's class-specific
//     bookkeeping `inferLifetimesCore` doesn't do.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Inference of method-side return-type lifetimes when a method
//     captures a constructor param (the constructor gets the lifetime,
//     but the method's return type does not yet inherit it).
//   - Capture through call results: `self.f = wrap(p)` where `wrap`'s
//     return aliases its argument is currently NOT detected. This pass
//     runs in the placeholder phase, before any callee body has been
//     inferred, so `determineCheckerAliasSource` sees no lifetime info on
//     the callee's return type and falls back to treating the call as
//     opaque. The non-ctor path solves the analogous problem via the
//     post-body fixed-point reinference loop (`ReinferLifetimes` in
//     `InferComponent`); extending that loop to constructors requires
//     making `setLifetimeOnType` / `setLifetimeArgsOnType` /
//     `typeAlias.LifetimeParams` updates idempotent and reconciling them
//     with class consumers that may have already resolved the class
//     during the body phase. Tracked by `TestCtorCapturesViaWrappingCall`
//     (currently t.Skip).
func (c *Checker) InferConstructorLifetimes(
	ctx Context,
	classDecl *ast.ClassDecl,
	typeAlias *type_system.TypeAlias,
	ctorFn *type_system.FuncType,
) {
	if classDecl == nil || ctorFn == nil || typeAlias == nil {
		return
	}

	// Per #499: constructor calls always return immutable instances; the user
	// opts in to a mutable instance via the `mut` prefix at the call site.

	// Honor explicit lifetime params if the user already wrote them.
	if len(typeAlias.LifetimeParams) > 0 {
		return
	}

	// Locate the (single) in-body `ConstructorElem`. After Phase 4 there is
	// no other source of constructor params on a class. If synthesis or
	// parsing left no constructor in place, there's nothing to do.
	var ctorElem *ast.ConstructorElem
	for _, e := range classDecl.Body {
		if c, ok := e.(*ast.ConstructorElem); ok {
			ctorElem = c
			break
		}
	}
	// User-written ctors always parse with a body, and no synthesizer
	// produces a body-less ConstructorElem; the Body == nil guard is
	// defensive only.
	if ctorElem == nil || ctorElem.Fn == nil || ctorElem.Fn.Body == nil {
		return
	}
	// The first AST param is `mut self`. The remaining params are the
	// callable params and line up with `ctorFn.Params` (which
	// `inferConstructorSig` populates by dropping `self`).
	allCtorParams := ctorElem.Fn.Params
	if len(allCtorParams) == 0 {
		return
	}
	callableParams := allCtorParams[1:]
	if len(callableParams) == 0 {
		return
	}

	// Run a mini rename pass so we have VarIDs on the callable params and
	// on every IdentExpr in the body. `self` is treated as an extra param
	// name (mirroring how methods see their implicit receiver in
	// `runLivenessPrePass`). Rename errors are ignored: any unresolved
	// references will be re-reported by the body phase's rename pass.
	//
	// VarIDs written here are safely overwritten when the body phase later
	// runs `runLivenessPrePass` on the same ctor body: `liveness.Rename`
	// does not recurse into nested FuncExpr/FuncDecl bodies (those get
	// their own pass), so the only nodes touched here are the outer ctor
	// body's IdentExprs/IdentPats — exactly the nodes the body phase
	// overwrites. Inner functions are unaffected by this pass.
	outerBindings := collectOuterBindings(ctx.Scope)
	renameResult := liveness.Rename(callableParams, *ctorElem.Fn.Body, outerBindings, "self")
	selfVarID, ok := renameResult.ExtraParamVarIDs["self"]
	if !ok {
		return
	}

	// Build the per-leaf index from callable-param patterns × types so
	// that destructured leaves are tracked individually (e.g. for
	// `constructor(self, [a, b]: [...])` each of `a`, `b` gets its own
	// leaf). Lifetime allocation below still happens at the top-level
	// param granularity, but per-leaf escape tracking lets the RHS
	// alias-source tracing find the right binding.
	leaves := collectParamLeaves(callableParams, ctorFn.Params)
	if len(leaves) == 0 {
		return
	}
	leafIndex := make(map[liveness.VarID]int)
	for i, l := range leaves {
		leafIndex[l.varID] = i
	}

	// Detect escapes: assignments `self.<…> = expr` where the RHS aliases
	// one of the callable params. `self`'s VarID is added as an extra
	// escape root because it is itself a positive-VarID parameter — the
	// non-local-only check would not recognize `self.x = p` as escaping.
	escapeRoots := set.NewSet[liveness.VarID]()
	escapeRoots.Add(selfVarID)
	escapingLeaves := detectEscapingLeafIndices(ctorElem.Fn.Body, leafIndex, escapeRoots)
	if escapingLeaves.Len() == 0 {
		return
	}

	// Allocate one lifetime per escaping leaf (in declaration order for
	// determinism). For an IdentPat top-level param the leaf type IS the
	// whole param type; for destructured patterns the leaf type is the
	// sub-position the rename pass bound (e.g. the tuple element type for
	// `[a, b]: [mut T, mut U]`).
	var lifetimeParams []*type_system.LifetimeVar
	for leafIdx, leaf := range leaves {
		if !escapingLeaves.Contains(leafIdx) {
			continue
		}
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		lv := c.FreshLifetimeVar(lifetimeParamName(len(lifetimeParams)))
		lifetimeParams = append(lifetimeParams, lv)
		setLifetimeOnType(leaf.leafType, lv)
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

// forEachLeafBinding invokes fn for every identifier-binding leaf
// introduced by a pattern, walking through Tuple/Object/Rest containers.
// Leaves are IdentPat (the pattern's own Name+VarID) and ObjShorthandPat
// (the property's Key.Name plus the shorthand's own VarID). ObjRestPat,
// RestPat, and ObjKeyValuePat are container-only; this helper recurses
// through them.
//
// Note: walkPatternForLeaves cannot use this helper because it walks the
// pattern in *lockstep with the parameter's type* (slicing tuple/object/
// array sub-positions to compute each leaf's leafType) and intentionally
// skips ObjRestPat (a freshly-assembled container has no
// caller-provided lifetime). The two name-only walkers below share this
// traversal; lifetime walking does not.
func forEachLeafBinding(pat ast.Pat, fn func(name string, varID int)) {
	if pat == nil {
		return
	}
	switch p := pat.(type) {
	case *ast.IdentPat:
		fn(p.Name, p.VarID)
	case *ast.TuplePat:
		for _, sub := range p.Elems {
			forEachLeafBinding(sub, fn)
		}
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				forEachLeafBinding(e.Value, fn)
			case *ast.ObjShorthandPat:
				if e.Key != nil {
					fn(e.Key.Name, e.VarID)
				}
			case *ast.ObjRestPat:
				forEachLeafBinding(e.Pattern, fn)
			}
		}
	case *ast.RestPat:
		forEachLeafBinding(p.Pattern, fn)
	}
}

// collectPatternBindingNames adds every identifier name introduced by a
// pattern (recursively) to the provided set.
func collectPatternBindingNames(p ast.Pat, into set.Set[string]) {
	forEachLeafBinding(p, func(name string, _ int) {
		into.Add(name)
	})
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
	case *type_system.MutType:
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
	case *type_system.MutType:
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

// generatorReturnType returns the TReturn slot of a
// Generator<T, TReturn, TNext> or AsyncGenerator<T, TReturn, TNext>
// reference, walking past mutability wrappers. Returns nil if t is not
// a generator reference or doesn't have a second type arg.
func generatorReturnType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	name := type_system.QualIdentToString(tref.Name)
	if name != "Generator" && name != "AsyncGenerator" {
		return nil
	}
	if len(tref.TypeArgs) < 2 {
		return nil
	}
	return tref.TypeArgs[1]
}

// promiseValueType returns the resolved value type T of a
// Promise<T, E> reference, walking past mutability wrappers. Returns
// nil if t is not a Promise reference or has no type args.
func promiseValueType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if type_system.QualIdentToString(tref.Name) != "Promise" {
		return nil
	}
	if len(tref.TypeArgs) == 0 {
		return nil
	}
	return tref.TypeArgs[0]
}

// collectYieldExprs walks a function body collecting the value
// expressions of every yield expression — both regular (`yield e`) and
// delegate (`yield from iter`). For a delegate yield, the iterator
// expression itself is the alias source: each yielded value is an
// element of the iterator and so borrows from it, meaning the relay
// generator's yield T inherits the iterator's lifetime. Bare `yield`
// (with no value) is skipped because there is no value to alias.
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
		// Both regular and delegate yields contribute alias-source
		// information. For `yield expr`, expr is the yielded value;
		// for `yield from iter`, iter is the source whose elements
		// are yielded one by one — propagating iter's lifetime to
		// each yielded element.
		if e.Value != nil {
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

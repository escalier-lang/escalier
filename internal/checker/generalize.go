package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// collectUnresolvedTypeVars walks a type tree and collects all unresolved
// TypeVarType nodes (where Prune returns the same TypeVarType). Results are
// stored in the vars map keyed by type var ID, and order tracks insertion order.
//
// TODO(#590): cycle detection. Composite types (UnionType, IntersectionType,
// ObjectType, etc.) are not tracked in a visited set, so a cyclic union (e.g.
// produced by mutually recursive functions with an if/else base case where
// each function's return type contains a TypeVar that prunes back to the
// other function's return) causes infinite recursion and a stack overflow.
func collectUnresolvedTypeVars(
	t type_system.Type,
	vars map[int]*type_system.TypeVarType,
	order *[]int,
) {
	if t == nil {
		return
	}
	t = type_system.Prune(t)
	switch t := t.(type) {
	case *type_system.TypeVarType:
		if _, seen := vars[t.ID]; !seen {
			vars[t.ID] = t
			*order = append(*order, t.ID)
			collectUnresolvedTypeVars(t.Constraint, vars, order)
			collectUnresolvedTypeVars(t.Default, vars, order)
			// Defensive: ArrayConstraints are resolved before generalization
			// runs, so this branch is unlikely to execute. If it does, we
			// need to collect the element type vars so they get generalized.
			if t.ArrayConstraint != nil {
				for _, elemTV := range t.ArrayConstraint.LiteralIndexes {
					collectUnresolvedTypeVars(elemTV, vars, order)
				}
				collectUnresolvedTypeVars(t.ArrayConstraint.ElemTypeVar, vars, order)
				for _, mev := range t.ArrayConstraint.MethodElemVars {
					collectUnresolvedTypeVars(mev, vars, order)
				}
			}
		}
	case *type_system.FuncType:
		for _, tp := range t.TypeParams {
			collectUnresolvedTypeVars(tp.Constraint, vars, order)
			collectUnresolvedTypeVars(tp.Default, vars, order)
		}
		for _, param := range t.Params {
			collectUnresolvedTypeVars(param.Type, vars, order)
		}
		collectUnresolvedTypeVars(t.Return, vars, order)
		collectUnresolvedTypeVars(t.Throws, vars, order)
	case *type_system.TypeRefType:
		for _, arg := range t.TypeArgs {
			collectUnresolvedTypeVars(arg, vars, order)
		}
	case *type_system.ObjectType:
		for _, elem := range t.Elems {
			switch e := elem.(type) {
			case *type_system.PropertyElem:
				collectUnresolvedTypeVars(e.Value, vars, order)
			case *type_system.MethodElem:
				collectUnresolvedTypeVars(e.Fn, vars, order)
			case *type_system.GetterElem:
				collectUnresolvedTypeVars(e.Fn, vars, order)
			case *type_system.SetterElem:
				collectUnresolvedTypeVars(e.Fn, vars, order)
			case *type_system.CallableElem:
				collectUnresolvedTypeVars(e.Fn, vars, order)
			case *type_system.ConstructorElem:
				collectUnresolvedTypeVars(e.Fn, vars, order)
			case *type_system.RestSpreadElem:
				collectUnresolvedTypeVars(e.Value, vars, order)
			case *type_system.MappedElem:
				collectUnresolvedTypeVars(e.Value, vars, order)
				collectUnresolvedTypeVars(e.Name, vars, order)
				collectUnresolvedTypeVars(e.Check, vars, order)
				collectUnresolvedTypeVars(e.Extends, vars, order)
				if e.TypeParam != nil {
					collectUnresolvedTypeVars(e.TypeParam.Constraint, vars, order)
				}
			case *type_system.IndexSignatureElem:
				collectUnresolvedTypeVars(e.KeyType, vars, order)
				collectUnresolvedTypeVars(e.Value, vars, order)
			}
		}
	case *type_system.TupleType:
		for _, elem := range t.Elems {
			collectUnresolvedTypeVars(elem, vars, order)
		}
	case *type_system.UnionType:
		for _, elem := range t.Types {
			collectUnresolvedTypeVars(elem, vars, order)
		}
	case *type_system.IntersectionType:
		for _, elem := range t.Types {
			collectUnresolvedTypeVars(elem, vars, order)
		}
	case *type_system.RestSpreadType:
		collectUnresolvedTypeVars(t.Type, vars, order)
	case *type_system.MutType:
		collectUnresolvedTypeVars(t.Type, vars, order)
	case *type_system.KeyOfType:
		collectUnresolvedTypeVars(t.Type, vars, order)
	case *type_system.IndexType:
		collectUnresolvedTypeVars(t.Target, vars, order)
		collectUnresolvedTypeVars(t.Index, vars, order)
	case *type_system.CondType:
		collectUnresolvedTypeVars(t.Check, vars, order)
		collectUnresolvedTypeVars(t.Extends, vars, order)
		collectUnresolvedTypeVars(t.Then, vars, order)
		collectUnresolvedTypeVars(t.Else, vars, order)
	// Leaf types with no type children to traverse:
	case *type_system.PrimType:
	case *type_system.LitType:
	case *type_system.UnknownType:
	case *type_system.NeverType:
	case *type_system.VoidType:
	case *type_system.AnyType:
	case *type_system.UniqueSymbolType:
	case *type_system.TemplateLitType:
		for _, t := range t.Types {
			collectUnresolvedTypeVars(t, vars, order)
		}
	case *type_system.GlobalThisType:
	case *type_system.ErrorType:
	case *type_system.RegexType:
	case *type_system.WildcardType:
	case *type_system.IntrinsicType:
	case *type_system.NamespaceType:
	case *type_system.TypeOfType:
	case *type_system.InferType:
	case *type_system.ExtractorType:
	}
}

// deepCloneType recursively clones a type, replacing all TypeVarType instances
// with fresh type variables. The varMapping ensures consistent replacement: if
// the same TypeVar is referenced multiple times, it maps to the same fresh var.
// Container types (FuncType, TupleType, etc.) are rebuilt; leaf types (LitType,
// PrimType, NeverType, etc.) are shared since Unify never mutates them.
func (c *Checker) deepCloneType(t type_system.Type, varMapping map[int]*type_system.TypeVarType) type_system.Type {
	t = type_system.Prune(t)
	switch t := t.(type) {
	case *type_system.TypeVarType:
		if fresh, ok := varMapping[t.ID]; ok {
			return fresh
		}
		fresh := c.FreshVar(nil)
		fresh.IsObjectRest = t.IsObjectRest
		varMapping[t.ID] = fresh
		if t.Constraint != nil {
			fresh.Constraint = c.deepCloneType(t.Constraint, varMapping)
		}
		if t.ArrayConstraint != nil {
			ac := t.ArrayConstraint
			clonedIndexes := make(map[int]type_system.Type, len(ac.LiteralIndexes))
			for idx, elemTV := range ac.LiteralIndexes {
				clonedIndexes[idx] = c.deepCloneType(elemTV, varMapping)
			}
			// Clone MethodElemVars without going through deepCloneType: the
			// fresh elem vars may already be bound (e.g. after push(5) binds
			// them to number), and deepCloneType would Prune through to the
			// concrete type, making the cast to *TypeVarType panic.
			clonedMethodElemVars := make([]*type_system.TypeVarType, len(ac.MethodElemVars))
			for i, mev := range ac.MethodElemVars {
				if existing, ok := varMapping[mev.ID]; ok {
					clonedMethodElemVars[i] = existing
				} else {
					freshMev := c.FreshVar(nil)
					freshMev.Widenable = mev.Widenable
					if mev.Instance != nil {
						freshMev.Instance = c.deepCloneType(mev.Instance, varMapping)
					}
					varMapping[mev.ID] = freshMev
					clonedMethodElemVars[i] = freshMev
				}
			}
			fresh.ArrayConstraint = &type_system.ArrayConstraint{
				LiteralIndexes:     clonedIndexes,
				HasNonLiteralIndex: ac.HasNonLiteralIndex,
				HasMutatingMethod:  ac.HasMutatingMethod,
				HasReadOnlyMethod:  ac.HasReadOnlyMethod,
				HasIndexAssignment: ac.HasIndexAssignment,
				ElemTypeVar:        c.deepCloneType(ac.ElemTypeVar, varMapping),
				MethodElemVars:     clonedMethodElemVars,
			}
		}
		return fresh
	case *type_system.FuncType:
		params := make([]*type_system.FuncParam, len(t.Params))
		for i, p := range t.Params {
			params[i] = &type_system.FuncParam{
				Pattern:  p.Pattern,
				Type:     c.deepCloneType(p.Type, varMapping),
				Optional: p.Optional,
			}
		}
		return type_system.NewFuncType(nil, t.TypeParams, params,
			c.deepCloneType(t.Return, varMapping),
			c.deepCloneType(t.Throws, varMapping))
	case *type_system.MutType:
		return &type_system.MutType{
			Type: c.deepCloneType(t.Type, varMapping),
		}
	case *type_system.TupleType:
		elems := make([]type_system.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = c.deepCloneType(e, varMapping)
		}
		return type_system.NewTupleType(nil, elems...)
	case *type_system.UnionType:
		types := make([]type_system.Type, len(t.Types))
		for i, u := range t.Types {
			types[i] = c.deepCloneType(u, varMapping)
		}
		return type_system.NewUnionType(nil, types...)
	case *type_system.IntersectionType:
		types := make([]type_system.Type, len(t.Types))
		for i, u := range t.Types {
			types[i] = c.deepCloneType(u, varMapping)
		}
		return type_system.NewIntersectionType(nil, types...)
	case *type_system.TypeRefType:
		args := make([]type_system.Type, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = c.deepCloneType(a, varMapping)
		}
		return type_system.NewTypeRefType(nil, type_system.QualIdentToString(t.Name), t.TypeAlias, args...)
	case *type_system.RestSpreadType:
		return type_system.NewRestSpreadType(nil, c.deepCloneType(t.Type, varMapping))
	case *type_system.ObjectType:
		elems := make([]type_system.ObjTypeElem, len(t.Elems))
		for i, elem := range t.Elems {
			switch e := elem.(type) {
			case *type_system.PropertyElem:
				elems[i] = &type_system.PropertyElem{
					Name:     e.Name,
					Optional: e.Optional,
					Readonly: e.Readonly,
					Value:    c.deepCloneType(e.Value, varMapping),
				}
			case *type_system.MethodElem:
				elems[i] = &type_system.MethodElem{
					Name: e.Name,
					Fn:   c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
				}
			case *type_system.GetterElem:
				elems[i] = &type_system.GetterElem{
					Name: e.Name,
					Fn:   c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
				}
			case *type_system.SetterElem:
				elems[i] = &type_system.SetterElem{
					Name: e.Name,
					Fn:   c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
				}
			case *type_system.CallableElem:
				elems[i] = &type_system.CallableElem{
					Fn: c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
				}
			case *type_system.ConstructorElem:
				elems[i] = &type_system.ConstructorElem{
					Fn: c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
				}
			case *type_system.RestSpreadElem:
				elems[i] = &type_system.RestSpreadElem{
					Value: c.deepCloneType(e.Value, varMapping),
				}
			case *type_system.MappedElem:
				var clonedTypeParam *type_system.IndexParam
				if e.TypeParam != nil {
					clonedTypeParam = &type_system.IndexParam{
						Name:       e.TypeParam.Name,
						Constraint: c.deepCloneType(e.TypeParam.Constraint, varMapping),
					}
				}
				elems[i] = &type_system.MappedElem{
					TypeParam: clonedTypeParam,
					Name:      c.deepCloneType(e.Name, varMapping),
					Value:     c.deepCloneType(e.Value, varMapping),
					Optional:  e.Optional,
					Readonly:  e.Readonly,
					Check:     c.deepCloneType(e.Check, varMapping),
					Extends:   c.deepCloneType(e.Extends, varMapping),
				}
			case *type_system.IndexSignatureElem:
				elems[i] = type_system.NewIndexSignatureElem(
					c.deepCloneType(e.KeyType, varMapping),
					c.deepCloneType(e.Value, varMapping),
					e.Readonly,
				)
			default:
				elems[i] = elem
			}
		}
		return &type_system.ObjectType{
			ID:           t.ID,
			Elems:        elems,
			Exact:        t.Exact,
			Immutable:    t.Immutable,
			Mutable:      t.Mutable,
			Nominal:      t.Nominal,
			Interface:    t.Interface,
			Extends:      t.Extends,
			Implements:   t.Implements,
			SymbolKeyMap: t.SymbolKeyMap,
			Open:         t.Open,
		}
	case *type_system.CondType:
		return type_system.NewCondType(nil,
			c.deepCloneType(t.Check, varMapping),
			c.deepCloneType(t.Extends, varMapping),
			c.deepCloneType(t.Then, varMapping),
			c.deepCloneType(t.Else, varMapping),
		)
	case *type_system.IndexType:
		return type_system.NewIndexType(nil,
			c.deepCloneType(t.Target, varMapping),
			c.deepCloneType(t.Index, varMapping),
		)
	case *type_system.KeyOfType:
		return type_system.NewKeyOfType(nil, c.deepCloneType(t.Type, varMapping))
	case *type_system.TemplateLitType:
		types := make([]type_system.Type, len(t.Types))
		for i, typ := range t.Types {
			types[i] = c.deepCloneType(typ, varMapping)
		}
		return type_system.NewTemplateLitType(nil, t.Quasis, types)
	default:
		// Leaf types (LitType, PrimType, NeverType, VoidType, etc.)
		// contain no TypeVars and are never mutated by Unify.
		return t
	}
}

// tryMergeCallSitesWithOptionalParams checks if call sites with different param
// counts can be merged into a single FuncType with optional trailing params.
// This handles the case where f(a) and f(a, b) should produce fn(a, b?) -> T
// rather than an intersection. Returns nil if sites are not prefix-compatible.
func (c *Checker) tryMergeCallSitesWithOptionalParams(ctx Context, sites []*type_system.FuncType) *type_system.FuncType {
	// Sort sites by param count (ascending) to find the prefix chain.
	sorted := make([]*type_system.FuncType, len(sites))
	copy(sorted, sites)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j].Params) < len(sorted[j-1].Params); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	// The shortest site defines the required prefix. Each subsequent site
	// must have the same prefix params (by structural equality).
	shortest := sorted[0]
	longest := sorted[len(sorted)-1]
	prefixLen := len(shortest.Params)

	// Require a contiguous arity chain from shortest to longest.
	// e.g., f(a) and f(a,b,c) without f(a,b) should not merge, because
	// we have no call site providing the type of the second param alone.
	arities := make(map[int]bool)
	for _, site := range sorted {
		arities[len(site.Params)] = true
	}
	for k := prefixLen; k <= len(longest.Params); k++ {
		if !arities[k] {
			return nil
		}
	}

	for _, site := range sorted[1:] {
		if len(site.Params) < prefixLen {
			return nil // shouldn't happen after sort, but defensive
		}
		for j := 0; j < prefixLen; j++ {
			if !type_system.Equals(type_system.Prune(site.Params[j].Type), type_system.Prune(shortest.Params[j].Type)) {
				return nil // prefix doesn't match
			}
		}
		// Also check that intermediate sites are prefixes of longer ones.
		// e.g., for f(), f(a), f(a, b): each intermediate must match the
		// corresponding prefix of the longest.
		for j := prefixLen; j < len(site.Params); j++ {
			if j < len(longest.Params) {
				if !type_system.Equals(type_system.Prune(site.Params[j].Type), type_system.Prune(longest.Params[j].Type)) {
					return nil
				}
			}
		}
	}

	// All sites are prefix-compatible. Build merged FuncType using the
	// longest param list, marking params beyond the shortest as optional.
	params := make([]*type_system.FuncParam, len(longest.Params))
	for i, p := range longest.Params {
		params[i] = &type_system.FuncParam{
			Pattern:  p.Pattern,
			Type:     p.Type,
			Optional: i >= prefixLen,
		}
	}

	// Unify return types across all sites so the merged function has one return type.
	// If any unification fails, abort the merge and let the caller fall back to intersection.
	// Note: calling Unify directly on the originals (without deep-cloning) is safe
	// here because the return types are fresh TypeVars from inferCallExpr, so
	// unification always succeeds (one var binds to the other). If it did fail,
	// the partial binding is harmless — we return nil and the caller uses the
	// original sites for the intersection, where each site retains its own return var.
	base := sorted[0]
	for _, site := range sorted[1:] {
		errs := c.Unify(ctx, site.Return, base.Return)
		if len(errs) > 0 {
			return nil
		}
	}

	return type_system.NewFuncType(nil, nil, params, base.Return, type_system.NewNeverType(nil))
}

// resolveCallSites binds each TypeVarType that was used as a function callee
// to either a single FuncType (if all call sites are compatible) or an
// IntersectionType (if they are not). Must be called before GeneralizeFuncType.
func (c *Checker) resolveCallSites(ctx Context) {
	if ctx.CallSites == nil {
		return
	}
	for id, sites := range *ctx.CallSites {
		tv := (*ctx.CallSiteTypeVars)[id]
		if tv == nil {
			continue
		}
		// If the TypeVar was already resolved (e.g., by unification elsewhere), skip.
		// This is safe because: (1) overwriting tv.Instance would discard the
		// existing binding, and (2) the call sites' arg types were already unified
		// against the synthetic FuncType params during handleFuncCall, so type
		// constraints from the calls have already been captured.
		if type_system.Prune(tv) != tv {
			continue
		}

		if len(sites) == 1 {
			tv.Instance = sites[0]
		} else {
			// Deep-clone the base once and cumulatively unify all site clones
			// into it, so mutual compatibility across all sites is checked.
			allCompatible := true
			baseMapping := make(map[int]*type_system.TypeVarType)
			baseClone := c.deepCloneType(sites[0], baseMapping).(*type_system.FuncType)
			for _, site := range sites[1:] {
				siteMapping := make(map[int]*type_system.TypeVarType)
				siteClone := c.deepCloneType(site, siteMapping).(*type_system.FuncType)
				errs := c.Unify(ctx, siteClone, baseClone)
				if len(errs) > 0 {
					allCompatible = false
					break
				}
			}
			if allCompatible {
				// Trial succeeded — safe to unify originals.
				base := sites[0]
				for _, site := range sites[1:] {
					c.Unify(ctx, site, base)
				}
				tv.Instance = base
			} else if merged := c.tryMergeCallSitesWithOptionalParams(ctx, sites); merged != nil {
				tv.Instance = merged
			} else {
				// Create an intersection of all call site FuncTypes (overloaded function).
				types := make([]type_system.Type, len(sites))
				for i, s := range sites {
					types[i] = s
				}
				tv.Instance = type_system.NewIntersectionType(nil, types...)
			}
		}
	}
	// Clear processed call sites.
	callSites := make(map[int][]*type_system.FuncType)
	callSiteTypeVars := make(map[int]*type_system.TypeVarType)
	*ctx.CallSites = callSites
	*ctx.CallSiteTypeVars = callSiteTypeVars
}

// finalizeOpenObject recursively finalizes an open ObjectType's mutability.
// It checks all properties for Written flags (including nested open objects)
// and wraps written nested objects in `mut`. Returns true if any property in
// the tree was written to.
//
// Invariant: open object property values are always TypeVars (created by
// newOpenObjectWithProperty) and are never pre-wrapped in MutType —
// nothing in the inference path produces MutType-wrapped open objects
// on property values.
func finalizeOpenObject(openObj *type_system.ObjectType) bool {
	hasWritten := false
	for _, elem := range openObj.Elems {
		prop, ok := elem.(*type_system.PropertyElem)
		if !ok {
			continue
		}
		// Recurse into nested open objects. The property's Value is the
		// widenable TypeVar created by newOpenObjectWithProperty; if any
		// chained access bound it to another open object, that object is
		// the recursive target.
		valPruned := type_system.Prune(prop.Value)
		if nestedObj, ok := valPruned.(*type_system.ObjectType); ok && nestedObj.Open {
			if finalizeOpenObject(nestedObj) {
				// Per the invariant documented above, prop.Value is always a
				// TypeVarType. Comma-ok rather than a bare assertion so an
				// invariant break surfaces as a clear no-op + (eventually) a
				// downstream type error rather than a cryptic panic.
				if tv, ok := prop.Value.(*type_system.TypeVarType); ok {
					tv.Instance = &type_system.MutType{
						Type: nestedObj,
					}
					// Nested writes propagate upward: the containing object
					// is also considered written to.
					hasWritten = true
				}
			}
		}
		if prop.Written {
			hasWritten = true
		}
	}
	return hasWritten
}

// GeneralizeFuncType finds unresolved type variables in a function's signature
// and converts them into proper type parameters. This must be called after type
// inference completes for the function body.
func GeneralizeFuncType(funcType *type_system.FuncType) {
	generalizeFuncTypes([]*type_system.FuncType{funcType}, nil)
}

// GeneralizeFuncTypeWithEnv is like GeneralizeFuncType, but excludes any
// unresolved type variables in `envTVs` (free type variables of the enclosing
// type environment) from generalization. Captured outer-function TVs must
// stay unresolved so the outer function continues to own them — generalizing
// or simplifying them on the inner function would leak the outer scope.
func GeneralizeFuncTypeWithEnv(funcType *type_system.FuncType, envTVs set.Set[int]) {
	generalizeFuncTypes([]*type_system.FuncType{funcType}, envTVs)
}

// CollectEnvUnresolvedTypeVars walks the scope chain rooted at `scope` and
// collects the set of unresolved TypeVar IDs reachable from every in-scope
// value binding's type. These are the TVs that belong to outer (or sibling)
// constructs and must NOT be generalized by an inner function that captures
// them.
func CollectEnvUnresolvedTypeVars(scope *Scope) set.Set[int] {
	envTVs := set.NewSet[int]()
	if scope == nil {
		return envTVs
	}
	vars := map[int]*type_system.TypeVarType{}
	// `collectUnresolvedTypeVars` requires a non-nil *[]int but its order
	// information is irrelevant here — we only need the set of IDs.
	var orderSink []int
	for s := scope; s != nil; s = s.Parent {
		if s.Namespace == nil {
			continue
		}
		for _, binding := range s.Namespace.Values {
			if binding == nil {
				continue
			}
			collectUnresolvedTypeVars(binding.Type, vars, &orderSink)
		}
	}
	for id := range vars {
		envTVs.Add(id)
	}
	return envTVs
}

// generalizeFuncTypes generalizes a group of function types together. Unresolved
// type variables shared across the group (e.g. introduced by mutually recursive
// calls within a strongly connected component) are assigned a single TypeParam
// shared by every function whose signature references them, ensuring all
// references resolve to the same generalized name rather than leaking a free
// type var from one declaration into another.
//
// For a single FuncType, this behaves identically to GeneralizeFuncType.
//
// `excluded` is an optional set of unresolved TypeVar IDs that must NOT be
// generalized — typically the free type variables of the enclosing type
// environment. TVs in `excluded` are left unresolved and are not subject to
// the throws-only-default-to-never or return-only-simplify-to-void
// transformations either. A nil `excluded` means "no exclusions".
func generalizeFuncTypes(funcTypes []*type_system.FuncType, excluded set.Set[int]) {
	isExcluded := func(id int) bool {
		if excluded == nil {
			return false
		}
		return excluded.Contains(id)
	}
	// Finalize open object mutability for each func. If any property on an
	// open object was written during inference, wrap the object in `mut`.
	for _, funcType := range funcTypes {
		for _, param := range funcType.Params {
			tv, ok := param.Type.(*type_system.TypeVarType)
			if !ok {
				continue
			}
			pruned := type_system.Prune(tv)
			openObj, ok := pruned.(*type_system.ObjectType)
			if !ok || !openObj.Open {
				continue
			}
			if finalizeOpenObject(openObj) {
				tv.Instance = &type_system.MutType{
					Type: openObj,
				}
			}
		}
	}

	// Collect param vars and return vars separately for each function so we
	// can detect type vars that appear only as a top-level return — those
	// can be simplified to `void` rather than generalized, since the
	// function provably can never produce a value of that type (the type
	// param would be unobservable from any caller).
	type funcVars struct {
		paramVars   map[int]*type_system.TypeVarType
		paramOrder  []int
		returnVars  map[int]*type_system.TypeVarType
		returnOrder []int
	}
	perFunc := make([]funcVars, len(funcTypes))
	// sigVars / sigOrder together form the deduplicated union of every
	// unresolved TV reachable from any function's *signature surface*
	// (params + return) across the whole group. Throws-position vars are
	// tracked separately below — keeping them out of sigVars is what lets
	// the throws-only-default-to-never check distinguish "appears in a
	// signature somewhere" from "appears only in throws".
	//
	// sigVars: id → TV pointer, for O(1) "is this id in a signature?" lookups.
	// sigOrder: insertion order of those ids (first-seen across the iteration
	// of funcTypes, then params before return within each function). The two
	// always agree on membership; sigOrder exists purely so the downstream
	// `T0`, `T1`, ... naming and per-function TypeParams append order are
	// deterministic instead of map-iteration-order-dependent.
	sigVars := map[int]*type_system.TypeVarType{}
	sigOrder := []int{}
	paramVarIDs := set.NewSet[int]()
	addToSig := func(id int, tv *type_system.TypeVarType) {
		if _, seen := sigVars[id]; !seen {
			sigVars[id] = tv
			sigOrder = append(sigOrder, id)
		}
	}
	for i, funcType := range funcTypes {
		fv := funcVars{
			paramVars:  map[int]*type_system.TypeVarType{},
			returnVars: map[int]*type_system.TypeVarType{},
		}
		for _, param := range funcType.Params {
			collectUnresolvedTypeVars(param.Type, fv.paramVars, &fv.paramOrder)
		}
		collectUnresolvedTypeVars(funcType.Return, fv.returnVars, &fv.returnOrder)
		perFunc[i] = fv
		for _, id := range fv.paramOrder {
			paramVarIDs.Add(id)
			addToSig(id, fv.paramVars[id])
		}
		for _, id := range fv.returnOrder {
			addToSig(id, fv.returnVars[id])
		}
	}

	// Collect throws vars across the group and default any throws-only vars
	// (not appearing in any function's params or return) to never.
	for _, funcType := range funcTypes {
		throwsVars := map[int]*type_system.TypeVarType{}
		throwsOrder := []int{}
		collectUnresolvedTypeVars(funcType.Throws, throwsVars, &throwsOrder)
		for id, tv := range throwsVars {
			if isExcluded(id) {
				continue
			}
			if _, inParamsOrReturn := sigVars[id]; !inParamsOrReturn {
				tv.Instance = type_system.NewNeverType(nil)
			}
		}
	}

	if len(sigVars) == 0 {
		return
	}

	// A type var simplifies to `void` iff it never appears in any param
	// (anywhere — top-level or nested) and, in every function that
	// references it, it IS the top-level return type. In that case the
	// only path the function has to produce a value of that type is
	// divergent (recursion through peers in the SCC, throws, etc.), so
	// the generalized type parameter is unobservable and `void` is the
	// honest signature.
	//
	// This applies to single-function inputs as well as SCCs: a lone
	// function whose return type stays a free TV after inference (e.g.
	// the body only throws and never returns) collapses to `void` rather
	// than producing a phantom `T0` no caller can supply or observe.
	simplifyToVoid := set.NewSet[int]()
	for _, id := range sigOrder {
		if isExcluded(id) {
			continue
		}
		if paramVarIDs.Contains(id) {
			continue
		}
		allTopLevel := true
		for i, funcType := range funcTypes {
			fv := perFunc[i]
			if _, inReturn := fv.returnVars[id]; !inReturn {
				continue
			}
			pruned := type_system.Prune(funcType.Return)
			tv, ok := pruned.(*type_system.TypeVarType)
			if !ok || tv.ID != id {
				allTopLevel = false
				break
			}
		}
		if allTopLevel {
			simplifyToVoid.Add(id)
		}
	}
	// Before binding `void` to the TV's Instance, rescue any function
	// whose Throws field is exactly that TV: replace it with `never` so
	// the throws position doesn't inherit the void from the shared TV.
	// (Return position legitimately wants void; throws position wants
	// never — these can't both be expressed via TV.Instance, so the
	// throws field is rewritten directly.)
	for _, funcType := range funcTypes {
		throwsTV, ok := type_system.Prune(funcType.Throws).(*type_system.TypeVarType)
		if !ok {
			continue
		}
		if simplifyToVoid.Contains(throwsTV.ID) {
			funcType.Throws = type_system.NewNeverType(nil)
		}
	}
	for id := range simplifyToVoid {
		sigVars[id].Instance = type_system.NewVoidType(nil)
	}

	// Seed name collisions from every function's existing type params so we
	// pick a unique T-name across the group.
	existingNames := set.NewSet[string]()
	for _, funcType := range funcTypes {
		for _, tp := range funcType.TypeParams {
			existingNames.Add(tp.Name)
		}
	}

	// One TypeParam per unique unresolved type var across the group (minus
	// those simplified to void). The TypeRefType bound to the type var
	// resolves by name in whichever function it appears in, and each
	// function's TypeParams list below carries a reference to the same
	// TypeParam object.
	typeParams := make(map[int]*type_system.TypeParam, len(sigVars))
	nameIndex := 0
	for _, id := range sigOrder {
		if simplifyToVoid.Contains(id) {
			continue
		}
		if isExcluded(id) {
			continue
		}
		tv := sigVars[id]
		name := fmt.Sprintf("T%d", nameIndex)
		for existingNames.Contains(name) {
			nameIndex++
			name = fmt.Sprintf("T%d", nameIndex)
		}
		nameIndex++
		existingNames.Add(name)
		tp := &type_system.TypeParam{
			Name:       name,
			Constraint: tv.Constraint,
			Default:    tv.Default,
		}
		typeParams[id] = tp

		// Bind the TV in place rather than walking each function's type
		// tree to substitute references. Every consumer of a type goes
		// through Prune, which follows tv.Instance — so a single mutation
		// here updates every reachable reference (params, return, throws,
		// nested generics, peers in the SCC) atomically and without us
		// having to enumerate them. A real substitution pass would also
		// have to handle structural sharing (the same TV appears in
		// multiple positions), avoid re-cloning unchanged subtrees, and
		// stay in sync with collectUnresolvedTypeVars' traversal — all
		// of which Prune-chasing gives us for free.
		tv.Instance = type_system.NewTypeRefType(nil, name, &type_system.TypeAlias{
			Type:        type_system.NewUnknownType(nil),
			TypeParams:  []*type_system.TypeParam{},
			IsTypeParam: true,
		})
	}

	// Append the relevant type params to each function in that function's
	// own insertion order (params first, then return), skipping vars that
	// were simplified to void and avoiding duplicates when a var appears
	// in both params and return.
	for i, funcType := range funcTypes {
		fv := perFunc[i]
		added := set.NewSet[int]()
		var newTypeParams []*type_system.TypeParam
		appendVar := func(id int) {
			if simplifyToVoid.Contains(id) || added.Contains(id) || isExcluded(id) {
				return
			}
			added.Add(id)
			newTypeParams = append(newTypeParams, typeParams[id])
		}
		for _, id := range fv.paramOrder {
			appendVar(id)
		}
		for _, id := range fv.returnOrder {
			appendVar(id)
		}
		funcType.TypeParams = append(funcType.TypeParams, newTypeParams...)
	}
}

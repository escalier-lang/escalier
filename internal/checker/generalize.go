package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// collectUnresolvedTypeVars walks a type tree and collects all unresolved
// TypeVarType nodes (where Prune returns the same TypeVarType). Results are
// stored in the vars map keyed by type var ID, and order tracks insertion order.
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
	case *type_system.MutabilityType:
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
		varMapping[t.ID] = fresh
		if t.Constraint != nil {
			fresh.Constraint = c.deepCloneType(t.Constraint, varMapping)
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
	case *type_system.MutabilityType:
		return &type_system.MutabilityType{
			Type:       c.deepCloneType(t.Type, varMapping),
			Mutability: t.Mutability,
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
					Name:    e.Name,
					Fn:      c.deepCloneType(e.Fn, varMapping).(*type_system.FuncType),
					MutSelf: e.MutSelf,
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

// GeneralizeFuncType finds unresolved type variables in a function's signature
// and converts them into proper type parameters. This must be called after type
// inference completes for the function body.
func GeneralizeFuncType(funcType *type_system.FuncType) {
	vars := map[int]*type_system.TypeVarType{}
	order := []int{}

	// Collect from params and return type
	for _, param := range funcType.Params {
		collectUnresolvedTypeVars(param.Type, vars, &order)
	}
	collectUnresolvedTypeVars(funcType.Return, vars, &order)

	// Collect from throws separately to detect throws-only type vars
	throwsVars := map[int]*type_system.TypeVarType{}
	throwsOrder := []int{}
	collectUnresolvedTypeVars(funcType.Throws, throwsVars, &throwsOrder)

	// If the throws type is an unresolved type var not referenced by params or
	// return, default it to never instead of generalizing it.
	for id, tv := range throwsVars {
		if _, inParamsOrReturn := vars[id]; !inParamsOrReturn {
			tv.Instance = type_system.NewNeverType(nil)
		}
	}

	if len(vars) == 0 {
		return
	}

	// Collect existing type param names to avoid collisions
	existingNames := map[string]bool{}
	for _, tp := range funcType.TypeParams {
		existingNames[tp.Name] = true
	}

	// Create type params and bind each unresolved type var
	newTypeParams := make([]*type_system.TypeParam, len(order))
	nameIndex := 0
	for i, id := range order {
		tv := vars[id]
		name := fmt.Sprintf("T%d", nameIndex)
		for existingNames[name] {
			nameIndex++
			name = fmt.Sprintf("T%d", nameIndex)
		}
		nameIndex++
		existingNames[name] = true
		tp := &type_system.TypeParam{
			Name:       name,
			Constraint: tv.Constraint,
			Default:    tv.Default,
		}
		newTypeParams[i] = tp

		// Bind the type var to a TypeRefType referencing the new type param.
		// All existing references to this type var will resolve via Prune.
		tv.Instance = type_system.NewTypeRefType(nil, name, &type_system.TypeAlias{
			Type:       type_system.NewUnknownType(nil),
			TypeParams: []*type_system.TypeParam{},
		})
	}

	funcType.TypeParams = append(funcType.TypeParams, newTypeParams...)
}

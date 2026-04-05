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
	default:
		// Leaf types (LitType, PrimType, NeverType, VoidType, etc.)
		// contain no TypeVars and are never mutated by Unify.
		return t
	}
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
		if type_system.Prune(tv) != tv {
			continue
		}

		if len(sites) == 1 {
			tv.Instance = sites[0]
		} else {
			// Deep-clone each site for trial unification so that Unify cannot
			// mutate TypeVarType instances shared with the originals.
			allCompatible := true
			for _, site := range sites[1:] {
				baseMapping := make(map[int]*type_system.TypeVarType)
				siteMapping := make(map[int]*type_system.TypeVarType)
				baseClone := c.deepCloneType(sites[0], baseMapping).(*type_system.FuncType)
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

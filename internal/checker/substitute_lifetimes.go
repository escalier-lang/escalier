package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

// SubstituteLifetimes walks t and replaces every LifetimeVar reference
// whose ID is a key in substs with the corresponding replacement
// Lifetime. The structure of t is preserved; only Lifetime/LifetimeArgs
// fields and FuncType.LifetimeParams (if rebinding internally) change.
//
// This is the lifetime analog of SubstituteTypeParams. It is used at
// generic-function instantiation sites to give each call its own fresh
// lifetime variables, so that bindings made for one call do not leak
// into another (see Phase 9.2).
//
// The walker is intentionally a small bespoke recursion rather than a
// TypeVisitor: lifetime substitution touches Lifetime fields that live
// alongside types but are not themselves visited as Types, and the
// existing visitor's ExitType signature has no notion of "rebuild
// because a non-Type child changed."
func SubstituteLifetimes[T type_system.Type](t T, substs map[int]type_system.Lifetime) T {
	if len(substs) == 0 {
		return t
	}
	// The final cast back to T is safe for the only current caller
	// (instantiateGenericFunc, which passes *FuncType) because
	// substituteLifetimesInFunc always returns a *FuncType. If a future
	// caller passes a Union- or Intersection-typed T, those branches
	// return type_system.Type rather than the concrete pointer type, and
	// this cast would panic — re-evaluate when adding new callers.
	return substituteLifetimesInType(t, substs).(T)
}

func substituteLifetimesInType(t type_system.Type, substs map[int]type_system.Lifetime) type_system.Type {
	if t == nil {
		return nil
	}
	t = type_system.Prune(t)
	switch ty := t.(type) {
	case *type_system.TypeRefType:
		return substituteLifetimesInTypeRef(ty, substs)
	case *type_system.ObjectType:
		return substituteLifetimesInObject(ty, substs)
	case *type_system.TupleType:
		return substituteLifetimesInTuple(ty, substs)
	case *type_system.MutType:
		inner := substituteLifetimesInType(ty.Type, substs)
		if inner == ty.Type {
			return ty
		}
		return type_system.NewMutType(nil, inner)
	case *type_system.FuncType:
		return substituteLifetimesInFunc(ty, substs)
	case *type_system.UnionType:
		return substituteLifetimesInUnion(ty, substs)
	case *type_system.IntersectionType:
		return substituteLifetimesInIntersection(ty, substs)
	default:
		return t
	}
}

func substituteLifetime(lt type_system.Lifetime, substs map[int]type_system.Lifetime) type_system.Lifetime {
	if lt == nil {
		return nil
	}
	switch v := lt.(type) {
	case *type_system.LifetimeVar:
		if repl, ok := substs[v.ID]; ok {
			return repl
		}
		return v
	case *type_system.LifetimeUnion:
		out := make([]type_system.Lifetime, len(v.Lifetimes))
		changed := false
		for i, m := range v.Lifetimes {
			nm := substituteLifetime(m, substs)
			out[i] = nm
			if nm != m {
				changed = true
			}
		}
		if !changed {
			return v
		}
		return &type_system.LifetimeUnion{Lifetimes: out}
	default:
		return lt
	}
}

func substituteLifetimesInTypeRef(t *type_system.TypeRefType, substs map[int]type_system.Lifetime) *type_system.TypeRefType {
	newArgs := make([]type_system.Type, len(t.TypeArgs))
	argsChanged := false
	for i, a := range t.TypeArgs {
		na := substituteLifetimesInType(a, substs)
		newArgs[i] = na
		if na != a {
			argsChanged = true
		}
	}
	newLifetime := substituteLifetime(t.Lifetime, substs)
	newLifetimeArgs := make([]type_system.Lifetime, len(t.LifetimeArgs))
	ltArgsChanged := false
	for i, lt := range t.LifetimeArgs {
		nlt := substituteLifetime(lt, substs)
		newLifetimeArgs[i] = nlt
		if nlt != lt {
			ltArgsChanged = true
		}
	}
	if !argsChanged && newLifetime == t.Lifetime && !ltArgsChanged {
		return t
	}
	r := type_system.NewTypeRefTypeFromQualIdent(nil, t.Name, t.TypeAlias, newArgs...)
	r.Lifetime = newLifetime
	if len(newLifetimeArgs) > 0 {
		r.LifetimeArgs = newLifetimeArgs
	}
	return r
}

func substituteLifetimesInObject(t *type_system.ObjectType, substs map[int]type_system.Lifetime) *type_system.ObjectType {
	newLifetime := substituteLifetime(t.Lifetime, substs)
	newElems := make([]type_system.ObjTypeElem, len(t.Elems))
	elemsChanged := false
	for i, elem := range t.Elems {
		ne := substituteLifetimesInObjElem(elem, substs)
		newElems[i] = ne
		if ne != elem {
			elemsChanged = true
		}
	}
	if !elemsChanged && newLifetime == t.Lifetime {
		return t
	}
	r := type_system.NewObjectType(nil, newElems)
	r.ID = t.ID
	r.Exact = t.Exact
	r.Immutable = t.Immutable
	r.Mutable = t.Mutable
	r.Nominal = t.Nominal
	r.Interface = t.Interface
	r.Extends = t.Extends
	r.Implements = t.Implements
	r.SymbolKeyMap = t.SymbolKeyMap
	r.Open = t.Open
	r.MatchedUnionMembers = t.MatchedUnionMembers
	r.Lifetime = newLifetime
	return r
}

func substituteLifetimesInObjElem(elem type_system.ObjTypeElem, substs map[int]type_system.Lifetime) type_system.ObjTypeElem {
	switch e := elem.(type) {
	case *type_system.PropertyElem:
		newValue := substituteLifetimesInType(e.Value, substs)
		if newValue == e.Value {
			return e
		}
		return &type_system.PropertyElem{
			Name:     e.Name,
			Value:    newValue,
			Optional: e.Optional,
			Readonly: e.Readonly,
		}
	case *type_system.MethodElem:
		newFn, _ := substituteLifetimesInType(e.Fn, substs).(*type_system.FuncType)
		if newFn == e.Fn {
			return e
		}
		return &type_system.MethodElem{
			Name:    e.Name,
			Fn:      newFn,
			MutSelf: e.MutSelf,
		}
	default:
		// TODO(#548): handle CallableElem, ConstructorElem, GetterElem,
		// SetterElem, MappedElem, IndexSignatureElem, RestSpreadElem.
		// These can carry lifetime-bearing inner types but are passed
		// through unchanged today; add cases as future phases need them.
		return elem
	}
}

func substituteLifetimesInTuple(t *type_system.TupleType, substs map[int]type_system.Lifetime) *type_system.TupleType {
	newElems := make([]type_system.Type, len(t.Elems))
	changed := false
	for i, e := range t.Elems {
		ne := substituteLifetimesInType(e, substs)
		newElems[i] = ne
		if ne != e {
			changed = true
		}
	}
	newLifetime := substituteLifetime(t.Lifetime, substs)
	if !changed && newLifetime == t.Lifetime {
		return t
	}
	r := type_system.NewTupleType(nil, newElems...)
	r.Lifetime = newLifetime
	return r
}

func substituteLifetimesInFunc(t *type_system.FuncType, substs map[int]type_system.Lifetime) *type_system.FuncType {
	// A nested FuncType may bind some of the same lifetime names. Mask
	// any LifetimeParam IDs that the inner function declares — those
	// must not be replaced by the outer substitution.
	innerSubsts := substs
	if len(t.LifetimeParams) > 0 {
		innerSubsts = make(map[int]type_system.Lifetime, len(substs))
		for k, v := range substs {
			innerSubsts[k] = v
		}
		for _, lp := range t.LifetimeParams {
			delete(innerSubsts, lp.ID)
		}
	}

	newParams := make([]*type_system.FuncParam, len(t.Params))
	paramsChanged := false
	for i, p := range t.Params {
		nt := substituteLifetimesInType(p.Type, innerSubsts)
		if nt != p.Type {
			paramsChanged = true
			newParams[i] = &type_system.FuncParam{
				Pattern:  p.Pattern,
				Type:     nt,
				Optional: p.Optional,
			}
		} else {
			newParams[i] = p
		}
	}
	var newReturn type_system.Type
	if t.Return != nil {
		newReturn = substituteLifetimesInType(t.Return, innerSubsts)
	}
	var newThrows type_system.Type
	if t.Throws != nil {
		newThrows = substituteLifetimesInType(t.Throws, innerSubsts)
	}
	if !paramsChanged && newReturn == t.Return && newThrows == t.Throws {
		return t
	}
	r := type_system.NewFuncType(nil, t.TypeParams, newParams, newReturn, newThrows)
	r.LifetimeParams = t.LifetimeParams
	return r
}

func substituteLifetimesInUnion(t *type_system.UnionType, substs map[int]type_system.Lifetime) type_system.Type {
	newTypes := make([]type_system.Type, len(t.Types))
	changed := false
	for i, m := range t.Types {
		nm := substituteLifetimesInType(m, substs)
		newTypes[i] = nm
		if nm != m {
			changed = true
		}
	}
	if !changed {
		return t
	}
	return type_system.NewUnionType(nil, newTypes...)
}

func substituteLifetimesInIntersection(t *type_system.IntersectionType, substs map[int]type_system.Lifetime) type_system.Type {
	newTypes := make([]type_system.Type, len(t.Types))
	changed := false
	for i, m := range t.Types {
		nm := substituteLifetimesInType(m, substs)
		newTypes[i] = nm
		if nm != m {
			changed = true
		}
	}
	if !changed {
		return t
	}
	return type_system.NewIntersectionType(nil, newTypes...)
}

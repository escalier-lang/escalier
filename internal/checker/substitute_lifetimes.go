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
// Implementation: a TypeVisitor handles the structural recursion through
// types and ObjTypeElems. EnterType injects substituted Lifetime fields
// on TypeRefType / ObjectType / TupleType by returning a replacement
// node (the visitor's auto-rebuild on child changes preserves Lifetime
// when copying). FuncType's LifetimeParams are tracked via a shadow
// stack so an inner function's parameter masks any matching outer subst.
func SubstituteLifetimes[T type_system.Type](t T, substs map[int]type_system.Lifetime) T {
	if len(substs) == 0 {
		return t
	}
	v := &lifetimeSubstVisitor{substs: substs}
	// The cast is safe for the only current caller (instantiateGenericFunc,
	// passing *FuncType) because every visited concrete type returns its
	// own kind. UnionType / IntersectionType passed at the top level would
	// also be preserved by the visitor's rebuild, but a caller passing T =
	// *UnionType receives Type from Accept and the assertion would still
	// match — re-evaluate if a future caller uses a less specific T.
	return t.Accept(v).(T)
}

type lifetimeSubstVisitor struct {
	substs      map[int]type_system.Lifetime
	shadowStack []map[int]bool
}

func (v *lifetimeSubstVisitor) EnterType(t type_system.Type) type_system.EnterResult {
	switch ty := t.(type) {
	case *type_system.TypeRefType:
		newLifetime := v.substituteLifetime(ty.Lifetime)
		newLifetimeArgs, ltArgsChanged := v.substituteLifetimeSlice(ty.LifetimeArgs)
		if newLifetime == ty.Lifetime && !ltArgsChanged {
			return type_system.EnterResult{}
		}
		r := *ty
		r.Lifetime = newLifetime
		r.LifetimeArgs = newLifetimeArgs
		return type_system.EnterResult{Type: &r}
	case *type_system.ObjectType:
		newLifetime := v.substituteLifetime(ty.Lifetime)
		if newLifetime == ty.Lifetime {
			return type_system.EnterResult{}
		}
		r := *ty
		r.Lifetime = newLifetime
		return type_system.EnterResult{Type: &r}
	case *type_system.TupleType:
		newLifetime := v.substituteLifetime(ty.Lifetime)
		if newLifetime == ty.Lifetime {
			return type_system.EnterResult{}
		}
		r := *ty
		r.Lifetime = newLifetime
		return type_system.EnterResult{Type: &r}
	case *type_system.FuncType:
		// A nested FuncType may bind some of the same lifetime names.
		// Mask any LifetimeParam IDs that the inner function declares —
		// those must not be replaced by the outer substitution.
		if len(ty.LifetimeParams) > 0 {
			frame := make(map[int]bool, len(ty.LifetimeParams))
			for _, lp := range ty.LifetimeParams {
				frame[lp.ID] = true
			}
			v.shadowStack = append(v.shadowStack, frame)
		} else {
			// Push an empty frame so ExitType's pop is symmetric.
			v.shadowStack = append(v.shadowStack, nil)
		}
	}
	return type_system.EnterResult{}
}

func (v *lifetimeSubstVisitor) ExitType(t type_system.Type) type_system.Type {
	if _, ok := t.(*type_system.FuncType); ok {
		v.shadowStack = v.shadowStack[:len(v.shadowStack)-1]
	}
	return nil
}

// substituteLifetime returns the replacement for lt, honoring shadowing
// from any enclosing FuncType.LifetimeParams. Returns lt unchanged when
// no substitution applies.
func (v *lifetimeSubstVisitor) substituteLifetime(lt type_system.Lifetime) type_system.Lifetime {
	if lt == nil {
		return nil
	}
	switch x := lt.(type) {
	case *type_system.LifetimeVar:
		for _, frame := range v.shadowStack {
			if frame[x.ID] {
				return x
			}
		}
		if repl, ok := v.substs[x.ID]; ok {
			return repl
		}
		return x
	case *type_system.LifetimeUnion:
		out := make([]type_system.Lifetime, len(x.Lifetimes))
		changed := false
		for i, m := range x.Lifetimes {
			nm := v.substituteLifetime(m)
			out[i] = nm
			if nm != m {
				changed = true
			}
		}
		if !changed {
			return x
		}
		return &type_system.LifetimeUnion{Lifetimes: out}
	default:
		return lt
	}
}

// substituteLifetimeSlice maps substituteLifetime over a []Lifetime,
// returning a new slice and a `changed` flag (or the original slice
// when no element was substituted, to preserve pointer identity for
// the caller's no-op short-circuit).
//
// We need this even though the rest of the recursion is handled by
// TypeVisitor: the visitor only walks Type-typed children. Fields
// like TypeRefType.LifetimeArgs are []Lifetime, and Lifetime is a
// separate interface hierarchy that Accept does not descend into —
// so any traversal of those slots has to be done by hand.
func (v *lifetimeSubstVisitor) substituteLifetimeSlice(lts []type_system.Lifetime) ([]type_system.Lifetime, bool) {
	if len(lts) == 0 {
		return lts, false
	}
	out := make([]type_system.Lifetime, len(lts))
	changed := false
	for i, lt := range lts {
		nlt := v.substituteLifetime(lt)
		out[i] = nlt
		if nlt != lt {
			changed = true
		}
	}
	if !changed {
		return lts, false
	}
	return out, true
}

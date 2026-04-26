package type_system

// Lifetime can be either a LifetimeVar (in function signatures) or a
// LifetimeValue (after instantiation at call sites).
type Lifetime interface {
	isLifetime()
}

// LifetimeVar represents a lifetime parameter (e.g. 'a, 'b).
// During inference, Instance is nil. Once bound at a call site,
// Instance points to the concrete LifetimeValue it resolved to.
type LifetimeVar struct {
	ID       int
	Name     string         // e.g. "a", "b" (without the tick)
	Instance *LifetimeValue // nil until bound
}

func (*LifetimeVar) isLifetime() {}

// LifetimeValue represents a concrete lifetime — the "identity" of a value
// that can be aliased. Each fresh value created at a program point gets a
// unique LifetimeValue. A LifetimeValue with IsStatic=true represents
// 'static (permanently aliased, e.g. stored into a global).
type LifetimeValue struct {
	ID       int
	Name     string // lvalue path for diagnostics (e.g. "items", "obj.values", "obj[key]")
	IsStatic bool   // true for 'static
}

func (*LifetimeValue) isLifetime() {}

// LifetimeUnion represents a value that may carry one of several lifetimes.
// Used when a function returns one of multiple parameters depending on
// control flow — e.g. `('a | 'b) Point` for `if cond { a } else { b }`.
// At call sites, the result is added to the alias sets of all corresponding
// arguments.
type LifetimeUnion struct {
	Lifetimes []Lifetime
}

func (*LifetimeUnion) isLifetime() {}

// PruneLifetime resolves a lifetime variable to its bound value, following
// the Instance pointer on LifetimeVar. Analogous to Prune for types.
// Returns the lifetime unchanged if it is nil, a LifetimeValue, or an
// unbound LifetimeVar. A nil untyped Lifetime is safe here because the
// type assertion will fail for a nil interface, returning lt unchanged.
//
// For a LifetimeUnion, each member is pruned recursively. The union itself
// is not collapsed even when all members resolve to the same value, because
// downstream code distinguishes "single source" from "multi-source" based
// on the wrapper type (a LifetimeUnion always means the value may alias
// any of its members).
func PruneLifetime(lt Lifetime) Lifetime {
	switch v := lt.(type) {
	case *LifetimeVar:
		if v.Instance != nil {
			return v.Instance
		}
		return v
	case *LifetimeUnion:
		pruned := make([]Lifetime, len(v.Lifetimes))
		changed := false
		for i, m := range v.Lifetimes {
			pm := PruneLifetime(m)
			pruned[i] = pm
			if pm != m {
				changed = true
			}
		}
		if !changed {
			return v
		}
		return &LifetimeUnion{Lifetimes: pruned}
	default:
		return lt
	}
}

// GetLifetime extracts the lifetime from a type, walking through wrapper
// types as needed. Returns nil if the type carries no lifetime.
func GetLifetime(t Type) Lifetime {
	switch ty := t.(type) {
	case *TypeRefType:
		return ty.Lifetime
	case *ObjectType:
		return ty.Lifetime
	case *TupleType:
		return ty.Lifetime
	case *MutType:
		return GetLifetime(ty.Type)
	case *UnionType:
		// Returns the common lifetime if all member types share the same
		// lifetime; returns nil if they differ. Comparison uses pointer
		// identity (!=) because lifetime values are always pointer-shared:
		// a single LifetimeVar or LifetimeValue is assigned to all types
		// that share it, matching how type variables work elsewhere.
		var common Lifetime
		for i, member := range ty.Types {
			lt := GetLifetime(member)
			if i == 0 {
				common = lt
			} else if lt != common {
				return nil
			}
		}
		return common
	case *IntersectionType:
		var common Lifetime
		for i, member := range ty.Types {
			lt := GetLifetime(member)
			if i == 0 {
				common = lt
			} else if lt != common {
				return nil
			}
		}
		return common
	default:
		// PrimType, LitType, VoidType, NeverType, etc. — no lifetime
		return nil
	}
}

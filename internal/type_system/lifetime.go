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
	Name     string // variable name for diagnostics (e.g. "items")
	IsStatic bool   // true for 'static
}

func (*LifetimeValue) isLifetime() {}

// PruneLifetime resolves a lifetime variable to its bound value, following
// the Instance pointer on LifetimeVar. Analogous to Prune for types.
// Returns the lifetime unchanged if it is nil, a LifetimeValue, or an
// unbound LifetimeVar. A nil untyped Lifetime is safe here because the
// type assertion will fail for a nil interface, returning lt unchanged.
func PruneLifetime(lt Lifetime) Lifetime {
	if lv, ok := lt.(*LifetimeVar); ok && lv.Instance != nil {
		return lv.Instance
	}
	return lt
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
	case *MutabilityType:
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

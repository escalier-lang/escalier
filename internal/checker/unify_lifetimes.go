package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// UnifyLifetimes reconciles two lifetime annotations during unification
// (Phase 9.1, 9.3). It implements the following table after PruneLifetime
// resolves both sides through any LifetimeVar.Instance chains:
//
//	nil           , nil           -> success
//	nil           , X             -> success (no constraint)
//	X             , nil           -> success
//	LifetimeVar   , Lifetime      -> bind v.Instance = Lifetime
//	Lifetime      , LifetimeVar   -> bind v.Instance = Lifetime (with same-id no-op)
//	LifetimeValue , LifetimeValue -> success if same ID, or if either is 'static
//	                                 (the other is upgraded to 'static); otherwise
//	                                 a LifetimeMismatchError
//	LifetimeUnion , X             -> each member unifies with X
//	X             , LifetimeUnion -> X unifies with each member
//
// Binding mutates LifetimeVar.Instance directly (mirroring how TypeVar
// binding works in bind()). The caller is responsible for calling
// UnifyLifetimes only after the surrounding type unification has
// otherwise succeeded — a unification error on the type carrier will
// suppress lifetime errors via the standard error-cascade rules.
func (c *Checker) UnifyLifetimes(ctx Context, l1, l2 type_system.Lifetime) []Error {
	if l1 == nil && l2 == nil {
		return nil
	}
	// A nil lifetime on one side means "no constraint" — propagation is
	// handled implicitly by leaving the other side untouched. This makes
	// `mut Point` (no lifetime) compatible with `mut 'a Point` (lifetime
	// flows back to the caller untouched).
	if l1 == nil || l2 == nil {
		return nil
	}

	l1 = type_system.PruneLifetime(l1)
	l2 = type_system.PruneLifetime(l2)

	// Reflexive identity (after Prune the same Var/Value is pointer-shared).
	if l1 == l2 {
		return nil
	}

	// LifetimeUnion handling: distribute over members. A union on one
	// side must unify with the other side as a whole — for each member,
	// the constraint applies. This is how multi-source returns
	// (e.g. `('a | 'b) Point`) propagate to all source alias sets at a
	// call site.
	if u, ok := l1.(*type_system.LifetimeUnion); ok {
		var errors []Error
		for _, m := range u.Lifetimes {
			errors = append(errors, c.UnifyLifetimes(ctx, m, l2)...)
		}
		return errors
	}
	if u, ok := l2.(*type_system.LifetimeUnion); ok {
		var errors []Error
		for _, m := range u.Lifetimes {
			errors = append(errors, c.UnifyLifetimes(ctx, l1, m)...)
		}
		return errors
	}

	// Var on one or both sides: bind. Equating two free Vars is a
	// directed binding (l1.Instance = l2). Subsequent prunes resolve
	// through the chain.
	if v1, ok := l1.(*type_system.LifetimeVar); ok {
		v1.Instance = l2
		return nil
	}
	if v2, ok := l2.(*type_system.LifetimeVar); ok {
		v2.Instance = l1
		return nil
	}

	// Value vs Value.
	val1, ok1 := l1.(*type_system.LifetimeValue)
	val2, ok2 := l2.(*type_system.LifetimeValue)
	if ok1 && ok2 {
		if val1.ID == val2.ID {
			return nil
		}
		// 'static absorbs concrete values: a function parameter declared
		// with `'static` may bind to any caller value, but the caller
		// must observe the value as permanently aliased. Symmetrically,
		// passing a `'static` value where a free lifetime expects a
		// concrete value is fine — the caller sees a `'static` binding.
		if val1.IsStatic || val2.IsStatic {
			return nil
		}
		// Two independent values with the same shared lifetime variable
		// requirement — the function asked for one identity but got two.
		return []Error{LifetimeMismatchError{L1: val1, L2: val2}}
	}

	// Defensive: any other shape (e.g. unknown future Lifetime variant)
	// — succeed silently rather than crash. PruneLifetime already
	// resolved Vars, and Unions were distributed above.
	return nil
}

// LifetimeMismatchError is reported when unification requires two
// distinct lifetime values to be the same — typically because a
// function's signature reuses a single lifetime variable across multiple
// parameter positions and the caller passed independent values.
type LifetimeMismatchError struct {
	L1   *type_system.LifetimeValue
	L2   *type_system.LifetimeValue
	span ast.Span
}

func (e LifetimeMismatchError) isError()         {}
func (e LifetimeMismatchError) IsWarning() bool  { return false }
func (e LifetimeMismatchError) Span() ast.Span   { return e.span }
func (e LifetimeMismatchError) Message() string {
	n1 := e.L1.Name
	if n1 == "" {
		n1 = "<anonymous>"
	}
	n2 := e.L2.Name
	if n2 == "" {
		n2 = "<anonymous>"
	}
	return "lifetime mismatch: '" + n1 + " and '" + n2 +
		" are independent values but the function requires them to share a lifetime"
}

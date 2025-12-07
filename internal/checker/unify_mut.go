package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

// unifyMut performs invariant type unification where t1 and t2 must be exactly
// the same type. This is used for mutable types where we need strict type
// equality to ensure memory safety.
func (c *Checker) unifyMut(ctx Context, mut1, mut2 *type_system.MutabilityType) []Error {
	if mut1 == nil || mut2 == nil {
		panic("Cannot unify nil types")
	}

	t1 := mut1.Type
	t2 := mut2.Type

	t1 = type_system.Prune(t1)
	t2 = type_system.Prune(t2)

	// For invariant unification, the types must be exactly equal
	if type_system.Equals(t1, t2) {
		return nil
	}

	// Try expanding the types and check again if they changed
	retry := false
	expandedT1, _ := c.ExpandType(ctx, t1, 1)
	if expandedT1 != t1 {
		t1 = expandedT1
		retry = true
	}
	expandedT2, _ := c.ExpandType(ctx, t2, 1)
	if expandedT2 != t2 {
		t2 = expandedT2
		retry = true
	}

	if retry {
		// We unwrap the mutable types above so we need to rewrap them here
		// before calling `unifyMut` again.
		mut1 = type_system.NewMutableType(nil, t1)
		mut2 = type_system.NewMutableType(nil, t2)
		return c.unifyMut(ctx, mut1, mut2)
	}

	// Types are not equal, return unification error
	return []Error{&CannotUnifyTypesError{
		T1: mut1,
		T2: mut2,
	}}
}

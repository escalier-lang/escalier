package soltype

// Lifetime is the sort of borrow lifetimes — a second, non-Type sort threaded
// through RefType.Lt. A Lifetime is either a variable carrying outlives bounds
// (LifetimeVar) or 'static (StaticLifetime), the bottom of the outlives lattice.
//
// Lifetimes are solved by the same constraint machinery as types but live in
// their own sort: a RefType's Lt is a Lifetime, never a Type, and the rewriting
// visitor carries it through unchanged (only the lifetime-aware passes walk it).
// The outlives relation `'a <: 'b` ("'a outlives, i.e. lives at least as long
// as, 'b") is asserted by solver.constrainLt. 'static outlives everything, so it is
// the bottom of this order: `'static <: X` holds for every X.
type Lifetime interface{ isLifetime() }

// LifetimeVar is a lifetime inference variable. Like a TypeVarType it carries
// lower and upper bounds and is coalesced polarity-dependently: a positive
// (output) occurrence joins its lower bounds, a negative (input) occurrence
// meets its upper bounds. 'static is the bottom of the order, so it absorbs a meet:
// a negative-position variable with 'static among its upper bounds resolves to
// 'static, regardless of any other upper bound. That upper bound comes from a real
// `v <: 'static` escape constraint, the one constraint that pins v to 'static. Bound
// lists are extended ONLY through the solver's
// addLowerLtBound/addUpperLtBound helpers, mirroring the type-sort discipline so
// a discarded speculation trial truncates them back.
//
// Level brings the lifetime sort into the let-generalization hierarchy that
// TypeVarType already rides (M4 D2.5). A lifetime minted onto a generalizable
// parameter sits ABOVE its scheme's generalize-level, so instantiate freshens it
// per use just as it does a type parameter, and two call sites of a
// borrow-passing function never share one LifetimeVar's bounds. The MLsub level
// invariant extends to this sort: a LifetimeVar's Level is >= the Level of every
// lifetime in its bounds, maintained by constrainLt's level extrusion. LevelOf's
// RefType arm reads it through LevelOfLifetime so the freshener/extruder prune
// stays sound.
type LifetimeVar struct {
	ID          int
	Level       int
	LowerBounds []Lifetime
	UpperBounds []Lifetime

	// Join marks an internal lifetime minted at a multi-source join site (M4 D3).
	// A join site is a return or branch that unites several borrows with distinct
	// lifetimes. A join variable is NOT a borrow origin, so it is never named in
	// the output. It renders as the union of the param lifetimes it reaches through
	// its bounds, e.g. returning one of two borrows coalesces to `('a | 'b)`. The
	// default is a param lifetime, Join false, which originates at a borrow
	// parameter and renders under its own name. The distinction governs
	// coalesceLifetime. A param variable is kept whole, a join variable is expanded
	// to its reachable members.
	Join bool
}

// BoundsAt returns a lifetime variable's polarity-relevant bounds. In Positive
// position it returns the lower bounds, because an output joins its lower bounds.
// In Negative position it returns the upper bounds, because an input meets its
// upper bounds. This is the lifetime-sort twin of TypeVarType.BoundsAt.
func (v *LifetimeVar) BoundsAt(pol Polarity) []Lifetime {
	if pol == Positive {
		return v.LowerBounds
	}
	return v.UpperBounds
}

// StaticLifetime is 'static, the bottom of the outlives lattice: it outlives every
// lifetime, so `'static <: X` always holds. The reverse, `X <: 'static`, holds only
// for X = 'static, so asserting it forces X to 'static — the escape-to-static
// constraint.
type StaticLifetime struct{}

// LifetimeUnion is a coalescing-output-only lifetime. It is the union of the param
// lifetimes a join variable reaches, e.g. returning one of two borrows renders as
// `('a | 'b)`. It never appears as a constraint input, since constrainLt relates
// LifetimeVars and 'static only. It is minted solely by coalesceLifetime when a
// join variable expands to more than one member. A single-member expansion yields
// the member directly, so a LifetimeUnion always carries at least two lifetimes.
type LifetimeUnion struct {
	Lifetimes []Lifetime
}

func (*LifetimeUnion) isLifetime() {}

// Static is the canonical 'static value. Prefer it over a fresh
// &StaticLifetime{} at every origination site (escape-to-static, annotations) so
// all 'static share one pointer identity. ContainsLifetime dedups 'static by
// value regardless, so a stray fresh instance is still correct — the singleton is
// the cheap default, not a correctness crutch.
var Static Lifetime = &StaticLifetime{}

func (*LifetimeVar) isLifetime()    {}
func (*StaticLifetime) isLifetime() {}

// IsStaticLifetime reports whether lt is 'static.
func IsStaticLifetime(lt Lifetime) bool {
	_, ok := lt.(*StaticLifetime)
	return ok
}

// LevelOfLifetime is the lifetime-sort twin of LevelOf for a single Lifetime: a
// LifetimeVar's own Level, and 0 for 'static or a nil slot. Neither 'static nor a
// nil slot carries a quantifiable variable. LevelOf's RefType arm folds this into
// the wrapper's level so the freshener/extruder level prune accounts for a borrow's
// lifetime, not just its inner.
func LevelOfLifetime(lt Lifetime) int {
	if lv, ok := lt.(*LifetimeVar); ok {
		return lv.Level
	}
	return 0
}

// ContainsLifetime reports whether lt is already present in bounds, so a repeated
// outlives constraint does not re-append a bound. A LifetimeVar matches by pointer
// identity, but 'static matches by VALUE: every StaticLifetime denotes the one
// lattice bottom, and origination sites mint a fresh &StaticLifetime{} per call, so
// pointer identity would wrongly treat two 'static values as distinct and let a
// bound list accumulate duplicate 'static entries.
func ContainsLifetime(bounds []Lifetime, lt Lifetime) bool {
	ltStatic := IsStaticLifetime(lt)
	for _, b := range bounds {
		if b == lt {
			return true
		}
		if ltStatic && IsStaticLifetime(b) {
			return true
		}
	}
	return false
}

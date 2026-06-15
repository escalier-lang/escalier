package soltype

// Lifetime is the sort of borrow lifetimes — a second, non-Type sort threaded
// through RefType.Lt. A Lifetime is either a variable carrying outlives bounds
// (LifetimeVar) or 'static (StaticLifetime), the top of the outlives lattice.
//
// Lifetimes are solved by the same constraint machinery as types but live in
// their own sort: a RefType's Lt is a Lifetime, never a Type, and the rewriting
// visitor carries it through unchanged (only the lifetime-aware passes walk it).
// The outlives relation `'a <: 'b` ("'a outlives, i.e. lives at least as long
// as, 'b") is asserted by solver.constrainLt; 'static outlives everything.
type Lifetime interface{ isLifetime() }

// LifetimeVar is a lifetime inference variable. Like a TypeVarType it carries
// lower and upper bounds and is coalesced polarity-dependently: a positive
// (output) occurrence joins its lower bounds, a negative (input) occurrence
// meets its upper bounds. A 'static upper bound drives a negative-position
// variable to 'static. Bound lists are extended ONLY through the solver's
// addLowerLtBound/addUpperLtBound helpers, mirroring the type-sort discipline so
// a discarded speculation trial truncates them back.
type LifetimeVar struct {
	ID          int
	LowerBounds []Lifetime
	UpperBounds []Lifetime
}

// StaticLifetime is 'static, the top of the outlives lattice: every lifetime
// outlives at most 'static, so `X <: 'static` always holds.
type StaticLifetime struct{}

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

// ContainsLifetime reports whether lt is already present in bounds, so a repeated
// outlives constraint does not re-append a bound. A LifetimeVar matches by pointer
// identity, but 'static matches by VALUE: every StaticLifetime denotes the one
// lattice top, and origination sites mint a fresh &StaticLifetime{} per call, so
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

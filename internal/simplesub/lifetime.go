package simplesub

import "github.com/escalier-lang/escalier/internal/set"

// ---- Lifetimes: a second sort, solved by the same constraint machinery ----
//
// The M4 thesis: lifetime inference is not a separate multi-phase dataflow
// analysis (as in the production infer_lifetime.go) but ordinary constraint
// solving over a *second sort* of variable. A Lifetime is either a variable
// (with lower/upper bounds, exactly like a type Variable) or 'static — the top
// of the "outlives" lattice (it outlives everything).
//
// Lifetimes ride on values: a borrowed record carries the lifetime of what it
// was borrowed from. So lifetime relationships fall out of the same value flow
// that drives type inference:
//   - a `mut` parameter is a borrow, so it gets a fresh lifetime variable;
//   - returning a parameter shares its lifetime by value identity (the returned
//     SimpleType *is* the parameter's, lifetime included);
//   - returning one of several values unions their lifetimes (coalesced to
//     `('a | 'b)`);
//   - a value escaping to module-level/static storage constrains its lifetime
//     `<: 'static`, which coalesces (negative position) to `'static`.

type Lifetime interface{ isLifetime() }

// LifetimeVar is a lifetime inference variable. Like a type Variable it carries
// lower/upper bounds and is coalesced polarity-dependently: positive position
// (output) joins its lower bounds, negative position (input) meets its upper
// bounds. 'static appearing as an upper bound drives a negative-position
// variable to 'static.
type LifetimeVar struct {
	id          int
	lowerBounds []Lifetime
	upperBounds []Lifetime
}

// StaticLifetime is 'static, the top of the outlives lattice.
type StaticLifetime struct{}

func (*LifetimeVar) isLifetime()    {}
func (*StaticLifetime) isLifetime() {}

func isStaticLifetime(lt Lifetime) bool {
	_, ok := lt.(*StaticLifetime)
	return ok
}

func (in *Inferer) freshLifetime() *LifetimeVar {
	lv := &LifetimeVar{id: in.lifetimeCounter}
	in.lifetimeCounter++
	return lv
}

// constrainLt asserts the outlives relation lhs <: rhs between lifetimes,
// mirroring constrain for the type sort. When a variable is on the left it gains
// an upper bound; when a variable is on the right it gains a lower bound. A
// var-to-var constraint records BOTH directions (lhs gains upper rhs, rhs gains
// lower lhs) so each variable sees the full relationship at coalescing — the
// type sort gets this from the separate symmetrize pass, but lifetimes are
// recorded directly here. 'static is the top, so X <: 'static always holds.
func (in *Inferer) constrainLt(lhs, rhs Lifetime) {
	in.constrainLtSeen(lhs, rhs, map[ltPair]bool{})
}

// ltPair keys the in-progress set so a transitive cycle (e.g. 'a <: 'b, 'b <: 'a,
// or a longer 'a <: 'b <: 'c <: 'a) terminates: the same (lhs, rhs) pair is never
// re-entered, and a bound already present is not re-appended.
type ltPair struct{ lhs, rhs Lifetime }

func (in *Inferer) constrainLtSeen(lhs, rhs Lifetime, seen map[ltPair]bool) {
	if lhs == rhs {
		return
	}
	key := ltPair{lhs, rhs}
	if seen[key] {
		return
	}
	seen[key] = true

	lv, lIsVar := lhs.(*LifetimeVar)
	rv, rIsVar := rhs.(*LifetimeVar)
	if lIsVar {
		if !containsLifetime(lv.upperBounds, rhs) {
			lv.upperBounds = append(lv.upperBounds, rhs)
		}
		for _, lb := range lv.lowerBounds {
			in.constrainLtSeen(lb, rhs, seen)
		}
	}
	if rIsVar {
		if !containsLifetime(rv.lowerBounds, lhs) {
			rv.lowerBounds = append(rv.lowerBounds, lhs)
		}
		for _, ub := range rv.upperBounds {
			in.constrainLtSeen(lhs, ub, seen)
		}
	}
}

// containsLifetime reports whether lt is already in bounds (by identity).
func containsLifetime(bounds []Lifetime, lt Lifetime) bool {
	for _, b := range bounds {
		if b == lt {
			return true
		}
	}
	return false
}

// lifetimeOf extracts the lifetime a value carries, or nil if it has none
// (a freshly-allocated value, or one with no borrow). It looks through Mut to
// the borrowed record.
func lifetimeOf(st SimpleType) Lifetime {
	switch t := st.(type) {
	case *Record:
		return t.lt
	case *Alias:
		return t.lt
	case *Mut:
		return lifetimeOf(t.inner)
	default:
		return nil
	}
}

// boundsAt returns a lifetime variable's polarity-relevant bounds: lower bounds
// in Positive position, upper bounds in Negative position — mirroring the type
// Variable.
func (v *LifetimeVar) boundsAt(pol Polarity) []Lifetime {
	if pol == Positive {
		return v.lowerBounds
	}
	return v.upperBounds
}

// analyzeLts walks a SimpleType recording, per lifetime variable, the polarities
// it occurs in. The lifetime on a `mut` record is COVARIANT (unlike the
// invariant field types), so Mut does not double it. This drives lifetime
// elision: a lifetime occurring in only one polarity (and not forced to
// 'static) connects nothing and is dropped — the lifetime-sort analogue of
// single-polarity type-variable elimination.
func analyzeLts(st SimpleType, pol Polarity, ltOcc map[int]map[Polarity]bool, vseen, ltseen map[polKey]bool) {
	switch t := st.(type) {
	case *Variable:
		pk := polKey{t.id, pol}
		if vseen[pk] {
			return
		}
		vseen[pk] = true
		for _, b := range t.boundsAt(pol) {
			analyzeLts(b, pol, ltOcc, vseen, ltseen)
		}
	case *Function:
		for _, p := range t.params {
			analyzeLts(p, pol.flip(), ltOcc, vseen, ltseen)
		}
		analyzeLts(t.ret, pol, ltOcc, vseen, ltseen)
	case *Tuple:
		for _, e := range t.elems {
			analyzeLts(e, pol, ltOcc, vseen, ltseen)
		}
	case *Record:
		if t.lt != nil {
			analyzeLifetime(t.lt, pol, ltOcc, ltseen)
		}
		for _, f := range t.fields {
			analyzeLts(f, pol, ltOcc, vseen, ltseen)
		}
	case *Alias:
		if t.lt != nil {
			analyzeLifetime(t.lt, pol, ltOcc, ltseen)
		}
		analyzeLts(t.body, pol, ltOcc, vseen, ltseen)
	case *Mut:
		analyzeLts(t.inner, pol, ltOcc, vseen, ltseen) // lifetime is covariant
	}
}

// analyzeLifetime records a lifetime variable's polarity and follows its
// polarity-relevant bounds, so a lifetime joined into a result (via a fresh
// join variable's lower bounds) is seen in the result's polarity too.
func analyzeLifetime(lt Lifetime, pol Polarity, ltOcc map[int]map[Polarity]bool, seen map[polKey]bool) {
	v, ok := lt.(*LifetimeVar)
	if !ok {
		return
	}
	if ltOcc[v.id] == nil {
		ltOcc[v.id] = map[Polarity]bool{}
	}
	ltOcc[v.id][pol] = true
	pk := polKey{v.id, pol}
	if seen[pk] {
		return
	}
	seen[pk] = true
	for _, b := range v.boundsAt(pol) {
		analyzeLifetime(b, pol, ltOcc, seen)
	}
}

// collectLifetimeVars gathers every lifetime variable reachable from st,
// following both type-variable bounds and lifetime-variable bounds.
func collectLifetimeVars(st SimpleType, out map[int]*LifetimeVar, vseen map[int]bool) {
	switch t := st.(type) {
	case *Variable:
		if vseen[t.id] {
			return
		}
		vseen[t.id] = true
		for _, b := range t.lowerBounds {
			collectLifetimeVars(b, out, vseen)
		}
		for _, b := range t.upperBounds {
			collectLifetimeVars(b, out, vseen)
		}
	case *Function:
		for _, p := range t.params {
			collectLifetimeVars(p, out, vseen)
		}
		collectLifetimeVars(t.ret, out, vseen)
	case *Tuple:
		for _, e := range t.elems {
			collectLifetimeVars(e, out, vseen)
		}
	case *Record:
		collectLtVarsFromLifetime(t.lt, out)
		for _, f := range t.fields {
			collectLifetimeVars(f, out, vseen)
		}
	case *Alias:
		collectLtVarsFromLifetime(t.lt, out)
		collectLifetimeVars(t.body, out, vseen)
	case *Mut:
		collectLifetimeVars(t.inner, out, vseen)
	}
}

func collectLtVarsFromLifetime(lt Lifetime, out map[int]*LifetimeVar) {
	v, ok := lt.(*LifetimeVar)
	if !ok || out[v.id] != nil {
		return
	}
	out[v.id] = v
	for _, b := range v.lowerBounds {
		collectLtVarsFromLifetime(b, out)
	}
	for _, b := range v.upperBounds {
		collectLtVarsFromLifetime(b, out)
	}
}

// attachParamLifetimes gives a `mut` record parameter a fresh lifetime variable
// if it doesn't already carry one — a mutable parameter is a borrow, so it has
// the lifetime of whatever the caller lent. The returned value shares the
// field map but carries the fresh lifetime, so the lifetime flows wherever the
// parameter does (e.g. when it is returned). The fresh lifetime is recorded as
// a "param lifetime": these are the only lifetimes that get named in the output
// (a borrow originates at a parameter). Internal join variables — created by
// joinBranches — are never named; only their param-lifetime members are.
func (in *Inferer) attachParamLifetimes(st SimpleType) SimpleType {
	m, ok := st.(*Mut)
	if !ok {
		return st
	}
	lt := in.freshLifetime()
	record := func() {
		if in.paramLifetimes == nil {
			in.paramLifetimes = set.NewSet[int]()
		}
		in.paramLifetimes.Add(lt.id)
	}
	switch inner := m.inner.(type) {
	case *Record:
		if inner.lt == nil {
			record()
			return &Mut{inner: &Record{fields: inner.fields, lt: lt}}
		}
	case *Alias:
		if inner.lt == nil {
			record()
			return &Mut{inner: &Alias{name: inner.name, body: inner.body, lt: lt}}
		}
	}
	return st
}

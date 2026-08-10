package set

// Recursion guards over a `seen` set, in the two flavours the compiler's walks need.
//
// Both take the set, the key identifying the current step, and the work to do at that step.
// They differ in when the entry comes back out. `Once` leaves the key in the set forever, so
// the work runs at most once per key for the lifetime of the set. `OnPath` removes the key
// once the work returns, so the set holds exactly the keys on the current recursion path.
//
// That difference decides which one a site wants. Use `Once` when the result depends only on
// the key, which makes a second visit redundant work. Use `OnPath` when the result depends on
// the context the walk arrived through, such as polarity or the open μ-binders, since two
// arrivals through different contexts then need two separate walks.

// Once runs fn the first time key is offered to seen and returns fn's result. Every later call
// with the same key returns onRepeat() and does not run fn. A repeat is not necessarily a
// cycle. The key stays in seen after fn returns, so an independent branch reaching the same key
// later also takes the onRepeat path.
//
// Because a repeat returns onRepeat() rather than the answer fn would have produced, Once fits
// only where fn's real output lands in state the caller reads afterwards. That state is an
// out-parameter at collectAllFrom and a field on the visitor at typeParamCollector.
//
// onRepeat is a thunk rather than a plain value so a site whose repeat answer is computed does
// not have to compute it on the first visit too.
func Once[K comparable, R any](seen Set[K], key K, onRepeat func() R, fn func() R) R {
	if seen.Contains(key) {
		return onRepeat()
	}
	seen.Add(key)
	return fn()
}

// OnceDo is Once for work that returns nothing. A repeat does nothing at all, so there is no
// onRepeat thunk to pass.
func OnceDo[K comparable](seen Set[K], key K, fn func()) {
	if seen.Contains(key) {
		return
	}
	seen.Add(key)
	fn()
}

// OnPath runs fn with key added to seen and removes key again once fn returns, so seen holds
// exactly the keys on the current recursion path. Offering a key already on that path is a
// genuine cycle: OnPath returns onCycle() and does not run fn. Two independent branches
// reaching the same key both run fn, because the first branch removed its entry before the
// second one started.
//
// The removal is deferred, so a panic unwinding through fn leaves seen as it found it.
//
// onCycle is a thunk rather than a plain value because the answer at a cycle is not always a
// constant. coalescer returns a reference to the μ-binder it minted for the key.
func OnPath[K comparable, R any](seen Set[K], key K, onCycle func() R, fn func() R) R {
	if seen.Contains(key) {
		return onCycle()
	}
	seen.Add(key)
	defer seen.Remove(key)
	return fn()
}

// Table memoizes one value per key and reports a key that is asked for again while its own fn
// is still running. It is the guard for a walk that has to hand back a real result rather than
// accumulate into shared state, so neither Once nor OnPath fits.
//
// A key moves through three states. It starts absent. Do marks it in progress before calling
// fn, and marks it settled with fn's result once fn returns. Asking for an in-progress key is
// a cycle, since the only way to reach one is from inside its own fn.
//
// The zero Table is ready to use.
type Table[K comparable, V any] struct {
	entries map[K]tableEntry[V]
}

// tableEntry is one key's slot in a Table. settled distinguishes a finished derivation from one
// still running, which a nil or zero value alone cannot do for an arbitrary V.
type tableEntry[V any] struct {
	value   V
	settled bool
}

// NewTable creates a new empty Table.
func NewTable[K comparable, V any]() *Table[K, V] {
	return &Table[K, V]{entries: map[K]tableEntry[V]{}}
}

// Do returns the value for key. On the first ask it runs fn and stores the result, which every
// later ask replays without re-running fn. An ask that arrives while fn is still running for
// that same key returns onCycle() instead, leaving the in-progress entry alone so the original
// fn still gets to settle it.
func (t *Table[K, V]) Do(key K, onCycle func() V, fn func() V) V {
	if e, found := t.entries[key]; found {
		if !e.settled {
			return onCycle()
		}
		return e.value
	}
	if t.entries == nil {
		t.entries = map[K]tableEntry[V]{}
	}
	t.entries[key] = tableEntry[V]{}
	value := fn()
	t.entries[key] = tableEntry[V]{value: value, settled: true}
	return value
}

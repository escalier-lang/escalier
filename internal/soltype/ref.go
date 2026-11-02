package soltype

// NewRef wraps inner in a borrow at lifetime lt. A nil lt names no borrow, so NewRef
// returns the bare inner, owned, whatever mut says: an owned value's mutability
// belongs to the binding that owns it, not to its type. A type-switch on *RefType
// can therefore assume the wrapper is a borrow. Construct a &RefType literal directly
// when a lifetime-less view must survive, as the bare<:RefType constrain arm (C2)
// does to re-dispatch a source as an immutable view without recursing forever.
func NewRef(mut bool, lt Lifetime, inner RefInner) Type {
	if lt == nil {
		return inner
	}
	return &RefType{Mut: mut, Lt: lt, Inner: inner}
}

// UnwrapRef peels a RefType into its inner carrier, mutability, and lifetime,
// returning (t, false, nil) when t is not a borrow.
func UnwrapRef(t Type) (inner Type, mut bool, lt Lifetime) {
	if r, ok := t.(*RefType); ok {
		return r.Inner, r.Mut, r.Lt
	}
	return t, false, nil
}

// UnwrapRefs is the N-ary UnwrapRef, for a caller asking whether one borrow can stand in
// for a whole set of them. It peels each type into its carrier and lifetime, and reports
// whether every one of them is mutable.
//
// ok is false unless EVERY type is a borrow carrying a lifetime. A plain value is not one,
// and an empty set names no borrow either, so both are false.
//
// allMut is the conjunction rather than a rejection, since callers differ on what a mixed
// set means. A join of returned borrows needs every input mutable and gives up otherwise,
// while a destructured union binds immutable leaves.
func UnwrapRefs(types []Type) (inners []Type, lts []Lifetime, allMut bool, ok bool) {
	if len(types) == 0 {
		return nil, nil, false, false
	}
	inners = make([]Type, len(types))
	lts = make([]Lifetime, len(types))
	allMut = true
	for i, t := range types {
		r, isRef := t.(*RefType)
		if !isRef || r.Lt == nil {
			return nil, nil, false, false
		}
		inners[i], lts[i] = r.Inner, r.Lt
		allMut = allMut && r.Mut
	}
	return inners, lts, allMut, true
}

// CarrierOf peels any RefType down to the value it wraps, returning t unchanged
// when it is not a borrow. Member access and pattern matching look through a borrow
// to its carrier.
func CarrierOf(t Type) Type {
	if r, ok := t.(*RefType); ok {
		return r.Inner
	}
	return t
}

// BorrowableType reports whether t may sit inside a RefType — i.e. whether it is a
// RefInner. A TypeVarType is borrowable mid-inference, with its content invariant
// deferred to constrain time. PrimType, LitType and FuncType are not borrowable.
func BorrowableType(t Type) bool {
	_, ok := t.(RefInner)
	return ok
}

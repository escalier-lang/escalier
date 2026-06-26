package soltype

// NewRef wraps inner in a borrow, collapsing the degenerate immutable-no-lifetime
// cell back to the bare inner so no *RefType ever names a value that isn't really a
// borrow. A type-switch on *RefType can therefore assume the wrapper is meaningful.
// Construct a &RefType literal directly when that cell must survive — the
// bare<:RefType constrain arm does, to re-dispatch a source as an immutable
// view without recursing forever.
func NewRef(mut bool, lt Lifetime, inner RefInner) Type {
	if !mut && lt == nil {
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
// deferred to constrain time; PrimType / LitType / FuncType / PromiseType are not.
func BorrowableType(t Type) bool {
	_, ok := t.(RefInner)
	return ok
}

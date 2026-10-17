package soltype

// TypeAppType is the application `F<A>` of a type-constructor parameter to its arguments. Head is
// what the parameter stands for at this point in solving:
//
//   - a TypeVarType, for a parameter that is still being inferred or that a generic body holds
//     rigid
//   - a SkolemType, for a parameter a subtype check skolemized
//
// A head that is a known constructor never stays here. NewTypeApp applies it, so `F<number>` with
// `F = Array` is the ClassType `Array<number>` and not an application node.
//
// Variances holds the declared variance of each argument, one entry per entry of Args.
type TypeAppType struct {
	Head      Type
	Args      []Type
	Variances []SlotVariance
}

// SlotVariance is the declared variance of one argument of a type-constructor parameter. It
// says how an application's subtyping depends on that argument.
type SlotVariance int

const (
	// SlotInvariant requires the argument to match in both directions. It is what a `_` with
	// no modifier declares.
	SlotInvariant SlotVariance = iota
	// SlotCovariant, written `out _`, lets a subtype argument make a subtype application.
	SlotCovariant
	// SlotContravariant, written `in _`, flips the direction.
	SlotContravariant
	// SlotInOut, written `in out _`, is invariant like SlotInvariant. It is kept apart only so
	// the modifier the source wrote renders back.
	SlotInOut
)

// modifier returns the source modifier for v followed by a space, or the empty string for an
// invariant slot.
func (v SlotVariance) modifier() string {
	switch v {
	case SlotCovariant:
		return "out "
	case SlotContravariant:
		return "in "
	case SlotInOut:
		return "in out "
	}
	return ""
}

// Variance returns v with SlotInOut folded into SlotInvariant, the form subtyping reads.
func (v SlotVariance) Variance() SlotVariance {
	if v == SlotInOut {
		return SlotInvariant
	}
	return v
}

// Meet returns the variance two declarations of one argument agree on. Two equal variances give
// that variance, and any disagreement gives SlotInvariant.
func (v SlotVariance) Meet(w SlotVariance) SlotVariance {
	if v.Variance() == w.Variance() {
		return v.Variance()
	}
	return SlotInvariant
}

// Slot returns the declared variance of t's i'th argument, with SlotInOut folded into
// SlotInvariant, and SlotInvariant when t records none for it.
func (t *TypeAppType) Slot(i int) SlotVariance {
	return SlotAt(t.Variances, i)
}

// SlotAt returns the variance slots declares at index i, with SlotInOut folded into
// SlotInvariant, and SlotInvariant when slots has no entry there.
func SlotAt(slots []SlotVariance, i int) SlotVariance {
	if i < len(slots) {
		return slots[i].Variance()
	}
	return SlotInvariant
}

// TypeCtorType is a type constructor used as a value of a constructor parameter, as `Array` is in
// `Functor<Array>`. Base is the class or alias handle it applies, with no type arguments. Fixed
// holds one entry per parameter of Base. A nil entry is a hole an application fills, and a non-nil
// entry is an argument the constructor already supplies, so `Promise<_, FetchError>` is Base
// `Promise` with Fixed `[nil, FetchError]`.
type TypeCtorType struct {
	Base  Type
	Fixed []Type
}

func (*TypeAppType) isType()  {}
func (*TypeCtorType) isType() {}

// Arity returns the number of holes in c, which is the number of arguments an application of c
// supplies.
func (c *TypeCtorType) Arity() int {
	n := 0
	for _, f := range c.Fixed {
		if f == nil {
			n++
		}
	}
	return n
}

// Name returns the qualified name of the class or alias c applies.
func (c *TypeCtorType) Name() string {
	switch b := c.Base.(type) {
	case *ClassType:
		return b.Name
	case *AliasType:
		return b.Name
	}
	return ""
}

// Apply fills c's holes with args in order and returns the resulting class or alias instance. It
// reports false when len(args) differs from c's arity.
func (c *TypeCtorType) Apply(args []Type) (Type, bool) {
	if len(args) != c.Arity() {
		return nil, false
	}
	filled := make([]Type, len(c.Fixed))
	next := 0
	for i, f := range c.Fixed {
		if f == nil {
			filled[i] = args[next]
			next++
		} else {
			filled[i] = f
		}
	}
	switch b := c.Base.(type) {
	case *ClassType:
		cp := *b
		cp.TypeArgs = filled
		return &cp, true
	case *AliasType:
		cp := *b
		cp.TypeArgs = filled
		return &cp, true
	}
	return nil, false
}

// NewTypeApp returns the application of head to args, whose declared variances are variances.
// The head decides the result:
//
//   - A TypeCtorType head is applied at once, giving the class or alias instance it names.
//   - A `never` or `unknown` head gives that bound itself.
//   - A union or intersection head that holds constructors gives the union or intersection of
//     their instances. Its other members are dropped, so `(T0 | Array)<number>` is
//     `Array<number>`.
//
// Any other head gives a TypeAppType.
func NewTypeApp(head Type, args []Type, variances []SlotVariance) Type {
	switch h := head.(type) {
	case *TypeCtorType:
		if applied, ok := h.Apply(args); ok {
			return applied
		}
	case *NeverType, *UnknownType:
		return h
	case *UnionType:
		if applied := applyCtorMembers(h.Types, args); len(applied) == 1 {
			return applied[0]
		} else if len(applied) > 1 {
			return &UnionType{Types: applied}
		}
	case *IntersectionType:
		if applied := applyCtorMembers(h.Types, args); len(applied) == 1 {
			return applied[0]
		} else if len(applied) > 1 {
			return &IntersectionType{Types: applied}
		}
	}
	return &TypeAppType{Head: head, Args: args, Variances: variances}
}

func (t *TypeAppType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	// The head walks covariantly. Each argument walks at the polarity its declared variance
	// gives it, so an `in _` argument walks flipped. An invariant argument walks covariantly,
	// as a class's arguments do.
	head := cur.Head.Accept(v, pol)
	args := cur.Args
	changed := false
	for i, a := range cur.Args {
		argPol := pol
		if cur.Slot(i) == SlotContravariant {
			argPol = pol.Flip()
		}
		na := a.Accept(v, argPol)
		if na != a {
			if !changed {
				args = append([]Type(nil), cur.Args...)
				changed = true
			}
			args[i] = na
		}
	}
	out := Type(cur)
	if head != cur.Head || changed {
		// A substitution that puts a constructor in the head turns the node into the
		// application the constructor names.
		out = NewTypeApp(head, args, cur.Variances)
	}
	return v.ExitType(out, pol)
}

func (t *TypeCtorType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	// Only the supplied arguments walk. A hole has nothing to visit, and Base carries no
	// arguments of its own.
	fixed := cur.Fixed
	changed := false
	for i, f := range cur.Fixed {
		if f == nil {
			continue
		}
		nf := f.Accept(v, pol)
		if nf != f {
			if !changed {
				fixed = append([]Type(nil), cur.Fixed...)
				changed = true
			}
			fixed[i] = nf
		}
	}
	out := cur
	if changed {
		out = &TypeCtorType{Base: cur.Base, Fixed: fixed}
	}
	return v.ExitType(out, pol)
}

// applyCtorMembers applies each constructor among members to args and returns the instances,
// skipping a constructor whose arity differs from len(args).
func applyCtorMembers(members []Type, args []Type) []Type {
	var applied []Type
	for _, m := range members {
		if ctor, ok := m.(*TypeCtorType); ok {
			if inst, ok := ctor.Apply(args); ok {
				applied = append(applied, inst)
			}
		}
	}
	return applied
}

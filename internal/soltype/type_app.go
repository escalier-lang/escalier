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
type TypeAppType struct {
	Head Type
	Args []Type
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

// NewTypeApp returns the application of head to args. A TypeCtorType head is applied at once, so
// the result is the class or alias instance the constructor names. Any other head gives a
// TypeAppType.
func NewTypeApp(head Type, args []Type) Type {
	if ctor, ok := head.(*TypeCtorType); ok {
		if applied, ok := ctor.Apply(args); ok {
			return applied
		}
	}
	return &TypeAppType{Head: head, Args: args}
}

func (t *TypeAppType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	// The head and the arguments walk covariantly, as a class's arguments do. Variance is
	// decided where constrain compares two applications.
	head := cur.Head.Accept(v, pol)
	args, changed := acceptTypes(cur.Args, v, pol)
	out := Type(cur)
	if head != cur.Head || changed {
		// A substitution that puts a constructor in the head turns the node into the
		// application the constructor names.
		out = NewTypeApp(head, args)
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

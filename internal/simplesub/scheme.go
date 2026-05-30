package simplesub

// ---- Type schemes (let-polymorphism) ----

type TypeScheme interface{ isScheme() }

// MonoScheme is a plain type (e.g. a lambda parameter): no generalization.
type MonoScheme struct{ ty SimpleType }

// PolyScheme generalizes variables in body whose level is > level.
type PolyScheme struct {
	level int
	body  SimpleType
}

func (*MonoScheme) isScheme() {}
func (*PolyScheme) isScheme() {}

func (in *Inferer) instantiate(s TypeScheme, lvl int) SimpleType {
	switch sc := s.(type) {
	case *MonoScheme:
		return sc.ty
	case *PolyScheme:
		return in.freshenAbove(sc.level, sc.body, lvl, map[int]*Variable{})
	}
	panic("unreachable")
}

// freshenAbove copies ty, replacing each variable with level > lim by a fresh
// variable at lvl (its bounds freshened too); variables at level <= lim are kept.
//
// KNOWN LIMITATION (deferred to the M1 production rewrite): the lifetime carried
// by a Record/Tuple/Alias is copied by reference, not freshened, so two
// instantiations of a generalized lifetime-bearing scheme share the same
// LifetimeVar. A correct fix needs the lifetime sort to carry its own levels
// (LifetimeVar has none today) plus a lifetime cache threaded through here and
// instantiate — a lifetime-generalization design change out of scope for the
// spike. No current inference exhibits wrong output from this (lifetimes that
// would collide tend to elide), so it is documented rather than band-aided.
func (in *Inferer) freshenAbove(lim int, ty SimpleType, lvl int, cache map[int]*Variable) SimpleType {
	if levelOf(ty) <= lim {
		return ty
	}
	switch t := ty.(type) {
	case *Variable:
		if nv, ok := cache[t.id]; ok {
			return nv
		}
		nv := in.freshVar(lvl)
		cache[t.id] = nv
		for _, lb := range t.lowerBounds {
			nv.lowerBounds = append(nv.lowerBounds, in.freshenAbove(lim, lb, lvl, cache))
		}
		for _, ub := range t.upperBounds {
			nv.upperBounds = append(nv.upperBounds, in.freshenAbove(lim, ub, lvl, cache))
		}
		return nv
	case *Function:
		params := make([]SimpleType, len(t.params))
		for i, p := range t.params {
			params[i] = in.freshenAbove(lim, p, lvl, cache)
		}
		return &Function{params: params, paramNames: t.paramNames, ret: in.freshenAbove(lim, t.ret, lvl, cache)}
	case *Tuple:
		elems := make([]SimpleType, len(t.elems))
		for i, e := range t.elems {
			elems[i] = in.freshenAbove(lim, e, lvl, cache)
		}
		return &Tuple{elems: elems}
	case *Record:
		fields := make(map[string]SimpleType, len(t.fields))
		for name, f := range t.fields {
			fields[name] = in.freshenAbove(lim, f, lvl, cache)
		}
		return &Record{fields: fields, lt: t.lt}
	case *Mut:
		return &Mut{inner: in.freshenAbove(lim, t.inner, lvl, cache)}
	case *Alias:
		return &Alias{name: t.name, body: in.freshenAbove(lim, t.body, lvl, cache), lt: t.lt}
	case *ResidualOp:
		return &ResidualOp{kind: t.kind, operand: in.freshenAbove(lim, t.operand, lvl, cache), key: t.key}
	case *Union:
		return &Union{types: in.freshenAll(lim, t.types, lvl, cache)}
	case *Intersection:
		return &Intersection{types: in.freshenAll(lim, t.types, lvl, cache)}
	default:
		return ty
	}
}

func (in *Inferer) freshenAll(lim int, types []SimpleType, lvl int, cache map[int]*Variable) []SimpleType {
	out := make([]SimpleType, len(types))
	for i, t := range types {
		out[i] = in.freshenAbove(lim, t, lvl, cache)
	}
	return out
}

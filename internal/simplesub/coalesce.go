package simplesub

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- Coalescing: SimpleType -> type_system.Type ----

type coalescer struct {
	names             map[int]string // keyed by representative id
	order             []string
	counter           int
	mergedOccurrences map[int]map[Polarity]bool // keyed by representative id
	uf                *unionFind
	inProc            map[polKey]bool // keyed by (representative id, polarity)

	// lifetime naming (the second sort): leaf lifetime variables become named
	// parameters 'a, 'b, ... collected in ltOrder for the function's <...> list.
	ltNames   map[int]string
	ltCounter int
	ltOrder   []string
	// ltKeep[id] is true for param lifetimes that survive elision: those
	// occurring in both polarities (connecting an input to an output) or forced
	// to 'static. A param lifetime occurring only on its parameter connects
	// nothing and is elided (rendered as no lifetime), the lifetime-sort analogue
	// of single-polarity type-variable elimination.
	ltKeep map[int]bool
	// paramLifetimes mirrors Inferer.paramLifetimes: the lifetime-variable ids
	// that originate on a parameter and may therefore be named.
	paramLifetimes set.Set[int]
}

// lifetimeForced reports whether a lifetime variable has 'static among its
// bounds (in which case it coalesces to 'static rather than being elided).
func lifetimeForced(v *LifetimeVar) bool {
	for _, b := range v.lowerBounds {
		if isStaticLifetime(b) {
			return true
		}
	}
	for _, b := range v.upperBounds {
		if isStaticLifetime(b) {
			return true
		}
	}
	return false
}

func (c *coalescer) nameForRep(rep int) string {
	if n, ok := c.names[rep]; ok {
		return n
	}
	n := "T" + strconv.Itoa(c.counter)
	c.counter++
	c.names[rep] = n
	c.order = append(c.order, n)
	return n
}

func (c *coalescer) ltNameFor(id int) string {
	if n, ok := c.ltNames[id]; ok {
		return n
	}
	n := alphaName(c.ltCounter)
	c.ltCounter++
	c.ltNames[id] = n
	c.ltOrder = append(c.ltOrder, n)
	return n
}

// alphaName maps 0,1,...,25,26,... to a,b,...,z,aa,ab,... (Excel-style base-26),
// so lifetime names stay valid letters beyond the first 26.
func alphaName(i int) string {
	var b []byte
	for {
		b = append([]byte{byte('a' + i%26)}, b...)
		i = i/26 - 1
		if i < 0 {
			break
		}
	}
	return string(b)
}

// coalesceLifetime renders a Lifetime into a type_system.Lifetime. The only
// named lifetimes are *param* lifetimes that survive elision (ltKeep). A param
// lifetime renders as its own name. A non-param (join) variable expands to the
// set of param lifetimes reachable through its bounds — its lower bounds in
// Positive position (a return uniting several borrows ⇒ `('a | 'b)`), its upper
// bounds in Negative position. 'static (top) absorbs.
func (c *coalescer) coalesceLifetime(lt Lifetime, pol Polarity) type_system.Lifetime {
	v, ok := lt.(*LifetimeVar)
	if !ok {
		if isStaticLifetime(lt) {
			return &type_system.LifetimeValue{IsStatic: true}
		}
		return nil
	}

	// A named param lifetime renders as itself — unless it is forced to 'static
	// (its borrow escapes), in which case it renders 'static.
	if c.paramLifetimes.Contains(v.id) {
		if lifetimeForced(v) {
			return &type_system.LifetimeValue{IsStatic: true}
		}
		if c.ltKeep[v.id] {
			return &type_system.LifetimeVar{Name: c.ltNameFor(v.id)}
		}
		return nil
	}

	// A join/internal variable: gather the param lifetimes it reaches.
	members, static := c.reachableParamLifetimes(v, pol, map[int]bool{})
	if static {
		return &type_system.LifetimeValue{IsStatic: true}
	}
	switch len(members) {
	case 0:
		return nil
	case 1:
		return members[0]
	default:
		return &type_system.LifetimeUnion{Lifetimes: members}
	}
}

// reachableParamLifetimes collects the kept param lifetimes reachable from v
// through its polarity-relevant bounds, and whether 'static is reached.
func (c *coalescer) reachableParamLifetimes(v *LifetimeVar, pol Polarity, seen map[int]bool) ([]type_system.Lifetime, bool) {
	if seen[v.id] {
		return nil, false
	}
	seen[v.id] = true
	var members []type_system.Lifetime
	static := false
	for _, b := range v.boundsAt(pol) {
		if isStaticLifetime(b) {
			static = true
			continue
		}
		bv, ok := b.(*LifetimeVar)
		if !ok {
			continue
		}
		if c.paramLifetimes.Contains(bv.id) {
			if c.ltKeep[bv.id] {
				members = append(members, &type_system.LifetimeVar{Name: c.ltNameFor(bv.id)})
			}
			continue
		}
		sub, subStatic := c.reachableParamLifetimes(bv, pol, seen)
		members = append(members, sub...)
		static = static || subStatic
	}
	return members, static
}

func (c *coalescer) coalesce(st SimpleType, pol Polarity) type_system.Type {
	switch t := st.(type) {
	case *Primitive:
		return primToType(t.name)
	case *Literal:
		return litToType(t)
	case *Function:
		params := make([]*type_system.FuncParam, len(t.params))
		for i, p := range t.params {
			params[i] = type_system.NewFuncParam(
				type_system.NewIdentPat(paramName(t.paramNames, i)),
				c.coalesce(p, pol.flip())) // contravariant
		}
		return type_system.NewFuncType(nil, nil, params, c.coalesce(t.ret, pol), nil) // covariant
	case *Tuple:
		elems := make([]type_system.Type, len(t.elems))
		for i, e := range t.elems {
			elems[i] = c.coalesce(e, pol)
		}
		return type_system.NewTupleType(nil, elems...)
	case *Record:
		names := make([]string, 0, len(t.fields))
		for name := range t.fields {
			names = append(names, name)
		}
		sort.Strings(names) // deterministic field order
		elems := make([]type_system.ObjTypeElem, len(names))
		for i, name := range names {
			elems[i] = type_system.NewPropertyElem(
				type_system.NewStrKey(name), c.coalesce(t.fields[name], pol))
		}
		obj := type_system.NewObjectType(nil, elems)
		if t.lt != nil {
			obj.Lifetime = c.coalesceLifetime(t.lt, pol)
		}
		return obj
	case *Mut:
		// inner is invariant, so its read and write views are equal; coalesce
		// via the read (current-polarity) view. Variables inside are bipolar, so
		// they survive simplification and print as consistent type parameters.
		return type_system.NewMutType(nil, c.coalesce(t.inner, pol))
	case *Void:
		return type_system.NewVoidType(nil)
	case *Alias:
		ref := type_system.NewTypeRefType(nil, t.name, nil)
		if t.lt != nil {
			ref.Lifetime = c.coalesceLifetime(t.lt, pol)
		}
		return ref
	case *ResidualOp:
		// Design A: a type operator left inert during the value solve reduces
		// here, in the post-solve coalescing pass, once its operand has a
		// concrete coalesced shape.
		return c.reduceResidual(t)
	case *Union:
		members := make([]type_system.Type, len(t.types))
		for i, m := range t.types {
			members[i] = c.coalesce(m, pol)
		}
		return type_system.NewUnionType(nil, members...)
	case *Intersection:
		members := make([]type_system.Type, len(t.types))
		for i, m := range t.types {
			members[i] = c.coalesce(m, pol)
		}
		return type_system.NewIntersectionType(nil, members...)
	case *Variable:
		rep := c.uf.find(t.id)
		bipolar := c.mergedOccurrences[rep][Positive] && c.mergedOccurrences[rep][Negative]
		pk := polKey{rep, pol}
		if c.inProc[pk] {
			return type_system.NewTypeRefType(nil, c.nameForRep(rep), nil)
		}
		c.inProc[pk] = true
		defer delete(c.inProc, pk)

		boundTypes := make([]type_system.Type, 0, len(t.boundsAt(pol)))
		for _, b := range t.boundsAt(pol) {
			boundTypes = append(boundTypes, c.coalesce(b, pol))
		}

		if !bipolar {
			// Single-polarity variable: drop the variable itself and keep only
			// its bounds (positive => union of lowers, negative => inter of uppers).
			parts := dedupTypes(boundTypes)
			if len(parts) == 0 {
				if pol == Positive {
					return type_system.NewNeverType(nil)
				}
				return type_system.NewUnknownType(nil)
			}
			return combine(pol, parts)
		}

		self := type_system.NewTypeRefType(nil, c.nameForRep(rep), nil)
		parts := dedupTypes(append([]type_system.Type{self}, boundTypes...))
		return combine(pol, parts)
	default:
		panic(fmt.Sprintf("coalesce: unhandled %T", st))
	}
}

// combine builds a union (positive) or intersection (negative) of parts,
// returning the sole element directly when only one remains. In Negative
// position, object types are merged into a single record (their meet).
func combine(pol Polarity, parts []type_system.Type) type_system.Type {
	if pol == Negative {
		parts = mergeObjects(parts)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	if pol == Positive {
		return type_system.NewUnionType(nil, parts...)
	}
	return type_system.NewIntersectionType(nil, parts...)
}

// mergeObjects collapses two or more object types in an intersection into one
// (the meet): field sets are unioned, and a field shared by several objects
// becomes the intersection of its types. This turns `{bar: T0} & {baz: T1}`
// into `{bar: T0, baz: T1}` — how member-access requirements on one receiver
// combine. Objects wrapped in `mut` are merged the same way, with the result
// re-wrapped: `mut {x} & mut {y}` becomes `mut {x, y}` — how multiple field
// writes to one receiver combine.
func mergeObjects(parts []type_system.Type) []type_system.Type {
	var bareObjs, mutObjs []*type_system.ObjectType
	var others []type_system.Type
	for _, p := range parts {
		switch t := p.(type) {
		case *type_system.ObjectType:
			bareObjs = append(bareObjs, t)
		case *type_system.MutType:
			if o, ok := t.Type.(*type_system.ObjectType); ok {
				mutObjs = append(mutObjs, o)
				continue
			}
			others = append(others, p)
		default:
			others = append(others, p)
		}
	}

	var merged []type_system.Type
	if m := mergeObjectGroup(bareObjs); m != nil {
		merged = append(merged, m)
	} else {
		for _, o := range bareObjs {
			merged = append(merged, o)
		}
	}
	if m := mergeObjectGroup(mutObjs); m != nil {
		merged = append(merged, type_system.NewMutType(nil, m))
	} else {
		for _, o := range mutObjs {
			merged = append(merged, type_system.NewMutType(nil, o))
		}
	}

	if len(merged) == 0 {
		return parts // nothing mergeable; leave as-is
	}
	return append(merged, others...)
}

// mergeObjectGroup merges a group of object types into a single object (the
// meet), or returns nil if there are fewer than two. A field shared by several
// objects becomes the intersection of its types.
func mergeObjectGroup(objs []*type_system.ObjectType) *type_system.ObjectType {
	if len(objs) < 2 {
		return nil
	}
	byName := map[string]type_system.Type{}
	var order []string
	for _, o := range objs {
		for _, elem := range o.Elems {
			pe, ok := elem.(*type_system.PropertyElem)
			if !ok {
				continue
			}
			name := pe.Name.Str
			if existing, dup := byName[name]; dup {
				byName[name] = type_system.NewIntersectionType(nil, existing, pe.Value)
			} else {
				byName[name] = pe.Value
				order = append(order, name)
			}
		}
	}
	sort.Strings(order)
	elems := make([]type_system.ObjTypeElem, len(order))
	for i, name := range order {
		elems[i] = type_system.NewPropertyElem(type_system.NewStrKey(name), byName[name])
	}
	return type_system.NewObjectType(nil, elems)
}

// dedupTypes removes parts that render to the same string, preserving order.
func dedupTypes(parts []type_system.Type) []type_system.Type {
	seen := map[string]bool{}
	out := parts[:0:0]
	for _, p := range parts {
		s := type_system.PrintType(p, type_system.PrintConfig{})
		if !seen[s] {
			seen[s] = true
			out = append(out, p)
		}
	}
	return out
}

func paramName(names []string, i int) string {
	if i < len(names) && names[i] != "" && names[i] != "_" {
		return names[i]
	}
	return "x" + strconv.Itoa(i)
}

func primToType(name string) type_system.Type {
	switch name {
	case "number":
		return type_system.NewNumPrimType(nil)
	case "string":
		return type_system.NewStrPrimType(nil)
	case "boolean":
		return type_system.NewBoolPrimType(nil)
	default:
		panic("simplesub: unknown primitive " + name)
	}
}

func litToType(l *Literal) type_system.Type {
	switch l.kind {
	case "str":
		return type_system.NewStrLitType(nil, l.str)
	case "num":
		return type_system.NewNumLitType(nil, l.num)
	case "bool":
		return type_system.NewBoolLitType(nil, l.b)
	default:
		panic("simplesub: unknown literal kind " + l.kind)
	}
}

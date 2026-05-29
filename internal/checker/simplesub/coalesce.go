package simplesub

import (
	"fmt"
	"sort"
	"strconv"

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
		return type_system.NewObjectType(nil, elems)
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
// combine.
func mergeObjects(parts []type_system.Type) []type_system.Type {
	var objs []*type_system.ObjectType
	var others []type_system.Type
	for _, p := range parts {
		if o, ok := p.(*type_system.ObjectType); ok {
			objs = append(objs, o)
		} else {
			others = append(others, p)
		}
	}
	if len(objs) < 2 {
		return parts
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
	merged := type_system.NewObjectType(nil, elems)
	return append([]type_system.Type{merged}, others...)
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

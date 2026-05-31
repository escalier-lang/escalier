package simplesub

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// ---- Inference engine ----

// fieldKey identifies a field of a particular receiver variable.
type fieldKey struct {
	recvID int
	field  string
}

type Inferer struct {
	varCounter      int
	lifetimeCounter int
	// paramLifetimes is the set of lifetime-variable ids that originate on a
	// parameter (a borrow). Only these are named in the output; internal join
	// variables are not.
	paramLifetimes set.Set[int]
	// written records the (widened) type stored into a field of a receiver
	// variable. A subsequent read of the same field on the same receiver
	// returns this type, so a value written to obj.x flows to a later read of
	// obj.x. The stored type is concrete (not a variable) so that, inside the
	// invariant `mut {x: ...}` requirement, the field renders as e.g. `number`
	// rather than surviving as a bipolar type parameter.
	written map[fieldKey]SimpleType
}

func NewInferer() *Inferer {
	return &Inferer{written: map[fieldKey]SimpleType{}}
}

func (in *Inferer) freshVar(level int) *Variable {
	v := &Variable{id: in.varCounter, level: level}
	in.varCounter++
	return v
}

type constraintKey struct{ lhs, rhs SimpleType }

// Constrain asserts lhs <: rhs, mutating bound lists. Empty result == success.
func (in *Inferer) Constrain(lhs, rhs SimpleType) []error {
	return in.constrain(lhs, rhs, map[constraintKey]bool{})
}

func (in *Inferer) constrain(lhs, rhs SimpleType, seen map[constraintKey]bool) []error {
	key := constraintKey{lhs, rhs}
	if seen[key] {
		return nil
	}
	seen[key] = true

	// Union/intersection lattice rules, applied before the structural and
	// variable cases because a union/intersection on either side decomposes
	// regardless of the other side. The two "for all" rules (a union on the
	// left, an intersection on the right) come before the two "exists" rules (a
	// union on the right, an intersection on the left): the universal sides
	// must always be fully decomposed, while the existential sides pick a
	// witness.
	if u, ok := lhs.(*Union); ok {
		// (A | B) <: Y  iff  A <: Y and B <: Y.
		var errs []error
		for _, m := range u.types {
			errs = append(errs, in.constrain(m, rhs, seen)...)
		}
		return errs
	}
	if i, ok := rhs.(*Intersection); ok {
		// X <: (A & B)  iff  X <: A and X <: B.
		var errs []error
		for _, m := range i.types {
			errs = append(errs, in.constrain(lhs, m, seen)...)
		}
		return errs
	}
	// The two "exists" rules pick a member as a witness by trial. That trial is
	// only sound when the *other* side is concrete: trying a branch against a
	// Variable would speculatively add a bound and "succeed", permanently
	// pinning the variable to the first member even though the choice was meant
	// to be non-committal. When the other side is a Variable we therefore fall
	// through to the variable case, which records the whole union/intersection
	// as a bound — sound, and what coalescing already expects. (A production
	// checker would instead probe-and-roll-back per the journaled-Probe design;
	// the spike takes the conservative route.)
	if u, ok := rhs.(*Union); ok {
		if !containsVariable(lhs) {
			// X <: (A | B)  iff  X <: A or X <: B.
			for _, m := range u.types {
				if errs := in.constrain(lhs, m, seen); len(errs) == 0 {
					return nil
				}
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
		}
	}
	if i, ok := lhs.(*Intersection); ok {
		if !containsVariable(rhs) {
			// (A & B) <: Y  iff  A <: Y or B <: Y.
			for _, m := range i.types {
				if errs := in.constrain(m, rhs, seen); len(errs) == 0 {
					return nil
				}
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
		}
	}

	// Structural cases first; fall through to the variable cases when a side
	// that didn't match here is a Variable.
	switch l := lhs.(type) {
	case *Primitive:
		if r, ok := rhs.(*Primitive); ok {
			if r.name == l.name {
				return nil
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", l.name, r.name)}
		}
	case *Literal:
		if r, ok := rhs.(*Literal); ok {
			if l.eq(r) {
				return nil
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(l), describe(r))}
		}
		if r, ok := rhs.(*Primitive); ok {
			if litKindPrim(l) == r.name {
				return nil // a literal is a subtype of its primitive
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(l), r.name)}
		}
	case *Function:
		if r, ok := rhs.(*Function); ok {
			// A function with FEWER params is a subtype of one with more: the
			// supertype's extra trailing params are ignored. l <: r requires
			// len(l.params) <= len(r.params).
			if len(l.params) > len(r.params) {
				return []error{fmt.Errorf(
					"cannot constrain function of arity %d <: function of arity %d",
					len(l.params), len(r.params))}
			}
			var errs []error
			for i := range l.params {
				errs = append(errs, in.constrain(r.params[i], l.params[i], seen)...) // contravariant
			}
			errs = append(errs, in.constrain(l.ret, r.ret, seen)...) // covariant
			return errs
		}
	case *Tuple:
		if r, ok := rhs.(*Tuple); ok {
			if len(l.elems) != len(r.elems) {
				return []error{fmt.Errorf(
					"cannot constrain tuple of length %d <: tuple of length %d",
					len(l.elems), len(r.elems))}
			}
			var errs []error
			for i := range l.elems {
				errs = append(errs, in.constrain(l.elems[i], r.elems[i], seen)...) // covariant
			}
			return errs
		}
	case *Record:
		if r, ok := rhs.(*Record); ok {
			// Width + depth subtyping: l must have every field r requires (l may
			// have more), and each shared field is covariant.
			var errs []error
			for name, rt := range r.fields {
				lt, ok := l.fields[name]
				if !ok {
					errs = append(errs, fmt.Errorf("record is missing field %q", name))
					continue
				}
				errs = append(errs, in.constrain(lt, rt, seen)...)
			}
			return errs
		}
	case *Mut:
		if r, ok := rhs.(*Mut); ok {
			// Invariance via the read/write decomposition: the read view is
			// covariant (l.inner <: r.inner) and the write view is contravariant
			// (r.inner <: l.inner). Emitting both directions forces the contents
			// to be equal, so e.g. `mut {x,y} <: mut {x}` fails even though the
			// immutable `{x,y} <: {x}` succeeds by width subtyping.
			errs := in.constrain(l.inner, r.inner, seen)                 // read (covariant)
			return append(errs, in.constrain(r.inner, l.inner, seen)...) // write (contravariant)
		}
		// Against a variable, record the Mut itself as a bound (fall through to
		// the variable cases below) so the result still renders as `mut ...`.
		// Against any other concrete type, a mutable reference can be read where
		// an immutable value is expected: mut T <: U via the read view, T <: U.
		if _, ok := rhs.(*Variable); !ok {
			return in.constrain(l.inner, rhs, seen)
		}
	case *Void:
		if _, ok := rhs.(*Void); ok {
			return nil
		}
	case *Alias:
		// An alias is structurally its body. Constrain through to the body on
		// whichever side is an alias, so `mut Point <: mut {x: number}` and
		// alias-vs-alias both reduce to structural checks.
		if r, ok := rhs.(*Alias); ok {
			return in.constrain(l.body, r.body, seen)
		}
		return in.constrain(l.body, rhs, seen)
	}
	if r, ok := rhs.(*Alias); ok {
		// lhs is not an alias (the switch above didn't return): relate it to the
		// alias's body.
		if _, lvar := lhs.(*Variable); !lvar {
			return in.constrain(lhs, r.body, seen)
		}
	}

	// lhs is a variable.
	if lv, ok := lhs.(*Variable); ok {
		if levelOf(rhs) <= lv.level {
			lv.upperBounds = append(lv.upperBounds, rhs)
			var errs []error
			for _, lb := range lv.lowerBounds {
				errs = append(errs, in.constrain(lb, rhs, seen)...)
			}
			return errs
		}
		// rhs lives at a higher level: extrude it down so it isn't wrongly
		// generalized at lv's level.
		return in.constrain(lhs, in.extrude(rhs, Negative, lv.level, map[int]*Variable{}), seen)
	}
	// rhs is a variable.
	if rv, ok := rhs.(*Variable); ok {
		if levelOf(lhs) <= rv.level {
			rv.lowerBounds = append(rv.lowerBounds, lhs)
			var errs []error
			for _, ub := range rv.upperBounds {
				errs = append(errs, in.constrain(lhs, ub, seen)...)
			}
			return errs
		}
		return in.constrain(in.extrude(lhs, Positive, rv.level, map[int]*Variable{}), rhs, seen)
	}

	return []error{fmt.Errorf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
}

// extrude copies ty so that variables above lvl are replaced by fresh variables
// at lvl, wired to the originals through the appropriate bound direction.
func (in *Inferer) extrude(ty SimpleType, pol Polarity, lvl int, cache map[int]*Variable) SimpleType {
	if levelOf(ty) <= lvl {
		return ty
	}
	switch t := ty.(type) {
	case *Variable:
		if nv, ok := cache[t.id]; ok {
			return nv
		}
		nv := in.freshVar(lvl)
		cache[t.id] = nv
		if pol == Positive {
			t.upperBounds = append(t.upperBounds, nv)
			for _, lb := range t.lowerBounds {
				nv.lowerBounds = append(nv.lowerBounds, in.extrude(lb, pol, lvl, cache))
			}
		} else {
			t.lowerBounds = append(t.lowerBounds, nv)
			for _, ub := range t.upperBounds {
				nv.upperBounds = append(nv.upperBounds, in.extrude(ub, pol, lvl, cache))
			}
		}
		return nv
	case *Function:
		params := make([]SimpleType, len(t.params))
		for i, p := range t.params {
			params[i] = in.extrude(p, pol.flip(), lvl, cache)
		}
		return &Function{params: params, paramNames: t.paramNames, ret: in.extrude(t.ret, pol, lvl, cache)}
	case *Tuple:
		elems := make([]SimpleType, len(t.elems))
		for i, e := range t.elems {
			elems[i] = in.extrude(e, pol, lvl, cache)
		}
		return &Tuple{elems: elems}
	case *Record:
		fields := make(map[string]SimpleType, len(t.fields))
		for name, f := range t.fields {
			fields[name] = in.extrude(f, pol, lvl, cache)
		}
		return &Record{fields: fields, lt: t.lt}
	case *Mut:
		// inner is invariant, so it is reachable in both polarities; extrude it
		// in the current polarity (the read view) — the write view shares the
		// same fresh variables via the cache.
		return &Mut{inner: in.extrude(t.inner, pol, lvl, cache)}
	case *Alias:
		return &Alias{name: t.name, body: in.extrude(t.body, pol, lvl, cache), lt: t.lt}
	case *ResidualOp:
		return &ResidualOp{kind: t.kind, operand: in.extrude(t.operand, pol, lvl, cache), key: t.key}
	case *Union:
		return &Union{types: in.extrudeAll(t.types, pol, lvl, cache)}
	case *Intersection:
		return &Intersection{types: in.extrudeAll(t.types, pol, lvl, cache)}
	default:
		return ty
	}
}

func (in *Inferer) extrudeAll(types []SimpleType, pol Polarity, lvl int, cache map[int]*Variable) []SimpleType {
	out := make([]SimpleType, len(types))
	for i, t := range types {
		out[i] = in.extrude(t, pol, lvl, cache)
	}
	return out
}

func describe(st SimpleType) string {
	switch t := st.(type) {
	case *Primitive:
		return t.name
	case *Literal:
		switch t.kind {
		case "str":
			return strconv.Quote(t.str)
		case "num":
			return strconv.FormatFloat(t.num, 'f', -1, 32)
		case "bool":
			return strconv.FormatBool(t.b)
		}
	case *Function:
		return "function"
	case *Tuple:
		return "tuple"
	case *Record:
		return "record"
	case *Mut:
		return "mut " + describe(t.inner)
	case *Void:
		return "void"
	case *Alias:
		return t.name
	case *Union:
		return joinDescribe(t.types, " | ")
	case *Intersection:
		return joinDescribe(t.types, " & ")
	case *Variable:
		return "t" + strconv.Itoa(t.id)
	}
	return "?"
}

func joinDescribe(types []SimpleType, sep string) string {
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = describe(t)
	}
	return strings.Join(parts, sep)
}

// containsVariable reports whether t contains any Variable, anywhere. The
// existential union/intersection rules trial a member via structural subtyping,
// which can speculatively bind a *nested* variable (not just a top-level one) —
// so the "is the other side concrete?" guard must look through composites, not
// just check the head. When a variable is present we fall through to the
// variable case instead of trialling.
func containsVariable(t SimpleType) bool {
	switch ty := t.(type) {
	case *Variable:
		return true
	case *Function:
		for _, p := range ty.params {
			if containsVariable(p) {
				return true
			}
		}
		return containsVariable(ty.ret)
	case *Tuple:
		for _, e := range ty.elems {
			if containsVariable(e) {
				return true
			}
		}
		return false
	case *Record:
		for _, f := range ty.fields {
			if containsVariable(f) {
				return true
			}
		}
		return false
	case *Mut:
		return containsVariable(ty.inner)
	case *Alias:
		return containsVariable(ty.body)
	case *Union:
		for _, m := range ty.types {
			if containsVariable(m) {
				return true
			}
		}
		return false
	case *Intersection:
		for _, m := range ty.types {
			if containsVariable(m) {
				return true
			}
		}
		return false
	default:
		// Primitive, Literal, Void, ResidualOp: no embedded variables to bind.
		return false
	}
}

// widen generalizes a literal to its primitive type. A value stored through a
// mutable reference is observed at its widened type — writing `5` into a field
// makes that field `number`, not the literal `5`, because a later write could
// store any number. Non-literals are returned unchanged.
func widen(st SimpleType) SimpleType {
	if l, ok := st.(*Literal); ok {
		return &Primitive{name: litKindPrim(l)}
	}
	return st
}

func litKindPrim(l *Literal) string {
	switch l.kind {
	case "str":
		return "string"
	case "num":
		return "number"
	case "bool":
		return "boolean"
	}
	return ""
}

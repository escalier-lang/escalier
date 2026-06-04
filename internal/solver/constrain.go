package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// constraintKey keys the coinductive seen-set by pointer identity (Go's
// interface == on pointer-backed soltype concretes). Sufficient for M1: cycles
// in subtype-checking can only form via TypeVarTypes, and TypeVarType pointers
// are stable throughout inference (extrude allocates fresh vars, but those are
// stable thereafter; structural decomposition hands child pointers around
// without copying). Structurally-equal-but-pointer-distinct duplicates produce
// redundant cache entries, not infinite loops. M4's recursive types (aliases,
// letrec) must preserve this property — see m1-implementation-plan §3.3
// "Forward requirements for the named-ref node design".
type constraintKey struct{ lhs, rhs soltype.Type }

// extrudeKey keys extrude's per-extrusion cache by both the origin variable's
// ID and the polarity it was reached in, so the same variable copied in
// covariant and contravariant position produces two distinct fresh vars.
type extrudeKey struct {
	id  int
	pol soltype.Polarity
}

// Constrain asserts lhs <: rhs, mutating bound lists. An empty result means
// success.
func (c *Context) Constrain(lhs, rhs soltype.Type) []SolverError {
	return c.constrain(lhs, rhs, set.NewSet[constraintKey]())
}

func (c *Context) constrain(lhs, rhs soltype.Type, seen set.Set[constraintKey]) []SolverError {
	key := constraintKey{lhs, rhs}
	if seen.Contains(key) {
		return nil
	}
	seen.Add(key)

	// Structural cases first; fall through to the variable cases when a side
	// that didn't match here is a TypeVarType.
	switch l := lhs.(type) {
	case *soltype.PrimType:
		if r, ok := rhs.(*soltype.PrimType); ok {
			if r.Prim == l.Prim {
				return nil
			}
			return []SolverError{&CannotConstrainError{LHS: l, RHS: r}}
		}
	case *soltype.LitType:
		if r, ok := rhs.(*soltype.LitType); ok {
			if l.Equal(r) {
				return nil
			}
			return []SolverError{&CannotConstrainError{LHS: l, RHS: r}}
		}
		if r, ok := rhs.(*soltype.PrimType); ok {
			if primOf(l.Lit) == r.Prim {
				return nil // a literal is a subtype of its primitive
			}
			return []SolverError{&CannotConstrainError{LHS: l, RHS: r}}
		}
	case *soltype.FuncType:
		if r, ok := rhs.(*soltype.FuncType); ok {
			// Exact arity: M1 functions are exact (Escalier is exact-by-default),
			// so subtyping requires the SAME number of params — parallel to the
			// exact-tuple same-length rule below. The inexact
			// "fewer-params-is-subtype" arm (a function ignoring extra trailing
			// args) lands in M3 with the exactness flag (`...`), exactly as the
			// inexact-tuple arm lands in M4. See
			// planning/simple_sub/01-milestones.md.
			if len(l.Params) != len(r.Params) {
				return []SolverError{&FuncArityMismatchError{LHS: l, RHS: r}}
			}
			var errs []SolverError
			for i := range l.Params {
				errs = append(errs, c.constrain(r.Params[i].Type, l.Params[i].Type, seen)...) // contravariant
			}
			return append(errs, c.constrain(l.Ret, r.Ret, seen)...) // covariant
		}
	case *soltype.TupleType:
		if r, ok := rhs.(*soltype.TupleType); ok {
			// Same length, element-wise covariant. M1's TupleType has no exact
			// flag — same-length is the *exact <: exact* case applied
			// implicitly; M4 adds the exact flag and the inexact arm.
			if len(l.Elems) != len(r.Elems) {
				return []SolverError{&TupleLengthMismatchError{LHS: l, RHS: r}}
			}
			var errs []SolverError
			for i := range l.Elems {
				errs = append(errs, c.constrain(l.Elems[i], r.Elems[i], seen)...) // covariant
			}
			return errs
		}
	case *soltype.RecordType:
		if r, ok := rhs.(*soltype.RecordType); ok {
			// Width + depth subtyping: every field the RHS requires must be present
			// on the LHS (the LHS may carry MORE fields — width), and the shared
			// fields are covariant (depth). M2 records are read-only — `mut` makes a
			// field invariant, and that lands in M4 — so there is no invariance arm
			// here. Fields are matched by name, so source order is irrelevant.
			lf := make(map[string]soltype.Type, len(l.Fields))
			for _, f := range l.Fields {
				lf[f.Name] = f.Type
			}
			var errs []SolverError
			for _, rf := range r.Fields {
				lt, ok := lf[rf.Name]
				if !ok {
					errs = append(errs, &MissingPropertyError{LHS: l, RHS: r, Name: rf.Name})
					continue
				}
				errs = append(errs, c.constrain(lt, rf.Type, seen)...) // covariant
			}
			return errs
		}
	case *soltype.Void:
		if _, ok := rhs.(*soltype.Void); ok {
			return nil
		}
	}

	// lhs is a variable: record rhs as an upper bound, propagate existing lowers.
	if lv, ok := lhs.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(rhs) <= lv.Level {
			lv.UpperBounds = append(lv.UpperBounds, rhs)
			var errs []SolverError
			for _, lb := range lv.LowerBounds {
				errs = append(errs, c.constrain(lb, rhs, seen)...)
			}
			return errs
		}
		// rhs lives at a higher level: extrude it down so it isn't wrongly
		// generalized at lv's level.
		return c.constrain(lhs, c.extrude(rhs, soltype.Negative, lv.Level, map[extrudeKey]*soltype.TypeVarType{}), seen)
	}
	// rhs is a variable: symmetric — record lhs as a lower bound, propagate uppers.
	if rv, ok := rhs.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(lhs) <= rv.Level {
			rv.LowerBounds = append(rv.LowerBounds, lhs)
			var errs []SolverError
			for _, ub := range rv.UpperBounds {
				errs = append(errs, c.constrain(lhs, ub, seen)...)
			}
			return errs
		}
		return c.constrain(c.extrude(lhs, soltype.Positive, rv.Level, map[extrudeKey]*soltype.TypeVarType{}), rhs, seen)
	}

	return []SolverError{&CannotConstrainError{LHS: lhs, RHS: rhs}}
}

// extrude copies t so that variables above lvl are replaced by fresh variables
// at lvl, wired to the originals through the polarity-appropriate bound
// direction. The cache is keyed by (var ID, polarity): a variable reached in
// both polarities during a single extrusion must yield two distinct fresh vars
// with opposite bound wiring (matching canonical Simple-sub's PolarVariable
// cache). Keying by ID alone would reuse a Negative-polarity copy in Positive
// position — skipping its covariant bounds — and vice versa.
func (c *Context) extrude(t soltype.Type, pol soltype.Polarity, lvl int, cache map[extrudeKey]*soltype.TypeVarType) soltype.Type {
	if soltype.LevelOf(t) <= lvl {
		return t
	}
	switch t := t.(type) {
	case *soltype.TypeVarType:
		key := extrudeKey{id: t.ID, pol: pol}
		if nv, ok := cache[key]; ok {
			return nv
		}
		nv := c.freshVar(lvl)
		cache[key] = nv
		if pol == soltype.Positive {
			t.UpperBounds = append(t.UpperBounds, nv)
			for _, lb := range t.LowerBounds {
				nv.LowerBounds = append(nv.LowerBounds, c.extrude(lb, pol, lvl, cache))
			}
		} else {
			t.LowerBounds = append(t.LowerBounds, nv)
			for _, ub := range t.UpperBounds {
				nv.UpperBounds = append(nv.UpperBounds, c.extrude(ub, pol, lvl, cache))
			}
		}
		return nv
	case *soltype.FuncType:
		params := make([]*soltype.FuncParam, len(t.Params))
		for i, p := range t.Params {
			params[i] = &soltype.FuncParam{Pattern: p.Pattern, Type: c.extrude(p.Type, pol.Flip(), lvl, cache)}
		}
		return &soltype.FuncType{Params: params, Ret: c.extrude(t.Ret, pol, lvl, cache)}
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = c.extrude(e, pol, lvl, cache)
		}
		return &soltype.TupleType{Elems: elems}
	case *soltype.RecordType:
		fields := make([]*soltype.RecordField, len(t.Fields))
		for i, f := range t.Fields {
			// Fields are covariant (read-only records in M2), so no polarity flip.
			fields[i] = &soltype.RecordField{Name: f.Name, Type: c.extrude(f.Type, pol, lvl, cache)}
		}
		return &soltype.RecordType{Fields: fields}
	default:
		return t
	}
}

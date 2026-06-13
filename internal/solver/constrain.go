package solver

import (
	"math"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// unboundedArity is the upper end of an inexact function's accept-set ([req, ∞)):
// an inexact function tolerates any number of trailing arguments as a callback.
const unboundedArity = math.MaxInt

// hasRest reports whether f's LAST parameter is a typed rest param (`...xs: T[]`).
// A rest param binds zero or more trailing arguments, so it never counts toward the
// required floor and lifts the accept-set upper bound to ∞ (#677 §4.2.3) — the same
// upper-bound effect as the inexact `...` marker, reached a different way.
func hasRest(f *soltype.FuncType) bool {
	n := len(f.Params)
	return n > 0 && f.Params[n-1].Rest
}

// requiredCount is the number of arguments a positional call must supply — the
// LOWER bound of f's accept-set. Because arguments bind positionally, a parameter
// only lowers the requirement when it is TRAILING: a trailing rest param (zero or
// more) and trailing optionals (`x?`) may be omitted, but in fn(a?, b) you cannot
// omit a while still supplying b, so a is effectively required. So required = the
// position after the last non-optional, non-rest param — NOT the count of all
// non-optional params, which would wrongly treat a leading optional (or the rest
// param) as droppable and let a call leave a required param unbound.
func requiredCount(f *soltype.FuncType) int {
	n := len(f.Params)
	if n > 0 && f.Params[n-1].Rest {
		n-- // a trailing rest param binds zero-or-more args, so it is never required
	}
	for n > 0 && f.Params[n-1].Optional {
		n--
	}
	return n
}

// acceptSet is the inclusive range [lo, hi] of argument counts f tolerates when
// invoked (#677 §4.2.1): lo = requiredCount(f); hi = len(f.Params) when f has a
// finite arity, and unboundedArity when its upper bound is open — either because it
// is inexact (the `...` marker) OR because its last param is a typed rest (§4.2.3).
// Read a supertype callback slot's accept-set as "the argument counts whoever holds
// this slot may invoke the supplied function with."
func acceptSet(f *soltype.FuncType) (lo, hi int) {
	lo = requiredCount(f)
	if f.Inexact || hasRest(f) {
		hi = unboundedArity
	} else {
		hi = len(f.Params)
	}
	return lo, hi
}

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

	// Error-recovery sentinel (PR8): an ErrorType operand carries an
	// already-reported diagnostic, so it ABSORBS in both directions — the
	// constraint trivially succeeds. Short-circuiting here, ABOVE the structural
	// switch and the variable arms, keeps it out of every bound list, so a
	// reported diagnostic's placeholder never cascades a second, spurious failure
	// (and coalesce / extrude / freshenAbove never see it propagated through
	// bounds). Unlike never (⊥) / unknown (⊤), which are coalesced-output only,
	// ErrorType is a legal constrain input.
	if _, ok := lhs.(*soltype.ErrorType); ok {
		return nil
	}
	if _, ok := rhs.(*soltype.ErrorType); ok {
		return nil
	}

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
			// Accept-set subtyping (#677 §4.2.1): read r as a callback slot. l <: r
			// iff accept(l) ⊇ accept(r) — l must tolerate every argument count a
			// holder of r may invoke it with. With accept(l) = [loL, hiL] and
			// accept(r) = [loR, hiR]:
			//   - loL <= loR — l must not DEMAND more args than r might supply, and
			//   - hiL >= hiR — l must not REFUSE an arg count r might supply.
			// The upper-bound clause is what exactness governs (an exact l caps hiL at
			// len(l.Params), so it can't fill a wider/inexact slot); the lower-bound
			// clause is the `required` part (a typed-rest/optional lowers it). This
			// subsumes M2's exact-same-arity rule: two EXACT functions have accept
			// [r, n], so ⊇ forces equal upper bounds, i.e. the old same-arity check.
			loL, hiL := acceptSet(l)
			loR, hiR := acceptSet(r)
			if loL > loR || hiL < hiR {
				return []SolverError{&FuncArityMismatchError{LHS: l, RHS: r}}
			}
			// Shared positions are checked per-parameter (params contravariant,
			// return covariant). When r is EXACT this is complete: r never supplies an
			// argument beyond its declared params, and any extra param l declares there
			// must be optional (the lo gate forced loL <= loR) and so is simply never
			// passed.
			//
			// KNOWN GAP (M4): when r is INEXACT and l declares MORE params than r, r's
			// `...` tail may supply arbitrarily-typed args at l's extra positions, so
			// soundness demands `unknown <: l.Params[i].Type` there — exact-types
			// §4.2.1.2 "Variation B", the load-bearing rejection. That check needs the
			// `_ <: unknown` (⊤) rule constrain lacks until M6, and an inexact function
			// is unreachable from M3 source anyway (resolveTypeAnn resolves no function
			// annotations), so the extra positions are left unchecked here for now. For
			// every M3-reachable input (exact functions only) the shared loop is complete.
			var errs []SolverError
			n := min(len(l.Params), len(r.Params))
			for i := 0; i < n; i++ {
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
	case *soltype.ObjectType:
		if r, ok := rhs.(*soltype.ObjectType); ok {
			// One ObjectType <: ObjectType rule serves both uses the M2 arm
			// conflated: member-access field SELECTION (the RHS is an inexact
			// "has at least this field" requirement minted by inferMember) and
			// concrete object <: object SUBTYPING for object-typed params/annotations.
			// The Inexact flag is the split — width tolerance IS inexactness.
			//
			// Depth first: every property the RHS requires must be present on the
			// LHS, matched by name (Prop), and the shared property types are
			// covariant. A required property the LHS lacks is a MissingPropertyError.
			var errs []SolverError
			for _, re := range r.Elems {
				rp, ok := re.(*soltype.PropertyElem) // M4: every elem is a property
				if !ok {
					continue
				}
				lp, ok := l.Prop(rp.Name)
				if !ok {
					errs = append(errs, &MissingPropertyError{LHS: l, RHS: r, Name: rp.Name})
					continue
				}
				errs = append(errs, c.constrain(lp.Type, rp.Type, seen)...) // covariant
			}
			// One-way exactness (02-design-notes §"Exactness"):
			//   exact <: inexact    ok (width)      inexact <: inexact   ok (width)
			//   exact <: exact      same member set  inexact <: exact     rejected
			// When the RHS is inexact, width tolerance is the complete rule and the
			// depth loop above is all there is. When the RHS is exact, the LHS may
			// carry no extra properties and may not itself be inexact.
			if !r.Inexact {
				if l.Inexact {
					errs = append(errs, &InexactIntoExactError{LHS: l, RHS: r})
				}
				for _, le := range l.Elems {
					lp, ok := le.(*soltype.PropertyElem)
					if !ok {
						continue
					}
					if _, ok := r.Prop(lp.Name); !ok {
						errs = append(errs, &ExtraPropertyError{LHS: l, RHS: r, Name: lp.Name})
					}
				}
			}
			return errs
		}
	case *soltype.PromiseType:
		if r, ok := rhs.(*soltype.PromiseType); ok {
			// PromiseType is covariant in its Inner: Promise<L> <: Promise<R> iff
			// L <: R. No auto-flatten — `await Promise<Promise<T>>` yields
			// `Promise<T>` (Awaited<T> lands in M9). When the two sides are unrelated
			// concretes (e.g. Promise<L> <: Tuple), fall through to the generic
			// CannotConstrainError below, matching the function/tuple/record arms.
			return c.constrain(l.Inner, r.Inner, seen)
		}
	case *soltype.Void:
		if _, ok := rhs.(*soltype.Void); ok {
			return nil
		}
	case *soltype.IntersectionType:
		// Function-intersection LHS (PR6 scoped lattice exception): (A & B & …) <: rhs
		// iff SOME member <: rhs. This is the ONE place the overload disjunction touches
		// the lattice — reached only when an overloaded value (inferIdent synthesizes the
		// arm intersection) flows into a constraint, e.g. a let-bound overload called
		// through the binding (`g = f; g(x)`). The disjunction stays confined to the
		// speculation phase: each member is trialled under a probe in SPECIFICITY order
		// (the same specificityOrder resolveOverload uses for a direct call, so a call
		// through a binding resolves to the same arm a direct call would), the first
		// success commits its bounds and the losers roll back. General IntersectionType
		// subtyping (objects, distribution, normalization) is out of M3; a coalesced
		// intersection never reaches constrain as input (the design keeps these
		// output-only), so this arm is overload-synthesis-only.
		//
		// Collapse only against a CONCRETE demand. If rhs is a variable — the overloaded
		// value is flowing INTO a binding (`intersection <: b.v`) — don't collapse here.
		// Fall through to the var arm below, which records the intersection WHOLE as a
		// lower bound. Collapsing now would commit to one arm prematurely and discard the
		// rest of the overload set. The collapse fires later instead, once that var is
		// constrained against a concrete function demand (a call shape) and the whole
		// intersection propagates to it.
		if _, rhsIsVar := rhs.(*soltype.TypeVarType); !rhsIsVar && len(l.Types) > 0 {
			funcs := make([]*soltype.FuncType, len(l.Types))
			for i, m := range l.Types {
				funcs[i], _ = m.(*soltype.FuncType)
			}
			var lastErrs []SolverError
			for _, idx := range specificityOrder(funcs) {
				p := newProbe(c.probe)
				c.probe = p
				// A cloned seen keeps each arm's coinductive cache independent, so a failed
				// arm's entries can't wrongly short-circuit a later arm to success.
				errs := c.constrain(l.Types[idx], rhs, seen.Clone())
				c.probe = p.parent
				if len(errs) == 0 {
					p.Commit()
					return nil
				}
				p.Discard()
				lastErrs = errs
			}
			return lastErrs
		}
	}

	// lhs is a variable: record rhs as an upper bound, propagate existing lowers.
	if lv, ok := lhs.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(rhs) <= lv.Level {
			c.addUpperBound(lv, rhs)
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
			c.addLowerBound(rv, lhs)
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
	return t.Accept(&extruder{c: c, lvl: lvl, cache: cache}, pol)
}

// extruder is the soltype-visitor form of extrude. The structural arms and the
// variance flip come from soltype.Accept; the level prune and the var node — which
// mints a fresh var, wires it to the original through the polarity-appropriate
// bound direction, and mutates the original mid-walk — are the bespoke content,
// handled in EnterType.
type extruder struct {
	c     *Context
	lvl   int
	cache map[extrudeKey]*soltype.TypeVarType
}

func (e *extruder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	// A subtree with no variable above lvl extrudes to itself (identity-shared).
	if soltype.LevelOf(t) <= e.lvl {
		return soltype.EnterResult{Type: t, SkipChildren: true}
	}
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural node: let Accept rebuild it
	}
	key := extrudeKey{id: v.ID, pol: pol}
	if nv, ok := e.cache[key]; ok {
		return soltype.EnterResult{Type: nv, SkipChildren: true}
	}
	nv := e.c.freshVar(e.lvl)
	e.cache[key] = nv
	// v is the original (possibly pre-probe) var — its append is the one a Discard
	// must truncate. nv was just minted here, so journaling it is a harmless no-op
	// (a fresh var is unreachable after a Discard); it goes through the helpers
	// only to keep a single uniform append path.
	if pol == soltype.Positive {
		e.c.addUpperBound(v, nv)
		for _, lb := range v.LowerBounds {
			e.c.addLowerBound(nv, lb.Accept(e, pol))
		}
	} else {
		e.c.addLowerBound(v, nv)
		for _, ub := range v.UpperBounds {
			e.c.addUpperBound(nv, ub.Accept(e, pol))
		}
	}
	return soltype.EnterResult{Type: nv, SkipChildren: true}
}

func (e *extruder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

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
type constraintKey struct{ sub, super soltype.Type }

// extrudeKey keys extrude's per-extrusion cache by both the origin variable's
// ID and the polarity it was reached in, so the same variable copied in
// covariant and contravariant position produces two distinct fresh vars.
type extrudeKey struct {
	id  int
	pol soltype.Polarity
}

// Constrain asserts sub <: super, mutating bound lists. An empty result means
// success.
//
// Naming: sub is the subtype — the source value being checked; super is the
// supertype — the target/expected type. In `x = e`, the value `e` is sub and the
// target `x` is the super. The checker-level wrapper (checker.constrain) names
// these source/target, which map to sub/super here.
func (c *Context) Constrain(sub, super soltype.Type) []SolverError {
	return c.constrain(sub, super, set.NewSet[constraintKey]())
}

func (c *Context) constrain(sub, super soltype.Type, seen set.Set[constraintKey]) []SolverError {
	key := constraintKey{sub, super}
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
	if _, ok := sub.(*soltype.ErrorType); ok {
		return nil
	}
	if _, ok := super.(*soltype.ErrorType); ok {
		return nil
	}

	// Structural cases first; fall through to the variable cases when a side
	// that didn't match here is a TypeVarType.
	switch sub := sub.(type) {
	case *soltype.PrimType:
		if sup, ok := super.(*soltype.PrimType); ok {
			if sup.Prim == sub.Prim {
				return nil
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: sup}}
		}
	case *soltype.LitType:
		if sup, ok := super.(*soltype.LitType); ok {
			if sub.Equal(sup) {
				return nil
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: sup}}
		}
		if sup, ok := super.(*soltype.PrimType); ok {
			if primOf(sub.Lit) == sup.Prim {
				return nil // a literal is a subtype of its primitive
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: sup}}
		}
	case *soltype.FuncType:
		if sup, ok := super.(*soltype.FuncType); ok {
			// Accept-set subtyping (#677 §4.2.1): read super as a callback slot.
			// sub <: super iff accept(sub) ⊇ accept(super) — sub must tolerate every
			// argument count a holder of super may invoke it with. With
			// accept(sub) = [loSub, hiSub] and accept(super) = [loSup, hiSup]:
			//   - loSub <= loSup — sub must not DEMAND more args than super might supply,
			//   - hiSub >= hiSup — sub must not REFUSE an arg count super might supply.
			// The upper-bound clause is what exactness governs (an exact sub caps hiSub
			// at len(sub.Params), so it can't fill a wider/inexact slot); the lower-bound
			// clause is the `required` part (a typed-rest/optional lowers it). This
			// subsumes M2's exact-same-arity rule: two EXACT functions have accept
			// [r, n], so ⊇ forces equal upper bounds, i.e. the old same-arity check.
			loSub, hiSub := acceptSet(sub)
			loSup, hiSup := acceptSet(sup)
			if loSub > loSup || hiSub < hiSup {
				return []SolverError{&FuncArityMismatchError{Sub: sub, Super: sup}}
			}
			// Shared positions are checked per-parameter (params contravariant,
			// return covariant). When super is EXACT this is complete: super never
			// supplies an argument beyond its declared params, and any extra param sub
			// declares there must be optional (the lo gate forced loSub <= loSup) and so
			// is simply never passed.
			//
			// KNOWN GAP (M4): when super is INEXACT and sub declares MORE params than
			// super, super's `...` tail may supply arbitrarily-typed args at sub's extra
			// positions, so soundness demands `unknown <: sub.Params[i].Type` there —
			// exact-types §4.2.1.2 "Variation B", the load-bearing rejection. That check
			// needs the `_ <: unknown` (⊤) rule constrain lacks until M6, and an inexact
			// function is unreachable from M3 source anyway (resolveTypeAnn resolves no
			// function annotations), so the extra positions are left unchecked here for
			// now. For every M3-reachable input (exact functions only) the loop is complete.
			var errs []SolverError
			n := min(len(sub.Params), len(sup.Params))
			for i := 0; i < n; i++ {
				errs = append(errs, c.constrain(sup.Params[i].Type, sub.Params[i].Type, seen)...) // contravariant
			}
			return append(errs, c.constrain(sub.Ret, sup.Ret, seen)...) // covariant
		}
	case *soltype.TupleType:
		if sup, ok := super.(*soltype.TupleType); ok {
			// Element-wise covariant over the shared prefix. Length tolerance follows
			// the super's exactness: an exact super (`[A, B]`) fixes its length, while
			// an inexact super (`[A, ...]`) only requires the sub to be at least as
			// long — the longer <: shorter case the `...` tail permits. This mirrors
			// the ObjectType width rule (inexact super = width-tolerant).
			if sup.Inexact {
				if len(sub.Elems) < len(sup.Elems) {
					return []SolverError{&TupleLengthMismatchError{Sub: sub, Super: sup}}
				}
			} else if len(sub.Elems) != len(sup.Elems) {
				return []SolverError{&TupleLengthMismatchError{Sub: sub, Super: sup}}
			}
			var errs []SolverError
			// Range over sup.Elems, not sub.Elems: an inexact super (`[A, ...]`) lets
			// the sub be longer, so sup is the shorter side. This walks the shared
			// prefix and keeps sup.Elems[i] in bounds.
			for i := range sup.Elems {
				errs = append(errs, c.constrain(sub.Elems[i], sup.Elems[i], seen)...) // covariant
			}
			return errs
		}
	case *soltype.ObjectType:
		if sup, ok := super.(*soltype.ObjectType); ok {
			// One ObjectType <: ObjectType rule serves both uses the M2 arm
			// conflated: member-access field SELECTION (the super is an inexact
			// "has at least this field" requirement minted by inferMember) and
			// concrete object <: object SUBTYPING for object-typed params/annotations.
			// The Inexact flag is the split — width tolerance IS inexactness.
			//
			// Depth first: every property the super requires must be present on the
			// sub, matched by name (Prop), and the shared property types are
			// covariant. PropertyElem.Optional makes presence part of the shape, so
			// the loop is presence-aware before recursing on the property type:
			//   - absent on the sub: a MissingPropertyError only when the super
			//     property is REQUIRED; an optional super property may be absent.
			//   - present on both, optional on the sub but required on the super: the
			//     source may omit it, so it cannot fill a required slot —
			//     OptionalPropertyError, and skip the covariant type check (the
			//     presence mismatch already rejects the constraint).
			//   - otherwise (required<:required, required<:optional, optional<:
			//     optional): covariant on the property type.
			var errs []SolverError
			for _, superElem := range sup.Elems {
				superProp := soltype.AsProperty(superElem) // M4: every elem is a property
				subProp, ok := sub.Prop(superProp.Name)
				if !ok {
					if !superProp.Optional {
						errs = append(errs, &MissingPropertyError{Sub: sub, Super: sup, Name: superProp.Name})
					}
					continue
				}
				if subProp.Optional && !superProp.Optional {
					errs = append(errs, &OptionalPropertyError{Sub: sub, Super: sup, Name: superProp.Name})
					continue
				}
				errs = append(errs, c.constrain(subProp.Type, superProp.Type, seen)...) // covariant
			}
			// One-way exactness (02-design-notes §"Exactness"):
			//   exact <: inexact    ok (width)      inexact <: inexact   ok (width)
			//   exact <: exact      same member set  inexact <: exact     rejected
			// When the super is inexact, width tolerance is the complete rule and the
			// depth loop above is all there is. When the super is exact, the sub may
			// carry no extra properties and may not itself be inexact.
			if !sup.Inexact {
				if sub.Inexact {
					errs = append(errs, &InexactIntoExactError{Sub: sub, Super: sup})
				}
				for _, subElem := range sub.Elems {
					subProp := soltype.AsProperty(subElem)
					if _, ok := sup.Prop(subProp.Name); !ok {
						errs = append(errs, &ExtraPropertyError{Sub: sub, Super: sup, Name: subProp.Name})
					}
				}
			}
			return errs
		}
	case *soltype.PromiseType:
		if sup, ok := super.(*soltype.PromiseType); ok {
			// PromiseType is covariant in its Inner: Promise<L> <: Promise<R> iff
			// L <: R. No auto-flatten — `await Promise<Promise<T>>` yields
			// `Promise<T>` (Awaited<T> lands in M9). When the two sides are unrelated
			// concretes (e.g. Promise<L> <: Tuple), fall through to the generic
			// CannotConstrainError below, matching the function/tuple/record arms.
			return c.constrain(sub.Inner, sup.Inner, seen)
		}
	case *soltype.RefType:
		// THE GATE (M4 C2): the single RefType <: RefType rule. The mut-driven inner
		// invariance is the highest-risk encoding in the migration — see the M4 plan.
		if sup, ok := super.(*soltype.RefType); ok {
			// 1. Mutability compatibility: an immutable source cannot fill a mutable
			//    slot (writing through the target would mutate a read-only borrow). The
			//    reverse, mut-decay (mut sub, immutable super), is allowed and falls
			//    through to the covariant read view below.
			if !sub.Mut && sup.Mut {
				return []SolverError{&MutabilityMismatchError{Sub: sub, Super: sup}}
			}
			// 2. Inner variance: the read view is always covariant; a mutable target
			//    also takes a contravariant write view, and read + write together IS
			//    invariance. So `mut {x, y} <: mut {x, ...}` rejects (the write view's
			//    `{x, ...} <: {x, y}` is missing y), while `{x, y} <: {x, ...}` as bare
			//    objects width-succeeds.
			//
			//    The write view gates on `sup.Mut`, which is load-bearing-equivalent to
			//    `sub.Mut && sup.Mut`: the mutability check above already returned for
			//    `!sub.Mut && sup.Mut`, so reaching here with sup.Mut implies sub.Mut.
			//    If that earlier gate is ever weakened, re-gate the write view explicitly
			//    or it would impose a spurious contravariant constraint on an immutable
			//    source.
			errs := c.constrain(sub.Inner, sup.Inner, seen)
			if sup.Mut {
				errs = append(errs, c.constrain(sup.Inner, sub.Inner, seen)...)
			}
			// 3. Lifetime outlives, covariant. Written now, INERT in C2 because Lt is
			//    always nil until the lifetime sort lands (D1); constrainLt wires the
			//    var-to-var case in D2. The escape branch's error is unreachable until
			//    borrows carry lifetimes.
			switch {
			case sub.Lt != nil && sup.Lt != nil:
				// D2: c.constrainLt(sup.Lt, sub.Lt)
			case sub.Lt == nil && sup.Lt != nil:
				// An owned source satisfies any borrow slot — no lifetime constraint.
			case sub.Lt != nil && sup.Lt == nil:
				errs = append(errs, &BorrowEscapeError{Sub: sub, Super: sup})
			}
			return errs
		}
		// RefType <: a concrete non-borrow: peel to the inner. An owned value (Lt nil)
		// satisfies a bare slot; a borrow escaping into an owned slot is a
		// BorrowEscapeError (inert in C2). When super is a VARIABLE, fall through to the
		// var arm so the WHOLE borrow is recorded as a bound — peeling there would drop
		// its mutability.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if sub.Lt != nil {
				return []SolverError{&BorrowEscapeError{Sub: sub, Super: super}}
			}
			return c.constrain(sub.Inner, super, seen)
		}
	case *soltype.Void:
		if _, ok := super.(*soltype.Void); ok {
			return nil
		}
	case *soltype.IntersectionType:
		// Function-intersection sub (PR6 scoped lattice exception): (A & B & …) <: super
		// iff SOME member <: super. This is the ONE place the overload disjunction touches
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
		// Collapse only against a CONCRETE demand. If super is a variable — the overloaded
		// value is flowing INTO a binding (`intersection <: b.v`) — don't collapse here.
		// Fall through to the var arm below, which records the intersection WHOLE as a
		// lower bound. Collapsing now would commit to one arm prematurely and discard the
		// rest of the overload set. The collapse fires later instead, once that var is
		// constrained against a concrete function demand (a call shape) and the whole
		// intersection propagates to it.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar && len(sub.Types) > 0 {
			funcs := make([]*soltype.FuncType, len(sub.Types))
			for i, m := range sub.Types {
				funcs[i], _ = m.(*soltype.FuncType)
			}
			var lastErrs []SolverError
			for _, idx := range specificityOrder(funcs) {
				p := newProbe(c.probe)
				c.probe = p
				// A cloned seen keeps each arm's coinductive cache independent, so a failed
				// arm's entries can't wrongly short-circuit a later arm to success.
				errs := c.constrain(sub.Types[idx], super, seen.Clone())
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

	// bare <: RefType: wrap a borrowable owned source as an immutable, no-lifetime
	// view and re-dispatch into the RefType <: RefType branch above. Build the struct
	// literal DIRECTLY — NewRef would collapse the (false, nil) cell back to the bare
	// inner and recurse forever. A source that is not a RefInner (a primitive,
	// function, promise) is not borrowable, so it falls through to CannotConstrainError
	// naming the borrow. A variable source is excluded here so the subVar arm below
	// records the borrow as an upper bound instead.
	if sup, ok := super.(*soltype.RefType); ok {
		if _, subIsVar := sub.(*soltype.TypeVarType); !subIsVar {
			if inner, ok := sub.(soltype.RefInner); ok {
				return c.constrain(&soltype.RefType{Mut: false, Lt: nil, Inner: inner}, sup, seen)
			}
		}
	}

	// sub is a variable: record super as an upper bound, propagate existing lowers.
	if subVar, ok := sub.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(super) <= subVar.Level {
			c.addUpperBound(subVar, super)
			var errs []SolverError
			for _, lb := range subVar.LowerBounds {
				errs = append(errs, c.constrain(lb, super, seen)...)
			}
			return errs
		}
		// super lives at a higher level: extrude it down so it isn't wrongly
		// generalized at subVar's level.
		return c.constrain(sub, c.extrude(super, soltype.Negative, subVar.Level, map[extrudeKey]*soltype.TypeVarType{}), seen)
	}
	// super is a variable: symmetric — record sub as a lower bound, propagate uppers.
	if superVar, ok := super.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(sub) <= superVar.Level {
			c.addLowerBound(superVar, sub)
			var errs []SolverError
			for _, ub := range superVar.UpperBounds {
				errs = append(errs, c.constrain(sub, ub, seen)...)
			}
			return errs
		}
		return c.constrain(c.extrude(sub, soltype.Positive, superVar.Level, map[extrudeKey]*soltype.TypeVarType{}), super, seen)
	}

	return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
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

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

// commonBorrowEscape returns a representative BorrowEscapeError when every
// per-trial error is itself a BorrowEscapeError. That means sub is a borrow
// with a lifetime and no super-union member could host it. The caller uses
// the returned value to emit one union-level BorrowEscapeError so the
// lifetime cause survives. Without this collapse the union-super exists
// rule would shadow BorrowEscape with a generic CannotConstrain and the
// actionable diagnostic would be lost.
//
// Example. With `&'a {x: number} <: (number | string)`:
//   - trial `&'a {x: number} <: number`: BorrowEscapeError (the borrow can't fit a number slot)
//   - trial `&'a {x: number} <: string`: BorrowEscapeError (same)
//
// Both trials returned the same shape, so commonBorrowEscape returns one of
// them. The caller emits `BorrowEscapeError{Sub: borrow, Super: union}` and
// the user sees "borrow does not live long enough" rather than the unhelpful
// "cannot constrain mut object <: number | string".
func commonBorrowEscape(trials [][]SolverError) *BorrowEscapeError {
	if len(trials) == 0 {
		return nil
	}
	var first *BorrowEscapeError
	for _, errs := range trials {
		if len(errs) != 1 {
			return nil
		}
		esc, ok := errs[0].(*BorrowEscapeError)
		if !ok {
			return nil
		}
		if first == nil {
			first = esc
		}
	}
	return first
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
//
// mutCtx is part of the key (PR 14): the same (sub, super) pair means something
// different inside a mutable borrow's inner, where the object/tuple arms add the
// contravariant write view, than in a covariant position. Keying without it would
// let a covariant visit cache-skip a later invariant visit and drop the write-view
// check. The flag is position-determined and takes two values, so it keeps the key
// set finite and the recursion terminating.
type constraintKey struct {
	sub, super soltype.Type
	mutCtx     bool
}

// extrudeKey keys extrude's per-extrusion cache by both the origin variable's
// ID and the polarity it was reached in, so the same variable copied in
// covariant and contravariant position produces two distinct fresh vars.
type extrudeKey struct {
	id  int
	pol soltype.Polarity
}

// ltExtrudeKey is the lifetime-sort twin of extrudeKey (M4 D2.5): it keys the
// lifetime extrusion cache by the origin LifetimeVar's ID and polarity, so a
// lifetime reached in both polarities yields two fresh vars with opposite outlives
// wiring, exactly as the type-var cache does.
type ltExtrudeKey struct {
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
	return c.constrain(sub, super, set.NewSet[constraintKey](), false)
}

// needsResidualWriteBack reports whether a mutable borrow's inner needs an explicit
// contravariant write view in the RefType arm (PR 14). The object/tuple arms pin
// matched object/object and tuple/tuple inners via the mut-context flag, so those
// need no residual. Any other inner — a type variable, or two mismatched kinds —
// the flag's structural arm does not reach, so the whole reverse constraint pins it.
func needsResidualWriteBack(sub, sup soltype.Type) bool {
	if _, ok := sub.(*soltype.ObjectType); ok {
		_, ok := sup.(*soltype.ObjectType)
		return !ok
	}
	if _, ok := sub.(*soltype.TupleType); ok {
		_, ok := sup.(*soltype.TupleType)
		return !ok
	}
	return true
}

// constrain asserts sub <: super. mutCtx (PR 14) is the deep-mut context flag: true
// inside a mutable borrow's inner, where the object/tuple arms treat each named
// field as invariant rather than covariant. The RefType arm sets it from the target
// borrow's mutability, the object/tuple arms propagate it, and the function and
// promise arms reset it since each carries its own annotation context.
func (c *Context) constrain(sub, super soltype.Type, seen set.Set[constraintKey], mutCtx bool) []SolverError {
	key := constraintKey{sub, super, mutCtx}
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

	// M6 PR2 pre-switch lattice block. The structural switch below dispatches
	// on sub and several arms return early on a non-variable super (the
	// RefType arm most importantly), so a union/intersection super has to be
	// matched here or it would be intercepted as a concrete-non-var demand
	// before the lattice rule could fire. A variable on the deciding side
	// falls through to the var arms instead of decomposing, so the whole
	// union/intersection is recorded as one bound.

	// (A | B) <: super ⟹ A <: super AND B <: super. Inexact sub against a
	// closed super also emits one InexactUnionIntoExactError for the open tail.
	// When super is a TypeVar, fall through to the superVar arm so the whole
	// union (with its Inexact flag) is recorded as one lower bound on the var.
	if subU, ok := sub.(*soltype.UnionType); ok {
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			var errs []SolverError
			if subU.Inexact {
				closed := true
				switch s := super.(type) {
				case *soltype.UnknownType:
					closed = false
				case *soltype.UnionType:
					closed = !s.Inexact
				}
				if closed {
					errs = append(errs, &InexactUnionIntoExactError{Sub: subU, Super: super})
				}
			}
			for _, m := range subU.Types {
				errs = append(errs, c.constrain(m, super, seen, mutCtx)...)
			}
			return errs
		}
	}

	// sub <: (A & B) ⟹ sub <: A AND sub <: B. When sub is a TypeVar, fall
	// through to the subVar arm so the whole intersection is recorded as one
	// upper bound on the var.
	if supI, ok := super.(*soltype.IntersectionType); ok {
		if _, subIsVar := sub.(*soltype.TypeVarType); !subIsVar {
			var errs []SolverError
			for _, m := range supI.Types {
				errs = append(errs, c.constrain(sub, m, seen, mutCtx)...)
			}
			return errs
		}
	}

	// sub <: (A | B) ⟹ sub <: A OR sub <: B. Trial each member under a probe;
	// the first success commits, the losers roll back. Free TypeVar members
	// are skipped to avoid speculatively pinning them to sub.
	if supU, ok := super.(*soltype.UnionType); ok {
		if _, subIsVar := sub.(*soltype.TypeVarType); !subIsVar {
			var trialErrs [][]SolverError
			triedAny := false
			for _, m := range supU.Types {
				if _, isVar := m.(*soltype.TypeVarType); isVar {
					continue
				}
				triedAny = true
				p := newProbe(c.probe)
				c.probe = p
				errs := c.constrain(sub, m, seen.Clone(), mutCtx)
				c.probe = p.parent
				if len(errs) == 0 {
					p.Commit()
					return nil
				}
				p.Discard()
				trialErrs = append(trialErrs, errs)
			}
			if !triedAny {
				// Every member was a free var; fall through to the var arms.
			} else {
				// Every concrete branch failed. Promote a uniform
				// BorrowEscape outcome so the lifetime cause survives;
				// otherwise emit the generic union-level error.
				if commonBorrowEscape(trialErrs) != nil {
					return []SolverError{&BorrowEscapeError{Sub: sub.(*soltype.RefType), Super: super}}
				}
				return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
			}
		}
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
			// A function is its own annotation context, so the deep-mut flag resets.
			var errs []SolverError
			n := min(len(sub.Params), len(sup.Params))
			for i := 0; i < n; i++ {
				errs = append(errs, c.constrain(sup.Params[i].Type, sub.Params[i].Type, seen, false)...) // contravariant
			}
			return append(errs, c.constrain(sub.Ret, sup.Ret, seen, false)...) // covariant
		}
	case *soltype.TupleType:
		if sup, ok := super.(*soltype.TupleType); ok {
			// Element-wise covariant over the shared prefix. Length tolerance
			// follows the super's exactness. An exact super such as `[A, B]`
			// fixes its length. An inexact super such as `[A, ...]` only
			// requires the sub to be at least as long, the longer <: shorter
			// case the `...` tail permits. The ObjectType width rule has the
			// same shape: an inexact super is width-tolerant.
			if sup.Inexact {
				if len(sub.Elems) < len(sup.Elems) {
					return []SolverError{&TupleLengthMismatchError{Sub: sub, Super: sup}}
				}
			} else {
				if len(sub.Elems) != len(sup.Elems) {
					return []SolverError{&TupleLengthMismatchError{Sub: sub, Super: sup}}
				}
				// An exact super pins its length. An inexact sub whose
				// declared elements happen to match is still rejected, since
				// the open tail could carry extra trailing elements at
				// runtime. Mirrors the ObjectType InexactIntoExactError rule.
				if sub.Inexact {
					return []SolverError{&InexactTupleIntoExactError{Sub: sub, Super: sup}}
				}
			}
			var errs []SolverError
			// Range over sup.Elems, not sub.Elems: an inexact super such as
			// `[A, ...]` lets the sub be longer, so sup is the shorter side.
			// This walks the shared prefix and keeps sup.Elems[i] in bounds.
			for i := range sup.Elems {
				errs = append(errs, c.constrain(sub.Elems[i], sup.Elems[i], seen, mutCtx)...) // covariant read view
				// Inside a mutable wrapper every element is invariant (PR 14): the
				// contravariant write view pins it. Outside one, elements stay covariant.
				if mutCtx {
					errs = append(errs, c.constrain(sup.Elems[i], sub.Elems[i], seen, mutCtx)...)
				}
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
				errs = append(errs, c.constrain(subProp.Type, superProp.Type, seen, mutCtx)...) // covariant read view
				// Inside a mutable wrapper (PR 14), a writable field is invariant: the
				// contravariant write view below pins it, the per-field write the eager
				// form's constrainWriteBack did. A readonly TARGET needs only the read
				// view, so a wider source can fill it; a readonly SOURCE cannot fill a
				// writable target slot, the structural twin of inferMemberAssign's check.
				if mutCtx && !superProp.Readonly {
					if subProp.Readonly {
						errs = append(errs, &ReadonlyFieldSubtypeError{Field: superProp.Name})
						continue
					}
					errs = append(errs, c.constrain(superProp.Type, subProp.Type, seen, mutCtx)...) // contravariant write view
				}
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
			// A promise's payload is its own annotation context, so the flag resets.
			return c.constrain(sub.Inner, sup.Inner, seen, false)
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
			// 2. Inner variance (PR 14): the read view is always covariant; a mutable
			//    target also makes every field it NAMES invariant. That per-field pin is
			//    carried by the mut-context flag, set to sup.Mut here, which the
			//    ObjectType/TupleType arms read to add the contravariant write view. An
			//    INEXACT target tolerates extra source fields while still pinning its
			//    named ones, so `mut {x, y} <: mut {x, ...}` SUCCEEDS — the inexact target
			//    names only x — while `mut {x: 5} <: mut {x: number}` still rejects (x
			//    invariant) and an EXACT target demands an identical field set.
			//
			//    A literal-typed field like the `5` in `mut {x: 5}` only arises from an
			//    ANNOTATION. inferMemberAssign builds its requirement with widen(source),
			//    so `obj.x = 5` lowers to `mut {x: number, ...}`, not `mut {x: 5, ...}`.
			//
			//    The flag passes `sup.Mut`, equivalent to `sub.Mut && sup.Mut`: the check
			//    above already returned for `!sub.Mut && sup.Mut`, so reaching here with
			//    sup.Mut implies sub.Mut. If that gate is ever weakened, re-gate the flag.
			errs := c.constrain(sub.Inner, sup.Inner, seen, sup.Mut)
			// The arms above only reach the write view when both inners are the same
			// object/tuple kind. Any other inner — a type variable, or two mismatched
			// kinds — needs the whole reverse constraint to stay invariant.
			if sup.Mut && needsResidualWriteBack(sub.Inner, sup.Inner) {
				errs = append(errs, c.constrain(sup.Inner, sub.Inner, seen, false)...)
			}
			// 3. Lifetime outlives, covariant (M4 D2). Active now that borrows carry
			//    lifetimes. D1 minted the sort. Each borrow site mints a lifetime:
			//    resolveLifetimeAnn for an `&` annotation and inferBorrow for an
			//    `&p` expression.
			switch {
			case sub.Lt != nil && sup.Lt != nil:
				// Both borrows carry a lifetime: relate them covariantly through the
				// outlives lattice, mirroring the covariant read view on the inner.
				c.constrainLt(sup.Lt, sub.Lt)
			case sub.Lt == nil && sup.Lt != nil:
				// An owned source satisfies any borrow slot — no lifetime constraint.
			case sub.Lt != nil && sup.Lt == nil:
				// A borrow flowing into an owned slot escapes its region.
				errs = append(errs, &BorrowEscapeError{Sub: sub, Super: sup})
			}
			return errs
		}
		// RefType <: a concrete non-borrow: peel to the inner. An owned value (Lt nil)
		// satisfies a bare slot; a borrow escaping into an owned slot is a
		// BorrowEscapeError (live in D2). When super is a VARIABLE, fall through to the
		// var arm so the WHOLE borrow is recorded as a bound — peeling there would drop
		// its mutability.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if sub.Lt != nil {
				return []SolverError{&BorrowEscapeError{Sub: sub, Super: super}}
			}
			// Peeling an owned value into a bare slot is a covariant read; flag resets.
			return c.constrain(sub.Inner, super, seen, false)
		}
	case *soltype.Void:
		if _, ok := super.(*soltype.Void); ok {
			return nil
		}
	case *soltype.IntersectionType:
		// (A & B) <: super ⟹ A <: super OR B <: super. Trial each member in
		// specificity order under a probe; the first success commits. Stays in
		// the structural switch (not the pre-switch block) because the switch
		// already dispatches on sub, so a lattice sub is matched by its own case
		// here without needing a pre-switch interception. When super is a
		// TypeVar, fall through to the superVar arm so the whole intersection is
		// recorded as one lower bound rather than committing to one arm and
		// discarding the rest. Two callers reach this:
		//
		//   - Overload synthesis. inferIdent builds an intersection out of an
		//     overloaded value's arms so a let-bound overload called through the
		//     binding (`g = f; g(x)`) resolves the arm via the same trial loop a
		//     direct call would, in the same order.
		//   - Annotation input (M6 PR2). `val x: A & B = e` resolves through
		//     resolveTypeAnn into an IntersectionType and reaches here when the
		//     binding flows into a constraint. specificityOrder ranks
		//     non-function arms last in declaration order, so a non-function
		//     intersection trials in declaration order without a separate code
		//     path.
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
				errs := c.constrain(sub.Types[idx], super, seen.Clone(), mutCtx)
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
				return c.constrain(&soltype.RefType{Mut: false, Lt: nil, Inner: inner}, sup, seen, mutCtx)
			}
		}
	}

	// sub is a variable: record super as an upper bound, propagate existing lowers.
	if subVar, ok := sub.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(super) <= subVar.Level {
			c.addUpperBound(subVar, super)
			var errs []SolverError
			// A variable is a boundary for the deep-mut flag: a recorded RefType bound
			// re-establishes the flag when it propagates, so this step runs flag-free.
			for _, lb := range subVar.LowerBounds {
				errs = append(errs, c.constrain(lb, super, seen, false)...)
			}
			return errs
		}
		// super lives at an inner level: extrude it out so it isn't wrongly
		// generalized at subVar's level.
		return c.constrain(sub, c.extrude(super, soltype.Negative, subVar.Level, map[extrudeKey]*soltype.TypeVarType{}), seen, mutCtx)
	}
	// super is a variable: symmetric — record sub as a lower bound, propagate uppers.
	if superVar, ok := super.(*soltype.TypeVarType); ok {
		if soltype.LevelOf(sub) <= superVar.Level {
			c.addLowerBound(superVar, sub)
			var errs []SolverError
			for _, ub := range superVar.UpperBounds {
				errs = append(errs, c.constrain(sub, ub, seen, false)...)
			}
			return errs
		}
		return c.constrain(c.extrude(sub, soltype.Positive, superVar.Level, map[extrudeKey]*soltype.TypeVarType{}), super, seen, mutCtx)
	}

	return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
}

// ltPair keys constrainLt's coinductive seen-set by (sub, super) lifetime
// identity so a transitive cycle (`'a <: 'b`, `'b <: 'a`, or a longer chain back
// to 'a) terminates: the same pair is never re-entered. Pointer equality on the
// soltype lifetime concretes is the identity here, matching constraintKey.
type ltPair struct{ sub, super soltype.Lifetime }

// constrainLt asserts the outlives relation sub <: super between lifetimes,
// mirroring constrain for the type sort. It is the M4 D1 lifetime-sort solver: a
// variable on the left gains an upper bound, a variable on the right a lower
// bound, and a var-to-var constraint records BOTH directions so each variable
// sees the full relationship at coalescing — the type sort gets symmetry from a
// separate pass, but lifetimes record it directly here. 'static is the bottom of the
// lattice: `'static <: X` always holds, while `X <: 'static` is the forcing escape
// constraint, satisfiable only by X = 'static. The latter records 'static as X's
// upper bound, and since 'static is the bottom it absorbs the meet at a
// negative-position variable, so X coalesces to 'static regardless of any other upper
// bound. The bound is deduped by value through ContainsLifetime, so repeating the
// constraint does not pile up duplicate 'static bounds.
//
// Bound appends route through addLowerLtBound/addUpperLtBound so a speculation
// trial journals them; the (sub, super)-keyed seen-set terminates cycles. The
// rule is written and unit-tested now; the RefType constrain arm activates it in
// D2 (its lifetime step is inert while every Lt is nil).
func (c *Context) constrainLt(sub, super soltype.Lifetime) {
	c.constrainLtSeen(sub, super, set.NewSet[ltPair]())
}

func (c *Context) constrainLtSeen(sub, super soltype.Lifetime, seen set.Set[ltPair]) {
	if sub == super {
		return
	}
	key := ltPair{sub, super}
	if seen.Contains(key) {
		return
	}
	seen.Add(key)

	subVar, subIsVar := sub.(*soltype.LifetimeVar)
	superVar, superIsVar := super.(*soltype.LifetimeVar)
	if subIsVar {
		// Maintain the level invariant: a bound's level must not exceed the var's, or
		// the freshener/extruder level prune over the lifetime sort becomes unsound (M4
		// D2.5). super sits in subVar's upper bounds, a negative-position slot, so when
		// it is inner to subVar extrude it out before recording, mirroring constrain's
		// var arm. extrudeOuterAsUpper reuses an existing outer-extruded proxy of super
		// if one is already a bound, so a repeated constraint does not mint a second
		// proxy and defeat the ContainsLifetime dedup.
		recSuper := c.extrudeOuterAsUpper(super, subVar)
		if !soltype.ContainsLifetime(subVar.UpperBounds, recSuper) {
			c.addUpperLtBound(subVar, recSuper)
		}
		// Propagate to existing lower bounds: lb <: sub <: super.
		for _, lb := range subVar.LowerBounds {
			c.constrainLtSeen(lb, recSuper, seen)
		}
	}
	if superIsVar {
		// sub sits in superVar's lower bounds, a positive-position slot; extrude it out
		// to superVar's level for the same invariant, reusing an existing proxy.
		recSub := c.extrudeOuterAsLower(sub, superVar)
		if !soltype.ContainsLifetime(superVar.LowerBounds, recSub) {
			c.addLowerLtBound(superVar, recSub)
		}
		// Propagate to existing upper bounds: sub <: super <: ub.
		for _, ub := range superVar.UpperBounds {
			c.constrainLtSeen(recSub, ub, seen)
		}
	}
}

// extrudeOuterAsUpper returns the lifetime to record as v's upper bound when
// constraining v <: lt (M4 D2.5). lt is shared when it is not inner to v. When it
// is, it is extruded out to v's level so v's bound is never inner to v. A repeated
// constraint must not mint a fresh proxy each time, or the ContainsLifetime dedup —
// which keys on identity — never matches and bounds accumulate. So an existing
// outer-extruded proxy of lt already among v's upper bounds is reused. This is
// probe-safe: the scan reads v's current journal-managed bounds, so a proxy from a
// discarded trial is already gone and a fresh one is minted.
func (c *Context) extrudeOuterAsUpper(lt soltype.Lifetime, v *soltype.LifetimeVar) soltype.Lifetime {
	if soltype.LevelOfLifetime(lt) <= v.Level {
		return lt
	}
	if proxy := c.findLtProxy(v.UpperBounds, lt); proxy != nil {
		return proxy
	}
	return c.extrudeLt(lt, soltype.Negative, v.Level, map[ltExtrudeKey]*soltype.LifetimeVar{})
}

// extrudeOuterAsLower is the lower-bound counterpart of extrudeOuterAsUpper, for
// constraining lt <: v: it reuses an existing outer-extruded proxy of lt among v's
// lower bounds, else extrudes lt out in positive position.
func (c *Context) extrudeOuterAsLower(lt soltype.Lifetime, v *soltype.LifetimeVar) soltype.Lifetime {
	if soltype.LevelOfLifetime(lt) <= v.Level {
		return lt
	}
	if proxy := c.findLtProxy(v.LowerBounds, lt); proxy != nil {
		return proxy
	}
	return c.extrudeLt(lt, soltype.Positive, v.Level, map[ltExtrudeKey]*soltype.LifetimeVar{})
}

// extrude copies t so that variables inner to lvl are replaced by fresh variables
// at lvl, wired to the originals through the polarity-appropriate bound
// direction. The cache is keyed by (var ID, polarity): a variable reached in
// both polarities during a single extrusion must yield two distinct fresh vars
// with opposite bound wiring (matching canonical Simple-sub's PolarVariable
// cache). Keying by ID alone would reuse a Negative-polarity copy in Positive
// position — skipping its covariant bounds — and vice versa.
func (c *Context) extrude(t soltype.Type, pol soltype.Polarity, lvl int, cache map[extrudeKey]*soltype.TypeVarType) soltype.Type {
	// ltCache is left nil and lazily allocated on the first borrow encountered (see
	// the RefType arm in EnterType), so a borrow-free extrusion pays no allocation.
	return t.Accept(&extruder{c: c, lvl: lvl, cache: cache}, pol)
}

// extrudeLt copies a lifetime so a LifetimeVar inner to lvl is replaced by a fresh
// lifetime at lvl, wired to the original through the polarity-appropriate outlives
// bound (M4 D2.5) — the lifetime-sort twin of extrude's var node. The cache is
// keyed by (var ID, polarity) for the same reason the type-var cache is. A
// 'static, nil slot, or lifetime at lvl or outside it extrudes to itself. Bound
// appends route through the journaling helpers; mutating the ORIGINAL var's bound is
// the append a Discard must truncate. The fresh var's appends are a harmless no-op,
// since a fresh var is unreachable after a Discard.
func (c *Context) extrudeLt(lt soltype.Lifetime, pol soltype.Polarity, lvl int, cache map[ltExtrudeKey]*soltype.LifetimeVar) soltype.Lifetime {
	lv, ok := lt.(*soltype.LifetimeVar)
	if !ok || lv.Level <= lvl {
		return lt
	}
	key := ltExtrudeKey{id: lv.ID, pol: pol}
	if nlv, ok := cache[key]; ok {
		return nlv
	}
	nlv := c.freshLifetime(lvl)
	nlv.Join = lv.Join // an extruded proxy keeps the origin's join-vs-param nature
	cache[key] = nlv
	// Remember which lifetime nlv is an outer-extruded proxy of, so a repeated outlives
	// constraint can reuse this proxy instead of minting a second one (constrainLt's
	// findExtrudedLtBound dedup, M4 D2.5).
	c.recordLtProxy(nlv, lv)
	if pol == soltype.Positive {
		c.addUpperLtBound(lv, nlv)
		for _, lb := range lv.LowerBounds {
			c.addLowerLtBound(nlv, c.extrudeLt(lb, pol, lvl, cache))
		}
	} else {
		c.addLowerLtBound(lv, nlv)
		for _, ub := range lv.UpperBounds {
			c.addUpperLtBound(nlv, c.extrudeLt(ub, pol, lvl, cache))
		}
	}
	return nlv
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
	// ltCache is the lifetime-sort twin of cache (M4 D2.5); see extrudeLt.
	ltCache map[ltExtrudeKey]*soltype.LifetimeVar
}

func (e *extruder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	// A subtree with no variable inner to lvl extrudes to itself (identity-shared).
	if soltype.LevelOf(t) <= e.lvl {
		return soltype.EnterResult{Type: t, SkipChildren: true}
	}
	if r, ok := t.(*soltype.RefType); ok {
		// A borrow's lifetime is covariant on the wrapper and never walked by Accept, so
		// extrude it here in the wrapper's polarity (M4 D2.5). refLifetimeResult then
		// hands back a RefType carrying the fresh lifetime for the descend path to
		// rebuild Inner around, or signals an ordinary rebuild when no extrusion was
		// needed. The cache is allocated on first use so a borrow-free pass pays nothing.
		if e.ltCache == nil {
			e.ltCache = map[ltExtrudeKey]*soltype.LifetimeVar{}
		}
		return refLifetimeResult(r, e.c.extrudeLt(r.Lt, pol, e.lvl, e.ltCache))
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

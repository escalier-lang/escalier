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
// Read a supertype callback parameter's accept-set as "the argument counts whoever
// holds this parameter may invoke the supplied function with."
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

// aliasKey returns the seen-set key operand for one side of a constraint: an AliasType's
// interned canonical representative, or the type itself for every other kind. Canonicalizing
// only the alias operands keeps the common path a cheap type assertion and confines the
// interning cost to a reference that carries type arguments.
func (c *Context) aliasKey(t soltype.Type) soltype.Type {
	if at, ok := t.(*soltype.AliasType); ok {
		return c.internAlias(at)
	}
	return t
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

// evalTypeOperator evaluates the outermost transparent type operator of t to the type it stands
// for, so constrain can check a constraint against that while the stored residual keeps its name.
// An alias expands to its body, a `typeof` query resolves to the value's type, a `keyof` reduces
// to its key set, an indexed access `T[K]` reduces to the type at that key, and a tuple carrying a
// `...P` spread splices its operand tuples into a plain tuple. A plain tuple is a structural type
// constrain decomposes, not an operator, so it reports ok=false. The returned errs carry any
// diagnostic the reduction produced — an unknown object key or an out-of-range tuple index — for
// constrain to surface at the constraint site. It reports ok=false for any other type, and for a
// reduced operator that does not fully ground — a `keyof T`, `T[K]`, or `[...T, x]` over a type
// parameter, or an expanding alias whose reduction is truncated to a residual that would re-expand
// without bound — which stays inert.
func (c *Context) evalTypeOperator(t soltype.Type) (soltype.Type, []SolverError, bool) {
	switch t := t.(type) {
	case *soltype.AliasType:
		return c.expandAlias(t), nil, true
	case *soltype.TypeofType:
		return t.Ty, nil, true
	case *soltype.KeyofType, *soltype.IndexType:
		return c.reduceResidual(t)
	case *soltype.TupleType:
		if !tupleHasSpread(t) {
			return nil, nil, false
		}
		return c.reduceResidual(t)
	case *soltype.ObjectSpreadType:
		e := newTypeEvaluator(c)
		reduced := e.reduce(t)
		// A spread whose operands ground merges to an ObjectType. One with an abstract operand
		// stays an ObjectSpreadType, which is inert, so leave it for the structural switch. The
		// check is on the root rather than reduceResidual's containsResidualOp: a merged object
		// grounds even when a field value is itself a residual, which reduces at its own site.
		if _, stillSpread := reduced.(*soltype.ObjectSpreadType); stillSpread {
			return nil, nil, false
		}
		return reduced, e.errs, true
	default:
		return nil, nil, false
	}
}

// reduceResidual reduces a residual operator and reports its value, or ok=false when the reduction
// stays symbolic — an operand that never ground, or an expanding alias truncated to a residual
// that would re-expand without bound. The errs carry any diagnostic the reduction produced.
func (c *Context) reduceResidual(t soltype.Type) (soltype.Type, []SolverError, bool) {
	e := newTypeEvaluator(c)
	reduced := e.reduce(t)
	if containsResidualOp(reduced) {
		return nil, nil, false
	}
	return reduced, e.errs, true
}

// constrain asserts sub <: super. mutCtx (PR 14) is the deep-mut context flag: true
// inside a mutable borrow's inner, where the object/tuple arms treat each named
// field as invariant rather than covariant. The RefType arm sets it from the target
// borrow's mutability, the object/tuple arms propagate it, and the function and
// promise arms reset it since each carries its own annotation context.
func (c *Context) constrain(sub, super soltype.Type, seen set.Set[constraintKey], mutCtx bool) []SolverError {
	// An AliasType operand keys on the canonical identity formed from the alias and its
	// arguments rather than on its raw pointer, so two structurally-equal instances of a
	// generic recursive alias close the cycle. expandAlias substitutes arguments into a fresh
	// node each unfold, so a raw pointer key would see a new List<number> every lap and
	// diverge. Every other operand is its own key.
	key := constraintKey{c.aliasKey(sub), c.aliasKey(super), mutCtx}
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

	// unknown is the top of the subtype lattice and never the bottom, so a super of
	// unknown or a sub of never succeeds. Both short-circuit above the structural
	// switch and the variable arms, since recording the bound would be the meet or
	// join identity and add nothing. Normalization drops never from unions and
	// unknown from intersections, but a bare never can still reach here as a sub and
	// an annotation's unknown as a super.
	if _, ok := super.(*soltype.UnknownType); ok {
		return nil
	}
	if _, ok := sub.(*soltype.NeverType); ok {
		return nil
	}

	// A variable constrained against itself is reflexively true, so it records
	// nothing. Without this guard the self-edge `T <: T` lands in the variable's own
	// upper bounds and renders as `fn <T: T>(x: T) -> T`. A generic function whose
	// body flows a parameter straight into a same-typed return annotation, such as
	// `fn id<T>(x: T) -> T { return x }`, constrains the inferred return `T` against
	// the annotated return `T` and hits exactly this.
	if subVar, ok := sub.(*soltype.TypeVarType); ok {
		if superVar, ok := super.(*soltype.TypeVarType); ok && subVar == superVar {
			return nil
		}
	}

	// An alias, a `typeof` query, a `keyof`, and an indexed access `T[K]` are transparent for
	// checking: evaluate the outermost operator to the type it stands for and recurse, so the
	// constraint runs on that while the stored residual keeps its name. This sits above the
	// structural switch, which dispatches on sub and would otherwise reject a concrete sub against
	// an operator super before it could unwrap. When the other side is a variable, fall through to
	// the var arms so the whole operator is recorded as one bound and its name survives on the
	// coalesced binding. A recursive alias closes through the existing seen-set, and a `keyof` or
	// `T[K]` that does not fully ground stays inert — see evalTypeOperator — and falls through to
	// its residual arm in the switch.
	if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
		if evaluated, reduceErrs, ok := c.evalTypeOperator(sub); ok {
			// A reduction diagnostic — an unknown key or an out-of-range index — makes the
			// operator malformed, so surface it and stop rather than checking the constraint
			// against the error sentinel the reduction returned.
			if len(reduceErrs) > 0 {
				return reduceErrs
			}
			return c.constrain(evaluated, super, seen, mutCtx)
		}
	}
	if _, subIsVar := sub.(*soltype.TypeVarType); !subIsVar {
		if evaluated, reduceErrs, ok := c.evalTypeOperator(super); ok {
			if len(reduceErrs) > 0 {
				return reduceErrs
			}
			return c.constrain(sub, evaluated, seen, mutCtx)
		}
	}

	// M6 PR2 pre-switch lattice block. The structural switch below dispatches
	// on sub and several arms return early on a non-variable super (the
	// RefType arm most importantly), so a union/intersection super has to be
	// matched here or it would be intercepted as a concrete-non-var demand
	// before the lattice rule could fire. A variable on the deciding side
	// falls through to the var arms instead of decomposing, so the whole
	// union/intersection is recorded as one bound.

	// (A | B) <: super ⟹ A <: super AND B <: super. An inexact sub against a
	// closed super also emits one InexactUnionIntoExactError for the open tail.
	// The unknown rule above already handled an unknown super, so the only open super
	// reachable here is an inexact union. When super is a TypeVar, fall through to the
	// superVar arm so the whole union, including its Inexact flag, is recorded as one
	// lower bound on the var.
	if subU, ok := sub.(*soltype.UnionType); ok {
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			// M5 D4: a field-read or destructure requirement against a union reads the
			// property per member rather than demanding it on every member. When it
			// applies, this yields `T | undefined` instead of failing on the first
			// member that lacks the field.
			if errs, ok := c.constrainUnionFieldRead(subU, super, seen, mutCtx); ok {
				return errs
			}
			var errs []SolverError
			if subU.Inexact {
				closed := true
				if s, ok := super.(*soltype.UnionType); ok {
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

	// sub <: (A | B) ⟹ sub <: A OR sub <: B. Trial each member under a probe in
	// specificityOrder, which ranks a variable below every concrete, so a concrete member
	// is tried before a bare TypeVar member. A var member is therefore a last-resort
	// catch-all, not a speculative first pin. `5 <: (T | number)` commits `number` and
	// never touches T. `"hi" <: (T | number)` finds no concrete match and falls through to
	// `"hi" <: T`, recording "hi" as T's lower bound. This two-pass exists rule settles the
	// M7 union-super open question. It mirrors the IntersectionType-sub arm below, which
	// trials its members through the same specificityOrder, so the union-super and
	// intersection-sub exists rules share one ordering rather than diverging on whether a
	// var member is trialled.
	if supU, ok := super.(*soltype.UnionType); ok {
		if _, subIsVar := sub.(*soltype.TypeVarType); !subIsVar && len(supU.Types) > 0 {
			order := specificityOrder(supU.Types)
			committed, winIdx, winErrs, _ := c.trialAndCommit(order, func(idx int) []SolverError {
				// A cloned seen keeps each member's coinductive cache independent, so a
				// failed member's entries can't wrongly short-circuit a later member.
				return c.constrain(sub, supU.Types[idx], seen.Clone(), mutCtx)
			})
			if committed {
				// winErrs carries any warning the winning member's own nested trial emitted.
				// Propagate it so a nested ambiguous union is not silently swallowed.
				diags := winErrs
				// A committed bare type-variable member pins that var to sub. Tag it so a
				// later constraint that forces an incompatible bound onto the var can name
				// the union choice that pinned it.
				if v, ok := supU.Types[winIdx].(*soltype.TypeVarType); ok {
					c.tagUnionCommit(v, supU)
				}
				// When another member would also match while binding an inference variable,
				// the committed choice is ambiguous. Warn at the union so the user can
				// annotate rather than depend on specificity order.
				if alt := c.ambiguousAlternate(sub, supU, order, winIdx, seen, mutCtx); alt != nil {
					diags = append(diags, &AmbiguousUnionCommitWarning{
						Union: supU, Committed: supU.Types[winIdx], Alternate: alt,
					})
				}
				return diags
			}
			if supU.Inexact {
				// An inexact union super has an open, unknown-typed tail. A sub that
				// matches no named member is subsumed by that tail, so accept it. This
				// is the dual of the union-sub arm above, which rejects an inexact sub
				// into a closed super because that open tail can't be absorbed.
				return nil
			}
			// Every branch failed, the var members included. Promote a BorrowEscapeError
			// when sub's peeled inner still satisfies the union; else emit the generic error.
			if ref, ok := sub.(*soltype.RefType); ok && ref.Lt != nil {
				if !hasHardError(c.trialUnderProbe(ref.Inner, super)) {
					return []SolverError{&BorrowEscapeError{Sub: ref, Super: super}}
				}
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
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
	case *soltype.SkolemType:
		// A skolem is a subtype of the same skolem and of its declared upper bound, so an
		// unconstrained `T` fails against `number` or a distinct `U`, while a `<U: T>` skolem
		// satisfies `T`. When super is a var the case falls through to the superVar arm, which
		// records the skolem as a lower bound so it propagates through the var. A union super
		// is handled by the exists rule above, so `T <: T | number` succeeds before here.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if sup, ok := super.(*soltype.SkolemType); ok && sub.ID == sup.ID {
				return nil
			}
			if sub.Upper != nil {
				return c.constrain(sub.Upper, super, seen, mutCtx)
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
		}
	case *soltype.FuncType:
		if sup, ok := super.(*soltype.FuncType); ok {
			// Higher-rank subtyping: skolemize a super quantifier, instantiate a sub one, then
			// re-enter. Contravariant param recursion swaps the sides, so polarity needs no flag.
			if len(sup.TypeParams) > 0 {
				return c.constrain(sub, c.skolemizeFuncBinder(sup), seen, mutCtx)
			}
			if len(sub.TypeParams) > 0 {
				return c.constrain(c.instantiateFuncBinder(sub, sub.TypeParams[0].Var.Level), sup, seen, mutCtx)
			}
			// Accept-set subtyping (#677 §4.2.1): read super as a callback parameter.
			// sub <: super iff accept(sub) ⊇ accept(super) — sub must tolerate every
			// argument count a holder of super may invoke it with. With
			// accept(sub) = [loSub, hiSub] and accept(super) = [loSup, hiSup]:
			//   - loSub <= loSup — sub must not DEMAND more args than super might supply,
			//   - hiSub >= hiSup — sub must not REFUSE an arg count super might supply.
			// The upper-bound clause is what exactness governs (an exact sub caps hiSub
			// at len(sub.Params), so it can't fill a wider/inexact parameter); the lower-bound
			// clause is the `required` part (a typed-rest/optional lowers it). This
			// subsumes M2's exact-same-arity rule: two EXACT functions have accept
			// [r, n], so ⊇ forces equal upper bounds, i.e. the old same-arity check.
			loSub, hiSub := acceptSet(sub)
			loSup, hiSup := acceptSet(sup)
			if loSub > loSup || hiSub < hiSup {
				return []SolverError{&FuncArityMismatchError{Sub: sub, Super: sup}}
			}
			// Shared positions are contravariant in the params and covariant in the
			// return. An exact super passes no argument beyond its declared params, so
			// this loop is its complete rule. The lower-bound gate forced any extra sub
			// param to be optional, so it is never passed.
			//
			// An inexact super passes unknown-typed arguments past its arity, so each
			// surplus sub param must accept unknown, contravariantly. For example,
			//
			//	val wide: fn(a: number, b?: number, ...) -> number = ...
			//	val slot: fn(x: number, ...) -> number = wide
			//
			// is rejected, because slot's `...` tail may pass any type at b's position,
			// which b's number cannot accept. A surplus param typed unknown or an
			// inference variable is accepted instead. A surplus rest param is left
			// arity-only, because checking its trailing arguments against the element
			// type needs Array<T>, which lands in M7. A function is its own annotation
			// context, so the deep-mut flag resets.
			var errs []SolverError
			n := min(len(sub.Params), len(sup.Params))
			for i := 0; i < n; i++ {
				errs = append(errs, c.constrain(sup.Params[i].Type, sub.Params[i].Type, seen, false)...) // contravariant
			}
			if sup.Inexact {
				unknownT := &soltype.UnknownType{}
				for i := n; i < len(sub.Params); i++ {
					if sub.Params[i].Rest {
						continue // rest-param element checking against Array<T> is M7
					}
					errs = append(errs, c.constrain(unknownT, sub.Params[i].Type, seen, false)...)
				}
			}
			return append(errs, c.constrain(sub.Ret, sup.Ret, seen, false)...) // covariant
		}
	case *soltype.TupleType:
		if tupleHasSpread(sub) || tupleHasSpread(super) {
			// One side carries an unreduced `...P` spread the pre-switch could not ground: a spread
			// over a type parameter, or an expanding recursive alias. constrain treats such a tuple
			// inert, the same as the KeyofType/IndexType residuals — it is never decomposed, so two
			// spread-tuples are compatible only when structurally identical. When super is a variable
			// the case falls through to the superVar arm below, which records the whole tuple as one
			// lower bound, keeping the spread symbolic on the coalesced binding.
			if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
				if equalType(sub, super) {
					return nil
				}
				return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
			}
		} else if sup, ok := super.(*soltype.TupleType); ok {
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
		if sup, ok := super.(*soltype.FuncType); ok {
			// An object with a constructor signature is a subtype of the matching function
			// type; codegen makes the constructor behave as a plain function where expected.
			if ctor, ok := sub.Constructor(); ok {
				return c.constrain(ctor.Fn, sup, seen, mutCtx)
			}
		}
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
			//     source may omit it, so it cannot fill a required property —
			//     OptionalPropertyError, and skip the covariant type check (the
			//     presence mismatch already rejects the constraint). The exception is
			//     a field-read requirement, which reads the optional property as
			//     `T | undefined` instead of erroring. See fieldRead below.
			//   - otherwise (required<:required, required<:optional, optional<:
			//     optional): covariant on the property type.
			//
			// A field-read or destructure requirement is not a subtyping demand but a read
			// of `sub`'s property into a fresh result variable. Reading an optional property
			// `x?: T` off a single object yields `T | undefined`, matching the union
			// field-read path in constrainUnionFieldRead rather than rejecting the optional
			// source. A read always constrains outside a mutable context, so the mutCtx
			// guard keeps this off the write-back path a `mut` field selection takes.
			fieldRead := !mutCtx && isFieldReadReq(sup)
			var errs []SolverError
			for _, superElem := range sup.Elems {
				// A constructor requirement is satisfied by the source's own constructor,
				// its call signature checked covariantly. This lets a class value flow into
				// an object target that names a call signature. A source with no constructor
				// cannot fill one.
				if superCtor, ok := superElem.(*soltype.ConstructorElem); ok {
					if subCtor, has := sub.Constructor(); has {
						errs = append(errs, c.constrain(subCtor.Fn, superCtor.Fn, seen, mutCtx)...)
					} else {
						errs = append(errs, &CannotConstrainError{Sub: sub, Super: sup})
					}
					continue
				}
				// A method, getter, or setter requirement, carried only by a class value,
				// checks against the sub's member by variance instead of panicking in AsProperty.
				if _, isProp := superElem.(*soltype.PropertyElem); !isProp {
					errs = append(errs, c.constrainObjMember(superElem, sub, sup, seen, mutCtx)...)
					continue
				}
				superProp := soltype.AsProperty(superElem) // every remaining elem is a property
				subProp, ok := sub.Prop(superProp.Name)
				if !ok {
					if !superProp.Optional {
						errs = append(errs, &MissingPropertyError{Sub: sub, Super: sup, Name: superProp.Name})
					}
					continue
				}
				if subProp.Optional && !superProp.Optional {
					if fieldRead {
						// Read the optional property as `T | undefined`. The property's type
						// flows into the result var, and undefined joins it because the source
						// may omit the property at runtime.
						errs = append(errs, c.constrain(subProp.Type, superProp.Type, seen, mutCtx)...)
						errs = append(errs, c.constrain(&soltype.UndefinedType{}, superProp.Type, seen, mutCtx)...)
						continue
					}
					errs = append(errs, &OptionalPropertyError{Sub: sub, Super: sup, Name: superProp.Name})
					continue
				}
				errs = append(errs, c.constrain(subProp.Type, superProp.Type, seen, mutCtx)...) // covariant read view
				// Inside a mutable wrapper (PR 14), a writable field is invariant: the
				// contravariant write view below pins it, the per-field write the eager
				// form's constrainWriteBack did. A readonly TARGET needs only the read
				// view, so a wider source can fill it; a readonly SOURCE cannot fill a
				// writable target field, the structural twin of inferMemberAssign's check.
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
					// A class value carries an unnamed ConstructorElem and may carry static
					// method, getter, and setter members. None is a named property, so none
					// counts as an extra property against an exact target. Unifying properties
					// with methods and accessors here is escalier-lang/escalier#864.
					subProp, ok := subElem.(*soltype.PropertyElem)
					if !ok {
						continue
					}
					if _, ok := sup.Prop(subProp.Name); !ok {
						errs = append(errs, &ExtraPropertyError{Sub: sub, Super: sup, Name: subProp.Name})
					}
				}
			}
			return errs
		}
		// A structural object never satisfies a nominal class target: nominal identity is
		// declared, not structural, so `{x: 0}` is not a Point even when it carries every
		// field. A ClassType super is concrete, so intercept it here rather than letting it
		// fall through to the var arms.
		if sup, ok := super.(*soltype.ClassType); ok {
			return []SolverError{&StructuralIntoClassError{Sub: sub, Super: sup}}
		}
	case *soltype.ClassType:
		switch sup := super.(type) {
		case *soltype.ClassType:
			// Nominal: identical name with a per-position argument check, or sub reaches
			// sup transitively through the declared extends graph.
			return c.constrainNominal(sub, sup, seen)
		case *soltype.ObjectType:
			// Target-dispatched (m4 plan forward note): a class instance satisfies a
			// structural object target when the target is inexact, or when the class is
			// final so its member set is closed. A non-final instance into an exact target
			// rejects, since a subclass could add members the exact target cannot tolerate.
			// Otherwise project the class body — exact when final, inexact otherwise — and
			// reuse the object arm's width and exactness rules.
			if !sup.Inexact && !sub.Final {
				return []SolverError{&ClassIntoExactObjectError{Sub: sub, Super: sup}}
			}
			body, ok := c.projectClassBody(sub)
			if !ok {
				return []SolverError{&CannotConstrainError{Sub: sub, Super: sup}}
			}
			return c.constrain(body, sup, seen, mutCtx)
		}
		// A ClassType against any other concrete falls through to the var arms below.
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
			//    target (writing through the target would mutate a read-only borrow). The
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
				// An owned source satisfies any borrow destination — no lifetime constraint.
			case sub.Lt != nil && sup.Lt == nil:
				// A borrow flowing into an owned destination escapes its region.
				errs = append(errs, &BorrowEscapeError{Sub: sub, Super: sup})
			}
			return errs
		}
		// RefType <: a concrete non-borrow: peel to the inner. An owned value (Lt nil)
		// satisfies a bare destination; a borrow escaping into an owned destination is a
		// BorrowEscapeError (live in D2). When super is a VARIABLE, fall through to the
		// var arm so the WHOLE borrow is recorded as a bound — peeling there would drop
		// its mutability.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if sub.Lt != nil {
				// Emit BorrowEscapeError only when the peeled inner satisfies super, so
				// the lifetime is the blocker; otherwise surface the inner's mismatch.
				if innerErrs := c.trialUnderProbe(sub.Inner, super); hasHardError(innerErrs) {
					return innerErrs
				}
				return []SolverError{&BorrowEscapeError{Sub: sub, Super: super}}
			}
			// Peeling an owned value into a bare destination is a covariant read; flag resets.
			return c.constrain(sub.Inner, super, seen, false)
		}
	case *soltype.Void:
		if _, ok := super.(*soltype.Void); ok {
			return nil
		}
	case *soltype.UndefinedType:
		if _, ok := super.(*soltype.UndefinedType); ok {
			return nil
		}
	case *soltype.KeyofType:
		// A `keyof` residual the pre-switch could not ground reaches here: `keyof T` over a type
		// parameter, or an expanding recursive alias. constrain treats it inert, neither
		// decomposing nor reducing it, so two residuals are compatible only when structurally
		// identical. `keyof T <: keyof T` succeeds reflexively without recording any bound, and a
		// residual against any other concrete fails. When super is a variable the case falls
		// through to the superVar arm below, which records the whole residual as one lower bound,
		// keeping the operator symbolic on the coalesced binding.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if equalType(sub, super) {
				return nil
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
		}
	case *soltype.IndexType:
		// A `T[K]` residual the pre-switch could not ground reaches here: an access over a type
		// parameter, or an expanding recursive alias. constrain treats it inert, the same as the
		// KeyofType arm above — two residuals are compatible only when structurally identical, so
		// `T[K] <: T[K]` succeeds reflexively without recording a bound, and a residual against any
		// other concrete fails. When super is a variable the case falls through to the superVar arm,
		// which records the whole residual as one lower bound, keeping the operator symbolic on the
		// coalesced binding. A tuple carrying a `...P` spread is handled inertly in the TupleType arm
		// above, the spread twin of this arm.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if equalType(sub, super) {
				return nil
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
		}
	case *soltype.ObjectSpreadType:
		// An object spread the pre-switch could not ground reaches here: a `{...A}` over a type
		// parameter, or a truncated expanding alias. constrain treats it inert like a `keyof`
		// residual, neither merging nor decomposing it, so two residuals are compatible only when
		// structurally identical. When super is a variable the case falls through to the superVar
		// arm below, which records the whole residual as one lower bound so the operator stays
		// symbolic on the coalesced binding.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar {
			if equalType(sub, super) {
				return nil
			}
			return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
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
		//     binding flows into a constraint. specificityOrder ranks general
		//     types, so a non-function intersection trials its more-specific
		//     members first; incomparable members keep declaration order.
		if _, superIsVar := super.(*soltype.TypeVarType); !superIsVar && len(sub.Types) > 0 {
			committed, _, winErrs, trialErrs := c.trialAndCommit(specificityOrder(sub.Types), func(idx int) []SolverError {
				// A cloned seen keeps each arm's coinductive cache independent, so a failed
				// arm's entries can't wrongly short-circuit a later arm to success.
				return c.constrain(sub.Types[idx], super, seen.Clone(), mutCtx)
			})
			if committed {
				// winErrs carries any warning the winning arm's nested trial emitted.
				return winErrs
			}
			if len(trialErrs) > 0 {
				return trialErrs[len(trialErrs)-1] // no arm matched: surface the last arm's failure
			}
			return nil
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
			return c.breadcrumbUnionCommit(errs, subVar)
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
			return c.breadcrumbUnionCommit(errs, superVar)
		}
		return c.constrain(c.extrude(sub, soltype.Positive, superVar.Level, map[extrudeKey]*soltype.TypeVarType{}), super, seen, mutCtx)
	}

	return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
}

// ambiguousAlternate returns a union member, other than the committed one, that would also
// match sub while binding an inference variable, or nil when none does. For `5 <: (T | number)`
// number commits but T would also match by pinning to 5, so T is returned. Each candidate is
// trialled under a throwaway probe, so the peek records no bound. A union with no bare
// type-variable member cannot bind ambiguously, so the scan is skipped, sparing the common
// all-concrete union super such as an enum value flowing into its variant union.
func (c *Context) ambiguousAlternate(sub soltype.Type, u *soltype.UnionType, order []int, winIdx int, seen set.Set[constraintKey], mutCtx bool) soltype.Type {
	hasVar := false
	for _, m := range u.Types {
		if _, ok := m.(*soltype.TypeVarType); ok {
			hasVar = true
			break
		}
	}
	if !hasVar {
		return nil
	}
	for _, j := range order {
		if j == winIdx {
			continue
		}
		if ok, mutated := c.trialMutatesBounds(sub, u.Types[j], seen, mutCtx); ok && mutated {
			return u.Types[j]
		}
	}
	return nil
}

// breadcrumbUnionCommit stamps each CannotConstrainError in errs with v's union-trial origin
// when v was pinned by a committed bare type-variable member, so the message names the union
// choice that forced the conflict. It is a no-op when v carries no tag; an error that already
// has an origin keeps it, so the innermost breadcrumb survives as the failure unwinds.
func (c *Context) breadcrumbUnionCommit(errs []SolverError, v *soltype.TypeVarType) []SolverError {
	u, ok := c.unionCommits[v]
	if !ok {
		return errs
	}
	for _, e := range errs {
		if cc, ok := e.(*CannotConstrainError); ok && cc.commitUnion == nil {
			cc.commitUnion = u
			cc.commitVar = v
		}
	}
	return errs
}

// isFieldReadReq reports whether an object super is a field-read or destructure
// requirement rather than a concrete subtyping target. valueProp and propReq mint
// such a requirement as an inexact object whose every property type is a fresh
// inference variable the read result flows into, for example `{x: β}` for `p.x`.
// A concrete annotation target is either exact or carries a concrete property type,
// so it fails this test and keeps the strict subtyping rules. Both the single-object
// optional-read widening and the union field-read join key off this shape.
func isFieldReadReq(o *soltype.ObjectType) bool {
	if !o.Inexact {
		return false
	}
	for _, elem := range o.Elems {
		prop, isProp := elem.(*soltype.PropertyElem)
		if !isProp {
			return false
		}
		if _, isVar := prop.Type.(*soltype.TypeVarType); !isVar {
			return false
		}
	}
	return true
}

// constrainUnionFieldRead reads a member `f` off a union sub for a field-read/destructure
// requirement `union <: {f: β}` (M5 D4), joining one contribution per member into β:
//   - a member exposing `f` as a property or getter contributes its read type;
//   - a member exposing `f` as a method contributes the method's callable type, the same
//     receiver-stripped signature a direct `p.f` read yields, or their intersection for an
//     overload set;
//   - a member lacking `f`, or exposing it only as a setter, contributes undefined — a
//     setter-only member reads as undefined at runtime — so `f` readable on some but not all
//     members reads as `T | undefined`;
//   - an inexact union's open `...` tail contributes unknown, since it may carry `f` at any type;
//   - `f` readable on no member of an exact union is a MissingPropertyError, since the read is
//     a constant undefined, like reading an absent field off a single object. This case also
//     covers a member exposing `f` only as a setter with no readable member anywhere, so a
//     write-only member is rejected rather than read as bare undefined.
//
// Each union member is normalized to the ObjectType its members read through: a structural
// object directly, or a class instance's projected body (#886), so a union of class instances,
// or a mix of objects and instances such as `{x: number} | Point`, joins through the same
// per-member read.
//
// ok is false unless the shapes fit — an inexact object super of fresh-var properties over a
// union whose every member is an object or a class instance — so a genuine subtyping demand
// keeps the strict every-member rule.
func (c *Context) constrainUnionFieldRead(sub *soltype.UnionType, super soltype.Type, seen set.Set[constraintKey], mutCtx bool) (errs []SolverError, ok bool) {
	req, isObj := super.(*soltype.ObjectType)
	if !isObj || !isFieldReadReq(req) {
		return nil, false
	}
	members := make([]*soltype.ObjectType, 0, len(sub.Types))
	for _, m := range sub.Types {
		obj, ok := c.readCarrierObject(soltype.CarrierOf(m))
		if !ok {
			return nil, false
		}
		members = append(members, obj)
	}
	for _, elem := range req.Elems {
		prop := soltype.AsProperty(elem)
		// The read joins what each member can yield for this property. anyValue records that
		// some member yields a readable value; anyUndefined that the read can also yield
		// undefined, because some member lacks the property, carries it optionally, or exposes
		// it as a write-only setter, so it may be absent at runtime. A member carrying an
		// optional field sets both.
		anyValue := false
		anyUndefined := false
		for _, obj := range members {
			read, hasValue, mayUndef := memberReadContribution(obj, prop.Name)
			if hasValue {
				anyValue = true
				errs = append(errs, c.constrain(read, prop.Type, seen, mutCtx)...)
			}
			if mayUndef {
				anyUndefined = true
			}
		}
		if !anyValue && !sub.Inexact {
			// No listed member yields a readable value and there is no open tail to carry the
			// property, so the read is a constant undefined. Report it like an absent field on a
			// single object. members is non-empty here, so members[0] is a valid receiver to blame.
			errs = append(errs, &MissingPropertyError{Sub: members[0], Super: req, Name: prop.Name})
			continue
		}
		if anyUndefined {
			errs = append(errs, c.constrain(&soltype.UndefinedType{}, prop.Type, seen, mutCtx)...)
		}
		if sub.Inexact {
			errs = append(errs, c.constrain(&soltype.UnknownType{}, prop.Type, seen, mutCtx)...)
		}
	}
	return errs, true
}

// readCarrierObject returns the ObjectType a union member's fields are read through: a
// structural object directly, or a class instance's projected body. It returns ok=false for
// any other carrier — a primitive, a bare type variable — so the field-read join falls back to
// the strict every-member rule rather than reading a member off a value that carries none.
func (c *Context) readCarrierObject(carrier soltype.Type) (*soltype.ObjectType, bool) {
	switch t := carrier.(type) {
	case *soltype.ObjectType:
		return t, true
	case *soltype.ClassType:
		return c.projectClassBody(t)
	}
	return nil, false
}

// memberReadContribution reports what reading `name` off one union member yields for the
// field-read join. hasValue is true when the member exposes a readable value — a property,
// getter, or method — and read is that value's type: a property's or getter's declared type,
// or a method's receiver-stripped callable signature, joined into an intersection for an
// overload set, matching memberValue. mayUndef is true when the read can also yield undefined,
// because the member is absent, is an optional property, or is a write-only setter, which reads
// as undefined at runtime. An absent or setter-only member has hasValue false, so a property
// readable on no member surfaces as a missing-property error rather than bare undefined.
func memberReadContribution(obj *soltype.ObjectType, name string) (read soltype.Type, hasValue, mayUndef bool) {
	member, found := obj.Member(name)
	if !found {
		return nil, false, true
	}
	switch m := member.(type) {
	case *soltype.PropertyElem:
		return m.Type, true, m.Optional
	case *soltype.GetterElem:
		return m.Type, true, false
	case *soltype.MethodElem:
		switch len(m.Signatures) {
		case 0:
			return &soltype.ErrorType{}, true, false
		case 1:
			return callableView(m.Signatures[0]), true, false
		default:
			arms := make([]soltype.Type, len(m.Signatures))
			for i, sig := range m.Signatures {
				arms[i] = callableView(sig)
			}
			return &soltype.IntersectionType{Types: arms}, true, false
		}
	case *soltype.SetterElem:
		return nil, false, true
	}
	return nil, false, true
}

// constrainObjMember checks a method, getter, or setter requirement on an object super
// against the sub's same-named member by variance: a method by its receiver-stripped
// callable signature (first arm; overload dispatch is E1), a getter covariantly, a setter
// contravariantly. A missing or wrong-kind member fails.
func (c *Context) constrainObjMember(superElem soltype.ObjTypeElem, sub, sup *soltype.ObjectType, seen set.Set[constraintKey], mutCtx bool) []SolverError {
	name := soltype.ObjElemName(superElem)
	subElem, ok := sub.Member(name)
	if !ok {
		return []SolverError{&MissingPropertyError{Sub: sub, Super: sup, Name: name}}
	}
	switch se := superElem.(type) {
	case *soltype.MethodElem:
		sm, ok := subElem.(*soltype.MethodElem)
		if !ok || len(sm.Signatures) == 0 || len(se.Signatures) == 0 {
			break
		}
		// Compares only the first arm of each overload set; full overload-set
		// reconciliation is escalier-lang/escalier#865.
		return c.constrain(callableView(sm.Signatures[0]), callableView(se.Signatures[0]), seen, mutCtx)
	case *soltype.GetterElem:
		if sg, ok := subElem.(*soltype.GetterElem); ok {
			return c.constrain(sg.Type, se.Type, seen, mutCtx) // covariant read
		}
	case *soltype.SetterElem:
		if ss, ok := subElem.(*soltype.SetterElem); ok {
			return c.constrain(se.Param, ss.Param, seen, mutCtx) // contravariant write
		}
	}
	return []SolverError{&CannotConstrainError{Sub: sub, Super: sup}}
}

// callableView returns a method signature as the callable value a member read yields:
// the signature with its receiver dropped, since a method value binds no `self`. It is
// the subtyping counterpart of memberValue's method projection.
func callableView(ft *soltype.FuncType) *soltype.FuncType {
	return &soltype.FuncType{
		Params:         ft.Params,
		Ret:            ft.Ret,
		Inexact:        ft.Inexact,
		TypeParams:     ft.TypeParams,
		LifetimeParams: ft.LifetimeParams,
	}
}

// skolemizeFuncBinder replaces ft's own type parameters with fresh skolems, so a term checked
// against ft as a supertype must satisfy it for every instantiation. Each parameter's declared
// bound becomes its skolem's Upper, seeded first so a bound naming a sibling reaches it. For
// example `<T>(x: T) -> T` becomes `(x: sk) -> sk` for a fresh skolem sk, which `fn (x) {
// return 5 }` fails but a polymorphic identity satisfies.
func (c *Context) skolemizeFuncBinder(ft *soltype.FuncType) *soltype.FuncType {
	sks := make([]*soltype.SkolemType, len(ft.TypeParams))
	args := make([]soltype.Type, len(ft.TypeParams))
	for i, tp := range ft.TypeParams {
		sks[i] = c.freshSkolem(tp.Name)
		args[i] = sks[i]
	}
	sub := newTypeSubst(ft.TypeParams, args, nil, nil)
	for i, tp := range ft.TypeParams {
		// resolveTypeParams records at most one upper bound per parameter, itself an
		// IntersectionType for a `<T: A & B>` bound, so the first bound is the whole declared
		// constraint.
		if len(tp.Var.UpperBounds) > 0 {
			sks[i].Upper = tp.Var.UpperBounds[0].Accept(sub, soltype.Positive)
		}
	}
	return substFuncBinder(ft, sub)
}

// instantiateFuncBinder replaces ft's own type parameters with fresh inference vars at lvl, so
// the sub side of a subtype check picks the instantiation. Each fresh var carries its bounds.
// For example a call `cb(5)` instantiates `cb: <T>(x: T) -> T` to `(x: T0) -> T0` for a fresh
// var T0, which `5` then binds, so a later `cb("hi")` gets its own T1 rather than reusing T0.
func (c *Context) instantiateFuncBinder(ft *soltype.FuncType, lvl int) *soltype.FuncType {
	nvs := make([]*soltype.TypeVarType, len(ft.TypeParams))
	args := make([]soltype.Type, len(ft.TypeParams))
	for i := range ft.TypeParams {
		nvs[i] = c.freshVar(lvl)
		args[i] = nvs[i]
	}
	sub := newTypeSubst(ft.TypeParams, args, nil, nil)
	for i, tp := range ft.TypeParams {
		nvs[i].LowerBounds = acceptBounds(tp.Var.LowerBounds, sub)
		nvs[i].UpperBounds = acceptBounds(tp.Var.UpperBounds, sub)
	}
	return substFuncBinder(ft, sub)
}

// substFuncBinder rebuilds ft with sub applied to its parameters and return and its own
// TypeParams dropped. A nested generic function's own TypeParams pass through unchanged.
func substFuncBinder(ft *soltype.FuncType, sub *typeSubst) *soltype.FuncType {
	cp := *ft
	cp.TypeParams = nil
	cp.Params = make([]*soltype.FuncParam, len(ft.Params))
	for i, p := range ft.Params {
		np := *p
		np.Type = p.Type.Accept(sub, soltype.Negative)
		cp.Params[i] = &np
	}
	cp.Ret = ft.Ret.Accept(sub, soltype.Positive)
	if ft.SelfParam != nil {
		ns := *ft.SelfParam
		ns.Type = ft.SelfParam.Type.Accept(sub, soltype.Negative)
		cp.SelfParam = &ns
	}
	return &cp
}

// acceptBounds applies sub to each bound in a var's bound list, preserving the nil-for-empty
// shape.
func acceptBounds(bounds []soltype.Type, sub *typeSubst) []soltype.Type {
	if len(bounds) == 0 {
		return nil
	}
	out := make([]soltype.Type, len(bounds))
	for i, b := range bounds {
		out[i] = b.Accept(sub, soltype.Positive)
	}
	return out
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
		// D2.5). super sits in subVar's upper bounds, a negative position, so when
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
		// sub sits in superVar's lower bounds, a positive position; extrude it out
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
// 'static, nil lifetime, or lifetime at lvl or outside it extrudes to itself. Bound
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
		// extrude it here in the wrapper's polarity (M4 D2.5). rewriteRefLifetime then
		// hands back a RefType carrying the fresh lifetime for the descend path to
		// rebuild Inner around, or signals an ordinary rebuild when no extrusion was
		// needed. The cache is allocated on first use so a borrow-free pass pays nothing.
		if e.ltCache == nil {
			e.ltCache = map[ltExtrudeKey]*soltype.LifetimeVar{}
		}
		return rewriteRefLifetime(r, e.c.extrudeLt(r.Lt, pol, e.lvl, e.ltCache))
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

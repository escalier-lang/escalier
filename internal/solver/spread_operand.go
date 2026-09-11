package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// spreadSeen records what one operand check has already followed, so a cycle ends the
// walk instead of running forever. A depth budget would not do: the answer at the
// cutoff has to be "accept", since a deep operand may still be a list, and a long
// enough acyclic chain of aliases would then pass whatever it ends in. Only an alias
// and a type variable can lead back to themselves; every other step this walk takes
// goes one level into a finite type.
type spreadSeen struct {
	aliases set.Set[string]
	vars    set.Set[*soltype.TypeVarType]
}

func newSpreadSeen() *spreadSeen {
	return &spreadSeen{
		aliases: set.NewSet[string](),
		vars:    set.NewSet[*soltype.TypeVarType](),
	}
}

// spreadableOperand reports whether t names the positional list a `...P` element
// splices into its tuple. Three shapes qualify:
//
//   - a tuple, which is what the evaluator's `reduceTuple` already splices;
//   - an instance of the well-known `Array`, the variadic-tail form;
//   - a type parameter bounded by either of those, which is how the stdlib tree
//     writes one. `Function.bind` in std/function.esc binds two parameters to an
//     owned-mutable array and spreads both, as `[...A, ...B]`.
//
// An alias is followed to its body and a union to its members, so `[...Pair]` over
// `type Pair = [number, string]` qualifies and `[] | [T]` does too.
//
// Anything the check cannot decide is accepted, since rejecting a type that may yet
// reduce to a tuple would turn a legal program into a diagnostic. A conditional, a
// mapped key, an `infer` capture, and a recursive reference are all symbolic here.
// So is an alias whose body is not yet filled, which `expandAlias` hands back as the
// error sentinel. That last case is what lets the check run while an annotation is
// being resolved rather than after every body is final.
//
// What is left to reject is a type with no positions under any substitution: a
// primitive, a literal, an object, a function, a class that is not `Array`, `null`,
// `undefined`, and `unknown`. `unknown` is on that list because it is what a vacuous
// bound resolves to, so `<T: unknown>` says no more about T than a bare `<T>` and the
// two are rejected alike.
//
// An ITERABLE is rejected with them, which #1552 covers. A class or object type
// declaring `[Symbol.iterator]` is the same unknown-length sequence an `Array` is, so
// the line drawn here is at one class rather than at the property that matters. It
// costs nothing today, since the rules that read a spread understand a tuple and an
// `Array` and nothing else, so such an operand would leave the slot arity-only even if
// it were accepted.
func (c *checker) spreadableOperand(t soltype.Type, seen *spreadSeen) bool {
	switch t := t.(type) {
	case *soltype.TupleType:
		return true
	case *soltype.ClassType:
		_, isArray := c.ctx.arrayElem(t)
		return isArray
	case *soltype.RefType:
		// `mut Array<E>` and `&Array<E>` both reach their positions through the cell.
		return c.spreadableOperand(t.Inner, seen)
	case *soltype.TypeVarType:
		// A variable already on the walk is a cycle in the bound graph, which settles
		// nothing, so it is accepted the way any undecidable operand is.
		if seen.vars.Contains(t) {
			return true
		}
		seen.vars.Add(t)
		return c.someSpreadable(t.UpperBounds, seen)
	case *soltype.SkolemType:
		return c.spreadableOperand(t.Upper, seen)
	case *soltype.AliasType:
		// An alias reached twice on one walk is recursive. Its body never settles to a
		// list or to anything else, so the walk stops and accepts. Keying on the name
		// rather than the reference also catches an alias that recurses through a
		// changing argument, such as `type Nest<T> = Nest<[T]>`.
		if seen.aliases.Contains(t.Name) {
			return true
		}
		seen.aliases.Add(t.Name)
		return c.spreadableOperand(c.ctx.expandAlias(t), seen)
	case *soltype.UnionType:
		// A union spreads only if every member does. One member with no positions
		// makes the whole slot undecidable, which is what the rest-parameter rules
		// see when they distribute the union into per-member candidates.
		return c.everySpreadable(t.Types, seen)
	case *soltype.IntersectionType:
		return c.someSpreadable(t.Types, seen)
	case *soltype.PrimType, *soltype.LitType, *soltype.ObjectType, *soltype.FuncType,
		*soltype.NullType, *soltype.UndefinedType, *soltype.UnknownType,
		*soltype.PromiseType, *soltype.GeneratorType, *soltype.TemplateLitType,
		*soltype.StringIntrinsicType:
		return false
	default:
		return true
	}
}

// someSpreadable reports whether any of ts is spreadable, and false for an empty
// list. An unconstrained type parameter has no bounds and lands here, which is what
// rejects `<T>(...args: [...T, string])`.
func (c *checker) someSpreadable(ts []soltype.Type, seen *spreadSeen) bool {
	for _, t := range ts {
		if c.spreadableOperand(t, seen) {
			return true
		}
	}
	return false
}

// everySpreadable reports whether all of ts are spreadable, and true for an empty
// list.
func (c *checker) everySpreadable(ts []soltype.Type, seen *spreadSeen) bool {
	for _, t := range ts {
		if !c.spreadableOperand(t, seen) {
			return false
		}
	}
	return true
}

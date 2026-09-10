package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// spreadFollowBudget caps how many aliases, bounds, and wrappers one operand check
// follows. A recursive alias would otherwise walk forever, and the guards that stop
// the evaluator are not available here. An operand that exhausts it is accepted,
// which keeps the budget from turning a legal program into a diagnostic.
const spreadFollowBudget = 16

// spreadableOperand reports whether t names the positional list a `...P` element
// splices into its tuple. Three shapes qualify:
//
//   - a tuple, which is what the evaluator's `reduceTuple` already splices;
//   - an instance of the well-known `Array`, the variadic-tail form;
//   - a type parameter bounded by either of those, which is how the stdlib tree
//     writes one. `bind<T, A: mut Array<any>, B: mut Array<any>, R>` in
//     std/function.esc spreads `[...A, ...B]`.
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
// bound resolves to. `<T: unknown>` and `<T: any>` say no more about T than a bare
// `<T>`, so all three are rejected alike.
func (c *checker) spreadableOperand(t soltype.Type, budget int) bool {
	if budget <= 0 {
		return true
	}
	switch t := t.(type) {
	case *soltype.TupleType:
		return true
	case *soltype.ClassType:
		_, isArray := c.ctx.arrayElem(t)
		return isArray
	case *soltype.RefType:
		// `mut Array<E>` and `&Array<E>` both reach their positions through the cell.
		return c.spreadableOperand(t.Inner, budget-1)
	case *soltype.TypeVarType:
		return c.someSpreadable(t.UpperBounds, budget-1)
	case *soltype.SkolemType:
		return c.spreadableOperand(t.Upper, budget-1)
	case *soltype.AliasType:
		return c.spreadableOperand(c.ctx.expandAlias(t), budget-1)
	case *soltype.UnionType:
		// A union spreads only if every member does. One member with no positions
		// makes the whole slot undecidable, which is what the rest-parameter rules
		// see when they distribute the union into per-member candidates.
		return c.everySpreadable(t.Types, budget-1)
	case *soltype.IntersectionType:
		return c.someSpreadable(t.Types, budget-1)
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
func (c *checker) someSpreadable(ts []soltype.Type, budget int) bool {
	for _, t := range ts {
		if c.spreadableOperand(t, budget) {
			return true
		}
	}
	return false
}

// everySpreadable reports whether all of ts are spreadable, and true for an empty
// list.
func (c *checker) everySpreadable(ts []soltype.Type, budget int) bool {
	for _, t := range ts {
		if !c.spreadableOperand(t, budget) {
			return false
		}
	}
	return true
}

package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// variadic_tuple.go checks a tuple with a variadic tail, `[number, ...Array<string>]`.
//
// Such a tuple holds a fixed prefix, then any number of values of one element type, then a
// fixed suffix. soltype writes the middle as a RestSpreadType element whose operand is an
// `Array`, so `[number, ...Array<string>, boolean]` is three elements with the spread in the
// middle. A spread over anything that is not an `Array`, such as a type parameter, is a
// residual the evaluator splices once it grounds, and is not variadic.

// tupleShape is a tuple read as its fixed prefix, its variadic middle, and its fixed suffix.
// rest is the element type of the middle, or nil for a tuple of fixed length. An inexact
// tuple `[A, ...]` has an `unknown` middle and no suffix, since its open tail may hold
// anything.
type tupleShape struct {
	prefix []soltype.Type
	rest   soltype.Type
	suffix []soltype.Type
}

// minLen returns how many values every tuple of the shape holds.
func (s tupleShape) minLen() int { return len(s.prefix) + len(s.suffix) }

// variadicTail returns the element type of the `Array` a tuple element spreads, and false
// for an element that is not such a spread.
func (c *Context) variadicTail(elem soltype.Type) (soltype.Type, bool) {
	spread, ok := elem.(*soltype.RestSpreadType)
	if !ok {
		return nil, false
	}
	return c.arrayElem(c.peelTransparent(spread.Operand))
}

// isVariadicTuple reports whether t holds a spread of an `Array`.
func (c *Context) isVariadicTuple(t *soltype.TupleType) bool {
	for _, elem := range t.Elems {
		if _, ok := c.variadicTail(elem); ok {
			return true
		}
	}
	return false
}

// shapeOf reads t as a tupleShape, and returns false when t holds a residual spread or more
// than one variadic middle, neither of which fixes a position for the elements after it.
func (c *Context) shapeOf(t *soltype.TupleType) (tupleShape, bool) {
	var shape tupleShape
	for _, elem := range t.Elems {
		if tail, ok := c.variadicTail(elem); ok {
			if shape.rest != nil {
				return tupleShape{}, false
			}
			shape.rest = tail
			continue
		}
		if isRestSpread(elem) {
			return tupleShape{}, false
		}
		if shape.rest == nil {
			shape.prefix = append(shape.prefix, elem)
		} else {
			shape.suffix = append(shape.suffix, elem)
		}
	}
	if t.Inexact {
		if shape.rest != nil {
			return tupleShape{}, false
		}
		shape.rest = &soltype.UnknownType{}
	}
	return shape, true
}

// variadicTuple returns the type holding prefix, then any number of elem values, then
// suffix. With nothing fixed on either side it is `Array<elem>` itself.
func (c *Context) variadicTuple(prefix []soltype.Type, elem soltype.Type, suffix []soltype.Type) soltype.Type {
	array := c.arrayOf(elem)
	if len(prefix) == 0 && len(suffix) == 0 {
		return array
	}
	elems := make([]soltype.Type, 0, len(prefix)+len(suffix)+1)
	elems = append(elems, prefix...)
	elems = append(elems, &soltype.RestSpreadType{Operand: array})
	elems = append(elems, suffix...)
	return &soltype.TupleType{Elems: elems}
}

// variadicShape returns the shape of the variadic tuple t stands for, looking through a
// borrow and a variable's lower bound, and false when t is not one. An inexact tuple is not
// variadic here, since its open tail has no element type to read.
func (c *Context) variadicShape(t soltype.Type) (tupleShape, bool) {
	tup, ok := groundedCarrier(t).(*soltype.TupleType)
	if !ok || tup.Inexact || !c.isVariadicTuple(tup) {
		return tupleShape{}, false
	}
	return c.shapeOf(tup)
}

// constrainVariadicTuple constrains sub against super where at least one of the two is
// variadic. It returns ok=false when either side holds a residual spread, which the caller
// leaves to its inert rule.
//
// A value of sub fits super when it is at least as long as super requires and each of its
// positions fits the position of super it lands on:
//
//   - The first len(super.prefix) values of sub fill super's prefix in order, and the last
//     len(super.suffix) fill its suffix.
//   - Every other value of sub, its extra fixed values and its own middle alike, lands in
//     super's middle and must fit super's element type.
//
// A super of fixed length takes only a sub of that same fixed length, which the caller's
// exact-tuple rule decides, so here it rejects any variadic sub. A sub with a middle of its
// own has an unknown length, so its prefix and suffix must each cover super's, since its
// middle may be empty. Under a mutable view every matched pair is checked in both
// directions, the same invariance the exact-tuple rule applies.
func (c *Context) constrainVariadicTuple(sub, super *soltype.TupleType, seen *seenPairs, mutCtx bool) ([]SolverError, bool) {
	subShape, subOK := c.shapeOf(sub)
	supShape, supOK := c.shapeOf(super)
	if !subOK || !supOK {
		return nil, false
	}
	mismatch := []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
	if supShape.rest == nil {
		return mismatch, true
	}
	if subShape.rest == nil {
		if len(subShape.prefix) < supShape.minLen() {
			return mismatch, true
		}
	} else if len(subShape.prefix) < len(supShape.prefix) || len(subShape.suffix) < len(supShape.suffix) {
		return mismatch, true
	}

	var errs []SolverError
	pair := func(s, p soltype.Type) {
		errs = append(errs, c.constrain(s, p, seen, mutCtx)...)
		if mutCtx {
			errs = append(errs, c.constrain(p, s, seen, mutCtx)...)
		}
	}
	// A sub of fixed length is all prefix, so its tail values sit at the end of it.
	subPrefix, subSuffix := subShape.prefix, subShape.suffix
	if subShape.rest == nil {
		split := len(subPrefix) - len(supShape.suffix)
		subPrefix, subSuffix = subPrefix[:split], subPrefix[split:]
	}
	for i, p := range supShape.prefix {
		pair(subPrefix[i], p)
	}
	offset := len(subSuffix) - len(supShape.suffix)
	for j, p := range supShape.suffix {
		pair(subSuffix[offset+j], p)
	}
	for _, s := range subPrefix[len(supShape.prefix):] {
		pair(s, supShape.rest)
	}
	for _, s := range subSuffix[:offset] {
		pair(s, supShape.rest)
	}
	if subShape.rest != nil {
		pair(subShape.rest, supShape.rest)
	}
	return errs, true
}

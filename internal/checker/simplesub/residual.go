package simplesub

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- M7: Design A — residual type-operators + post-solve fixpoint ----
//
// M5 (typeops.go) reduces a type-level operator only when its operands are
// ground. But an operand can be a value whose type is inferred *from usage* —
// not known until the value solve finishes and coalescing runs. Baseline D
// leaves such an operator symbolic; Design A keeps it as an inert residual node
// during constraint solving, then reduces it in a post-solve fixpoint once the
// operand has coalesced to a concrete type.
//
// Design A's defining property (vs. designs B/C): the residual node adds NO new
// mutable solver state. constrain never touches it (it carries no bounds and
// participates in no subtype relation while residual); all reduction happens
// after coalescing. The fixpoint re-runs reduce → coalesce until nothing
// changes, bounded by maxResidualRounds so an irreducible/cyclic operator
// terminates rather than loops.

// ResidualKind is which type operator a ResidualOp represents.
type ResidualKind int

const (
	ResidualKeyof ResidualKind = iota // keyof Operand
	ResidualIndex                     // Operand[Key]
)

// ResidualOp is a type-level operator whose operand is a value-inference
// SimpleType (typically a usage-inferred Variable) that isn't ground during the
// value solve. It is inert: it carries no bounds and constrain leaves it alone.
// After the value solve, coalescing reduces it via reduceResidual.
type ResidualOp struct {
	kind    ResidualKind
	operand SimpleType
	key     string // for ResidualIndex: the (literal) property key
}

func (*ResidualOp) isSimpleType() {}

// Keyof builds a residual `keyof operand`.
func Keyof(operand SimpleType) *ResidualOp {
	return &ResidualOp{kind: ResidualKeyof, operand: operand}
}

// Index builds a residual `operand[key]` for a literal string key.
func Index(operand SimpleType, key string) *ResidualOp {
	return &ResidualOp{kind: ResidualIndex, operand: operand, key: key}
}

// reduceResidual reduces a residual operator after the value solve. It coalesces
// the operand to a concrete type_system.Type (driving the value solve's result
// for a usage-inferred operand) and then applies the operator once. If the
// operand isn't a shape the operator can reduce against, the result stays
// symbolic (keyof T / T[k]) — Baseline-D behavior.
//
// This is a single reduction step: the operator is applied to the operand's
// coalesced shape. Recursion/termination concerns live in the type-level
// evaluator (typeops.go), whose cycle cache + depth budget guarantee the
// operand itself coalesces to a finite type even when it is recursive — so a
// keyof/index over a recursive type (see TestResidualKeyofOverRecursiveType)
// reduces here without any loop.
func (c *coalescer) reduceResidual(op *ResidualOp) type_system.Type {
	// The operand is *read* (its required shape determines the reduction), so
	// coalesce it in Negative position regardless of where the operator's result
	// sits — a usage-inferred operand records its shape as upper bounds, which
	// are the negative-position view.
	target := c.coalesce(op.operand, Negative)
	obj, ok := findObjectType(target)
	switch op.kind {
	case ResidualKeyof:
		if !ok {
			return type_system.NewKeyOfType(nil, target) // symbolic
		}
		return keyofObject(obj)
	case ResidualIndex:
		if ok {
			if v := propValue(obj, op.key); v != nil {
				return v
			}
		}
		return type_system.NewIndexType(nil, target,
			type_system.NewStrLitType(nil, op.key)) // symbolic
	}
	return target
}

// findObjectType locates the object type a residual operand reduced to, looking
// through the wrappers coalescing can produce: a mut reference (read view), and
// an intersection (the operand variable is kept alongside its object upper
// bound, e.g. `{a, b} & T0`). Returns the first ObjectType found.
func findObjectType(t type_system.Type) (*type_system.ObjectType, bool) {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.ObjectType:
		return ty, true
	case *type_system.MutType:
		return findObjectType(ty.Type)
	case *type_system.IntersectionType:
		for _, m := range ty.Types {
			if obj, ok := findObjectType(m); ok {
				return obj, true
			}
		}
	}
	return nil, false
}

// keyofObject is the keyof reduction shared with the M5 evaluator: the union of
// an object's string-keyed property names as string literals.
func keyofObject(obj *type_system.ObjectType) type_system.Type {
	var keys []type_system.Type
	for _, elem := range obj.Elems {
		if pe, ok := elem.(*type_system.PropertyElem); ok && pe.Name.Kind == type_system.StrObjTypeKeyKind {
			keys = append(keys, type_system.NewStrLitType(nil, pe.Name.Str))
		}
	}
	if len(keys) == 0 {
		return type_system.NewNeverType(nil)
	}
	return type_system.NewUnionType(nil, keys...)
}

// propValue returns the value type of a string-keyed property, or nil.
func propValue(obj *type_system.ObjectType, key string) type_system.Type {
	for _, elem := range obj.Elems {
		if pe, ok := elem.(*type_system.PropertyElem); ok &&
			pe.Name.Kind == type_system.StrObjTypeKeyKind && pe.Name.Str == key {
			return pe.Value
		}
	}
	return nil
}

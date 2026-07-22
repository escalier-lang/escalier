package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// maxExpandDepth bounds alias-instantiation depth for the case the cycle guard cannot
// catch, where each instantiation state is distinct because an argument grows without
// bound, as `type Grow<T> = Grow<Array<T>>` grows its argument every lap. It is a safety
// budget, not a derived maximum. No finite maximum exists for that fragment, so the budget
// stops the walk and the operator over the unexpanded alias stays symbolic.
const maxExpandDepth = 200

// typeEvaluator reduces a residual type-level operator to its value once the operand is
// ground. M9 PR1b handles `keyof T`; the later operators join as they land. An operand is
// ground when it has a projectable head shape rather than an unresolved type variable or a
// still-unreduced residual. A ground `keyof {x: number}` reduces to the union of its keys,
// and a `keyof T` over a type parameter stays the symbolic KeyofType.
//
// A recursive alias reached through an operand is made safe by a two-part termination
// strategy:
//
//   - active holds the alias-instantiation states currently being expanded, keyed by the
//     alias name and its rendered arguments. When one recurs with the same state, the
//     evaluator leaves the alias unexpanded, the finite knot standing in for the infinite
//     regular type, rather than expanding it again.
//   - depth is the remaining expansion budget, the catch-all for unbounded growth where the
//     active guard never fires because every state is distinct.
//
// The evaluator adds no mutable solver state. reduce is a pure function of its input, so it
// runs at annotation time on a ground operator and again at coalescing time on a residual
// whose operand only grounded after the value solve.
type typeEvaluator struct {
	// ctx is the alias environment, consulted to expand an alias operand and project a class
	// body. It is nil for the coalescing-time sweep, where an operand has already coalesced to
	// a concrete shape and needs no alias or class projection. An alias or class operand that
	// reaches that path keeps the operator symbolic.
	ctx    *Context
	active set.Set[string]
	depth  int
}

func newTypeEvaluator(ctx *Context) *typeEvaluator {
	return &typeEvaluator{ctx: ctx, active: set.NewSet[string](), depth: maxExpandDepth}
}

// reduce reduces one type-level operator node to its value, returning any other type
// unchanged. A node whose operand is not yet ground reduces to the same operator rebuilt
// around the expanded operand, so it stays symbolic and reduces later once the operand
// grounds.
func (e *typeEvaluator) reduce(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.KeyofType:
		return e.reduceKeyof(t.Operand, t.Exact)
	default:
		return t
	}
}

// reduceKeyof reduces `keyof operand` to the union of the operand's keys, mirroring the old
// checker's KeyOfType case (internal/checker/expand_type.go):
//
//   - an object projects its property, getter, and setter names as string-literal types;
//   - a class projects its instance body the same way;
//   - a tuple yields its numeric indices plus the string literal "length";
//   - `keyof` distributes over a union or intersection, unioning each member's keys;
//   - `keyof` of a primitive, literal, `never`, or `unknown` is `never`, since none has
//     enumerable keys;
//   - an alias expands to its body, and `keyof` reduces over that under the termination guard.
//
// A type variable, a skolem, or an alias the guard left unexpanded keeps the operator symbolic,
// rebuilt around the operand.
func (e *typeEvaluator) reduceKeyof(operand soltype.Type, exact bool) soltype.Type {
	switch op := operand.(type) {
	case *soltype.KeyofType:
		// The operand is itself an operator, so reduce it to its value, then take keyof that value.
		return e.reduceKeyof(e.reduce(op), exact)
	case *soltype.AliasType:
		return e.reduceKeyofAlias(op, exact)
	case *soltype.ObjectType:
		return e.keyofObject(op)
	case *soltype.ClassType:
		if e.ctx == nil {
			return &soltype.KeyofType{Operand: operand, Exact: exact}
		}
		obj, ok := e.ctx.projectClassBody(op)
		if !ok {
			return &soltype.KeyofType{Operand: operand, Exact: exact}
		}
		return e.keyofObject(obj)
	case *soltype.TupleType:
		return e.keyofTuple(op)
	case *soltype.UnionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.IntersectionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.PrimType, *soltype.LitType, *soltype.NeverType, *soltype.UnknownType:
		return &soltype.NeverType{}
	default:
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	}
}

// reduceKeyofAlias reduces `keyof Alias` by expanding the alias and reducing `keyof` over its
// body under the two-part termination guard. The alias stays on the active path for the whole
// reduction of its body, so distribution over a union or intersection member that re-references
// the alias, directly or through a chain, sees it active and stops rather than expanding it
// again. The depth budget decrements along the expansion path and restores as the path unwinds,
// so it bounds an expanding recursion whose instantiation state never repeats without
// truncating a wide union of distinct non-recursive aliases. A recurring instantiation state,
// an exhausted budget, an unresolved body, or a nil evaluator context each leaves the alias
// unexpanded and the operator symbolic.
func (e *typeEvaluator) reduceKeyofAlias(op *soltype.AliasType, exact bool) soltype.Type {
	symbolic := &soltype.KeyofType{Operand: op, Exact: exact}
	if e.ctx == nil {
		return symbolic
	}
	key := soltype.PrintQualified(op)
	if e.active.Contains(key) || e.depth <= 0 {
		return symbolic
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		// expandAlias yields ErrorType for an unregistered alias, or one whose body a dep-graph
		// sibling has not filled yet. Keep the operator symbolic rather than reducing `keyof error`.
		return symbolic
	}
	e.active.Add(key)
	e.depth--
	result := e.reduceKeyof(body, exact)
	e.active.Remove(key)
	e.depth++
	return result
}

// keyofObject projects an object's property, getter, and setter names as string-literal
// types and unions them, matching the old checker's ObjectType arm. An empty projection
// collapses to `never`, the union identity newUnion returns for no members.
func (e *typeEvaluator) keyofObject(obj *soltype.ObjectType) soltype.Type {
	keys := make([]soltype.Type, 0, len(obj.Elems))
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *soltype.PropertyElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.GetterElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.SetterElem:
			keys = append(keys, strLitType(elem.Name))
		}
	}
	return newUnion(e.ctx, keys, false)
}

// keyofTuple yields a tuple's keys: the string literal "length" plus one number-literal type
// per positional element, matching the old checker's TupleType arm. `keyof [number, string]`
// reduces to `0 | 1 | "length"`.
func (e *typeEvaluator) keyofTuple(tup *soltype.TupleType) soltype.Type {
	keys := make([]soltype.Type, 0, len(tup.Elems)+1)
	keys = append(keys, strLitType("length"))
	for i := range tup.Elems {
		keys = append(keys, &soltype.LitType{Lit: &soltype.NumLit{Value: float64(i)}})
	}
	return newUnion(e.ctx, keys, false)
}

// keyofDistribute unions the keys of each member of a union or intersection operand, the
// shared body of both distribution arms: `keyof (A | B)` and `keyof (A & B)` both reduce to
// `keyof A | keyof B`, since an intersection carries the keys of all its members.
func (e *typeEvaluator) keyofDistribute(members []soltype.Type, exact bool) soltype.Type {
	parts := make([]soltype.Type, len(members))
	for i, m := range members {
		parts[i] = e.reduceKeyof(m, exact)
	}
	return newUnion(e.ctx, parts, false)
}

// strLitType builds the string-literal type for one key name, the form a projected object or
// tuple key takes in a `keyof` union.
func strLitType(name string) soltype.Type {
	return &soltype.LitType{Lit: &soltype.StrLit{Value: name}}
}

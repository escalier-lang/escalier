package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// maxExpandDepth caps how many times an alias may expand along one reduction path. The
// active-state guard already stops a regular recursive alias, whose instantiation state repeats.
// This budget backstops an expanding recursive alias such as `type Grow<T> = Grow<Array<T>>`,
// whose argument grows every lap so its state never repeats and the active guard never matches.
// No finite analytical bound exists for that fragment, so the budget stops the walk and the
// operator over the unexpanded alias stays symbolic.
const maxExpandDepth = 200

// typeEvaluator reduces a residual type-level operator to its value once the operand is
// ground. It currently handles `keyof T`; later operators join as they land. An operand is
// ground when it has a projectable head shape rather than an unresolved type variable or a
// still-unreduced residual. A ground `keyof {x: number}` reduces to the union of its keys,
// and a `keyof T` over a type parameter stays the symbolic KeyofType.
//
// A recursive alias reached through an operand is made safe by a two-part termination
// strategy:
//
//   - active holds the alias instantiations currently being expanded, each keyed by the alias
//     name together with its rendered arguments. When one recurs with the identical key, the
//     evaluator leaves the alias unexpanded, the finite knot standing in for the infinite
//     regular type, rather than expanding it again.
//   - depth caps expansions along one path. It backstops an expanding recursion whose argument
//     grows every lap, so its key never repeats and the active guard never fires.
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
//   - a tuple yields only its own numeric indices, omitting the inherited "length"; see keyofTuple;
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
		// The operand is itself a keyof operator. Reduce it first, then take keyof its value. If
		// the inner operator stays symbolic because its own operand is not ground, wrap it as
		// `keyof (keyof …)` rather than re-reducing the same shape forever.
		inner := e.reduce(op)
		if _, stillKeyof := inner.(*soltype.KeyofType); stillKeyof {
			return &soltype.KeyofType{Operand: inner, Exact: exact}
		}
		return e.reduceKeyof(inner, exact)
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
// body under the termination guard. The alias stays on the active path for the whole reduction
// of its body, so a union or intersection member that re-references it, directly or through a
// chain, sees it active and stops. A recurring instantiation state, an exhausted budget, an
// unresolved body, or a nil evaluator context each leaves the alias unexpanded and symbolic.
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

// keyofObject projects an object's property, getter, and setter names as string-literal types
// and unions them. An empty projection collapses to `never`, the union identity newUnion returns
// for no members.
//
// It omits methods, which is correct for a class instance whose methods live on the prototype
// and so are absent from Object.keys, but wrong for a bare object whose methods are own
// enumerable keys. keyofObject cannot tell the two apart from the ObjectType alone, so it
// under-approximates the bare-object case. Issue #916 tracks deciding how keyof should account
// for own vs inherited members.
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

// keyofTuple yields a tuple's own keys: one number-literal type per positional element, the
// indices Object.keys returns. `keyof [number, string]` reduces to `0 | 1`. This deliberately
// deviates from TypeScript, whose keyof of a tuple also includes "length" and the other
// Array.prototype members. Those are inherited rather than own keys, so Escalier omits them.
// TODO: decide how keyof should account for inherited prototype members once interop is designed.
func (e *typeEvaluator) keyofTuple(tup *soltype.TupleType) soltype.Type {
	keys := make([]soltype.Type, 0, len(tup.Elems))
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

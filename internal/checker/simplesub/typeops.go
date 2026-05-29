package simplesub

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- M5: Baseline-D type-level operators ----
//
// Type operators (conditional types `if T : U { X } else { Y }`, `keyof T`,
// indexed access `T[K]`) are type-level *computation*, distinct from the
// value-expression constraint solver in the rest of the spike. M5 implements
// "Baseline D" from the de-risking plan: an operator reduces only when its
// operands are ground (no unresolved type parameter), and otherwise stays
// symbolic. For the common case — a generic type alias applied to concrete
// arguments — every operand is ground at use, so reduction always fires.
//
// Results are concrete type_system.Types (no inference variables), so the
// evaluator produces them directly and renders with the production printer.

// TyExpr is a type-level expression (the input to the evaluator). It is separate
// from SimpleType (the value-inference representation) and Term (value
// expressions).
type TyExpr interface{ isTyExpr() }

// TyRef references a type parameter (resolved from the environment) or a named
// type alias (instantiated with Args). A name that is neither stays symbolic as
// a nominal TypeRefType.
type TyRef struct {
	Name string
	Args []TyExpr
}

type TyPrim struct{ Name string } // "number" | "string" | "boolean"
type TyStrLit struct{ Value string }
type TyBoolLit struct{ Value bool }
type TyAny struct{}
type TyNever struct{}
type TyUnion struct{ Members []TyExpr }
type TyArray struct{ Elem TyExpr } // Array<Elem>
type TyRecord struct{ Fields map[string]TyExpr }

// TyCond is a conditional type: if Check : Extends { Then } else { Else }.
type TyCond struct{ Check, Extends, Then, Else TyExpr }

// TyInfer is an `infer U` placeholder, valid only inside a conditional's Extends.
type TyInfer struct{ Name string }

type TyKeyof struct{ Target TyExpr }
type TyIndex struct {
	Target TyExpr
	Index  TyExpr
}

func (*TyRef) isTyExpr()     {}
func (*TyPrim) isTyExpr()    {}
func (*TyStrLit) isTyExpr()  {}
func (*TyBoolLit) isTyExpr() {}
func (*TyAny) isTyExpr()     {}
func (*TyNever) isTyExpr()   {}
func (*TyUnion) isTyExpr()   {}
func (*TyArray) isTyExpr()   {}
func (*TyRecord) isTyExpr()  {}
func (*TyCond) isTyExpr()    {}
func (*TyInfer) isTyExpr()   {}
func (*TyKeyof) isTyExpr()   {}
func (*TyIndex) isTyExpr()   {}

// TyAlias is a (possibly generic) type alias: `type Name<Params...> = Body`.
type TyAlias struct {
	Params []string
	Body   TyExpr
}

// TypeEvaluator reduces type-level expressions against a set of aliases.
//
// Recursive aliases (`type List<T> = {head: T, tail: List<T> | null}`) are
// handled by a two-part termination strategy, the principled alternative to a
// magic round counter:
//
//   - A cycle cache keyed on the (alias, evaluated-args) instantiation state.
//     When an alias instantiation recurs with the *same* state, the evaluator
//     emits a symbolic TypeRefType back to it instead of expanding again — a
//     finite representation of the infinite (regular) type. This is the
//     analytically-bounded case: the number of expansions is the number of
//     distinct reachable instantiation states.
//   - A depth budget (maxExpandDepth) as the catch-all for the Turing-complete
//     fragment where the cache never fires because every state is distinct
//     (`type Grow<T> = Grow<Array<T>>` grows its argument forever). There is no
//     finite analytical bound there, so the budget stops it and the result
//     stays symbolic.
type TypeEvaluator struct {
	aliases map[string]*TyAlias
}

func NewTypeEvaluator() *TypeEvaluator {
	return &TypeEvaluator{aliases: map[string]*TyAlias{}}
}

func (e *TypeEvaluator) Define(name string, params []string, body TyExpr) {
	e.aliases[name] = &TyAlias{Params: params, Body: body}
}

// maxExpandDepth bounds alias-instantiation depth for the case where the cycle
// cache can't fire (each instantiation state is distinct because an argument
// grows without bound). It is a safety budget, not a derived maximum — no finite
// maximum exists for that (Turing-complete) fragment.
const maxExpandDepth = 200

// tyEnv maps type-parameter names to evaluated concrete types.
type tyEnv map[string]type_system.Type

// evalState threads the recursion-control state through Eval: the set of
// alias-instantiation states currently being expanded (for cycle detection) and
// the remaining expansion budget.
type evalState struct {
	active map[string]bool // instantiation keys on the current expansion path
	depth  int             // remaining alias expansions before the budget trips
}

// Eval reduces a type-level expression to a concrete type_system.Type.
func (e *TypeEvaluator) Eval(expr TyExpr, env tyEnv) type_system.Type {
	return e.eval(expr, env, &evalState{active: map[string]bool{}, depth: maxExpandDepth})
}

func (e *TypeEvaluator) eval(expr TyExpr, env tyEnv, st *evalState) type_system.Type {
	switch t := expr.(type) {
	case *TyPrim:
		return primToType(t.Name)
	case *TyStrLit:
		return type_system.NewStrLitType(nil, t.Value)
	case *TyBoolLit:
		return type_system.NewBoolLitType(nil, t.Value)
	case *TyAny:
		return type_system.NewAnyType(nil)
	case *TyNever:
		return type_system.NewNeverType(nil)
	case *TyUnion:
		members := make([]type_system.Type, len(t.Members))
		for i, m := range t.Members {
			members[i] = e.eval(m, env, st)
		}
		return type_system.NewUnionType(nil, members...)
	case *TyArray:
		return type_system.NewTypeRefType(nil, "Array", nil, e.eval(t.Elem, env, st))
	case *TyRecord:
		return e.evalRecord(t, env, st)
	case *TyRef:
		return e.evalRef(t, env, st)
	case *TyCond:
		return e.evalCond(t, env, st)
	case *TyKeyof:
		return e.evalKeyof(t, env, st)
	case *TyIndex:
		return e.evalIndex(t, env, st)
	default:
		panic("typeops: unhandled TyExpr")
	}
}

// evalRef evaluates a type reference: a type-parameter lookup, a (possibly
// recursive) alias instantiation, or a nominal/symbolic reference. Alias
// instantiation is where recursion is controlled — see TypeEvaluator's doc.
func (e *TypeEvaluator) evalRef(t *TyRef, env tyEnv, st *evalState) type_system.Type {
	// A bare name bound in the environment is a type parameter.
	if bound, ok := env[t.Name]; ok && len(t.Args) == 0 {
		return bound
	}
	alias, ok := e.aliases[t.Name]
	if !ok {
		// Nominal/symbolic (e.g. a class name): evaluate args, keep the head.
		args := make([]type_system.Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = e.eval(a, env, st)
		}
		return type_system.NewTypeRefType(nil, t.Name, nil, args...)
	}

	// Evaluate the arguments, then form the instantiation key (alias name +
	// rendered args) used for both cycle detection and the symbolic fallback.
	args := make([]type_system.Type, 0, len(t.Args))
	newEnv := tyEnv{}
	for i, p := range alias.Params {
		if i < len(t.Args) {
			a := e.eval(t.Args[i], env, st)
			newEnv[p] = a
			args = append(args, a)
		}
	}
	key := instantiationKey(t.Name, args)

	// Cycle: this exact instantiation is already being expanded on the current
	// path. Emit a symbolic reference to it — the finite "knot" that represents
	// the infinite regular type (e.g. List<number> referring back to itself).
	if st.active[key] {
		return type_system.NewTypeRefType(nil, t.Name, nil, args...)
	}
	// Budget exhausted (unbounded-growth recursion the cycle cache can't catch).
	if st.depth <= 0 {
		return type_system.NewTypeRefType(nil, t.Name, nil, args...)
	}

	st.active[key] = true
	st.depth--
	result := e.eval(alias.Body, newEnv, st)
	delete(st.active, key)
	st.depth++
	return result
}

// instantiationKey identifies an alias applied to a specific list of evaluated
// arguments, by rendering each argument to its canonical string form.
func instantiationKey(name string, args []type_system.Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = renderType(a)
	}
	return fmt.Sprintf("%s<%s>", name, strings.Join(parts, ","))
}

func (e *TypeEvaluator) evalRecord(t *TyRecord, env tyEnv, st *evalState) type_system.Type {
	names := make([]string, 0, len(t.Fields))
	for name := range t.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	elems := make([]type_system.ObjTypeElem, len(names))
	for i, name := range names {
		elems[i] = type_system.NewPropertyElem(
			type_system.NewStrKey(name), e.eval(t.Fields[name], env, st))
	}
	return type_system.NewObjectType(nil, elems)
}

// evalCond reduces a conditional type. Distributive conditionals: when the Check
// is a bare type parameter bound to a union, the conditional distributes over
// the union members (matching TypeScript's behavior for naked type parameters).
func (e *TypeEvaluator) evalCond(t *TyCond, env tyEnv, st *evalState) type_system.Type {
	if ref, ok := t.Check.(*TyRef); ok && len(ref.Args) == 0 {
		if bound, ok := env[ref.Name]; ok {
			if union, ok := type_system.Prune(bound).(*type_system.UnionType); ok {
				results := make([]type_system.Type, len(union.Types))
				for i, member := range union.Types {
					branchEnv := cloneTyEnv(env)
					branchEnv[ref.Name] = member
					results[i] = e.evalCondNonDistributive(t, branchEnv, st)
				}
				return type_system.NewUnionType(nil, results...)
			}
		}
	}
	return e.evalCondNonDistributive(t, env, st)
}

func (e *TypeEvaluator) evalCondNonDistributive(t *TyCond, env tyEnv, st *evalState) type_system.Type {
	checkT := e.eval(t.Check, env, st)
	bindings := tyEnv{}
	if e.matches(checkT, t.Extends, env, bindings, st) {
		thenEnv := cloneTyEnv(env)
		for k, v := range bindings {
			thenEnv[k] = v // bring `infer` bindings into scope for the Then branch
		}
		return e.eval(t.Then, thenEnv, st)
	}
	return e.eval(t.Else, env, st)
}

// matches reports whether the concrete type checkT satisfies the Extends
// pattern, recording any `infer` bindings. This is the Baseline-D structural
// subtype check over ground types — it covers any / infer / Array<pat> /
// primitive / literal / union, which is what the supported conditionals need.
func (e *TypeEvaluator) matches(checkT type_system.Type, pat TyExpr, env tyEnv, bindings tyEnv, st *evalState) bool {
	checkT = type_system.Prune(checkT)
	switch p := pat.(type) {
	case *TyAny:
		return true
	case *TyInfer:
		bindings[p.Name] = checkT
		return true
	case *TyArray:
		ref, ok := checkT.(*type_system.TypeRefType)
		if !ok || type_system.QualIdentToString(ref.Name) != "Array" || len(ref.TypeArgs) != 1 {
			return false
		}
		return e.matches(ref.TypeArgs[0], p.Elem, env, bindings, st)
	case *TyPrim:
		// A primitive matches its own primitive, and a literal matches its
		// primitive kind (literal <: primitive).
		if prim, ok := checkT.(*type_system.PrimType); ok {
			return string(prim.Prim) == p.Name
		}
		if lit, ok := checkT.(*type_system.LitType); ok {
			return litPrimName(lit) == p.Name
		}
		return false
	case *TyStrLit:
		lit, ok := checkT.(*type_system.LitType)
		if !ok {
			return false
		}
		s, ok := lit.Lit.(*type_system.StrLit)
		return ok && s.Value == p.Value
	case *TyUnion:
		// checkT matches if it matches any member of the union pattern.
		for _, m := range p.Members {
			if e.matches(checkT, m, env, bindings, st) {
				return true
			}
		}
		return false
	default:
		// Fallback: evaluate the pattern and compare structurally by rendered
		// form (sound for ground, variable-free types).
		return renderType(checkT) == renderType(e.eval(pat, env, st))
	}
}

// evalKeyof reduces `keyof T`: for an object type, the union of its string-keyed
// property names as string literals; otherwise it stays symbolic. Shares the
// reduction with the M7 residual path (keyofObject).
func (e *TypeEvaluator) evalKeyof(t *TyKeyof, env tyEnv, st *evalState) type_system.Type {
	target := type_system.Prune(e.eval(t.Target, env, st))
	obj, ok := target.(*type_system.ObjectType)
	if !ok {
		return type_system.NewKeyOfType(nil, target) // symbolic
	}
	return keyofObject(obj)
}

// evalIndex reduces indexed access `T[K]`: for an object type indexed by a
// string-literal key, the property's value type; otherwise it stays symbolic.
func (e *TypeEvaluator) evalIndex(t *TyIndex, env tyEnv, st *evalState) type_system.Type {
	target := type_system.Prune(e.eval(t.Target, env, st))
	index := type_system.Prune(e.eval(t.Index, env, st))
	obj, objOK := target.(*type_system.ObjectType)
	lit, litOK := index.(*type_system.LitType)
	if objOK && litOK {
		if s, ok := lit.Lit.(*type_system.StrLit); ok {
			if v := propValue(obj, s.Value); v != nil {
				return v
			}
		}
	}
	return type_system.NewIndexType(nil, target, index) // symbolic
}

func cloneTyEnv(env tyEnv) tyEnv {
	c := make(tyEnv, len(env)+1)
	for k, v := range env {
		c[k] = v
	}
	return c
}

func litPrimName(lit *type_system.LitType) string {
	switch lit.Lit.(type) {
	case *type_system.StrLit:
		return "string"
	case *type_system.NumLit:
		return "number"
	case *type_system.BoolLit:
		return "boolean"
	default:
		return ""
	}
}

func renderType(t type_system.Type) string {
	return type_system.PrintType(t, type_system.PrintConfig{})
}

// EvalType is a convenience entry point: define `expr` against the evaluator's
// aliases and render the reduced result.
func (e *TypeEvaluator) Render(expr TyExpr) string {
	return renderType(e.Eval(expr, tyEnv{}))
}

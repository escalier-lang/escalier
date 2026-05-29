package simplesub

import (
	"sort"

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
type TypeEvaluator struct {
	aliases map[string]*TyAlias
}

func NewTypeEvaluator() *TypeEvaluator {
	return &TypeEvaluator{aliases: map[string]*TyAlias{}}
}

func (e *TypeEvaluator) Define(name string, params []string, body TyExpr) {
	e.aliases[name] = &TyAlias{Params: params, Body: body}
}

// tyEnv maps type-parameter names to evaluated concrete types.
type tyEnv map[string]type_system.Type

// Eval reduces a type-level expression to a concrete type_system.Type.
func (e *TypeEvaluator) Eval(expr TyExpr, env tyEnv) type_system.Type {
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
			members[i] = e.Eval(m, env)
		}
		return type_system.NewUnionType(nil, members...)
	case *TyArray:
		return type_system.NewTypeRefType(nil, "Array", nil, e.Eval(t.Elem, env))
	case *TyRecord:
		return e.evalRecord(t, env)
	case *TyRef:
		// A bare name bound in the environment is a type parameter.
		if bound, ok := env[t.Name]; ok && len(t.Args) == 0 {
			return bound
		}
		// A known alias is instantiated with its evaluated arguments.
		if alias, ok := e.aliases[t.Name]; ok {
			newEnv := tyEnv{}
			for i, p := range alias.Params {
				if i < len(t.Args) {
					newEnv[p] = e.Eval(t.Args[i], env)
				}
			}
			return e.Eval(alias.Body, newEnv)
		}
		// Otherwise nominal/symbolic (e.g. a class name).
		args := make([]type_system.Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = e.Eval(a, env)
		}
		return type_system.NewTypeRefType(nil, t.Name, nil, args...)
	case *TyCond:
		return e.evalCond(t, env)
	case *TyKeyof:
		return e.evalKeyof(t, env)
	case *TyIndex:
		return e.evalIndex(t, env)
	default:
		panic("typeops: unhandled TyExpr")
	}
}

func (e *TypeEvaluator) evalRecord(t *TyRecord, env tyEnv) type_system.Type {
	names := make([]string, 0, len(t.Fields))
	for name := range t.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	elems := make([]type_system.ObjTypeElem, len(names))
	for i, name := range names {
		elems[i] = type_system.NewPropertyElem(
			type_system.NewStrKey(name), e.Eval(t.Fields[name], env))
	}
	return type_system.NewObjectType(nil, elems)
}

// evalCond reduces a conditional type. Distributive conditionals: when the Check
// is a bare type parameter bound to a union, the conditional distributes over
// the union members (matching TypeScript's behavior for naked type parameters).
func (e *TypeEvaluator) evalCond(t *TyCond, env tyEnv) type_system.Type {
	if ref, ok := t.Check.(*TyRef); ok && len(ref.Args) == 0 {
		if bound, ok := env[ref.Name]; ok {
			if union, ok := type_system.Prune(bound).(*type_system.UnionType); ok {
				results := make([]type_system.Type, len(union.Types))
				for i, member := range union.Types {
					branchEnv := cloneTyEnv(env)
					branchEnv[ref.Name] = member
					results[i] = e.evalCondNonDistributive(t, branchEnv)
				}
				return type_system.NewUnionType(nil, results...)
			}
		}
	}
	return e.evalCondNonDistributive(t, env)
}

func (e *TypeEvaluator) evalCondNonDistributive(t *TyCond, env tyEnv) type_system.Type {
	checkT := e.Eval(t.Check, env)
	bindings := tyEnv{}
	if e.matches(checkT, t.Extends, env, bindings) {
		thenEnv := cloneTyEnv(env)
		for k, v := range bindings {
			thenEnv[k] = v // bring `infer` bindings into scope for the Then branch
		}
		return e.Eval(t.Then, thenEnv)
	}
	return e.Eval(t.Else, env)
}

// matches reports whether the concrete type checkT satisfies the Extends
// pattern, recording any `infer` bindings. This is the Baseline-D structural
// subtype check over ground types — it covers any / infer / Array<pat> /
// primitive / literal / union, which is what the supported conditionals need.
func (e *TypeEvaluator) matches(checkT type_system.Type, pat TyExpr, env tyEnv, bindings tyEnv) bool {
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
		return e.matches(ref.TypeArgs[0], p.Elem, env, bindings)
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
			if e.matches(checkT, m, env, bindings) {
				return true
			}
		}
		return false
	default:
		// Fallback: evaluate the pattern and compare structurally by rendered
		// form (sound for ground, variable-free types).
		return renderType(checkT) == renderType(e.Eval(pat, env))
	}
}

// evalKeyof reduces `keyof T`: for an object type, the union of its string-keyed
// property names as string literals; otherwise it stays symbolic. Shares the
// reduction with the M7 residual path (keyofObject).
func (e *TypeEvaluator) evalKeyof(t *TyKeyof, env tyEnv) type_system.Type {
	target := type_system.Prune(e.Eval(t.Target, env))
	obj, ok := target.(*type_system.ObjectType)
	if !ok {
		return type_system.NewKeyOfType(nil, target) // symbolic
	}
	return keyofObject(obj)
}

// evalIndex reduces indexed access `T[K]`: for an object type indexed by a
// string-literal key, the property's value type; otherwise it stays symbolic.
func (e *TypeEvaluator) evalIndex(t *TyIndex, env tyEnv) type_system.Type {
	target := type_system.Prune(e.Eval(t.Target, env))
	index := type_system.Prune(e.Eval(t.Index, env))
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

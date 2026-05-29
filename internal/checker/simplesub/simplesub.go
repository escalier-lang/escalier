// Package simplesub is a throwaway proof-of-concept — Milestone M0 of the
// algebraic-subtyping de-risking plan — implementing the core of Lionel
// Parreaux's "Simple-sub" algorithm:
//
//   - fresh type variables that carry lower/upper *bound lists* (not a single
//     Instance, the way type_system.TypeVarType does today),
//   - a constrain(lhs <: rhs) primitive with a coinductive seen-cache, and
//   - polarity-driven coalescing into a production type_system.Type, so the
//     result renders with the real printer (type_system.PrintType) and can be
//     string-compared against the existing checker test expectations.
//
// Scope (M0): type variables, functions, and primitives, driven by a tiny
// hand-built expression IR (the parser bridge is a later milestone). NOT yet
// covered, by design: a simplification pass (M1), records / usage-based
// inference (M2), `mut` invariance (M3), and lifetimes (M4). In particular, M0
// coalesces bound-carrying variables verbatim (e.g. `T0 | boolean`); the M1
// simplification pass is what reduces those to their expected compact form
// (`boolean`).
//
// The Simple-sub variable bounds live on the spike-local Variable struct, never
// on type_system.TypeVarType — keeping the shared type system untouched, as the
// plan requires.
package simplesub

import (
	"fmt"
	"strconv"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- SimpleType: the internal inference representation ----

// SimpleType is the term language constraint solving operates on. It is
// deliberately separate from type_system.Type: solving uses bound-carrying
// Variables, and only coalescing produces a type_system.Type for rendering.
type SimpleType interface{ isSimpleType() }

// Variable is an inference variable carrying the Simple-sub lower/upper bound
// lists. Under algebraic subtyping a variable is never "the single type it was
// unified to"; it accumulates the lower bounds (things assigned into it) and
// upper bounds (things it is used as) monotonically, and coalescing turns those
// into a union/intersection respectively.
type Variable struct {
	id          int
	lowerBounds []SimpleType
	upperBounds []SimpleType
}

func (*Variable) isSimpleType() {}

// Primitive is a base type. name is one of "number", "string", "boolean".
type Primitive struct{ name string }

func (*Primitive) isSimpleType() {}

// Function is a (possibly multi-argument) function type. paramNames is carried
// only for rendering and may be empty/shorter than params.
type Function struct {
	params     []SimpleType
	paramNames []string
	ret        SimpleType
}

func (*Function) isSimpleType() {}

// ---- Inference engine ----

// Inferer owns the fresh-variable counter for one inference run.
type Inferer struct{ varCounter int }

func NewInferer() *Inferer { return &Inferer{} }

func (in *Inferer) freshVar() *Variable {
	v := &Variable{id: in.varCounter}
	in.varCounter++
	return v
}

// constraintKey caches (lhs, rhs) pairs so constrain terminates on recursive
// types. Interface values backed by pointers are comparable, so they work as
// map keys.
type constraintKey struct{ lhs, rhs SimpleType }

// Constrain asserts lhs <: rhs, mutating the bound lists of any variables
// involved. It returns the errors found (empty == success). This is the
// algebraic-subtyping replacement for unification's bind/unify.
func (in *Inferer) Constrain(lhs, rhs SimpleType) []error {
	return in.constrain(lhs, rhs, map[constraintKey]bool{})
}

func (in *Inferer) constrain(lhs, rhs SimpleType, seen map[constraintKey]bool) []error {
	key := constraintKey{lhs, rhs}
	if seen[key] {
		return nil
	}
	seen[key] = true

	// Structural cases first; fall through to the variable cases when a side
	// that didn't match here is a Variable.
	switch l := lhs.(type) {
	case *Primitive:
		if r, ok := rhs.(*Primitive); ok {
			if r.name == l.name {
				return nil
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", l.name, r.name)}
		}
	case *Function:
		if r, ok := rhs.(*Function); ok {
			if len(l.params) != len(r.params) {
				return []error{fmt.Errorf(
					"cannot constrain function of arity %d <: function of arity %d",
					len(l.params), len(r.params))}
			}
			var errs []error
			for i := range l.params {
				// parameters are contravariant
				errs = append(errs, in.constrain(r.params[i], l.params[i], seen)...)
			}
			// return type is covariant
			errs = append(errs, in.constrain(l.ret, r.ret, seen)...)
			return errs
		}
	}

	// lhs is a variable: rhs becomes an upper bound, and must hold against every
	// existing lower bound.
	if v, ok := lhs.(*Variable); ok {
		v.upperBounds = append(v.upperBounds, rhs)
		var errs []error
		for _, lb := range v.lowerBounds {
			errs = append(errs, in.constrain(lb, rhs, seen)...)
		}
		return errs
	}
	// rhs is a variable: lhs becomes a lower bound, and must hold against every
	// existing upper bound.
	if v, ok := rhs.(*Variable); ok {
		v.lowerBounds = append(v.lowerBounds, lhs)
		var errs []error
		for _, ub := range v.upperBounds {
			errs = append(errs, in.constrain(lhs, ub, seen)...)
		}
		return errs
	}

	return []error{fmt.Errorf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
}

func describe(st SimpleType) string {
	switch t := st.(type) {
	case *Primitive:
		return t.name
	case *Function:
		return "function"
	case *Variable:
		return "t" + strconv.Itoa(t.id)
	default:
		return "?"
	}
}

// ---- Tiny expression IR (stands in for the parser at M0) ----

// Term is the source-expression IR. At M0 it is hand-built in tests; later
// milestones bridge to internal/parser + internal/ast.
type Term interface{ isTerm() }

// Lit is a primitive literal carrying its primitive type name, e.g. {"boolean"}.
type Lit struct{ Prim string }

// Var references a binding by name.
type Var struct{ Name string }

// Lam is a single-parameter lambda: fn (Param) { return Body }.
type Lam struct {
	Param string
	Body  Term
}

// App is application: Fn(Arg).
type App struct {
	Fn  Term
	Arg Term
}

func (*Lit) isTerm() {}
func (*Var) isTerm() {}
func (*Lam) isTerm() {}
func (*App) isTerm() {}

// typeTerm walks a Term, generating constraints and returning its SimpleType.
func (in *Inferer) typeTerm(term Term, ctx map[string]SimpleType) (SimpleType, []error) {
	switch t := term.(type) {
	case *Lit:
		return &Primitive{name: t.Prim}, nil
	case *Var:
		if st, ok := ctx[t.Name]; ok {
			return st, nil
		}
		return in.freshVar(), []error{fmt.Errorf("unbound variable: %s", t.Name)}
	case *Lam:
		param := in.freshVar()
		newCtx := make(map[string]SimpleType, len(ctx)+1)
		for k, v := range ctx {
			newCtx[k] = v
		}
		newCtx[t.Param] = param
		body, errs := in.typeTerm(t.Body, newCtx)
		return &Function{
			params:     []SimpleType{param},
			paramNames: []string{t.Param},
			ret:        body,
		}, errs
	case *App:
		fnT, e1 := in.typeTerm(t.Fn, ctx)
		argT, e2 := in.typeTerm(t.Arg, ctx)
		res := in.freshVar()
		errs := append(append([]error{}, e1...), e2...)
		errs = append(errs, in.Constrain(fnT, &Function{
			params: []SimpleType{argT},
			ret:    res,
		})...)
		return res, errs
	default:
		panic(fmt.Sprintf("typeTerm: unhandled %T", term))
	}
}

// ---- Coalescing: SimpleType -> type_system.Type ----

type polKey struct {
	id  int
	pol bool
}

type coalescer struct {
	names   map[int]string
	order   []string // names in first-seen order; become the top-level TypeParams
	counter int
	inProc  map[polKey]bool // breaks recursion on (variable, polarity)
}

func newCoalescer() *coalescer {
	return &coalescer{names: map[int]string{}, inProc: map[polKey]bool{}}
}

func (c *coalescer) nameFor(v *Variable) string {
	if n, ok := c.names[v.id]; ok {
		return n
	}
	n := "T" + strconv.Itoa(c.counter)
	c.counter++
	c.names[v.id] = n
	c.order = append(c.order, n)
	return n
}

// coalesce renders st as a type_system.Type. polarity=true is positive (output)
// position, where a variable becomes the UNION of its lower bounds; polarity
// false is negative (input), where it becomes the INTERSECTION of its upper
// bounds. M0 performs no simplification, so a bound-carrying variable renders
// verbatim (e.g. `T0 | boolean`); M1's simplification pass collapses those.
func (c *coalescer) coalesce(st SimpleType, polarity bool) type_system.Type {
	switch t := st.(type) {
	case *Primitive:
		return primToType(t.name)
	case *Function:
		params := make([]*type_system.FuncParam, len(t.params))
		for i, p := range t.params {
			params[i] = type_system.NewFuncParam(
				type_system.NewIdentPat(paramName(t.paramNames, i)),
				c.coalesce(p, !polarity), // parameters are contravariant
			)
		}
		ret := c.coalesce(t.ret, polarity) // return is covariant
		return type_system.NewFuncType(nil, nil, params, ret, nil)
	case *Variable:
		self := type_system.NewTypeRefType(nil, c.nameFor(t), nil)
		bounds := t.lowerBounds
		if !polarity {
			bounds = t.upperBounds
		}
		if len(bounds) == 0 {
			return self
		}
		pk := polKey{t.id, polarity}
		if c.inProc[pk] {
			return self // recursive type: stop at the variable reference
		}
		c.inProc[pk] = true
		defer delete(c.inProc, pk)

		parts := []type_system.Type{self}
		for _, b := range bounds {
			parts = append(parts, c.coalesce(b, polarity))
		}
		if polarity {
			return type_system.NewUnionType(nil, parts...)
		}
		return type_system.NewIntersectionType(nil, parts...)
	default:
		panic(fmt.Sprintf("coalesce: unhandled %T", st))
	}
}

func paramName(names []string, i int) string {
	if i < len(names) && names[i] != "" && names[i] != "_" {
		return names[i]
	}
	return "x" + strconv.Itoa(i)
}

func primToType(name string) type_system.Type {
	switch name {
	case "number":
		return type_system.NewNumPrimType(nil)
	case "string":
		return type_system.NewStrPrimType(nil)
	case "boolean":
		return type_system.NewBoolPrimType(nil)
	default:
		panic("simplesub: unknown primitive " + name)
	}
}

// ---- Public entry points ----

// Infer types a term and renders it as a type_system.Type. Free variables of a
// top-level function are generalized into named type parameters (T0, T1, ...).
// Proper level-based let-generalization is M1; M0 only needs this top-level
// generalization for the identity case.
func Infer(term Term) (type_system.Type, []error) {
	in := NewInferer()
	st, errs := in.typeTerm(term, map[string]SimpleType{})
	c := newCoalescer()
	ty := c.coalesce(st, true)
	if ft, ok := ty.(*type_system.FuncType); ok && len(c.order) > 0 {
		tps := make([]*type_system.TypeParam, len(c.order))
		for i, n := range c.order {
			tps[i] = type_system.NewTypeParam(n)
		}
		ft.TypeParams = tps
	}
	return ty, errs
}

// Render is Infer followed by the production type printer.
func Render(term Term) (string, []error) {
	ty, errs := Infer(term)
	return type_system.PrintType(ty, type_system.PrintConfig{}), errs
}

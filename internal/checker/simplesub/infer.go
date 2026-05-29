package simplesub

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- Tiny expression IR (stands in for the parser) ----

type Term interface{ isTerm() }

type Lit struct {
	Kind string // "str" | "num" | "bool"
	Str  string
	Num  float64
	Bool bool
}
type Var struct{ Name string }
type Lam struct {
	Params []string
	Body   Term
}
type App struct {
	Fn  Term
	Arg Term
}
type Let struct {
	Name string
	Rhs  Term
	Body Term
}
type TupleExpr struct{ Elems []Term }

func (*Lit) isTerm()       {}
func (*Var) isTerm()       {}
func (*Lam) isTerm()       {}
func (*App) isTerm()       {}
func (*Let) isTerm()       {}
func (*TupleExpr) isTerm() {}

func litToSimple(t *Lit) *Literal {
	return &Literal{kind: t.Kind, str: t.Str, num: t.Num, b: t.Bool}
}

func cloneCtx(ctx map[string]TypeScheme) map[string]TypeScheme {
	c := make(map[string]TypeScheme, len(ctx)+1)
	for k, v := range ctx {
		c[k] = v
	}
	return c
}

func (in *Inferer) typeTerm(term Term, ctx map[string]TypeScheme, lvl int) (SimpleType, []error) {
	switch t := term.(type) {
	case *Lit:
		return litToSimple(t), nil
	case *Var:
		if s, ok := ctx[t.Name]; ok {
			return in.instantiate(s, lvl), nil
		}
		return in.freshVar(lvl), []error{fmt.Errorf("unbound variable: %s", t.Name)}
	case *Lam:
		newCtx := cloneCtx(ctx)
		params := make([]SimpleType, len(t.Params))
		for i, p := range t.Params {
			pv := in.freshVar(lvl)
			params[i] = pv
			newCtx[p] = &MonoScheme{ty: pv}
		}
		body, errs := in.typeTerm(t.Body, newCtx, lvl)
		return &Function{params: params, paramNames: append([]string{}, t.Params...), ret: body}, errs
	case *App:
		fnT, e1 := in.typeTerm(t.Fn, ctx, lvl)
		argT, e2 := in.typeTerm(t.Arg, ctx, lvl)
		res := in.freshVar(lvl)
		errs := append(append([]error{}, e1...), e2...)
		errs = append(errs, in.constrain(fnT,
			&Function{params: []SimpleType{argT}, ret: res}, map[constraintKey]bool{})...)
		return res, errs
	case *Let:
		// Type the rhs one level deeper, then generalize: variables created at
		// lvl+1 (or above) become quantifiable; captured outer variables (level
		// <= lvl) do not.
		rhsT, e1 := in.typeTerm(t.Rhs, ctx, lvl+1)
		newCtx := cloneCtx(ctx)
		newCtx[t.Name] = &PolyScheme{level: lvl, body: rhsT}
		bodyT, e2 := in.typeTerm(t.Body, newCtx, lvl)
		return bodyT, append(e1, e2...)
	case *TupleExpr:
		elems := make([]SimpleType, len(t.Elems))
		var errs []error
		for i, e := range t.Elems {
			et, ee := in.typeTerm(e, ctx, lvl)
			elems[i] = et
			errs = append(errs, ee...)
		}
		return &Tuple{elems: elems}, errs
	default:
		panic(fmt.Sprintf("typeTerm: unhandled %T", term))
	}
}

// ---- Public entry points ----

// Infer types a top-level binding's body (at level 1), simplifies, and renders
// it as a type_system.Type. Free variables surviving simplification are
// generalized into named type parameters (T0, T1, ...) on a top-level function.
func Infer(term Term) (type_system.Type, []error) {
	in := NewInferer()
	st, errs := in.typeTerm(term, map[string]TypeScheme{}, 1)

	// Mirror var-to-var bounds so each variable sees all its subtyping facts.
	vars := map[int]*Variable{}
	collectVars(st, vars)
	symmetrize(vars)

	// Occurrence + co-occurrence analysis, then merge variables that always
	// co-occur.
	occurrences := map[int]map[Polarity]bool{}
	analyze(st, Positive, occurrences, map[polKey]bool{})
	coOcc := map[polKey]map[int]bool{}
	collectCoOcc(st, Positive, coOcc, map[polKey]bool{})
	uf := mergeCoOccurring(vars, occurrences, coOcc)

	mergedOccurrences := map[int]map[Polarity]bool{}
	for id, pols := range occurrences {
		rep := uf.find(id)
		if mergedOccurrences[rep] == nil {
			mergedOccurrences[rep] = map[Polarity]bool{}
		}
		for pol := range pols {
			mergedOccurrences[rep][pol] = true
		}
	}

	c := &coalescer{
		names:             map[int]string{},
		mergedOccurrences: mergedOccurrences,
		uf:                uf,
		inProc:            map[polKey]bool{},
	}
	ty := c.coalesce(st, Positive)
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

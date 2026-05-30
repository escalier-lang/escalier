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
	// ParamTypes optionally annotates parameters. A nil slice (or a nil entry)
	// leaves that parameter unannotated, so it gets a fresh inference variable.
	ParamTypes []SimpleType
	Body       Term
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

// LetRec is a recursive binding: Name is in scope within its own Rhs (e.g.
// `fact` referring to itself). Compare Let, where Name is only in scope in Body.
type LetRec struct {
	Name string
	Rhs  Term
	Body Term
}

// recBinding is one binding of a mutually-recursive group.
type recBinding struct {
	Name string
	Rhs  Term
}

// LetRecGroup is a set of mutually-recursive bindings: every Name is in scope
// within every Rhs and within Body (e.g. `isEven`/`isOdd`). This is the
// expression-level analogue of inferring one strongly-connected component of
// declarations.
type LetRecGroup struct {
	Bindings []recBinding
	Body     Term
}

type TupleExpr struct{ Elems []Term }

// RecordExpr is a record literal, e.g. {bar: e, baz: e}.
type RecordExpr struct{ Fields map[string]Term }

// Sel is field access, e.g. obj.bar.
type Sel struct {
	Receiver Term
	Name     string
}

// Assign is a field assignment statement, e.g. obj.x = v. Its type is void; its
// effect is to require the receiver to be mutable in that field.
type Assign struct {
	Receiver Term
	Name     string
	Value    Term
}

// Block is a sequence of statements/expressions. Each is typed for its effects;
// the block's type is that of the last term, or void when empty.
type Block struct{ Exprs []Term }

// IfExpr is a conditional whose value is one of its two branches. Both branches
// are typed and unified into one result, so when both return borrowed records
// the result carries the union of their lifetimes (e.g. `('a | 'b)`).
type IfExpr struct {
	Cond Term
	Then Term
	Else Term
}

// Escape models a value escaping into module-level/static storage (e.g. storing
// a parameter into a global). Its type is void; its effect is to constrain the
// escaping value's lifetime to outlive everything, i.e. `<: 'static`.
type Escape struct{ Value Term }

// KeyofExpr is the type-level operator `keyof typeof e` applied to a value whose
// type may be usage-inferred. It produces a residual operator (M7 / Design A):
// inert during the value solve, reduced at coalescing once e's type is known.
type KeyofExpr struct{ Value Term }

// IndexExpr is `(typeof e)[key]` for a literal key — likewise residual.
type IndexExpr struct {
	Value Term
	Key   string
}

func (*Lit) isTerm()         {}
func (*Var) isTerm()         {}
func (*Lam) isTerm()         {}
func (*App) isTerm()         {}
func (*Let) isTerm()         {}
func (*TupleExpr) isTerm()   {}
func (*RecordExpr) isTerm()  {}
func (*Sel) isTerm()         {}
func (*Assign) isTerm()      {}
func (*Block) isTerm()       {}
func (*IfExpr) isTerm()      {}
func (*Escape) isTerm()      {}
func (*KeyofExpr) isTerm()   {}
func (*IndexExpr) isTerm()   {}
func (*LetRec) isTerm()      {}
func (*LetRecGroup) isTerm() {}

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
			var pt SimpleType
			if i < len(t.ParamTypes) && t.ParamTypes[i] != nil {
				// A `mut` record parameter is a borrow: give it a fresh lifetime
				// variable, so the borrow's lifetime flows wherever the param does.
				pt = in.attachParamLifetimes(t.ParamTypes[i])
			} else {
				pt = in.freshVar(lvl) // unannotated: fresh inference variable
			}
			params[i] = pt
			newCtx[p] = &MonoScheme{ty: pt}
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
	case *LetRec:
		return in.typeTerm(&LetRecGroup{
			Bindings: []recBinding{{Name: t.Name, Rhs: t.Rhs}},
			Body:     t.Body,
		}, ctx, lvl)
	case *LetRecGroup:
		// Inferring a (mutually) recursive group, the expression-level analogue
		// of one strongly-connected component of declarations. No placeholder
		// types and no post-inference patching are needed (contrast the
		// unification checker's placeholder phase): give each binding a fresh
		// variable at lvl+1, make ALL of them visible in EVERY rhs (so the rhss
		// can reference each other and themselves), then constrain each rhs's
		// inferred type to be a subtype of its variable. References resolve
		// through the variable, whose meaning is just the bounds that accumulate.
		recCtx := cloneCtx(ctx)
		vars := make([]*Variable, len(t.Bindings))
		for i, b := range t.Bindings {
			v := in.freshVar(lvl + 1)
			vars[i] = v
			// Monomorphic inside the group: recursive calls share one type,
			// generalization happens once for the whole group below.
			recCtx[b.Name] = &MonoScheme{ty: v}
		}
		var errs []error
		for i, b := range t.Bindings {
			rhsT, e := in.typeTerm(b.Rhs, recCtx, lvl+1)
			errs = append(errs, e...)
			errs = append(errs, in.constrain(rhsT, vars[i], map[constraintKey]bool{})...)
		}
		// Generalize the whole group at once (shared level boundary lvl), so
		// mutually recursive bindings get a consistent set of quantified vars.
		bodyCtx := cloneCtx(ctx)
		for i, b := range t.Bindings {
			bodyCtx[b.Name] = &PolyScheme{level: lvl, body: vars[i]}
		}
		bodyT, e := in.typeTerm(t.Body, bodyCtx, lvl)
		return bodyT, append(errs, e...)
	case *TupleExpr:
		elems := make([]SimpleType, len(t.Elems))
		var errs []error
		for i, e := range t.Elems {
			et, ee := in.typeTerm(e, ctx, lvl)
			elems[i] = et
			errs = append(errs, ee...)
		}
		return &Tuple{elems: elems}, errs
	case *RecordExpr:
		fields := make(map[string]SimpleType, len(t.Fields))
		var errs []error
		for name, e := range t.Fields {
			ft, ee := in.typeTerm(e, ctx, lvl)
			fields[name] = ft
			errs = append(errs, ee...)
		}
		return &Record{fields: fields}, errs
	case *Sel:
		// Member access drives usage-based inference: obj.bar requires the
		// receiver to be a subtype of {bar: <fresh>}, accumulating the field
		// requirement as an upper bound on the receiver's variable.
		recvT, errs := in.typeTerm(t.Receiver, ctx, lvl)
		// If this field was already written on the same receiver variable, the
		// read returns the (concrete, widened) written type — so a value written
		// to obj.x flows to a later read of obj.x. The mut write requirement
		// already subsumes the read requirement, so no extra read constraint is
		// emitted in that case.
		if rv, ok := recvT.(*Variable); ok {
			if wt := in.written[fieldKey{rv.id, t.Name}]; wt != nil {
				return wt, errs
			}
		}
		res := in.freshVar(lvl)
		errs = append(errs, in.constrain(recvT,
			&Record{fields: map[string]SimpleType{t.Name: res}}, map[constraintKey]bool{})...)
		return res, errs
	case *Assign:
		// Field assignment drives mutability inference: obj.x = v requires the
		// receiver to be a subtype of `mut {x: widen(typeof v)}`. The mut forces
		// the receiver's field invariant, and widen lifts a literal to its
		// primitive (writing 5 makes the field `number`, since a later write
		// could store any number). Result type is void.
		recvT, errs := in.typeTerm(t.Receiver, ctx, lvl)
		valT, ve := in.typeTerm(t.Value, ctx, lvl)
		errs = append(errs, ve...)
		fieldT := widen(valT)
		// Record the written field type per receiver variable so a later read of
		// the same field returns it (see the *Sel case).
		if rv, ok := recvT.(*Variable); ok {
			in.written[fieldKey{rv.id, t.Name}] = fieldT
		}
		errs = append(errs, in.constrain(recvT,
			&Mut{inner: &Record{fields: map[string]SimpleType{t.Name: fieldT}}},
			map[constraintKey]bool{})...)
		return &Void{}, errs
	case *Block:
		var errs []error
		var last SimpleType = &Void{}
		for _, e := range t.Exprs {
			et, ee := in.typeTerm(e, ctx, lvl)
			last = et
			errs = append(errs, ee...)
		}
		return last, errs
	case *IfExpr:
		_, ce := in.typeTerm(t.Cond, ctx, lvl)
		thenT, te := in.typeTerm(t.Then, ctx, lvl)
		elseT, ee := in.typeTerm(t.Else, ctx, lvl)
		errs := append(append(ce, te...), ee...)
		res, je := in.joinBranches(thenT, elseT, lvl)
		return res, append(errs, je...)
	case *Escape:
		// The value escapes to static storage: its lifetime must outlive
		// everything, i.e. lifetime(value) <: 'static.
		valT, errs := in.typeTerm(t.Value, ctx, lvl)
		if lt := lifetimeOf(valT); lt != nil {
			in.constrainLt(lt, &StaticLifetime{})
		}
		return &Void{}, errs
	case *KeyofExpr:
		// Type the value (generating any usage constraints on it), then wrap its
		// type in a residual `keyof` that reduces post-solve (Design A).
		valT, errs := in.typeTerm(t.Value, ctx, lvl)
		return Keyof(valT), errs
	case *IndexExpr:
		valT, errs := in.typeTerm(t.Value, ctx, lvl)
		return Index(valT, t.Key), errs
	default:
		panic(fmt.Sprintf("typeTerm: unhandled %T", term))
	}
}

// joinBranches computes the result type of an if-expression whose branches have
// types a and b. For two borrowed (mut) records it produces a mut record whose
// lifetime is a fresh variable bounded below by both branches' lifetimes — so a
// positive-position result coalesces to `('a | 'b)` — with fields constrained
// equal across the branches. Otherwise it falls back to a fresh type variable
// bounded below by both branch types (their union).
func (in *Inferer) joinBranches(a, b SimpleType, lvl int) (SimpleType, []error) {
	ma, aIsMut := a.(*Mut)
	mb, bIsMut := b.(*Mut)
	if aIsMut && bIsMut {
		ra, aok := ma.inner.(*Record)
		rb, bok := mb.inner.(*Record)
		// Only synthesize a joined mut record when the branches have the SAME
		// field set. A mut record is invariant, so its field set is observable
		// (you can read AND write every field); unioning differing field sets
		// would invent writable fields absent from one branch — unsound. When
		// the keys differ, fall through to the generic union path below.
		if aok && bok && ra.lt != nil && rb.lt != nil && sameKeys(ra.fields, rb.fields) {
			joined := in.freshLifetime()
			in.constrainLt(ra.lt, joined)
			in.constrainLt(rb.lt, joined)
			// Fields are invariant inside mut; constrain shared fields equal.
			fields := map[string]SimpleType{}
			var errs []error
			for name, at := range ra.fields {
				bt := rb.fields[name] // present: keys are equal
				fields[name] = at
				errs = append(errs, in.constrain(at, bt, map[constraintKey]bool{})...)
				errs = append(errs, in.constrain(bt, at, map[constraintKey]bool{})...)
			}
			return &Mut{inner: &Record{fields: fields, lt: joined}}, errs
		}
	}
	res := in.freshVar(lvl)
	var errs []error
	errs = append(errs, in.constrain(a, res, map[constraintKey]bool{})...)
	errs = append(errs, in.constrain(b, res, map[constraintKey]bool{})...)
	return res, errs
}

// sameKeys reports whether two field maps have exactly the same set of keys.
func sameKeys(a, b map[string]SimpleType) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// ---- Public entry points ----

// Infer types a top-level binding's body (at level 1), simplifies, and renders
// it as a type_system.Type. Free variables surviving simplification are
// generalized into named type parameters (T0, T1, ...) on a top-level function.
func Infer(term Term) (type_system.Type, []error) {
	return inferWith(NewInferer(), term)
}

// inferWith is Infer using a caller-supplied Inferer, so a test can pre-create
// inference variables (sharing the same id counter) and reference them in an
// annotation before inferring.
func inferWith(in *Inferer, term Term) (type_system.Type, []error) {
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

	// Lifetime elision: a *param* lifetime is named only if it connects an input
	// to an output (occurs in both polarities) or is forced to 'static. A param
	// lifetime that occurs only on the parameter (its borrow is never returned
	// or stored) is elided. Join/internal variables are never named — they
	// expand to their param-lifetime members — so they are not kept here.
	ltOcc := map[int]map[Polarity]bool{}
	analyzeLts(st, Positive, ltOcc, map[polKey]bool{}, map[polKey]bool{})
	ltKeep := map[int]bool{}
	ltVars := map[int]*LifetimeVar{}
	collectLifetimeVars(st, ltVars, map[int]bool{})
	for _, id := range in.paramLifetimes.ToSlice() {
		pols := ltOcc[id]
		if pols[Positive] && pols[Negative] {
			ltKeep[id] = true
		}
		if v := ltVars[id]; v != nil && lifetimeForced(v) {
			ltKeep[id] = true
		}
	}

	c := &coalescer{
		names:             map[int]string{},
		mergedOccurrences: mergedOccurrences,
		uf:                uf,
		inProc:            map[polKey]bool{},
		ltNames:           map[int]string{},
		ltKeep:            ltKeep,
		paramLifetimes:    in.paramLifetimes,
	}
	ty := c.coalesce(st, Positive)
	if ft, ok := ty.(*type_system.FuncType); ok {
		if len(c.order) > 0 {
			tps := make([]*type_system.TypeParam, len(c.order))
			for i, n := range c.order {
				tps[i] = type_system.NewTypeParam(n)
			}
			ft.TypeParams = tps
		}
		if len(c.ltOrder) > 0 {
			lps := make([]*type_system.LifetimeVar, len(c.ltOrder))
			for i, n := range c.ltOrder {
				lps[i] = &type_system.LifetimeVar{Name: n}
			}
			ft.LifetimeParams = lps
		}
	}
	return ty, errs
}

// Render is Infer followed by the production type printer.
func Render(term Term) (string, []error) {
	ty, errs := Infer(term)
	return type_system.PrintType(ty, type_system.PrintConfig{}), errs
}

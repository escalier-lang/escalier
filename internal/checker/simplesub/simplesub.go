// Package simplesub is a throwaway proof-of-concept — Milestones M0/M1 of the
// algebraic-subtyping de-risking plan — implementing the core of Lionel
// Parreaux's "Simple-sub" algorithm:
//
//   - fresh type variables that carry lower/upper *bound lists* plus a level,
//   - a constrain(lhs <: rhs) primitive with a coinductive seen-cache, plus
//     level-aware extrusion,
//   - level-based let-generalization (instantiate / freshenAbove),
//   - a simplification pass, and
//   - polarity-driven coalescing into a production type_system.Type, rendered
//     with the real printer (type_system.PrintType) so the result can be
//     string-compared against the existing checker test expectations.
//
// Driven by a tiny hand-built expression IR (the parser bridge is a later
// milestone).
//
// M1 simplification: single-polarity elimination (a variable occurring in only
// one polarity is replaced by the union/intersection of its bounds, so e.g.
// id(5) yields `5`, not `T0 | 5`) plus co-occurrence variable merging (variables
// that mutually co-occur in every polarity they appear in are unified, so
// InnerCapturesOuterParam coalesces to `fn <T0>(y: T0) -> [T0, T0]`).
// Records/usage inference (M2), `mut` invariance (M3), and lifetimes (M4) remain
// out of scope.
//
// Variable bounds live on the spike-local Variable struct, never on
// type_system.TypeVarType — the shared type system stays untouched.
package simplesub

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/escalier-lang/escalier/internal/type_system"
)

// ---- Polarity ----

// Polarity is the position a type occupies: Positive (output / covariant, e.g. a
// function's result) or Negative (input / contravariant, e.g. a parameter).
// Under algebraic subtyping a variable coalesces to the union of its lower
// bounds in Positive position and the intersection of its upper bounds in
// Negative position.
type Polarity int

const (
	Positive Polarity = iota
	Negative
)

// flip returns the opposite polarity, used when descending into contravariant
// positions such as function parameters.
func (p Polarity) flip() Polarity {
	if p == Positive {
		return Negative
	}
	return Positive
}

func (p Polarity) String() string {
	if p == Positive {
		return "positive"
	}
	return "negative"
}

// ---- SimpleType: the internal inference representation ----

type SimpleType interface{ isSimpleType() }

// Variable is an inference variable carrying Simple-sub lower/upper bounds and
// the level at which it was created (used for let-generalization).
type Variable struct {
	id          int
	level       int
	lowerBounds []SimpleType
	upperBounds []SimpleType
}

// boundsAt returns the bounds relevant to the given polarity: lower bounds in
// Positive position (the variable becomes their union), upper bounds in Negative
// position (the variable becomes their intersection).
func (v *Variable) boundsAt(pol Polarity) []SimpleType {
	if pol == Positive {
		return v.lowerBounds
	}
	return v.upperBounds
}

// Primitive is a base type: "number" | "string" | "boolean".
type Primitive struct{ name string }

// Literal is a literal type, e.g. "hello" or 5.
type Literal struct {
	kind string // "str" | "num" | "bool"
	str  string
	num  float64
	b    bool
}

// Function is a (possibly multi-argument) function type.
type Function struct {
	params     []SimpleType
	paramNames []string
	ret        SimpleType
}

// Tuple is a fixed-length tuple type.
type Tuple struct{ elems []SimpleType }

func (*Variable) isSimpleType()  {}
func (*Primitive) isSimpleType() {}
func (*Literal) isSimpleType()   {}
func (*Function) isSimpleType()  {}
func (*Tuple) isSimpleType()     {}

func (l *Literal) eq(o *Literal) bool {
	if l.kind != o.kind {
		return false
	}
	switch l.kind {
	case "str":
		return l.str == o.str
	case "num":
		return l.num == o.num
	case "bool":
		return l.b == o.b
	}
	return false
}

// levelOf is the maximum level of any variable inside ty; concrete leaves are
// level 0. Used to decide generalization and extrusion.
func levelOf(ty SimpleType) int {
	switch t := ty.(type) {
	case *Variable:
		return t.level
	case *Function:
		m := 0
		for _, p := range t.params {
			m = max(m, levelOf(p))
		}
		return max(m, levelOf(t.ret))
	case *Tuple:
		m := 0
		for _, e := range t.elems {
			m = max(m, levelOf(e))
		}
		return m
	default:
		return 0
	}
}

// ---- Inference engine ----

type Inferer struct{ varCounter int }

func NewInferer() *Inferer { return &Inferer{} }

func (in *Inferer) freshVar(level int) *Variable {
	v := &Variable{id: in.varCounter, level: level}
	in.varCounter++
	return v
}

type constraintKey struct{ lhs, rhs SimpleType }

// Constrain asserts lhs <: rhs, mutating bound lists. Empty result == success.
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
	case *Literal:
		if r, ok := rhs.(*Literal); ok {
			if l.eq(r) {
				return nil
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(l), describe(r))}
		}
		if r, ok := rhs.(*Primitive); ok {
			if litKindPrim(l) == r.name {
				return nil // a literal is a subtype of its primitive
			}
			return []error{fmt.Errorf("cannot constrain %s <: %s", describe(l), r.name)}
		}
	case *Function:
		if r, ok := rhs.(*Function); ok {
			// A function with FEWER params is a subtype of one with more: the
			// supertype's extra trailing params are ignored. l <: r requires
			// len(l.params) <= len(r.params).
			if len(l.params) > len(r.params) {
				return []error{fmt.Errorf(
					"cannot constrain function of arity %d <: function of arity %d",
					len(l.params), len(r.params))}
			}
			var errs []error
			for i := range l.params {
				errs = append(errs, in.constrain(r.params[i], l.params[i], seen)...) // contravariant
			}
			errs = append(errs, in.constrain(l.ret, r.ret, seen)...) // covariant
			return errs
		}
	case *Tuple:
		if r, ok := rhs.(*Tuple); ok {
			if len(l.elems) != len(r.elems) {
				return []error{fmt.Errorf(
					"cannot constrain tuple of length %d <: tuple of length %d",
					len(l.elems), len(r.elems))}
			}
			var errs []error
			for i := range l.elems {
				errs = append(errs, in.constrain(l.elems[i], r.elems[i], seen)...) // covariant
			}
			return errs
		}
	}

	// lhs is a variable.
	if lv, ok := lhs.(*Variable); ok {
		if levelOf(rhs) <= lv.level {
			lv.upperBounds = append(lv.upperBounds, rhs)
			var errs []error
			for _, lb := range lv.lowerBounds {
				errs = append(errs, in.constrain(lb, rhs, seen)...)
			}
			return errs
		}
		// rhs lives at a higher level: extrude it down so it isn't wrongly
		// generalized at lv's level.
		return in.constrain(lhs, in.extrude(rhs, Negative, lv.level, map[int]*Variable{}), seen)
	}
	// rhs is a variable.
	if rv, ok := rhs.(*Variable); ok {
		if levelOf(lhs) <= rv.level {
			rv.lowerBounds = append(rv.lowerBounds, lhs)
			var errs []error
			for _, ub := range rv.upperBounds {
				errs = append(errs, in.constrain(lhs, ub, seen)...)
			}
			return errs
		}
		return in.constrain(in.extrude(lhs, Positive, rv.level, map[int]*Variable{}), rhs, seen)
	}

	return []error{fmt.Errorf("cannot constrain %s <: %s", describe(lhs), describe(rhs))}
}

// extrude copies ty so that variables above lvl are replaced by fresh variables
// at lvl, wired to the originals through the appropriate bound direction.
func (in *Inferer) extrude(ty SimpleType, pol Polarity, lvl int, cache map[int]*Variable) SimpleType {
	if levelOf(ty) <= lvl {
		return ty
	}
	switch t := ty.(type) {
	case *Variable:
		if nv, ok := cache[t.id]; ok {
			return nv
		}
		nv := in.freshVar(lvl)
		cache[t.id] = nv
		if pol == Positive {
			t.upperBounds = append(t.upperBounds, nv)
			for _, lb := range t.lowerBounds {
				nv.lowerBounds = append(nv.lowerBounds, in.extrude(lb, pol, lvl, cache))
			}
		} else {
			t.lowerBounds = append(t.lowerBounds, nv)
			for _, ub := range t.upperBounds {
				nv.upperBounds = append(nv.upperBounds, in.extrude(ub, pol, lvl, cache))
			}
		}
		return nv
	case *Function:
		params := make([]SimpleType, len(t.params))
		for i, p := range t.params {
			params[i] = in.extrude(p, pol.flip(), lvl, cache)
		}
		return &Function{params: params, paramNames: t.paramNames, ret: in.extrude(t.ret, pol, lvl, cache)}
	case *Tuple:
		elems := make([]SimpleType, len(t.elems))
		for i, e := range t.elems {
			elems[i] = in.extrude(e, pol, lvl, cache)
		}
		return &Tuple{elems: elems}
	default:
		return ty
	}
}

func describe(st SimpleType) string {
	switch t := st.(type) {
	case *Primitive:
		return t.name
	case *Literal:
		switch t.kind {
		case "str":
			return strconv.Quote(t.str)
		case "num":
			return strconv.FormatFloat(t.num, 'f', -1, 32)
		case "bool":
			return strconv.FormatBool(t.b)
		}
	case *Function:
		return "function"
	case *Tuple:
		return "tuple"
	case *Variable:
		return "t" + strconv.Itoa(t.id)
	}
	return "?"
}

func litKindPrim(l *Literal) string {
	switch l.kind {
	case "str":
		return "string"
	case "num":
		return "number"
	case "bool":
		return "boolean"
	}
	return ""
}

// ---- Type schemes (let-polymorphism) ----

type TypeScheme interface{ isScheme() }

// MonoScheme is a plain type (e.g. a lambda parameter): no generalization.
type MonoScheme struct{ ty SimpleType }

// PolyScheme generalizes variables in body whose level is > level.
type PolyScheme struct {
	level int
	body  SimpleType
}

func (*MonoScheme) isScheme() {}
func (*PolyScheme) isScheme() {}

func (in *Inferer) instantiate(s TypeScheme, lvl int) SimpleType {
	switch sc := s.(type) {
	case *MonoScheme:
		return sc.ty
	case *PolyScheme:
		return in.freshenAbove(sc.level, sc.body, lvl, map[int]*Variable{})
	}
	panic("unreachable")
}

// freshenAbove copies ty, replacing each variable with level > lim by a fresh
// variable at lvl (its bounds freshened too); variables at level <= lim are kept.
func (in *Inferer) freshenAbove(lim int, ty SimpleType, lvl int, cache map[int]*Variable) SimpleType {
	if levelOf(ty) <= lim {
		return ty
	}
	switch t := ty.(type) {
	case *Variable:
		if nv, ok := cache[t.id]; ok {
			return nv
		}
		nv := in.freshVar(lvl)
		cache[t.id] = nv
		for _, lb := range t.lowerBounds {
			nv.lowerBounds = append(nv.lowerBounds, in.freshenAbove(lim, lb, lvl, cache))
		}
		for _, ub := range t.upperBounds {
			nv.upperBounds = append(nv.upperBounds, in.freshenAbove(lim, ub, lvl, cache))
		}
		return nv
	case *Function:
		params := make([]SimpleType, len(t.params))
		for i, p := range t.params {
			params[i] = in.freshenAbove(lim, p, lvl, cache)
		}
		return &Function{params: params, paramNames: t.paramNames, ret: in.freshenAbove(lim, t.ret, lvl, cache)}
	case *Tuple:
		elems := make([]SimpleType, len(t.elems))
		for i, e := range t.elems {
			elems[i] = in.freshenAbove(lim, e, lvl, cache)
		}
		return &Tuple{elems: elems}
	default:
		return ty
	}
}

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

// ---- Occurrence analysis + coalescing/simplification ----

type polKey struct {
	id  int
	pol Polarity
}

// analyze records, for each variable, the polarities it occurs in (following
// bounds in the relevant direction). This drives single-polarity elimination.
func analyze(st SimpleType, pol Polarity, occurrences map[int]map[Polarity]bool, seen map[polKey]bool) {
	switch t := st.(type) {
	case *Variable:
		if occurrences[t.id] == nil {
			occurrences[t.id] = map[Polarity]bool{}
		}
		occurrences[t.id][pol] = true
		pk := polKey{t.id, pol}
		if seen[pk] {
			return
		}
		seen[pk] = true
		for _, b := range t.boundsAt(pol) {
			analyze(b, pol, occurrences, seen)
		}
	case *Function:
		for _, p := range t.params {
			analyze(p, pol.flip(), occurrences, seen)
		}
		analyze(t.ret, pol, occurrences, seen)
	case *Tuple:
		for _, e := range t.elems {
			analyze(e, pol, occurrences, seen)
		}
	}
}

// ---- Co-occurrence merging support ----

// collectVars gathers every variable reachable from st, following both bounds.
func collectVars(st SimpleType, set map[int]*Variable) {
	switch t := st.(type) {
	case *Variable:
		if _, ok := set[t.id]; ok {
			return
		}
		set[t.id] = t
		for _, b := range t.lowerBounds {
			collectVars(b, set)
		}
		for _, b := range t.upperBounds {
			collectVars(b, set)
		}
	case *Function:
		for _, p := range t.params {
			collectVars(p, set)
		}
		collectVars(t.ret, set)
	case *Tuple:
		for _, e := range t.elems {
			collectVars(e, set)
		}
	}
}

func containsVarBound(bounds []SimpleType, v *Variable) bool {
	for _, b := range bounds {
		if bv, ok := b.(*Variable); ok && bv.id == v.id {
			return true
		}
	}
	return false
}

// symmetrize records every var-to-var bound in both directions: if v <: w is
// stored on v.upperBounds, ensure w.lowerBounds contains v (and vice versa).
// constrain records bounds one-sided, but coalescing/co-occurrence needs each
// variable to see all of its own subtyping facts. Mirroring is idempotent, so a
// single pass over a snapshot of the edges suffices.
func symmetrize(vars map[int]*Variable) {
	type edge struct {
		from  *Variable
		to    *Variable
		upper bool // add 'to' to from.upperBounds (else lowerBounds)
	}
	var edges []edge
	for _, v := range vars {
		for _, u := range v.upperBounds {
			if uv, ok := u.(*Variable); ok {
				edges = append(edges, edge{uv, v, false}) // v <: uv  =>  uv has lower v
			}
		}
		for _, l := range v.lowerBounds {
			if lv, ok := l.(*Variable); ok {
				edges = append(edges, edge{lv, v, true}) // lv <: v  =>  lv has upper v
			}
		}
	}
	for _, e := range edges {
		if e.upper {
			if !containsVarBound(e.from.upperBounds, e.to) {
				e.from.upperBounds = append(e.from.upperBounds, e.to)
			}
		} else {
			if !containsVarBound(e.from.lowerBounds, e.to) {
				e.from.lowerBounds = append(e.from.lowerBounds, e.to)
			}
		}
	}
}

// gatherGroup collects the transitive same-polarity variable closure of v: the
// variables that end up in the same flattened union (positive) or intersection
// (negative) as v.
func gatherGroup(v *Variable, pol Polarity, g map[int]bool) {
	if g[v.id] {
		return
	}
	g[v.id] = true
	for _, b := range v.boundsAt(pol) {
		if bv, ok := b.(*Variable); ok {
			gatherGroup(bv, pol, g)
		}
	}
}

// collectCoOcc records, per (variable, polarity), the set of variables that
// co-occur with it (share a union/intersection node).
func collectCoOcc(st SimpleType, pol Polarity, coOcc map[polKey]map[int]bool, seen map[polKey]bool) {
	switch t := st.(type) {
	case *Variable:
		g := map[int]bool{}
		gatherGroup(t, pol, g)
		for a := range g {
			for b := range g {
				if a != b {
					k := polKey{a, pol}
					if coOcc[k] == nil {
						coOcc[k] = map[int]bool{}
					}
					coOcc[k][b] = true
				}
			}
		}
		pk := polKey{t.id, pol}
		if seen[pk] {
			return
		}
		seen[pk] = true
		for _, b := range t.boundsAt(pol) {
			collectCoOcc(b, pol, coOcc, seen)
		}
	case *Function:
		for _, p := range t.params {
			collectCoOcc(p, pol.flip(), coOcc, seen)
		}
		collectCoOcc(t.ret, pol, coOcc, seen)
	case *Tuple:
		for _, e := range t.elems {
			collectCoOcc(e, pol, coOcc, seen)
		}
	}
}

type unionFind struct{ parent map[int]int }

func newUnionFind() *unionFind { return &unionFind{parent: map[int]int{}} }

func (u *unionFind) find(x int) int {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p != x {
		r := u.find(p)
		u.parent[x] = r
		return r
	}
	return x
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if rb < ra { // keep the smaller id as representative for stable naming
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}

// mutualCoOcc reports whether a and b co-occur in every polarity each occurs in
// — the condition under which they can be soundly merged.
func mutualCoOcc(a, b int, occurrences map[int]map[Polarity]bool, coOcc map[polKey]map[int]bool) bool {
	if len(occurrences[a]) == 0 || len(occurrences[b]) == 0 {
		return false
	}
	for pol := range occurrences[a] {
		if !coOcc[polKey{a, pol}][b] {
			return false
		}
	}
	for pol := range occurrences[b] {
		if !coOcc[polKey{b, pol}][a] {
			return false
		}
	}
	return true
}

func mergeCoOccurring(vars map[int]*Variable, occurrences map[int]map[Polarity]bool, coOcc map[polKey]map[int]bool) *unionFind {
	uf := newUnionFind()
	ids := make([]int, 0, len(vars))
	for id := range vars {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if mutualCoOcc(ids[i], ids[j], occurrences, coOcc) {
				uf.union(ids[i], ids[j])
			}
		}
	}
	return uf
}

func dedupTypes(parts []type_system.Type) []type_system.Type {
	seen := map[string]bool{}
	out := parts[:0:0]
	for _, p := range parts {
		s := type_system.PrintType(p, type_system.PrintConfig{})
		if !seen[s] {
			seen[s] = true
			out = append(out, p)
		}
	}
	return out
}

// ---- Coalescing ----

type coalescer struct {
	names             map[int]string // keyed by representative id
	order             []string
	counter           int
	mergedOccurrences map[int]map[Polarity]bool // keyed by representative id
	uf                *unionFind
	inProc            map[polKey]bool // keyed by (representative id, polarity)
}

func (c *coalescer) nameForRep(rep int) string {
	if n, ok := c.names[rep]; ok {
		return n
	}
	n := "T" + strconv.Itoa(c.counter)
	c.counter++
	c.names[rep] = n
	c.order = append(c.order, n)
	return n
}

func (c *coalescer) coalesce(st SimpleType, pol Polarity) type_system.Type {
	switch t := st.(type) {
	case *Primitive:
		return primToType(t.name)
	case *Literal:
		return litToType(t)
	case *Function:
		params := make([]*type_system.FuncParam, len(t.params))
		for i, p := range t.params {
			params[i] = type_system.NewFuncParam(
				type_system.NewIdentPat(paramName(t.paramNames, i)),
				c.coalesce(p, pol.flip())) // contravariant
		}
		return type_system.NewFuncType(nil, nil, params, c.coalesce(t.ret, pol), nil) // covariant
	case *Tuple:
		elems := make([]type_system.Type, len(t.elems))
		for i, e := range t.elems {
			elems[i] = c.coalesce(e, pol)
		}
		return type_system.NewTupleType(nil, elems...)
	case *Variable:
		rep := c.uf.find(t.id)
		bipolar := c.mergedOccurrences[rep][Positive] && c.mergedOccurrences[rep][Negative]
		pk := polKey{rep, pol}
		if c.inProc[pk] {
			return type_system.NewTypeRefType(nil, c.nameForRep(rep), nil)
		}
		c.inProc[pk] = true
		defer delete(c.inProc, pk)

		boundTypes := make([]type_system.Type, 0, len(t.boundsAt(pol)))
		for _, b := range t.boundsAt(pol) {
			boundTypes = append(boundTypes, c.coalesce(b, pol))
		}

		if !bipolar {
			// Single-polarity variable: drop the variable itself and keep only
			// its bounds (positive => union of lowers, negative => inter of uppers).
			parts := dedupTypes(boundTypes)
			if len(parts) == 0 {
				if pol == Positive {
					return type_system.NewNeverType(nil)
				}
				return type_system.NewUnknownType(nil)
			}
			return combine(pol, parts)
		}

		self := type_system.NewTypeRefType(nil, c.nameForRep(rep), nil)
		parts := dedupTypes(append([]type_system.Type{self}, boundTypes...))
		return combine(pol, parts)
	default:
		panic(fmt.Sprintf("coalesce: unhandled %T", st))
	}
}

// combine builds a union (positive) or intersection (negative) of parts,
// returning the sole element directly when only one remains.
func combine(pol Polarity, parts []type_system.Type) type_system.Type {
	if len(parts) == 1 {
		return parts[0]
	}
	if pol == Positive {
		return type_system.NewUnionType(nil, parts...)
	}
	return type_system.NewIntersectionType(nil, parts...)
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

func litToType(l *Literal) type_system.Type {
	switch l.kind {
	case "str":
		return type_system.NewStrLitType(nil, l.str)
	case "num":
		return type_system.NewNumLitType(nil, l.num)
	case "bool":
		return type_system.NewBoolLitType(nil, l.b)
	default:
		panic("simplesub: unknown literal kind " + l.kind)
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

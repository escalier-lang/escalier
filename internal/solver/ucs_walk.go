package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
)

// The typing walk over the UCS normalized form.
//
// A conditional form is lowered by `ucs.Desugar*` and `ucs.Normalize` into a tree of
// splits, binds, guards, and leaves. A term is one node of that tree, which is what
// `ucs.Term` names. The bare word `node` is kept for an `ast.Node`, the surface node a
// diagnostic blames, so neither has to be read from context. This file types the tree for
// a `match`. A split applies its branch's tag test to the path binder, a bind resolves its
// projection and defines the leaf, a guard is an ordinary boolean condition over the names
// above it, and a leaf infers the body the user wrote for its arm.
//
// The walk carries three states rather than one, because a term's continuation does not
// run in the state the term itself runs in.
//
//   - cur is where the term being typed runs.
//   - fall is where the whole form started. Its scope holds none of the names an arm
//     bound, and its binder has no branch's tag test applied.
//   - matched is fall's binder with the enclosing branch's tag test applied. Control that
//     falls out of a branch reaches the arms below it knowing that branch's test held. In
//     `Ok(v) if g => v, Ok(v) => v` that is what resolves the second arm's `v`, since
//     normalization drops the second `Ok` test as one the first already proved.
//
// Some continuations are reached by more than one path. Such a continuation runs whether
// or not the enclosing branch's test matched, so it inherits no test. It runs in fall
// rather than in matched, and normGraph below is what identifies those continuations.

// condState is the scope and path binder one point of the walk runs in. A branch derives
// a new state from the state of the split it belongs to, so two branches of one split
// neither see each other's names nor each other's narrowing.
type condState struct {
	scope  *Scope
	binder *pathBinder
}

// condWalk types one conditional form. Everything it holds is fixed for that form: the
// checker, the level its variables are minted at, the node a body's join is blamed on,
// and the branch-join variable every non-diverging body constrains into.
type condWalk struct {
	c    *checker
	lvl  int
	node ast.Node
	res  soltype.Type
	// bodies collects the type of each non-diverging body, which the caller hands to
	// checkUniformOwnership. A diverging body produces no value, so it joins neither res
	// nor this slice.
	bodies []soltype.Type
	// arms holds the surface arm behind each body the walk typed. Normalization leaves an
	// arm below an unguarded catch-all out of the split, so a caller reads this to find
	// the arms it has to type some other way.
	arms set.Set[ucs.Spanned]
	// seen holds every term the walk has already typed, so a term two edges reach is
	// typed once. Typing it twice would infer the arm body twice and report anything
	// wrong with it twice.
	seen set.Set[ucs.Norm]
	// graph answers what the walk needs to know about the form's shape: whether a
	// continuation is one of those shared terms, and which arm bodies a term can reach.
	graph *normGraph
}

// newCondWalk builds the walk over norm, the normalized form of one conditional. node is
// the construct each body's join is blamed on, and res the branch-join variable those
// bodies constrain into.
func newCondWalk(c *checker, lvl int, node ast.Node, res soltype.Type, norm ucs.Norm) *condWalk {
	return &condWalk{
		c:     c,
		lvl:   lvl,
		node:  node,
		res:   res,
		arms:  set.NewSet[ucs.Spanned](),
		seen:  set.NewSet[ucs.Norm](),
		graph: newNormGraph(norm),
	}
}

// walkNorm types term. cur is the state term runs in, fall the state the whole form
// started in, and matched fall's binder with the enclosing branch's tag test applied.
func (w *condWalk) walkNorm(cur, fall condState, matched *pathBinder, term ucs.Norm) {
	if w.walked(term) {
		return
	}
	w.seen.Add(term)
	switch n := term.(type) {
	case *ucs.NormSplit:
		w.walkSplit(cur, fall, matched, n)
	case *ucs.NormBind:
		w.walkBind(cur, fall, matched, n)
	case *ucs.NormGuard:
		w.walkGuard(cur, fall, matched, n)
	case *ucs.BodyLeaf:
		w.walkBody(cur, n)
	case *ucs.EscapeLeaf, *ucs.FallbackLeaf:
		// The two leaves of a `val pat = init else { … }`. Its success path carries no body
		// at all. Its `else` carries one, but binding it needs the declaration's rules rather
		// than an arm's. Neither is typed here, and `ucs.DesugarMatch` mints neither.
	}
}

// walkSplit types each branch under the tag test it makes, then the tail control reaches
// when no test matched.
//
// The tail is a fallthrough, so it continues from the state the enclosing branch matched
// in. In `Line { start: {x, y} } => body` the split over `l.start` is one flattening the
// nested pattern produced, and its tail is whatever the `Line` branch falls to.
func (w *condWalk) walkSplit(cur, fall condState, matched *pathBinder, split *ucs.NormSplit) {
	for _, branch := range split.Branches {
		if w.walked(branch.Cont) {
			// The branch is a copy of one already typed. Its test is asked about before its
			// continuation is walked, so the skip has to happen here rather than inside
			// walkNorm, or applying the test would report the same diagnostics a second time.
			continue
		}
		tested := cur.binder.narrowedBy(cur.scope, split.Scrutinee, branch.Test, branch.Origin.Node)
		w.walkNorm(condState{scope: cur.scope, binder: tested}, fall, tested, branch.Cont)
	}
	w.walkFallthrough(fall, matched, split.Default)
}

// walkBind resolves the projection a bind names and defines its leaf, then types what
// runs with that leaf in scope.
//
// The leaf goes into a child scope, so it is visible to the guard and body below it and
// invisible everywhere else. That matters most for a bind under a split's tail, such as
// the `other` of `match p { {x, y} if g => x, other => other }`. Defining it in the
// enclosing scope would leak the name into the block the `match` sits in.
func (w *condWalk) walkBind(cur, fall condState, matched *pathBinder, bind *ucs.NormBind) {
	scope := cur.scope.Child()
	switch {
	case bind.Elem != nil:
		cur.binder.bindElemAt(scope, bind.Source, bind.Elem)
	case bind.Pat != nil:
		cur.binder.bindAt(scope, bind.Source, bind.Pat)
	}
	w.walkNorm(condState{scope: scope, binder: cur.binder}, fall, matched, bind.Cont)
}

// walkGuard types a guard's condition as a boolean over the names its branch bound, then
// types both continuations: the body the guard admits, and where a false condition goes.
func (w *condWalk) walkGuard(cur, fall condState, matched *pathBinder, guard *ucs.NormGuard) {
	// A guard is an ordinary boolean condition. As in inferIfElse, the synthesized
	// boolean requirement is left out of Prov. It is a language rule, not a user
	// annotation, so there is no source node to anchor a related span to.
	cond := w.c.inferExpr(cur.scope, w.lvl, guard.Cond)
	w.c.constrain(guard.Cond, cond, &soltype.PrimType{Prim: soltype.BoolPrim})
	w.walkNorm(cur, fall, matched, guard.Cont)
	w.walkFallthrough(fall, matched, guard.Default)
}

// walkFallthrough types where a failed test or a false guard continues. The names the
// branch bound are out of scope again, so it runs in fall's scope.
//
// It keeps the enclosing branch's tag test unless term is a join point. In
// `match p { {x, y} if g => x, other => other }` the `other` arm is one. The failed guard
// reaches it and so does a `p` that is no `{x, y}`, so its one body cannot assume the
// shape matched.
func (w *condWalk) walkFallthrough(fall condState, matched *pathBinder, term ucs.Norm) {
	next := condState{scope: fall.scope, binder: matched}
	if w.graph.reachesJoin(term) {
		next = fall
	}
	w.walkNorm(next, fall, next.binder, term)
}

// walked reports whether term carries no work the walk has left to do, either because the
// walk already typed that very term or because term is a copy of one it typed.
func (w *condWalk) walked(term ucs.Norm) bool {
	return term == nil || w.seen.Contains(term) || w.typedAlready(term)
}

// typedAlready reports whether every arm body term can reach has already been typed, which
// makes term a copy of work the walk has done.
//
// Normalization emits an arm as a branch of its split and again inside each earlier
// fallible branch's fallthrough. The copies are separate terms over one shared arm body, so
// the body is what identifies them. `match p { {x} if b => 1, {y} => 2 }` produces the
// `{y}` branch twice, and without this its test and its leaf would each run twice.
//
// A term reaching no body is not a copy. That is a `✗` tail, with no work to skip.
func (w *condWalk) typedAlready(term ucs.Norm) bool {
	reached := w.graph.bodiesUnder(term)
	if len(reached) == 0 {
		return false
	}
	for _, body := range reached {
		if !w.seen.Contains(body) {
			return false
		}
	}
	return true
}

// walkBody infers an arm body and joins it into the form's result. A diverging body
// produces no value, so when every body diverges nothing is constrained into res and it
// coalesces to `never`.
func (w *condWalk) walkBody(cur condState, leaf *ucs.BodyLeaf) {
	w.arms.Add(leaf.Arm)
	bodyT, diverges := w.c.inferBlockOrExpr(cur.scope, w.lvl, &leaf.Body)
	if diverges {
		return
	}
	w.c.constrain(w.node, bodyT, w.res)
	w.bodies = append(w.bodies, bodyT)
}

// normGraph is what the walk reads off the shape of one normalized form before typing it:
// which terms more than one path reaches, and which arm bodies each term can reach.
//
// Both questions exist because normalization emits an arm more than once. A branch that
// can fail falls into a copy of everything below it, so an arm is a branch of its own
// split and again inside each earlier fallible branch's fallthrough. The copies are
// separate terms over one shared arm body, which is why neither question can be answered
// by identity alone.
type normGraph struct {
	// edgesInto counts the continuations that reach each term. A term reached by one edge
	// runs under exactly one set of matched tests. A term reached by more can assume only
	// what holds on every path into it.
	edgesInto map[ucs.Norm]int
	// joins memoizes reachesJoin per term, since a fallthrough shared by several branches
	// is asked about once per branch.
	joins map[ucs.Norm]bool
	// bodies memoizes bodiesUnder per term.
	bodies map[ucs.Norm][]ucs.Norm
}

// newNormGraph counts the edges of the normalized form rooted at norm.
func newNormGraph(norm ucs.Norm) *normGraph {
	g := &normGraph{
		edgesInto: map[ucs.Norm]int{},
		joins:     map[ucs.Norm]bool{},
		bodies:    map[ucs.Norm][]ucs.Norm{},
	}
	g.countEdges(norm, set.NewSet[ucs.Norm]())
	return g
}

// countEdges records one entry per edge into each term, visiting each term once so a
// shared subterm's own edges are not counted twice.
func (g *normGraph) countEdges(term ucs.Norm, walked set.Set[ucs.Norm]) {
	for _, next := range continuations(term) {
		g.edgesInto[next]++
		if walked.Contains(next) {
			continue
		}
		walked.Add(next)
		g.countEdges(next, walked)
	}
}

// reachesJoin reports whether term is reached by more than one edge, or reaches such a
// term without falling through again. `match p { {x, y} if g => x, other => other }`
// produces two `bind other = p` terms over a single `leaf other`, and both binds answer
// true through that body.
//
// The walk below term is the one underMatchedTest describes. A term this one falls
// through to is left out, since walkFallthrough asks about it separately, and counting it
// here would answer true for a term whose own arm nothing else reaches.
func (g *normGraph) reachesJoin(term ucs.Norm) bool {
	if term == nil {
		return false
	}
	if got, walked := g.joins[term]; walked {
		return got
	}
	// Seed the memo before recursing. The normalized form is a tree of shared subterms with
	// no cycle, so no caller ever reads the seed. It is there to bound the walk should a
	// later rewrite introduce one.
	g.joins[term] = false
	got := g.edgesInto[term] > 1
	for _, next := range underMatchedTest(term) {
		if g.reachesJoin(next) {
			got = true
		}
	}
	g.joins[term] = got
	return got
}

// underMatchedTest returns the terms control reaches from term while the tag test that
// admitted term still holds: a split's branches, a guard's admitted continuation, and a
// bind's. A split's tail and a guard's failure continuation are left out, because each is
// a fallthrough that walkFallthrough decides the test of on its own.
func underMatchedTest(term ucs.Norm) []ucs.Norm {
	switch n := term.(type) {
	case *ucs.NormSplit:
		next := make([]ucs.Norm, 0, len(n.Branches))
		for _, branch := range n.Branches {
			next = appendTerm(next, branch.Cont)
		}
		return next
	case *ucs.NormGuard:
		return appendTerm(nil, n.Cont)
	case *ucs.NormBind:
		return appendTerm(nil, n.Cont)
	default:
		return nil
	}
}

// bodiesUnder returns the arm bodies control can reach from term, each listed once.
func (g *normGraph) bodiesUnder(term ucs.Norm) []ucs.Norm {
	if term == nil {
		return nil
	}
	if got, walked := g.bodies[term]; walked {
		return got
	}
	// Seeded before recursing for the reason reachesJoin's memo is.
	g.bodies[term] = nil
	reached := set.NewSet[ucs.Norm]()
	var found []ucs.Norm
	if isLeaf(term) {
		found = append(found, term)
		reached.Add(term)
	}
	for _, next := range continuations(term) {
		for _, body := range g.bodiesUnder(next) {
			if reached.Contains(body) {
				continue
			}
			reached.Add(body)
			found = append(found, body)
		}
	}
	g.bodies[term] = found
	return found
}

// isLeaf reports whether term ends a branch rather than continuing into another term. A
// leaf stands for one arm of the surface form, which is what makes it the unit
// bodiesUnder counts.
func isLeaf(term ucs.Norm) bool {
	switch term.(type) {
	case *ucs.BodyLeaf, *ucs.EscapeLeaf, *ucs.FallbackLeaf:
		return true
	default:
		return false
	}
}

// continuations returns every term control can reach from term in one step, which is what
// it reaches with its test still holding plus what it falls through to.
func continuations(term ucs.Norm) []ucs.Norm {
	return appendTerm(underMatchedTest(term), fallthroughOf(term))
}

// fallthroughOf returns where term continues when its own test fails. A split falls to its
// tail and a guard to its failure continuation. Every other term returns nil, as does a
// split with no covering branch, which the printer renders `✗`.
func fallthroughOf(term ucs.Norm) ucs.Norm {
	switch n := term.(type) {
	case *ucs.NormSplit:
		return n.Default
	case *ucs.NormGuard:
		return n.Default
	default:
		return nil
	}
}

// appendTerm adds term to next when it names a continuation at all.
func appendTerm(next []ucs.Norm, term ucs.Norm) []ucs.Norm {
	if term == nil {
		return next
	}
	return append(next, term)
}

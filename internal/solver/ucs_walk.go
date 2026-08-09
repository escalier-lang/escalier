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
// splits, binds, guards, and leaves. This file types that tree. A split applies its
// branch's tag test to the path binder, a bind resolves its projection and defines the
// leaf, a guard is an ordinary boolean condition over the names above it, and a leaf
// infers the body the user wrote for its arm.
//
// The walk carries three states rather than one, because a term's continuation does not
// run in the state the term itself runs in.
//
//   - cur is where the term being typed runs.
//   - fall is where the whole form started. Its scope holds none of the names an arm
//     bound, and its binder has no branch's tag test applied.
//   - matched is fall's scope with the enclosing branch's tag test applied. Control that
//     falls out of a branch reaches the arms below it knowing that branch's test held, so
//     `Ok(v) if g => v, Ok(v) => v` types the second arm's `v` through the `Ok` the first
//     arm tested. Normalization drops the second `Ok` test for exactly that reason.
//
// A continuation more than one path reaches takes fall rather than matched, per
// joinPoints below.

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
	// seen holds every term the walk has already typed, so a term two edges reach is
	// typed once. Typing it twice would infer the arm body twice and report anything
	// wrong with it twice.
	seen set.Set[ucs.Norm]
	// joins answers whether a continuation is one of those shared terms.
	joins *joinPoints
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
		seen:  set.NewSet[ucs.Norm](),
		joins: newJoinPoints(norm),
	}
}

// walkNorm types term. cur is the state term runs in, fall the state the whole form
// started in, and matched fall with the enclosing branch's tag test applied.
func (w *condWalk) walkNorm(cur, fall condState, matched *pathBinder, term ucs.Norm) {
	if term == nil || w.seen.Contains(term) {
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
	}
}

// walkSplit types each branch of a split under the tag test that branch makes, then the
// tail control reaches when no test matched.
//
// The tail is a fallthrough rather than part of the split. A split reached from inside a
// branch is one normalization built by flattening a nested pattern, and its tail is the
// fallthrough of the branch that encloses it. In `Line { start: {x, y} } => body` the
// split over `l.start` falls to whatever the `Line` branch falls to, not back into the
// `Line` branch, so the tail continues from the state the enclosing branch matched in.
func (w *condWalk) walkSplit(cur, fall condState, matched *pathBinder, split *ucs.NormSplit) {
	for _, branch := range split.Branches {
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
// branch bound are out of scope again, so the continuation runs in fall's scope.
//
// It keeps the enclosing branch's tag test unless term is a join point. A join point runs
// both when that test matched and when it did not, so only what holds on both paths may
// be assumed of it. In `match p { {x, y} if g => x, other => other }` the `other` arm is
// the join point: the failed guard reaches it, and so does a `p` that is not a `{x, y}`
// at all, and its one body cannot be typed as though the shape had matched.
func (w *condWalk) walkFallthrough(fall condState, matched *pathBinder, term ucs.Norm) {
	next := condState{scope: fall.scope, binder: matched}
	if w.joins.reachesJoin(term) {
		next = fall
	}
	w.walkNorm(next, fall, next.binder, term)
}

// walkBody infers an arm body and joins it into the form's result. A diverging body
// produces no value, so when every body diverges nothing is constrained into res and it
// coalesces to `never`.
func (w *condWalk) walkBody(cur condState, leaf *ucs.BodyLeaf) {
	bodyT, diverges := w.c.inferBlockOrExpr(cur.scope, w.lvl, &leaf.Body)
	if diverges {
		return
	}
	w.c.constrain(w.node, bodyT, w.res)
	w.bodies = append(w.bodies, bodyT)
}

// joinPoints answers which terms of one normalized form more than one path reaches.
//
// Normalization copies a branch's continuation into each earlier branch's fallthrough, so
// a term reached both ways is not always one node. The copies are separate nodes over one
// shared arm body, which is why a term counts as a join point when it merely reaches one.
// `match p { {x, y} if g => x, other => other }` produces two `bind other = p` nodes over
// a single `leaf other`, and both binds have to be treated as joins for the arm's `other`
// to be typed at what it holds on either path.
type joinPoints struct {
	// indeg counts the edges into each term. A term one edge reaches runs under exactly
	// one set of matched tests.
	indeg map[ucs.Norm]int
	// reaches memoizes the answer per term, since a fallthrough shared by several branches
	// is asked about once per branch.
	reaches map[ucs.Norm]bool
}

// newJoinPoints counts the edges of the normalized form rooted at norm.
func newJoinPoints(norm ucs.Norm) *joinPoints {
	j := &joinPoints{indeg: map[ucs.Norm]int{}, reaches: map[ucs.Norm]bool{}}
	j.countEdges(norm, set.NewSet[ucs.Norm]())
	return j
}

// countEdges records one entry per edge into each term, visiting each term once so a
// shared subterm's own edges are not counted twice.
func (j *joinPoints) countEdges(term ucs.Norm, walked set.Set[ucs.Norm]) {
	for _, next := range continuations(term) {
		j.indeg[next]++
		if walked.Contains(next) {
			continue
		}
		walked.Add(next)
		j.countEdges(next, walked)
	}
}

// reachesJoin reports whether term is reached by more than one edge, or can reach a term
// that is.
func (j *joinPoints) reachesJoin(term ucs.Norm) bool {
	if term == nil {
		return false
	}
	if got, walked := j.reaches[term]; walked {
		return got
	}
	// Seed the memo before recursing. The normalized form is a tree of shared subterms
	// with no cycle, so the seed is never the answer a caller reads; it bounds the walk if
	// a later rewrite ever introduces one.
	j.reaches[term] = false
	got := j.indeg[term] > 1
	for _, next := range continuations(term) {
		if j.reachesJoin(next) {
			got = true
		}
	}
	j.reaches[term] = got
	return got
}

// continuations returns the terms control can reach from term in one step. A nil
// continuation is left out, which is what a split with no covering branch carries and
// what a guard with nothing to fall into carries. A leaf continues nowhere.
func continuations(term ucs.Norm) []ucs.Norm {
	switch n := term.(type) {
	case *ucs.NormSplit:
		next := make([]ucs.Norm, 0, len(n.Branches)+1)
		for _, branch := range n.Branches {
			next = appendTerm(next, branch.Cont)
		}
		return appendTerm(next, n.Default)
	case *ucs.NormGuard:
		return appendTerm(appendTerm(nil, n.Cont), n.Default)
	case *ucs.NormBind:
		return appendTerm(nil, n.Cont)
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

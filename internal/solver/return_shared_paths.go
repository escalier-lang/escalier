package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Two paths out of one return. A returned value may reach a function-local more than once, and
// when a write can go through one of those paths the caller ends up holding two disagreeing
// views of one object. `return [a.peer, &mut b]` over `val a = {peer: &mut b}` hands out two
// mutable handles to b.
//
// Two SHARED paths are fine, since two readers see the same value. What makes the pair a hazard
// is that one of them can write.
//
// The borrow-edge graph cannot answer this on its own. addBorrowEdge keeps one edge per route
// into the referent, so the two borrows of b in that tuple, which take the same route, collapse
// to a single edge and the graph reads the same as a single path. The RETURN TYPE is what still
// distinguishes them, because it carries one borrow position per path. So the count comes from
// the type and the data each position reaches comes from the graph.
//
// Two paths are a hazard only when they reach OVERLAPPING data. `{p: &mut b.x, q: &mut b.y}`
// hands out two handles to one local and no caller can write through either and be seen by the
// other, so the pair is fine. An edge carries the path inside its referent, so the check
// compares those paths and reports only a pair whose paths are prefix-related.
//
// Pairing a position with an edge is exact only where a field path names it. An object's
// properties each extend the path, so `{p: &mut b, q: &mut b}` gives two positions at two
// paths, both resolving to b. A tuple's elements all sit at the container path, since placeSeg
// has no tuple-index kind, so the pairing there is by count: two positions over one edge means
// both reach that edge's referent.
//
// What this does NOT cover: a path group holding more positions than edges when several edges
// share it. Which position reaches which referent is unknown there, so the group is skipped
// rather than guessed at.

// SharedReturnPathsError reports a returned value that reaches one local through two paths
// where a write can go through at least one of them.
type SharedReturnPathsError struct {
	// LocalName is the local reached twice, for the message.
	LocalName string
	// node is the returned expression, which is where both paths leave together.
	node ast.Node
}

func (*SharedReturnPathsError) isSolverError()        {}
func (e *SharedReturnPathsError) Span() ast.Span      { return e.node.Span() }
func (e *SharedReturnPathsError) Related() []ast.Span { return nil }
func (e *SharedReturnPathsError) Message() string {
	return fmt.Sprintf("returned value reaches '%s' through two paths while one of them can write", e.LocalName)
}

// borrowPosition is one borrow the return type carries: the field path within the carrier where
// it sits, and whether a write can go through it.
type borrowPosition struct {
	path []placeSeg
	mut  bool
}

// reportSharedReturnPaths reports each local the returned value reaches more than once with a
// write available through one of those paths. root is the carrier the graph's edges hang off,
// and blame is the returned expression both paths leave through.
func (c *checker) reportSharedReturnPaths(
	ret soltype.Type,
	root liveness.VarID,
	graph map[liveness.VarID][]fieldBorrow,
	blame ast.Expr,
) {
	var reached []reachedPlace
	// A literal carrier is counted from its own elements, which name their referents whether or
	// not the evaluator has settled their types. A place carrier has no elements to walk, so
	// its count comes from the type below.
	if c.reportLiteralSharedPaths(blame, graph) {
		return
	}
	var positions []borrowPosition
	collectBorrowPositions(ret, nil, &positions)
	if len(positions) < 2 {
		return
	}
	for _, group := range groupByPath(positions) {
		edges := edgesAt(graph, root, group.path)
		// A group with no edge names no tracked local, a parameter borrow among them.
		if len(edges) == 0 {
			continue
		}
		if len(edges) > 1 && len(group.positions) > len(edges) {
			// Which position reaches which referent is unknown, so this group is left alone.
			continue
		}
		if len(edges) == 1 {
			// Every position at this path reaches the one place the edge names, so each of them
			// is one more path to it, writable or not on its own terms.
			for _, pos := range group.positions {
				reached = append(reached, reachedPlace{referent: edges[0].referent, refPath: edges[0].refPath, mut: pos.mut})
			}
			continue
		}
		// The positions pair off one to one with the edges, but which position takes which
		// edge is unknown. A group mixing a written borrow with a read one would mark the read
		// edge writable on the strength of the other, so only an all-writable group says
		// anything about any single edge here.
		allMut := !slices.ContainsFunc(group.positions, func(p borrowPosition) bool { return !p.mut })
		for _, edge := range edges {
			reached = append(reached, reachedPlace{referent: edge.referent, refPath: edge.refPath, mut: allMut})
		}
	}
	c.reportReachedTwice(reached, blame)
}

// reachedPlace is one path the returned value takes into a local: the local it lands in, the
// field path within it the path reaches, and whether a write can go through that path.
type reachedPlace struct {
	referent liveness.VarID
	refPath  []placeSeg
	mut      bool
}

// reportReachedTwice reports each local two of the paths reach overlapping data in, with a write
// available through at least one of the two. Reports come in VarID order so a value reached from
// several places reads the same way every run.
//
// Two paths into one local overlap when their field paths within it are prefix-related. Equal
// paths reach the same data, and a path above another contains it. Disjoint fields such as
// `b.x` and `b.y` are neither, and a write through one is invisible through the other.
func (c *checker) reportReachedTwice(reached []reachedPlace, blame ast.Expr) {
	var shared []liveness.VarID
	for i, a := range reached {
		for _, b := range reached[i+1:] {
			if a.referent != b.referent || slices.Contains(shared, a.referent) {
				continue
			}
			if (a.mut || b.mut) && pathPrefixRelated(a.refPath, b.refPath) {
				shared = append(shared, a.referent)
			}
		}
	}
	slices.Sort(shared)
	for _, referent := range shared {
		c.noteSharedPathReturn(blame)
		c.report(&SharedReturnPathsError{LocalName: c.varIDToName(referent), node: blame})
	}
}

// countLiteralPaths counts the locals a returned object or tuple literal reaches, one element at
// a time. ok is false for any other carrier, which the type walk counts instead.
//
// Each element names its referents directly, so this does not depend on the evaluator having
// settled the element's type. That matters because a field read such as `a.peer` records its
// type as a variable the evaluator settles after this pass runs, which leaves the type walk
// seeing one borrow where the literal has two.
//
// An element that is an explicit `&mut` marks its referents writable. Any other element counts
// toward the reach without claiming a write, so a pair of reads is not reported and a pair whose
// writability is unknown is reported only when the other path is a written `&mut`.
func (c *checker) reportLiteralSharedPaths(
	e ast.Expr,
	graph map[liveness.VarID][]fieldBorrow,
) bool {
	var elems []ast.Expr
	switch e := e.(type) {
	case *ast.TupleExpr:
		elems = e.Elems
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			if prop, isProp := elem.(*ast.PropertyExpr); isProp && prop.Value != nil {
				elems = append(elems, prop.Value)
			}
		}
	default:
		return false
	}
	// One reach per element, keeping the field path so two disjoint fields of one local stay
	// apart. `[&mut b.x, &mut b.y]` names b twice and reaches nothing twice.
	type reach struct {
		place movePlace
		mut   bool
	}
	var reaches []reach
	for _, elem := range elems {
		mut := false
		if borrow, isBorrow := elem.(*ast.BorrowExpr); isBorrow {
			mut = borrow.Mut
		}
		for _, pl := range c.elementReferents(elem, graph) {
			reaches = append(reaches, reach{place: pl, mut: mut})
		}
	}
	seen := set.NewSet[liveness.VarID]()
	for i := range reaches {
		for j := i + 1; j < len(reaches); j++ {
			if !placesOverlap(reaches[i].place, reaches[j].place) {
				continue
			}
			if !reaches[i].mut && !reaches[j].mut {
				continue
			}
			root := reaches[i].place.root
			if seen.Contains(root) {
				continue
			}
			seen.Add(root)
			c.noteSharedPathReturn(e)
			c.report(&SharedReturnPathsError{LocalName: c.varIDToName(root), node: e})
		}
	}
	return true
}

// noteSharedPathReturn records the returned expression a shared-path report blamed, so the use
// check does not add a second diagnostic naming a read this very expression contains. Only a
// read inside it is covered, since a read elsewhere in the body is a separate fact.
func (c *checker) noteSharedPathReturn(blame ast.Expr) {
	span := blame.Span()
	if slices.ContainsFunc(c.fn.sharedPathSpans, func(s ast.Span) bool { return s == span }) {
		return
	}
	c.fn.sharedPathSpans = append(c.fn.sharedPathSpans, span)
}

// elementReferents returns the locals one element of a returned literal reaches.
//
// A `&mut b` element reaches b outright, and whatever b itself borrows beyond that. A plain
// place reaches only what its edges lead to: `a.peer` over `val a = {peer: &mut b}` reaches b,
// not a. Reading a as the referent would miss the pair in `[a.peer, &mut b]`, where both
// elements lead to the same b.
func (c *checker) elementReferents(
	elem ast.Expr,
	graph map[liveness.VarID][]fieldBorrow,
) []movePlace {
	var out []movePlace
	reached := set.NewSet[liveness.VarID]()
	if borrow, isBorrow := elem.(*ast.BorrowExpr); isBorrow {
		// The borrow names the place outright, field path and all, so `&mut b.x` reaches b.x.
		if _, ok := c.isLocalReferent(borrow.Arg); !ok {
			return nil
		}
		pl, ok := exprPlace(borrow.Arg)
		if !ok || pl.root <= 0 {
			return nil
		}
		out = append(out, pl)
		c.collectBorrowedFrom(pl.root, pl.path, reached, set.NewSet[liveness.VarID](), graph)
	} else {
		pl, ok := exprPlace(elem)
		if !ok || pl.root <= 0 {
			return nil
		}
		c.collectBorrowedFrom(pl.root, pl.path, reached, set.NewSet[liveness.VarID](), graph)
	}
	// An edge names the whole local it reaches, since fieldBorrow records no path within the
	// referent. Those come back as whole-binding places.
	ids := reached.ToSlice()
	slices.Sort(ids)
	for _, id := range ids {
		out = append(out, movePlace{root: id})
	}
	return out
}

// pathGroup is the borrow positions sharing one field path.
type pathGroup struct {
	path      []placeSeg
	positions []borrowPosition
}

// groupByPath gathers the positions that sit at the same field path, in first-seen order so the
// grouping does not depend on map iteration.
func groupByPath(positions []borrowPosition) []pathGroup {
	var groups []pathGroup
	for _, p := range positions {
		i := slices.IndexFunc(groups, func(g pathGroup) bool { return slices.Equal(g.path, p.path) })
		if i < 0 {
			groups = append(groups, pathGroup{path: p.path, positions: []borrowPosition{p}})
			continue
		}
		groups[i].positions = append(groups[i].positions, p)
	}
	return groups
}

// edgesAt returns the carrier's edges sitting exactly at path, one per distinct place they
// reach. Two borrows the graph records at one path into one place are a single edge, since
// addBorrowEdge keeps one per route.
func edgesAt(graph map[liveness.VarID][]fieldBorrow, root liveness.VarID, path []placeSeg) []fieldBorrow {
	var out []fieldBorrow
	for _, e := range graph[root] {
		if !slices.Equal(e.path, path) {
			continue
		}
		if slices.ContainsFunc(out, func(x fieldBorrow) bool {
			return x.referent == e.referent && slices.Equal(x.refPath, e.refPath)
		}) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// collectBorrowPositions gathers every borrow the type carries, with the field path it sits at.
// It mirrors stripBorrowTree's descent so the paths it produces match the ones the graph's edges
// are keyed on.
//
//   - A borrow carrying a lifetime is a position and is not descended into. What it points at
//     belongs to the referent, not to this carrier.
//   - An owned-mutable cell descends at the same path, since the cell names no field.
//   - An object descends each property at the path extended by its name.
//   - A tuple descends each element at the same path, since a tuple index contributes no
//     field segment.
func collectBorrowPositions(t soltype.Type, path []placeSeg, out *[]borrowPosition) {
	switch t := t.(type) {
	case *soltype.RefType:
		if t.Lt != nil {
			*out = append(*out, borrowPosition{path: path, mut: t.Mut})
			return
		}
		collectBorrowPositions(t.Inner, path, out)
	case *soltype.ObjectType:
		if soltype.HasResidualElem(t.Elems) {
			return
		}
		for _, e := range t.Elems {
			// Only a plain property names a field path a borrow edge can be keyed on. A getter,
			// setter, method, or index signature carries no such path, and asserting one here
			// would panic on the element kind rather than skipping it.
			prop, isProp := e.(*soltype.PropertyElem)
			if !isProp {
				continue
			}
			propPath := path
			if isDotPlaceSegment(prop.Name) {
				propPath = appendSeg(path, prop.Name)
			}
			collectBorrowPositions(prop.Type, propPath, out)
		}
	case *soltype.TupleType:
		for _, elem := range t.Elems {
			collectBorrowPositions(elem, path, out)
		}
	}
}

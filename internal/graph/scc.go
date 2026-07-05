// Package graph holds small, reusable directed-graph algorithms shared across
// the compiler. Its first member is a generic Tarjan strongly-connected-
// components pass that the solver, checker, and dep_graph each wrap.
package graph

import "github.com/escalier-lang/escalier/internal/set"

// StronglyConnectedComponents returns the strongly connected components of the
// directed graph whose vertices are nodes and whose edges are given by
// successors: successors(v) yields the out-neighbours of v. It runs Tarjan's
// algorithm.
//
// Components come back in reverse topological order. A component is emitted only
// after every component reachable from it, so if some vertex in A has an edge
// into B, then B appears before A. Within a component the members are in the
// order Tarjan pops them off its stack. Callers that need a canonical member
// order sort or reduce each component themselves.
//
// Determinism is the caller's contract. The traversal is seeded in the order
// nodes is given and visits each vertex's out-neighbours in the order
// successors returns them. A caller that wants a reproducible result passes both
// pre-sorted. The helper imposes no ordering on T, which is what lets it serve
// int, string, and other comparable keys from one body.
//
// A vertex reached through successors but absent from nodes is still traversed
// and placed in a component, but it is never used as a DFS seed. A caller that
// wants an isolated vertex represented as its own singleton must include it in
// nodes.
//
// The DFS runs on an explicit work stack rather than the goroutine call stack,
// so a pathologically deep graph cannot overflow it. The bound is available
// memory, far beyond any graph the compiler builds today.
func StronglyConnectedComponents[T comparable](nodes []T, successors func(T) []T) [][]T {
	index := 0
	indices := map[T]int{}
	lowlink := map[T]int{}
	onStack := set.NewSet[T]()
	var stack []T
	var sccs [][]T

	// visit assigns v its DFS index, pushes it onto the Tarjan stack, and returns
	// the work frame that drives iteration over its out-neighbours.
	visit := func(v T) frame[T] {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack.Add(v)
		return frame[T]{v: v, succ: successors(v)}
	}

	for _, n := range nodes {
		if _, seen := indices[n]; seen {
			continue
		}

		// work is the explicit recursion stack. Each frame tracks a vertex and how
		// far we have advanced through its out-neighbours, so we can resume where a
		// simulated recursive call left off.
		work := []frame[T]{visit(n)}
		for len(work) > 0 {
			f := &work[len(work)-1]
			if f.i < len(f.succ) {
				w := f.succ[f.i]
				f.i++
				if _, seen := indices[w]; !seen {
					// Descend into w. Its lowlink folds back into f.v when its frame
					// pops, matching the post-recursion update in the recursive form.
					work = append(work, visit(w))
				} else if onStack.Contains(w) {
					if indices[w] < lowlink[f.v] {
						lowlink[f.v] = indices[w]
					}
				}
				continue
			}

			// v's out-neighbours are exhausted, so its recursive call would return here.
			v := f.v
			if lowlink[v] == indices[v] {
				// v roots a component. Pop the stack down to and including v.
				var scc []T
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack.Remove(w)
					scc = append(scc, w)
					if w == v {
						break
					}
				}
				sccs = append(sccs, scc)
			}

			work = work[:len(work)-1]
			if len(work) > 0 {
				// Fold v's lowlink into its parent, the caller in the recursive form.
				parent := &work[len(work)-1]
				if lowlink[v] < lowlink[parent.v] {
					lowlink[parent.v] = lowlink[v]
				}
			}
		}
	}
	return sccs
}

// frame is one entry on the explicit DFS stack: the vertex v under expansion,
// its cached out-neighbours succ, and i, the index of the next out-neighbour to
// visit.
type frame[T comparable] struct {
	v    T
	succ []T
	i    int
}

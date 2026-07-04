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
// The DFS recurses, so a pathologically deep graph can overflow the goroutine
// stack. Go grows goroutine stacks dynamically, so the practical bound is
// available memory, far beyond any graph the compiler builds today.
func StronglyConnectedComponents[T comparable](nodes []T, successors func(T) []T) [][]T {
	index := 0
	indices := map[T]int{}
	lowlink := map[T]int{}
	onStack := set.NewSet[T]()
	var stack []T
	var sccs [][]T

	var strongconnect func(v T)
	strongconnect = func(v T) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack.Add(v)

		for _, w := range successors(v) {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack.Contains(w) {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] != indices[v] {
			return // v is not a component root, so its members stay on the stack
		}
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

	for _, n := range nodes {
		if _, seen := indices[n]; !seen {
			strongconnect(n)
		}
	}
	return sccs
}

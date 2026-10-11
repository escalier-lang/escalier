package solver

import (
	"fmt"
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// rigidLtCheck is one super signature's lifetime binder waiting for checkRigidLifetimes.
// params are the binder's parameters and fresh the lifetimes skolemizeFuncBinder instantiated
// them with, in the same order. site is the node the diagnostic points at and mark the lifetime
// counter at the start of that node's inference, both filled in by siteRigidLifetimeChecks.
// mark is -1 while unknown, and the check then judges no relation to a lifetime outside the
// binder.
type rigidLtCheck struct {
	params []*soltype.LifetimeParam
	fresh  []*soltype.LifetimeVar
	site   ast.Node
	mark   int
}

// checkRigidLifetimes reports each queued binder whose sub required something of a quantified
// lifetime that the signature's caller cannot be held to. The caller chooses each lifetime,
// so the sub has to work for every choice, and the fresh lifetimes the check instantiated the
// binder with show what it required instead. A relation between two of them, or from one of
// them to a lifetime outside the binder, has to follow from the declared bounds. Any other
// relation is allowed only with a lifetime the sub minted itself, one minted at or after the
// site's mark, since the sub is free to narrow its own lifetimes to the caller's choice. A
// relation to an earlier lifetime, one the sub shares with its surroundings, and a forcing
// to 'static the bounds do not declare are each reported once per site and message.
//
// It runs once the enclosing component has solved, when the outlives graph the fresh lifetimes
// sit in is complete, and reads one graph for every check queued. A check with no site, queued
// by a constraint run that committed outside the checker's wrapper, reports nothing. A trial
// under a probe evaluates its own checks before the probe rolls them back, through
// trialUnderProbeSeen.
func (c *checker) checkRigidLifetimes() {
	pending := c.ctx.pendingRigidLts
	c.ctx.pendingRigidLts = nil
	if len(pending) == 0 {
		return
	}
	graph := buildLtBoundSet(occOf(rigidLifetimes(pending)))
	type diagnostic struct {
		site ast.Node
		text string
	}
	reported := set.NewSet[diagnostic]()
	for _, p := range pending {
		if p.site == nil {
			continue
		}
		requires, ok := p.violation(graph)
		if !ok || reported.Contains(diagnostic{p.site, requires.text}) {
			continue
		}
		reported.Add(diagnostic{p.site, requires.text})
		c.report(&LifetimeBinderNotSatisfiedError{Name: requires.name, Requires: requires.text, Node: p.site})
	}
}

// rigidLifetimes returns every fresh lifetime the checks hold, the variables a graph over
// them is seeded from.
func rigidLifetimes(checks []*rigidLtCheck) []*soltype.LifetimeVar {
	var out []*soltype.LifetimeVar
	for _, p := range checks {
		out = append(out, p.fresh...)
	}
	return out
}

// rigidRequirement is what a sub required of one quantified lifetime: the lifetime's source
// name and the requirement in the message's words.
type rigidRequirement struct {
	name string
	text string
}

// violation returns the first requirement the sub put on one of the check's lifetimes that the
// binder does not declare, in parameter order, or ok=false when the sub works for every choice.
// graph is the outlives graph seeded from the check's lifetimes, read by representative so a
// lifetime condensed into a component with a declared bound counts as that bound.
func (p *rigidLtCheck) violation(graph *ltBoundSet) (rigidRequirement, bool) {
	own := set.NewSet[int]()
	for _, lv := range p.fresh {
		own.Add(graph.repOf(lv.ID))
	}
	for i, lv := range p.fresh {
		name := p.params[i].Name
		declared, declaredStatic := p.declaredOutlives(graph, i)
		if declaredStatic {
			// Every choice of a lifetime declared to outlive 'static is 'static, so nothing
			// the sub requires of it narrows the caller's choice.
			continue
		}
		rep := graph.repOf(lv.ID)
		if graph.static.Contains(rep) {
			return rigidRequirement{name, name + " to be 'static"}, true
		}
		for j, other := range p.fresh {
			if j == i || declared.Contains(graph.repOf(other.ID)) {
				continue
			}
			if graph.implies(lv.ID, other.ID) {
				return rigidRequirement{name, fmt.Sprintf("%s to outlive %s, which the signature does not declare", name, p.params[j].Name)}, true
			}
		}
		if p.mark < 0 {
			continue
		}
		// Sorted so the first violation found, and with it the message, is the same on every run.
		for _, id := range slices.Sorted(maps.Keys(graph.rep)) {
			if id >= p.mark || own.Contains(graph.repOf(id)) {
				continue
			}
			if graph.implies(lv.ID, id) && !declared.Contains(graph.repOf(id)) {
				return rigidRequirement{name, name + " to outlive a lifetime the signature does not quantify"}, true
			}
			if graph.implies(id, lv.ID) {
				return rigidRequirement{name, name + " to be outlived by a lifetime the signature does not quantify"}, true
			}
		}
	}
	return rigidRequirement{}, false
}

// declaredOutlives returns the representatives in graph of the lifetimes the binder declares
// parameter i to outlive, closed through the siblings it names, so `<'a: 'b, 'b: 'c, 'c>`
// puts 'c in 'a's set and `<'b: 'a>` on a method puts the class's 'a in 'b's. static reports
// a declared bound on 'static, which the set does not hold since 'static is no variable. A
// bound naming a sibling is read as that sibling's fresh lifetime, the one the graph holds.
func (p *rigidLtCheck) declaredOutlives(graph *ltBoundSet, i int) (reach set.Set[int], static bool) {
	fresh := map[*soltype.LifetimeVar]*soltype.LifetimeVar{}
	index := map[*soltype.LifetimeVar]int{}
	for j, lp := range p.params {
		fresh[lp.Var] = p.fresh[j]
		index[p.fresh[j]] = j
	}
	asFresh := func(lt soltype.Lifetime) soltype.Lifetime {
		if lv, ok := lt.(*soltype.LifetimeVar); ok {
			if f, sibling := fresh[lv]; sibling {
				return f
			}
		}
		return lt
	}
	reach = set.NewSet[int]()
	seen := set.NewSet[soltype.Lifetime]()
	var work []soltype.Lifetime
	for _, b := range p.params[i].Bounds {
		work = append(work, asFresh(b))
	}
	for len(work) > 0 {
		next := work[len(work)-1]
		work = work[:len(work)-1]
		if seen.Contains(next) {
			continue
		}
		seen.Add(next)
		nv, isVar := next.(*soltype.LifetimeVar)
		if !isVar {
			static = static || next == soltype.Static
			continue
		}
		reach.Add(graph.repOf(nv.ID))
		if j, sibling := index[nv]; sibling {
			for _, b := range p.params[j].Bounds {
				work = append(work, asFresh(b))
			}
		}
	}
	return reach, static
}

package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A recursive alias is safe to expand exactly when it reaches finitely many distinct instantiation
// states. Such recursion is called regular: a parameter travels around the cycle without gaining
// structure. Expanding `List<number>` for `type List<T> = {head: T, tail?: List<T>}` yields a body
// naming `List<number>` again, the identical state, and the evaluator's active-state guard closes
// the recursion there.
//
// Recursion that wraps a parameter under a type constructor every lap never repeats a state. Each
// lap of `type Grow<T> = Grow<{a: T}>` builds a strictly larger argument, so the reachable
// instantiations are infinite and the guard never fires. Only maxExpandDepth and maxExpandKeyChars
// stop that walk, and they stop it with a truncated residual rather than an explanation.
//
// checkRegular is the definition-time diagnostic for the second shape. It is decidable and
// conservative, so it accepts every regular alias and rejects some terminating programs. An alias
// whose growth is gated on a base-case conditional does terminate. Deciding that in general is the
// halting problem, so the check rejects it too. The runtime budgets stay in place underneath as the
// backstop for what the check cannot see, such as expansion reached through an imported type or a
// chain of non-recursive aliases that fans out exponentially.
//
// Mutual recursion goes through the alias reference graph. A recursive reference means a reference
// to any alias in the same strongly connected component, so `type A<T> = B<{x: T}>` paired with
// `type B<U> = A<U>` is caught even though neither body names itself.

// checkRegular reports every alias in shells whose recursion grows one of its own type parameters.
// shells are the aliases of one dep_graph component, which is already a strongly connected set of
// declarations, so no alias outside it can close a cycle with one inside it. Their bodies must be
// resolved before the check runs, since it reads AliasDef.Body.
func (c *checker) checkRegular(shells []*aliasShell) {
	if len(shells) == 0 {
		return
	}
	groups := recursionGroups(shells)
	for _, sh := range shells {
		group, recursive := groups[sh.qname]
		if !recursive || len(sh.def.TypeParams) == 0 {
			continue
		}
		growing := growingParams(sh.def, group)
		// Report in declaration order rather than by name, so a two-parameter alias blames its
		// parameters the way the source lists them.
		for _, p := range sh.def.TypeParams {
			if growing.Contains(p.Name) {
				c.report(&NotRegularAliasError{Decl: sh.decl, Name: sh.qname, Param: p.Name})
			}
		}
	}
}

// growingParams returns the names of def's type parameters that appear under a type constructor in
// an argument of a recursive reference. Such a parameter is strictly larger on the next lap. group
// is the set of alias names in def's recursion cycle, so a reference to any of them is recursive.
func growingParams(def *AliasDef, group set.Set[string]) set.Set[string] {
	params := map[*soltype.TypeVarType]string{}
	for _, p := range def.TypeParams {
		params[p.Var] = p.Name
	}
	finder := &expandingCallFinder{group: group, params: params, growing: set.NewSet[string]()}
	def.Body.Accept(finder, soltype.Positive)
	return finder.growing
}

// recursionGroups returns, for each alias that participates in a recursion cycle, the names of the
// aliases in its cycle including itself. An alias that reaches no cycle is absent from the result.
//
// The cycles are the strongly connected components of the graph whose vertices are the aliases in
// the component and whose edges are the alias references in their bodies. A component of two or
// more aliases is a cycle. A component of one is a cycle only when that alias's body names itself.
func recursionGroups(shells []*aliasShell) map[string]set.Set[string] {
	names := make([]string, 0, len(shells))
	for _, sh := range shells {
		names = append(names, sh.qname)
	}
	within := set.FromSlice(names)
	refs := map[string][]string{}
	for _, sh := range shells {
		collector := &aliasRefCollector{within: within, found: set.NewSet[string]()}
		sh.def.Body.Accept(collector, soltype.Positive)
		out := collector.found.ToSlice()
		// Sort each alias's out-edges, and the seeds below, so the components come back in the same
		// order for the same input and a diagnostic never depends on map iteration order.
		slices.Sort(out)
		refs[sh.qname] = out
	}
	slices.Sort(names)

	groups := map[string]set.Set[string]{}
	for _, component := range graph.StronglyConnectedComponents(names, func(n string) []string { return refs[n] }) {
		if len(component) == 1 && !slices.Contains(refs[component[0]], component[0]) {
			continue
		}
		group := set.FromSlice(component)
		for _, name := range component {
			groups[name] = group
		}
	}
	return groups
}

// aliasRefCollector records which of a fixed set of alias names a type references. It rewrites
// nothing, so every node it visits comes back unchanged.
type aliasRefCollector struct {
	within set.Set[string]
	found  set.Set[string]
}

func (v *aliasRefCollector) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if a, ok := t.(*soltype.AliasType); ok && v.within.Contains(a.Name) {
		v.found.Add(a.Name)
	}
	return soltype.EnterResult{}
}

func (v *aliasRefCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// expandingCallFinder walks an alias body and, at every reference into the alias's recursion cycle,
// checks each type argument for a parameter that gained structure. It records the names of the
// parameters that did and rewrites nothing.
type expandingCallFinder struct {
	group   set.Set[string]
	params  map[*soltype.TypeVarType]string
	growing set.Set[string]
}

func (v *expandingCallFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	a, ok := t.(*soltype.AliasType)
	if !ok || !v.group.Contains(a.Name) {
		return soltype.EnterResult{}
	}
	// Each argument is measured from its own root, so the constructors enclosing the reference in
	// the body do not count. The `{…}` around `DeepPartial<T[K]>` in
	// `type DeepPartial<T> = {[K]?: DeepPartial<T[K]> for K in keyof T}` wraps the reference, not
	// the argument, and that alias is regular.
	for _, arg := range a.TypeArgs {
		nested := &nestedParamFinder{params: v.params, growing: v.growing}
		arg.Accept(nested, soltype.Positive)
	}
	return soltype.EnterResult{}
}

func (v *expandingCallFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// nestedParamFinder records which type parameters occur strictly below a type constructor in the
// type it walks. A parameter passed through as the whole argument, the `T` of `List<T>`, is at
// depth zero and leaves the argument the same size each lap. The same parameter under a
// constructor, the `T` of `Grow<{a: T}>`, makes the argument one constructor larger each lap.
type nestedParamFinder struct {
	params  map[*soltype.TypeVarType]string
	depth   int
	growing set.Set[string]
}

func (v *nestedParamFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if tv, ok := t.(*soltype.TypeVarType); ok {
		if name, isParam := v.params[tv]; isParam && v.depth > 0 {
			v.growing.Add(name)
		}
		return soltype.EnterResult{}
	}
	if growsItsOperands(t) {
		v.depth++
	}
	return soltype.EnterResult{}
}

func (v *nestedParamFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if growsItsOperands(t) {
		v.depth--
	}
	return t
}

// growsItsOperands reports whether a type makes its operands one constructor deeper. The two groups
// split on whether an operand comes out of the type larger than it went in.
//
// A growing type builds a strictly larger term from its operands. Wrapping `T` in `{a: T}`, `[T]`,
// `(T) -> T`, or `Box<T>` gives a term the recursion has to carry on every later lap.
//
// The rest hold their operands at the same depth. A union is a choice among its members, so `T` and
// `T | number` are the same size as far as growth goes. The type-level operators — `keyof`, indexed
// access, and the conditional — read a component out of their operand rather than wrapping it, so
// `T[K]` is no larger than `T`. Treating them as growth would reject
// `type DeepPartial<T> = {[K]?: DeepPartial<T[K]> for K in keyof T}`, which recurses on exactly
// that shape.
func growsItsOperands(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.FuncType, *soltype.RefType,
		*soltype.PromiseType, *soltype.TemplateLitType, *soltype.ClassType, *soltype.AliasType:
		return true
	default:
		return false
	}
}

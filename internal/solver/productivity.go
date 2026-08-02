package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A recursive type alias names a type only when its recursion is productive. Recursion is
// productive when every path back to the alias passes under a type constructor, so each lap emits
// one level of structure. `type List<T> = {head: T, tail?: List<T>}` emits a `{head, tail}` object
// every lap, and the infinite tree those laps build is the type `List` names.
//
// Recursion that returns to the alias emitting nothing names no type at all. Read
// `type Grow<T> = Grow<{a: T}>` as the equation `G(T) = G({a: T})`. Every constant function
// satisfies it, so the declaration pins down nothing. `type Bad = Bad` is the same shape with no
// type parameter.
//
// checkProductive is the definition-time diagnostic for the second shape. It is decidable, and it
// errs toward accepting. Every alias it rejects really does name no type, and it accepts some
// aliases whose reduction still fails to settle. `type Grow<T> = {[K]: Grow<{a: T}>[K] for K in
// keyof T}` emits an object every lap and passes, yet reducing one of its fields chases
// `Grow<…>["a"]` through instantiations that never repeat. maxExpandDepth and maxExpandKeyChars
// stay in place as the runtime backstop for those, and for a wide reduction that involves no
// recursion at all.
//
// Productivity is weaker than regularity, and the gap is what this check buys. A type is regular
// when it has finitely many distinct subtrees, so a finite knot represents it.
// `type Deep<T> = {a: Deep<{b: T}>}` is productive and not regular. It emits `{a: …}` every lap, so
// `Deep<number>` is a well-defined infinite tree, but its payloads are `{b: number}`, then
// `{b: {b: number}}`, and so on, all distinct. No finite normal form exists for it, so the
// evaluator cannot materialize it. constrain compares two such aliases by their canonical identity
// instead of unfolding them, which is what makes accepting them useful rather than only permissive.
// That identity keeps only the arguments the denoted type depends on — see markPhantomParams —
// which is why `Deep<number>` and `Deep<string>` compare equal rather than diverging.
//
// Mutual recursion goes through the alias reference graph. A recursive reference means a reference
// to any alias in the same strongly connected component, so `type A = B` paired with `type B = A`
// is caught even though neither body names itself.

// checkProductive reports every alias in shells that reaches itself without passing under a type
// constructor. shells are the aliases of one dep_graph component, which is already a strongly
// connected set of declarations, so no alias outside it can close a cycle with one inside it. Their
// bodies must be resolved before the check runs, since it reads AliasDef.Body.
func (c *checker) checkProductive(shells []*aliasShell) {
	if len(shells) == 0 {
		return
	}
	cycles := unguardedCycles(shells)
	for _, sh := range shells {
		cycle, unproductive := cycles[sh.qname]
		if !unproductive {
			continue
		}
		// Mark the alias so the evaluator declines to expand it. Expanding a lap of a
		// non-productive alias only hands the next lap another state to expand. Declining keeps
		// every later diagnostic naming the arguments the source wrote rather than ones an
		// expansion had grown.
		sh.def.NotProductive = true
		through := slices.DeleteFunc(cycle.ToSlice(), func(name string) bool { return name == sh.qname })
		slices.Sort(through)
		c.report(&NotProductiveAliasError{Decl: sh.decl, Name: sh.qname, Through: through})
	}
}

// unguardedCycles returns, for each alias that reaches itself through unguarded references, the
// names of the aliases on that cycle including itself. An alias reference is unguarded when no type
// constructor stands between it and the root of the body it was written in, so reaching it emits no
// structure. An alias with no unguarded cycle is absent from the result.
//
// The cycles are the strongly connected components of the graph whose vertices are the aliases in
// the dep_graph component and whose edges are the unguarded alias references in their bodies. A
// component of two or more aliases is a cycle. A component of one is a cycle only when that alias's
// body names itself unguarded.
//
// Everything below but the collector repeats aliasComponents in phantom.go, and #965 tracks lifting
// it into a shared helper. The two graphs stay distinct: that one records a reference at any depth,
// which would make a guarded recursion look unguarded and reject an alias that emits structure every
// lap.
func unguardedCycles(shells []*aliasShell) map[string]set.Set[string] {
	names := make([]string, 0, len(shells))
	for _, sh := range shells {
		names = append(names, sh.qname)
	}
	within := set.FromSlice(names)
	refs := map[string][]string{}
	for _, sh := range shells {
		collector := &unguardedRefCollector{within: within, found: set.NewSet[string]()}
		sh.def.Body.Accept(collector, soltype.Positive)
		out := collector.found.ToSlice()
		// Sort each alias's out-edges, and the seeds below, so the components come back in the same
		// order for the same input and a diagnostic never depends on map iteration order.
		slices.Sort(out)
		refs[sh.qname] = out
	}
	slices.Sort(names)

	cycles := map[string]set.Set[string]{}
	for _, component := range graph.StronglyConnectedComponents(names, func(n string) []string { return refs[n] }) {
		if len(component) == 1 && !slices.Contains(refs[component[0]], component[0]) {
			continue
		}
		cycle := set.FromSlice(component)
		for _, name := range component {
			cycles[name] = cycle
		}
	}
	return cycles
}

// unguardedRefCollector walks an alias body and records which of a fixed set of alias names it
// reaches with no type constructor in between. It rewrites nothing, so every node it visits comes
// back unchanged.
//
// guard counts the type constructors enclosing the node being visited. A reference found at guard
// zero emits nothing on its way around the recursion, which is the shape checkProductive rejects.
type unguardedRefCollector struct {
	within set.Set[string]
	found  set.Set[string]
	guard  int
}

func (v *unguardedRefCollector) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.AliasType:
		if v.guard == 0 && v.within.Contains(t.Name) {
			v.found.Add(t.Name)
		}
	case *soltype.CondType:
		// A conditional guards its two branches and nothing else. A branch is reached only when the
		// Check decided in its favor, so a later instantiation can decide the other way and stop the
		// recursion. That is how every recursive utility type terminates. `Awaited<T>` written as
		// `if T : Promise<infer U> { Awaited<U> } else { T }` stops at the first T that is not a
		// promise. Check and Extends offer no such escape, since every lap evaluates both, so
		// `type Bad = if Bad : number { number } else { string }` is caught the way `type Bad = Bad`
		// is.
		t.Check.Accept(v, pol)
		t.Extends.Accept(v, pol)
		v.guard++
		t.Then.Accept(v, pol)
		t.Else.Accept(v, pol)
		v.guard--
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ObjectType:
		// An object guards every member except a `...A` spread. A spread contributes its operand's
		// fields to the object rather than nesting them under one, so `type Grow<T> =
		// {...Grow<{a: T}>}` emits nothing per lap. Walking the members here, rather than letting
		// Accept walk them, is what lets a spread stay at the current guard while every other
		// member kind goes one level deeper.
		for _, elem := range t.Elems {
			if spread, ok := elem.(*soltype.SpreadElem); ok {
				spread.Type.Accept(v, pol)
				continue
			}
			v.guard++
			soltype.AcceptObjElem(elem, v, pol)
			v.guard--
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.TupleType:
		// The positional twin of the object arm. A `...P` element splices its operand's elements
		// into the tuple, so it guards nothing, while a positional element sits under the tuple.
		for _, elem := range t.Elems {
			if spread, ok := elem.(*soltype.RestSpreadType); ok {
				spread.Operand.Accept(v, pol)
				continue
			}
			v.guard++
			elem.Accept(v, pol)
			v.guard--
		}
		return soltype.EnterResult{SkipChildren: true}
	}
	if guardsEveryOperand(t) {
		v.guard++
	}
	return soltype.EnterResult{}
}

// ExitType lowers the guard the matching EnterType raised. The three kinds EnterType walks itself
// hand back a balanced count and return SkipChildren, so guardsEveryOperand excludes them and this
// never unwinds a level they already closed.
func (v *unguardedRefCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if guardsEveryOperand(t) {
		v.guard--
	}
	return t
}

// guardsEveryOperand reports whether a type wraps every one of its operands in one level of
// structure. Putting `T` in `(T) -> T`, `&T`, `Promise<T>`, `Point<T>`, or `Box<T>` emits a
// constructor the recursion then carries, so a lap through one has produced a level of the infinite
// tree the alias names. A template literal counts too, since interpolating `T` into one builds a
// string type around it.
//
// An object, a tuple, and a conditional guard only part of their structure. A spread member merges
// its operand in rather than nesting it, and a conditional's Check and Extends are evaluated on
// every lap. EnterType walks those three itself so it can raise the guard over the wrapped parts
// alone, which is why they are absent here.
//
// Every other kind emits nothing. A union or intersection is a choice among its members rather than
// a wrapper, so `type A<T> = {x: T} | A<{y: T}>` unfolds to an ever-widening union and settles on no
// type. The type-level operators — `keyof`, indexed access, the string intrinsics — read a
// component out of their operand instead of wrapping it, so a lap through one emits nothing either.
func guardsEveryOperand(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.FuncType, *soltype.RefType, *soltype.PromiseType, *soltype.ArrayType,
		*soltype.TemplateLitType, *soltype.ClassType, *soltype.AliasType:
		return true
	default:
		return false
	}
}

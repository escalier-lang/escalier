package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A type parameter is phantom when no argument passed to it can appear in the type the alias
// denotes. The opposite is a relevant parameter, one the denoted type does depend on.
//
// `type Deep<T> = {a: Deep<{b: T}>}` has a phantom T. Unfolding it once emits `{a: …}` and hands
// `{b: T}` to the next unfolding, so the payload is always one unfolding further in than the
// structure emitted so far and never lands in it. `Deep<number>` and `Deep<string>` are both the
// infinite type `{a: {a: {a: …}}}`, and TypeScript treats them as interchangeable.
//
// A parameter the body never mentions is phantom too. `type Ignore<T> = number` denotes `number`
// whatever T is.
//
// markPhantomParams records the phantom parameters on each AliasDef, and internAlias drops their
// arguments when it renders an alias reference's canonical identity. Two references that differ only
// in phantom arguments then intern to one representative, so constrain's reflexive rule settles
// `Deep<number> <: Deep<string>` in one step. Without the erasure the two sides unfold against each
// other, reach a pair no earlier unfolding reached every time, and hit maxUnwrapDepth.
//
// A relevant parameter keeps its argument, so the comparison still decides on it.
// `type Nest<T> = {here: T, deeper: Nest<{b: T}>}` writes T at `here`, so
// `Nest<number> <: Nest<string>` still reports the `number` against `string` mismatch.
//
// The erasure reaches no further than that identity key. The reference node keeps every argument the
// source wrote, so expandAlias substitutes them unchanged and a binding renders under them.

// markPhantomParams computes and stores the phantom parameters of every alias in shells, which are
// the aliases of one dep_graph component. Their bodies must be resolved first, since the walk reads
// AliasDef.Body.
//
// The rule is a fixed point over those bodies. Start with every parameter phantom, and mark one
// relevant when it occurs at a position the denoted type reaches. Every position in a body reaches
// it except the arguments of a reference back into the alias's own strongly connected component,
// which reach it only when the parameter they are passed to is itself relevant. Marking one
// parameter relevant can therefore open a position that marks another, so the walk repeats until a
// pass marks nothing new.
func markPhantomParams(shells []*aliasShell) {
	if len(shells) == 0 {
		return
	}
	sccOf := aliasComponents(shells)

	// phantom[qname][i] is true while parameter i of that alias is still believed unreachable.
	phantom := map[string][]bool{}
	for _, sh := range shells {
		marks := make([]bool, len(sh.def.TypeParams))
		for i := range marks {
			marks[i] = true
		}
		phantom[sh.qname] = marks
	}

	for {
		changed := false
		for _, sh := range shells {
			slots := map[*soltype.TypeVarType]int{}
			for i, p := range sh.def.TypeParams {
				if p.Var != nil {
					slots[p.Var] = i
				}
			}
			w := &phantomWalker{
				name:      sh.qname,
				scc:       sccOf[sh.qname],
				sccOf:     sccOf,
				slots:     slots,
				phantom:   phantom,
				reachable: true,
			}
			sh.def.Body.Accept(w, soltype.Positive)
			changed = changed || w.changed
		}
		if !changed {
			break
		}
	}

	for _, sh := range shells {
		sh.def.PhantomParams = phantom[sh.qname]
	}
}

// aliasComponents groups the component's aliases into the strongly connected components of the
// graph whose vertices are those aliases and whose edges are the alias references in their bodies,
// at any depth. It returns each alias's component number.
//
// Only a reference whose target shares the referring alias's component can close a recursion, and
// only such a reference gates its arguments behind a parameter. A reference to an alias in another
// component expands finitely, so an argument reaching it reaches the denoted type.
func aliasComponents(shells []*aliasShell) map[string]int {
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
		slices.Sort(out)
		refs[sh.qname] = out
	}
	slices.Sort(names)

	sccOf := map[string]int{}
	for i, component := range graph.StronglyConnectedComponents(names, func(n string) []string { return refs[n] }) {
		for _, name := range component {
			sccOf[name] = i
		}
	}
	return sccOf
}

// aliasRefCollector walks an alias body and records which of a fixed set of alias names it
// references, at any depth and in any position. It rewrites nothing, so every node it visits comes
// back unchanged. It is the unguarded-reference collector's counterpart in productivity.go, which
// records only the references no type constructor encloses.
type aliasRefCollector struct {
	within set.Set[string]
	found  set.Set[string]
}

func (v *aliasRefCollector) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if at, ok := t.(*soltype.AliasType); ok && v.within.Contains(at.Name) {
		v.found.Add(at.Name)
	}
	return soltype.EnterResult{}
}

func (v *aliasRefCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// phantomWalker walks one alias body and clears a phantom mark for each of the alias's own type
// parameters that it finds at a position the type the alias denotes reaches. It rewrites nothing, so
// every node it visits comes back unchanged.
type phantomWalker struct {
	// name is the walked alias's qualified name, the key its own marks live under in phantom.
	name string
	// scc is the walked alias's component number, so a reference into that same component is
	// recognized as one that can close a recursion.
	scc   int
	sccOf map[string]int
	// slots maps each of the walked alias's type-parameter variables to its declaration position.
	// The body holds those variables symbolically, so an occurrence is found by pointer identity.
	slots map[*soltype.TypeVarType]int
	// phantom is the marks of every alias in the component, shared across the fixed point's
	// passes. The walker reads other aliases' marks to decide whether an argument slot reaches the
	// denoted type, and clears its own.
	phantom map[string][]bool
	// reachable is true while the node being visited sits at a position the type an instantiation
	// denotes reaches. It starts true at the body root and goes false inside an argument handed to a
	// phantom parameter of a same-component reference.
	reachable bool
	// changed records whether this pass cleared any mark, which is what the fixed point iterates on.
	changed bool
}

func (w *phantomWalker) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if !w.reachable {
			return soltype.EnterResult{}
		}
		marks := w.phantom[w.name]
		if i, own := w.slots[t]; own && i < len(marks) && marks[i] {
			marks[i] = false
			w.changed = true
		}
	case *soltype.AliasType:
		if id, known := w.sccOf[t.Name]; !known || id != w.scc {
			// A reference out of the component expands finitely, so its arguments carry the
			// current reachability rather than being gated behind a parameter.
			return soltype.EnterResult{}
		}
		// A reference back into the component hands each argument to one parameter, so the argument
		// reaches this alias's denoted type only when that parameter reaches the referenced alias's.
		// Walking the arguments here, rather than letting Accept walk them, is what lets each one
		// carry its own reachability.
		target := w.phantom[t.Name]
		outer := w.reachable
		for i, arg := range t.TypeArgs {
			// An argument past the referenced alias's parameter list is an arity mismatch already
			// reported at the reference. Read it as reaching the denoted type, the conservative
			// answer, rather than as phantom.
			gated := i < len(target) && target[i]
			w.reachable = outer && !gated
			arg.Accept(w, pol)
		}
		w.reachable = outer
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (w *phantomWalker) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// erasePhantomArgs rewrites t, dropping from every alias reference in it the arguments its alias's
// phantom parameters would receive. internAlias renders the result to build a canonical identity
// key, so two references differing only in erased arguments produce the same key. The rewrite
// reaches nested references too, so `Nest<Deep<number>>` and `Nest<Deep<string>>` both render as
// `Nest<Deep>` even though Nest's own parameter is relevant.
//
// The result is a rendering input, not a type to check or expand. It returns t itself when no
// reference in it has a phantom argument to drop, which is the common case.
func (c *Context) erasePhantomArgs(t soltype.Type) soltype.Type {
	return t.Accept(&phantomEraser{ctx: c}, soltype.Positive)
}

type phantomEraser struct {
	ctx *Context
}

func (e *phantomEraser) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	return soltype.EnterResult{}
}

// ExitType drops the phantom arguments of an alias reference. It runs bottom-up on already-rewritten
// children, so an argument that survives has had its own nested references erased.
func (e *phantomEraser) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	at, ok := t.(*soltype.AliasType)
	if !ok || len(at.TypeArgs) == 0 {
		return t
	}
	// Most aliases have nothing phantom, so answer those without allocating.
	def, registered := e.ctx.aliasDef(at.Name)
	if !registered || !slices.Contains(def.PhantomParams, true) {
		return t
	}
	kept := make([]soltype.Type, 0, len(at.TypeArgs))
	for i, arg := range at.TypeArgs {
		if i < len(def.PhantomParams) && def.PhantomParams[i] {
			continue
		}
		kept = append(kept, arg)
	}
	if len(kept) == len(at.TypeArgs) {
		// An arity mismatch left fewer arguments than parameters, so no phantom slot was
		// actually filled. Keep the node rather than allocating a copy of it.
		return t
	}
	return &soltype.AliasType{Name: at.Name, TypeArgs: kept, LifetimeArgs: at.LifetimeArgs}
}

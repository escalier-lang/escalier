package solver

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A type parameter is phantom when no argument passed to it can appear in the type the alias
// denotes. `type Deep<T> = {a: Deep<{b: T}>}` pushes `{b: T}` one unfolding deeper forever, so
// `Deep<number>` and `Deep<string>` are both the infinite type `{a: {a: …}}`.

// markPhantomParams marks the aliases of one dep_graph component, whose bodies must already be
// resolved, so internAlias can drop a phantom argument from a reference's identity key. It is a
// fixed point: a parameter starts phantom and turns relevant where it occurs, except inside the
// arguments of a same-component reference, which reach the type only if the parameter they fill
// does.
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
//
// Everything below but the collector repeats unguardedCycles in productivity.go, and #965 tracks
// lifting it into a shared helper. The two graphs stay distinct: that one records a reference only
// where no type constructor encloses it, so `type Deep<T> = {a: Deep<{b: T}>}` gives it no edge at
// all and reading it here would leave every parameter relevant.
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

// reportPhantomParams warns about each alias type parameter whose argument a caller gains
// nothing by writing. It runs beside markPhantomParams, after every body in the dep_graph
// component is resolved, so the marks it reads are final.
//
// A parameter is *mentioned* when its var occurs in the alias body, in a sibling
// parameter's bound, or in a sibling parameter's default. Its own bound does not count,
// since the `number` of `<T: number>` constrains T rather than using it. A parameter no
// position mentions is unused. A parameter the body mentions is unreachable when it is also
// marked phantom, which means no argument passed to it lands in the type an instantiation
// denotes.
//
// The marks alone cannot answer this. markPhantomParams asks whether an argument reaches
// the denoted type through the parameter's own slot, and two shapes come back phantom while
// the parameter is still doing work. `type Foo<T, U: T> = {x: U}` marks T phantom, and T
// bounds U. `type Pair<T, U = T> = {b: U}` marks T phantom, and `Pair<number>` denotes
// `{b: number}`. Erasing T is right in both, since the argument reaches the denoted type
// through U's slot rather than T's, and warning about either would be a false positive. The
// sibling-mention rule is what excludes them.
func (c *checker) reportPhantomParams(shells []*aliasShell) {
	for _, sh := range shells {
		if !sh.bodyClean || sh.def.NotProductive {
			// Two shapes report nothing. A body that drew a diagnostic is a partial record of
			// what the source wrote, so a parameter it dropped would read as unused. An alias
			// checkProductive rejected denotes no type at all, so no parameter can be
			// unreachable in it. Both already carry a diagnostic to act on.
			continue
		}
		params := sh.def.TypeParams
		if len(params) == 0 {
			continue
		}
		inBody, inSibling := typeParamMentions(params, func(v soltype.TypeVisitor) {
			sh.def.Body.Accept(v, soltype.Positive)
		})
		for i, p := range params {
			if strings.HasPrefix(p.Name, "_") || inSibling[i] {
				continue
			}
			decl := sh.decl.TypeParams[i]
			if !inBody[i] {
				c.report(&UnusedTypeParamError{Name: p.Name, Param: decl})
				continue
			}
			if i < len(sh.def.PhantomParams) && sh.def.PhantomParams[i] {
				names := make([]string, len(params))
				for k, q := range params {
					names[k] = q.Name
				}
				c.report(&UnreachableTypeParamError{
					Alias:  sh.qname,
					Params: names,
					Index:  i,
					Param:  decl,
				})
			}
		}
	}
}

// reportUnusedTypeParams warns about each type parameter of a class or enum that the
// declaration never mentions, the unused tier of the alias warning applied to the two
// nominal sorts. walkBody visits every type the declaration writes, and decls are the
// binders the warnings blame, one per entry of params.
//
// Only the unused tier extends here. A nominal handle carries its arguments into its
// identity and constrain compares them position by position, so an argument to a parameter
// the body does write is observable however deep the recursion drives it. `class Nest<T>
// {deeper: Nest<{b: T}>}` reports a mismatch between `Nest<number>` and `Nest<string>`
// where the alias of the same shape settles them as one type. Unreachability is a property
// of a transparent alias, and there is no nominal counterpart to warn about.
func (c *checker) reportUnusedTypeParams(
	params []*soltype.TypeParam,
	decls []*ast.TypeParam,
	walkBody func(soltype.TypeVisitor),
) {
	if len(params) == 0 || len(decls) < len(params) {
		return
	}
	mentioned, inSibling := typeParamMentions(params, walkBody)
	for i, p := range params {
		if strings.HasPrefix(p.Name, "_") || mentioned[i] || inSibling[i] {
			continue
		}
		c.report(&UnusedTypeParamError{Name: p.Name, Param: decls[i]})
	}
}

// typeParamMentions reports where each of a declaration's type parameters occurs. inBody is
// true for a parameter whose var walkBody reaches, and inSibling is true for one that occurs
// in another parameter's bound or default. A parameter's own binder does not count, so the T
// an F-bound `<T: Foo<T>>` writes in its own bound is not a use of T.
//
// The two answers stay separate because they carry different weight. A parameter no body
// mentions is unused whatever its bound says, while a parameter a sibling's bound or default
// mentions is doing work the body cannot show. `type Foo<T, U: T> = {x: U}` writes T nowhere
// in its body, and T still decides which arguments U accepts.
func typeParamMentions(params []*soltype.TypeParam, walkBody func(soltype.TypeVisitor)) (inBody, inSibling []bool) {
	slots := map[*soltype.TypeVarType]int{}
	for i, p := range params {
		if p.Var != nil {
			slots[p.Var] = i
		}
	}
	inBody = make([]bool, len(params))
	walkBody(&paramOccurrenceWalker{slots: slots, found: inBody})

	inSibling = make([]bool, len(params))
	onBinder := make([]bool, len(params))
	for j, p := range params {
		clear(onBinder)
		for _, t := range []soltype.Type{p.Constraint, p.Default} {
			if t != nil {
				markOccurrences(slots, t, onBinder)
			}
		}
		onBinder[j] = false
		for i, occurs := range onBinder {
			inSibling[i] = inSibling[i] || occurs
		}
	}
	return inBody, inSibling
}

// markOccurrences sets found[i] for each of the declaration's own type parameters whose var
// occurs anywhere in t, leaving the entries it does not reach alone so a caller may fold
// several types into one slice. slots maps each parameter's var to its position, and found is
// one entry per parameter. A stored body and bound hold those vars symbolically, so an
// occurrence is found by pointer identity.
//
// Position and reachability play no part, unlike in the phantom marks. `type Deep<T> =
// {a: Deep<{b: T}>}` mentions T, even though no argument passed to T reaches the type Deep
// denotes.
func markOccurrences(slots map[*soltype.TypeVarType]int, t soltype.Type, found []bool) {
	t.Accept(&paramOccurrenceWalker{slots: slots, found: found}, soltype.Positive)
}

// paramOccurrenceWalker records which of a fixed set of type variables a type mentions. It
// rewrites nothing, so every node it visits comes back unchanged. A TypeVarType is a leaf to
// the visitor, so reaching one records it without descending into the bounds solving has
// accumulated on it.
type paramOccurrenceWalker struct {
	slots map[*soltype.TypeVarType]int
	found []bool
}

func (w *paramOccurrenceWalker) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if tv, ok := t.(*soltype.TypeVarType); ok {
		if i, own := w.slots[tv]; own {
			w.found[i] = true
		}
	}
	return soltype.EnterResult{}
}

func (w *paramOccurrenceWalker) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// erasePhantomArgs drops from every alias reference in t the arguments its phantom parameters
// receive, nested ones included, so internAlias renders two references differing only there to one
// key. It is a rendering input, not a type to check or expand, and returns t when nothing drops.
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

package solver

import (
	"maps"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
)

// ambient.go binds the builtins a program reaches without an import, such as
// `Math.PI`, `console.log`, `parseInt`, and `Error`.
//
// The ambient scope sits between the prelude scope and a module's own scope, so a
// module's declaration and a file's import both shadow an ambient name. It is a
// compatibility surface over the packages ambientPackages lists. Importing one of
// them still works, and binds the package as a namespace the way any import does.

// ambientPackages lists the packages whose exports the ambient scope binds. The
// first package to claim a name keeps it, so a later package never replaces an
// earlier one's binding.
var ambientPackages = []string{
	"std:async",
	"std:bigint",
	"std:boolean",
	"std:console",
	"std:date",
	"std:decorators",
	"std:disposable",
	"std:error",
	"std:function",
	"std:intl",
	"std:iterator",
	"std:json",
	"std:map",
	"std:math",
	"std:number",
	"std:object",
	"std:proxy",
	"std:reflect",
	"std:regexp",
	"std:set",
	"std:string",
	"std:typed_arrays",
	"std:url",
	"std:weak_ref",
}

// ambientScope returns the per-run scope that holds the ambient builtins, creating
// it empty as a child of the prelude scope on the first request and returning the
// same scope on every later one. bindAmbientExports fills it.
func (c *checker) ambientScope() *Scope {
	if c.ambient == nil {
		c.ambient = c.preludeScope().Child()
	}
	return c.ambient
}

// bindAmbientExports loads each package in ambientPackages and binds its exports
// into the ambient scope by three rules:
//
//  1. A type-only export binds under its declared name. `interface Console` binds
//     `Console`.
//  2. A value export whose `@js` path has one segment binds under that path.
//     `@js("parseInt")` binds `parseInt`.
//  3. A value export whose `@js` path is dotted binds its last segment into the
//     namespace the prefix names, created on first use. `@js("Math.clz32")` binds
//     `clz32` into a namespace `Math`.
//
// A type that shares its name with a value export, as a class's does, binds beside
// that value. `@js("Intl.Collator") class Collator` binds both the value and the
// type `Collator` into `Intl`.
//
// A package the source does not answer for binds nothing and reports nothing. A
// package that loads and reports diagnostics of its own reaches the run with them.
// The first package to claim a name keeps it.
func (c *checker) bindAmbientExports() {
	// The packages draw from the range the prelude draws from, so a program's own
	// variables and symbols are numbered the same whether or not any builtin loaded.
	programVars, programSymbols := c.ctx.varCounter, c.ctx.symbolCounter
	c.ctx.varCounter, c.ctx.symbolCounter = c.libVarCounter, c.libSymbolCounter
	defer func() {
		c.libVarCounter, c.libSymbolCounter = c.ctx.varCounter, c.ctx.symbolCounter
		c.ctx.varCounter, c.ctx.symbolCounter = programVars, programSymbols
	}()

	b := &ambientBinder{scope: c.ambientScope(), namespaces: map[string]*Namespace{}}
	for _, uri := range ambientPackages {
		ns, errs := c.loadPackage(uri, ast.Span{})
		// A run against a tree that lacks the package, such as a test's partial tree,
		// goes on without its names.
		if ns == nil {
			continue
		}
		for _, err := range errs {
			c.report(err)
		}
		b.bindPackage(ns)
	}
}

// ambientBinder fills one ambient scope. namespaces maps each dotted `@js` prefix
// to the namespace rule 3 created for it, keyed by the whole prefix.
type ambientBinder struct {
	scope      *Scope
	namespaces map[string]*Namespace
}

// bindPackage binds every export of ns by the rules bindAmbientExports lists.
// Names are visited in sorted order so a run does not depend on map iteration.
func (b *ambientBinder) bindPackage(ns *Namespace) {
	besideValue := set.NewSet[string]()
	for _, name := range slices.Sorted(maps.Keys(ns.Values)) {
		vb := ns.Values[name]
		path, ok := jsPath(vb.Sources)
		if !ok {
			continue
		}
		segments := strings.Split(path, ".")
		leaf := segments[len(segments)-1]
		values, types := b.target(segments[:len(segments)-1])
		tb, hasType := ns.Types[name]
		if hasType {
			besideValue.Add(name)
		}
		// A class's value and type come from one declaration, so a package whose value
		// lost the name to an earlier package does not bind its type there either.
		if _, taken := values[leaf]; taken {
			continue
		}
		values[leaf] = vb
		if _, taken := types[leaf]; hasType && !taken {
			types[leaf] = tb
		}
	}
	for _, name := range slices.Sorted(maps.Keys(ns.Types)) {
		if besideValue.Contains(name) {
			continue
		}
		if _, taken := b.scope.types[name]; !taken {
			b.scope.types[name] = ns.Types[name]
		}
	}
}

// target returns the value and type maps a binding under prefix goes into. An empty
// prefix is the ambient scope itself. Any other prefix is a namespace, created on
// first use and nested one level per segment, so `A.B` lives in `B` inside `A`.
func (b *ambientBinder) target(prefix []string) (map[string]ValueBinding, map[string]TypeBinding) {
	if len(prefix) == 0 {
		return b.scope.values, b.scope.types
	}
	var parent *Namespace
	for i, segment := range prefix {
		key := strings.Join(prefix[:i+1], ".")
		ns, held := b.namespaces[key]
		if !held {
			ns = &Namespace{
				Name:   key,
				Values: map[string]ValueBinding{},
				Types:  map[string]TypeBinding{},
				Nested: map[string]*Namespace{},
			}
			b.namespaces[key] = ns
			if parent == nil {
				b.scope.defineNamespace(segment, ns)
			} else {
				parent.Nested[segment] = ns
			}
		}
		parent = ns
	}
	return parent.Values, parent.Types
}

// jsPath returns the `@js` path on the first declaration in sources that carries
// one, and false when none does.
func jsPath(sources []provenance.Provenance) (string, bool) {
	for _, src := range sources {
		node, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		decl, ok := node.Node.(ast.Decl)
		if !ok {
			continue
		}
		if _, path, ok := ast.FindJsDecorator(decl); ok {
			return path, true
		}
	}
	return "", false
}

package solver

import (
	"maps"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
)

// ambient.go binds the globals scope, the scope an npm package's `.d.ts` is checked
// against. It stands in for TypeScript's global library, so it holds names such as
// `Math.PI`, `console.log`, `parseInt`, and `Error`.
//
// A project's own files never read the globals scope. A module or script reaches a
// builtin through the prelude or through an import, such as `import "std:console"`
// for `console.log`.

// globalsPackages lists the packages whose exports the globals scope binds. The
// first package to claim a name keeps it, so a later package never replaces an
// earlier one's binding.
var globalsPackages = []string{
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
	"web:core",
	"web:fetch",
}

// globalsScope returns the per-run scope that holds the exports of every package in
// globalsPackages, creating it empty as a child of the prelude scope on the first
// request and returning the same scope on every later one. bindGlobals fills it.
func (c *checker) globalsScope() *Scope {
	if c.globals == nil {
		c.globals = c.preludeScope().Child()
		c.globals.builtin = true
	}
	return c.globals
}

// loadLiteralClassPackages loads the packages literalClassPackages lists and binds
// none of their exports. A run loads them once, and a later call returns without
// loading anything.
func (c *checker) loadLiteralClassPackages() {
	if c.literalClassesLoaded {
		return
	}
	c.literalClassesLoaded = true
	c.loadLibraryPackages(literalClassPackages(), nil)
}

// bindGlobals loads each package in globalsPackages and binds its exports into the
// globals scope by three rules:
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
// package that loads with diagnostics of its own has them held back by
// holdLoadErrors. The first package to claim a name keeps it.
//
// A run binds the globals scope once. A later call returns without loading anything.
func (c *checker) bindGlobals() {
	if c.globalsBound {
		return
	}
	c.globalsBound = true
	c.loadLibraryPackages(globalsPackages, &globalsBinder{scope: c.globalsScope(), namespaces: map[string]*Namespace{}})
}

// loadLibraryPackages loads each package uris names, holds its diagnostics back with
// holdLoadErrors, and binds its exports through b when b is non-nil. A package the
// source does not answer for is skipped.
func (c *checker) loadLibraryPackages(uris []string, b *globalsBinder) {
	// The packages draw from the range the prelude draws from, so a program's own
	// variables and symbols are numbered the same whether or not any builtin loaded.
	programVars, programSymbols := c.ctx.varCounter, c.ctx.symbolCounter
	c.ctx.varCounter, c.ctx.symbolCounter = c.libVarCounter, c.libSymbolCounter
	defer func() {
		c.libVarCounter, c.libSymbolCounter = c.ctx.varCounter, c.ctx.symbolCounter
		c.ctx.varCounter, c.ctx.symbolCounter = programVars, programSymbols
	}()

	for _, uri := range uris {
		ns, errs := c.loadPackage(uri, ast.Span{})
		// A run against a tree that lacks the package, such as a test's partial tree,
		// goes on without its names.
		if ns == nil {
			continue
		}
		c.holdLoadErrors(uri, errs)
		if b != nil {
			b.bindPackage(ns)
		}
	}
}

// heldLoadErrors is the diagnostics one library load returned, shared by every member
// of the group that load inferred.
type heldLoadErrors struct {
	errs     []SolverError
	reported bool
}

// holdLoadErrors records the diagnostics loading uri returned, under uri and every
// package in its group, instead of reporting them. loadPackage reports them at the
// first import that names one of those packages, and reportHeldLoadErrors reports
// the ones no import named.
func (c *checker) holdLoadErrors(uri string, errs []SolverError) {
	if len(errs) == 0 {
		return
	}
	held := &heldLoadErrors{errs: errs}
	c.heldLoadOrder = append(c.heldLoadOrder, held)
	members, grouped := c.groups.GroupOf(uri)
	if !grouped {
		members = []string{uri}
	}
	if c.heldLoadErrs == nil {
		c.heldLoadErrs = map[string]*heldLoadErrors{}
	}
	for _, member := range members {
		c.heldLoadErrs[member] = held
	}
}

// claimHeldLoadErrors returns the diagnostics held for uri, placed at span, and marks
// them reported. It returns nil when nothing is held or they were already reported.
func (c *checker) claimHeldLoadErrors(uri string, span ast.Span) []SolverError {
	held := c.heldLoadErrs[uri]
	if held == nil || held.reported {
		return nil
	}
	held.reported = true
	errs := make([]SolverError, len(held.errs))
	for i, err := range held.errs {
		errs[i] = withLoadSpan(err, span)
	}
	return errs
}

// withLoadSpan returns err placed at span when err is one of the diagnostics a
// package load places at the import that loads it, and err itself otherwise.
func withLoadSpan(err SolverError, span ast.Span) SolverError {
	switch e := err.(type) {
	case *PackageInferenceError:
		placed := *e
		placed.span = span
		return &placed
	case *PseudoPackageDeclError:
		placed := *e
		placed.span = span
		return &placed
	default:
		return err
	}
}

// reportHeldLoadErrors reports, in load order, the held diagnostics no import claimed.
func (c *checker) reportHeldLoadErrors() {
	for _, held := range c.heldLoadOrder {
		if held.reported {
			continue
		}
		held.reported = true
		for _, err := range held.errs {
			c.report(err)
		}
	}
}

// literalClassPackages returns, sorted, the packages that declare the classes a literal's
// value reads through. Those are each primitive's wrapper, such as `String` for `"ab"`, and
// `RegExp` for a regex literal. The prelude is left out, since every run loads it first.
func literalClassPackages() []string {
	uris := set.FromSlice([]string{regexpClass.uri})
	for _, wrapper := range primitiveWrappers {
		if wrapper.uri != preludeURI {
			uris.Add(wrapper.uri)
		}
	}
	sorted := uris.ToSlice()
	slices.Sort(sorted)
	return sorted
}

// globalsBinder fills the globals scope. namespaces maps each dotted `@js` prefix
// to the namespace rule 3 created for it, keyed by the whole prefix.
type globalsBinder struct {
	scope      *Scope
	namespaces map[string]*Namespace
}

// bindPackage binds every export of ns by the rules bindGlobals lists.
// Names are visited in sorted order so a run does not depend on map iteration.
func (b *globalsBinder) bindPackage(ns *Namespace) {
	// boundWithValue holds the names of this package's types that share a name with
	// one of its `@js` values. The first loop binds such a type next to its value, so
	// `@js("Intl.Collator") class Collator` puts the type `Collator` in `Intl`. It
	// leaves the type unbound when an earlier package claimed the value. The second
	// loop skips these names, which keeps the type from also binding at the top level.
	boundWithValue := set.NewSet[string]()
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
			boundWithValue.Add(name)
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
		// `std:iterator` declares an interface under each built-in type name, and binding
		// it would shadow the form resolveTypeAnn builds for that name.
		if boundWithValue.Contains(name) || builtinTypeNames.Contains(name) {
			continue
		}
		if _, taken := b.scope.types[name]; !taken {
			b.scope.types[name] = ns.Types[name]
		}
	}
}

// target returns the value and type maps a binding under prefix goes into. An empty
// prefix is the globals scope itself. Any other prefix is a namespace, created on
// first use and nested one level per segment, so `A.B` lives in `B` inside `A`.
func (b *globalsBinder) target(prefix []string) (map[string]ValueBinding, map[string]TypeBinding) {
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

package solver

import (
	"maps"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
)

// ambient.go binds the names a scope reaches without an import.
//
// Two layers sit above the prelude scope:
//
//  1. The web prelude scope holds the exports of `web:prelude`, such as `Event`
//     and `AbortSignal`. It is the ambient scope of a project's own files and of
//     every `web:*` package but `web:prelude` itself, so in a project's own file
//     the `*:prelude` packages are the whole ambient layer.
//  2. The globals scope is a child of the web prelude scope and holds the exports
//     of every package ambientPackages lists. An npm package's scope is its child,
//     because a `.d.ts` is written against TypeScript's global library and names
//     `Console`, `Blob` or `HTMLElement` without importing them.

// webPreludeURI is the web platform's prelude.
const webPreludeURI = "web:prelude"

// ambientPackages lists the packages whose exports the globals scope binds. The
// first package to claim a name keeps it, so a later package never replaces an
// earlier one's binding.
//
// `web:dom` is left out while it loads with errors of its own, which #2377 tracks.
// A package listed here loads in every run that imports an npm package, and the
// errors it reports would land in each of those runs.
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
	"web:fetch",
	"web:file",
	"web:streams",
	"web:url",
}

// webPreludeScope returns the per-run scope that holds the exports of
// `web:prelude`, a child of the prelude scope. The first call loads the package
// and binds its exports, and every later call returns the same scope.
func (c *checker) webPreludeScope() *Scope {
	if c.webPrelude != nil {
		return c.webPrelude
	}
	c.webPrelude = c.preludeScope().Child()
	c.webPrelude.builtin = true
	c.loadLibraryPackages([]string{webPreludeURI}, &ambientBinder{scope: c.webPrelude, namespaces: map[string]*Namespace{}})
	return c.webPrelude
}

// packageParentScope returns the scope a pseudo-package group's scope is a child
// of. A group holding a `web:*` package other than `web:prelude` reaches the web
// prelude's exports without an import. Every other group reaches the prelude's
// alone.
func (c *checker) packageParentScope(group []string) *Scope {
	if slices.Contains(group, webPreludeURI) {
		return c.preludeScope()
	}
	for _, uri := range group {
		if strings.HasPrefix(uri, "web:") {
			return c.webPreludeScope()
		}
	}
	return c.preludeScope()
}

// globalsScope returns the per-run scope that holds the exports of every package in
// ambientPackages, creating it empty as a child of the web prelude scope on the
// first request and returning the same scope on every later one. bindGlobals fills
// it.
func (c *checker) globalsScope() *Scope {
	if c.globals == nil {
		c.globals = c.webPreludeScope().Child()
		c.globals.builtin = true
	}
	return c.globals
}

// ambientScope returns the scope a project's own module or script is a child of,
// which is the web prelude scope.
func (c *checker) ambientScope() *Scope {
	return c.webPreludeScope()
}

// bindAmbientExports loads the packages literalClassPackages lists, so a literal's
// members resolve with no import, and binds none of their names. A run loads them
// once, and a later call returns without loading anything.
func (c *checker) bindAmbientExports() {
	if c.ambientBound {
		return
	}
	c.ambientBound = true
	c.loadLibraryPackages(literalClassPackages(), nil)
}

// bindGlobals loads each package in ambientPackages and binds its exports into the
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
	c.loadLibraryPackages(ambientPackages, &ambientBinder{scope: c.globalsScope(), namespaces: map[string]*Namespace{}})
}

// loadLibraryPackages loads each package uris names, holds its diagnostics back with
// holdLoadErrors, and binds its exports through b when b is non-nil. A package the
// source does not answer for is skipped.
func (c *checker) loadLibraryPackages(uris []string, b *ambientBinder) {
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

// heldLoadErrors is the diagnostics one ambient load returned, shared by every member
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
// value reads through. Those are each primitive's wrapper, such as `String` for `"ab"`,
// `RegExp` for a regex literal, and `Function` for a function. The prelude is left out,
// since every run loads it first.
func literalClassPackages() []string {
	uris := set.FromSlice([]string{regexpClass.uri, functionClass.uri})
	for _, wrapper := range primitiveWrappers {
		if wrapper.uri != preludeURI {
			uris.Add(wrapper.uri)
		}
	}
	sorted := uris.ToSlice()
	slices.Sort(sorted)
	return sorted
}

// ambientBinder fills one ambient scope. namespaces maps each dotted `@js` prefix
// to the namespace rule 3 created for it, keyed by the whole prefix.
type ambientBinder struct {
	scope      *Scope
	namespaces map[string]*Namespace
}

// bindPackage binds every export of ns by the rules bindGlobals lists.
// Names are visited in sorted order so a run does not depend on map iteration.
func (b *ambientBinder) bindPackage(ns *Namespace) {
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

package checker

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/tidwall/btree"
)

// nextSourceID is an atomic counter for generating unique source IDs
var nextSourceID atomic.Int64

// referenceDirectivePattern matches /// <reference lib="es2015.core" /> directives
// Compiled once at package level for efficiency.
var referenceDirectivePattern = regexp.MustCompile(`/// <reference lib="([^"]+)" />`)

// pathReferenceDirectivePattern matches /// <reference path="global.d.ts" /> directives
// These reference other .d.ts files relative to the current file.
var pathReferenceDirectivePattern = regexp.MustCompile(`/// <reference path="([^"]+)" />`)

// findRepoRoot walks up the directory tree to find the Escalier project root
// by looking for an escalier.toml file.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		tomlPath := filepath.Join(dir, "escalier.toml")
		if _, err := os.Lstat(tomlPath); err == nil {
			return dir, nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// discoverESLibFiles returns ES lib files for the given target version,
// sorted in dependency order by recursively following reference directives.
//
// The targetVersion parameter specifies which ES version to load:
// - "es5" - only lib.es5.d.ts
// - "es2015" - ES5 + ES2015 files (via references in lib.es2015.d.ts)
// - "es2016" - ES5 + ES2015 + ES2016 files
// - etc.
//
// The function starts with lib.<targetVersion>.d.ts and recursively follows
// /// <reference lib="..." /> directives, returning files in dependency order
// (dependencies before the files that reference them).
func discoverESLibFiles(libDir string, targetVersion string) ([]string, error) {
	visited := make(map[string]bool)
	return loadLibFilesRecursive(libDir, targetVersion, visited)
}

// parseReferenceDirectives extracts lib references from a .d.ts file.
// Example: /// <reference lib="es2015.core" /> -> "es2015.core"
func parseReferenceDirectives(filePath string) ([]string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	matches := referenceDirectivePattern.FindAllStringSubmatch(string(content), -1)
	var refs []string
	for _, match := range matches {
		if len(match) >= 2 {
			refs = append(refs, match[1])
		}
	}
	return refs, nil
}

// parsePathReferenceDirectives extracts path references from a .d.ts file.
// Example: /// <reference path="global.d.ts" /> -> "global.d.ts"
func parsePathReferenceDirectives(filePath string) ([]string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	return parsePathRefsFromContent(string(content)), nil
}

// parsePathRefsFromContent extracts path reference directives from file content.
// This is used by loadClassifiedTypeScriptModule to avoid reading the file twice.
func parsePathRefsFromContent(content string) []string {
	matches := pathReferenceDirectivePattern.FindAllStringSubmatch(content, -1)
	var refs []string
	for _, match := range matches {
		if len(match) >= 2 {
			refs = append(refs, match[1])
		}
	}
	return refs
}

// loadLibFilesRecursive loads a lib file and all its references recursively.
// Returns filenames in dependency order (dependencies first).
func loadLibFilesRecursive(libDir string, libName string, visited map[string]bool) ([]string, error) {
	filename := "lib." + libName + ".d.ts"
	if visited[filename] {
		return nil, nil // Already processed
	}
	visited[filename] = true

	filePath := filepath.Join(libDir, filename)
	refs, err := parseReferenceDirectives(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", filename, err)
	}

	var result []string

	// Process dependencies first (depth-first)
	for _, ref := range refs {
		refFilename := "lib." + ref + ".d.ts"
		// Skip unstable ESNext features
		if isESNextFile(refFilename) {
			continue
		}
		// Only follow ES-related references (es5, es2015, es2016, etc.)
		// Skip non-ES references like decorators, scripthost, webworker, etc.
		if !isESLibReference(ref) {
			continue
		}
		refFiles, err := loadLibFilesRecursive(libDir, ref, visited)
		if err != nil {
			return nil, err
		}
		result = append(result, refFiles...)
	}

	// Add this file after its dependencies
	result = append(result, filename)

	return result, nil
}

// isESLibReference returns true if the reference is an ES-related lib (es5, es2015.core, etc.)
// This filters out non-ES libs like decorators, scripthost, webworker, dom, etc.
func isESLibReference(ref string) bool {
	return strings.HasPrefix(ref, "es")
}

// isESNextFile returns true for lib.esnext.*.d.ts files (unstable features)
func isESNextFile(name string) bool {
	return strings.HasPrefix(name, "lib.esnext")
}

// filterLibFileErrors filters out expected errors that can occur during lib file loading.
// InvalidObjectKeyError can occur for computed keys like [Symbol.iterator] when
// the symbol property isn't available yet during processing. These are expected
// and should not cause lib file loading to fail.
func filterLibFileErrors(errors []Error) []Error {
	var fatalErrors []Error
	for _, err := range errors {
		switch err.(type) {
		case *InvalidObjectKeyError:
			// Skip - this is expected during ES2015+ lib loading
			continue
		default:
			fatalErrors = append(fatalErrors, err)
		}
	}
	return fatalErrors
}

// mergeModules merges the source module into the target module.
// All fields (Namespaces, Files, Sources) are combined. This is used to
// combine multiple lib files into a single module with a unified dependency graph.
func mergeModules(target, source *ast.Module) {
	if source == nil {
		return
	}

	// Merge namespaces
	iter := source.Namespaces.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		name := iter.Key()
		ns := iter.Value()
		if existingNs, exists := target.Namespaces.Get(name); exists {
			// Merge declarations within the namespace
			existingNs.Decls = append(existingNs.Decls, ns.Decls...)
		} else {
			target.Namespaces.Set(name, ns)
		}
	}

	// Merge files
	target.Files = append(target.Files, source.Files...)

	// Merge sources
	for id, src := range source.Sources {
		target.Sources[id] = src
	}
}

// MethodNames names the methods of a single class whose receiver should
// be stripped of its default `mut self` polarity. Membership = "strip
// mut". The set form is intentional: there is no use for a "set mut on
// this method" rung because `mut self` is already the post-#612 default
// from populateSelfParams.
type MethodNames = set.Set[string]

// TODO(#500): extend mutabilityOverrides for Promise, Error, and other
// classes whose non-mutating methods should be callable on non-mut
// receivers. Entries here are exceptions the name-only heuristics in
// interop.ClassifyMethodByName (issue #614) either miss (e.g.
// String.charAt — no prefix match) or actively mis-classify (e.g.
// String.replace — `replace` is a mutating-prefix). Method names
// already covered by the heuristics — `get*`, `to*`, `is*`, `has*`,
// well-known `toString`/`valueOf`, and so on — are redundant and must
// not be re-listed here; the prelude pass applies the heuristics as a
// fall-through whenever a method has no override entry.
//
// the key is the interface name
var mutabilityOverrides = map[string]MethodNames{
	"String": set.FromSlice([]string{
		// Heuristic misses (no prefix/exact match) and active
		// mis-classifications (`replace`/`replaceAll` look mutating).
		"charAt",
		"charCodeAt",
		"codePointAt",
		"endsWith",
		"localeCompare",
		"match",
		"matchAll",
		"normalize",
		"padEnd",
		"padStart",
		"repeat",
		"replace",
		"replaceAll",
		"search",
		"split",
		"startsWith",
		"substr",
		"substring",
		"trim",
		"trimEnd",
		"trimStart",
	}),
	// RegExp.toString is covered by the well-known allow-list in
	// ClassifyMethodByName. `compile`, `exec` (with global/sticky), and
	// `test` (with global/sticky) are mutating and inherit the default
	// `mut self`. `Symbol.search` / `Symbol.split` are non-mutating per
	// spec but can't be expressed in this string-keyed map — see #620.
	"Object": set.FromSlice([]string{
		// Heuristic miss: `propertyIsEnumerable` doesn't start with any
		// known non-mutating prefix. The rest of Object.prototype
		// (hasOwnProperty, isPrototypeOf, toLocaleString, toString,
		// valueOf) is covered by the heuristics.
		"propertyIsEnumerable",
	}),
	"Function": set.FromSlice([]string{
		// Heuristic misses; `toString` is covered by the well-known list.
		"apply",
		"bind",
		"call",
	}),
	// `Number`, `Boolean`, and `Date` had every entry covered by the
	// name-only heuristics (`get*`, `to*`, well-known `toString`/`valueOf`)
	// — issue #614 removed the redundant bootstrap blocks.
	"Console": set.FromSlice([]string{
		// Heuristic misses (most Console methods are bare nouns) plus
		// `clear` (mis-classified as mutating by the `clear` prefix —
		// Console.clear is non-mutating on the Console object itself).
		"assert",
		"clear",
		"debug",
		"dir",
		"dirxml",
		"error",
		"group",
		"groupCollapsed",
		"groupEnd",
		"info",
		"log",
		"table",
		"time",
		"timeEnd",
		"timeLog",
		"timeStamp",
		"trace",
		"warn",
	}),
	"Body": set.FromSlice([]string{
		// Heuristic misses — every Body method is a bare noun.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
	"Response": set.FromSlice([]string{
		// Heuristic misses; `clone` is covered by the `clone` prefix.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
	"Request": set.FromSlice([]string{
		// Heuristic misses; `clone` is covered by the `clone` prefix.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
}

// applyMethodMutability classifies each MethodElem on objType using the
// per-class override set first and the name-only interop heuristics as
// the fall-through (issue #614). When neither source positively
// classifies the method as non-mutating it keeps the default `mut self`
// set by populateSelfParams. The override entries always win — they
// encode known exceptions that the heuristics either miss or
// mis-classify (e.g. String.replace, Console.clear).
//
// Only MethodElem is consulted. GetterElem / SetterElem polarity is
// fixed by populateSelfParams (getters non-mut, setters mut) — passing
// an accessor name in `names` would silently miss here. If an accessor
// ever needs its polarity overridden, extend the type switch below.
func applyMethodMutability(objType *type_system.ObjectType, names MethodNames) {
	for _, elem := range objType.Elems {
		me, ok := elem.(*type_system.MethodElem)
		if !ok {
			continue
		}
		if me.Name.Kind != type_system.StrObjTypeKeyKind {
			continue
		}
		name := me.Name.Str
		if names.Contains(name) {
			setReceiverMut(me.Fn, false)
			continue
		}
		if mut, classified := interop.ClassifyMethodByName(name); classified && !mut {
			setReceiverMut(me.Fn, false)
		}
	}
}

func UpdateMethodMutability(ctx Context, namespace *type_system.Namespace) {
	// First pass: trio-shaped classes (interface X + interface XConstructor +
	// declare var X: XConstructor) — look up the instance type via the
	// constructor's return type. Iterate in sorted order so the
	// stderr warnings emitted on instance-type-resolution failure
	// (see `Warning: could not resolve instance type alias`) come
	// out in a stable order across runs.
	typeNames := make([]string, 0, len(namespace.Types))
	for name := range namespace.Types {
		typeNames = append(typeNames, name)
	}
	slices.Sort(typeNames)
	for _, name := range typeNames {
		if strings.HasSuffix(name, "Constructor") && name != "ArrayConstructor" {
			classTypeAlias := namespace.Types[name]

			var instIdent type_system.QualIdent
			if ct, ok := type_system.Prune(classTypeAlias.Type).(*type_system.ObjectType); ok {
				for _, elem := range ct.Elems {
					if ce, ok := elem.(*type_system.ConstructorElem); ok {
						if rt, ok := type_system.Prune(ce.Fn.Return).(*type_system.TypeRefType); ok {
							instIdent = rt.Name
						}
					}
				}
			}

			instTypeAlias := resolveQualifiedTypeAlias(ctx, instIdent)
			if instTypeAlias == nil {
				// This prints out the following:
				// Warning: could not resolve instance type alias for SymbolConstructor
				// Warning: could not resolve instance type alias for ProxyConstructor
				// TODO: investigate this further.
				fmt.Fprintf(os.Stderr, "Warning: could not resolve instance type alias for %s\n", name)
				// Skip if the instance type alias couldn't be resolved
				// This can happen if computed keys in the type weren't processed
				continue
			}
			if ident, ok := instIdent.(*type_system.Ident); ok {
				instName := ident.Name
				// TODO(#254): Support qualified identifiers in mutability overrides
				overrides := mutabilityOverrides[instName]

				if it, ok := type_system.Prune(instTypeAlias.Type).(*type_system.ObjectType); ok {
					// TypeScript .d.ts has no mut-self annotation, so
					// methods default to `mut self` (set by
					// populateSelfParams). Apply per-interface overrides
					// and, as a fall-through for any unlisted method,
					// the name-only interop heuristics (#614).
					applyMethodMutability(it, overrides)
				} else {
					panic("Instance type is not an ObjectType: " + instTypeAlias.Type.String())
				}
			}
		}
	}

	// Second pass: every named ObjectType in the namespace, including
	// types declared via `declare var X: {...}` (e.g. Response, Request)
	// or as bare interfaces (e.g. Function, Body), and any class with no
	// override entry at all. Without the per-class entry the override
	// set is empty and only the name-only interop heuristics fire — that
	// fall-through is the point of #614, so a `.d.ts` getFoo on a class
	// the override map never heard of still ends up non-mut.
	//
	// The trio pass above already touched constructor-shaped classes
	// (via their resolved instance types); duplicate work on those is
	// harmless because setReceiverMut is idempotent. This pass also
	// visits the `*Constructor` types themselves, so heuristic-only
	// classifications now reach static methods like Array.isArray and
	// Object.keys/values/entries.
	//
	// Iterate in a deterministic order so snapshot tests downstream don't
	// pick up map iteration randomness.
	typeNames = typeNames[:0]
	for name := range namespace.Types {
		typeNames = append(typeNames, name)
	}
	slices.Sort(typeNames)
	for _, name := range typeNames {
		typeAlias := namespace.Types[name]
		objType, ok := type_system.Prune(typeAlias.Type).(*type_system.ObjectType)
		if !ok {
			continue
		}
		applyMethodMutability(objType, mutabilityOverrides[name])
	}
}

func UpdateCollectionMutability(namespace *type_system.Namespace) {
	for _, pair := range []struct {
		mutable, readonly string
	}{
		{"Array", "ReadonlyArray"},
		{"Map", "ReadonlyMap"},
		{"Set", "ReadonlySet"},
	} {
		mergeReadonlyVariant(namespace, pair.mutable, pair.readonly)
	}
}

// mergeReadonlyVariant merges a TypeScript Readonly* interface into its
// mutable counterpart, e.g. ReadonlyArray and Array. Presence on the Readonly
// variant is positive evidence that a method does not mutate, so methods that
// appear on the readonly type get their receiver stripped of `mut` (overriding
// the default `mut self` set by populateSelfParams). Methods unique to the
// mutable type inherit the default `mut self` — the setReceiverMut call
// in that branch is a no-op left for clarity. The merged element list is
// shared between both type aliases so that lookups against either name
// see the union.
func mergeReadonlyVariant(namespace *type_system.Namespace, mutableName, readonlyName string) {
	mutableTypeAlias, ok := namespace.Types[mutableName]
	if !ok {
		return
	}
	readonlyTypeAlias, ok := namespace.Types[readonlyName]
	if !ok {
		return
	}
	mutableType, ok := type_system.Prune(mutableTypeAlias.Type).(*type_system.ObjectType)
	if !ok {
		return
	}
	readonlyType, ok := type_system.Prune(readonlyTypeAlias.Type).(*type_system.ObjectType)
	if !ok {
		return
	}

	readonlyElems := make(set.Set[type_system.ObjTypeKey])
	for _, v := range readonlyType.Elems {
		if me, ok := v.(*type_system.MethodElem); ok {
			// Keys must match exactly (kind + payload) so that
			// symbol-keyed methods like `[Symbol.iterator]` are
			// matched alongside string-keyed ones.
			readonlyElems.Add(me.Name)

			// Methods on the Readonly* variant are non-mutating.
			setReceiverMut(me.Fn, false)
		}
	}

	readonlyType.Elems = mutableType.Elems
	for _, elem := range mutableType.Elems {
		me, ok := elem.(*type_system.MethodElem)
		if !ok {
			continue
		}
		mut := !readonlyElems.Contains(me.Name)
		setReceiverMut(me.Fn, mut)
	}
}

var cachedGlobalScope *Scope
var cachedSymbolIDCounter int
var cachedCustomMatcherSymbolID int
var cachedPackageRegistry *PackageRegistry
var cachedOverrideStore *interop.OverrideStore

// initializeGlobalScope creates the global scope containing TypeScript built-in types
// (Array, Promise, etc. from lib.es5.d.ts and lib.dom.d.ts), operator bindings, and
// the Symbol object.
//
// This method sets c.GlobalScope to the newly created scope. The global scope has no
// parent (it is the root of the scope chain). User code scopes should use the global
// scope as their parent to access built-in types.
//
// Named modules from .d.ts files are registered in the PackageRegistry, not in the
// global scope. This separates package symbols from global symbols.
func (c *Checker) initializeGlobalScope() {
	// Create a fresh global namespace and scope
	globalNs := type_system.NewNamespace()
	globalScope := &Scope{
		Parent:    nil, // Global scope has no parent
		Namespace: globalNs,
	}

	// Load global definitions from TypeScript lib files. Toggle the
	// loadingExternalTypes flag so Phase 11 elision treats lib
	// signatures permissively (no ambiguous-elision diagnostics).
	prev := c.loadingExternalTypes
	c.loadingExternalTypes = true
	defer func() { c.loadingExternalTypes = prev }()
	c.loadGlobalDefinitions(globalScope)

	// Post-process the namespace
	for _, typeAlias := range globalNs.Types {
		typeAlias.Type = type_system.Prune(typeAlias.Type)
	}

	for _, binding := range globalNs.Values {
		binding.Type = type_system.Prune(binding.Type)
	}

	inferCtx := Context{
		Scope:      globalScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	// Wire SelfParam onto every .d.ts-loaded method first (default
	// `mut self`, plus iterator-protocol fixups; see populateSelfParams),
	// then let the override passes strip `mut` from receivers positively
	// classified as non-mutating by mutating SelfParam.Type in place.
	populateSelfParams(globalNs)
	UpdateMethodMutability(inferCtx, globalNs)
	UpdateCollectionMutability(globalNs)

	// Add built-in operator bindings
	c.addOperatorBindings(globalNs)

	// Add customMatcher to SymbolConstructor for enum pattern matching
	c.addCustomMatcherToSymbol(globalNs)

	// Add globalThis binding - provides access to the global namespace
	// This allows accessing shadowed globals via globalThis.Array, globalThis.Promise, etc.
	c.addGlobalThisBinding(globalNs)

	// Set the global scope on the Checker
	c.GlobalScope = globalScope
}

// loadGlobalDefinitions loads TypeScript lib files and infers their declarations
// into the global scope. Named modules are registered in the PackageRegistry.
//
// All lib files are combined into a single module with a unified dependency graph
// before inference. This ensures that interface declarations split across multiple
// files (e.g., SymbolConstructor in lib.es2015.symbol.d.ts and lib.es2015.symbol.wellknown.d.ts)
// are properly merged together. See Risk 2 mitigation in requirements.md.
//
// Load order:
// 1. ES lib files (lib.es5.d.ts, lib.es2015.*.d.ts, lib.es2016.*.d.ts, etc.)
// 2. DOM lib file (lib.dom.d.ts) - loaded after ES libs since DOM types may reference ES2015+ types
func (c *Checker) loadGlobalDefinitions(globalScope *Scope) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		panic(fmt.Sprintf("failed to find repository root: %v", err))
	}

	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")

	// Verify TypeScript is installed
	if _, statErr := os.Stat(libDir); statErr != nil {
		if os.IsNotExist(statErr) {
			panic(fmt.Sprintf(
				"TypeScript lib directory not found at %s. "+
					"Please install TypeScript: npm install typescript",
				libDir,
			))
		}
		panic(fmt.Sprintf("cannot access TypeScript lib directory %s: %v", libDir, statErr))
	}

	// Discover ES lib files
	targetVersion := "es2015"
	esLibFiles, err := discoverESLibFiles(libDir, targetVersion)
	if err != nil {
		// Hard error - can't proceed without lib files
		panic(fmt.Sprintf("failed to discover ES lib files: %v", err))
	}

	if len(esLibFiles) == 0 {
		panic(fmt.Sprintf(
			"no ES lib files found in %s. "+
				"TypeScript installation may be corrupted. "+
				"Try: rm -rf node_modules && npm install",
			libDir,
		))
	}

	// Add DOM lib file to the list
	// DOM types may reference ES2015+ types (e.g., Promise, Symbol)
	allLibFiles := make([]string, 0, len(esLibFiles)+1)
	allLibFiles = append(allLibFiles, esLibFiles...)
	allLibFiles = append(allLibFiles, "lib.dom.d.ts")

	// Start with an empty combined module. All lib files will be merged into this
	// single module, creating a unified dependency graph where interface declarations
	// from different files are properly grouped together.
	combinedModule := ast.NewModule(btree.Map[string, *ast.Namespace]{})

	for _, filename := range allLibFiles {
		libPath := filepath.Join(libDir, filename)
		parsedTypeDef, loadErr := parseTypeDef(libPath, c.OverrideStore, "")
		if loadErr != nil {
			panic(fmt.Sprintf("Failed to load TypeScript lib file: %s: %v", libPath, loadErr))
		}

		// Merge GlobalModule into combined module
		mergeModules(combinedModule, parsedTypeDef.GlobalModule)

		// NOTE: We don't bother merging parsedTypeDef.PackageModule or
		// parsedTypeDef.NamedModules since we know that TypeScript lib files don't
		// have any top-level exports or named modules.
	}

	inferCtx := Context{
		Scope:      globalScope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	fatalErrors := c.InferModule(inferCtx, combinedModule)
	if len(fatalErrors) > 0 {
		for _, err := range fatalErrors {
			sourceID := err.Span().SourceID
			filePath := "unknown"
			if source, ok := combinedModule.Sources[sourceID]; ok {
				filePath = source.Path
			}
			fmt.Fprintf(os.Stderr, "Inference error in lib files: %s (file: %s, span: %v)\n", err.Message(), filePath, err.Span())
		}
		panic("Failed to infer types for TypeScript lib files")
	}
}

// addOperatorBindings adds built-in operator bindings to the namespace
func (c *Checker) addOperatorBindings(ns *type_system.Namespace) {
	binArithType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewNumPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewNumPrimType(nil)),
		},
		type_system.NewNumPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binArithBinding := type_system.Binding{
		Source:     nil,
		Type:       binArithType,
		Assignable: false,
		Mutable:    false,
	}

	binCompType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewNumPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewNumPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binACompBinding := type_system.Binding{
		Source:     nil,
		Type:       binCompType,
		Assignable: false,
		Mutable:    false,
	}

	binEqType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewAnyType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewAnyType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binEqBinding := type_system.Binding{
		Source:     nil,
		Type:       binEqType,
		Assignable: false,
		Mutable:    false,
	}

	binLogicType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewBoolPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewBoolPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binLogicBinding := type_system.Binding{
		Source:     nil,
		Type:       binLogicType,
		Assignable: false,
		Mutable:    false,
	}

	unaryLogicType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewBoolPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	unaryLogicBinding := type_system.Binding{
		Source:     nil,
		Type:       unaryLogicType,
		Assignable: false,
		Mutable:    false,
	}

	// String concatenation operator
	strConcatType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewStrPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewStrPrimType(nil)),
		},
		type_system.NewStrPrimType(nil),
		type_system.NewNeverType(nil),
	)
	strConcatBinding := type_system.Binding{
		Source:     nil,
		Type:       strConcatType,
		Assignable: false,
		Mutable:    false,
	}

	ns.Values["+"] = &binArithBinding
	ns.Values["-"] = &binArithBinding
	ns.Values["*"] = &binArithBinding
	ns.Values["/"] = &binArithBinding

	ns.Values["=="] = &binEqBinding
	ns.Values["!="] = &binEqBinding
	ns.Values["<"] = &binACompBinding
	ns.Values[">"] = &binACompBinding
	ns.Values["<="] = &binACompBinding
	ns.Values[">="] = &binACompBinding

	ns.Values["&&"] = &binLogicBinding
	ns.Values["||"] = &binLogicBinding

	ns.Values["!"] = &unaryLogicBinding

	ns.Values["++"] = &strConcatBinding
}

// addCustomMatcherToSymbol adds the customMatcher property to the Symbol binding.
// It's part of the https://github.com/tc39/proposal-extractors proposal, and is
// used for enum pattern matching in Escalier.
// The ES2015+ lib files define Symbol and SymbolConstructor with standard well-known symbols
// (iterator, toStringTag, etc.), but customMatcher is not part of the standard.
func (c *Checker) addCustomMatcherToSymbol(ns *type_system.Namespace) {
	// Get the existing SymbolConstructor type alias
	symbolConstructor := ns.Types["SymbolConstructor"]
	if symbolConstructor == nil {
		return
	}

	// Get the ObjectType from the type alias
	objType, ok := type_system.Prune(symbolConstructor.Type).(*type_system.ObjectType)
	if !ok {
		return
	}

	// Create a new unique symbol for customMatcher
	c.SymbolID++
	c.CustomMatcherSymbolID = c.SymbolID // Store the symbol ID for use in unify.go
	customMatcherSymbol := type_system.NewUniqueSymbolType(nil, c.SymbolID)

	// Add the customMatcher property to the SymbolConstructor type
	objType.Elems = append(objType.Elems, &type_system.PropertyElem{
		Name:     type_system.NewStrKey("customMatcher"),
		Value:    customMatcherSymbol,
		Optional: false,
		Readonly: true,
	})
}

// addGlobalThisBinding adds the globalThis binding which provides access to the global namespace.
// This allows accessing shadowed globals via globalThis.Array, globalThis.Promise, etc.
func (c *Checker) addGlobalThisBinding(ns *type_system.Namespace) {
	globalThisType := type_system.NewNamespaceType(nil, ns)
	ns.Values["globalThis"] = &type_system.Binding{
		Source:     nil,
		Type:       globalThisType,
		Assignable: false,
		Mutable:    false,
	}
}

// Prelude initializes the global scope if not already done, and returns a child scope
// that can be used for user code. The global scope is cached for efficiency.
//
// We assume that a new Checker instance is being passed in every time Prelude is called.
// TODO(#256): Report all errors to the caller.
func Prelude(c *Checker) *Scope {
	// Populate c.OverrideStore from the embedded BuiltinFS unless a
	// caller (typically a test) has already injected one. This must
	// happen before the cache check below so that the store pointer
	// used as the cache key is the one parseTypeDef will actually
	// consult during loadGlobalDefinitions.
	//
	// §6.A: BuiltinFS contains no .esc files yet, so BuildBuiltinStore
	// returns a memoized empty store and never invokes a TypeChecker
	// callback — nil here is safe. §6.B onwards will need a real
	// callback that resolves override-file references against lib
	// globals.
	if c.OverrideStore == nil {
		store, errs := interop.BuildBuiltinStore(c.ctx, nil)
		if len(errs) > 0 {
			panic(fmt.Sprintf("interop.BuildBuiltinStore returned %d error(s) during prelude initialization: %v", len(errs), errors.Join(errs...)))
		}
		c.OverrideStore = store
	}

	// The cached global scope encodes mutability decisions that depend on
	// c.OverrideStore (see parseTypeDef → ConvertModuleWithOverrides). If a
	// different store is in play, the cache is stale — invalidate it.
	//
	// Identity comparison is intentional: OverrideStore is constructed by
	// Merge and is read-only thereafter (Resolve never writes), so equal
	// pointers imply equal contents. In-place mutation of a store after
	// caching would defeat this — callers must not mutate.
	if cachedGlobalScope != nil && cachedOverrideStore != c.OverrideStore {
		cachedGlobalScope = nil
		cachedPackageRegistry = nil
		cachedOverrideStore = nil
	}
	if cachedGlobalScope != nil {
		c.SymbolID = cachedSymbolIDCounter
		c.CustomMatcherSymbolID = cachedCustomMatcherSymbolID
		// Shallow-copy the cached global scope's namespace so that mutations
		// (e.g. processExportAsNamespace adding to Namespaces) don't pollute
		// the cache across test runs with -count>1.
		cachedNs := cachedGlobalScope.Namespace
		c.GlobalScope = &Scope{
			Namespace: &type_system.Namespace{
				Values:     maps.Clone(cachedNs.Values),
				Types:      maps.Clone(cachedNs.Types),
				Namespaces: maps.Clone(cachedNs.Namespaces),
			},
		}
		c.PackageRegistry.CopyFrom(cachedPackageRegistry)
		return c.GlobalScope.WithNewScope()
	}

	// Initialize the global scope for the first time
	c.initializeGlobalScope()

	// Cache for subsequent calls
	cachedGlobalScope = c.GlobalScope
	cachedSymbolIDCounter = c.SymbolID
	cachedCustomMatcherSymbolID = c.CustomMatcherSymbolID
	cachedPackageRegistry = c.PackageRegistry.Copy() // copy to prevent test pollution
	cachedOverrideStore = c.OverrideStore

	return c.GlobalScope.WithNewScope()
}

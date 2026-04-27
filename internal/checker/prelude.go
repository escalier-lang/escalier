package checker

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/escalier-lang/escalier/internal/ast"
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

// the key is the method name
type Overrides map[string]bool

// TODO(#500): populate mutabilityOverrides for Date, Promise, Error, and other
// classes whose methods mutate the receiver. Methods on classes not listed
// here default to MutSelf=nil (treated as not-mut-self), so e.g.
// `someImmutableDate.setHours(...)` is currently visible on an immutable
// receiver. Adding an entry like `"Date": {"setHours": true, ...}` restores
// mut-self gating for those methods.
//
// the key is the interface name
var mutabilityOverrides = map[string]Overrides{
	"String": {
		"at":                false,
		"chatAt":            false,
		"charCodeAt":        false,
		"codePointAt":       false,
		"concat":            false,
		"endsWith":          false,
		"includes":          false,
		"indexOf":           false,
		"lastIndexOf":       false,
		"localeCompare":     false,
		"match":             false,
		"matchAll":          false,
		"normalize":         false,
		"padEnd":            false,
		"padStart":          false,
		"repeat":            false,
		"replace":           false,
		"replaceAll":        false,
		"search":            false,
		"slice":             false,
		"split":             false,
		"startsWith":        false,
		"substr":            false,
		"substring":         false,
		"toLocaleLowerCase": false,
		"toLocaleUpperCase": false,
		"toLowerCase":       false,
		"toUpperCase":       false,
		"trim":              false,
		"trimEnd":           false,
		"trimStart":         false,
		"valueOf":           false,
		// TODO: handle Symbol.iterator as key
	},
	"RexExp": {
		"compile":  true,
		"exec":     true, // when using global or sticky flags
		"test":     true, // when using global or sticky flags
		"toString": false,
		// TODO: handle Symbol.match, Symbol.replace, Symbol.search, Symbol.split as keys
	},
	"Number": {
		"toExponential":  false,
		"toFixed":        false,
		"toLocaleString": false,
		"toPrecision":    false,
		"toString":       false,
		"valueOf":        false,
	},
	"Boolean": {
		"valueOf": false,
	},
}

func UpdateMethodMutability(ctx Context, namespace *type_system.Namespace) {
	for name := range namespace.Types {
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
					for _, elem := range it.Elems {
						if me, ok := elem.(*type_system.MethodElem); ok {
							// TypeScript .d.ts has no mut-self annotation, so
							// default to nil (unknown) and only override when
							// the method appears in the per-interface overrides
							// table. Treating "unknown" as not-mut-self matches
							// the rest of the checker (e.g. binding-mutability
							// in infer_module.go) and avoids hiding non-mutating
							// methods on classes like Function.
							if me.Name.Kind != type_system.StrObjTypeKeyKind {
								continue
							}
							if value, exists := overrides[me.Name.Str]; exists {
								mutSelf := value
								me.MutSelf = &mutSelf
							}
						}
					}
				} else {
					panic("Instance type is not an ObjectType: " + instTypeAlias.Type.String())
				}
			}
		}
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
// mutable counterpart. Methods that exist on the readonly type are marked
// `mut_self = false`; methods unique to the mutable type are marked
// `mut_self = true`. The merged element list is shared between both type
// aliases so that lookups against either name see the union.
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
			key := type_system.ObjTypeKey{
				Kind: type_system.StrObjTypeKeyKind,
				Str:  me.Name.Str,
			}
			readonlyElems.Add(key)

			// Methods on the Readonly* variant are non-mutating.
			mutSelf := false
			me.MutSelf = &mutSelf
		}
	}

	readonlyType.Elems = mutableType.Elems
	for _, elem := range mutableType.Elems {
		me, ok := elem.(*type_system.MethodElem)
		if !ok {
			continue
		}
		mutSelf := true
		key := type_system.ObjTypeKey{
			Kind: type_system.StrObjTypeKeyKind,
			Str:  me.Name.Str,
		}
		if readonlyElems.Contains(key) {
			mutSelf = false
		}
		me.MutSelf = &mutSelf
	}
}

var cachedGlobalScope *Scope
var cachedSymbolIDCounter int
var cachedCustomMatcherSymbolID int
var cachedPackageRegistry *PackageRegistry

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

	// Load global definitions from TypeScript lib files
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
		parsedTypeDef, loadErr := parseTypeDef(libPath)
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

	return c.GlobalScope.WithNewScope()
}

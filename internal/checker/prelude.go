package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// findRepoRoot walks up the directory tree to find the repository root
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		// Check if go.mod exists in current directory
		goModPath := filepath.Join(dir, "go.mod")
		_, err := os.Lstat(goModPath)
		if err == nil {
			return dir, nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding go.mod
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// the key is the method name
type Overrides map[string]bool

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
			if ident, ok := instIdent.(*type_system.Ident); ok {
				instName := ident.Name
				// TODO(#254): Support qualified identifiers in mutability overrides
				overrides := mutabilityOverrides[instName]

				if it, ok := type_system.Prune(instTypeAlias.Type).(*type_system.ObjectType); ok {
					for _, elem := range it.Elems {
						if me, ok := elem.(*type_system.MethodElem); ok {
							mutSelf := true
							if me.Name.Kind == type_system.StrObjTypeKeyKind {
								value, exists := overrides[me.Name.Str]
								if exists {
									mutSelf = value
								}
							}
							me.MutSelf = &mutSelf
						}
					}
				} else {
					panic("Instance type is not an ObjectType: " + instTypeAlias.Type.String())
				}
			}
		}
	}
}

func UpdateArrayMutability(namespace *type_system.Namespace) {
	arrayTypeAlias := namespace.Types["Array"]
	readonlyArrayTypeAlias := namespace.Types["ReadonlyArray"]
	arrayType := type_system.Prune(arrayTypeAlias.Type).(*type_system.ObjectType)
	readonlyArrayType := type_system.Prune(readonlyArrayTypeAlias.Type).(*type_system.ObjectType)

	readonlyArrayElems := make(set.Set[type_system.ObjTypeKey])
	for _, v := range readonlyArrayType.Elems {
		if me, ok := v.(*type_system.MethodElem); ok {
			key := type_system.ObjTypeKey{
				Kind: type_system.StrObjTypeKeyKind,
				Str:  me.Name.Str,
				Num:  0,
				Sym:  0,
			}
			readonlyArrayElems.Add(key)

			// All methods on ReadonlyArray are non-mutating
			mutSelf := false
			me.MutSelf = &mutSelf
		}
	}

	readonlyArrayType.Elems = arrayType.Elems
	for _, elem := range arrayType.Elems {
		switch me := elem.(type) {
		case *type_system.MethodElem:
			mutSelf := true
			key := type_system.ObjTypeKey{
				Kind: type_system.StrObjTypeKeyKind,
				Str:  me.Name.Str,
				Num:  0,
				Sym:  0,
			}
			if readonlyArrayElems.Contains(key) {
				mutSelf = false
			}
			me.MutSelf = &mutSelf
		}
	}
}

// LoadedPackageResult holds the result of loading and classifying a .d.ts file.
type LoadedPackageResult struct {
	// PackageModule is the AST module containing package declarations (exported symbols).
	// nil if the file has no top-level exports.
	PackageModule *ast.Module

	// GlobalModule is the AST module containing global declarations.
	// This includes declarations from `declare global { ... }` blocks,
	// and all declarations if the file has no top-level exports.
	GlobalModule *ast.Module

	// NamedModules maps module names to their AST modules.
	// e.g., `declare module "lodash/fp" { ... }` creates an entry for "lodash/fp".
	NamedModules map[string]*ast.Module
}

// loadClassifiedTypeScriptModule loads a .d.ts file and classifies its contents
// using the FileClassification system from dts_parser/classifier.go.
func loadClassifiedTypeScriptModule(filename string) (*LoadedPackageResult, error) {
	// Read the file
	contents, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading DTS file: %s\n", err.Error())
		return nil, err
	}

	source := &ast.Source{
		Path:     filename,
		Contents: string(contents),
		ID:       0,
	}

	// Parse the module
	parser := dts_parser.NewDtsParser(source)
	dtsModule, parseErrors := parser.ParseModule()

	if len(parseErrors) > 0 {
		fmt.Fprintf(os.Stderr, "Errors parsing DTS module:\n")
		for _, parseErr := range parseErrors {
			fmt.Fprintf(os.Stderr, "- %s\n", parseErr)
		}
		return nil, fmt.Errorf("failed to parse DTS module %s: %d errors", filename, len(parseErrors))
	}

	// Classify the file using the FileClassification system
	classification := dts_parser.ClassifyDTSFile(dtsModule)

	result := &LoadedPackageResult{
		NamedModules: make(map[string]*ast.Module),
	}

	// Process package declarations (exported symbols)
	if len(classification.PackageDecls) > 0 {
		pkgDtsModule := &dts_parser.Module{
			Statements: classification.PackageDecls,
		}
		pkgAstModule, err := interop.ConvertModule(pkgDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting package declarations: %w", err)
		}
		result.PackageModule = pkgAstModule
	}

	// Process global declarations
	if len(classification.GlobalDecls) > 0 {
		globalDtsModule := &dts_parser.Module{
			Statements: classification.GlobalDecls,
		}
		globalAstModule, err := interop.ConvertModule(globalDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting global declarations: %w", err)
		}
		result.GlobalModule = globalAstModule
	}

	// Process named modules
	for _, namedMod := range classification.NamedModules {
		namedDtsModule := &dts_parser.Module{
			Statements: namedMod.Decls,
		}
		namedAstModule, err := interop.ConvertModule(namedDtsModule)
		if err != nil {
			return nil, fmt.Errorf("converting named module %s: %w", namedMod.ModuleName, err)
		}
		result.NamedModules[namedMod.ModuleName] = namedAstModule
	}

	return result, nil
}

var cachedGlobalScope *Scope
var cachedSymbolIDCounter int
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
	UpdateArrayMutability(globalNs)

	// Add built-in operator bindings
	c.addOperatorBindings(globalNs)

	// Add Symbol object with iterator and customMatcher unique symbols
	c.addSymbolBinding(globalNs)

	// Add globalThis binding - provides access to the global namespace
	// This allows accessing shadowed globals via globalThis.Array, globalThis.Promise, etc.
	c.addGlobalThisBinding(globalNs)

	// Set the global scope on the Checker
	c.GlobalScope = globalScope
}

// loadGlobalDefinitions loads TypeScript lib files (lib.es5.d.ts, lib.dom.d.ts)
// and infers their declarations into the global scope.
// Named modules are registered in the PackageRegistry.
func (c *Checker) loadGlobalDefinitions(globalScope *Scope) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		panic(fmt.Sprintf("Failed to find repository root: %s", err))
	}

	// Load lib.es5.d.ts
	libES5Path := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.es5.d.ts")
	c.loadGlobalFile(libES5Path, globalScope)

	// Load lib.dom.d.ts
	libDOMPath := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.dom.d.ts")
	c.loadGlobalFile(libDOMPath, globalScope)
}

// loadGlobalFile loads a single .d.ts file and adds its declarations to the appropriate scope:
// - Global declarations go to globalScope
// - Package declarations (if any) also go to globalScope for lib files
// - Named modules (declare module "...") are registered in the PackageRegistry
func (c *Checker) loadGlobalFile(filePath string, globalScope *Scope) {
	loadResult, err := loadClassifiedTypeScriptModule(filePath)
	if err != nil {
		panic(fmt.Sprintf("Failed to load TypeScript lib file: %s: %v", filePath, err))
	}

	// Process global declarations
	if loadResult.GlobalModule != nil {
		inferCtx := Context{
			Scope:      globalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		inferErrors := c.InferModule(inferCtx, loadResult.GlobalModule)
		if len(inferErrors) > 0 {
			for _, err := range inferErrors {
				fmt.Fprintf(os.Stderr, "Inference error in %s: %s\n", filePath, err.Message())
			}
			panic(fmt.Sprintf("Failed to infer types for %s", filePath))
		}
	}

	// Process package declarations (for lib files, these are also globals)
	if loadResult.PackageModule != nil {
		inferCtx := Context{
			Scope:      globalScope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		inferErrors := c.InferModule(inferCtx, loadResult.PackageModule)
		if len(inferErrors) > 0 {
			for _, err := range inferErrors {
				fmt.Fprintf(os.Stderr, "Inference error in %s: %s\n", filePath, err.Message())
			}
			panic(fmt.Sprintf("Failed to infer types for %s", filePath))
		}
	}

	// Process named modules - register them in the PackageRegistry
	for moduleName, astModule := range loadResult.NamedModules {
		// Create a namespace for this named module
		moduleNs := type_system.NewNamespace()
		moduleScope := &Scope{
			Parent:    globalScope, // Named modules can reference globals
			Namespace: moduleNs,
		}

		inferCtx := Context{
			Scope:      moduleScope,
			IsAsync:    false,
			IsPatMatch: false,
		}

		inferErrors := c.InferModule(inferCtx, astModule)
		if len(inferErrors) > 0 {
			for _, err := range inferErrors {
				fmt.Fprintf(os.Stderr, "Inference error in module %s: %s\n", moduleName, err.Message())
			}
			// Don't panic for named modules - continue processing other modules
			continue
		}

		// Register the named module in the PackageRegistry
		// Use the module name as the key since lib files don't have package paths
		if err := c.PackageRegistry.Register(moduleName, moduleNs); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to register module %s: %s\n", moduleName, err.Error())
		}
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
		Source:  nil,
		Type:    binArithType,
		Mutable: false,
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
		Source:  nil,
		Type:    binCompType,
		Mutable: false,
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
		Source:  nil,
		Type:    binEqType,
		Mutable: false,
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
		Source:  nil,
		Type:    binLogicType,
		Mutable: false,
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
		Source:  nil,
		Type:    unaryLogicType,
		Mutable: false,
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
		Source:  nil,
		Type:    strConcatType,
		Mutable: false,
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

// addSymbolBinding adds the Symbol object with iterator and customMatcher unique symbols
func (c *Checker) addSymbolBinding(ns *type_system.Namespace) {
	c.SymbolID++
	iteratorSymbol := type_system.NewUniqueSymbolType(nil, c.SymbolID)
	c.SymbolID++
	customMatcherSymbol := type_system.NewUniqueSymbolType(nil, c.SymbolID)

	symbolElems := []type_system.ObjTypeElem{
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("iterator"),
			Value:    iteratorSymbol,
			Optional: false,
			Readonly: true,
		},
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("customMatcher"),
			Value:    customMatcherSymbol,
			Optional: false,
			Readonly: true,
		},
	}

	ns.Values["Symbol"] = &type_system.Binding{
		Source:  nil,
		Type:    type_system.NewObjectType(nil, symbolElems),
		Mutable: false,
	}
}

// addGlobalThisBinding adds the globalThis binding which provides access to the global namespace.
// This allows accessing shadowed globals via globalThis.Array, globalThis.Promise, etc.
func (c *Checker) addGlobalThisBinding(ns *type_system.Namespace) {
	globalThisType := type_system.NewNamespaceType(nil, ns)
	ns.Values["globalThis"] = &type_system.Binding{
		Source:  nil,
		Type:    globalThisType,
		Mutable: false,
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
		c.GlobalScope = cachedGlobalScope
		c.PackageRegistry.CopyFrom(cachedPackageRegistry)
		return cachedGlobalScope.WithNewScope()
	}

	// Initialize the global scope for the first time
	c.initializeGlobalScope()

	// Cache for subsequent calls
	cachedGlobalScope = c.GlobalScope
	cachedSymbolIDCounter = c.SymbolID
	cachedPackageRegistry = c.PackageRegistry.Copy() // copy to prevent test pollution

	return c.GlobalScope.WithNewScope()
}

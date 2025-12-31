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
				// TODO: Support qualified identifiers in mutability overrides
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

func loadTypeScriptModule(filename string) (*ast.Module, error) {
	if _, err := os.Lstat(filename); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "DTS file not found: %s\n", filename)
		return nil, err
	}

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
		return nil, err
	}

	astModule, err := interop.ConvertModule(dtsModule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error converting DTS to AST: %s\n", err.Error())
		return nil, err
	}

	return astModule, nil
}

var preludeScope *Scope
var symbolIDCounter int

// We assume that a new Checker instance is being passed in every time Prelude is called.
func Prelude(c *Checker) *Scope {
	if preludeScope != nil {
		c.SymbolID = symbolIDCounter
		return preludeScope.WithNewScope()
	}

	repoRoot, _ := findRepoRoot()

	libES5Path := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.es5.d.ts")
	libES5Module, err := loadTypeScriptModule(libES5Path)
	if err != nil {
		panic("Failed to load TypeScript lib.es5.d.ts")
	}

	// TODO: Re-enable once function overloads are supported
	libDOMPath := filepath.Join(repoRoot, "node_modules", "typescript", "lib", "lib.dom.d.ts")
	libDOMModule, err := loadTypeScriptModule(libDOMPath)
	if err != nil {
		panic("Failed to load TypeScript lib.dom.d.ts")
	}

	scope := NewScope()
	inferCtx := Context{
		Scope:      scope,
		IsAsync:    false,
		IsPatMatch: false,
	}

	inferErrors := c.InferModule(inferCtx, libES5Module)
	if len(inferErrors) > 0 {
		panic("Failed to infer types for lib.es5.d.ts")
	}

	// TODO: Re-enable once function overloads are supported
	inferErrors = c.InferModule(inferCtx, libDOMModule)
	if len(inferErrors) > 0 {
		for _, err := range inferErrors {
			fmt.Fprintf(os.Stderr, "Inference error: %s\n", err.Message())
		}
		panic("Failed to infer types for lib.dom.d.ts")
	}

	inferredScope := inferCtx.Scope.Namespace

	for _, typeAlias := range inferredScope.Types {
		typeAlias.Type = type_system.Prune(typeAlias.Type)
	}

	for _, binding := range inferredScope.Values {
		binding.Type = type_system.Prune(binding.Type)
	}

	UpdateMethodMutability(inferCtx, inferredScope)
	UpdateArrayMutability(inferredScope)

	scope.Namespace = inferredScope

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

	// unaryArithType := &FuncType{
	// 	Params: []*type_system.FuncParam{
	// 		type_system.NewFuncParam(type_system.NewIdentPat("a"), NewNumType()),
	// 	},
	// 	Return: NewNumType(),
	// }
	// unaryArithBinding := type_system.Binding{
	// 	Source:  nil,
	// 	Type:    unaryArithType,
	// 	Mutable: false,
	// }

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

	scope.Namespace.Values["+"] = &binArithBinding
	scope.Namespace.Values["-"] = &binArithBinding
	scope.Namespace.Values["*"] = &binArithBinding
	scope.Namespace.Values["/"] = &binArithBinding

	scope.Namespace.Values["=="] = &binEqBinding
	scope.Namespace.Values["!="] = &binEqBinding
	scope.Namespace.Values["<"] = &binACompBinding
	scope.Namespace.Values[">"] = &binACompBinding
	scope.Namespace.Values["<="] = &binACompBinding
	scope.Namespace.Values[">="] = &binACompBinding

	scope.Namespace.Values["&&"] = &binLogicBinding
	scope.Namespace.Values["||"] = &binLogicBinding

	// TODO: uncomment after adding support for calling overloaded functions
	// scope.Namespace.Values["-"] = type_system.Binding{
	// 	Source:  nil,
	// 	Type:    NewIntersectionType(binArithType, unaryArithType),
	// 	Mutable: false,
	// }

	scope.Namespace.Values["!"] = &unaryLogicBinding

	var objElems []type_system.ObjTypeElem

	objElems = append(objElems, &type_system.MethodElem{
		Name: type_system.NewStrKey("log"),
		Fn: type_system.NewFuncType(
			nil,
			nil,
			[]*type_system.FuncParam{
				type_system.NewFuncParam(type_system.NewIdentPat("msg"), type_system.NewStrPrimType(nil)),
			},
			type_system.NewUndefinedType(nil),
			type_system.NewNeverType(nil),
		),
		MutSelf: nil,
	})

	scope.Namespace.Values["console"] = &type_system.Binding{
		Source:  nil,
		Type:    type_system.NewObjectType(nil, objElems),
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

	scope.Namespace.Values["++"] = &strConcatBinding

	// Symbol object with iterator and customMatcher unique symbols
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

	scope.Namespace.Values["Symbol"] = &type_system.Binding{
		Source:  nil,
		Type:    type_system.NewObjectType(nil, symbolElems),
		Mutable: false,
	}

	preludeScope = scope
	symbolIDCounter = c.SymbolID

	return scope.WithNewScope()
}

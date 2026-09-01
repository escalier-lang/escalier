package dts_to_esc

import (
	"fmt"
	"os"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/tidwall/btree"
)

// convertCtx carries override-related state through the conversion
// pipeline. nil store / empty modulePath mean "no overrides registered"
// — Classify falls through to its built-in heuristics.
type convertCtx struct {
	// store is the merged override store consulted by Classify; nil
	// means no overrides are registered.
	store OverrideLookup

	// facts is the ECMA-262 receiver source consulted by Classify's fact
	// tier; nil means the caller supplied no spec graph.
	facts *ReceiverFacts

	// modulePath is the store key for the module being converted: ""
	// for globals/prelude lib files, the import specifier for an
	// imported package (e.g. "lodash/fp"), or the module name from a
	// `declare module "X"` wrapper.
	modulePath string

	// namespacePath is the dotted name of the enclosing `namespace`
	// chain, such as "Outer.Inner". It is empty at the module root.
	// Classify threads it into the override lookup, which needs the
	// full chain to address a member declared inside nested
	// namespaces.
	namespacePath string

	// keyDrops accumulates every singleton member flattenSingleton
	// skipped because the member's key has no plain-name form.
	// ReportSingletonKeyDrops decides which of these are expected.
	//
	// The drops belong to the module rather than to one namespace, so the
	// accumulator is shared: a context built for a nested namespace walk
	// carries the same pointer, and a drop recorded there reaches the module
	// the walk started from. It is nil for a caller that builds a context
	// itself and reads no drops back.
	keyDrops *[]SingletonMember
}

// noteSingletonKeyDrop records that flattenSingleton skipped a member
// declared under key. The singleton is named by its dotted runtime
// path, so nested singletons stay distinct from top-level ones.
func (c *convertCtx) noteSingletonKeyDrop(singleton string, key dts_parser.PropertyKey) {
	if c.keyDrops == nil {
		return
	}
	*c.keyDrops = append(*c.keyDrops, SingletonMember{
		Singleton: singleton,
		Key:       singletonKeyLabel(key),
	})
}

// singletonKeyLabel renders a property key that has no plain-name form
// so a drop report can name it: a computed key as its dotted expression
// ("Symbol.toStringTag") or its literal, a string key quoted, a numeric
// key as its literal text, and any other shape as a placeholder naming
// the node type so it cannot be mistaken for a real member key.
func singletonKeyLabel(pk dts_parser.PropertyKey) string {
	switch k := pk.(type) {
	case *dts_parser.ComputedKey:
		if dotted := exprDottedName(k.Expr); dotted != "" {
			return dotted
		}
		if lit, ok := k.Expr.(*dts_parser.LitExpr); ok {
			if inner, ok := lit.Lit.(dts_parser.PropertyKey); ok {
				return singletonKeyLabel(inner)
			}
		}
		return fmt.Sprintf("<computed %T>", k.Expr)
	case *dts_parser.StringLiteral:
		return strconv.Quote(k.Value)
	case *dts_parser.NumberLiteral:
		return strconv.FormatFloat(k.Value, 'g', -1, 64)
	}
	return fmt.Sprintf("<%T>", pk)
}

// classifyMember runs Classify for a member of the given enclosing class,
// threading the convertCtx's module/namespace/store fields through.
func (c *convertCtx) classifyMember(member dts_parser.ClassMember, className string) ClassifyResult {
	return Classify(ClassifyContext{
		Member:        member,
		ClassName:     className,
		ModulePath:    c.modulePath,
		NamespacePath: c.namespacePath,
		Store:         c.store,
		Facts:         c.facts,
	})
}

// qualifiedName constructs a qualified namespace name by appending a child name to a parent name.
// If parent is empty (root namespace), returns just the child name.
// Otherwise, returns "parent.child".
func qualifiedName(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// processNamespace recursively processes a namespace and adds declarations to the btree map.
// The name parameter is the qualified namespace name (e.g., "Foo.Bar.Baz" for nested namespaces).
// For the root/global namespace, use an empty string "".
// The inAmbientNamespace parameter is true when inside a declare namespace block;
// all declarations inside an ambient namespace are implicitly exported.
// The isExported parameter indicates whether this namespace was declared with 'export' keyword.
func processNamespace(
	cctx *convertCtx,
	name string,
	stmts []dts_parser.Statement,
	namespaces *btree.Map[string, *ast.Namespace],
	inAmbientNamespace bool,
	isExported bool,
) error {
	var decls []ast.Decl

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.NamespaceDecl:
			// Process the namespace recursively
			// If the namespace has Declare() set, it's a "declare namespace" and
			// all declarations inside are implicitly ambient/exported
			nestedName := qualifiedName(name, s.Name.Name)
			nestedAmbient := inAmbientNamespace || s.Declare()
			nestedExported := s.Export()
			nestedCctx := &convertCtx{
				store:         cctx.store,
				facts:         cctx.facts,
				modulePath:    cctx.modulePath,
				namespacePath: nestedName,
				keyDrops:      cctx.keyDrops,
			}
			if err := processNamespace(nestedCctx, nestedName, s.Statements, namespaces, nestedAmbient, nestedExported); err != nil {
				return fmt.Errorf("processing namespace %s: %w", s.Name.Name, err)
			}

		case *dts_parser.ModuleDecl:
			// Module declarations (e.g., declare module "foo") are not supported
			// since Escalier doesn't support importing other packages yet
			return fmt.Errorf("module declarations are not supported: %s", s.Name)

		case *dts_parser.ImportDecl:
			// Skip imports for now
			continue

		case *dts_parser.ExportAssignmentStmt:
			// Convert "export = identifier" to ast.ExportAssignmentStmt
			// The checker will process this to determine what gets exported
			exportAssignment := ast.NewExportAssignmentStmt(
				ast.NewIdentifier(s.Name.Name, convertSpan(s.Name.Span())),
				true, // declare is always true for .d.ts files
				convertSpan(s.Span()),
			)
			decls = append(decls, exportAssignment)
			continue

		case *dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt, *dts_parser.ExportAsNamespaceStmt:
			// Skip these - they're processed directly in the checker during inferParsedTypeDef.
			// See processExportStatements() in infer_import.go for implementation.
			continue

		default:
			// Convert regular declarations
			// Skip declarations that fail to convert (e.g., due to unsupported features)
			decl, err := convertStatement(cctx, s)
			if err != nil {
				// Log the error but continue processing other declarations
				fmt.Fprintf(os.Stderr, "Warning: skipping statement due to conversion error: %v\n", err)
				continue
			}
			if decl != nil {
				// Auto-export if inside an ambient namespace, otherwise check original export flag
				if inAmbientNamespace {
					decl.SetExport(true)
				} else if dtsDecl, ok := s.(dts_parser.Decl); ok && dtsDecl.Export() {
					decl.SetExport(true)
				}
				decls = append(decls, decl)
			}
		}
	}

	// Merge the declarations into the namespace
	if len(decls) > 0 || isExported {
		mergeNamespace(name, decls, namespaces, isExported)
	}

	return nil
}

// mergeNamespace merges declarations into an existing namespace or creates a new one.
// The name parameter is the qualified namespace name (empty string for root/global namespace).
// The isExported parameter indicates whether this namespace was declared with 'export' keyword.
func mergeNamespace(
	name string,
	decls []ast.Decl,
	namespaces *btree.Map[string, *ast.Namespace],
	isExported bool,
) {
	// Get the existing namespace if it exists
	existing, exists := namespaces.Get(name)

	if exists {
		// Merge the new declarations with existing ones
		existing.Decls = append(existing.Decls, decls...)
		// If this namespace is exported, mark it as such
		// (once exported, always exported - don't unset if previously exported)
		if isExported {
			existing.Exported = true
		}
	} else {
		// Create a new namespace
		namespace := &ast.Namespace{
			Decls:    decls,
			Exported: isExported,
		}
		namespaces.Set(name, namespace)
	}
}

// ConvertModule converts dts_parser.Module to ast.Module with no
// override store; tier-1 and tier-4 will miss for every member.
func ConvertModule(dtsModule *dts_parser.Module) (*ast.Module, error) {
	return ConvertModuleWithOverrides(dtsModule, nil, "", nil)
}

// ConvertModuleWithOverrides converts dts_parser.Module to ast.Module.
// Member mutability classification reads two sources it takes here. `store`
// answers the user overrides of tier 1 and the builtin overrides of tier 4,
// and `facts` answers the ECMA-262 receiver claims of tier 5. Either may be
// nil, which leaves that tier with nothing to answer from.
//
// `modulePath` is the path the store was keyed under for these declarations.
// It is "" for globals and prelude lib files. For an imported package's
// package decls it is the import specifier, such as "lodash/fp". For a
// `declare module "X" { ... }` block it is the module name, which the caller
// passes because the classifier strips that wrapper before getting here.
func ConvertModuleWithOverrides(dtsModule *dts_parser.Module, store OverrideLookup, modulePath string, facts *ReceiverFacts) (*ast.Module, error) {
	cctx := &convertCtx{store: store, facts: facts, modulePath: modulePath}
	var namespaces btree.Map[string, *ast.Namespace]

	// Process all statements, organizing them into namespaces
	// Use empty string "" as the root/global namespace name
	// Pass false for inAmbientNamespace since we're at the top level
	// Pass false for isExported since the root namespace is not exported
	if err := processNamespace(cctx, "", dtsModule.Statements, &namespaces, false, false); err != nil {
		return nil, fmt.Errorf("converting module: %w", err)
	}

	return ast.NewModule(namespaces), nil
}

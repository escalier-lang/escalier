package dts_parser

// FileClassification represents the classification of a .d.ts file's contents.
// It separates declarations into globals, package exports, and named modules.
type FileClassification struct {
	// HasTopLevelExports indicates whether the file has any top-level export statements
	// (export interface, export type, export function, export =, etc.)
	// If true, the file is treated as a package/module; if false, all declarations are globals.
	HasTopLevelExports bool

	// NamedModules contains declarations from `declare module "name" { ... }` blocks.
	// Each entry maps a module name to its declarations.
	// The export flag on ast.Decl distinguishes exported from non-exported declarations.
	NamedModules []NamedModuleDecl

	// GlobalDecls contains declarations that should be added to the global namespace.
	// If HasTopLevelExports is false, all non-module declarations go here.
	// If HasTopLevelExports is true, only `declare global { ... }` contents go here.
	GlobalDecls []Statement

	// PackageDecls contains all declarations when HasTopLevelExports is true.
	// The export flag on ast.Decl distinguishes exported from non-exported declarations.
	PackageDecls []Statement
}

// NamedModuleDecl represents a named module declaration (`declare module "name" { ... }`).
type NamedModuleDecl struct {
	// ModuleName is the string literal name of the module (e.g., "lodash", "lodash/fp")
	ModuleName string

	// Decls contains the declarations within the module
	// The export flag on ast.Decl distinguishes exported from non-exported declarations.
	Decls []Statement
}

// ClassifyDTSFile analyzes a parsed .d.ts module and classifies its declarations
// into globals, package declarations, and named modules.
func ClassifyDTSFile(module *Module) *FileClassification {
	classification := &FileClassification{
		HasTopLevelExports: false,
		NamedModules:       make([]NamedModuleDecl, 0),
		GlobalDecls:        make([]Statement, 0),
		PackageDecls:       make([]Statement, 0),
	}

	// First pass: detect if there are any top-level exports
	for _, stmt := range module.Statements {
		if isTopLevelExport(stmt) {
			classification.HasTopLevelExports = true
			break
		}
	}

	// Second pass: classify each declaration
	for _, stmt := range module.Statements {
		// Check for named module declarations
		if namedModule := extractNamedModule(stmt); namedModule != nil {
			classification.NamedModules = append(classification.NamedModules, *namedModule)
			continue
		}

		// Check for global augmentation (declare global { ... })
		if globalDecls := extractGlobalAugmentation(stmt); globalDecls != nil {
			classification.GlobalDecls = append(classification.GlobalDecls, globalDecls...)
			continue
		}

		// If the file has top-level exports, all declarations go to PackageDecls
		// The export flag on ast.Decl will distinguish exported from non-exported
		if classification.HasTopLevelExports {
			if isTopLevelExport(stmt) {
				// Exported declarations - expand export = Namespace if needed
				expanded := expandExportEquals(stmt, module)
				classification.PackageDecls = append(classification.PackageDecls, expanded...)
			} else {
				// Non-exported declarations also go to PackageDecls
				classification.PackageDecls = append(classification.PackageDecls, stmt)
			}
		} else {
			// No top-level exports means all declarations are globals
			classification.GlobalDecls = append(classification.GlobalDecls, stmt)
		}
	}

	return classification
}

// isTopLevelExport checks if a statement represents a top-level export.
// This includes:
// - export interface/type/function/class/const/var
// - export { ... }
// - export * from "..."
// - export = ...
// - export default ...
func isTopLevelExport(stmt Statement) bool {
	switch s := stmt.(type) {
	case *NamedExportStmt, *ExportAllStmt, *ExportAssignmentStmt, *ExportAsNamespaceStmt:
		// Standalone export statement types
		return true

	case *AmbientDecl:
		// Check if inner declaration is exported
		if decl, ok := s.Declaration.(Decl); ok {
			return decl.Export()
		}
		return false

	default:
		// Check if the statement is a declaration with Export flag set
		if decl, ok := stmt.(Decl); ok {
			return decl.Export()
		}
		return false
	}
}

// extractNamedModule extracts a named module declaration if the statement is one.
// Returns nil if the statement is not a named module declaration.
// Named modules are `declare module "name" { ... }` blocks.
func extractNamedModule(stmt Statement) *NamedModuleDecl {
	switch s := stmt.(type) {
	case *ModuleDecl:
		// ModuleDecl represents `declare module "name" { ... }`
		return &NamedModuleDecl{
			ModuleName: s.Name,
			Decls:      s.Statements,
		}

	case *AmbientDecl:
		// Check if the ambient declaration wraps a module declaration
		if moduleDecl, ok := s.Declaration.(*ModuleDecl); ok {
			return &NamedModuleDecl{
				ModuleName: moduleDecl.Name,
				Decls:      moduleDecl.Statements,
			}
		}
		return nil

	default:
		return nil
	}
}

// extractGlobalAugmentation extracts declarations from `declare global { ... }` blocks.
// Returns nil if the statement is not a global augmentation.
func extractGlobalAugmentation(stmt Statement) []Statement {
	if globalDecl, ok := stmt.(*GlobalDecl); ok {
		return globalDecl.Statements
	}
	return nil
}

// expandExportEquals handles the `export = Namespace` syntax.
// When a file uses `export = Foo` where Foo is a namespace, we need to
// treat the namespace's members as the top-level exports of the package.
//
// Example:
//
//	declare namespace Foo {
//	    export const bar: number;
//	    export function baz(): string;
//	}
//	export = Foo;
//
// This is equivalent to:
//
//	export const bar: number;
//	export function baz(): string;
//
// Returns the expanded declarations, or the original statement in a slice if not an export = pattern.
func expandExportEquals(stmt Statement, module *Module) []Statement {
	exportAssignment, ok := stmt.(*ExportAssignmentStmt)
	if !ok {
		return []Statement{stmt}
	}

	// Find the namespace/interface/type with that name in the module
	ns := findNamespaceDecl(exportAssignment.Name.Name, module)
	if ns == nil {
		// Not a namespace export, just return the original
		return []Statement{stmt}
	}

	// Return the namespace's statements as top-level exports
	// Each statement from the namespace becomes a package declaration
	return ns.Statements
}

// findNamespaceDecl searches for a namespace declaration with the given name in the module.
func findNamespaceDecl(name string, module *Module) *NamespaceDecl {
	for _, stmt := range module.Statements {
		switch s := stmt.(type) {
		case *NamespaceDecl:
			if s.Name != nil && s.Name.Name == name {
				return s
			}

		case *AmbientDecl:
			if nsDecl, ok := s.Declaration.(*NamespaceDecl); ok {
				if nsDecl.Name != nil && nsDecl.Name.Name == name {
					return nsDecl
				}
			}
		}
	}
	return nil
}

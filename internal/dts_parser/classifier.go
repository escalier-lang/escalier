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

	// Imports contains all import declarations from the file.
	// These need to be processed to load transitive dependencies.
	Imports []*ImportDecl

	// NamedExports contains export { ... } statements.
	// Local exports (From == "") mark local declarations as exported.
	// Re-exports (From != "") need module loading to copy items from another module.
	NamedExports []*NamedExportStmt

	// ExportAllStmts contains export * from statements.
	// These re-export all items from another module.
	ExportAllStmts []*ExportAllStmt

	// ExportAsNamespace contains the export as namespace statement (if present).
	// Only one such statement is valid per file (UMD pattern).
	ExportAsNamespace *ExportAsNamespaceStmt
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
		Imports:            make([]*ImportDecl, 0),
		NamedExports:       make([]*NamedExportStmt, 0),
		ExportAllStmts:     make([]*ExportAllStmt, 0),
		ExportAsNamespace:  nil,
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
		// Capture import declarations
		if importDecl, ok := stmt.(*ImportDecl); ok {
			classification.Imports = append(classification.Imports, importDecl)
			continue
		}

		// Capture named export statements (export { ... } or export { ... } from "module")
		if namedExport, ok := stmt.(*NamedExportStmt); ok {
			classification.NamedExports = append(classification.NamedExports, namedExport)
			continue
		}

		// Capture export all statements (export * from "module")
		if exportAll, ok := stmt.(*ExportAllStmt); ok {
			classification.ExportAllStmts = append(classification.ExportAllStmts, exportAll)
			continue
		}

		// Capture export as namespace statement (export as namespace MyLib)
		if exportAsNs, ok := stmt.(*ExportAsNamespaceStmt); ok {
			classification.ExportAsNamespace = exportAsNs
			continue
		}

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
		// The export flag on ast.Decl will distinguish exported from non-exported.
		// An `export = Foo` is kept as a statement, and the consumer resolves
		// what Foo names.
		if classification.HasTopLevelExports {
			classification.PackageDecls = append(classification.PackageDecls, stmt)
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
	switch stmt.(type) {
	case *NamedExportStmt, *ExportAllStmt, *ExportAssignmentStmt, *ExportAsNamespaceStmt:
		// Standalone export statement types
		return true

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
	if moduleDecl, ok := stmt.(*ModuleDecl); ok {
		return &NamedModuleDecl{
			ModuleName: moduleDecl.Name,
			Decls:      moduleDecl.Statements,
		}
	}
	return nil
}

// extractGlobalAugmentation extracts declarations from `declare global { ... }` blocks.
// Returns nil if the statement is not a global augmentation.
func extractGlobalAugmentation(stmt Statement) []Statement {
	if globalDecl, ok := stmt.(*GlobalDecl); ok {
		return globalDecl.Statements
	}
	return nil
}

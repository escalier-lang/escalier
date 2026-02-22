package interop

import (
	"fmt"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/tidwall/btree"
)

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
func processNamespace(
	name string,
	stmts []dts_parser.Statement,
	namespaces *btree.Map[string, *ast.Namespace],
) error {
	var decls []ast.Decl
	var exportAssignmentName string // Track name from "export = X" pattern

	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.NamespaceDecl:
			// Process the nested namespace recursively
			nestedName := qualifiedName(name, s.Name.Name)
			if err := processNamespace(nestedName, s.Statements, namespaces); err != nil {
				return fmt.Errorf("processing nested namespace %s: %w", s.Name.Name, err)
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
			exportAssignment := ast.NewExportAssignmentStmt(
				ast.NewIdentifier(s.Name.Name, convertSpan(s.Name.Span())),
				true, // declare is always true for .d.ts files
				convertSpan(s.Span()),
			)
			decls = append(decls, exportAssignment)
			// Track the name so we can mark the referenced declaration as exported
			exportAssignmentName = s.Name.Name
			continue

		case *dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt, *dts_parser.ExportAsNamespaceStmt:
			// Skip these export forms for now (re-exports, named exports, UMD namespace)
			continue

		case *dts_parser.AmbientDecl:
			// Unwrap and process the inner declaration
			switch inner := s.Declaration.(type) {
			case *dts_parser.NamespaceDecl:
				// Process nested namespace inside ambient declaration
				nestedName := qualifiedName(name, inner.Name.Name)
				if err := processNamespace(nestedName, inner.Statements, namespaces); err != nil {
					return fmt.Errorf("processing ambient namespace %s: %w", inner.Name.Name, err)
				}

			case *dts_parser.ModuleDecl:
				// Module declarations are not supported
				return fmt.Errorf("module declarations are not supported: %s", inner.Name)

			default:
				// Convert the ambient declaration like any other statement
				decl, err := convertStatement(inner)
				if err != nil {
					return fmt.Errorf("converting ambient declaration: %w", err)
				}
				if decl != nil {
					// Check if the inner declaration has export flag set
					if dtsDecl, ok := inner.(dts_parser.Decl); ok && dtsDecl.Export() {
						decl.SetExport(true)
					}
					decls = append(decls, decl)
				}
			}

		default:
			// Convert regular declarations
			// Skip declarations that fail to convert (e.g., due to unsupported features)
			decl, err := convertStatement(s)
			if err != nil {
				// Log the error but continue processing other declarations
				fmt.Fprintf(os.Stderr, "Warning: skipping statement due to conversion error: %v\n", err)
				continue
			}
			if decl != nil {
				// Check if the declaration has export flag set
				if dtsDecl, ok := s.(dts_parser.Decl); ok && dtsDecl.Export() {
					decl.SetExport(true)
				}
				decls = append(decls, decl)
			}
		}
	}

	// Post-process: Mark the declaration referenced by export assignment as exported
	// This handles "export = identifier" patterns - the referenced declaration
	// needs to be marked as exported for the type system to see it
	if exportAssignmentName != "" {
		for _, decl := range decls {
			if getDeclName(decl) == exportAssignmentName {
				decl.SetExport(true)
				break // Only mark the first matching declaration
			}
		}
	}

	// Merge the declarations into the namespace
	if len(decls) > 0 {
		mergeNamespace(name, decls, namespaces)
	}

	return nil
}

// getDeclName returns the name of a declaration, or empty string if unnamed.
func getDeclName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if ident, ok := d.Pattern.(*ast.IdentPat); ok {
			return ident.Name
		}
	case *ast.FuncDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.TypeDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.InterfaceDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.EnumDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	}
	return ""
}

// mergeNamespace merges declarations into an existing namespace or creates a new one.
// The name parameter is the qualified namespace name (empty string for root/global namespace).
func mergeNamespace(
	name string,
	decls []ast.Decl,
	namespaces *btree.Map[string, *ast.Namespace],
) {
	// Get the existing namespace if it exists
	existing, exists := namespaces.Get(name)

	if exists {
		// Merge the new declarations with existing ones
		existing.Decls = append(existing.Decls, decls...)
	} else {
		// Create a new namespace
		namespace := &ast.Namespace{
			Decls: decls,
		}
		namespaces.Set(name, namespace)
	}
}

// ConvertModule converts dts_parser.Module to ast.Module.
func ConvertModule(dtsModule *dts_parser.Module) (*ast.Module, error) {
	var namespaces btree.Map[string, *ast.Namespace]

	// Process all statements, organizing them into namespaces
	// Use empty string "" as the root/global namespace name
	if err := processNamespace("", dtsModule.Statements, &namespaces); err != nil {
		return nil, fmt.Errorf("converting module: %w", err)
	}

	return ast.NewModule(namespaces), nil
}

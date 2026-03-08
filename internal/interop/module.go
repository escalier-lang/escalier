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
// The inAmbientNamespace parameter is true when inside a declare namespace block;
// all declarations inside an ambient namespace are implicitly exported.
func processNamespace(
	name string,
	stmts []dts_parser.Statement,
	namespaces *btree.Map[string, *ast.Namespace],
	inAmbientNamespace bool,
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
			if err := processNamespace(nestedName, s.Statements, namespaces, nestedAmbient); err != nil {
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
			// Skip these export forms for now (re-exports, named exports, UMD namespace)
			continue

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
	if len(decls) > 0 {
		mergeNamespace(name, decls, namespaces)
	}

	return nil
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
	// Pass false for inAmbientNamespace since we're at the top level
	if err := processNamespace("", dtsModule.Statements, &namespaces, false); err != nil {
		return nil, fmt.Errorf("converting module: %w", err)
	}

	return ast.NewModule(namespaces), nil
}

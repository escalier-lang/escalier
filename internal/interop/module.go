package interop

import (
	"fmt"

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

		case *dts_parser.ExportDecl:
			// Handle export declarations
			if s.Declaration != nil {
				// Export wraps another declaration (e.g., export interface Foo {}, export namespace Bar {})
				switch inner := s.Declaration.(type) {
				case *dts_parser.NamespaceDecl:
					// Process exported namespace
					nestedName := qualifiedName(name, inner.Name.Name)
					if err := processNamespace(nestedName, inner.Statements, namespaces); err != nil {
						return fmt.Errorf("processing exported namespace %s: %w", inner.Name.Name, err)
					}

				case *dts_parser.ModuleDecl:
					// Module declarations are not supported
					return fmt.Errorf("module declarations are not supported: %s", inner.Name)

				default:
					// Convert the exported declaration like any other statement
					decl, err := convertStatement(inner)
					if err != nil {
						return fmt.Errorf("converting exported declaration: %w", err)
					}
					if decl != nil {
						decls = append(decls, decl)
					}
				}
			}
			// For other export forms (export {}, export * from "...", etc.), skip for now
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
					decls = append(decls, decl)
				}
			}

		default:
			// Convert regular declarations
			decl, err := convertStatement(s)
			if err != nil {
				return fmt.Errorf("converting statement: %w", err)
			}
			if decl != nil {
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
	if err := processNamespace("", dtsModule.Statements, &namespaces); err != nil {
		return nil, fmt.Errorf("converting module: %w", err)
	}

	return &ast.Module{Namespaces: namespaces}, nil
}

package codegen

import (
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	type_sys "github.com/escalier-lang/escalier/internal/type_system"
)

// BuildTopLevelDeclsV2 builds JavaScript code from a V2 dependency graph.
// This version uses BindingKey instead of DeclID and automatically handles
// overloaded functions and interface merging.
func (b *Builder) BuildTopLevelDeclsV2(depGraph *dep_graph.DepGraphV2) *Module {
	// Set up builder state
	b.depGraph = nil       // We're using V2, so set old depGraph to nil
	b.depGraphV2 = depGraph // Store V2 dep graph for namespace lookups
	b.isModule = true

	var stmts []Stmt

	// Build namespace hierarchy statements
	nsStmts := b.buildNamespaceStatementsV2(depGraph)
	stmts = slices.Concat(stmts, nsStmts)

	// Track which declarations we've already processed to avoid duplicates.
	// A single VarDecl with pattern destructuring introduces multiple bindings
	// but should only emit code once. For example:
	//   val C(D(msg), E(x, y)) = subject
	// This creates three binding keys in the dep_graph Decls map:
	//   "value:msg" → [VarDecl]
	//   "value:x"   → [VarDecl]
	//   "value:y"   → [VarDecl]
	// All pointing to the same VarDecl instance. When we iterate over binding keys
	// below, we'll encounter this declaration three times. We track processed
	// declarations to ensure we only emit the code once, on the first binding key
	// we encounter.
	processedDecls := make(map[ast.Decl]bool)

	// Iterate over components in topological order
	for _, component := range depGraph.Components {
		for _, key := range component {
			decls := depGraph.GetDecls(key)
			if len(decls) == 0 {
				continue
			}

			nsName := depGraph.GetNamespace(key)

			// Skip type-only bindings unless they also have a value binding
			if key.Kind() == dep_graph.DepKindType {
				// Check if there's also a value binding with the same name
				valueKey := dep_graph.ValueBindingKey(key.Name())
				if !depGraph.HasBinding(valueKey) {
					continue // Type-only, skip codegen
				}
				// If there is a value binding, that will handle codegen
				continue
			}

			// Handle multiple declarations (overloaded functions)
			if len(decls) > 1 {
				// Check if all declarations are function declarations
				allFuncs := true
				funcDecls := make([]*ast.FuncDecl, 0, len(decls))
				for _, decl := range decls {
					if fd, ok := decl.(*ast.FuncDecl); ok {
						funcDecls = append(funcDecls, fd)
					} else {
						allFuncs = false
						break
					}
				}

				if allFuncs && len(funcDecls) > 0 {
					// Build overloaded function dispatch
					stmts = slices.Concat(stmts, b.buildOverloadedFunc(funcDecls, nsName))
					continue
				}

				// For interface merging, only the first declaration is used for codegen
				// (interfaces don't generate runtime code)
			}

			// Single declaration or first of merged declarations
			decl := decls[0]

			// Skip if we've already processed this declaration
			// (VarDecls with pattern destructuring appear under multiple binding keys)
			if processedDecls[decl] {
				continue
			}
			processedDecls[decl] = true

			stmts = slices.Concat(stmts, b.buildDeclWithNamespace(decl, nsName))

			// Handle namespace assignment for namespaced bindings
			// If the binding name contains a dot, we need to assign it to the namespace
			bindingName := key.Name()
			if strings.Contains(bindingName, ".") {
				parts := strings.Split(bindingName, ".")
				dunderName := strings.Join(parts, "__")

				// Create assignment: namespace.member = dunderName
				assignExpr := NewBinaryExpr(
					NewIdentExpr(bindingName, "", nil),
					Assign,
					NewIdentExpr(dunderName, "", nil),
					nil,
				)

				stmts = append(stmts, &ExprStmt{
					Expr:   assignExpr,
					span:   nil,
					source: decl,
				})
			}
		}
	}

	if b.hasExtractor {
		// Add an import statement at the start of `stmts`
		importDecl := NewImportDecl(
			[]string{"InvokeCustomMatcherOrThrow"},
			"@escalier/runtime",
			nil,
		)
		importStmt := &DeclStmt{
			Decl:   importDecl,
			span:   nil,
			source: nil,
		}
		stmts = slices.Concat([]Stmt{importStmt}, stmts)

		// Reset hasExtractor for future builds
		b.hasExtractor = false
	}

	return &Module{
		Stmts: stmts,
	}
}

// buildNamespaceStatementsV2 generates statements to create namespace objects
// for all namespaces in the V2 dependency graph
func (b *Builder) buildNamespaceStatementsV2(depGraph *dep_graph.DepGraphV2) []Stmt {
	// Track which namespace segments have been defined to avoid redefinition
	definedNamespaces := make(map[string]bool)
	var stmts []Stmt

	// For each namespace, generate the hierarchy of statements
	for _, namespace := range depGraph.Namespaces {
		if namespace == "" {
			continue // Skip the root namespace
		}
		stmts = slices.Concat(stmts, b.buildNamespaceHierarchy(namespace, definedNamespaces))
	}

	return stmts
}

// BuildDefinitionsV2 builds TypeScript .d.ts definitions from a V2 dependency graph.
// This version uses BindingKey instead of DeclID.
func (b *Builder) BuildDefinitionsV2(
	depGraph *dep_graph.DepGraphV2,
	moduleNS *type_sys.Namespace,
) *Module {
	// Group declarations by namespace
	namespaceGroups := make(map[string][]dep_graph.BindingKey)

	// Collect all binding keys in topological order
	var topoBindingKeys []dep_graph.BindingKey
	for _, component := range depGraph.Components {
		topoBindingKeys = append(topoBindingKeys, component...)
	}

	// Group binding keys by their namespace
	for _, key := range topoBindingKeys {
		namespace := depGraph.GetNamespace(key)
		namespaceGroups[namespace] = append(namespaceGroups[namespace], key)
	}

	// Build statements for each namespace
	stmts := []Stmt{}

	// Sort namespace names for consistent output
	var namespaceNames []string
	for namespace := range namespaceGroups {
		namespaceNames = append(namespaceNames, namespace)
	}
	sort.Strings(namespaceNames)

	// Track which declarations we've already processed to avoid duplicates
	// (enums and classes have both type and value bindings pointing to the same decl)
	processedDecls := make(map[ast.Decl]bool)

	for _, namespace := range namespaceNames {
		bindingKeys := namespaceGroups[namespace]

		if namespace == "" {
			// Root namespace declarations go directly to module level
			for _, key := range bindingKeys {
				decls := depGraph.GetDecls(key)
				if len(decls) == 0 {
					continue
				}

				// For multiple declarations (overloads, interface merging), process all of them
				for _, decl := range decls {
					// Skip if we've already processed this declaration
					if processedDecls[decl] {
						continue
					}
					processedDecls[decl] = true

					declStmts := b.buildDeclStmt(decl, moduleNS, true)
					if len(declStmts) != 0 {
						stmts = append(stmts, declStmts...)
					}
				}
			}
		} else {
			// Non-root namespace declarations go inside namespace blocks
			namespaceStmts := []Stmt{}
			for _, key := range bindingKeys {
				decls := depGraph.GetDecls(key)
				if len(decls) == 0 {
					continue
				}

				// Find the nested namespace in moduleNS based on the namespace string
				nestedNS := findNamespace(moduleNS, namespace)
				if nestedNS == nil {
					// If the nested namespace doesn't exist, fall back to the module namespace
					nestedNS = moduleNS
				}

				// For multiple declarations (overloads, interface merging), process all of them
				for _, decl := range decls {
					// Skip if we've already processed this declaration
					if processedDecls[decl] {
						continue
					}
					processedDecls[decl] = true

					declStmts := b.buildDeclStmt(decl, nestedNS, false)
					if len(declStmts) != 0 {
						namespaceStmts = append(namespaceStmts, declStmts...)
					}
				}
			}

			if len(namespaceStmts) > 0 {
				namespaceDecl := b.buildNamespaceDecl(namespace, namespaceStmts)
				stmts = append(stmts, &DeclStmt{
					Decl:   namespaceDecl,
					span:   nil,
					source: nil,
				})
			}
		}
	}

	return &Module{Stmts: stmts}
}

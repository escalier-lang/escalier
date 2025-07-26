package dep_graph

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/tidwall/btree"
)

type DepKind int

const (
	DepKindValue DepKind = iota
	DepKindType
)

type DepBinding struct {
	Name string
	Kind DepKind
}

// DeclID represents a unique identifier for each declaration
type DeclID int

// ModuleBindingVisitor collects all declarations with unique IDs and their bindings
type ModuleBindingVisitor struct {
	ast.DefaulVisitor
	Decls         btree.Map[DeclID, ast.Decl] // Map from unique ID to declaration
	ValueBindings btree.Map[string, DeclID]   // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID]   // Map from type binding name to declaration ID
	nextDeclID    DeclID                      // Next unique ID to assign
	currentNSName string                      // Current namespace being visited
}

func (v *ModuleBindingVisitor) generateDeclID() DeclID {
	// Generate a unique ID for this declaration
	id := v.nextDeclID
	v.nextDeclID++
	return id
}

// EnterDecl visits declarations and assigns unique IDs
func (v *ModuleBindingVisitor) EnterDecl(decl ast.Decl) bool {
	// Generate a unique ID for this declaration
	declID := v.generateDeclID()
	v.Decls.Set(declID, decl)

	switch d := decl.(type) {
	case *ast.VarDecl:
		// Extract bindings from the pattern
		bindingNames := ast.FindBindings(d.Pattern)
		for name := range bindingNames {
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			v.ValueBindings.Set(name, declID)
		}
	case *ast.FuncDecl:
		// Function declarations introduce a binding with the function name
		if d.Name != nil && d.Name.Name != "" {
			name := d.Name.Name
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			v.ValueBindings.Set(name, declID)
		}
	case *ast.TypeDecl:
		// Type declarations introduce a binding with the type name
		if d.Name != nil && d.Name.Name != "" {
			name := d.Name.Name
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			v.TypeBindings.Set(name, declID)
		}
	}
	return false // Don't traverse into the declaration's body
}

// Other visitor methods should return false to avoid traversing into nested structures
func (v *ModuleBindingVisitor) EnterStmt(stmt ast.Stmt) bool               { return false }
func (v *ModuleBindingVisitor) EnterExpr(expr ast.Expr) bool               { return false }
func (v *ModuleBindingVisitor) EnterPat(pat ast.Pat) bool                  { return false }
func (v *ModuleBindingVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool { return false }
func (v *ModuleBindingVisitor) EnterTypeAnn(t ast.TypeAnn) bool            { return false }
func (v *ModuleBindingVisitor) EnterLit(lit ast.Lit) bool                  { return false }
func (v *ModuleBindingVisitor) EnterBlock(block ast.Block) bool            { return false }

// FindModuleBindings returns all bindings and declarations in a module with unique IDs
func FindModuleBindings(module *ast.Module) (btree.Map[DeclID, ast.Decl], btree.Map[string, DeclID], btree.Map[string, DeclID]) {
	var decls btree.Map[DeclID, ast.Decl]
	var valueBindings btree.Map[string, DeclID]
	var typeBindings btree.Map[string, DeclID]
	visitor := &ModuleBindingVisitor{
		DefaulVisitor: ast.DefaulVisitor{},
		Decls:         decls,
		ValueBindings: valueBindings,
		TypeBindings:  typeBindings,
		nextDeclID:    1,  // Start IDs from 1
		currentNSName: "", // Default namespace
	}

	// Visit all declarations in the module
	iter := module.Namespaces.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		nsName := iter.Key()
		ns := iter.Value()
		for _, decl := range ns.Decls {
			visitor.currentNSName = nsName
			decl.Accept(visitor)
		}
	}

	return visitor.Decls, visitor.ValueBindings, visitor.TypeBindings
}

// DependencyVisitor finds IdentExpr dependencies in a declaration while tracking scope
type DependencyVisitor struct {
	ast.DefaulVisitor
	DepGraph         *DepGraph         // The dependency graph containing all module bindings
	Dependencies     btree.Set[DeclID] // Found dependencies by declaration ID
	LocalBindings    []set.Set[string] // Stack of local scopes (still strings for local scope)
	CurrentNamespace string            // Current namespace being analyzed
}

// EnterStmt handles statements that introduce new scopes
func (v *DependencyVisitor) EnterStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.DeclStmt:
		// Declaration statement introduces bindings in the current scope
		if len(v.LocalBindings) > 0 {
			switch decl := s.Decl.(type) {
			case *ast.VarDecl:
				bindings := ast.FindBindings(decl.Pattern)
				for binding := range bindings {
					v.LocalBindings[len(v.LocalBindings)-1].Add(binding)
				}
			case *ast.FuncDecl:
				// Function declarations introduce a binding with the function name
				if decl.Name != nil && decl.Name.Name != "" {
					v.LocalBindings[len(v.LocalBindings)-1].Add(decl.Name.Name)
				}
			}
		}
		return true
	default:
		return true
	}
}

// ExitStmt handles exiting scopes
func (v *DependencyVisitor) ExitStmt(stmt ast.Stmt) {
	// No special handling needed for statements since blocks are handled in expressions
}

// EnterExpr handles expressions that might introduce scope or contain dependencies
func (v *DependencyVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// Check if this identifier is a valid dependency
		// If we're in a non-empty namespace, first try the qualified name (current namespace)
		if v.CurrentNamespace != "" {
			qualifiedName := v.CurrentNamespace + "." + e.Name
			if declID, exists := v.DepGraph.ValueBindings.Get(qualifiedName); exists &&
				!v.isLocalBinding(e.Name) {
				e.Namespace = v.DepGraph.GetNamespaceID(v.CurrentNamespace) // Allows us to codegen a fully qualified name
				v.Dependencies.Insert(declID)
				return false
			}
		}
		// Then try the unqualified name (global namespace or explicit global reference)
		if declID, exists := v.DepGraph.ValueBindings.Get(e.Name); exists &&
			!v.isLocalBinding(e.Name) {
			v.Dependencies.Insert(declID)
			return false
		}
		return false // Don't traverse into IdentExpr
	case *ast.MemberExpr:
		// For member expressions like obj.prop, check if the full qualified name exists in bindings
		qualifiedName := v.buildQualifiedName(e)
		if qualifiedName != "" {
			// Check if the qualified name is a valid value dependency
			if declID, exists := v.DepGraph.ValueBindings.Get(qualifiedName); exists &&
				!v.isLocalBinding(qualifiedName) {
				v.Dependencies.Insert(declID)
				return false // Don't traverse further since we found the qualified dependency
			}
			// NOTE: MemberExprs are value-only AST nodes so we don't bother
			// checking if it's a type dependency.
		}
		// If no qualified name match, continue traversing to find dependencies in sub-expressions
		return true
	case *ast.FuncExpr:
		// Function expression introduces a new scope for parameters
		v.pushScope()
		// Add parameters to the current scope
		if len(v.LocalBindings) > 0 {
			for _, param := range e.Params {
				bindings := ast.FindBindings(param.Pattern)
				for binding := range bindings {
					v.LocalBindings[len(v.LocalBindings)-1].Add(binding)
				}
			}
		}
		return true
	default:
		return true
	}
}

// ExitExpr handles exiting expression scopes
func (v *DependencyVisitor) ExitExpr(expr ast.Expr) {
	switch expr.(type) {
	case *ast.FuncExpr:
		// Pop the scope when exiting a function expression
		v.popScope()
	}
}

// EnterPat handles patterns that might introduce bindings
func (v *DependencyVisitor) EnterPat(pat ast.Pat) bool {
	// For patterns in function parameters, bindings are already handled in EnterExpr
	// For patterns in variable declarations, bindings are handled in EnterStmt
	return true
}

// EnterTypeAnn handles type annotations that might contain dependencies
func (v *DependencyVisitor) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	switch t := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		// Check if this type reference is a valid dependency
		typeName := ast.QualIdentToString(t.Name)
		// If we're in a non-empty namespace, first try the qualified name (current namespace)
		if v.CurrentNamespace != "" {
			qualifiedTypeName := v.CurrentNamespace + "." + typeName
			if declID, exists := v.DepGraph.TypeBindings.Get(qualifiedTypeName); exists &&
				!v.isLocalBinding(typeName) {
				v.Dependencies.Insert(declID)
				return true
			}
		}
		// Then try the unqualified name (global namespace or explicit global reference)
		if declID, exists := v.DepGraph.TypeBindings.Get(typeName); exists &&
			!v.isLocalBinding(typeName) {
			v.Dependencies.Insert(declID)
			return true
		}
		return true // Continue traversing type arguments
	case *ast.ObjectTypeAnn:
		// Handle object type annotations which may contain computed keys
		for _, elem := range t.Elems {
			if prop, ok := elem.(*ast.PropertyTypeAnn); ok {
				// Check if the property key is a computed key that references other bindings
				if compKey, ok := prop.Name.(*ast.ComputedKey); ok {
					// Traverse the computed key expression to find dependencies
					compKey.Expr.Accept(v)
				}
			}
		}
		return true // Continue traversing other elements
	default:
		return true
	}
}

// EnterBlock handles entering blocks which introduce new scopes
func (v *DependencyVisitor) EnterBlock(block ast.Block) bool {
	v.pushScope()
	return true
}

// ExitBlock handles exiting blocks which end scopes
func (v *DependencyVisitor) ExitBlock(block ast.Block) {
	v.popScope()
}

func (v *DependencyVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	// TODO: Figure out a better solution for dealing with property shorthand.
	if prop, ok := elem.(*ast.PropertyExpr); ok {
		if prop.Value == nil {
			switch key := prop.Name.(type) {
			case *ast.IdentExpr:
				// Check if this identifier is a valid dependency
				if declID, exists := v.DepGraph.ValueBindings.Get(key.Name); exists &&
					!v.isLocalBinding(key.Name) {
					v.Dependencies.Insert(declID)
				}
			}
			return false // Don't traverse into IdentExpr
		}
	}

	return true
}

// pushScope adds a new local scope
func (v *DependencyVisitor) pushScope() {
	v.LocalBindings = append(v.LocalBindings, set.NewSet[string]())
}

// popScope removes the current local scope
func (v *DependencyVisitor) popScope() {
	if len(v.LocalBindings) > 0 {
		v.LocalBindings = v.LocalBindings[:len(v.LocalBindings)-1]
	}
}

// isLocalBinding checks if a binding is in any local scope
func (v *DependencyVisitor) isLocalBinding(name string) bool {
	for _, scope := range v.LocalBindings {
		if scope.Contains(name) {
			return true
		}
	}
	return false
}

// FindDeclDependencies finds all IdentExpr dependencies in a declaration
// that are valid module-level bindings, while properly handling scope
func FindDeclDependencies(
	decl ast.Decl,
	depGraph *DepGraph,
	currentNamespace string,
) btree.Set[DeclID] {
	var dependencies btree.Set[DeclID]
	visitor := &DependencyVisitor{
		DefaulVisitor:    ast.DefaulVisitor{},
		DepGraph:         depGraph,
		Dependencies:     dependencies,
		LocalBindings:    make([]set.Set[string], 0),
		CurrentNamespace: currentNamespace,
	}

	// Handle different declaration types
	switch d := decl.(type) {
	case *ast.VarDecl:
		// For variable declarations, visit the type annotation if present
		if d.TypeAnn != nil {
			d.TypeAnn.Accept(visitor)
		}
		// Visit the initializer if present
		if d.Init != nil {
			d.Init.Accept(visitor)
		}
	case *ast.FuncDecl:
		// For function declarations, create a scope for parameters and visit the body
		if d.Body != nil {
			visitor.pushScope()
			// Add parameters to the function scope
			for _, param := range d.Params {
				bindings := ast.FindBindings(param.Pattern)
				for binding := range bindings {
					visitor.LocalBindings[len(visitor.LocalBindings)-1].Add(binding)
				}
			}
			// Visit the function body (block scope will be handled by EnterBlock/ExitBlock)
			d.Body.Accept(visitor)
			visitor.popScope()
		}
	case *ast.TypeDecl:
		// For type declarations, visit the type annotation
		d.TypeAnn.Accept(visitor)
	}

	return visitor.Dependencies
}

type DepGraph struct {
	// We use a btree.Map because insert order is not guaranteed in Go maps,
	// and we want to maintain a consistent order for declarations.  This is so
	// that codegen is deterministic.
	// NOTE: Binding names are fully qualified.
	Decls         btree.Map[DeclID, ast.Decl]          // All declarations in the module
	Deps          btree.Map[DeclID, btree.Set[DeclID]] // Dependencies for each declaration ID
	ValueBindings btree.Map[string, DeclID]            // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID]            // Map from type binding name to declaration ID
	DeclNamespace btree.Map[DeclID, string]            // Map from declaration ID to namespace
	NamespaceMap  []string                             // Index is the NamespaceID, value is the namespace string
}

// NewDepGraph creates a new DepGraph with initialized empty maps.
// This constructor ensures all required maps are properly initialized
// and provides a consistent way to create DepGraph instances.
func NewDepGraph(namespaceMap []string) *DepGraph {
	return &DepGraph{
		Decls:         btree.Map[DeclID, ast.Decl]{},
		Deps:          btree.Map[DeclID, btree.Set[DeclID]]{},
		ValueBindings: btree.Map[string, DeclID]{},
		TypeBindings:  btree.Map[string, DeclID]{},
		DeclNamespace: btree.Map[DeclID, string]{},
		NamespaceMap:  namespaceMap,
	}
}

// collectNamespaces collects all namespace names from a module and returns a namespace map
func collectNamespaces(module *ast.Module) []string {
	namespaceMap := make([]string, 1) // Start with capacity for root namespace
	namespaceMap[0] = ""              // Register root namespace at index 0

	nsIter := module.Namespaces.Iter()
	for ok := nsIter.First(); ok; ok = nsIter.Next() {
		nsName := nsIter.Key()
		// Check if namespace already exists
		if !slices.Contains(namespaceMap, nsName) {
			// Add new namespace
			namespaceMap = append(namespaceMap, nsName)
		}
	}

	return namespaceMap
}

// BuildDepGraph builds a dependency graph for a module
func BuildDepGraph(module *ast.Module) *DepGraph {
	// Collect all namespaces from the module
	namespaceMap := collectNamespaces(module)

	// Create a DepGraph with initialized maps and namespaces
	depGraph := NewDepGraph(namespaceMap)

	// Find all decls and bindings in the module
	decls, valueBindings, typeBindings := FindModuleBindings(module)

	var declNamespace btree.Map[DeclID, string]

	// We need to track which namespace each declaration belongs to
	// Create a map from DeclID to namespace by re-traversing the module
	nextDeclID := DeclID(1)
	nsIterForDecls := module.Namespaces.Iter()
	for ok := nsIterForDecls.First(); ok; ok = nsIterForDecls.Next() {
		nsName := nsIterForDecls.Key()
		ns := nsIterForDecls.Value()
		for range ns.Decls {
			declNamespace.Set(nextDeclID, nsName)
			nextDeclID++
		}
	}

	// Populate the DepGraph with declarations and bindings
	depGraph.Decls = decls
	depGraph.ValueBindings = valueBindings
	depGraph.TypeBindings = typeBindings
	depGraph.DeclNamespace = declNamespace

	// For each declaration, find its dependencies
	declIter := decls.Iter()
	for ok := declIter.First(); ok; ok = declIter.Next() {
		declID := declIter.Key()
		decl := declIter.Value()
		namespace, _ := declNamespace.Get(declID)
		dependencies := FindDeclDependencies(decl, depGraph, namespace)
		depGraph.Deps.Set(declID, dependencies)
	}

	return depGraph
}

// GetDependencies returns the dependencies for a given declaration ID
func (g *DepGraph) GetDependencies(declID DeclID) btree.Set[DeclID] {
	if deps, exists := g.Deps.Get(declID); exists {
		return deps
	}
	var result btree.Set[DeclID]
	return result
}

// GetDeclaration returns the declaration for a given declaration ID
func (g *DepGraph) GetDeclaration(declID DeclID) (ast.Decl, bool) {
	return g.Decls.Get(declID)
}

// GetNamespace returns the namespace for a given declaration ID
func (g *DepGraph) GetNamespace(declID DeclID) (string, bool) {
	return g.DeclNamespace.Get(declID)
}

// GetNamespaceID returns the namespace ID for a given namespace string
func (g *DepGraph) GetNamespaceID(namespace string) ast.NamespaceID {
	// Check if namespace exists, return 0 if not found
	for i, ns := range g.NamespaceMap {
		if ns == namespace {
			return ast.NamespaceID(i)
		}
	}
	return 0 // Return 0 (root namespace) if not found
}

// GetNamespaceString returns the namespace string for a given namespace ID
func (g *DepGraph) GetNamespaceString(id ast.NamespaceID) string {
	if int(id) < len(g.NamespaceMap) {
		return g.NamespaceMap[id]
	}
	return ""
}

// AllDeclarations returns all declaration IDs in the graph
func (g *DepGraph) AllDeclarations() []DeclID {
	declIDs := make([]DeclID, 0, g.Decls.Len())
	iter := g.Decls.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		declID := iter.Key()
		declIDs = append(declIDs, declID)
	}
	return declIDs
}

// AllNamespaces returns all unique namespace names in the graph
func (g *DepGraph) AllNamespaces() []string {
	namespaces := set.NewSet[string]()
	iter := g.DeclNamespace.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		namespace := iter.Value()
		namespaces.Add(namespace)
	}

	return namespaces.ToSlice()
}

// buildQualifiedName constructs a qualified name from a MemberExpr chain
// Returns empty string if the expression doesn't form a valid qualified identifier chain
func (v *DependencyVisitor) buildQualifiedName(expr *ast.MemberExpr) string {
	parts := make([]string, 0)

	// Walk the chain backwards, collecting property names
	current := expr
	for current != nil {
		if current.Prop == nil {
			return "" // Invalid member expression
		}
		parts = append([]string{current.Prop.Name}, parts...) // Prepend to build left-to-right

		// Check if the object is another MemberExpr
		if memberObj, ok := current.Object.(*ast.MemberExpr); ok {
			current = memberObj
		} else if identObj, ok := current.Object.(*ast.IdentExpr); ok {
			// Base case: we've reached an identifier
			parts = append([]string{identObj.Name}, parts...) // Prepend the base identifier
			break
		} else {
			// Not a simple qualified name chain (e.g., function call result, complex expression)
			return ""
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Build the qualified name using strings.Builder for efficient concatenation
	var builder strings.Builder
	builder.WriteString(parts[0])
	for i := 1; i < len(parts); i++ {
		builder.WriteByte('.')
		builder.WriteString(parts[i])
	}
	return builder.String()
}

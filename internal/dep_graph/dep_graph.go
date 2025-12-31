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
	ast.DefaultVisitor
	Decls         []ast.Decl                // Slice of declarations, indexed by DeclID
	ValueBindings btree.Map[string, DeclID] // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID] // Map from type binding name to declaration ID
	nextDeclID    DeclID                    // Next unique ID to assign
	currentNSName string                    // Current namespace being visited
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
	v.Decls = append(v.Decls, decl) // Append to slice, DeclID will be the index

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
	case *ast.ClassDecl:
		// Class declarations introduce both a type binding and a value binding (constructor)
		if d.Name != nil && d.Name.Name != "" {
			name := d.Name.Name
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			// Classes introduce both a type binding (for the class type) and a value binding (for the constructor)
			v.TypeBindings.Set(name, declID)
			v.ValueBindings.Set(name, declID)
		}
	case *ast.InterfaceDecl:
		// Interface declarations introduce a type binding
		if d.Name != nil && d.Name.Name != "" {
			name := d.Name.Name
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			v.TypeBindings.Set(name, declID)
		}
	case *ast.EnumDecl:
		// Enum declarations introduce both a type binding and a value binding (for the variants)
		if d.Name != nil && d.Name.Name != "" {
			name := d.Name.Name
			if v.currentNSName != "" {
				name = v.currentNSName + "." + name // Fully qualify with namespace
			}
			v.TypeBindings.Set(name, declID)
			v.ValueBindings.Set(name, declID)
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
func FindModuleBindings(module *ast.Module) ([]ast.Decl, btree.Map[string, DeclID], btree.Map[string, DeclID]) {
	var decls []ast.Decl
	var valueBindings btree.Map[string, DeclID]
	var typeBindings btree.Map[string, DeclID]
	visitor := &ModuleBindingVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Decls:          decls,
		ValueBindings:  valueBindings,
		TypeBindings:   typeBindings,
		nextDeclID:     0,  // Start IDs from 0
		currentNSName:  "", // Default namespace
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

// LocalScope represents a single scope with separate value and type bindings
type LocalScope struct {
	ValueBindings set.Set[string] // Local value bindings in this scope
	TypeBindings  set.Set[string] // Local type bindings in this scope
}

// DependencyVisitor finds IdentExpr dependencies in a declaration while tracking scope
type DependencyVisitor struct {
	ast.DefaultVisitor
	ValueBindings    btree.Map[string, DeclID]  // Map from value binding name to declaration ID
	TypeBindings     btree.Map[string, DeclID]  // Map from type binding name to declaration ID
	NamespaceMap     map[string]ast.NamespaceID // Map from namespace name to ID
	Dependencies     btree.Set[DeclID]          // Found dependencies by declaration ID
	LocalScopes      []LocalScope               // Stack of local scopes with separate value/type bindings
	CurrentNamespace string                     // Current namespace being analyzed
}

// EnterStmt handles statements that introduce new scopes
func (v *DependencyVisitor) EnterStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.DeclStmt:
		// Declaration statement introduces bindings in the current scope
		if len(v.LocalScopes) > 0 {
			currentScope := &v.LocalScopes[len(v.LocalScopes)-1]
			switch decl := s.Decl.(type) {
			case *ast.VarDecl:
				bindings := ast.FindBindings(decl.Pattern)
				for binding := range bindings {
					currentScope.ValueBindings.Add(binding)
				}
			case *ast.FuncDecl:
				// Function declarations introduce a binding with the function name
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.ValueBindings.Add(decl.Name.Name)
				}
			case *ast.TypeDecl:
				// Type declarations introduce a binding with the type name
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
				}
			case *ast.InterfaceDecl:
				// Interface declarations introduce a binding with the interface name
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
				}
			case *ast.EnumDecl:
				// Enum declarations introduce both a type binding and value binding
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
					currentScope.ValueBindings.Add(decl.Name.Name)
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
			if declID, exists := v.ValueBindings.Get(qualifiedName); exists &&
				!v.isLocalValueBinding(e.Name) {
				e.Namespace = v.NamespaceMap[v.CurrentNamespace] // Allows us to codegen a fully qualified name
				v.Dependencies.Insert(declID)
				return false
			}
		}
		// Then try the unqualified name (global namespace or explicit global reference)
		if declID, exists := v.ValueBindings.Get(e.Name); exists &&
			!v.isLocalValueBinding(e.Name) {
			v.Dependencies.Insert(declID)
			return false
		}
		return false // Don't traverse into IdentExpr
	case *ast.MemberExpr:
		// For member expressions like obj.prop, check if the full qualified name exists in bindings
		qualifiedName := v.buildQualifiedName(e)
		if qualifiedName != "" {
			// Check if the qualified name is a valid value dependency
			if declID, exists := v.ValueBindings.Get(qualifiedName); exists &&
				!v.isLocalValueBinding(qualifiedName) {
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
		if len(v.LocalScopes) > 0 {
			currentScope := &v.LocalScopes[len(v.LocalScopes)-1]
			for _, param := range e.Params {
				bindings := ast.FindBindings(param.Pattern)
				for binding := range bindings {
					currentScope.ValueBindings.Add(binding)
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
			if declID, exists := v.TypeBindings.Get(qualifiedTypeName); exists &&
				!v.isLocalTypeBinding(typeName) {
				v.Dependencies.Insert(declID)
				return true
			}
		}
		// Then try the unqualified name (global namespace or explicit global reference)
		if declID, exists := v.TypeBindings.Get(typeName); exists &&
			!v.isLocalTypeBinding(typeName) {
			v.Dependencies.Insert(declID)
			return true
		}
		return true // Continue traversing type arguments
	case *ast.TypeOfTypeAnn:
		// For typeof expressions, we need to track dependencies on the value binding
		// For qualified identifiers like shapes.unitCircle.x, we need to find the
		// base value binding (e.g., shapes or shapes.unitCircle)
		qualName := ast.QualIdentToString(t.Value)

		// Try progressively shorter qualified names to find the dependency
		// For "shapes.unitCircle.x", try: "shapes.unitCircle.x", then "shapes.unitCircle", then "shapes"
		parts := strings.Split(qualName, ".")
		for i := len(parts); i > 0; i-- {
			candidateName := strings.Join(parts[:i], ".")

			// Try with current namespace prefix first
			if v.CurrentNamespace != "" {
				qualifiedName := v.CurrentNamespace + "." + candidateName
				if declID, exists := v.ValueBindings.Get(qualifiedName); exists &&
					!v.isLocalValueBinding(candidateName) {
					v.Dependencies.Insert(declID)
					break
				}
			}

			// Try without namespace prefix
			if declID, exists := v.ValueBindings.Get(candidateName); exists &&
				!v.isLocalValueBinding(candidateName) {
				v.Dependencies.Insert(declID)
				break
			}
		}
		return true
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
				if declID, exists := v.ValueBindings.Get(key.Name); exists &&
					!v.isLocalValueBinding(key.Name) {
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
	newScope := LocalScope{
		ValueBindings: set.NewSet[string](),
		TypeBindings:  set.NewSet[string](),
	}
	v.LocalScopes = append(v.LocalScopes, newScope)
}

// popScope removes the current local scope
func (v *DependencyVisitor) popScope() {
	if len(v.LocalScopes) > 0 {
		v.LocalScopes = v.LocalScopes[:len(v.LocalScopes)-1]
	}
}

// isLocalValueBinding checks if a binding is a value binding in any local scope
func (v *DependencyVisitor) isLocalValueBinding(name string) bool {
	for _, scope := range v.LocalScopes {
		if scope.ValueBindings.Contains(name) {
			return true
		}
	}
	return false
}

// isLocalTypeBinding checks if a binding is a type binding in any local scope
func (v *DependencyVisitor) isLocalTypeBinding(name string) bool {
	for _, scope := range v.LocalScopes {
		if scope.TypeBindings.Contains(name) {
			return true
		}
	}
	return false
}

// FindDeclDependencies finds all IdentExpr dependencies in a declaration
// that are valid module-level bindings, while properly handling scope
func FindDeclDependencies(
	declID DeclID,
	depGraph *DepGraph,
) btree.Set[DeclID] {
	decl, _ := depGraph.GetDecl(declID)
	currentNamespace := depGraph.DeclNamespace[declID]

	namespaceMap := make(map[string]ast.NamespaceID)
	for i, nsName := range depGraph.Namespaces {
		namespaceMap[nsName] = ast.NamespaceID(i)
	}

	var dependencies btree.Set[DeclID]
	visitor := &DependencyVisitor{
		DefaultVisitor:   ast.DefaultVisitor{},
		ValueBindings:    depGraph.ValueBindings,
		TypeBindings:     depGraph.TypeBindings,
		NamespaceMap:     namespaceMap,
		Dependencies:     dependencies,
		CurrentNamespace: currentNamespace,
		LocalScopes:      make([]LocalScope, 0),
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
		for _, tp := range d.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(visitor)
			}
			if tp.Default != nil {
				tp.Default.Accept(visitor)
			}
		}
		// Visit parameter type annotations (if any)
		for _, param := range d.Params {
			if param.TypeAnn != nil {
				param.TypeAnn.Accept(visitor)
			}
		}
		// Visit return type annotation (if any)
		if d.Return != nil {
			d.Return.Accept(visitor)
		}
		// Visit throws type annotation (if any)
		if d.Throws != nil {
			d.Throws.Accept(visitor)
		}
		// For function declarations, create a scope for parameters and visit the body
		if d.Body != nil {
			visitor.pushScope()
			// Add parameters to the function scope
			if len(visitor.LocalScopes) > 0 {
				currentScope := &visitor.LocalScopes[len(visitor.LocalScopes)-1]
				for _, param := range d.Params {
					bindings := ast.FindBindings(param.Pattern)
					for binding := range bindings {
						currentScope.ValueBindings.Add(binding)
					}
				}
			}
			// Visit the function body (block scope will be handled by EnterBlock/ExitBlock)
			d.Body.Accept(visitor)
			visitor.popScope()
		}
	case *ast.TypeDecl:
		// For type declarations, visit the type annotation
		for _, tp := range d.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(visitor)
			}
			if tp.Default != nil {
				tp.Default.Accept(visitor)
			}
		}
		d.TypeAnn.Accept(visitor)
	case *ast.InterfaceDecl:
		// For interface declarations, visit the type annotation
		for _, tp := range d.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(visitor)
			}
			if tp.Default != nil {
				tp.Default.Accept(visitor)
			}
		}
		d.TypeAnn.Accept(visitor)
	case *ast.EnumDecl:
		// For enum declarations, visit enum elements for dependencies
		for _, elem := range d.Elems {
			if spread, ok := elem.(*ast.EnumSpread); ok {
				// EnumSpread references another enum, add it as a dependency
				if spread.Arg != nil {
					enumName := spread.Arg.Name
					// Try with current namespace prefix first
					if currentNamespace != "" {
						qualifiedName := currentNamespace + "." + enumName
						if declID, exists := depGraph.TypeBindings.Get(qualifiedName); exists {
							visitor.Dependencies.Insert(declID)
							continue
						}
					}
					// Try without namespace prefix
					if declID, exists := depGraph.TypeBindings.Get(enumName); exists {
						visitor.Dependencies.Insert(declID)
					}
				}
			} else if variant, ok := elem.(*ast.EnumVariant); ok {
				// Visit variant parameters to find type dependencies
				for _, param := range variant.Params {
					if param.TypeAnn != nil {
						param.TypeAnn.Accept(visitor)
					}
				}
			}
		}
	case *ast.ClassDecl:
		// panic("TODO: handle ClassDecls in FindDeclDependencies")
	}

	return visitor.Dependencies
}

type DepGraph struct {
	// We use a slice to maintain a consistent order for declarations.  This is so
	// that codegen is deterministic. DeclID values start from 0, so we use DeclID
	// as the slice index directly.
	// NOTE: Binding names are fully qualified.
	Decls         []ast.Decl                // All declarations in the module, indexed by DeclID
	DeclDeps      []btree.Set[DeclID]       // Dependencies for each declaration, indexed by DeclID
	DeclNamespace []string                  // Namespace for each declaration, indexed by DeclID
	Components    [][]DeclID                // Strongly connected components of declarations
	Namespaces    []string                  // Index is the NamespaceID, value is the namespace string
	ValueBindings btree.Map[string, DeclID] // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID] // Map from type binding name to declaration ID
}

// NewDepGraph creates a new DepGraph with initialized empty maps.
// This constructor ensures all required maps are properly initialized
// and provides a consistent way to create DepGraph instances.
func NewDepGraph(namespaceMap []string) *DepGraph {
	return &DepGraph{
		Decls:         []ast.Decl{},
		DeclDeps:      []btree.Set[DeclID]{},
		DeclNamespace: []string{},
		Components:    [][]DeclID{},
		Namespaces:    namespaceMap,
		ValueBindings: btree.Map[string, DeclID]{},
		TypeBindings:  btree.Map[string, DeclID]{},
	}
}

// collectNamespaces collects all namespace names from a module and returns a namespace map
func collectNamespaces(module *ast.Module) []string {
	namespaces := make([]string, 1) // Start with capacity for root namespace
	namespaces[0] = ""              // Register root namespace at index 0

	nsIter := module.Namespaces.Iter()
	for ok := nsIter.First(); ok; ok = nsIter.Next() {
		nsName := nsIter.Key()
		// Check if namespace already exists
		if !slices.Contains(namespaces, nsName) {
			// Add new namespace
			namespaces = append(namespaces, nsName)
		}
	}

	return namespaces
}

// BuildDepGraph builds a dependency graph for a module
func BuildDepGraph(module *ast.Module) *DepGraph {
	// Find all decls and bindings in the module
	decls, valueBindings, typeBindings := FindModuleBindings(module)

	// We need to track which namespace each declaration belongs to
	// Create a slice with space for all declarations
	declNamespace := make([]string, len(decls))

	// We need to track which namespace each declaration belongs to
	// Create a map from DeclID to namespace by re-traversing the module
	var nextDeclID DeclID
	nsIterForDecls := module.Namespaces.Iter()
	for ok := nsIterForDecls.First(); ok; ok = nsIterForDecls.Next() {
		nsName := nsIterForDecls.Key()
		ns := nsIterForDecls.Value()
		for range ns.Decls {
			declNamespace[nextDeclID] = nsName // Use DeclID as slice index
			nextDeclID++
		}
	}

	// Collect all namespaces from the module
	namespaceMap := collectNamespaces(module)

	// Create a DepGraph with initialized maps and namespaces
	depGraph := NewDepGraph(namespaceMap)

	// Populate the DepGraph with declarations and bindings
	depGraph.Decls = decls
	depGraph.ValueBindings = valueBindings
	depGraph.TypeBindings = typeBindings
	depGraph.DeclNamespace = declNamespace

	// Initialize DeclDeps slice with the correct size
	depGraph.DeclDeps = make([]btree.Set[DeclID], len(decls))

	// For each declaration, find its dependencies
	for i := range decls {
		declID := DeclID(i) // Use slice index directly as DeclID
		dependencies := FindDeclDependencies(declID, depGraph)
		depGraph.DeclDeps[declID] = dependencies // Use slice index directly
	}

	depGraph.Components = depGraph.FindStronglyConnectedComponents(0)

	return depGraph
}

// GetDeclDeps returns the dependencies for a given declaration ID
func (g *DepGraph) GetDeclDeps(declID DeclID) btree.Set[DeclID] {
	index := int(declID) // DeclID is now the slice index directly
	if index < 0 || index >= len(g.DeclDeps) {
		var result btree.Set[DeclID]
		return result
	}
	return g.DeclDeps[index]
}

// GetDecl returns the declaration for a given declaration ID
func (g *DepGraph) GetDecl(declID DeclID) (ast.Decl, bool) {
	index := int(declID) // DeclID is now the slice index directly
	if index < 0 || index >= len(g.Decls) {
		return nil, false
	}
	return g.Decls[index], true
}

// GetDeclNamespace returns the namespace for a given declaration ID
func (g *DepGraph) GetDeclNamespace(declID DeclID) (string, bool) {
	index := int(declID) // DeclID is now the slice index directly
	if index < 0 || index >= len(g.DeclNamespace) {
		return "", false
	}
	return g.DeclNamespace[index], true
}

// GetNamespaceID returns the namespace ID for a given namespace string
func (g *DepGraph) GetNamespaceID(namespace string) ast.NamespaceID {
	// Check if namespace exists, return 0 if not found
	for i, ns := range g.Namespaces {
		if ns == namespace {
			return ast.NamespaceID(i)
		}
	}
	return 0 // Return 0 (root namespace) if not found
}

// GetNamespaceString returns the namespace string for a given namespace ID
func (g *DepGraph) GetNamespaceString(id ast.NamespaceID) string {
	if int(id) < len(g.Namespaces) {
		return g.Namespaces[id]
	}
	return ""
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

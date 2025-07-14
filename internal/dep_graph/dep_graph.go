package dep_graph

import (
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

var nextDeclID DeclID = 1

// generateDeclID generates a new unique declaration ID
func generateDeclID() DeclID {
	id := nextDeclID
	nextDeclID++
	return id
}

// ModuleBindingVisitor collects all declarations with unique IDs and their bindings
type ModuleBindingVisitor struct {
	ast.DefaulVisitor
	Decls         btree.Map[DeclID, ast.Decl] // Map from unique ID to declaration
	ValueBindings btree.Map[string, DeclID]   // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID]   // Map from type binding name to declaration ID
}

// EnterDecl visits declarations and assigns unique IDs
func (v *ModuleBindingVisitor) EnterDecl(decl ast.Decl) bool {
	// Generate a unique ID for this declaration
	declID := generateDeclID()
	v.Decls.Set(declID, decl)

	switch d := decl.(type) {
	case *ast.VarDecl:
		// Extract bindings from the pattern
		patternBindings := ast.FindBindings(d.Pattern)
		for binding := range patternBindings {
			v.ValueBindings.Set(binding, declID)
		}
	case *ast.FuncDecl:
		// Function declarations introduce a binding with the function name
		if d.Name != nil && d.Name.Name != "" {
			v.ValueBindings.Set(d.Name.Name, declID)
		}
	case *ast.TypeDecl:
		// Type declarations introduce a binding with the type name
		if d.Name != nil && d.Name.Name != "" {
			v.TypeBindings.Set(d.Name.Name, declID)
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
	}

	// Visit all declarations in the module
	for _, decl := range module.Decls {
		decl.Accept(visitor)
	}

	return visitor.Decls, visitor.ValueBindings, visitor.TypeBindings
}

// DependencyVisitor finds IdentExpr dependencies in a declaration while tracking scope
type DependencyVisitor struct {
	ast.DefaulVisitor
	ValueBindings btree.Map[string, DeclID] // Valid value dependencies from the current module
	TypeBindings  btree.Map[string, DeclID] // Valid type dependencies from the current module
	Dependencies  btree.Set[DeclID]         // Found dependencies by declaration ID
	LocalBindings []set.Set[string]         // Stack of local scopes (still strings for local scope)
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
		if declID, exists := v.ValueBindings.Get(e.Name); exists &&
			!v.isLocalBinding(e.Name) {
			v.Dependencies.Insert(declID)
		}
		return false // Don't traverse into IdentExpr
	case *ast.MemberExpr:
		// For member expressions like obj.prop, we need to check the object part
		// Continue traversing to find any identifier dependencies
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
		if declID, exists := v.TypeBindings.Get(t.Name); exists &&
			!v.isLocalBinding(t.Name) {
			v.Dependencies.Insert(declID)
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
				if declID, exists := v.ValueBindings.Get(key.Name); exists &&
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
	valueBindings btree.Map[string, DeclID],
	typeBindings btree.Map[string, DeclID],
) btree.Set[DeclID] {
	var dependencies btree.Set[DeclID]
	visitor := &DependencyVisitor{
		DefaulVisitor: ast.DefaulVisitor{},
		ValueBindings: valueBindings,
		TypeBindings:  typeBindings,
		Dependencies:  dependencies,
		LocalBindings: make([]set.Set[string], 0),
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
	Decls         btree.Map[DeclID, ast.Decl]          // All declarations in the module
	Deps          btree.Map[DeclID, btree.Set[DeclID]] // Dependencies for each declaration ID
	ValueBindings btree.Map[string, DeclID]            // Map from value binding name to declaration ID
	TypeBindings  btree.Map[string, DeclID]            // Map from type binding name to declaration ID
}

// BuildDepGraph builds a dependency graph for a module
func BuildDepGraph(module *ast.Module) *DepGraph {
	// First, find all decls and bindings in the module
	decls, valueBindings, typeBindings := FindModuleBindings(module)

	// Build the dependency map
	var deps btree.Map[DeclID, btree.Set[DeclID]]

	// For each declaration, find its dependencies
	iter := decls.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		declID := iter.Key()
		decl := iter.Value()
		dependencies := FindDeclDependencies(decl, valueBindings, typeBindings)
		deps.Set(declID, dependencies)
	}

	return &DepGraph{
		Decls:         decls,
		ValueBindings: valueBindings,
		TypeBindings:  typeBindings,
		Deps:          deps,
	}
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

// GetDependents returns all declaration IDs that depend on the given declaration ID
func (g *DepGraph) GetDependents(target DeclID) set.Set[DeclID] {
	dependents := set.NewSet[DeclID]()
	iter := g.Deps.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		declID := iter.Key()
		deps := iter.Value()
		if deps.Contains(target) {
			dependents.Add(declID)
		}
	}
	return dependents
}

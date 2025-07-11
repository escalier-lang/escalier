package dep_graph

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
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

// ModuleBindingVisitor collects all binding names introduced by a module
type ModuleBindingVisitor struct {
	ast.DefaulVisitor
	Bindings map[DepBinding]ast.Decl
}

// EnterDecl visits declarations and extracts binding names
func (v *ModuleBindingVisitor) EnterDecl(decl ast.Decl) bool {
	switch d := decl.(type) {
	case *ast.VarDecl:
		// Extract bindings from the pattern
		patternBindings := ast.FindBindings(d.Pattern)
		for binding := range patternBindings {
			depBinding := DepBinding{Name: binding, Kind: DepKindValue}
			v.Bindings[depBinding] = d
		}
	case *ast.FuncDecl:
		// Function declarations introduce a binding with the function name
		if d.Name != nil && d.Name.Name != "" {
			depBinding := DepBinding{Name: d.Name.Name, Kind: DepKindValue}
			v.Bindings[depBinding] = d
		}
	case *ast.TypeDecl:
		// Type declarations introduce a binding with the type name
		if d.Name != nil && d.Name.Name != "" {
			depBinding := DepBinding{Name: d.Name.Name, Kind: DepKindType}
			v.Bindings[depBinding] = d
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

// FindModuleBindings returns all binding names introduced by a module
func FindModuleBindings(module *ast.Module) map[DepBinding]ast.Decl {
	visitor := &ModuleBindingVisitor{
		DefaulVisitor: ast.DefaulVisitor{},
		Bindings:      make(map[DepBinding]ast.Decl),
	}

	// Visit all declarations in the module
	for _, decl := range module.Decls {
		decl.Accept(visitor)
	}

	return visitor.Bindings
}

// DependencyVisitor finds IdentExpr dependencies in a declaration while tracking scope
type DependencyVisitor struct {
	ast.DefaulVisitor
	ValidBindings set.Set[DepBinding] // Valid dependencies from the current module
	Dependencies  set.Set[DepBinding] // Found dependencies
	LocalBindings []set.Set[string]   // Stack of local scopes (still strings for local scope)
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
		if binding, exists := v.hasValidBinding(e.Name); exists && !v.isLocalBinding(e.Name) {
			v.Dependencies.Add(binding)
		}
		return false // Don't traverse into IdentExpr
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
		if binding, exists := v.hasValidBinding(t.Name); exists && !v.isLocalBinding(t.Name) {
			v.Dependencies.Add(binding)
		}
		return true // Continue traversing type arguments
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

// hasValidBinding checks if a binding name exists in ValidBindings
func (v *DependencyVisitor) hasValidBinding(name string) (DepBinding, bool) {
	for binding := range v.ValidBindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return DepBinding{}, false
}

// FindDeclDependencies finds all IdentExpr dependencies in a declaration
// that are valid module-level bindings, while properly handling scope
func FindDeclDependencies(decl ast.Decl, validBindings set.Set[DepBinding]) set.Set[DepBinding] {
	visitor := &DependencyVisitor{
		DefaulVisitor: ast.DefaulVisitor{},
		ValidBindings: validBindings,
		Dependencies:  set.NewSet[DepBinding](),
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
	Bindings map[DepBinding]ast.Decl            // All bindings in the module
	Deps     map[DepBinding]set.Set[DepBinding] // Dependencies for each binding
}

// BuildDepGraph builds a dependency graph for a module
func BuildDepGraph(module *ast.Module) *DepGraph {
	// First, find all bindings in the module
	bindings := FindModuleBindings(module)

	// Create a set of all valid bindings for dependency resolution
	validBindings := set.NewSet[DepBinding]()
	for binding := range bindings {
		validBindings.Add(binding)
	}

	// Build the dependency map
	deps := make(map[DepBinding]set.Set[DepBinding])

	// For each declaration, find its dependencies
	for binding, decl := range bindings {
		dependencies := FindDeclDependencies(decl, validBindings)
		deps[binding] = dependencies
	}

	return &DepGraph{
		Bindings: bindings,
		Deps:     deps,
	}
}

// GetDependencies returns the dependencies for a given binding
func (g *DepGraph) GetDependencies(binding DepBinding) set.Set[DepBinding] {
	if deps, exists := g.Deps[binding]; exists {
		return deps
	}
	return set.NewSet[DepBinding]()
}

// GetBinding returns the declaration for a given binding
func (g *DepGraph) GetBinding(binding DepBinding) (ast.Decl, bool) {
	decl, exists := g.Bindings[binding]
	return decl, exists
}

// HasBinding checks if a binding exists in the graph
func (g *DepGraph) HasBinding(binding DepBinding) bool {
	_, exists := g.Bindings[binding]
	return exists
}

// AllBindings returns all bindings in the graph
func (g *DepGraph) AllBindings() []DepBinding {
	bindings := make([]DepBinding, 0, len(g.Bindings))
	for binding := range g.Bindings {
		bindings = append(bindings, binding)
	}
	return bindings
}

// GetDependents returns all bindings that depend on the given binding
func (g *DepGraph) GetDependents(target DepBinding) set.Set[DepBinding] {
	dependents := set.NewSet[DepBinding]()
	for binding, deps := range g.Deps {
		if deps.Contains(target) {
			dependents.Add(binding)
		}
	}
	return dependents
}

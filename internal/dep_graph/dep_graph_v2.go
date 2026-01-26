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

// DeclID represents a unique identifier for each declaration
type DeclID int

// BindingKey uniquely identifies a binding in the dependency graph.
// It is a string that combines the dependency kind ("value" or "type") with
// the fully qualified name, separated by a colon.
// Examples: "value:foo.bar", "type:foo.MyType", "value:createUser"
type BindingKey string

// ValueBindingKey creates a BindingKey for a value binding with the given qualified name.
func ValueBindingKey(qualifiedName string) BindingKey {
	return BindingKey("value:" + qualifiedName)
}

// TypeBindingKey creates a BindingKey for a type binding with the given qualified name.
func TypeBindingKey(qualifiedName string) BindingKey {
	return BindingKey("type:" + qualifiedName)
}

// NewBindingKey creates a BindingKey from a qualified name and dependency kind.
func NewBindingKey(qualifiedName string, kind DepKind) BindingKey {
	if kind == DepKindType {
		return TypeBindingKey(qualifiedName)
	}
	return ValueBindingKey(qualifiedName)
}

// Kind returns the dependency kind (DepKindValue or DepKindType) for this binding key.
func (k BindingKey) Kind() DepKind {
	if strings.HasPrefix(string(k), "type:") {
		return DepKindType
	}
	return DepKindValue
}

// Name returns the fully qualified name portion of the binding key.
func (k BindingKey) Name() string {
	if idx := strings.Index(string(k), ":"); idx != -1 {
		return string(k)[idx+1:]
	}
	return string(k)
}

// String returns the string representation of the BindingKey.
func (k BindingKey) String() string {
	return string(k)
}

// DepGraph is the refactored dependency graph using BindingKey as the primary key.
type DepGraph struct {
	// Map from binding key to all declarations that contribute to that binding.
	// For most bindings this will be a single declaration, but for interfaces
	// (declaration merging) and functions (overloading) there may be multiple.
	Decls btree.Map[BindingKey, []ast.Decl]

	// Dependencies for each binding key.
	// The dependencies are the union of all dependencies from all declarations
	// that contribute to this binding.
	DeclDeps btree.Map[BindingKey, btree.Set[BindingKey]]

	// Namespace for each binding (derived from the qualified name, but stored
	// for convenience).
	DeclNamespace btree.Map[BindingKey, string]

	// Strongly connected components of bindings sorted in topological order.
	// Each component is a slice of BindingKeys that form a cycle.
	Components [][]BindingKey

	// All namespace names in the module, indexed by NamespaceID.
	Namespaces []string
}

// NewDepGraphV2 creates a new DepGraphV2 with initialized empty maps.
func NewDepGraphV2(namespaceMap []string) *DepGraph {
	return &DepGraph{
		Decls:         btree.Map[BindingKey, []ast.Decl]{},
		DeclDeps:      btree.Map[BindingKey, btree.Set[BindingKey]]{},
		DeclNamespace: btree.Map[BindingKey, string]{},
		Components:    [][]BindingKey{},
		Namespaces:    namespaceMap,
	}
}

// GetDecls returns all declarations for a binding key
func (g *DepGraph) GetDecls(key BindingKey) []ast.Decl {
	decls, _ := g.Decls.Get(key)
	return decls
}

// GetDeps returns the dependencies for a binding key
func (g *DepGraph) GetDeps(key BindingKey) btree.Set[BindingKey] {
	deps, _ := g.DeclDeps.Get(key)
	return deps
}

// GetNamespace returns the namespace for a binding key
func (g *DepGraph) GetNamespace(key BindingKey) string {
	ns, _ := g.DeclNamespace.Get(key)
	return ns
}

// GetNamespaceString returns the namespace string for a given namespace ID
func (g *DepGraph) GetNamespaceString(id ast.NamespaceID) string {
	if int(id) < len(g.Namespaces) {
		return g.Namespaces[id]
	}
	return ""
}

// AllBindings returns all binding keys in the graph in deterministic order
func (g *DepGraph) AllBindings() []BindingKey {
	keys := make([]BindingKey, 0, g.Decls.Len())
	iter := g.Decls.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		keys = append(keys, iter.Key())
	}
	return keys
}

// HasBinding checks if a binding exists
func (g *DepGraph) HasBinding(key BindingKey) bool {
	_, exists := g.Decls.Get(key)
	return exists
}

// AddDecl adds a declaration to the graph under the given binding key.
// If the key already exists, the declaration is appended to the existing slice.
func (g *DepGraph) AddDecl(key BindingKey, decl ast.Decl, namespace string) {
	existing, _ := g.Decls.Get(key)
	g.Decls.Set(key, append(existing, decl))
	g.DeclNamespace.Set(key, namespace)
}

// SetDeps sets the dependencies for a binding key
func (g *DepGraph) SetDeps(key BindingKey, deps btree.Set[BindingKey]) {
	g.DeclDeps.Set(key, deps)
}

// ModuleBindingVisitorV2 collects all declarations and populates a DepGraphV2.
// Unlike the original ModuleBindingVisitor, this version:
// - Uses BindingKey instead of DeclID
// - Automatically groups multiple declarations under the same key (for overloads and interface merging)
// - Does not require separate ValueBindings/TypeBindings maps
type ModuleBindingVisitorV2 struct {
	ast.DefaultVisitor
	Graph         *DepGraph
	currentNSName string // Current namespace being visited
}

// qualifyName returns the fully qualified name by prepending the current namespace
func (v *ModuleBindingVisitorV2) qualifyName(name string) string {
	if v.currentNSName != "" {
		return v.currentNSName + "." + name
	}
	return name
}

// EnterDecl visits declarations and adds them to the graph under the appropriate BindingKey.
// Multiple declarations with the same name (overloaded functions, merged interfaces)
// will be appended to the same key.
func (v *ModuleBindingVisitorV2) EnterDecl(decl ast.Decl) bool {
	switch d := decl.(type) {
	case *ast.VarDecl:
		// Extract bindings from the pattern
		bindingNames := ast.FindBindings(d.Pattern)
		for name := range bindingNames {
			qualName := v.qualifyName(name)
			key := ValueBindingKey(qualName)
			v.Graph.AddDecl(key, decl, v.currentNSName)
		}
	case *ast.FuncDecl:
		// Function declarations introduce a value binding with the function name.
		// Multiple FuncDecls with the same name (overloads) will be grouped together.
		if d.Name != nil && d.Name.Name != "" {
			qualName := v.qualifyName(d.Name.Name)
			key := ValueBindingKey(qualName)
			v.Graph.AddDecl(key, decl, v.currentNSName)
		}
	case *ast.TypeDecl:
		// Type declarations introduce a type binding
		if d.Name != nil && d.Name.Name != "" {
			qualName := v.qualifyName(d.Name.Name)
			key := TypeBindingKey(qualName)
			v.Graph.AddDecl(key, decl, v.currentNSName)
		}
	case *ast.ClassDecl:
		// Class declarations introduce both a type binding and a value binding (constructor)
		if d.Name != nil && d.Name.Name != "" {
			qualName := v.qualifyName(d.Name.Name)
			// Add to both type and value bindings
			typeKey := TypeBindingKey(qualName)
			valueKey := ValueBindingKey(qualName)
			v.Graph.AddDecl(typeKey, decl, v.currentNSName)
			v.Graph.AddDecl(valueKey, decl, v.currentNSName)
		}
	case *ast.InterfaceDecl:
		// Interface declarations introduce a type binding.
		// Multiple InterfaceDecls with the same name (declaration merging) will be grouped.
		if d.Name != nil && d.Name.Name != "" {
			qualName := v.qualifyName(d.Name.Name)
			key := TypeBindingKey(qualName)
			v.Graph.AddDecl(key, decl, v.currentNSName)
		}
	case *ast.EnumDecl:
		// Enum declarations introduce both a type binding and a value binding
		if d.Name != nil && d.Name.Name != "" {
			qualName := v.qualifyName(d.Name.Name)
			// Add to both type and value bindings
			typeKey := TypeBindingKey(qualName)
			valueKey := ValueBindingKey(qualName)
			v.Graph.AddDecl(typeKey, decl, v.currentNSName)
			v.Graph.AddDecl(valueKey, decl, v.currentNSName)
		}
	}
	return false // Don't traverse into the declaration's body
}

// Other visitor methods return false to avoid traversing into nested structures
func (v *ModuleBindingVisitorV2) EnterStmt(stmt ast.Stmt) bool               { return false }
func (v *ModuleBindingVisitorV2) EnterExpr(expr ast.Expr) bool               { return false }
func (v *ModuleBindingVisitorV2) EnterPat(pat ast.Pat) bool                  { return false }
func (v *ModuleBindingVisitorV2) EnterObjExprElem(elem ast.ObjExprElem) bool { return false }
func (v *ModuleBindingVisitorV2) EnterTypeAnn(t ast.TypeAnn) bool            { return false }
func (v *ModuleBindingVisitorV2) EnterLit(lit ast.Lit) bool                  { return false }
func (v *ModuleBindingVisitorV2) EnterBlock(block ast.Block) bool            { return false }

// PopulateBindings visits all declarations in a module and populates the graph.
// This is the entry point for collecting bindings.
func PopulateBindings(graph *DepGraph, module *ast.Module) {
	visitor := &ModuleBindingVisitorV2{
		DefaultVisitor: ast.DefaultVisitor{},
		Graph:          graph,
		currentNSName:  "",
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
}

// LocalScopeV2 represents a single scope with separate value and type bindings.
// This is used to track local bindings that shadow module-level bindings.
type LocalScopeV2 struct {
	ValueBindings set.Set[string] // Local value bindings in this scope
	TypeBindings  set.Set[string] // Local type bindings in this scope
}

// DependencyVisitorV2 finds dependencies in a declaration and returns them as BindingKeys.
// Unlike the original DependencyVisitor, this version:
// - Uses BindingKey instead of DeclID
// - Looks up bindings directly in the DepGraphV2
// - Returns dependencies as btree.Set[BindingKey]
type DependencyVisitorV2 struct {
	ast.DefaultVisitor
	Graph            *DepGraph                  // The dependency graph to look up bindings
	NamespaceMap     map[string]ast.NamespaceID // Map from namespace name to ID
	Dependencies     btree.Set[BindingKey]      // Found dependencies
	LocalScopes      []LocalScopeV2             // Stack of local scopes
	CurrentNamespace string                     // Current namespace being analyzed
}

// pushScope adds a new local scope
func (v *DependencyVisitorV2) pushScope() {
	newScope := LocalScopeV2{
		ValueBindings: set.NewSet[string](),
		TypeBindings:  set.NewSet[string](),
	}
	v.LocalScopes = append(v.LocalScopes, newScope)
}

// popScope removes the current local scope
func (v *DependencyVisitorV2) popScope() {
	if len(v.LocalScopes) > 0 {
		v.LocalScopes = v.LocalScopes[:len(v.LocalScopes)-1]
	}
}

// isLocalValueBinding checks if a binding is a value binding in any local scope
func (v *DependencyVisitorV2) isLocalValueBinding(name string) bool {
	for _, scope := range v.LocalScopes {
		if scope.ValueBindings.Contains(name) {
			return true
		}
	}
	return false
}

// isLocalTypeBinding checks if a binding is a type binding in any local scope
func (v *DependencyVisitorV2) isLocalTypeBinding(name string) bool {
	for _, scope := range v.LocalScopes {
		if scope.TypeBindings.Contains(name) {
			return true
		}
	}
	return false
}

// addValueDependency adds a value dependency if it exists in the graph and is not shadowed locally
func (v *DependencyVisitorV2) addValueDependency(name string, expr *ast.IdentExpr) bool {
	if v.isLocalValueBinding(name) {
		return false
	}

	// If we're in a non-empty namespace, first try the qualified name
	if v.CurrentNamespace != "" {
		qualifiedName := v.CurrentNamespace + "." + name
		key := ValueBindingKey(qualifiedName)
		if v.Graph.HasBinding(key) {
			if expr != nil {
				expr.Namespace = v.NamespaceMap[v.CurrentNamespace]
			}
			v.Dependencies.Insert(key)
			return true
		}
	}

	// Then try the unqualified name (global namespace)
	key := ValueBindingKey(name)
	if v.Graph.HasBinding(key) {
		// Set the namespace based on where the binding was declared
		if expr != nil {
			if declNS, ok := v.Graph.DeclNamespace.Get(key); ok {
				expr.Namespace = v.NamespaceMap[declNS]
			}
		}
		v.Dependencies.Insert(key)
		return true
	}

	return false
}

// addTypeDependency adds a type dependency if it exists in the graph and is not shadowed locally
func (v *DependencyVisitorV2) addTypeDependency(typeName string) bool {
	if v.isLocalTypeBinding(typeName) {
		return false
	}

	// If we're in a non-empty namespace, first try the qualified name
	if v.CurrentNamespace != "" {
		qualifiedTypeName := v.CurrentNamespace + "." + typeName
		key := TypeBindingKey(qualifiedTypeName)
		if v.Graph.HasBinding(key) {
			v.Dependencies.Insert(key)
			return true
		}
	}

	// Then try the unqualified name (global namespace)
	key := TypeBindingKey(typeName)
	if v.Graph.HasBinding(key) {
		v.Dependencies.Insert(key)
		return true
	}

	return false
}

// EnterStmt handles statements that introduce new scopes
func (v *DependencyVisitorV2) EnterStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.DeclStmt:
		// For local variable declarations, we must NOT hoist the binding.
		// We need to visit the initializer BEFORE adding the binding to scope,
		// so that references to the same name in the initializer are treated
		// as dependencies on module-level bindings, not as local references.
		if len(v.LocalScopes) > 0 {
			currentScope := &v.LocalScopes[len(v.LocalScopes)-1]
			switch decl := s.Decl.(type) {
			case *ast.VarDecl:
				// Visit type annotation and initializer FIRST
				if decl.TypeAnn != nil {
					decl.TypeAnn.Accept(v)
				}
				if decl.Init != nil {
					decl.Init.Accept(v)
				}
				// THEN add bindings to scope
				bindings := ast.FindBindings(decl.Pattern)
				for binding := range bindings {
					currentScope.ValueBindings.Add(binding)
				}
				return false // Don't traverse again
			case *ast.FuncDecl:
				// Function declarations are hoisted, so add to scope first
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.ValueBindings.Add(decl.Name.Name)
				}
			case *ast.TypeDecl:
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
				}
			case *ast.ClassDecl:
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
					currentScope.ValueBindings.Add(decl.Name.Name)
				}
			case *ast.InterfaceDecl:
				if decl.Name != nil && decl.Name.Name != "" {
					currentScope.TypeBindings.Add(decl.Name.Name)
				}
			case *ast.EnumDecl:
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

// EnterExpr handles expressions that might introduce scope or contain dependencies
func (v *DependencyVisitorV2) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		v.addValueDependency(e.Name, e)
		return false // Don't traverse into IdentExpr
	case *ast.MemberExpr:
		// For member expressions like obj.prop, check if the full qualified name exists in bindings
		qualifiedName := v.buildQualifiedName(e)
		if qualifiedName != "" {
			root := qualifiedName
			if idx := strings.IndexByte(root, '.'); idx != -1 {
				root = root[:idx]
			}
			if v.isLocalValueBinding(root) {
				return true // local root shadows module namespace
			}

			// Check if the qualified name is a valid value dependency
			key := ValueBindingKey(qualifiedName)
			if v.Graph.HasBinding(key) && !v.isLocalValueBinding(qualifiedName) {
				v.Dependencies.Insert(key)
				return false // Don't traverse further since we found the qualified dependency
			}
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
func (v *DependencyVisitorV2) ExitExpr(expr ast.Expr) {
	switch expr.(type) {
	case *ast.FuncExpr:
		v.popScope()
	}
}

// EnterTypeAnn handles type annotations that might contain dependencies
func (v *DependencyVisitorV2) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	switch t := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		typeName := ast.QualIdentToString(t.Name)
		v.addTypeDependency(typeName)
		return true // Continue traversing type arguments
	case *ast.TypeOfTypeAnn:
		// For typeof expressions, we need to track dependencies on the value binding
		qualName := ast.QualIdentToString(t.Value)

		// Try progressively shorter qualified names to find the dependency
		parts := strings.Split(qualName, ".")
		for i := len(parts); i > 0; i-- {
			candidateName := strings.Join(parts[:i], ".")
			root := candidateName
			if idx := strings.IndexByte(root, '.'); idx != -1 {
				root = root[:idx]
			}
			if v.isLocalValueBinding(root) {
				break // local root shadows module namespace
			}

			// Try with current namespace prefix first
			if v.CurrentNamespace != "" {
				qualifiedName := v.CurrentNamespace + "." + candidateName
				key := ValueBindingKey(qualifiedName)
				if v.Graph.HasBinding(key) && !v.isLocalValueBinding(candidateName) {
					v.Dependencies.Insert(key)
					break
				}
			}

			// Try without namespace prefix
			key := ValueBindingKey(candidateName)
			if v.Graph.HasBinding(key) && !v.isLocalValueBinding(candidateName) {
				v.Dependencies.Insert(key)
				break
			}
		}
		return true
	case *ast.ObjectTypeAnn:
		// Handle object type annotations which may contain computed keys
		for _, elem := range t.Elems {
			if prop, ok := elem.(*ast.PropertyTypeAnn); ok {
				if compKey, ok := prop.Name.(*ast.ComputedKey); ok {
					compKey.Expr.Accept(v)
				}
			}
		}
		return true
	default:
		return true
	}
}

// EnterBlock handles entering blocks which introduce new scopes
func (v *DependencyVisitorV2) EnterBlock(block ast.Block) bool {
	v.pushScope()
	return true
}

// ExitBlock handles exiting blocks which end scopes
func (v *DependencyVisitorV2) ExitBlock(block ast.Block) {
	v.popScope()
}

// EnterObjExprElem handles object expression elements
func (v *DependencyVisitorV2) EnterObjExprElem(elem ast.ObjExprElem) bool {
	if el, ok := elem.(*ast.PropertyExpr); ok {
		// Handle property shorthand (e.g., { foo } where foo refers to a binding)
		if el.Value == nil {
			switch key := el.Name.(type) {
			case *ast.IdentExpr:
				v.addValueDependency(key.Name, nil)
			}
			return false
		}
	}

	return true
}

// processTypeParams handles type parameters by adding them to scope and visiting constraints
func (v *DependencyVisitorV2) processTypeParams(typeParams []*ast.TypeParam) {
	sortedTypeParams := ast.SortTypeParamsTopologically(typeParams)

	if len(v.LocalScopes) > 0 {
		currentScope := &v.LocalScopes[len(v.LocalScopes)-1]
		for _, tp := range sortedTypeParams {
			currentScope.TypeBindings.Add(tp.Name)

			if tp.Constraint != nil {
				tp.Constraint.Accept(v)
			}
			if tp.Default != nil {
				tp.Default.Accept(v)
			}
		}
	}
}

// buildQualifiedName constructs a qualified name from a MemberExpr chain
func (v *DependencyVisitorV2) buildQualifiedName(expr *ast.MemberExpr) string {
	parts := make([]string, 0)

	current := expr
	for current != nil {
		if current.Prop == nil {
			return ""
		}
		parts = append([]string{current.Prop.Name}, parts...)

		if memberObj, ok := current.Object.(*ast.MemberExpr); ok {
			current = memberObj
		} else if identObj, ok := current.Object.(*ast.IdentExpr); ok {
			parts = append([]string{identObj.Name}, parts...)
			break
		} else {
			return ""
		}
	}

	if len(parts) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(parts[0])
	for i := 1; i < len(parts); i++ {
		builder.WriteByte('.')
		builder.WriteString(parts[i])
	}
	return builder.String()
}

// FindDeclDependenciesV2 finds all dependencies for declarations under a binding key.
// For bindings with multiple declarations (overloaded functions, merged interfaces),
// the dependencies are the union of all declarations' dependencies.
func FindDeclDependenciesV2(key BindingKey, graph *DepGraph) btree.Set[BindingKey] {
	decls := graph.GetDecls(key)
	currentNamespace := graph.GetNamespace(key)

	// Build namespace map for setting IdentExpr.Namespace
	namespaceMap := make(map[string]ast.NamespaceID)
	for i, nsName := range graph.Namespaces {
		namespaceMap[nsName] = ast.NamespaceID(i)
	}

	var allDeps btree.Set[BindingKey]

	// Process each declaration and union their dependencies
	for _, decl := range decls {
		visitor := &DependencyVisitorV2{
			DefaultVisitor:   ast.DefaultVisitor{},
			Graph:            graph,
			NamespaceMap:     namespaceMap,
			Dependencies:     btree.Set[BindingKey]{},
			CurrentNamespace: currentNamespace,
			LocalScopes:      make([]LocalScopeV2, 0),
		}

		// Create a scope for type parameters
		visitor.pushScope()

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
			visitor.processTypeParams(d.TypeParams)

			// Visit parameter type annotations
			for _, param := range d.Params {
				if param.TypeAnn != nil {
					param.TypeAnn.Accept(visitor)
				}
			}

			// Visit return type annotation
			if d.Return != nil {
				d.Return.Accept(visitor)
			}

			// Visit throws type annotation
			if d.Throws != nil {
				d.Throws.Accept(visitor)
			}

			// Visit the function body
			if d.Body != nil {
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
				d.Body.Accept(visitor)
			}
		case *ast.TypeDecl:
			visitor.processTypeParams(d.TypeParams)
			d.TypeAnn.Accept(visitor)
		case *ast.InterfaceDecl:
			visitor.processTypeParams(d.TypeParams)

			// Visit extends clause
			for _, ext := range d.Extends {
				ext.Accept(visitor)
			}

			d.TypeAnn.Accept(visitor)
		case *ast.EnumDecl:
			visitor.processTypeParams(d.TypeParams)

			// Visit enum elements for dependencies
			for _, elem := range d.Elems {
				if spread, ok := elem.(*ast.EnumSpread); ok {
					// EnumSpread references another enum
					if spread.Arg != nil {
						enumName := spread.Arg.Name
						// Try with current namespace prefix first
						if currentNamespace != "" {
							qualifiedName := currentNamespace + "." + enumName
							key := TypeBindingKey(qualifiedName)
							if graph.HasBinding(key) {
								visitor.Dependencies.Insert(key)
								continue
							}
						}
						// Try without namespace prefix
						key := TypeBindingKey(enumName)
						if graph.HasBinding(key) {
							visitor.Dependencies.Insert(key)
						}
					}
				} else if variant, ok := elem.(*ast.EnumVariant); ok {
					// Visit variant parameters
					for _, param := range variant.Params {
						if param.TypeAnn != nil {
							param.TypeAnn.Accept(visitor)
						}
					}
				}
			}
		case *ast.ClassDecl:
			visitor.processTypeParams(d.TypeParams)

			// Visit extends clause
			if d.Extends != nil {
				d.Extends.Accept(visitor)
			}

			// Visit constructor parameter type annotations
			for _, param := range d.Params {
				if param.TypeAnn != nil {
					param.TypeAnn.Accept(visitor)
				}
			}

			// Add constructor parameters to scope
			if len(visitor.LocalScopes) > 0 {
				currentScope := &visitor.LocalScopes[len(visitor.LocalScopes)-1]
				for _, param := range d.Params {
					bindings := ast.FindBindings(param.Pattern)
					for binding := range bindings {
						currentScope.ValueBindings.Add(binding)
					}
				}
			}

			// Visit class body elements
			for _, elem := range d.Body {
				elem.Accept(visitor)
			}
		}

		// Pop the type parameter scope
		visitor.popScope()

		// Union the dependencies from this declaration
		iter := visitor.Dependencies.Iter()
		for ok := iter.First(); ok; ok = iter.Next() {
			allDeps.Insert(iter.Key())
		}
	}

	return allDeps
}

// FindStronglyConnectedComponentsV2 uses Tarjan's algorithm to find SCCs using BindingKey.
// The returned components appear in topological order, meaning that if component
// A depends on component B, then A will appear after B in the result.
//
// The threshold parameter specifies the minimum size of a strongly connected
// component to be reported. If a component has size equal to threshold, it is
// reported only if it contains a self-reference.
func (g *DepGraph) FindStronglyConnectedComponentsV2(threshold int) [][]BindingKey {
	index := 0
	stack := make([]BindingKey, 0)
	indices := make(map[BindingKey]int)
	lowlinks := make(map[BindingKey]int)
	onStack := make(map[BindingKey]bool)
	sccs := make([][]BindingKey, 0)

	var strongConnect func(BindingKey)
	strongConnect = func(v BindingKey) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		// Consider successors of v
		deps := g.GetDeps(v)
		iter := deps.Iter()
		for ok := iter.First(); ok; ok = iter.Next() {
			w := iter.Key()
			if _, exists := indices[w]; !exists {
				// Successor w has not yet been visited; recurse on it
				strongConnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				// Successor w is in stack and hence in the current SCC
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}

		// If v is a root node, pop the stack and create an SCC
		if lowlinks[v] == indices[v] {
			var scc []BindingKey
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			// Report cycles: either multiple bindings OR a self-reference
			deps := g.GetDeps(scc[0])
			if len(scc) > threshold || (len(scc) == threshold && deps.Contains(scc[0])) {
				sccs = append(sccs, scc)
			}
		}
	}

	// Run the algorithm for all unvisited binding keys
	allKeys := g.AllBindings()
	for _, key := range allKeys {
		if _, exists := indices[key]; !exists {
			strongConnect(key)
		}
	}

	return sccs
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

// BuildDepGraphV2 builds a dependency graph for a module using the new BindingKey-based approach.
// This is the main entry point for building the V2 dependency graph.
func BuildDepGraphV2(module *ast.Module) *DepGraph {
	// Collect all namespaces from the module
	namespaceMap := collectNamespaces(module)
	graph := NewDepGraphV2(namespaceMap)

	// Populate bindings by visiting all declarations
	PopulateBindings(graph, module)

	// Find dependencies for each binding
	allKeys := graph.AllBindings()
	for _, key := range allKeys {
		deps := FindDeclDependenciesV2(key, graph)
		graph.SetDeps(key, deps)
	}

	// Compute strongly connected components
	// Note: FindStronglyConnectedComponentsV2 will be implemented in Phase 3
	graph.Components = graph.FindStronglyConnectedComponentsV2(0)

	return graph
}

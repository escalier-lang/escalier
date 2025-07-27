package dep_graph

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// CycleInfo represents information about a problematic cycle
type CycleInfo struct {
	Cycle   []DeclID // The declaration IDs involved in the cycle
	Message string   // Description of why this cycle is problematic
}

// FindStronglyConnectedComponents uses Tarjan's algorithm to find SCCs
// The returned components appear in topological order, meaning that if component
// A depends on component B, then A will appear after B in the result.
//
// The threshold parameter specifies the minimimum size of a strongly connected
// component to be reported. If a component has size equal to threshold, it is
// reported only if it contains a self-reference, e.g. a function that calls itself.
func (g *DepGraph) FindStronglyConnectedComponents(threshold int) [][]DeclID {
	// Tarjan's algorithm implementation
	index := 0
	stack := make([]DeclID, 0)
	indices := make(map[DeclID]int)
	lowlinks := make(map[DeclID]int)
	onStack := make(map[DeclID]bool)
	sccs := make([][]DeclID, 0)

	var strongConnect func(DeclID)
	strongConnect = func(v DeclID) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		// Consider successors of v
		deps := g.GetDeclDeps(v)
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
			var scc []DeclID
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
			deps := g.GetDeclDeps(scc[0])
			if len(scc) > threshold || (len(scc) == threshold && deps.Contains(scc[0])) {
				sccs = append(sccs, scc)
			}
		}
	}

	// Run the algorithm for all unvisited nodes
	for i := range g.Decls {
		declID := DeclID(i) // DeclID is now the slice index directly
		if _, exists := indices[declID]; !exists {
			strongConnect(declID)
		}
	}

	return sccs
}

// getBindingsForDecl returns all bindings associated with a declaration ID
func (g *DepGraph) getBindingsForDecl(declID DeclID) []DepBinding {
	var bindings []DepBinding
	iter := g.ValueBindings.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		name := iter.Key()
		id := iter.Value()
		if id == declID {
			bindings = append(bindings, DepBinding{Name: name, Kind: DepKindValue})
		}
	}
	iter = g.TypeBindings.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		name := iter.Key()
		id := iter.Value()
		if id == declID {
			bindings = append(bindings, DepBinding{Name: name, Kind: DepKindType})
		}
	}
	return bindings
}

// hasValueBinding checks if a declaration has any value bindings
func (g *DepGraph) hasValueBinding(declID DeclID) bool {
	bindings := g.getBindingsForDecl(declID)
	for _, binding := range bindings {
		if binding.Kind == DepKindValue {
			return true
		}
	}
	return false
}

// hasTypeBinding checks if a declaration has any type bindings
func (g *DepGraph) hasTypeBinding(declID DeclID) bool {
	bindings := g.getBindingsForDecl(declID)
	for _, binding := range bindings {
		if binding.Kind == DepKindType {
			return true
		}
	}
	return false
}

// getDeclNames returns all binding names for a declaration
func (g *DepGraph) GetDeclNames(declID DeclID) []string {
	bindings := g.getBindingsForDecl(declID)
	names := make([]string, len(bindings))
	for i, binding := range bindings {
		names[i] = binding.Name
	}
	return names
}

// FindCycles detects problematic cycles in the dependency graph.
// It uses Tarjan's algorithm to find strongly connected components, then identifies
// cycles that are problematic according to these rules:
// - Type-only cycles are allowed and ignored
// - Mixed cycles (containing both types and values) are always problematic
// - Value-only cycles are problematic if any binding in the cycle is used outside function bodies
// Returns a slice of CycleInfo containing details about each problematic cycle found.
func (g *DepGraph) FindCycles() []CycleInfo {
	var problematicCycles []CycleInfo

	// Find all strongly connected components (cycles)
	cycles := g.FindStronglyConnectedComponents(1)

	// Pre-compute bindings used outside function bodies (only once for all cycles)
	var usedOutsideFunctionBodies set.Set[DepBinding]
	var hasComputedUsage bool

	for _, cycle := range cycles {
		// Check if cycle contains any value bindings
		hasValue := false
		for _, declID := range cycle {
			if g.hasValueBinding(declID) {
				hasValue = true
				break
			}
		}

		if !hasValue {
			// Type-only cycles are allowed, skip
			continue
		}

		// For cycles involving values, they are problematic in these cases:
		// 1. Mixed cycles (type + value) are always problematic
		// 2. Value-only cycles are problematic if any value is used outside function bodies

		isProblematic := false

		// This branch handles both mixed cycles (always problematic) and value-only cycles
		// (problematic if any value is used outside function bodies).
		if hasValue {
			hasType := false
			for _, declID := range cycle {
				if g.hasTypeBinding(declID) {
					hasType = true
					break
				}
			}

			if hasType {
				// Mixed cycle: always problematic
				isProblematic = true
			} else {
				// Value-only cycle: check if any value is used outside function bodies
				// Compute usage info only if we haven't done so yet and we need it
				if !hasComputedUsage {
					usedOutsideFunctionBodies = g.findBindingsUsedOutsideFunctionBodies()
					hasComputedUsage = true
				}

				for _, declID := range cycle {
					bindings := g.getBindingsForDecl(declID)
					for _, binding := range bindings {
						if binding.Kind == DepKindValue && usedOutsideFunctionBodies.Contains(binding) {
							isProblematic = true
							break
						}
					}
					if isProblematic {
						break
					}
				}
			}
		}

		if isProblematic {
			problematicCycles = append(problematicCycles, CycleInfo{
				Cycle:   cycle,
				Message: "Cycle detected between bindings that are used outside of function bodies",
			})
		}
	}

	return problematicCycles
}

// findBindingsUsedOutsideFunctionBodies finds all bindings that are used outside function bodies in any declaration
// This function traverses the AST only once and returns a set of all such bindings
func (g *DepGraph) findBindingsUsedOutsideFunctionBodies() set.Set[DepBinding] {
	// Create a set to track bindings used outside function bodies
	usedOutsideFunctionBodies := set.NewSet[DepBinding]()

	// Create a map for fast lookup of existing bindings by name
	bindingsByName := make(map[string][]DepBinding)
	valueIter := g.ValueBindings.Iter()
	for ok := valueIter.First(); ok; ok = valueIter.Next() {
		name := valueIter.Key()
		binding := DepBinding{Name: name, Kind: DepKindValue}
		bindingsByName[name] = append(bindingsByName[name], binding)
	}
	typeIter := g.TypeBindings.Iter()
	for ok := typeIter.First(); ok; ok = typeIter.Next() {
		name := typeIter.Key()
		binding := DepBinding{Name: name, Kind: DepKindType}
		bindingsByName[name] = append(bindingsByName[name], binding)
	}

	// Check all declarations to see if they use any bindings outside function bodies
	for _, decl := range g.Decls {
		visitor := &AllBindingsUsageVisitor{
			DefaulVisitor:                   ast.DefaulVisitor{},
			FunctionDepth:                   0,
			LocalBindings:                   make([]set.Set[string], 0),
			BindingsUsedOutsideFunctionBody: usedOutsideFunctionBodies,
			BindingsByName:                  bindingsByName, // Only track bindings that exist
		}

		decl.Accept(visitor)
	}

	return usedOutsideFunctionBodies
}

// AllBindingsUsageVisitor checks if any bindings are used outside function bodies
type AllBindingsUsageVisitor struct {
	ast.DefaulVisitor
	FunctionDepth                   int                     // Track nesting depth in function bodies
	LocalBindings                   []set.Set[string]       // Stack of local scopes
	BindingsUsedOutsideFunctionBody set.Set[DepBinding]     // Set of bindings used outside function body
	BindingsByName                  map[string][]DepBinding // Map of existing bindings by name
}

// EnterExpr handles expressions to track usage
func (v *AllBindingsUsageVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// Check if this is a valid binding used outside function body
		if bindings, exists := v.BindingsByName[e.Name]; exists && !v.isLocalBinding(e.Name) && v.FunctionDepth == 0 {
			// Add only the actual bindings that exist for this name
			for _, binding := range bindings {
				v.BindingsUsedOutsideFunctionBody.Add(binding)
			}
		}
		return false
	case *ast.CallExpr:
		// Check if the callee is a valid binding
		if ident, ok := e.Callee.(*ast.IdentExpr); ok {
			if bindings, exists := v.BindingsByName[ident.Name]; exists && !v.isLocalBinding(ident.Name) && v.FunctionDepth == 0 {
				// Only add value bindings for function calls
				for _, binding := range bindings {
					if binding.Kind == DepKindValue {
						v.BindingsUsedOutsideFunctionBody.Add(binding)
					}
				}
			}
		}
		// Check for method calls
		if member, ok := e.Callee.(*ast.MemberExpr); ok {
			if ident, ok := member.Object.(*ast.IdentExpr); ok {
				if bindings, exists := v.BindingsByName[ident.Name]; exists && !v.isLocalBinding(ident.Name) && v.FunctionDepth == 0 {
					// Only add value bindings for member expressions
					for _, binding := range bindings {
						if binding.Kind == DepKindValue {
							v.BindingsUsedOutsideFunctionBody.Add(binding)
						}
					}
				}
			}
		}
		return true
	case *ast.MemberExpr:
		// Check member access like obj.prop
		if ident, ok := e.Object.(*ast.IdentExpr); ok {
			if bindings, exists := v.BindingsByName[ident.Name]; exists && !v.isLocalBinding(ident.Name) && v.FunctionDepth == 0 {
				// Only add value bindings for member expressions
				for _, binding := range bindings {
					if binding.Kind == DepKindValue {
						v.BindingsUsedOutsideFunctionBody.Add(binding)
					}
				}
			}
		}
		return true
	case *ast.FuncExpr:
		// Function expression introduces a new scope and increases function depth
		v.FunctionDepth++
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
func (v *AllBindingsUsageVisitor) ExitExpr(expr ast.Expr) {
	switch expr.(type) {
	case *ast.FuncExpr:
		// Exit function expression scope
		v.FunctionDepth--
		v.popScope()
	}
}

// EnterDecl handles function declarations which introduce function body scope
func (v *AllBindingsUsageVisitor) EnterDecl(decl ast.Decl) bool {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Body != nil {
			// Function declaration body increases function depth
			v.FunctionDepth++
			v.pushScope()
			// Add parameters to the function scope
			for _, param := range d.Params {
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

// ExitDecl handles exiting declaration scopes
func (v *AllBindingsUsageVisitor) ExitDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Body != nil {
			// Exit function declaration scope
			v.FunctionDepth--
			v.popScope()
		}
	}
}

// EnterBlock handles entering blocks which introduce new scopes
func (v *AllBindingsUsageVisitor) EnterBlock(block ast.Block) bool {
	v.pushScope()
	return true
}

// ExitBlock handles exiting blocks which end scopes
func (v *AllBindingsUsageVisitor) ExitBlock(block ast.Block) {
	v.popScope()
}

// EnterTypeAnn handles type annotations to track usage
func (v *AllBindingsUsageVisitor) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	switch t := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		// Type references are always outside function bodies (at declaration level)
		typeName := ast.QualIdentToString(t.Name)
		if bindings, exists := v.BindingsByName[typeName]; exists && !v.isLocalBinding(typeName) && v.FunctionDepth == 0 {
			// Only add type bindings for type references
			for _, binding := range bindings {
				if binding.Kind == DepKindType {
					v.BindingsUsedOutsideFunctionBody.Add(binding)
				}
			}
		}
		return true
	default:
		return true
	}
}

// pushScope adds a new local scope
func (v *AllBindingsUsageVisitor) pushScope() {
	v.LocalBindings = append(v.LocalBindings, set.NewSet[string]())
}

// popScope removes the current local scope
func (v *AllBindingsUsageVisitor) popScope() {
	if len(v.LocalBindings) > 0 {
		v.LocalBindings = v.LocalBindings[:len(v.LocalBindings)-1]
	}
}

// isLocalBinding checks if a binding is in any local scope
func (v *AllBindingsUsageVisitor) isLocalBinding(name string) bool {
	for _, scope := range v.LocalBindings {
		if scope.Contains(name) {
			return true
		}
	}
	return false
}

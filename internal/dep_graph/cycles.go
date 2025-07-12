package dep_graph

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// CycleInfo represents information about a problematic cycle
type CycleInfo struct {
	Cycle   []DepBinding // The bindings involved in the cycle
	Message string       // Description of why this cycle is problematic
}

// findStronglyConnectedComponents uses Tarjan's algorithm to find SCCs
func (g *DepGraph) findStronglyConnectedComponents() [][]DepBinding {
	// Tarjan's algorithm implementation
	index := 0
	stack := make([]DepBinding, 0)
	indices := make(map[DepBinding]int)
	lowlinks := make(map[DepBinding]int)
	onStack := make(map[DepBinding]bool)
	sccs := make([][]DepBinding, 0)

	var strongConnect func(DepBinding)
	strongConnect = func(v DepBinding) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		// Consider successors of v
		for w := range g.GetDependencies(v) {
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
			var scc []DepBinding
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w.Name == v.Name && w.Kind == v.Kind {
					break
				}
			}
			// Report cycles: either multiple bindings OR a self-reference
			if len(scc) > 1 || (len(scc) == 1 && g.GetDependencies(scc[0]).Contains(scc[0])) {
				sccs = append(sccs, scc)
			}
		}
	}

	// Run the algorithm for all unvisited nodes
	for binding := range g.Bindings {
		if _, exists := indices[binding]; !exists {
			strongConnect(binding)
		}
	}

	return sccs
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
	cycles := g.findStronglyConnectedComponents()

	for _, cycle := range cycles {
		// Check if cycle contains only type bindings
		allTypes := true
		hasValue := false
		for _, binding := range cycle {
			if binding.Kind != DepKindType {
				allTypes = false
			}
			if binding.Kind == DepKindValue {
				hasValue = true
			}
		}

		if allTypes {
			// Type-only cycles are allowed, skip
			continue
		}

		// For cycles involving values, they are problematic in these cases:
		// 1. Mixed cycles (type + value) are always problematic
		// 2. Value-only cycles are problematic if any value is used outside function bodies

		isProblematic := false

		// If it's a mixed cycle (has both types and values), it's always problematic
		if hasValue && !allTypes {
			hasType := false
			for _, binding := range cycle {
				if binding.Kind == DepKindType {
					hasType = true
					break
				}
			}

			if hasType {
				// Mixed cycle: always problematic
				isProblematic = true
			} else {
				// Value-only cycle: check if any value is used outside function bodies
				for _, binding := range cycle {
					if binding.Kind == DepKindValue && g.isBindingUsedOutsideFunctionBodies(binding) {
						isProblematic = true
						break
					}
				}
			}
		}

		if isProblematic {
			// Create a descriptive message
			names := make([]string, len(cycle))
			for i, binding := range cycle {
				names[i] = binding.Name
			}

			problematicCycles = append(problematicCycles, CycleInfo{
				Cycle:   cycle,
				Message: "Cycle detected between bindings that are used outside of function bodies",
			})
		}
	}

	return problematicCycles
}

// isBindingUsedOutsideFunctionBodies checks if a binding is used outside function bodies in any declaration
func (g *DepGraph) isBindingUsedOutsideFunctionBodies(targetBinding DepBinding) bool {
	// Check all declarations to see if they use the target binding outside function bodies
	for _, decl := range g.Bindings {
		visitor := &FunctionBodyUsageVisitor{
			DefaulVisitor:           ast.DefaulVisitor{},
			TargetBinding:           targetBinding.Name,
			FunctionDepth:           0,
			LocalBindings:           make([]set.Set[string], 0),
			UsedOutsideFunctionBody: false,
		}

		decl.Accept(visitor)

		if visitor.UsedOutsideFunctionBody {
			return true
		}
	}

	return false
}

// FunctionBodyUsageVisitor checks if a target binding is used outside function bodies
type FunctionBodyUsageVisitor struct {
	ast.DefaulVisitor
	TargetBinding           string            // Name of the binding to track
	FunctionDepth           int               // Track nesting depth in function bodies
	LocalBindings           []set.Set[string] // Stack of local scopes
	UsedOutsideFunctionBody bool              // Set to true if target binding is used outside function body
}

// EnterExpr handles expressions to track usage
func (v *FunctionBodyUsageVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// Check if this is our target binding used outside function body
		if e.Name == v.TargetBinding && !v.isLocalBinding(e.Name) {
			if v.FunctionDepth == 0 {
				v.UsedOutsideFunctionBody = true
			}
		}
		return false
	case *ast.CallExpr:
		// Check if the callee is our target binding
		if ident, ok := e.Callee.(*ast.IdentExpr); ok {
			if ident.Name == v.TargetBinding && !v.isLocalBinding(ident.Name) {
				if v.FunctionDepth == 0 {
					v.UsedOutsideFunctionBody = true
				}
			}
		}
		// Check for method calls
		if member, ok := e.Callee.(*ast.MemberExpr); ok {
			if ident, ok := member.Object.(*ast.IdentExpr); ok {
				if ident.Name == v.TargetBinding && !v.isLocalBinding(ident.Name) {
					if v.FunctionDepth == 0 {
						v.UsedOutsideFunctionBody = true
					}
				}
			}
		}
		return true
	case *ast.MemberExpr:
		// Check member access like obj.prop
		if ident, ok := e.Object.(*ast.IdentExpr); ok {
			if ident.Name == v.TargetBinding && !v.isLocalBinding(ident.Name) {
				if v.FunctionDepth == 0 {
					v.UsedOutsideFunctionBody = true
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
func (v *FunctionBodyUsageVisitor) ExitExpr(expr ast.Expr) {
	switch expr.(type) {
	case *ast.FuncExpr:
		// Exit function expression scope
		v.FunctionDepth--
		v.popScope()
	}
}

// EnterDecl handles function declarations which introduce function body scope
func (v *FunctionBodyUsageVisitor) EnterDecl(decl ast.Decl) bool {
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
func (v *FunctionBodyUsageVisitor) ExitDecl(decl ast.Decl) {
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
func (v *FunctionBodyUsageVisitor) EnterBlock(block ast.Block) bool {
	v.pushScope()
	return true
}

// ExitBlock handles exiting blocks which end scopes
func (v *FunctionBodyUsageVisitor) ExitBlock(block ast.Block) {
	v.popScope()
}

// EnterTypeAnn handles type annotations to track usage
func (v *FunctionBodyUsageVisitor) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	// Type annotations are always outside function bodies (at declaration level)
	// So we should continue traversing them to find any usage
	return true
}

// pushScope adds a new local scope
func (v *FunctionBodyUsageVisitor) pushScope() {
	v.LocalBindings = append(v.LocalBindings, set.NewSet[string]())
}

// popScope removes the current local scope
func (v *FunctionBodyUsageVisitor) popScope() {
	if len(v.LocalBindings) > 0 {
		v.LocalBindings = v.LocalBindings[:len(v.LocalBindings)-1]
	}
}

// isLocalBinding checks if a binding is in any local scope
func (v *FunctionBodyUsageVisitor) isLocalBinding(name string) bool {
	for _, scope := range v.LocalBindings {
		if scope.Contains(name) {
			return true
		}
	}
	return false
}

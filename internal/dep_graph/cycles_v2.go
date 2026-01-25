package dep_graph

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// CycleInfoV2 represents information about a problematic cycle using BindingKey
type CycleInfoV2 struct {
	Cycle   []BindingKey // The binding keys involved in the cycle
	Message string       // Description of why this cycle is problematic
}

// IsValueBinding returns true if this is a value binding
func (k BindingKey) IsValueBinding() bool {
	return k.Kind() == DepKindValue
}

// IsTypeBinding returns true if this is a type binding
func (k BindingKey) IsTypeBinding() bool {
	return k.Kind() == DepKindType
}

// FindCyclesV2 detects problematic cycles in the dependency graph.
// It uses Tarjan's algorithm to find strongly connected components, then identifies
// cycles that are problematic according to these rules:
// - Type-only cycles are allowed and ignored
// - Mixed cycles (containing both types and values) are always problematic
// - Value-only cycles are problematic if any binding in the cycle is used outside function bodies
// Returns a slice of CycleInfoV2 containing details about each problematic cycle found.
func (g *DepGraphV2) FindCyclesV2() []CycleInfoV2 {
	var problematicCycles []CycleInfoV2

	// Find all strongly connected components (cycles)
	cycles := g.FindStronglyConnectedComponentsV2(1)

	// Pre-compute bindings used outside function bodies (only once for all cycles)
	var usedOutsideFunctionBodies set.Set[BindingKey]
	var hasComputedUsage bool

	for _, cycle := range cycles {
		// Check if cycle contains any value bindings
		hasValue := false
		for _, key := range cycle {
			if key.IsValueBinding() {
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

		hasType := false
		for _, key := range cycle {
			if key.IsTypeBinding() {
				hasType = true
				break
			}
		}

		if hasType {
			// Mixed cycle: always problematic
			isProblematic = true
		} else {
			// Value-only cycle: check if any value is used outside function bodies
			if !hasComputedUsage {
				usedOutsideFunctionBodies = g.findBindingsUsedOutsideFunctionBodiesV2()
				hasComputedUsage = true
			}

			for _, key := range cycle {
				if key.IsValueBinding() && usedOutsideFunctionBodies.Contains(key) {
					isProblematic = true
					break
				}
			}
		}

		if isProblematic {
			problematicCycles = append(problematicCycles, CycleInfoV2{
				Cycle:   cycle,
				Message: "Cycle detected between bindings that are used outside of function bodies",
			})
		}
	}

	return problematicCycles
}

// findBindingsUsedOutsideFunctionBodiesV2 finds all bindings that are used outside function bodies
// This function traverses the AST only once and returns a set of all such bindings
func (g *DepGraphV2) findBindingsUsedOutsideFunctionBodiesV2() set.Set[BindingKey] {
	usedOutsideFunctionBodies := set.NewSet[BindingKey]()

	// Iterate over all bindings and their declarations
	iter := g.Decls.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		decls := iter.Value()
		for _, decl := range decls {
			visitor := &AllBindingsUsageVisitorV2{
				DefaultVisitor:                  ast.DefaultVisitor{},
				Graph:                           g,
				FunctionDepth:                   0,
				LocalBindings:                   make([]set.Set[string], 0),
				BindingsUsedOutsideFunctionBody: usedOutsideFunctionBodies,
			}
			decl.Accept(visitor)
		}
	}

	return usedOutsideFunctionBodies
}

// AllBindingsUsageVisitorV2 checks if any bindings are used outside function bodies
type AllBindingsUsageVisitorV2 struct {
	ast.DefaultVisitor
	Graph                           *DepGraphV2
	FunctionDepth                   int               // Track nesting depth in function bodies
	LocalBindings                   []set.Set[string] // Stack of local scopes
	BindingsUsedOutsideFunctionBody set.Set[BindingKey]
}

// EnterExpr handles expressions to track usage
func (v *AllBindingsUsageVisitorV2) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// Check if this is a valid binding used outside function body
		if !v.isLocalBinding(e.Name) && v.FunctionDepth == 0 {
			// Check if this name exists as a value binding in the graph
			valueKey := ValueBindingKey(e.Name)
			if v.Graph.HasBinding(valueKey) {
				v.BindingsUsedOutsideFunctionBody.Add(valueKey)
			}
		}
		return false
	case *ast.CallExpr:
		// Check if the callee is a valid binding
		if ident, ok := e.Callee.(*ast.IdentExpr); ok {
			if !v.isLocalBinding(ident.Name) && v.FunctionDepth == 0 {
				valueKey := ValueBindingKey(ident.Name)
				if v.Graph.HasBinding(valueKey) {
					v.BindingsUsedOutsideFunctionBody.Add(valueKey)
				}
			}
		}
		// Check for method calls like utils.func() or obj.method()
		if member, ok := e.Callee.(*ast.MemberExpr); ok {
			qualifiedName := v.buildQualifiedName(member)
			if qualifiedName != "" {
				// Check if the root is shadowed by a local binding
				root := qualifiedName
				if idx := strings.IndexByte(root, '.'); idx != -1 {
					root = root[:idx]
				}
				if !v.isLocalBinding(root) && v.FunctionDepth == 0 {
					valueKey := ValueBindingKey(qualifiedName)
					if v.Graph.HasBinding(valueKey) {
						v.BindingsUsedOutsideFunctionBody.Add(valueKey)
					}
				}
			}
		}
		return true
	case *ast.MemberExpr:
		// Check member access like obj.prop or utils.b
		qualifiedName := v.buildQualifiedName(e)
		if qualifiedName != "" {
			// Check if the root is shadowed by a local binding
			root := qualifiedName
			if idx := strings.IndexByte(root, '.'); idx != -1 {
				root = root[:idx]
			}
			if v.isLocalBinding(root) {
				return true // local root shadows module namespace
			}

			// Check if the qualified name is a valid value binding
			if !v.isLocalBinding(qualifiedName) && v.FunctionDepth == 0 {
				valueKey := ValueBindingKey(qualifiedName)
				if v.Graph.HasBinding(valueKey) {
					v.BindingsUsedOutsideFunctionBody.Add(valueKey)
					return false // Don't traverse further since we found the qualified dependency
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
func (v *AllBindingsUsageVisitorV2) ExitExpr(expr ast.Expr) {
	switch expr.(type) {
	case *ast.FuncExpr:
		// Exit function expression scope
		v.FunctionDepth--
		v.popScope()
	}
}

// EnterDecl handles function declarations which introduce function body scope
func (v *AllBindingsUsageVisitorV2) EnterDecl(decl ast.Decl) bool {
	switch d := decl.(type) {
	case *ast.VarDecl:
		// VarDecl.Accept doesn't visit TypeAnn, so we need to manually visit it
		if d.TypeAnn != nil {
			d.TypeAnn.Accept(v)
		}
		return true
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
func (v *AllBindingsUsageVisitorV2) ExitDecl(decl ast.Decl) {
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
func (v *AllBindingsUsageVisitorV2) EnterBlock(block ast.Block) bool {
	v.pushScope()
	return true
}

// ExitBlock handles exiting blocks which end scopes
func (v *AllBindingsUsageVisitorV2) ExitBlock(block ast.Block) {
	v.popScope()
}

// EnterTypeAnn handles type annotations to track usage
func (v *AllBindingsUsageVisitorV2) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	switch t := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		// Type references are always outside function bodies (at declaration level)
		typeName := ast.QualIdentToString(t.Name)
		if !v.isLocalBinding(typeName) && v.FunctionDepth == 0 {
			typeKey := TypeBindingKey(typeName)
			if v.Graph.HasBinding(typeKey) {
				v.BindingsUsedOutsideFunctionBody.Add(typeKey)
			}
		}
		return true
	case *ast.TypeOfTypeAnn:
		// typeof references a value binding, not a type binding
		valueName := ast.QualIdentToString(t.Value)
		if !v.isLocalBinding(valueName) && v.FunctionDepth == 0 {
			key := ValueBindingKey(valueName)
			if v.Graph.HasBinding(key) {
				v.BindingsUsedOutsideFunctionBody.Add(key)
			}
		}
		return true
	default:
		return true
	}
}

// pushScope adds a new local scope
func (v *AllBindingsUsageVisitorV2) pushScope() {
	v.LocalBindings = append(v.LocalBindings, set.NewSet[string]())
}

// popScope removes the current local scope
func (v *AllBindingsUsageVisitorV2) popScope() {
	if len(v.LocalBindings) > 0 {
		v.LocalBindings = v.LocalBindings[:len(v.LocalBindings)-1]
	}
}

// isLocalBinding checks if a binding is in any local scope
func (v *AllBindingsUsageVisitorV2) isLocalBinding(name string) bool {
	for _, scope := range v.LocalBindings {
		if scope.Contains(name) {
			return true
		}
	}
	return false
}

// buildQualifiedName constructs a qualified name from a MemberExpr chain
func (v *AllBindingsUsageVisitorV2) buildQualifiedName(expr *ast.MemberExpr) string {
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

	return strings.Join(parts, ".")
}

// GetBindingNames returns the names for a slice of binding keys
func GetBindingNames(keys []BindingKey) []string {
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = key.Name()
	}
	return names
}

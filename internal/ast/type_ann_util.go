package ast

// typeParamRefVisitor collects type parameter references in type annotations
type typeParamRefVisitor struct {
	DefaultVisitor
	availableTypeParams map[string]bool
	refs                map[string]bool
}

func (v *typeParamRefVisitor) EnterTypeAnn(typeAnn TypeAnn) bool {
	if ref, ok := typeAnn.(*TypeRefTypeAnn); ok {
		// Check if this is a simple identifier (not qualified)
		if ident, ok := ref.Name.(*Ident); ok {
			name := ident.Name
			if v.availableTypeParams[name] {
				v.refs[name] = true
			}
		}
	}
	return true
}

// sortTypeParamsTopologically sorts type parameters so that dependencies come before dependents
func SortTypeParamsTopologically(typeParams []*TypeParam) []*TypeParam {
	if len(typeParams) <= 1 {
		return typeParams
	}

	// Build a map of type parameter names for quick lookup
	typeParamNames := make(map[string]bool)
	for _, tp := range typeParams {
		typeParamNames[tp.Name] = true
	}

	// Build dependency graph: map from type param name to list of type params it depends on
	deps := make(map[string][]string)
	for _, tp := range typeParams {
		var tpDeps []string
		if tp.Constraint != nil {
			tpDeps = append(tpDeps, extractTypeParamRefs(tp.Constraint, typeParamNames)...)
		}
		if tp.Default != nil {
			tpDeps = append(tpDeps, extractTypeParamRefs(tp.Default, typeParamNames)...)
		}
		deps[tp.Name] = tpDeps
	}

	// Perform topological sort using DFS
	sorted := make([]*TypeParam, 0, len(typeParams))
	visited := make(map[string]bool)
	visiting := make(map[string]bool) // For cycle detection

	var visit func(string) bool
	visit = func(name string) bool {
		if visited[name] {
			return true // Already processed
		}
		if visiting[name] {
			// Cycle detected - return false to indicate we should keep original order
			return false
		}

		visiting[name] = true
		for _, depName := range deps[name] {
			if !visit(depName) {
				return false
			}
		}
		visiting[name] = false
		visited[name] = true

		// Find the type param and add it to sorted list
		for _, tp := range typeParams {
			if tp.Name == name {
				sorted = append(sorted, tp)
				break
			}
		}
		return true
	}

	// Visit all type parameters
	for _, tp := range typeParams {
		if !visited[tp.Name] {
			if !visit(tp.Name) {
				// Cycle detected, return original order
				return typeParams
			}
		}
	}

	return sorted
}

// extractTypeParamRefs extracts all type parameter names referenced in a type annotation
func extractTypeParamRefs(typeAnn TypeAnn, availableTypeParams map[string]bool) []string {
	if typeAnn == nil {
		return nil
	}

	var refs []string
	var visitor typeParamRefVisitor
	visitor.availableTypeParams = availableTypeParams
	visitor.refs = make(map[string]bool)
	typeAnn.Accept(&visitor)

	// Convert map to slice
	for ref := range visitor.refs {
		refs = append(refs, ref)
	}
	return refs
}

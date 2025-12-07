package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

// TypeParamSubstitutionVisitor implements TypeVisitor for type parameter substitution
type TypeParamSubstitutionVisitor struct {
	substitutions map[string]type_system.Type
	// Stack of shadowed type parameters to handle scoping
	shadowStack []map[string]bool
}

// NewTypeParamSubstitutionVisitor creates a new visitor using the standard TypeVisitor pattern
func NewTypeParamSubstitutionVisitor(substitutions map[string]type_system.Type) *TypeParamSubstitutionVisitor {
	return &TypeParamSubstitutionVisitor{
		substitutions: substitutions,
		shadowStack:   []map[string]bool{},
	}
}

// SubstituteType is the main entry point for substituting type parameters in a type (backward compatibility)
func (v *TypeParamSubstitutionVisitor) SubstituteType(t type_system.Type) type_system.Type {
	if t == nil {
		return nil
	}

	t = type_system.Prune(t)
	return t.Accept(v)
}

func (v *TypeParamSubstitutionVisitor) EnterType(t type_system.Type) type_system.Type {
	// When entering a FuncType with type parameters, push shadowed parameters onto stack
	if funcType, ok := t.(*type_system.FuncType); ok && len(funcType.TypeParams) > 0 {
		shadows := make(map[string]bool)
		for _, param := range funcType.TypeParams {
			shadows[param.Name] = true
		}
		v.shadowStack = append(v.shadowStack, shadows)
	}
	return nil
}

func (v *TypeParamSubstitutionVisitor) ExitType(t type_system.Type) type_system.Type {
	// When exiting a FuncType with type parameters, pop shadowed parameters from stack
	if funcType, ok := t.(*type_system.FuncType); ok && len(funcType.TypeParams) > 0 {
		if len(v.shadowStack) > 0 {
			v.shadowStack = v.shadowStack[:len(v.shadowStack)-1]
		}
	}

	switch t := t.(type) {
	case *type_system.TypeRefType:
		// Check if this type parameter is shadowed by any inner function
		typeName := type_system.QualIdentToString(t.Name)
		if v.isShadowed(typeName) {
			// Don't substitute if shadowed
			return nil // Return original type unchanged
		}

		// Check if this is a type parameter reference that should be substituted
		if substitute, found := v.substitutions[typeName]; found {
			return substitute
		}

		// Handle type arguments if they exist
		if len(t.TypeArgs) > 0 {
			newTypeArgs := make([]type_system.Type, len(t.TypeArgs))
			changed := false
			for i, arg := range t.TypeArgs {
				newArg := arg.Accept(v)
				newTypeArgs[i] = newArg
				if newArg != arg {
					changed = true
				}
			}

			if changed {
				return type_system.NewTypeRefType(t.Provenance(), typeName, t.TypeAlias, newTypeArgs...)
			}
		}
	}
	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// isShadowed checks if a type parameter name is currently shadowed by any inner function
func (v *TypeParamSubstitutionVisitor) isShadowed(name string) bool {
	for _, shadows := range v.shadowStack {
		if shadows[name] {
			return true
		}
	}
	return false
}

// SubstituteTypeParams replaces type parameters in a type with their corresponding type arguments
func SubstituteTypeParams[T type_system.Type](t T, substitutions map[string]type_system.Type) T {
	if len(substitutions) == 0 {
		return t
	}

	t = type_system.Prune(t).(T)
	visitor := NewTypeParamSubstitutionVisitor(substitutions)
	result := t.Accept(visitor)
	return result.(T)
}

// createTypeParamSubstitutions creates a map of type parameter substitutions from type arguments and type parameters,
// handling default values when type arguments are nil.
func createTypeParamSubstitutions(typeArgs []type_system.Type, typeParams []*type_system.TypeParam) map[string]type_system.Type {
	substitutions := make(map[string]type_system.Type, len(typeArgs))
	for typeArg, param := range Zip(typeArgs, typeParams) {
		if param.Default != nil && typeArg == nil {
			// Use the default type if the type argument is nil
			substitutions[param.Name] = param.Default
		} else {
			substitutions[param.Name] = typeArg
		}
	}
	return substitutions
}

// generateSubstitutionSets creates substitution maps for type parameters and type arguments,
// handling cartesian products when union types are present in the type arguments.
func (c *Checker) generateSubstitutionSets(
	ctx Context,
	typeParams []*type_system.TypeParam,
	typeArgs []type_system.Type,
) ([]map[string]type_system.Type, []Error) {
	// If no type params or args, return empty slice
	if len(typeParams) == 0 || len(typeArgs) == 0 {
		return []map[string]type_system.Type{}, nil
	}

	var errors []Error

	// Extract all possible types for each type argument position
	argTypeSets := make([][]type_system.Type, len(typeArgs))
	for i, argType := range typeArgs {
		// TODO: recursively expand union types in case some of the elements are
		// also union types.
		argType, argErrors := c.ExpandType(ctx, argType, 1)
		if len(argErrors) > 0 {
			errors = append(errors, argErrors...)
		}
		if unionType, ok := argType.(*type_system.UnionType); ok {
			// For union types, use all the union members
			argTypeSets[i] = unionType.Types
		} else {
			// For non-union types, create a single-element slice
			argTypeSets[i] = []type_system.Type{argType}
		}
	}

	// Generate cartesian product
	var result []map[string]type_system.Type

	// Helper function to generate cartesian product recursively
	var generateCombinations func(int, map[string]type_system.Type)
	generateCombinations = func(pos int, current map[string]type_system.Type) {
		if pos >= len(typeParams) {
			// Make a copy of the current map and add it to results
			combination := make(map[string]type_system.Type)
			for k, v := range current {
				combination[k] = v
			}
			result = append(result, combination)
			return
		}

		// Get the type parameter name for this position
		typeParamName := typeParams[pos].Name

		// Try each possible type for this position
		for _, argType := range argTypeSets[pos] {
			current[typeParamName] = argType
			generateCombinations(pos+1, current)
		}
	}

	generateCombinations(0, make(map[string]type_system.Type))

	return result, errors
}

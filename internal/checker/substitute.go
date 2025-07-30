package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// TypeParamSubstitutionVisitor implements TypeVisitor for type parameter substitution
type TypeParamSubstitutionVisitor struct {
	substitutions map[string]Type
	// Stack of shadowed type parameters to handle scoping
	shadowStack []map[string]bool
}

// NewTypeParamSubstitutionVisitor creates a new visitor using the standard TypeVisitor pattern
func NewTypeParamSubstitutionVisitor(substitutions map[string]Type) *TypeParamSubstitutionVisitor {
	return &TypeParamSubstitutionVisitor{
		substitutions: substitutions,
		shadowStack:   []map[string]bool{},
	}
}

// SubstituteType is the main entry point for substituting type parameters in a type (backward compatibility)
func (v *TypeParamSubstitutionVisitor) SubstituteType(t Type) Type {
	if t == nil {
		return nil
	}

	t = Prune(t)
	return t.Accept(v)
}

func (v *TypeParamSubstitutionVisitor) EnterType(t Type) {
	// When entering a FuncType with type parameters, push shadowed parameters onto stack
	if funcType, ok := t.(*FuncType); ok && len(funcType.TypeParams) > 0 {
		shadows := make(map[string]bool)
		for _, param := range funcType.TypeParams {
			shadows[param.Name] = true
		}
		v.shadowStack = append(v.shadowStack, shadows)
	}
}

func (v *TypeParamSubstitutionVisitor) ExitType(t Type) Type {
	// When exiting a FuncType with type parameters, pop shadowed parameters from stack
	if funcType, ok := t.(*FuncType); ok && len(funcType.TypeParams) > 0 {
		if len(v.shadowStack) > 0 {
			v.shadowStack = v.shadowStack[:len(v.shadowStack)-1]
		}
	}

	switch t := t.(type) {
	case *TypeRefType:
		// Check if this type parameter is shadowed by any inner function
		if v.isShadowed(t.Name) {
			// Don't substitute if shadowed
			if len(t.TypeArgs) > 0 {
				// Still need to process type arguments
				newTypeArgs := make([]Type, len(t.TypeArgs))
				changed := false
				for i, arg := range t.TypeArgs {
					newArg := arg.Accept(v)
					newTypeArgs[i] = newArg
					if newArg != arg {
						changed = true
					}
				}

				if changed {
					result := NewTypeRefType(t.Name, t.TypeAlias, newTypeArgs...)
					if t.Provenance() != nil {
						result.SetProvenance(t.Provenance())
					}
					return result
				}
			}
			return nil // Return original type unchanged
		}

		// Check if this is a type parameter reference that should be substituted
		if substitute, found := v.substitutions[t.Name]; found {
			return substitute
		}

		// Handle type arguments if they exist
		if len(t.TypeArgs) > 0 {
			newTypeArgs := make([]Type, len(t.TypeArgs))
			changed := false
			for i, arg := range t.TypeArgs {
				newArg := arg.Accept(v)
				newTypeArgs[i] = newArg
				if newArg != arg {
					changed = true
				}
			}

			if changed {
				result := NewTypeRefType(t.Name, t.TypeAlias, newTypeArgs...)
				if t.Provenance() != nil {
					result.SetProvenance(t.Provenance())
				}
				return result
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

// substituteTypeParamsInObjElem handles substitution for object type elements (backward compatibility)
func (v *TypeParamSubstitutionVisitor) substituteTypeParamsInObjElem(elem ObjTypeElem) ObjTypeElem {
	return elem.Accept(v)
}

// substituteTypeParams replaces type parameters in a type with their corresponding type arguments
func (c *Checker) substituteTypeParams(t Type, substitutions map[string]Type) Type {
	if len(substitutions) == 0 {
		return t
	}

	t = Prune(t)
	visitor := NewTypeParamSubstitutionVisitor(substitutions)
	result := t.Accept(visitor)
	return result
}

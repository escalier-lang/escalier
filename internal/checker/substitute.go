package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// TypeParamSubstitutionVisitor implements TypeVisitor for type parameter substitution
type TypeParamSubstitutionVisitor struct {
	substitutions map[string]Type
}

// NewTypeParamSubstitutionVisitor creates a new visitor using the standard TypeVisitor pattern
func NewTypeParamSubstitutionVisitor(substitutions map[string]Type) *TypeParamSubstitutionVisitor {
	return &TypeParamSubstitutionVisitor{
		substitutions: substitutions,
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

func (v *TypeParamSubstitutionVisitor) VisitType(t Type) Type {
	switch t := t.(type) {
	case *TypeRefType:
		// Check if this is a type parameter reference
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

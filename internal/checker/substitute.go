package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// StandardTypeParamSubstitutionVisitor implements TypeVisitor for type parameter substitution
type StandardTypeParamSubstitutionVisitor struct {
	substitutions map[string]Type
	result        Type
}

// TypeParamSubstitutionVisitor is an alias for backward compatibility
type TypeParamSubstitutionVisitor = StandardTypeParamSubstitutionVisitor

// NewStandardTypeParamSubstitutionVisitor creates a new visitor using the standard TypeVisitor pattern
func NewStandardTypeParamSubstitutionVisitor(substitutions map[string]Type) *StandardTypeParamSubstitutionVisitor {
	return &StandardTypeParamSubstitutionVisitor{
		substitutions: substitutions,
		result:        nil,
	}
}

// NewTypeParamSubstitutionVisitor creates a new visitor (backward compatibility)
func NewTypeParamSubstitutionVisitor(substitutions map[string]Type) *StandardTypeParamSubstitutionVisitor {
	return NewStandardTypeParamSubstitutionVisitor(substitutions)
}

// SubstituteType is the main entry point for substituting type parameters in a type (backward compatibility)
func (v *StandardTypeParamSubstitutionVisitor) SubstituteType(t Type) Type {
	if t == nil {
		return nil
	}

	t = Prune(t)
	visitor := NewStandardTypeParamSubstitutionVisitor(v.substitutions)
	visitor.VisitType(t)

	if visitor.result != nil {
		return visitor.result
	}
	return t
}

func (v *StandardTypeParamSubstitutionVisitor) VisitType(t Type) {
	switch t := t.(type) {
	case *TypeRefType:
		v.visitTypeRefType(t)
	case *FuncType:
		v.visitFuncType(t)
	case *ObjectType:
		v.visitObjectType(t)
	case *TupleType:
		v.visitTupleType(t)
	case *UnionType:
		v.visitUnionType(t)
	case *IntersectionType:
		v.visitIntersectionType(t)
	case *RestSpreadType:
		v.visitRestSpreadType(t)
	default:
		// For primitive types, literals, and other types that don't contain type parameters
		v.result = t
	}
}

func (v *StandardTypeParamSubstitutionVisitor) visitTypeRefType(t *TypeRefType) {
	// Check if this is a type parameter reference
	if substitute, found := v.substitutions[t.Name]; found {
		v.result = substitute
		return
	}

	// Recursively substitute in type arguments
	var newTypeArgs []Type
	changed := false
	if len(t.TypeArgs) > 0 {
		newTypeArgs = make([]Type, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			newArg := v.substituteType(arg)
			newTypeArgs[i] = newArg
			if newArg != arg {
				changed = true
			}
		}
	}

	// Only create a new object if something changed
	if !changed {
		v.result = t
	} else {
		result := NewTypeRefType(t.Name, t.TypeAlias, newTypeArgs...)
		if t.Provenance() != nil {
			result.SetProvenance(t.Provenance())
		}
		v.result = result
	}
}

func (v *StandardTypeParamSubstitutionVisitor) visitFuncType(t *FuncType) {
	// Substitute in parameter types
	newParams := make([]*FuncParam, len(t.Params))
	for i, param := range t.Params {
		newParams[i] = &FuncParam{
			Pattern:  param.Pattern,
			Type:     v.substituteType(param.Type),
			Optional: param.Optional,
		}
	}

	var self Type
	if t.Self != nil {
		self = v.substituteType(t.Self)
	}

	// Substitute return and throws types
	result := &FuncType{
		TypeParams: t.TypeParams, // Type parameters remain unchanged
		Self:       self,
		Params:     newParams,
		Return:     v.substituteType(t.Return),
		Throws:     v.substituteType(t.Throws),
	}
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

func (v *StandardTypeParamSubstitutionVisitor) visitObjectType(t *ObjectType) {
	newElems := make([]ObjTypeElem, len(t.Elems))
	for i, elem := range t.Elems {
		newElems[i] = v.substituteTypeParamsInObjElem(elem)
	}

	result := NewObjectType(newElems)
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

func (v *StandardTypeParamSubstitutionVisitor) visitTupleType(t *TupleType) {
	newElems := make([]Type, len(t.Elems))
	for i, elem := range t.Elems {
		newElems[i] = v.substituteType(elem)
	}

	result := NewTupleType(newElems...)
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

func (v *StandardTypeParamSubstitutionVisitor) visitUnionType(t *UnionType) {
	newTypes := make([]Type, len(t.Types))
	for i, typ := range t.Types {
		newTypes[i] = v.substituteType(typ)
	}

	result := NewUnionType(newTypes...)
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

func (v *StandardTypeParamSubstitutionVisitor) visitIntersectionType(t *IntersectionType) {
	newTypes := make([]Type, len(t.Types))
	for i, typ := range t.Types {
		newTypes[i] = v.substituteType(typ)
	}

	result := NewIntersectionType(newTypes...)
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

func (v *StandardTypeParamSubstitutionVisitor) visitRestSpreadType(t *RestSpreadType) {
	substitutedType := v.substituteType(t.Type)
	result := NewRestSpreadType(substitutedType)
	if t.Provenance() != nil {
		result.SetProvenance(t.Provenance())
	}

	v.result = result
}

// substituteType is a helper method for recursive substitution
func (v *StandardTypeParamSubstitutionVisitor) substituteType(t Type) Type {
	if t == nil {
		return nil
	}

	t = Prune(t)
	visitor := NewStandardTypeParamSubstitutionVisitor(v.substitutions)
	// Don't use Accept here because it would cause unwanted recursion
	visitor.VisitType(t)

	if visitor.result != nil {
		return visitor.result
	}
	return t
}

// substituteTypeParamsInObjElem handles substitution for object type elements
func (v *StandardTypeParamSubstitutionVisitor) substituteTypeParamsInObjElem(elem ObjTypeElem) ObjTypeElem {
	switch elem := elem.(type) {
	case *PropertyElemType:
		return &PropertyElemType{
			Name:     elem.Name,
			Optional: elem.Optional,
			Readonly: elem.Readonly,
			Value:    v.substituteType(elem.Value),
		}
	case *MethodElemType:
		return &MethodElemType{
			Name: elem.Name,
			Fn:   v.substituteType(elem.Fn).(*FuncType),
		}
	case *GetterElemType:
		return &GetterElemType{
			Name: elem.Name,
			Fn:   v.substituteType(elem.Fn).(*FuncType),
		}
	case *SetterElemType:
		return &SetterElemType{
			Name: elem.Name,
			Fn:   v.substituteType(elem.Fn).(*FuncType),
		}
	case *CallableElemType:
		return &CallableElemType{
			Fn: v.substituteType(elem.Fn).(*FuncType),
		}
	case *ConstructorElemType:
		return &ConstructorElemType{
			Fn: v.substituteType(elem.Fn).(*FuncType),
		}
	case *RestSpreadElemType:
		return &RestSpreadElemType{
			Value: v.substituteType(elem.Value),
		}
	default:
		// For other element types that don't need substitution
		return elem
	}
}

// substituteTypeParams replaces type parameters in a type with their corresponding type arguments
// This implementation uses the TypeVisitor interface from types.go but avoids using the Accept()
// method directly because Accept() causes unwanted recursion for transformation use cases.
// Instead, we manually call VisitType() to maintain control over the traversal.
func (c *Checker) substituteTypeParams(t Type, substitutions map[string]Type) Type {
	if len(substitutions) == 0 {
		return t
	}

	t = Prune(t)
	visitor := NewStandardTypeParamSubstitutionVisitor(substitutions)
	// Don't use Accept here because it would cause unwanted recursion
	visitor.VisitType(t)

	if visitor.result != nil {
		return visitor.result
	}
	return t
}

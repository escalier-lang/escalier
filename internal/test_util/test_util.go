package test_util

import (
	"context"
	"fmt"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// typeAnnToType converts an ast.TypeAnn to a type_system.Type
// without calling infer. This is useful for testing and other scenarios
// where you want to directly convert a type annotation to a type.
func typeAnnToType(typeAnn ast.TypeAnn) Type {
	switch ta := typeAnn.(type) {
	case *ast.LitTypeAnn:
		return convertLitToLitType(ta.Lit)
	case *ast.NumberTypeAnn:
		return NewNumType()
	case *ast.StringTypeAnn:
		return NewStrType()
	case *ast.BooleanTypeAnn:
		return NewBoolType()
	case *ast.AnyTypeAnn:
		return NewAnyType()
	case *ast.UnknownTypeAnn:
		return NewUnknownType()
	case *ast.NeverTypeAnn:
		return NewNeverType()
	case *ast.TypeRefTypeAnn:
		name := ast.QualIdentToString(ta.Name)
		typeArgs := make([]Type, len(ta.TypeArgs))
		for i, arg := range ta.TypeArgs {
			typeArgs[i] = typeAnnToType(arg)
		}
		return NewTypeRefType(name, nil, typeArgs...)
	case *ast.TupleTypeAnn:
		elems := make([]Type, len(ta.Elems))
		for i, elem := range ta.Elems {
			elems[i] = typeAnnToType(elem)
		}
		return NewTupleType(elems...)
	case *ast.UnionTypeAnn:
		types := make([]Type, len(ta.Types))
		for i, t := range ta.Types {
			types[i] = typeAnnToType(t)
		}
		return NewUnionType(types...)
	case *ast.IntersectionTypeAnn:
		types := make([]Type, len(ta.Types))
		for i, t := range ta.Types {
			types[i] = typeAnnToType(t)
		}
		return NewIntersectionType(types...)
	case *ast.ObjectTypeAnn:
		elems := make([]ObjTypeElem, len(ta.Elems))
		for i, elem := range ta.Elems {
			elems[i] = convertObjTypeAnnElem(elem)
		}
		return NewObjectType(elems)
	case *ast.FuncTypeAnn:
		params := make([]*FuncParam, len(ta.Params))
		for i, param := range ta.Params {
			var paramType Type
			if param.TypeAnn != nil {
				paramType = typeAnnToType(param.TypeAnn)
			} else {
				paramType = NewAnyType() // Default to any if no type annotation
			}
			params[i] = &FuncParam{
				Pattern:  convertPatternToTypePat(param.Pattern),
				Type:     paramType,
				Optional: param.Optional,
			}
		}

		var returnType Type
		if ta.Return != nil {
			returnType = typeAnnToType(ta.Return)
		} else {
			returnType = NewNeverType() // Default to never if no return type
		}

		var throwsType Type
		if ta.Throws != nil {
			throwsType = typeAnnToType(ta.Throws)
		}

		// Convert TypeParams if needed
		var typeParams []*TypeParam
		if len(ta.TypeParams) > 0 {
			typeParams = make([]*TypeParam, len(ta.TypeParams))
			for i, tp := range ta.TypeParams {
				var constraint Type
				if tp.Constraint != nil {
					constraint = typeAnnToType(tp.Constraint)
				}
				var defaultType Type
				if tp.Default != nil {
					defaultType = typeAnnToType(tp.Default)
				}
				typeParams[i] = &TypeParam{
					Name:       tp.Name,
					Constraint: constraint,
					Default:    defaultType,
				}
			}
		}

		return &FuncType{
			TypeParams: typeParams,
			Self:       nil,
			Params:     params,
			Return:     returnType,
			Throws:     throwsType,
		}
	case *ast.KeyOfTypeAnn:
		targetType := typeAnnToType(ta.Type)
		return &KeyOfType{
			Type: targetType,
		}
	case *ast.IndexTypeAnn:
		targetType := typeAnnToType(ta.Target)
		indexType := typeAnnToType(ta.Index)
		return &IndexType{
			Target: targetType,
			Index:  indexType,
		}
	case *ast.CondTypeAnn:
		checkType := typeAnnToType(ta.Check)
		extendsType := typeAnnToType(ta.Extends)
		thenType := typeAnnToType(ta.Then)
		elseType := typeAnnToType(ta.Else)
		return NewCondType(checkType, extendsType, thenType, elseType)
	case *ast.InferTypeAnn:
		return NewInferType(ta.Name)
	case *ast.MutableTypeAnn:
		targetType := typeAnnToType(ta.Target)
		return NewMutableType(targetType)
	case *ast.WildcardTypeAnn:
		return &WildcardType{}
	default:
		panic(fmt.Sprintf("ConvertTypeAnnToType: unsupported type annotation: %T", typeAnn))
	}
}

// convertLitToLitType converts an ast.Lit to a type_system.LitType
func convertLitToLitType(lit ast.Lit) Type {
	switch l := lit.(type) {
	case *ast.StrLit:
		return NewLitType(&StrLit{Value: l.Value})
	case *ast.NumLit:
		return NewLitType(&NumLit{Value: l.Value})
	case *ast.BoolLit:
		return NewLitType(&BoolLit{Value: l.Value})
	case *ast.BigIntLit:
		return NewLitType(&BigIntLit{Value: l.Value})
	case *ast.NullLit:
		return NewLitType(&NullLit{})
	case *ast.UndefinedLit:
		return NewLitType(&UndefinedLit{})
	default:
		panic(fmt.Sprintf("convertLitToLitType: unsupported literal type: %T", lit))
	}
}

// convertPatternToTypePat converts an ast.Pat to a type_system.Pat
func convertPatternToTypePat(pat ast.Pat) Pat {
	switch p := pat.(type) {
	case *ast.IdentPat:
		return NewIdentPat(p.Name)
	case *ast.WildcardPat:
		return &WildcardPat{}
	case *ast.ObjectPat:
		elems := make([]ObjPatElem, len(p.Elems))
		for i, elem := range p.Elems {
			switch elem := elem.(type) {
			case *ast.ObjKeyValuePat:
				elems[i] = NewObjKeyValuePat(elem.Key.Name, convertPatternToTypePat(elem.Value))
			case *ast.ObjShorthandPat:
				elems[i] = NewObjShorthandPat(elem.Key.Name)
			case *ast.ObjRestPat:
				elems[i] = NewObjRestPat(convertPatternToTypePat(elem.Pattern))
			default:
				panic(fmt.Sprintf("convertPatternToTypePat: unsupported object pattern element: %T", elem))
			}
		}
		return NewObjectPat(elems)
	case *ast.TuplePat:
		elems := make([]Pat, len(p.Elems))
		for i, elem := range p.Elems {
			elems[i] = convertPatternToTypePat(elem)
		}
		return NewTuplePat(elems)
	case *ast.RestPat:
		innerPattern := convertPatternToTypePat(p.Pattern)
		return NewRestPat(innerPattern)
	case *ast.LitPat:
		panic("Literal patterns are not supported in type annotations")
	case *ast.ExtractorPat:
		panic("Extractor patterns are not supported in type annotations")
	default:
		panic("Unknown pattern type in type annotation")
	}
}

// convertObjTypeAnnElem converts an ast.ObjTypeAnnElem to a type_system.ObjTypeElem
func convertObjTypeAnnElem(elem ast.ObjTypeAnnElem) ObjTypeElem {
	switch e := elem.(type) {
	case *ast.PropertyTypeAnn:
		key := convertObjKey(e.Name)
		var valueType Type
		if e.Value != nil {
			valueType = typeAnnToType(e.Value)
		} else {
			valueType = NewLitType(&UndefinedLit{})
		}
		return &PropertyElemType{
			Name:     key,
			Optional: e.Optional,
			Readonly: e.Readonly,
			Value:    valueType,
		}
	case *ast.MethodTypeAnn:
		key := convertObjKey(e.Name)
		funcType := typeAnnToType(e.Fn).(*FuncType)
		return &MethodElemType{
			Name: key,
			Fn:   funcType,
		}
	case *ast.GetterTypeAnn:
		key := convertObjKey(e.Name)
		funcType := typeAnnToType(e.Fn).(*FuncType)
		return &GetterElemType{
			Name: key,
			Fn:   funcType,
		}
	case *ast.SetterTypeAnn:
		key := convertObjKey(e.Name)
		funcType := typeAnnToType(e.Fn).(*FuncType)
		return &SetterElemType{
			Name: key,
			Fn:   funcType,
		}
	case *ast.CallableTypeAnn:
		funcType := typeAnnToType(e.Fn).(*FuncType)
		return &CallableElemType{
			Fn: funcType,
		}
	case *ast.ConstructorTypeAnn:
		funcType := typeAnnToType(e.Fn).(*FuncType)
		return &ConstructorElemType{
			Fn: funcType,
		}
	case *ast.RestSpreadTypeAnn:
		valueType := typeAnnToType(e.Value)
		return NewRestSpreadElemType(valueType)
	case *ast.MappedTypeAnn:
		var constraintType Type
		if e.TypeParam.Constraint != nil {
			constraintType = typeAnnToType(e.TypeParam.Constraint)
		} else {
			constraintType = NewAnyType()
		}

		typeParam := &IndexParamType{
			Name:       e.TypeParam.Name,
			Constraint: constraintType,
		}

		valueType := typeAnnToType(e.Value)

		// Create the MappedElemType directly since the name field is unexported
		mapped := &MappedElemType{
			TypeParam: typeParam,
			Value:     valueType,
			Optional:  convertMappedModifier(e.Optional),
			ReadOnly:  convertMappedModifier(e.ReadOnly),
		}

		return mapped
	default:
		panic(fmt.Sprintf("convertObjTypeAnnElem: unsupported object element type: %T", elem))
	}
}

// convertObjKey converts an ast.ObjKey to a type_system.ObjTypeKey
func convertObjKey(key ast.ObjKey) ObjTypeKey {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return NewStrKey(k.Name)
	case *ast.StrLit:
		return NewStrKey(k.Value)
	case *ast.NumLit:
		return NewNumKey(k.Value)
	case *ast.ComputedKey:
		// For computed keys, we'd need more context to determine the actual key
		// For now, we'll treat it as a string key with a placeholder
		return NewStrKey("[computed]")
	default:
		panic(fmt.Sprintf("convertObjKey: unsupported key type: %T", key))
	}
}

// convertMappedModifier converts an ast.MappedModifier to a type_system.MappedModifier
func convertMappedModifier(mod *ast.MappedModifier) *MappedModifier {
	if mod == nil {
		return nil
	}
	switch *mod {
	case ast.MMAdd:
		result := MMAdd
		return &result
	case ast.MMRemove:
		result := MMRemove
		return &result
	default:
		panic(fmt.Sprintf("convertMappedModifier: unsupported modifier: %v", *mod))
	}
}

// ParseTypeAnn parses a type annotation string and converts the resulting
// *ast.TypeAnn to a type_system.Type.
//
// This function combines the parsing step (string -> ast.TypeAnn) with the
// conversion step (ast.TypeAnn -> type_system.Type) for convenience.
func ParseTypeAnn(typeAnnStr string) Type {
	// Create a context with a timeout for parsing
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Parse the type annotation string using the parser
	typeAnn, parseErrors := parser.ParseTypeAnn(ctx, typeAnnStr)

	// Check for parsing errors
	if len(parseErrors) > 0 {
		// Return the first error
		firstError := parseErrors[0]
		panic(fmt.Sprintf("parsing error at %s: %s", firstError.Span.Start, firstError.Message))
	}

	// Convert the parsed AST type annotation to a type system type
	return typeAnnToType(typeAnn)
}

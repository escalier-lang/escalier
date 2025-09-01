package graphql

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/vektah/gqlparser/v2/ast"
)

// GraphQLInferenceResult contains the inferred types for a GraphQL operation
type GraphQLInferenceResult struct {
	ResultType    Type
	VariablesType Type
}

// InferGraphQLQuery infers the types for a GraphQL query operation
func InferGraphQLQuery(schema *ast.Schema, queryDoc *ast.QueryDocument) *GraphQLInferenceResult {

	// Only handle the first operation for now
	if len(queryDoc.Operations) == 0 {
		panic("no operations in query document")
	}
	op := queryDoc.Operations[0]

	// Find the root type for the operation (query/mutation/subscription)
	var rootType *ast.Definition
	switch op.Operation {
	case ast.Query:
		if schema.Query == nil {
			panic("schema.Query is nil")
		}
		rootType = schema.Query
	case ast.Mutation:
		if schema.Mutation == nil {
			panic("schema.Mutation is nil")
		}
		rootType = schema.Mutation
	case ast.Subscription:
		if schema.Subscription == nil {
			panic("schema.Subscription is nil")
		}
		rootType = schema.Subscription
	default:
		panic("unknown operation type")
	}
	if rootType == nil {
		panic("root type not found in schema")
	}

	// Helper to recursively infer the type of a selection set
	var inferSelectionSet func(parentType *ast.Definition, selSet ast.SelectionSet) Type
	inferSelectionSet = func(parentType *ast.Definition, selSet ast.SelectionSet) Type {
		// Helper to process union types with inline fragments
		inferUnionType := func(unionDef *ast.Definition, selectionSet ast.SelectionSet) Type {
			var unionTypes []Type
			// Collect inline fragments from the selection set
			unionInlineFragments := make(map[string]ast.SelectionSet)
			for _, sel := range selectionSet {
				if frag, ok := sel.(*ast.InlineFragment); ok && frag.TypeCondition != "" {
					unionInlineFragments[frag.TypeCondition] = frag.SelectionSet
				}
			}
			for _, t := range unionDef.Types {
				typeDef := schema.Types[t]
				if typeDef == nil {
					continue
				}
				selSetForType, ok := unionInlineFragments[t]
				if !ok {
					selSetForType = ast.SelectionSet{}
				}
				unionTypes = append(unionTypes, inferSelectionSet(typeDef, selSetForType))
			}
			return NewUnionType(unionTypes...)
		}

		var elems []ObjTypeElem
		// Collect inline fragments by type condition
		inlineFragments := make(map[string]ast.SelectionSet)
		var otherFields []ast.Selection
		for _, sel := range selSet {
			switch frag := sel.(type) {
			case *ast.InlineFragment:
				if frag.TypeCondition != "" {
					inlineFragments[frag.TypeCondition] = frag.SelectionSet
				}
			case *ast.Field:
				otherFields = append(otherFields, sel)
			}
		}
		for _, sel := range otherFields {
			field := sel.(*ast.Field)
			fieldDef := schema.Types[parentType.Name].Fields.ForName(field.Name)
			if fieldDef == nil {
				continue // skip unknown fields
			}

			var fieldType Type

			// Check if the field type is a list
			if fieldDef.Type.Elem != nil {
				// This is a list type - get the element type
				elemTypeName := fieldDef.Type.Elem.Name()
				elemTypeDef := schema.Types[elemTypeName]
				var elemType Type

				if elemTypeDef != nil && elemTypeDef.Kind == ast.Object && len(field.SelectionSet) > 0 {
					// Recursively infer subfields for object element types
					elemType = inferSelectionSet(elemTypeDef, field.SelectionSet)
				} else if elemTypeDef != nil && elemTypeDef.Kind == ast.Union && len(field.SelectionSet) > 0 {
					// Handle union element types with inline fragments
					elemType = inferUnionType(elemTypeDef, field.SelectionSet)
				} else {
					// Use InferGraphQLType to handle the element type conversion
					elemType = InferGraphQLType(schema, fieldDef.Type.Elem)
				}

				// Wrap in Array type
				fieldType = NewTypeRefType("Array", nil, elemType)

				// Check if the list itself is nullable
				if !fieldDef.Type.NonNull {
					nullType := NewLitType(&NullLit{})
					fieldType = NewUnionType(fieldType, nullType)
				}
			} else {
				// Not a list type - handle as before
				fieldTypeDef := schema.Types[fieldDef.Type.Name()]
				if fieldTypeDef != nil && fieldTypeDef.Kind == ast.Object && len(field.SelectionSet) > 0 {
					// Recursively infer subfields for object types
					fieldType = inferSelectionSet(fieldTypeDef, field.SelectionSet)

					// Check if this object field is nullable and add | null if so
					if !fieldDef.Type.NonNull {
						nullType := NewLitType(&NullLit{})
						fieldType = NewUnionType(fieldType, nullType)
					}
				} else if fieldTypeDef != nil && fieldTypeDef.Kind == ast.Union {
					// For unions, use the field's selection set for inline fragments
					fieldType = inferUnionType(fieldTypeDef, field.SelectionSet)
				} else {
					// Use InferGraphQLType to handle the field type conversion
					fieldType = InferGraphQLType(schema, fieldDef.Type)
				}
			}

			// Check if the field is nullable (not non-null) for property optionality
			isNullable := !fieldDef.Type.NonNull

			// Create property element with optional flag for nullable fields
			propertyElem := &PropertyElemType{
				Name:     NewStrKey(field.Name),
				Optional: isNullable,
				Readonly: false,
				Value:    fieldType,
			}
			elems = append(elems, propertyElem)
		}
		return NewObjectType(elems)
	}

	// Infer the result type from the selection set
	resultType := inferSelectionSet(rootType, op.SelectionSet)

	// Infer the variables type from the operation's variable definitions
	var variablesElems []ObjTypeElem
	for _, varDef := range op.VariableDefinitions {
		varType := InferGraphQLType(schema, varDef.Type)
		isNullable := !varDef.Type.NonNull

		propertyElem := &PropertyElemType{
			Name:     NewStrKey(varDef.Variable),
			Optional: isNullable,
			Readonly: false,
			Value:    varType,
		}
		variablesElems = append(variablesElems, propertyElem)
	}
	variablesType := NewObjectType(variablesElems)

	return &GraphQLInferenceResult{
		ResultType:    resultType,
		VariablesType: variablesType,
	}
}

// InferGraphQLType converts a GraphQL type definition to an Escalier type
func InferGraphQLType(schema *ast.Schema, gqlType *ast.Type) Type {
	// Handle list types
	if gqlType.Elem != nil {
		elemType := InferGraphQLType(schema, gqlType.Elem)
		// For GraphQL lists, we can represent them as arrays/tuples
		// For now, let's use a simple array representation
		arrayType := NewTypeRefType("Array<"+elemType.String()+">", nil)

		// If this list type is nullable (NonNull is false), create union with null
		if !gqlType.NonNull {
			nullType := NewLitType(&NullLit{})
			return NewUnionType(arrayType, nullType)
		}
		return arrayType
	}

	// Handle named types
	if gqlType.NamedType != "" {
		var baseType Type
		switch gqlType.NamedType {
		case "String":
			baseType = NewStrType()
		case "Int", "Float":
			baseType = NewNumType()
		case "Boolean":
			baseType = NewBoolType()
		case "ID":
			// Keep ID as a type reference to preserve the ID semantics
			baseType = NewTypeRefType("ID", nil)
		default:
			// Check if it's a defined type in the schema
			if typeDef := schema.Types[gqlType.NamedType]; typeDef != nil {
				switch typeDef.Kind {
				case ast.Enum:
					// Expand enums as a union of string literal types
					var enumTypes []Type
					for _, v := range typeDef.EnumValues {
						enumTypes = append(enumTypes, NewLitType(&StrLit{Value: v.Name}))
					}
					baseType = NewUnionType(enumTypes...)
				case ast.Scalar:
					// For custom scalars, fall back to the base type or use a type reference
					baseType = NewTypeRefType(gqlType.NamedType, nil)
				case ast.InputObject:
					// For input objects, create an object type with all fields
					var elems []ObjTypeElem
					for _, field := range typeDef.Fields {
						fieldType := InferGraphQLType(schema, field.Type)
						isNullable := !field.Type.NonNull
						propertyElem := &PropertyElemType{
							Name:     NewStrKey(field.Name),
							Optional: isNullable,
							Readonly: false,
							Value:    fieldType,
						}
						elems = append(elems, propertyElem)
					}
					baseType = NewObjectType(elems)
				case ast.Object, ast.Interface, ast.Union:
					// For other types (Object, Interface, Union), use type reference
					baseType = NewTypeRefType(gqlType.NamedType, nil)
				default:
					// Fallback for any other types
					baseType = NewTypeRefType(gqlType.NamedType, nil)
				}
			} else {
				// Fallback to type reference for unknown types
				baseType = NewTypeRefType(gqlType.NamedType, nil)
			}
		}

		// If the type is nullable (NonNull is false), create union with null
		if !gqlType.NonNull {
			nullType := NewLitType(&NullLit{})
			return NewUnionType(baseType, nullType)
		}
		return baseType
	}

	// Fallback - should not reach here in normal cases
	return NewTypeRefType("Unknown", nil)
}

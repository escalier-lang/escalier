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
			fieldTypeDef := schema.Types[fieldDef.Type.Name()]
			var fieldType Type
			if fieldTypeDef != nil && fieldTypeDef.Kind == ast.Object && len(field.SelectionSet) > 0 {
				// Recursively infer subfields for object types
				fieldType = inferSelectionSet(fieldTypeDef, field.SelectionSet)
			} else if fieldTypeDef != nil && fieldTypeDef.Kind == ast.Union {
				// For unions, always use the field's selection set (not the selection set of the field node)
				// so that inline fragments are visible and can be matched
				def := fieldTypeDef
				var unionTypes []Type
				// Collect inline fragments from the field's selection set
				unionInlineFragments := make(map[string]ast.SelectionSet)
				for _, sel := range field.SelectionSet {
					if frag, ok := sel.(*ast.InlineFragment); ok && frag.TypeCondition != "" {
						unionInlineFragments[frag.TypeCondition] = frag.SelectionSet
					}
				}
				for _, t := range def.Types {
					unionDef := schema.Types[t]
					if unionDef == nil {
						continue
					}
					selSetForType, ok := unionInlineFragments[t]
					if !ok {
						selSetForType = ast.SelectionSet{}
					}
					unionTypes = append(unionTypes, inferSelectionSet(unionDef, selSetForType))
				}
				fieldType = NewUnionType(unionTypes...)
			} else {
				switch fieldDef.Type.Name() {
				case "String":
					fieldType = NewStrType()
				case "Int", "Float":
					fieldType = NewNumType()
				case "Boolean":
					fieldType = NewBoolType()
				default:
					def := schema.Types[fieldDef.Type.Name()]
					if def == nil {
						// fallback to TypeRefType if not found
						fieldType = NewTypeRefType(fieldDef.Type.Name(), nil)
					} else if def.Kind == ast.Enum {
						// Expand enums as a union of string literal types
						var enumTypes []Type
						for _, v := range def.EnumValues {
							enumTypes = append(enumTypes, NewLitType(&StrLit{Value: v.Name}))
						}
						fieldType = NewUnionType(enumTypes...)
					} else {
						// fallback to TypeRefType for other types (interfaces, etc.)
						fieldType = NewTypeRefType(fieldDef.Type.Name(), nil)
					}
				}
			}

			// Check if the field is nullable (not non-null)
			isNullable := !fieldDef.Type.NonNull
			if isNullable {
				// For nullable fields, create a union with null and make the property optional
				nullType := NewLitType(&NullLit{})
				fieldType = NewUnionType(fieldType, nullType)
			}

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
		isNullable := !varDef.Type.NonNull
		varType := InferGraphQLType(schema, varDef.Type)
		// For variables, we don't make them optional properties since they're explicitly declared
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
			// ID can be string or number, but typically treated as string
			baseType = NewStrType()
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

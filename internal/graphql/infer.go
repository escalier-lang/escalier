package graphql

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	. "github.com/escalier-lang/escalier/internal/type_system"
	gqlast "github.com/vektah/gqlparser/v2/ast"
)

// GraphQLProvenance tracks the source of a type from GraphQL schema/query
type GraphQLProvenance struct {
	Position *gqlast.Position
}

func (*GraphQLProvenance) IsProvenance() {}

var _ provenance.Provenance = (*GraphQLProvenance)(nil)

// GraphQLInferenceResult contains the inferred types for a GraphQL operation
type GraphQLInferenceResult struct {
	ResultType    Type
	VariablesType Type
}

// inferUnionType processes union types with inline fragments
func inferUnionType(schema *gqlast.Schema, unionDef *gqlast.Definition, selectionSet gqlast.SelectionSet) Type {
	var unionTypes []Type
	// Collect inline fragments from the selection set
	unionInlineFragments := make(map[string]gqlast.SelectionSet)
	for _, sel := range selectionSet {
		if frag, ok := sel.(*gqlast.InlineFragment); ok && frag.TypeCondition != "" {
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
			selSetForType = gqlast.SelectionSet{}
		}
		unionTypes = append(unionTypes, inferSelectionSet(schema, typeDef, selSetForType))
	}
	unionType := NewUnionType(unionTypes...)
	unionType.SetProvenance(&GraphQLProvenance{
		Position: unionDef.Position,
	})
	return unionType
}

// inferSelectionSet recursively infers the type of a GraphQL selection set
func inferSelectionSet(schema *gqlast.Schema, parentType *gqlast.Definition, selSet gqlast.SelectionSet) Type {
	var elems []ObjTypeElem

	// Collect inline fragments by type condition
	inlineFragments := make(map[string]gqlast.SelectionSet)
	var otherFields []gqlast.Selection

	for _, sel := range selSet {
		switch frag := sel.(type) {
		case *gqlast.InlineFragment:
			if frag.TypeCondition != "" {
				inlineFragments[frag.TypeCondition] = frag.SelectionSet
			}
		case *gqlast.Field:
			otherFields = append(otherFields, sel)
		}
	}

	for _, sel := range otherFields {
		field := sel.(*gqlast.Field)
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

			if elemTypeDef != nil && elemTypeDef.Kind == gqlast.Object && len(field.SelectionSet) > 0 {
				// Recursively infer subfields for object element types
				elemType = inferSelectionSet(schema, elemTypeDef, field.SelectionSet)
			} else if elemTypeDef != nil && elemTypeDef.Kind == gqlast.Union && len(field.SelectionSet) > 0 {
				// Handle union element types with inline fragments
				elemType = inferUnionType(schema, elemTypeDef, field.SelectionSet)
			} else {
				// Use InferGraphQLType to handle the element type conversion
				elemType = InferGraphQLType(schema, fieldDef.Type.Elem)
			}

			// Wrap in Array type
			arrayType := NewTypeRefType("Array", nil, elemType)
			arrayType.SetProvenance(&GraphQLProvenance{
				Position: field.Position,
			})
			fieldType = arrayType

			// Check if the list itself is nullable
			if !fieldDef.Type.NonNull {
				nullType := NewLitType(&NullLit{})
				fieldType = NewUnionType(fieldType, nullType)
				fieldType.SetProvenance(&GraphQLProvenance{
					Position: fieldDef.Position,
				})
			}
		} else {
			// Not a list type - handle as before
			fieldTypeDef := schema.Types[fieldDef.Type.Name()]
			if fieldTypeDef != nil && fieldTypeDef.Kind == gqlast.Object && len(field.SelectionSet) > 0 {
				// Recursively infer subfields for object types
				fieldType = inferSelectionSet(schema, fieldTypeDef, field.SelectionSet)

				// Check if this object field is nullable and add | null if so
				if !fieldDef.Type.NonNull {
					nullType := NewLitType(&NullLit{})
					fieldType = NewUnionType(fieldType, nullType)
					fieldType.SetProvenance(&GraphQLProvenance{
						Position: fieldDef.Position,
					})
				}
			} else if fieldTypeDef != nil && fieldTypeDef.Kind == gqlast.Union {
				// For unions, use the field's selection set for inline fragments
				fieldType = inferUnionType(schema, fieldTypeDef, field.SelectionSet)
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

	objType := NewObjectType(elems)
	objType.SetProvenance(&GraphQLProvenance{
		Position: parentType.Position,
	})
	return objType
}

// InferGraphQLQuery infers the types for a GraphQL query operation
func InferGraphQLQuery(schema *gqlast.Schema, doc *gqlast.QueryDocument) *GraphQLInferenceResult {

	// Only handle the first operation for now
	if len(doc.Operations) == 0 {
		panic("no operations in query document")
	}
	op := doc.Operations[0]

	// Find the root type for the operation (query/mutation/subscription)
	var rootType *gqlast.Definition
	switch op.Operation {
	case gqlast.Query:
		if schema.Query == nil {
			panic("schema.Query is nil")
		}
		rootType = schema.Query
	case gqlast.Mutation:
		if schema.Mutation == nil {
			panic("schema.Mutation is nil")
		}
		rootType = schema.Mutation
	case gqlast.Subscription:
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

	// Infer the result type from the selection set
	resultType := inferSelectionSet(schema, rootType, op.SelectionSet)

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
	variablesType.SetProvenance(&GraphQLProvenance{
		Position: op.Position,
	})

	return &GraphQLInferenceResult{
		ResultType:    resultType,
		VariablesType: variablesType,
	}
}

// InferGraphQLType converts a GraphQL type definition to an Escalier type
func InferGraphQLType(schema *gqlast.Schema, gqlType *gqlast.Type) Type {
	// Handle named types
	if gqlType.NamedType != "" {
		var baseType Type
		switch gqlType.NamedType {
		case "String":
			baseType = NewStrType()
			baseType.SetProvenance(&GraphQLProvenance{
				Position: gqlType.Position,
			})
		case "Int", "Float":
			baseType = NewNumType()
			baseType.SetProvenance(&GraphQLProvenance{
				Position: gqlType.Position,
			})
		case "Boolean":
			baseType = NewBoolType()
			baseType.SetProvenance(&GraphQLProvenance{
				Position: gqlType.Position,
			})
		case "ID":
			// Keep ID as a type reference to preserve the ID semantics
			baseType = NewTypeRefType("ID", nil)
			baseType.SetProvenance(&GraphQLProvenance{
				Position: gqlType.Position,
			})
		default:
			// Check if it's a defined type in the schema
			if typeDef := schema.Types[gqlType.NamedType]; typeDef != nil {
				switch typeDef.Kind {
				case gqlast.Enum:
					// Expand enums as a union of string literal types
					var enumTypes []Type
					for _, v := range typeDef.EnumValues {
						enumType := NewLitType(&StrLit{Value: v.Name})
						enumType.SetProvenance(&GraphQLProvenance{
							Position: v.Position,
						})
						enumTypes = append(enumTypes, enumType)
					}
					baseType = NewUnionType(enumTypes...)
					baseType.SetProvenance(&GraphQLProvenance{
						Position: typeDef.Position,
					})
				case gqlast.Scalar:
					// For custom scalars, fall back to the base type or use a type reference
					baseType = NewTypeRefType(gqlType.NamedType, nil)
					baseType.SetProvenance(&GraphQLProvenance{
						Position: typeDef.Position,
					})
				case gqlast.InputObject:
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
					baseType.SetProvenance(&GraphQLProvenance{
						Position: typeDef.Position,
					})
				case gqlast.Object, gqlast.Interface, gqlast.Union:
					// For other types (Object, Interface, Union), use type reference
					baseType = NewTypeRefType(gqlType.NamedType, nil)
					baseType.SetProvenance(&GraphQLProvenance{
						Position: typeDef.Position,
					})
				default:
					// Fallback for any other types
					baseType = NewTypeRefType(gqlType.NamedType, nil)
					baseType.SetProvenance(&GraphQLProvenance{
						Position: typeDef.Position,
					})
				}
			} else {
				// Fallback to type reference for unknown types
				baseType = NewTypeRefType(gqlType.NamedType, nil)
				baseType.SetProvenance(&GraphQLProvenance{
					Position: gqlType.Position,
				})
			}
		}

		// If the type is nullable (NonNull is false), create union with null
		if !gqlType.NonNull {
			nullType := NewLitType(&NullLit{})
			unionType := NewUnionType(baseType, nullType)
			unionType.SetProvenance(&GraphQLProvenance{
				Position: gqlType.Position,
			})
			return unionType
		}
		return baseType
	}

	// Fallback - should not reach here in normal cases
	panic("unexpected GraphQL type structure")
}

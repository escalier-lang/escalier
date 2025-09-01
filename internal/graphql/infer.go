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
					} else if def.Kind == ast.Object {
						// Expand custom object types as their fields
						var allFields ast.SelectionSet
						for _, f := range def.Fields {
							allFields = append(allFields, &ast.Field{
								Name:             f.Name,
								SelectionSet:     nil, // no sub-selection by default
								Alias:            "",
								Arguments:        nil,
								Directives:       nil,
								Position:         nil,
								Comment:          nil,
								Definition:       f,
								ObjectDefinition: def,
							})
						}
						fieldType = inferSelectionSet(def, allFields)
					} else {
						// fallback to TypeRefType for other types (interfaces, etc.)
						fieldType = NewTypeRefType(fieldDef.Type.Name(), nil)
					}
				}
			}
			elems = append(elems, NewPropertyElemType(NewStrKey(field.Name), fieldType))
		}
		return NewObjectType(elems)
	}

	// Infer the result type from the selection set
	resultType := inferSelectionSet(rootType, op.SelectionSet)

	// Infer the variables type from the operation's variable definitions
	var variablesElems []ObjTypeElem
	for _, varDef := range op.VariableDefinitions {
		varType := InferGraphQLType(schema, varDef.Type)
		variablesElems = append(variablesElems, NewPropertyElemType(NewStrKey(varDef.Variable), varType))
	}
	variablesType := NewObjectType(variablesElems)

	return &GraphQLInferenceResult{
		ResultType:    resultType,
		VariablesType: variablesType,
	}
}

// InferGraphQLType converts a GraphQL type definition to an Escalier type
func InferGraphQLType(schema *ast.Schema, gqlType *ast.Type) Type {
	// Handle non-null wrapper
	if gqlType.NonNull {
		// For non-null types, we just return the inner type (Escalier doesn't have explicit null handling here)
		innerType := &ast.Type{
			NamedType: gqlType.NamedType,
			Elem:      gqlType.Elem,
			NonNull:   false, // Remove the non-null wrapper
			Position:  gqlType.Position,
		}
		return InferGraphQLType(schema, innerType)
	}

	// Handle list types
	if gqlType.Elem != nil {
		elemType := InferGraphQLType(schema, gqlType.Elem)
		// For GraphQL lists, we can represent them as arrays/tuples
		// For now, let's use a simple array representation
		return NewTypeRefType("Array<"+elemType.String()+">", nil)
	}

	// Handle named types
	if gqlType.NamedType != "" {
		switch gqlType.NamedType {
		case "String":
			return NewStrType()
		case "Int", "Float":
			return NewNumType()
		case "Boolean":
			return NewBoolType()
		case "ID":
			// ID can be string or number, but typically treated as string
			return NewStrType()
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
					return NewUnionType(enumTypes...)
				case ast.Scalar:
					// For custom scalars, fall back to the base type or use a type reference
					return NewTypeRefType(gqlType.NamedType, nil)
				case ast.InputObject:
					// For input objects, create an object type with all fields
					var elems []ObjTypeElem
					for _, field := range typeDef.Fields {
						fieldType := InferGraphQLType(schema, field.Type)
						elems = append(elems, NewPropertyElemType(NewStrKey(field.Name), fieldType))
					}
					return NewObjectType(elems)
				case ast.Object, ast.Interface, ast.Union:
					// For other types (Object, Interface, Union), use type reference
					return NewTypeRefType(gqlType.NamedType, nil)
				default:
					// Fallback for any other types
					return NewTypeRefType(gqlType.NamedType, nil)
				}
			}
			// Fallback to type reference for unknown types
			return NewTypeRefType(gqlType.NamedType, nil)
		}
	}

	// Fallback - should not reach here in normal cases
	return NewTypeRefType("Unknown", nil)
}

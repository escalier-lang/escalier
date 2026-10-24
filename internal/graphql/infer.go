package graphql

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	. "github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	gqlast "github.com/vektah/gqlparser/v2/ast"
)

// GraphQLProvenance is the position in a GraphQL schema or query that a type was inferred from.
type GraphQLProvenance struct {
	Position *gqlast.Position
}

func (*GraphQLProvenance) IsProvenance() {}

var _ provenance.Provenance = (*GraphQLProvenance)(nil)

// GraphQLInferenceResult contains the inferred types for a GraphQL operation. Prov maps each
// type the inference minted to a solver.FromExternal whose Source is a *GraphQLProvenance.
type GraphQLInferenceResult struct {
	ResultType    Type
	VariablesType Type
	Prov          solver.Prov
}

// inferrer holds the schema an inference reads and the provenance table it fills.
type inferrer struct {
	schema *gqlast.Schema
	prov   solver.Prov
}

// record maps t to pos in the provenance table and returns t.
func (in *inferrer) record(t Type, pos *gqlast.Position) Type {
	in.prov[t] = solver.FromExternal{Source: &GraphQLProvenance{Position: pos}}
	return t
}

// inferUnionType processes union types with inline fragments
func (in *inferrer) inferUnionType(unionDef *gqlast.Definition, selectionSet gqlast.SelectionSet) Type {
	var unionTypes []Type
	// Collect inline fragments from the selection set
	unionInlineFragments := make(map[string]gqlast.SelectionSet)
	for _, sel := range selectionSet {
		if frag, ok := sel.(*gqlast.InlineFragment); ok && frag.TypeCondition != "" {
			unionInlineFragments[frag.TypeCondition] = frag.SelectionSet
		}
	}
	for _, t := range unionDef.Types {
		typeDef := in.schema.Types[t]
		if typeDef == nil {
			continue
		}
		selSetForType, ok := unionInlineFragments[t]
		if !ok {
			selSetForType = gqlast.SelectionSet{}
		}
		unionTypes = append(unionTypes, in.inferSelectionSet(typeDef, selSetForType))
	}
	return in.record(&UnionType{Types: unionTypes}, unionDef.Position)
}

// inferSelectionSet recursively infers the type of a GraphQL selection set
func (in *inferrer) inferSelectionSet(parentType *gqlast.Definition, selSet gqlast.SelectionSet) Type {
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
		fieldDef := in.schema.Types[parentType.Name].Fields.ForName(field.Name)
		if fieldDef == nil {
			continue // skip unknown fields
		}

		var fieldType Type

		// Check if the field type is a list
		if fieldDef.Type.Elem != nil {
			// This is a list type - get the element type
			elemTypeName := fieldDef.Type.Elem.Name()
			elemTypeDef := in.schema.Types[elemTypeName]
			var elemType Type

			if elemTypeDef != nil && elemTypeDef.Kind == gqlast.Object && len(field.SelectionSet) > 0 {
				// Recursively infer subfields for object element types
				elemType = in.inferSelectionSet(elemTypeDef, field.SelectionSet)
			} else if elemTypeDef != nil && elemTypeDef.Kind == gqlast.Union && len(field.SelectionSet) > 0 {
				// Handle union element types with inline fragments
				elemType = in.inferUnionType(elemTypeDef, field.SelectionSet)
			} else {
				// Use inferType to handle the element type conversion
				elemType = in.inferType(fieldDef.Type.Elem)
			}

			// Wrap in Array type
			fieldType = in.record(&ClassType{Name: "Array", TypeArgs: []Type{elemType}}, field.Position)

			// Check if the list itself is nullable
			if !fieldDef.Type.NonNull {
				nullType := &NullType{}
				fieldType = in.record(&UnionType{Types: []Type{fieldType, nullType}}, fieldDef.Position)
			}
		} else {
			// Not a list type - handle as before
			fieldTypeDef := in.schema.Types[fieldDef.Type.Name()]
			if fieldTypeDef != nil && fieldTypeDef.Kind == gqlast.Object && len(field.SelectionSet) > 0 {
				// Recursively infer subfields for object types
				fieldType = in.inferSelectionSet(fieldTypeDef, field.SelectionSet)

				// Check if this object field is nullable and add | null if so
				if !fieldDef.Type.NonNull {
					nullType := &NullType{}
					fieldType = in.record(&UnionType{Types: []Type{fieldType, nullType}}, fieldDef.Position)
				}
			} else if fieldTypeDef != nil && fieldTypeDef.Kind == gqlast.Union {
				// For unions, use the field's selection set for inline fragments
				fieldType = in.inferUnionType(fieldTypeDef, field.SelectionSet)
			} else {
				// Use inferType to handle the field type conversion
				fieldType = in.inferType(fieldDef.Type)
			}
		}

		// Check if the field is nullable (not non-null) for property optionality
		isNullable := !fieldDef.Type.NonNull

		// Create property element with optional flag for nullable fields
		propertyElem := &PropertyElem{
			Name:     field.Name,
			Optional: isNullable,
			Readonly: false,
			Type:     fieldType,
		}
		elems = append(elems, propertyElem)
	}

	return in.record(&ObjectType{Elems: elems}, parentType.Position)
}

// InferGraphQLQuery infers the types for a GraphQL query operation
func InferGraphQLQuery(schema *gqlast.Schema, doc *gqlast.QueryDocument) *GraphQLInferenceResult {
	in := &inferrer{schema: schema, prov: solver.Prov{}}

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
	resultType := in.inferSelectionSet(rootType, op.SelectionSet)

	// Infer the variables type from the operation's variable definitions
	var variablesElems []ObjTypeElem
	for _, varDef := range op.VariableDefinitions {
		varType := in.inferType(varDef.Type)
		isNullable := !varDef.Type.NonNull

		propertyElem := &PropertyElem{
			Name:     varDef.Variable,
			Optional: isNullable,
			Readonly: false,
			Type:     varType,
		}
		variablesElems = append(variablesElems, propertyElem)
	}
	variablesType := in.record(&ObjectType{Elems: variablesElems}, op.Position)

	return &GraphQLInferenceResult{
		ResultType:    resultType,
		VariablesType: variablesType,
		Prov:          in.prov,
	}
}

// inferType converts a GraphQL type reference to an Escalier type
func (in *inferrer) inferType(gqlType *gqlast.Type) Type {
	// Handle named types
	if gqlType.NamedType != "" {
		var baseType Type
		switch gqlType.NamedType {
		case "String":
			baseType = in.record(&PrimType{Prim: StrPrim}, gqlType.Position)
		case "Int", "Float":
			baseType = in.record(&PrimType{Prim: NumPrim}, gqlType.Position)
		case "Boolean":
			baseType = in.record(&PrimType{Prim: BoolPrim}, gqlType.Position)
		case "ID":
			// Keep ID as a type reference to preserve the ID semantics
			baseType = in.record(&AliasType{Name: "ID"}, gqlType.Position)
		default:
			// Check if it's a defined type in the schema
			if typeDef := in.schema.Types[gqlType.NamedType]; typeDef != nil {
				switch typeDef.Kind {
				case gqlast.Enum:
					// Expand enums as a union of string literal types
					var enumTypes []Type
					for _, v := range typeDef.EnumValues {
						enumType := in.record(&LitType{Lit: &StrLit{Value: v.Name}}, v.Position)
						enumTypes = append(enumTypes, enumType)
					}
					baseType = in.record(&UnionType{Types: enumTypes}, typeDef.Position)
				case gqlast.Scalar:
					// For custom scalars, fall back to the base type or use a type reference
					baseType = in.record(&AliasType{Name: gqlType.NamedType}, typeDef.Position)
				case gqlast.InputObject:
					// For input objects, create an object type with all fields
					var elems []ObjTypeElem
					for _, field := range typeDef.Fields {
						fieldType := in.inferType(field.Type)
						isNullable := !field.Type.NonNull
						propertyElem := &PropertyElem{
							Name:     field.Name,
							Optional: isNullable,
							Readonly: false,
							Type:     fieldType,
						}
						elems = append(elems, propertyElem)
					}
					baseType = in.record(&ObjectType{Elems: elems}, typeDef.Position)
				case gqlast.Object, gqlast.Interface, gqlast.Union:
					// For other types (Object, Interface, Union), use type reference
					baseType = in.record(&AliasType{Name: gqlType.NamedType}, typeDef.Position)
				default:
					// Fallback for any other types
					baseType = in.record(&AliasType{Name: gqlType.NamedType}, typeDef.Position)
				}
			} else {
				// Fallback to type reference for unknown types
				baseType = in.record(&AliasType{Name: gqlType.NamedType}, gqlType.Position)
			}
		}

		// If the type is nullable (NonNull is false), create union with null
		if !gqlType.NonNull {
			nullType := &NullType{}
			return in.record(&UnionType{Types: []Type{baseType, nullType}}, gqlType.Position)
		}
		return baseType
	}

	// Fallback - should not reach here in normal cases
	panic("unexpected GraphQL type structure")
}

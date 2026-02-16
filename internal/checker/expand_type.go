package checker

import (
	"fmt"
	"slices"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) ExpandType(ctx Context, t type_system.Type, expandTypeRefsCount int) (type_system.Type, []Error) {
	t = type_system.Prune(t)
	visitor := NewTypeExpansionVisitor(c, ctx, expandTypeRefsCount)

	result := t.Accept(visitor)
	return result, visitor.errors
}

// TypeExpansionVisitor implements TypeVisitor for expanding type references
type TypeExpansionVisitor struct {
	checker             *Checker
	ctx                 Context
	errors              []Error
	skipTypeRefsCount   int // if > 0, skip expanding TypeRefTypes
	expandTypeRefsCount int // if > 0, number of TypeRefTypes expanded, if -1 then unlimited
}

// NewTypeExpansionVisitor creates a new visitor for expanding type references
func NewTypeExpansionVisitor(checker *Checker, ctx Context, expandTypeRefsCount int) *TypeExpansionVisitor {
	return &TypeExpansionVisitor{
		checker:             checker,
		ctx:                 ctx,
		errors:              []Error{},
		skipTypeRefsCount:   0,
		expandTypeRefsCount: expandTypeRefsCount,
	}
}

// resolveTypeOfQualIdent resolves a qualified identifier to its type
// Handles both simple identifiers (p1) and member access (p1.x)
func (v *TypeExpansionVisitor) resolveTypeOfQualIdent(ident type_system.QualIdent, prov provenance.Provenance) type_system.Type {
	// Extract span from provenance if available
	span := ast.Span{}
	if prov != nil {
		if nodeProv, ok := prov.(*ast.NodeProvenance); ok {
			span = nodeProv.Node.Span()
		}
	}

	switch id := ident.(type) {
	case *type_system.Ident:
		// Simple identifier - look it up as a value or namespace
		if binding := v.ctx.Scope.GetValue(id.Name); binding != nil {
			return binding.Type
		} else if namespace := v.ctx.Scope.getNamespace(id.Name); namespace != nil {
			return type_system.NewNamespaceType(prov, namespace)
		} else {
			v.errors = append(v.errors, &UnknownIdentifierError{
				Ident: ast.NewIdent(id.Name, span),
				span:  span,
			})
			return type_system.NewNeverType(prov)
		}
	case *type_system.Member:
		// Member access - recursively resolve the left side, then access the property
		leftType := v.resolveTypeOfQualIdent(id.Left, prov)

		// Get the property type from the left type
		propKey := PropertyKey{
			Name:     id.Right.Name,
			OptChain: false,
			span:     span,
		}
		memberType, memberErrors := v.checker.getMemberType(v.ctx, leftType, propKey)
		v.errors = slices.Concat(v.errors, memberErrors)
		return memberType
	default:
		panic(fmt.Sprintf("Unknown QualIdent type: %T", ident))
	}
}

func (v *TypeExpansionVisitor) EnterType(t type_system.Type) type_system.Type {
	switch t := t.(type) {
	case *type_system.FuncType:
		v.skipTypeRefsCount++ // don't expand type refs inside function types
	case *type_system.ObjectType:
		v.skipTypeRefsCount++ // don't expand type refs inside object types
	case *type_system.CondType:
		// We need to expand the CondType's extends type on entering so that
		// we can replace InferTypes in the extends type with fresh type variables
		// and then replace the corresponding TypeVarTypes in the alt and cons types
		// with those fresh type variables.  If we did this on exit, we wouldn't
		// be able to replace all the types in nested CondTypes.
		// TODO: Add a test case to ensure that infer type shadowing works and
		// fix the bug if it doesn't.

		inferSubs := v.checker.findInferTypes(t.Extends)
		groupSubs := v.checker.FindNamedGroups(t.Extends)
		extendsType := v.checker.replaceRegexGroupTypes(t.Extends, groupSubs)
		extendsType = v.checker.replaceInferTypes(extendsType, inferSubs)

		maps.Copy(inferSubs, groupSubs)

		return type_system.NewCondType(
			t.Provenance(),
			t.Check,
			extendsType,
			SubstituteTypeParams(t.Then, inferSubs),
			// TODO: don't use substitutions for the Then type because the Checks
			// type didn't have any InferTypes in it, so we don't need to
			// replace them with fresh type variables.
			SubstituteTypeParams(t.Else, inferSubs),
		)
	}

	return nil
}

func (v *TypeExpansionVisitor) ExitType(t type_system.Type) type_system.Type {
	switch t := t.(type) {
	case *type_system.FuncType:
		v.skipTypeRefsCount--
	case *type_system.ObjectType:
		v.skipTypeRefsCount--
	case *type_system.CondType:
		errors := v.checker.Unify(v.ctx, t.Check, t.Extends)
		if len(errors) == 0 {
			return t.Then
		} else {
			return t.Else
		}
	case *type_system.UnionType:
		// filter out `never` types from the union
		var filteredTypes []type_system.Type
		for _, typ := range t.Types {
			if _, ok := typ.(*type_system.NeverType); !ok {
				filteredTypes = append(filteredTypes, typ)
			}
		}
		if len(filteredTypes) == len(t.Types) {
			return nil // No filtering needed, return nil to let Accept handle it
		}
		return type_system.NewUnionType(nil, filteredTypes...)
	case *type_system.IntersectionType:
		// First distribute intersection over any unions it contains
		// e.g., A & (B | C) becomes (A & B) | (A & C)
		distributed, changed := distributeIntersectionOverUnion(t)

		if !changed {
			// If no distribution occurred, re-normalize intersection after type expansion
			// Type expansion may reveal equivalent types or simplifications
			return v.checker.NormalizeIntersectionType(v.ctx, t)
		}

		// TODO: Consider moving this code into distributeIntersectionOverUnion
		// If the result of distribution is a union, recursively expand the union members
		if union, isUnion := distributed.(*type_system.UnionType); isUnion {
			// Recursively expand each member of the union to handle nested intersections
			expandedMembers := make([]type_system.Type, len(union.Types))
			for i, member := range union.Types {
				expanded, _ := v.checker.ExpandType(v.ctx, member, v.expandTypeRefsCount)
				expandedMembers[i] = expanded
			}

			// Filter out never types from the expanded union
			var filteredMembers []type_system.Type
			for _, member := range expandedMembers {
				if _, ok := member.(*type_system.NeverType); !ok {
					filteredMembers = append(filteredMembers, member)
				}
			}

			// If all members were filtered out, return never
			if len(filteredMembers) == 0 {
				return type_system.NewNeverType(nil)
			}

			// If only one member remains, return it directly
			if len(filteredMembers) == 1 {
				return filteredMembers[0]
			}

			return type_system.NewUnionType(nil, filteredMembers...)
		}

		return distributed
	case *type_system.KeyOfType:
		// Expand keyof T by extracting the keys from the type T
		targetType := type_system.Prune(t.Type)

		// First, try to expand the target type
		expandedTarget, _ := v.checker.ExpandType(v.ctx, targetType, 1)
		expandedTarget = type_system.Prune(expandedTarget)

		switch target := expandedTarget.(type) {
		case *type_system.ObjectType:
			// Extract all keys from the object type (excluding methods)
			keys := []type_system.Type{}
			for _, elem := range target.Elems {
				switch e := elem.(type) {
				case *type_system.PropertyElem:
					// Add the property key as a literal type
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.GetterElem:
					// Add getter names as string literal types
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.SetterElem:
					// Add setter names as string literal types
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.MappedElem:
					// For mapped types, return the constraint type
					// e.g., keyof {[K]: T[K] for K in Keys} -> Keys
					return e.TypeParam.Constraint
				case *type_system.RestSpreadElem:
					// For rest spread, recursively get keyof the spread type
					spreadKeys, _ := v.checker.ExpandType(v.ctx, type_system.NewKeyOfType(nil, e.Value), -1)
					keys = append(keys, spreadKeys)
				}
			}

			if len(keys) == 0 {
				return type_system.NewNeverType(nil)
			}
			if len(keys) == 1 {
				return keys[0]
			}
			return type_system.NewUnionType(nil, keys...)
		case *type_system.UnionType:
			// Distribute keyof over union types: keyof (A | B) = keyof A | keyof B
			keyofTypes := make([]type_system.Type, len(target.Types))
			for i, typ := range target.Types {
				keyofType := type_system.NewKeyOfType(nil, typ)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				keyofTypes[i] = expandedKeyof
			}
			return type_system.NewUnionType(nil, keyofTypes...)
		case *type_system.IntersectionType:
			// For intersection types, get the union of all keys
			// keyof (A & B) = keyof A | keyof B
			keyofTypes := make([]type_system.Type, len(target.Types))
			for i, typ := range target.Types {
				keyofType := type_system.NewKeyOfType(nil, typ)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				keyofTypes[i] = expandedKeyof
			}
			return type_system.NewUnionType(nil, keyofTypes...)
		case *type_system.TypeRefType:
			// Try to expand the type reference and get keyof that
			expandedRef, _ := v.checker.expandTypeRef(v.ctx, target)
			if expandedRef != target {
				keyofType := type_system.NewKeyOfType(nil, expandedRef)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				return expandedKeyof
			}
			// If we can't expand further, return the KeyOfType as-is
			return nil
		case *type_system.PrimType:
			// For primitive types, keyof returns never (TypeScript behavior)
			// because primitives don't have enumerable keys
			return type_system.NewNeverType(nil)
		case *type_system.TupleType:
			// For tuples, return the numeric indices as literal types plus "length"
			keys := []type_system.Type{type_system.NewStrLitType(nil, "length")}
			for i := range target.Elems {
				keys = append(keys, type_system.NewNumLitType(nil, float64(i)))
			}
			return type_system.NewUnionType(nil, keys...)
		case *type_system.NeverType:
			// keyof never = never
			return type_system.NewNeverType(nil)
		case *type_system.AnyType:
			// keyof any = string | number | symbol
			return type_system.NewUnionType(nil,
				type_system.NewStrPrimType(nil),
				type_system.NewNumPrimType(nil),
				type_system.NewSymPrimType(nil),
			)
		case *type_system.UnknownType:
			// keyof unknown = never
			return type_system.NewNeverType(nil)
		default:
			// For other types, return the KeyOfType as-is
			return nil
		}
	case *type_system.TypeRefType:
		// TODO: implement once TypeAliases have been marked as recursive.
		// `expandType` is eager so we can't expand recursive type aliases as it
		// would lead to infinite recursion.

		// Check if we've reached the maximum expansion depth
		if v.skipTypeRefsCount > 0 {
			// Return the type reference without expanding
			return nil
		}

		if v.expandTypeRefsCount == 0 {
			// Return the type reference without expanding
			return nil
		}

		typeAlias := resolveQualifiedTypeAlias(v.ctx, t.Name)
		// TODO: Check if the qualifier is a type.  If it is, we can treat this
		// as a member access type.
		if typeAlias == nil {
			v.errors = append(v.errors, &UnknownTypeError{TypeName: type_system.QualIdentToString(t.Name), TypeRef: t})
			neverType := type_system.NewNeverType(nil)
			neverType.SetProvenance(&type_system.TypeProvenance{Type: t})
			return neverType
		} // Replace type params with type args if the type is generic
		expandedType := typeAlias.Type

		// Don't expand nominal object types
		if t, ok := expandedType.(*type_system.ObjectType); ok {
			if t.Nominal {
				return nil
			}
		}

		// TODO:
		// - ensure that the number of type args matches the number of type params
		// - handle type params with defaults
		if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {

			// TODO:
			// Handle case such as:
			// - type Foo<T> = boolean | T extends string ? T : number
			// - type Bar<T> = string & T extends string ? T : number
			// Do not perform distributions if the conditional type is the child
			// of any other type.
			switch prunedType := type_system.Prune(expandedType).(type) {
			case *type_system.CondType:
				substitutionSets, subSetErrors := v.checker.generateSubstitutionSets(v.ctx, typeAlias.TypeParams, t.TypeArgs)
				if len(subSetErrors) > 0 {
					v.errors = slices.Concat(v.errors, subSetErrors)
				}

				// If there are more than one substitution sets, distribute the
				// type arguments across the conditional type.
				if len(substitutionSets) > 1 {
					expandedTypes := make([]type_system.Type, len(substitutionSets))
					for i, substitutionSet := range substitutionSets {
						expandedTypes[i] = SubstituteTypeParams(prunedType, substitutionSet)
					}
					// Create a union type of all expanded types
					expandedType = type_system.NewUnionType(nil, expandedTypes...)
				} else {
					substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
					expandedType = SubstituteTypeParams(prunedType, substitutions)
				}
			case *type_system.ObjectType:
				// Expand any MappedElem elements in the object type
				substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
				objType := SubstituteTypeParams(prunedType, substitutions)
				expandedType = v.expandMappedElems(objType)
			default:
				substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
				expandedType = SubstituteTypeParams(prunedType, substitutions)
			}
		} else if len(typeAlias.TypeParams) == 0 && len(t.TypeArgs) == 0 {
			// Expand MappedElems in ObjectTypes even if there are no type params/args
			// `{[P]: string for P in "foo" | "bar"}` should be expanded to
			// `{foo: string, bar: string}`
			if objType, ok := type_system.Prune(expandedType).(*type_system.ObjectType); ok {
				expandedType = v.expandMappedElems(objType)
			}
		}

		// Recursively expand the resolved type using the same visitor to maintain state
		if v.expandTypeRefsCount == -1 {
			result, _ := v.checker.ExpandType(v.ctx, expandedType, -1)
			return result
		}

		result, _ := v.checker.ExpandType(v.ctx, expandedType, v.expandTypeRefsCount-1)
		return result
	case *type_system.TemplateLitType:
		// Expand template literal types by generating all possible string combinations
		// from the cartesian product of the union types in the template
		return v.expandTemplateLitType(t)
	case *type_system.IndexType:
		// Expand index types by resolving the indexed property
		// e.g., {x: number, y: string}["x"] => number
		expandedTarget, _ := v.checker.ExpandType(v.ctx, t.Target, 1)
		expandedIndex, _ := v.checker.ExpandType(v.ctx, t.Index, 1)

		// Extract span from provenance if available
		span := ast.Span{}
		if t.Provenance() != nil {
			if nodeProv, ok := t.Provenance().(*ast.NodeProvenance); ok {
				span = nodeProv.Node.Span()
			}
		}

		// Use getMemberType to resolve the property access
		key := IndexKey{Type: expandedIndex, span: span}
		memberType, memberErrors := v.checker.getMemberType(v.ctx, expandedTarget, key)
		v.errors = slices.Concat(v.errors, memberErrors)
		return memberType
	case *type_system.TypeOfType:
		// Expand typeof by looking up the value and returning its type
		// Handle both simple identifiers (p1) and member access (p1.x)
		resultType := v.resolveTypeOfQualIdent(t.Ident, t.Provenance())
		return resultType
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// getMemberType is a unified function for getting types from objects via property access or indexing
func (c *Checker) getMemberType(ctx Context, objType type_system.Type, key MemberAccessKey) (type_system.Type, []Error) {
	errors := []Error{}

	objType = type_system.Prune(objType)

	// Repeatedly expand objType until it's either an ObjectType, NamespaceType,
	// IntersectionType, or can't be expanded any further
	for {
		// Check if we've reached a terminal type that we can directly get properties from
		// before attempting expansion (this avoids infinite recursion on NamespaceType
		// when globalThis points back to the global namespace)
		if _, ok := objType.(*type_system.ObjectType); ok {
			break
		}
		if _, ok := objType.(*type_system.NamespaceType); ok {
			break
		}
		if _, ok := objType.(*type_system.IntersectionType); ok {
			break
		}

		expandedType, expandErrors := c.ExpandType(ctx, objType, 1)
		errors = slices.Concat(errors, expandErrors)

		// If expansion didn't change the type, we're done expanding
		if expandedType == objType {
			break
		}

		objType = expandedType
	}

	switch t := objType.(type) {
	case *type_system.MutabilityType:
		// For mutable types, get the access from the inner type
		return c.getMemberType(ctx, t.Type, key)
	case *type_system.TypeRefType:
		// Handle Array access
		if indexKey, ok := key.(IndexKey); ok && type_system.QualIdentToString(t.Name) == "Array" {
			unifyErrors := c.Unify(ctx, indexKey.Type, type_system.NewNumPrimType(nil))
			errors = slices.Concat(errors, unifyErrors)
			return t.TypeArgs[0], errors
		} else if _, ok := key.(IndexKey); ok && type_system.QualIdentToString(t.Name) == "Array" {
			errors = append(errors, &ExpectedArrayError{Type: t})
			return type_system.NewNeverType(nil), errors
		}

		// Try to expand the type alias and call getAccessType recursively
		expandType, expandErrors := c.expandTypeRef(ctx, t)
		accessType, accessErrors := c.getMemberType(ctx, expandType, key)

		errors = slices.Concat(errors, accessErrors, expandErrors)

		return accessType, errors
	case *type_system.TupleType:
		if indexKey, ok := key.(IndexKey); ok {
			var keyType type_system.Type = indexKey.Type
			if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
				keyType = mut.Type
			}
			if indexLit, ok := keyType.(*type_system.LitType); ok {
				if numLit, ok := indexLit.Lit.(*type_system.NumLit); ok {
					index := int(numLit.Value)
					if index < len(t.Elems) {
						return t.Elems[index], errors
					} else {
						errors = append(errors, &OutOfBoundsError{
							Index:  index,
							Length: len(t.Elems),
							span:   indexKey.Span(),
						})
						return type_system.NewNeverType(nil), errors
					}
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexKey.Type,
				span: indexKey.Span(),
			})
			return type_system.NewNeverType(nil), errors
		}
		// If a property (method) is accessed on a tuple, delegate to Array<T>
		if _, ok := key.(PropertyKey); ok {
			// Compute the union of the tuple element types
			var elemUnion type_system.Type
			switch len(t.Elems) {
			case 0:
				elemUnion = type_system.NewNeverType(nil)
			case 1:
				elemUnion = t.Elems[0]
			default:
				elemUnion = type_system.NewUnionType(nil, t.Elems...)
			}
			arrayRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent("Array"),
				TypeArgs: []type_system.Type{elemUnion},
			}
			return c.getMemberType(ctx, arrayRef, key)
		}
		// TupleType doesn't support other property access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	case *type_system.PrimType:
		// Delegate method calls on primitive types to their built‑in wrapper types
		var builtinName string
		switch t.Prim {
		case type_system.NumPrim:
			builtinName = "Number"
		case type_system.StrPrim:
			builtinName = "String"
		case type_system.BoolPrim:
			builtinName = "Boolean"
		default:
			builtinName = ""
		}
		if builtinName != "" {
			if _, ok := key.(PropertyKey); ok {
				builtinRef := &type_system.TypeRefType{
					Name:     type_system.NewIdent(builtinName),
					TypeArgs: []type_system.Type{},
				}
				return c.getMemberType(ctx, builtinRef, key)
			}
		}
		// Not a supported primitive method call
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.LitType:
		// Delegate literal method calls to the corresponding built‑in wrapper type
		var builtinName string
		switch t.Lit.(type) {
		case *type_system.NumLit:
			builtinName = "Number"
		case *type_system.StrLit:
			builtinName = "String"
		case *type_system.BoolLit:
			builtinName = "Boolean"
		default:
			builtinName = ""
		}
		if builtinName != "" {
			if _, ok := key.(PropertyKey); ok {
				builtinRef := &type_system.TypeRefType{
					Name:     type_system.NewIdent(builtinName),
					TypeArgs: []type_system.Type{},
				}
				return c.getMemberType(ctx, builtinRef, key)
			}
		}
		// Fallback handling for other literals
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.FuncType:
		// Delegate function method calls to the Function interface
		if _, ok := key.(PropertyKey); ok {
			functionRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent("Function"),
				TypeArgs: []type_system.Type{},
			}
			return c.getMemberType(ctx, functionRef, key)
		}
		// FuncType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.ObjectType:
		return c.getObjectAccess(t, key, errors)
	case *type_system.UnionType:
		return c.getUnionAccess(ctx, t, key, errors)
	case *type_system.NamespaceType:
		if propKey, ok := key.(PropertyKey); ok {
			if value := t.Namespace.Values[propKey.Name]; value != nil {
				return value.Type, errors
			} else if namespace, ok := t.Namespace.GetNamespace(propKey.Name); ok {
				return type_system.NewNamespaceType(nil, namespace), errors
			} else {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: objType,
					Property:   propKey.Name,
					span:       propKey.Span(),
				})
				return type_system.NewNeverType(nil), errors
			}
		}
		// NamespaceType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	case *type_system.IntersectionType:
		return c.getIntersectionAccess(ctx, t, key, errors)
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	}
}

// getObjectAccess handles property and index access on ObjectType
func (c *Checker) getObjectAccess(objType *type_system.ObjectType, key MemberAccessKey, errors []Error) (type_system.Type, []Error) {
	switch k := key.(type) {
	case PropertyKey:
		for _, elem := range objType.Elems {
			switch elem := elem.(type) {
			case *type_system.PropertyElem:
				if elem.Name == type_system.NewStrKey(k.Name) {
					propType := elem.Value
					if elem.Optional {
						propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
					}
					return propType, errors
				}
			case *type_system.MethodElem:
				if elem.Name == type_system.NewStrKey(k.Name) {
					return elem.Fn, errors
				}
			case *type_system.GetterElem:
				if elem.Name == type_system.NewStrKey(k.Name) {
					return elem.Fn.Return, errors
				}
			case *type_system.SetterElem:
				if elem.Name == type_system.NewStrKey(k.Name) {
					return elem.Fn.Params[0].Type, errors
				}
			case *type_system.MappedElem:
				panic("MappedElems should have been expanded before property access")
			case *type_system.ConstructorElem:
			case *type_system.CallableElem:
				continue
			default:
				panic(fmt.Sprintf("Unknown object type element: %#v", elem))
			}
		}

		// Check the Extends field if property not found
		for _, extendsTypeRef := range objType.Extends {
			// Resolve TypeRefType through TypeAlias
			extendsType := type_system.Prune(extendsTypeRef)

			if typeRef, ok := extendsType.(*type_system.TypeRefType); ok {
				if typeRef.TypeAlias != nil {
					extendsType = type_system.Prune(typeRef.TypeAlias.Type)
				}
			}

			if extendsObjType, ok := extendsType.(*type_system.ObjectType); ok {
				// Recursively check the extended type
				return c.getObjectAccess(extendsObjType, key, errors)
			}

			// If the extended type cannot be resolved to an ObjectType,
			// report this instead of silently skipping it.
			errors = append(errors, &ExpectedObjectError{Type: extendsType})
		}

		errors = append(errors, &UnknownPropertyError{
			ObjectType: objType,
			Property:   k.Name,
			span:       k.Span(),
		})
		return type_system.NewUndefinedType(nil), errors
	case IndexKey:
		var keyType type_system.Type = k.Type
		if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
			keyType = mut.Type
		}
		if indexLit, ok := keyType.(*type_system.LitType); ok {
			if strLit, ok := indexLit.Lit.(*type_system.StrLit); ok {
				for _, elem := range objType.Elems {
					switch elem := elem.(type) {
					case *type_system.PropertyElem:
						if elem.Name == type_system.NewStrKey(strLit.Value) {
							propType := elem.Value
							if elem.Optional {
								propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
							}
							return propType, errors
						}
					case *type_system.MethodElem:
						if elem.Name == type_system.NewStrKey(strLit.Value) {
							return elem.Fn, errors
						}
					case *type_system.MappedElem:
						panic("MappedElems should have been expanded before property access")
					default:
						panic(fmt.Sprintf("Unknown object type element: %#v", elem))
					}
				}
			}
		}

		// Check the Extends field if property not found
		for _, extendsTypeRef := range objType.Extends {
			// Resolve TypeRefType through TypeAlias
			extendsType := type_system.Prune(extendsTypeRef)
			if typeRef, ok := extendsType.(*type_system.TypeRefType); ok {
				if typeRef.TypeAlias != nil {
					extendsType = type_system.Prune(typeRef.TypeAlias.Type)
				}
			}
			if extendsObjType, ok := extendsType.(*type_system.ObjectType); ok {
				// Recursively check the extended type
				return c.getObjectAccess(extendsObjType, key, errors)
			}
		}

		errors = append(errors, &InvalidObjectKeyError{
			Key:  keyType,
			span: k.Span(),
		})
		return type_system.NewUndefinedType(nil), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return type_system.NewUndefinedType(nil), errors
	}
}

// getUnionAccess handles property and index access on UnionType
func (c *Checker) getUnionAccess(ctx Context, unionType *type_system.UnionType, key MemberAccessKey, errors []Error) (type_system.Type, []Error) {
	propKey, isPropertyKey := key.(PropertyKey)

	definedElems := c.getDefinedElems(unionType)

	undefinedCount := len(unionType.Types) - len(definedElems)

	// If there are no defined elements (only null/undefined), we can't access properties
	if len(definedElems) == 0 {
		errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
		return type_system.NewUndefinedType(nil), errors
	}

	if len(definedElems) == 1 {
		if undefinedCount == 0 {
			return c.getMemberType(ctx, definedElems[0], key)
		}

		if undefinedCount > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
			return type_system.NewUndefinedType(nil), errors
		}

		pType, pErrors := c.getMemberType(ctx, definedElems[0], key)
		errors = slices.Concat(errors, pErrors)
		propType := type_system.NewUnionType(nil, pType, type_system.NewUndefinedType(nil))
		return propType, errors
	}

	if len(definedElems) > 1 {
		// Get the member type from each element in the union and combine them
		memberTypes := []type_system.Type{}
		for _, elem := range definedElems {
			memberType, memberErrors := c.getMemberType(ctx, elem, key)
			errors = slices.Concat(errors, memberErrors)
			memberTypes = append(memberTypes, memberType)
		}

		// If there are undefined elements and we're accessing without optional chaining,
		// we need to report an error
		if undefinedCount > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
		}

		// Create a union of all member types
		resultType := type_system.NewUnionType(nil, memberTypes...)

		// If there are undefined elements, add undefined to the union
		if undefinedCount > 0 {
			resultType = type_system.NewUnionType(nil, resultType, type_system.NewUndefinedType(nil))
		}

		return resultType, errors
	}

	return type_system.NewNeverType(nil), errors
}

// getIntersectionAccess handles property and index access on IntersectionType
func (c *Checker) getIntersectionAccess(ctx Context, intersectionType *type_system.IntersectionType, key MemberAccessKey, errors []Error) (type_system.Type, []Error) {
	// For an intersection A & B, member access should:
	// 1. If all parts are object types, merge their properties (the result is the intersection of matching property types)
	// 2. Otherwise, try to access from each part and use the first successful one

	// Separate object types from non-object types
	objectTypes := []*type_system.ObjectType{}
	funcTypes := []*type_system.FuncType{}

	for _, part := range intersectionType.Types {
		part = type_system.Prune(part)
		if objType, ok := part.(*type_system.ObjectType); ok {
			objectTypes = append(objectTypes, objType)
		} else if funcType, ok := part.(*type_system.FuncType); ok {
			funcTypes = append(funcTypes, funcType)
		}
	}

	// If all parts are object types, merge their properties
	if len(objectTypes) == len(intersectionType.Types) {
		memberTypes := []type_system.Type{}
		foundAny := false

		for _, objType := range objectTypes {
			memberType, memberErrors := c.getObjectAccess(objType, key, nil)
			// Only include results from object types that have this property
			if len(memberErrors) == 0 {
				memberTypes = append(memberTypes, memberType)
				foundAny = true
			}
		}

		if !foundAny {
			// Property doesn't exist in any part of the intersection
			if propKey, ok := key.(PropertyKey); ok {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: intersectionType,
					Property:   propKey.Name,
					span:       propKey.Span(),
				})
			} else {
				indexKey := key.(IndexKey)
				errors = append(errors, &InvalidObjectKeyError{
					Key:  indexKey.Type,
					span: indexKey.Span(),
				})
			}
			return type_system.NewNeverType(nil), errors
		}

		// The result type is the intersection of all matching property types
		// This ensures that the property type satisfies all parts of the intersection
		if len(memberTypes) == 1 {
			return memberTypes[0], errors
		}
		return type_system.NewIntersectionType(nil, memberTypes...), errors
	}

	// For mixed cases (e.g., branded primitives: string & {__brand: "email"}, or function & {metadata: string})
	// First, collect all properties from object type parts
	memberTypesFromObjects := []type_system.Type{}
	for _, objType := range objectTypes {
		memberType, memberErrors := c.getObjectAccess(objType, key, nil)
		if len(memberErrors) == 0 {
			memberTypesFromObjects = append(memberTypesFromObjects, memberType)
		}
	}

	// If we found the property in multiple object types, intersect them
	if len(memberTypesFromObjects) > 1 {
		return type_system.NewIntersectionType(nil, memberTypesFromObjects...), errors
	}

	// If we found the property in exactly one object type, return it
	if len(memberTypesFromObjects) == 1 {
		return memberTypesFromObjects[0], errors
	}

	// If not found in object types, try other parts (primitives, functions, etc.)
	for _, part := range intersectionType.Types {
		part = type_system.Prune(part)
		// Skip object types since we already checked them
		if _, ok := part.(*type_system.ObjectType); ok {
			continue
		}
		memberType, memberErrors := c.getMemberType(ctx, part, key)
		if len(memberErrors) == 0 {
			return memberType, errors
		}
	}

	// If no part has this property, report error
	if propKey, ok := key.(PropertyKey); ok {
		errors = append(errors, &UnknownPropertyError{
			ObjectType: intersectionType,
			Property:   propKey.Name,
			span:       propKey.Span(),
		})
	} else {
		indexKey := key.(IndexKey)
		errors = append(errors, &InvalidObjectKeyError{
			Key:  indexKey.Type,
			span: indexKey.Span(),
		})
	}
	return type_system.NewNeverType(nil), errors
}

// expandMappedElems expands MappedElem elements in an ObjectType into concrete properties
// For example, {[P in "foo" | "bar"]: string} becomes {foo: string, bar: string}
func (v *TypeExpansionVisitor) expandMappedElems(objType *type_system.ObjectType) *type_system.ObjectType {
	// Check if there are any MappedElem elements to expand
	hasMappedElems := false
	for _, elem := range objType.Elems {
		if _, ok := elem.(*type_system.MappedElem); ok {
			hasMappedElems = true
			break
		}
	}

	if !hasMappedElems {
		return objType
	}

	// Expand mapped elements into concrete properties
	expandedElems := []type_system.ObjTypeElem{}
	for _, elem := range objType.Elems {
		if mappedElem, ok := elem.(*type_system.MappedElem); ok {
			// Expand the constraint to get all possible keys
			expandedConstraint, _ := v.checker.ExpandType(v.ctx, mappedElem.TypeParam.Constraint, -1)

			// Extract keys from the constraint
			var keys []type_system.Type
			switch constraint := type_system.Prune(expandedConstraint).(type) {
			case *type_system.UnionType:
				keys = constraint.Types
			default:
				keys = []type_system.Type{constraint}
			}

			// For each key in the constraint, create a property
			for _, keyType := range keys {
				keyType = type_system.Prune(keyType)

				// Substitute the type parameter with the key type
				keySubs := map[string]type_system.Type{
					mappedElem.TypeParam.Name: keyType,
				}

				// Apply filter if Check and Extends are present
				if mappedElem.Check != nil && mappedElem.Extends != nil {
					// Substitute the type parameter in both Check and Extends
					checkType := SubstituteTypeParams(mappedElem.Check, keySubs)
					extendsType := SubstituteTypeParams(mappedElem.Extends, keySubs)

					// Expand both types
					checkType, _ = v.checker.ExpandType(v.ctx, checkType, -1)
					extendsType, _ = v.checker.ExpandType(v.ctx, extendsType, -1)

					// Check if checkType extends extendsType
					errors := v.checker.Unify(v.ctx, checkType, extendsType)
					if len(errors) > 0 {
						// Skip this property if it doesn't satisfy the filter
						continue
					}
				}

				// Determine the property key
				var propKey type_system.ObjTypeKey
				if mappedElem.Name != nil {
					// If a Name is provided, use it to compute the property key
					// Example: {[`prefix_${K}`]: T[K] for K in keyof T}
					propKeyType := SubstituteTypeParams(mappedElem.Name, keySubs)

					// Expand the property key to resolve template literals
					propKeyType, _ = v.checker.ExpandType(v.ctx, propKeyType, -1)
					propKeyType = type_system.Prune(propKeyType)

					// Convert the expanded key type to an ObjTypeKey
					switch kt := propKeyType.(type) {
					case *type_system.LitType:
						switch lit := kt.Lit.(type) {
						case *type_system.StrLit:
							propKey = type_system.NewStrKey(lit.Value)
						case *type_system.NumLit:
							propKey = type_system.NewNumKey(lit.Value)
						default:
							// Skip non-string/number keys
							continue
						}
					case *type_system.UniqueSymbolType:
						propKey = type_system.NewSymKey(kt.Value)
					default:
						// Skip non-literal keys
						continue
					}
				} else {
					// Otherwise, use the constraint key directly
					switch kt := keyType.(type) {
					case *type_system.LitType:
						switch lit := kt.Lit.(type) {
						case *type_system.StrLit:
							propKey = type_system.NewStrKey(lit.Value)
						case *type_system.NumLit:
							propKey = type_system.NewNumKey(lit.Value)
						default:
							panic("Invalid property key type in mapped element")
						}
					case *type_system.UniqueSymbolType:
						propKey = type_system.NewSymKey(kt.Value)
					default:
						panic("Invalid property key type in mapped element")
					}
				}

				propValue := SubstituteTypeParams(mappedElem.Value, keySubs)

				// Expand the property value to resolve any index types such as `T[K]`
				// This is necessary to get the actual type including | undefined for optional properties
				propValue, _ = v.checker.ExpandType(v.ctx, propValue, 1)

				// Create property element
				propElem := &type_system.PropertyElem{
					Name:     propKey,
					Value:    propValue,
					Optional: false,
					Readonly: false,
				}

				// Handle optional modifier
				if mappedElem.Optional != nil {
					switch *mappedElem.Optional {
					case type_system.MMAdd:
						propElem.Optional = true
					case type_system.MMRemove:
						propElem.Optional = false
						// When removing the optional modifier, also remove undefined from the value type
						propElem.Value = removeUndefinedFromType(propElem.Value)
					}
				}

				// Handle readonly modifier
				if mappedElem.Readonly != nil {
					switch *mappedElem.Readonly {
					case type_system.MMAdd:
						propElem.Readonly = true
					case type_system.MMRemove:
						propElem.Readonly = false
					}
				}

				expandedElems = append(expandedElems, propElem)
			}
		} else {
			// Keep non-mapped elements as-is
			expandedElems = append(expandedElems, elem)
		}
	}

	return type_system.NewObjectType(objType.Provenance(), expandedElems)
}

func (c *Checker) expandTypeRef(ctx Context, t *type_system.TypeRefType) (type_system.Type, []Error) {
	// Resolve the type alias
	typeAlias := resolveQualifiedTypeAlias(ctx, t.Name)
	if typeAlias == nil {
		return type_system.NewNeverType(nil), []Error{&UnknownTypeError{TypeName: type_system.QualIdentToString(t.Name), TypeRef: t}}
	}

	// Expand the type alias
	expandedType := typeAlias.Type

	// Handle type parameter substitution if the type is generic
	if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {
		substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
		expandedType = SubstituteTypeParams(typeAlias.Type, substitutions)
	}

	return expandedType, []Error{}
}

// expandTemplateLitType expands a template literal type by generating all possible
// string combinations from the cartesian product of union types in the template.
// Example: `${0 | 1},${0 | 1}` => "0,0" | "0,1" | "1,0" | "1,1"
func (v *TypeExpansionVisitor) expandTemplateLitType(t *type_system.TemplateLitType) type_system.Type {
	// Extract the members of each type in the template
	// If a type is a union, we get all its members
	// If it's a literal, we get just that literal
	typeOptions := make([][]type_system.Type, len(t.Types))

	for i, t := range t.Types {
		t = type_system.Prune(t)
		t, _ = v.checker.ExpandType(v.ctx, t, -1) // fully expand nested type refs

		if unionType, ok := t.(*type_system.UnionType); ok {
			typeOptions[i] = unionType.Types
		} else {
			typeOptions[i] = []type_system.Type{t}
		}
	}

	// Generate cartesian product of all type options
	combinations := v.cartesianProduct(typeOptions)

	// Convert each combination into a string literal type
	resultTypes := make([]type_system.Type, 0, len(combinations))

	for _, combo := range combinations {
		newQuasis := []*type_system.Quasi{}
		newTypes := []type_system.Type{}
		currentQuasi := ""

		for i, quasi := range t.Quasis {
			currentQuasi += quasi.Value

			if i < len(combo) {
				// Check if this is a literal type that should be concatenated to currentQuasi
				if litType, ok := combo[i].(*type_system.LitType); ok {
					switch lit := litType.Lit.(type) {
					case *type_system.StrLit:
						currentQuasi += lit.Value
					case *type_system.NumLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *type_system.BoolLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *type_system.BigIntLit:
						currentQuasi += lit.Value.String()
					default:
						// Other literal types: append currentQuasi and add the type
						newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})
						currentQuasi = ""
						newTypes = append(newTypes, combo[i])
					}
				} else {
					// Non-literal types: append currentQuasi and add the type
					newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})
					currentQuasi = ""
					newTypes = append(newTypes, combo[i])
				}
			}
		}

		// Append the final currentQuasi (this is the tail)
		newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})

		// If we have no types (all were literals), convert to a string literal
		if len(newTypes) == 0 {
			resultTypes = append(resultTypes, type_system.NewStrLitType(t.Provenance(), newQuasis[0].Value))
		} else {
			// Otherwise, create a new template literal type
			newTemplateLitType := &type_system.TemplateLitType{
				Quasis: newQuasis,
				Types:  newTypes,
			}
			newTemplateLitType.SetProvenance(t.Provenance())
			resultTypes = append(resultTypes, newTemplateLitType)
		}
	}

	// Return a union of all possible string literals
	return type_system.NewUnionType(t.Provenance(), resultTypes...)
}

// NormalizeIntersectionType performs deep normalization of an intersection type
// after type inference and expansion. This handles cases that NewIntersectionType
// cannot handle because types haven't been fully resolved yet, such as:
// - Type aliases that resolve to the same underlying type
// - Type variables after substitution
// - Type references that point to the same concrete type
func (c *Checker) NormalizeIntersectionType(ctx Context, t *type_system.IntersectionType) type_system.Type {
	// Step 1: Prune and expand all types to resolve type variables and type aliases
	expanded := make([]type_system.Type, len(t.Types))
	for i, typ := range t.Types {
		// Prune to resolve type variables
		typ = type_system.Prune(typ)

		// Expand type aliases to their underlying types
		// Use depth 1 to expand one level of type aliases
		if _, ok := typ.(*type_system.TypeRefType); ok {
			expandedType, _ := c.ExpandType(ctx, typ, 1)
			expanded[i] = expandedType
		} else {
			expanded[i] = typ
		}
	}

	// Step 2: Use NewIntersectionType to apply basic normalization
	// This handles flattening, duplicates, never/any/unknown, primitives, mutability
	result := type_system.NewIntersectionType(t.Provenance(), expanded...)

	// Step 3: If still an intersection after normalization, check for further simplifications
	if inter, ok := result.(*type_system.IntersectionType); ok {
		// Check if all types in the intersection are ObjectTypes - if so, merge them
		allObjects := true
		for _, typ := range inter.Types {
			if _, ok := typ.(*type_system.ObjectType); !ok {
				allObjects = false
				break
			}
		}

		if allObjects && len(inter.Types) > 1 {
			// Merge all object types into a single object type
			return c.mergeObjectTypes(inter.Provenance(), inter.Types)
		}
	}

	return result
}

// mergeObjectTypes merges multiple object types into a single object type
// by combining their elements. When properties have the same name, their types
// are intersected (e.g., {x: string} & {x: number} becomes {x: string & number}).
func (c *Checker) mergeObjectTypes(prov provenance.Provenance, types []type_system.Type) type_system.Type {
	mergedElems := []type_system.ObjTypeElem{}
	propMap := make(map[string]int) // Maps property key to index in mergedElems

	// Iterate through object types and collect their elements
	for _, typ := range types {
		if obj, ok := typ.(*type_system.ObjectType); ok {
			for _, elem := range obj.Elems {
				if propElem, ok := elem.(*type_system.PropertyElem); ok {
					propKey := propElem.Name.String()

					if idx, exists := propMap[propKey]; exists {
						// Property already exists - create intersection of the types
						existingProp := mergedElems[idx].(*type_system.PropertyElem)
						intersectedType := type_system.NewIntersectionType(
							nil,
							existingProp.Value,
							propElem.Value,
						)

						// Update the property with the intersected type
						// Preserve other attributes from the first occurrence
						updatedProp := &type_system.PropertyElem{
							Name:     existingProp.Name,
							Value:    intersectedType,
							Optional: existingProp.Optional && propElem.Optional, // Both must be optional
							Readonly: existingProp.Readonly || propElem.Readonly, // Either readonly makes it readonly
						}
						mergedElems[idx] = updatedProp
					} else {
						// First occurrence of this property - add it
						propMap[propKey] = len(mergedElems)
						mergedElems = append(mergedElems, propElem)
					}
				} else {
					// For non-property elements (methods, getters, setters), just append
					// TODO: Handle method/getter/setter conflicts similarly
					mergedElems = append(mergedElems, elem)
				}
			}
		}
	}

	return type_system.NewObjectType(prov, mergedElems)
}

// cartesianProduct generates the cartesian product of multiple slices of types
func (v *TypeExpansionVisitor) cartesianProduct(sets [][]type_system.Type) [][]type_system.Type {
	if len(sets) == 0 {
		return [][]type_system.Type{{}}
	}

	// Start with combinations from the first set
	result := make([][]type_system.Type, 0)
	for _, item := range sets[0] {
		result = append(result, []type_system.Type{item})
	}

	// For each remaining set, combine with existing results
	for i := 1; i < len(sets); i++ {
		var newResult [][]type_system.Type
		for _, existing := range result {
			for _, item := range sets[i] {
				combination := make([]type_system.Type, len(existing)+1)
				copy(combination, existing)
				combination[len(existing)] = item
				newResult = append(newResult, combination)
			}
		}
		result = newResult
	}

	return result
}

// findInferTypes finds all InferType nodes in a type and replaces them with fresh type variables.
// Returns a mapping from infer names to the type variables that replaced them.
func (c *Checker) findInferTypes(t type_system.Type) map[string]type_system.Type {
	visitor := &InferTypeFinder{
		checker:   c,
		inferVars: make(map[string]type_system.Type),
	}
	t.Accept(visitor)
	return visitor.inferVars
}

// InferTypeFinder collects all InferType nodes and replaces them with fresh type variables
type InferTypeFinder struct {
	checker   *Checker
	inferVars map[string]type_system.Type
}

func (v *InferTypeFinder) EnterType(t type_system.Type) type_system.Type {
	// No-op - just for traversal
	return nil
}

func (v *InferTypeFinder) ExitType(t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	if inferType, ok := t.(*type_system.InferType); ok {
		if existingVar, exists := v.inferVars[inferType.Name]; exists {
			// Reuse existing type variable for same infer name
			return existingVar
		}
		// Create fresh type variable
		freshVar := v.checker.FreshVar(&type_system.TypeProvenance{Type: inferType})
		v.inferVars[inferType.Name] = freshVar
		return freshVar
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// replaceInferTypes substitutes infer variables in a type with their inferred values from the mapping.
func (c *Checker) replaceInferTypes(t type_system.Type, inferMapping map[string]type_system.Type) type_system.Type {
	visitor := &InferTypeReplacer{
		inferMapping: inferMapping,
	}
	return t.Accept(visitor)
}

// InferTypeReplacer substitutes type variables that correspond to infer types
// with their actual inferred values
type InferTypeReplacer struct {
	inferMapping map[string]type_system.Type
}

func (v *InferTypeReplacer) EnterType(t type_system.Type) type_system.Type {
	// No-op - just for traversal
	return nil
}

func (v *InferTypeReplacer) ExitType(t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	// Check if this is an InferType that should be replaced
	if inferType, ok := t.(*type_system.InferType); ok {
		if typeVar, exists := v.inferMapping[inferType.Name]; exists {
			// Return the inferred type (what the type variable was unified with)
			return typeVar
		}
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

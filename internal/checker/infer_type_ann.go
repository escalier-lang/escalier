package checker

import (
	"fmt"
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferTypeAnn(
	ctx Context,
	typeAnn ast.TypeAnn,
) (type_system.Type, []Error) {
	errors := []Error{}
	var t type_system.Type = type_system.NewNeverType(nil)
	provenance := &ast.NodeProvenance{Node: typeAnn}

	switch typeAnn := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		typeName := ast.QualIdentToString(typeAnn.Name)
		typeAlias := c.resolveQualifiedTypeAlias(ctx, typeAnn.Name)
		if typeAlias != nil {
			typeArgs := make([]type_system.Type, len(typeAnn.TypeArgs))
			for i, typeArg := range typeAnn.TypeArgs {
				typeArgType, typeArgErrors := c.inferTypeAnn(ctx, typeArg)
				typeArgs[i] = typeArgType
				errors = slices.Concat(errors, typeArgErrors)
			}

			t = type_system.NewTypeRefType(provenance, typeName, typeAlias, typeArgs...)
		} else {
			// TODO: include type args
			typeRef := type_system.NewTypeRefType(provenance, typeName, nil, nil)
			errors = append(errors, &UnknownTypeError{TypeName: typeName, typeRef: typeRef})
		}
	case *ast.NumberTypeAnn:
		t = type_system.NewNumPrimType(provenance)
	case *ast.StringTypeAnn:
		t = type_system.NewStrPrimType(provenance)
	case *ast.BooleanTypeAnn:
		t = type_system.NewBoolPrimType(provenance)
	case *ast.SymbolTypeAnn:
		t = type_system.NewSymPrimType(provenance)
	case *ast.UniqueSymbolTypeAnn:
		c.SymbolID++
		t = type_system.NewUniqueSymbolType(provenance, c.SymbolID)
	case *ast.AnyTypeAnn:
		t = type_system.NewAnyType(provenance)
	case *ast.UnknownTypeAnn:
		t = type_system.NewUnknownType(provenance)
	case *ast.NeverTypeAnn:
		t = type_system.NewNeverType(provenance)
	case *ast.LitTypeAnn:
		switch lit := typeAnn.Lit.(type) {
		case *ast.StrLit:
			t = type_system.NewStrLitType(provenance, lit.Value)
		case *ast.NumLit:
			t = type_system.NewNumLitType(provenance, lit.Value)
		case *ast.BoolLit:
			t = type_system.NewBoolLitType(provenance, lit.Value)
		case *ast.RegexLit:
			t, _ = type_system.NewRegexTypeWithPatternString(provenance, lit.Value)
		case *ast.BigIntLit:
			t = type_system.NewBigIntLitType(provenance, lit.Value)
		case *ast.NullLit:
			t = type_system.NewNullType(provenance)
		case *ast.UndefinedLit:
			t = type_system.NewUndefinedType(provenance)
		default:
			panic(fmt.Sprintf("Unknown literal type: %T", lit))
		}
	case *ast.TupleTypeAnn:
		elems := make([]type_system.Type, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			elemType, elemErrors := c.inferTypeAnn(ctx, elem)
			elems[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		t = type_system.NewTupleType(provenance, elems...)
	case *ast.ObjectTypeAnn:
		elems := make([]type_system.ObjTypeElem, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			switch elem := elem.(type) {
			case *ast.CallableTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &type_system.CallableElem{Fn: fn}
			case *ast.ConstructorTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &type_system.ConstructorElem{Fn: fn}
			case *ast.MethodTypeAnn:
				// TODO: handle `self` and `mut self` parameters
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = type_system.NewMethodElem(*key, fn, nil)
			case *ast.GetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &type_system.GetterElem{Name: *key, Fn: fn}
			case *ast.SetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &type_system.SetterElem{Name: *key, Fn: fn}
			case *ast.PropertyTypeAnn:
				var t type_system.Type
				if elem.Value != nil {
					typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, elem.Value)
					errors = slices.Concat(errors, typeAnnErrors)
					t = typeAnnType
				} else {
					t = type_system.NewUndefinedType(nil)
				}
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				elems[i] = &type_system.PropertyElem{
					Name:     *key,
					Optional: elem.Optional,
					Readonly: elem.Readonly,
					Value:    t,
				}
			case *ast.MappedTypeAnn:
				// Infer the constraint type for the type parameter
				var constraintType type_system.Type
				if elem.TypeParam.Constraint != nil {
					var constraintErrors []Error
					constraintType, constraintErrors = c.inferTypeAnn(ctx, elem.TypeParam.Constraint)
					errors = slices.Concat(errors, constraintErrors)
				} else {
					constraintType = type_system.NewAnyType(nil)
				}

				typeParam := &type_system.IndexParam{
					Name:       elem.TypeParam.Name,
					Constraint: constraintType,
				}

				// Create a new context with the type parameter in scope
				mappedCtx := ctx
				if elem.TypeParam.Name != "" {
					mappedScope := ctx.Scope.WithNewScope()

					// Add the type parameter as a type alias to the scope
					typeParamTypeRef := type_system.NewTypeRefType(nil, elem.TypeParam.Name, nil)
					typeParamAlias := &type_system.TypeAlias{
						Type:       typeParamTypeRef,
						TypeParams: []*type_system.TypeParam{},
					}
					mappedScope.SetTypeAlias(elem.TypeParam.Name, typeParamAlias)

					mappedCtx = Context{
						Scope:      mappedScope,
						IsAsync:    ctx.IsAsync,
						IsPatMatch: ctx.IsPatMatch,
					}
				}

				// Infer the value type with the type parameter in scope
				valueType, valueErrors := c.inferTypeAnn(mappedCtx, elem.Value)
				errors = slices.Concat(errors, valueErrors)

				// Infer the optional name type if present
				var nameType type_system.Type
				if elem.Name != nil {
					var nameErrors []Error
					nameType, nameErrors = c.inferTypeAnn(mappedCtx, elem.Name)
					errors = slices.Concat(errors, nameErrors)
				}

				// Convert mapped modifiers
				var optional *type_system.MappedModifier
				if elem.Optional != nil {
					switch *elem.Optional {
					case ast.MMAdd:
						opt := type_system.MMAdd
						optional = &opt
					case ast.MMRemove:
						opt := type_system.MMRemove
						optional = &opt
					}
				}

				var readOnly *type_system.MappedModifier
				if elem.ReadOnly != nil {
					switch *elem.ReadOnly {
					case ast.MMAdd:
						ro := type_system.MMAdd
						readOnly = &ro
					case ast.MMRemove:
						ro := type_system.MMRemove
						readOnly = &ro
					}
				}

				// Infer the check and extends types if present (for filtering)
				var checkType type_system.Type
				if elem.Check != nil {
					var checkErrors []Error
					checkType, checkErrors = c.inferTypeAnn(mappedCtx, elem.Check)
					errors = slices.Concat(errors, checkErrors)
				}

				var extendsType type_system.Type
				if elem.Extends != nil {
					var extendsErrors []Error
					extendsType, extendsErrors = c.inferTypeAnn(mappedCtx, elem.Extends)
					errors = slices.Concat(errors, extendsErrors)
				}

				elems[i] = &type_system.MappedElem{
					TypeParam: typeParam,
					Name:      nameType,
					Value:     valueType,
					Optional:  optional,
					Readonly:  readOnly,
					Check:     checkType,
					Extends:   extendsType,
				}
			case *ast.RestSpreadTypeAnn:
				panic("TODO: handle RestSpreadTypeAnn")
			}
		}

		t = type_system.NewObjectType(provenance, elems)
	case *ast.UnionTypeAnn:
		types := make([]type_system.Type, len(typeAnn.Types))
		for i, unionType := range typeAnn.Types {
			unionElemType, unionElemErrors := c.inferTypeAnn(ctx, unionType)
			types[i] = unionElemType
			errors = slices.Concat(errors, unionElemErrors)
		}
		t = type_system.NewUnionType(nil, types...)
	case *ast.FuncTypeAnn:
		funcType, funcErrors := c.inferFuncTypeAnn(ctx, typeAnn)
		t = funcType
		errors = slices.Concat(errors, funcErrors)
	case *ast.CondTypeAnn:
		// TODO: this needs to be done in the Enter method of the visitor
		// so that we can we can replace InferType nodes with fresh type variables
		// and computing a new context with those infer types in scope before
		// inferring the Cons and Alt branches.
		// This only affects nested conditional types.
		checkType, checkErrors := c.inferTypeAnn(ctx, typeAnn.Check)
		errors = slices.Concat(errors, checkErrors)

		extendsType, extendsErrors := c.inferTypeAnn(ctx, typeAnn.Extends)
		errors = slices.Concat(errors, extendsErrors)

		// Find all InferType nodes in the extends type and create type aliases for them
		inferTypesMap := c.findInferTypes(extendsType)

		// TODO: find all named capture groups in the extends type
		// and add them to the context scope as type aliases
		namedCaptureGroups := c.FindNamedGroups(extendsType)

		names := make([]string, 0, len(inferTypesMap)+len(namedCaptureGroups))
		for name := range inferTypesMap {
			names = append(names, name)
		}
		for name := range namedCaptureGroups {
			names = append(names, name)
		}

		// Create a new context with infer types in scope for inferring Cons and Alt
		condCtx := ctx
		if len(names) > 0 {
			// Create a new scope that includes the infer types as type aliases
			condScope := ctx.Scope.WithNewScope()

			// Add infer types as type aliases to the scope
			for _, name := range names {
				inferTypeRef := type_system.NewTypeRefType(nil, name, nil)
				inferTypeAlias := &type_system.TypeAlias{
					Type:       inferTypeRef,
					TypeParams: []*type_system.TypeParam{},
				}
				condScope.SetTypeAlias(name, inferTypeAlias)
			}

			condCtx = Context{
				Scope:      condScope,
				IsAsync:    ctx.IsAsync,
				IsPatMatch: ctx.IsPatMatch,
			}
		}

		thenType, thenErrors := c.inferTypeAnn(condCtx, typeAnn.Then)
		errors = slices.Concat(errors, thenErrors)

		elseType, elseErrors := c.inferTypeAnn(condCtx, typeAnn.Else)
		errors = slices.Concat(errors, elseErrors)

		t = type_system.NewCondType(provenance, checkType, extendsType, thenType, elseType)
	case *ast.InferTypeAnn:
		t = type_system.NewInferType(provenance, typeAnn.Name)
	case *ast.KeyOfTypeAnn:
		argType, argErrors := c.inferTypeAnn(ctx, typeAnn.Type)
		errors = slices.Concat(errors, argErrors)
		t = type_system.NewKeyOfType(provenance, argType)
	case *ast.MutableTypeAnn:
		targetType, targetErrors := c.inferTypeAnn(ctx, typeAnn.Target)
		errors = slices.Concat(errors, targetErrors)
		t = type_system.NewMutableType(provenance, targetType)
	case *ast.TemplateLitTypeAnn:
		types := make([]type_system.Type, len(typeAnn.TypeAnns))
		quasis := make([]*type_system.Quasi, len(typeAnn.Quasis))
		strOrNumType := type_system.NewUnionType(nil, type_system.NewStrPrimType(nil), type_system.NewNumPrimType(nil))
		for i, typeAnn := range typeAnn.TypeAnns {
			typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, typeAnn)
			// Each type in a template literal type must be a subtype of either
			// string or number.
			// However, if it's a TypeRefType (type parameter), we skip the check
			// because it will be validated when the type is actually used/expanded.
			// TODO: Also check if the value has a .toString() method.
			if _, isTypeRef := type_system.Prune(typeAnnType).(*type_system.TypeRefType); !isTypeRef {
				unifyErrors := c.Unify(ctx, typeAnnType, strOrNumType)
				errors = slices.Concat(errors, unifyErrors)
			}
			types[i] = typeAnnType
			errors = slices.Concat(errors, typeAnnErrors)
		}
		for i, quasi := range typeAnn.Quasis {
			quasis[i] = &type_system.Quasi{
				Value: quasi.Value,
			}
		}
		t = type_system.NewTemplateLitType(provenance, quasis, types)
	case *ast.IndexTypeAnn:
		objectType, objectErrors := c.inferTypeAnn(ctx, typeAnn.Target)
		errors = slices.Concat(errors, objectErrors)
		indexType, indexErrors := c.inferTypeAnn(ctx, typeAnn.Index)
		errors = slices.Concat(errors, indexErrors)
		t = type_system.NewIndexType(provenance, objectType, indexType)
	case *ast.TypeOfTypeAnn:
		// Convert ast.QualIdent to type_system.QualIdent
		var ident type_system.QualIdent = convertQualIdent(typeAnn.Value)
		t = type_system.NewTypeOfType(provenance, ident)
	default:
		panic(fmt.Sprintf("Unknown type annotation: %T", typeAnn))
	}

	t.SetProvenance(provenance)
	typeAnn.SetInferredType(t)

	return t, errors
}

func (c *Checker) inferFuncTypeAnn(
	ctx Context,
	funcTypeAnn *ast.FuncTypeAnn,
) (*type_system.FuncType, []Error) {
	errors := []Error{}
	params := make([]*type_system.FuncParam, len(funcTypeAnn.Params))
	for i, param := range funcTypeAnn.Params {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)
		errors = slices.Concat(errors, patErrors)

		// TODO: make type annoations required on parameters in function type
		// annotations
		var typeAnn type_system.Type
		if param.TypeAnn == nil {
			typeAnn = c.FreshVar(nil)
		} else {
			var typeAnnErrors []Error
			typeAnn, typeAnnErrors = c.inferTypeAnn(ctx, param.TypeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
		}

		c.Unify(ctx, patType, typeAnn)

		maps.Copy(ctx.Scope.Namespace.Values, patBindings)

		params[i] = &type_system.FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: param.Optional,
		}
	}
	returnType, returnErrors := c.inferTypeAnn(ctx, funcTypeAnn.Return)
	errors = slices.Concat(errors, returnErrors)

	funcType := type_system.FuncType{
		Params:     params,
		Return:     returnType,
		Throws:     type_system.NewNeverType(nil),
		TypeParams: []*type_system.TypeParam{},
	}

	return &funcType, errors
}

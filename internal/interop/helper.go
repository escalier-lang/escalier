package interop

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

func convertIdent(id *dts_parser.Ident) *ast.Ident {
	return ast.NewIdentifier(id.Name, id.Span())
}

func convertQualIdent(qi dts_parser.QualIdent) ast.QualIdent {
	switch q := qi.(type) {
	case *dts_parser.Ident:
		return convertIdent(q)
	case *dts_parser.Member:
		return &ast.Member{
			Left:  convertQualIdent(q.Left),
			Right: convertIdent(q.Right),
		}
	default:
		return nil
	}
}

func convertTypeParam(tp *dts_parser.TypeParam) *ast.TypeParam {
	var constraint ast.TypeAnn
	if tp.Constraint != nil {
		constraint = convertTypeAnn(tp.Constraint)
	}

	var defaultType ast.TypeAnn
	if tp.Default != nil {
		defaultType = convertTypeAnn(tp.Default)
	}

	typeParam := ast.NewTypeParam(tp.Name.Name, constraint, defaultType)
	return &typeParam
}

func convertParam(p *dts_parser.Param) *ast.Param {
	// Convert the parameter name to an IdentPat pattern
	pattern := ast.NewIdentPat(p.Name.Name, nil, nil, p.Span())

	var typeAnn ast.TypeAnn
	if p.Type != nil {
		typeAnn = convertTypeAnn(p.Type)
	}

	return &ast.Param{
		Pattern:  pattern,
		Optional: p.Optional,
		TypeAnn:  typeAnn,
	}
}

func convertPropertyKey(pk dts_parser.PropertyKey) ast.ObjKey {
	switch k := pk.(type) {
	case *dts_parser.Ident:
		return ast.NewIdent(k.Name, k.Span())
	case *dts_parser.StringLiteral:
		return ast.NewString(k.Value, k.Span())
	case *dts_parser.NumberLiteral:
		return ast.NewNumber(k.Value, k.Span())
	case *dts_parser.ComputedKey:
		// In dts_parser, ComputedKey.Expr is a TypeAnn
		// In ast, ComputedKey.Expr is an Expr
		// We need to handle this conversion somehow
		// For now, panic as this requires more complex handling
		panic("convertPropertyKey: ComputedKey not fully implemented")
	default:
		panic("convertPropertyKey: unknown property key type")
	}
}

func convertInterfaceMember(member dts_parser.InterfaceMember) ast.ObjTypeAnnElem {
	switch m := member.(type) {
	case *dts_parser.CallSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			typeParams[i] = convertTypeParam(tp)
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			params[i] = convertParam(p)
		}
		returnType := convertTypeAnn(m.ReturnType)
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		return &ast.CallableTypeAnn{Fn: fn}
	case *dts_parser.ConstructSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			typeParams[i] = convertTypeParam(tp)
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			params[i] = convertParam(p)
		}
		returnType := convertTypeAnn(m.ReturnType)
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		return &ast.ConstructorTypeAnn{Fn: fn}
	case *dts_parser.MethodSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			typeParams[i] = convertTypeParam(tp)
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			params[i] = convertParam(p)
		}
		returnType := convertTypeAnn(m.ReturnType)
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		return &ast.MethodTypeAnn{
			Name: convertPropertyKey(m.Name),
			Fn:   fn,
		}
	case *dts_parser.PropertySignature:
		typeAnn := convertTypeAnn(m.TypeAnn)
		return &ast.PropertyTypeAnn{
			Name:     convertPropertyKey(m.Name),
			Optional: m.Optional,
			Readonly: m.Readonly,
			Value:    typeAnn,
		}
	case *dts_parser.GetterSignature:
		// Getter has no parameters, returns the type
		returnType := convertTypeAnn(m.ReturnType)
		fn := ast.NewFuncTypeAnn(nil, []*ast.Param{}, returnType, nil, m.Span())
		return &ast.GetterTypeAnn{
			Name: convertPropertyKey(m.Name),
			Fn:   fn,
		}
	case *dts_parser.SetterSignature:
		// Setter has one parameter, returns undefined
		param := convertParam(m.Param)
		returnType := ast.NewLitTypeAnn(ast.NewUndefined(m.Span()), m.Span())
		fn := ast.NewFuncTypeAnn(nil, []*ast.Param{param}, returnType, nil, m.Span())
		return &ast.SetterTypeAnn{
			Name: convertPropertyKey(m.Name),
			Fn:   fn,
		}
	case *dts_parser.IndexSignature:
		// Index signatures don't have a direct equivalent in Escalier's ObjTypeAnnElem
		// We could potentially use a MappedTypeAnn or skip them for now
		// For now, we'll skip index signatures
		// TODO: determine how to properly represent index signatures
		return nil
	default:
		panic("convertInterfaceMember: unknown interface member type")
	}
}

func convertTypeAnn(ta dts_parser.TypeAnn) ast.TypeAnn {
	switch t := ta.(type) {
	case *dts_parser.PrimitiveType:
		span := t.Span()
		switch t.Kind {
		case dts_parser.PrimAny:
			return ast.NewAnyTypeAnn(span)
		case dts_parser.PrimUnknown:
			return ast.NewUnknownTypeAnn(span)
		case dts_parser.PrimVoid:
			// TODO: Add support for `void` type to Escalier's type system.
			// For now, map void to undefined as a temporary workaround.
			return ast.NewLitTypeAnn(ast.NewUndefined(span), span)
		case dts_parser.PrimNull:
			return ast.NewLitTypeAnn(ast.NewNull(span), span)
		case dts_parser.PrimUndefined:
			return ast.NewLitTypeAnn(ast.NewUndefined(span), span)
		case dts_parser.PrimNever:
			return ast.NewNeverTypeAnn(span)
		case dts_parser.PrimString:
			return ast.NewStringTypeAnn(span)
		case dts_parser.PrimNumber:
			return ast.NewNumberTypeAnn(span)
		case dts_parser.PrimBoolean:
			return ast.NewBooleanTypeAnn(span)
		case dts_parser.PrimBigInt:
			return ast.NewBigintTypeAnn(span)
		case dts_parser.PrimSymbol:
			return ast.NewSymbolTypeAnn(span)
		case dts_parser.PrimObject:
			return ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{}, span)
		default:
			panic("convertTypeAnn: unknown primitive type")
		}
	case *dts_parser.LiteralType:
		span := t.Span()
		switch lit := t.Literal.(type) {
		case *dts_parser.StringLiteral:
			return ast.NewLitTypeAnn(ast.NewString(lit.Value, lit.Span()), span)
		case *dts_parser.NumberLiteral:
			return ast.NewLitTypeAnn(ast.NewNumber(lit.Value, lit.Span()), span)
		case *dts_parser.BooleanLiteral:
			return ast.NewLitTypeAnn(ast.NewBoolean(lit.Value, lit.Span()), span)
		case *dts_parser.BigIntLiteral:
			// TODO: parse the string value into a big.Int
			panic("convertTypeAnn: BigIntLiteral not fully implemented")
		default:
			panic("convertTypeAnn: unknown literal type")
		}
	case *dts_parser.TypeReference:
		typeArgs := make([]ast.TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			typeArgs[i] = convertTypeAnn(arg)
		}
		return ast.NewRefTypeAnn(convertQualIdent(t.Name), typeArgs, t.Span())
	case *dts_parser.ArrayType:
		elemType := convertTypeAnn(t.ElementType)
		// Array types in TypeScript are represented as TypeRef to Array<T>
		arrayIdent := ast.NewIdentifier("Array", t.Span())
		return ast.NewRefTypeAnn(arrayIdent, []ast.TypeAnn{elemType}, t.Span())
	case *dts_parser.TupleType:
		elems := make([]ast.TypeAnn, len(t.Elements))
		for i, elem := range t.Elements {
			if elem.Rest {
				elems[i] = ast.NewRestSpreadTypeAnn(convertTypeAnn(elem.Type), elem.Span())
			} else {
				elems[i] = convertTypeAnn(elem.Type)
			}
			// TODO: handle optional elements and named elements
		}
		return ast.NewTupleTypeAnn(elems, t.Span())
	case *dts_parser.UnionType:
		types := make([]ast.TypeAnn, len(t.Types))
		for i, typ := range t.Types {
			types[i] = convertTypeAnn(typ)
		}
		return ast.NewUnionTypeAnn(types, t.Span())
	case *dts_parser.IntersectionType:
		types := make([]ast.TypeAnn, len(t.Types))
		for i, typ := range t.Types {
			types[i] = convertTypeAnn(typ)
		}
		return ast.NewIntersectionTypeAnn(types, t.Span())
	case *dts_parser.FunctionType:
		typeParams := make([]*ast.TypeParam, len(t.TypeParams))
		for i, tp := range t.TypeParams {
			typeParams[i] = convertTypeParam(tp)
		}
		params := make([]*ast.Param, len(t.Params))
		for i, p := range t.Params {
			params[i] = convertParam(p)
		}
		returnType := convertTypeAnn(t.ReturnType)
		return ast.NewFuncTypeAnn(typeParams, params, returnType, nil, t.Span())
	case *dts_parser.ConstructorType:
		// Constructor types don't have a direct equivalent in Escalier
		// Convert to a function type for now
		typeParams := make([]*ast.TypeParam, len(t.TypeParams))
		for i, tp := range t.TypeParams {
			typeParams[i] = convertTypeParam(tp)
		}
		params := make([]*ast.Param, len(t.Params))
		for i, p := range t.Params {
			params[i] = convertParam(p)
		}
		returnType := convertTypeAnn(t.ReturnType)
		return ast.NewFuncTypeAnn(typeParams, params, returnType, nil, t.Span())
	case *dts_parser.ObjectType:
		elems := make([]ast.ObjTypeAnnElem, 0, len(t.Members))
		for _, member := range t.Members {
			elem := convertInterfaceMember(member)
			if elem != nil { // Skip nil elements (e.g., index signatures)
				elems = append(elems, elem)
			}
		}
		return ast.NewObjectTypeAnn(elems, t.Span())
	case *dts_parser.ParenthesizedType:
		return convertTypeAnn(t.Type)
	case *dts_parser.IndexedAccessType:
		target := convertTypeAnn(t.ObjectType)
		index := convertTypeAnn(t.IndexType)
		return ast.NewIndexTypeAnn(target, index, t.Span())
	case *dts_parser.ConditionalType:
		check := convertTypeAnn(t.CheckType)
		extends := convertTypeAnn(t.ExtendsType)
		trueType := convertTypeAnn(t.TrueType)
		falseType := convertTypeAnn(t.FalseType)
		return ast.NewCondTypeAnn(check, extends, trueType, falseType, t.Span())
	case *dts_parser.InferType:
		return ast.NewInferTypeAnn(t.TypeParam.Name.Name, t.Span())
	case *dts_parser.MappedType:
		// Convert type parameter
		var constraint ast.TypeAnn
		if t.TypeParam.Constraint != nil {
			constraint = convertTypeAnn(t.TypeParam.Constraint)
		}
		indexParam := &ast.IndexParamTypeAnn{
			Name:       t.TypeParam.Name.Name,
			Constraint: constraint,
		}

		// Convert value type
		valueType := convertTypeAnn(t.ValueType)

		// Convert optional modifier
		var optional *ast.MappedModifier
		switch t.Optional {
		case dts_parser.OptionalAdd:
			m := ast.MMAdd
			optional = &m
		case dts_parser.OptionalRemove:
			m := ast.MMRemove
			optional = &m
		case dts_parser.OptionalNone:
			optional = nil
		}

		// Convert readonly modifier
		var readonly *ast.MappedModifier
		switch t.Readonly {
		case dts_parser.ReadonlyAdd:
			m := ast.MMAdd
			readonly = &m
		case dts_parser.ReadonlyRemove:
			m := ast.MMRemove
			readonly = &m
		case dts_parser.ReadonlyNone:
			readonly = nil
		}

		// Convert as clause (key remapping)
		var asClause ast.TypeAnn
		if t.AsClause != nil {
			asClause = convertTypeAnn(t.AsClause)
		}

		// MappedTypeAnn is an ObjTypeAnnElem, so wrap it in an ObjectTypeAnn
		mappedElem := &ast.MappedTypeAnn{
			TypeParam: indexParam,
			Name:      asClause,
			Value:     valueType,
			Optional:  optional,
			ReadOnly:  readonly,
			Check:     nil, // dts_parser doesn't have Check field
			Extends:   nil, // dts_parser doesn't have Extends field
		}
		return ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{mappedElem}, t.Span())
	case *dts_parser.TemplateLiteralType:
		quasis := []*ast.Quasi{}
		typeAnns := []ast.TypeAnn{}
		for _, part := range t.Parts {
			switch p := part.(type) {
			case *dts_parser.TemplateString:
				quasis = append(quasis, &ast.Quasi{Value: p.Value, Span: p.Span()})
			case *dts_parser.TemplateType:
				typeAnns = append(typeAnns, convertTypeAnn(p.Type))
			}
		}
		return ast.NewTemplateLitTypeAnn(quasis, typeAnns, t.Span())
	case *dts_parser.KeyOfType:
		typ := convertTypeAnn(t.Type)
		return ast.NewKeyOfTypeAnn(typ, t.Span())
	case *dts_parser.TypeOfType:
		return ast.NewTypeOfTypeAnn(convertQualIdent(t.Expr), t.Span())
	case *dts_parser.ImportType:
		typeArgs := make([]ast.TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			typeArgs[i] = convertTypeAnn(arg)
		}
		var qualifier ast.QualIdent
		if t.Name != nil {
			qualifier = convertQualIdent(t.Name)
		}
		return ast.NewImportType(t.Module, qualifier, typeArgs, t.Span())
	case *dts_parser.TypePredicate:
		// Type predicates don't have a direct equivalent in Escalier
		// Convert to the type being asserted
		// TODO: add support for type predicates to Escalier
		if t.Type != nil {
			return convertTypeAnn(t.Type)
		}
		return ast.NewBooleanTypeAnn(t.Span())
	case *dts_parser.ThisType:
		// TODO: determine the right way to handle `this` type
		panic("convertTypeAnn: ThisType not fully implemented")
	case *dts_parser.RestType:
		typ := convertTypeAnn(t.Type)
		return ast.NewRestSpreadTypeAnn(typ, t.Span())
	case *dts_parser.OptionalType:
		// Optional types in tuples - convert to union with undefined
		typ := convertTypeAnn(t.Type)
		undefinedType := ast.NewLitTypeAnn(ast.NewUndefined(t.Span()), t.Span())
		return ast.NewUnionTypeAnn([]ast.TypeAnn{typ, undefinedType}, t.Span())
	default:
		panic("convertTypeAnn: unknown type annotation")
	}
}

func convertModifiers(m dts_parser.Modifiers) (static, private, readonly bool) {
	panic("convertModifiers: not implemented")
}
